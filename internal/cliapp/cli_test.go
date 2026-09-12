package cliapp_test

import (
	"bytes"
	"context"
	"fmt"
	"testing"

	"github.com/narasaka/kamui/internal/cliapp"
)

func TestPlannedCommandsReturnExplicitNotImplementedErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "primary", args: []string{"kamui", "reyna"}, want: "ensure is not implemented"},
		{name: "status", args: []string{"kamui", "status", "reyna"}, want: "status is not implemented"},
		{name: "stop destination", args: []string{"kamui", "stop", "reyna"}, want: "stop is not implemented"},
		{name: "stop all", args: []string{"kamui", "stop", "--all"}, want: "stop is not implemented"},
		{name: "open", args: []string{"kamui", "open", "reyna", "http://localhost:3000"}, want: "open is not implemented"},
		{name: "doctor", args: []string{"kamui", "doctor", "reyna"}, want: "doctor is not implemented"},
		{name: "browsers", args: []string{"kamui", "browsers"}, want: "browsers is not implemented"},
		{name: "ssh hook", args: []string{"kamui", "ssh-hook", "reyna"}, want: "ssh-hook is not implemented"},
		{name: "SSH config", args: []string{"kamui", "print-ssh-config", "reyna"}, want: "print-ssh-config is not implemented"},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			var stdout, stderr bytes.Buffer
			command := cliapp.NewCommand(cliapp.Streams{Out: &stdout, ErrOut: &stderr})
			err := command.Run(context.Background(), test.args)
			if got := fmt.Sprint(err); got != test.want {
				t.Fatalf("Run(%q) error = %q, want %q", test.args, got, test.want)
			}
		})
	}
}

func TestVersionFlagReportsBuildVersion(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	command := cliapp.NewCommand(cliapp.Streams{Out: &stdout, ErrOut: &stdout})
	if err := command.Run(context.Background(), []string{"kamui", "--version"}); err != nil {
		t.Fatal(err)
	}
	if got, want := stdout.String(), "kamui version dev\n"; got != want {
		t.Fatalf("version output = %q, want %q", got, want)
	}
}
