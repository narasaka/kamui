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
	"sync/atomic"
	"time"
)

// DefaultReconcileInterval paces discovery for polling discoverers. With a
// Watcher it paces local conflict retries and stream restarts instead, because
// listener changes arrive as soon as the remote host reports them.
const DefaultReconcileInterval = 5 * time.Second

// DefaultDiscoveryTimeout bounds each polling discovery subprocess and the
// silence between reports from a Watcher.
const DefaultDiscoveryTimeout = 10 * time.Second

// Discoverer finds TCP ports currently listening on one SSH destination.
type Discoverer interface {
	ListeningPorts(context.Context, string) ([]uint16, error)
}

// Watcher streams listener reports over one long-lived discovery process. It
// calls report with each pass's listener set or discovery error, including
// unchanged passes, and returns when the process exits or ctx ends.
type Watcher interface {
	WatchListeningPorts(ctx context.Context, destination string, report func([]uint16, error)) error
}

// DiagnosticDiscoverer can stream subprocess diagnostics during discovery.
type DiagnosticDiscoverer interface {
	Discoverer
	WithDiagnostics(io.Writer) Discoverer
}

// DialFunc opens a TCP connection through the destination's existing SSH
// transport.
type DialFunc func(context.Context, string, string) (net.Conn, error)

// Options controls reconciliation and port selection. A zero interval uses
// DefaultReconcileInterval. A nil policy uses DefaultPortPolicy.
type Options struct {
	ReconcileInterval time.Duration
	DiscoveryTimeout  time.Duration
	PortPolicy        *PortPolicy
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
	ExcludedPorts   []uint16
	ConflictedPorts []Conflict
	LastError       error
}

// Running continuously reconciles one destination's listeners.
type Running struct {
	ctx              context.Context
	cancel           context.CancelFunc
	destination      string
	discoverer       Discoverer
	watcher          Watcher
	dial             DialFunc
	interval         time.Duration
	discoveryTimeout time.Duration
	portPolicy       PortPolicy

	reconcileMu      sync.Mutex
	mu               sync.Mutex
	forwards         map[uint16]*portForward
	excluded         map[uint16]struct{}
	conflicts        map[uint16]string
	forwardingErrors map[uint16]error
	discovered       []uint16
	hasDiscovered    bool
	discoveryErr     error
	closed           bool
	workers          sync.WaitGroup
}

// Start performs an initial discovery and begins continuous reconciliation.
// A discoverer that is also a Watcher keeps one discovery process open and
// reconciles on each changed report; others are polled. Discovery and bind
// failures are status conditions, not whole-session failures.
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
	portPolicy := DefaultPortPolicy()
	if options.PortPolicy != nil {
		portPolicy = *options.PortPolicy
	}
	runningContext, cancel := context.WithCancel(ctx)
	running := &Running{
		ctx: runningContext, cancel: cancel, destination: destination,
		discoverer: discoverer, dial: dial, interval: interval, discoveryTimeout: discoveryTimeout, portPolicy: portPolicy,
		forwards: make(map[uint16]*portForward), excluded: make(map[uint16]struct{}), conflicts: make(map[uint16]string), forwardingErrors: make(map[uint16]error),
	}
	if watcher, ok := discoverer.(Watcher); ok {
		running.watcher = watcher
		initial := make(chan struct{})
		var once sync.Once
		running.workers.Add(1)
		go running.watch(func() { once.Do(func() { close(initial) }) })
		<-initial
	} else {
		running.reconcile()
	}
	running.workers.Add(1)
	go running.loop()
	return running, nil
}

func (r *Running) loop() {
	defer r.workers.Done()
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

// watch keeps one discovery stream running. A stream that exits or stays
// silent for the discovery timeout is restarted after one interval; the last
// valid listener set stays mirrored meanwhile. initial is called after the
// first report or the first stream failure.
func (r *Running) watch(initial func()) {
	defer r.workers.Done()
	defer initial()
	for {
		err := r.watchOnce(initial)
		if r.ctx.Err() != nil {
			return
		}
		r.recordDiscoveryError(err)
		initial()
		timer := time.NewTimer(r.interval)
		select {
		case <-r.ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

func (r *Running) watchOnce(reported func()) error {
	streamContext, cancel := context.WithCancel(r.ctx)
	defer cancel()
	var stalled atomic.Bool
	watchdog := time.AfterFunc(r.discoveryTimeout, func() {
		stalled.Store(true)
		cancel()
	})
	defer watchdog.Stop()
	err := r.watcher.WatchListeningPorts(streamContext, r.destination, func(ports []uint16, err error) {
		if stalled.Load() {
			return
		}
		watchdog.Reset(r.discoveryTimeout)
		r.applyReport(ports, err)
		reported()
	})
	if stalled.Load() {
		return fmt.Errorf("no listener report within %s: %w", r.discoveryTimeout, context.DeadlineExceeded)
	}
	if err == nil {
		err = errors.New("remote listener discovery stream ended")
	}
	return err
}

// applyReport reconciles a streamed report unless it repeats the current
// healthy listener set.
func (r *Running) applyReport(ports []uint16, err error) {
	r.reconcileMu.Lock()
	defer r.reconcileMu.Unlock()
	if err == nil {
		r.mu.Lock()
		unchanged := r.hasDiscovered && r.discoveryErr == nil && slices.Equal(r.discovered, ports)
		r.mu.Unlock()
		if unchanged {
			return
		}
	}
	r.applyDiscovery(ports, err)
}

func (r *Running) reconcile() {
	r.reconcileMu.Lock()
	defer r.reconcileMu.Unlock()
	r.reconcileOnce()
}

// reconcileOnce polls the discoverer, or with a Watcher reapplies the last
// streamed listener set so conflicted ports are retried without a remote
// command.
func (r *Running) reconcileOnce() {
	if r.watcher != nil {
		r.mu.Lock()
		ports, ok := slices.Clone(r.discovered), r.hasDiscovered
		r.mu.Unlock()
		if ok {
			_ = r.applyPorts(ports)
		}
		return
	}
	discoveryContext, cancel := context.WithTimeout(r.ctx, r.discoveryTimeout)
	ports, err := r.discoverer.ListeningPorts(discoveryContext, r.destination)
	cancel()
	r.applyDiscovery(ports, err)
}

func (r *Running) recordDiscoveryError(err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.closed {
		r.discoveryErr = fmt.Errorf("discover remote TCP listeners: %w", err)
	}
}

// applyDiscovery records one discovery result. A failed discovery keeps the
// last valid listener set.
func (r *Running) applyDiscovery(ports []uint16, err error) {
	if err != nil {
		r.recordDiscoveryError(err)
		return
	}
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return
	}
	r.discovered = slices.Clone(ports)
	r.hasDiscovered = true
	r.mu.Unlock()
	err = r.applyPorts(ports)
	r.mu.Lock()
	if !r.closed {
		r.discoveryErr = err
	}
	r.mu.Unlock()
}

// applyPorts makes the forwards match ports under the current policy and
// returns any invalid discovered port.
func (r *Running) applyPorts(ports []uint16) error {
	var err error
	desired := make(map[uint16]struct{}, len(ports))
	excluded := make(map[uint16]struct{})
	for _, port := range ports {
		if port == 0 {
			err = errors.Join(err, fmt.Errorf("discovery returned invalid TCP port 0"))
			continue
		}
		if r.portPolicy.Excludes(port) {
			excluded[port] = struct{}{}
			continue
		}
		desired[port] = struct{}{}
	}

	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return err
	}
	r.excluded = excluded
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
		return err
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
	return err
}

// SetPortPolicy replaces the effective policy and immediately reconciles the
// currently discovered listeners. Polling discoverers rediscover first.
func (r *Running) SetPortPolicy(policy PortPolicy) error {
	r.reconcileMu.Lock()
	defer r.reconcileMu.Unlock()
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return fmt.Errorf("mirror is closed")
	}
	r.portPolicy = policy
	r.mu.Unlock()
	r.reconcileOnce()
	return nil
}

// Status returns a sorted snapshot safe for display or wire transport.
func (r *Running) Status() Status {
	r.mu.Lock()
	defer r.mu.Unlock()
	status := Status{Enabled: !r.closed, LastError: r.discoveryErr}
	for port := range r.forwards {
		status.MirroredPorts = append(status.MirroredPorts, port)
	}
	for port := range r.excluded {
		status.ExcludedPorts = append(status.ExcludedPorts, port)
	}
	for port, message := range r.conflicts {
		status.ConflictedPorts = append(status.ConflictedPorts, Conflict{Port: port, Error: message})
	}
	sort.Slice(status.MirroredPorts, func(i, j int) bool { return status.MirroredPorts[i] < status.MirroredPorts[j] })
	sort.Slice(status.ExcludedPorts, func(i, j int) bool { return status.ExcludedPorts[i] < status.ExcludedPorts[j] })
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
	r.excluded = make(map[uint16]struct{})
	r.conflicts = make(map[uint16]string)
	r.forwardingErrors = make(map[uint16]error)
	r.discovered = nil
	r.hasDiscovered = false
	r.mu.Unlock()

	var joined error
	for _, forward := range forwards {
		joined = errors.Join(joined, forward.close())
	}
	r.workers.Wait()
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
