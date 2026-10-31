package controller

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/narasaka/kamui/internal/askpass"
)

func TestRelayAfterCancellationNeverStartsAnotherAnswerRead(t *testing.T) {
	t.Parallel()

	server, client := net.Pipe()
	t.Cleanup(func() { _ = server.Close(); _ = client.Close() })
	relay := relayPrompts(&responseWriter{encoder: json.NewEncoder(server)}, json.NewDecoder(server))
	ctx, cancel := context.WithCancel(context.Background())

	first := make(chan error, 1)
	go func() {
		_, err := relay(ctx, askpass.Prompt{Text: "Password:", Kind: askpass.Secret})
		first <- err
	}()
	var sent wireResponse
	if err := json.NewDecoder(client).Decode(&sent); err != nil || sent.Prompt == nil {
		t.Fatalf("first prompt = %#v, %v", sent, err)
	}
	cancel()
	if err := <-first; !errors.Is(err, context.Canceled) {
		t.Fatalf("first relay error = %v, want cancellation", err)
	}

	// OpenSSH retries after the helper fails. The cancelled relay must not
	// send another prompt or start a second read of the same connection.
	second := make(chan error, 1)
	go func() {
		_, err := relay(ctx, askpass.Prompt{Text: "Password:", Kind: askpass.Secret})
		second <- err
	}()
	select {
	case err := <-second:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("second relay error = %v, want cancellation", err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancelled relay tried to send another prompt")
	}
	if err := client.SetReadDeadline(time.Now().Add(100 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	var unexpected wireResponse
	if err := json.NewDecoder(client).Decode(&unexpected); err == nil {
		t.Fatalf("cancelled relay sent %#v", unexpected)
	}
	// The abandoned read of the first answer completes without a race.
	_ = client.SetWriteDeadline(time.Now().Add(time.Second))
	_ = json.NewEncoder(client).Encode(wireAnswer{OK: true, Answer: "late"})
}
