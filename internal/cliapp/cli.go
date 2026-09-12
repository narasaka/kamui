// Package cliapp adapts Kamui's command interface to urfave/cli.
package cliapp

import (
	"context"
	"fmt"
	"io"

	kamuiapp "github.com/narasaka/kamui/internal/app"
	"github.com/narasaka/kamui/internal/browser"
	"github.com/narasaka/kamui/internal/session"
	"github.com/narasaka/kamui/internal/version"
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
		Version:   version.Version,
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
			if err == nil && result.ShowSecurityWarning && streams.ErrOut != nil {
				_, err = fmt.Fprintln(streams.ErrOut, "WARNING: Remote content receives localhost origin trust in this dedicated profile; genuine Mac localhost is unavailable there.")
			}
			return err
		},
	}

	command.Commands = []*cli.Command{
		{
			Name:  "logs",
			Usage: "show OpenSSH background diagnostics",
			Flags: []cli.Flag{
				&cli.BoolFlag{Name: "follow", Aliases: []string{"f"}},
				&cli.IntFlag{Name: "lines", Aliases: []string{"n"}, Value: 100},
			},
			Action: func(ctx context.Context, cmd *cli.Command) error {
				if err := noArguments(cmd); err != nil {
					return err
				}
				if cmd.Int("lines") < 0 {
					return fmt.Errorf("logs --lines cannot be negative")
				}
				if application == nil {
					return notImplemented("logs")
				}
				return application.StreamLogs(ctx, streams.Out, cmd.Bool("follow"), cmd.Int("lines"))
			},
		},
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
					return printStatuses(streams.Out, result.Sessions, cmd.Bool("verbose"))
				}
				return printStatuses(streams.Out, []session.SessionStatus{result.Session}, cmd.Bool("verbose"))
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
			Action: func(ctx context.Context, cmd *cli.Command) error {
				if err := exactlyOneDestination(cmd); err != nil {
					return err
				}
				if application == nil {
					return notImplemented("doctor")
				}
				result, err := application.Execute(ctx, kamuiapp.Request{
					Operation: kamuiapp.Doctor, Destination: cmd.Args().First(), JSON: cmd.Bool("json"),
				})
				if err != nil {
					return err
				}
				_, err = io.WriteString(streams.Out, result.Output)
				return err
			},
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
					Operation: kamuiapp.SSHHook, Destination: cmd.Args().First(),
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

func printStatuses(writer io.Writer, statuses []session.SessionStatus, verbose bool) error {
	header := "DESTINATION\tSTATE\tBROWSER\tPROXY\tSSH"
	if verbose {
		header += "\tLAST ERROR"
	}
	if _, err := fmt.Fprintln(writer, header); err != nil {
		return err
	}
	for _, status := range statuses {
		line := fmt.Sprintf("%s\t%s\t%s\t%s\t%s",
			status.Destination, sessionState(status.State), status.Browser, status.Proxy, sshState(status.State))
		if verbose {
			lastError := ""
			if status.LastError != nil {
				lastError = status.LastError.Error()
			}
			line += "\t" + lastError
		}
		if _, err := fmt.Fprintln(writer, line); err != nil {
			return err
		}
	}
	return nil
}

func sshState(state session.SessionState) string {
	if state == session.SessionConnected {
		return "healthy"
	}
	return sessionState(state)
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
