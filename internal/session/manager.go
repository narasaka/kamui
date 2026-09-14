package session

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/netip"
	"sort"
	"sync"
	"time"

	"github.com/narasaka/kamui/internal/browser"
	"github.com/narasaka/kamui/internal/mirror"
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
	LoopbackMode      proxy.LoopbackMode
	Unattended        bool
	EnableMirror      bool
	PortPolicy        *mirror.PortPolicy
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
	Destination  Destination
	State        SessionState
	Proxy        netip.AddrPort
	LoopbackMode proxy.LoopbackMode
	LastError    error
	Browser      string
	Mirror       mirror.Status
}

// ManagerOptions supplies session implementations and profile storage.
type ManagerOptions struct {
	Transport        ssh.Transport
	Browsers         *browser.Catalog
	ProfileRoot      string
	Logger           *slog.Logger
	ProxyAddresses   ProxyAddressStore
	MirrorDiscoverer mirror.Discoverer
	MirrorInterval   time.Duration
}

// ProxyAddressStore persists one loopback proxy endpoint per exact destination.
type ProxyAddressStore interface {
	PreviousProxy(Destination) (netip.AddrPort, error)
	RememberProxy(Destination, netip.AddrPort) error
}

// Result is the outcome of a lifecycle command.
type Result struct {
	Session  SessionStatus
	Sessions []SessionStatus
}

// Manager owns all in-process host sessions.
type Manager struct {
	mu               sync.Mutex
	transport        ssh.Transport
	browsers         *browser.Catalog
	profileRoot      string
	ctx              context.Context
	cancel           context.CancelFunc
	sessions         map[string]*managedSession
	closed           bool
	logger           *slog.Logger
	proxyAddresses   ProxyAddressStore
	mirrorDiscoverer mirror.Discoverer
	mirrorInterval   time.Duration
}

type managedSession struct {
	mu              sync.RWMutex
	ctx             context.Context
	cancel          context.CancelFunc
	destination     Destination
	connection      *ssh.Connection
	proxy           *proxy.RunningProxy
	loopbackMode    proxy.LoopbackMode
	lastError       error
	requiresAuth    bool
	browserAdapter  browser.Adapter
	browserProfile  browser.Profile
	idleTimeout     time.Duration
	stopBrowser     bool
	reconnectPaused bool
	mirror          *mirror.Running
}

// NewManager creates an empty session manager.
func NewManager(transport ssh.Transport) *Manager {
	return NewManagerWithOptions(ManagerOptions{Transport: transport})
}

// NewManagerWithOptions creates a manager with browser lifecycle support.
func NewManagerWithOptions(options ManagerOptions) *Manager {
	ctx, cancel := context.WithCancel(context.Background())
	logger := options.Logger
	if logger == nil {
		logger = slog.New(slog.NewJSONHandler(io.Discard, nil))
	}
	return &Manager{
		transport:        options.Transport,
		browsers:         options.Browsers,
		profileRoot:      options.ProfileRoot,
		ctx:              ctx,
		cancel:           cancel,
		sessions:         make(map[string]*managedSession),
		logger:           logger,
		proxyAddresses:   options.ProxyAddresses,
		mirrorDiscoverer: options.MirrorDiscoverer,
		mirrorInterval:   options.MirrorInterval,
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
			connection, err := m.connect(m.ctx, destination, command.Unattended)
			if err != nil {
				return Result{}, err
			}
			existing.mu.Lock()
			old := existing.connection
			existing.connection = connection
			existing.lastError = nil
			existing.requiresAuth = false
			existing.reconnectPaused = false
			existing.mu.Unlock()
			_ = old.Close()
		}
		if err := m.addCapabilities(ctx, existing, command); err != nil {
			return Result{}, err
		}
		return Result{Session: existing.snapshot()}, nil
	}

	var listenAddress netip.AddrPort
	var err error
	if m.proxyAddresses != nil {
		listenAddress, err = m.proxyAddresses.PreviousProxy(destination)
		if err != nil {
			return Result{}, fmt.Errorf("read saved proxy address: %w", err)
		}
	}
	connection, err := m.connect(m.ctx, destination, command.Unattended)
	if err != nil {
		m.logger.Warn("Kamui session lifecycle", "event", "ssh_bootstrap_failed", "session_key", destination.Key(), "failure_kind", connectFailureKind(err))
		return Result{}, fmt.Errorf("SSH authentication or connection failed for %s: %w", destination, err)
	}
	sessionContext, cancel := context.WithCancel(m.ctx)
	managed := &managedSession{
		ctx: sessionContext, cancel: cancel, destination: destination,
		connection: connection, loopbackMode: command.LoopbackMode,
		idleTimeout: command.IdleTimeout, stopBrowser: command.StopBrowserOnStop,
	}
	runningProxy, err := proxy.Start(m.ctx, proxy.Dialers{
		Direct: (&net.Dialer{}).DialContext,
		Remote: func(ctx context.Context, network, address string) (net.Conn, error) {
			return managed.dial(ctx, network, address)
		},
	}, proxy.Options{ListenAddress: listenAddress, LoopbackMode: command.LoopbackMode})
	if err != nil {
		_ = connection.Close()
		return Result{}, err
	}
	managed.proxy = runningProxy
	if m.proxyAddresses != nil {
		if err := m.proxyAddresses.RememberProxy(destination, runningProxy.Addr()); err != nil {
			_ = managed.close()
			return Result{}, fmt.Errorf("remember proxy address: %w", err)
		}
	}
	if err := m.addCapabilities(ctx, managed, command); err != nil {
		_ = managed.close()
		return Result{}, err
	}
	m.sessions[destination.Key()] = managed
	m.logger.Info("Kamui session lifecycle", "event", "session_started", "session_key", destination.Key(), "browser", managed.snapshot().Browser)
	go m.monitor(managed)
	return Result{Session: managed.snapshot()}, nil
}

func (m *Manager) addCapabilities(ctx context.Context, managed *managedSession, command Command) error {
	managed.mu.RLock()
	runningMirror := managed.mirror
	hasMirror := runningMirror != nil
	hasBrowser := managed.browserAdapter != nil
	runningProxy := managed.proxy
	managed.mu.RUnlock()
	if command.EnableMirror && !hasMirror {
		discoverer := m.mirrorDiscoverer
		if discoverer == nil {
			discoverer = mirror.SSHDiscoverer{SSHPath: m.transport.SSHPath}
		}
		runningMirror, err := mirror.Start(managed.ctx, managed.destination.String(), discoverer, func(ctx context.Context, network, address string) (net.Conn, error) {
			return managed.dial(ctx, network, address)
		}, mirror.Options{ReconcileInterval: m.mirrorInterval, PortPolicy: command.PortPolicy})
		if err != nil {
			return err
		}
		managed.mu.Lock()
		managed.mirror = runningMirror
		managed.mu.Unlock()
	} else if command.EnableMirror && command.PortPolicy != nil {
		if err := runningMirror.SetPortPolicy(*command.PortPolicy); err != nil {
			return err
		}
	}
	if m.browsers != nil && !command.SkipBrowser && !hasBrowser {
		selected, err := m.browsers.Select(ctx, command.Browser)
		if err != nil {
			return err
		}
		profile, err := selected.Adapter.PrepareProfile(ctx, browser.Session{
			Key:         managed.destination.Key(),
			Proxy:       runningProxy.Addr(),
			ProfileRoot: m.profileRoot,
		}, selected.Installation)
		if err != nil {
			return err
		}
		if err := selected.Adapter.Launch(ctx, profile, command.URLs); err != nil {
			return err
		}
		managed.mu.Lock()
		managed.browserAdapter = selected.Adapter
		managed.browserProfile = profile
		managed.mu.Unlock()
	}
	return nil
}

func (m *Manager) connect(ctx context.Context, destination Destination, unattended bool) (*ssh.Connection, error) {
	if unattended {
		return m.transport.ConnectUnattended(ctx, destination.String())
	}
	return m.transport.Connect(ctx, destination.String())
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
	m.logger.Info("Kamui session lifecycle", "event", "session_stopped", "session_key", key)
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
		Destination:  s.destination,
		State:        state,
		Proxy:        s.proxy.Addr(),
		LoopbackMode: s.loopbackMode,
		LastError:    errors.Join(transport.Error, s.lastError),
	}
	if s.browserAdapter != nil {
		status.Browser = s.browserAdapter.ID()
	}
	if s.mirror != nil {
		status.Mirror = s.mirror.Status()
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
	runningMirror := s.mirror
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
	var mirrorErr error
	if runningMirror != nil {
		mirrorErr = runningMirror.Close()
	}
	return errors.Join(browserErr, mirrorErr, runningProxy.Close(), connection.Close())
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
		managed.mu.RLock()
		reconnectPaused := managed.reconnectPaused
		managed.mu.RUnlock()
		if reconnectPaused {
			continue
		}

		delay := 100 * time.Millisecond
		reconnected := false
		for attempt := 0; attempt < attempts; attempt++ {
			m.logger.Info("Kamui session lifecycle", "event", "ssh_reconnect_attempt", "session_key", managed.destination.Key(), "attempt", attempt+1)
			connection, err := m.transport.ConnectUnattended(managed.ctx, managed.destination.String())
			if err == nil {
				managed.mu.Lock()
				old := managed.connection
				managed.connection = connection
				managed.lastError = nil
				managed.reconnectPaused = false
				managed.mu.Unlock()
				_ = old.Close()
				m.logger.Info("Kamui session lifecycle", "event", "ssh_reconnected", "session_key", managed.destination.Key())
				reconnected = true
				break
			}
			managed.mu.Lock()
			managed.lastError = err
			var connectError *ssh.ConnectError
			if errors.As(err, &connectError) && connectError.Kind != ssh.TransientFailure {
				managed.requiresAuth = true
				managed.reconnectPaused = true
				managed.mu.Unlock()
				m.logger.Warn("Kamui session lifecycle", "event", "ssh_authentication_required", "session_key", managed.destination.Key(), "failure_kind", connectFailureKind(err))
				break
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
		if !reconnected {
			managed.mu.Lock()
			managed.reconnectPaused = true
			managed.mu.Unlock()
		}
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
	m.logger.Info("Kamui session lifecycle", "event", "session_expired", "session_key", key)
	_ = managed.close()
}

func connectFailureKind(err error) string {
	var connectError *ssh.ConnectError
	if !errors.As(err, &connectError) {
		return "unknown"
	}
	switch connectError.Kind {
	case ssh.AuthenticationFailure:
		return "authentication"
	case ssh.ConfigurationFailure:
		return "configuration"
	default:
		return "transient"
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
