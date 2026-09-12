// Package session owns the identity and lifecycle of a Kamui SSH session.
package session

import (
	"crypto/sha256"
	"fmt"
	"strings"
	"unicode"
)

// Destination is the exact positional destination passed to OpenSSH.
type Destination struct {
	raw string
}

// Key returns a stable, path-safe identity for the exact destination.
func (d Destination) Key() string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(d.raw)))
}

// ParseDestination constructs an OpenSSH destination.
func ParseDestination(raw string) (Destination, error) {
	if raw == "" {
		return Destination{}, fmt.Errorf("SSH destination is required")
	}
	if strings.HasPrefix(raw, "-") {
		return Destination{}, fmt.Errorf("SSH destination %q must not begin with '-'", raw)
	}
	if strings.IndexFunc(raw, unicode.IsSpace) >= 0 || strings.IndexFunc(raw, unicode.IsControl) >= 0 {
		return Destination{}, fmt.Errorf("SSH destination %q must not contain whitespace or control characters", raw)
	}
	return Destination{raw: raw}, nil
}

// String returns the destination exactly as supplied by the user.
func (d Destination) String() string {
	return d.raw
}
