//go:build linux

package cli

import "syscall"

const (
	ioctlReadTermios  = syscall.TCGETS
	ioctlWriteTermios = syscall.TCSETS
)
