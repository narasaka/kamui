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
	"net/url"
	"strings"
	"sync"
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
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, // test server certificate
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
