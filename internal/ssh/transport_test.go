package ssh_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/narasaka/kamui/internal/session"
	"github.com/narasaka/kamui/internal/ssh"
)

func TestTransportStartsSafeOpenSSHAndWaitsForSOCKSReadiness(t *testing.T) {
	t.Parallel()

	launcher := &readyLauncher{}
	destination, err := session.ParseDestination("narasaka@dev.example.com")
	if err != nil {
		t.Fatal(err)
	}
	transport := ssh.Transport{
		SSHPath:          "/usr/bin/ssh",
		Launcher:         launcher,
		ReadinessTimeout: time.Second,
	}
	connection, err := transport.Connect(context.Background(), destination.String())
	if err != nil {
		t.Fatalf("Connect returned error: %v", err)
	}
	t.Cleanup(func() { _ = connection.Close() })

	wantPrefix := []string{
		"-N", "-T", "-D", launcher.address,
		"-o", "ExitOnForwardFailure=yes",
		"-o", "ServerAliveInterval=15",
		"-o", "ServerAliveCountMax=3",
		"-o", "PermitLocalCommand=no",
	}
	want := append(wantPrefix, "narasaka@dev.example.com")
	if launcher.request.Path != "/usr/bin/ssh" {
		t.Errorf("process path = %q, want /usr/bin/ssh", launcher.request.Path)
	}
	if !reflect.DeepEqual(launcher.request.Args, want) {
		t.Fatalf("process args = %#v, want %#v", launcher.request.Args, want)
	}
	if connection.Snapshot().State != ssh.Connected {
		t.Fatalf("connection state = %v, want connected", connection.Snapshot().State)
	}
}

func TestTransportFindsOpenSSHOnPathWhenNoExecutableIsConfigured(t *testing.T) {
	root := t.TempDir()
	sshPath := filepath.Join(root, "ssh")
	if err := os.WriteFile(sshPath, []byte("#!/bin/sh\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", root)

	launcher := &readyLauncher{}
	connection, err := (ssh.Transport{Launcher: launcher, ReadinessTimeout: time.Second}).Connect(context.Background(), "reyna")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = connection.Close() })
	if launcher.request.Path != sshPath {
		t.Fatalf("OpenSSH executable = %q, want PATH result %q", launcher.request.Path, sshPath)
	}
}

func TestTransportRoutesPostConnectOpenSSHErrorsOnlyToBackgroundLog(t *testing.T) {
	t.Parallel()

	launcher := &readyLauncher{}
	var terminal, background bytes.Buffer
	transport := ssh.Transport{
		Launcher:         launcher,
		Stderr:           &terminal,
		BackgroundStderr: &background,
		ReadinessTimeout: time.Second,
	}
	connection, err := transport.Connect(context.Background(), "reyna")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = connection.Close() })

	message := "channel 1: open failed: connect failed: dial tcp 127.0.0.1:3000: connect: connection refused\n"
	if _, err := io.WriteString(launcher.request.Stderr, message); err != nil {
		t.Fatal(err)
	}
	if got := terminal.String(); got != "" {
		t.Fatalf("terminal stderr after readiness = %q, want no output", got)
	}
	if got := background.String(); got != message {
		t.Fatalf("background stderr = %q, want %q", got, message)
	}
}

func TestUnattendedTransportNeverWritesOpenSSHErrorsToTerminal(t *testing.T) {
	t.Parallel()

	launcher := &diagnosticReadyLauncher{message: "Permission denied (publickey).\n"}
	var terminal, background bytes.Buffer
	transport := ssh.Transport{
		Launcher:         launcher,
		Stderr:           &terminal,
		BackgroundStderr: &background,
		ReadinessTimeout: time.Second,
	}
	connection, err := transport.ConnectUnattended(context.Background(), "reyna")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = connection.Close() })

	if got := terminal.String(); got != "" {
		t.Fatalf("terminal stderr = %q, want no unattended output", got)
	}
	if got := background.String(); got != launcher.message {
		t.Fatalf("background stderr = %q, want %q", got, launcher.message)
	}
}

func TestInteractiveTransportCopiesBootstrapDiagnosticsToTerminalAndLog(t *testing.T) {
	t.Parallel()

	launcher := &diagnosticReadyLauncher{message: "The authenticity of host cannot be established.\n"}
	var terminal, background bytes.Buffer
	transport := ssh.Transport{
		Launcher:         launcher,
		Stderr:           &terminal,
		BackgroundStderr: &background,
		ReadinessTimeout: time.Second,
	}
	connection, err := transport.Connect(context.Background(), "reyna")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = connection.Close() })

	if got := terminal.String(); got != launcher.message {
		t.Fatalf("terminal bootstrap stderr = %q, want %q", got, launcher.message)
	}
	if got := background.String(); got != launcher.message {
		t.Fatalf("background bootstrap stderr = %q, want %q", got, launcher.message)
	}
}

func TestTransportRetriesWhenSOCKSPortLosesBindRace(t *testing.T) {
	t.Parallel()

	launcher := &bindRaceLauncher{}
	destination, _ := session.ParseDestination("reyna")
	connection, err := (ssh.Transport{Launcher: launcher, ReadinessTimeout: time.Second}).Connect(context.Background(), destination.String())
	if err != nil {
		t.Fatalf("Connect returned error after bind race: %v", err)
	}
	t.Cleanup(func() { _ = connection.Close() })
	if launcher.starts != 2 {
		t.Fatalf("OpenSSH start count = %d, want 2", launcher.starts)
	}
}

func TestConnectionCloseTreatsKilledChildExitAsSuccessfulCleanup(t *testing.T) {
	t.Parallel()

	launcher := &readyLauncher{waitErr: errors.New("signal: killed")}
	connection, err := (ssh.Transport{Launcher: launcher, ReadinessTimeout: time.Second}).Connect(context.Background(), "reyna")
	if err != nil {
		t.Fatal(err)
	}
	if err := connection.Close(); err != nil {
		t.Fatalf("Close returned expected killed-child exit: %v", err)
	}
}

func TestUnattendedReconnectDisablesAuthenticationPrompts(t *testing.T) {
	t.Parallel()

	launcher := &readyLauncher{}
	connection, err := (ssh.Transport{Launcher: launcher, ReadinessTimeout: time.Second}).ConnectUnattended(context.Background(), "reyna")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = connection.Close() })
	wantPair := false
	for index := 0; index+1 < len(launcher.request.Args); index++ {
		if launcher.request.Args[index] == "-o" && launcher.request.Args[index+1] == "BatchMode=yes" {
			wantPair = true
		}
	}
	if !wantPair {
		t.Fatalf("unattended OpenSSH args = %v, want BatchMode=yes", launcher.request.Args)
	}
}

func TestInteractiveConnectWarnsWhenAgentForwardingIsEnabled(t *testing.T) {
	t.Parallel()

	launcher := &readyLauncher{}
	var stderr bytes.Buffer
	transport := ssh.Transport{
		Launcher: launcher, Inspector: enabledAgentInspector{}, Stderr: &stderr,
		ReadinessTimeout: time.Second,
	}
	connection, err := transport.Connect(context.Background(), "reyna")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = connection.Close() })
	if got := stderr.String(); !strings.Contains(got, "agent forwarding") || !strings.Contains(got, "reyna") {
		t.Fatalf("warning = %q, want destination-specific agent-forwarding warning", got)
	}
}

type enabledAgentInspector struct{}

func (enabledAgentInspector) AgentForwarding(context.Context, string, string) (bool, error) {
	return true, nil
}

func TestConnectionDialsTargetsThroughSOCKS5(t *testing.T) {
	t.Parallel()

	launcher := &socksLauncher{}
	destination, _ := session.ParseDestination("reyna")
	connection, err := (ssh.Transport{Launcher: launcher, ReadinessTimeout: time.Second}).Connect(context.Background(), destination.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = connection.Close() })

	stream, err := connection.DialContext(context.Background(), "tcp", "127.0.0.1:3003")
	if err != nil {
		t.Fatalf("DialContext returned error: %v", err)
	}
	defer func() { _ = stream.Close() }()
	if _, err := stream.Write([]byte("ping")); err != nil {
		t.Fatal(err)
	}
	echo := make([]byte, 4)
	if _, err := io.ReadFull(stream, echo); err != nil {
		t.Fatal(err)
	}
	if string(echo) != "ping" {
		t.Fatalf("echo = %q, want ping", echo)
	}
	if got := launcher.target(); got != "127.0.0.1:3003" {
		t.Fatalf("SOCKS target = %q, want 127.0.0.1:3003", got)
	}
}

func TestConnectionReturnsSOCKSFailureCode(t *testing.T) {
	t.Parallel()

	launcher := &socksLauncher{replyCode: 5}
	connection, err := (ssh.Transport{Launcher: launcher, ReadinessTimeout: time.Second}).Connect(context.Background(), "reyna")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = connection.Close() })
	_, err = connection.DialContext(context.Background(), "tcp", "127.0.0.1:3999")
	var socksError *ssh.SOCKSError
	if !errors.As(err, &socksError) || socksError.Code != 5 {
		t.Fatalf("DialContext error = %v, want SOCKS code 5", err)
	}
}

type readyLauncher struct {
	request ssh.StartRequest
	address string
	waitErr error
}

type diagnosticReadyLauncher struct {
	readyLauncher
	message string
}

func (l *diagnosticReadyLauncher) Start(request ssh.StartRequest) (ssh.Process, error) {
	if _, err := io.WriteString(request.Stderr, l.message); err != nil {
		return nil, err
	}
	return l.readyLauncher.Start(request)
}

func (l *readyLauncher) Start(request ssh.StartRequest) (ssh.Process, error) {
	l.request = request
	for index, argument := range request.Args {
		if argument == "-D" && index+1 < len(request.Args) {
			l.address = request.Args[index+1]
			break
		}
	}
	listener, err := net.Listen("tcp4", l.address)
	if err != nil {
		return nil, err
	}
	process := &listeningProcess{listener: listener, done: make(chan struct{}), waitErr: l.waitErr}
	go func() {
		for {
			connection, err := listener.Accept()
			if err != nil {
				return
			}
			_ = connection.Close()
		}
	}()
	return process, nil
}

type listeningProcess struct {
	listener net.Listener
	done     chan struct{}
	waitErr  error
}

type socksLauncher struct {
	mu        sync.Mutex
	requested string
	replyCode byte
}

type bindRaceLauncher struct {
	starts int
	readyLauncher
}

func (l *bindRaceLauncher) Start(request ssh.StartRequest) (ssh.Process, error) {
	l.starts++
	if l.starts == 1 {
		_, _ = io.WriteString(request.Stderr, "bind [127.0.0.1]: Address already in use\n")
		return exitedProcess{err: errors.New("exit status 255")}, nil
	}
	return l.readyLauncher.Start(request)
}

type exitedProcess struct {
	err error
}

func (p exitedProcess) Wait() error          { return p.err }
func (exitedProcess) Signal(os.Signal) error { return os.ErrProcessDone }
func (exitedProcess) Kill() error            { return os.ErrProcessDone }

func (l *socksLauncher) Start(request ssh.StartRequest) (ssh.Process, error) {
	var address string
	for index, argument := range request.Args {
		if argument == "-D" && index+1 < len(request.Args) {
			address = request.Args[index+1]
		}
	}
	listener, err := net.Listen("tcp4", address)
	if err != nil {
		return nil, err
	}
	process := &listeningProcess{listener: listener, done: make(chan struct{})}
	go func() {
		for {
			connection, err := listener.Accept()
			if err != nil {
				return
			}
			go l.handle(connection)
		}
	}()
	return process, nil
}

func (l *socksLauncher) handle(connection net.Conn) {
	defer func() { _ = connection.Close() }()
	greeting := make([]byte, 3)
	if _, err := io.ReadFull(connection, greeting); err != nil {
		return
	}
	if _, err := connection.Write([]byte{5, 0}); err != nil {
		return
	}
	header := make([]byte, 4)
	if _, err := io.ReadFull(connection, header); err != nil || header[3] != 1 {
		return
	}
	address := make([]byte, 4)
	port := make([]byte, 2)
	if _, err := io.ReadFull(connection, address); err != nil {
		return
	}
	if _, err := io.ReadFull(connection, port); err != nil {
		return
	}
	l.mu.Lock()
	l.requested = net.JoinHostPort(net.IP(address).String(), fmt.Sprint(binary.BigEndian.Uint16(port)))
	l.mu.Unlock()
	if _, err := connection.Write([]byte{5, l.replyCode, 0, 1, 0, 0, 0, 0, 0, 0}); err != nil || l.replyCode != 0 {
		return
	}
	_, _ = io.Copy(connection, connection)
}

func (l *socksLauncher) target() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.requested
}

func (p *listeningProcess) Wait() error {
	<-p.done
	return p.waitErr
}

func (p *listeningProcess) Signal(os.Signal) error {
	return p.Kill()
}

func (p *listeningProcess) Kill() error {
	select {
	case <-p.done:
		return nil
	default:
		close(p.done)
		return p.listener.Close()
	}
}

var _ io.Closer = (*ssh.Connection)(nil)
