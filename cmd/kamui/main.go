package main

import (
	"context"
	"fmt"
	"os"

	"github.com/narasaka/kamui/internal/cliapp"
)

func main() {
	command := cliapp.NewCommand(cliapp.Streams{In: os.Stdin, Out: os.Stdout, ErrOut: os.Stderr})
	if err := command.Run(context.Background(), os.Args); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
