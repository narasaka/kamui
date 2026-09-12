//go:build linux

package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/narasaka/kamui/internal/controller"
	"github.com/narasaka/kamui/internal/state"
)

func TestCommandAndControllerShareDefaultXDGRuntime(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "config"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(root, "state"))
	t.Setenv("XDG_RUNTIME_DIR", filepath.Join(root, "runtime"))

	executable := filepath.Join(root, "kamui")
	build := exec.Command("go", "build", "-o", executable, ".")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build kamui: %v: %s", err, output)
	}
	command := exec.Command(executable, "status")
	null, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = null.Close() }()
	command.Stdin = null
	command.Stdout = null
	command.Stderr = null
	if err := command.Run(); err != nil {
		t.Fatalf("kamui status: %v", err)
	}

	layout, err := state.DefaultLayout()
	if err != nil {
		t.Fatal(err)
	}
	client := controller.Client{Layout: layout}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if identity, err := client.Identity(ctx); err == nil {
			_ = client.Shutdown(ctx, identity)
		}
		legacyLayout := state.NewLayout(layout.Root)
		legacyClient := controller.Client{Layout: legacyLayout}
		if identity, err := legacyClient.Identity(ctx); err == nil {
			_ = legacyClient.Shutdown(ctx, identity)
		}
	})

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := client.Identity(ctx); err != nil {
		t.Fatalf("contact controller through XDG runtime: %v", err)
	}
	if info, err := os.Stat(layout.Runtime); err != nil || info.Mode().Perm()&0o077 != 0 {
		t.Fatalf("runtime directory is not user-only: info=%v err=%v", info, err)
	}
}
