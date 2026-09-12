//go:build !darwin

package controller

import (
	"fmt"
	"net"
)

func peerProcessID(net.Conn) (int, error) {
	return 0, fmt.Errorf("controller peer PID is unavailable on this platform")
}
