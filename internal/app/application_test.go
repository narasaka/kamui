package app_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/narasaka/kamui/internal/app"
	"github.com/narasaka/kamui/internal/controller"
	"github.com/narasaka/kamui/internal/session"
	"github.com/narasaka/kamui/internal/ssh"
	"github.com/narasaka/kamui/internal/state"
	"github.com/narasaka/kamui/internal/testsupport"
	"github.com/narasaka/kamui/internal/version"
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
	want := "# %n preserves the original SSH alias supplied to OpenSSH.\nHost reyna\n    PermitLocalCommand yes\n    LocalCommand kamui ssh-hook %n\n"
	if result.Output != want {
		t.Fatalf("snippet = %q, want %q", result.Output, want)
	}
}

func TestStreamLogsFollowsOpenSSHDiagnosticsUntilCancelled(t *testing.T) {
	t.Parallel()

	layout := state.NewLayout(t.TempDir())
	if err := layout.Ensure(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(layout.SSHLog, []byte("existing\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	application := app.New(layout, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var output lockedBuffer
	done := make(chan error, 1)
	go func() {
		done <- application.StreamLogs(ctx, &output, true, 100)
	}()

	deadline := time.Now().Add(2 * time.Second)
	for !strings.Contains(output.String(), "existing\n") {
		if time.Now().After(deadline) {
			t.Fatalf("initial log output = %q, want existing line", output.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
	file, err := os.OpenFile(layout.SSHLog, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString("new channel error\n"); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	for !strings.Contains(output.String(), "new channel error\n") {
		if time.Now().After(deadline) {
			t.Fatalf("followed log output = %q, want appended line", output.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("StreamLogs returned error: %v", err)
	}
}

type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (b *lockedBuffer) Write(value []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.Write(value)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.String()
}

func TestDoctorReportsAllLocalAndRemoteChecksWithoutStartingController(t *testing.T) {
	t.Parallel()

	application := app.New(state.NewLayout(t.TempDir()), &failingStarter{t: t})
	result, err := application.Execute(context.Background(), app.Request{Operation: app.Doctor, Destination: "localhost"})
	if err != nil {
		t.Fatalf("doctor returned error: %v", err)
	}
	for _, check := range []string{"SSH executable", "SSH connectivity", "browser discovery", "state directory", "port allocation", "loopback proxy"} {
		if !strings.Contains(result.Output, check) {
			t.Errorf("doctor output missing %q:\n%s", check, result.Output)
		}
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

func TestApplicationRestartsStaleControllerAndRetriesEnsure(t *testing.T) {
	t.Parallel()

	root, err := os.MkdirTemp("/tmp", "kamui-app-upgrade-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	layout := state.NewLayout(root)
	launcher := &testsupport.SSHLauncher{}
	staleManager := session.NewManager(ssh.Transport{Launcher: launcher, ReadinessTimeout: time.Second})
	t.Cleanup(func() { _ = staleManager.Close() })
	ctx, cancel := context.WithCancel(context.Background())
	stale, err := controller.StartWithIdentity(ctx, layout, staleManager, controller.Identity{
		Protocol: controller.ProtocolVersion, Build: "older-installed-build",
	})
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	replacementManager := session.NewManager(ssh.Transport{Launcher: launcher, ReadinessTimeout: time.Second})
	t.Cleanup(func() { _ = replacementManager.Close() })
	starter := &inProcessStarter{ctx: ctx, manager: replacementManager}
	application := app.New(layout, starter)

	result, err := application.Execute(context.Background(), app.Request{Operation: app.Ensure, Destination: "reyna"})
	if err != nil {
		cancel()
		t.Fatalf("Execute returned error: %v", err)
	}
	if result.Session.State != session.SessionConnected {
		t.Fatalf("session state = %v, want connected", result.Session.State)
	}
	if starter.count() != 1 {
		t.Fatalf("replacement controller starts = %d, want 1", starter.count())
	}
	identity, err := (controller.Client{Layout: layout}).Identity(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if identity.Build != version.BuildIdentity() {
		t.Fatalf("replacement build = %q, want %q", identity.Build, version.BuildIdentity())
	}
	logContents, err := os.ReadFile(layout.ControlLog)
	if err != nil || !strings.Contains(string(logContents), "controller_upgrade_restart") {
		t.Fatalf("controller log = %q, error=%v; want upgrade diagnostic", logContents, err)
	}
	select {
	case <-stale.Done():
	default:
		t.Fatal("stale controller is still running")
	}
	t.Cleanup(func() { cancel(); starter.close() })
}

func TestApplicationDoesNotRestartCurrentController(t *testing.T) {
	t.Parallel()

	root, err := os.MkdirTemp("/tmp", "kamui-app-current-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	layout := state.NewLayout(root)
	manager := session.NewManager(ssh.Transport{Launcher: &testsupport.SSHLauncher{}, ReadinessTimeout: time.Second})
	t.Cleanup(func() { _ = manager.Close() })
	server, err := controller.Start(context.Background(), layout, manager)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Close() })
	starter := &inProcessStarter{ctx: context.Background(), manager: manager}

	result, err := app.New(layout, starter).Execute(context.Background(), app.Request{Operation: app.Ensure, Destination: "reyna"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Session.State != session.SessionConnected || starter.count() != 0 {
		t.Fatalf("state=%v replacement starts=%d, want connected without restart", result.Session.State, starter.count())
	}
}

func TestConcurrentUpgradeDetectionStartsOneReplacementController(t *testing.T) {
	t.Parallel()

	root, err := os.MkdirTemp("/tmp", "kamui-app-concurrent-upgrade-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	layout := state.NewLayout(root)
	staleManager := session.NewManager(ssh.Transport{Launcher: &testsupport.SSHLauncher{}, ReadinessTimeout: time.Second})
	t.Cleanup(func() { _ = staleManager.Close() })
	ctx, cancel := context.WithCancel(context.Background())
	stale, err := controller.StartWithIdentity(ctx, layout, staleManager, controller.Identity{
		Protocol: controller.ProtocolVersion, Build: "older-installed-build",
	})
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	replacementManager := session.NewManager(ssh.Transport{Launcher: &testsupport.SSHLauncher{}, ReadinessTimeout: time.Second})
	t.Cleanup(func() { _ = replacementManager.Close() })
	starter := &inProcessStarter{ctx: ctx, manager: replacementManager}

	const callers = 8
	errors := make(chan error, callers)
	var wait sync.WaitGroup
	for range callers {
		wait.Add(1)
		go func() {
			defer wait.Done()
			_, err := app.New(layout, starter).Execute(context.Background(), app.Request{Operation: app.Ensure, Destination: "reyna"})
			errors <- err
		}()
	}
	wait.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Errorf("concurrent command: %v", err)
		}
	}
	if starter.count() != 1 {
		t.Fatalf("replacement controller starts = %d, want 1", starter.count())
	}
	t.Cleanup(func() { cancel(); _ = stale.Close(); starter.close() })
}

func TestApplicationMigratesV004ControllerProtocol(t *testing.T) {
	root, err := os.MkdirTemp("/tmp", "kamui-app-v004-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	layout := state.NewLayout(root)
	command := exec.Command(os.Args[0], "-test.run=TestV004ControllerHelperProcess", "--", root)
	command.Env = append(os.Environ(), "KAMUI_V004_HELPER=1")
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	waited := make(chan error, 1)
	go func() { waited <- command.Wait() }()
	t.Cleanup(func() {
		if command.Process != nil {
			_ = command.Process.Kill()
		}
		select {
		case <-waited:
		case <-time.After(time.Second):
		}
	})

	deadline := time.Now().Add(3 * time.Second)
	var legacyIdentity controller.Identity
	for {
		identity, probeErr := (controller.Client{Layout: layout}).Identity(context.Background())
		if probeErr == nil && identity.Legacy {
			legacyIdentity = identity
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("v0.0.4 helper did not become ready: identity=%#v error=%v", identity, probeErr)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if legacyIdentity.PID != command.Process.Pid {
		t.Fatalf("authenticated legacy peer PID = %d, want helper PID %d", legacyIdentity.PID, command.Process.Pid)
	}

	manager := session.NewManager(ssh.Transport{Launcher: &testsupport.SSHLauncher{}, ReadinessTimeout: time.Second})
	t.Cleanup(func() { _ = manager.Close() })
	ctx, cancel := context.WithCancel(context.Background())
	starter := &inProcessStarter{ctx: ctx, manager: manager}
	result, err := app.New(layout, starter).Execute(context.Background(), app.Request{Operation: app.Ensure, Destination: "reyna"})
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	if result.Session.State != session.SessionConnected || starter.count() != 1 {
		t.Fatalf("state=%v replacement starts=%d, want successful migrated command", result.Session.State, starter.count())
	}
	t.Cleanup(func() { cancel(); starter.close() })
}

func TestV004ControllerHelperProcess(t *testing.T) {
	if os.Getenv("KAMUI_V004_HELPER") != "1" {
		return
	}
	root := os.Args[len(os.Args)-1]
	layout := state.NewLayout(root)
	if err := layout.Ensure(); err != nil {
		os.Exit(2)
	}
	lock, err := os.OpenFile(layout.Lock, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil || syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) != nil {
		os.Exit(2)
	}
	defer func() { _ = lock.Close() }()
	_ = os.Remove(layout.Socket)
	listener, err := net.Listen("unix", layout.Socket)
	if err != nil {
		os.Exit(2)
	}
	defer func() { _ = listener.Close() }()
	const token = "v004-authenticated-token"
	if err := os.WriteFile(layout.Token, []byte(token), 0o600); err != nil {
		os.Exit(2)
	}
	for {
		connection, err := listener.Accept()
		if err != nil {
			return
		}
		var request struct {
			Token string `json:"token"`
		}
		if json.NewDecoder(connection).Decode(&request) == nil && request.Token == token {
			_, _ = connection.Write([]byte("{\"result\":{\"sessions\":[]}}\n"))
		}
		_ = connection.Close()
	}
}

func TestSSHHookReturnsAfterControllerAcceptsSlowActivation(t *testing.T) {
	t.Parallel()

	root, err := os.MkdirTemp("/tmp", "kamui-hook-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	layout := state.NewLayout(root)
	launcher := newGatedLauncher()
	manager := session.NewManager(ssh.Transport{Launcher: launcher, ReadinessTimeout: time.Second})
	ctx, cancel := context.WithCancel(context.Background())
	starter := &inProcessStarter{ctx: ctx, manager: manager}
	application := app.New(layout, starter)
	t.Cleanup(func() {
		launcher.release()
		cancel()
		starter.close()
		_ = manager.Close()
	})

	result := make(chan error, 1)
	go func() {
		_, err := application.Execute(context.Background(), app.Request{Operation: app.SSHHook, Destination: "reyna"})
		result <- err
	}()
	select {
	case <-launcher.started:
	case <-time.After(time.Second):
		t.Fatal("SSH hook activation did not reach the launcher")
	}
	select {
	case err := <-result:
		if err != nil {
			t.Fatalf("SSH hook: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("SSH hook waited for blocked SSH activation")
	}
	launcher.release()
}

type gatedLauncher struct {
	testsupport.SSHLauncher
	started     chan struct{}
	proceed     chan struct{}
	startedOnce sync.Once
	releaseOnce sync.Once
}

func newGatedLauncher() *gatedLauncher {
	return &gatedLauncher{started: make(chan struct{}), proceed: make(chan struct{})}
}

func (l *gatedLauncher) Start(request ssh.StartRequest) (ssh.Process, error) {
	l.startedOnce.Do(func() { close(l.started) })
	<-l.proceed
	return l.SSHLauncher.Start(request)
}

func (l *gatedLauncher) release() {
	l.releaseOnce.Do(func() { close(l.proceed) })
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
