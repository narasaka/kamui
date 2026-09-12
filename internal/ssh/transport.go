// Package ssh starts and supervises the system OpenSSH client used by Kamui.
package ssh

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/narasaka/kamui/internal/session"
)

// State describes the availability of an SSH connection.
type State uint32

const (
	// Connecting means the OpenSSH child has started but SOCKS is not ready.
	Connecting State = iota
	// Connected means new SOCKS connections can be opened.
	Connected
	// Unavailable means the child exited or was stopped.
	Unavailable
	// AuthenticationRequired means an unattended reconnect needs user action.
	AuthenticationRequired
)

// Snapshot is an instantaneous view of transport health.
type Snapshot struct {
	State State
	Error error
}

// StartRequest is one operating-system process invocation.
type StartRequest struct {
	Path   string
	Args   []string
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
}

// Process is the controllable portion of an operating-system child process.
type Process interface {
	Wait() error
	Signal(os.Signal) error
	Kill() error
}

// Launcher starts external processes. The production adapter uses os/exec.
type Launcher interface {
	Start(StartRequest) (Process, error)
}

// Transport configures the system OpenSSH client.
type Transport struct {
	SSHPath          string
	Launcher         Launcher
	Stdin            io.Reader
	Stdout           io.Writer
	Stderr           io.Writer
	ReadinessTimeout time.Duration
}

// Connect starts OpenSSH and returns only after its SOCKS listener is ready.
func (t Transport) Connect(ctx context.Context, destination session.Destination) (*Connection, error) {
	var lastErr error
	for range 3 {
		connection, stderr, err := t.connectAttempt(ctx, destination)
		if err == nil {
			return connection, nil
		}
		lastErr = err
		if !isForwardBindRace(stderr) {
			return nil, err
		}
	}
	return nil, fmt.Errorf("allocate OpenSSH SOCKS port after retries: %w", lastErr)
}

func (t Transport) connectAttempt(ctx context.Context, destination session.Destination) (*Connection, string, error) {
	address, err := availableLoopbackAddress()
	if err != nil {
		return nil, "", err
	}

	path := t.SSHPath
	if path == "" {
		path = "/usr/bin/ssh"
	}
	launcher := t.Launcher
	if launcher == nil {
		launcher = execLauncher{}
	}
	var stderr bytes.Buffer
	request := StartRequest{
		Path: path,
		Args: []string{
			"-N", "-T", "-D", address,
			"-o", "ExitOnForwardFailure=yes",
			"-o", "ServerAliveInterval=15",
			"-o", "ServerAliveCountMax=3",
			"-o", "PermitLocalCommand=no",
			destination.String(),
		},
		Stdin:  defaultReader(t.Stdin),
		Stdout: defaultWriter(t.Stdout),
		Stderr: io.MultiWriter(defaultWriter(t.Stderr), &stderr),
	}
	process, err := launcher.Start(request)
	if err != nil {
		return nil, stderr.String(), fmt.Errorf("start OpenSSH: %w", err)
	}

	connection := newConnection(address, process)
	timeout := t.ReadinessTimeout
	if timeout == 0 {
		timeout = 30 * time.Second
	}
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()

	for {
		probe, err := (&net.Dialer{Timeout: 100 * time.Millisecond}).DialContext(ctx, "tcp4", address)
		if err == nil {
			_ = probe.Close()
			connection.state.Store(uint32(Connected))
			return connection, stderr.String(), nil
		}
		select {
		case <-connection.done:
			return nil, stderr.String(), fmt.Errorf("OpenSSH exited before SOCKS became ready: %w", connection.exitError())
		case <-ctx.Done():
			_ = connection.Close()
			return nil, stderr.String(), ctx.Err()
		case <-deadline.C:
			_ = connection.Close()
			return nil, stderr.String(), fmt.Errorf("OpenSSH SOCKS listener was not ready within %s", timeout)
		case <-ticker.C:
		}
	}
}

func isForwardBindRace(stderr string) bool {
	folded := strings.ToLower(stderr)
	return strings.Contains(folded, "address already in use") ||
		strings.Contains(folded, "cannot listen to port")
}

// Connection is a supervised OpenSSH SOCKS connection.
type Connection struct {
	address   string
	process   Process
	state     atomic.Uint32
	done      chan struct{}
	closeOnce sync.Once
	errMu     sync.RWMutex
	err       error
}

func newConnection(address string, process Process) *Connection {
	connection := &Connection{address: address, process: process, done: make(chan struct{})}
	connection.state.Store(uint32(Connecting))
	go func() {
		err := process.Wait()
		connection.errMu.Lock()
		connection.err = err
		connection.errMu.Unlock()
		connection.state.Store(uint32(Unavailable))
		close(connection.done)
	}()
	return connection
}

// Snapshot reports whether the OpenSSH child and SOCKS listener are available.
func (c *Connection) Snapshot() Snapshot {
	return Snapshot{State: State(c.state.Load()), Error: c.exitError()}
}

// Close kills and reaps the OpenSSH child.
func (c *Connection) Close() error {
	c.closeOnce.Do(func() {
		_ = c.process.Kill()
		<-c.done
	})
	return c.exitError()
}

func (c *Connection) exitError() error {
	c.errMu.RLock()
	defer c.errMu.RUnlock()
	return c.err
}

func availableLoopbackAddress() (string, error) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return "", fmt.Errorf("allocate SOCKS port: %w", err)
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		return "", fmt.Errorf("release SOCKS port reservation: %w", err)
	}
	return address, nil
}

type execLauncher struct{}

func (execLauncher) Start(request StartRequest) (Process, error) {
	command := exec.Command(request.Path, request.Args...)
	command.Stdin = request.Stdin
	command.Stdout = request.Stdout
	command.Stderr = request.Stderr
	if err := command.Start(); err != nil {
		return nil, err
	}
	return execProcess{command: command}, nil
}

type execProcess struct {
	command *exec.Cmd
}

func (p execProcess) Wait() error                   { return p.command.Wait() }
func (p execProcess) Signal(signal os.Signal) error { return p.command.Process.Signal(signal) }
func (p execProcess) Kill() error                   { return p.command.Process.Kill() }

func defaultReader(reader io.Reader) io.Reader {
	if reader == nil {
		return os.Stdin
	}
	return reader
}

func defaultWriter(writer io.Writer) io.Writer {
	if writer == nil {
		return io.Discard
	}
	return writer
}
