// Package config loads and resolves Kamui's user configuration.
package config

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/narasaka/kamui/internal/session"
)

// Loader reads the optional configuration file at Path.
type Loader struct {
	Path string
}

// Overrides contains explicitly supplied command-line values. Nil means the
// corresponding flag was not supplied.
type Overrides struct {
	Browser           *string
	OpenBrowserOnSSH  *bool
	IdleTimeout       *time.Duration
	StopBrowserOnStop *bool
}

// Effective is the fully resolved configuration for one destination.
type Effective struct {
	Browser           string
	OpenBrowserOnSSH  bool
	IdleTimeout       time.Duration
	StopBrowserOnStop bool
}

type fileConfig struct {
	DefaultBrowser    string                `json:"defaultBrowser"`
	OpenBrowserOnSSH  *bool                 `json:"openBrowserOnSSH"`
	IdleTimeout       string                `json:"idleTimeout"`
	StopBrowserOnStop *bool                 `json:"stopBrowserOnStop"`
	Hosts             map[string]hostConfig `json:"hosts"`
}

type hostConfig struct {
	Browser           string `json:"browser"`
	OpenBrowserOnSSH  *bool  `json:"openBrowserOnSSH"`
	IdleTimeout       string `json:"idleTimeout"`
	StopBrowserOnStop *bool  `json:"stopBrowserOnStop"`
}

// Resolve applies CLI, host, global, then built-in precedence.
func (l Loader) Resolve(ctx context.Context, destination session.Destination, overrides Overrides) (Effective, error) {
	if err := ctx.Err(); err != nil {
		return Effective{}, err
	}

	var file fileConfig
	contents, err := os.ReadFile(l.Path)
	if err != nil && !os.IsNotExist(err) {
		return Effective{}, fmt.Errorf("read configuration: %w", err)
	}
	if err == nil {
		decoder := json.NewDecoder(bytes.NewReader(contents))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&file); err != nil {
			return Effective{}, fmt.Errorf("decode configuration: %w", err)
		}
		if err := decoder.Decode(&struct{}{}); err != io.EOF {
			if err == nil {
				err = fmt.Errorf("multiple JSON values")
			}
			return Effective{}, fmt.Errorf("decode configuration: trailing content: %w", err)
		}
	}

	effective := Effective{
		Browser: file.DefaultBrowser,
	}
	if file.OpenBrowserOnSSH != nil {
		effective.OpenBrowserOnSSH = *file.OpenBrowserOnSSH
	}
	if file.StopBrowserOnStop != nil {
		effective.StopBrowserOnStop = *file.StopBrowserOnStop
	}
	if file.IdleTimeout != "" {
		effective.IdleTimeout, err = time.ParseDuration(file.IdleTimeout)
		if err != nil {
			return Effective{}, fmt.Errorf("parse global idleTimeout: %w", err)
		}
		if effective.IdleTimeout < 0 {
			return Effective{}, fmt.Errorf("global idleTimeout must not be negative")
		}
	}

	if host, ok := file.Hosts[destination.String()]; ok {
		if host.Browser != "" {
			effective.Browser = host.Browser
		}
		if host.OpenBrowserOnSSH != nil {
			effective.OpenBrowserOnSSH = *host.OpenBrowserOnSSH
		}
		if host.StopBrowserOnStop != nil {
			effective.StopBrowserOnStop = *host.StopBrowserOnStop
		}
		if host.IdleTimeout != "" {
			effective.IdleTimeout, err = time.ParseDuration(host.IdleTimeout)
			if err != nil {
				return Effective{}, fmt.Errorf("parse idleTimeout for %s: %w", destination, err)
			}
			if effective.IdleTimeout < 0 {
				return Effective{}, fmt.Errorf("idleTimeout for %s must not be negative", destination)
			}
		}
	}

	if overrides.Browser != nil {
		effective.Browser = *overrides.Browser
	}
	if overrides.OpenBrowserOnSSH != nil {
		effective.OpenBrowserOnSSH = *overrides.OpenBrowserOnSSH
	}
	if overrides.IdleTimeout != nil {
		if *overrides.IdleTimeout < 0 {
			return Effective{}, fmt.Errorf("idleTimeout override must not be negative")
		}
		effective.IdleTimeout = *overrides.IdleTimeout
	}
	if overrides.StopBrowserOnStop != nil {
		effective.StopBrowserOnStop = *overrides.StopBrowserOnStop
	}

	return effective, nil
}
