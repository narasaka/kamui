package cliapp

import (
	"bytes"
	"errors"
	"net/netip"
	"testing"

	"github.com/narasaka/kamui/internal/session"
)

func TestPrintStatusesDistinguishesSessionStateFromSSHHealth(t *testing.T) {
	t.Parallel()

	destination, err := session.ParseDestination("reyna")
	if err != nil {
		t.Fatal(err)
	}
	status := session.SessionStatus{
		Destination: destination,
		State:       session.SessionConnected,
		Browser:     "firefox",
		Proxy:       netip.MustParseAddrPort("127.0.0.1:52144"),
	}
	var output bytes.Buffer
	if err := printStatuses(&output, []session.SessionStatus{status}, false); err != nil {
		t.Fatal(err)
	}
	want := "DESTINATION\tSTATE\tBROWSER\tPROXY\tSSH\nreyna\tconnected\tfirefox\t127.0.0.1:52144\thealthy\n"
	if got := output.String(); got != want {
		t.Fatalf("status output = %q, want %q", got, want)
	}
}

func TestPrintVerboseStatusesIncludesLastTunnelError(t *testing.T) {
	t.Parallel()

	destination, err := session.ParseDestination("reyna")
	if err != nil {
		t.Fatal(err)
	}
	status := session.SessionStatus{
		Destination: destination,
		State:       session.SessionAuthenticationRequired,
		Proxy:       netip.MustParseAddrPort("127.0.0.1:52144"),
		LastError:   errors.New("SSH authentication failed"),
	}
	var output bytes.Buffer
	if err := printStatuses(&output, []session.SessionStatus{status}, true); err != nil {
		t.Fatal(err)
	}
	want := "DESTINATION\tSTATE\tBROWSER\tPROXY\tSSH\tLAST ERROR\nreyna\tauthentication-required\t\t127.0.0.1:52144\tauthentication-required\tSSH authentication failed\n"
	if got := output.String(); got != want {
		t.Fatalf("verbose status output = %q, want %q", got, want)
	}
}
