//go:build darwin

package main

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/narasaka/kamui/internal/controller"
	"github.com/narasaka/kamui/internal/state"
)

func TestCommandAndControllerShareDefaultMacOSRuntime(t *testing.T) {
	home, err := os.MkdirTemp("/tmp", "kamui-darwin-home-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(home) })
	executable := filepath.Join(t.TempDir(), "kamui")
	build := exec.Command("go", "build", "-o", executable, ".")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build kamui: %v: %s", err, output)
	}
	t.Setenv("HOME", home)

	layout, err := state.DefaultLayout()
	if err != nil {
		t.Fatal(err)
	}
	client := controller.Client{Layout: layout}
	controllerLayout := state.NewLayoutWithRuntime(layout.Root, layout.Runtime)
	controllerClient := controller.Client{Layout: controllerLayout}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		for _, candidate := range []controller.Client{client, controllerClient} {
			if identity, err := candidate.Identity(ctx); err == nil {
				_ = candidate.Shutdown(ctx, identity)
			}
		}
	})

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	// Status and stop never start a controller. Open does, and its missing
	// session error can only come from the controller it started.
	command := exec.CommandContext(ctx, executable, "open", "kamui-runtime-test")
	null, err := os.OpenFile(os.DevNull, os.O_RDONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = null.Close() }()
	output, err := os.CreateTemp(t.TempDir(), "kamui-output-*")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = output.Close() }()
	command.Stdin = null
	command.Stdout = output
	command.Stderr = output
	err = command.Run()
	_ = output.Sync()
	diagnostics, _ := os.ReadFile(output.Name())
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		t.Fatalf("kamui open did not contact its newly started controller within 3 seconds: %s", diagnostics)
	}
	if err == nil || !strings.Contains(string(diagnostics), "no Kamui session for kamui-runtime-test") {
		t.Fatalf("kamui open: %v: %s; want the controller's missing-session error", err, diagnostics)
	}

	probeCtx, probeCancel := context.WithTimeout(context.Background(), time.Second)
	defer probeCancel()
	if _, err := client.Identity(probeCtx); err != nil {
		t.Fatalf("contact controller through the default macOS layout: %v", err)
	}
}
