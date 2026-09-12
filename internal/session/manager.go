package session

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"sort"
	"sync"
	"time"

	"github.com/narasaka/kamui/internal/browser"
	"github.com/narasaka/kamui/internal/proxy"
	"github.com/narasaka/kamui/internal/ssh"
)

// Operation identifies a lifecycle command.
type Operation uint8

const (
	Ensure Operation = iota
	Status
	Stop
	Open
	StopAll
)

// Command is one request against the session lifecycle interface.
type Command struct {
	Operation         Operation
	Destination       Destination
	Browser           browser.Selection
	URLs              []string
	SkipBrowser       bool
	IdleTimeout       time.Duration
	StopBrowserOnStop bool
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
	Browser     string
}

// ManagerOptions supplies session implementations and profile storage.
type ManagerOptions struct {
	Transport   ssh.Transport
	Browsers    *browser.Catalog
	ProfileRoot string
}

// Result is the outcome of a lifecycle command.
type Result struct {
	Session  SessionStatus
	Sessions []SessionStatus
}

// Manager owns all in-process host sessions.
type Manager struct {
	mu          sync.Mutex
	transport   ssh.Transport
	browsers    *browser.Catalog
	profileRoot string
	ctx         context.Context
	cancel      context.CancelFunc
	sessions    map[string]*managedSession
	closed      bool
}

type managedSession struct {
	mu             sync.RWMutex
	ctx            context.Context
	cancel         context.CancelFunc
	destination    Destination
	connection     *ssh.Connection
	proxy          *proxy.RunningProxy
	lastError      error
	requiresAuth   bool
	browserAdapter browser.Adapter
	browserProfile browser.Profile
	idleTimeout    time.Duration
	stopBrowser    bool
}

// NewManager creates an empty session manager.
func NewManager(transport ssh.Transport) *Manager {
	return NewManagerWithOptions(ManagerOptions{Transport: transport})
}

// NewManagerWithOptions creates a manager with browser lifecycle support.
func NewManagerWithOptions(options ManagerOptions) *Manager {
	ctx, cancel := context.WithCancel(context.Background())
	return &Manager{
		transport:   options.Transport,
		browsers:    options.Browsers,
		profileRoot: options.ProfileRoot,
		ctx:         ctx,
		cancel:      cancel,
		sessions:    make(map[string]*managedSession),
	}
}

// Execute applies one lifecycle command.
func (m *Manager) Execute(ctx context.Context, command Command) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	switch command.Operation {
	case Ensure:
		return m.ensure(ctx, command)
	case Status:
		return m.status(command.Destination)
	case Stop:
		return Result{}, m.stop(command.Destination)
	case Open:
		return m.open(ctx, command)
	case StopAll:
		return Result{}, m.stopAll()
	default:
		return Result{}, fmt.Errorf("unknown session operation %d", command.Operation)
	}
}

func (m *Manager) ensure(ctx context.Context, command Command) (Result, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	destination := command.Destination
	if m.closed {
		return Result{}, errors.New("session manager is closed")
	}
	if existing := m.sessions[destination.Key()]; existing != nil {
		state := existing.snapshot().State
		if state == SessionUnavailable || state == SessionAuthenticationRequired {
			connection, err := m.transport.Connect(m.ctx, destination.String())
			if err != nil {
				return Result{}, err
			}
			existing.mu.Lock()
			old := existing.connection
			existing.connection = connection
			existing.lastError = nil
			existing.requiresAuth = false
			existing.mu.Unlock()
			_ = old.Close()
		}
		return Result{Session: existing.snapshot()}, nil
	}

	connection, err := m.transport.Connect(m.ctx, destination.String())
	if err != nil {
		return Result{}, fmt.Errorf("SSH authentication or connection failed for %s: %w", destination, err)
	}
	sessionContext, cancel := context.WithCancel(m.ctx)
	managed := &managedSession{
		ctx: sessionContext, cancel: cancel, destination: destination,
		connection: connection, idleTimeout: command.IdleTimeout, stopBrowser: command.StopBrowserOnStop,
	}
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
	if m.browsers != nil && !command.SkipBrowser {
		selected, err := m.browsers.Select(ctx, command.Browser)
		if err != nil {
			_ = managed.close()
			return Result{}, err
		}
		profile, err := selected.Adapter.PrepareProfile(ctx, browser.Session{
			Key:         destination.Key(),
			Proxy:       runningProxy.Addr(),
			ProfileRoot: m.profileRoot,
		}, selected.Installation)
		if err != nil {
			_ = managed.close()
			return Result{}, err
		}
		if err := selected.Adapter.Launch(ctx, profile, command.URLs); err != nil {
			_ = managed.close()
			return Result{}, err
		}
		managed.browserAdapter = selected.Adapter
		managed.browserProfile = profile
	}
	m.sessions[destination.Key()] = managed
	go m.monitor(managed)
	return Result{Session: managed.snapshot()}, nil
}

func (m *Manager) open(ctx context.Context, command Command) (Result, error) {
	m.mu.Lock()
	managed := m.sessions[command.Destination.Key()]
	m.mu.Unlock()
	if managed == nil {
		return Result{}, fmt.Errorf("no Kamui session for %s", command.Destination)
	}
	managed.mu.RLock()
	adapter := managed.browserAdapter
	profile := managed.browserProfile
	managed.mu.RUnlock()
	if adapter == nil {
		return Result{}, fmt.Errorf("session for %s has no development browser", command.Destination)
	}
	if err := adapter.Open(ctx, profile, command.URLs); err != nil {
		return Result{}, err
	}
	return Result{Session: managed.snapshot()}, nil
}

func (m *Manager) status(destination Destination) (Result, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if destination.String() == "" {
		result := Result{Sessions: make([]SessionStatus, 0, len(m.sessions))}
		for _, managed := range m.sessions {
			result.Sessions = append(result.Sessions, managed.snapshot())
		}
		sort.Slice(result.Sessions, func(i, j int) bool {
			return result.Sessions[i].Destination.String() < result.Sessions[j].Destination.String()
		})
		return result, nil
	}
	managed := m.sessions[destination.Key()]
	if managed == nil {
		return Result{}, fmt.Errorf("no Kamui session for %s", destination)
	}
	return Result{Session: managed.snapshot()}, nil
}

func (m *Manager) stopAll() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	var joined error
	for key, managed := range m.sessions {
		joined = errors.Join(joined, managed.close())
		delete(m.sessions, key)
	}
	return joined
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
	status := SessionStatus{
		Destination: s.destination,
		State:       state,
		Proxy:       s.proxy.Addr(),
		LastError:   errors.Join(transport.Error, s.lastError),
	}
	if s.browserAdapter != nil {
		status.Browser = s.browserAdapter.ID()
	}
	return status
}

func (s *managedSession) dial(ctx context.Context, network, address string) (net.Conn, error) {
	s.mu.RLock()
	connection := s.connection
	s.mu.RUnlock()
	return connection.DialContext(ctx, network, address)
}

func (s *managedSession) close() error {
	s.mu.RLock()
	connection := s.connection
	runningProxy := s.proxy
	adapter := s.browserAdapter
	profile := s.browserProfile
	stopBrowser := s.stopBrowser
	s.mu.RUnlock()
	var browserErr error
	if stopBrowser && adapter != nil {
		if closer, ok := adapter.(interface {
			Close(context.Context, browser.Profile) error
		}); ok {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			browserErr = closer.Close(ctx, profile)
			cancel()
		}
	}
	s.cancel()
	return errors.Join(browserErr, runningProxy.Close(), connection.Close())
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
		activity := managed.proxy.Activity()
		if managed.idleTimeout > 0 && activity.Connections == 0 && time.Since(activity.LastActivity) >= managed.idleTimeout {
			m.expire(managed)
			return
		}
		if managed.snapshot().State != SessionUnavailable {
			continue
		}

		delay := 100 * time.Millisecond
		for attempt := 0; attempt < attempts; attempt++ {
			connection, err := m.transport.ConnectUnattended(managed.ctx, managed.destination.String())
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
			var connectError *ssh.ConnectError
			if errors.As(err, &connectError) && connectError.Kind != ssh.TransientFailure {
				managed.requiresAuth = true
				managed.mu.Unlock()
				return
			}
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
		return
	}
}

func (m *Manager) expire(managed *managedSession) {
	m.mu.Lock()
	key := managed.destination.Key()
	if m.sessions[key] != managed {
		m.mu.Unlock()
		return
	}
	delete(m.sessions, key)
	m.mu.Unlock()
	_ = managed.close()
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
