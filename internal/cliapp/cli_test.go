package cliapp_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
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
		{name: "mirror", args: []string{"kamui", "mirror", "reyna"}, want: "mirror is not implemented"},
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

func TestNoArgumentsShowsCommandHelp(t *testing.T) {
	t.Parallel()

	var output bytes.Buffer
	command := cliapp.NewCommand(cliapp.Streams{Out: &output, ErrOut: &output})
	if err := command.Run(context.Background(), []string{"kamui"}); err != nil {
		t.Fatal(err)
	}

	got := output.String()
	for _, want := range []string{
		"USAGE:\n",
		"COMMANDS:\n",
		"logs              show OpenSSH background diagnostics\n",
		"mirror            mirror remote TCP listeners on Mac loopback\n",
		"status            show session status\n",
		"stop              stop one or all sessions\n",
		"open              open URLs in a session browser\n",
		"doctor            diagnose SSH connectivity\n",
		"browsers          list detected supported browsers\n",
		"ssh-hook          activate a session from OpenSSH\n",
		"print-ssh-config  print an OpenSSH LocalCommand snippet\n",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("help output = %q, want substring %q", got, want)
		}
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

func TestVersionFlagReportsDevelopmentVersion(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	command := cliapp.NewCommand(cliapp.Streams{Out: &stdout, ErrOut: &stdout})
	if err := command.Run(context.Background(), []string{"kamui", "--version"}); err != nil {
		t.Fatal(err)
	}
	if got, want := stdout.String(), "dev\n"; got != want {
		t.Fatalf("version output = %q, want %q", got, want)
	}
}

func TestPrimaryCommandEnablesLocalFirstLoopbackRouting(t *testing.T) {
	t.Parallel()

	root, err := os.MkdirTemp("/tmp", "kamui-cli-loopback-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	layout := state.NewLayout(root)
	manager := session.NewManager(ssh.Transport{Launcher: &testsupport.SSHLauncher{}, ReadinessTimeout: time.Second})
	t.Cleanup(func() { _ = manager.Close() })
	ctx, cancel := context.WithCancel(context.Background())
	server, err := controller.Start(ctx, layout, manager)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	t.Cleanup(func() { cancel(); _ = server.Close() })
	application := kamuiapp.New(layout, nil)
	var output bytes.Buffer
	command := cliapp.NewCommandWithApplication(application, cliapp.Streams{Out: &output, ErrOut: &output})

	if err := command.Run(context.Background(), []string{"kamui", "reyna", "--browser-loopback", "local-first"}); err != nil {
		t.Fatalf("run primary command: %v", err)
	}
	status, err := application.Execute(context.Background(), kamuiapp.Request{Operation: kamuiapp.Status, Destination: "reyna"})
	if err != nil {
		t.Fatal(err)
	}
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "Mac loopback")
	}))
	t.Cleanup(backend.Close)
	backendURL, _ := url.Parse(backend.URL)
	proxyURL, _ := url.Parse("http://" + status.Session.Proxy.String())
	client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)}}

	response, err := client.Get("http://localhost:" + backendURL.Port())
	if err != nil {
		t.Fatalf("GET Mac loopback through CLI-configured proxy: %v", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "Mac loopback" {
		t.Fatalf("body = %q, want Mac loopback", body)
	}
	if !strings.Contains(output.String(), "may access genuine Mac localhost services in local-first mode") {
		t.Fatalf("output = %q, want local-first security warning", output.String())
	}
	var repeatedOutput bytes.Buffer
	repeated := cliapp.NewCommandWithApplication(application, cliapp.Streams{Out: &repeatedOutput, ErrOut: &repeatedOutput})
	if err := repeated.Run(context.Background(), []string{"kamui", "reyna", "--browser-loopback", "local-first"}); err != nil {
		t.Fatalf("repeat primary command: %v", err)
	}
	if !strings.Contains(repeatedOutput.String(), "may access genuine Mac localhost services in local-first mode") {
		t.Fatalf("repeated output = %q, want persistent local-first security warning", repeatedOutput.String())
	}
}

func TestDeprecatedLoopbackAliasPrintsNotice(t *testing.T) {
	t.Parallel()

	var stdout, stderr bytes.Buffer
	command := cliapp.NewCommand(cliapp.Streams{Out: &stdout, ErrOut: &stderr})
	err := command.Run(context.Background(), []string{"kamui", "reyna", "--loopback", "local-first"})
	if fmt.Sprint(err) != "ensure is not implemented" {
		t.Fatalf("error = %v", err)
	}
	if got := stderr.String(); !strings.Contains(got, "--loopback is deprecated; use --browser-loopback") {
		t.Fatalf("stderr = %q, want deprecation notice", got)
	}
}

func TestDeprecatedLoopbackAliasRetainsLocalFirstBehavior(t *testing.T) {
	t.Parallel()

	root, err := os.MkdirTemp("/tmp", "kamui-cli-loopback-alias-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	layout := state.NewLayout(root)
	manager := session.NewManager(ssh.Transport{Launcher: &testsupport.SSHLauncher{}, ReadinessTimeout: time.Second})
	t.Cleanup(func() { _ = manager.Close() })
	server, err := controller.Start(context.Background(), layout, manager)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Close() })
	application := kamuiapp.New(layout, nil)
	var output bytes.Buffer
	command := cliapp.NewCommandWithApplication(application, cliapp.Streams{Out: &output, ErrOut: &output})
	if err := command.Run(context.Background(), []string{"kamui", "reyna", "--loopback", "local-first"}); err != nil {
		t.Fatal(err)
	}
	status, err := application.Execute(context.Background(), kamuiapp.Request{Operation: kamuiapp.Status, Destination: "reyna"})
	if err != nil {
		t.Fatal(err)
	}
	if status.Session.LoopbackMode.String() != "local-first" || !strings.Contains(output.String(), "--loopback is deprecated") {
		t.Fatalf("mode=%s output=%q", status.Session.LoopbackMode, output.String())
	}
}

func TestMirrorCommandDoesNotLaunchBrowserAndStatusShowsPorts(t *testing.T) {
	t.Parallel()

	root, err := os.MkdirTemp("/tmp", "kamui-cli-mirror-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	occupied, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = occupied.Close() })
	conflictPort := uint16(occupied.Addr().(*net.TCPAddr).Port)
	probe, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	mirroredPort := uint16(probe.Addr().(*net.TCPAddr).Port)
	_ = probe.Close()

	layout := state.NewLayout(root)
	manager := session.NewManagerWithOptions(session.ManagerOptions{
		Transport:        ssh.Transport{Launcher: &testsupport.SSHLauncher{}, ReadinessTimeout: time.Second},
		MirrorDiscoverer: cliDiscoverer{ports: []uint16{mirroredPort, conflictPort}},
		MirrorInterval:   10 * time.Millisecond,
	})
	t.Cleanup(func() { _ = manager.Close() })
	server, err := controller.Start(context.Background(), layout, manager)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Close() })
	application := kamuiapp.New(layout, nil)
	var output bytes.Buffer
	command := cliapp.NewCommandWithApplication(application, cliapp.Streams{Out: &output, ErrOut: &output})
	if err := command.Run(context.Background(), []string{"kamui", "mirror", "reyna"}); err != nil {
		t.Fatal(err)
	}
	if got := output.String(); !strings.Contains(got, "mirroring enabled") || !strings.Contains(got, fmt.Sprint(mirroredPort)) || !strings.Contains(got, fmt.Sprint(conflictPort)) {
		t.Fatalf("mirror output = %q", got)
	}
	output.Reset()
	if err := command.Run(context.Background(), []string{"kamui", "status", "reyna"}); err != nil {
		t.Fatal(err)
	}
	if got := output.String(); !strings.Contains(got, "\tenabled\t") || !strings.Contains(got, fmt.Sprint(mirroredPort)) || !strings.Contains(got, fmt.Sprint(conflictPort)) {
		t.Fatalf("status output = %q", got)
	}
	status, err := application.Execute(context.Background(), kamuiapp.Request{Operation: kamuiapp.Status, Destination: "reyna"})
	if err != nil {
		t.Fatal(err)
	}
	if status.Session.Browser != "" {
		t.Fatalf("mirror launched browser %q", status.Session.Browser)
	}
}

type cliDiscoverer struct{ ports []uint16 }

func (d cliDiscoverer) ListeningPorts(context.Context, string) ([]uint16, error) {
	return append([]uint16(nil), d.ports...), nil
}

func TestPrimaryCommandRejectsInvalidLoopbackMode(t *testing.T) {
	t.Parallel()

	application := kamuiapp.New(state.NewLayout(t.TempDir()), nil)
	var output bytes.Buffer
	command := cliapp.NewCommandWithApplication(application, cliapp.Streams{Out: &output, ErrOut: &output})
	err := command.Run(context.Background(), []string{"kamui", "reyna", "--browser-loopback", "sometimes-local"})
	if err == nil || !strings.Contains(err.Error(), `unsupported loopback mode "sometimes-local"`) {
		t.Fatalf("error = %v, want unsupported loopback mode", err)
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
	want := fmt.Sprintf("DESTINATION\tSTATE\tBROWSER\tPROXY\tSSH\tMIRROR\tMIRRORED\tCONFLICTS\tMIRROR ERROR\nreyna\tconnected\t\t%s\thealthy\tdisabled\t-\t-\t\n", status.Proxy)
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
		"DESTINATION\tSTATE\tBROWSER\tPROXY\tSSH\tMIRROR\tMIRRORED\tCONFLICTS\tMIRROR ERROR\tLAST ERROR\n",
		"reyna\tauthentication-required\t",
		"SSH authentication failed for reyna:",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("verbose status output = %q, want substring %q", got, want)
		}
	}
}

func TestStopWithoutDestinationSuggestsKnownSessions(t *testing.T) {
	t.Parallel()

	application, _, _ := runningStatusApplication(t)
	var output bytes.Buffer
	command := cliapp.NewCommandWithApplication(application, cliapp.Streams{Out: &output, ErrOut: &output})
	err := command.Run(context.Background(), []string{"kamui", "stop"})
	want := "Choose a session to stop:\n\n  reyna  connected\n\nRun `kamui stop SSH_DESTINATION` or `kamui stop --all`."
	if got := fmt.Sprint(err); got != want {
		t.Fatalf("stop error = %q, want %q", got, want)
	}
}

func TestStopWithoutDestinationReportsWhenNoSessionsExist(t *testing.T) {
	t.Parallel()

	application, _, _ := runningStatusApplication(t)
	if _, err := application.Execute(context.Background(), kamuiapp.Request{
		Operation: kamuiapp.Stop, Destination: "reyna",
	}); err != nil {
		t.Fatal(err)
	}

	var output bytes.Buffer
	command := cliapp.NewCommandWithApplication(application, cliapp.Streams{Out: &output, ErrOut: &output})
	err := command.Run(context.Background(), []string{"kamui", "stop"})
	if got, want := fmt.Sprint(err), "No sessions to stop."; got != want {
		t.Fatalf("stop error = %q, want %q", got, want)
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
