package config_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/narasaka/kamui/internal/config"
	"github.com/narasaka/kamui/internal/session"
)

func TestLoaderResolvesCLIHostGlobalAndDefaultPrecedence(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "config.json")
	contents := `{
  "defaultBrowser": "chrome",
  "openBrowserOnSSH": true,
  "hosts": {
    "reyna": {
      "browser": "firefox",
      "idleTimeout": "30m"
    }
  }
}`
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	destination, err := session.ParseDestination("reyna")
	if err != nil {
		t.Fatal(err)
	}
	overrideBrowser := "arc"
	overrideOpen := false

	got, err := (config.Loader{Path: path}).Resolve(context.Background(), destination, config.Overrides{
		Browser:          &overrideBrowser,
		OpenBrowserOnSSH: &overrideOpen,
	})
	if err != nil {
		t.Fatalf("Resolve returned error: %v", err)
	}
	if got.Browser != "arc" {
		t.Errorf("Browser = %q, want CLI override %q", got.Browser, "arc")
	}
	if got.OpenBrowserOnSSH {
		t.Error("OpenBrowserOnSSH = true, want CLI override false")
	}
	if got.IdleTimeout != 30*time.Minute {
		t.Errorf("IdleTimeout = %v, want host value %v", got.IdleTimeout, 30*time.Minute)
	}
	if got.StopBrowserOnStop {
		t.Error("StopBrowserOnStop = true, want built-in default false")
	}
}

func TestLoaderRejectsUnknownConfigurationFields(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"defaultBrowzer":"firefox"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	destination, err := session.ParseDestination("reyna")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := (config.Loader{Path: path}).Resolve(context.Background(), destination, config.Overrides{}); err == nil {
		t.Fatal("Resolve succeeded with an unknown field, want an error")
	}
}

func TestLoaderRejectsTrailingJSONAndNegativeIdleTimeouts(t *testing.T) {
	t.Parallel()

	destination, _ := session.ParseDestination("reyna")
	for name, contents := range map[string]string{
		"trailing document":       `{} {}`,
		"negative global timeout": `{"idleTimeout":"-1s"}`,
		"negative host timeout":   `{"hosts":{"reyna":{"idleTimeout":"-1s"}}}`,
	} {
		name, contents := name, contents
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(t.TempDir(), "config.json")
			if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := (config.Loader{Path: path}).Resolve(context.Background(), destination, config.Overrides{}); err == nil {
				t.Fatalf("Resolve accepted %s", contents)
			}
		})
	}
}
