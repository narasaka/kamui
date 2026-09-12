package session_test

import (
	"testing"

	"github.com/narasaka/kamui/internal/session"
)

func TestParseDestinationAcceptsAliasAndDirectHost(t *testing.T) {
	t.Parallel()

	for _, raw := range []string{"reyna", "narasaka@dev.example.com"} {
		raw := raw
		t.Run(raw, func(t *testing.T) {
			t.Parallel()
			destination, err := session.ParseDestination(raw)
			if err != nil {
				t.Fatalf("ParseDestination(%q) returned error: %v", raw, err)
			}
			if got := destination.String(); got != raw {
				t.Fatalf("Destination.String() = %q, want %q", got, raw)
			}
		})
	}
}

func TestParseDestinationRejectsUnsafeArguments(t *testing.T) {
	t.Parallel()

	for _, raw := range []string{"", "-oProxyCommand=evil", "host\nother"} {
		raw := raw
		t.Run(raw, func(t *testing.T) {
			t.Parallel()
			if _, err := session.ParseDestination(raw); err == nil {
				t.Fatalf("ParseDestination(%q) succeeded, want an error", raw)
			}
		})
	}
}

func TestDestinationKeyIsStableAndFilesystemSafe(t *testing.T) {
	t.Parallel()

	destination, err := session.ParseDestination("reyna")
	if err != nil {
		t.Fatal(err)
	}
	const want = "39e99aa3ca8d405a122b8c92c14bbe69e08c61fade009846f56b218410cbb84c"
	if got := destination.Key(); got != want {
		t.Fatalf("Destination.Key() = %q, want %q", got, want)
	}
}
