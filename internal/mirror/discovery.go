package mirror

import (
	"context"
	"fmt"
	"os/exec"
	"sort"
	"strconv"
	"strings"
)

// CommandRunner is the external-process seam used by SSHDiscoverer.
type CommandRunner interface {
	CombinedOutput(context.Context, string, ...string) ([]byte, error)
}

// SSHDiscoverer runs Linux ss through the system OpenSSH client. It starts a
// bounded subprocess per reconciliation; CommandContext terminates it when the
// mirror session stops.
type SSHDiscoverer struct {
	SSHPath string
	Runner  CommandRunner
}

// ListeningPorts returns the deduplicated TCP listen ports reported by ss.
func (d SSHDiscoverer) ListeningPorts(ctx context.Context, destination string) ([]uint16, error) {
	path := d.SSHPath
	if path == "" {
		path = "/usr/bin/ssh"
	}
	runner := d.Runner
	if runner == nil {
		runner = execCommandRunner{}
	}
	arguments := []string{
		"-T", "-o", "BatchMode=yes", "-o", "PermitLocalCommand=no",
		"--", destination, "ss", "-H", "-ltn",
	}
	output, err := runner.CombinedOutput(ctx, path, arguments...)
	if err != nil {
		detail := strings.TrimSpace(string(output))
		if len(detail) > 240 {
			detail = detail[:240] + "..."
		}
		if detail != "" {
			return nil, fmt.Errorf("run remote ss TCP listener discovery: %w: %s", err, detail)
		}
		return nil, fmt.Errorf("run remote ss TCP listener discovery: %w", err)
	}
	return parseSSPorts(string(output))
}

func parseSSPorts(output string) ([]uint16, error) {
	unique := make(map[uint16]struct{})
	for index, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 5 || !strings.EqualFold(fields[0], "LISTEN") {
			return nil, fmt.Errorf("parse remote ss output line %d: expected TCP LISTEN row", index+1)
		}
		port, err := portFromSSAddress(fields[len(fields)-2])
		if err != nil {
			return nil, fmt.Errorf("parse remote ss output line %d: %w", index+1, err)
		}
		unique[port] = struct{}{}
	}
	ports := make([]uint16, 0, len(unique))
	for port := range unique {
		ports = append(ports, port)
	}
	sort.Slice(ports, func(i, j int) bool { return ports[i] < ports[j] })
	return ports, nil
}

func portFromSSAddress(address string) (uint16, error) {
	separator := strings.LastIndexByte(address, ':')
	if separator < 0 || separator == len(address)-1 {
		return 0, fmt.Errorf("invalid local address %q", address)
	}
	value, err := strconv.ParseUint(address[separator+1:], 10, 16)
	if err != nil || value == 0 {
		return 0, fmt.Errorf("invalid TCP port in local address %q", address)
	}
	return uint16(value), nil
}

type execCommandRunner struct{}

func (execCommandRunner) CombinedOutput(ctx context.Context, path string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, path, args...).CombinedOutput()
}
