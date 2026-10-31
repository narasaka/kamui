package askpass

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
)

// ErrInterrupted reports that the user interrupted a prompt.
var ErrInterrupted = errors.New("authentication prompt interrupted")

// Terminal returns a Prompter that answers on this process's controlling
// terminal, or nil when the process has none.
func Terminal() Prompter {
	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return nil
	}
	_ = tty.Close()
	return promptTerminal
}

func promptTerminal(ctx context.Context, prompt Prompt) (string, error) {
	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return "", fmt.Errorf("open terminal for OpenSSH prompt: %w", err)
	}
	defer func() { _ = tty.Close() }()
	text := strings.TrimRight(prompt.Text, " ")
	if prompt.Kind == Notice {
		_, err := fmt.Fprintln(tty, text)
		return "", err
	}
	if prompt.Kind == Confirm {
		text += " (yes/no)"
	}
	// Catch signals before hiding input so none can leave echo disabled.
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, promptSignals...)
	defer signal.Stop(signals)
	if _, err := fmt.Fprint(tty, text+" "); err != nil {
		return "", err
	}
	restore, err := disableEcho(tty)
	if err != nil {
		return "", fmt.Errorf("hide OpenSSH prompt input: %w", err)
	}
	defer func() {
		restore()
		_, _ = fmt.Fprintln(tty)
	}()
	answer, err := readLine(ctx, tty, signals)
	if err != nil {
		return "", err
	}
	if prompt.Kind == Confirm && !strings.EqualFold(answer, "yes") {
		return "", ErrDeclined
	}
	return answer, nil
}

// promptSignals end a prompt, except SIGTSTP, which is ignored so that a
// suspended process never leaves the terminal without echo.
var promptSignals = []os.Signal{os.Interrupt, syscall.SIGTERM, syscall.SIGHUP, syscall.SIGQUIT, syscall.SIGTSTP}

// readLine reads one line and reports a terminating signal as an
// interruption, so callers restore the terminal before the process exits.
func readLine(ctx context.Context, tty *os.File, signals <-chan os.Signal) (string, error) {
	type result struct {
		line string
		err  error
	}
	lines := make(chan result, 1)
	go func() {
		line, err := bufio.NewReader(tty).ReadString('\n')
		lines <- result{line: line, err: err}
	}()
	for {
		select {
		case received := <-signals:
			if received == syscall.SIGTSTP {
				continue
			}
			return "", ErrInterrupted
		case <-ctx.Done():
			return "", ctx.Err()
		case read := <-lines:
			if read.err != nil && read.line == "" {
				return "", fmt.Errorf("read OpenSSH prompt answer: %w", read.err)
			}
			return strings.TrimRight(read.line, "\r\n"), nil
		}
	}
}
