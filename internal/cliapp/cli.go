// Package cliapp adapts Kamui's command interface to urfave/cli.
package cliapp

import (
	"context"
	"fmt"
	"io"

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
		Action: destinationAction("ensure", exactlyOneDestination),
	}

	command.Commands = []*cli.Command{
		{
			Name:      "status",
			ArgsUsage: "[SSH_DESTINATION]",
			Flags:     []cli.Flag{&cli.BoolFlag{Name: "verbose"}},
			Action:    notImplementedAction("status", atMostOneDestination),
		},
		{
			Name:      "stop",
			ArgsUsage: "SSH_DESTINATION",
			Flags:     []cli.Flag{&cli.BoolFlag{Name: "all"}},
			Action: func(_ context.Context, cmd *cli.Command) error {
				if cmd.Bool("all") {
					if cmd.NArg() != 0 {
						return fmt.Errorf("stop accepts either SSH_DESTINATION or --all, not both")
					}
					return notImplemented("stop")
				}
				if err := exactlyOneDestination(cmd); err != nil {
					return err
				}
				return notImplemented("stop")
			},
		},
		{
			Name:      "open",
			ArgsUsage: "SSH_DESTINATION [URL ...]",
			Action: func(_ context.Context, cmd *cli.Command) error {
				if cmd.NArg() < 1 {
					return fmt.Errorf("open requires SSH_DESTINATION")
				}
				if _, err := session.ParseDestination(cmd.Args().First()); err != nil {
					return err
				}
				return notImplemented("open")
			},
		},
		{
			Name:      "doctor",
			ArgsUsage: "SSH_DESTINATION",
			Flags:     []cli.Flag{&cli.BoolFlag{Name: "json"}},
			Action:    destinationAction("doctor", exactlyOneDestination),
		},
		{Name: "browsers", Action: notImplementedAction("browsers", noArguments)},
		{
			Name:      "ssh-hook",
			ArgsUsage: "SSH_DESTINATION",
			Flags:     []cli.Flag{&cli.BoolFlag{Name: "verbose"}},
			Action:    destinationAction("ssh-hook", exactlyOneDestination),
		},
		{
			Name:      "print-ssh-config",
			ArgsUsage: "SSH_ALIAS",
			Action:    destinationAction("print-ssh-config", exactlyOneDestination),
		},
	}

	return command
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
