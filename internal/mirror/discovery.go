package mirror

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
)

// CommandRunner is the external-process seam used by SSHDiscoverer.
type CommandRunner interface {
	Run(context.Context, string, io.Writer, io.Writer, ...string) error
}

// SSHDiscoverer discovers listeners with tools already present on the remote
// Unix host. ListeningPorts starts one bounded subprocess per call;
// WatchListeningPorts keeps one subprocess open and streams reports.
// CommandContext terminates either when the mirror session stops.
type SSHDiscoverer struct {
	SSHPath string
	Runner  CommandRunner
	Stderr  io.Writer
}

// WithDiagnostics returns a discoverer that copies SSH stderr to writer.
func (d SSHDiscoverer) WithDiagnostics(writer io.Writer) Discoverer {
	d.Stderr = writer
	return d
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
	path, err := d.sshPath()
	if err != nil {
		return nil, err
	}
	runner := d.Runner
	if runner == nil {
		runner = execCommandRunner{}
	}
	arguments := []string{
		"-T", "-o", "BatchMode=yes", "-o", "PermitLocalCommand=no",
		"--", destination, "sh", "-c", "'" + remoteListenerDiscovery + "'",
	}
	var stdout, stderr bytes.Buffer
	diagnosticStderr := io.Writer(&stderr)
	if d.Stderr != nil {
		diagnosticStderr = io.MultiWriter(&stderr, d.Stderr)
	}
	err = runner.Run(ctx, path, &stdout, diagnosticStderr, arguments...)
	output := stdout.String()
	format, body := splitDiscoveryOutput(output)
	var status interface{ ExitCode() int }
	if err != nil && errors.As(err, &status) && status.ExitCode() == 1 && format == "lsof" && strings.TrimSpace(body) == "" {
		return []uint16{}, nil
	}
	if err != nil {
		return nil, discoveryRunFailure(err, strings.Join([]string{output, stderr.String()}, "\n"))
	}
	if format == "" {
		return parseSSPorts(output)
	}
	return parseListenerOutput(format, body)
}

// remoteListenerWatch repeats discovery every second on the remote host. After
// the tool marker it writes one line per pass: a full snapshot when listeners
// changed, kamui:same when they did not, or kamui:error with the tool's exit
// status. Only lsof uses status 1 with no output to mean no listeners. The
// script contains no single quotes because it is quoted for the remote login
// shell.
const remoteListenerWatch = `if command -v ss >/dev/null 2>&1; then
  tool=ss
  list() { ss -H -ltn; }
elif command -v lsof >/dev/null 2>&1; then
  tool=lsof
  list() { lsof -nP -iTCP -sTCP:LISTEN -Fn; }
elif [ -x /usr/sbin/lsof ]; then
  tool=lsof
  list() { /usr/sbin/lsof -nP -iTCP -sTCP:LISTEN -Fn; }
elif command -v netstat >/dev/null 2>&1; then
  tool=netstat
  list() { out=$(netstat -an) || return; printf "%s\n" "$out" | grep -i listen || :; }
elif [ -x /usr/sbin/netstat ]; then
  tool=netstat
  list() { out=$(/usr/sbin/netstat -an) || return; printf "%s\n" "$out" | grep -i listen || :; }
else
  printf "Kamui requires ss, lsof, or netstat for TCP listener discovery\n" >&2
  exit 127
fi
printf "kamui:%s\n" "$tool"
previous=
reported=no
while :; do
  if current=$(list); then status=0; else status=$?; fi
  if [ "$status" -ne 0 ] && ! { [ "$tool" = lsof ] && [ "$status" -eq 1 ] && [ -z "$current" ]; }; then
    printf "kamui:error %s\n" "$status"
    reported=no
  elif [ "$reported" = yes ] && [ "$current" = "$previous" ]; then
    printf "kamui:same\n"
  else
    printf "kamui:snapshot\n%s\nkamui:end\n" "$current"
    previous=$current
    reported=yes
  fi
  sleep 1
done`

// watchStderrLimit bounds retained diagnostics from a long-lived discovery
// process to the amount reported in an error.
const watchStderrLimit = 240

// WatchListeningPorts runs one long-lived remote discovery loop and calls
// report about once per second until ctx ends or the SSH process exits. Each
// call carries the current listener set or that pass's discovery error, so
// calls also prove the stream is alive.
func (d SSHDiscoverer) WatchListeningPorts(ctx context.Context, destination string, report func([]uint16, error)) error {
	path, err := d.sshPath()
	if err != nil {
		return err
	}
	runner := d.Runner
	if runner == nil {
		runner = execCommandRunner{}
	}
	arguments := []string{
		"-T", "-o", "BatchMode=yes", "-o", "PermitLocalCommand=no",
		"--", destination, "sh", "-c", "'" + remoteListenerWatch + "'",
	}
	runContext, cancel := context.WithCancel(ctx)
	defer cancel()
	reader, writer := io.Pipe()
	streamed := make(chan error, 1)
	go func() {
		err := readListenerReports(reader, report)
		if err != nil {
			cancel()
		}
		_, _ = io.Copy(io.Discard, reader)
		streamed <- err
	}()
	stderr := &tailBuffer{limit: watchStderrLimit}
	diagnosticStderr := io.Writer(stderr)
	if d.Stderr != nil {
		diagnosticStderr = io.MultiWriter(stderr, d.Stderr)
	}
	runErr := runner.Run(runContext, path, writer, diagnosticStderr, arguments...)
	_ = writer.Close()
	if err := <-streamed; err != nil {
		return err
	}
	if runErr != nil {
		return discoveryRunFailure(runErr, string(stderr.data))
	}
	return nil
}

func readListenerReports(reader io.Reader, report func([]uint16, error)) error {
	scanner := bufio.NewScanner(reader)
	format := ""
	var snapshot []string
	inSnapshot := false
	var last []uint16
	var lastErr error
	reported := false
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		switch {
		case inSnapshot && line == "kamui:end":
			inSnapshot = false
			last, lastErr = parseListenerOutput(format, strings.Join(snapshot, "\n"))
			reported = true
			report(slices.Clone(last), lastErr)
		case inSnapshot:
			snapshot = append(snapshot, line)
		case format == "":
			// Login scripts may print before the marker; ignore them as
			// one-shot discovery does.
			if strings.HasPrefix(line, "kamui:") {
				format = strings.TrimPrefix(line, "kamui:")
			}
		case line == "kamui:snapshot":
			inSnapshot = true
			snapshot = snapshot[:0]
		case line == "kamui:same" && reported:
			report(slices.Clone(last), lastErr)
		case strings.HasPrefix(line, "kamui:error "):
			reported = false
			report(nil, fmt.Errorf("remote %s exited with status %s", format, strings.TrimPrefix(line, "kamui:error ")))
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read remote TCP listener reports: %w", err)
	}
	return nil
}

// tailBuffer keeps the most recent limit bytes written to it.
type tailBuffer struct {
	data  []byte
	limit int
}

func (b *tailBuffer) Write(p []byte) (int, error) {
	b.data = append(b.data, p...)
	if excess := len(b.data) - b.limit; excess > 0 {
		b.data = append(b.data[:0], b.data[excess:]...)
	}
	return len(p), nil
}

func (d SSHDiscoverer) sshPath() (string, error) {
	if d.SSHPath != "" {
		return d.SSHPath, nil
	}
	path, err := exec.LookPath("ssh")
	if err != nil {
		return "", fmt.Errorf("find OpenSSH executable: %w", err)
	}
	return path, nil
}

func discoveryRunFailure(err error, detail string) error {
	detail = strings.TrimSpace(detail)
	if len(detail) > 240 {
		detail = detail[:240] + "..."
	}
	if detail != "" {
		return fmt.Errorf("run remote TCP listener discovery: %w: %s", err, detail)
	}
	return fmt.Errorf("run remote TCP listener discovery: %w", err)
}

func parseListenerOutput(format, body string) ([]uint16, error) {
	switch format {
	case "lsof":
		return parseLsofPorts(body)
	case "netstat":
		return parseNetstatPorts(body)
	default:
		return parseSSPorts(body)
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

// commandWaitDelay bounds how long a canceled discovery command may leave its
// output pipes open, for example through an inherited descendant process.
const commandWaitDelay = 2 * time.Second

type execCommandRunner struct{}

func (execCommandRunner) Run(ctx context.Context, path string, stdout, stderr io.Writer, args ...string) error {
	command := exec.CommandContext(ctx, path, args...)
	command.Stdout = stdout
	command.Stderr = stderr
	command.WaitDelay = commandWaitDelay
	return command.Run()
}
