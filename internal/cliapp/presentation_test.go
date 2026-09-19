package cliapp

import (
	"bytes"
	"errors"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/narasaka/kamui/internal/session"
)

func TestPrintErrorUsesFailureMarkerWithoutColorForRedirectedOutput(t *testing.T) {
	t.Parallel()

	var output bytes.Buffer
	PrintError(&output, errors.New("connection failed"))
	if got, want := output.String(), "✗ connection failed\n"; got != want {
		t.Fatalf("error output = %q, want %q", got, want)
	}
}

func TestColoredStatusKeepsTableAligned(t *testing.T) {
	t.Parallel()

	destination, err := session.ParseDestination("reyna")
	if err != nil {
		t.Fatal(err)
	}
	status := session.SessionStatus{
		Destination: destination,
		State:       session.SessionConnected,
		Proxy:       netip.MustParseAddrPort("127.0.0.1:51747"),
	}
	var output bytes.Buffer
	if err := printStatusesWithStyle(&output, []session.SessionStatus{status}, false, terminalStyle{color: true}); err != nil {
		t.Fatal(err)
	}
	got := output.String()
	if !strings.Contains(got, ansiGreen+"● connected"+ansiReset) {
		t.Fatalf("status output = %q, want green connected state", got)
	}
	if strings.ContainsRune(got, '\xff') {
		t.Fatalf("status output contains tabwriter escape bytes: %q", got)
	}
	plain := strings.NewReplacer(ansiGreen, "", ansiReset, "").Replace(got)
	want := "DESTINATION  STATE\n" +
		"reyna        ● connected\n"
	if plain != want {
		t.Fatalf("visible status output = %q, want %q", plain, want)
	}
}

func TestTerminalStyleColorsFailuresRed(t *testing.T) {
	t.Parallel()

	if got, want := (terminalStyle{color: true}).failure("✗ failed"), ansiRed+"✗ failed"+ansiReset; got != want {
		t.Fatalf("failure style = %q, want %q", got, want)
	}
}

func TestConnectionProgressClearsItsTerminalLine(t *testing.T) {
	t.Parallel()

	var output synchronizedPresentationBuffer
	progress := connectionProgress{writer: &output, visible: true, interval: time.Millisecond}
	if err := progress.start("reyna"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(500 * time.Millisecond)
	for !strings.Contains(output.String(), "◓ connecting to reyna...\r") && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if err := progress.stop(); err != nil {
		t.Fatal(err)
	}
	got := output.String()
	if !strings.HasPrefix(got, "◐ connecting to reyna...\r") {
		t.Fatalf("progress output = %q, want initial frame", got)
	}
	if !strings.Contains(got, "◓ connecting to reyna...\r") {
		t.Fatalf("progress output = %q, want an animated frame", got)
	}
	if !strings.HasSuffix(got, "\r\x1b[2K") {
		t.Fatalf("progress output = %q, want cleared final line", got)
	}
}

type synchronizedPresentationBuffer struct {
	mu     sync.Mutex
	buffer bytes.Buffer
}

func (b *synchronizedPresentationBuffer) Write(value []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.Write(value)
}

func (b *synchronizedPresentationBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.String()
}

func TestConnectionProgressIsSilentWhenOutputIsRedirected(t *testing.T) {
	t.Parallel()

	var output bytes.Buffer
	progress := newConnectionProgress(&output)
	if err := progress.start("reyna"); err != nil {
		t.Fatal(err)
	}
	if err := progress.stop(); err != nil {
		t.Fatal(err)
	}
	if got := output.String(); got != "" {
		t.Fatalf("redirected progress output = %q, want empty", got)
	}
}

func TestShellWordQuotesDestinationsThatNeedShellEscaping(t *testing.T) {
	t.Parallel()

	for input, want := range map[string]string{
		"reyna":       "reyna",
		"nara@reyna":  "nara@reyna",
		"host$(evil)": "'host$(evil)'",
		"host'name":   `'host'"'"'name'`,
	} {
		if got := shellWord(input); got != want {
			t.Errorf("shellWord(%q) = %q, want %q", input, got, want)
		}
	}
}
