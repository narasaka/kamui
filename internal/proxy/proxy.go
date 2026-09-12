// Package proxy implements Kamui's loopback-only smart HTTP proxy.
package proxy

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/netip"
	"sync"
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

// Options controls protocol timeouts. Zero values use safe defaults.
type Options struct {
	ReadHeaderTimeout time.Duration
}

// RunningProxy is a live smart proxy bound to Mac loopback.
type RunningProxy struct {
	addr      netip.AddrPort
	server    *http.Server
	listener  net.Listener
	closeOnce sync.Once
	closeErr  error
}

// Start binds a new proxy to an ephemeral IPv4 loopback port.
func Start(ctx context.Context, dialers Dialers, options Options) (*RunningProxy, error) {
	if dialers.Direct == nil || dialers.Remote == nil {
		return nil, fmt.Errorf("proxy requires direct and remote dialers")
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("listen on loopback: %w", err)
	}
	address := listener.Addr().(*net.TCPAddr).AddrPort()

	handler := newHandler(dialers)
	readHeaderTimeout := options.ReadHeaderTimeout
	if readHeaderTimeout == 0 {
		readHeaderTimeout = 10 * time.Second
	}
	server := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: readHeaderTimeout,
		IdleTimeout:       90 * time.Second,
	}
	running := &RunningProxy{addr: address, server: server, listener: listener}
	go func() {
		<-ctx.Done()
		_ = running.Close()
	}()
	go func() {
		_ = server.Serve(listener)
	}()
	return running, nil
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
	dialers Dialers
	reverse *httputil.ReverseProxy
}

func newHandler(dialers Dialers) *handler {
	h := &handler{dialers: dialers}
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
		dial = h.dialers.Remote
	}
	upstream, err := dial(request.Context(), "tcp", plan.Address)
	if err != nil {
		http.Error(w, fmt.Sprintf("Kamui could not open the requested tunnel: %v", err), http.StatusBadGateway)
		return
	}

	hijacker, ok := w.(http.Hijacker)
	if !ok {
		upstream.Close()
		http.Error(w, "Kamui proxy cannot create a TCP tunnel", http.StatusInternalServerError)
		return
	}
	client, buffered, err := hijacker.Hijack()
	if err != nil {
		upstream.Close()
		return
	}
	if _, err := io.WriteString(client, "HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
		client.Close()
		upstream.Close()
		return
	}
	go bridgeTunnel(client, buffered, upstream)
}

func bridgeTunnel(client net.Conn, buffered *bufio.ReadWriter, upstream net.Conn) {
	defer client.Close()
	defer upstream.Close()

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
	if tcp, ok := connection.(*net.TCPConn); ok {
		_ = tcp.CloseWrite()
	}
}

func (h *handler) dialContext(ctx context.Context, network, address string) (net.Conn, error) {
	plan, err := routing.Plan(address, 0)
	if err != nil {
		return nil, err
	}
	if plan.Kind == routing.RemoteLoopback {
		return h.dialers.Remote(ctx, network, plan.Address)
	}
	return h.dialers.Direct(ctx, network, plan.Address)
}
