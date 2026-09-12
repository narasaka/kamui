package session_test

import (
	"context"
	"net"
	"os"
	"path/filepath"
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
}

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
