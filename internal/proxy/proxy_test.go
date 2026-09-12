package proxy_test

import (
	"bufio"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"sync"
	"syscall"
	"testing"

	"github.com/narasaka/kamui/internal/proxy"
)

func TestHTTPProxyRoutesLoopbackRemotelyAndOtherHostsDirectly(t *testing.T) {
	t.Parallel()

	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "%s %s", r.Host, r.URL.Path)
	}))
	t.Cleanup(backend.Close)
	backendAddress := strings.TrimPrefix(backend.URL, "http://")

	var mu sync.Mutex
	var remoteTargets, directTargets []string
	dialBackend := func(targets *[]string) proxy.DialFunc {
		return func(ctx context.Context, network, address string) (net.Conn, error) {
			mu.Lock()
			*targets = append(*targets, address)
			mu.Unlock()
			return (&net.Dialer{}).DialContext(ctx, network, backendAddress)
		}
	}

	running, err := proxy.Start(context.Background(), proxy.Dialers{
		Remote: dialBackend(&remoteTargets),
		Direct: dialBackend(&directTargets),
	}, proxy.Options{})
	if err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	t.Cleanup(func() { _ = running.Close() })
	if !running.Addr().Addr().IsLoopback() {
		t.Fatalf("proxy listens on %s, want loopback", running.Addr())
	}

	proxyURL, err := url.Parse("http://" + running.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)}}

	for _, target := range []string{
		"http://localhost:3003/backend",
		"http://direct.test:8080/frontend",
	} {
		response, err := client.Get(target)
		if err != nil {
			t.Fatalf("GET %s: %v", target, err)
		}
		body, err := io.ReadAll(response.Body)
		response.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		parsed, _ := url.Parse(target)
		wantBody := parsed.Host + " " + parsed.Path
		if got := string(body); got != wantBody {
			t.Fatalf("GET %s body = %q, want %q", target, got, wantBody)
		}
	}

	mu.Lock()
	defer mu.Unlock()
	if fmt.Sprint(remoteTargets) != "[127.0.0.1:3003]" {
		t.Errorf("remote dial targets = %v, want [127.0.0.1:3003]", remoteTargets)
	}
	if fmt.Sprint(directTargets) != "[direct.test:8080]" {
		t.Errorf("direct dial targets = %v, want [direct.test:8080]", directTargets)
	}
}

func TestHTTPProxyLocalFirstUsesAvailableMacLoopback(t *testing.T) {
	t.Parallel()

	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "local response")
	}))
	t.Cleanup(backend.Close)
	backendAddress := strings.TrimPrefix(backend.URL, "http://")

	var remoteCalled bool
	running, err := proxy.Start(context.Background(), proxy.Dialers{
		Direct: func(ctx context.Context, network, address string) (net.Conn, error) {
			if address != "127.0.0.1:3003" {
				t.Fatalf("direct dial address = %q, want 127.0.0.1:3003", address)
			}
			return (&net.Dialer{}).DialContext(ctx, network, backendAddress)
		},
		Remote: func(context.Context, string, string) (net.Conn, error) {
			remoteCalled = true
			return nil, fmt.Errorf("remote dial must not be used")
		},
	}, proxy.Options{LoopbackMode: proxy.LocalFirst})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = running.Close() })
	proxyURL, _ := url.Parse("http://" + running.Addr().String())
	client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)}}

	response, err := client.Get("http://localhost:3003")
	if err != nil {
		t.Fatalf("GET local loopback through proxy: %v", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "local response" {
		t.Fatalf("body = %q, want local response", body)
	}
	if remoteCalled {
		t.Fatal("remote dialer was called while Mac loopback was available")
	}
}

func TestHTTPProxyLocalFirstFallsBackWhenMacLoopbackRefusesConnection(t *testing.T) {
	t.Parallel()

	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "remote response")
	}))
	t.Cleanup(backend.Close)
	backendAddress := strings.TrimPrefix(backend.URL, "http://")

	var remoteAddress string
	running, err := proxy.Start(context.Background(), proxy.Dialers{
		Direct: func(context.Context, string, string) (net.Conn, error) {
			return nil, fmt.Errorf("connect locally: %w", syscall.ECONNREFUSED)
		},
		Remote: func(ctx context.Context, network, address string) (net.Conn, error) {
			remoteAddress = address
			return (&net.Dialer{}).DialContext(ctx, network, backendAddress)
		},
	}, proxy.Options{LoopbackMode: proxy.LocalFirst})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = running.Close() })
	proxyURL, _ := url.Parse("http://" + running.Addr().String())
	client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)}}

	response, err := client.Get("http://localhost:3003")
	if err != nil {
		t.Fatalf("GET remote fallback through proxy: %v", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "remote response" {
		t.Fatalf("body = %q, want remote response", body)
	}
	if remoteAddress != "127.0.0.1:3003" {
		t.Fatalf("remote dial address = %q, want 127.0.0.1:3003", remoteAddress)
	}
}

func TestHTTPProxyLocalFirstReachesIPv6OnlyMacLoopback(t *testing.T) {
	t.Parallel()

	listener, err := net.Listen("tcp6", "[::1]:0")
	if err != nil {
		t.Skipf("IPv6 loopback is unavailable: %v", err)
	}
	backend := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "local IPv6 response")
	}))
	backend.Listener = listener
	backend.Start()
	t.Cleanup(backend.Close)
	port := listener.Addr().(*net.TCPAddr).Port

	var remoteCalled bool
	running, err := proxy.Start(context.Background(), proxy.Dialers{
		Direct: (&net.Dialer{}).DialContext,
		Remote: func(context.Context, string, string) (net.Conn, error) {
			remoteCalled = true
			return nil, fmt.Errorf("remote dial must not be used")
		},
	}, proxy.Options{LoopbackMode: proxy.LocalFirst})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = running.Close() })
	proxyURL, _ := url.Parse("http://" + running.Addr().String())
	client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)}}

	response, err := client.Get(fmt.Sprintf("http://localhost:%d", port))
	if err != nil {
		t.Fatalf("GET IPv6-only Mac loopback: %v", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "local IPv6 response" {
		t.Fatalf("body = %q, want local IPv6 response", body)
	}
	if remoteCalled {
		t.Fatal("remote dialer was called while Mac IPv6 loopback was available")
	}
}

func TestHTTPProxyLocalFirstDoesNotFallBackAfterOtherLocalErrors(t *testing.T) {
	t.Parallel()

	var remoteCalled bool
	running, err := proxy.Start(context.Background(), proxy.Dialers{
		Direct: func(context.Context, string, string) (net.Conn, error) {
			return nil, fmt.Errorf("connect locally: %w", syscall.EACCES)
		},
		Remote: func(context.Context, string, string) (net.Conn, error) {
			remoteCalled = true
			return nil, fmt.Errorf("remote dial must not be used")
		},
	}, proxy.Options{LoopbackMode: proxy.LocalFirst})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = running.Close() })
	proxyURL, _ := url.Parse("http://" + running.Addr().String())
	client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)}}

	response, err := client.Get("http://localhost:3003")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusBadGateway || !strings.Contains(string(body), "permission denied") {
		t.Fatalf("status=%d body=%q, want local permission error", response.StatusCode, body)
	}
	if remoteCalled {
		t.Fatal("remote dialer was called after a non-refusal local error")
	}
}

func TestHTTPProxyReachesIPv6OnlyRemoteLoopback(t *testing.T) {
	t.Parallel()

	listener, err := net.Listen("tcp6", "[::1]:0")
	if err != nil {
		t.Skipf("IPv6 loopback is unavailable: %v", err)
	}
	backend := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "IPv6 remote response")
	}))
	backend.Listener = listener
	backend.Start()
	t.Cleanup(backend.Close)
	port := listener.Addr().(*net.TCPAddr).Port

	running, err := proxy.Start(context.Background(), proxy.Dialers{
		Remote: (&net.Dialer{}).DialContext,
		Direct: (&net.Dialer{}).DialContext,
	}, proxy.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = running.Close() })
	proxyURL, _ := url.Parse("http://" + running.Addr().String())
	client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)}}

	response, err := client.Get(fmt.Sprintf("http://localhost:%d", port))
	if err != nil {
		t.Fatalf("GET IPv6-only remote loopback: %v", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK || string(body) != "IPv6 remote response" {
		t.Fatalf("status=%d body=%q, want 200 IPv6 remote response", response.StatusCode, body)
	}
}

func TestProxyBindsRequestedLoopbackAddressAndRejectsOtherInterfaces(t *testing.T) {
	t.Parallel()

	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().(*net.TCPAddr).AddrPort()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	dialer := (&net.Dialer{}).DialContext
	running, err := proxy.Start(context.Background(), proxy.Dialers{Direct: dialer, Remote: dialer}, proxy.Options{ListenAddress: address})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = running.Close() })
	if running.Addr() != address {
		t.Fatalf("proxy address = %s, want %s", running.Addr(), address)
	}
	if _, err := proxy.Start(context.Background(), proxy.Dialers{Direct: dialer, Remote: dialer}, proxy.Options{
		ListenAddress: netip.MustParseAddrPort("0.0.0.0:12345"),
	}); err == nil {
		t.Fatal("proxy accepted a non-loopback listen address")
	}
}

func TestProxyCarriesPlainWebSocketUpgrade(t *testing.T) {
	t.Parallel()

	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
			http.Error(w, "missing upgrade", http.StatusBadRequest)
			return
		}
		client, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			return
		}
		defer client.Close()
		_, _ = io.WriteString(client, "HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: websocket\r\n\r\n")
		message := make([]byte, 4)
		if _, err := io.ReadFull(client, message); err == nil {
			_, _ = client.Write(message)
		}
	}))
	t.Cleanup(backend.Close)
	backendAddress := strings.TrimPrefix(backend.URL, "http://")

	running, err := proxy.Start(context.Background(), proxy.Dialers{
		Remote: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, backendAddress)
		},
		Direct: (&net.Dialer{}).DialContext,
	}, proxy.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = running.Close() })

	connection, err := net.Dial("tcp", running.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	_, _ = io.WriteString(connection, "GET ws://localhost:3004/socket HTTP/1.1\r\nHost: localhost:3004\r\nConnection: Upgrade\r\nUpgrade: websocket\r\n\r\n")
	reader := bufio.NewReader(connection)
	request, _ := http.NewRequest(http.MethodGet, "http://localhost:3004/socket", nil)
	response, err := http.ReadResponse(reader, request)
	if err != nil {
		t.Fatalf("read upgrade response: %v", err)
	}
	if response.StatusCode != http.StatusSwitchingProtocols {
		body, _ := io.ReadAll(response.Body)
		t.Fatalf("upgrade status = %d, want 101; body %q", response.StatusCode, body)
	}
	if _, err := connection.Write([]byte("ping")); err != nil {
		t.Fatal(err)
	}
	echo := make([]byte, 4)
	if _, err := io.ReadFull(reader, echo); err != nil {
		t.Fatalf("read upgraded echo: %v", err)
	}
	if string(echo) != "ping" {
		t.Fatalf("upgraded echo = %q, want ping", echo)
	}
}

func TestProxyTunnelsHTTPSWithoutTerminatingTLS(t *testing.T) {
	t.Parallel()

	backend := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "secure remote response")
	}))
	t.Cleanup(backend.Close)
	backendAddress := strings.TrimPrefix(backend.URL, "https://")

	var dialed string
	running, err := proxy.Start(context.Background(), proxy.Dialers{
		Remote: func(ctx context.Context, network, address string) (net.Conn, error) {
			dialed = address
			return (&net.Dialer{}).DialContext(ctx, network, backendAddress)
		},
		Direct: (&net.Dialer{}).DialContext,
	}, proxy.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = running.Close() })
	proxyURL, _ := url.Parse("http://" + running.Addr().String())
	client := &http.Client{Transport: &http.Transport{
		Proxy:           http.ProxyURL(proxyURL),
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	}}

	response, err := client.Get("https://localhost:3443/secret")
	if err != nil {
		t.Fatalf("HTTPS through proxy: %v", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(body); got != "secure remote response" {
		t.Fatalf("body = %q, want secure remote response", got)
	}
	if dialed != "127.0.0.1:3443" {
		t.Fatalf("remote dial target = %q, want 127.0.0.1:3443", dialed)
	}
}

func TestProxyLocalFirstTunnelsHTTPSToAvailableMacLoopback(t *testing.T) {
	t.Parallel()

	backend := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "secure local response")
	}))
	t.Cleanup(backend.Close)
	backendAddress := strings.TrimPrefix(backend.URL, "https://")

	var remoteCalled bool
	running, err := proxy.Start(context.Background(), proxy.Dialers{
		Direct: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, backendAddress)
		},
		Remote: func(context.Context, string, string) (net.Conn, error) {
			remoteCalled = true
			return nil, fmt.Errorf("remote dial must not be used")
		},
	}, proxy.Options{LoopbackMode: proxy.LocalFirst})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = running.Close() })
	proxyURL, _ := url.Parse("http://" + running.Addr().String())
	client := &http.Client{Transport: &http.Transport{
		Proxy:           http.ProxyURL(proxyURL),
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	}}

	response, err := client.Get("https://localhost:3443")
	if err != nil {
		t.Fatalf("HTTPS to Mac loopback through proxy: %v", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "secure local response" {
		t.Fatalf("body = %q, want secure local response", body)
	}
	if remoteCalled {
		t.Fatal("remote dialer was called while Mac loopback was available")
	}
}

func TestProxyTunnelsHTTPSToIPv6OnlyRemoteLoopback(t *testing.T) {
	t.Parallel()

	listener, err := net.Listen("tcp6", "[::1]:0")
	if err != nil {
		t.Skipf("IPv6 loopback is unavailable: %v", err)
	}
	backend := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "secure IPv6 remote response")
	}))
	backend.Listener = listener
	backend.StartTLS()
	t.Cleanup(backend.Close)
	port := listener.Addr().(*net.TCPAddr).Port

	running, err := proxy.Start(context.Background(), proxy.Dialers{
		Remote: (&net.Dialer{}).DialContext,
		Direct: (&net.Dialer{}).DialContext,
	}, proxy.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = running.Close() })
	proxyURL, _ := url.Parse("http://" + running.Addr().String())
	client := &http.Client{Transport: &http.Transport{
		Proxy:           http.ProxyURL(proxyURL),
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	}}

	response, err := client.Get(fmt.Sprintf("https://localhost:%d", port))
	if err != nil {
		t.Fatalf("GET HTTPS through IPv6-only remote loopback: %v", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK || string(body) != "secure IPv6 remote response" {
		t.Fatalf("status=%d body=%q, want 200 secure IPv6 remote response", response.StatusCode, body)
	}
}
