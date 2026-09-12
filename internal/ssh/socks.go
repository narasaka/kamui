package ssh

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"time"
)

// ErrTunnelUnavailable means the OpenSSH SOCKS transport cannot accept a new
// connection.
var ErrTunnelUnavailable = errors.New("SSH tunnel is unavailable")

// SOCKSError is a failure reply returned by the OpenSSH SOCKS listener.
type SOCKSError struct {
	Code byte
}

func (e *SOCKSError) Error() string {
	description := map[byte]string{
		1: "general failure",
		2: "connection not allowed",
		3: "network unreachable",
		4: "host unreachable",
		5: "connection refused",
		6: "TTL expired",
		7: "command not supported",
		8: "address type not supported",
	}[e.Code]
	if description == "" {
		description = "unknown error"
	}
	return fmt.Sprintf("SOCKS5 proxy returned %s (code %d)", description, e.Code)
}

// DialContext opens a TCP stream through the supervised OpenSSH SOCKS listener.
func (c *Connection) DialContext(ctx context.Context, network, target string) (net.Conn, error) {
	if !strings.HasPrefix(network, "tcp") {
		return nil, fmt.Errorf("SOCKS5 supports TCP only, not %q", network)
	}
	if State(c.state.Load()) != Connected {
		return nil, ErrTunnelUnavailable
	}

	connection, err := (&net.Dialer{}).DialContext(ctx, "tcp4", c.address)
	if err != nil {
		return nil, fmt.Errorf("connect to OpenSSH SOCKS listener: %w", err)
	}
	succeeded := false
	defer func() {
		if !succeeded {
			_ = connection.Close()
		}
	}()
	stopCancellation := context.AfterFunc(ctx, func() { _ = connection.Close() })
	defer stopCancellation()
	deadline := time.Now().Add(10 * time.Second)
	if contextDeadline, ok := ctx.Deadline(); ok && contextDeadline.Before(deadline) {
		deadline = contextDeadline
	}
	_ = connection.SetDeadline(deadline)

	if _, err := connection.Write([]byte{5, 1, 0}); err != nil {
		return nil, fmt.Errorf("write SOCKS5 greeting: %w", err)
	}
	greeting := make([]byte, 2)
	if _, err := io.ReadFull(connection, greeting); err != nil {
		return nil, fmt.Errorf("read SOCKS5 greeting: %w", err)
	}
	if greeting[0] != 5 || greeting[1] != 0 {
		return nil, fmt.Errorf("SOCKS5 listener rejected unauthenticated method")
	}

	request, err := connectRequest(target)
	if err != nil {
		return nil, err
	}
	if _, err := connection.Write(request); err != nil {
		return nil, fmt.Errorf("write SOCKS5 request: %w", err)
	}
	reply := make([]byte, 4)
	if _, err := io.ReadFull(connection, reply); err != nil {
		return nil, fmt.Errorf("read SOCKS5 reply: %w", err)
	}
	if reply[0] != 5 || reply[2] != 0 {
		return nil, fmt.Errorf("malformed SOCKS5 reply")
	}
	if reply[1] != 0 {
		return nil, &SOCKSError{Code: reply[1]}
	}
	if err := discardBoundAddress(connection, reply[3]); err != nil {
		return nil, err
	}
	if err := connection.SetDeadline(time.Time{}); err != nil {
		return nil, fmt.Errorf("clear SOCKS5 deadline: %w", err)
	}
	succeeded = true
	return connection, nil
}

func connectRequest(target string) ([]byte, error) {
	host, portText, err := net.SplitHostPort(target)
	if err != nil {
		return nil, fmt.Errorf("invalid SOCKS5 target %q: %w", target, err)
	}
	port, err := strconv.ParseUint(portText, 10, 16)
	if err != nil || port == 0 {
		return nil, fmt.Errorf("invalid SOCKS5 target port %q", portText)
	}

	request := []byte{5, 1, 0}
	if address := net.ParseIP(host); address != nil {
		if ipv4 := address.To4(); ipv4 != nil {
			request = append(request, 1)
			request = append(request, ipv4...)
		} else {
			request = append(request, 4)
			request = append(request, address.To16()...)
		}
	} else {
		if len(host) == 0 || len(host) > 255 {
			return nil, fmt.Errorf("invalid SOCKS5 target host")
		}
		request = append(request, 3, byte(len(host)))
		request = append(request, host...)
	}
	request = binary.BigEndian.AppendUint16(request, uint16(port))
	return request, nil
}

func discardBoundAddress(connection net.Conn, addressType byte) error {
	length := 0
	switch addressType {
	case 1:
		length = 4
	case 4:
		length = 16
	case 3:
		size := []byte{0}
		if _, err := io.ReadFull(connection, size); err != nil {
			return fmt.Errorf("read SOCKS5 bound hostname length: %w", err)
		}
		length = int(size[0])
	default:
		return fmt.Errorf("SOCKS5 reply has unsupported address type %d", addressType)
	}
	buffer := make([]byte, length+2)
	if _, err := io.ReadFull(connection, buffer); err != nil {
		return fmt.Errorf("read SOCKS5 bound address: %w", err)
	}
	return nil
}
