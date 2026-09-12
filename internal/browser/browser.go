// Package browser discovers and launches isolated Kamui development browsers.
package browser

import (
	"context"
	"net/netip"
)

// Installation is one supported browser executable.
type Installation struct {
	ID         string
	Executable string
}

// Session contains the browser-visible settings for one SSH destination.
type Session struct {
	Key         string
	Proxy       netip.AddrPort
	ProfileRoot string
}

// Profile is a prepared, isolated browser profile.
type Profile struct {
	Path         string
	Session      Session
	Installation Installation
}

// Adapter separates browser discovery and profiles from networking code.
type Adapter interface {
	ID() string
	Detect(context.Context) ([]Installation, error)
	PrepareProfile(context.Context, Session, Installation) (Profile, error)
	Launch(context.Context, Profile, []string) error
	Open(context.Context, Profile, []string) error
}

// Launcher starts an external browser process.
type Launcher interface {
	Launch(context.Context, string, []string) error
}
