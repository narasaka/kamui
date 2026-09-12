package controller_test

import (
	"context"
	"net"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/narasaka/kamui/internal/controller"
	"github.com/narasaka/kamui/internal/session"
	"github.com/narasaka/kamui/internal/ssh"
	"github.com/narasaka/kamui/internal/state"
)

func TestTenConcurrentClientsCreateOneHostSession(t *testing.T) {
	t.Parallel()

	root, err := os.MkdirTemp("/tmp", "kamui-controller-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	layout := state.NewLayout(root)
	if err := layout.Ensure(); err != nil {
		t.Fatal(err)
	}
	launcher := &controllerLauncher{}
	manager := session.NewManager(ssh.Transport{Launcher: launcher, ReadinessTimeout: time.Second})
	t.Cleanup(func() { _ = manager.Close() })
	ctx, cancel := context.WithCancel(context.Background())
	server, err := controller.Start(ctx, layout, manager)
	if err != nil {
		t.Fatalf("Start controller: %v", err)
	}
	t.Cleanup(func() { cancel(); _ = server.Close() })
	client := controller.Client{Layout: layout}
	destination, _ := session.ParseDestination("reyna")

	const clients = 10
	results := make(chan session.Result, clients)
	errors := make(chan error, clients)
	var wait sync.WaitGroup
	for range clients {
		wait.Add(1)
		go func() {
			defer wait.Done()
			result, err := client.Execute(context.Background(), session.Command{Operation: session.Ensure, Destination: destination})
			if err != nil {
				errors <- err
				return
			}
			results <- result
		}()
	}
	wait.Wait()
	close(results)
	close(errors)
	for err := range errors {
		t.Errorf("client ensure: %v", err)
	}
	var proxyAddress string
	for result := range results {
		if proxyAddress == "" {
			proxyAddress = result.Session.Proxy.String()
		} else if result.Session.Proxy.String() != proxyAddress {
			t.Errorf("proxy = %s, want shared %s", result.Session.Proxy, proxyAddress)
		}
	}
	if got := launcher.count(); got != 1 {
		t.Fatalf("OpenSSH starts = %d, want 1", got)
	}
	if _, err := client.Execute(context.Background(), session.Command{Operation: session.Stop, Destination: destination}); err != nil {
		t.Fatalf("stop through controller: %v", err)
	}
}

func TestClientNegotiatesAuthenticatedControllerIdentity(t *testing.T) {
	t.Parallel()

	root, err := os.MkdirTemp("/tmp", "kamui-identity-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	layout := state.NewLayout(root)
	manager := session.NewManager(ssh.Transport{Launcher: &controllerLauncher{}, ReadinessTimeout: time.Second})
	t.Cleanup(func() { _ = manager.Close() })
	ctx, cancel := context.WithCancel(context.Background())
	identity := controller.Identity{Protocol: 2, Build: "test-build"}
	server, err := controller.StartWithIdentity(ctx, layout, manager, identity)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cancel(); _ = server.Close() })

	got, err := (controller.Client{Layout: layout}).Identity(context.Background())
	if err != nil {
		t.Fatalf("Identity returned error: %v", err)
	}
	if got.Protocol != identity.Protocol || got.Build != identity.Build || got.Legacy {
		t.Fatalf("identity = %#v, want protocol %d build %q and non-legacy", got, identity.Protocol, identity.Build)
	}
}

func TestAuthenticatedShutdownStopsTheNegotiatedController(t *testing.T) {
	t.Parallel()

	root, err := os.MkdirTemp("/tmp", "kamui-shutdown-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	layout := state.NewLayout(root)
	manager := session.NewManager(ssh.Transport{Launcher: &controllerLauncher{}, ReadinessTimeout: time.Second})
	t.Cleanup(func() { _ = manager.Close() })
	identity := controller.Identity{Protocol: 2, Build: "old-build"}
	server, err := controller.StartWithIdentity(context.Background(), layout, manager, identity)
	if err != nil {
		t.Fatal(err)
	}

	if err := (controller.Client{Layout: layout}).Shutdown(context.Background(), identity); err != nil {
		t.Fatalf("Shutdown returned error: %v", err)
	}
	select {
	case <-server.Done():
	case <-time.After(time.Second):
		t.Fatal("controller did not stop after authenticated shutdown")
	}
	replacement, err := controller.StartWithIdentity(context.Background(), layout, manager, controller.Identity{Protocol: 2, Build: "new-build"})
	if err != nil {
		t.Fatalf("controller lock was not released: %v", err)
	}
	t.Cleanup(func() { _ = replacement.Close() })
}

func TestLegacyShutdownRejectsUnverifiedProcessMetadata(t *testing.T) {
	t.Parallel()

	layout := state.NewLayout(t.TempDir())
	err := (controller.Client{Layout: layout}).ShutdownLegacy(context.Background(), controller.Identity{
		Legacy: false, PID: os.Getpid(),
	})
	if err == nil || !strings.Contains(err.Error(), "verified peer PID") {
		t.Fatalf("ShutdownLegacy error = %v, want rejection before signaling process", err)
	}
}

func TestControllerStatusRoundTripsMirrorConflicts(t *testing.T) {
	t.Parallel()

	root, err := os.MkdirTemp("/tmp", "kamui-mirror-status-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	occupied, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = occupied.Close() })
	port := uint16(occupied.Addr().(*net.TCPAddr).Port)
	layout := state.NewLayout(root)
	manager := session.NewManagerWithOptions(session.ManagerOptions{
		Transport:        ssh.Transport{Launcher: &controllerLauncher{}, ReadinessTimeout: time.Second},
		MirrorDiscoverer: controllerDiscoverer{ports: []uint16{port}},
		MirrorInterval:   10 * time.Millisecond,
	})
	t.Cleanup(func() { _ = manager.Close() })
	server, err := controller.Start(context.Background(), layout, manager)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Close() })
	destination, _ := session.ParseDestination("reyna")
	result, err := (controller.Client{Layout: layout}).Execute(context.Background(), session.Command{
		Operation: session.Ensure, Destination: destination, SkipBrowser: true, EnableMirror: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Session.Mirror.Enabled || len(result.Session.Mirror.ConflictedPorts) != 1 || result.Session.Mirror.ConflictedPorts[0].Port != port {
		t.Fatalf("mirror status = %#v, want conflict on %d", result.Session.Mirror, port)
	}
}

type controllerDiscoverer struct{ ports []uint16 }

func (d controllerDiscoverer) ListeningPorts(context.Context, string) ([]uint16, error) {
	return append([]uint16(nil), d.ports...), nil
}

type controllerLauncher struct {
	mu     sync.Mutex
	starts int
}

func (l *controllerLauncher) Start(request ssh.StartRequest) (ssh.Process, error) {
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
	process := &controllerProcess{listener: listener, done: make(chan struct{})}
	l.mu.Lock()
	l.starts++
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

func (l *controllerLauncher) count() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.starts
}

type controllerProcess struct {
	listener net.Listener
	done     chan struct{}
	once     sync.Once
}

func (p *controllerProcess) Wait() error            { <-p.done; return nil }
func (p *controllerProcess) Signal(os.Signal) error { return p.Kill() }
func (p *controllerProcess) Kill() error {
	var err error
	p.once.Do(func() { close(p.done); err = p.listener.Close() })
	return err
}
