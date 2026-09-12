// Package app is the presentation-neutral command gateway for Kamui.
package app

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/narasaka/kamui/internal/browser"
	"github.com/narasaka/kamui/internal/config"
	"github.com/narasaka/kamui/internal/controller"
	"github.com/narasaka/kamui/internal/session"
	"github.com/narasaka/kamui/internal/state"
)

// Operation is a user-visible Kamui command.
type Operation uint8

const (
	Ensure Operation = iota
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
	URLs        []string
	All         bool
	JSON        bool
	Verbose     bool
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
}

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
		return Result{Output: fmt.Sprintf("Host %s\n    PermitLocalCommand yes\n    LocalCommand kamui ssh-hook %%n\n", destination)}, nil
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
	if request.Operation == Ensure || request.Operation == SSHHook {
		var overrides config.Overrides
		if selection.Explicit != "" {
			overrides.Browser = &selection.Explicit
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
		idleTimeout = effective.IdleTimeout
		stopBrowserOnStop = effective.StopBrowserOnStop
	}
	command := session.Command{
		Operation: operation, Destination: destination, Browser: selection, URLs: request.URLs,
		SkipBrowser:       skipBrowser,
		IdleTimeout:       idleTimeout,
		StopBrowserOnStop: stopBrowserOnStop,
		Unattended:        request.Operation == SSHHook,
	}
	call := func() (session.Result, error) {
		if request.Operation == SSHHook {
			return session.Result{}, a.client.ExecuteAsync(ctx, command)
		}
		return a.client.Execute(ctx, command)
	}
	result, err := call()
	if err == nil {
		if request.Operation == Ensure && result.Session.Browser != "" {
			if err := a.layout.RememberBrowser(destination, result.Session.Browser); err != nil {
				return Result{}, fmt.Errorf("remember browser selection: %w", err)
			}
		}
		showWarning := false
		if request.Operation == Ensure {
			showWarning, err = a.layout.TakeFirstRunWarning()
			if err != nil {
				return Result{}, err
			}
		}
		return Result{Result: result, ShowSecurityWarning: showWarning}, nil
	}
	if !controllerUnavailable(err) {
		return Result{}, err
	}
	if err := a.starter.Start(ctx, a.layout); err != nil && !strings.Contains(err.Error(), "another Kamui controller") {
		return Result{}, fmt.Errorf("start Kamui controller: %w", err)
	}

	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	for {
		result, err = call()
		if err == nil {
			if request.Operation == Ensure && result.Session.Browser != "" {
				if err := a.layout.RememberBrowser(destination, result.Session.Browser); err != nil {
					return Result{}, fmt.Errorf("remember browser selection: %w", err)
				}
			}
			showWarning := false
			if request.Operation == Ensure {
				showWarning, err = a.layout.TakeFirstRunWarning()
				if err != nil {
					return Result{}, err
				}
			}
			return Result{Result: result, ShowSecurityWarning: showWarning}, nil
		}
		if !controllerUnavailable(err) {
			return Result{}, err
		}
		select {
		case <-ctx.Done():
			return Result{}, ctx.Err()
		case <-deadline.C:
			return Result{}, fmt.Errorf("Kamui controller did not become ready: %w", err)
		case <-ticker.C:
		}
	}
}

func sessionOperation(operation Operation, all bool) (session.Operation, error) {
	switch operation {
	case Ensure, SSHHook:
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
	text := err.Error()
	return strings.Contains(text, "read controller token") || strings.Contains(text, "contact Kamui controller")
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
	command := exec.Command(executable, "__controller", "--state-root", layout.Root)
	command.Stdin = s.Stdin
	command.Stdout = s.Stdout
	command.Stderr = s.Stderr
	if err := command.Start(); err != nil {
		return err
	}
	return command.Process.Release()
}
