//go:build !unix

package browser

import "os/exec"

func prepareBrowserCommand(cmd *exec.Cmd) {}

func killBrowserProcess(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	_ = cmd.Process.Kill()
}
