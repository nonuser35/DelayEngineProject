//go:build windows

package api

import (
	"os/exec"
	"syscall"
)

func applyHiddenWindow(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
}
