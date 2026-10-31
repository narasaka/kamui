//go:build darwin || linux

package askpass

import (
	"os"
	"syscall"
	"testing"
	"time"
)

func TestRestoringEchoDiscardsPartiallyTypedSecret(t *testing.T) {
	t.Parallel()

	terminal, controller := openPseudoTerminal(t)
	restore, err := disableEcho(terminal)
	if err != nil {
		t.Fatal(err)
	}
	var hidden syscall.Termios
	if err := termiosIoctl(terminal.Fd(), ioctlGetTermios, &hidden); err != nil {
		t.Fatal(err)
	}
	if hidden.Lflag&syscall.ECHO != 0 {
		t.Fatal("echo is still enabled while a secret is typed")
	}
	// The user types part of a secret, then the prompt is cancelled.
	if _, err := controller.Write([]byte("partial-secret")); err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)
	restore()

	var restored syscall.Termios
	if err := termiosIoctl(terminal.Fd(), ioctlGetTermios, &restored); err != nil {
		t.Fatal(err)
	}
	if restored.Lflag&syscall.ECHO == 0 {
		t.Fatal("echo was not restored")
	}
	if _, err := controller.Write([]byte("next\n")); err != nil {
		t.Fatal(err)
	}
	lines := make(chan string, 1)
	go func() {
		buffer := make([]byte, 128)
		count, _ := terminal.Read(buffer)
		lines <- string(buffer[:count])
	}()
	select {
	case line := <-lines:
		if line != "next\n" {
			t.Fatalf("shell would read %q after the cancelled prompt, want only %q", line, "next\n")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("terminal produced no input")
	}
}

func openPseudoTerminal(t *testing.T) (terminal, controller *os.File) {
	t.Helper()
	controller, err := os.OpenFile("/dev/ptmx", os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		t.Skipf("pseudo-terminals are unavailable: %v", err)
	}
	t.Cleanup(func() { _ = controller.Close() })
	name, err := unlockPseudoTerminal(controller)
	if err != nil {
		t.Fatal(err)
	}
	terminal, err = os.OpenFile(name, os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = terminal.Close() })
	return terminal, controller
}

func ioctl(fd, request, argument uintptr) error {
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, fd, request, argument); errno != 0 {
		return errno
	}
	return nil
}
