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
