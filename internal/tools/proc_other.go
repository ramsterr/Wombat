//go:build !unix

package tools

import "os/exec"

func prepareCommand(cmd *exec.Cmd) {}
