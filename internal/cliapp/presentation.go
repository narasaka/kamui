package cliapp

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"
)

const (
	ansiGreen = "\x1b[32m"
	ansiRed   = "\x1b[31m"
	ansiReset = "\x1b[0m"
)

type terminalStyle struct {
	color bool
}

func isTerminalWriter(writer io.Writer) bool {
	file, ok := writer.(*os.File)
	if !ok {
		return false
	}
	info, err := file.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

func styleForWriter(writer io.Writer) terminalStyle {
	_, noColor := os.LookupEnv("NO_COLOR")
	if !isTerminalWriter(writer) || noColor || os.Getenv("TERM") == "dumb" {
		return terminalStyle{}
	}
	return terminalStyle{color: true}
}

type connectionProgress struct {
	writer   io.Writer
	visible  bool
	interval time.Duration
	done     chan struct{}
	wait     sync.WaitGroup
	err      chan error
}

var connectionFrames = [...]string{"◐", "◓", "◑", "◒"}

func newConnectionProgress(writer io.Writer) connectionProgress {
	return connectionProgress{
		writer:   writer,
		visible:  isTerminalWriter(writer) && os.Getenv("TERM") != "dumb",
		interval: 100 * time.Millisecond,
	}
}

func (p *connectionProgress) start(destination string) error {
	if !p.visible {
		return nil
	}
	if _, err := fmt.Fprintf(p.writer, "%s connecting to %s...\r", connectionFrames[0], destination); err != nil {
		return err
	}
	p.done = make(chan struct{})
	p.err = make(chan error, 1)
	p.wait.Add(1)
	go func() {
		defer p.wait.Done()
		interval := p.interval
		if interval <= 0 {
			interval = 100 * time.Millisecond
		}
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		frame := 1
		for {
			select {
			case <-p.done:
				return
			case <-ticker.C:
				if _, err := fmt.Fprintf(p.writer, "\r\x1b[2K%s connecting to %s...\r", connectionFrames[frame], destination); err != nil {
					p.err <- err
					return
				}
				frame = (frame + 1) % len(connectionFrames)
			}
		}
	}()
	return nil
}

func (p *connectionProgress) stop() error {
	if !p.visible {
		return nil
	}
	close(p.done)
	p.wait.Wait()
	var animationErr error
	select {
	case animationErr = <-p.err:
	default:
	}
	_, clearErr := io.WriteString(p.writer, "\r\x1b[2K")
	return errors.Join(animationErr, clearErr)
}

func (s terminalStyle) failure(text string) string {
	return s.colorize(ansiRed, text)
}

func (s terminalStyle) success(text string) string {
	return s.colorize(ansiGreen, text)
}

func (s terminalStyle) colorize(color, text string) string {
	if !s.color {
		return text
	}
	return color + text + ansiReset
}

func (s terminalStyle) statusSummary(text string) string {
	if !s.color {
		return text
	}
	return strings.NewReplacer(
		"● connected", s.success("● connected"),
		"✗ authentication-required", s.failure("✗ authentication-required"),
		"✗ unavailable", s.failure("✗ unavailable"),
	).Replace(text)
}

// PrintError writes a command error using Kamui's terminal presentation.
func PrintError(writer io.Writer, err error) {
	if err == nil {
		return
	}
	_, _ = fmt.Fprintln(writer, styleForWriter(writer).failure("✗ "+err.Error()))
}
