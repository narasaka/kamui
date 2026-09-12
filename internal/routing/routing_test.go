package routing_test

import (
	"testing"

	"github.com/narasaka/kamui/internal/routing"
)

func TestPlanRoutesOnlyExplicitLoopbackSyntaxThroughSSH(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		authority string
		port      uint16
		wantKind  routing.Kind
		wantAddr  string
	}{
		{name: "localhost", authority: "localhost:3000", wantKind: routing.RemoteLoopback, wantAddr: "127.0.0.1:3000"},
		{name: "case insensitive", authority: "LOCALHOST:3001", wantKind: routing.RemoteLoopback, wantAddr: "127.0.0.1:3001"},
		{name: "localhost subdomain", authority: "api.dev.LOCALHOST:3002", wantKind: routing.RemoteLoopback, wantAddr: "127.0.0.1:3002"},
		{name: "IPv4 loopback range", authority: "127.42.9.1:3003", wantKind: routing.RemoteLoopback, wantAddr: "127.0.0.1:3003"},
		{name: "IPv6 loopback", authority: "[::1]:8443", wantKind: routing.RemoteLoopback, wantAddr: "127.0.0.1:8443"},
		{name: "default port", authority: "localhost", port: 80, wantKind: routing.RemoteLoopback, wantAddr: "127.0.0.1:80"},
		{name: "deceptive suffix", authority: "localhost.example:80", wantKind: routing.Direct, wantAddr: "localhost.example:80"},
		{name: "deceptive prefix", authority: "notlocalhost:80", wantKind: routing.Direct, wantAddr: "notlocalhost:80"},
		{name: "other IPv4", authority: "128.0.0.1:80", wantKind: routing.Direct, wantAddr: "128.0.0.1:80"},
		{name: "other IPv6", authority: "[::2]:80", wantKind: routing.Direct, wantAddr: "[::2]:80"},
		{name: "DNS is not resolved", authority: "loopback.invalid:80", wantKind: routing.Direct, wantAddr: "loopback.invalid:80"},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			plan, err := routing.Plan(test.authority, test.port)
			if err != nil {
				t.Fatalf("Plan(%q, %d) returned error: %v", test.authority, test.port, err)
			}
			if plan.Kind != test.wantKind || plan.Address != test.wantAddr {
				t.Fatalf("Plan(%q, %d) = {%v %q}, want {%v %q}", test.authority, test.port, plan.Kind, plan.Address, test.wantKind, test.wantAddr)
			}
		})
	}
}
