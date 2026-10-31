package app_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/narasaka/kamui/internal/app"
	"github.com/narasaka/kamui/internal/askpass"
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
	s.t.Error("controller must not start for this operation")
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

func TestSSHHookEnablesMirroring(t *testing.T) {
	t.Parallel()

	root, err := os.MkdirTemp("/tmp", "kamui-hook-mirror-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	layout := state.NewLayout(root)
	manager := session.NewManagerWithOptions(session.ManagerOptions{
		Transport:        ssh.Transport{Launcher: &testsupport.SSHLauncher{}, ReadinessTimeout: time.Second},
		MirrorDiscoverer: staticDiscoverer{},
		MirrorInterval:   time.Hour,
	})
	t.Cleanup(func() { _ = manager.Close() })
	server, err := controller.Start(context.Background(), layout, manager)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Close() })
	application := app.New(layout, nil)

	if _, err := application.Execute(context.Background(), app.Request{Operation: app.SSHHook, Destination: "reyna"}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for {
		result, statusErr := application.Execute(context.Background(), app.Request{Operation: app.Status, Destination: "reyna"})
		if statusErr == nil && result.Session.Mirror.Enabled {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("mirror enabled = false after SSH hook; last status error: %v", statusErr)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

type staticDiscoverer struct{}

func (staticDiscoverer) ListeningPorts(context.Context, string) ([]uint16, error) {
	return nil, nil
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

func TestStatusAndStopDoNotStartMissingController(t *testing.T) {
	t.Parallel()

	application := app.New(state.NewLayout(t.TempDir()), &failingStarter{t: t})
	result, err := application.Execute(context.Background(), app.Request{Operation: app.Status})
	if err != nil {
		t.Fatalf("status returned error: %v", err)
	}
	if result.Sessions == nil || len(result.Sessions) != 0 {
		t.Fatalf("sessions = %#v, want empty list", result.Sessions)
	}
	if _, err := application.Execute(context.Background(), app.Request{Operation: app.Stop, All: true}); err != nil {
		t.Fatalf("stop --all returned error: %v", err)
	}
	for _, operation := range []app.Operation{app.Status, app.Stop} {
		_, err := application.Execute(context.Background(), app.Request{Operation: operation, Destination: "reyna"})
		if got, want := fmt.Sprint(err), "no Kamui session for reyna"; got != want {
			t.Fatalf("operation %d error = %q, want %q", operation, got, want)
		}
	}
}

func TestStatusAndStopReportControllerFromAnotherBuildWithoutReplacingIt(t *testing.T) {
	t.Parallel()

	root, err := os.MkdirTemp("/tmp", "kamui-app-mismatch-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	layout := state.NewLayout(root)
	manager := session.NewManager(ssh.Transport{Launcher: &testsupport.SSHLauncher{}, ReadinessTimeout: time.Second})
	t.Cleanup(func() { _ = manager.Close() })
	other := controller.Identity{Protocol: controller.ProtocolVersion, Build: "other-installed-build"}
	server, err := controller.StartWithIdentity(context.Background(), layout, manager, other)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Close() })

	application := app.New(layout, &failingStarter{t: t})
	for _, request := range []app.Request{{Operation: app.Status}, {Operation: app.Stop, Destination: "reyna"}} {
		_, err = application.Execute(context.Background(), request)
		if err == nil || !strings.Contains(err.Error(), "different kamui build") {
			t.Fatalf("operation %d error = %v, want different-build report", request.Operation, err)
		}
	}
	identity, err := (controller.Client{Layout: layout}).Identity(context.Background())
	if err != nil || identity.Build != other.Build {
		t.Fatalf("controller after status = %#v, %v; want the other build still running", identity, err)
	}
}

func TestUnansweredPromptIsCancelledWhenConnectionAttemptTimesOut(t *testing.T) {
	t.Parallel()

	root, err := os.MkdirTemp("/tmp", "kamui-app-stalled-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	layout := state.NewLayout(root)
	manager := session.NewManagerWithOptions(session.ManagerOptions{
		Transport: ssh.Transport{
			Launcher: stalledPromptLauncher{}, ReadinessTimeout: 200 * time.Millisecond,
			PromptLimit: time.Second, AskpassProgram: "/usr/local/bin/kamui",
		},
		MirrorDiscoverer: staticDiscoverer{},
		MirrorInterval:   time.Hour,
	})
	t.Cleanup(func() { _ = manager.Close() })
	server, err := controller.Start(context.Background(), layout, manager)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Close() })
	application := app.New(layout, nil)

	prompted := make(chan struct{})
	cancelled := make(chan struct{})
	prompter := func(ctx context.Context, _ askpass.Prompt) (string, error) {
		close(prompted)
		<-ctx.Done()
		close(cancelled)
		return "", ctx.Err()
	}
	done := make(chan error, 1)
	go func() {
		_, err := application.Execute(context.Background(), app.Request{Operation: app.Mirror, Destination: "reyna", Prompter: prompter})
		done <- err
	}()
	<-prompted
	// The open prompt must not block status, such as the menu bar app's polling.
	statusStarted := time.Now()
	if _, err := application.Execute(context.Background(), app.Request{Operation: app.Status}); err != nil {
		t.Fatalf("status while a prompt is open: %v", err)
	}
	if elapsed := time.Since(statusStarted); elapsed > 500*time.Millisecond {
		t.Fatalf("status waited %s behind an open prompt", elapsed)
	}
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "not ready") {
			t.Fatalf("connect error = %v, want readiness timeout", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("connect stayed blocked on an unanswered prompt")
	}
	select {
	case <-cancelled:
	default:
		t.Fatal("open prompt was not cancelled when the attempt ended")
	}
	statusDone := make(chan error, 1)
	go func() {
		_, err := application.Execute(context.Background(), app.Request{Operation: app.Status})
		statusDone <- err
	}()
	select {
	case err := <-statusDone:
		if err != nil {
			t.Fatalf("status after abandoned prompt: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("controller stopped answering status after an abandoned prompt")
	}
}

// stalledPromptLauncher starts the askpass helper in the background, as
// OpenSSH does, and never becomes ready.
type stalledPromptLauncher struct{}

func (stalledPromptLauncher) Start(request ssh.StartRequest) (ssh.Process, error) {
	environment := map[string]string{}
	for _, entry := range request.Env {
		if name, value, ok := strings.Cut(entry, "="); ok {
			environment[name] = value
		}
	}
	go askpass.RunHelper([]string{"Enter passphrase for key '/k': "}, func(name string) string { return environment[name] }, io.Discard)
	return &blockedProcess{done: make(chan struct{})}, nil
}

type blockedProcess struct {
	done chan struct{}
	once sync.Once
}

func (p *blockedProcess) Wait() error {
	<-p.done
	return errors.New("signal: killed")
}

func (p *blockedProcess) Signal(os.Signal) error { return nil }

func (p *blockedProcess) Kill() error {
	p.once.Do(func() { close(p.done) })
	return nil
}

func TestStatusAndStopNeverReplaceControllerThatChangesAfterProbe(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name    string
		request app.Request
		vanish  bool
		wantErr string
	}{
		{name: "status after replacement", request: app.Request{Operation: app.Status}},
		{name: "stop after replacement", request: app.Request{Operation: app.Stop, Destination: "reyna"}, wantErr: "no Kamui session for reyna"},
		{name: "status after exit", request: app.Request{Operation: app.Status}, vanish: true},
		{name: "stop after exit", request: app.Request{Operation: app.Stop, Destination: "reyna"}, vanish: true, wantErr: "no Kamui session for reyna"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			root, err := os.MkdirTemp("/tmp", "kamui-app-race-")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.RemoveAll(root) })
			layout := state.NewLayout(root)
			fake := startChangingController(t, layout, test.vanish)

			_, err = app.New(layout, &failingStarter{t: t}).Execute(context.Background(), test.request)
			if got := fmt.Sprint(err); (test.wantErr == "" && err != nil) || (test.wantErr != "" && got != test.wantErr) {
				t.Fatalf("error = %v, want %q", err, test.wantErr)
			}
			if fake.shutdowns() != 0 {
				t.Fatalf("observing command asked the replacement controller to shut down %d times", fake.shutdowns())
			}
		})
	}
}

// changingController answers its first identity probe with the caller's
// build and every later probe with another build, as if a terminal using a
// different kamui had replaced it. With vanish, it closes command requests
// without answering, as if it had exited.
type changingController struct {
	mu       sync.Mutex
	probes   int
	shutdown int
}

func (c *changingController) shutdowns() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.shutdown
}

func startChangingController(t *testing.T, layout state.Layout, vanish bool) *changingController {
	t.Helper()
	if err := layout.Ensure(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(layout.Token, []byte("token"), 0o600); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("unix", layout.Socket)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	fake := &changingController{}
	go func() {
		for {
			connection, err := listener.Accept()
			if err != nil {
				return
			}
			go fake.serve(connection, vanish)
		}
	}()
	return fake
}

func (c *changingController) serve(connection net.Conn, vanish bool) {
	defer func() { _ = connection.Close() }()
	var request struct {
		Probe       bool   `json:"probe"`
		Shutdown    bool   `json:"shutdown"`
		Destination string `json:"destination"`
	}
	if err := json.NewDecoder(connection).Decode(&request); err != nil {
		return
	}
	encoder := json.NewEncoder(connection)
	c.mu.Lock()
	defer c.mu.Unlock()
	switch {
	case request.Probe:
		c.probes++
		build := version.BuildIdentity()
		if c.probes > 1 {
			build = "replacement-from-another-build"
		}
		_ = encoder.Encode(map[string]any{"result": map[string]any{}, "identity": controller.Identity{Protocol: controller.ProtocolVersion, Build: build}})
	case request.Shutdown:
		c.shutdown++
		_ = encoder.Encode(map[string]any{"result": map[string]any{}})
	case vanish:
	case request.Destination == "":
		_ = encoder.Encode(map[string]any{"result": map[string]any{"sessions": []any{}}})
	default:
		_ = encoder.Encode(map[string]any{"error": "no Kamui session for " + request.Destination})
	}
}

func TestOpenPromptDoesNotBlockOtherSessionsAndStopCancelsIt(t *testing.T) {
	t.Parallel()

	root, err := os.MkdirTemp("/tmp", "kamui-app-concurrent-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	layout := state.NewLayout(root)
	manager := session.NewManagerWithOptions(session.ManagerOptions{
		Transport: ssh.Transport{
			Launcher: &selectivePromptLauncher{}, ReadinessTimeout: 200 * time.Millisecond,
			PromptLimit: time.Minute, AskpassProgram: "/usr/local/bin/kamui",
		},
		MirrorDiscoverer: staticDiscoverer{},
		MirrorInterval:   time.Hour,
	})
	t.Cleanup(func() { _ = manager.Close() })
	server, err := controller.Start(context.Background(), layout, manager)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Close() })
	application := app.New(layout, nil)
	if _, err := application.Execute(context.Background(), app.Request{Operation: app.Mirror, Destination: "other"}); err != nil {
		t.Fatal(err)
	}

	prompted := make(chan struct{})
	prompter := func(ctx context.Context, _ askpass.Prompt) (string, error) {
		close(prompted)
		<-ctx.Done()
		return "", ctx.Err()
	}
	waiting := make(chan error, 1)
	go func() {
		_, err := application.Execute(context.Background(), app.Request{Operation: app.Mirror, Destination: "reyna", Prompter: prompter})
		waiting <- err
	}()
	<-prompted

	quickly := func(name string, request app.Request) {
		t.Helper()
		started := time.Now()
		if _, err := application.Execute(context.Background(), request); err != nil {
			t.Fatalf("%s while a prompt is open: %v", name, err)
		}
		if elapsed := time.Since(started); elapsed > time.Second {
			t.Fatalf("%s waited %s behind an open prompt", name, elapsed)
		}
	}
	quickly("stop another session", app.Request{Operation: app.Stop, Destination: "other"})
	quickly("connect another destination", app.Request{Operation: app.Mirror, Destination: "third"})
	quickly("stop the prompting destination", app.Request{Operation: app.Stop, Destination: "reyna"})
	select {
	case err := <-waiting:
		if err == nil {
			t.Fatal("cancelled connection attempt succeeded")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("stop did not cancel the connection attempt waiting for a prompt")
	}
	if _, err := application.Execute(context.Background(), app.Request{Operation: app.Status, Destination: "reyna"}); err == nil {
		t.Fatal("cancelled destination has a session")
	}
}

// selectivePromptLauncher stalls on a prompt when the transport relays
// prompts and otherwise connects like testsupport.SSHLauncher.
type selectivePromptLauncher struct{ testsupport.SSHLauncher }

func (l *selectivePromptLauncher) Start(request ssh.StartRequest) (ssh.Process, error) {
	for _, entry := range request.Env {
		if strings.HasPrefix(entry, askpass.SocketEnv+"=") {
			return stalledPromptLauncher{}.Start(request)
		}
	}
	return l.SSHLauncher.Start(request)
}
