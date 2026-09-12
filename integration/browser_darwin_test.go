//go:build darwin

package integration_test

import (
	"context"
	"crypto/sha1"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/narasaka/kamui/internal/browser"
	"github.com/narasaka/kamui/internal/session"
	"github.com/narasaka/kamui/internal/ssh"
	"github.com/narasaka/kamui/internal/testsupport"
)

func TestInstalledChromiumBrowsersUseKamuiProxy(t *testing.T) {
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
	httpsServer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		_, _ = io.WriteString(w, "<html><body>remote-https</body></html>")
	}))
	defer httpsServer.Close()

	webSocket := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		serveBrowserWebSocket(w, r, "ws-ok")
	}))
	defer webSocket.Close()
	secureWebSocket := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		serveBrowserWebSocket(w, r, "wss-ok")
	}))
	defer secureWebSocket.Close()
	reports := make(chan string, 2)
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
	httpPorts := make([]string, 0, len(httpServers))
	for _, server := range httpServers {
		httpPorts = append(httpPorts, strings.TrimPrefix(server.URL, "http://127.0.0.1:"))
	}
	page := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(w, `<html><body>waiting<script type="module">
const socketResult=(target)=>new Promise((resolve,reject)=>{const socket=new WebSocket(target);socket.onmessage=(event)=>resolve(event.data);socket.onerror=reject;});
const values=await Promise.all([
  fetch("http://localhost:%s").then(r=>r.text()),
  fetch("http://localhost:%s").then(r=>r.text()),
  fetch("http://localhost:%s").then(r=>r.text()),
  fetch("http://localhost:%s").then(r=>r.text()),
  fetch("https://localhost:%s").then(r=>r.text()),
  socketResult("ws://localhost:%s"),
  socketResult("wss://localhost:%s")
]);
document.body.textContent=values.join("|");
await fetch("http://localhost:%s/report?value="+encodeURIComponent(values.join("|")));
</script></body></html>`, httpPorts[0], httpPorts[1], httpPorts[2], httpPorts[3], httpsPort, wsPort, wssPort, reportPort)
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
				t.Skipf("%s is not installed", installed.id)
			}
			launcher := &headlessLauncher{reports: reports}
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
				"remote-https", "ws-ok", "wss-ok",
			}
			if err := adapter.Launch(context.Background(), profile, []string{"http://localhost:" + pagePort}); err != nil {
				t.Fatalf("browser protocol gate: %v", err)
			}
		})
	}
}

type headlessLauncher struct {
	want    []string
	reports <-chan string
}

func (l *headlessLauncher) Launch(ctx context.Context, path string, args []string) error {
	arguments := []string{
		"--headless=new", "--disable-gpu", "--disable-background-networking",
		"--disable-component-update", "--disable-sync", "--ignore-certificate-errors",
	}
	arguments = append(arguments, args...)
	launchContext, cancel := context.WithCancel(ctx)
	defer cancel()
	command := exec.CommandContext(launchContext, path, arguments...)
	var output strings.Builder
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
