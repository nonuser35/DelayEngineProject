package app

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"path/filepath"
	"time"

	"delayengine/internal/api"
	"delayengine/internal/buffer"
	"delayengine/internal/config"
	"delayengine/internal/control"
	"delayengine/internal/delay"
	inputrtmp "delayengine/internal/input/rtmp"
	outputrtmp "delayengine/internal/output/rtmp"
)

// Engine wires DelayEngine modules together.
type Engine struct {
	cfg    config.Config
	logger *slog.Logger
}

func New(cfg config.Config, logger *slog.Logger) *Engine {
	if logger == nil {
		logger = slog.Default()
	}

	return &Engine{
		cfg:    cfg,
		logger: logger,
	}
}

func (e *Engine) Run(ctx context.Context) error {
	e.logger.Info(
		"delayengine starting",
		"input_url", e.cfg.InputURL,
		"output_url", config.RedactURLForLog(e.cfg.OutputURL),
		"http_addr", e.cfg.HTTPAddr,
		"max_buffer_duration", e.cfg.MaxBufferDuration,
		"fixed_delay", e.cfg.FixedDelay,
		"delay_enabled", e.cfg.DelayEnabled,
	)

	delayState := delay.NewState(e.cfg.DelayEnabled, e.cfg.FixedDelay)
	packetBuffer, err := buffer.NewDisk(buffer.DiskOptions{
		Directory:   filepath.Join(config.RuntimeRoot(), "runtime", "buffer"),
		MaxDuration: e.cfg.MaxBufferDuration,
	})
	if err != nil {
		return err
	}
	defer packetBuffer.Close()

	publisher := outputrtmp.NewPublisher(outputrtmp.PublisherConfig{
		URL:          e.cfg.OutputURL,
		WriteTimeout: e.cfg.WriteTimeout,
		Logger:       e.logger.With("module", "rtmp_output"),
	})
	if err := publisher.Connect(ctx); err != nil {
		e.logger.Warn("RTMP output unavailable; will retry while running", "error", err, "status", "waiting")
	}
	defer publisher.Close()

	reader := inputrtmp.NewReader(inputrtmp.ReaderConfig{
		URL:         e.cfg.InputURL,
		ReadTimeout: e.cfg.ReadTimeout,
		Logger:      e.logger.With("module", "rtmp_input"),
		Buffer:      packetBuffer,
		Publisher:   publisher,
		DelayState:  delayState,
	})

	controller := control.NewController(delayState, packetBuffer, reader, publisher)
	apiServer := api.NewServer(e.cfg.HTTPAddr, controller, e.logger.With("module", "api"))
	apiErr := make(chan error, 1)
	go func() {
		if err := apiServer.Start(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			apiErr <- err
		}
	}()
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := apiServer.Shutdown(shutdownCtx); err != nil {
			e.logger.Error("HTTP API shutdown failed", "error", err, "status", "error")
		}
	}()

	readerErr := make(chan error, 1)
	go func() {
		readerErr <- reader.Run(ctx)
	}()

	select {
	case err := <-apiErr:
		return err
	case err := <-readerErr:
		if err != nil {
			return err
		}
	case <-ctx.Done():
	}

	e.logger.Info("delayengine stopped", "status", "ok")
	return nil
}
