//go:build windows

package main

import (
	"log/slog"
	"runtime"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	wmHotkey uint32 = 0x0312
	wmQuit   uint32 = 0x0012
)

var (
	user32                = windows.NewLazySystemDLL("user32.dll")
	kernel32              = windows.NewLazySystemDLL("kernel32.dll")
	procRegisterHotKey    = user32.NewProc("RegisterHotKey")
	procUnregisterHotKey  = user32.NewProc("UnregisterHotKey")
	procGetMessageW       = user32.NewProc("GetMessageW")
	procPostThreadMessage = user32.NewProc("PostThreadMessageW")
	procGetCurrentThread  = kernel32.NewProc("GetCurrentThreadId")
)

type hotkeyAction struct {
	ID          int
	Modifiers   uint32
	Key         uint32
	Description string
	Run         func()
}

type hotkeyMessage struct {
	HWND    uintptr
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Point   struct {
		X int32
		Y int32
	}
}

func startGlobalHotkeys(logger *slog.Logger, actions []hotkeyAction) func() {
	if len(actions) == 0 {
		return nil
	}

	ready := make(chan uint32, 1)
	done := make(chan struct{})
	var once sync.Once

	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		defer close(done)

		threadID, _, _ := procGetCurrentThread.Call()
		ready <- uint32(threadID)

		registered := make(map[int]hotkeyAction, len(actions))
		for _, action := range actions {
			if action.Run == nil {
				continue
			}
			ok, _, err := procRegisterHotKey.Call(0, uintptr(action.ID), uintptr(action.Modifiers), uintptr(action.Key))
			if ok == 0 {
				logger.Warn("global hotkey not registered", "hotkey", action.Description, "error", err, "status", "warning")
				continue
			}
			registered[action.ID] = action
			logger.Info("global hotkey registered", "hotkey", action.Description, "status", "ok")
		}
		defer func() {
			for id, action := range registered {
				if ok, _, err := procUnregisterHotKey.Call(0, uintptr(id)); ok == 0 {
					logger.Warn("global hotkey unregister failed", "hotkey", action.Description, "error", err, "status", "warning")
				}
			}
		}()

		var msg hotkeyMessage
		for {
			ret, _, err := procGetMessageW.Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0)
			if int32(ret) <= 0 {
				if int32(ret) < 0 {
					logger.Warn("global hotkey message loop stopped with error", "error", err, "status", "warning")
				}
				return
			}
			if msg.Message != wmHotkey {
				continue
			}
			action, ok := registered[int(msg.WParam)]
			if !ok {
				continue
			}
			logger.Info("global hotkey pressed", "hotkey", action.Description, "status", "ok")
			go action.Run()
		}
	}()

	threadID := <-ready
	return func() {
		once.Do(func() {
			if threadID != 0 {
				procPostThreadMessage.Call(uintptr(threadID), uintptr(wmQuit), 0, 0)
			}
			<-done
		})
	}
}
