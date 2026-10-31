package askpass

import (
	"bytes"
	"os"
	"syscall"
	"unsafe"
)

func unlockPseudoTerminal(controller *os.File) (string, error) {
	if err := ioctl(controller.Fd(), syscall.TIOCPTYGRANT, 0); err != nil {
		return "", err
	}
	if err := ioctl(controller.Fd(), syscall.TIOCPTYUNLK, 0); err != nil {
		return "", err
	}
	var name [128]byte
	if err := ioctl(controller.Fd(), syscall.TIOCPTYGNAME, uintptr(unsafe.Pointer(&name[0]))); err != nil {
		return "", err
	}
	return string(name[:bytes.IndexByte(name[:], 0)]), nil
}
