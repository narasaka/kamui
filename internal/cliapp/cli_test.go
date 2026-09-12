package cliapp_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/netip"
	"testing"

	kamuiapp "github.com/narasaka/kamui/internal/app"
	"github.com/narasaka/kamui/internal/cliapp"
	"github.com/narasaka/kamui/internal/session"
)

func TestPlannedCommandsReturnExplicitNotImplementedErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "primary", args: []string{"kamui", "reyna"}, want: "ensure is not implemented"},
		{name: "status", args: []string{"kamui", "status", "reyna"}, want: "status is not implemented"},
		{name: "stop destination", args: []string{"kamui", "stop", "reyna"}, want: "stop is not implemented"},
		{name: "stop all", args: []string{"kamui", "stop", "--all"}, want: "stop is not implemented"},
		{name: "open", args: []string{"kamui", "open", "reyna", "http://localhost:3000"}, want: "open is not implemented"},
		{name: "doctor", args: []string{"kamui", "doctor", "reyna"}, want: "doctor is not implemented"},
		{name: "browsers", args: []string{"kamui", "browsers"}, want: "browsers is not implemented"},
		{name: "ssh hook", args: []string{"kamui", "ssh-hook", "reyna"}, want: "ssh-hook is not implemented"},
		{name: "SSH config", args: []string{"kamui", "print-ssh-config", "reyna"}, want: "print-ssh-config is not implemented"},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			var stdout, stderr bytes.Buffer
			command := cliapp.NewCommand(cliapp.Streams{Out: &stdout, ErrOut: &stderr})
			err := command.Run(context.Background(), test.args)
			if got := fmt.Sprint(err); got != test.want {
				t.Fatalf("Run(%q) error = %q, want %q", test.args, got, test.want)
			}
		})
	}
}

func TestVersionFlagReportsBuildVersion(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	command := cliapp.NewCommand(cliapp.Streams{Out: &stdout, ErrOut: &stdout})
	if err := command.Run(context.Background(), []string{"kamui", "--version"}); err != nil {
		t.Fatal(err)
	}
	if got, want := stdout.String(), "kamui version dev\n"; got != want {
		t.Fatalf("version output = %q, want %q", got, want)
	}
}

func TestStatusDistinguishesSessionStateFromSSHHealth(t *testing.T) {
	t.Parallel()

	status := statusFixture(t, session.SessionConnected, nil)
	application := executorFunc(func(_ context.Context, request kamuiapp.Request) (kamuiapp.Result, error) {
		if request.Operation != kamuiapp.Status || request.Destination != "reyna" {
			t.Fatalf("request = %#v", request)
		}
		return kamuiapp.Result{Result: session.Result{Session: status}}, nil
	})
	var output bytes.Buffer
	command := cliapp.NewCommandWithApplication(application, cliapp.Streams{Out: &output, ErrOut: &output})
	if err := command.Run(context.Background(), []string{"kamui", "status", "reyna"}); err != nil {
		t.Fatal(err)
	}
	want := "DESTINATION\tSTATE\tBROWSER\tPROXY\tSSH\nreyna\tconnected\tfirefox\t127.0.0.1:52144\thealthy\n"
	if got := output.String(); got != want {
		t.Fatalf("status output = %q, want %q", got, want)
	}
}

func TestVerboseStatusIncludesLastTunnelError(t *testing.T) {
	t.Parallel()

	status := statusFixture(t, session.SessionAuthenticationRequired, errors.New("SSH authentication failed"))
	application := executorFunc(func(_ context.Context, _ kamuiapp.Request) (kamuiapp.Result, error) {
		return kamuiapp.Result{Result: session.Result{Session: status}}, nil
	})
	var output bytes.Buffer
	command := cliapp.NewCommandWithApplication(application, cliapp.Streams{Out: &output, ErrOut: &output})
	if err := command.Run(context.Background(), []string{"kamui", "status", "--verbose", "reyna"}); err != nil {
		t.Fatal(err)
	}
	want := "DESTINATION\tSTATE\tBROWSER\tPROXY\tSSH\tLAST ERROR\nreyna\tauthentication-required\tfirefox\t127.0.0.1:52144\tauthentication-required\tSSH authentication failed\n"
	if got := output.String(); got != want {
		t.Fatalf("verbose status output = %q, want %q", got, want)
	}
}

type executorFunc func(context.Context, kamuiapp.Request) (kamuiapp.Result, error)

func (execute executorFunc) Execute(ctx context.Context, request kamuiapp.Request) (kamuiapp.Result, error) {
	return execute(ctx, request)
}

func statusFixture(t *testing.T, state session.SessionState, lastError error) session.SessionStatus {
	t.Helper()
	destination, err := session.ParseDestination("reyna")
	if err != nil {
		t.Fatal(err)
	}
	return session.SessionStatus{
		Destination: destination,
		State:       state,
		Browser:     "firefox",
		Proxy:       netip.MustParseAddrPort("127.0.0.1:52144"),
		LastError:   lastError,
	}
}
