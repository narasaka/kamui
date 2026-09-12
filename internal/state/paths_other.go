//go:build !darwin

package state

import (
	"fmt"
	"os"
	"path/filepath"
)

// DefaultRoot returns a test/development state root on non-macOS systems.
func DefaultRoot() (string, error) {
	configRoot, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("find user configuration directory: %w", err)
	}
	return filepath.Join(configRoot, "kamui"), nil
}
