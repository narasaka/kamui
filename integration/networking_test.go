package integration_test

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/narasaka/kamui/internal/session"
	"github.com/narasaka/kamui/internal/ssh"
	"github.com/narasaka/kamui/internal/testsupport"
)

func TestNetworkingSessionCarriesUnannouncedPortsAndLeavesOtherHostsDirect(t *testing.T) {
	t.Parallel()

	servers := make([]*httptest.Server, 4)
	for index := range servers {
		index := index
		servers[index] = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			fmt.Fprintf(w, "remote service %d", index)
		}))
		t.Cleanup(servers[index].Close)
	}
	launcher := &testsupport.SSHLauncher{}
	manager := session.NewManager(ssh.Transport{Launcher: launcher, ReadinessTimeout: time.Second})
	t.Cleanup(func() { _ = manager.Close() })
	destination, _ := session.ParseDestination("reyna")
	ensured, err := manager.Execute(context.Background(), session.Command{Operation: session.Ensure, Destination: destination})
	if err != nil {
		t.Fatal(err)
	}
	proxyURL, _ := url.Parse("http://" + ensured.Session.Proxy.String())
	client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)}}

	for index, server := range servers {
		port := strings.TrimPrefix(server.URL, "http://127.0.0.1:")
		response, err := client.Get("http://localhost:" + port)
		if err != nil {
			t.Fatalf("remote service %d: %v", index, err)
		}
		body, _ := io.ReadAll(response.Body)
		response.Body.Close()
		if got, want := string(body), fmt.Sprintf("remote service %d", index); got != want {
			t.Fatalf("remote service %d body = %q, want %q", index, got, want)
		}
	}

	directPort := strings.TrimPrefix(servers[0].URL, "http://127.0.0.1:")
	response, err := client.Get("http://0.0.0.0:" + directPort)
	if err != nil {
		t.Fatalf("direct request: %v", err)
	}
	response.Body.Close()
	if launcher.Starts() != 1 {
		t.Fatalf("OpenSSH starts = %d, want one transport for every port", launcher.Starts())
	}
}

func TestNetworkingSessionReportsRemoteRefusalAndCleansUp(t *testing.T) {
	t.Parallel()

	launcher := &testsupport.SSHLauncher{}
	manager := session.NewManager(ssh.Transport{Launcher: launcher, ReadinessTimeout: time.Second})
	t.Cleanup(func() { _ = manager.Close() })
	destination, _ := session.ParseDestination("reyna")
	ensured, err := manager.Execute(context.Background(), session.Command{Operation: session.Ensure, Destination: destination})
	if err != nil {
		t.Fatal(err)
	}
	unused := availableIntegrationPort(t)
	proxyURL, _ := url.Parse("http://" + ensured.Session.Proxy.String())
	client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)}}
	response, err := client.Get(fmt.Sprintf("http://localhost:%d", unused))
	if err != nil {
		t.Fatalf("proxy refusal response: %v", err)
	}
	body, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != http.StatusBadGateway || !strings.Contains(strings.ToLower(string(body)), "connection refused") {
		t.Fatalf("refusal status=%d body=%q", response.StatusCode, body)
	}
	if _, err := manager.Execute(context.Background(), session.Command{Operation: session.Stop, Destination: destination}); err != nil {
		t.Fatal(err)
	}
	if launcher.Active() != 0 {
		t.Fatalf("active OpenSSH children = %d, want 0", launcher.Active())
	}
	connection, err := net.DialTimeout("tcp", ensured.Session.Proxy.String(), 100*time.Millisecond)
	if err == nil {
		connection.Close()
		t.Fatal("proxy still accepted connections after stop")
	}
}

func availableIntegrationPort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	return port
}
