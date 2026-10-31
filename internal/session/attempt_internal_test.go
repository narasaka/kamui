package session

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/narasaka/kamui/internal/ssh"
	"github.com/narasaka/kamui/internal/testsupport"
)

func TestStopBetweenConnectAndPublicationDiscardsConnection(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name string
		stop func(*Manager, Destination) error
	}{
		{name: "stop destination", stop: func(m *Manager, destination Destination) error { return m.stop(destination) }},
		{name: "stop all", stop: func(m *Manager, _ Destination) error { return m.stopAll() }},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			launcher := &testsupport.SSHLauncher{}
			manager := NewManager(ssh.Transport{Launcher: launcher, ReadinessTimeout: time.Second})
			t.Cleanup(func() { _ = manager.Close() })
			destination, err := ParseDestination("reyna")
			if err != nil {
				t.Fatal(err)
			}
			var stopErr error
			manager.connected = func() { stopErr = test.stop(manager, destination) }

			_, err = manager.Execute(context.Background(), Command{Operation: Ensure, Destination: destination, SkipBrowser: true})
			if err == nil {
				t.Fatal("ensure published a session that was stopped while it connected")
			}
			if stopErr != nil {
				t.Fatalf("stop during the attempt: %v", stopErr)
			}
			if _, err := manager.Execute(context.Background(), Command{Operation: Status, Destination: destination}); err == nil {
				t.Fatal("stopped destination has a session")
			}
			if active := launcher.Active(); active != 0 {
				t.Fatalf("%d OpenSSH processes survive the cancelled attempt", active)
			}
		})
	}
}

func TestExpiryCancelsReconnectAttemptForTheSession(t *testing.T) {
	t.Parallel()

	manager := NewManager(ssh.Transport{Launcher: &testsupport.SSHLauncher{}, ReadinessTimeout: time.Second})
	t.Cleanup(func() { _ = manager.Close() })
	destination, err := ParseDestination("reyna")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Execute(context.Background(), Command{Operation: Ensure, Destination: destination, SkipBrowser: true}); err != nil {
		t.Fatal(err)
	}
	manager.sessionsMu.RLock()
	managed := manager.sessions[destination.Key()]
	manager.sessionsMu.RUnlock()

	// A foreground reconnect for the session is waiting for a prompt.
	cancelled := make(chan struct{})
	var once sync.Once
	attempt := &connectionAttempt{cancel: func() { once.Do(func() { close(cancelled) }) }, done: make(chan struct{})}
	manager.mu.Lock()
	manager.attempts[destination.Key()] = attempt
	manager.mu.Unlock()

	manager.expire(managed)
	select {
	case <-cancelled:
	default:
		t.Fatal("expiry left the session's reconnect attempt running")
	}
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if !attempt.cancelled || attempt.stops != 0 {
		t.Fatalf("attempt cancelled=%v stops=%d, want cancelled by expiry, not stopped", attempt.cancelled, attempt.stops)
	}
	// Finish the simulated attempt as connectUnlocked would.
	delete(manager.attempts, destination.Key())
	close(attempt.done)
}

func TestCloseWaitsForInFlightAttemptToKillOpenSSH(t *testing.T) {
	t.Parallel()

	launcher := newNeverReadyLauncher()
	manager := NewManager(ssh.Transport{Launcher: launcher, ReadinessTimeout: time.Minute})
	destination, err := ParseDestination("reyna")
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		_, _ = manager.Execute(context.Background(), Command{Operation: Ensure, Destination: destination, SkipBrowser: true})
	}()
	receive(t, launcher.started, "the first OpenSSH start")

	if err := manager.Close(); err != nil {
		t.Fatal(err)
	}
	if launcher.alive() != 0 {
		t.Fatal("Close returned while an in-flight OpenSSH child was still running")
	}
}

func TestStopFailsRequestsWaitingForTheStoppedAttempt(t *testing.T) {
	t.Parallel()

	launcher := newNeverReadyLauncher()
	manager := NewManager(ssh.Transport{Launcher: launcher, ReadinessTimeout: time.Minute})
	t.Cleanup(func() { _ = manager.Close() })
	waiting := make(chan struct{}, 1)
	manager.waiting = func() { waiting <- struct{}{} }
	destination, err := ParseDestination("reyna")
	if err != nil {
		t.Fatal(err)
	}
	results := make(chan error, 2)
	ensure := func() {
		_, err := manager.Execute(context.Background(), Command{Operation: Ensure, Destination: destination, SkipBrowser: true})
		results <- err
	}
	go ensure()
	receive(t, launcher.started, "the first OpenSSH start")
	go ensure()
	receive(t, waiting, "a request waiting for the attempt")

	if err := manager.stop(destination); err != nil {
		t.Fatalf("stop: %v", err)
	}
	for range 2 {
		select {
		case err := <-results:
			if err == nil {
				t.Fatal("a request connected after its destination was stopped")
			}
		case <-time.After(5 * time.Second):
			t.Fatal("a request kept waiting after stop")
		}
	}
	if starts := launcher.starts(); starts != 1 {
		t.Fatalf("OpenSSH started %d times, want only the stopped attempt", starts)
	}
}

// neverReadyLauncher starts OpenSSH children that never open their SOCKS
// listener and records whether each has been killed.
type neverReadyLauncher struct {
	mu        sync.Mutex
	started   chan struct{}
	processes []*neverReadyProcess
}

func newNeverReadyLauncher() *neverReadyLauncher {
	return &neverReadyLauncher{started: make(chan struct{}, 16)}
}

func (l *neverReadyLauncher) Start(ssh.StartRequest) (ssh.Process, error) {
	process := &neverReadyProcess{done: make(chan struct{})}
	l.mu.Lock()
	l.processes = append(l.processes, process)
	l.mu.Unlock()
	l.started <- struct{}{}
	return process, nil
}

func (l *neverReadyLauncher) starts() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.processes)
}

func (l *neverReadyLauncher) alive() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	alive := 0
	for _, process := range l.processes {
		select {
		case <-process.done:
		default:
			alive++
		}
	}
	return alive
}

type neverReadyProcess struct {
	done chan struct{}
	once sync.Once
}

func (p *neverReadyProcess) Wait() error {
	<-p.done
	return errors.New("signal: killed")
}

func (p *neverReadyProcess) Signal(os.Signal) error { return nil }

func (p *neverReadyProcess) Kill() error {
	p.once.Do(func() { close(p.done) })
	return nil
}

func TestRequestSentAfterStopConnectsOnceCleanupFinishes(t *testing.T) {
	t.Parallel()

	launcher := &stalledThenReadyLauncher{started: make(chan struct{}, 1)}
	manager := NewManager(ssh.Transport{Launcher: launcher, ReadinessTimeout: time.Minute})
	t.Cleanup(func() { _ = manager.Close() })
	destination, err := ParseDestination("reyna")
	if err != nil {
		t.Fatal(err)
	}
	waiting := make(chan struct{}, 1)
	manager.waiting = func() { waiting <- struct{}{} }
	late := make(chan error, 1)
	var once sync.Once
	// While the stopped attempt is still cleaning up, a new request arrives.
	manager.connected = func() {
		once.Do(func() {
			go func() {
				_, err := manager.Execute(context.Background(), Command{Operation: Ensure, Destination: destination, SkipBrowser: true})
				late <- err
			}()
			if !awaitInHook(t, waiting, "a request waiting for the attempt") {
				return
			}
		})
	}
	first := make(chan error, 1)
	go func() {
		_, err := manager.Execute(context.Background(), Command{Operation: Ensure, Destination: destination, SkipBrowser: true})
		first <- err
	}()
	receive(t, launcher.started, "the first OpenSSH start")
	if err := manager.stop(destination); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-first:
		if err == nil {
			t.Fatal("stopped attempt connected")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("stopped attempt never returned")
	}
	select {
	case err := <-late:
		if err != nil {
			t.Fatalf("request sent after stop: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("request sent after stop never connected")
	}
}

// stalledThenReadyLauncher never becomes ready on its first start and then
// behaves like testsupport.SSHLauncher.
type stalledThenReadyLauncher struct {
	testsupport.SSHLauncher
	started chan struct{}
	once    sync.Once
}

func (l *stalledThenReadyLauncher) Start(request ssh.StartRequest) (ssh.Process, error) {
	first := false
	l.once.Do(func() { first = true })
	if first {
		l.started <- struct{}{}
		return &neverReadyProcess{done: make(chan struct{})}, nil
	}
	return l.SSHLauncher.Start(request)
}

func TestSecondStopFailsRequestThatArrivedAfterFirstStop(t *testing.T) {
	t.Parallel()

	launcher := &stalledThenReadyLauncher{started: make(chan struct{}, 1)}
	manager := NewManager(ssh.Transport{Launcher: launcher, ReadinessTimeout: time.Minute})
	t.Cleanup(func() { _ = manager.Close() })
	destination, err := ParseDestination("reyna")
	if err != nil {
		t.Fatal(err)
	}
	waiting := make(chan struct{}, 1)
	manager.waiting = func() { waiting <- struct{}{} }
	late := make(chan error, 1)
	var once sync.Once
	// During the first stop's cleanup a request arrives, then a second stop.
	manager.connected = func() {
		once.Do(func() {
			go func() {
				_, err := manager.Execute(context.Background(), Command{Operation: Ensure, Destination: destination, SkipBrowser: true})
				late <- err
			}()
			if !awaitInHook(t, waiting, "a request waiting for the attempt") {
				return
			}
			if err := manager.stop(destination); err != nil {
				t.Errorf("second stop: %v", err)
			}
		})
	}
	first := make(chan error, 1)
	go func() {
		_, err := manager.Execute(context.Background(), Command{Operation: Ensure, Destination: destination, SkipBrowser: true})
		first <- err
	}()
	receive(t, launcher.started, "the first OpenSSH start")
	if err := manager.stop(destination); err != nil {
		t.Fatal(err)
	}
	receive(t, first, "the stopped attempt to return")
	select {
	case err := <-late:
		if err == nil {
			t.Fatal("request connected although a stop arrived while it waited")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("request kept waiting after the second stop")
	}
	if _, err := manager.Execute(context.Background(), Command{Operation: Status, Destination: destination}); err == nil {
		t.Fatal("stopped destination has a session")
	}
}

// receive fails the test instead of hanging when an expected event never
// happens.
func receive[T any](t *testing.T, events <-chan T, what string) {
	t.Helper()
	select {
	case <-events:
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for %s", what)
	}
}

// awaitInHook is receive for manager hooks, which run on the manager's own
// goroutines: it reports a timeout without t.Fatal so the manager can finish
// its cleanup and unlock normally.
func awaitInHook[T any](t *testing.T, events <-chan T, what string) bool {
	t.Helper()
	select {
	case <-events:
		return true
	case <-time.After(5 * time.Second):
		t.Errorf("timed out waiting for %s", what)
		return false
	}
}

func TestShutdownReportsClosedToWaitingRequests(t *testing.T) {
	t.Parallel()

	launcher := newNeverReadyLauncher()
	manager := NewManager(ssh.Transport{Launcher: launcher, ReadinessTimeout: time.Minute})
	waiting := make(chan struct{}, 1)
	manager.waiting = func() { waiting <- struct{}{} }
	destination, err := ParseDestination("reyna")
	if err != nil {
		t.Fatal(err)
	}
	results := make(chan error, 2)
	ensure := func() {
		_, err := manager.Execute(context.Background(), Command{Operation: Ensure, Destination: destination, SkipBrowser: true})
		results <- err
	}
	go ensure()
	receive(t, launcher.started, "the first OpenSSH start")
	go ensure()
	receive(t, waiting, "a request waiting for the attempt")

	if err := manager.Close(); err != nil {
		t.Fatal(err)
	}
	var waited error
	for range 2 {
		select {
		case err := <-results:
			if err != nil && err.Error() == "session manager is closed" {
				waited = err
			}
		case <-time.After(5 * time.Second):
			t.Fatal("a request kept waiting after shutdown")
		}
	}
	if waited == nil {
		t.Fatal("the waiting request did not report that the manager closed")
	}
}
