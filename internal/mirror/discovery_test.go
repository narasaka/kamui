package mirror_test

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/narasaka/kamui/internal/mirror"
)

func TestSSHDiscovererStreamsAuthenticationDiagnosticsBeforeExit(t *testing.T) {
	t.Parallel()

	const diagnostic = "# Tailscale SSH requires an additional check.\n# To authenticate, visit: https://login.tailscale.com/a/example\n"
	runner := &blockingDiagnosticRunner{
		diagnostic: diagnostic,
		written:    make(chan struct{}),
		release:    make(chan struct{}),
	}
	var diagnostics bytes.Buffer
	discoverer := mirror.SSHDiscoverer{
		SSHPath: "/usr/bin/ssh",
		Runner:  runner,
		Stderr:  &diagnostics,
	}
	done := make(chan error, 1)
	go func() {
		_, err := discoverer.ListeningPorts(context.Background(), "reyna")
		done <- err
	}()

	select {
	case <-runner.written:
	case <-time.After(time.Second):
		t.Fatal("fake SSH process did not write its authentication diagnostic")
	}
	if got := diagnostics.String(); got != diagnostic {
		t.Fatalf("streamed diagnostics = %q, want %q before SSH exits", got, diagnostic)
	}
	close(runner.release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestSSHDiscovererParsesAndSortsSSListeningPorts(t *testing.T) {
	t.Parallel()

	runner := &recordingRunner{output: []byte("LISTEN 0 4096 127.0.0.1:3001 0.0.0.0:*\nLISTEN 0 128 [::]:3000 [::]:*\nLISTEN 0 128 *:3001 *:*\n")}
	discoverer := mirror.SSHDiscoverer{SSHPath: "/usr/bin/ssh", Runner: runner}
	ports, err := discoverer.ListeningPorts(context.Background(), "reyna")
	if err != nil {
		t.Fatal(err)
	}
	if want := []uint16{3000, 3001}; !reflect.DeepEqual(ports, want) {
		t.Fatalf("ports = %v, want %v", ports, want)
	}
	if got := runner.args[:9]; !reflect.DeepEqual(got, []string{
		"-T", "-o", "BatchMode=yes", "-o", "PermitLocalCommand=no", "--", "reyna", "sh", "-c",
	}) {
		t.Fatalf("SSH argument prefix = %v", got)
	}
}

func TestSSHDiscovererDiscoversMacOSLsofListeningPortsWithoutRemoteKamui(t *testing.T) {
	t.Parallel()

	runner := &recordingRunner{output: []byte("kamui:lsof\np412\nf10\nn127.0.0.1:3001\nf11\nn[::1]:3000\np913\nf8\nn*:3001\n")}
	discoverer := mirror.SSHDiscoverer{SSHPath: "/custom/bin/ssh", Runner: runner}
	ports, err := discoverer.ListeningPorts(context.Background(), "mac-studio")
	if err != nil {
		t.Fatal(err)
	}
	if want := []uint16{3000, 3001}; !reflect.DeepEqual(ports, want) {
		t.Fatalf("ports = %v, want %v", ports, want)
	}
	if runner.path != "/custom/bin/ssh" {
		t.Fatalf("SSH path = %q, want custom executable", runner.path)
	}
	if got := runner.args[:8]; !reflect.DeepEqual(got, []string{
		"-T", "-o", "BatchMode=yes", "-o", "PermitLocalCommand=no", "--", "mac-studio", "sh",
	}) {
		t.Fatalf("SSH argument prefix = %v", got)
	}
	remoteCommand := strings.Join(runner.args[8:], " ")
	if !strings.Contains(remoteCommand, "command -v ss") || !strings.Contains(remoteCommand, "lsof") {
		t.Fatalf("remote discovery command = %q, want Linux and macOS capability probes", remoteCommand)
	}
}

func TestSSHDiscovererFallsBackToMacOSNetstatForListeningPorts(t *testing.T) {
	t.Parallel()

	runner := &recordingRunner{output: []byte("kamui:netstat\nActive Internet connections\nProto Recv-Q Send-Q  Local Address          Foreign Address        (state)\ntcp4       0      0  127.0.0.1.3001       *.*                    LISTEN\ntcp6       0      0  ::1.3000              *.*                    LISTEN\ntcp4       0      0  192.0.2.10.443        198.51.100.8.51000     ESTABLISHED\n")}
	ports, err := (mirror.SSHDiscoverer{Runner: runner}).ListeningPorts(context.Background(), "mac-mini")
	if err != nil {
		t.Fatal(err)
	}
	if want := []uint16{3000, 3001}; !reflect.DeepEqual(ports, want) {
		t.Fatalf("ports = %v, want %v", ports, want)
	}
	if remoteCommand := strings.Join(runner.args[8:], " "); !strings.Contains(remoteCommand, "exec netstat -an") || strings.Contains(remoteCommand, "netstat -an -p tcp") {
		t.Fatalf("remote discovery command = %q, want portable netstat fallback", remoteCommand)
	}
}

func TestSSHDiscovererParsesLinuxNetstatFallback(t *testing.T) {
	t.Parallel()

	runner := &recordingRunner{output: []byte("kamui:netstat\nActive Internet connections\nProto Recv-Q Send-Q Local Address Foreign Address State\ntcp 0 0 127.0.0.1:3001 0.0.0.0:* LISTEN\ntcp6 0 0 :::3000 :::* LISTEN\nudp 0 0 127.0.0.1:5353 0.0.0.0:*\n")}
	ports, err := (mirror.SSHDiscoverer{Runner: runner}).ListeningPorts(context.Background(), "linux-minimal")
	if err != nil {
		t.Fatal(err)
	}
	if want := []uint16{3000, 3001}; !reflect.DeepEqual(ports, want) {
		t.Fatalf("ports = %v, want %v", ports, want)
	}
}

func TestSSHDiscovererTreatsLsofNoMatchesAsNoListeners(t *testing.T) {
	t.Parallel()

	runner := &recordingRunner{output: []byte("kamui:lsof\n"), err: exitStatus(1)}
	ports, err := (mirror.SSHDiscoverer{Runner: runner}).ListeningPorts(context.Background(), "quiet-mac")
	if err != nil {
		t.Fatal(err)
	}
	if len(ports) != 0 {
		t.Fatalf("ports = %v, want no listeners", ports)
	}
}

func TestSSHDiscovererReportsUnavailableAndMalformedSSOutput(t *testing.T) {
	t.Parallel()

	for name, runner := range map[string]*recordingRunner{
		"unavailable": {output: []byte("sh: ss: command not found\n"), err: errors.New("exit status 127")},
		"malformed":   {output: []byte("this is not ss output\n")},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, err := (mirror.SSHDiscoverer{Runner: runner}).ListeningPorts(context.Background(), "reyna")
			if err == nil || !strings.Contains(err.Error(), "ss") {
				t.Fatalf("error = %v, want clear ss discovery error", err)
			}
		})
	}
}

func TestSSHDiscovererFindsOpenSSHOnPath(t *testing.T) {
	root := t.TempDir()
	sshPath := filepath.Join(root, "ssh")
	if err := os.WriteFile(sshPath, []byte("#!/bin/sh\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", root)
	runner := &recordingRunner{output: []byte("kamui:ss\n")}

	if _, err := (mirror.SSHDiscoverer{Runner: runner}).ListeningPorts(context.Background(), "reyna"); err != nil {
		t.Fatal(err)
	}
	if runner.path != sshPath {
		t.Fatalf("OpenSSH executable = %q, want PATH result %q", runner.path, sshPath)
	}
}

type recordingRunner struct {
	output []byte
	stderr string
	err    error
	path   string
	args   []string
}

type blockingDiagnosticRunner struct {
	diagnostic string
	written    chan struct{}
	release    chan struct{}
}

func (r *blockingDiagnosticRunner) Run(_ context.Context, _ string, stdout, stderr io.Writer, _ ...string) error {
	if _, err := io.WriteString(stderr, r.diagnostic); err != nil {
		return err
	}
	close(r.written)
	<-r.release
	_, err := io.WriteString(stdout, "kamui:ss\n")
	return err
}

type exitStatus int

func (status exitStatus) Error() string { return "exit status " + fmt.Sprint(int(status)) }
func (status exitStatus) ExitCode() int { return int(status) }

func (r *recordingRunner) Run(_ context.Context, path string, stdout, stderr io.Writer, args ...string) error {
	r.path = path
	r.args = append([]string(nil), args...)
	_, _ = stdout.Write(r.output)
	_, _ = io.WriteString(stderr, r.stderr)
	return r.err
}

func TestSSHDiscovererWatchStreamsListenerReports(t *testing.T) {
	t.Parallel()

	runner := &recordingRunner{output: []byte(strings.Join([]string{
		"Welcome to reyna",
		"kamui:ss",
		"kamui:snapshot",
		"LISTEN 0 4096 127.0.0.1:3001 0.0.0.0:*",
		"LISTEN 0 128 [::]:3000 [::]:*",
		"kamui:end",
		"kamui:same",
		"kamui:error 2",
		"kamui:snapshot",
		"",
		"kamui:end",
		"kamui:snapshot",
		"not ss output",
		"kamui:end",
		"kamui:same",
	}, "\n") + "\n")}
	type listenerReport struct {
		ports []uint16
		err   string
	}
	var reports []listenerReport
	err := (mirror.SSHDiscoverer{SSHPath: "/usr/bin/ssh", Runner: runner}).WatchListeningPorts(context.Background(), "reyna", func(ports []uint16, err error) {
		report := listenerReport{ports: ports}
		if err != nil {
			report.err = err.Error()
		}
		reports = append(reports, report)
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := runner.args[:9]; !reflect.DeepEqual(got, []string{
		"-T", "-o", "BatchMode=yes", "-o", "PermitLocalCommand=no", "--", "reyna", "sh", "-c",
	}) {
		t.Fatalf("SSH argument prefix = %v", got)
	}
	if len(reports) != 6 {
		t.Fatalf("reports = %#v, want 6", reports)
	}
	for index, want := range []listenerReport{
		{ports: []uint16{3000, 3001}},
		{ports: []uint16{3000, 3001}},
		{err: "remote ss exited with status 2"},
		{ports: []uint16{}},
	} {
		if !reflect.DeepEqual(reports[index], want) {
			t.Fatalf("report %d = %#v, want %#v", index, reports[index], want)
		}
	}
	for _, report := range reports[4:] {
		if !strings.Contains(report.err, "parse remote ss output") {
			t.Fatalf("malformed snapshot report = %#v, want parse error repeated while unchanged", report)
		}
	}
}

func TestSSHDiscovererWatchReportsSSHFailureDetail(t *testing.T) {
	t.Parallel()

	runner := &recordingRunner{output: []byte("kamui:ss\n"), err: exitStatus(255)}
	runner.stderr = "Connection to reyna closed by remote host.\n"
	err := (mirror.SSHDiscoverer{Runner: runner}).WatchListeningPorts(context.Background(), "reyna", func([]uint16, error) {})
	if err == nil || !strings.Contains(err.Error(), "exit status 255") || !strings.Contains(err.Error(), "closed by remote host") {
		t.Fatalf("error = %v, want SSH exit status and stderr detail", err)
	}
}

// This test is not parallel: a child forked while another test closes a
// listener briefly inherits that socket and makes immediate rebinding fail.
func TestRemoteListenerWatchScriptReportsLocalListenerChanges(t *testing.T) {
	tool := ""
	for _, candidate := range []string{"ss", "lsof", "netstat"} {
		if _, err := exec.LookPath(candidate); err == nil {
			tool = candidate
			break
		}
	}
	if tool == "" {
		t.Skip("ss, lsof, and netstat are unavailable")
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := uint16(listener.Addr().(*net.TCPAddr).Port)
	reports := make(chan []uint16, 16)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- (mirror.SSHDiscoverer{SSHPath: "ssh", Runner: remoteShellRunner{}}).WatchListeningPorts(ctx, "localhost", func(ports []uint16, err error) {
			if err != nil {
				t.Errorf("%s report error: %v", tool, err)
				return
			}
			select {
			case reports <- ports:
			default:
			}
		})
	}()
	defer func() {
		cancel()
		<-done
	}()

	waitForReport := func(want bool) {
		t.Helper()
		deadline := time.After(10 * time.Second)
		for {
			select {
			case ports := <-reports:
				if slices.Contains(ports, port) == want {
					return
				}
			case <-deadline:
				t.Fatalf("%s watch never reported listener %d present=%v", tool, port, want)
			}
		}
	}
	waitForReport(true)
	_ = listener.Close()
	waitForReport(false)
}

// This test is not parallel for the same reason as the local listener test.
func TestRemoteListenerWatchScriptToolStatuses(t *testing.T) {
	for _, path := range []string{"/usr/sbin/lsof", "/usr/sbin/netstat"} {
		if _, err := os.Stat(path); err == nil {
			t.Skipf("%s would shadow the fake discovery tools", path)
		}
	}
	for name, test := range map[string]struct {
		tool, script string
		brokenGrep   bool
		ports        []uint16
		err          string
	}{
		"lsof without listeners":    {tool: "lsof", script: "exit 1", ports: []uint16{}},
		"lsof failure":              {tool: "lsof", script: "echo lsof broke >&2; exit 2", err: "remote lsof exited with status 2"},
		"netstat listeners":         {tool: "netstat", script: "echo \"tcp 0 0 127.0.0.1:3001 0.0.0.0:* LISTEN\"; echo \"tcp 0 0 127.0.0.1:3001 127.0.0.1:5000 ESTABLISHED\"", ports: []uint16{3001}},
		"netstat without listeners": {tool: "netstat", script: "echo \"tcp 0 0 127.0.0.1:3001 127.0.0.1:5000 ESTABLISHED\"", ports: []uint16{}},
		"netstat failure":           {tool: "netstat", script: "exit 1", err: "remote netstat exited with status 1"},
		"grep failure":              {tool: "netstat", script: "echo \"tcp 0 0 127.0.0.1:3001 0.0.0.0:* LISTEN\"", brokenGrep: true, err: "remote netstat exited with status 2"},
	} {
		t.Run(name, func(t *testing.T) {
			bin := t.TempDir()
			for _, command := range []string{"sh", "grep", "sleep"} {
				target, err := exec.LookPath(command)
				if err != nil {
					t.Skipf("%s is unavailable", command)
				}
				if err := os.Symlink(target, filepath.Join(bin, command)); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.WriteFile(filepath.Join(bin, test.tool), []byte("#!/bin/sh\n"+test.script+"\n"), 0o700); err != nil {
				t.Fatal(err)
			}
			if test.brokenGrep {
				_ = os.Remove(filepath.Join(bin, "grep"))
				if err := os.WriteFile(filepath.Join(bin, "grep"), []byte("#!/bin/sh\nexit 2\n"), 0o700); err != nil {
					t.Fatal(err)
				}
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var ports []uint16
			var reportErr error
			reported := false
			_ = (mirror.SSHDiscoverer{SSHPath: "ssh", Runner: remoteShellRunner{path: bin}}).WatchListeningPorts(ctx, "localhost", func(got []uint16, err error) {
				if !reported {
					ports, reportErr, reported = got, err, true
				}
				cancel()
			})
			if !reported {
				t.Fatal("watch script made no report")
			}
			if test.err != "" {
				if reportErr == nil || !strings.Contains(reportErr.Error(), test.err) {
					t.Fatalf("report error = %v, want %q", reportErr, test.err)
				}
				return
			}
			if reportErr != nil || !reflect.DeepEqual(ports, test.ports) {
				t.Fatalf("report = %v, %v; want %v", ports, reportErr, test.ports)
			}
		})
	}
}

// This test is not parallel for the same reason as the local listener test.
func TestRemoteListenerWatchScriptExitsAfterChannelCloseWithSIGPIPEIgnored(t *testing.T) {
	runner := &recordingRunner{}
	_ = (mirror.SSHDiscoverer{Runner: runner}).WatchListeningPorts(context.Background(), "reyna", func([]uint16, error) {})
	command := exec.Command("/bin/sh", "-c", "trap \"\" PIPE; sh -c "+runner.args[len(runner.args)-1])
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	if _, err := bufio.NewReader(stdout).ReadString('\n'); err != nil {
		t.Fatal(err)
	}
	_ = stdout.Close()
	exited := make(chan struct{})
	go func() {
		_ = command.Wait()
		close(exited)
	}()
	select {
	case <-exited:
	case <-time.After(5 * time.Second):
		_ = command.Process.Kill()
		t.Fatal("remote watch loop kept running after its output channel closed")
	}
}

func TestRemoteListenerWatchScriptHasNoSingleQuotes(t *testing.T) {
	t.Parallel()

	runner := &recordingRunner{}
	_ = (mirror.SSHDiscoverer{Runner: runner}).WatchListeningPorts(context.Background(), "reyna", func([]uint16, error) {})
	script := runner.args[len(runner.args)-1]
	if inner := strings.TrimSuffix(strings.TrimPrefix(script, "'"), "'"); strings.Contains(inner, "'") {
		t.Fatal("watch script contains a single quote that would end the remote shell quoting")
	}
}

// remoteShellRunner runs the remote command through a local shell the way an
// SSH server passes it to the login shell. A non-empty path replaces PATH.
type remoteShellRunner struct{ path string }

func (r remoteShellRunner) Run(ctx context.Context, _ string, stdout, stderr io.Writer, args ...string) error {
	separator := slices.Index(args, "--")
	command := exec.CommandContext(ctx, "/bin/sh", "-c", strings.Join(args[separator+2:], " "))
	if r.path != "" {
		command.Env = append(os.Environ(), "PATH="+r.path)
	}
	command.Stdout = stdout
	command.Stderr = stderr
	// The nested remote shell can outlive the killed outer shell; closing the
	// pipes ends its loop at the next write as a closed SSH channel would.
	command.WaitDelay = time.Second
	return command.Run()
}
