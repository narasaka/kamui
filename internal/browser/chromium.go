package browser

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
)

type chromiumAdapter struct {
	adapterBase
}

// NewChromiumAdapter creates an adapter for one Chromium-family identifier.
func NewChromiumAdapter(id string, candidates []string, launcher Launcher) Adapter {
	return &chromiumAdapter{adapterBase: newAdapterBase(id, candidates, launcher)}
}

func (a *chromiumAdapter) PrepareProfile(ctx context.Context, session Session, installation Installation) (Profile, error) {
	path, err := a.prepareProfileDirectory(ctx, session)
	if err != nil {
		return Profile{}, err
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

func (a *chromiumAdapter) Close(ctx context.Context, profile Profile) error {
	stopper, ok := a.launcher.(ProfileStopper)
	if !ok {
		return nil
	}
	return stopper.Stop(ctx, profile.Installation.Executable, []string{"--user-data-dir=" + profile.Path})
}

type execLauncher struct {
	mu        sync.Mutex
	processes map[string]*browserProcess
}

type browserProcess struct {
	command *exec.Cmd
	done    chan error
}

func newExecLauncher() *execLauncher {
	return &execLauncher{processes: make(map[string]*browserProcess)}
}

func (l *execLauncher) Launch(_ context.Context, path string, args []string) error {
	command := exec.Command(path, args...)
	if err := command.Start(); err != nil {
		return err
	}
	process := &browserProcess{command: command, done: make(chan error, 1)}
	key := browserProcessKey(path, args)
	l.mu.Lock()
	if _, exists := l.processes[key]; !exists {
		l.processes[key] = process
	}
	l.mu.Unlock()
	go func() {
		err := command.Wait()
		process.done <- err
		close(process.done)
		l.mu.Lock()
		if l.processes[key] == process {
			delete(l.processes, key)
		}
		l.mu.Unlock()
	}()
	return nil
}

func (l *execLauncher) Stop(ctx context.Context, path string, args []string) error {
	key := browserProcessKey(path, args)
	l.mu.Lock()
	process := l.processes[key]
	l.mu.Unlock()
	if process == nil {
		return nil
	}
	if err := process.command.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return err
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-process.done:
		return nil
	}
}

func browserProcessKey(path string, args []string) string {
	for index, argument := range args {
		if strings.HasPrefix(argument, "--user-data-dir=") {
			return path + "\x00" + argument
		}
		if argument == "-profile" && index+1 < len(args) {
			return path + "\x00-profile=" + args[index+1]
		}
	}
	return path
}
