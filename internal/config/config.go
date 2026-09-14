// Package config loads and resolves Kamui's user configuration.
package config

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/narasaka/kamui/internal/mirror"
	"github.com/narasaka/kamui/internal/proxy"
	"github.com/narasaka/kamui/internal/session"
)

// Loader reads the optional configuration file at Path.
type Loader struct {
	Path string
}

// Overrides contains explicitly supplied command-line values. A nil pointer
// or slice means the corresponding flag was not supplied.
type Overrides struct {
	Browser           *string
	Loopback          *string
	OpenBrowserOnSSH  *bool
	IdleTimeout       *time.Duration
	StopBrowserOnStop *bool
	IncludePorts      []string
	ExcludePorts      []string
}

// Effective is the fully resolved configuration for one destination.
type Effective struct {
	Browser           string
	LoopbackMode      proxy.LoopbackMode
	OpenBrowserOnSSH  bool
	IdleTimeout       time.Duration
	StopBrowserOnStop bool
	PortPolicy        mirror.PortPolicy
}

type fileConfig struct {
	DefaultBrowser    string                `json:"defaultBrowser"`
	BrowserLoopback   string                `json:"browserLoopback"`
	Loopback          string                `json:"loopback"`
	OpenBrowserOnSSH  *bool                 `json:"openBrowserOnSSH"`
	IdleTimeout       string                `json:"idleTimeout"`
	StopBrowserOnStop *bool                 `json:"stopBrowserOnStop"`
	Ports             portConfig            `json:"ports"`
	Hosts             map[string]hostConfig `json:"hosts"`
}

type hostConfig struct {
	Browser           string     `json:"browser"`
	BrowserLoopback   string     `json:"browserLoopback"`
	Loopback          string     `json:"loopback"`
	OpenBrowserOnSSH  *bool      `json:"openBrowserOnSSH"`
	IdleTimeout       string     `json:"idleTimeout"`
	StopBrowserOnStop *bool      `json:"stopBrowserOnStop"`
	Ports             portConfig `json:"ports"`
}

type portConfig struct {
	Include []string `json:"include"`
	Exclude []string `json:"exclude"`
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
	var hostPorts portConfig
	globalLoopback, err := configuredBrowserLoopback(file.BrowserLoopback, file.Loopback, "global configuration")
	if err != nil {
		return Effective{}, err
	}
	if globalLoopback != "" {
		effective.LoopbackMode, err = proxy.ParseLoopbackMode(globalLoopback)
		if err != nil {
			return Effective{}, fmt.Errorf("parse global browserLoopback: %w", err)
		}
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
		hostPorts = host.Ports
		if host.Browser != "" {
			effective.Browser = host.Browser
		}
		hostLoopback, loopbackErr := configuredBrowserLoopback(host.BrowserLoopback, host.Loopback, "host "+destination.String())
		if loopbackErr != nil {
			return Effective{}, loopbackErr
		}
		if hostLoopback != "" {
			effective.LoopbackMode, err = proxy.ParseLoopbackMode(hostLoopback)
			if err != nil {
				return Effective{}, fmt.Errorf("parse browserLoopback for %s: %w", destination, err)
			}
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
	if overrides.Loopback != nil {
		effective.LoopbackMode, err = proxy.ParseLoopbackMode(*overrides.Loopback)
		if err != nil {
			return Effective{}, fmt.Errorf("parse loopback override: %w", err)
		}
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
	effective.PortPolicy, err = resolvePortPolicy(file.Ports, hostPorts, overrides)
	if err != nil {
		return Effective{}, err
	}

	return effective, nil
}

func resolvePortPolicy(global, host portConfig, overrides Overrides) (mirror.PortPolicy, error) {
	excluded := make([]bool, 1<<16)
	defaultPolicy := mirror.DefaultPortPolicy()
	for port := 1; port < len(excluded); port++ {
		excluded[port] = defaultPolicy.Excludes(uint16(port))
	}
	for _, rules := range []struct {
		location string
		ports    portConfig
	}{
		{location: "global ports", ports: global},
		{location: "host ports", ports: host},
		{location: "command-line ports", ports: portConfig{Include: overrides.IncludePorts, Exclude: overrides.ExcludePorts}},
	} {
		if err := applyPortRules(excluded, rules.ports, rules.location); err != nil {
			return mirror.PortPolicy{}, err
		}
	}
	return mirror.NewPortPolicy(excludedPortRanges(excluded)), nil
}

func applyPortRules(excluded []bool, configured portConfig, location string) error {
	for _, spec := range configured.Exclude {
		portRange, err := parsePortRange(spec)
		if err != nil {
			return fmt.Errorf("parse %s.exclude: %w", location, err)
		}
		for port := int(portRange.Start); port <= int(portRange.End); port++ {
			excluded[port] = true
		}
	}
	for _, spec := range configured.Include {
		portRange, err := parsePortRange(spec)
		if err != nil {
			return fmt.Errorf("parse %s.include: %w", location, err)
		}
		for port := int(portRange.Start); port <= int(portRange.End); port++ {
			excluded[port] = false
		}
	}
	return nil
}

func parsePortRange(spec string) (mirror.PortRange, error) {
	value := strings.TrimSpace(spec)
	startText, endText, hasRange := strings.Cut(value, "-")
	if !hasRange {
		endText = startText
	}
	if startText == "" || endText == "" || strings.Contains(endText, "-") {
		return mirror.PortRange{}, fmt.Errorf("invalid port or range %q", spec)
	}
	parsePort := func(text string) (uint16, error) {
		value, err := strconv.ParseUint(strings.TrimSpace(text), 10, 16)
		if err != nil || value == 0 {
			return 0, fmt.Errorf("invalid TCP port %q", strings.TrimSpace(text))
		}
		return uint16(value), nil
	}
	start, err := parsePort(startText)
	if err != nil {
		return mirror.PortRange{}, err
	}
	end, err := parsePort(endText)
	if err != nil {
		return mirror.PortRange{}, err
	}
	if start > end {
		return mirror.PortRange{}, fmt.Errorf("port range %q starts after it ends", spec)
	}
	return mirror.PortRange{Start: start, End: end}, nil
}

func excludedPortRanges(excluded []bool) []mirror.PortRange {
	var ranges []mirror.PortRange
	for start := 1; start < len(excluded); {
		if !excluded[start] {
			start++
			continue
		}
		end := start
		for end+1 < len(excluded) && excluded[end+1] {
			end++
		}
		ranges = append(ranges, mirror.PortRange{Start: uint16(start), End: uint16(end)})
		start = end + 1
	}
	return ranges
}

func configuredBrowserLoopback(current, legacy, location string) (string, error) {
	if current != "" && legacy != "" {
		return "", fmt.Errorf("%s sets both browserLoopback and deprecated loopback", location)
	}
	if current != "" {
		return current, nil
	}
	return legacy, nil
}
