//go:build linux

package state_test

import (
	"path/filepath"
	"testing"

	"github.com/narasaka/kamui/internal/state"
)

func TestDefaultLayoutUsesLinuxXDGDirectories(t *testing.T) {
	root := t.TempDir()
	configHome := filepath.Join(root, "config")
	stateHome := filepath.Join(root, "state")
	runtimeHome := filepath.Join(root, "runtime")
	t.Setenv("XDG_CONFIG_HOME", configHome)
	t.Setenv("XDG_STATE_HOME", stateHome)
	t.Setenv("XDG_RUNTIME_DIR", runtimeHome)

	layout, err := state.DefaultLayout()
	if err != nil {
		t.Fatal(err)
	}
	wants := map[string]string{
		"config":   filepath.Join(configHome, "kamui", "config.json"),
		"root":     filepath.Join(stateHome, "kamui"),
		"runtime":  filepath.Join(runtimeHome, "kamui"),
		"socket":   filepath.Join(runtimeHome, "kamui", "kamui.sock"),
		"profiles": filepath.Join(stateHome, "kamui", "profiles"),
	}
	gots := map[string]string{
		"config": layout.Config, "root": layout.Root, "runtime": layout.Runtime,
		"socket": layout.Socket, "profiles": layout.Profiles,
	}
	for name, want := range wants {
		if got := gots[name]; got != want {
			t.Errorf("%s path = %q, want %q", name, got, want)
		}
	}
	if err := layout.Ensure(); err != nil {
		t.Fatal(err)
	}
}

func TestDefaultLayoutIgnoresRelativeXDGRuntimeDirectory(t *testing.T) {
	root := t.TempDir()
	stateHome := filepath.Join(root, "state")
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "config"))
	t.Setenv("XDG_STATE_HOME", stateHome)
	t.Setenv("XDG_RUNTIME_DIR", "relative/runtime")

	layout, err := state.DefaultLayout()
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(stateHome, "kamui", "runtime")
	if layout.Runtime != want {
		t.Fatalf("runtime path = %q, want safe fallback %q", layout.Runtime, want)
	}
}
