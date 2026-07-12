//go:build !windows

package main

import "log/slog"

type hotkeyAction struct {
	ID          int
	Modifiers   uint32
	Key         uint32
	Description string
	Run         func()
}

func startGlobalHotkeys(logger *slog.Logger, actions []hotkeyAction) func() {
	if logger != nil {
		logger.Info("global hotkeys are only available on Windows", "status", "waiting")
	}
	return nil
}
