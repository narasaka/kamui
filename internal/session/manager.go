package session

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"sync"
	"time"

	"github.com/narasaka/kamui/internal/proxy"
	"github.com/narasaka/kamui/internal/ssh"
)

// Operation identifies a lifecycle command.
type Operation uint8

const (
	Ensure Operation = iota
	Status
	Stop
)

// Command is one request against the session lifecycle interface.
type Command struct {
	Operation   Operation
	Destination Destination
}

// SessionState is the user-visible lifecycle state.
type SessionState uint8

const (
	SessionConnecting SessionState = iota
	SessionConnected
	SessionUnavailable
	SessionAuthenticationRequired
)

// SessionStatus is the observable state for one exact destination.
type SessionStatus struct {
	Destination Destination
	State       SessionState
	Proxy       netip.AddrPort
	LastError   error
}

// Result is the outcome of a lifecycle command.
type Result struct {
	Session  SessionStatus
	Sessions []SessionStatus
}

// Manager owns all in-process host sessions.
type Manager struct {
	mu        sync.Mutex
	transport ssh.Transport
	ctx       context.Context
	cancel    context.CancelFunc
	sessions  map[string]*managedSession
	closed    bool
}

type managedSession struct {
	mu           sync.RWMutex
	ctx          context.Context
	cancel       context.CancelFunc
	destination  Destination
	connection   *ssh.Connection
	proxy        *proxy.RunningProxy
	lastError    error
	requiresAuth bool
}

// NewManager creates an empty session manager.
func NewManager(transport ssh.Transport) *Manager {
	ctx, cancel := context.WithCancel(context.Background())
	return &Manager{
		transport: transport,
		ctx:       ctx,
		cancel:    cancel,
		sessions:  make(map[string]*managedSession),
	}
}

// Execute applies one lifecycle command.
func (m *Manager) Execute(ctx context.Context, command Command) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	switch command.Operation {
	case Ensure:
		return m.ensure(command.Destination)
	case Status:
		return m.status(command.Destination)
	case Stop:
		return Result{}, m.stop(command.Destination)
	default:
		return Result{}, fmt.Errorf("unknown session operation %d", command.Operation)
	}
}

func (m *Manager) ensure(destination Destination) (Result, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return Result{}, errors.New("session manager is closed")
	}
	if existing := m.sessions[destination.Key()]; existing != nil {
		return Result{Session: existing.snapshot()}, nil
	}

	connection, err := m.transport.Connect(m.ctx, destination.String())
	if err != nil {
		return Result{}, fmt.Errorf("SSH authentication or connection failed for %s: %w", destination, err)
	}
	sessionContext, cancel := context.WithCancel(m.ctx)
	managed := &managedSession{ctx: sessionContext, cancel: cancel, destination: destination, connection: connection}
	runningProxy, err := proxy.Start(m.ctx, proxy.Dialers{
		Direct: (&net.Dialer{}).DialContext,
		Remote: func(ctx context.Context, network, address string) (net.Conn, error) {
			return managed.dial(ctx, network, address)
		},
	}, proxy.Options{})
	if err != nil {
		_ = connection.Close()
		return Result{}, err
	}
	managed.proxy = runningProxy
	m.sessions[destination.Key()] = managed
	go m.monitor(managed)
	return Result{Session: managed.snapshot()}, nil
}

func (m *Manager) status(destination Destination) (Result, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	managed := m.sessions[destination.Key()]
	if managed == nil {
		return Result{}, fmt.Errorf("no Kamui session for %s", destination)
	}
	return Result{Session: managed.snapshot()}, nil
}

func (m *Manager) stop(destination Destination) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := destination.Key()
	managed := m.sessions[key]
	if managed == nil {
		return fmt.Errorf("no Kamui session for %s", destination)
	}
	delete(m.sessions, key)
	return managed.close()
}

func (s *managedSession) snapshot() SessionStatus {
	s.mu.RLock()
	defer s.mu.RUnlock()
	transport := s.connection.Snapshot()
	state := SessionUnavailable
	if s.requiresAuth {
		state = SessionAuthenticationRequired
	} else if transport.State == ssh.Connected {
		state = SessionConnected
	} else if transport.State == ssh.Connecting {
		state = SessionConnecting
	}
	return SessionStatus{
		Destination: s.destination,
		State:       state,
		Proxy:       s.proxy.Addr(),
		LastError:   errors.Join(transport.Error, s.lastError),
	}
}

func (s *managedSession) dial(ctx context.Context, network, address string) (net.Conn, error) {
	s.mu.RLock()
	connection := s.connection
	s.mu.RUnlock()
	return connection.DialContext(ctx, network, address)
}

func (s *managedSession) close() error {
	s.cancel()
	s.mu.RLock()
	connection := s.connection
	runningProxy := s.proxy
	s.mu.RUnlock()
	return errors.Join(runningProxy.Close(), connection.Close())
}

func (m *Manager) monitor(managed *managedSession) {
	const attempts = 5
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-managed.ctx.Done():
			return
		case <-ticker.C:
		}
		if managed.snapshot().State != SessionUnavailable {
			continue
		}

		delay := 100 * time.Millisecond
		for attempt := 0; attempt < attempts; attempt++ {
			connection, err := m.transport.Connect(managed.ctx, managed.destination.String())
			if err == nil {
				managed.mu.Lock()
				old := managed.connection
				managed.connection = connection
				managed.lastError = nil
				managed.mu.Unlock()
				_ = old.Close()
				break
			}
			managed.mu.Lock()
			managed.lastError = err
			managed.mu.Unlock()
			select {
			case <-managed.ctx.Done():
				return
			case <-time.After(delay):
			}
			if delay < 2*time.Second {
				delay *= 2
			}
		}
	}
}

// Close stops all proxies and child processes.
func (m *Manager) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil
	}
	m.closed = true
	m.cancel()
	var joined error
	for key, managed := range m.sessions {
		joined = errors.Join(joined, managed.close())
		delete(m.sessions, key)
	}
	return joined
}
