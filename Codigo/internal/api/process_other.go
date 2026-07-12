//go:build !windows

package api

import "os/exec"

func applyHiddenWindow(cmd *exec.Cmd) {
}
