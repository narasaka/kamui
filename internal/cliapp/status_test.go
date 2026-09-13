package cliapp

import (
	"bytes"
	"net/netip"
	"strings"
	"testing"

	"github.com/narasaka/kamui/internal/mirror"
	"github.com/narasaka/kamui/internal/session"
)

func TestPrintStatusesWrapsLongMirrorDetails(t *testing.T) {
	t.Parallel()
	const wantLineWidth = 80

	destination, err := session.ParseDestination("reyna")
	if err != nil {
		t.Fatal(err)
	}
	status := session.SessionStatus{
		Destination: destination,
		State:       session.SessionConnected,
		Proxy:       netip.MustParseAddrPort("127.0.0.1:51747"),
		Mirror: mirror.Status{
			Enabled:       true,
			MirroredPorts: []uint16{2019, 3011, 3389, 3773, 5432, 8317, 8385, 20241, 22000, 34180, 34715, 38223, 38331, 41533, 41715, 42971, 43713, 43887, 44389, 53113},
			ConflictedPorts: []mirror.Conflict{
				{Port: 22}, {Port: 53}, {Port: 80}, {Port: 631},
			},
		},
	}

	var output bytes.Buffer
	if err := printStatuses(&output, []session.SessionStatus{status}, false); err != nil {
		t.Fatal(err)
	}
	got := output.String()
	for _, want := range []string{
		"DESTINATION  STATE      BROWSER  PROXY            SSH      MIRROR\n",
		"reyna        connected  -        127.0.0.1:51747  healthy  enabled\n\n",
		"MIRRORED:     ",
		"\nCONFLICTS:    22, 53, 80, 631\n",
		"\nMIRROR ERROR: -\n",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("status output = %q, want substring %q", got, want)
		}
	}
	if strings.Contains(got, "\t") {
		t.Fatalf("status output contains terminal-dependent tab stops: %q", got)
	}
	compact := strings.NewReplacer(" ", "", "\n", "", "\t", "").Replace(got)
	wantPorts := "MIRRORED:2019,3011,3389,3773,5432,8317,8385,20241,22000,34180,34715,38223,38331,41533,41715,42971,43713,43887,44389,53113CONFLICTS:"
	if !strings.Contains(compact, wantPorts) {
		t.Fatalf("status output lost or reordered mirrored ports: %q", got)
	}
	for _, line := range strings.Split(strings.TrimSuffix(got, "\n"), "\n") {
		if len(line) > wantLineWidth {
			t.Fatalf("status line is %d characters, want at most %d: %q", len(line), wantLineWidth, line)
		}
	}
}
