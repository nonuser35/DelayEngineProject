package api

import (
	"bytes"
	"context"
	crand "crypto/rand"
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

	"delayengine/internal/secret"
)

//go:embed static/*
var staticFiles embed.FS

type DelayController interface {
	Status(ctx context.Context) (Status, error)
	EnableDelay(ctx context.Context) error
	DisableDelay(ctx context.Context) error
	DisableDelayWithSlate(ctx context.Context, slatePath string, slateDuration time.Duration) error
	SmoothDisableDelay(ctx context.Context) error
	SetDelay(ctx context.Context, delay time.Duration) error
	ArmDelay(ctx context.Context, delay time.Duration, slatePath string, playFullSlate bool) error
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
	Message           string       `json:"message,omitempty"`
}

type BufferStatus struct {
	Packets  int    `json:"packets"`
	Duration string `json:"duration"`
	Bytes    int    `json:"bytes"`
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
	mux.HandleFunc("GET /status", server.handleStatus)
	mux.HandleFunc("GET /logs", server.handleLogs)
	mux.HandleFunc("GET /settings", server.handleSettingsGet)
	mux.HandleFunc("POST /settings", server.handleSettingsSave)
	mux.HandleFunc("POST /tools/open-converter", server.handleOpenConverter)
	mux.HandleFunc("POST /tools/open-videos", server.handleOpenVideos)
	mux.HandleFunc("GET /tools/conversion-status", server.handleConversionStatus)
	mux.HandleFunc("POST /tools/convert-video", server.handleConvertVideo)
	mux.HandleFunc("GET /encoding/status", server.handleEncodingStatus)
	mux.HandleFunc("POST /encoding/start", server.handleEncodingStart)
	mux.HandleFunc("POST /encoding/stop", server.handleEncodingStop)
	mux.HandleFunc("GET /preview-hls/", server.handlePreviewHLS)
	mux.HandleFunc("GET /videos", server.handleVideos)
	mux.HandleFunc("POST /videos/activate", server.handleVideoActivate)
	mux.HandleFunc("POST /videos/delete", server.handleVideoDelete)
	mux.HandleFunc("POST /delay/on", server.handleDelayOn)
	mux.HandleFunc("POST /delay/off", server.handleDelayOff)
	mux.HandleFunc("POST /delay/off-smooth", server.handleDelayOffSmooth)
	mux.HandleFunc("POST /delay/off-reset", server.handleDelayOffReset)
	mux.HandleFunc("POST /delay/off-hard-reset", server.handleDelayOffHardReset)
	mux.HandleFunc("POST /delay/set", server.handleDelaySet)
	mux.HandleFunc("POST /delay/arm", server.handleDelayArm)
	mux.HandleFunc("POST /live/sync", server.handleLiveSync)

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
	const retryInterval = 5 * time.Second
	ticker := time.NewTicker(retryInterval)
	defer ticker.Stop()

	for {
		settings, err := loadSettings()
		if err != nil {
			s.logger.Warn("could not load settings for Twitch polished auto-start", "error", err, "status", "waiting")
			<-ticker.C
			continue
		}
		if settings.OutputMode != "encoded" || s.encodingPaused.Load() {
			<-ticker.C
			continue
		}

		status, err := s.controller.Status(context.Background())
		if err != nil || !status.Input.Connected || !status.Output.Connected || status.Output.Video == 0 {
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

		if _, err := s.startEncodedRelay(settings); err != nil {
			s.logger.Warn("Twitch polished encoder auto-start waiting", "error", err, "retry_in", retryInterval, "status", "waiting")
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
	writeJSON(w, http.StatusOK, status)
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
	var settings appSettings
	if err := json.NewDecoder(r.Body).Decode(&settings); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid JSON body: %w", err))
		return
	}
	settings = normalizeSettings(settings)
	if err := saveSettings(settings); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	s.logger.Info("settings saved from API", "status", "ok")
	writeJSON(w, http.StatusOK, settings)
}

func (s *Server) handleVideos(w http.ResponseWriter, r *http.Request) {
	videos, err := listVideos()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, videos)
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
		"ok":     true,
		"active": active,
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
	if settings.OutputMode != "encoded" {
		settings.OutputMode = "encoded"
		if err := saveSettings(settings); err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		settings = normalizeSettings(settings)
	}
	s.encodingPaused.Store(false)
	status, err := s.startEncodedRelay(settings)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusAccepted, status)
}

func (s *Server) startEncodedRelay(settings appSettings) (encodedRelayState, error) {
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

	root := runtimeRoot()
	if err := os.MkdirAll(filepath.Join(root, "logs"), 0755); err != nil {
		encodedRelayMu.Unlock()
		return encodedRelayState{}, err
	}
	logPath := filepath.Join(root, "logs", "ffmpeg-encoded-twitch.log")
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		encodedRelayMu.Unlock()
		return encodedRelayState{}, fmt.Errorf("abrir log do codificador: %w", err)
	}

	outputURL := strings.TrimRight(settings.TwitchServer, "/") + "/" + key
	args := encodedRelayArgs(settings, outputURL, ffmpegPath)
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
	s.logger.Info("encoded Twitch relay started", "input", settings.EncodedLocalOutputURL, "pid", cmd.Process.Pid, "status", "ok")

	go s.monitorEncodedRelay(cmd, logPath, time.Now())

	go func() {
		err := cmd.Wait()
		_ = logFile.Close()
		encodedRelayMu.Lock()
		defer encodedRelayMu.Unlock()
		if encodedRelayCmd == cmd {
			encodedRelayCmd = nil
		}
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

func (s *Server) monitorEncodedRelay(cmd *exec.Cmd, logPath string, started time.Time) {
	const (
		warmupDuration = 90 * time.Second
		checkInterval  = 5 * time.Second
		minSpeed       = 0.99
		slowDuration   = 45 * time.Second
		logInterval    = 30 * time.Second
		correctionCool = 2 * time.Minute
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
			estimatedDelaySeconds += elapsedSeconds * (1 - speed)
			if estimatedDelaySeconds < 0 {
				estimatedDelaySeconds = 0
			}
		}

		health := "ok"
		if time.Since(started) < warmupDuration {
			health = "warming-up"
		} else if speed < minSpeed {
			health = "slow"
		} else if estimatedDelaySeconds >= 1 {
			health = "recovering"
		}

		encodedRelayMu.Lock()
		if encodedRelayCmd == cmd {
			currentRelay.Speed = fmt.Sprintf("%.3fx", speed)
			currentRelay.Health = health
			if speed < minSpeed && time.Since(started) >= warmupDuration {
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
	if !settings.AutoLatencyCorrection {
		return false
	}
	threshold := settings.AutoLatencySeconds
	if threshold <= 0 {
		threshold = 3
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
	outputPath, err := nextReadyVideoPath(filepath.Join(root, "videos", "ready"), request)
	if err != nil {
		_ = os.Remove(uploadPath)
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	setConversionState(conversionState{
		OK:        true,
		State:     "running",
		Message:   "Convertendo video. Pode deixar esta tela aberta; eu aviso quando terminar.",
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
		if strings.TrimSpace(request.Slate) != "" {
			slateDuration := time.Duration(request.Seconds * float64(time.Second))
			if slateDuration < 0 {
				slateDuration = 0
			}
			if slateDuration > 20*time.Second {
				slateDuration = 20 * time.Second
			}
			return s.controller.DisableDelayWithSlate(ctx, request.Slate, slateDuration)
		}
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

	var err error
	if strings.TrimSpace(request.Slate) != "" {
		slateDuration := time.Duration(request.Seconds * float64(time.Second))
		if slateDuration < 0 {
			slateDuration = 0
		}
		if slateDuration > 20*time.Second {
			slateDuration = 20 * time.Second
		}
		err = s.controller.DisableDelayWithSlate(r.Context(), request.Slate, slateDuration)
	} else {
		err = s.controller.DisableDelay(r.Context())
	}
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
	slatePath := request.Slate
	playFullSlate := request.PlayFullSlate || strings.EqualFold(request.SlateMode, "full")
	if !s.startAsyncAction("delay armed from API", func(ctx context.Context) error {
		return s.controller.ArmDelay(ctx, parsedDelay, slatePath, playFullSlate)
	}) {
		writeError(w, http.StatusConflict, errors.New("uma transicao da live ja esta em andamento; aguarde terminar"))
		return
	}
	status, err := s.controller.Status(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusAccepted, withTransition(status, fmt.Sprintf("delay de %s com loading iniciado", parsedDelay)))
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
	EncodedVideoBitrate   string   `json:"encodedVideoBitrate"`
	EncodedAudioBitrate   string   `json:"encodedAudioBitrate"`
	AutoLatencyCorrection bool     `json:"autoLatencyCorrection"`
	AutoLatencySeconds    float64  `json:"autoLatencySeconds"`
	MediaMTXPath          string   `json:"mediaMtxPath"`
	ActiveLoadingPath     string   `json:"activeLoadingPath"`
	DefaultDelaySeconds   float64  `json:"defaultDelaySeconds"`
	ReturnLoadingSeconds  float64  `json:"returnLoadingSeconds"`
	ViewerLatencySeconds  float64  `json:"viewerLatencySeconds"`
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
	var settings appSettings
	if err := json.Unmarshal(data, &settings); err != nil {
		return appSettings{}, err
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
	if settings.LocalSourcePath == "" || settings.LocalSourcePath == "live/teste" {
		settings.LocalSourcePath = defaultLocalSourcePath(root)
	}
	settings.LocalSourcePath = strings.Trim(settings.LocalSourcePath, "/")
	_ = os.WriteFile(filepath.Join(root, ".local-stream-name"), []byte(settings.LocalSourcePath), 0600)
	settings.InputURL = "rtmp://127.0.0.1:1935/" + settings.LocalSourcePath
	if settings.TwitchServer == "" {
		settings.TwitchServer = "rtmp://live.twitch.tv:1935/app"
	}
	if settings.OutputMode == "" || settings.OutputMode == "direct" {
		settings.OutputMode = "encoded"
	}
	if settings.OutputMode != "encoded" {
		settings.OutputMode = "direct"
	}
	if settings.EncodedLocalOutputURL == "" {
		settings.EncodedLocalOutputURL = "rtmp://127.0.0.1:1935/live/delayengine-out"
	}
	if settings.OutputMode == "encoded" {
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
	switch strings.ToLower(strings.TrimSpace(settings.EncodedEncoder)) {
	case "", "auto":
		settings.EncodedEncoder = defaultEncodedEncoder()
	case "amd", "amf", "h264_amf":
		settings.EncodedEncoder = "amd"
	case "nvidia", "nvenc", "h264_nvenc":
		settings.EncodedEncoder = "nvidia"
	case "intel", "qsv", "h264_qsv":
		settings.EncodedEncoder = "intel"
	case "cpu", "x264", "libx264":
		settings.EncodedEncoder = "cpu"
	default:
		settings.EncodedEncoder = defaultEncodedEncoder()
	}
	if settings.EncodedVideoBitrate == "" {
		settings.EncodedVideoBitrate = "4500k"
	}
	if settings.EncodedAudioBitrate == "" {
		settings.EncodedAudioBitrate = "160k"
	}
	if settings.AutoLatencySeconds <= 0 {
		settings.AutoLatencySeconds = 3
	}
	if settings.AutoLatencySeconds < 1 {
		settings.AutoLatencySeconds = 1
	}
	if settings.AutoLatencySeconds > 30 {
		settings.AutoLatencySeconds = 30
	}
	if settings.ActiveLoadingPath == "" || !fileExists(filepath.FromSlash(settings.ActiveLoadingPath)) {
		settings.ActiveLoadingPath = filepath.ToSlash(filepath.Join(root, "videos", "live", "loading.flv"))
	}
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
	if settings.ViewerLatencySeconds < 0 {
		settings.ViewerLatencySeconds = 0
	}
	settings.OBS = buildOBSGuide(settings.LocalSourcePath)
	settings.StreamKeySaved = secret.Exists(root)
	settings.OK = true
	return settings
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
		if value != "" && value != "live/teste" {
			return value
		}
	}

	name := "live/delayengine-" + randomToken(4)
	_ = os.WriteFile(path, []byte(name), 0600)
	return name
}

func randomToken(length int) string {
	const alphabet = "23456789abcdefghjkmnpqrstuvwxyz"
	bytes := make([]byte, length)
	if _, err := crand.Read(bytes); err != nil {
		return fmt.Sprintf("%06d", time.Now().UnixNano()%1000000)
	}
	for index := range bytes {
		bytes[index] = alphabet[int(bytes[index])%len(alphabet)]
	}
	return string(bytes)
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
	ActiveExists bool        `json:"activeExists"`
	Videos       []videoInfo `json:"videos"`
}

type videoInfo struct {
	Name    string `json:"name"`
	Path    string `json:"path"`
	Size    int64  `json:"size"`
	SizeMB  string `json:"sizeMB"`
	Active  bool   `json:"active"`
	ModTime string `json:"modTime"`
}

type conversionRequest struct {
	profile         conversionProfile
	durationSeconds int
	activate        bool
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
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".flv" {
			continue
		}
		path := filepath.Join(readyDir, entry.Name())
		info, err := entry.Info()
		if err != nil {
			return videosResponse{}, err
		}
		videos = append(videos, videoInfo{
			Name:    entry.Name(),
			Path:    filepath.ToSlash(path),
			Size:    info.Size(),
			SizeMB:  fmt.Sprintf("%.2f MB", float64(info.Size())/1024/1024),
			Active:  sameFileContent(path, activePath),
			ModTime: info.ModTime().Format(time.RFC3339),
		})
	}

	return videosResponse{
		OK:           true,
		Active:       filepath.ToSlash(activePath),
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
	activeInfo, _ := os.Stat(filepath.Join(projectRoot, "videos", "live", "loading.flv"))
	if activeInfo != nil && activeInfo.Size() == sourceInfo.Size() && sameFileContent(source, filepath.Join(projectRoot, "videos", "live", "loading.flv")) {
		return errors.New("este video parece ser o loading ativo; ative outro video antes de apagar")
	}
	if err := os.Remove(source); err != nil {
		return fmt.Errorf("apagar video: %w", err)
	}
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

	args := []string{
		"-y",
		"-stream_loop", "-1",
		"-i", inputPath,
		"-t", strconv.Itoa(request.durationSeconds),
		"-map", "0:v:0",
		"-map", "0:a:0?",
		"-vf", vf,
		"-r", strconv.Itoa(profile.FPS),
		"-vsync", "cfr",
		"-c:v", "libx264",
		"-preset", "veryfast",
		"-tune", "zerolatency",
		"-profile:v", "high",
		"-level", "4.2",
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
		"-f", "flv",
		outputPath,
	}

	cmd := exec.Command(ffmpegPath, args...)
	cmd.Dir = runtimeRoot()
	applyHiddenWindow(cmd)
	output, err := cmd.CombinedOutput()
	if err != nil {
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

	activePath := ""
	if request.activate {
		var err error
		activePath, err = copyConvertedVideoToLive(outputPath)
		if err != nil {
			setConversionError(outputPath, err)
			return
		}
	}

	message := "Video convertido e salvo em videos/ready."
	if activePath != "" {
		message = "Video convertido e ativado para o proximo loading."
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

func encodedRelayArgs(settings appSettings, outputURL string, ffmpegPath string) []string {
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
	gop := fps * 2
	if gop < 1 {
		gop = 1
	}
	scaleFilter := fmt.Sprintf("scale=%d:%d:flags=fast_bilinear:force_original_aspect_ratio=decrease,pad=%d:%d:(ow-iw)/2:(oh-ih)/2,format=yuv420p", width, height, width, height)
	encoder := resolveEncodedVideoEncoder(settings.EncodedEncoder, ffmpegPath)
	args := []string{
		"-hide_banner",
		"-loglevel", "info",
		"-rtmp_live", "live",
		"-thread_queue_size", "1024",
		"-i", inputURL,
		"-map", "0:v:0",
		"-map", "0:a:0?",
		"-vf", scaleFilter,
		"-r", strconv.Itoa(fps),
		"-fps_mode", "cfr",
	}
	args = append(args, encodedVideoEncoderArgs(encoder, gop, videoBitrate)...)
	args = append(args,
		"-c:a", "aac",
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

func encodedVideoEncoderArgs(encoder string, gop int, videoBitrate string) []string {
	common := []string{
		"-c:v", encoder,
		"-profile:v", "high",
		"-pix_fmt", "yuv420p",
		"-g", strconv.Itoa(gop),
		"-keyint_min", strconv.Itoa(gop),
		"-bf", "0",
		"-b:v", videoBitrate,
		"-maxrate", videoBitrate,
		"-bufsize", bitrateBuffer(videoBitrate),
	}
	switch encoder {
	case "h264_amf":
		return append(common, "-usage", "lowlatency", "-quality", "speed", "-rc", "cbr")
	case "h264_nvenc":
		return append(common, "-preset", "p1", "-tune", "ull", "-rc", "cbr")
	case "h264_qsv":
		return append(common, "-preset", "veryfast")
	default:
		x264Params := fmt.Sprintf("keyint=%d:min-keyint=%d:scenecut=0:bframes=0:force-cfr=1:nal-hrd=cbr:rc-lookahead=0:sync-lookahead=0:sliced-threads=1", gop, gop)
		return append(common,
			"-preset", "ultrafast",
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
	if durationSeconds < 5 || durationSeconds > 600 {
		return conversionRequest{}, errors.New("duracao precisa ficar entre 5 e 600 segundos")
	}

	profile, err := profileFromRequest(r)
	if err != nil {
		return conversionRequest{}, err
	}
	return conversionRequest{
		profile:         profile,
		durationSeconds: durationSeconds,
		activate:        parseBool(r.FormValue("activate")),
	}, nil
}

func profileFromRequest(r *http.Request) (conversionProfile, error) {
	switch r.FormValue("profile") {
	case "1080p30hq":
		return conversionProfile{Width: 1920, Height: 1080, FPS: 30, VideoBitrate: "4500k"}, nil
	case "1080p60":
		return conversionProfile{Width: 1920, Height: 1080, FPS: 60, VideoBitrate: "5500k"}, nil
	case "720p30":
		return conversionProfile{Width: 1280, Height: 720, FPS: 30, VideoBitrate: "3000k"}, nil
	case "720p60":
		return conversionProfile{Width: 1280, Height: 720, FPS: 60, VideoBitrate: "4000k"}, nil
	case "custom":
		width := parsePositiveInt(r.FormValue("width"), 1920)
		height := parsePositiveInt(r.FormValue("height"), 1080)
		fps := parsePositiveInt(r.FormValue("fps"), 30)
		bitrate := strings.TrimSpace(r.FormValue("bitrate"))
		if bitrate == "" {
			bitrate = "4000k"
		}
		if width < 320 || height < 240 || fps < 10 || fps > 120 {
			return conversionProfile{}, errors.New("perfil customizado invalido")
		}
		return conversionProfile{Width: width, Height: height, FPS: fps, VideoBitrate: normalizeBitrate(bitrate)}, nil
	default:
		return conversionProfile{Width: 1920, Height: 1080, FPS: 30, VideoBitrate: "4000k"}, nil
	}
}

func findLocalTool(exeName string) (string, error) {
	if path, err := exec.LookPath(exeName); err == nil {
		return path, nil
	}

	root := runtimeRoot()
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
	base := fmt.Sprintf("loading_%dx%d_%dfps_%sk_%ds", profile.Width, profile.Height, profile.FPS, bitrate, request.durationSeconds)
	for index := 1; ; index++ {
		candidate := filepath.Join(directory, fmt.Sprintf("%s_%02d.flv", base, index))
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
		return "4000k"
	}
	if strings.HasSuffix(value, "k") || strings.HasSuffix(value, "m") {
		return value
	}
	if _, err := strconv.Atoi(value); err == nil {
		return value + "k"
	}
	return "4000k"
}

func bitrateBuffer(bitrate string) string {
	bitrate = normalizeBitrate(bitrate)
	if strings.HasSuffix(bitrate, "k") {
		kbps, err := strconv.Atoi(strings.TrimSuffix(bitrate, "k"))
		if err == nil {
			return fmt.Sprintf("%dk", maxInt(kbps+(kbps/2), 1000))
		}
	}
	return "6000k"
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
