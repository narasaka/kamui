package browser_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/narasaka/kamui/internal/browser"
)

func TestCatalogUsesExplicitSelectionBeforeConfiguredChoices(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	chromePath := executable(t, root, "chrome")
	firefoxPath := executable(t, root, "firefox")
	launcher := &recordingLauncher{}
	catalog := browser.NewCatalog([]browser.Adapter{
		browser.NewChromiumAdapter("chrome", []string{chromePath}, launcher),
		browser.NewChromiumAdapter("firefox", []string{firefoxPath}, launcher),
	})

	selected, err := catalog.Select(context.Background(), browser.Selection{
		Explicit: "firefox",
		Host:     "chrome",
		Global:   "chrome",
		Previous: "chrome",
	})
	if err != nil {
		t.Fatalf("Select returned error: %v", err)
	}
	if selected.Adapter.ID() != "firefox" || selected.Installation.Executable != firefoxPath {
		t.Fatalf("selected %q at %q, want firefox at %q", selected.Adapter.ID(), selected.Installation.Executable, firefoxPath)
	}
}

func TestCatalogExplicitlyRejectsTorBrowser(t *testing.T) {
	t.Parallel()

	_, err := browser.NewCatalog(nil).Select(context.Background(), browser.Selection{Explicit: "tor"})
	if err == nil || !strings.Contains(err.Error(), "privacy guarantees") {
		t.Fatalf("Select Tor error = %v, want specific privacy explanation", err)
	}
}

func TestDefaultChromiumAdaptersUseRequiredStableIdentifiers(t *testing.T) {
	t.Parallel()

	adapters := browser.DefaultChromiumAdapters(&recordingLauncher{})
	got := make([]string, 0, len(adapters))
	for _, adapter := range adapters {
		got = append(got, adapter.ID())
	}
	want := []string{"chrome", "chromium", "arc", "brave", "edge"}
	if len(got) != len(want) {
		t.Fatalf("adapter IDs = %v, want %v", got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("adapter IDs = %v, want %v", got, want)
		}
	}
}

func executable(t *testing.T, root, name string) string {
	t.Helper()
	path := filepath.Join(root, name)
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}
