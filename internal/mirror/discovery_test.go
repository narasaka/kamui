package mirror_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
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

func (r *recordingRunner) Run(_ context.Context, path string, stdout, _ io.Writer, args ...string) error {
	r.path = path
	r.args = append([]string(nil), args...)
	_, _ = stdout.Write(r.output)
	return r.err
}
