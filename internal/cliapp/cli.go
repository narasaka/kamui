// Package cliapp adapts Kamui's command interface to urfave/cli.
package cliapp

import (
	"context"
	"fmt"
	"io"
	"strings"

	kamuiapp "github.com/narasaka/kamui/internal/app"
	"github.com/narasaka/kamui/internal/browser"
	"github.com/narasaka/kamui/internal/mirror"
	"github.com/narasaka/kamui/internal/proxy"
	"github.com/narasaka/kamui/internal/session"
	"github.com/narasaka/kamui/internal/version"
	cli "github.com/urfave/cli/v3"
)

func init() {
	cli.VersionPrinter = func(cmd *cli.Command) {
		value := cmd.Version
		if value != "dev" && !strings.HasPrefix(value, "v") {
			value = "v" + value
		}
		_, _ = fmt.Fprintln(cmd.Root().Writer, value)
	}
}

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
		Usage:     "use a remote SSH host's loopback services on this machine",
		ArgsUsage: "SSH_DESTINATION",
		Reader:    streams.In,
		Writer:    streams.Out,
		ErrWriter: streams.ErrOut,
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "browser"},
			&cli.StringFlag{Name: "browser-family"},
			&cli.StringFlag{Name: "browser-loopback", Usage: "route dedicated-browser loopback using remote-only or local-first"},
			&cli.StringFlag{Name: "loopback", Usage: "deprecated alias for --browser-loopback"},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			if cmd.NArg() == 0 {
				return cli.ShowRootCommandHelp(cmd)
			}
			if err := exactlyOneDestination(cmd); err != nil {
				return err
			}
			destination, err := session.ParseDestination(cmd.Args().First())
			if err != nil {
				return err
			}
			if application == nil {
				if _, err := browserLoopbackValue(cmd, streams.ErrOut); err != nil {
					return err
				}
				return notImplemented("ensure")
			}
			loopback, err := browserLoopbackValue(cmd, streams.ErrOut)
			if err != nil {
				return err
			}
			result, err := application.Execute(ctx, kamuiapp.Request{
				Operation:   kamuiapp.Ensure,
				Destination: destination.String(),
				Browser:     browser.Selection{Explicit: cmd.String("browser"), Family: cmd.String("browser-family")},
				Loopback:    loopback,
			})
			if err != nil {
				return err
			}
			_, err = fmt.Fprintf(streams.Out, "%s connected; proxy %s; browser %s\n", destination, result.Session.Proxy, result.Session.Browser)
			if err == nil && streams.ErrOut != nil && (result.ShowSecurityWarning || result.Session.LoopbackMode == proxy.LocalFirst) {
				warning := "WARNING: Remote content receives localhost origin trust in this dedicated profile; genuine local-machine localhost is unavailable there."
				if result.Session.LoopbackMode == proxy.LocalFirst {
					warning = "WARNING: Remote content receives localhost origin trust and may access genuine local-machine localhost services in local-first mode."
				}
				_, err = fmt.Fprintln(streams.ErrOut, warning)
			}
			return err
		},
	}

	command.Commands = []*cli.Command{
		{
			Name:      "mirror",
			Usage:     "mirror remote TCP listeners on local loopback",
			ArgsUsage: "SSH_DESTINATION",
			Action: func(ctx context.Context, cmd *cli.Command) error {
				if err := exactlyOneDestination(cmd); err != nil {
					return err
				}
				if application == nil {
					return notImplemented("mirror")
				}
				result, err := application.Execute(ctx, kamuiapp.Request{
					Operation: kamuiapp.Mirror, Destination: cmd.Args().First(),
				})
				if err != nil {
					return err
				}
				_, err = fmt.Fprintf(streams.Out, "%s mirroring enabled; mirrored %s; conflicts %s\n",
					cmd.Args().First(), formatPorts(result.Session.Mirror.MirroredPorts), formatConflicts(result.Session.Mirror.ConflictedPorts))
				return err
			},
		},
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
			Usage:     "show session status",
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
			Usage:     "stop one or all sessions",
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
				if cmd.NArg() == 0 {
					if application == nil {
						return notImplemented("status")
					}
					result, err := application.Execute(ctx, kamuiapp.Request{Operation: kamuiapp.Status})
					if err != nil {
						return fmt.Errorf("list sessions available to stop: %w", err)
					}
					if len(result.Sessions) == 0 {
						return fmt.Errorf("no sessions to stop")
					}
					var message strings.Builder
					message.WriteString("Choose a session to stop:\n")
					for _, status := range result.Sessions {
						fmt.Fprintf(&message, "\n  %s  %s", status.Destination, sessionState(status.State))
					}
					message.WriteString("\n\nRun `kamui stop SSH_DESTINATION` or `kamui stop --all`.")
					return fmt.Errorf("%s", message.String())
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
			Usage:     "open URLs in a session browser",
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
			Usage:     "diagnose SSH connectivity",
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
		{Name: "browsers", Usage: "list detected supported browsers", Action: func(ctx context.Context, cmd *cli.Command) error {
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
			Usage:     "activate a session from OpenSSH",
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
			Usage:     "print an OpenSSH LocalCommand snippet",
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
	header := "DESTINATION\tSTATE\tBROWSER\tPROXY\tSSH\tMIRROR\tMIRRORED\tCONFLICTS\tMIRROR ERROR"
	if verbose {
		header += "\tLAST ERROR"
	}
	if _, err := fmt.Fprintln(writer, header); err != nil {
		return err
	}
	for _, status := range statuses {
		mirrorState := "disabled"
		if status.Mirror.Enabled {
			mirrorState = "enabled"
		}
		mirrorError := ""
		if status.Mirror.LastError != nil {
			mirrorError = status.Mirror.LastError.Error()
		}
		line := fmt.Sprintf("%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s",
			status.Destination, sessionState(status.State), status.Browser, status.Proxy, sshState(status.State),
			mirrorState, formatPorts(status.Mirror.MirroredPorts), formatConflicts(status.Mirror.ConflictedPorts), mirrorError)
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

func browserLoopbackValue(cmd *cli.Command, notices io.Writer) (string, error) {
	if cmd.IsSet("browser-loopback") && cmd.IsSet("loopback") {
		return "", fmt.Errorf("use either --browser-loopback or deprecated --loopback, not both")
	}
	if cmd.IsSet("loopback") {
		if notices != nil {
			_, _ = fmt.Fprintln(notices, "NOTICE: --loopback is deprecated; use --browser-loopback (dedicated browser only).")
		}
		return cmd.String("loopback"), nil
	}
	return cmd.String("browser-loopback"), nil
}

func formatPorts(ports []uint16) string {
	if len(ports) == 0 {
		return "-"
	}
	values := make([]string, 0, len(ports))
	for _, port := range ports {
		values = append(values, fmt.Sprint(port))
	}
	return strings.Join(values, ",")
}

func formatConflicts(conflicts []mirror.Conflict) string {
	if len(conflicts) == 0 {
		return "-"
	}
	values := make([]string, 0, len(conflicts))
	for _, conflict := range conflicts {
		values = append(values, fmt.Sprint(conflict.Port))
	}
	return strings.Join(values, ",")
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
