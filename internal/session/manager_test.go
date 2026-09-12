package session_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/narasaka/kamui/internal/browser"
	"github.com/narasaka/kamui/internal/session"
	"github.com/narasaka/kamui/internal/ssh"
)

func TestManagerEnsuresAndReusesOneNetworkingSession(t *testing.T) {
	t.Parallel()

	launcher := &managerLauncher{}
	manager := session.NewManager(ssh.Transport{Launcher: launcher, ReadinessTimeout: time.Second})
	t.Cleanup(func() { _ = manager.Close() })
	destination, _ := session.ParseDestination("reyna")

	first, err := manager.Execute(context.Background(), session.Command{Operation: session.Ensure, Destination: destination})
	if err != nil {
		t.Fatalf("first ensure: %v", err)
	}
	second, err := manager.Execute(context.Background(), session.Command{Operation: session.Ensure, Destination: destination})
	if err != nil {
		t.Fatalf("second ensure: %v", err)
	}
	if first.Session.Proxy != second.Session.Proxy {
		t.Fatalf("proxy changed from %s to %s", first.Session.Proxy, second.Session.Proxy)
	}
	if !first.Session.Proxy.Addr().IsLoopback() {
		t.Fatalf("proxy address = %s, want loopback", first.Session.Proxy)
	}
	if got := launcher.startCount(); got != 1 {
		t.Fatalf("OpenSSH starts = %d, want 1", got)
	}

	status, err := manager.Execute(context.Background(), session.Command{Operation: session.Status, Destination: destination})
	if err != nil {
		t.Fatal(err)
	}
	if status.Session.State != session.SessionConnected {
		t.Fatalf("session state = %v, want connected", status.Session.State)
	}
}

func TestManagerWritesStructuredLifecycleLogsWithoutRawDestination(t *testing.T) {
	t.Parallel()

	var logs bytes.Buffer
	manager := session.NewManagerWithOptions(session.ManagerOptions{
		Transport: ssh.Transport{Launcher: &managerLauncher{}, ReadinessTimeout: time.Second},
		Logger:    slog.New(slog.NewJSONHandler(&logs, nil)),
	})
	t.Cleanup(func() { _ = manager.Close() })
	destination, _ := session.ParseDestination("narasaka@private.example.com")
	if _, err := manager.Execute(context.Background(), session.Command{Operation: session.Ensure, Destination: destination}); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Execute(context.Background(), session.Command{Operation: session.Stop, Destination: destination}); err != nil {
		t.Fatal(err)
	}
	got := logs.String()
	if !strings.Contains(got, `"event":"session_started"`) || !strings.Contains(got, `"session_key":`) {
		t.Fatalf("structured logs = %q, want session event and safe key", got)
	}
	if strings.Contains(got, destination.String()) {
		t.Fatalf("structured logs exposed raw destination: %q", got)
	}
}

func TestManagerStopsReconnectsWhenAuthenticationNeedsUser(t *testing.T) {
	t.Parallel()

	launcher := &authenticationLauncher{}
	manager := session.NewManager(ssh.Transport{Launcher: launcher, ReadinessTimeout: time.Second})
	t.Cleanup(func() { _ = manager.Close() })
	destination, _ := session.ParseDestination("reyna")
	if _, err := manager.Execute(context.Background(), session.Command{Operation: session.Ensure, Destination: destination}); err != nil {
		t.Fatal(err)
	}
	launcher.terminateConnected()

	deadline := time.Now().Add(2 * time.Second)
	for {
		status, err := manager.Execute(context.Background(), session.Command{Operation: session.Status, Destination: destination})
		if err == nil && status.Session.State == session.SessionAuthenticationRequired {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("state = %v, error %v; want authentication required", status.Session.State, err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	time.Sleep(200 * time.Millisecond)
	if got := launcher.count(); got != 2 {
		t.Fatalf("OpenSSH starts = %d, want initial plus one authentication failure", got)
	}
	launcher.allowAuthentication()
	result, err := manager.Execute(context.Background(), session.Command{Operation: session.Ensure, Destination: destination})
	if err != nil {
		t.Fatalf("interactive rerun after authentication: %v", err)
	}
	if result.Session.State != session.SessionConnected || launcher.count() != 3 {
		t.Fatalf("rerun state=%v starts=%d, want connected after third start", result.Session.State, launcher.count())
	}
}

func TestManagerLaunchesSelectedBrowserAndOpensURLsInItsProfile(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	executable := filepath.Join(root, "chrome")
	if err := os.WriteFile(executable, []byte("browser"), 0o700); err != nil {
		t.Fatal(err)
	}
	browserLauncher := &sessionBrowserLauncher{}
	catalog := browser.NewCatalog([]browser.Adapter{
		browser.NewChromiumAdapter("chrome", []string{executable}, browserLauncher),
	})
	manager := session.NewManagerWithOptions(session.ManagerOptions{
		Transport:   ssh.Transport{Launcher: &managerLauncher{}, ReadinessTimeout: time.Second},
		Browsers:    catalog,
		ProfileRoot: filepath.Join(root, "profiles"),
	})
	t.Cleanup(func() { _ = manager.Close() })
	destination, _ := session.ParseDestination("reyna")
	ensured, err := manager.Execute(context.Background(), session.Command{
		Operation:   session.Ensure,
		Destination: destination,
		Browser:     browser.Selection{Explicit: "chrome"},
	})
	if err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if ensured.Session.Browser != "chrome" {
		t.Fatalf("browser = %q, want chrome", ensured.Session.Browser)
	}
	if _, err := manager.Execute(context.Background(), session.Command{
		Operation:   session.Open,
		Destination: destination,
		URLs:        []string{"http://localhost:3003"},
	}); err != nil {
		t.Fatalf("open: %v", err)
	}
	if got := browserLauncher.count(); got != 2 {
		t.Fatalf("browser launches = %d, want initial launch plus open", got)
	}
}

func TestManagerStopsDedicatedBrowserWhenConfigured(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	executable := filepath.Join(root, "chrome")
	if err := os.WriteFile(executable, []byte("browser"), 0o700); err != nil {
		t.Fatal(err)
	}
	browserLauncher := &sessionBrowserLauncher{}
	catalog := browser.NewCatalog([]browser.Adapter{
		browser.NewChromiumAdapter("chrome", []string{executable}, browserLauncher),
	})
	manager := session.NewManagerWithOptions(session.ManagerOptions{
		Transport:   ssh.Transport{Launcher: &managerLauncher{}, ReadinessTimeout: time.Second},
		Browsers:    catalog,
		ProfileRoot: filepath.Join(root, "profiles"),
	})
	t.Cleanup(func() { _ = manager.Close() })
	destination, _ := session.ParseDestination("reyna")
	if _, err := manager.Execute(context.Background(), session.Command{
		Operation: session.Ensure, Destination: destination,
		Browser: browser.Selection{Explicit: "chrome"}, StopBrowserOnStop: true,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Execute(context.Background(), session.Command{Operation: session.Stop, Destination: destination}); err != nil {
		t.Fatal(err)
	}
	if got := browserLauncher.stopCount(); got != 1 {
		t.Fatalf("browser stops = %d, want 1", got)
	}
}

func TestManagerReconnectsAfterTransientSSHExit(t *testing.T) {
	t.Parallel()

	launcher := &managerLauncher{}
	manager := session.NewManager(ssh.Transport{Launcher: launcher, ReadinessTimeout: time.Second})
	t.Cleanup(func() { _ = manager.Close() })
	destination, _ := session.ParseDestination("reyna")
	if _, err := manager.Execute(context.Background(), session.Command{Operation: session.Ensure, Destination: destination}); err != nil {
		t.Fatal(err)
	}
	launcher.terminateFirst()

	deadline := time.Now().Add(3 * time.Second)
	for {
		status, err := manager.Execute(context.Background(), session.Command{Operation: session.Status, Destination: destination})
		if err == nil && status.Session.State == session.SessionConnected && launcher.startCount() == 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("session did not reconnect: state=%v starts=%d error=%v", status.Session.State, launcher.startCount(), err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestManagerListsAndStopsAllSessions(t *testing.T) {
	t.Parallel()

	manager := session.NewManager(ssh.Transport{Launcher: &managerLauncher{}, ReadinessTimeout: time.Second})
	t.Cleanup(func() { _ = manager.Close() })
	for _, raw := range []string{"zeta", "alpha"} {
		destination, _ := session.ParseDestination(raw)
		if _, err := manager.Execute(context.Background(), session.Command{Operation: session.Ensure, Destination: destination}); err != nil {
			t.Fatal(err)
		}
	}
	listed, err := manager.Execute(context.Background(), session.Command{Operation: session.Status})
	if err != nil {
		t.Fatal(err)
	}
	if len(listed.Sessions) != 2 || listed.Sessions[0].Destination.String() != "alpha" || listed.Sessions[1].Destination.String() != "zeta" {
		t.Fatalf("listed sessions = %#v, want alpha then zeta", listed.Sessions)
	}
	if _, err := manager.Execute(context.Background(), session.Command{Operation: session.StopAll}); err != nil {
		t.Fatalf("stop all: %v", err)
	}
	listed, err = manager.Execute(context.Background(), session.Command{Operation: session.Status})
	if err != nil || len(listed.Sessions) != 0 {
		t.Fatalf("sessions after stop all = %#v, error %v", listed.Sessions, err)
	}
}

func TestManagerExpiresSessionAfterConfiguredProxyIdleTimeout(t *testing.T) {
	t.Parallel()

	manager := session.NewManager(ssh.Transport{Launcher: &managerLauncher{}, ReadinessTimeout: time.Second})
	t.Cleanup(func() { _ = manager.Close() })
	destination, _ := session.ParseDestination("reyna")
	if _, err := manager.Execute(context.Background(), session.Command{
		Operation: session.Ensure, Destination: destination, IdleTimeout: 150 * time.Millisecond,
	}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		_, err := manager.Execute(context.Background(), session.Command{Operation: session.Status, Destination: destination})
		if err != nil && strings.Contains(err.Error(), "no Kamui session") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("session did not expire after idle timeout")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

type managerLauncher struct {
	mu        sync.Mutex
	starts    int
	processes []*managerProcess
}

func (l *managerLauncher) Start(request ssh.StartRequest) (ssh.Process, error) {
	var address string
	for index, argument := range request.Args {
		if argument == "-D" && index+1 < len(request.Args) {
			address = request.Args[index+1]
		}
	}
	listener, err := net.Listen("tcp4", address)
	if err != nil {
		return nil, err
	}
	process := &managerProcess{listener: listener, done: make(chan struct{})}
	l.mu.Lock()
	l.starts++
	l.processes = append(l.processes, process)
	l.mu.Unlock()
	go func() {
		for {
			connection, err := listener.Accept()
			if err != nil {
				return
			}
			_ = connection.Close()
		}
	}()
	return process, nil
}

func (l *managerLauncher) terminateFirst() {
	l.mu.Lock()
	process := l.processes[0]
	l.mu.Unlock()
	_ = process.Kill()
}

func (l *managerLauncher) startCount() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.starts
}

type managerProcess struct {
	listener net.Listener
	done     chan struct{}
	once     sync.Once
}

type sessionBrowserLauncher struct {
	mu    sync.Mutex
	calls int
	stops int
}

type authenticationLauncher struct {
	mu            sync.Mutex
	starts        int
	process       *managerProcess
	authenticated bool
}

func (l *authenticationLauncher) Start(request ssh.StartRequest) (ssh.Process, error) {
	l.mu.Lock()
	l.starts++
	starts := l.starts
	l.mu.Unlock()
	if starts > 1 && !l.isAuthenticated() {
		_, _ = io.WriteString(request.Stderr, "Permission denied (publickey).\n")
		return authExitedProcess{}, nil
	}
	var address string
	for index, argument := range request.Args {
		if argument == "-D" && index+1 < len(request.Args) {
			address = request.Args[index+1]
		}
	}
	listener, err := net.Listen("tcp4", address)
	if err != nil {
		return nil, err
	}
	process := &managerProcess{listener: listener, done: make(chan struct{})}
	l.mu.Lock()
	l.process = process
	l.mu.Unlock()
	go func() {
		for {
			connection, err := listener.Accept()
			if err != nil {
				return
			}
			_ = connection.Close()
		}
	}()
	return process, nil
}

func (l *authenticationLauncher) isAuthenticated() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.authenticated
}

func (l *authenticationLauncher) allowAuthentication() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.authenticated = true
}

func (l *authenticationLauncher) terminateConnected() {
	l.mu.Lock()
	process := l.process
	l.mu.Unlock()
	_ = process.Kill()
}

func (l *authenticationLauncher) count() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.starts
}

type authExitedProcess struct{}

func (authExitedProcess) Wait() error            { return errors.New("exit status 255") }
func (authExitedProcess) Signal(os.Signal) error { return os.ErrProcessDone }
func (authExitedProcess) Kill() error            { return os.ErrProcessDone }

func (l *sessionBrowserLauncher) Launch(context.Context, string, []string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.calls++
	return nil
}

func (l *sessionBrowserLauncher) count() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.calls
}

func (l *sessionBrowserLauncher) Stop(context.Context, string, []string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.stops++
	return nil
}

func (l *sessionBrowserLauncher) stopCount() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.stops
}

func (p *managerProcess) Wait() error {
	<-p.done
	return nil
}

func (p *managerProcess) Signal(os.Signal) error { return p.Kill() }

func (p *managerProcess) Kill() error {
	var err error
	p.once.Do(func() {
		close(p.done)
		err = p.listener.Close()
	})
	return err
}
