package browser

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
)

// Catalog discovers installations and applies deterministic selection.
type Catalog struct {
	adapters []Adapter
}

// Selection contains each browser preference in descending precedence.
type Selection struct {
	Explicit string
	Family   string
	Host     string
	Global   string
	Previous string
}

// Selected couples an installation to the adapter that owns it.
type Selected struct {
	Adapter      Adapter
	Installation Installation
}

// NewCatalog creates a catalog in documented preference order.
func NewCatalog(adapters []Adapter) *Catalog {
	return &Catalog{adapters: append([]Adapter(nil), adapters...)}
}

// Detect lists supported installations in catalog preference order.
func (c *Catalog) Detect(ctx context.Context) ([]Installation, error) {
	var detected []Installation
	for _, adapter := range c.adapters {
		installations, err := adapter.Detect(ctx)
		if err != nil {
			return nil, err
		}
		detected = append(detected, installations...)
	}
	return detected, nil
}

// Select discovers supported browsers and returns the highest-precedence match.
func (c *Catalog) Select(ctx context.Context, selection Selection) (Selected, error) {
	explicit := strings.ToLower(strings.TrimSpace(selection.Explicit))
	base := strings.ToLower(filepath.Base(selection.Explicit))
	if explicit == "tor" || explicit == "tor-browser" || explicit == "tor browser" ||
		base == "tor-browser" || base == "tor browser" || strings.Contains(explicit, "tor browser.app") {
		return Selected{}, fmt.Errorf("changing Tor Browser routing is unsupported because it would violate its privacy guarantees")
	}
	if filepath.IsAbs(selection.Explicit) {
		return c.selectAbsolute(ctx, selection)
	}

	desired := firstNonempty(selection.Explicit, selection.Host, selection.Global, selection.Previous)
	for _, adapter := range c.adapters {
		if desired != "" && adapter.ID() != desired {
			continue
		}
		installations, err := adapter.Detect(ctx)
		if err != nil {
			return Selected{}, err
		}
		if len(installations) > 0 {
			return Selected{Adapter: adapter, Installation: installations[0]}, nil
		}
	}
	if desired != "" {
		return Selected{}, fmt.Errorf("configured browser %q was not found", desired)
	}
	return Selected{}, fmt.Errorf("no supported development browser was found")
}

func (c *Catalog) selectAbsolute(ctx context.Context, selection Selection) (Selected, error) {
	family := selection.Family
	if family == "" {
		family = familyFromExecutable(selection.Explicit)
	}
	if family == "" {
		return Selected{}, fmt.Errorf("browser family is ambiguous for %q; use --browser-family", selection.Explicit)
	}
	for _, adapter := range c.adapters {
		if adapter.ID() == family || adapterFamily(adapter.ID()) == family {
			return Selected{
				Adapter:      adapter,
				Installation: Installation{ID: adapter.ID(), Executable: selection.Explicit},
			}, nil
		}
	}
	return Selected{}, fmt.Errorf("browser family %q is not supported", family)
}

func adapterFamily(id string) string {
	switch id {
	case "chrome", "chromium", "arc", "brave", "edge":
		return "chromium"
	case "firefox", "firefox-developer-edition", "zen", "librewolf", "floorp":
		return "gecko"
	default:
		return ""
	}
}

func familyFromExecutable(path string) string {
	folded := strings.ToLower(path)
	switch {
	case strings.Contains(folded, "chrome"), strings.Contains(folded, "chromium"), strings.Contains(folded, "arc.app"), strings.Contains(folded, "brave"), strings.Contains(folded, "edge"):
		return "chromium"
	case strings.Contains(folded, "firefox"), strings.Contains(folded, "zen.app"), strings.Contains(folded, "librewolf"), strings.Contains(folded, "floorp"):
		return "gecko"
	default:
		return ""
	}
}

func firstNonempty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
