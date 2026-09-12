// Package routing decides whether an HTTP proxy target is reached directly or
// through the remote host's loopback interface.
package routing

import (
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
)

// Kind is the network path selected for a target.
type Kind uint8

const (
	// Direct connects from the Mac.
	Direct Kind = iota
	// RemoteLoopback connects through SSH to the remote loopback interface.
	RemoteLoopback
)

// DialPlan is the validated network path and address for a proxy target.
type DialPlan struct {
	Kind    Kind
	Address string
}

// Plan validates an HTTP authority and selects its network path. defaultPort
// is used only when authority does not contain an explicit port.
func Plan(authority string, defaultPort uint16) (DialPlan, error) {
	parsed, err := url.Parse("http://" + authority)
	if err != nil {
		return DialPlan{}, fmt.Errorf("malformed target %q: %w", authority, err)
	}
	if parsed.Host != authority || parsed.User != nil {
		return DialPlan{}, fmt.Errorf("malformed target %q", authority)
	}
	host := parsed.Hostname()
	if host == "" {
		return DialPlan{}, fmt.Errorf("malformed target %q: host is required", authority)
	}

	port := defaultPort
	if text := parsed.Port(); text != "" {
		value, err := strconv.ParseUint(text, 10, 16)
		if err != nil || value == 0 {
			return DialPlan{}, fmt.Errorf("malformed target %q: invalid port", authority)
		}
		port = uint16(value)
	} else if strings.HasSuffix(authority, ":") || port == 0 {
		return DialPlan{}, fmt.Errorf("malformed target %q: port is required", authority)
	}

	kind := Direct
	dialHost := host
	if isExplicitLoopback(host) {
		kind = RemoteLoopback
		dialHost = "127.0.0.1"
	}

	return DialPlan{Kind: kind, Address: net.JoinHostPort(dialHost, strconv.Itoa(int(port)))}, nil
}

func isExplicitLoopback(host string) bool {
	folded := strings.ToLower(host)
	if folded == "localhost" || strings.HasSuffix(folded, ".localhost") {
		return true
	}
	address, err := netip.ParseAddr(host)
	if err != nil {
		return false
	}
	if address.Is4() {
		return address.As4()[0] == 127
	}
	return address == netip.IPv6Loopback()
}
