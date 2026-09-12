//go:build darwin

package browser

import (
	"os"
	"path/filepath"
)

// DefaultChromiumAdapters returns macOS Chromium-family adapters in
// deterministic preference order.
func DefaultChromiumAdapters(launcher Launcher) []Adapter {
	return []Adapter{
		NewChromiumAdapter("chrome", browserPaths("Google Chrome.app/Contents/MacOS/Google Chrome"), launcher),
		NewChromiumAdapter("chromium", browserPaths("Chromium.app/Contents/MacOS/Chromium"), launcher),
		NewChromiumAdapter("arc", browserPaths("Arc.app/Contents/MacOS/Arc"), launcher),
		NewChromiumAdapter("brave", browserPaths("Brave Browser.app/Contents/MacOS/Brave Browser"), launcher),
		NewChromiumAdapter("edge", browserPaths("Microsoft Edge.app/Contents/MacOS/Microsoft Edge"), launcher),
	}
}

// DefaultGeckoAdapters returns macOS Gecko-family adapters in deterministic
// preference order.
func DefaultGeckoAdapters(launcher Launcher) []Adapter {
	return []Adapter{
		NewGeckoAdapter("firefox", browserPaths("Firefox.app/Contents/MacOS/firefox"), launcher),
		NewGeckoAdapter("firefox-developer-edition", browserPaths("Firefox Developer Edition.app/Contents/MacOS/firefox"), launcher),
		NewGeckoAdapter("zen", browserPaths("Zen.app/Contents/MacOS/zen"), launcher),
		NewGeckoAdapter("librewolf", browserPaths("LibreWolf.app/Contents/MacOS/librewolf"), launcher),
		NewGeckoAdapter("floorp", browserPaths("Floorp.app/Contents/MacOS/floorp"), launcher),
	}
}

// DefaultAdapters returns every supported macOS browser adapter.
func DefaultAdapters(launcher Launcher) []Adapter {
	adapters := DefaultChromiumAdapters(launcher)
	return append(adapters, DefaultGeckoAdapters(launcher)...)
}

func browserPaths(applicationRelativePath string) []string {
	paths := []string{filepath.Join("/Applications", applicationRelativePath)}
	if home, err := os.UserHomeDir(); err == nil {
		paths = append(paths, filepath.Join(home, "Applications", applicationRelativePath))
	}
	return paths
}
