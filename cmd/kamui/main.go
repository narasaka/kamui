package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/narasaka/kamui/internal/app"
	"github.com/narasaka/kamui/internal/browser"
	"github.com/narasaka/kamui/internal/cliapp"
	"github.com/narasaka/kamui/internal/controller"
	"github.com/narasaka/kamui/internal/session"
	"github.com/narasaka/kamui/internal/ssh"
	"github.com/narasaka/kamui/internal/state"
)

func main() {
	if len(os.Args) >= 2 && os.Args[1] == "__controller" {
		if err := runController(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	root, err := state.DefaultRoot()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	layout := state.NewLayout(root)
	application := app.New(layout, app.ProcessStarter{
		Stdin: os.Stdin, Stdout: os.Stdout, Stderr: os.Stderr,
	})
	command := cliapp.NewCommandWithApplication(application, cliapp.Streams{In: os.Stdin, Out: os.Stdout, ErrOut: os.Stderr})
	if err := command.Run(context.Background(), os.Args); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func runController(arguments []string) error {
	if len(arguments) != 2 || arguments[0] != "--state-root" || arguments[1] == "" {
		return fmt.Errorf("internal controller requires --state-root PATH")
	}
	layout := state.NewLayout(arguments[1])
	if err := layout.Ensure(); err != nil {
		return err
	}
	logFile, err := os.OpenFile(layout.ControlLog, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("open controller log: %w", err)
	}
	defer logFile.Close()
	sshLogFile, err := os.OpenFile(layout.SSHLog, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("open OpenSSH log: %w", err)
	}
	defer sshLogFile.Close()
	manager := session.NewManagerWithOptions(session.ManagerOptions{
		Transport: ssh.Transport{
			Stdin: os.Stdin, Stdout: os.Stdout, Stderr: os.Stderr, BackgroundStderr: sshLogFile,
			Inspector: ssh.SystemConfigInspector{},
		},
		Browsers:       browser.NewCatalog(browser.DefaultAdapters(nil)),
		ProfileRoot:    layout.Profiles,
		Logger:         slog.New(slog.NewJSONHandler(logFile, nil)),
		ProxyAddresses: layout,
	})
	defer manager.Close()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	server, err := controller.Start(ctx, layout, manager)
	if err != nil {
		return err
	}
	select {
	case <-ctx.Done():
	case <-server.Done():
	}
	return server.Close()
}
