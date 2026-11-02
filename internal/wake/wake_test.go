package wake

import (
	"context"
	"errors"
	"net"
	"reflect"
	"sync"
	"testing"
	"time"
)

func TestDetectorReportsSleepWhenWallClockRunsAheadOfMonotonicClock(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, 11, 2, 9, 0, 0, 0, time.UTC)
	addresses := []string{"192.168.1.20"}
	var detector detector
	if got := detector.observe(observation{wall: start, addresses: addresses}); got != nil {
		t.Fatalf("first observation = %v, want nothing", got)
	}
	if got := detector.observe(observation{wall: start.Add(2 * time.Second), monotonic: 2 * time.Second, addresses: addresses}); got != nil {
		t.Fatalf("ordinary tick = %v, want nothing", got)
	}
	got := detector.observe(observation{wall: start.Add(10 * time.Minute), monotonic: 4 * time.Second, addresses: addresses})
	if !reflect.DeepEqual(got, []Reason{Slept}) {
		t.Fatalf("after suspend = %v, want slept", got)
	}
}

func TestDetectorIgnoresShortClockAdjustments(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, 11, 2, 9, 0, 0, 0, time.UTC)
	var detector detector
	detector.observe(observation{wall: start})
	if got := detector.observe(observation{wall: start.Add(4 * time.Second), monotonic: 2 * time.Second}); got != nil {
		t.Fatalf("two-second clock step = %v, want nothing", got)
	}
}

func TestDetectorReportsChangedAddressesButNotUnreadableOnes(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, 11, 2, 9, 0, 0, 0, time.UTC)
	tick := func(seconds int, addresses []string) observation {
		elapsed := time.Duration(seconds) * time.Second
		return observation{wall: start.Add(elapsed), monotonic: elapsed, addresses: addresses}
	}
	var detector detector
	detector.observe(tick(0, []string{"192.168.1.20"}))
	if got := detector.observe(tick(2, nil)); got != nil {
		t.Fatalf("unreadable addresses = %v, want nothing", got)
	}
	if got := detector.observe(tick(4, []string{"192.168.1.20"})); got != nil {
		t.Fatalf("same addresses after unreadable ones = %v, want nothing", got)
	}
	if got := detector.observe(tick(6, []string{"10.0.0.7"})); !reflect.DeepEqual(got, []Reason{NetworkChanged}) {
		t.Fatalf("new network = %v, want network changed", got)
	}
	if got := detector.observe(tick(8, []string{})); !reflect.DeepEqual(got, []Reason{NetworkChanged}) {
		t.Fatalf("network lost = %v, want network changed", got)
	}
}

func TestRoutableAddressesIgnoreLoopbackAndLinkLocal(t *testing.T) {
	t.Parallel()

	network := func(text string) net.Addr {
		return &net.IPNet{IP: net.ParseIP(text), Mask: net.CIDRMask(64, 128)}
	}
	got := routableAddresses(func() ([]net.Addr, error) {
		return []net.Addr{
			network("127.0.0.1"), network("::1"), network("fe80::1"), network("169.254.3.4"),
			network("100.101.102.103"), network("192.168.1.20"), &net.IPAddr{IP: net.ParseIP("2001:db8::5")},
			network("192.168.1.20"),
		}, nil
	})
	want := []string{"100.101.102.103", "192.168.1.20", "2001:db8::5"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("routable addresses = %v, want %v", got, want)
	}
	if got := routableAddresses(func() ([]net.Addr, error) { return nil, errors.New("denied") }); got != nil {
		t.Fatalf("unreadable addresses = %v, want nil", got)
	}
}

func TestWatcherNotifiesWhenAddressesChange(t *testing.T) {
	t.Parallel()

	var mu sync.Mutex
	current := []net.Addr{&net.IPNet{IP: net.ParseIP("192.168.1.20"), Mask: net.CIDRMask(24, 32)}}
	watcher := Watcher{
		Interval: 10 * time.Millisecond,
		Addresses: func() ([]net.Addr, error) {
			mu.Lock()
			defer mu.Unlock()
			return current, nil
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	reasons := make(chan Reason, 10)
	done := make(chan struct{})
	go func() {
		watcher.Run(ctx, func(reason Reason) { reasons <- reason })
		close(done)
	}()
	time.Sleep(50 * time.Millisecond)
	mu.Lock()
	current = []net.Addr{&net.IPNet{IP: net.ParseIP("10.0.0.7"), Mask: net.CIDRMask(24, 32)}}
	mu.Unlock()
	select {
	case reason := <-reasons:
		if reason != NetworkChanged {
			t.Fatalf("reason = %v, want network changed", reason)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("watcher did not report the new address")
	}
	cancel()
	<-done
}
