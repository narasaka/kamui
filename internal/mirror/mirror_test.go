package mirror_test

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/narasaka/kamui/internal/mirror"
)

func TestRemoteListenerAppearsAndIsReachableOnIPv4AndIPv6(t *testing.T) {
	t.Parallel()

	backend := http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "mirrored HTTP")
	})}
	backendListener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = backend.Close() })
	go func() { _ = backend.Serve(backendListener) }()

	port := availableDualStackPort(t)
	discoverer := &mutableDiscoverer{}
	running, err := mirror.Start(context.Background(), "reyna", discoverer, func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "tcp4", backendListener.Addr().String())
	}, mirror.Options{ReconcileInterval: 10 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = running.Close() })

	discoverer.set(port)
	waitForMirrorStatus(t, running, func(status mirror.Status) bool {
		return slices.Contains(status.MirroredPorts, port)
	})
	for _, host := range []string{"127.0.0.1", "::1"} {
		connection, err := net.DialTimeout("tcp", net.JoinHostPort(host, fmt.Sprint(port)), time.Second)
		if err != nil {
			t.Fatalf("dial mirrored %s: %v", host, err)
		}
		_, _ = io.WriteString(connection, "GET / HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n")
		response, err := http.ReadResponse(bufio.NewReader(connection), nil)
		if err != nil {
			_ = connection.Close()
			t.Fatalf("read HTTP through %s: %v", host, err)
		}
		body, _ := io.ReadAll(response.Body)
		_ = response.Body.Close()
		_ = connection.Close()
		if string(body) != "mirrored HTTP" {
			t.Fatalf("body through %s = %q", host, body)
		}
	}
}

func TestExistingMacListenerWinsAndConflictIsReported(t *testing.T) {
	t.Parallel()

	local, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = local.Close() })
	port := uint16(local.Addr().(*net.TCPAddr).Port)
	discoverer := &mutableDiscoverer{ports: []uint16{port}}
	running, err := mirror.Start(context.Background(), "reyna", discoverer, unreachableDial, mirror.Options{ReconcileInterval: 10 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = running.Close() })

	status := waitForMirrorStatus(t, running, func(status mirror.Status) bool {
		return len(status.ConflictedPorts) == 1
	})
	if status.ConflictedPorts[0].Port != port || len(status.MirroredPorts) != 0 {
		t.Fatalf("status = %#v, want local conflict on %d", status, port)
	}
}

func TestRemoteListenerDisappearsAndReleasesLocalPort(t *testing.T) {
	t.Parallel()

	port := availableDualStackPort(t)
	discoverer := &mutableDiscoverer{ports: []uint16{port}}
	running, err := mirror.Start(context.Background(), "reyna", discoverer, unreachableDial, mirror.Options{ReconcileInterval: 10 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = running.Close() })
	waitForMirrorStatus(t, running, func(status mirror.Status) bool { return slices.Contains(status.MirroredPorts, port) })

	discoverer.set()
	waitForMirrorStatus(t, running, func(status mirror.Status) bool { return len(status.MirroredPorts) == 0 })
	listeners := claimDualStackPort(t, port)
	for _, listener := range listeners {
		_ = listener.Close()
	}
}

func TestConflictingPortIsClaimedAfterLocalListenerStops(t *testing.T) {
	t.Parallel()

	port := availableDualStackPort(t)
	local, err := net.Listen("tcp4", net.JoinHostPort("127.0.0.1", fmt.Sprint(port)))
	if err != nil {
		t.Fatal(err)
	}
	discoverer := &mutableDiscoverer{ports: []uint16{port}}
	running, err := mirror.Start(context.Background(), "reyna", discoverer, unreachableDial, mirror.Options{ReconcileInterval: 10 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = running.Close() })
	waitForMirrorStatus(t, running, func(status mirror.Status) bool { return len(status.ConflictedPorts) == 1 })

	if err := local.Close(); err != nil {
		t.Fatal(err)
	}
	waitForMirrorStatus(t, running, func(status mirror.Status) bool {
		return slices.Contains(status.MirroredPorts, port) && len(status.ConflictedPorts) == 0
	})
}

func TestExistingDestinationMirrorWinsSharedPortUntilItStops(t *testing.T) {
	t.Parallel()

	port := availableDualStackPort(t)
	discoverer := &mutableDiscoverer{ports: []uint16{port}}
	first, err := mirror.Start(context.Background(), "alpha", discoverer, unreachableDial, mirror.Options{ReconcileInterval: 10 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = first.Close() })
	waitForMirrorStatus(t, first, func(status mirror.Status) bool { return slices.Contains(status.MirroredPorts, port) })
	second, err := mirror.Start(context.Background(), "beta", discoverer, unreachableDial, mirror.Options{ReconcileInterval: 10 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = second.Close() })
	waitForMirrorStatus(t, second, func(status mirror.Status) bool { return len(status.ConflictedPorts) == 1 })

	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	waitForMirrorStatus(t, second, func(status mirror.Status) bool { return slices.Contains(status.MirroredPorts, port) })
}

func TestCloseReleasesEveryMirroredListener(t *testing.T) {
	t.Parallel()

	firstPort := availableDualStackPort(t)
	secondPort := availableDualStackPort(t)
	for secondPort == firstPort {
		secondPort = availableDualStackPort(t)
	}
	discoverer := &mutableDiscoverer{ports: []uint16{firstPort, secondPort}}
	running, err := mirror.Start(context.Background(), "reyna", discoverer, unreachableDial, mirror.Options{ReconcileInterval: 10 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	waitForMirrorStatus(t, running, func(status mirror.Status) bool { return len(status.MirroredPorts) == 2 })
	if err := running.Close(); err != nil {
		t.Fatal(err)
	}
	for _, port := range []uint16{firstPort, secondPort} {
		listeners := claimDualStackPort(t, port)
		for _, listener := range listeners {
			_ = listener.Close()
		}
	}
}

func TestForwardingFailureIsVisibleInStatus(t *testing.T) {
	t.Parallel()

	port := availableDualStackPort(t)
	discoverer := &mutableDiscoverer{ports: []uint16{port}}
	running, err := mirror.Start(context.Background(), "reyna", discoverer, func(context.Context, string, string) (net.Conn, error) {
		return nil, fmt.Errorf("SSH channel refused")
	}, mirror.Options{ReconcileInterval: 10 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = running.Close() })
	waitForMirrorStatus(t, running, func(status mirror.Status) bool { return slices.Contains(status.MirroredPorts, port) })
	connection, err := net.DialTimeout("tcp4", net.JoinHostPort("127.0.0.1", fmt.Sprint(port)), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	_ = connection.Close()
	status := waitForMirrorStatus(t, running, func(status mirror.Status) bool { return status.LastError != nil })
	if !strings.Contains(status.LastError.Error(), "SSH channel refused") || !strings.Contains(status.LastError.Error(), fmt.Sprint(port)) {
		t.Fatalf("forwarding error = %v", status.LastError)
	}
}

func TestListenerDisappearsWhileForwardDialIsBlocked(t *testing.T) {
	t.Parallel()

	port := availableDualStackPort(t)
	discoverer := &mutableDiscoverer{ports: []uint16{port}}
	dialStarted := make(chan struct{})
	var once sync.Once
	running, err := mirror.Start(context.Background(), "reyna", discoverer, func(ctx context.Context, _, _ string) (net.Conn, error) {
		once.Do(func() { close(dialStarted) })
		<-ctx.Done()
		return nil, ctx.Err()
	}, mirror.Options{ReconcileInterval: 10 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	connection, err := net.DialTimeout("tcp4", net.JoinHostPort("127.0.0.1", fmt.Sprint(port)), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = connection.Close() }()
	select {
	case <-dialStarted:
	case <-time.After(time.Second):
		t.Fatal("forward dial did not start")
	}
	discoverer.set()
	waitForMirrorStatus(t, running, func(status mirror.Status) bool { return len(status.MirroredPorts) == 0 })
	closed := make(chan error, 1)
	go func() { closed <- running.Close() }()
	select {
	case err := <-closed:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("mirror shutdown deadlocked behind a removed listener")
	}
}

func TestDiscoverySubprocessIsBoundedByTimeout(t *testing.T) {
	t.Parallel()

	started := time.Now()
	running, err := mirror.Start(context.Background(), "reyna", blockingDiscoverer{}, unreachableDial, mirror.Options{
		ReconcileInterval: time.Second,
		DiscoveryTimeout:  20 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = running.Close() })
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("initial discovery took %v, want bounded timeout", elapsed)
	}
	if status := running.Status(); status.LastError == nil || !errors.Is(status.LastError, context.DeadlineExceeded) {
		t.Fatalf("status error = %v, want discovery deadline", status.LastError)
	}
}

type mutableDiscoverer struct {
	mu    sync.Mutex
	ports []uint16
	err   error
}

type blockingDiscoverer struct{}

func (blockingDiscoverer) ListeningPorts(ctx context.Context, _ string) ([]uint16, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func (d *mutableDiscoverer) ListeningPorts(context.Context, string) ([]uint16, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return slices.Clone(d.ports), d.err
}

func (d *mutableDiscoverer) set(ports ...uint16) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.ports = slices.Clone(ports)
}

func availableDualStackPort(t *testing.T) uint16 {
	t.Helper()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := uint16(listener.Addr().(*net.TCPAddr).Port)
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	probe, err := net.Listen("tcp6", net.JoinHostPort("::1", fmt.Sprint(port)))
	if err != nil {
		t.Skipf("IPv6 loopback is unavailable: %v", err)
	}
	_ = probe.Close()
	return port
}

func claimDualStackPort(t *testing.T, port uint16) []net.Listener {
	t.Helper()
	portText := fmt.Sprint(port)
	ipv4, err := net.Listen("tcp4", net.JoinHostPort("127.0.0.1", portText))
	if err != nil {
		t.Fatalf("claim released IPv4 port %d: %v", port, err)
	}
	ipv6, err := net.Listen("tcp6", net.JoinHostPort("::1", portText))
	if err != nil {
		_ = ipv4.Close()
		t.Fatalf("claim released IPv6 port %d: %v", port, err)
	}
	return []net.Listener{ipv4, ipv6}
}

func unreachableDial(context.Context, string, string) (net.Conn, error) {
	return nil, fmt.Errorf("not used")
}

func waitForMirrorStatus(t *testing.T, running *mirror.Running, condition func(mirror.Status) bool) mirror.Status {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		status := running.Status()
		if condition(status) {
			return status
		}
		if time.Now().After(deadline) {
			t.Fatalf("mirror status did not converge: %#v", status)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
