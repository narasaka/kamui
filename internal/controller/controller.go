// Package controller exposes the session manager over a user-owned Unix socket.
package controller

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/narasaka/kamui/internal/browser"
	"github.com/narasaka/kamui/internal/session"
	"github.com/narasaka/kamui/internal/state"
)

const maximumMessageSize = 1 << 20

// Client sends lifecycle commands to a running controller.
type Client struct {
	Layout state.Layout
}

// Execute performs one command through the controller socket.
func (c Client) Execute(ctx context.Context, command session.Command) (session.Result, error) {
	return c.execute(ctx, command, false)
}

// ExecuteAsync asks the controller to accept a command without waiting for its
// lifecycle work to finish.
func (c Client) ExecuteAsync(ctx context.Context, command session.Command) error {
	_, err := c.execute(ctx, command, true)
	return err
}

func (c Client) execute(ctx context.Context, command session.Command, async bool) (session.Result, error) {
	token, err := os.ReadFile(c.Layout.Token)
	if err != nil {
		return session.Result{}, fmt.Errorf("read controller token: %w", err)
	}
	connection, err := (&net.Dialer{}).DialContext(ctx, "unix", c.Layout.Socket)
	if err != nil {
		return session.Result{}, fmt.Errorf("contact Kamui controller: %w", err)
	}
	defer connection.Close()
	request := wireRequest{
		Token:             string(token),
		Operation:         command.Operation,
		Destination:       command.Destination.String(),
		Browser:           command.Browser,
		URLs:              command.URLs,
		Async:             async,
		SkipBrowser:       command.SkipBrowser,
		IdleTimeout:       command.IdleTimeout,
		StopBrowserOnStop: command.StopBrowserOnStop,
		Unattended:        command.Unattended,
	}
	if err := json.NewEncoder(connection).Encode(request); err != nil {
		return session.Result{}, fmt.Errorf("send controller command: %w", err)
	}
	var response wireResponse
	if err := json.NewDecoder(io.LimitReader(connection, maximumMessageSize)).Decode(&response); err != nil {
		return session.Result{}, fmt.Errorf("read controller response: %w", err)
	}
	if response.Error != "" {
		return session.Result{}, errors.New(response.Error)
	}
	return response.Result.sessionResult()
}

// Server is one controller listener holding the advisory controller lock.
type Server struct {
	layout     state.Layout
	listener   net.Listener
	lock       *os.File
	token      string
	manager    *session.Manager
	ctx        context.Context
	cancel     context.CancelFunc
	closeOnce  sync.Once
	closeError error
	handlers   sync.WaitGroup
	done       chan struct{}
}

// Start acquires the controller lock and begins accepting requests.
func Start(ctx context.Context, layout state.Layout, manager *session.Manager) (*Server, error) {
	if err := layout.Ensure(); err != nil {
		return nil, err
	}
	lock, err := os.OpenFile(layout.Lock, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open controller lock: %w", err)
	}
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		lock.Close()
		return nil, fmt.Errorf("another Kamui controller owns %s: %w", layout.Lock, err)
	}
	if err := os.Remove(layout.Socket); err != nil && !os.IsNotExist(err) {
		_ = syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
		lock.Close()
		return nil, fmt.Errorf("remove stale controller socket: %w", err)
	}
	listener, err := net.Listen("unix", layout.Socket)
	if err != nil {
		_ = syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
		lock.Close()
		return nil, fmt.Errorf("listen on controller socket: %w", err)
	}
	if err := os.Chmod(layout.Socket, 0o600); err != nil {
		listener.Close()
		_ = syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
		lock.Close()
		return nil, fmt.Errorf("protect controller socket: %w", err)
	}
	token, err := writeToken(layout.Token)
	if err != nil {
		listener.Close()
		_ = syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
		lock.Close()
		return nil, err
	}
	serverContext, cancel := context.WithCancel(ctx)
	server := &Server{
		layout: layout, listener: listener, lock: lock, token: token,
		manager: manager, ctx: serverContext, cancel: cancel, done: make(chan struct{}),
	}
	go server.accept()
	go func() {
		<-serverContext.Done()
		_ = server.Close()
	}()
	return server, nil
}

func (s *Server) accept() {
	defer close(s.done)
	for {
		connection, err := s.listener.Accept()
		if err != nil {
			return
		}
		s.handlers.Add(1)
		go func() {
			defer s.handlers.Done()
			defer connection.Close()
			s.handle(connection)
		}()
	}
}

func (s *Server) handle(connection net.Conn) {
	var request wireRequest
	if err := json.NewDecoder(io.LimitReader(connection, maximumMessageSize)).Decode(&request); err != nil {
		_ = json.NewEncoder(connection).Encode(wireResponse{Error: "malformed controller request"})
		return
	}
	if subtle.ConstantTimeCompare([]byte(request.Token), []byte(s.token)) != 1 {
		_ = json.NewEncoder(connection).Encode(wireResponse{Error: "controller authentication failed"})
		return
	}
	var destination session.Destination
	var err error
	if request.Destination != "" {
		destination, err = session.ParseDestination(request.Destination)
		if err != nil {
			_ = json.NewEncoder(connection).Encode(wireResponse{Error: err.Error()})
			return
		}
	} else if request.Operation != session.Status && request.Operation != session.StopAll {
		_ = json.NewEncoder(connection).Encode(wireResponse{Error: "SSH destination is required"})
		return
	}
	command := session.Command{
		Operation: request.Operation, Destination: destination, Browser: request.Browser, URLs: request.URLs,
		SkipBrowser:       request.SkipBrowser,
		IdleTimeout:       request.IdleTimeout,
		StopBrowserOnStop: request.StopBrowserOnStop,
		Unattended:        request.Unattended,
	}
	if request.Async {
		_ = json.NewEncoder(connection).Encode(wireResponse{})
		go func() { _, _ = s.manager.Execute(s.ctx, command) }()
		return
	}
	result, err := s.manager.Execute(s.ctx, command)
	if err != nil {
		_ = json.NewEncoder(connection).Encode(wireResponse{Error: err.Error()})
		return
	}
	_ = json.NewEncoder(connection).Encode(wireResponse{Result: makeWireResult(result)})
}

// Close stops the listener and releases the controller lock.
func (s *Server) Close() error {
	s.closeOnce.Do(func() {
		s.cancel()
		s.closeError = s.listener.Close()
		<-s.done
		s.handlers.Wait()
		if err := os.Remove(s.layout.Socket); err != nil && !os.IsNotExist(err) {
			s.closeError = errors.Join(s.closeError, err)
		}
		if err := os.Remove(s.layout.Token); err != nil && !os.IsNotExist(err) {
			s.closeError = errors.Join(s.closeError, err)
		}
		s.closeError = errors.Join(s.closeError, syscall.Flock(int(s.lock.Fd()), syscall.LOCK_UN), s.lock.Close())
	})
	if errors.Is(s.closeError, net.ErrClosed) {
		return nil
	}
	return s.closeError
}

func writeToken(path string) (string, error) {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", fmt.Errorf("generate controller token: %w", err)
	}
	token := hex.EncodeToString(bytes)
	temporary, err := os.CreateTemp(filepath.Dir(path), "controller-token-*")
	if err != nil {
		return "", fmt.Errorf("create controller token: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return "", err
	}
	if _, err := temporary.WriteString(token); err != nil {
		temporary.Close()
		return "", err
	}
	if err := temporary.Close(); err != nil {
		return "", err
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return "", fmt.Errorf("install controller token: %w", err)
	}
	return token, nil
}

type wireRequest struct {
	Token             string            `json:"token"`
	Operation         session.Operation `json:"operation"`
	Destination       string            `json:"destination"`
	Browser           browser.Selection `json:"browser"`
	URLs              []string          `json:"urls,omitempty"`
	Async             bool              `json:"async,omitempty"`
	SkipBrowser       bool              `json:"skipBrowser,omitempty"`
	IdleTimeout       time.Duration     `json:"idleTimeout,omitempty"`
	StopBrowserOnStop bool              `json:"stopBrowserOnStop,omitempty"`
	Unattended        bool              `json:"unattended,omitempty"`
}

type wireStatus struct {
	Destination string               `json:"destination"`
	State       session.SessionState `json:"state"`
	Proxy       string               `json:"proxy"`
	Browser     string               `json:"browser,omitempty"`
	LastError   string               `json:"lastError,omitempty"`
}

type wireResult struct {
	Session  *wireStatus  `json:"session,omitempty"`
	Sessions []wireStatus `json:"sessions,omitempty"`
}

type wireResponse struct {
	Result wireResult `json:"result"`
	Error  string     `json:"error,omitempty"`
}

func makeWireResult(result session.Result) wireResult {
	var wire wireResult
	if result.Session.Destination.String() != "" {
		status := makeWireStatus(result.Session)
		wire.Session = &status
	}
	for _, status := range result.Sessions {
		wire.Sessions = append(wire.Sessions, makeWireStatus(status))
	}
	return wire
}

func makeWireStatus(status session.SessionStatus) wireStatus {
	wire := wireStatus{
		Destination: status.Destination.String(), State: status.State,
		Proxy: status.Proxy.String(), Browser: status.Browser,
	}
	if status.LastError != nil {
		wire.LastError = status.LastError.Error()
	}
	return wire
}

func (r wireResult) sessionResult() (session.Result, error) {
	var result session.Result
	if r.Session != nil {
		status, err := r.Session.sessionStatus()
		if err != nil {
			return session.Result{}, err
		}
		result.Session = status
	}
	for _, wire := range r.Sessions {
		status, err := wire.sessionStatus()
		if err != nil {
			return session.Result{}, err
		}
		result.Sessions = append(result.Sessions, status)
	}
	return result, nil
}

func (s wireStatus) sessionStatus() (session.SessionStatus, error) {
	destination, err := session.ParseDestination(s.Destination)
	if err != nil {
		return session.SessionStatus{}, err
	}
	proxyAddress, err := netip.ParseAddrPort(s.Proxy)
	if err != nil {
		return session.SessionStatus{}, fmt.Errorf("invalid proxy address from controller: %w", err)
	}
	status := session.SessionStatus{Destination: destination, State: s.State, Proxy: proxyAddress, Browser: s.Browser}
	if s.LastError != "" {
		status.LastError = errors.New(s.LastError)
	}
	return status, nil
}
