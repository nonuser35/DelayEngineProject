package main

import (
	"bytes"
	"context"
	"encoding/binary"
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

func (t *trayApp) onReady() {
	if icon := trayIcon(); len(icon) > 0 {
		systray.SetIcon(icon)
	}
	systray.SetTitle("DelayEngine")
	systray.SetTooltip("DelayEngine rodando")

	openPanel := systray.AddMenuItem("Abrir painel", "Abre a interface web")
	syncLive := systray.AddMenuItem("Voltar ao vivo agora", "Descarta delay acumulado")
	openLogs := systray.AddMenuItem("Abrir pasta de logs", "Abre a pasta logs")
	systray.AddSeparator()
	quit := systray.AddMenuItem("Sair", "Fecha o DelayEngine")

	go func() {
		for {
			select {
			case <-openPanel.ClickedCh:
				t.openPanel()
			case <-syncLive.ClickedCh:
				t.forceRealtime()
			case <-openLogs.ClickedCh:
				t.openLogs()
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
}

func (t *trayApp) onExit() {
	t.cancel()
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

	cmd := exec.Command(path)
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

func (t *trayApp) forceRealtime() {
	url := "http://127.0.0.1" + t.cfg.HTTPAddr + "/live/sync"
	if t.cfg.HTTPAddr == ":8080" {
		url = "http://127.0.0.1:8080/live/sync"
	}
	request, err := http.NewRequest(http.MethodPost, url, nil)
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
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
	case "darwin":
		return exec.Command("open", url).Start()
	default:
		return exec.Command("xdg-open", url).Start()
	}
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
	const width = 16
	const height = 16
	headerSize := 6 + 16
	bitmapSize := 40 + width*height*4 + width*height/8
	buf := bytes.NewBuffer(make([]byte, 0, headerSize+bitmapSize))

	_ = binary.Write(buf, binary.LittleEndian, uint16(0))
	_ = binary.Write(buf, binary.LittleEndian, uint16(1))
	_ = binary.Write(buf, binary.LittleEndian, uint16(1))
	buf.WriteByte(width)
	buf.WriteByte(height)
	buf.WriteByte(0)
	buf.WriteByte(0)
	_ = binary.Write(buf, binary.LittleEndian, uint16(1))
	_ = binary.Write(buf, binary.LittleEndian, uint16(32))
	_ = binary.Write(buf, binary.LittleEndian, uint32(bitmapSize))
	_ = binary.Write(buf, binary.LittleEndian, uint32(headerSize))

	_ = binary.Write(buf, binary.LittleEndian, uint32(40))
	_ = binary.Write(buf, binary.LittleEndian, int32(width))
	_ = binary.Write(buf, binary.LittleEndian, int32(height*2))
	_ = binary.Write(buf, binary.LittleEndian, uint16(1))
	_ = binary.Write(buf, binary.LittleEndian, uint16(32))
	_ = binary.Write(buf, binary.LittleEndian, uint32(0))
	_ = binary.Write(buf, binary.LittleEndian, uint32(width*height*4))
	_ = binary.Write(buf, binary.LittleEndian, int32(0))
	_ = binary.Write(buf, binary.LittleEndian, int32(0))
	_ = binary.Write(buf, binary.LittleEndian, uint32(0))
	_ = binary.Write(buf, binary.LittleEndian, uint32(0))

	for y := height - 1; y >= 0; y-- {
		for x := 0; x < width; x++ {
			r, g, b, a := byte(45), byte(212), byte(191), byte(255)
			if x < 2 || y < 2 || x > 13 || y > 13 {
				r, g, b = 56, 189, 248
			}
			if x >= 5 && x <= 10 && y >= 5 && y <= 10 {
				r, g, b = 15, 23, 32
			}
			buf.Write([]byte{b, g, r, a})
		}
	}
	buf.Write(make([]byte, width*height/8))
	return buf.Bytes()
}
