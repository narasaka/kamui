package cliapp_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	kamuiapp "github.com/narasaka/kamui/internal/app"
	"github.com/narasaka/kamui/internal/cliapp"
	"github.com/narasaka/kamui/internal/controller"
	"github.com/narasaka/kamui/internal/session"
	"github.com/narasaka/kamui/internal/ssh"
	"github.com/narasaka/kamui/internal/state"
	"github.com/narasaka/kamui/internal/testsupport"
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
		{name: "logs", args: []string{"kamui", "logs"}, want: "logs is not implemented"},
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

func TestLogsPrintsTheRequestedNumberOfRecentOpenSSHDiagnostics(t *testing.T) {
	t.Parallel()

	layout := state.NewLayout(t.TempDir())
	if err := layout.Ensure(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(layout.SSHLog, []byte("first\nsecond\nthird\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	application := kamuiapp.New(layout, nil)
	var output bytes.Buffer
	command := cliapp.NewCommandWithApplication(application, cliapp.Streams{Out: &output, ErrOut: &output})
	if err := command.Run(context.Background(), []string{"kamui", "logs", "--lines", "2"}); err != nil {
		t.Fatal(err)
	}
	if got, want := output.String(), "second\nthird\n"; got != want {
		t.Fatalf("logs output = %q, want %q", got, want)
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

	application, _, status := runningStatusApplication(t)
	var output bytes.Buffer
	command := cliapp.NewCommandWithApplication(application, cliapp.Streams{Out: &output, ErrOut: &output})
	if err := command.Run(context.Background(), []string{"kamui", "status", "reyna"}); err != nil {
		t.Fatal(err)
	}
	want := fmt.Sprintf("DESTINATION\tSTATE\tBROWSER\tPROXY\tSSH\nreyna\tconnected\t\t%s\thealthy\n", status.Proxy)
	if got := output.String(); got != want {
		t.Fatalf("status output = %q, want %q", got, want)
	}
}

func TestVerboseStatusIncludesLastTunnelError(t *testing.T) {
	t.Parallel()

	application, launcher, _ := runningStatusApplication(t)
	launcher.disconnect(t)
	deadline := time.Now().Add(2 * time.Second)
	for {
		var probe bytes.Buffer
		command := cliapp.NewCommandWithApplication(application, cliapp.Streams{Out: &probe, ErrOut: &probe})
		if err := command.Run(context.Background(), []string{"kamui", "status", "--verbose", "reyna"}); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(probe.String(), "authentication-required") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("session did not require authentication; last status: %s", probe.String())
		}
		time.Sleep(20 * time.Millisecond)
	}
	var output bytes.Buffer
	command := cliapp.NewCommandWithApplication(application, cliapp.Streams{Out: &output, ErrOut: &output})
	if err := command.Run(context.Background(), []string{"kamui", "status", "--verbose", "reyna"}); err != nil {
		t.Fatal(err)
	}
	got := output.String()
	for _, want := range []string{
		"DESTINATION\tSTATE\tBROWSER\tPROXY\tSSH\tLAST ERROR\n",
		"reyna\tauthentication-required\t",
		"\tauthentication-required\tSSH authentication failed for reyna:",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("verbose status output = %q, want substring %q", got, want)
		}
	}
}

func runningStatusApplication(t *testing.T) (*kamuiapp.Application, *authenticationFailureLauncher, session.SessionStatus) {
	t.Helper()
	root, err := os.MkdirTemp("/tmp", "kamui-cli-status-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	layout := state.NewLayout(root)
	launcher := &authenticationFailureLauncher{}
	manager := session.NewManager(ssh.Transport{Launcher: launcher, ReadinessTimeout: time.Second})
	t.Cleanup(func() { _ = manager.Close() })
	destination, err := session.ParseDestination("reyna")
	if err != nil {
		t.Fatal(err)
	}
	result, err := manager.Execute(context.Background(), session.Command{
		Operation: session.Ensure, Destination: destination, SkipBrowser: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	server, err := controller.Start(ctx, layout, manager)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	t.Cleanup(func() { cancel(); _ = server.Close() })
	return kamuiapp.New(layout, nil), launcher, result.Session
}

type authenticationFailureLauncher struct {
	base    testsupport.SSHLauncher
	mu      sync.Mutex
	process *testsupport.SSHProcess
}

func (l *authenticationFailureLauncher) Start(request ssh.StartRequest) (ssh.Process, error) {
	l.mu.Lock()
	if l.process != nil {
		l.mu.Unlock()
		_, _ = io.WriteString(request.Stderr, "Permission denied (publickey).\n")
		return nil, errors.New("exit status 255")
	}
	l.mu.Unlock()
	process, err := l.base.Start(request)
	if err != nil {
		return nil, err
	}
	l.mu.Lock()
	l.process = process.(*testsupport.SSHProcess)
	l.mu.Unlock()
	return process, nil
}

func (l *authenticationFailureLauncher) disconnect(t *testing.T) {
	t.Helper()
	l.mu.Lock()
	process := l.process
	l.mu.Unlock()
	if process == nil {
		t.Fatal("OpenSSH process was not started")
	}
	if err := process.Kill(); err != nil {
		t.Fatal(err)
	}
}
