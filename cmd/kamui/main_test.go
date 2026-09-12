package main

import (
	"bytes"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestVersionFlagsPrintOnlyReleaseVersion(t *testing.T) {
	executable := filepath.Join(t.TempDir(), "kamui")
	build := exec.Command("go", "build",
		"-ldflags=-X github.com/narasaka/kamui/internal/version.Version=0.0.1",
		"-o", executable,
		".",
	)
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build kamui: %v\n%s", err, output)
	}

	for _, flag := range []string{"-v", "--version"} {
		t.Run(flag, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			command := exec.Command(executable, flag)
			command.Stdout = &stdout
			command.Stderr = &stderr
			if err := command.Run(); err != nil {
				t.Fatal(err)
			}
			if got, want := stdout.String(), "v0.0.1\n"; got != want {
				t.Fatalf("stdout = %q, want %q", got, want)
			}
			if got := stderr.String(); got != "" {
				t.Fatalf("stderr = %q, want empty", got)
			}
		})
	}
}
