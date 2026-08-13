//go:build darwin

// raw 模式（macOS）：通过 termios 关闭 ICANON/ECHO，逐字节读入，
// 由 readLineRaw 自己做 UTF-8 感知的编辑与回显。
package console

import (
	"syscall"
	"unsafe"
)

// makeRaw 把 fd 设为 raw 模式，返回恢复函数。
func makeRaw(fd int) (func(), error) {
	var old syscall.Termios
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), syscall.TIOCGETA, uintptr(unsafe.Pointer(&old))); errno != 0 {
		return nil, errno
	}
	raw := old
	raw.Iflag &^= syscall.ICRNL | syscall.INLCR | syscall.IGNCR
	raw.Lflag &^= syscall.ECHO | syscall.ICANON | syscall.IEXTEN | syscall.ISIG
	raw.Oflag &^= syscall.OPOST
	raw.Cc[syscall.VMIN] = 1
	raw.Cc[syscall.VTIME] = 0
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), syscall.TIOCSETA, uintptr(unsafe.Pointer(&raw))); errno != 0 {
		return nil, errno
	}
	return func() {
		_, _, _ = syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), syscall.TIOCSETA, uintptr(unsafe.Pointer(&old)))
	}, nil
}
