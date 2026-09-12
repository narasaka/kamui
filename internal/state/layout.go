// Package state owns Kamui's user-only filesystem layout.
package state

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/narasaka/kamui/internal/session"
)

// Layout is every persistent or runtime path Kamui owns.
type Layout struct {
	Root     string
	Config   string
	Socket   string
	Lock     string
	Token    string
	State    string
	Profiles string
	Logs     string
}

// NewLayout derives a complete layout from an application-support root.
func NewLayout(root string) Layout {
	stateRoot := filepath.Join(root, "state")
	return Layout{
		Root:     root,
		Config:   filepath.Join(root, "config.json"),
		Socket:   filepath.Join(root, "kamui.sock"),
		Lock:     filepath.Join(root, "controller.lock"),
		Token:    filepath.Join(stateRoot, "controller.token"),
		State:    stateRoot,
		Profiles: filepath.Join(root, "profiles"),
		Logs:     filepath.Join(root, "logs"),
	}
}

// Ensure creates all directories with user-only permissions.
func (l Layout) Ensure() error {
	for _, directory := range []string{l.Root, l.State, l.Profiles, l.Logs} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			return fmt.Errorf("create Kamui directory %s: %w", directory, err)
		}
		if err := os.Chmod(directory, 0o700); err != nil {
			return fmt.Errorf("protect Kamui directory %s: %w", directory, err)
		}
	}
	return nil
}

// SessionState returns a contained state-file path for an exact destination.
func (l Layout) SessionState(destination session.Destination) string {
	return filepath.Join(l.State, destination.Key()+".json")
}
