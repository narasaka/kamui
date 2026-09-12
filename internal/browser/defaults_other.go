//go:build !darwin

package browser

import "os/exec"

// DefaultChromiumAdapters returns Unix Chromium-family adapters in
// deterministic preference order.
func DefaultChromiumAdapters(launcher Launcher) []Adapter {
	return []Adapter{
		NewChromiumAdapter("chrome", executablePaths("google-chrome", "google-chrome-stable"), launcher),
		NewChromiumAdapter("chromium", executablePaths("chromium", "chromium-browser"), launcher),
		NewChromiumAdapter("arc", nil, launcher),
		NewChromiumAdapter("brave", executablePaths("brave-browser", "brave-browser-stable"), launcher),
		NewChromiumAdapter("edge", executablePaths("microsoft-edge", "microsoft-edge-stable"), launcher),
	}
}

// DefaultGeckoAdapters returns Unix Gecko-family adapters in deterministic
// preference order.
func DefaultGeckoAdapters(launcher Launcher) []Adapter {
	return []Adapter{
		NewGeckoAdapter("firefox", executablePaths("firefox", "firefox-esr"), launcher),
		NewGeckoAdapter("firefox-developer-edition", executablePaths("firefox-developer-edition"), launcher),
		NewGeckoAdapter("zen", executablePaths("zen-browser", "zen"), launcher),
		NewGeckoAdapter("librewolf", executablePaths("librewolf"), launcher),
		NewGeckoAdapter("floorp", executablePaths("floorp"), launcher),
	}
}

// DefaultAdapters returns every supported Unix browser adapter.
func DefaultAdapters(launcher Launcher) []Adapter {
	adapters := DefaultChromiumAdapters(launcher)
	return append(adapters, DefaultGeckoAdapters(launcher)...)
}

func executablePaths(names ...string) []string {
	paths := make([]string, 0, len(names))
	seen := make(map[string]struct{}, len(names))
	for _, name := range names {
		path, err := exec.LookPath(name)
		if err != nil {
			continue
		}
		if _, exists := seen[path]; exists {
			continue
		}
		seen[path] = struct{}{}
		paths = append(paths, path)
	}
	return paths
}
