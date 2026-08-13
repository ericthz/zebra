//go:build linux

// raw 模式（Linux）：与 darwin 语义一致，仅 ioctl 常量不同（TCGETS/TCSETS）。
package console

import (
	"syscall"
	"unsafe"
)

// makeRaw 把 fd 设为 raw 模式，返回恢复函数。
func makeRaw(fd int) (func(), error) {
	var old syscall.Termios
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), syscall.TCGETS, uintptr(unsafe.Pointer(&old))); errno != 0 {
		return nil, errno
	}
	raw := old
	raw.Iflag &^= syscall.ICRNL | syscall.INLCR | syscall.IGNCR
	raw.Lflag &^= syscall.ECHO | syscall.ICANON | syscall.IEXTEN | syscall.ISIG
	raw.Oflag &^= syscall.OPOST
	raw.Cc[syscall.VMIN] = 1
	raw.Cc[syscall.VTIME] = 0
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), syscall.TCSETS, uintptr(unsafe.Pointer(&raw))); errno != 0 {
		return nil, errno
	}
	return func() {
		_, _, _ = syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), syscall.TCSETS, uintptr(unsafe.Pointer(&old)))
	}, nil
}
