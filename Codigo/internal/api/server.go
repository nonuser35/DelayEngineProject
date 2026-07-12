package api

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"delayengine/internal/hotkey"
	"delayengine/internal/logging"
	"delayengine/internal/secret"
)

//go:embed static/*
var staticFiles embed.FS

type DelayController interface {
	Status(ctx context.Context) (Status, error)
	EnableDelay(ctx context.Context) error
	DisableDelay(ctx context.Context) error
	SmoothDisableDelay(ctx context.Context) error
	SetDelay(ctx context.Context, delay time.Duration) error
	ArmDelay(ctx context.Context, delay time.Duration, slatePath string, playFullSlate bool, shortSlate bool) error
	ArmDelayFromBuffer(ctx context.Context, delay time.Duration) error
	ForceRealtime(ctx context.Context) error
	ForceRealtimeReset(ctx context.Context) error
	ForceRealtimeResetPause(ctx context.Context, pause time.Duration) error
}

type Status struct {
	OK                bool         `json:"ok"`
	DelayEnabled      bool         `json:"delayEnabled"`
	Delay             string       `json:"delay"`
	DelaySeconds      float64      `json:"delaySeconds"`
	Buffer            BufferStatus `json:"buffer"`
	Input             StreamStatus `json:"input"`
	Output            StreamStatus `json:"output"`
	Note              string       `json:"note,omitempty"`
	TransitionStarted bool         `json:"transitionStarted,omitempty"`
	LiveActionActive  bool         `json:"liveActionActive,omitempty"`
	Message           string       `json:"message,omitempty"`
}

type BufferStatus struct {
	Packets        int    `json:"packets"`
	Duration       string `json:"duration"`
	Bytes          int    `json:"bytes"`
	DiskFreeBytes  uint64 `json:"diskFreeBytes,omitempty"`
	DiskTotalBytes uint64 `json:"diskTotalBytes,omitempty"`
}

type StreamStatus struct {
	Connected           bool    `json:"connected"`
	Audio               uint64  `json:"audioPackets"`
	Video               uint64  `json:"videoPackets"`
	Bytes               uint64  `json:"bytes"`
	VideoCodec          string  `json:"videoCodec,omitempty"`
	AudioCodec          string  `json:"audioCodec,omitempty"`
	Width               int     `json:"width,omitempty"`
	Height              int     `json:"height,omitempty"`
	FPS                 float64 `json:"fps,omitempty"`
	BitrateKbps         float64 `json:"bitrateKbps,omitempty"`
	KeyframeInterval    string  `json:"keyframeInterval,omitempty"`
	KeyframeIntervalSec float64 `json:"keyframeIntervalSeconds,omitempty"`
	KeyframeAgeMillis   float64 `json:"keyframeAgeMillis,omitempty"`
	OutputQueue         int     `json:"outputQueue,omitempty"`
	RealtimeState       string  `json:"realtimeState,omitempty"`
	RealtimeDrops       uint64  `json:"realtimeDrops,omitempty"`
}

type conversionState struct {
	OK         bool   `json:"ok"`
	State      string `json:"state"`
	Message    string `json:"message"`
	Output     string `json:"output,omitempty"`
	Active     string `json:"active,omitempty"`
	StartedAt  string `json:"startedAt,omitempty"`
	FinishedAt string `json:"finishedAt,omitempty"`
	Error      string `json:"error,omitempty"`
}

type encodedRelayState struct {
	OK         bool   `json:"ok"`
	Running    bool   `json:"running"`
	Message    string `json:"message"`
	PID        int    `json:"pid,omitempty"`
	Input      string `json:"input,omitempty"`
	Output     string `json:"output,omitempty"`
	Speed      string `json:"speed,omitempty"`
	Health     string `json:"health,omitempty"`
	StartedAt  string `json:"startedAt,omitempty"`
	FinishedAt string `json:"finishedAt,omitempty"`
	Error      string `json:"error,omitempty"`
}

type encodedAutoProfile struct {
	Width        int
	Height       int
	FPS          int
	VideoBitrate string
}

type settingsSaveResponse struct {
	appSettings
	OutputModeChanged  bool   `json:"outputModeChanged,omitempty"`
	OutputRestarted    bool   `json:"outputRestarted,omitempty"`
	OutputRestartError string `json:"outputRestartError,omitempty"`
}

const realtimeQueueHealthyLimit = 96

type delayOffRequest struct {
	Slate   string  `json:"slate"`
	Seconds float64 `json:"seconds"`
}

type conversionProfile struct {
	Width        int
	Height       int
	FPS          int
	VideoBitrate string
}

var (
	conversionMu      sync.Mutex
	currentConversion = conversionState{OK: true, State: "idle", Message: "Nenhuma conversao em andamento."}
	encodedRelayMu    sync.Mutex
	encodedRelayCmd   *exec.Cmd
	currentRelay      = encodedRelayState{OK: true, Running: false, Message: "Codificador parado."}
	ffmpegSpeedRegex  = regexp.MustCompile(`speed=\s*([0-9.]+)x`)
	encoderDetectOnce sync.Once
	detectedEncoder   string
)

const maxSettingsDelaySeconds = 60

type Server struct {
	addr             string
	controller       DelayController
	logger           *slog.Logger
	httpServer       *http.Server
	liveActionActive atomic.Bool
	encodingPaused   atomic.Bool
}

func NewServer(addr string, controller DelayController, logger *slog.Logger) *Server {
	if logger == nil {
		logger = slog.Default()
	}

	server := &Server{
		addr:       addr,
		controller: controller,
		logger:     logger,
	}

	mux := http.NewServeMux()
	staticFS, err := fs.Sub(staticFiles, "static")
	if err != nil {
		panic(err)
	}
	mux.Handle("GET /", webFileServer(staticFS))
	mux.HandleFunc("GET /remote", server.handleRemotePage)
	mux.HandleFunc("GET /status", server.handleStatus)
	mux.HandleFunc("GET /logs", server.handleLogs)
	mux.HandleFunc("GET /settings", server.handleSettingsGet)
	mux.HandleFunc("POST /settings", server.handleSettingsSave)
	mux.HandleFunc("POST /settings/default-delay", server.handleDefaultDelaySave)
	mux.HandleFunc("POST /tools/open-converter", server.handleOpenConverter)
	mux.HandleFunc("POST /tools/open-videos", server.handleOpenVideos)
	mux.HandleFunc("GET /tools/conversion-status", server.handleConversionStatus)
	mux.HandleFunc("POST /tools/convert-video", server.handleConvertVideo)
	mux.HandleFunc("GET /encoding/status", server.handleEncodingStatus)
	mux.HandleFunc("POST /encoding/start", server.handleEncodingStart)
	mux.HandleFunc("POST /encoding/restart", server.handleEncodingRestart)
	mux.HandleFunc("POST /encoding/stop", server.handleEncodingStop)
	mux.HandleFunc("GET /preview-hls/", server.handlePreviewHLS)
	mux.HandleFunc("GET /videos", server.handleVideos)
	mux.HandleFunc("GET /videos/preview", server.handleVideoPreview)
	mux.HandleFunc("POST /videos/activate", server.handleVideoActivate)
	mux.HandleFunc("POST /videos/delete", server.handleVideoDelete)
	mux.HandleFunc("POST /delay/on", server.handleDelayOn)
	mux.HandleFunc("POST /delay/off", server.handleDelayOff)
	mux.HandleFunc("POST /delay/off-smooth", server.handleDelayOffSmooth)
	mux.HandleFunc("POST /delay/off-reset", server.handleDelayOffReset)
	mux.HandleFunc("POST /delay/off-hard-reset", server.handleDelayOffHardReset)
	mux.HandleFunc("POST /delay/set", server.handleDelaySet)
	mux.HandleFunc("POST /delay/arm", server.handleDelayArm)
	mux.HandleFunc("POST /delay/arm-buffer", server.handleDelayArmBuffer)
	mux.HandleFunc("POST /live/sync", server.handleLiveSync)
	mux.HandleFunc("GET /control/status", server.handleControlStatus)
	mux.HandleFunc("POST /control/delay/arm", server.handleControlDelayArm)
	mux.HandleFunc("POST /control/delay/off", server.handleControlDelayOff)
	mux.HandleFunc("POST /control/delay/toggle", server.handleControlDelayToggle)

	server.httpServer = &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	return server
}

func (s *Server) Start() error {
	s.logger.Info("HTTP API starting", "addr", s.addr, "status", "ok")
	go s.autoStartEncodedRelay()
	return s.httpServer.ListenAndServe()
}

func (s *Server) Shutdown(ctx context.Context) error {
	return s.httpServer.Shutdown(ctx)
}

func (s *Server) startAsyncAction(name string, action func(context.Context) error) bool {
	if !s.liveActionActive.CompareAndSwap(false, true) {
		s.logger.Warn(name, "status", "busy", "reason", "another live action is already running")
		return false
	}
	s.logger.Info(name, "status", "starting")
	go func() {
		defer s.liveActionActive.Store(false)
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		if err := action(ctx); err != nil {
			s.logger.Error(name, "error", err, "status", "error")
			return
		}
		s.logger.Info(name, "status", "ok")
	}()
	return true
}

func withTransition(status Status, message string) Status {
	status.OK = true
	status.TransitionStarted = true
	status.Message = message
	return status
}

func (s *Server) autoStartEncodedRelay() {
	const (
		// Attach to the local live path promptly. Waiting several seconds means
		// a new RTMP reader receives an already-open GOP and relays it in a
		// startup burst, which Twitch can report as ignored frames.
		retryInterval = 100 * time.Millisecond
	)
	ticker := time.NewTicker(retryInterval)
	defer ticker.Stop()

	for {
		settings, err := loadSettings()
		if err != nil {
			s.logger.Warn("could not load settings for Twitch polished auto-start", "error", err, "status", "waiting")
			<-ticker.C
			continue
		}
		if (settings.OutputMode != "encoded" && settings.OutputMode != "copy") || s.encodingPaused.Load() {
			<-ticker.C
			continue
		}

		status, err := s.controller.Status(context.Background())
		// A fresh keyframe gives a new Twitch reader a complete and current GOP,
		// avoiding both an old cached segment and a wait for a second keyframe.
		keyframeFresh := status.Input.KeyframeAgeMillis >= 0 && status.Input.KeyframeAgeMillis <= 350
		if err != nil || !status.Input.Connected || !status.Output.Connected || status.Output.Video == 0 || !keyframeFresh {
			s.logger.Debug("Twitch polished encoder waiting for local live output", "status", "waiting")
			<-ticker.C
			continue
		}

		encodedRelayMu.Lock()
		running := encodedRelayCmd != nil && encodedRelayCmd.Process != nil
		encodedRelayMu.Unlock()
		if running {
			<-ticker.C
			continue
		}

		if _, err := s.startEncodedRelay(settings, false); err != nil {
			s.logger.Warn("Twitch polished encoder manual profile auto-start waiting", "error", err, "retry_in", retryInterval, "status", "waiting")
		}
		<-ticker.C
	}
}

func webFileServer(fallback fs.FS) http.Handler {
	root := runtimeRoot()
	if _, err := os.Stat(filepath.Join(root, "web", "index.html")); err == nil {
		return http.FileServer(http.Dir(filepath.Join(root, "web")))
	}
	return http.FileServer(http.FS(fallback))
}

func (s *Server) handleRemotePage(w http.ResponseWriter, r *http.Request) {
	root := runtimeRoot()
	path := filepath.Join(root, "web", "remote.html")
	if _, err := os.Stat(path); err == nil {
		http.ServeFile(w, r, path)
		return
	}
	data, err := staticFiles.ReadFile("static/remote.html")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

func runtimeRoot() string {
	if envRoot := os.Getenv("DELAYENGINE_ROOT"); envRoot != "" {
		return envRoot
	}
	if cwd, err := os.Getwd(); err == nil {
		if hasRuntimeDirs(cwd) {
			return cwd
		}
		if hasRuntimeDirs(filepath.Dir(cwd)) {
			return filepath.Dir(cwd)
		}
	}
	if exe, err := os.Executable(); err == nil {
		exeDir := filepath.Dir(exe)
		if hasRuntimeDirs(exeDir) {
			return exeDir
		}
		if hasRuntimeDirs(filepath.Dir(exeDir)) {
			return filepath.Dir(exeDir)
		}
		return exeDir
	}
	return "."
}

func hasRuntimeDirs(path string) bool {
	if path == "" || path == "." {
		return false
	}
	if _, err := os.Stat(filepath.Join(path, "videos")); err == nil {
		return true
	}
	if _, err := os.Stat(filepath.Join(path, "web")); err == nil {
		return true
	}
	return false
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	status, err := s.controller.Status(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	enrichDiskStatus(&status)
	status.LiveActionActive = s.liveActionActive.Load()
	writeJSON(w, http.StatusOK, status)
}

func (s *Server) handleControlStatus(w http.ResponseWriter, r *http.Request) {
	status, err := s.controller.Status(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	enrichDiskStatus(&status)
	settings, err := loadSettings()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	videos, err := listVideos()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	delaySeconds := controlDelaySeconds(settings)
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":                      true,
		"status":                  status,
		"delaySeconds":            delaySeconds,
		"configuredDelay":         fmt.Sprintf("%.0fs", settings.DefaultDelaySeconds),
		"playFullLoading":         settings.PlayFullLoading,
		"activeLoadingPath":       videos.Active,
		"activeLoadingExists":     videos.ActiveExists,
		"activeLoadingConfigured": videos.ActiveExists && strings.TrimSpace(videos.ActiveName) != "",
		"delayArmMode":            settings.DelayArmMode,
		"busy":                    s.liveActionActive.Load(),
	})
}

func enrichDiskStatus(status *Status) {
	free, total, ok := diskSpace(filepath.Join(runtimeRoot(), "runtime", "buffer"))
	if !ok {
		free, total, ok = diskSpace(runtimeRoot())
	}
	if !ok {
		return
	}
	status.Buffer.DiskFreeBytes = free
	status.Buffer.DiskTotalBytes = total
}

func (s *Server) handleControlDelayArm(w http.ResponseWriter, r *http.Request) {
	status, err := s.controller.Status(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	settings, err := loadSettings()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	videos, err := listVideos()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	delay := time.Duration(controlDelaySeconds(settings) * float64(time.Second))
	if settings.DelayArmMode == "buffer" {
		if err := validateDelayBufferPreflight(status, delay); err != nil {
			writeError(w, http.StatusConflict, err)
			return
		}
		if !s.startAsyncAction("delay armed from remote control buffer mode", func(ctx context.Context) error {
			return s.controller.ArmDelayFromBuffer(ctx, delay)
		}) {
			writeError(w, http.StatusConflict, errors.New("uma transicao da live ja esta em andamento; aguarde terminar"))
			return
		}
		writeJSON(w, http.StatusAccepted, map[string]any{
			"ok":      true,
			"message": "delay iniciado pelo buffer",
			"delay":   delay.String(),
			"mode":    settings.DelayArmMode,
		})
		return
	}
	if !videos.ActiveExists || strings.TrimSpace(videos.Active) == "" {
		writeError(w, http.StatusBadRequest, errors.New("nenhum video ou imagem de loading ativo foi encontrado"))
		return
	}
	slatePath := resolveRuntimePath(videos.Active)
	if err := ensureFLVHasAudio(slatePath); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if err := validateDelayArmPreflight(status, slatePath, delay); err != nil {
		writeError(w, http.StatusConflict, err)
		return
	}
	if err := validateLoadingBitrateForSettings(slatePath, settings); err != nil {
		writeError(w, http.StatusConflict, err)
		return
	}
	shortSlate := settings.DelayArmMode == "short-loading"
	if !s.startAsyncAction("delay armed from remote control", func(ctx context.Context) error {
		return s.controller.ArmDelay(ctx, delay, slatePath, !shortSlate && settings.PlayFullLoading, shortSlate)
	}) {
		writeError(w, http.StatusConflict, errors.New("uma transicao da live ja esta em andamento; aguarde terminar"))
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{
		"ok":      true,
		"message": map[bool]string{true: "delay iniciado com loading curto", false: "delay iniciado"}[shortSlate],
		"delay":   delay.String(),
		"slate":   slatePath,
		"full":    !shortSlate && settings.PlayFullLoading,
		"mode":    map[bool]string{true: "short-loading", false: "loading"}[shortSlate],
	})
}

func (s *Server) handleControlDelayOff(w http.ResponseWriter, r *http.Request) {
	if !s.startAsyncAction("delay disabled from remote control", func(ctx context.Context) error {
		return s.controller.DisableDelay(ctx)
	}) {
		writeError(w, http.StatusConflict, errors.New("uma transicao da live ja esta em andamento; aguarde terminar"))
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{
		"ok":      true,
		"message": "retorno ao vivo iniciado",
	})
}

func (s *Server) handleControlDelayToggle(w http.ResponseWriter, r *http.Request) {
	status, err := s.controller.Status(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if status.DelayEnabled || status.DelaySeconds > 0 {
		s.handleControlDelayOff(w, r)
		return
	}
	s.handleControlDelayArm(w, r)
}

func (s *Server) handleLogs(w http.ResponseWriter, r *http.Request) {
	source := r.URL.Query().Get("source")
	if source == "" {
		source = "delayengine"
	}
	lines := 220
	if value := r.URL.Query().Get("lines"); value != "" {
		parsed, err := strconv.Atoi(value)
		if err == nil && parsed > 0 && parsed <= 1000 {
			lines = parsed
		}
	}

	logs, err := readLogSource(source, lines)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":     true,
		"source": source,
		"logs":   logs,
	})
}

func (s *Server) handleSettingsGet(w http.ResponseWriter, r *http.Request) {
	settings, err := loadSettings()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, settings)
}

func (s *Server) handleSettingsSave(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()
	previous, err := loadSettings()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	var settings appSettings
	if err := json.NewDecoder(r.Body).Decode(&settings); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid JSON body: %w", err))
		return
	}
	armHotkey := strings.TrimSpace(settings.HotkeyArm)
	if armHotkey == "" {
		armHotkey = hotkey.DefaultArm
	}
	liveHotkey := strings.TrimSpace(settings.HotkeyLive)
	if liveHotkey == "" {
		liveHotkey = hotkey.DefaultLive
	}
	hotkeys, err := hotkey.ValidatePair(armHotkey, liveHotkey)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	settings.HotkeyArm = hotkeys.Arm.Canonical
	settings.HotkeyLive = hotkeys.Live.Canonical
	settings = normalizeSettings(settings)
	if err := saveSettings(settings); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	modeChanged := previous.OutputMode != settings.OutputMode
	restarted := false
	restartError := ""
	if modeChanged {
		wasRunning, restartErr := s.restartRunningEncodedRelay(settings)
		restarted = wasRunning && restartErr == nil
		if restartErr != nil {
			restartError = restartErr.Error()
			s.logger.Warn("Twitch output mode saved; relay restart will retry automatically", "from", previous.OutputMode, "to", settings.OutputMode, "error", restartErr, "status", "waiting")
		} else if wasRunning {
			s.logger.Info("Twitch output mode changed and relay restarted", "from", previous.OutputMode, "to", settings.OutputMode, "status", "ok")
		}
	}
	s.logger.Info("settings saved from API", "output_mode", settings.OutputMode, "delay_arm_mode", settings.DelayArmMode, "status", "ok")
	writeJSON(w, http.StatusOK, settingsSaveResponse{
		appSettings:        settings,
		OutputModeChanged:  modeChanged,
		OutputRestarted:    restarted,
		OutputRestartError: restartError,
	})
}

func (s *Server) restartRunningEncodedRelay(settings appSettings) (bool, error) {
	encodedRelayMu.Lock()
	cmd := encodedRelayCmd
	if cmd == nil || cmd.Process == nil {
		encodedRelayMu.Unlock()
		return false, nil
	}
	encodedRelayCmd = nil
	encodedRelayMu.Unlock()

	_ = cmd.Process.Kill()
	time.Sleep(500 * time.Millisecond)
	s.encodingPaused.Store(false)
	_, err := s.startEncodedRelay(settings, false)
	return true, err
}

func (s *Server) handleDefaultDelaySave(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()
	var request struct {
		Seconds float64 `json:"seconds"`
	}
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid JSON body: %w", err))
		return
	}
	settings, err := loadSettings()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	settings.DefaultDelaySeconds = request.Seconds
	settings = normalizeSettings(settings)
	if err := saveSettings(settings); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":                  true,
		"defaultDelaySeconds": settings.DefaultDelaySeconds,
	})
}

func (s *Server) handleVideos(w http.ResponseWriter, r *http.Request) {
	videos, err := listVideos()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, videos)
}

func (s *Server) handleVideoPreview(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.URL.Query().Get("name"))
	if filepath.Base(name) != name || filepath.Ext(name) != ".flv" {
		writeError(w, http.StatusBadRequest, errors.New("video de preview invalido"))
		return
	}

	root := runtimeRoot()
	source := filepath.Join(root, "videos", "ready", name)
	if !fileExists(source) {
		writeError(w, http.StatusNotFound, errors.New("video de loading nao encontrado"))
		return
	}

	previewPath := previewPathForReadyVideo(source)
	if !fileExists(previewPath) {
		ffmpegPath, err := findLocalTool("ffmpeg.exe")
		if err != nil {
			writeError(w, http.StatusInternalServerError, fmt.Errorf("FFmpeg nao encontrado para gerar preview: %w", err))
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
		defer cancel()
		if err := createVideoPreview(ctx, ffmpegPath, source, previewPath); err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
	}

	w.Header().Set("Content-Type", "video/mp4")
	http.ServeFile(w, r, previewPath)
}

func (s *Server) handleVideoActivate(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()
	var req videoActivateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid JSON body: %w", err))
		return
	}
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, errors.New(`missing name; use {"name":"video1x30s.flv"}`))
		return
	}

	active, err := activateVideo(req.Name)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	s.logger.Info("loading video activated from API", "path", active, "status", "ok")
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":         true,
		"active":     active,
		"activeName": req.Name,
	})
}

func (s *Server) handleVideoDelete(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()
	var req videoActivateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid JSON body: %w", err))
		return
	}
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, errors.New(`missing name; use {"name":"video1x30s.flv"}`))
		return
	}

	if err := deleteReadyVideo(req.Name); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	s.logger.Info("ready video deleted from API", "name", req.Name, "status", "ok")
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":   true,
		"name": req.Name,
	})
}

func (s *Server) handleOpenConverter(w http.ResponseWriter, r *http.Request) {
	if err := openConverter(); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	s.logger.Info("video converter opened from API", "status", "ok")
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleOpenVideos(w http.ResponseWriter, r *http.Request) {
	path := filepath.Join(runtimeRoot(), "videos")
	if err := os.MkdirAll(path, 0755); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if err := openPath(path); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleConversionStatus(w http.ResponseWriter, r *http.Request) {
	conversionMu.Lock()
	status := currentConversion
	conversionMu.Unlock()
	writeJSON(w, http.StatusOK, status)
}

func (s *Server) handleEncodingStatus(w http.ResponseWriter, r *http.Request) {
	encodedRelayMu.Lock()
	status := currentRelay
	if encodedRelayCmd != nil && encodedRelayCmd.Process != nil {
		status.Running = true
		status.PID = encodedRelayCmd.Process.Pid
	}
	encodedRelayMu.Unlock()
	writeJSON(w, http.StatusOK, status)
}

func (s *Server) handleEncodingStart(w http.ResponseWriter, r *http.Request) {
	settings, err := loadSettings()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if settings.OutputMode != "encoded" && settings.OutputMode != "copy" {
		settings.OutputMode = "copy"
		if err := saveSettings(settings); err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		settings = normalizeSettings(settings)
	}
	s.encodingPaused.Store(false)
	status, err := s.startEncodedRelay(settings, false)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusAccepted, status)
}

func (s *Server) handleEncodingRestart(w http.ResponseWriter, r *http.Request) {
	settings, err := loadSettings()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if settings.OutputMode != "encoded" && settings.OutputMode != "copy" {
		settings.OutputMode = "copy"
		if err := saveSettings(settings); err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		settings = normalizeSettings(settings)
	}
	s.encodingPaused.Store(false)
	encodedRelayMu.Lock()
	cmd := encodedRelayCmd
	if cmd != nil && cmd.Process != nil {
		encodedRelayCmd = nil
	}
	encodedRelayMu.Unlock()
	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
		time.Sleep(500 * time.Millisecond)
	}
	status, err := s.startEncodedRelay(settings, false)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	s.logger.Info("encoded Twitch relay restarted from API", "status", "ok")
	writeJSON(w, http.StatusAccepted, status)
}

func (s *Server) startEncodedRelay(settings appSettings, detectProfile bool) (encodedRelayState, error) {
	key, err := secret.Read(runtimeRoot())
	if err != nil || strings.TrimSpace(key) == "" {
		return encodedRelayState{}, errors.New("salve a stream key da Twitch antes de iniciar o codificador")
	}
	ffmpegPath, err := findLocalTool("ffmpeg.exe")
	if err != nil {
		return encodedRelayState{}, err
	}
	encodedRelayMu.Lock()
	if encodedRelayCmd != nil && encodedRelayCmd.Process != nil {
		status := currentRelay
		status.Running = true
		status.PID = encodedRelayCmd.Process.Pid
		encodedRelayMu.Unlock()
		return status, nil
	}

	settings, skipVideoFilter := s.settingsForEncodedRelay(settings, detectProfile)
	root := runtimeRoot()
	if err := os.MkdirAll(filepath.Join(root, "logs"), 0755); err != nil {
		encodedRelayMu.Unlock()
		return encodedRelayState{}, err
	}
	logPath := filepath.Join(root, "logs", "ffmpeg-encoded-twitch.log")
	_ = logging.Rotate(logPath, 8*1024*1024, 5)
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		encodedRelayMu.Unlock()
		return encodedRelayState{}, fmt.Errorf("abrir log do codificador: %w", err)
	}

	outputURL := strings.TrimRight(settings.TwitchServer, "/") + "/" + key
	args := encodedRelayArgs(settings, outputURL, ffmpegPath, skipVideoFilter)
	cmd := exec.Command(ffmpegPath, args...)
	applyHiddenWindow(cmd)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	if err := cmd.Start(); err != nil {
		_ = logFile.Close()
		encodedRelayMu.Unlock()
		return encodedRelayState{}, fmt.Errorf("iniciar FFmpeg: %w", err)
	}

	startedAt := time.Now().Format(time.RFC3339)
	encodedRelayCmd = cmd
	currentRelay = encodedRelayState{
		OK:        true,
		Running:   true,
		Message:   "Codificador enviando para Twitch.",
		PID:       cmd.Process.Pid,
		Input:     settings.EncodedLocalOutputURL,
		Output:    configRedactedTwitch(settings.TwitchServer),
		Health:    "warming-up",
		StartedAt: startedAt,
	}
	s.logger.Info(
		"encoded Twitch relay started",
		"input", settings.EncodedLocalOutputURL,
		"pid", cmd.Process.Pid,
		"width", settings.EncodedWidth,
		"height", settings.EncodedHeight,
		"fps", settings.EncodedFPS,
		"bitrate", normalizeBitrate(settings.EncodedVideoBitrate),
		"video_filter", !skipVideoFilter,
		"status", "ok",
	)

	go s.monitorEncodedRelay(cmd, logPath, time.Now())

	go func() {
		err := cmd.Wait()
		_ = logFile.Close()
		encodedRelayMu.Lock()
		defer encodedRelayMu.Unlock()
		if encodedRelayCmd != cmd {
			return
		}
		encodedRelayCmd = nil
		currentRelay.Running = false
		currentRelay.PID = 0
		currentRelay.FinishedAt = time.Now().Format(time.RFC3339)
		if err != nil {
			currentRelay.OK = false
			currentRelay.Message = "Codificador parou com erro."
			currentRelay.Error = err.Error()
			s.logger.Error("encoded Twitch relay stopped with error", "error", err, "status", "error")
			return
		}
		currentRelay.OK = true
		currentRelay.Message = "Codificador parado."
		currentRelay.Error = ""
		s.logger.Info("encoded Twitch relay stopped", "status", "ok")
	}()

	status := currentRelay
	encodedRelayMu.Unlock()
	return status, nil
}

func (s *Server) settingsForEncodedRelay(settings appSettings, detectProfile bool) (appSettings, bool) {
	_ = detectProfile
	settings = normalizeSettings(settings)
	return settings, true
}

func (s *Server) monitorEncodedRelay(cmd *exec.Cmd, logPath string, started time.Time) {
	const (
		warmupDuration = 90 * time.Second
		checkInterval  = 5 * time.Second
		minSpeed       = 0.99
		// FFmpeg speed is sampled from progress output and naturally fluctuates
		// around realtime. Do not turn a stable 0.998x/1.000x stream into a
		// permanent latency estimate; sustained slower delivery remains tracked.
		speedNoiseFloor = 0.995
		neutralRecovery = 0.03
		slowDuration    = 45 * time.Second
		logInterval     = 30 * time.Second
		correctionCool  = 2 * time.Minute
	)

	ticker := time.NewTicker(checkInterval)
	defer ticker.Stop()

	var slowSince time.Time
	var slowWarningLogged bool
	var lastSample time.Time
	var lastLatencyLog time.Time
	var lastCorrection time.Time
	var latencyWarningActive bool
	var estimatedDelaySeconds float64
	for {
		<-ticker.C

		encodedRelayMu.Lock()
		active := encodedRelayCmd == cmd && cmd.Process != nil
		encodedRelayMu.Unlock()
		if !active {
			return
		}

		speed, ok := latestFFmpegSpeed(logPath)
		if !ok {
			continue
		}

		now := time.Now()
		if lastSample.IsZero() {
			lastSample = now
		}
		elapsedSeconds := now.Sub(lastSample).Seconds()
		lastSample = now
		if elapsedSeconds > 0 && time.Since(started) >= warmupDuration {
			if speed < speedNoiseFloor {
				estimatedDelaySeconds += elapsedSeconds * (1 - speed)
			} else {
				// A speed within the normal sampling band confirms that the relay
				// is keeping pace. Let a previous transient estimate clear.
				estimatedDelaySeconds -= elapsedSeconds * neutralRecovery
			}
			if estimatedDelaySeconds < 0 {
				estimatedDelaySeconds = 0
			}
		}

		sustainedSlow := speed < minSpeed && !slowSince.IsZero() && time.Since(slowSince) >= slowDuration
		health := "ok"
		if time.Since(started) < warmupDuration {
			health = "warming-up"
		} else if sustainedSlow {
			health = "slow"
		} else if estimatedDelaySeconds >= 1 {
			health = "recovering"
		}

		encodedRelayMu.Lock()
		if encodedRelayCmd == cmd {
			currentRelay.Speed = fmt.Sprintf("%.3fx", speed)
			currentRelay.Health = health
			if sustainedSlow {
				currentRelay.Message = fmt.Sprintf("Codificador abaixo do tempo real; atraso estimado %.1fs.", estimatedDelaySeconds)
			} else if estimatedDelaySeconds >= 1 {
				currentRelay.Message = fmt.Sprintf("Codificador recuperando; atraso estimado %.1fs.", estimatedDelaySeconds)
			} else {
				currentRelay.Message = "Codificador enviando para Twitch."
			}
		}
		encodedRelayMu.Unlock()

		if time.Since(started) < warmupDuration {
			continue
		}
		if estimatedDelaySeconds >= 1 && (lastLatencyLog.IsZero() || time.Since(lastLatencyLog) >= logInterval) {
			level := "low"
			if estimatedDelaySeconds >= 5 {
				level = "high"
			} else if estimatedDelaySeconds >= 2 {
				level = "medium"
			}
			s.logger.Warn("encoded Twitch relay latency estimate", "speed", fmt.Sprintf("%.3fx", speed), "estimated_delay", fmt.Sprintf("%.1fs", estimatedDelaySeconds), "level", level, "status", "warning")
			lastLatencyLog = time.Now()
			latencyWarningActive = true
		}
		if latencyWarningActive && estimatedDelaySeconds < 0.2 {
			s.logger.Info("encoded Twitch relay latency recovered", "speed", fmt.Sprintf("%.3fx", speed), "estimated_delay", fmt.Sprintf("%.1fs", estimatedDelaySeconds), "status", "ok")
			latencyWarningActive = false
		}
		if s.shouldAutoCorrectLatency(estimatedDelaySeconds, lastCorrection, correctionCool) {
			if s.requestAutomaticLatencyCorrection(estimatedDelaySeconds, speed) {
				lastCorrection = time.Now()
				estimatedDelaySeconds = 0
				latencyWarningActive = false
			}
		}
		if speed >= minSpeed {
			slowSince = time.Time{}
			slowWarningLogged = false
			continue
		}
		if slowSince.IsZero() {
			slowSince = time.Now()
			continue
		}
		if time.Since(slowSince) < slowDuration {
			continue
		}

		if !slowWarningLogged {
			s.logger.Warn("encoded Twitch relay is below realtime; keeping connection alive", "speed", fmt.Sprintf("%.3fx", speed), "status", "warning")
			slowWarningLogged = true
		}
		encodedRelayMu.Lock()
		if encodedRelayCmd == cmd {
			currentRelay.OK = true
			currentRelay.Health = "slow"
			currentRelay.Message = fmt.Sprintf("Codificador abaixo do tempo real; conexão mantida. Atraso estimado %.1fs.", estimatedDelaySeconds)
		}
		encodedRelayMu.Unlock()
	}
}

func (s *Server) shouldAutoCorrectLatency(estimatedDelaySeconds float64, lastCorrection time.Time, cooldown time.Duration) bool {
	settings, err := loadSettings()
	if err != nil {
		s.logger.Warn("could not load settings for automatic latency correction", "error", err, "status", "warning")
		return false
	}
	threshold := 0.0
	if settings.AutoLatencyCorrection {
		threshold = settings.AutoLatencySeconds
		if threshold <= 0 {
			threshold = 2.5
		}
	}
	if settings.RealtimePriority {
		realtimeThreshold := settings.RealtimePrioritySecs
		if realtimeThreshold <= 0 {
			realtimeThreshold = 8
		}
		if threshold <= 0 || realtimeThreshold < threshold {
			threshold = realtimeThreshold
		}
	}
	if threshold <= 0 {
		return false
	}
	if estimatedDelaySeconds < threshold {
		return false
	}
	if !lastCorrection.IsZero() && time.Since(lastCorrection) < cooldown {
		return false
	}
	return true
}

func (s *Server) requestAutomaticLatencyCorrection(estimatedDelaySeconds float64, speed float64) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	status, err := s.controller.Status(ctx)
	if err != nil {
		s.logger.Warn("automatic latency correction skipped; status unavailable", "error", err, "estimated_delay", fmt.Sprintf("%.1fs", estimatedDelaySeconds), "status", "warning")
		return false
	}
	if status.DelayEnabled {
		s.logger.Info("automatic latency correction skipped; manual delay is active", "estimated_delay", fmt.Sprintf("%.1fs", estimatedDelaySeconds), "delay", status.Delay, "status", "ok")
		return false
	}
	if !status.Output.Connected {
		s.logger.Info("automatic latency correction skipped; output is not connected", "estimated_delay", fmt.Sprintf("%.1fs", estimatedDelaySeconds), "status", "waiting")
		return false
	}
	if status.DelaySeconds <= 0 && status.Input.OutputQueue <= realtimeQueueHealthyLimit {
		s.logger.Info("automatic latency correction skipped; local output queue is already realtime", "estimated_delay", fmt.Sprintf("%.1fs", estimatedDelaySeconds), "output_queue", status.Input.OutputQueue, "status", "ok")
		return false
	}
	started := s.startAsyncAction("automatic latency correction requested", func(ctx context.Context) error {
		return s.controller.ForceRealtime(ctx)
	})
	if !started {
		s.logger.Warn("automatic latency correction skipped; live action already running", "estimated_delay", fmt.Sprintf("%.1fs", estimatedDelaySeconds), "status", "busy")
		return false
	}
	s.logger.Warn("automatic latency correction started", "speed", fmt.Sprintf("%.3fx", speed), "estimated_delay", fmt.Sprintf("%.1fs", estimatedDelaySeconds), "status", "correction")
	return true
}

func latestFFmpegSpeed(logPath string) (float64, bool) {
	data, err := os.ReadFile(logPath)
	if err != nil || len(data) == 0 {
		return 0, false
	}
	const maxBytes = 96 * 1024
	if len(data) > maxBytes {
		data = data[len(data)-maxBytes:]
	}
	matches := ffmpegSpeedRegex.FindAllSubmatch(data, -1)
	if len(matches) == 0 {
		return 0, false
	}
	last := matches[len(matches)-1]
	if len(last) < 2 {
		return 0, false
	}
	speed, err := strconv.ParseFloat(string(last[1]), 64)
	if err != nil || speed <= 0 {
		return 0, false
	}
	return speed, true
}

func (s *Server) handleEncodingStop(w http.ResponseWriter, r *http.Request) {
	s.encodingPaused.Store(true)
	encodedRelayMu.Lock()
	cmd := encodedRelayCmd
	if cmd == nil || cmd.Process == nil {
		currentRelay.Running = false
		currentRelay.Message = "Codificador ja estava parado."
		status := currentRelay
		encodedRelayMu.Unlock()
		writeJSON(w, http.StatusOK, status)
		return
	}
	encodedRelayCmd = nil
	encodedRelayMu.Unlock()

	_ = cmd.Process.Kill()
	encodedRelayMu.Lock()
	currentRelay.Running = false
	currentRelay.PID = 0
	currentRelay.Message = "Codificador parado pelo usuario."
	currentRelay.FinishedAt = time.Now().Format(time.RFC3339)
	status := currentRelay
	encodedRelayMu.Unlock()
	s.logger.Info("encoded Twitch relay stopped from API", "status", "ok")
	writeJSON(w, http.StatusOK, status)
}

func (s *Server) handlePreviewHLS(w http.ResponseWriter, r *http.Request) {
	targetPath := strings.TrimPrefix(r.URL.Path, "/preview-hls/")
	targetPath = strings.TrimLeft(targetPath, "/")
	if targetPath == "" || strings.Contains(targetPath, "..") {
		writeError(w, http.StatusBadRequest, errors.New("preview invalido"))
		return
	}

	targetURL := "http://127.0.0.1:8888/" + targetPath
	if r.URL.RawQuery != "" {
		targetURL += "?" + r.URL.RawQuery
	}
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, targetURL, nil)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		writeError(w, http.StatusBadGateway, fmt.Errorf("abrir preview local: %w", err))
		return
	}
	defer resp.Body.Close()

	for _, key := range []string{"Content-Type", "Cache-Control", "Accept-Ranges"} {
		if value := resp.Header.Get(key); value != "" {
			w.Header().Set(key, value)
		}
	}
	if w.Header().Get("Content-Type") == "" {
		switch strings.ToLower(filepath.Ext(targetPath)) {
		case ".m3u8":
			w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
		case ".ts":
			w.Header().Set("Content-Type", "video/mp2t")
		case ".m4s":
			w.Header().Set("Content-Type", "video/iso.segment")
		case ".mp4":
			w.Header().Set("Content-Type", "video/mp4")
		}
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

func (s *Server) handleConvertVideo(w http.ResponseWriter, r *http.Request) {
	conversionMu.Lock()
	if currentConversion.State == "running" {
		conversionMu.Unlock()
		writeError(w, http.StatusConflict, errors.New("ja existe uma conversao em andamento"))
		return
	}
	conversionMu.Unlock()

	r.Body = http.MaxBytesReader(w, r.Body, 3<<30)
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("ler video enviado: %w", err))
		return
	}

	file, header, err := r.FormFile("video")
	if err != nil {
		writeError(w, http.StatusBadRequest, errors.New("escolha um arquivo de video"))
		return
	}
	defer file.Close()

	root := runtimeRoot()
	uploadDir := filepath.Join(root, "tmp", "uploads")
	if err := os.MkdirAll(uploadDir, 0755); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	uploadPath := filepath.Join(uploadDir, fmt.Sprintf("%d_%s", time.Now().UnixNano(), safeUploadName(header.Filename)))
	destination, err := os.Create(uploadPath)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Errorf("criar arquivo temporario: %w", err))
		return
	}
	if _, err := io.Copy(destination, file); err != nil {
		_ = destination.Close()
		writeError(w, http.StatusInternalServerError, fmt.Errorf("salvar video temporario: %w", err))
		return
	}
	if err := destination.Close(); err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Errorf("fechar video temporario: %w", err))
		return
	}

	request, err := parseConversionRequest(r)
	if err != nil {
		_ = os.Remove(uploadPath)
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if isStillImagePath(header.Filename) {
		request.mediaKind = "image"
	} else {
		request.mediaKind = "video"
	}
	outputPath, err := nextReadyVideoPath(filepath.Join(root, "videos", "ready"), request)
	if err != nil {
		_ = os.Remove(uploadPath)
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	setConversionState(conversionState{
		OK:        true,
		State:     "running",
		Message:   conversionRunningMessage(request.mediaKind),
		Output:    filepath.ToSlash(outputPath),
		StartedAt: time.Now().Format(time.RFC3339),
	})
	s.logger.Info("video conversion started from API", "input", uploadPath, "output", outputPath, "status", "ok")

	go s.convertVideo(uploadPath, outputPath, request)
	writeJSON(w, http.StatusAccepted, currentConversionSnapshot())
}

func (s *Server) handleDelayOn(w http.ResponseWriter, r *http.Request) {
	if err := s.controller.EnableDelay(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	status, err := s.controller.Status(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	s.logger.Info("delay enabled from API", "delay", status.Delay, "status", "ok")
	writeJSON(w, http.StatusOK, status)
}

func (s *Server) handleDelayOff(w http.ResponseWriter, r *http.Request) {
	request := delayOffRequest{}
	if r.Body != nil && r.Body != http.NoBody {
		defer r.Body.Close()
		body, err := io.ReadAll(r.Body)
		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		if len(strings.TrimSpace(string(body))) > 0 {
			if err := json.Unmarshal(body, &request); err != nil {
				writeError(w, http.StatusBadRequest, fmt.Errorf("invalid JSON body: %w", err))
				return
			}
		}
	}

	action := func(ctx context.Context) error {
		return s.controller.DisableDelay(ctx)
	}
	if !s.startAsyncAction("delay disabled from API", action) {
		writeError(w, http.StatusConflict, errors.New("uma transicao da live ja esta em andamento; aguarde terminar"))
		return
	}
	status, err := s.controller.Status(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusAccepted, withTransition(status, "retorno ao vivo iniciado"))
}

func (s *Server) handleDelayOffSync(w http.ResponseWriter, r *http.Request) {
	request := delayOffRequest{}
	if r.Body != nil && r.Body != http.NoBody {
		defer r.Body.Close()
		body, err := io.ReadAll(r.Body)
		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		if len(strings.TrimSpace(string(body))) > 0 {
			if err := json.Unmarshal(body, &request); err != nil {
				writeError(w, http.StatusBadRequest, fmt.Errorf("invalid JSON body: %w", err))
				return
			}
		}
	}

	err := s.controller.DisableDelay(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	status, err := s.controller.Status(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	s.logger.Info("delay disabled from API", "status", "ok")
	writeJSON(w, http.StatusOK, status)
}

func (s *Server) handleDelayOffSmooth(w http.ResponseWriter, r *http.Request) {
	if !s.startAsyncAction("smooth delay disable started from API", func(ctx context.Context) error {
		return s.controller.SmoothDisableDelay(ctx)
	}) {
		writeError(w, http.StatusConflict, errors.New("uma transicao da live ja esta em andamento; aguarde terminar"))
		return
	}
	status, err := s.controller.Status(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusAccepted, withTransition(status, "retorno suave iniciado"))
}

func (s *Server) handleDelayOffReset(w http.ResponseWriter, r *http.Request) {
	if !s.startAsyncAction("delay disabled with RTMP output reset from API", func(ctx context.Context) error {
		return s.controller.ForceRealtimeReset(ctx)
	}) {
		writeError(w, http.StatusConflict, errors.New("uma transicao da live ja esta em andamento; aguarde terminar"))
		return
	}
	status, err := s.controller.Status(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusAccepted, withTransition(status, "reabertura da Twitch iniciada"))
}

func (s *Server) handleDelayOffHardReset(w http.ResponseWriter, r *http.Request) {
	pause := 8 * time.Second
	if r.Body != nil && r.Body != http.NoBody {
		defer r.Body.Close()
		var req struct {
			Seconds float64 `json:"seconds"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err == nil && req.Seconds > 0 {
			pause = time.Duration(req.Seconds * float64(time.Second))
		}
	}
	if pause > 20*time.Second {
		pause = 20 * time.Second
	}
	if !s.startAsyncAction("delay disabled with hard RTMP output reset from API", func(ctx context.Context) error {
		return s.controller.ForceRealtimeResetPause(ctx, pause)
	}) {
		writeError(w, http.StatusConflict, errors.New("uma transicao da live ja esta em andamento; aguarde terminar"))
		return
	}
	status, err := s.controller.Status(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusAccepted, withTransition(status, fmt.Sprintf("reset forte iniciado com pausa de %s", pause)))
}

func (s *Server) handleDelaySet(w http.ResponseWriter, r *http.Request) {
	delay, err := parseDelayRequest(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	if err := s.controller.SetDelay(r.Context(), delay); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	status, err := s.controller.Status(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	s.logger.Info("delay changed from API", "delay", status.Delay, "status", "ok")
	writeJSON(w, http.StatusOK, status)
}

func (s *Server) handleDelayArm(w http.ResponseWriter, r *http.Request) {
	request, err := parseDelayArmRequest(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	parsedDelay := request.ParsedDelay()
	slatePath := resolveRuntimePath(request.Slate)
	shortSlate := strings.EqualFold(request.SlateMode, "short")
	playFullSlate := !shortSlate && (request.PlayFullSlate || strings.EqualFold(request.SlateMode, "full"))
	if err := ensureFLVHasAudio(slatePath); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	status, err := s.controller.Status(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if err := validateDelayArmPreflight(status, slatePath, parsedDelay); err != nil {
		writeError(w, http.StatusConflict, err)
		return
	}
	settings, err := loadSettings()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if shortSlate && settings.OutputMode != "encoded" {
		writeError(w, http.StatusConflict, errors.New("loading curto está disponível apenas no modo Encoded"))
		return
	}
	if err := validateLoadingBitrateForSettings(slatePath, settings); err != nil {
		writeError(w, http.StatusConflict, err)
		return
	}
	if !s.startAsyncAction("delay armed from API", func(ctx context.Context) error {
		return s.controller.ArmDelay(ctx, parsedDelay, slatePath, playFullSlate, shortSlate)
	}) {
		writeError(w, http.StatusConflict, errors.New("uma transicao da live ja esta em andamento; aguarde terminar"))
		return
	}
	message := fmt.Sprintf("delay de %s com loading iniciado", parsedDelay)
	if shortSlate {
		message = fmt.Sprintf("delay de %s com loading curto iniciado", parsedDelay)
	}
	writeJSON(w, http.StatusAccepted, withTransition(status, message))
}

func (s *Server) handleDelayArmBuffer(w http.ResponseWriter, r *http.Request) {
	delayValue, err := parseDelayRequest(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	status, err := s.controller.Status(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if err := validateDelayBufferPreflight(status, delayValue); err != nil {
		writeError(w, http.StatusConflict, err)
		return
	}
	if !s.startAsyncAction("delay armed from buffer API", func(ctx context.Context) error {
		return s.controller.ArmDelayFromBuffer(ctx, delayValue)
	}) {
		writeError(w, http.StatusConflict, errors.New("uma transicao da live ja esta em andamento; aguarde terminar"))
		return
	}
	writeJSON(w, http.StatusAccepted, withTransition(status, fmt.Sprintf("delay de %s iniciado pelo buffer", delayValue)))
}

func validateDelayArmPreflight(status Status, slatePath string, delayValue time.Duration) error {
	if status.DelayEnabled || status.DelaySeconds > 0 {
		return errors.New("delay ja esta ativo; use voltar ao vivo antes de adicionar outro delay")
	}
	if !status.Input.Connected {
		return errors.New("a live ainda nao esta chegando do OBS/Streamlabs")
	}
	if !status.Output.Connected {
		return errors.New("a saida ainda nao esta pronta para enviar a live")
	}
	if strings.TrimSpace(slatePath) == "" || !fileExists(slatePath) {
		return errors.New("nenhum video de loading ativo foi encontrado")
	}
	return validateDelayBufferReady(status, delayValue)
}

func validateLoadingBitrateForSettings(slatePath string, settings appSettings) error {
	name := filepath.Base(slatePath)
	if bitrateKbpsFromName(name) == 0 {
		if activeName := readyVideoNameForPath(slatePath); activeName != "" {
			name = activeName
		}
	}
	loadingKbps := bitrateKbpsFromName(name)
	if loadingKbps == 0 {
		return fmt.Errorf("video de loading sem bitrate no nome; converta novamente pelo app antes de adicionar delay")
	}
	liveKbps := bitrateKbpsFromName(normalizeBitrate(settings.EncodedVideoBitrate))
	if liveKbps == 0 {
		return nil
	}
	minKbps := (liveKbps * 85) / 100
	maxKbps := (liveKbps * 110) / 100
	if loadingKbps < minKbps || loadingKbps > maxKbps {
		return fmt.Errorf("bitrate do loading (%dk) nao combina com a live (%dk); converta um loading entre %dk e %dk", loadingKbps, liveKbps, minKbps, maxKbps)
	}
	return nil
}

func readyVideoNameForPath(path string) string {
	path = resolveRuntimePath(path)
	root := runtimeRoot()
	readyDir := filepath.Join(root, "videos", "ready")
	activePath := filepath.Join(root, "videos", "live", "loading.flv")
	if !sameFilePathOrContent(path, activePath) {
		return filepath.Base(path)
	}
	entries, err := os.ReadDir(readyDir)
	if err != nil {
		return ""
	}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".flv" {
			continue
		}
		candidate := filepath.Join(readyDir, entry.Name())
		if sameFileContent(candidate, activePath) {
			return entry.Name()
		}
	}
	return ""
}

func sameFilePathOrContent(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	if sameCleanPath(a, b) {
		return true
	}
	return sameFileContent(a, b)
}

func sameCleanPath(a, b string) bool {
	absA, errA := filepath.Abs(filepath.Clean(a))
	absB, errB := filepath.Abs(filepath.Clean(b))
	if errA != nil || errB != nil {
		return false
	}
	return strings.EqualFold(absA, absB)
}

func bitrateKbpsFromName(name string) int {
	match := regexp.MustCompile(`(?i)(?:^|[_-])(\d{3,6})k(?:[_-]|$)`).FindStringSubmatch(name)
	if len(match) < 2 {
		return 0
	}
	value, _ := strconv.Atoi(match[1])
	return value
}

func validateDelayBufferPreflight(status Status, delayValue time.Duration) error {
	if status.DelayEnabled || status.DelaySeconds > 0 {
		return errors.New("delay ja esta ativo; use voltar ao vivo antes de adicionar outro delay")
	}
	if !status.Input.Connected {
		return errors.New("a live ainda nao esta chegando do OBS/Streamlabs")
	}
	if !status.Output.Connected {
		return errors.New("a saida ainda nao esta pronta para enviar a live")
	}
	return validateDelayBufferReady(status, delayValue)
}

func validateDelayBufferReady(status Status, delayValue time.Duration) error {
	if delayValue <= 0 {
		return nil
	}
	available, err := time.ParseDuration(status.Buffer.Duration)
	if err != nil || available < delayValue {
		if err != nil {
			available = 0
		}
		return fmt.Errorf("o buffer ainda esta em %s; aguarde acumular %s antes de adicionar delay", available.Round(time.Second), delayValue.Round(time.Second))
	}
	return nil
}

func (s *Server) handleLiveSync(w http.ResponseWriter, r *http.Request) {
	if err := s.controller.ForceRealtime(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	status, err := s.controller.Status(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	s.logger.Info("live synchronized from API", "status", "ok")
	writeJSON(w, http.StatusOK, status)
}

type appSettings struct {
	OK                    bool     `json:"ok"`
	Mode                  string   `json:"mode"`
	StreamKey             string   `json:"streamKey,omitempty"`
	SaveStreamKey         bool     `json:"saveStreamKey,omitempty"`
	ClearStreamKey        bool     `json:"clearStreamKey,omitempty"`
	LocalSourcePath       string   `json:"localSourcePath"`
	InputURL              string   `json:"inputUrl"`
	OutputMode            string   `json:"outputMode"`
	OutputURL             string   `json:"outputUrl"`
	EncodedLocalOutputURL string   `json:"encodedLocalOutputUrl"`
	TwitchServer          string   `json:"twitchServer"`
	EncodedWidth          int      `json:"encodedWidth"`
	EncodedHeight         int      `json:"encodedHeight"`
	EncodedFPS            int      `json:"encodedFps"`
	EncodedEncoder        string   `json:"encodedEncoder"`
	EncodedProfileMode    string   `json:"encodedProfileMode"`
	EncodedVideoBitrate   string   `json:"encodedVideoBitrate"`
	EncodedAudioBitrate   string   `json:"encodedAudioBitrate"`
	EncodedQualityPreset  string   `json:"encodedQualityPreset"`
	EncodedScaleQuality   string   `json:"encodedScaleQuality"`
	EncodedBitrateMode    string   `json:"encodedBitrateMode"`
	AutoLatencyCorrection bool     `json:"autoLatencyCorrection"`
	AutoLatencySeconds    float64  `json:"autoLatencySeconds"`
	RealtimePriority      bool     `json:"realtimePriority"`
	RealtimePrioritySecs  float64  `json:"realtimePrioritySeconds"`
	MediaMTXPath          string   `json:"mediaMtxPath"`
	ActiveLoadingPath     string   `json:"activeLoadingPath"`
	ActiveTransitionPath  string   `json:"activeTransitionPath"`
	DelayArmMode          string   `json:"delayArmMode"`
	DefaultDelaySeconds   float64  `json:"defaultDelaySeconds"`
	PlayFullLoading       bool     `json:"playFullLoading"`
	TransitionSeconds     float64  `json:"transitionSeconds"`
	ReturnLoadingSeconds  float64  `json:"returnLoadingSeconds"`
	ViewerLatencySeconds  float64  `json:"viewerLatencySeconds"`
	HotkeyArm             string   `json:"hotkeyArm"`
	HotkeyLive            string   `json:"hotkeyLive"`
	OBS                   obsGuide `json:"obs"`
	StreamKeySaved        bool     `json:"streamKeySaved"`
}

type obsGuide struct {
	Server    string `json:"server"`
	StreamKey string `json:"streamKey"`
	FullURL   string `json:"fullUrl"`
}

func loadSettings() (appSettings, error) {
	path := settingsPath()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return normalizeSettings(appSettings{}), nil
		}
		return appSettings{}, err
	}
	data = bytes.TrimPrefix(data, []byte{0xEF, 0xBB, 0xBF})
	var raw map[string]json.RawMessage
	_ = json.Unmarshal(data, &raw)
	var settings appSettings
	if err := json.Unmarshal(data, &settings); err != nil {
		return appSettings{}, err
	}
	if _, ok := raw["autoLatencyCorrection"]; !ok {
		settings.AutoLatencyCorrection = false
	}
	if _, ok := raw["realtimePriority"]; !ok {
		settings.RealtimePriority = false
	}
	return normalizeSettings(settings), nil
}

func saveSettings(settings appSettings) error {
	path := settingsPath()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	if strings.TrimSpace(settings.StreamKey) != "" {
		if settings.SaveStreamKey {
			if err := secret.Save(runtimeRoot(), settings.StreamKey); err != nil {
				return err
			}
		}
		settings.StreamKey = ""
	}
	if settings.ClearStreamKey {
		if err := secret.Remove(runtimeRoot()); err != nil {
			return err
		}
		settings.ClearStreamKey = false
		settings.SaveStreamKey = false
	}
	data, err := json.MarshalIndent(normalizeSettings(settings), "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

func settingsPath() string {
	return filepath.Join(runtimeRoot(), "settings.json")
}

func normalizeSettings(settings appSettings) appSettings {
	root := runtimeRoot()
	if settings.Mode == "" {
		settings.Mode = "twitch"
	}
	if settings.LocalSourcePath == "" || settings.LocalSourcePath == "live/teste" || isGeneratedLocalSourcePath(settings.LocalSourcePath) {
		settings.LocalSourcePath = defaultLocalSourcePath(root)
	}
	settings.LocalSourcePath = strings.Trim(settings.LocalSourcePath, "/")
	_ = os.WriteFile(filepath.Join(root, ".local-stream-name"), []byte(settings.LocalSourcePath), 0600)
	settings.InputURL = "rtmp://127.0.0.1:1935/" + settings.LocalSourcePath
	if settings.TwitchServer == "" {
		settings.TwitchServer = "rtmp://live.twitch.tv:1935/app"
	}
	if settings.OutputMode == "" || settings.OutputMode == "direct" {
		settings.OutputMode = "copy"
	}
	if settings.OutputMode != "encoded" && settings.OutputMode != "copy" {
		settings.OutputMode = "copy"
	}
	if settings.EncodedLocalOutputURL == "" {
		settings.EncodedLocalOutputURL = "rtmp://127.0.0.1:1935/live/delayengine-out"
	}
	if settings.OutputMode == "encoded" || settings.OutputMode == "copy" {
		settings.OutputURL = settings.EncodedLocalOutputURL
	} else {
		settings.OutputURL = ""
	}
	if settings.EncodedWidth <= 0 {
		settings.EncodedWidth = 1920
	}
	if settings.EncodedHeight <= 0 {
		settings.EncodedHeight = 1080
	}
	if settings.EncodedFPS <= 0 {
		settings.EncodedFPS = 30
	}
	settings.EncodedProfileMode = "manual"
	switch strings.ToLower(strings.TrimSpace(settings.EncodedEncoder)) {
	case "", "auto":
		settings.EncodedEncoder = "auto"
	case "amd", "amf", "h264_amf":
		settings.EncodedEncoder = "amd"
	case "nvidia", "nvenc", "h264_nvenc":
		settings.EncodedEncoder = "nvidia"
	case "intel", "qsv", "h264_qsv":
		settings.EncodedEncoder = "intel"
	case "cpu", "x264", "libx264":
		settings.EncodedEncoder = "cpu"
	default:
		settings.EncodedEncoder = "auto"
	}
	if settings.EncodedVideoBitrate == "" {
		settings.EncodedVideoBitrate = "4500k"
	}
	if settings.EncodedAudioBitrate == "" {
		settings.EncodedAudioBitrate = "160k"
	}
	switch strings.ToLower(strings.TrimSpace(settings.EncodedQualityPreset)) {
	case "latency", "balanced":
		settings.EncodedQualityPreset = strings.ToLower(strings.TrimSpace(settings.EncodedQualityPreset))
	default:
		settings.EncodedQualityPreset = "latency"
	}
	switch strings.ToLower(strings.TrimSpace(settings.EncodedScaleQuality)) {
	case "fast", "balanced", "sharp":
		settings.EncodedScaleQuality = strings.ToLower(strings.TrimSpace(settings.EncodedScaleQuality))
	default:
		settings.EncodedScaleQuality = "balanced"
	}
	switch strings.ToLower(strings.TrimSpace(settings.EncodedBitrateMode)) {
	case "strict", "cbr":
		settings.EncodedBitrateMode = "strict"
	default:
		settings.EncodedBitrateMode = "strict"
	}
	if settings.AutoLatencySeconds <= 0 {
		settings.AutoLatencySeconds = 5
	}
	if settings.AutoLatencySeconds < 1 {
		settings.AutoLatencySeconds = 1
	}
	if settings.AutoLatencySeconds > 30 {
		settings.AutoLatencySeconds = 30
	}
	if settings.RealtimePrioritySecs <= 0 {
		settings.RealtimePrioritySecs = 8
	}
	if settings.RealtimePrioritySecs < 2 {
		settings.RealtimePrioritySecs = 2
	}
	if settings.RealtimePrioritySecs > 60 {
		settings.RealtimePrioritySecs = 60
	}
	if settings.ActiveLoadingPath == "" || !fileExists(filepath.FromSlash(settings.ActiveLoadingPath)) {
		settings.ActiveLoadingPath = filepath.ToSlash(filepath.Join(root, "videos", "live", "loading.flv"))
	}
	if settings.ActiveTransitionPath == "" || !fileExists(filepath.FromSlash(settings.ActiveTransitionPath)) {
		settings.ActiveTransitionPath = filepath.ToSlash(filepath.Join(root, "videos", "live", "transition.flv"))
	}
	switch strings.ToLower(strings.TrimSpace(settings.DelayArmMode)) {
	case "buffer":
		settings.DelayArmMode = "buffer"
	case "short-loading":
		settings.DelayArmMode = "short-loading"
	case "direct":
		// Compatibility with configurations created before the two identical
		// no-loading choices were unified.
		settings.DelayArmMode = "buffer"
	default:
		// First use starts with the direct buffer cut. It keeps the OBS
		// bitstream in Copy mode and does not depend on loading media.
		settings.DelayArmMode = "buffer"
	}
	// Scene-transition preference owns the Twitch output mode. Both loading
	// modes switch between a media file and OBS, so Encoded keeps a single codec
	// timeline. Direct buffer entry stays on the OBS bitstream and uses Copy.
	settings.OutputMode = requiredOutputModeForDelayArmMode(settings.DelayArmMode)
	if settings.DefaultDelaySeconds <= 0 {
		settings.DefaultDelaySeconds = 30
	}
	if settings.DefaultDelaySeconds > maxSettingsDelaySeconds {
		settings.DefaultDelaySeconds = maxSettingsDelaySeconds
	}
	if settings.ReturnLoadingSeconds <= 0 {
		settings.ReturnLoadingSeconds = 6
	}
	if settings.ReturnLoadingSeconds > 20 {
		settings.ReturnLoadingSeconds = 20
	}
	if settings.TransitionSeconds <= 0 {
		settings.TransitionSeconds = 1
	}
	if settings.TransitionSeconds > 5 {
		settings.TransitionSeconds = 5
	}
	if settings.ViewerLatencySeconds < 0 {
		settings.ViewerLatencySeconds = 0
	}
	settings.HotkeyArm = hotkey.Normalize(settings.HotkeyArm, hotkey.DefaultArm)
	settings.HotkeyLive = hotkey.Normalize(settings.HotkeyLive, hotkey.DefaultLive)
	settings.OBS = buildOBSGuide(settings.LocalSourcePath)
	settings.StreamKeySaved = secret.Exists(root)
	settings.OK = true
	return settings
}

func requiredOutputModeForDelayArmMode(delayArmMode string) string {
	if delayArmMode == "short-loading" || delayArmMode == "loading" {
		return "encoded"
	}
	return "copy"
}

func controlDelaySeconds(settings appSettings) float64 {
	seconds := settings.DefaultDelaySeconds + settings.ViewerLatencySeconds
	if seconds <= 0 {
		seconds = settings.DefaultDelaySeconds
	}
	if seconds <= 0 {
		seconds = 30
	}
	if seconds > maxSettingsDelaySeconds {
		seconds = maxSettingsDelaySeconds
	}
	return seconds
}

func encodedProfileChanged(a appSettings, b appSettings) bool {
	return a.EncodedWidth != b.EncodedWidth ||
		a.EncodedHeight != b.EncodedHeight ||
		a.EncodedFPS != b.EncodedFPS ||
		normalizeBitrate(a.EncodedVideoBitrate) != normalizeBitrate(b.EncodedVideoBitrate)
}

func encodedProfileFromStatus(status Status) (encodedAutoProfile, bool) {
	profile := encodedAutoProfile{
		Width:        status.Input.Width,
		Height:       status.Input.Height,
		FPS:          roundedStreamFPS(status.Input.FPS),
		VideoBitrate: bitrateFromKbps(status.Input.BitrateKbps),
	}
	if profile.Width <= 0 || profile.Height <= 0 || profile.FPS <= 0 || profile.VideoBitrate == "" {
		return encodedAutoProfile{}, false
	}
	return profile, true
}

func roundedStreamFPS(fps float64) int {
	switch {
	case fps >= 55:
		return 60
	case fps >= 45:
		return 50
	case fps >= 25:
		return 30
	case fps >= 20:
		return 24
	default:
		return 0
	}
}

func bitrateFromKbps(kbps float64) string {
	if kbps < 1000 {
		return ""
	}
	rounded := int((kbps + 250) / 500)
	rounded *= 500
	if rounded < 1000 {
		rounded = 1000
	}
	return strconv.Itoa(rounded) + "k"
}

func buildOBSGuide(sourcePath string) obsGuide {
	sourcePath = strings.Trim(sourcePath, "/")
	if sourcePath == "" {
		sourcePath = defaultLocalSourcePath(runtimeRoot())
	}
	parts := strings.Split(sourcePath, "/")
	streamKey := parts[len(parts)-1]
	serverPath := "live"
	if len(parts) > 1 {
		serverPath = strings.Join(parts[:len(parts)-1], "/")
	}
	server := "rtmp://127.0.0.1:1935/" + serverPath
	return obsGuide{
		Server:    server,
		StreamKey: streamKey,
		FullURL:   server + "/" + streamKey,
	}
}

func defaultLocalSourcePath(root string) string {
	path := filepath.Join(root, ".local-stream-name")
	if data, err := os.ReadFile(path); err == nil {
		value := strings.Trim(strings.TrimSpace(string(data)), "/")
		if value != "" && value != "live/teste" && !isGeneratedLocalSourcePath(value) {
			return value
		}
	}

	name := "live/delayengine"
	_ = os.WriteFile(path, []byte(name), 0600)
	return name
}

func isGeneratedLocalSourcePath(value string) bool {
	value = strings.Trim(strings.TrimSpace(value), "/")
	if !strings.HasPrefix(value, "live/delayengine-") {
		return false
	}
	suffix := strings.TrimPrefix(value, "live/delayengine-")
	return len(suffix) >= 3 && len(suffix) <= 8
}

func readLogSource(source string, lines int) (string, error) {
	root := runtimeRoot()
	var path string
	switch source {
	case "delayengine", "live":
		path = filepath.Join(root, "logs", "delayengine.log")
	case "mediamtx":
		path = filepath.Join(root, "logs", "mediamtx.log")
	default:
		return "", fmt.Errorf("unknown log source: %s", source)
	}

	text, err := readLastLogLines(path, lines*3)
	if err != nil {
		if os.IsNotExist(err) {
			return "Log ainda nao criado: " + filepath.ToSlash(path), nil
		}
		return "", err
	}
	if source == "live" {
		text = filterLiveLogs(text)
	}
	return limitLines(text, lines), nil
}

func readLastLogLines(path string, lines int) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	const maxBytes = 512 * 1024
	if len(data) > maxBytes {
		data = data[len(data)-maxBytes:]
		if index := strings.IndexByte(string(data), '\n'); index >= 0 && index+1 < len(data) {
			data = data[index+1:]
		}
	}
	return limitLines(string(data), lines), nil
}

func filterLiveLogs(text string) string {
	keywords := []string{
		"RTMP pipeline status",
		"connected to RTMP input",
		"registered video track",
		"registered audio track",
		"published first",
		"received first",
		"delay",
		"slate",
		"live synchronized",
		"realtime",
		"output_queue",
	}
	var filtered []string
	for _, line := range strings.Split(text, "\n") {
		for _, keyword := range keywords {
			if strings.Contains(line, keyword) {
				filtered = append(filtered, line)
				break
			}
		}
	}
	if len(filtered) == 0 {
		return "Sem eventos de live filtrados ainda."
	}
	return strings.Join(filtered, "\n")
}

func limitLines(text string, lines int) string {
	if lines <= 0 {
		return text
	}
	parts := strings.Split(strings.TrimRight(text, "\r\n"), "\n")
	if len(parts) > lines {
		parts = parts[len(parts)-lines:]
	}
	return strings.Join(parts, "\n")
}

type delaySetRequest struct {
	Delay   string   `json:"delay"`
	Seconds *float64 `json:"seconds"`
}

type delayArmRequest struct {
	Delay         string   `json:"delay"`
	Seconds       *float64 `json:"seconds"`
	Slate         string   `json:"slate"`
	SlateMode     string   `json:"slateMode"`
	PlayFullSlate bool     `json:"playFullSlate"`
}

type videoActivateRequest struct {
	Name string `json:"name"`
}

type videosResponse struct {
	OK           bool        `json:"ok"`
	Active       string      `json:"active"`
	ActiveName   string      `json:"activeName"`
	ActiveExists bool        `json:"activeExists"`
	Videos       []videoInfo `json:"videos"`
}

type videoInfo struct {
	Name          string `json:"name"`
	Kind          string `json:"kind"`
	Label         string `json:"label"`
	Path          string `json:"path"`
	Preview       string `json:"preview"`
	PreviewExists bool   `json:"previewExists"`
	Size          int64  `json:"size"`
	SizeMB        string `json:"sizeMB"`
	Active        bool   `json:"active"`
	ActiveTarget  string `json:"activeTarget"`
	ModTime       string `json:"modTime"`
}

type conversionRequest struct {
	profile         conversionProfile
	durationSeconds int
	activate        bool
	mediaKind       string
}

func listVideos() (videosResponse, error) {
	projectRoot := runtimeRoot()
	readyDir := filepath.Join(projectRoot, "videos", "ready")
	activePath := filepath.Join(projectRoot, "videos", "live", "loading.flv")
	activeInfo, activeErr := os.Stat(activePath)
	activeExists := activeErr == nil && activeInfo != nil && activeInfo.Size() > 0

	entries, err := os.ReadDir(readyDir)
	if err != nil {
		if os.IsNotExist(err) {
			return videosResponse{OK: true, Active: filepath.ToSlash(activePath), ActiveExists: activeExists, Videos: []videoInfo{}}, nil
		}
		return videosResponse{}, err
	}

	videos := make([]videoInfo, 0, len(entries))
	activeName := ""
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".flv" {
			continue
		}
		if strings.HasPrefix(strings.ToLower(entry.Name()), "transition_") {
			continue
		}
		path := filepath.Join(readyDir, entry.Name())
		info, err := entry.Info()
		if err != nil {
			return videosResponse{}, err
		}
		previewPath := previewPathForReadyVideo(path)
		kind := mediaKindFromName(entry.Name())
		activeTarget := ""
		active := sameFileContent(path, activePath)
		if active {
			activeName = entry.Name()
			activeTarget = "loading"
		}
		videos = append(videos, videoInfo{
			Name:          entry.Name(),
			Kind:          kind,
			Label:         mediaKindLabel(kind),
			Path:          filepath.ToSlash(path),
			Preview:       filepath.ToSlash(previewPath),
			PreviewExists: fileExists(previewPath),
			Size:          info.Size(),
			SizeMB:        fmt.Sprintf("%.2f MB", float64(info.Size())/1024/1024),
			Active:        active,
			ActiveTarget:  activeTarget,
			ModTime:       info.ModTime().Format(time.RFC3339),
		})
	}

	return videosResponse{
		OK:           true,
		Active:       filepath.ToSlash(activePath),
		ActiveName:   activeName,
		ActiveExists: activeExists,
		Videos:       videos,
	}, nil
}

func activateVideo(name string) (string, error) {
	if filepath.Base(name) != name || filepath.Ext(name) != ".flv" {
		return "", fmt.Errorf("invalid video name: %s", name)
	}

	projectRoot := runtimeRoot()
	source := filepath.Join(projectRoot, "videos", "ready", name)
	destination := filepath.Join(projectRoot, "videos", "live", "loading.flv")

	if err := ensureFLVHasAudio(source); err != nil {
		return "", err
	}
	data, err := os.ReadFile(source)
	if err != nil {
		return "", fmt.Errorf("read ready video: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0755); err != nil {
		return "", err
	}
	if err := os.WriteFile(destination, data, 0644); err != nil {
		return "", fmt.Errorf("activate video: %w", err)
	}
	return filepath.ToSlash(destination), nil
}

func deleteReadyVideo(name string) error {
	if filepath.Base(name) != name || filepath.Ext(name) != ".flv" {
		return fmt.Errorf("invalid video name: %s", name)
	}

	projectRoot := runtimeRoot()
	source := filepath.Join(projectRoot, "videos", "ready", name)
	sourceInfo, err := os.Stat(source)
	if err != nil {
		return fmt.Errorf("video nao encontrado: %w", err)
	}
	activePath := filepath.Join(projectRoot, "videos", "live", "loading.flv")
	activeInfo, _ := os.Stat(activePath)
	if activeInfo != nil && activeInfo.Size() == sourceInfo.Size() && sameFileContent(source, activePath) {
		return errors.New("esta midia parece estar ativa; ative outra antes de apagar")
	}
	if err := os.Remove(source); err != nil {
		return fmt.Errorf("apagar video: %w", err)
	}
	_ = os.Remove(previewPathForReadyVideo(source))
	return nil
}

func sameFileContent(leftPath, rightPath string) bool {
	leftInfo, err := os.Stat(leftPath)
	if err != nil {
		return false
	}
	rightInfo, err := os.Stat(rightPath)
	if err != nil || leftInfo.Size() != rightInfo.Size() {
		return false
	}
	left, err := os.ReadFile(leftPath)
	if err != nil {
		return false
	}
	right, err := os.ReadFile(rightPath)
	if err != nil {
		return false
	}
	return bytes.Equal(left, right)
}

func mediaKindFromName(name string) string {
	lower := strings.ToLower(name)
	switch {
	case strings.HasPrefix(lower, "loading_imagem_"):
		return "image"
	case strings.HasPrefix(lower, "loading_video_"):
		return "video"
	default:
		return "loading"
	}
}

func mediaKindLabel(kind string) string {
	switch kind {
	case "image":
		return "imagem de loading"
	case "video":
		return "video de loading"
	default:
		return "loading convertido"
	}
}

func durationSecondsFromName(name string) int {
	match := regexp.MustCompile(`(?i)_(\d{1,3})s(?:_|\.flv$)`).FindStringSubmatch(name)
	if len(match) < 2 {
		return 0
	}
	value, _ := strconv.Atoi(match[1])
	return value
}

func openConverter() error {
	root := runtimeRoot()
	script := filepath.Join(root, "scripts", "prepare-slate.ps1")
	if _, err := os.Stat(script); err != nil {
		return fmt.Errorf("conversor nao encontrado: %w", err)
	}
	cmd := exec.Command("powershell", "-NoProfile", "-ExecutionPolicy", "Bypass", "-File", script)
	cmd.Dir = root
	return cmd.Start()
}

func openPath(path string) error {
	switch runtime.GOOS {
	case "windows":
		return exec.Command("explorer", path).Start()
	case "darwin":
		return exec.Command("open", path).Start()
	default:
		return exec.Command("xdg-open", path).Start()
	}
}

func (s *Server) convertVideo(inputPath, outputPath string, request conversionRequest) {
	defer func() {
		_ = os.Remove(inputPath)
	}()

	ffmpegPath, err := findLocalTool("ffmpeg.exe")
	if err != nil {
		setConversionState(conversionState{
			OK:         false,
			State:      "error",
			Message:    "FFmpeg nao encontrado. Use o instalador do app ou instale o FFmpeg.",
			Output:     filepath.ToSlash(outputPath),
			FinishedAt: time.Now().Format(time.RFC3339),
			Error:      err.Error(),
		})
		s.logger.Error("video conversion failed", "error", err, "status", "error")
		return
	}

	if err := os.MkdirAll(filepath.Dir(outputPath), 0755); err != nil {
		setConversionError(outputPath, fmt.Errorf("criar pasta de videos prontos: %w", err))
		return
	}

	profile := request.profile
	gop := maxInt(1, profile.FPS*2)
	bufSize := bitrateBuffer(profile.VideoBitrate)
	vf := fmt.Sprintf("scale=%d:%d:force_original_aspect_ratio=decrease,pad=%d:%d:(ow-iw)/2:(oh-ih)/2,setsar=1,fps=%d,format=yuv420p", profile.Width, profile.Height, profile.Width, profile.Height, profile.FPS)
	x264Params := fmt.Sprintf("keyint=%d:min-keyint=%d:scenecut=0:bframes=0:force-cfr=1:nal-hrd=cbr", gop, gop)
	audioMap := "1:a:0"
	if inputHasAudioStream(inputPath) {
		audioMap = "0:a:0"
	}

	args := []string{"-y"}
	if isStillImagePath(inputPath) {
		// Give still images an advancing clock. Reopening a JPG with
		// stream_loop resets its PTS to zero, so a duration-limited conversion
		// can run forever without ever completing the output timeline.
		args = append(args, "-loop", "1", "-framerate", strconv.Itoa(profile.FPS), "-i", inputPath)
	} else {
		args = append(args, "-stream_loop", "-1", "-i", inputPath)
	}
	args = append(args,
		"-f", "lavfi",
		"-i", "anullsrc=channel_layout=stereo:sample_rate=48000",
		"-t", strconv.Itoa(request.durationSeconds),
		"-map", "0:v:0",
		"-map", audioMap,
		"-vf", vf,
		"-r", strconv.Itoa(profile.FPS),
		"-vsync", "cfr",
		"-shortest",
		"-c:v", "libx264",
		"-preset", "veryfast",
		"-tune", "zerolatency",
		"-profile:v", "high",
		"-level", h264LevelForProfile(profile),
		"-pix_fmt", "yuv420p",
		"-b:v", profile.VideoBitrate,
		"-maxrate", profile.VideoBitrate,
		"-bufsize", bufSize,
		"-bf", "0",
		"-g", strconv.Itoa(gop),
		"-keyint_min", strconv.Itoa(gop),
		"-sc_threshold", "0",
		"-force_key_frames", "expr:gte(t,n_forced*2)",
		"-x264-params", x264Params,
		"-c:a", "aac",
		"-b:a", "160k",
		"-ar", "48000",
		"-ac", "2",
		"-map_metadata", "-1",
		"-disposition:a:0", "default",
		"-f", "flv",
		outputPath,
	)

	conversionTimeout := 2 * time.Minute
	if requested := time.Duration(request.durationSeconds)*4*time.Second + 30*time.Second; requested > conversionTimeout {
		conversionTimeout = requested
	}
	conversionCtx, cancel := context.WithTimeout(context.Background(), conversionTimeout)
	defer cancel()
	cmd := exec.CommandContext(conversionCtx, ffmpegPath, args...)
	cmd.Dir = runtimeRoot()
	applyHiddenWindow(cmd)
	output, err := cmd.CombinedOutput()
	if err != nil {
		_ = os.Remove(outputPath)
		if errors.Is(conversionCtx.Err(), context.DeadlineExceeded) {
			err = fmt.Errorf("conversao excedeu o tempo limite de %s", conversionTimeout)
		}
		setConversionState(conversionState{
			OK:         false,
			State:      "error",
			Message:    "FFmpeg terminou com erro:\n" + tailString(string(output), 1800),
			Output:     filepath.ToSlash(outputPath),
			FinishedAt: time.Now().Format(time.RFC3339),
			Error:      err.Error(),
		})
		s.logger.Error("video conversion failed", "error", err, "status", "error")
		return
	}
	if err := createVideoPreview(context.Background(), ffmpegPath, outputPath, previewPathForReadyVideo(outputPath)); err != nil {
		s.logger.Warn("video preview generation failed", "error", err, "status", "warn")
	}

	activePath := ""
	if request.activate {
		var err error
		activePath, err = activateVideo(filepath.Base(outputPath))
		if err != nil {
			setConversionError(outputPath, err)
			return
		}
	}

	message := conversionDoneMessage(request.mediaKind, false)
	if activePath != "" {
		message = conversionDoneMessage(request.mediaKind, true)
	}
	setConversionState(conversionState{
		OK:         true,
		State:      "done",
		Message:    message,
		Output:     filepath.ToSlash(outputPath),
		Active:     filepath.ToSlash(activePath),
		FinishedAt: time.Now().Format(time.RFC3339),
	})
	s.logger.Info("video conversion finished", "output", outputPath, "active", activePath, "status", "ok")
}

func isStillImagePath(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".jpg", ".jpeg", ".png", ".webp", ".bmp", ".tif", ".tiff":
		return true
	default:
		return false
	}
}

func conversionRunningMessage(kind string) string {
	if kind == "image" {
		return "Convertendo imagem. Pode deixar esta tela aberta; eu aviso quando terminar."
	}
	return "Convertendo video. Pode deixar esta tela aberta; eu aviso quando terminar."
}

func conversionDoneMessage(kind string, active bool) string {
	mediaName := "Video"
	if kind == "image" {
		mediaName = "Imagem"
	}
	if active {
		return mediaName + " convertida e ativada para o proximo loading."
	}
	return mediaName + " convertida e salva em videos/ready."
}

func encodedRelayArgs(settings appSettings, outputURL string, ffmpegPath string, skipVideoFilter bool) []string {
	width := settings.EncodedWidth
	height := settings.EncodedHeight
	fps := settings.EncodedFPS
	if width <= 0 {
		width = 1920
	}
	if height <= 0 {
		height = 1080
	}
	if fps <= 0 {
		fps = 30
	}
	videoBitrate := normalizeBitrate(settings.EncodedVideoBitrate)
	if videoBitrate == "" {
		videoBitrate = "4500k"
	}
	audioBitrate := normalizeBitrate(settings.EncodedAudioBitrate)
	if audioBitrate == "" {
		audioBitrate = "160k"
	}
	inputURL := settings.EncodedLocalOutputURL
	if inputURL == "" {
		inputURL = "rtmp://127.0.0.1:1935/live/delayengine-out"
	}
	if settings.OutputMode == "copy" {
		return []string{
			"-hide_banner",
			"-loglevel", "info",
			"-fflags", "nobuffer",
			"-flags", "low_delay",
			// Wait just long enough to receive the H264 configuration / keyframe.
			// Starting with no probe can leave FFmpeg without video dimensions,
			// making the FLV/Twitch header fail and the relay restart in a loop.
			"-analyzeduration", "2000000",
			"-probesize", "1048576",
			"-rtmp_live", "live",
			"-rtmp_buffer", "0",
			"-thread_queue_size", "64",
			// Rebase the new RTMP reader at zero. Without this, an already-open
			// local GOP carries its old timestamps into Twitch at startup.
			"-copyts",
			"-start_at_zero",
			"-i", inputURL,
			"-map", "0:v:0",
			"-map", "0:a:0?",
			"-c", "copy",
			"-max_interleave_delta", "0",
			"-muxdelay", "0",
			"-muxpreload", "0",
			"-flush_packets", "1",
			"-flvflags", "no_duration_filesize",
			"-f", "flv",
			outputURL,
		}
	}
	gop := fps * 2
	if gop < 1 {
		gop = 1
	}
	encoder := resolveEncodedVideoEncoder(settings.EncodedEncoder, ffmpegPath)
	args := []string{
		"-hide_banner",
		"-loglevel", "info",
		"-fflags", "nobuffer",
		"-flags", "low_delay",
		"-analyzeduration", "1000000",
		"-probesize", "32768",
		"-rtmp_live", "live",
		"-rtmp_buffer", "0",
		"-thread_queue_size", "256",
		// The delayed cursor is replenished in short batches. Its media clock is
		// continuous, but the packets can reach FFmpeg faster than wall clock for
		// a few hundred milliseconds after a transition. Rate-limit the input by
		// that clock so the hardware encoder cannot turn a nominal 6 Mbps stream
		// into short 12-17 Mbps bursts that make low-latency players rebuffer.
		"-readrate", "1.0",
		// Recover short input/network stalls gradually instead of preserving them
		// as permanent viewer latency. The explicit 1.05x ceiling is intentionally
		// modest: it avoids the unbounded 12-17 Mbps bursts seen with unrestricted
		// catch-up while giving a 6000 kbps profile only about 300 kbps of recovery
		// headroom.
		"-readrate_catchup", "1.05",
		"-i", inputURL,
	}
	args = append(args, "-map", "0:v:0", "-map", "0:a:0?")
	if !skipVideoFilter {
		scaleFilter := fmt.Sprintf("scale=%d:%d:flags=%s:force_original_aspect_ratio=decrease,pad=%d:%d:(ow-iw)/2:(oh-ih)/2,format=yuv420p", width, height, scaleFilterFlag(settings.EncodedScaleQuality), width, height)
		args = append(args, "-vf", scaleFilter)
	}
	// RTMP inputs do not always expose a usable frame rate to FFmpeg (they can
	// appear as 1k fps). The hardware encoder needs the configured rate for
	// correct CBR pacing, even when no scale filter is necessary.
	args = append(args, "-r", strconv.Itoa(fps))
	args = append(args, encodedVideoEncoderArgs(encoder, gop, videoBitrate, settings.EncodedQualityPreset, settings.EncodedBitrateMode)...)
	args = append(args,
		"-c:a", "aac",
		"-af", "aresample=async=1000:first_pts=0",
		"-b:a", audioBitrate,
		"-ar", "48000",
		"-ac", "2",
		"-max_interleave_delta", "0",
		"-muxdelay", "0",
		"-muxpreload", "0",
		"-flush_packets", "1",
		"-flvflags", "no_duration_filesize",
		"-f", "flv",
		outputURL,
	)
	return args
}

func resolveEncodedVideoEncoder(choice string, ffmpegPath string) string {
	switch strings.ToLower(strings.TrimSpace(choice)) {
	case "amd", "amf", "h264_amf":
		return "h264_amf"
	case "nvidia", "nvenc", "h264_nvenc":
		return "h264_nvenc"
	case "intel", "qsv", "h264_qsv":
		return "h264_qsv"
	case "cpu", "x264", "libx264":
		return "libx264"
	}
	for _, encoder := range []string{"h264_amf", "h264_nvenc", "h264_qsv"} {
		if ffmpegEncoderAvailable(ffmpegPath, encoder) {
			return encoder
		}
	}
	return "libx264"
}

func defaultEncodedEncoder() string {
	encoderDetectOnce.Do(func() {
		detectedEncoder = detectWindowsEncodedEncoder()
		if detectedEncoder == "" {
			detectedEncoder = "cpu"
		}
	})
	return detectedEncoder
}

func detectWindowsEncodedEncoder() string {
	if runtime.GOOS != "windows" {
		return "cpu"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "powershell", "-NoProfile", "-Command", "(Get-CimInstance Win32_VideoController | Select-Object -ExpandProperty Name) -join ' | '")
	applyHiddenWindow(cmd)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return ""
	}
	gpus := strings.ToLower(string(output))
	switch {
	case strings.Contains(gpus, "nvidia"):
		return "nvidia"
	case strings.Contains(gpus, "amd") || strings.Contains(gpus, "radeon") || strings.Contains(gpus, "advanced micro devices"):
		return "amd"
	case strings.Contains(gpus, "intel"):
		return "intel"
	default:
		return ""
	}
}

func ffmpegEncoderAvailable(ffmpegPath string, encoder string) bool {
	if strings.TrimSpace(ffmpegPath) == "" || strings.TrimSpace(encoder) == "" {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, ffmpegPath, "-hide_banner", "-encoders")
	applyHiddenWindow(cmd)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return false
	}
	return bytes.Contains(output, []byte(encoder))
}

func scaleFilterFlag(quality string) string {
	switch strings.ToLower(strings.TrimSpace(quality)) {
	case "fast":
		return "fast_bilinear"
	case "sharp":
		return "lanczos"
	default:
		return "bicubic"
	}
}

func encodedVideoEncoderArgs(encoder string, gop int, videoBitrate string, qualityPreset string, bitrateMode string) []string {
	bitrateMode = strings.ToLower(strings.TrimSpace(bitrateMode))
	strictBitrate := bitrateMode == "strict" || bitrateMode == "cbr"
	minRate := bitratePercent(videoBitrate, 50)
	if minRate == "" {
		minRate = videoBitrate
	}
	maxRate := bitratePercent(videoBitrate, 150)
	if maxRate == "" {
		maxRate = videoBitrate
	}
	bufferSize := bitrateBufferPercent(videoBitrate, 100)
	common := []string{
		"-c:v", encoder,
		"-profile:v", "high",
		"-pix_fmt", "yuv420p",
		"-g", strconv.Itoa(gop),
		"-keyint_min", strconv.Itoa(gop),
		"-bf", "0",
		"-b:v", videoBitrate,
		"-minrate", minRate,
		"-maxrate", maxRate,
		"-bufsize", bufferSize,
	}
	if strictBitrate {
		// Hardware encoders can otherwise treat the configured bitrate as only a
		// ceiling and emit far below it on static scenes. Strict mode is meant to
		// deliver the chosen Twitch bitrate consistently.
		minRate = videoBitrate
		maxRate = videoBitrate
		// Half a second of VBV keeps transition IDRs inside the configured CBR
		// envelope. A two-second VBV allowed a nominal 6 Mbps stream to emit
		// 9-16 Mbps bursts followed by starvation at the Twitch player.
		bufferSize = bitrateBufferPercent(videoBitrate, 50)
		common = []string{
			"-c:v", encoder,
			"-profile:v", "high",
			"-pix_fmt", "yuv420p",
			"-g", strconv.Itoa(gop),
			"-keyint_min", strconv.Itoa(gop),
			"-bf", "0",
			"-b:v", videoBitrate,
			"-minrate", minRate,
			"-maxrate", maxRate,
			"-bufsize", bufferSize,
		}
	}
	qualityPreset = strings.ToLower(strings.TrimSpace(qualityPreset))
	switch encoder {
	case "h264_amf":
		quality := "speed"
		if qualityPreset == "balanced" || qualityPreset == "quality" {
			quality = "balanced"
		}
		usage := "lowlatency"
		rateControl := "vbr_latency"
		extra := []string{
			"-usage", usage,
			"-quality", quality,
			"-rc", rateControl,
			"-enforce_hrd", "true",
			"-high_motion_quality_boost_enable", "true",
		}
		if strictBitrate {
			rateControl = "cbr"
			usage = "lowlatency_high_quality"
			if quality == "speed" {
				quality = "balanced"
			}
			extra = []string{
				"-usage", usage,
				"-quality", quality,
				"-rc", rateControl,
				"-enforce_hrd", "true",
				"-filler_data", "true",
				"-frame_skipping", "false",
				"-async_depth", "4",
				// Keep a transition keyframe from consuming the entire small VBV in
				// one access unit. Twelve average frames leaves useful IDR headroom
				// without producing a network-sized burst.
				"-max_au_size", maxAccessUnitBits(videoBitrate, gop),
				"-high_motion_quality_boost_enable", "true",
			}
		}
		return append(common, extra...)
	case "h264_nvenc":
		// p4 keeps NVENC's quality/performance balance while tune=ull and the
		// zero-latency flags preserve the realtime behavior. p1 saved some GPU
		// time, but spent visibly more quality at the same Twitch bitrate.
		preset := "p4"
		rateControl := "vbr"
		extra := []string{"-preset", preset, "-tune", "ull", "-rc", rateControl, "-multipass", "disabled", "-zerolatency", "1"}
		if strictBitrate {
			extra = []string{"-preset", preset, "-tune", "ull", "-rc", "cbr", "-multipass", "disabled", "-zerolatency", "1", "-cbr", "true", "-cbr_padding", "true"}
		}
		return append(common, extra...)
	case "h264_qsv":
		preset := "veryfast"
		extra := []string{"-preset", preset, "-low_power", "1"}
		if strictBitrate {
			extra = append(extra, "-look_ahead", "0")
		}
		return append(common, extra...)
	default:
		x264Params := fmt.Sprintf("keyint=%d:min-keyint=%d:scenecut=0:bframes=0:force-cfr=1:rc-lookahead=0:sync-lookahead=0:sliced-threads=1", gop, gop)
		if strictBitrate {
			x264Params += ":nal-hrd=cbr:filler=1"
		}
		// veryfast is still realtime-oriented, but avoids the large compression
		// efficiency penalty of ultrafast at the same bitrate.
		preset := "veryfast"
		return append(common,
			"-preset", preset,
			"-tune", "zerolatency",
			"-sc_threshold", "0",
			"-force_key_frames", "expr:gte(t,n_forced*2)",
			"-x264-params", x264Params,
		)
	}
}

func configRedactedTwitch(server string) string {
	if server == "" {
		server = "rtmp://live.twitch.tv:1935/app"
	}
	return strings.TrimRight(server, "/") + "/<stream-key>"
}

func parseConversionRequest(r *http.Request) (conversionRequest, error) {
	durationSeconds := parsePositiveInt(r.FormValue("duration"), 30)
	mediaKind := "video"
	if durationSeconds < 1 || durationSeconds > 600 {
		return conversionRequest{}, errors.New("duracao precisa ficar entre 1 e 600 segundos")
	}

	profile, err := profileFromRequest(r)
	if err != nil {
		return conversionRequest{}, err
	}
	return conversionRequest{
		profile:         profile,
		durationSeconds: durationSeconds,
		activate:        parseBool(r.FormValue("activate")),
		mediaKind:       mediaKind,
	}, nil
}

func profileFromRequest(r *http.Request) (conversionProfile, error) {
	switch r.FormValue("profile") {
	case "1080p30":
		return conversionProfile{Width: 1920, Height: 1080, FPS: 30, VideoBitrate: "6000k"}, nil
	case "1080p30hq":
		return conversionProfile{Width: 1920, Height: 1080, FPS: 30, VideoBitrate: "6000k"}, nil
	case "1080p60":
		return conversionProfile{Width: 1920, Height: 1080, FPS: 60, VideoBitrate: "8000k"}, nil
	case "720p30":
		return conversionProfile{Width: 1280, Height: 720, FPS: 30, VideoBitrate: "4500k"}, nil
	case "720p60":
		return conversionProfile{Width: 1280, Height: 720, FPS: 60, VideoBitrate: "6000k"}, nil
	case "1440p30":
		return conversionProfile{Width: 2560, Height: 1440, FPS: 30, VideoBitrate: "9000k"}, nil
	case "1440p60":
		return conversionProfile{Width: 2560, Height: 1440, FPS: 60, VideoBitrate: "10000k"}, nil
	case "2160p30", "4k30":
		return conversionProfile{Width: 3840, Height: 2160, FPS: 30, VideoBitrate: "10000k"}, nil
	case "2160p60", "4k60":
		return conversionProfile{Width: 3840, Height: 2160, FPS: 60, VideoBitrate: "10000k"}, nil
	case "custom":
		width := parsePositiveInt(r.FormValue("width"), 1920)
		height := parsePositiveInt(r.FormValue("height"), 1080)
		fps := parsePositiveInt(r.FormValue("fps"), 30)
		bitrate := strings.TrimSpace(r.FormValue("bitrate"))
		if bitrate == "" {
			bitrate = "6000k"
		}
		if width < 320 || height < 240 || fps < 10 || fps > 120 {
			return conversionProfile{}, errors.New("perfil customizado invalido")
		}
		return conversionProfile{Width: width, Height: height, FPS: fps, VideoBitrate: normalizeBitrate(bitrate)}, nil
	default:
		return conversionProfile{Width: 1920, Height: 1080, FPS: 30, VideoBitrate: "6000k"}, nil
	}
}

func h264LevelForProfile(profile conversionProfile) string {
	pixels := profile.Width * profile.Height
	fps := maxInt(1, profile.FPS)
	samplesPerSecond := pixels * fps

	switch {
	case profile.Width >= 3840 || profile.Height >= 2160 || samplesPerSecond > 2560*1440*60:
		if fps > 30 {
			return "5.2"
		}
		return "5.1"
	case profile.Width >= 2560 || profile.Height >= 1440 || samplesPerSecond > 1920*1080*60:
		return "5.1"
	case fps > 30:
		return "4.2"
	default:
		return "4.1"
	}
}

func findLocalTool(exeName string) (string, error) {
	if path, err := exec.LookPath(exeName); err == nil {
		return path, nil
	}

	root := runtimeRoot()
	// The portable bundle has a stable FFmpeg location. Check it directly
	// before walking tool folders or the WinGet package tree on startup.
	if bundled := filepath.Join(root, "tools", "ffmpeg", "bin", exeName); fileExists(bundled) {
		return bundled, nil
	}
	savedPathFile := filepath.Join(root, ".ffmpeg-bin-path")
	if data, err := os.ReadFile(savedPathFile); err == nil {
		dir := strings.TrimSpace(string(data))
		if dir != "" {
			candidate := filepath.Join(dir, exeName)
			if fileExists(candidate) {
				return candidate, nil
			}
		}
	}

	candidates := []string{
		filepath.Join(root, "tools", "ffmpeg", "bin"),
		filepath.Join(root, "tools", "ffmpeg"),
		filepath.Join(os.Getenv("LOCALAPPDATA"), "Microsoft", "WinGet", "Packages"),
		filepath.Join(os.Getenv("ProgramFiles"), "ffmpeg", "bin"),
		filepath.Join(os.Getenv("ProgramFiles(x86)"), "ffmpeg", "bin"),
	}
	for _, dir := range candidates {
		if dir == "" || !fileExists(dir) {
			continue
		}
		found := ""
		_ = filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
			if err != nil || found != "" {
				return nil
			}
			if !entry.IsDir() && strings.EqualFold(entry.Name(), exeName) {
				found = path
				return filepath.SkipAll
			}
			return nil
		})
		if found != "" {
			_ = os.WriteFile(savedPathFile, []byte(filepath.Dir(found)), 0644)
			return found, nil
		}
	}
	return "", fmt.Errorf("%s nao encontrado", exeName)
}

func nextReadyVideoPath(directory string, request conversionRequest) (string, error) {
	if err := os.MkdirAll(directory, 0755); err != nil {
		return "", err
	}
	profile := request.profile
	bitrate := safeFilenamePart(strings.TrimSuffix(normalizeBitrate(profile.VideoBitrate), "k"))
	if bitrate == "" {
		bitrate = "4000"
	}
	prefix := "loading_video"
	if request.mediaKind == "image" {
		prefix = "loading_imagem"
	}
	stamp := time.Now().Format("20060102_150405")
	base := fmt.Sprintf("%s_%dx%d_%dfps_%sk_%ds_%s", prefix, profile.Width, profile.Height, profile.FPS, bitrate, request.durationSeconds, stamp)
	for index := 1; ; index++ {
		candidate := filepath.Join(directory, fmt.Sprintf("%s_%03d.flv", base, index))
		if !fileExists(candidate) {
			return candidate, nil
		}
	}
}

func safeFilenamePart(value string) string {
	value = strings.TrimSpace(value)
	var builder strings.Builder
	for _, char := range value {
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9') || char == '-' || char == '_' {
			builder.WriteRune(char)
		}
	}
	return builder.String()
}

func copyConvertedVideoToLive(source string) (string, error) {
	root := runtimeRoot()
	destination := filepath.Join(root, "videos", "live", "loading.flv")
	input, err := os.Open(source)
	if err != nil {
		return "", fmt.Errorf("abrir video convertido: %w", err)
	}
	defer input.Close()
	if err := os.MkdirAll(filepath.Dir(destination), 0755); err != nil {
		return "", err
	}
	output, err := os.Create(destination)
	if err != nil {
		return "", fmt.Errorf("ativar video convertido: %w", err)
	}
	if _, err := io.Copy(output, input); err != nil {
		_ = output.Close()
		return "", fmt.Errorf("copiar video convertido: %w", err)
	}
	if err := output.Close(); err != nil {
		return "", fmt.Errorf("fechar video ativo: %w", err)
	}
	return destination, nil
}

func previewPathForReadyVideo(path string) string {
	ext := filepath.Ext(path)
	if ext == "" {
		return path + ".mp4"
	}
	return strings.TrimSuffix(path, ext) + ".mp4"
}

func createVideoPreview(ctx context.Context, ffmpegPath, source, destination string) error {
	if err := os.MkdirAll(filepath.Dir(destination), 0755); err != nil {
		return err
	}
	args := []string{
		"-y",
		"-i", source,
		"-map", "0:v:0",
		"-map", "0:a:0?",
		"-c", "copy",
		"-movflags", "+faststart",
		destination,
	}
	cmd := exec.CommandContext(ctx, ffmpegPath, args...)
	cmd.Dir = runtimeRoot()
	applyHiddenWindow(cmd)
	output, err := cmd.CombinedOutput()
	if err != nil {
		_ = os.Remove(destination)
		return fmt.Errorf("gerar preview MP4: %w: %s", err, tailString(string(output), 1200))
	}
	return nil
}

func inputHasAudioStream(path string) bool {
	ffprobePath, err := findLocalTool("ffprobe.exe")
	if err != nil {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx,
		ffprobePath,
		"-v", "error",
		"-select_streams", "a:0",
		"-show_entries", "stream=index",
		"-of", "csv=p=0",
		path,
	)
	applyHiddenWindow(cmd)
	output, err := cmd.Output()
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(output)) != ""
}

func ensureFLVHasAudio(path string) error {
	if strings.TrimSpace(path) == "" || !fileExists(path) || inputHasAudioStream(path) {
		return nil
	}
	ffmpegPath, err := findLocalTool("ffmpeg.exe")
	if err != nil {
		return fmt.Errorf("FFmpeg nao encontrado para adicionar audio silencioso ao loading: %w", err)
	}
	tempPath := strings.TrimSuffix(path, filepath.Ext(path)) + ".with-audio.tmp.flv"
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	args := []string{
		"-y",
		"-i", path,
		"-f", "lavfi",
		"-i", "anullsrc=channel_layout=stereo:sample_rate=48000",
		"-map", "0:v:0",
		"-map", "1:a:0",
		"-c:v", "copy",
		"-c:a", "aac",
		"-b:a", "160k",
		"-ar", "48000",
		"-ac", "2",
		"-shortest",
		"-map_metadata", "-1",
		"-f", "flv",
		tempPath,
	}
	cmd := exec.CommandContext(ctx, ffmpegPath, args...)
	cmd.Dir = runtimeRoot()
	applyHiddenWindow(cmd)
	output, err := cmd.CombinedOutput()
	if err != nil {
		_ = os.Remove(tempPath)
		return fmt.Errorf("adicionar audio silencioso ao loading: %w: %s", err, tailString(string(output), 1200))
	}
	backupPath := strings.TrimSuffix(path, filepath.Ext(path)) + ".no-audio.bak.flv"
	_ = os.Remove(backupPath)
	if err := os.Rename(path, backupPath); err != nil {
		_ = os.Remove(tempPath)
		return fmt.Errorf("preparar troca do loading com audio silencioso: %w", err)
	}
	if err := os.Rename(tempPath, path); err != nil {
		_ = os.Rename(backupPath, path)
		_ = os.Remove(tempPath)
		return fmt.Errorf("substituir loading com audio silencioso: %w", err)
	}
	_ = os.Remove(backupPath)
	return nil
}

func currentConversionSnapshot() conversionState {
	conversionMu.Lock()
	defer conversionMu.Unlock()
	return currentConversion
}

func setConversionState(state conversionState) {
	conversionMu.Lock()
	currentConversion = state
	conversionMu.Unlock()
}

func setConversionError(outputPath string, err error) {
	setConversionState(conversionState{
		OK:         false,
		State:      "error",
		Message:    err.Error(),
		Output:     filepath.ToSlash(outputPath),
		FinishedAt: time.Now().Format(time.RFC3339),
		Error:      err.Error(),
	})
}

func safeUploadName(name string) string {
	name = filepath.Base(strings.TrimSpace(name))
	if name == "" || name == "." {
		return "video"
	}
	replacer := strings.NewReplacer("/", "_", "\\", "_", ":", "_", "*", "_", "?", "_", `"`, "_", "<", "_", ">", "_", "|", "_")
	return replacer.Replace(name)
}

func parsePositiveInt(value string, fallback int) int {
	parsed, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || parsed <= 0 {
		return fallback
	}
	return parsed
}

func parseBool(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "sim", "s", "on":
		return true
	default:
		return false
	}
}

func normalizeBitrate(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	if value == "" {
		return "6000k"
	}
	if strings.HasSuffix(value, "k") || strings.HasSuffix(value, "m") {
		return value
	}
	if _, err := strconv.Atoi(value); err == nil {
		return value + "k"
	}
	return "6000k"
}

func bitrateBuffer(bitrate string) string {
	return bitrateBufferPercent(bitrate, 150)
}

func bitrateBufferPercent(bitrate string, percent int) string {
	bitrate = normalizeBitrate(bitrate)
	if strings.HasSuffix(bitrate, "k") {
		kbps, err := strconv.Atoi(strings.TrimSuffix(bitrate, "k"))
		if err == nil {
			return fmt.Sprintf("%dk", maxInt((kbps*percent)/100, 1000))
		}
	}
	return "6000k"
}

func bitratePercent(bitrate string, percent int) string {
	bitrate = normalizeBitrate(bitrate)
	if strings.HasSuffix(bitrate, "k") {
		kbps, err := strconv.Atoi(strings.TrimSuffix(bitrate, "k"))
		if err == nil {
			return fmt.Sprintf("%dk", maxInt((kbps*percent)/100, 1000))
		}
	}
	return ""
}

func maxAccessUnitBits(bitrate string, gop int) string {
	bitrate = normalizeBitrate(bitrate)
	kbps, err := strconv.Atoi(strings.TrimSuffix(bitrate, "k"))
	if err != nil || kbps <= 0 {
		kbps = 6000
	}
	fps := maxInt(gop/2, 1)
	bits := maxInt((kbps*1000/fps)*12, 250000)
	return strconv.Itoa(bits)
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func resolveRuntimePath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	path = filepath.FromSlash(path)
	if filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(runtimeRoot(), path)
}

func tailString(text string, maxLength int) string {
	if len(text) <= maxLength {
		return text
	}
	return text[len(text)-maxLength:]
}

func parseDelayArmRequest(r *http.Request) (delayArmRequest, error) {
	defer r.Body.Close()
	var req delayArmRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return req, fmt.Errorf("invalid JSON body: %w", err)
	}
	if req.Slate == "" {
		return req, errors.New(`missing slate; use {"delay":"30s","slate":"C:/path/loading.flv"}`)
	}
	if req.Delay != "" {
		delay, err := time.ParseDuration(req.Delay)
		if err != nil {
			return req, err
		}
		req.Seconds = nil
		req.Delay = delay.String()
		return req, nil
	}
	if req.Seconds != nil {
		delay := time.Duration(*req.Seconds * float64(time.Second))
		req.Delay = delay.String()
		return req, nil
	}
	return req, errors.New(`missing delay; use {"delay":"30s","slate":"C:/path/loading.flv"}`)
}

func (r delayArmRequest) ParsedDelay() time.Duration {
	delay, _ := time.ParseDuration(r.Delay)
	return delay
}

func parseDelayRequest(r *http.Request) (time.Duration, error) {
	if value := r.URL.Query().Get("delay"); value != "" {
		return time.ParseDuration(value)
	}
	if value := r.URL.Query().Get("seconds"); value != "" {
		seconds, err := strconv.ParseFloat(value, 64)
		if err != nil {
			return 0, fmt.Errorf("invalid seconds: %w", err)
		}
		return time.Duration(seconds * float64(time.Second)), nil
	}

	defer r.Body.Close()
	var req delaySetRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return 0, fmt.Errorf("invalid JSON body: %w", err)
	}
	if req.Delay != "" {
		return time.ParseDuration(req.Delay)
	}
	if req.Seconds != nil {
		return time.Duration(*req.Seconds * float64(time.Second)), nil
	}
	return 0, errors.New(`missing delay; use {"delay":"30s"} or {"seconds":30}`)
}

func writeJSON(w http.ResponseWriter, statusCode int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, statusCode int, err error) {
	writeJSON(w, statusCode, map[string]any{
		"ok":    false,
		"error": err.Error(),
	})
}
