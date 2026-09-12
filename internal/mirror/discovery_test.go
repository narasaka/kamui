package mirror_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/narasaka/kamui/internal/mirror"
)

func TestSSHDiscovererParsesAndSortsSSListeningPorts(t *testing.T) {
	t.Parallel()

	runner := &recordingRunner{output: []byte("LISTEN 0 4096 127.0.0.1:3001 0.0.0.0:*\nLISTEN 0 128 [::]:3000 [::]:*\nLISTEN 0 128 *:3001 *:*\n")}
	discoverer := mirror.SSHDiscoverer{SSHPath: "/usr/bin/ssh", Runner: runner}
	ports, err := discoverer.ListeningPorts(context.Background(), "reyna")
	if err != nil {
		t.Fatal(err)
	}
	if want := []uint16{3000, 3001}; !reflect.DeepEqual(ports, want) {
		t.Fatalf("ports = %v, want %v", ports, want)
	}
	if got := runner.args; !reflect.DeepEqual(got, []string{
		"-T", "-o", "BatchMode=yes", "-o", "PermitLocalCommand=no", "--", "reyna", "ss", "-H", "-ltn",
	}) {
		t.Fatalf("SSH args = %v", got)
	}
}

func TestSSHDiscovererReportsUnavailableAndMalformedSSOutput(t *testing.T) {
	t.Parallel()

	for name, runner := range map[string]*recordingRunner{
		"unavailable": {output: []byte("sh: ss: command not found\n"), err: errors.New("exit status 127")},
		"malformed":   {output: []byte("this is not ss output\n")},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, err := (mirror.SSHDiscoverer{Runner: runner}).ListeningPorts(context.Background(), "reyna")
			if err == nil || !strings.Contains(err.Error(), "ss") {
				t.Fatalf("error = %v, want clear ss discovery error", err)
			}
		})
	}
}

type recordingRunner struct {
	output []byte
	err    error
	path   string
	args   []string
}

func (r *recordingRunner) CombinedOutput(_ context.Context, path string, args ...string) ([]byte, error) {
	r.path = path
	r.args = append([]string(nil), args...)
	return r.output, r.err
}
