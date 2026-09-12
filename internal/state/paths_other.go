//go:build !darwin

package state

import (
	"fmt"
	"os"
	"path/filepath"
)

// DefaultRoot returns the persistent state root on non-macOS systems.
func DefaultRoot() (string, error) {
	stateRoot, err := userStateDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(stateRoot, "kamui"), nil
}

// DefaultLayout returns an XDG-aware filesystem layout on Unix-like systems.
func DefaultLayout() (Layout, error) {
	configRoot, err := os.UserConfigDir()
	if err != nil {
		return Layout{}, fmt.Errorf("find user configuration directory: %w", err)
	}
	root, err := DefaultRoot()
	if err != nil {
		return Layout{}, err
	}
	runtimeRoot := os.Getenv("XDG_RUNTIME_DIR")
	if !filepath.IsAbs(runtimeRoot) {
		runtimeRoot = filepath.Join(root, "runtime")
	} else {
		runtimeRoot = filepath.Join(runtimeRoot, "kamui")
	}
	layout := NewLayoutWithRuntime(root, runtimeRoot)
	layout.Config = filepath.Join(configRoot, "kamui", "config.json")
	return layout, nil
}

func userStateDir() (string, error) {
	if root := os.Getenv("XDG_STATE_HOME"); filepath.IsAbs(root) {
		return root, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("find home directory: %w", err)
	}
	return filepath.Join(home, ".local", "state"), nil
}
