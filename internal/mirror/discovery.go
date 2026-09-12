package mirror

import (
	"context"
	"errors"
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

// SSHDiscoverer discovers listeners with tools already present on the remote
// Unix host. It starts a bounded subprocess per reconciliation; CommandContext
// terminates it when the mirror session stops.
type SSHDiscoverer struct {
	SSHPath string
	Runner  CommandRunner
}

const remoteListenerDiscovery = `if command -v ss >/dev/null 2>&1; then
  printf "kamui:ss\n"
  exec ss -H -ltn
elif command -v lsof >/dev/null 2>&1; then
  printf "kamui:lsof\n"
  exec lsof -nP -iTCP -sTCP:LISTEN -Fn
elif [ -x /usr/sbin/lsof ]; then
  printf "kamui:lsof\n"
  exec /usr/sbin/lsof -nP -iTCP -sTCP:LISTEN -Fn
elif command -v netstat >/dev/null 2>&1; then
  printf "kamui:netstat\n"
  exec netstat -an
elif [ -x /usr/sbin/netstat ]; then
  printf "kamui:netstat\n"
  exec /usr/sbin/netstat -an
else
  printf "Kamui requires ss, lsof, or netstat for TCP listener discovery\n" >&2
  exit 127
fi`

// ListeningPorts returns the deduplicated TCP listen ports reported by the
// remote host's available socket-inspection tool.
func (d SSHDiscoverer) ListeningPorts(ctx context.Context, destination string) ([]uint16, error) {
	path := d.SSHPath
	if path == "" {
		var err error
		path, err = exec.LookPath("ssh")
		if err != nil {
			return nil, fmt.Errorf("find OpenSSH executable: %w", err)
		}
	}
	runner := d.Runner
	if runner == nil {
		runner = execCommandRunner{}
	}
	arguments := []string{
		"-T", "-o", "BatchMode=yes", "-o", "PermitLocalCommand=no",
		"--", destination, "sh", "-c", "'" + remoteListenerDiscovery + "'",
	}
	output, err := runner.CombinedOutput(ctx, path, arguments...)
	format, body := splitDiscoveryOutput(string(output))
	var status interface{ ExitCode() int }
	if err != nil && errors.As(err, &status) && status.ExitCode() == 1 && format == "lsof" && strings.TrimSpace(body) == "" {
		return []uint16{}, nil
	}
	if err != nil {
		detail := strings.TrimSpace(string(output))
		if len(detail) > 240 {
			detail = detail[:240] + "..."
		}
		if detail != "" {
			return nil, fmt.Errorf("run remote TCP listener discovery: %w: %s", err, detail)
		}
		return nil, fmt.Errorf("run remote TCP listener discovery: %w", err)
	}
	switch format {
	case "lsof":
		return parseLsofPorts(body)
	case "netstat":
		return parseNetstatPorts(body)
	case "ss":
		return parseSSPorts(body)
	default:
		return parseSSPorts(string(output))
	}
}

func splitDiscoveryOutput(output string) (string, string) {
	lines := strings.Split(output, "\n")
	for index, marker := range lines {
		if strings.HasPrefix(marker, "kamui:") {
			return strings.TrimPrefix(marker, "kamui:"), strings.Join(lines[index+1:], "\n")
		}
	}
	return "", output
}

func parseLsofPorts(output string) ([]uint16, error) {
	return parseUniquePorts(output, func(index int, line string) (uint16, bool, error) {
		if !strings.HasPrefix(line, "n") {
			return 0, false, nil
		}
		port, err := portFromAddress(strings.TrimPrefix(line, "n"), ':')
		if err != nil {
			return 0, false, fmt.Errorf("parse remote lsof output line %d: %w", index+1, err)
		}
		return port, true, nil
	})
}

func parseNetstatPorts(output string) ([]uint16, error) {
	return parseUniquePorts(output, func(index int, line string) (uint16, bool, error) {
		fields := strings.Fields(line)
		if len(fields) < 6 || !strings.HasPrefix(strings.ToLower(fields[0]), "tcp") || !strings.EqualFold(fields[len(fields)-1], "LISTEN") {
			return 0, false, nil
		}
		port, err := portFromNetstatAddress(fields[3])
		if err != nil {
			return 0, false, fmt.Errorf("parse remote netstat output line %d: %w", index+1, err)
		}
		return port, true, nil
	})
}

func portFromNetstatAddress(address string) (uint16, error) {
	separator := byte('.')
	if strings.LastIndexByte(address, ':') > strings.LastIndexByte(address, '.') {
		separator = ':'
	}
	return portFromAddress(address, separator)
}

func parseUniquePorts(output string, parse func(int, string) (uint16, bool, error)) ([]uint16, error) {
	unique := make(map[uint16]struct{})
	for index, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		port, include, err := parse(index, line)
		if err != nil {
			return nil, err
		}
		if include {
			unique[port] = struct{}{}
		}
	}
	ports := make([]uint16, 0, len(unique))
	for port := range unique {
		ports = append(ports, port)
	}
	sort.Slice(ports, func(i, j int) bool { return ports[i] < ports[j] })
	return ports, nil
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
			return nil, fmt.Errorf("parse remote ss output line %d: expected TCP LISTEN row, got %q", index+1, line)
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
	return portFromAddress(address, ':')
}

func portFromAddress(address string, separatorByte byte) (uint16, error) {
	separator := strings.LastIndexByte(address, separatorByte)
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
