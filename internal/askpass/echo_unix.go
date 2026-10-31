//go:build darwin || linux

package askpass

import (
	"os"
	"syscall"
	"unsafe"
)

// disableEcho turns off terminal echo and returns a function that restores
// the previous terminal settings. Like OpenSSH's readpassphrase, restoring
// discards unread input, so part of a secret typed into a cancelled prompt
// never reaches the shell.
func disableEcho(tty *os.File) (func(), error) {
	var original syscall.Termios
	if err := termiosIoctl(tty.Fd(), ioctlGetTermios, &original); err != nil {
		return nil, err
	}
	hidden := original
	hidden.Lflag &^= syscall.ECHO
	hidden.Lflag |= syscall.ICANON | syscall.ISIG
	if err := termiosIoctl(tty.Fd(), ioctlSetTermios, &hidden); err != nil {
		return nil, err
	}
	return func() { _ = termiosIoctl(tty.Fd(), ioctlSetTermiosFlush, &original) }, nil
}

func termiosIoctl(fd uintptr, request uintptr, value *syscall.Termios) error {
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, fd, request, uintptr(unsafe.Pointer(value))); errno != 0 {
		return errno
	}
	return nil
}
