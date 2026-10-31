package askpass

import (
	"fmt"
	"os"
	"syscall"
	"unsafe"
)

func unlockPseudoTerminal(controller *os.File) (string, error) {
	var unlock int32
	if err := ioctl(controller.Fd(), syscall.TIOCSPTLCK, uintptr(unsafe.Pointer(&unlock))); err != nil {
		return "", err
	}
	var number uint32
	if err := ioctl(controller.Fd(), syscall.TIOCGPTN, uintptr(unsafe.Pointer(&number))); err != nil {
		return "", err
	}
	return fmt.Sprintf("/dev/pts/%d", number), nil
}
