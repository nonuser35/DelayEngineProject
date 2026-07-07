package main

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"delayengine/internal/app"
	"delayengine/internal/config"
	"delayengine/internal/logging"

	"github.com/getlantern/systray"
)

//go:embed assets/app-icon.ico
var appIcon []byte

const (
	windowsRunKey  = `HKCU\Software\Microsoft\Windows\CurrentVersion\Run`
	windowsRunName = "DelayEngine"
)

func main() {
	root := config.RuntimeRoot()
	_ = os.Chdir(root)
	logger, closeLog := newLogger()
	defer closeLog()

	cfg, err := config.Load()
	if err != nil {
		logger.Error("failed to load configuration", "error", err)
		os.Exit(1)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	startMediaMTXSupervisor(ctx, logger)

	engineDone := make(chan struct{})
	go func() {
		defer close(engineDone)
		engine := app.New(cfg, logger)
		if err := engine.Run(ctx); err != nil && ctx.Err() == nil {
			logger.Error("delayengine stopped with error", "error", err)
		}
	}()

	go func() {
		<-engineDone
		systray.Quit()
	}()

	tray := &trayApp{
		cfg:        cfg,
		logger:     logger,
		cancel:     cancel,
		engineDone: engineDone,
	}
	systray.Run(tray.onReady, tray.onExit)
}

type trayApp struct {
	cfg        config.Config
	logger     *slog.Logger
	cancel     context.CancelFunc
	engineDone <-chan struct{}
}

type trayStatusResponse struct {
	DelayEnabled bool    `json:"delayEnabled"`
	Delay        string  `json:"delay"`
	DelaySeconds float64 `json:"delaySeconds"`
	Input        struct {
		Connected bool `json:"connected"`
	} `json:"input"`
	Output struct {
		Connected bool `json:"connected"`
	} `json:"output"`
}

func (t *trayApp) onReady() {
	if icon := trayIcon(); len(icon) > 0 {
		systray.SetIcon(icon)
	}
	systray.SetTitle("DelayEngine")
	systray.SetTooltip("DelayEngine rodando")

	openPanel := systray.AddMenuItem("Abrir painel", "Abre a interface web")
	delayStatus := systray.AddMenuItem("Delay: verificando...", "Status atual do delay manual")
	delayStatus.Disable()
	systray.AddSeparator()
	armDelay := systray.AddMenuItem("Adicionar delay com loading", "Usa o delay e o video ativo configurados no painel")
	syncLive := systray.AddMenuItem("Voltar ao vivo agora", "Descarta delay acumulado")
	openLogs := systray.AddMenuItem("Abrir pasta de logs", "Abre a pasta logs")
	startWithWindows := systray.AddMenuItemCheckbox("Iniciar com Windows", "Abre o DelayEngine automaticamente ao entrar no Windows", t.startupEnabled())
	systray.AddSeparator()
	quit := systray.AddMenuItem("Sair", "Fecha o DelayEngine")

	go func() {
		for {
			select {
			case <-openPanel.ClickedCh:
				t.openPanel()
			case <-armDelay.ClickedCh:
				t.armDelay()
			case <-syncLive.ClickedCh:
				t.forceRealtime()
			case <-openLogs.ClickedCh:
				t.openLogs()
			case <-startWithWindows.ClickedCh:
				t.toggleStartup(startWithWindows)
			case <-quit.ClickedCh:
				t.cancel()
				select {
				case <-t.engineDone:
				case <-time.After(3 * time.Second):
				}
				systray.Quit()
				return
			}
		}
	}()

	go func() {
		time.Sleep(1200 * time.Millisecond)
		t.openPanel()
	}()

	go t.watchDelayStatus(delayStatus)
}

func (t *trayApp) onExit() {
	t.cancel()
}

func (t *trayApp) startupEnabled() bool {
	if runtime.GOOS != "windows" {
		return false
	}

	output, err := hiddenCommand("reg", "query", windowsRunKey, "/v", windowsRunName).CombinedOutput()
	if err != nil {
		return false
	}

	exe, err := os.Executable()
	if err != nil {
		return true
	}

	return strings.Contains(strings.ToLower(string(output)), strings.ToLower(exe))
}

func (t *trayApp) toggleStartup(item *systray.MenuItem) {
	if runtime.GOOS != "windows" {
		item.Uncheck()
		t.logger.Warn("startup with Windows is only available on Windows", "status", "warning")
		return
	}

	if t.startupEnabled() {
		if err := t.disableStartup(); err != nil {
			item.Check()
			t.logger.Error("failed to disable startup with Windows", "error", err, "status", "error")
			return
		}
		item.Uncheck()
		t.logger.Info("startup with Windows disabled", "status", "ok")
		return
	}

	if err := t.enableStartup(); err != nil {
		item.Uncheck()
		t.logger.Error("failed to enable startup with Windows", "error", err, "status", "error")
		return
	}
	item.Check()
	t.logger.Info("startup with Windows enabled", "status", "ok")
}

func (t *trayApp) enableStartup() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}

	value := `"` + exe + `"`
	output, err := hiddenCommand("reg", "add", windowsRunKey, "/v", windowsRunName, "/t", "REG_SZ", "/d", value, "/f").CombinedOutput()
	if err != nil {
		return fmt.Errorf("register startup: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

func (t *trayApp) disableStartup() error {
	output, err := hiddenCommand("reg", "delete", windowsRunKey, "/v", windowsRunName, "/f").CombinedOutput()
	if err != nil {
		return fmt.Errorf("remove startup: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

func startMediaMTXSupervisor(ctx context.Context, logger *slog.Logger) {
	path := findMediaMTX()
	if path == "" {
		logger.Warn("MediaMTX executable not found; waiting for external MediaMTX", "status", "waiting")
		return
	}

	go func() {
		var owned *mediaMTXProcess
		portReadyLogged := false
		for {
			if tcpOpen("127.0.0.1:1935") {
				if owned == nil && !portReadyLogged {
					logger.Info("MediaMTX already running or ready", "status", "ok")
					portReadyLogged = true
				}
				select {
				case <-ctx.Done():
					if owned != nil && owned.cmd.Process != nil {
						_ = owned.cmd.Process.Kill()
					}
					return
				case <-time.After(2 * time.Second):
					continue
				}
			}
			portReadyLogged = false

			process := startMediaMTXProcess(path, logger)
			if process == nil {
				select {
				case <-ctx.Done():
					return
				case <-time.After(2 * time.Second):
					continue
				}
			}
			owned = process

			done := make(chan struct{})
			go func() {
				_ = process.cmd.Wait()
				_ = process.logFile.Close()
				close(done)
			}()

			select {
			case <-ctx.Done():
				if process.cmd.Process != nil {
					_ = process.cmd.Process.Kill()
				}
				<-done
				return
			case <-done:
				owned = nil
				logger.Warn("MediaMTX process stopped; restarting when port is free", "status", "waiting")
				time.Sleep(time.Second)
			}
		}
	}()
}

type mediaMTXProcess struct {
	cmd     *exec.Cmd
	logFile *os.File
}

func startMediaMTXProcess(path string, logger *slog.Logger) *mediaMTXProcess {
	logPath := filepath.Join(config.RuntimeRoot(), "logs", "mediamtx.log")
	_ = os.MkdirAll(filepath.Dir(logPath), 0755)
	_ = logging.Rotate(logPath, logging.DefaultMaxBytes, logging.DefaultBackups)
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		logger.Error("failed to open MediaMTX log", "error", err, "status", "error")
		return nil
	}

	cmd := hiddenCommand(path)
	cmd.Dir = filepath.Dir(path)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	if runtime.GOOS == "windows" {
		cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	}
	if err := cmd.Start(); err != nil {
		_ = logFile.Close()
		logger.Error("failed to start MediaMTX", "error", err, "path", path, "status", "error")
		return nil
	}

	logger.Info("MediaMTX started hidden", "path", path, "log", logPath, "status", "ok")
	return &mediaMTXProcess{cmd: cmd, logFile: logFile}
}

func findMediaMTX() string {
	root := config.RuntimeRoot()
	if data, err := os.ReadFile(filepath.Join(root, ".mediamtx-path")); err == nil {
		path := strings.TrimSpace(string(data))
		if path != "" {
			if _, err := os.Stat(path); err == nil {
				return path
			}
		}
	}

	candidates := []string{
		filepath.Join(root, "tools", "mediamtx", "mediamtx.exe"),
		filepath.Join(root, "mediamtx", "mediamtx.exe"),
		filepath.Join(root, "bin", "mediamtx.exe"),
	}
	for _, candidate := range candidates {
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}

	var found string
	_ = filepath.WalkDir(filepath.Join(root, "tools"), func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry == nil || entry.IsDir() || found != "" {
			return nil
		}
		if strings.EqualFold(entry.Name(), "mediamtx.exe") {
			found = path
		}
		return nil
	})
	return found
}

func tcpOpen(address string) bool {
	conn, err := net.DialTimeout("tcp", address, 500*time.Millisecond)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

func (t *trayApp) openPanel() {
	url := "http://127.0.0.1" + t.cfg.HTTPAddr + "/"
	if t.cfg.HTTPAddr == ":8080" {
		url = "http://127.0.0.1:8080/"
	}
	if err := openBrowser(url); err != nil {
		t.logger.Error("failed to open panel", "error", err, "status", "error")
	}
}

func (t *trayApp) watchDelayStatus(item *systray.MenuItem) {
	client := &http.Client{Timeout: 2 * time.Second}
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		t.updateDelayStatus(client, item)
		select {
		case <-t.engineDone:
			return
		case <-ticker.C:
		}
	}
}

func (t *trayApp) updateDelayStatus(client *http.Client, item *systray.MenuItem) {
	var status trayStatusResponse
	if err := t.getJSON(client, "/status", &status); err != nil {
		item.SetTitle("Delay: aguardando app")
		systray.SetTooltip("DelayEngine aguardando status")
		return
	}
	liveState := "sem live"
	if status.Input.Connected && status.Output.Connected {
		liveState = "ao vivo"
	} else if status.Input.Connected {
		liveState = "preparando"
	}
	if status.DelayEnabled || status.DelaySeconds > 0 {
		delay := strings.TrimSpace(status.Delay)
		if delay == "" {
			delay = "ativo"
		}
		item.SetTitle("Delay: ativo " + delay)
		systray.SetTooltip("DelayEngine: delay ativo (" + delay + "), " + liveState)
		return
	}
	item.SetTitle("Delay: OFF")
	systray.SetTooltip("DelayEngine: delay OFF, " + liveState)
}

func (t *trayApp) armDelay() {
	client := &http.Client{Timeout: 5 * time.Second}
	settings := struct {
		DefaultDelaySeconds  float64 `json:"defaultDelaySeconds"`
		ViewerLatencySeconds float64 `json:"viewerLatencySeconds"`
	}{}
	if err := t.getJSON(client, "/settings", &settings); err != nil {
		t.logger.Error("failed to read settings for tray delay", "error", err, "status", "error")
		return
	}

	videos := struct {
		Active       string `json:"active"`
		ActiveExists bool   `json:"activeExists"`
	}{}
	if err := t.getJSON(client, "/videos", &videos); err != nil {
		t.logger.Error("failed to read active loading video for tray delay", "error", err, "status", "error")
		return
	}
	if !videos.ActiveExists || strings.TrimSpace(videos.Active) == "" {
		t.logger.Warn("tray delay skipped; no active loading video", "status", "waiting")
		return
	}

	seconds := settings.DefaultDelaySeconds + settings.ViewerLatencySeconds
	if seconds <= 0 {
		seconds = settings.DefaultDelaySeconds
	}
	if seconds <= 0 {
		seconds = 30
	}
	if seconds > 60 {
		seconds = 60
	}
	body, err := json.Marshal(map[string]any{
		"seconds": seconds,
		"slate":   videos.Active,
	})
	if err != nil {
		t.logger.Error("failed to prepare tray delay request", "error", err, "status", "error")
		return
	}
	response, err := client.Post(t.apiURL("/delay/arm"), "application/json", bytes.NewReader(body))
	if err != nil {
		t.logger.Error("failed to arm delay from tray", "error", err, "status", "error")
		return
	}
	defer response.Body.Close()
	if response.StatusCode >= 300 {
		t.logger.Error("arm delay from tray failed", "status_code", response.StatusCode, "status", "error")
		return
	}
	t.logger.Info("delay armed from tray", "delay_seconds", seconds, "slate", videos.Active, "status", "ok")
}

func (t *trayApp) forceRealtime() {
	request, err := http.NewRequest(http.MethodPost, t.apiURL("/live/sync"), nil)
	if err != nil {
		t.logger.Error("failed to create realtime request", "error", err, "status", "error")
		return
	}
	client := &http.Client{Timeout: 5 * time.Second}
	response, err := client.Do(request)
	if err != nil {
		t.logger.Error("failed to force realtime from tray", "error", err, "status", "error")
		return
	}
	defer response.Body.Close()
	if response.StatusCode >= 300 {
		t.logger.Error("force realtime from tray failed", "status_code", response.StatusCode, "status", "error")
		return
	}
	t.logger.Info("force realtime requested from tray", "status", "ok")
}

func (t *trayApp) getJSON(client *http.Client, path string, target any) error {
	response, err := client.Get(t.apiURL(path))
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode >= 300 {
		return fmt.Errorf("GET %s returned HTTP %d", path, response.StatusCode)
	}
	return json.NewDecoder(response.Body).Decode(target)
}

func (t *trayApp) apiURL(path string) string {
	base := "http://127.0.0.1" + t.cfg.HTTPAddr
	if t.cfg.HTTPAddr == ":8080" {
		base = "http://127.0.0.1:8080"
	}
	return base + path
}

func (t *trayApp) openLogs() {
	path := filepath.Join(config.RuntimeRoot(), "logs")
	_ = os.MkdirAll(path, 0755)
	if err := openPath(path); err != nil {
		t.logger.Error("failed to open logs folder", "error", err, "status", "error")
	}
}

func openBrowser(url string) error {
	switch runtime.GOOS {
	case "windows":
		return hiddenCommand("rundll32", "url.dll,FileProtocolHandler", url).Start()
	case "darwin":
		return exec.Command("open", url).Start()
	default:
		return exec.Command("xdg-open", url).Start()
	}
}

func openPath(path string) error {
	switch runtime.GOOS {
	case "windows":
		return hiddenCommand("explorer", path).Start()
	case "darwin":
		return exec.Command("open", path).Start()
	default:
		return exec.Command("xdg-open", path).Start()
	}
}

func hiddenCommand(name string, args ...string) *exec.Cmd {
	cmd := exec.Command(name, args...)
	if runtime.GOOS == "windows" {
		cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	}
	return cmd
}

func newLogger() (*slog.Logger, func()) {
	root := config.RuntimeRoot()
	logsDir := filepath.Join(root, "logs")
	if err := os.MkdirAll(logsDir, 0755); err != nil {
		return slog.New(slog.NewTextHandler(io.Discard, nil)), func() {}
	}

	logPath := filepath.Join(logsDir, "delayengine.log")
	_ = logging.Rotate(logPath, logging.DefaultMaxBytes, logging.DefaultBackups)
	file, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return slog.New(slog.NewTextHandler(io.Discard, nil)), func() {}
	}

	logger := slog.New(slog.NewTextHandler(file, nil))
	return logger, func() {
		_ = file.Close()
	}
}

func trayIcon() []byte {
	return appIcon
}
