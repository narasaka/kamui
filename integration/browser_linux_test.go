//go:build linux

package integration_test

import (
	"context"
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

func TestNativeLinuxBrowsersUseKamuiProxy(t *testing.T) {
	if os.Getenv("KAMUI_BROWSER_TESTS") != "1" {
		t.Skip("set KAMUI_BROWSER_TESTS=1 to launch installed browsers headlessly")
	}

	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		_, _ = w.Write([]byte("remote-linux-browser"))
	}))
	defer backend.Close()
	reports := make(chan string, 1)
	reportServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		reports <- request.URL.Query().Get("value")
		w.WriteHeader(http.StatusNoContent)
	}))
	defer reportServer.Close()
	backendPort := strings.TrimPrefix(backend.URL, "http://127.0.0.1:")
	reportPort := strings.TrimPrefix(reportServer.URL, "http://127.0.0.1:")
	page := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprintf(w, `<script type="module">
const value = await fetch("http://localhost:%s").then(response => response.text());
await fetch("http://localhost:%s/report?value=" + encodeURIComponent(value));
</script>`, backendPort, reportPort)
	}))
	defer page.Close()

	manager := session.NewManager(ssh.Transport{Launcher: &testsupport.SSHLauncher{}, ReadinessTimeout: time.Second})
	t.Cleanup(func() { _ = manager.Close() })
	destination, _ := session.ParseDestination("linux-browser-gate")
	result, err := manager.Execute(context.Background(), session.Command{Operation: session.Ensure, Destination: destination})
	if err != nil {
		t.Fatal(err)
	}
	pagePort := strings.TrimPrefix(page.URL, "http://127.0.0.1:")

	for _, browserID := range []string{"chrome", "chromium", "firefox"} {
		browserID := browserID
		t.Run(browserID, func(t *testing.T) {
			launcher := &linuxBrowserLauncher{browserID: browserID, reports: reports}
			catalog := browser.NewCatalog(browser.DefaultAdapters(launcher))
			selected, err := catalog.Select(context.Background(), browser.Selection{Explicit: browserID})
			if err != nil {
				if requiredBrowser(browserID) {
					t.Fatalf("required native Linux browser: %v", err)
				}
				t.Skip(err)
			}
			profile, err := selected.Adapter.PrepareProfile(context.Background(), browser.Session{
				Key: destination.Key(), Proxy: result.Session.Proxy, ProfileRoot: filepath.Join(t.TempDir(), "profiles"),
			}, selected.Installation)
			if err != nil {
				t.Fatal(err)
			}
			if err := selected.Adapter.Launch(context.Background(), profile, []string{"http://localhost:" + pagePort}); err != nil {
				t.Fatal(err)
			}
		})
	}
}

type linuxBrowserLauncher struct {
	browserID string
	reports   <-chan string
}

func (l *linuxBrowserLauncher) Launch(ctx context.Context, path string, args []string) error {
	prefix := []string{"--headless=new", "--disable-gpu", "--disable-background-networking"}
	if l.browserID == "firefox" {
		prefix = []string{"-headless"}
	} else if os.Geteuid() == 0 {
		// Chromium refuses to start as root without this flag. Production launches
		// never add it; this is only for integration tests in root-owned containers.
		prefix = append(prefix, "--no-sandbox")
	}
	launchContext, cancel := context.WithCancel(ctx)
	defer cancel()
	command := exec.CommandContext(launchContext, path, append(prefix, args...)...)
	command.Stdout = io.Discard
	command.Stderr = io.Discard
	if err := command.Start(); err != nil {
		return err
	}
	defer func() {
		_ = command.Process.Kill()
		_ = command.Wait()
	}()
	select {
	case report := <-l.reports:
		if report != "remote-linux-browser" {
			return fmt.Errorf("browser report = %q, want remote-linux-browser", report)
		}
		return nil
	case <-time.After(60 * time.Second):
		return fmt.Errorf("native Linux browser did not finish proxy check")
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
