package browser

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

type chromiumAdapter struct {
	id         string
	candidates []string
	launcher   Launcher
}

// DefaultChromiumAdapters returns Chromium-family adapters in deterministic
// preference order.
func DefaultChromiumAdapters(launcher Launcher) []Adapter {
	return []Adapter{
		NewChromiumAdapter("chrome", browserPaths("Google Chrome.app/Contents/MacOS/Google Chrome"), launcher),
		NewChromiumAdapter("chromium", browserPaths("Chromium.app/Contents/MacOS/Chromium"), launcher),
		NewChromiumAdapter("arc", browserPaths("Arc.app/Contents/MacOS/Arc"), launcher),
		NewChromiumAdapter("brave", browserPaths("Brave Browser.app/Contents/MacOS/Brave Browser"), launcher),
		NewChromiumAdapter("edge", browserPaths("Microsoft Edge.app/Contents/MacOS/Microsoft Edge"), launcher),
	}
}

// NewChromiumAdapter creates an adapter for one Chromium-family identifier.
func NewChromiumAdapter(id string, candidates []string, launcher Launcher) Adapter {
	if launcher == nil {
		launcher = execLauncher{}
	}
	return &chromiumAdapter{id: id, candidates: append([]string(nil), candidates...), launcher: launcher}
}

func (a *chromiumAdapter) ID() string { return a.id }

func (a *chromiumAdapter) Detect(ctx context.Context) ([]Installation, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	installations := make([]Installation, 0, len(a.candidates))
	for _, candidate := range a.candidates {
		info, err := os.Stat(candidate)
		if err == nil && !info.IsDir() && info.Mode()&0o111 != 0 {
			installations = append(installations, Installation{ID: a.id, Executable: candidate})
		}
		if err != nil && !os.IsNotExist(err) {
			return nil, fmt.Errorf("inspect %s executable: %w", a.id, err)
		}
	}
	return installations, nil
}

func (a *chromiumAdapter) PrepareProfile(ctx context.Context, session Session, installation Installation) (Profile, error) {
	if err := ctx.Err(); err != nil {
		return Profile{}, err
	}
	path := filepath.Join(session.ProfileRoot, session.Key, a.id)
	if err := os.MkdirAll(path, 0o700); err != nil {
		return Profile{}, fmt.Errorf("create %s profile: %w", a.id, err)
	}
	if err := os.Chmod(path, 0o700); err != nil {
		return Profile{}, fmt.Errorf("protect %s profile: %w", a.id, err)
	}
	return Profile{Path: path, Session: session, Installation: installation}, nil
}

func (a *chromiumAdapter) Launch(ctx context.Context, profile Profile, urls []string) error {
	args := []string{
		"--user-data-dir=" + profile.Path,
		"--proxy-server=http://" + profile.Session.Proxy.String(),
		"--proxy-bypass-list=<-loopback>",
		"--no-first-run",
	}
	args = append(args, urls...)
	if err := a.launcher.Launch(ctx, profile.Installation.Executable, args); err != nil {
		return fmt.Errorf("launch %s: %w", a.id, err)
	}
	return nil
}

func (a *chromiumAdapter) Open(ctx context.Context, profile Profile, urls []string) error {
	return a.Launch(ctx, profile, urls)
}

type execLauncher struct{}

func (execLauncher) Launch(_ context.Context, path string, args []string) error {
	command := exec.Command(path, args...)
	if err := command.Start(); err != nil {
		return err
	}
	return command.Process.Release()
}

func browserPaths(applicationRelativePath string) []string {
	paths := []string{filepath.Join("/Applications", applicationRelativePath)}
	if home, err := os.UserHomeDir(); err == nil {
		paths = append(paths, filepath.Join(home, "Applications", applicationRelativePath))
	}
	return paths
}
