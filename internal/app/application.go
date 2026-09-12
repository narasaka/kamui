// Package app is the presentation-neutral command gateway for Kamui.
package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"github.com/narasaka/kamui/internal/browser"
	"github.com/narasaka/kamui/internal/config"
	"github.com/narasaka/kamui/internal/controller"
	"github.com/narasaka/kamui/internal/proxy"
	"github.com/narasaka/kamui/internal/session"
	"github.com/narasaka/kamui/internal/state"
	"github.com/narasaka/kamui/internal/version"
)

// Operation is a user-visible Kamui command.
type Operation uint8

const (
	Ensure Operation = iota
	Mirror
	Status
	Stop
	Open
	Doctor
	Browsers
	SSHHook
	PrintSSHConfig
)

// Request contains presentation-neutral command values.
type Request struct {
	Operation   Operation
	Destination string
	Browser     browser.Selection
	Loopback    string
	URLs        []string
	All         bool
	JSON        bool
}

// Result is the observable command result.
type Result struct {
	session.Result
	Output              string
	Installations       []browser.Installation
	ShowSecurityWarning bool
}

// Starter starts the absent controller at the operating-system process seam.
type Starter interface {
	Start(context.Context, state.Layout) error
}

// Application starts or contacts the controller and executes commands.
type Application struct {
	layout   state.Layout
	client   controller.Client
	starter  Starter
	config   config.Loader
	browsers *browser.Catalog
	identity controller.Identity
}

const controllerTransitionTimeout = 15 * time.Second

// New creates a command gateway for one user state root.
func New(layout state.Layout, starter Starter) *Application {
	return NewWithBrowsers(layout, starter, browser.NewCatalog(browser.DefaultAdapters(nil)))
}

// NewWithBrowsers creates a gateway with an explicit browser catalog.
func NewWithBrowsers(layout state.Layout, starter Starter, browsers *browser.Catalog) *Application {
	if starter == nil {
		starter = ProcessStarter{}
	}
	return &Application{
		layout: layout, client: controller.Client{Layout: layout}, starter: starter,
		config:   config.Loader{Path: layout.Config},
		browsers: browsers,
		identity: controller.Identity{Protocol: controller.ProtocolVersion, Build: version.BuildIdentity()},
	}
}

// Execute applies one user command.
func (a *Application) Execute(ctx context.Context, request Request) (Result, error) {
	if request.Operation == Browsers {
		installations, err := a.browsers.Detect(ctx)
		return Result{Installations: installations}, err
	}
	var destination session.Destination
	var err error
	allowsEmptyDestination := (request.Operation == Status && request.Destination == "") ||
		(request.Operation == Stop && request.All)
	if !allowsEmptyDestination {
		destination, err = session.ParseDestination(request.Destination)
		if err != nil {
			return Result{}, err
		}
	}
	if request.Operation == PrintSSHConfig {
		return Result{Output: fmt.Sprintf("# %%n preserves the original SSH alias supplied to OpenSSH.\nHost %s\n    PermitLocalCommand yes\n    LocalCommand kamui ssh-hook %%n\n", destination)}, nil
	}
	if request.Operation == Doctor {
		return a.runDoctor(ctx, destination, request.JSON)
	}
	operation, err := sessionOperation(request.Operation, request.All)
	if err != nil {
		return Result{}, err
	}
	selection := request.Browser
	skipBrowser := false
	idleTimeout := time.Duration(0)
	stopBrowserOnStop := false
	loopbackMode := proxy.RemoteOnly
	if request.Operation == Ensure || request.Operation == SSHHook || request.Operation == Mirror {
		var overrides config.Overrides
		if selection.Explicit != "" {
			overrides.Browser = &selection.Explicit
		}
		if request.Loopback != "" {
			overrides.Loopback = &request.Loopback
		}
		effective, err := a.config.Resolve(ctx, destination, overrides)
		if err != nil {
			return Result{}, err
		}
		if selection.Explicit == "" {
			selection.Host = effective.Browser
		}
		previous, err := a.layout.PreviousBrowser(destination)
		if err != nil {
			return Result{}, fmt.Errorf("read previous browser selection: %w", err)
		}
		selection.Previous = previous
		if request.Operation == SSHHook && !effective.OpenBrowserOnSSH {
			skipBrowser = true
		}
		if request.Operation == Mirror {
			skipBrowser = true
		}
		idleTimeout = effective.IdleTimeout
		stopBrowserOnStop = effective.StopBrowserOnStop
		loopbackMode = effective.LoopbackMode
	}
	command := session.Command{
		Operation: operation, Destination: destination, Browser: selection, URLs: request.URLs,
		SkipBrowser:       skipBrowser,
		IdleTimeout:       idleTimeout,
		StopBrowserOnStop: stopBrowserOnStop,
		LoopbackMode:      loopbackMode,
		Unattended:        request.Operation == SSHHook,
		EnableMirror:      request.Operation == Mirror,
	}
	call := func() (session.Result, error) {
		if request.Operation == SSHHook {
			return session.Result{}, a.client.ExecuteAsync(ctx, command)
		}
		return a.client.Execute(ctx, command)
	}
	if err := a.ensureCompatibleController(ctx); err != nil {
		return Result{}, err
	}
	result, err := call()
	if err != nil {
		return Result{}, err
	}
	return a.completeResult(destination, request.Operation, result)
}

func (a *Application) ensureCompatibleController(ctx context.Context) error {
	identity, err := a.client.Identity(ctx)
	if err == nil && identitiesMatch(identity, a.identity) {
		return nil
	}
	if err != nil && !controllerUnavailable(err) {
		return err
	}

	if err == nil {
		_ = a.logControllerRestart(identity)
		if identity.Legacy {
			err = a.client.ShutdownLegacy(ctx, identity)
		} else {
			err = a.client.Shutdown(ctx, identity)
		}
		if err != nil {
			current, probeErr := a.client.Identity(ctx)
			if probeErr == nil && identitiesMatch(current, a.identity) {
				return nil
			}
			if probeErr == nil && current.Protocol == identity.Protocol && current.Build == identity.Build && current.Legacy == identity.Legacy {
				return fmt.Errorf("stop stale Kamui controller: %w", err)
			}
			if probeErr != nil && !controllerUnavailable(probeErr) {
				return fmt.Errorf("verify stale controller shutdown: %w", probeErr)
			}
		}
		current, waitErr := a.waitForController(ctx, false)
		if waitErr == nil && identitiesMatch(current, a.identity) {
			return nil
		}
		if waitErr != nil && !controllerUnavailable(waitErr) {
			return waitErr
		}
	}

	if err := a.starter.Start(ctx, a.layout); err != nil && !strings.Contains(err.Error(), "another Kamui controller") {
		return fmt.Errorf("start Kamui controller: %w", err)
	}
	identity, err = a.waitForController(ctx, true)
	if err != nil {
		return err
	}
	if !identitiesMatch(identity, a.identity) {
		return fmt.Errorf("controller takeover produced protocol %d build %q; want protocol %d build %q",
			identity.Protocol, identity.Build, a.identity.Protocol, a.identity.Build)
	}
	return nil
}

func identitiesMatch(got, want controller.Identity) bool {
	return !got.Legacy && got.Protocol == want.Protocol && got.Build == want.Build
}

func (a *Application) waitForController(ctx context.Context, ready bool) (controller.Identity, error) {
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	deadline := time.NewTimer(controllerTransitionTimeout)
	defer deadline.Stop()
	var lastErr error
	for {
		identity, err := a.client.Identity(ctx)
		if ready && err == nil && identitiesMatch(identity, a.identity) {
			return identity, nil
		}
		if !ready && err != nil && controllerUnavailable(err) {
			return controller.Identity{}, err
		}
		if !ready && err == nil && identitiesMatch(identity, a.identity) {
			return identity, nil
		}
		lastErr = err
		select {
		case <-ctx.Done():
			return controller.Identity{}, ctx.Err()
		case <-deadline.C:
			state := "stop"
			if ready {
				state = "become ready"
			}
			if lastErr != nil {
				return controller.Identity{}, fmt.Errorf("controller for Kamui did not %s after upgrade: %w", state, lastErr)
			}
			return controller.Identity{}, fmt.Errorf("controller for Kamui did not %s after upgrade within %s", state, controllerTransitionTimeout)
		case <-ticker.C:
		}
	}
}

func (a *Application) logControllerRestart(old controller.Identity) error {
	if err := a.layout.Ensure(); err != nil {
		return err
	}
	file, err := os.OpenFile(a.layout.ControlLog, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("open controller log: %w", err)
	}
	defer func() { _ = file.Close() }()
	_, err = fmt.Fprintf(file, "{\"event\":\"controller_upgrade_restart\",\"old_protocol\":%d,\"old_build\":%q,\"new_protocol\":%d,\"new_build\":%q}\n",
		old.Protocol, old.Build, a.identity.Protocol, a.identity.Build)
	return err
}

func (a *Application) completeResult(destination session.Destination, operation Operation, result session.Result) (Result, error) {
	if operation == Ensure && result.Session.Browser != "" {
		if err := a.layout.RememberBrowser(destination, result.Session.Browser); err != nil {
			return Result{}, fmt.Errorf("remember browser selection: %w", err)
		}
	}
	showWarning := false
	if operation == Ensure {
		var err error
		showWarning, err = a.layout.TakeFirstRunWarning()
		if err != nil {
			return Result{}, err
		}
	}
	return Result{Result: result, ShowSecurityWarning: showWarning}, nil
}

func sessionOperation(operation Operation, all bool) (session.Operation, error) {
	switch operation {
	case Ensure, Mirror, SSHHook:
		return session.Ensure, nil
	case Status:
		return session.Status, nil
	case Stop:
		if all {
			return session.StopAll, nil
		}
		return session.Stop, nil
	case Open:
		return session.Open, nil
	default:
		return 0, fmt.Errorf("operation %d does not use a host session", operation)
	}
}

func controllerUnavailable(err error) bool {
	return errors.Is(err, os.ErrNotExist) ||
		errors.Is(err, io.EOF) ||
		errors.Is(err, io.ErrUnexpectedEOF) ||
		errors.Is(err, net.ErrClosed) ||
		errors.Is(err, syscall.ECONNREFUSED) ||
		errors.Is(err, syscall.ECONNRESET) ||
		errors.Is(err, syscall.EPIPE)
}

// ProcessStarter starts the hidden controller command using the current binary.
type ProcessStarter struct {
	Executable string
	Stdin      io.Reader
	Stdout     io.Writer
	Stderr     io.Writer
}

func (s ProcessStarter) Start(_ context.Context, layout state.Layout) error {
	executable := s.Executable
	if executable == "" {
		var err error
		executable, err = os.Executable()
		if err != nil {
			return err
		}
	}
	command := exec.Command(executable, "__controller", "--state-root", layout.Root, "--runtime-root", layout.Runtime)
	command.Stdin = s.Stdin
	command.Stdout = s.Stdout
	command.Stderr = s.Stderr
	if err := command.Start(); err != nil {
		return err
	}
	return command.Process.Release()
}
