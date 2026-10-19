//go:build darwin || linux

package cli

import (
	"os"
	"syscall"
	"unsafe"
)

func makeRaw(f *os.File) (func(), error) {
	fd := f.Fd()
	var old syscall.Termios
	if _, _, errno := syscall.Syscall6(syscall.SYS_IOCTL, fd, ioctlReadTermios,
		uintptr(unsafe.Pointer(&old)), 0, 0, 0); errno != 0 {
		return nil, errno
	}
	raw := old
	raw.Lflag &^= syscall.ECHO | syscall.ICANON
	raw.Iflag &^= syscall.ICRNL
	raw.Cc[syscall.VMIN] = 1
	raw.Cc[syscall.VTIME] = 0
	if _, _, errno := syscall.Syscall6(syscall.SYS_IOCTL, fd, ioctlWriteTermios,
		uintptr(unsafe.Pointer(&raw)), 0, 0, 0); errno != 0 {
		return nil, errno
	}
	return func() {
		syscall.Syscall6(syscall.SYS_IOCTL, fd, ioctlWriteTermios,
			uintptr(unsafe.Pointer(&old)), 0, 0, 0)
	}, nil
}
