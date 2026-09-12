// Package cliapp adapts Kamui's command interface to urfave/cli.
package cliapp

import (
	"context"
	"fmt"
	"io"

	kamuiapp "github.com/narasaka/kamui/internal/app"
	"github.com/narasaka/kamui/internal/browser"
	"github.com/narasaka/kamui/internal/session"
	cli "github.com/urfave/cli/v3"
)

// Streams are the standard streams visible to a command invocation.
type Streams struct {
	In     io.Reader
	Out    io.Writer
	ErrOut io.Writer
}

// NewCommand returns Kamui's complete command grammar.
func NewCommand(streams Streams) *cli.Command {
	return NewCommandWithApplication(nil, streams)
}

// NewCommandWithApplication returns the command grammar wired to Kamui.
func NewCommandWithApplication(application *kamuiapp.Application, streams Streams) *cli.Command {
	command := &cli.Command{
		Name:      "kamui",
		Usage:     "use a remote SSH host's loopback services in a development browser",
		ArgsUsage: "SSH_DESTINATION",
		Reader:    streams.In,
		Writer:    streams.Out,
		ErrWriter: streams.ErrOut,
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "browser"},
			&cli.StringFlag{Name: "browser-family"},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			if err := exactlyOneDestination(cmd); err != nil {
				return err
			}
			destination, err := session.ParseDestination(cmd.Args().First())
			if err != nil {
				return err
			}
			if application == nil {
				return notImplemented("ensure")
			}
			result, err := application.Execute(ctx, kamuiapp.Request{
				Operation:   kamuiapp.Ensure,
				Destination: destination.String(),
				Browser:     browser.Selection{Explicit: cmd.String("browser"), Family: cmd.String("browser-family")},
			})
			if err != nil {
				return err
			}
			_, err = fmt.Fprintf(streams.Out, "%s connected; proxy %s; browser %s\n", destination, result.Session.Proxy, result.Session.Browser)
			return err
		},
	}

	command.Commands = []*cli.Command{
		{
			Name:      "status",
			ArgsUsage: "[SSH_DESTINATION]",
			Flags:     []cli.Flag{&cli.BoolFlag{Name: "verbose"}},
			Action: func(ctx context.Context, cmd *cli.Command) error {
				if err := atMostOneDestination(cmd); err != nil {
					return err
				}
				if application == nil {
					return notImplemented("status")
				}
				result, err := application.Execute(ctx, kamuiapp.Request{Operation: kamuiapp.Status, Destination: cmd.Args().First()})
				if err != nil {
					return err
				}
				if cmd.NArg() == 0 {
					return printStatuses(streams.Out, result.Sessions)
				}
				return printStatuses(streams.Out, []session.SessionStatus{result.Session})
			},
		},
		{
			Name:      "stop",
			ArgsUsage: "SSH_DESTINATION",
			Flags:     []cli.Flag{&cli.BoolFlag{Name: "all"}},
			Action: func(ctx context.Context, cmd *cli.Command) error {
				if cmd.Bool("all") {
					if cmd.NArg() != 0 {
						return fmt.Errorf("stop accepts either SSH_DESTINATION or --all, not both")
					}
					if application == nil {
						return notImplemented("stop")
					}
					_, err := application.Execute(ctx, kamuiapp.Request{Operation: kamuiapp.Stop, All: true})
					return err
				}
				if err := exactlyOneDestination(cmd); err != nil {
					return err
				}
				if application == nil {
					return notImplemented("stop")
				}
				_, err := application.Execute(ctx, kamuiapp.Request{Operation: kamuiapp.Stop, Destination: cmd.Args().First()})
				return err
			},
		},
		{
			Name:      "open",
			ArgsUsage: "SSH_DESTINATION [URL ...]",
			Action: func(ctx context.Context, cmd *cli.Command) error {
				if cmd.NArg() < 1 {
					return fmt.Errorf("open requires SSH_DESTINATION")
				}
				if _, err := session.ParseDestination(cmd.Args().First()); err != nil {
					return err
				}
				if application == nil {
					return notImplemented("open")
				}
				_, err := application.Execute(ctx, kamuiapp.Request{
					Operation: kamuiapp.Open, Destination: cmd.Args().First(), URLs: cmd.Args().Slice()[1:],
				})
				return err
			},
		},
		{
			Name:      "doctor",
			ArgsUsage: "SSH_DESTINATION",
			Flags:     []cli.Flag{&cli.BoolFlag{Name: "json"}},
			Action:    destinationAction("doctor", exactlyOneDestination),
		},
		{Name: "browsers", Action: func(ctx context.Context, cmd *cli.Command) error {
			if err := noArguments(cmd); err != nil {
				return err
			}
			if application == nil {
				return notImplemented("browsers")
			}
			result, err := application.Execute(ctx, kamuiapp.Request{Operation: kamuiapp.Browsers})
			if err != nil {
				return err
			}
			for _, installation := range result.Installations {
				if _, err := fmt.Fprintf(streams.Out, "%s\t%s\n", installation.ID, installation.Executable); err != nil {
					return err
				}
			}
			return nil
		}},
		{
			Name:      "ssh-hook",
			ArgsUsage: "SSH_DESTINATION",
			Flags:     []cli.Flag{&cli.BoolFlag{Name: "verbose"}},
			Action: func(ctx context.Context, cmd *cli.Command) error {
				if err := exactlyOneDestination(cmd); err != nil {
					return err
				}
				if application == nil {
					return notImplemented("ssh-hook")
				}
				_, err := application.Execute(ctx, kamuiapp.Request{
					Operation: kamuiapp.SSHHook, Destination: cmd.Args().First(), Verbose: cmd.Bool("verbose"),
				})
				if err == nil && cmd.Bool("verbose") {
					_, err = fmt.Fprintf(streams.Out, "Kamui activation requested for %s\n", cmd.Args().First())
				}
				return err
			},
		},
		{
			Name:      "print-ssh-config",
			ArgsUsage: "SSH_ALIAS",
			Action: func(ctx context.Context, cmd *cli.Command) error {
				if err := exactlyOneDestination(cmd); err != nil {
					return err
				}
				if application == nil {
					return notImplemented("print-ssh-config")
				}
				result, err := application.Execute(ctx, kamuiapp.Request{
					Operation: kamuiapp.PrintSSHConfig, Destination: cmd.Args().First(),
				})
				if err != nil {
					return err
				}
				_, err = io.WriteString(streams.Out, result.Output)
				return err
			},
		},
	}

	return command
}

func printStatuses(writer io.Writer, statuses []session.SessionStatus) error {
	if _, err := fmt.Fprintln(writer, "DESTINATION\tSTATE\tBROWSER\tPROXY\tSSH"); err != nil {
		return err
	}
	for _, status := range statuses {
		if _, err := fmt.Fprintf(writer, "%s\t%s\t%s\t%s\t%s\n",
			status.Destination, sessionState(status.State), status.Browser, status.Proxy, sessionState(status.State)); err != nil {
			return err
		}
	}
	return nil
}

func sessionState(state session.SessionState) string {
	switch state {
	case session.SessionConnected:
		return "connected"
	case session.SessionConnecting:
		return "connecting"
	case session.SessionAuthenticationRequired:
		return "authentication-required"
	default:
		return "unavailable"
	}
}

type argumentValidator func(*cli.Command) error

func destinationAction(operation string, validate argumentValidator) cli.ActionFunc {
	return func(_ context.Context, cmd *cli.Command) error {
		if err := validate(cmd); err != nil {
			return err
		}
		if cmd.NArg() == 1 {
			if _, err := session.ParseDestination(cmd.Args().First()); err != nil {
				return err
			}
		}
		return notImplemented(operation)
	}
}

func notImplementedAction(operation string, validate argumentValidator) cli.ActionFunc {
	return func(_ context.Context, cmd *cli.Command) error {
		if err := validate(cmd); err != nil {
			return err
		}
		return notImplemented(operation)
	}
}

func exactlyOneDestination(cmd *cli.Command) error {
	if cmd.NArg() != 1 {
		return fmt.Errorf("%s requires exactly one SSH_DESTINATION", cmd.Name)
	}
	return nil
}

func atMostOneDestination(cmd *cli.Command) error {
	if cmd.NArg() > 1 {
		return fmt.Errorf("%s accepts at most one SSH_DESTINATION", cmd.Name)
	}
	return nil
}

func noArguments(cmd *cli.Command) error {
	if cmd.NArg() != 0 {
		return fmt.Errorf("%s does not accept arguments", cmd.Name)
	}
	return nil
}

func notImplemented(operation string) error {
	return fmt.Errorf("%s is not implemented", operation)
}
