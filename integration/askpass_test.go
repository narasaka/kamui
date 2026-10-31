package integration_test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/narasaka/kamui/internal/app"
	"github.com/narasaka/kamui/internal/askpass"
	"github.com/narasaka/kamui/internal/controller"
	"github.com/narasaka/kamui/internal/session"
	"github.com/narasaka/kamui/internal/ssh"
	"github.com/narasaka/kamui/internal/state"
)

// A controller started without a terminal, such as by the menu bar app, must
// still let a later interactive command answer host-key and passphrase
// prompts from real OpenSSH.
func TestOpenSSHPromptsReachRequesterThroughBackgroundController(t *testing.T) {
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
	knownHosts := filepath.Join(fixture.root, "prompt_known_hosts")
	clientConfig := filepath.Join(fixture.root, "prompt_ssh_config")
	configuration := fmt.Sprintf(`Host kamui-prompt-alias
    HostName 127.0.0.1
    User %s
    Port %d
    IdentityFile %s
    IdentitiesOnly yes
    IdentityAgent none
    UserKnownHostsFile %s
    StrictHostKeyChecking ask
`, fixture.username, fixture.port, key, knownHosts)
	if err := os.WriteFile(clientConfig, []byte(configuration), 0o600); err != nil {
		t.Fatal(err)
	}
	wrapper := writeSSHWrapper(t, fixture.root, clientConfig)
	kamuiBinary := filepath.Join(fixture.root, "kamui")
	build := exec.Command("go", "build", "-o", kamuiBinary, "github.com/narasaka/kamui/cmd/kamui")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build kamui askpass helper: %v: %s", err, output)
	}

	stateRoot, err := os.MkdirTemp("/tmp", "kamui-prompt-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(stateRoot) })
	layout := state.NewLayout(stateRoot)
	manager := session.NewManagerWithOptions(session.ManagerOptions{
		Transport:        ssh.Transport{SSHPath: wrapper, ReadinessTimeout: 20 * time.Second, AskpassProgram: kamuiBinary},
		MirrorDiscoverer: noListeners{},
		MirrorInterval:   time.Hour,
	})
	t.Cleanup(func() { _ = manager.Close() })
	server, err := controller.Start(context.Background(), layout, manager)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Close() })

	var prompts []askpass.Prompt
	prompter := func(_ context.Context, prompt askpass.Prompt) (string, error) {
		prompts = append(prompts, prompt)
		if strings.Contains(prompt.Text, "(yes/no") {
			return "yes", nil
		}
		return passphrase, nil
	}
	result, err := app.New(layout, nil).Execute(context.Background(), app.Request{
		Operation: app.Mirror, Destination: "kamui-prompt-alias", Prompter: prompter,
	})
	if err != nil {
		t.Fatalf("interactive connect: %v; prompts %#v", err, prompts)
	}
	if result.Session.State != session.SessionConnected {
		t.Fatalf("session state = %v, want connected", result.Session.State)
	}
	if len(prompts) != 2 || prompts[0].Kind != askpass.Secret || !strings.Contains(prompts[0].Text, "(yes/no") ||
		prompts[1].Kind != askpass.Secret || !strings.Contains(prompts[1].Text, "passphrase") {
		t.Fatalf("prompts = %#v, want host-key confirmation then passphrase", prompts)
	}
	if hosts, err := os.ReadFile(knownHosts); err != nil || len(hosts) == 0 {
		t.Fatalf("accepted host key was not recorded: %v", err)
	}
}

type noListeners struct{}

func (noListeners) ListeningPorts(context.Context, string) ([]uint16, error) { return nil, nil }
