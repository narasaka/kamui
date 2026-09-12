package state_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/narasaka/kamui/internal/session"
	"github.com/narasaka/kamui/internal/state"
)

func TestLayoutCreatesUserOnlyPathsAndContainsDestinationState(t *testing.T) {
	t.Parallel()

	root := filepath.Join(t.TempDir(), "Application Support", "kamui")
	layout := state.NewLayout(root)
	if err := layout.Ensure(); err != nil {
		t.Fatalf("Ensure returned error: %v", err)
	}
	for _, path := range []string{layout.Root, layout.State, layout.Profiles} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm()&0o077 != 0 {
			t.Fatalf("%s permissions = %o, want user-only", path, info.Mode().Perm())
		}
	}
	destination, _ := session.ParseDestination("name@example.com:/unsafe-looking")
	path := layout.SessionState(destination)
	if filepath.Dir(path) != layout.State || strings.Contains(filepath.Base(path), "@") || strings.Contains(filepath.Base(path), "/") {
		t.Fatalf("session state path %q escaped or exposed destination", path)
	}
}

func TestLayoutRemembersSuccessfulBrowserPerExactDestination(t *testing.T) {
	t.Parallel()

	layout := state.NewLayout(t.TempDir())
	if err := layout.Ensure(); err != nil {
		t.Fatal(err)
	}
	destination, _ := session.ParseDestination("reyna")
	if err := layout.RememberBrowser(destination, "firefox"); err != nil {
		t.Fatal(err)
	}
	got, err := layout.PreviousBrowser(destination)
	if err != nil {
		t.Fatal(err)
	}
	if got != "firefox" {
		t.Fatalf("PreviousBrowser = %q, want firefox", got)
	}
}

func TestLayoutReturnsSecurityWarningOnlyOnFirstSuccessfulUse(t *testing.T) {
	t.Parallel()

	layout := state.NewLayout(t.TempDir())
	first, err := layout.TakeFirstRunWarning()
	if err != nil {
		t.Fatal(err)
	}
	second, err := layout.TakeFirstRunWarning()
	if err != nil {
		t.Fatal(err)
	}
	if !first || second {
		t.Fatalf("warning results = %v then %v, want true then false", first, second)
	}
}
