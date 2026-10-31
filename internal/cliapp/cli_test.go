package cliapp_test

import (
	"bytes"
	"context"
	"encoding/json"
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
	"github.com/narasaka/kamui/internal/askpass"
	"github.com/narasaka/kamui/internal/cliapp"
	"github.com/narasaka/kamui/internal/controller"
	"github.com/narasaka/kamui/internal/mirror"
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
		{name: "primary", args: []string{"kamui", "reyna"}, want: "mirror is not implemented"},
		{name: "browser", args: []string{"kamui", "browser", "reyna"}, want: "ensure is not implemented"},
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
		"kamui - mirror a remote SSH host's loopback TCP services on this machine\n",
		"USAGE:\n",
		"COMMANDS:\n",
		"browser           open a dedicated browser for remote loopback services\n",
		"logs              show OpenSSH background diagnostics\n",
		"mirror            mirror remote TCP listeners on local loopback\n",
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

func TestPrimaryCommandRejectsBrowserFlags(t *testing.T) {
	t.Parallel()

	var output bytes.Buffer
	command := cliapp.NewCommand(cliapp.Streams{Out: &output, ErrOut: &output})
	err := command.Run(context.Background(), []string{"kamui", "reyna", "--browser", "firefox"})
	if err == nil || !strings.Contains(err.Error(), "flag provided but not defined: -browser") {
		t.Fatalf("error = %v, want browser flag rejection", err)
	}
}

func TestBrowserCommandEnablesLocalFirstLoopbackRouting(t *testing.T) {
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

	if err := command.Run(context.Background(), []string{"kamui", "browser", "reyna", "--browser-loopback", "local-first"}); err != nil {
		t.Fatalf("run browser command: %v", err)
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
	defer func() { _ = response.Body.Close() }()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "Mac loopback" {
		t.Fatalf("body = %q, want Mac loopback", body)
	}
	if !strings.Contains(output.String(), "may access genuine local-machine localhost services in local-first mode") {
		t.Fatalf("output = %q, want local-first security warning", output.String())
	}
	var repeatedOutput bytes.Buffer
	repeated := cliapp.NewCommandWithApplication(application, cliapp.Streams{Out: &repeatedOutput, ErrOut: &repeatedOutput})
	if err := repeated.Run(context.Background(), []string{"kamui", "browser", "reyna", "--browser-loopback", "local-first"}); err != nil {
		t.Fatalf("repeat browser command: %v", err)
	}
	if !strings.Contains(repeatedOutput.String(), "may access genuine local-machine localhost services in local-first mode") {
		t.Fatalf("repeated output = %q, want persistent local-first security warning", repeatedOutput.String())
	}
}

func TestDeprecatedLoopbackAliasPrintsNotice(t *testing.T) {
	t.Parallel()

	var stdout, stderr bytes.Buffer
	command := cliapp.NewCommand(cliapp.Streams{Out: &stdout, ErrOut: &stderr})
	err := command.Run(context.Background(), []string{"kamui", "browser", "reyna", "--loopback", "local-first"})
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
	if err := command.Run(context.Background(), []string{"kamui", "browser", "reyna", "--loopback", "local-first"}); err != nil {
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

func TestPrimaryCommandMirrorsPortsWithoutLaunchingBrowser(t *testing.T) {
	t.Parallel()
	const diagnostic = "# Tailscale SSH requires an additional check.\n# To authenticate, visit: https://login.tailscale.com/a/example\n"

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
		MirrorDiscoverer: cliDiscoverer{ports: []uint16{80, mirroredPort, conflictPort}, diagnostic: diagnostic},
		MirrorInterval:   10 * time.Millisecond,
	})
	t.Cleanup(func() { _ = manager.Close() })
	server, err := controller.Start(context.Background(), layout, manager)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Close() })
	application := kamuiapp.New(layout, nil)
	var output, errorOutput bytes.Buffer
	command := cliapp.NewCommandWithApplication(application, cliapp.Streams{Out: &output, ErrOut: &errorOutput})
	if err := command.Run(context.Background(), []string{"kamui", "reyna"}); err != nil {
		t.Fatal(err)
	}
	if got := errorOutput.String(); got != diagnostic {
		t.Fatalf("SSH diagnostics = %q, want %q", got, diagnostic)
	}
	if got := output.String(); !strings.Contains(got, "● connected") || !strings.Contains(got, "MIRRORED:     "+fmt.Sprint(mirroredPort)) || !strings.Contains(got, "EXCLUDED:     80") || !strings.Contains(got, "CONFLICTS:    "+fmt.Sprint(conflictPort)) || !strings.HasSuffix(got, "\nTo disconnect, run: kamui stop reyna\n") {
		t.Fatalf("mirror output = %q", got)
	}
	output.Reset()
	if err := command.Run(context.Background(), []string{"kamui", "status", "reyna"}); err != nil {
		t.Fatal(err)
	}
	if got := output.String(); !strings.Contains(got, "reyna        ● connected\n") || !strings.Contains(got, "MIRRORED:     "+fmt.Sprint(mirroredPort)) || !strings.Contains(got, "EXCLUDED:     80") || !strings.Contains(got, "CONFLICTS:    "+fmt.Sprint(conflictPort)) {
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

func TestPrimaryCommandAppliesPortFlagsOverHostConfiguration(t *testing.T) {
	t.Parallel()

	root, err := os.MkdirTemp("/tmp", "kamui-cli-port-policy-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	includePort := availablePort(t)
	excludePort := availablePort(t)
	for excludePort == includePort {
		excludePort = availablePort(t)
	}

	layout := state.NewLayout(root)
	if err := layout.Ensure(); err != nil {
		t.Fatal(err)
	}
	configuration := fmt.Sprintf(`{"hosts":{"reyna":{"ports":{"exclude":["%d"]}}}}`, includePort)
	if err := os.WriteFile(layout.Config, []byte(configuration), 0o600); err != nil {
		t.Fatal(err)
	}
	manager := session.NewManagerWithOptions(session.ManagerOptions{
		Transport:        ssh.Transport{Launcher: &testsupport.SSHLauncher{}, ReadinessTimeout: time.Second},
		MirrorDiscoverer: cliDiscoverer{ports: []uint16{includePort, excludePort}},
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

	err = command.Run(context.Background(), []string{
		"kamui", "reyna",
		"--include-port", fmt.Sprint(includePort),
		"--exclude-port", fmt.Sprint(excludePort),
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := output.String(); !strings.Contains(got, "MIRRORED:     "+fmt.Sprint(includePort)) || !strings.Contains(got, "EXCLUDED:     "+fmt.Sprint(excludePort)) {
		t.Fatalf("mirror output = %q", got)
	}

	output.Reset()
	compatibilityCommand := cliapp.NewCommandWithApplication(application, cliapp.Streams{Out: &output, ErrOut: &output})
	err = compatibilityCommand.Run(context.Background(), []string{
		"kamui", "mirror", "reyna",
		"--exclude-port", fmt.Sprint(includePort),
		"--include-port", fmt.Sprint(excludePort),
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := output.String(); !strings.Contains(got, "mirrored "+fmt.Sprint(excludePort)) || !strings.Contains(got, "excluded "+fmt.Sprint(includePort)) {
		t.Fatalf("compatibility mirror output = %q", got)
	}
}

type cliDiscoverer struct {
	ports       []uint16
	diagnostic  string
	diagnostics io.Writer
}

func (d cliDiscoverer) ListeningPorts(context.Context, string) ([]uint16, error) {
	if d.diagnostics != nil {
		_, _ = io.WriteString(d.diagnostics, d.diagnostic)
	}
	return append([]uint16(nil), d.ports...), nil
}

func (d cliDiscoverer) WithDiagnostics(writer io.Writer) mirror.Discoverer {
	d.diagnostics = writer
	return d
}

func availablePort(t *testing.T) uint16 {
	t.Helper()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := uint16(listener.Addr().(*net.TCPAddr).Port)
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	return port
}

func TestMirrorCommandsRejectInvalidPortFlags(t *testing.T) {
	t.Parallel()

	application := kamuiapp.New(state.NewLayout(t.TempDir()), nil)
	for name, args := range map[string][]string{
		"primary":       {"kamui", "reyna", "--include-port", "0"},
		"compatibility": {"kamui", "mirror", "reyna", "--exclude-port", "65536"},
	} {
		name, args := name, args
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			var output bytes.Buffer
			command := cliapp.NewCommandWithApplication(application, cliapp.Streams{Out: &output, ErrOut: &output})
			err := command.Run(context.Background(), args)
			if err == nil || !strings.Contains(err.Error(), "invalid TCP port") {
				t.Fatalf("error = %v, want invalid TCP port", err)
			}
		})
	}
}

func TestBrowserCommandRejectsInvalidLoopbackMode(t *testing.T) {
	t.Parallel()

	application := kamuiapp.New(state.NewLayout(t.TempDir()), nil)
	var output bytes.Buffer
	command := cliapp.NewCommandWithApplication(application, cliapp.Streams{Out: &output, ErrOut: &output})
	err := command.Run(context.Background(), []string{"kamui", "browser", "reyna", "--browser-loopback", "sometimes-local"})
	if err == nil || !strings.Contains(err.Error(), `unsupported loopback mode "sometimes-local"`) {
		t.Fatalf("error = %v, want unsupported loopback mode", err)
	}
}

func TestStatusOmitsRedundantSSHAndMirrorColumns(t *testing.T) {
	t.Parallel()

	application, _, _ := runningStatusApplication(t)
	var output bytes.Buffer
	command := cliapp.NewCommandWithApplication(application, cliapp.Streams{Out: &output, ErrOut: &output})
	if err := command.Run(context.Background(), []string{"kamui", "status", "reyna"}); err != nil {
		t.Fatal(err)
	}
	want := "DESTINATION  STATE\nreyna        ● connected\n"
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
		"DESTINATION  STATE",
		"reyna        ✗ authentication-required",
		"LAST ERROR:   SSH authentication failed for reyna:",
		"Permission denied (publickey).",
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
	if got, want := fmt.Sprint(err), "no sessions to stop"; got != want {
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

func TestStatusJSONWithoutControllerReportsNoSessionsWithoutStartingOne(t *testing.T) {
	t.Parallel()

	application := kamuiapp.New(state.NewLayout(t.TempDir()), controllerStartForbidden{t: t})
	var output, errorOutput bytes.Buffer
	command := cliapp.NewCommandWithApplication(application, cliapp.Streams{Out: &output, ErrOut: &errorOutput})
	if err := command.Run(context.Background(), []string{"kamui", "status", "--json"}); err != nil {
		t.Fatal(err)
	}
	if got, want := output.String(), "{\n  \"format\": 1,\n  \"sessions\": []\n}\n"; got != want {
		t.Fatalf("status JSON = %q, want %q", got, want)
	}
}

func TestStatusJSONReportsRunningSession(t *testing.T) {
	t.Parallel()

	application, _, _ := runningStatusApplication(t)
	var output bytes.Buffer
	command := cliapp.NewCommandWithApplication(application, cliapp.Streams{Out: &output, ErrOut: &output})
	if err := command.Run(context.Background(), []string{"kamui", "status", "--json", "reyna"}); err != nil {
		t.Fatal(err)
	}
	var document struct {
		Format   int `json:"format"`
		Sessions []struct {
			Destination string `json:"destination"`
			State       string `json:"state"`
		} `json:"sessions"`
	}
	if err := json.Unmarshal(output.Bytes(), &document); err != nil {
		t.Fatalf("decode status JSON %q: %v", output.String(), err)
	}
	if document.Format != 1 || len(document.Sessions) != 1 ||
		document.Sessions[0].Destination != "reyna" || document.Sessions[0].State != "connected" {
		t.Fatalf("status JSON = %s", output.String())
	}
}

func TestPrimaryCommandNonInteractiveForbidsOpenSSHPrompts(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name      string
		args      []string
		wantBatch bool
	}{
		{name: "interactive", args: []string{"kamui", "reyna"}, wantBatch: false},
		{name: "non-interactive", args: []string{"kamui", "--non-interactive", "reyna"}, wantBatch: true},
		{name: "mirror non-interactive", args: []string{"kamui", "mirror", "--non-interactive", "reyna"}, wantBatch: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			root, err := os.MkdirTemp("/tmp", "kamui-cli-batch-")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.RemoveAll(root) })
			layout := state.NewLayout(root)
			launcher := &testsupport.SSHLauncher{}
			manager := session.NewManagerWithOptions(session.ManagerOptions{
				Transport:        ssh.Transport{Launcher: launcher, ReadinessTimeout: time.Second},
				MirrorDiscoverer: cliDiscoverer{},
				MirrorInterval:   time.Hour,
			})
			t.Cleanup(func() { _ = manager.Close() })
			server, err := controller.Start(context.Background(), layout, manager)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = server.Close() })
			var output bytes.Buffer
			command := cliapp.NewCommandWithApplication(kamuiapp.New(layout, nil), cliapp.Streams{Out: &output, ErrOut: &output})
			if err := command.Run(context.Background(), test.args); err != nil {
				t.Fatal(err)
			}
			batch := strings.Contains(strings.Join(launcher.LastArgs(), " "), "BatchMode=yes")
			if batch != test.wantBatch {
				t.Fatalf("OpenSSH args = %q, BatchMode=yes present %v, want %v", launcher.LastArgs(), batch, test.wantBatch)
			}
		})
	}
}

type controllerStartForbidden struct{ t *testing.T }

func (s controllerStartForbidden) Start(context.Context, state.Layout) error {
	s.t.Error("observing status must not start a controller")
	return errors.New("controller start forbidden")
}

func TestPrimaryCommandRelaysOpenSSHPromptsToTheRequester(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name         string
		args         []string
		answer       string
		decline      bool
		wantErr      bool
		wantPrompted int
	}{
		{name: "correct passphrase", args: []string{"kamui", "reyna"}, answer: "open sesame", wantPrompted: 1},
		{name: "wrong passphrase", args: []string{"kamui", "reyna"}, answer: "guess", wantErr: true, wantPrompted: 1},
		{name: "declined", args: []string{"kamui", "reyna"}, decline: true, wantErr: true, wantPrompted: 1},
		{name: "non-interactive", args: []string{"kamui", "--non-interactive", "reyna"}, answer: "open sesame", wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			root, err := os.MkdirTemp("/tmp", "kamui-cli-prompt-")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.RemoveAll(root) })
			layout := state.NewLayout(root)
			launcher := &testsupport.PromptingSSHLauncher{Prompt: "Enter passphrase for key '/k': ", Passphrase: "open sesame"}
			manager := session.NewManagerWithOptions(session.ManagerOptions{
				Transport:        ssh.Transport{Launcher: launcher, ReadinessTimeout: time.Second, AskpassProgram: "/usr/local/bin/kamui"},
				MirrorDiscoverer: cliDiscoverer{},
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
				if test.decline {
					return "", askpass.ErrDeclined
				}
				return test.answer, nil
			}
			var output bytes.Buffer
			command := cliapp.NewCommandWithApplication(kamuiapp.New(layout, nil), cliapp.Streams{Out: &output, ErrOut: &output, Prompter: prompter})
			err = command.Run(context.Background(), test.args)
			if (err != nil) != test.wantErr {
				t.Fatalf("Run error = %v, want error %v; output %q", err, test.wantErr, output.String())
			}
			if len(prompts) != test.wantPrompted || launcher.Prompted() != test.wantPrompted {
				t.Fatalf("requester saw %d prompts, OpenSSH asked %d times; want %d", len(prompts), launcher.Prompted(), test.wantPrompted)
			}
			if test.wantPrompted > 0 && prompts[0] != (askpass.Prompt{Text: "Enter passphrase for key '/k': ", Kind: askpass.Secret}) {
				t.Fatalf("prompt = %#v", prompts[0])
			}
			if !test.wantErr && !strings.Contains(output.String(), "● connected") {
				t.Fatalf("output = %q, want connected session", output.String())
			}
		})
	}
}

func TestNonInteractiveConnectKeepsAgentForwardingWarning(t *testing.T) {
	t.Parallel()

	root, err := os.MkdirTemp("/tmp", "kamui-cli-warning-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	layout := state.NewLayout(root)
	launcher := &testsupport.SSHLauncher{}
	manager := session.NewManagerWithOptions(session.ManagerOptions{
		Transport: ssh.Transport{
			SSHPath: "/usr/bin/true", Launcher: launcher, ReadinessTimeout: time.Second,
			Inspector: agentForwardingInspector{},
		},
		MirrorDiscoverer: cliDiscoverer{},
		MirrorInterval:   time.Hour,
	})
	t.Cleanup(func() { _ = manager.Close() })
	server, err := controller.Start(context.Background(), layout, manager)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Close() })
	var output, errorOutput bytes.Buffer
	command := cliapp.NewCommandWithApplication(kamuiapp.New(layout, nil), cliapp.Streams{Out: &output, ErrOut: &errorOutput})
	if err := command.Run(context.Background(), []string{"kamui", "mirror", "--non-interactive", "reyna"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(errorOutput.String(), "WARNING: SSH agent forwarding is enabled for reyna") {
		t.Fatalf("stderr = %q, want agent-forwarding warning", errorOutput.String())
	}
	if !strings.Contains(strings.Join(launcher.LastArgs(), " "), "BatchMode=yes") {
		t.Fatalf("OpenSSH args = %q, want BatchMode=yes", launcher.LastArgs())
	}
}

func TestMirrorCommandTreatsCommandNamesAsDestinations(t *testing.T) {
	t.Parallel()

	root, err := os.MkdirTemp("/tmp", "kamui-cli-reserved-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	layout := state.NewLayout(root)
	manager := session.NewManagerWithOptions(session.ManagerOptions{
		Transport:        ssh.Transport{Launcher: &testsupport.SSHLauncher{}, ReadinessTimeout: time.Second},
		MirrorDiscoverer: cliDiscoverer{},
		MirrorInterval:   time.Hour,
	})
	t.Cleanup(func() { _ = manager.Close() })
	server, err := controller.Start(context.Background(), layout, manager)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Close() })
	application := kamuiapp.New(layout, nil)
	for _, name := range []string{"status", "logs", "browsers"} {
		var output bytes.Buffer
		command := cliapp.NewCommandWithApplication(application, cliapp.Streams{Out: &output, ErrOut: &output})
		if err := command.Run(context.Background(), []string{"kamui", "mirror", "--non-interactive", name}); err != nil {
			t.Fatalf("mirror %s: %v: %s", name, err, output.String())
		}
		if _, err := application.Execute(context.Background(), kamuiapp.Request{Operation: kamuiapp.Status, Destination: name}); err != nil {
			t.Fatalf("mirror %s did not create a session: %v", name, err)
		}
	}
}

type agentForwardingInspector struct{}

func (agentForwardingInspector) AgentForwarding(context.Context, string, string) (bool, error) {
	return true, nil
}

func TestConnectWithoutPromptRelayStillWorksWhenBrokerCannotStart(t *testing.T) {
	t.Parallel()
	if os.Geteuid() == 0 {
		t.Skip("root ignores the directory permissions that block the prompt socket")
	}

	root, err := os.MkdirTemp("/tmp", "kamui-cli-nobroker-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	layout := state.NewLayout(root)
	manager := session.NewManagerWithOptions(session.ManagerOptions{
		Transport:        ssh.Transport{Launcher: &testsupport.SSHLauncher{}, ReadinessTimeout: time.Second, AskpassProgram: "/usr/local/bin/kamui"},
		MirrorDiscoverer: cliDiscoverer{},
		MirrorInterval:   time.Hour,
	})
	t.Cleanup(func() { _ = manager.Close() })
	server, err := controller.Start(context.Background(), layout, manager)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Close() })
	// The controller's socket already exists; a read-only runtime directory
	// prevents creating a prompt socket.
	if err := os.Chmod(layout.Runtime, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(layout.Runtime, 0o700) })
	prompter := func(context.Context, askpass.Prompt) (string, error) { return "", nil }
	var output, errorOutput bytes.Buffer
	command := cliapp.NewCommandWithApplication(kamuiapp.New(layout, nil), cliapp.Streams{Out: &output, ErrOut: &errorOutput, Prompter: prompter})
	if err := command.Run(context.Background(), []string{"kamui", "reyna"}); err != nil {
		t.Fatalf("connect: %v; stderr %q", err, errorOutput.String())
	}
	if !strings.Contains(errorOutput.String(), "NOTICE: OpenSSH prompts cannot be relayed") {
		t.Fatalf("stderr = %q, want relay notice", errorOutput.String())
	}
}
