package askpass_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/narasaka/kamui/internal/askpass"
)

func TestHelperRelaysPromptThroughBroker(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		text       string
		hint       string
		answer     string
		relayErr   error
		wantKind   askpass.Kind
		wantStatus int
		wantOutput string
	}{
		{name: "passphrase", text: "Enter passphrase for key '/k': ", answer: "open sesame", wantKind: askpass.Secret, wantOutput: "open sesame\n"},
		{name: "host key", text: "Are you sure you want to continue connecting (yes/no/[fingerprint])? ", answer: "yes", wantKind: askpass.Secret, wantOutput: "yes\n"},
		{name: "passphrase path resembling a question", text: "Enter passphrase for key '/home/n/.ssh/(yes/no)/id_ed25519': ", answer: "open sesame", wantKind: askpass.Secret, wantOutput: "open sesame\n"},
		{name: "confirm", text: "Allow use of key?", hint: "confirm", wantKind: askpass.Confirm, wantOutput: "yes\n"},
		{name: "notice", text: "Confirm user presence for key", hint: "none", wantKind: askpass.Notice},
		{name: "declined", text: "Allow use of key?", hint: "confirm", relayErr: askpass.ErrDeclined, wantKind: askpass.Confirm, wantStatus: 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			prompts := make(chan askpass.Prompt, 2)
			broker := newBroker(t, func(_ context.Context, prompt askpass.Prompt) (string, error) {
				prompts <- prompt
				return test.answer, test.relayErr
			})
			environment := map[string]string{
				askpass.SocketEnv: broker.Socket(), askpass.TokenEnv: broker.Token(), "SSH_ASKPASS_PROMPT": test.hint,
			}
			var output bytes.Buffer
			status := askpass.RunHelper([]string{test.text}, func(name string) string { return environment[name] }, &output)

			if status != test.wantStatus || output.String() != test.wantOutput {
				t.Fatalf("helper status=%d output=%q, want status=%d output=%q", status, output.String(), test.wantStatus, test.wantOutput)
			}
			close(prompts)
			var got []askpass.Prompt
			for prompt := range prompts {
				got = append(got, prompt)
			}
			if len(got) != 1 || got[0] != (askpass.Prompt{Text: test.text, Kind: test.wantKind}) {
				t.Fatalf("relayed prompts = %#v, want one %s prompt %q", got, test.wantKind, test.text)
			}
		})
	}
}

func TestHelperWithWrongTokenNeverReachesRequester(t *testing.T) {
	t.Parallel()

	broker := newBroker(t, func(context.Context, askpass.Prompt) (string, error) {
		t.Error("relay received a prompt with the wrong token")
		return "secret", nil
	})
	environment := map[string]string{askpass.SocketEnv: broker.Socket(), askpass.TokenEnv: "forged"}
	var output bytes.Buffer
	if status := askpass.RunHelper([]string{"Password:"}, func(name string) string { return environment[name] }, &output); status != 1 || output.Len() != 0 {
		t.Fatalf("helper status=%d output=%q, want failure without output", status, output.String())
	}
}

func TestHelperWithoutBrokerFails(t *testing.T) {
	t.Parallel()

	var output bytes.Buffer
	if status := askpass.RunHelper([]string{"Password:"}, func(string) string { return "" }, &output); status != 1 {
		t.Fatalf("helper status = %d, want 1", status)
	}
}

func TestBrokerCloseAbandonsPendingPromptAndRemovesSocket(t *testing.T) {
	t.Parallel()

	started := make(chan struct{})
	broker := newBroker(t, func(ctx context.Context, _ askpass.Prompt) (string, error) {
		close(started)
		<-ctx.Done()
		return "", ctx.Err()
	})
	environment := map[string]string{askpass.SocketEnv: broker.Socket(), askpass.TokenEnv: broker.Token()}
	status := make(chan int, 1)
	go func() {
		status <- askpass.RunHelper([]string{"Password:"}, func(name string) string { return environment[name] }, &bytes.Buffer{})
	}()
	<-started
	if err := broker.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-status:
		if got != 1 {
			t.Fatalf("helper status = %d, want 1", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("helper kept waiting after the broker closed")
	}
	if _, err := os.Stat(broker.Socket()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("broker socket still exists: %v", err)
	}
}

func TestBrokerSocketIsUserOnly(t *testing.T) {
	t.Parallel()

	broker := newBroker(t, func(context.Context, askpass.Prompt) (string, error) { return "", nil })
	info, err := os.Stat(broker.Socket())
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o077 != 0 {
		t.Fatalf("socket mode = %v, want user-only", info.Mode().Perm())
	}
}

func newBroker(t *testing.T, relay askpass.Prompter) *askpass.Broker {
	t.Helper()
	directory, err := os.MkdirTemp("/tmp", "kamui-askpass-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(directory) })
	broker, err := askpass.NewBroker(context.Background(), directory, relay)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = broker.Close() })
	return broker
}
