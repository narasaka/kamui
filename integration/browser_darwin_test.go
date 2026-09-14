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
	"errors"
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
	"syscall"
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
			_, _ = fmt.Fprintf(w, "<html><body>remote-http-%d</body></html>", index)
		}))
		defer httpServers[index].Close()
	}
	caCertificate, serverCertificate := browserTestCertificate(t)
	httpsServer := newBrowserTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		_, _ = io.WriteString(w, "<html><body>remote-https</body></html>")
	}), serverCertificate)
	defer httpsServer.Close()
	webSocket := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		serveBrowserWebSocket(w, r, "ws-ok")
	}))
	defer webSocket.Close()
	secureWebSocket := newBrowserTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		serveBrowserWebSocket(w, r, "wss-ok")
	}), serverCertificate)
	defer secureWebSocket.Close()
	reports, reportServer := newBrowserReportServer()
	defer reportServer.Close()
	wsPort := strings.TrimPrefix(webSocket.URL, "http://127.0.0.1:")
	wssPort := strings.TrimPrefix(secureWebSocket.URL, "https://127.0.0.1:")
	reportPort := strings.TrimPrefix(reportServer.URL, "http://127.0.0.1:")
	httpsPort := strings.TrimPrefix(httpsServer.URL, "https://127.0.0.1:")
	httpPorts := make([]string, 0, len(httpServers))
	for _, server := range httpServers {
		httpPorts = append(httpPorts, strings.TrimPrefix(server.URL, "http://127.0.0.1:"))
	}
	// The page echoes the launch token from its query string on every report so a
	// late report from one browser launch can never satisfy another. It beacons
	// "loaded" before any protocol check and annotates every result with elapsed
	// time, and every WebSocket with its readyState, so a stalled check is
	// distinguishable from a browser that never ran the page.
	page := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprintf(w, `<html><body>waiting<script type="module">
const token=new URLSearchParams(location.search).get("token");
const started=performance.now();
const elapsed=()=>"@"+Math.round(performance.now()-started)+"ms";
const report=(stage,value)=>fetch("http://localhost:%s/report?token="+encodeURIComponent(token)+"&stage="+stage+"&value="+encodeURIComponent(value));
report("loaded",navigator.userAgent).catch(()=>{});
const socketResult=(target)=>{let socket;const promise=new Promise((resolve,reject)=>{
  socket=new WebSocket(target);
  socket.onmessage=(event)=>resolve(event.data);
  socket.onerror=()=>reject("socket error readyState="+socket.readyState);
  socket.onclose=(event)=>reject("socket closed code="+event.code+" clean="+event.wasClean);
});promise.describe=()=>"[readyState="+socket.readyState+"]";return promise;};
const inspect=(name,promise)=>Promise.race([
  promise.then(value=>name+"="+value+elapsed()).catch(error=>name+"=ERROR:"+error+elapsed()),
  new Promise(resolve=>setTimeout(()=>resolve(name+"=TIMEOUT"+(promise.describe?promise.describe():"")+elapsed()),10000))
]);
const values=await Promise.all([
  inspect("http-localhost",fetch("http://localhost:%s").then(r=>r.text())),
  inspect("http-subdomain",fetch("http://app.localhost:%s").then(r=>r.text())),
  inspect("http-ipv4",fetch("http://127.42.0.1:%s").then(r=>r.text())),
  inspect("http-ipv6",fetch("http://[::1]:%s").then(r=>r.text())),
  inspect("https",fetch("https://localhost:%s").then(r=>r.text())),
  inspect("websocket",socketResult("ws://localhost:%s")),
  inspect("secure-websocket",socketResult("wss://localhost:%s"))
]);
document.body.textContent=values.join("|");
await report("result",values.join("|"));
</script></body></html>`, reportPort, httpPorts[0], httpPorts[1], httpPorts[2], httpPorts[3], httpsPort, wsPort, wssPort)
	}))
	defer page.Close()
	pagePort := strings.TrimPrefix(page.URL, "http://127.0.0.1:")
	wantReport := []string{
		"remote-http-0", "remote-http-1", "remote-http-2", "remote-http-3",
		"remote-https", "ws-ok", "wss-ok",
	}

	manager := session.NewManager(ssh.Transport{Launcher: &testsupport.SSHLauncher{}, ReadinessTimeout: time.Second})
	t.Cleanup(func() { _ = manager.Close() })
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
				prefix:  append(chromiumHeadlessFlags(), "--disable-component-update", "--disable-sync", "--ignore-certificate-errors"),
				want:    wantReport,
				reports: reports,
				logf:    t.Logf,
			}
			adapter := browser.NewChromiumAdapter(installed.id, []string{installed.path}, launcher)
			runBrowserGate(t, installed.id, launcher, func(token string) error {
				profile, err := adapter.PrepareProfile(context.Background(), browser.Session{
					Key: destination.Key(), Proxy: result.Session.Proxy,
					ProfileRoot: filepath.Join(t.TempDir(), "profiles"),
				}, browser.Installation{ID: installed.id, Executable: installed.path})
				if err != nil {
					return err
				}
				return adapter.Launch(context.Background(), profile, []string{"http://localhost:" + pagePort + "/?token=" + token})
			})
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
			launcher := &browserTestLauncher{prefix: []string{"-headless"}, want: wantReport, reports: reports, logf: t.Logf}
			adapter := browser.NewGeckoAdapter(installed.id, []string{installed.path}, launcher)
			runBrowserGate(t, installed.id, launcher, func(token string) error {
				profile, err := adapter.PrepareProfile(context.Background(), browser.Session{
					Key: destination.Key(), Proxy: result.Session.Proxy,
					ProfileRoot: filepath.Join(t.TempDir(), "profiles"),
				}, browser.Installation{ID: installed.id, Executable: installed.path})
				if err != nil {
					return err
				}
				if err := quietGeckoTestProfile(profile.Path); err != nil {
					return err
				}
				installBrowserTestCA(t, certutil, profile.Path, caCertificate)
				return adapter.Launch(context.Background(), profile, []string{"http://localhost:" + pagePort + "/?token=" + token})
			})
		})
	}
}

// browserGateAttempts is how many fresh-profile launches one browser gets before
// the gate fails. A first-launch stall on a cold CI runner is not a proxy
// regression; a browser that fails twice in a row is.
const browserGateAttempts = 2

// runBrowserGate launches one browser through launch, retrying once with a new
// token and profile when the first attempt fails. Every attempt is logged and a
// retried pass is surfaced as a GitHub Actions warning so retries stay visible.
func runBrowserGate(t *testing.T, id string, launcher *browserTestLauncher, launch func(token string) error) {
	t.Helper()
	var failures []error
	for attempt := 1; attempt <= browserGateAttempts; attempt++ {
		token := fmt.Sprintf("%s-%d-%d", id, attempt, time.Now().UnixNano())
		launcher.token = token
		started := time.Now()
		err := launch(token)
		if err == nil {
			if attempt > 1 {
				message := fmt.Sprintf("%s passed on attempt %d/%d after: %v", id, attempt, browserGateAttempts, errors.Join(failures...))
				t.Log(message)
				if os.Getenv("GITHUB_ACTIONS") == "true" {
					fmt.Printf("::warning title=Browser gate retried::%s\n", strings.ReplaceAll(message, "\n", " "))
				}
			}
			return
		}
		t.Logf("%s attempt %d/%d failed after %s: %v", id, attempt, browserGateAttempts, time.Since(started).Round(time.Millisecond), err)
		failures = append(failures, fmt.Errorf("attempt %d: %w", attempt, err))
	}
	t.Fatalf("browser protocol gate: %s failed %d attempts: %v", id, browserGateAttempts, errors.Join(failures...))
}

// chromiumHeadlessFlags skips first-run and default-browser work so a new
// profile spends its startup on the page under test.
func chromiumHeadlessFlags() []string {
	return []string{"--headless=new", "--disable-gpu", "--disable-background-networking", "--no-first-run", "--no-default-browser-check"}
}

// quietGeckoTestProfile appends test-only preferences to the Kamui user.js so a
// new profile does not spend its first seconds on update checks, captive portal
// probes, or onboarding pages that all route through the proxy and fail.
// Production profiles keep the minimal user.js from ADR 0001.
func quietGeckoTestProfile(profilePath string) error {
	file, err := os.OpenFile(filepath.Join(profilePath, "user.js"), os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("open test user.js: %w", err)
	}
	_, err = file.WriteString(`// Test-only first-run quieting appended by the integration gate.
user_pref("app.update.disabledForTesting", true);
user_pref("browser.shell.checkDefaultBrowser", false);
user_pref("browser.startup.homepage_override.mstone", "ignore");
user_pref("browser.aboutwelcome.enabled", false);
user_pref("datareporting.policy.dataSubmissionPolicyBypassNotification", true);
user_pref("network.captive-portal-service.enabled", false);
user_pref("network.connectivity-service.enabled", false);
user_pref("toolkit.telemetry.reportingpolicy.firstRun", false);
`)
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return fmt.Errorf("append test user.js: %w", err)
	}
	return nil
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
	reports, reportServer := newBrowserReportServer()
	defer reportServer.Close()
	wsPort := strings.TrimPrefix(webSocketURL, "ws://127.0.0.1:")
	reportPort := strings.TrimPrefix(reportServer.URL, "http://127.0.0.1:")
	page := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprintf(w, `<script type="module">
const token=new URLSearchParams(location.search).get("token");
const report=(stage,value)=>fetch("http://localhost:%s/report?token="+encodeURIComponent(token)+"&stage="+stage+"&value="+encodeURIComponent(value));
report("loaded",navigator.userAgent).catch(()=>{});
const once=()=>new Promise((resolve,reject)=>{const socket=new WebSocket("ws://localhost:%s");socket.onmessage=e=>resolve(e.data);socket.onerror=reject;});
const first=await once();
await new Promise(resolve=>setTimeout(resolve, 700));
let second;
for(let attempt=0;attempt<20&&!second;attempt++){try{second=await once();}catch{await new Promise(resolve=>setTimeout(resolve,100));}}
await report("result",first+"|"+second);
</script>`, reportPort, wsPort)
	}))
	defer page.Close()

	manager := session.NewManager(ssh.Transport{Launcher: &testsupport.SSHLauncher{}, ReadinessTimeout: time.Second})
	t.Cleanup(func() { _ = manager.Close() })
	destination, _ := session.ParseDestination("browser-hmr-gate")
	result, err := manager.Execute(context.Background(), session.Command{Operation: session.Ensure, Destination: destination})
	if err != nil {
		t.Fatal(err)
	}
	launcher := &browserTestLauncher{
		prefix:  append(chromiumHeadlessFlags(), "--disable-component-update", "--disable-sync"),
		reports: reports, want: []string{"before-restart", "after-restart"}, logf: t.Logf,
	}
	adapter := browser.NewChromiumAdapter("chrome", []string{chromePath}, launcher)
	pagePort := strings.TrimPrefix(page.URL, "http://127.0.0.1:")
	runBrowserGate(t, "chrome-reconnect", launcher, func(token string) error {
		profile, err := adapter.PrepareProfile(context.Background(), browser.Session{
			Key: destination.Key(), Proxy: result.Session.Proxy, ProfileRoot: filepath.Join(t.TempDir(), "profiles"),
		}, browser.Installation{ID: "chrome", Executable: chromePath})
		if err != nil {
			return err
		}
		return adapter.Launch(context.Background(), profile, []string{"http://localhost:" + pagePort + "/?token=" + token})
	})
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
			_, _ = fmt.Fprintf(w, "tab-%d", index)
		}))
		defer servers[index].Close()
	}
	manager := session.NewManager(ssh.Transport{Launcher: &testsupport.SSHLauncher{}, ReadinessTimeout: time.Second})
	t.Cleanup(func() { _ = manager.Close() })
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
	arguments := append(chromiumHeadlessFlags(), "--remote-debugging-port=0")
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
		_ = response.Body.Close()
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

// browserReport is one beacon from the page under test. Stage is "loaded" when
// the page script started and "result" when the protocol checks finished.
type browserReport struct {
	token string
	stage string
	value string
}

// newBrowserReportServer serves the page's report endpoint. Reports are
// delivered on a buffered channel and dropped rather than blocking the handler
// if the buffer is ever full, so a chatty browser cannot wedge the test.
func newBrowserReportServer() (<-chan browserReport, *httptest.Server) {
	reports := make(chan browserReport, 64)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		query := request.URL.Query()
		report := browserReport{token: query.Get("token"), stage: query.Get("stage"), value: query.Get("value")}
		select {
		case reports <- report:
		default:
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	return reports, server
}

// browserLaunchTimeout bounds one headless launch. The page itself caps each
// protocol check at ten seconds, so the remainder covers a cold first launch on
// a CI runner that is still scanning a freshly installed bundle.
const browserLaunchTimeout = 45 * time.Second

type browserTestLauncher struct {
	prefix  []string
	want    []string
	token   string
	reports <-chan browserReport
	logf    func(format string, args ...any)
}

func TestBrowserTestLauncherSynchronizesProcessDiagnostics(t *testing.T) {
	reports := make(chan browserReport, 1)
	time.AfterFunc(20*time.Millisecond, func() { reports <- browserReport{token: "t", stage: "result", value: "completed"} })
	launcher := &browserTestLauncher{want: []string{"missing"}, token: "t", reports: reports}
	if err := launcher.Launch(context.Background(), "/usr/bin/yes", []string{"browser diagnostic"}); err == nil {
		t.Fatal("Launch returned nil, want missing-report error")
	}
}

func TestBrowserTestLauncherIgnoresReportsForOtherTokens(t *testing.T) {
	reports := make(chan browserReport, 2)
	reports <- browserReport{token: "stale", stage: "result", value: "wanted"}
	time.AfterFunc(20*time.Millisecond, func() { reports <- browserReport{token: "live", stage: "result", value: "wanted"} })
	launcher := &browserTestLauncher{want: []string{"wanted"}, token: "live", reports: reports}
	if err := launcher.Launch(context.Background(), "/usr/bin/yes", []string{"browser diagnostic"}); err != nil {
		t.Fatalf("Launch = %v, want the live-token report to satisfy the launch", err)
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
	// Browsers fork helper processes. Start the browser in its own process group
	// so teardown removes the helpers too instead of leaving them to compete
	// with the next browser for the runner's CPU.
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	var output synchronizedBuffer
	command.Stdout = &output
	command.Stderr = &output
	if err := command.Start(); err != nil {
		return err
	}
	started := time.Now()
	defer func() {
		_ = syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
		_ = command.Process.Kill()
		_ = command.Wait()
	}()
	deadline := time.NewTimer(browserLaunchTimeout)
	defer deadline.Stop()
	var loaded time.Duration
	var loadedAgent string
	for {
		var report browserReport
		select {
		case report = <-l.reports:
		case <-deadline.C:
			if loaded == 0 {
				return fmt.Errorf("headless browser never loaded the page within %s: %s", browserLaunchTimeout, output.String())
			}
			return fmt.Errorf("headless browser loaded the page after %s (%s) but did not report protocol checks within %s: %s",
				loaded.Round(time.Millisecond), loadedAgent, browserLaunchTimeout, output.String())
		}
		if report.token != l.token {
			l.log("ignoring report for another launch: token=%q stage=%s value=%q", report.token, report.stage, report.value)
			continue
		}
		switch report.stage {
		case "loaded":
			loaded = time.Since(started)
			loadedAgent = report.value
			l.log("page loaded after %s: %s", loaded.Round(time.Millisecond), loadedAgent)
			continue
		case "result":
		default:
			l.log("ignoring unknown report stage %q: %q", report.stage, report.value)
			continue
		}
		l.log("protocol checks reported after %s: %s", time.Since(started).Round(time.Millisecond), report.value)
		for _, wanted := range l.want {
			if !strings.Contains(report.value, wanted) {
				return fmt.Errorf("headless browser report %q did not contain %q (page loaded after %s): %s",
					report.value, wanted, loaded.Round(time.Millisecond), output.String())
			}
		}
		return nil
	}
}

func (l *browserTestLauncher) log(format string, args ...any) {
	if l.logf != nil {
		l.logf(format, args...)
	}
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
	defer func() { _ = connection.Close() }()
	_, _ = fmt.Fprintf(connection, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: %s\r\n\r\n", base64.StdEncoding.EncodeToString(digest[:]))
	frame := []byte{0x81, byte(len(message))}
	frame = append(frame, message...)
	_, _ = connection.Write(frame)
	time.Sleep(200 * time.Millisecond)
}
