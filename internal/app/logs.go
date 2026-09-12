package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"time"
)

const logPollInterval = 200 * time.Millisecond

// StreamLogs writes the most recent OpenSSH diagnostics and optionally follows
// additions until the context is cancelled. A lines value of zero means all
// existing diagnostics.
func (a *Application) StreamLogs(ctx context.Context, writer io.Writer, follow bool, lines int) error {
	if lines < 0 {
		return fmt.Errorf("log line count cannot be negative")
	}
	if writer == nil {
		writer = io.Discard
	}
	if follow {
		if err := a.layout.Ensure(); err != nil {
			return err
		}
	}

	flags := os.O_RDONLY
	if follow {
		flags |= os.O_CREATE
	}
	file, err := os.OpenFile(a.layout.SSHLog, flags, 0o600)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("open OpenSSH log: %w", err)
	}
	defer func() { _ = file.Close() }()

	offset, err := lastLinesOffset(file, lines)
	if err != nil {
		return fmt.Errorf("read OpenSSH log: %w", err)
	}
	if _, err := file.Seek(offset, io.SeekStart); err != nil {
		return fmt.Errorf("seek OpenSSH log: %w", err)
	}
	if _, err := io.Copy(writer, file); err != nil {
		return fmt.Errorf("write OpenSSH log: %w", err)
	}
	if !follow {
		return nil
	}

	ticker := time.NewTicker(logPollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			position, err := file.Seek(0, io.SeekCurrent)
			if err != nil {
				return fmt.Errorf("inspect OpenSSH log: %w", err)
			}
			info, err := file.Stat()
			if err != nil {
				return fmt.Errorf("inspect OpenSSH log: %w", err)
			}
			if info.Size() < position {
				if _, err := file.Seek(0, io.SeekStart); err != nil {
					return fmt.Errorf("seek truncated OpenSSH log: %w", err)
				}
			}
			if _, err := io.Copy(writer, file); err != nil {
				return fmt.Errorf("write OpenSSH log: %w", err)
			}
		}
	}
}

func lastLinesOffset(file *os.File, lines int) (int64, error) {
	if lines == 0 {
		return 0, nil
	}
	info, err := file.Stat()
	if err != nil {
		return 0, err
	}
	position := info.Size()
	if position == 0 {
		return 0, nil
	}

	const blockSize = 32 * 1024
	buffer := make([]byte, blockSize)
	remainingLines := lines
	ignoreTrailingNewline := true
	for position > 0 {
		start := max(int64(0), position-blockSize)
		length := position - start
		if _, err := file.ReadAt(buffer[:length], start); err != nil && !errors.Is(err, io.EOF) {
			return 0, err
		}
		for index := int(length) - 1; index >= 0; index-- {
			if buffer[index] != '\n' {
				ignoreTrailingNewline = false
				continue
			}
			if ignoreTrailingNewline {
				ignoreTrailingNewline = false
				continue
			}
			remainingLines--
			if remainingLines == 0 {
				return start + int64(index) + 1, nil
			}
		}
		position = start
	}
	return 0, nil
}
