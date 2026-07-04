package main

import (
	"context"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"delayengine/internal/app"
	"delayengine/internal/config"
	"delayengine/internal/logging"
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

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	engine := app.New(cfg, logger)
	if err := engine.Run(ctx); err != nil {
		logger.Error("delayengine stopped with error", "error", err)
		os.Exit(1)
	}
}

func newLogger() (*slog.Logger, func()) {
	if err := os.MkdirAll("logs", 0755); err != nil {
		return slog.New(slog.NewTextHandler(os.Stdout, nil)), func() {}
	}

	logPath := filepath.Join("logs", "delayengine.log")
	_ = logging.Rotate(logPath, logging.DefaultMaxBytes, logging.DefaultBackups)
	file, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return slog.New(slog.NewTextHandler(os.Stdout, nil)), func() {}
	}

	writer := io.MultiWriter(os.Stdout, file)
	logger := slog.New(slog.NewTextHandler(writer, nil))
	return logger, func() {
		_ = file.Close()
	}
}
