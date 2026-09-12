// Package testsupport contains operating-system boundary adapters used by
// integration-style tests.
package testsupport

import (
	"fmt"
	"io"
	"net"
	"os"
	"sync"

	"github.com/narasaka/kamui/internal/ssh"
)

// SSHLauncher behaves like an OpenSSH dynamic forward but makes ordinary local
// TCP connections. It is safe only for tests.
type SSHLauncher struct {
	mu        sync.Mutex
	starts    int
	processes []*SSHProcess
	requests  []ssh.StartRequest
}

func (l *SSHLauncher) Start(request ssh.StartRequest) (ssh.Process, error) {
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
	process := &SSHProcess{listener: listener, done: make(chan struct{})}
	l.mu.Lock()
	l.starts++
	l.processes = append(l.processes, process)
	l.requests = append(l.requests, request)
	l.mu.Unlock()
	go process.accept()
	return process, nil
}

// LastArgs returns the most recent simulated OpenSSH argument vector.
func (l *SSHLauncher) LastArgs() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.requests) == 0 {
		return nil
	}
	return append([]string(nil), l.requests[len(l.requests)-1].Args...)
}

// Starts returns the number of simulated OpenSSH children.
func (l *SSHLauncher) Starts() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.starts
}

// Active returns the number of simulated OpenSSH children not yet reaped.
func (l *SSHLauncher) Active() int {
	l.mu.Lock()
	processes := append([]*SSHProcess(nil), l.processes...)
	l.mu.Unlock()
	active := 0
	for _, process := range processes {
		select {
		case <-process.done:
		default:
			active++
		}
	}
	return active
}

// SSHProcess is a test-only dynamic-forward process.
type SSHProcess struct {
	listener net.Listener
	done     chan struct{}
	once     sync.Once
}

func (p *SSHProcess) accept() {
	for {
		connection, err := p.listener.Accept()
		if err != nil {
			return
		}
		go serveSOCKS(connection)
	}
}

func serveSOCKS(client net.Conn) {
	defer func() { _ = client.Close() }()
	greeting := make([]byte, 3)
	if _, err := io.ReadFull(client, greeting); err != nil {
		return
	}
	if _, err := client.Write([]byte{5, 0}); err != nil {
		return
	}
	header := make([]byte, 4)
	if _, err := io.ReadFull(client, header); err != nil || header[0] != 5 || header[1] != 1 {
		return
	}
	host, ok := readSOCKSHost(client, header[3])
	if !ok {
		return
	}
	port := make([]byte, 2)
	if _, err := io.ReadFull(client, port); err != nil {
		return
	}
	address := net.JoinHostPort(host, stringPort(port))
	remote, err := net.Dial("tcp", address)
	if err != nil {
		_, _ = client.Write([]byte{5, 5, 0, 1, 0, 0, 0, 0, 0, 0})
		return
	}
	defer func() { _ = remote.Close() }()
	if _, err := client.Write([]byte{5, 0, 0, 1, 0, 0, 0, 0, 0, 0}); err != nil {
		return
	}
	go func() { _, _ = io.Copy(remote, client); _ = remote.Close() }()
	_, _ = io.Copy(client, remote)
}

func readSOCKSHost(connection net.Conn, addressType byte) (string, bool) {
	var size int
	switch addressType {
	case 1:
		size = 4
	case 4:
		size = 16
	case 3:
		length := []byte{0}
		if _, err := io.ReadFull(connection, length); err != nil {
			return "", false
		}
		size = int(length[0])
	default:
		return "", false
	}
	address := make([]byte, size)
	if _, err := io.ReadFull(connection, address); err != nil {
		return "", false
	}
	if addressType == 3 {
		return string(address), true
	}
	return net.IP(address).String(), true
}

func stringPort(port []byte) string {
	value := uint16(port[0])<<8 | uint16(port[1])
	return fmt.Sprint(value)
}

func (p *SSHProcess) Wait() error            { <-p.done; return nil }
func (p *SSHProcess) Signal(os.Signal) error { return p.Kill() }
func (p *SSHProcess) Kill() error {
	var err error
	p.once.Do(func() {
		close(p.done)
		err = p.listener.Close()
	})
	return err
}
