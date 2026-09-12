//go:build darwin

package integration_test

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	cryptorand "crypto/rand"
	"crypto/sha1"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/narasaka/kamui/internal/browser"
	"github.com/narasaka/kamui/internal/session"
	"github.com/narasaka/kamui/internal/ssh"
	"github.com/narasaka/kamui/internal/testsupport"
)

func TestInstalledBrowsersUseKamuiProxy(t *testing.T) {
	if os.Getenv("KAMUI_BROWSER_TESTS") != "1" {
		t.Skip("set KAMUI_BROWSER_TESTS=1 to launch installed browsers headlessly")
	}

	httpServers := make([]*httptest.Server, 4)
	for index := range httpServers {
		index := index
		httpServers[index] = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Access-Control-Allow-Origin", "*")
			fmt.Fprintf(w, "<html><body>remote-http-%d</body></html>", index)
		}))
		defer httpServers[index].Close()
	}
	caCertificate, serverCertificate := browserTestCertificate(t)
	httpsServer := newBrowserTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		_, _ = io.WriteString(w, "<html><body>remote-https</body></html>")
	}), serverCertificate)
	defer httpsServer.Close()
	directServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		_, _ = io.WriteString(w, "mac-direct")
	}))
	defer directServer.Close()

	webSocket := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		serveBrowserWebSocket(w, r, "ws-ok")
	}))
	defer webSocket.Close()
	secureWebSocket := newBrowserTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		serveBrowserWebSocket(w, r, "wss-ok")
	}), serverCertificate)
	defer secureWebSocket.Close()
	reports := make(chan string, 3)
	reportServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		reports <- request.URL.Query().Get("value")
		w.WriteHeader(http.StatusNoContent)
	}))
	defer reportServer.Close()
	wsPort := strings.TrimPrefix(webSocket.URL, "http://127.0.0.1:")
	wssPort := strings.TrimPrefix(secureWebSocket.URL, "https://127.0.0.1:")
	reportPort := strings.TrimPrefix(reportServer.URL, "http://127.0.0.1:")
	httpsPort := strings.TrimPrefix(httpsServer.URL, "https://127.0.0.1:")
	directPort := strings.TrimPrefix(directServer.URL, "http://127.0.0.1:")
	httpPorts := make([]string, 0, len(httpServers))
	for _, server := range httpServers {
		httpPorts = append(httpPorts, strings.TrimPrefix(server.URL, "http://127.0.0.1:"))
	}
	page := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(w, `<html><body>waiting<script type="module">
const socketResult=(target)=>new Promise((resolve,reject)=>{const socket=new WebSocket(target);socket.onmessage=(event)=>resolve(event.data);socket.onerror=reject;});
const values=await Promise.all([
  fetch("http://localhost:%s").then(r=>r.text()),
  fetch("http://app.localhost:%s").then(r=>r.text()),
  fetch("http://127.42.0.1:%s").then(r=>r.text()),
  fetch("http://[::1]:%s").then(r=>r.text()),
  fetch("https://localhost:%s").then(r=>r.text()),
  socketResult("ws://localhost:%s"),
  socketResult("wss://localhost:%s"),
  fetch("http://0.0.0.0:%s").then(r=>r.text())
]);
document.body.textContent=values.join("|");
await fetch("http://localhost:%s/report?value="+encodeURIComponent(values.join("|")));
</script></body></html>`, httpPorts[0], httpPorts[1], httpPorts[2], httpPorts[3], httpsPort, wsPort, wssPort, directPort, reportPort)
	}))
	defer page.Close()

	manager := session.NewManager(ssh.Transport{Launcher: &testsupport.SSHLauncher{}, ReadinessTimeout: time.Second})
	defer manager.Close()
	destination, _ := session.ParseDestination("browser-gate")
	result, err := manager.Execute(context.Background(), session.Command{Operation: session.Ensure, Destination: destination})
	if err != nil {
		t.Fatal(err)
	}

	browsers := []struct {
		id   string
		path string
	}{
		{id: "chrome", path: "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"},
		{id: "brave", path: "/Applications/Brave Browser.app/Contents/MacOS/Brave Browser"},
	}
	for _, installed := range browsers {
		installed := installed
		t.Run(installed.id, func(t *testing.T) {
			if _, err := os.Stat(installed.path); err != nil {
				if requiredBrowser(installed.id) {
					t.Fatalf("required %s browser is not installed at %s", installed.id, installed.path)
				}
				t.Skipf("%s is not installed", installed.id)
			}
			launcher := &browserTestLauncher{
				prefix:  []string{"--headless=new", "--disable-gpu", "--disable-background-networking", "--disable-component-update", "--disable-sync", "--ignore-certificate-errors"},
				reports: reports,
			}
			adapter := browser.NewChromiumAdapter(installed.id, []string{installed.path}, launcher)
			profile, err := adapter.PrepareProfile(context.Background(), browser.Session{
				Key: destination.Key(), Proxy: result.Session.Proxy,
				ProfileRoot: filepath.Join(t.TempDir(), "profiles"),
			}, browser.Installation{ID: installed.id, Executable: installed.path})
			if err != nil {
				t.Fatal(err)
			}
			pagePort := strings.TrimPrefix(page.URL, "http://127.0.0.1:")
			launcher.want = []string{
				"remote-http-0", "remote-http-1", "remote-http-2", "remote-http-3",
				"remote-https", "ws-ok", "wss-ok", "mac-direct",
			}
			if err := adapter.Launch(context.Background(), profile, []string{"http://localhost:" + pagePort}); err != nil {
				t.Fatalf("browser protocol gate: %v", err)
			}
		})
	}

	geckoBrowsers := []struct {
		id   string
		path string
	}{
		{id: "firefox", path: "/Applications/Firefox.app/Contents/MacOS/firefox"},
		{id: "firefox-developer-edition", path: "/Applications/Firefox Developer Edition.app/Contents/MacOS/firefox"},
		{id: "zen", path: "/Applications/Zen.app/Contents/MacOS/zen"},
		{id: "librewolf", path: "/Applications/LibreWolf.app/Contents/MacOS/librewolf"},
		{id: "floorp", path: "/Applications/Floorp.app/Contents/MacOS/floorp"},
	}
	for _, installed := range geckoBrowsers {
		installed := installed
		t.Run(installed.id, func(t *testing.T) {
			if _, err := os.Stat(installed.path); err != nil {
				if requiredBrowser(installed.id) {
					t.Fatalf("required %s browser is not installed at %s", installed.id, installed.path)
				}
				t.Skipf("%s is not installed", installed.id)
			}
			certutil, err := exec.LookPath("certutil")
			if err != nil {
				if requiredBrowser(installed.id) {
					t.Fatalf("certutil is required for the %s release gate", installed.id)
				}
				t.Skip("certutil is required to trust the disposable HTTPS certificate")
			}
			launcher := &browserTestLauncher{prefix: []string{"-headless"}, reports: reports}
			adapter := browser.NewGeckoAdapter(installed.id, []string{installed.path}, launcher)
			profile, err := adapter.PrepareProfile(context.Background(), browser.Session{
				Key: destination.Key(), Proxy: result.Session.Proxy,
				ProfileRoot: filepath.Join(t.TempDir(), "profiles"),
			}, browser.Installation{ID: installed.id, Executable: installed.path})
			if err != nil {
				t.Fatal(err)
			}
			installBrowserTestCA(t, certutil, profile.Path, caCertificate)
			pagePort := strings.TrimPrefix(page.URL, "http://127.0.0.1:")
			launcher.want = []string{
				"remote-http-0", "remote-http-1", "remote-http-2", "remote-http-3",
				"remote-https", "ws-ok", "wss-ok", "mac-direct",
			}
			if err := adapter.Launch(context.Background(), profile, []string{"http://localhost:" + pagePort}); err != nil {
				t.Fatalf("browser protocol gate: %v", err)
			}
		})
	}
}

func TestChromeReconnectsWebSocketAfterRemoteServerRestart(t *testing.T) {
	if os.Getenv("KAMUI_BROWSER_TESTS") != "1" {
		t.Skip("set KAMUI_BROWSER_TESTS=1 to launch installed browsers headlessly")
	}
	const chromePath = "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"
	if _, err := os.Stat(chromePath); err != nil {
		t.Skip("Chrome is not installed")
	}
	webSocketURL, stopWebSocket := restartingWebSocketServer(t)
	defer stopWebSocket()
	reports := make(chan string, 1)
	reportServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		reports <- request.URL.Query().Get("value")
		w.WriteHeader(http.StatusNoContent)
	}))
	defer reportServer.Close()
	wsPort := strings.TrimPrefix(webSocketURL, "ws://127.0.0.1:")
	reportPort := strings.TrimPrefix(reportServer.URL, "http://127.0.0.1:")
	page := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(w, `<script type="module">
const once=()=>new Promise((resolve,reject)=>{const socket=new WebSocket("ws://localhost:%s");socket.onmessage=e=>resolve(e.data);socket.onerror=reject;});
const first=await once();
await new Promise(resolve=>setTimeout(resolve, 700));
let second;
for(let attempt=0;attempt<20&&!second;attempt++){try{second=await once();}catch{await new Promise(resolve=>setTimeout(resolve,100));}}
await fetch("http://localhost:%s/report?value="+encodeURIComponent(first+"|"+second));
</script>`, wsPort, reportPort)
	}))
	defer page.Close()

	manager := session.NewManager(ssh.Transport{Launcher: &testsupport.SSHLauncher{}, ReadinessTimeout: time.Second})
	defer manager.Close()
	destination, _ := session.ParseDestination("browser-hmr-gate")
	result, err := manager.Execute(context.Background(), session.Command{Operation: session.Ensure, Destination: destination})
	if err != nil {
		t.Fatal(err)
	}
	launcher := &browserTestLauncher{
		prefix:  []string{"--headless=new", "--disable-gpu", "--disable-background-networking", "--disable-component-update", "--disable-sync"},
		reports: reports, want: []string{"before-restart", "after-restart"},
	}
	adapter := browser.NewChromiumAdapter("chrome", []string{chromePath}, launcher)
	profile, err := adapter.PrepareProfile(context.Background(), browser.Session{
		Key: destination.Key(), Proxy: result.Session.Proxy, ProfileRoot: filepath.Join(t.TempDir(), "profiles"),
	}, browser.Installation{ID: "chrome", Executable: chromePath})
	if err != nil {
		t.Fatal(err)
	}
	pagePort := strings.TrimPrefix(page.URL, "http://127.0.0.1:")
	if err := adapter.Launch(context.Background(), profile, []string{"http://localhost:" + pagePort}); err != nil {
		t.Fatalf("HMR-style reconnect gate: %v", err)
	}
}

func TestChromeUsesSeveralRemoteLoopbackTabsSimultaneously(t *testing.T) {
	if os.Getenv("KAMUI_BROWSER_TESTS") != "1" {
		t.Skip("set KAMUI_BROWSER_TESTS=1 to launch installed browsers headlessly")
	}
	const chromePath = "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"
	if _, err := os.Stat(chromePath); err != nil {
		t.Skip("Chrome is not installed")
	}
	hits := make(chan string, 4)
	servers := make([]*httptest.Server, 4)
	for index := range servers {
		index := index
		servers[index] = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			hits <- fmt.Sprintf("tab-%d", index)
			fmt.Fprintf(w, "tab-%d", index)
		}))
		defer servers[index].Close()
	}
	manager := session.NewManager(ssh.Transport{Launcher: &testsupport.SSHLauncher{}, ReadinessTimeout: time.Second})
	defer manager.Close()
	destination, _ := session.ParseDestination("browser-tabs-gate")
	result, err := manager.Execute(context.Background(), session.Command{Operation: session.Ensure, Destination: destination})
	if err != nil {
		t.Fatal(err)
	}
	tabs := make([]string, 0, len(servers))
	for _, server := range servers {
		tabs = append(tabs, "http://localhost:"+strings.TrimPrefix(server.URL, "http://127.0.0.1:"))
	}
	launcher := &chromeTabsLauncher{tabs: tabs, hits: hits}
	adapter := browser.NewChromiumAdapter("chrome", []string{chromePath}, launcher)
	profile, err := adapter.PrepareProfile(context.Background(), browser.Session{
		Key: destination.Key(), Proxy: result.Session.Proxy, ProfileRoot: filepath.Join(t.TempDir(), "profiles"),
	}, browser.Installation{ID: "chrome", Executable: chromePath})
	if err != nil {
		t.Fatal(err)
	}
	if err := adapter.Launch(context.Background(), profile, []string{"about:blank"}); err != nil {
		t.Fatalf("several-tabs gate: %v", err)
	}
}

type chromeTabsLauncher struct {
	tabs []string
	hits <-chan string
}

func (l *chromeTabsLauncher) Launch(ctx context.Context, path string, args []string) error {
	arguments := []string{"--headless=new", "--disable-gpu", "--disable-background-networking", "--remote-debugging-port=0"}
	arguments = append(arguments, args...)
	command := exec.CommandContext(ctx, path, arguments...)
	var output strings.Builder
	command.Stdout = &output
	command.Stderr = &output
	if err := command.Start(); err != nil {
		return err
	}
	defer func() { _ = command.Process.Kill(); _ = command.Wait() }()
	profile := ""
	for _, argument := range args {
		if strings.HasPrefix(argument, "--user-data-dir=") {
			profile = strings.TrimPrefix(argument, "--user-data-dir=")
		}
	}
	if profile == "" {
		return fmt.Errorf("Chromium launch omitted its isolated profile")
	}
	var debugPort string
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		contents, err := os.ReadFile(filepath.Join(profile, "DevToolsActivePort"))
		if err == nil {
			debugPort = strings.SplitN(string(contents), "\n", 2)[0]
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if debugPort == "" {
		return fmt.Errorf("Chrome DevTools port did not become ready: %s", output.String())
	}
	for _, target := range l.tabs {
		endpoint := "http://127.0.0.1:" + debugPort + "/json/new?" + url.QueryEscape(target)
		request, _ := http.NewRequestWithContext(ctx, http.MethodPut, endpoint, nil)
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			return fmt.Errorf("open Chrome tab: %w", err)
		}
		response.Body.Close()
		if response.StatusCode != http.StatusOK {
			return fmt.Errorf("open Chrome tab returned %s", response.Status)
		}
	}
	seen := make(map[string]bool)
	deadlineTimer := time.NewTimer(10 * time.Second)
	defer deadlineTimer.Stop()
	for len(seen) < len(l.tabs) {
		select {
		case hit := <-l.hits:
			seen[hit] = true
		case <-deadlineTimer.C:
			return fmt.Errorf("only %d of %d Chrome tabs reached remote loopback: %s", len(seen), len(l.tabs), output.String())
		}
	}
	return nil
}

func restartingWebSocketServer(t *testing.T) (string, func()) {
	t.Helper()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	firstDone := make(chan struct{}, 1)
	first := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		serveBrowserWebSocket(w, request, "before-restart")
		firstDone <- struct{}{}
	})}
	go func() { _ = first.Serve(listener) }()
	ctx, cancel := context.WithCancel(context.Background())
	secondReady := make(chan *http.Server, 1)
	go func() {
		select {
		case <-ctx.Done():
			return
		case <-firstDone:
		}
		_ = first.Close()
		for {
			secondListener, err := net.Listen("tcp4", address)
			if err == nil {
				second := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
					serveBrowserWebSocket(w, request, "after-restart")
				})}
				secondReady <- second
				_ = second.Serve(secondListener)
				return
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(20 * time.Millisecond):
			}
		}
	}()
	return "ws://" + address, func() {
		cancel()
		_ = first.Close()
		select {
		case second := <-secondReady:
			_ = second.Close()
		case <-time.After(time.Second):
		}
	}
}

func requiredBrowser(id string) bool {
	for _, required := range strings.Split(os.Getenv("KAMUI_REQUIRED_BROWSERS"), ",") {
		if strings.TrimSpace(required) == id {
			return true
		}
	}
	return false
}

type browserTestLauncher struct {
	prefix  []string
	want    []string
	reports <-chan string
}

func TestBrowserTestLauncherSynchronizesProcessDiagnostics(t *testing.T) {
	reports := make(chan string, 1)
	time.AfterFunc(20*time.Millisecond, func() { reports <- "completed" })
	launcher := &browserTestLauncher{want: []string{"missing"}, reports: reports}
	if err := launcher.Launch(context.Background(), "/usr/bin/yes", []string{"browser diagnostic"}); err == nil {
		t.Fatal("Launch returned nil, want missing-report error")
	}
}

type synchronizedBuffer struct {
	mu     sync.Mutex
	buffer bytes.Buffer
}

func (b *synchronizedBuffer) Write(data []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.Write(data)
}

func (b *synchronizedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.String()
}

func (l *browserTestLauncher) Launch(ctx context.Context, path string, args []string) error {
	arguments := append([]string(nil), l.prefix...)
	arguments = append(arguments, args...)
	launchContext, cancel := context.WithCancel(ctx)
	defer cancel()
	command := exec.CommandContext(launchContext, path, arguments...)
	var output synchronizedBuffer
	command.Stdout = &output
	command.Stderr = &output
	if err := command.Start(); err != nil {
		return err
	}
	defer func() {
		_ = command.Process.Kill()
		_ = command.Wait()
	}()
	var report string
	select {
	case report = <-l.reports:
	case <-time.After(20 * time.Second):
		return fmt.Errorf("headless browser did not finish protocol checks: %s", output.String())
	}
	for _, wanted := range l.want {
		if !strings.Contains(report, wanted) {
			return fmt.Errorf("headless browser report %q did not contain %q: %s", report, wanted, output.String())
		}
	}
	return nil
}

func browserTestCertificate(t *testing.T) ([]byte, tls.Certificate) {
	t.Helper()
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), cryptorand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	caTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Kamui browser integration CA"},
		NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour),
		IsCA: true, BasicConstraintsValid: true,
		KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
	}
	caDER, err := x509.CreateCertificate(cryptorand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), cryptorand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	leafTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "localhost"},
		DNSNames: []string{"localhost", "*.localhost"}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")},
		NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	leafDER, err := x509.CreateCertificate(cryptorand.Reader, leafTemplate, caTemplate, &leafKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	return caDER, tls.Certificate{Certificate: [][]byte{leafDER, caDER}, PrivateKey: leafKey}
}

func newBrowserTLSServer(handler http.Handler, certificate tls.Certificate) *httptest.Server {
	server := httptest.NewUnstartedServer(handler)
	server.TLS = &tls.Config{Certificates: []tls.Certificate{certificate}}
	server.StartTLS()
	return server
}

func installBrowserTestCA(t *testing.T, certutil, profile string, certificate []byte) {
	t.Helper()
	database := "sql:" + profile
	if output, err := exec.Command(certutil, "-N", "--empty-password", "-d", database).CombinedOutput(); err != nil {
		t.Fatalf("initialize Firefox certificate database: %v: %s", err, output)
	}
	certificatePath := filepath.Join(t.TempDir(), "kamui-browser-test-ca.der")
	if err := os.WriteFile(certificatePath, certificate, 0o600); err != nil {
		t.Fatal(err)
	}
	if output, err := exec.Command(certutil, "-A", "-d", database, "-n", "kamui-browser-integration", "-t", "C,,", "-i", certificatePath).CombinedOutput(); err != nil {
		t.Fatalf("trust Firefox test certificate: %v: %s", err, output)
	}
}

func serveBrowserWebSocket(w http.ResponseWriter, request *http.Request, message string) {
	key := request.Header.Get("Sec-WebSocket-Key")
	if key == "" {
		http.Error(w, "missing WebSocket key", http.StatusBadRequest)
		return
	}
	digest := sha1.Sum([]byte(key + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))
	connection, _, err := w.(http.Hijacker).Hijack()
	if err != nil {
		return
	}
	defer connection.Close()
	fmt.Fprintf(connection, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: %s\r\n\r\n", base64.StdEncoding.EncodeToString(digest[:]))
	frame := []byte{0x81, byte(len(message))}
	frame = append(frame, message...)
	_, _ = connection.Write(frame)
	time.Sleep(200 * time.Millisecond)
}
