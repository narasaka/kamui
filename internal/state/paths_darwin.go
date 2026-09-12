//go:build darwin

package state

import (
	"fmt"
	"os"
	"path/filepath"
)

// DefaultRoot returns Kamui's macOS application-support directory.
func DefaultRoot() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("find home directory: %w", err)
	}
	return filepath.Join(home, "Library", "Application Support", "kamui"), nil
}
