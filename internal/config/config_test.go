package config_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/narasaka/kamui/internal/config"
	"github.com/narasaka/kamui/internal/proxy"
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

func TestLoaderDefaultsToExcludingSystemPorts(t *testing.T) {
	t.Parallel()

	destination, err := session.ParseDestination("reyna")
	if err != nil {
		t.Fatal(err)
	}
	effective, err := (config.Loader{Path: filepath.Join(t.TempDir(), "missing.json")}).Resolve(
		context.Background(), destination, config.Overrides{},
	)
	if err != nil {
		t.Fatal(err)
	}
	for _, port := range []uint16{1, 22, 80, 443, 1023} {
		if !effective.PortPolicy.Excludes(port) {
			t.Errorf("port %d is included, want excluded", port)
		}
	}
	for _, port := range []uint16{1024, 3000, 65535} {
		if effective.PortPolicy.Excludes(port) {
			t.Errorf("port %d is excluded, want included", port)
		}
	}
}

func TestLoaderResolvesPortRulesByScope(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "config.json")
	contents := `{
  "ports": {
    "exclude": ["5432-5433"],
    "include": ["80"]
  },
  "hosts": {
    "reyna": {
      "ports": {
        "exclude": ["80", "3000-3001"],
        "include": ["443-444"]
      }
    }
  }
}`
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	destination, _ := session.ParseDestination("reyna")

	effective, err := (config.Loader{Path: path}).Resolve(context.Background(), destination, config.Overrides{
		IncludePorts: []string{"3000-3001", "4001"},
		ExcludePorts: []string{"4000-4001"},
	})
	if err != nil {
		t.Fatal(err)
	}
	wantExcluded := map[uint16]bool{
		22: true, 80: true, 443: false, 444: false, 1024: false, 3000: false, 3001: false,
		4000: true, 4001: false, 5432: true, 5433: true,
	}
	for port, want := range wantExcluded {
		if got := effective.PortPolicy.Excludes(port); got != want {
			t.Errorf("port %d excluded = %t, want %t", port, got, want)
		}
	}
}

func TestLoaderRejectsInvalidPortRules(t *testing.T) {
	t.Parallel()

	destination, _ := session.ParseDestination("reyna")
	for name, contents := range map[string]string{
		"zero":                 `{"ports":{"exclude":["0"]}}`,
		"above TCP range":      `{"ports":{"exclude":["65536"]}}`,
		"reversed range":       `{"ports":{"exclude":["443-80"]}}`,
		"multiple separators":  `{"ports":{"exclude":["80-81-82"]}}`,
		"non-numeric":          `{"ports":{"include":["https"]}}`,
		"numeric JSON value":   `{"ports":{"include":[443]}}`,
		"unknown nested field": `{"ports":{"ignored":["22"]}}`,
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

func TestLoaderResolvesGlobalLoopbackMode(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"browserLoopback":"local-first"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	destination, _ := session.ParseDestination("reyna")

	effective, err := (config.Loader{Path: path}).Resolve(context.Background(), destination, config.Overrides{})
	if err != nil {
		t.Fatal(err)
	}
	if effective.LoopbackMode != proxy.LocalFirst {
		t.Fatalf("LoopbackMode = %v, want local-first", effective.LoopbackMode)
	}
}

func TestLoaderResolvesHostLoopbackModeOverGlobal(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "config.json")
	contents := `{"loopback":"local-first","hosts":{"reyna":{"loopback":"remote-only"}}}`
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	destination, _ := session.ParseDestination("reyna")

	effective, err := (config.Loader{Path: path}).Resolve(context.Background(), destination, config.Overrides{})
	if err != nil {
		t.Fatal(err)
	}
	if effective.LoopbackMode != proxy.RemoteOnly {
		t.Fatalf("LoopbackMode = %v, want host remote-only", effective.LoopbackMode)
	}
}

func TestLoaderResolvesLoopbackOverrideOverHost(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "config.json")
	contents := `{"hosts":{"reyna":{"loopback":"remote-only"}}}`
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	destination, _ := session.ParseDestination("reyna")
	override := "local-first"

	effective, err := (config.Loader{Path: path}).Resolve(context.Background(), destination, config.Overrides{Loopback: &override})
	if err != nil {
		t.Fatal(err)
	}
	if effective.LoopbackMode != proxy.LocalFirst {
		t.Fatalf("LoopbackMode = %v, want CLI local-first", effective.LoopbackMode)
	}
}

func TestLoaderRejectsTrailingJSONAndNegativeIdleTimeouts(t *testing.T) {
	t.Parallel()

	destination, _ := session.ParseDestination("reyna")
	for name, contents := range map[string]string{
		"trailing document":       `{} {}`,
		"negative global timeout": `{"idleTimeout":"-1s"}`,
		"negative host timeout":   `{"hosts":{"reyna":{"idleTimeout":"-1s"}}}`,
		"invalid global loopback": `{"loopback":"sometimes-local"}`,
		"invalid host loopback":   `{"hosts":{"reyna":{"loopback":"sometimes-local"}}}`,
		"both global spellings":   `{"browserLoopback":"remote-only","loopback":"local-first"}`,
		"both host spellings":     `{"hosts":{"reyna":{"browserLoopback":"remote-only","loopback":"local-first"}}}`,
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
