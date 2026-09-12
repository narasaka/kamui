package ssh

import (
	"context"
	"os/exec"
	"strings"
)

// Diagnostics performs read-only checks against the system OpenSSH client.
type Diagnostics struct {
	Path string
}

// Executable resolves the OpenSSH executable used by diagnostics.
func (d Diagnostics) Executable() (string, error) {
	return resolveExecutable(d.Path)
}

func resolveExecutable(configured string) (string, error) {
	if configured != "" {
		return configured, nil
	}
	return exec.LookPath("ssh")
}

// Connectivity checks whether OpenSSH can authenticate without prompting.
func (d Diagnostics) Connectivity(ctx context.Context, path, destination string) (string, error) {
	command := exec.CommandContext(ctx, path,
		"-T", "-o", "BatchMode=yes", "-o", "ConnectTimeout=3",
		"-o", "PermitLocalCommand=no", destination, "exit")
	output, err := command.CombinedOutput()
	return strings.TrimSpace(string(output)), err
}

// AgentForwarding reports the effective forwarding policy for a destination.
func (d Diagnostics) AgentForwarding(ctx context.Context, path, destination string) (bool, error) {
	return (SystemConfigInspector{}).AgentForwarding(ctx, path, destination)
}
