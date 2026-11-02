// Package wake reports when this machine may have slept or moved to another
// network, either of which can silently break an open SSH connection.
package wake

import (
	"context"
	"net"
	"slices"
	"time"
)

// Reason says why the watcher reported a wakeup.
type Reason string

const (
	// Slept means the wall clock moved further than the monotonic clock,
	// which does not advance while macOS or Linux is suspended.
	Slept Reason = "slept"
	// NetworkChanged means the set of routable local addresses changed.
	NetworkChanged Reason = "network_changed"
)

const (
	defaultInterval       = 2 * time.Second
	defaultSleepThreshold = 5 * time.Second
)

// Watcher polls the clocks and local addresses. The zero value is ready to
// use.
type Watcher struct {
	// Interval is how often to check. Zero means two seconds.
	Interval time.Duration
	// SleepThreshold is how far the wall clock must run ahead of the
	// monotonic clock between checks to count as a sleep. Zero means five
	// seconds.
	SleepThreshold time.Duration
	// Addresses lists local interface addresses. Nil uses net.InterfaceAddrs.
	Addresses func() ([]net.Addr, error)
}

// Run calls notify for each wakeup until ctx ends.
func (w Watcher) Run(ctx context.Context, notify func(Reason)) {
	interval := w.Interval
	if interval == 0 {
		interval = defaultInterval
	}
	addresses := w.Addresses
	if addresses == nil {
		addresses = net.InterfaceAddrs
	}
	started := time.Now()
	reading := func() observation {
		now := time.Now()
		return observation{
			wall:      now.Round(0),
			monotonic: now.Sub(started),
			addresses: routableAddresses(addresses),
		}
	}
	detector := detector{sleepThreshold: w.SleepThreshold}
	detector.observe(reading())
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		for _, reason := range detector.observe(reading()) {
			notify(reason)
		}
	}
}

type observation struct {
	// wall carries no monotonic reading, so subtracting two of them measures
	// the wall clock.
	wall      time.Time
	monotonic time.Duration
	// addresses is sorted, or nil when they could not be read.
	addresses []string
}

type detector struct {
	sleepThreshold time.Duration
	previous       *observation
}

// observe records one reading and returns what changed since the last one.
func (d *detector) observe(current observation) []Reason {
	previous := d.previous
	d.previous = &current
	if previous == nil {
		return nil
	}
	// current is stored above, so filling in its addresses below also
	// updates the stored reading.
	threshold := d.sleepThreshold
	if threshold == 0 {
		threshold = defaultSleepThreshold
	}
	var reasons []Reason
	wallElapsed := current.wall.Sub(previous.wall)
	monotonicElapsed := current.monotonic - previous.monotonic
	if wallElapsed-monotonicElapsed >= threshold {
		reasons = append(reasons, Slept)
	}
	if current.addresses == nil {
		// Keep comparing against the last addresses that could be read.
		current.addresses = previous.addresses
	} else if previous.addresses != nil && !slices.Equal(current.addresses, previous.addresses) {
		reasons = append(reasons, NetworkChanged)
	}
	return reasons
}

// routableAddresses ignores loopback and link-local addresses, which come and
// go with interfaces such as macOS's AirDrop link without changing any route
// to an SSH server.
func routableAddresses(list func() ([]net.Addr, error)) []string {
	addresses, err := list()
	if err != nil {
		return nil
	}
	result := []string{}
	for _, address := range addresses {
		var ip net.IP
		switch value := address.(type) {
		case *net.IPNet:
			ip = value.IP
		case *net.IPAddr:
			ip = value.IP
		default:
			continue
		}
		if ip.IsGlobalUnicast() {
			result = append(result, ip.String())
		}
	}
	slices.Sort(result)
	return slices.Compact(result)
}
