package session_test

import (
	"context"
	"errors"
	"io"
	"net"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/narasaka/kamui/internal/session"
	"github.com/narasaka/kamui/internal/ssh"
)

func TestManagerKeepsRetryingTransientFailures(t *testing.T) {
	t.Parallel()

	failures := make([]scriptedStart, 8)
	for index := range failures {
		failures[index] = failTransient
	}
	launcher := &scriptedLauncher{script: append(append([]scriptedStart{answer}, failures...), answer)}
	manager := scriptedManager(t, launcher, session.ManagerOptions{ReconnectDelay: 5 * time.Millisecond, MaxReconnectDelay: 20 * time.Millisecond})
	destination := ensureScripted(t, manager)
	launcher.terminate(0)

	waitForState(t, manager, destination, session.SessionConnected, func() bool { return launcher.starts() == 10 })
}

func TestManagerRetriesUnresolvableHostInTheBackground(t *testing.T) {
	t.Parallel()

	launcher := &scriptedLauncher{script: []scriptedStart{answer, failDNS, failDNS, answer}}
	manager := scriptedManager(t, launcher, session.ManagerOptions{ReconnectDelay: 5 * time.Millisecond})
	destination := ensureScripted(t, manager)
	launcher.terminate(0)

	waitForState(t, manager, destination, session.SessionConnected, func() bool { return launcher.starts() == 4 })
}

func TestManagerWakeRetriesWithoutWaitingForBackoff(t *testing.T) {
	t.Parallel()

	launcher := &scriptedLauncher{script: []scriptedStart{answer, failTransient, answer}}
	manager := scriptedManager(t, launcher, session.ManagerOptions{ReconnectDelay: time.Hour})
	destination := ensureScripted(t, manager)
	launcher.terminate(0)
	waitForState(t, manager, destination, session.SessionUnavailable, func() bool { return launcher.starts() == 2 })
	time.Sleep(100 * time.Millisecond)
	if got := launcher.starts(); got != 2 {
		t.Fatalf("OpenSSH starts before wakeup = %d, want the retry to wait for its backoff", got)
	}

	manager.Wake("test")
	waitForState(t, manager, destination, session.SessionConnected, func() bool { return launcher.starts() == 3 })
}

func TestManagerWakeReplacesUnresponsiveTransport(t *testing.T) {
	t.Parallel()

	launcher := &scriptedLauncher{script: []scriptedStart{silent, answer}}
	manager := scriptedManager(t, launcher, session.ManagerOptions{ProbeTimeout: 100 * time.Millisecond})
	destination := ensureScripted(t, manager)

	manager.Wake("test")
	waitForState(t, manager, destination, session.SessionConnected, func() bool { return launcher.starts() == 2 })
}

func TestManagerWakeKeepsResponsiveTransport(t *testing.T) {
	t.Parallel()

	launcher := &scriptedLauncher{script: []scriptedStart{answer, answer}}
	manager := scriptedManager(t, launcher, session.ManagerOptions{ProbeTimeout: time.Second})
	destination := ensureScripted(t, manager)

	manager.Wake("test")
	deadline := time.Now().Add(time.Second)
	for launcher.probes() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("wakeup did not probe the connected transport")
		}
		time.Sleep(10 * time.Millisecond)
	}
	time.Sleep(200 * time.Millisecond)
	status, err := manager.Execute(context.Background(), session.Command{Operation: session.Status, Destination: destination})
	if err != nil {
		t.Fatal(err)
	}
	if status.Session.State != session.SessionConnected || launcher.starts() != 1 {
		t.Fatalf("after wakeup state=%v starts=%d, want the original connection kept", status.Session.State, launcher.starts())
	}
}

func TestManagerDiscardsBackgroundReconnectAfterForegroundOne(t *testing.T) {
	t.Parallel()

	launcher := &scriptedLauncher{script: []scriptedStart{answer, gated, answer}, gate: make(chan struct{})}
	manager := scriptedManager(t, launcher, session.ManagerOptions{})
	destination := ensureScripted(t, manager)
	launcher.terminate(0)
	deadline := time.Now().Add(time.Second)
	for launcher.starts() < 2 {
		if time.Now().After(deadline) {
			t.Fatal("monitor did not start a background reconnect")
		}
		time.Sleep(10 * time.Millisecond)
	}

	ensureScripted(t, manager)
	close(launcher.gate)
	deadline = time.Now().Add(2 * time.Second)
	for !launcher.exited(1) {
		if time.Now().After(deadline) {
			t.Fatal("background reconnect that finished after the foreground one was kept")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if launcher.exited(2) {
		t.Fatal("background reconnect closed the foreground connection")
	}
	waitForState(t, manager, destination, session.SessionConnected, func() bool { return launcher.starts() == 3 })
}

func TestManagerForegroundEnsureKeepsBackgroundReconnect(t *testing.T) {
	t.Parallel()

	launcher := &scriptedLauncher{script: []scriptedStart{answer, failTransient, gated, answer}, gate: make(chan struct{})}
	manager := scriptedManager(t, launcher, session.ManagerOptions{ReconnectDelay: 300 * time.Millisecond})
	var release sync.Once
	t.Cleanup(func() { release.Do(func() { close(launcher.gate) }) })
	destination := ensureScripted(t, manager)
	launcher.terminate(0)
	waitForState(t, manager, destination, session.SessionUnavailable, func() bool { return launcher.starts() == 2 })

	ensured := make(chan error, 1)
	go func() {
		_, err := manager.Execute(context.Background(), session.Command{Operation: session.Ensure, Destination: destination})
		ensured <- err
	}()
	waitForState(t, manager, destination, session.SessionConnected, func() bool { return launcher.starts() == 4 })
	release.Do(func() { close(launcher.gate) })
	if err := <-ensured; err != nil {
		t.Fatal(err)
	}
	if !launcher.exited(2) || launcher.exited(3) {
		t.Fatalf("foreground exited=%v background exited=%v, want the background connection kept", launcher.exited(2), launcher.exited(3))
	}
}

func TestManagerBackoffRestartsAfterForegroundReconnect(t *testing.T) {
	t.Parallel()

	launcher := &scriptedLauncher{script: []scriptedStart{answer, failTransient, answer, answer}}
	manager := scriptedManager(t, launcher, session.ManagerOptions{ReconnectDelay: time.Hour})
	destination := ensureScripted(t, manager)
	launcher.terminate(0)
	waitForState(t, manager, destination, session.SessionUnavailable, func() bool { return launcher.starts() == 2 })
	time.Sleep(100 * time.Millisecond)

	ensureScripted(t, manager)
	time.Sleep(200 * time.Millisecond)
	launcher.terminate(2)
	waitForState(t, manager, destination, session.SessionConnected, func() bool { return launcher.starts() == 4 })
}

func scriptedManager(t *testing.T, launcher *scriptedLauncher, options session.ManagerOptions) *session.Manager {
	t.Helper()
	options.Transport = ssh.Transport{Launcher: launcher, ReadinessTimeout: time.Second}
	manager := session.NewManagerWithOptions(options)
	t.Cleanup(func() { _ = manager.Close() })
	return manager
}

func ensureScripted(t *testing.T, manager *session.Manager) session.Destination {
	t.Helper()
	destination, _ := session.ParseDestination("reyna")
	if _, err := manager.Execute(context.Background(), session.Command{Operation: session.Ensure, Destination: destination}); err != nil {
		t.Fatal(err)
	}
	return destination
}

func waitForState(t *testing.T, manager *session.Manager, destination session.Destination, want session.SessionState, also func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		status, err := manager.Execute(context.Background(), session.Command{Operation: session.Status, Destination: destination})
		if err == nil && status.Session.State == want && also() {
			return
		}
		if err == nil && status.Session.State == session.SessionAuthenticationRequired {
			t.Fatalf("state = authentication required (%v), want background retries to continue", status.Session.LastError)
		}
		if time.Now().After(deadline) {
			t.Fatalf("state = %v, error %v; want %v", status.Session.State, err, want)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

type scriptedStart uint8

const (
	// answer behaves like a working OpenSSH client: the SOCKS listener
	// answers, and a stream to a closed remote port is refused by closing it.
	answer scriptedStart = iota
	// silent accepts SOCKS connections but never answers, like an OpenSSH
	// client whose server has gone away.
	silent
	failTransient
	failDNS
	// gated starts like answer once the test closes the launcher's gate.
	gated
)

// scriptedLauncher fakes one OpenSSH child per entry of script, in order.
type scriptedLauncher struct {
	mu        sync.Mutex
	script    []scriptedStart
	processes []*scriptedProcess
	probeHits int
	gate      chan struct{}
}

func (l *scriptedLauncher) Start(request ssh.StartRequest) (ssh.Process, error) {
	l.mu.Lock()
	index := len(l.processes)
	behavior := answer
	if index < len(l.script) {
		behavior = l.script[index]
	}
	process := &scriptedProcess{done: make(chan struct{})}
	l.processes = append(l.processes, process)
	l.mu.Unlock()
	switch behavior {
	case failTransient:
		_, _ = io.WriteString(request.Stderr, "ssh: connect to host reyna port 22: Operation timed out\n")
		process.exit()
		return process, nil
	case failDNS:
		_, _ = io.WriteString(request.Stderr, "ssh: Could not resolve hostname reyna: nodename nor servname provided, or not known\n")
		process.exit()
		return process, nil
	case gated:
		<-l.gate
	}
	var address string
	for position, argument := range request.Args {
		if argument == "-D" && position+1 < len(request.Args) {
			address = request.Args[position+1]
		}
	}
	listener, err := net.Listen("tcp4", address)
	if err != nil {
		return nil, err
	}
	process.listener = listener
	go func() {
		for {
			connection, err := listener.Accept()
			if err != nil {
				return
			}
			go l.serve(connection, behavior, process)
		}
	}()
	return process, nil
}

func (l *scriptedLauncher) serve(connection net.Conn, behavior scriptedStart, process *scriptedProcess) {
	defer func() { _ = connection.Close() }()
	if behavior == silent {
		<-process.done
		return
	}
	greeting := make([]byte, 3)
	if _, err := io.ReadFull(connection, greeting); err != nil {
		return
	}
	if _, err := connection.Write([]byte{5, 0}); err != nil {
		return
	}
	request := make([]byte, 10)
	if _, err := io.ReadFull(connection, request); err != nil {
		return
	}
	if request[8] == 0 && request[9] == 0 {
		l.mu.Lock()
		l.probeHits++
		l.mu.Unlock()
	}
}

func (l *scriptedLauncher) terminate(index int) {
	l.mu.Lock()
	process := l.processes[index]
	l.mu.Unlock()
	_ = process.Kill()
}

func (l *scriptedLauncher) starts() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.processes)
}

func (l *scriptedLauncher) exited(index int) bool {
	l.mu.Lock()
	process := l.processes[index]
	l.mu.Unlock()
	select {
	case <-process.done:
		return true
	default:
		return false
	}
}

func (l *scriptedLauncher) probes() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.probeHits
}

type scriptedProcess struct {
	listener net.Listener
	done     chan struct{}
	once     sync.Once
}

func (p *scriptedProcess) exit() { p.once.Do(func() { close(p.done) }) }

func (p *scriptedProcess) Wait() error {
	<-p.done
	return errors.New("exit status 255")
}

func (p *scriptedProcess) Signal(os.Signal) error { return p.Kill() }

func (p *scriptedProcess) Kill() error {
	p.exit()
	if p.listener != nil {
		return p.listener.Close()
	}
	return nil
}
