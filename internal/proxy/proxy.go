// Package proxy implements Kamui's loopback-only smart HTTP proxy.
package proxy

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/netip"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/narasaka/kamui/internal/routing"
)

// DialFunc opens a TCP connection at an operating-system network seam.
type DialFunc func(context.Context, string, string) (net.Conn, error)

// Dialers provide the two possible network paths.
type Dialers struct {
	Direct DialFunc
	Remote DialFunc
}

// LoopbackMode controls how explicit loopback destinations are reached.
type LoopbackMode uint8

const (
	// RemoteOnly sends explicit loopback destinations through SSH.
	RemoteOnly LoopbackMode = iota
	// LocalFirst prefers local-machine loopback before considering the remote host.
	LocalFirst
)

// ParseLoopbackMode parses a user-facing loopback routing mode.
func ParseLoopbackMode(value string) (LoopbackMode, error) {
	switch value {
	case "remote-only":
		return RemoteOnly, nil
	case "local-first":
		return LocalFirst, nil
	default:
		return RemoteOnly, fmt.Errorf("unsupported loopback mode %q", value)
	}
}

func (m LoopbackMode) String() string {
	if m == LocalFirst {
		return "local-first"
	}
	return "remote-only"
}

// Options controls protocol timeouts. Zero values use safe defaults.
type Options struct {
	ReadHeaderTimeout time.Duration
	ListenAddress     netip.AddrPort
	LoopbackMode      LoopbackMode
}

// RunningProxy is a live smart proxy bound to local-machine loopback.
type RunningProxy struct {
	addr      netip.AddrPort
	server    *http.Server
	listener  net.Listener
	closeOnce sync.Once
	closeErr  error
	activity  *activity
}

// Activity is the observable connection state used by idle policy.
type Activity struct {
	Connections  int64
	LastActivity time.Time
}

// Start binds a new proxy to an ephemeral IPv4 loopback port.
func Start(ctx context.Context, dialers Dialers, options Options) (*RunningProxy, error) {
	if dialers.Direct == nil || dialers.Remote == nil {
		return nil, fmt.Errorf("proxy requires direct and remote dialers")
	}
	listenAddress := options.ListenAddress
	if !listenAddress.IsValid() {
		listenAddress = netip.AddrPortFrom(netip.MustParseAddr("127.0.0.1"), 0)
	}
	if listenAddress.Addr() != netip.MustParseAddr("127.0.0.1") {
		return nil, fmt.Errorf("proxy listen address must be IPv4 loopback 127.0.0.1")
	}
	listener, err := net.Listen("tcp4", listenAddress.String())
	if err != nil {
		return nil, fmt.Errorf("listen on loopback: %w", err)
	}
	address := listener.Addr().(*net.TCPAddr).AddrPort()
	activity := &activity{}
	activity.touch()
	trackedListener := &trackingListener{Listener: listener, activity: activity}

	handler := newHandler(dialers, options.LoopbackMode)
	readHeaderTimeout := options.ReadHeaderTimeout
	if readHeaderTimeout == 0 {
		readHeaderTimeout = 10 * time.Second
	}
	server := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: readHeaderTimeout,
		IdleTimeout:       90 * time.Second,
	}
	running := &RunningProxy{addr: address, server: server, listener: trackedListener, activity: activity}
	go func() {
		<-ctx.Done()
		_ = running.Close()
	}()
	go func() {
		_ = server.Serve(trackedListener)
	}()
	return running, nil
}

// Activity reports active TCP connections and the latest observed I/O.
func (p *RunningProxy) Activity() Activity {
	return Activity{
		Connections:  p.activity.connections.Load(),
		LastActivity: time.Unix(0, p.activity.last.Load()),
	}
}

// Addr returns the loopback address clients should configure as their HTTP
// proxy.
func (p *RunningProxy) Addr() netip.AddrPort {
	return p.addr
}

// Close stops accepting connections and closes idle connections.
func (p *RunningProxy) Close() error {
	p.closeOnce.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		p.closeErr = p.server.Shutdown(ctx)
		if p.closeErr != nil {
			_ = p.listener.Close()
		}
	})
	return p.closeErr
}

type handler struct {
	dialers      Dialers
	loopbackMode LoopbackMode
	reverse      *httputil.ReverseProxy
}

func newHandler(dialers Dialers, loopbackMode LoopbackMode) *handler {
	h := &handler{dialers: dialers, loopbackMode: loopbackMode}
	h.reverse = &httputil.ReverseProxy{
		Rewrite: func(request *httputil.ProxyRequest) {
			scheme := request.In.URL.Scheme
			if scheme == "ws" {
				scheme = "http"
			}
			request.Out.URL.Scheme = scheme
			request.Out.URL.Host = request.In.URL.Host
			request.Out.Host = request.In.Host
		},
		Transport: &http.Transport{
			Proxy:                 nil,
			DialContext:           h.dialContext,
			ForceAttemptHTTP2:     false,
			ResponseHeaderTimeout: 30 * time.Second,
		},
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, err error) {
			http.Error(w, fmt.Sprintf("Kamui could not reach the requested destination: %v", err), http.StatusBadGateway)
		},
	}
	return h
}

func (h *handler) ServeHTTP(w http.ResponseWriter, request *http.Request) {
	if request.Method == http.MethodConnect {
		h.serveConnect(w, request)
		return
	}
	if !request.URL.IsAbs() || request.URL.Host == "" {
		http.Error(w, "Kamui received a malformed absolute proxy request", http.StatusBadRequest)
		return
	}
	h.reverse.ServeHTTP(w, request)
}

func (h *handler) serveConnect(w http.ResponseWriter, request *http.Request) {
	plan, err := routing.Plan(request.Host, 443)
	if err != nil {
		http.Error(w, "Kamui received a malformed CONNECT target", http.StatusBadRequest)
		return
	}
	dial := h.dialers.Direct
	if plan.Kind == routing.RemoteLoopback {
		dial = h.dialLoopback
	}
	upstream, err := dial(request.Context(), "tcp", plan.Address)
	if err != nil {
		http.Error(w, fmt.Sprintf("Kamui could not open the requested tunnel: %v", err), http.StatusBadGateway)
		return
	}

	hijacker, ok := w.(http.Hijacker)
	if !ok {
		_ = upstream.Close()
		http.Error(w, "Kamui proxy cannot create a TCP tunnel", http.StatusInternalServerError)
		return
	}
	client, buffered, err := hijacker.Hijack()
	if err != nil {
		_ = upstream.Close()
		return
	}
	if _, err := io.WriteString(client, "HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
		_ = client.Close()
		_ = upstream.Close()
		return
	}
	go bridgeTunnel(client, buffered, upstream)
}

func bridgeTunnel(client net.Conn, buffered *bufio.ReadWriter, upstream net.Conn) {
	defer func() { _ = client.Close() }()
	defer func() { _ = upstream.Close() }()

	done := make(chan struct{}, 2)
	go func() {
		_, _ = io.Copy(upstream, buffered)
		closeWrite(upstream)
		done <- struct{}{}
	}()
	go func() {
		_, _ = io.Copy(client, upstream)
		closeWrite(client)
		done <- struct{}{}
	}()
	<-done
	<-done
}

func closeWrite(connection net.Conn) {
	if writer, ok := connection.(interface{ CloseWrite() error }); ok {
		_ = writer.CloseWrite()
	}
}

type activity struct {
	connections atomic.Int64
	last        atomic.Int64
}

func (a *activity) touch() { a.last.Store(time.Now().UnixNano()) }

type trackingListener struct {
	net.Listener
	activity *activity
}

func (l *trackingListener) Accept() (net.Conn, error) {
	connection, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	l.activity.connections.Add(1)
	l.activity.touch()
	return &trackingConnection{Conn: connection, activity: l.activity}, nil
}

type trackingConnection struct {
	net.Conn
	activity  *activity
	closeOnce sync.Once
}

func (c *trackingConnection) Read(buffer []byte) (int, error) {
	count, err := c.Conn.Read(buffer)
	if count > 0 {
		c.activity.touch()
	}
	return count, err
}

func (c *trackingConnection) Write(buffer []byte) (int, error) {
	count, err := c.Conn.Write(buffer)
	if count > 0 {
		c.activity.touch()
	}
	return count, err
}

func (c *trackingConnection) Close() error {
	var err error
	c.closeOnce.Do(func() {
		err = c.Conn.Close()
		c.activity.connections.Add(-1)
		c.activity.touch()
	})
	return err
}

func (c *trackingConnection) CloseWrite() error {
	if writer, ok := c.Conn.(interface{ CloseWrite() error }); ok {
		return writer.CloseWrite()
	}
	return nil
}

func (h *handler) dialContext(ctx context.Context, network, address string) (net.Conn, error) {
	plan, err := routing.Plan(address, 0)
	if err != nil {
		return nil, err
	}
	if plan.Kind == routing.RemoteLoopback {
		return h.dialLoopback(ctx, network, plan.Address)
	}
	return h.dialers.Direct(ctx, network, plan.Address)
}

func (h *handler) dialLoopback(ctx context.Context, network, address string) (net.Conn, error) {
	if h.loopbackMode == LocalFirst {
		connection, localErr := h.dialLocalLoopback(ctx, network, address)
		if localErr == nil {
			return connection, nil
		}
		if !errors.Is(localErr, syscall.ECONNREFUSED) {
			return nil, localErr
		}
	}
	return h.dialRemoteLoopback(ctx, network, address)
}

func (h *handler) dialLocalLoopback(ctx context.Context, network, ipv4Address string) (net.Conn, error) {
	connection, ipv4Err := h.dialers.Direct(ctx, network, ipv4Address)
	if ipv4Err == nil {
		return connection, nil
	}
	if ctx.Err() != nil || !errors.Is(ipv4Err, syscall.ECONNREFUSED) {
		return nil, ipv4Err
	}

	_, port, err := net.SplitHostPort(ipv4Address)
	if err != nil {
		return nil, ipv4Err
	}
	ipv6Address := net.JoinHostPort("::1", port)
	connection, ipv6Err := h.dialers.Direct(ctx, network, ipv6Address)
	if ipv6Err == nil {
		return connection, nil
	}
	return nil, fmt.Errorf("local loopback unavailable over IPv4 (%v) and IPv6: %w", ipv4Err, ipv6Err)
}

func (h *handler) dialRemoteLoopback(ctx context.Context, network, ipv4Address string) (net.Conn, error) {
	connection, ipv4Err := h.dialers.Remote(ctx, network, ipv4Address)
	if ipv4Err == nil {
		return connection, nil
	}
	if ctx.Err() != nil {
		return nil, ipv4Err
	}

	_, port, err := net.SplitHostPort(ipv4Address)
	if err != nil {
		return nil, ipv4Err
	}
	ipv6Address := net.JoinHostPort("::1", port)
	connection, ipv6Err := h.dialers.Remote(ctx, network, ipv6Address)
	if ipv6Err == nil {
		return connection, nil
	}
	return nil, fmt.Errorf("remote loopback unavailable over IPv4 (%v) and IPv6: %w", ipv4Err, ipv6Err)
}
