//go:build windows

package lsp

import "os/exec"

func configureProcess(_ *exec.Cmd) {}

func terminateProcess(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	_ = cmd.Process.Kill()
}
