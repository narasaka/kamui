package integration_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/narasaka/kamui/internal/askpass"
)

// The menu bar app and the terminal are two independent routes to the same
// controller. The app runs `kamui mirror --json-prompts`, answers OpenSSH's
// prompts itself, and polls `kamui status --json`; the terminal runs the plain
// commands. Each route must see and stop the other's sessions.
func TestAppAndTerminalRoutesShareSessionsThroughOneController(t *testing.T) {
	fixture := startDisposableOpenSSH(t)
	const passphrase = "open sesame"
	key := filepath.Join(fixture.root, "passphrase-key")
	keygen := exec.Command("/usr/bin/ssh-keygen", "-q", "-t", "ed25519", "-N", passphrase, "-f", key)
	if output, err := keygen.CombinedOutput(); err != nil {
		t.Fatalf("generate passphrase key: %v: %s", err, output)
	}
	publicKey, err := os.ReadFile(key + ".pub")
	if err != nil {
		t.Fatal(err)
	}
	authorized, err := os.OpenFile(filepath.Join(fixture.root, "authorized_keys"), os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := authorized.Write(publicKey); err != nil {
		t.Fatal(err)
	}
	if err := authorized.Close(); err != nil {
		t.Fatal(err)
	}
	knownHosts := filepath.Join(fixture.root, "routes_known_hosts")
	clientConfig := filepath.Join(fixture.root, "routes_ssh_config")
	configuration := fmt.Sprintf(`Host app-route
    HostName 127.0.0.1
    User %[1]s
    Port %[2]d
    IdentityFile %[3]s
    IdentitiesOnly yes
    IdentityAgent none
    UserKnownHostsFile %[4]s
    StrictHostKeyChecking ask

Host terminal-route
    HostName 127.0.0.1
    User %[1]s
    Port %[2]d
    IdentityFile %[5]s
    IdentitiesOnly yes
    IdentityAgent none
    UserKnownHostsFile %[4]s
    StrictHostKeyChecking no
`, fixture.username, fixture.port, key, knownHosts, fixture.clientKey)
	if err := os.WriteFile(clientConfig, []byte(configuration), 0o600); err != nil {
		t.Fatal(err)
	}
	// The controller finds OpenSSH on PATH, so a directory holding only the
	// wrapper, named ssh, comes first.
	binaries := filepath.Join(fixture.root, "bin")
	if err := os.Mkdir(binaries, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(writeSSHWrapper(t, fixture.root, clientConfig), filepath.Join(binaries, "ssh")); err != nil {
		t.Fatal(err)
	}
	kamuiBinary := filepath.Join(binaries, "kamui")
	build := exec.Command("go", "build", "-o", kamuiBinary, "github.com/narasaka/kamui/cmd/kamui")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build kamui: %v: %s", err, output)
	}

	// A short home keeps the controller's Unix socket path within limits.
	home, err := os.MkdirTemp("/tmp", "kamui-routes-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		// Stop the controller that the first command started.
		_ = exec.Command("/usr/bin/pkill", "-f", home).Run()
		_ = os.RemoveAll(home)
	})
	environment := []string{
		"HOME=" + home,
		"XDG_STATE_HOME=" + filepath.Join(home, "state"),
		"XDG_CONFIG_HOME=" + filepath.Join(home, "config"),
		"PATH=" + binaries + ":/usr/bin:/bin:/usr/sbin:/sbin",
	}
	kamui := func(arguments ...string) (string, error) {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		command := exec.CommandContext(ctx, kamuiBinary, arguments...)
		command.Env = environment
		var stdout, stderr bytes.Buffer
		command.Stdout, command.Stderr = &stdout, &stderr
		command.WaitDelay = 5 * time.Second
		err := command.Run()
		if err != nil {
			err = fmt.Errorf("kamui %s: %w: %s", strings.Join(arguments, " "), err, stderr.String())
		}
		return stdout.String(), err
	}
	sessions := func() map[string]string {
		t.Helper()
		output, err := kamui("status", "--json")
		if err != nil {
			t.Fatal(err)
		}
		var document struct {
			Sessions []struct {
				Destination string `json:"destination"`
				State       string `json:"state"`
			} `json:"sessions"`
		}
		if err := json.Unmarshal([]byte(output), &document); err != nil {
			t.Fatalf("decode status %q: %v", output, err)
		}
		states := map[string]string{}
		for _, session := range document.Sessions {
			states[session.Destination] = session.State
		}
		return states
	}

	// The app connects first, so the controller starts without a terminal,
	// and the app answers the host-key question and the passphrase.
	prompts := connectLikeTheApp(t, kamuiBinary, environment, "app-route", func(prompt askpass.LinePrompt) string {
		if strings.Contains(prompt.Text, "(yes/no") {
			return "yes"
		}
		return passphrase
	})
	if len(prompts) != 2 || !strings.Contains(prompts[0].Text, "(yes/no") || !strings.Contains(prompts[1].Text, "passphrase") {
		t.Fatalf("app answered %#v, want host-key confirmation then passphrase", prompts)
	}
	if got := sessions(); got["app-route"] != "connected" {
		t.Fatalf("kamui status after the app connected = %v", got)
	}

	// The terminal connects a second host; the app's status shows both.
	if _, err := kamui("mirror", "--non-interactive", "terminal-route"); err != nil {
		t.Fatal(err)
	}
	if got := sessions(); got["app-route"] != "connected" || got["terminal-route"] != "connected" {
		t.Fatalf("status with both routes' sessions = %v", got)
	}

	// The terminal stops the app's session, and the app stops the
	// terminal's; each disappears from the other's status.
	if _, err := kamui("stop", "app-route"); err != nil {
		t.Fatal(err)
	}
	if got := sessions(); len(got) != 1 || got["terminal-route"] != "connected" {
		t.Fatalf("status after the terminal stopped the app's session = %v", got)
	}
	if _, err := kamui("stop", "terminal-route"); err != nil {
		t.Fatal(err)
	}
	if got := sessions(); len(got) != 0 {
		t.Fatalf("status after the app stopped the terminal's session = %v", got)
	}
}

// connectLikeTheApp runs `kamui mirror --json-prompts` with pipes, as the menu
// bar app does, answers each prompt, and returns the prompts it saw. The
// command must exit and close its output even though it started the
// controller, which outlives it.
func connectLikeTheApp(t *testing.T, kamuiBinary string, environment []string, destination string, answer func(askpass.LinePrompt) string) []askpass.LinePrompt {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, kamuiBinary, "mirror", "--json-prompts", destination)
	command.Env = environment
	input, err := command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	output, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	// Like the app, capture standard error in a file: a controller that the
	// command starts keeps its standard error, by design.
	stderrFile, err := os.CreateTemp(t.TempDir(), "stderr")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = stderrFile.Close() }()
	command.Stderr = stderrFile
	stderr := func() string {
		contents, _ := os.ReadFile(stderrFile.Name())
		return string(contents)
	}
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	// A controller that inherited stdout would keep it open after the command
	// exits; the deadline ends the read instead of hanging.
	go func() {
		<-ctx.Done()
		_ = output.Close()
	}()
	var prompts []askpass.LinePrompt
	scanner := bufio.NewScanner(output)
	for scanner.Scan() {
		var message askpass.LineMessage
		if err := json.Unmarshal(scanner.Bytes(), &message); err != nil {
			t.Fatalf("stdout line %q is not a prompt message: %v", scanner.Text(), err)
		}
		if message.Prompt == nil || message.Prompt.Kind == askpass.Notice {
			continue
		}
		prompts = append(prompts, *message.Prompt)
		reply, err := json.Marshal(askpass.LineReply{ID: message.Prompt.ID, Answer: answer(*message.Prompt)})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := input.Write(append(reply, '\n')); err != nil {
			t.Fatal(err)
		}
	}
	_ = input.Close()
	if ctx.Err() != nil {
		t.Fatalf("app connect's output stayed open; prompts %#v; stderr %s", prompts, stderr())
	}
	if err := command.Wait(); err != nil {
		t.Fatalf("app connect: %v; prompts %#v; stderr %s", err, prompts, stderr())
	}
	if strings.Contains(stderr(), "open sesame") {
		t.Fatal("the answer appeared in kamui's output")
	}
	return prompts
}
