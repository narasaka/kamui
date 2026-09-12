// Package state owns Kamui's user-only filesystem layout.
package state

import (
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"strings"

	"github.com/narasaka/kamui/internal/session"
)

// Layout is every persistent or runtime path Kamui owns.
type Layout struct {
	Root       string
	Config     string
	Socket     string
	Lock       string
	Token      string
	State      string
	Profiles   string
	Logs       string
	SSHLog     string
	ControlLog string
	FirstRun   string
}

// NewLayout derives a complete layout from an application-support root.
func NewLayout(root string) Layout {
	stateRoot := filepath.Join(root, "state")
	logsRoot := filepath.Join(root, "logs")
	return Layout{
		Root:       root,
		Config:     filepath.Join(root, "config.json"),
		Socket:     filepath.Join(root, "kamui.sock"),
		Lock:       filepath.Join(root, "controller.lock"),
		Token:      filepath.Join(stateRoot, "controller.token"),
		State:      stateRoot,
		Profiles:   filepath.Join(root, "profiles"),
		Logs:       logsRoot,
		SSHLog:     filepath.Join(logsRoot, "openssh.log"),
		ControlLog: filepath.Join(logsRoot, "controller.jsonl"),
		FirstRun:   filepath.Join(stateRoot, "security-warning-shown"),
	}
}

// TakeFirstRunWarning atomically returns true to exactly one successful caller.
func (l Layout) TakeFirstRunWarning() (bool, error) {
	if err := l.Ensure(); err != nil {
		return false, err
	}
	file, err := os.OpenFile(l.FirstRun, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if os.IsExist(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("record first-run warning: %w", err)
	}
	if _, err := file.WriteString("Remote content receives localhost origin treatment in Kamui profiles.\n"); err != nil {
		file.Close()
		return false, err
	}
	if err := file.Close(); err != nil {
		return false, err
	}
	return true, nil
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

// RememberBrowser atomically records the last successful stable browser ID for
// one exact destination.
func (l Layout) RememberBrowser(destination session.Destination, browserID string) error {
	if strings.TrimSpace(browserID) == "" || strings.ContainsAny(browserID, `/\\`) {
		return fmt.Errorf("invalid browser identifier %q", browserID)
	}
	if err := l.Ensure(); err != nil {
		return err
	}
	return writeUserFile(l.State, "browser-*", l.browserPreference(destination), browserID+"\n")
}

// PreviousBrowser reads the last successful browser ID, or returns an empty ID
// when the destination has no history.
func (l Layout) PreviousBrowser(destination session.Destination) (string, error) {
	contents, err := os.ReadFile(l.browserPreference(destination))
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(contents)), nil
}

func (l Layout) browserPreference(destination session.Destination) string {
	return filepath.Join(l.State, destination.Key()+".browser")
}

// RememberProxy records the ephemeral loopback address so a browser left open
// can keep using the same endpoint after a controller restart.
func (l Layout) RememberProxy(destination session.Destination, address netip.AddrPort) error {
	if address.Addr() != netip.MustParseAddr("127.0.0.1") || address.Port() == 0 {
		return fmt.Errorf("invalid persisted proxy address %s", address)
	}
	if err := l.Ensure(); err != nil {
		return err
	}
	return writeUserFile(l.State, "proxy-*", l.proxyPreference(destination), address.String()+"\n")
}

func writeUserFile(directory, pattern, target, contents string) error {
	temporary, err := os.CreateTemp(directory, pattern)
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return err
	}
	if _, err := temporary.WriteString(contents); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryPath, target)
}

// PreviousProxy returns the last loopback proxy address for a destination.
func (l Layout) PreviousProxy(destination session.Destination) (netip.AddrPort, error) {
	contents, err := os.ReadFile(l.proxyPreference(destination))
	if os.IsNotExist(err) {
		return netip.AddrPort{}, nil
	}
	if err != nil {
		return netip.AddrPort{}, err
	}
	address, err := netip.ParseAddrPort(strings.TrimSpace(string(contents)))
	if err != nil || address.Addr() != netip.MustParseAddr("127.0.0.1") || address.Port() == 0 {
		return netip.AddrPort{}, fmt.Errorf("invalid persisted proxy address")
	}
	return address, nil
}

func (l Layout) proxyPreference(destination session.Destination) string {
	return filepath.Join(l.State, destination.Key()+".proxy")
}
