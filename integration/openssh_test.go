package integration_test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/narasaka/kamui/internal/session"
	"github.com/narasaka/kamui/internal/ssh"
)

func TestColdStartWithDisposableOpenSSHServer(t *testing.T) {
	fixture := startDisposableOpenSSH(t)
	clientConfig := filepath.Join(fixture.root, "ssh_config")
	clientConfiguration := fmt.Sprintf(`Host kamui-test-alias 127.0.0.1
    HostName 127.0.0.1
    User %s
    Port %d
    IdentityFile %s
    IdentitiesOnly yes
    UserKnownHostsFile %s
    StrictHostKeyChecking no
`, fixture.username, fixture.port, fixture.clientKey, filepath.Join(fixture.root, "known_hosts"))
	if err := os.WriteFile(clientConfig, []byte(clientConfiguration), 0o600); err != nil {
		t.Fatal(err)
	}
	wrapper := writeSSHWrapper(t, fixture.root, clientConfig)

	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "through real OpenSSH")
	}))
	t.Cleanup(backend.Close)
	backendPort := strings.TrimPrefix(backend.URL, "http://127.0.0.1:")

	for _, destinationText := range []string{"kamui-test-alias", fixture.username + "@127.0.0.1"} {
		t.Run(destinationText, func(t *testing.T) {
			var sshLog bytes.Buffer
			manager := session.NewManager(ssh.Transport{
				SSHPath: wrapper, Stderr: &sshLog, ReadinessTimeout: 5 * time.Second,
			})
			t.Cleanup(func() { _ = manager.Close() })
			destination, err := session.ParseDestination(destinationText)
			if err != nil {
				t.Fatal(err)
			}
			result, err := manager.Execute(context.Background(), session.Command{Operation: session.Ensure, Destination: destination})
			if err != nil {
				t.Fatalf("cold start: %v\nOpenSSH: %s", err, sshLog.String())
			}
			proxyURL, _ := url.Parse("http://" + result.Session.Proxy.String())
			client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)}}
			response, err := client.Get("http://localhost:" + backendPort)
			if err != nil {
				t.Fatalf("request through OpenSSH: %v", err)
			}
			body, _ := io.ReadAll(response.Body)
			response.Body.Close()
			if string(body) != "through real OpenSSH" {
				t.Fatalf("body = %q", body)
			}
		})
	}
}

func TestOpenSSHHookSupportsConcurrentMultiplexedLoginsAndFailedAuthentication(t *testing.T) {
	fixture := startDisposableOpenSSH(t)
	hookLog := filepath.Join(fixture.root, "hook.log")
	hookRecorder := filepath.Join(fixture.root, "hook-recorder")
	hookContents := fmt.Sprintf("#!/bin/sh\nprintf '%%s\\n' \"$1\" >> %q\n", hookLog)
	if err := os.WriteFile(hookRecorder, []byte(hookContents), 0o700); err != nil {
		t.Fatal(err)
	}
	clientConfig := filepath.Join(fixture.root, "hook_ssh_config")
	controlPath := fmt.Sprintf("/tmp/kamui-%d-%%C", fixture.port)
	configuration := fmt.Sprintf(`Host kamui-hook-alias
    HostName 127.0.0.1
    User %s
    Port %d
    IdentityFile %s
    IdentitiesOnly yes
    UserKnownHostsFile %s
    StrictHostKeyChecking no
    PermitLocalCommand yes
    LocalCommand %q %%n
    ControlMaster auto
    ControlPath %q
    ControlPersist 10
Host kamui-failed-alias
    HostName 127.0.0.1
    User %s
    Port %d
    IdentityFile %s
    IdentitiesOnly yes
    UserKnownHostsFile %s
    StrictHostKeyChecking no
    ControlMaster no
`, fixture.username, fixture.port, fixture.clientKey, filepath.Join(fixture.root, "known_hosts"),
		hookRecorder, controlPath, fixture.username, fixture.port,
		filepath.Join(fixture.root, "missing-key"), filepath.Join(fixture.root, "known_hosts"))
	if err := os.WriteFile(clientConfig, []byte(configuration), 0o600); err != nil {
		t.Fatal(err)
	}
	wrapper := writeSSHWrapper(t, fixture.root, clientConfig)
	if output, err := exec.Command(wrapper, "-MNf", "kamui-hook-alias").CombinedOutput(); err != nil {
		t.Fatalf("start multiplex master: %v: %s", err, output)
	}
	t.Cleanup(func() { _ = exec.Command(wrapper, "-O", "exit", "kamui-hook-alias").Run() })

	const terminals = 4
	started := time.Now()
	var wait sync.WaitGroup
	errors := make(chan error, terminals)
	for range terminals {
		wait.Add(1)
		go func() {
			defer wait.Done()
			if output, err := exec.Command(wrapper, "kamui-hook-alias", "true").CombinedOutput(); err != nil {
				errors <- fmt.Errorf("interactive login: %w: %s", err, output)
			}
		}()
	}
	wait.Wait()
	close(errors)
	for err := range errors {
		t.Error(err)
	}
	if elapsed := time.Since(started); elapsed > 3*time.Second {
		t.Fatalf("concurrent hook logins took %v", elapsed)
	}
	contents, err := os.ReadFile(hookLog)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Fields(string(contents))
	if len(lines) != 1 {
		t.Fatalf("hook executions = %d, want one activation on the multiplex master; log %q", len(lines), contents)
	}
	for _, line := range lines {
		if line != "kamui-hook-alias" {
			t.Fatalf("LocalCommand %%n = %q, want original alias", line)
		}
	}

	if output, err := exec.Command(wrapper, "-o", "BatchMode=yes", "kamui-failed-alias", "true").CombinedOutput(); err == nil {
		t.Fatalf("failed-login fixture authenticated unexpectedly: %s", output)
	}
}

type openSSHFixture struct {
	root      string
	username  string
	port      int
	clientKey string
}

func startDisposableOpenSSH(t *testing.T) openSSHFixture {
	t.Helper()
	sshdPath := "/usr/sbin/sshd"
	if runtime.GOOS != "darwin" {
		if path, err := exec.LookPath("sshd"); err == nil {
			sshdPath = path
		} else {
			t.Skip("disposable sshd is not installed")
		}
	}
	currentUser, err := user.Current()
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	hostKey := filepath.Join(root, "host-key")
	clientKey := filepath.Join(root, "client-key")
	generateKey(t, hostKey)
	generateKey(t, clientKey)
	publicKey, err := os.ReadFile(clientKey + ".pub")
	if err != nil {
		t.Fatal(err)
	}
	authorizedKeys := filepath.Join(root, "authorized_keys")
	if err := os.WriteFile(authorizedKeys, publicKey, 0o600); err != nil {
		t.Fatal(err)
	}
	port := availablePort(t)
	serverConfig := filepath.Join(root, "sshd_config")
	serverConfiguration := fmt.Sprintf(`Port %d
ListenAddress 127.0.0.1
HostKey %s
PidFile %s
AuthorizedKeysFile %s
PasswordAuthentication no
KbdInteractiveAuthentication no
PubkeyAuthentication yes
StrictModes no
UsePAM no
LogLevel ERROR
`, port, hostKey, filepath.Join(root, "sshd.pid"), authorizedKeys)
	if err := os.WriteFile(serverConfig, []byte(serverConfiguration), 0o600); err != nil {
		t.Fatal(err)
	}
	var serverLog bytes.Buffer
	server := exec.Command(sshdPath, "-D", "-e", "-f", serverConfig)
	server.Stderr = &serverLog
	if err := server.Start(); err != nil {
		t.Fatalf("start sshd: %v", err)
	}
	t.Cleanup(func() { _ = server.Process.Kill(); _ = server.Wait() })
	waitForListener(t, fmt.Sprintf("127.0.0.1:%d", port), &serverLog)
	return openSSHFixture{root: root, username: currentUser.Username, port: port, clientKey: clientKey}
}

func writeSSHWrapper(t *testing.T, root, clientConfig string) string {
	t.Helper()
	wrapper := filepath.Join(root, "ssh-wrapper")
	wrapperContents := fmt.Sprintf("#!/bin/sh\nexec /usr/bin/ssh -F %q \"$@\"\n", clientConfig)
	if err := os.WriteFile(wrapper, []byte(wrapperContents), 0o700); err != nil {
		t.Fatal(err)
	}
	return wrapper
}

func generateKey(t *testing.T, path string) {
	t.Helper()
	command := exec.Command("/usr/bin/ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-f", path)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("generate key: %v: %s", err, output)
	}
}

func availablePort(t *testing.T) int {
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

func waitForListener(t *testing.T, address string, log *bytes.Buffer) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		connection, err := net.DialTimeout("tcp", address, 50*time.Millisecond)
		if err == nil {
			connection.Close()
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("sshd did not listen on %s: %s", address, log.String())
}
