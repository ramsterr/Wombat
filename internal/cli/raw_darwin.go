//go:build darwin

package cli

import "syscall"

const (
	ioctlReadTermios  = syscall.TIOCGETA
	ioctlWriteTermios = syscall.TIOCSETA
)
