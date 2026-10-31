package testsupport

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"sync"

	"github.com/narasaka/kamui/internal/askpass"
	"github.com/narasaka/kamui/internal/ssh"
)

// PromptingSSHLauncher simulates OpenSSH asking for a key passphrase through
// SSH_ASKPASS. It runs the askpass helper in-process with the environment the
// transport supplied and connects only when the answer matches Passphrase.
type PromptingSSHLauncher struct {
	SSHLauncher
	Prompt     string
	Passphrase string

	mu       sync.Mutex
	prompted int
}

func (l *PromptingSSHLauncher) Start(request ssh.StartRequest) (ssh.Process, error) {
	environment := map[string]string{}
	for _, entry := range request.Env {
		if name, value, ok := strings.Cut(entry, "="); ok {
			environment[name] = value
		}
	}
	if environment["SSH_ASKPASS_REQUIRE"] != "force" || environment["SSH_ASKPASS"] == "" {
		_, _ = io.WriteString(request.Stderr, "read_passphrase: can't open /dev/tty: Device not configured\nPermission denied (publickey).\n")
		return nil, errors.New("exit status 255")
	}
	var answer bytes.Buffer
	status := askpass.RunHelper([]string{l.Prompt}, func(name string) string { return environment[name] }, &answer)
	l.mu.Lock()
	l.prompted++
	l.mu.Unlock()
	if status != 0 || answer.String() != l.Passphrase+"\n" {
		_, _ = io.WriteString(request.Stderr, "Permission denied (publickey).\n")
		return nil, errors.New("exit status 255")
	}
	return l.SSHLauncher.Start(request)
}

// Prompted returns how many times the simulated OpenSSH asked for input.
func (l *PromptingSSHLauncher) Prompted() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.prompted
}
