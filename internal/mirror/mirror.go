// Package mirror exposes remote TCP listeners as same-numbered listeners on
// local IPv4 and IPv6 loopback.
package mirror

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"slices"
	"sort"
	"sync"
	"time"
)

// DefaultReconcileInterval is deliberately conservative: it notices normal
// development-server changes promptly without continuously spawning remote
// discovery commands.
const DefaultReconcileInterval = 5 * time.Second

// DefaultDiscoveryTimeout bounds each remote discovery subprocess.
const DefaultDiscoveryTimeout = 10 * time.Second

// Discoverer finds TCP ports currently listening on one SSH destination.
type Discoverer interface {
	ListeningPorts(context.Context, string) ([]uint16, error)
}

// DialFunc opens a TCP connection through the destination's existing SSH
// transport.
type DialFunc func(context.Context, string, string) (net.Conn, error)

// Options controls reconciliation. A zero interval uses
// DefaultReconcileInterval.
type Options struct {
	ReconcileInterval time.Duration
	DiscoveryTimeout  time.Duration
}

// Conflict describes a desired remote port that could not be claimed locally.
type Conflict struct {
	Port  uint16
	Error string
}

// Status is the current observable mirror state.
type Status struct {
	Enabled         bool
	MirroredPorts   []uint16
	ConflictedPorts []Conflict
	LastError       error
}

// Running continuously reconciles one destination's listeners.
type Running struct {
	ctx              context.Context
	cancel           context.CancelFunc
	destination      string
	discoverer       Discoverer
	dial             DialFunc
	interval         time.Duration
	discoveryTimeout time.Duration

	mu               sync.Mutex
	forwards         map[uint16]*portForward
	conflicts        map[uint16]string
	forwardingErrors map[uint16]error
	lastError        error
	closed           bool
	done             chan struct{}
}

// Start performs an initial discovery and begins continuous reconciliation.
// Discovery and bind failures are status conditions, not whole-session
// failures.
func Start(ctx context.Context, destination string, discoverer Discoverer, dial DialFunc, options Options) (*Running, error) {
	if discoverer == nil || dial == nil {
		return nil, fmt.Errorf("mirror requires a listener discoverer and SSH dialer")
	}
	interval := options.ReconcileInterval
	if interval == 0 {
		interval = DefaultReconcileInterval
	}
	if interval < 0 {
		return nil, fmt.Errorf("mirror reconciliation interval must be positive")
	}
	discoveryTimeout := options.DiscoveryTimeout
	if discoveryTimeout == 0 {
		discoveryTimeout = DefaultDiscoveryTimeout
	}
	if discoveryTimeout < 0 {
		return nil, fmt.Errorf("mirror discovery timeout must be positive")
	}
	runningContext, cancel := context.WithCancel(ctx)
	running := &Running{
		ctx: runningContext, cancel: cancel, destination: destination,
		discoverer: discoverer, dial: dial, interval: interval, discoveryTimeout: discoveryTimeout,
		forwards: make(map[uint16]*portForward), conflicts: make(map[uint16]string), forwardingErrors: make(map[uint16]error), done: make(chan struct{}),
	}
	running.reconcile()
	go running.loop()
	return running, nil
}

func (r *Running) loop() {
	defer close(r.done)
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()
	for {
		select {
		case <-r.ctx.Done():
			return
		case <-ticker.C:
			r.reconcile()
		}
	}
}

func (r *Running) reconcile() {
	discoveryContext, cancel := context.WithTimeout(r.ctx, r.discoveryTimeout)
	ports, err := r.discoverer.ListeningPorts(discoveryContext, r.destination)
	cancel()
	if err != nil {
		r.mu.Lock()
		if !r.closed {
			r.lastError = fmt.Errorf("discover remote TCP listeners: %w", err)
		}
		r.mu.Unlock()
		return
	}
	desired := make(map[uint16]struct{}, len(ports))
	for _, port := range ports {
		if port == 0 {
			err = errors.Join(err, fmt.Errorf("discovery returned invalid TCP port 0"))
			continue
		}
		desired[port] = struct{}{}
	}

	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return
	}
	r.lastError = err
	removed := make(map[uint16]*portForward)
	for port, forward := range r.forwards {
		if _, ok := desired[port]; ok {
			continue
		}
		removed[port] = forward
	}
	for port := range r.conflicts {
		if _, ok := desired[port]; !ok {
			delete(r.conflicts, port)
		}
	}
	r.mu.Unlock()
	for _, forward := range removed {
		_ = forward.close()
	}

	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return
	}
	for port, forward := range removed {
		if r.forwards[port] == forward {
			delete(r.forwards, port)
			delete(r.forwardingErrors, port)
		}
	}
	ordered := make([]uint16, 0, len(desired))
	for port := range desired {
		ordered = append(ordered, port)
	}
	sort.Slice(ordered, func(i, j int) bool { return ordered[i] < ordered[j] })
	for _, port := range ordered {
		if r.forwards[port] != nil {
			delete(r.conflicts, port)
			continue
		}
		forward, bindErr := bindPort(r.ctx, port, r.dial, func(err error) { r.recordForwardingError(port, err) })
		if bindErr != nil {
			r.conflicts[port] = bindErr.Error()
			continue
		}
		r.forwards[port] = forward
		delete(r.conflicts, port)
	}
	r.mu.Unlock()
}

// Status returns a sorted snapshot safe for display or wire transport.
func (r *Running) Status() Status {
	r.mu.Lock()
	defer r.mu.Unlock()
	status := Status{Enabled: !r.closed, LastError: r.lastError}
	for port := range r.forwards {
		status.MirroredPorts = append(status.MirroredPorts, port)
	}
	for port, message := range r.conflicts {
		status.ConflictedPorts = append(status.ConflictedPorts, Conflict{Port: port, Error: message})
	}
	sort.Slice(status.MirroredPorts, func(i, j int) bool { return status.MirroredPorts[i] < status.MirroredPorts[j] })
	sort.Slice(status.ConflictedPorts, func(i, j int) bool { return status.ConflictedPorts[i].Port < status.ConflictedPorts[j].Port })
	errorPorts := make([]uint16, 0, len(r.forwardingErrors))
	for port := range r.forwardingErrors {
		errorPorts = append(errorPorts, port)
	}
	sort.Slice(errorPorts, func(i, j int) bool { return errorPorts[i] < errorPorts[j] })
	for _, port := range errorPorts {
		status.LastError = errors.Join(status.LastError, fmt.Errorf("forward mirrored TCP port %d: %w", port, r.forwardingErrors[port]))
	}
	return status
}

func (r *Running) recordForwardingError(port uint16, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return
	}
	if r.forwards[port] == nil {
		return
	}
	if err == nil {
		delete(r.forwardingErrors, port)
		return
	}
	r.forwardingErrors[port] = err
}

// Close releases every listener and accepted connection.
func (r *Running) Close() error {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return nil
	}
	r.closed = true
	r.cancel()
	forwards := make([]*portForward, 0, len(r.forwards))
	for _, forward := range r.forwards {
		forwards = append(forwards, forward)
	}
	r.forwards = make(map[uint16]*portForward)
	r.conflicts = make(map[uint16]string)
	r.forwardingErrors = make(map[uint16]error)
	r.mu.Unlock()

	var joined error
	for _, forward := range forwards {
		joined = errors.Join(joined, forward.close())
	}
	<-r.done
	return joined
}

type portForward struct {
	ctx       context.Context
	cancel    context.CancelFunc
	port      uint16
	dial      DialFunc
	report    func(error)
	listeners []net.Listener
	mu        sync.Mutex
	active    map[*connectionPair]struct{}
	wait      sync.WaitGroup
	closeOnce sync.Once
	closeErr  error
}

type connectionPair struct {
	client net.Conn
	remote net.Conn
}

func bindPort(ctx context.Context, port uint16, dial DialFunc, report func(error)) (*portForward, error) {
	portText := fmt.Sprint(port)
	ipv4, err := net.Listen("tcp4", net.JoinHostPort("127.0.0.1", portText))
	if err != nil {
		return nil, fmt.Errorf("local port %d is occupied on 127.0.0.1: %w", port, err)
	}
	ipv6, err := net.Listen("tcp6", net.JoinHostPort("::1", portText))
	if err != nil {
		_ = ipv4.Close()
		return nil, fmt.Errorf("local port %d is occupied on ::1: %w", port, err)
	}
	forwardContext, cancel := context.WithCancel(ctx)
	forward := &portForward{
		ctx: forwardContext, cancel: cancel, port: port, dial: dial, report: report,
		listeners: []net.Listener{ipv4, ipv6}, active: make(map[*connectionPair]struct{}),
	}
	for _, listener := range forward.listeners {
		forward.wait.Add(1)
		go forward.accept(listener)
	}
	return forward, nil
}

func (f *portForward) accept(listener net.Listener) {
	defer f.wait.Done()
	for {
		client, err := listener.Accept()
		if err != nil {
			return
		}
		f.wait.Add(1)
		go func() {
			defer f.wait.Done()
			f.forward(client)
		}()
	}
}

func (f *portForward) forward(client net.Conn) {
	target4 := net.JoinHostPort("127.0.0.1", fmt.Sprint(f.port))
	remote, err := f.dial(f.ctx, "tcp", target4)
	if err != nil && f.ctx.Err() == nil {
		target6 := net.JoinHostPort("::1", fmt.Sprint(f.port))
		remote, err = f.dial(f.ctx, "tcp", target6)
	}
	if err != nil {
		f.report(err)
		_ = client.Close()
		return
	}
	f.report(nil)
	pair := &connectionPair{client: client, remote: remote}
	f.mu.Lock()
	if f.ctx.Err() != nil {
		f.mu.Unlock()
		_ = client.Close()
		_ = remote.Close()
		return
	}
	f.active[pair] = struct{}{}
	f.mu.Unlock()
	defer func() {
		_ = client.Close()
		_ = remote.Close()
		f.mu.Lock()
		delete(f.active, pair)
		f.mu.Unlock()
	}()

	done := make(chan struct{}, 2)
	copyStream := func(destination, source net.Conn) {
		_, _ = io.Copy(destination, source)
		if writer, ok := destination.(interface{ CloseWrite() error }); ok {
			_ = writer.CloseWrite()
		}
		done <- struct{}{}
	}
	go copyStream(remote, client)
	go copyStream(client, remote)
	<-done
	<-done
}

func (f *portForward) close() error {
	f.closeOnce.Do(func() {
		f.cancel()
		for _, listener := range f.listeners {
			if err := listener.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
				f.closeErr = errors.Join(f.closeErr, err)
			}
		}
		f.mu.Lock()
		pairs := slices.Collect(func(yield func(*connectionPair) bool) {
			for pair := range f.active {
				if !yield(pair) {
					return
				}
			}
		})
		for _, pair := range pairs {
			_ = pair.client.Close()
			_ = pair.remote.Close()
		}
		f.mu.Unlock()
		f.wait.Wait()
	})
	return f.closeErr
}
