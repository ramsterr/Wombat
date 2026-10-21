//go:build !unix

package tools

import "os/exec"

func prepareCommand(cmd *exec.Cmd) {}

func terminate(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	return cmd.Process.Kill()
}
