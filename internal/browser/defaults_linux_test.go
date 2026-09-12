//go:build linux

package browser_test

import (
	"context"
	"testing"

	"github.com/narasaka/kamui/internal/browser"
)

func TestDefaultCatalogDiscoversNativeLinuxBrowsersFromPath(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"google-chrome", "chromium", "brave-browser", "microsoft-edge", "firefox"} {
		executable(t, root, name)
	}
	t.Setenv("PATH", root)

	installations, err := browser.NewCatalog(browser.DefaultAdapters(&recordingLauncher{})).Detect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	got := make([]string, 0, len(installations))
	for _, installation := range installations {
		got = append(got, installation.ID)
	}
	want := []string{"chrome", "chromium", "brave", "edge", "firefox"}
	if len(got) != len(want) {
		t.Fatalf("detected browser IDs = %v, want %v", got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("detected browser IDs = %v, want %v", got, want)
		}
	}
}

func TestDefaultCatalogTreatsFirefoxESRAsFirefox(t *testing.T) {
	root := t.TempDir()
	path := executable(t, root, "firefox-esr")
	t.Setenv("PATH", root)

	selected, err := browser.NewCatalog(browser.DefaultAdapters(&recordingLauncher{})).Select(context.Background(), browser.Selection{Explicit: "firefox"})
	if err != nil {
		t.Fatal(err)
	}
	if selected.Installation.ID != "firefox" || selected.Installation.Executable != path {
		t.Fatalf("selected = %#v, want Firefox ESR at %q", selected.Installation, path)
	}
}
