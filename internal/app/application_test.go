package app_test

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/narasaka/kamui/internal/app"
	"github.com/narasaka/kamui/internal/controller"
	"github.com/narasaka/kamui/internal/session"
	"github.com/narasaka/kamui/internal/ssh"
	"github.com/narasaka/kamui/internal/state"
	"github.com/narasaka/kamui/internal/testsupport"
)

func TestPrintSSHConfigReturnsSnippetWithoutStartingController(t *testing.T) {
	t.Parallel()

	layout := state.NewLayout(t.TempDir())
	starter := &failingStarter{t: t}
	application := app.New(layout, starter)
	result, err := application.Execute(context.Background(), app.Request{
		Operation: app.PrintSSHConfig, Destination: "reyna",
	})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	want := "Host reyna\n    PermitLocalCommand yes\n    LocalCommand kamui ssh-hook %n\n"
	if result.Output != want {
		t.Fatalf("snippet = %q, want %q", result.Output, want)
	}
}

type failingStarter struct{ t *testing.T }

func (s *failingStarter) Start(context.Context, state.Layout) error {
	s.t.Error("controller must not start for print-ssh-config")
	return nil
}

func TestApplicationStartsMissingControllerAndRetriesEnsure(t *testing.T) {
	t.Parallel()

	root, err := os.MkdirTemp("/tmp", "kamui-app-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	layout := state.NewLayout(root)
	launcher := &testsupport.SSHLauncher{}
	manager := session.NewManager(ssh.Transport{Launcher: launcher, ReadinessTimeout: time.Second})
	t.Cleanup(func() { _ = manager.Close() })
	ctx, cancel := context.WithCancel(context.Background())
	starter := &inProcessStarter{ctx: ctx, manager: manager}
	application := app.New(layout, starter)

	result, err := application.Execute(context.Background(), app.Request{Operation: app.Ensure, Destination: "reyna"})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if result.Session.State != session.SessionConnected || !result.Session.Proxy.Addr().IsLoopback() {
		t.Fatalf("session = %#v, want connected loopback proxy", result.Session)
	}
	if starter.count() != 1 || launcher.Starts() != 1 {
		t.Fatalf("controller starts=%d SSH starts=%d, want 1 each", starter.count(), launcher.Starts())
	}
	t.Cleanup(func() {
		cancel()
		starter.close()
	})
}

func TestSSHHookReturnsAfterControllerAcceptsSlowActivation(t *testing.T) {
	t.Parallel()

	root, err := os.MkdirTemp("/tmp", "kamui-hook-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	layout := state.NewLayout(root)
	launcher := &delayedLauncher{delay: 500 * time.Millisecond}
	manager := session.NewManager(ssh.Transport{Launcher: launcher, ReadinessTimeout: time.Second})
	t.Cleanup(func() { _ = manager.Close() })
	ctx, cancel := context.WithCancel(context.Background())
	starter := &inProcessStarter{ctx: ctx, manager: manager}
	application := app.New(layout, starter)

	started := time.Now()
	if _, err := application.Execute(context.Background(), app.Request{Operation: app.SSHHook, Destination: "reyna"}); err != nil {
		t.Fatalf("SSH hook: %v", err)
	}
	if elapsed := time.Since(started); elapsed >= 200*time.Millisecond {
		t.Fatalf("SSH hook took %v, want prompt acceptance", elapsed)
	}
	t.Cleanup(func() { cancel(); starter.close() })
}

type delayedLauncher struct {
	testsupport.SSHLauncher
	delay time.Duration
}

func (l *delayedLauncher) Start(request ssh.StartRequest) (ssh.Process, error) {
	time.Sleep(l.delay)
	return l.SSHLauncher.Start(request)
}

type inProcessStarter struct {
	mu      sync.Mutex
	ctx     context.Context
	manager *session.Manager
	server  *controller.Server
	starts  int
}

func (s *inProcessStarter) Start(_ context.Context, layout state.Layout) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	server, err := controller.Start(s.ctx, layout, s.manager)
	if err != nil {
		return err
	}
	s.server = server
	s.starts++
	return nil
}

func (s *inProcessStarter) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.starts
}

func (s *inProcessStarter) close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.server != nil {
		_ = s.server.Close()
	}
}
