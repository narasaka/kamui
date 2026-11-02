package integration_test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/narasaka/kamui/internal/session"
	"github.com/narasaka/kamui/internal/ssh"
)

// TestWakeReplacesFrozenOpenSSHConnection freezes the remote end of a real
// OpenSSH session, as a network that went away while the laptop slept would,
// and checks that a wakeup replaces it long before OpenSSH's server-alive
// limit would notice.
func TestWakeReplacesFrozenOpenSSHConnection(t *testing.T) {
	fixture := startDisposableOpenSSH(t)
	clientConfig := filepath.Join(fixture.root, "wake_ssh_config")
	configuration := fmt.Sprintf(`Host kamui-wake-alias
    HostName 127.0.0.1
    User %s
    Port %d
    IdentityFile %s
    IdentitiesOnly yes
    UserKnownHostsFile %s
    StrictHostKeyChecking no
`, fixture.username, fixture.port, fixture.clientKey, filepath.Join(fixture.root, "known_hosts"))
	if err := os.WriteFile(clientConfig, []byte(configuration), 0o600); err != nil {
		t.Fatal(err)
	}
	wrapper := writeSSHWrapper(t, fixture.root, clientConfig)
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "through real OpenSSH")
	}))
	t.Cleanup(backend.Close)
	backendURL := "http://localhost:" + strings.TrimPrefix(backend.URL, "http://127.0.0.1:")

	var sshLog bytes.Buffer
	manager := session.NewManagerWithOptions(session.ManagerOptions{
		Transport:    ssh.Transport{SSHPath: wrapper, Stderr: &sshLog, ReadinessTimeout: 5 * time.Second},
		ProbeTimeout: time.Second,
	})
	t.Cleanup(func() { _ = manager.Close() })
	destination, err := session.ParseDestination("kamui-wake-alias")
	if err != nil {
		t.Fatal(err)
	}
	result, err := manager.Execute(context.Background(), session.Command{Operation: session.Ensure, Destination: destination})
	if err != nil {
		t.Fatalf("connect: %v\nOpenSSH: %s", err, sshLog.String())
	}
	proxyURL, _ := url.Parse("http://" + result.Session.Proxy.String())
	client := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL), DisableKeepAlives: true}}
	get := func() error {
		response, err := client.Get(backendURL)
		if err != nil {
			return err
		}
		defer func() { _ = response.Body.Close() }()
		body, err := io.ReadAll(response.Body)
		if err == nil && string(body) != "through real OpenSSH" {
			err = fmt.Errorf("body = %q", body)
		}
		return err
	}
	if err := get(); err != nil {
		t.Fatalf("request before freezing: %v", err)
	}

	frozen := freezeSSHSessions(t, filepath.Join(fixture.root, "sshd.pid"))
	if response, err := (&http.Client{Timeout: 500 * time.Millisecond, Transport: client.Transport}).Get(backendURL); err == nil {
		_ = response.Body.Close()
		t.Fatal("request through the frozen session succeeded; the fixture did not freeze it")
	}

	woke := time.Now()
	manager.Wake("test")
	deadline := woke.Add(15 * time.Second)
	for {
		err := get()
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("request after wakeup: %v\nOpenSSH: %s", err, sshLog.String())
		}
		time.Sleep(100 * time.Millisecond)
	}
	if elapsed := time.Since(woke); elapsed > 10*time.Second {
		t.Fatalf("recovery took %s after wakeup, want well under OpenSSH's 45-second server-alive limit", elapsed)
	}
	t.Logf("froze sshd processes %v; recovered %s after wakeup", frozen, time.Since(woke).Round(time.Millisecond))
}

// freezeSSHSessions stops every descendant of the disposable sshd listener,
// which serve the connections already open, and leaves the listener free to
// accept the reconnect.
func freezeSSHSessions(t *testing.T, pidFile string) []int {
	t.Helper()
	raw, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatal(err)
	}
	listener, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil {
		t.Fatal(err)
	}
	var descendants []int
	pending := []int{listener}
	for len(pending) > 0 {
		parent := pending[0]
		pending = pending[1:]
		output, _ := exec.Command("pgrep", "-P", strconv.Itoa(parent)).Output()
		for _, field := range strings.Fields(string(output)) {
			child, err := strconv.Atoi(field)
			if err != nil {
				t.Fatal(err)
			}
			descendants = append(descendants, child)
			pending = append(pending, child)
		}
	}
	if len(descendants) == 0 {
		t.Fatal("sshd listener has no session processes to freeze")
	}
	for _, pid := range descendants {
		if err := syscall.Kill(pid, syscall.SIGSTOP); err != nil {
			t.Fatalf("freeze sshd process %d: %v", pid, err)
		}
	}
	t.Cleanup(func() {
		for _, pid := range descendants {
			_ = syscall.Kill(pid, syscall.SIGCONT)
		}
	})
	return descendants
}
