package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"time"

	"github.com/narasaka/kamui/internal/proxy"
	"github.com/narasaka/kamui/internal/session"
	"github.com/narasaka/kamui/internal/ssh"
)

type diagnostic struct {
	Name    string `json:"name"`
	Status  string `json:"status"`
	Details string `json:"details,omitempty"`
}

func (a *Application) runDoctor(ctx context.Context, destination session.Destination, jsonOutput bool) (Result, error) {
	checks := make([]diagnostic, 0, 7)
	sshDiagnostics := ssh.Diagnostics{}
	sshPath, err := sshDiagnostics.Executable()
	checks = append(checks, diagnosticResult("SSH executable", err, sshPath))
	if err == nil {
		connectContext, cancel := context.WithTimeout(ctx, 4*time.Second)
		output, connectErr := sshDiagnostics.Connectivity(connectContext, sshPath, destination.String())
		cancel()
		checks = append(checks, diagnosticResult("SSH connectivity", connectErr, concise(output)))

		forwarded, configErr := sshDiagnostics.AgentForwarding(ctx, sshPath, destination.String())
		if configErr == nil && forwarded {
			checks = append(checks, diagnostic{Name: "SSH agent forwarding", Status: "warn", Details: "enabled by OpenSSH configuration"})
		}
	} else {
		checks = append(checks, diagnostic{Name: "SSH connectivity", Status: "fail", Details: "SSH executable unavailable"})
	}

	installations, browserErr := a.browsers.Detect(ctx)
	browserDetails := fmt.Sprintf("%d supported installation(s)", len(installations))
	if browserErr == nil && len(installations) == 0 {
		browserErr = fmt.Errorf("no supported browser found")
	}
	checks = append(checks, diagnosticResult("browser discovery", browserErr, browserDetails))

	stateErr := a.layout.Ensure()
	if stateErr == nil {
		probe, err := os.CreateTemp(a.layout.State, "doctor-*")
		if err == nil {
			probe.Close()
			err = os.Remove(probe.Name())
		}
		stateErr = err
	}
	checks = append(checks, diagnosticResult("state directory", stateErr, a.layout.Root))

	listener, portErr := net.Listen("tcp4", "127.0.0.1:0")
	if portErr == nil {
		portErr = listener.Close()
	}
	checks = append(checks, diagnosticResult("port allocation", portErr, "127.0.0.1:0"))

	dialer := (&net.Dialer{}).DialContext
	running, proxyErr := proxy.Start(ctx, proxy.Dialers{Direct: dialer, Remote: dialer}, proxy.Options{})
	proxyDetails := ""
	if proxyErr == nil {
		proxyDetails = running.Addr().String()
		if !running.Addr().Addr().IsLoopback() {
			proxyErr = fmt.Errorf("proxy did not bind to loopback")
		}
		proxyErr = errors.Join(proxyErr, running.Close())
	}
	checks = append(checks, diagnosticResult("loopback proxy", proxyErr, proxyDetails))

	if jsonOutput {
		encoded, err := json.MarshalIndent(checks, "", "  ")
		return Result{Output: string(encoded) + "\n"}, err
	}
	var output strings.Builder
	for _, check := range checks {
		fmt.Fprintf(&output, "[%s] %s", check.Status, check.Name)
		if check.Details != "" {
			fmt.Fprintf(&output, ": %s", check.Details)
		}
		output.WriteByte('\n')
	}
	return Result{Output: output.String()}, nil
}

func diagnosticResult(name string, err error, details string) diagnostic {
	if err != nil {
		return diagnostic{Name: name, Status: "fail", Details: concise(err.Error())}
	}
	return diagnostic{Name: name, Status: "ok", Details: details}
}

func concise(value string) string {
	value = strings.TrimSpace(value)
	if index := strings.IndexByte(value, '\n'); index >= 0 {
		value = value[:index]
	}
	return value
}
