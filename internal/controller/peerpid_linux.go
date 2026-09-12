//go:build linux

package controller

import (
	"fmt"
	"net"
	"syscall"
)

func peerProcessID(connection net.Conn) (int, error) {
	unixConnection, ok := connection.(*net.UnixConn)
	if !ok {
		return 0, fmt.Errorf("controller connection is not a Unix socket")
	}
	raw, err := unixConnection.SyscallConn()
	if err != nil {
		return 0, err
	}
	var credentials *syscall.Ucred
	var socketErr error
	err = raw.Control(func(fd uintptr) {
		credentials, socketErr = syscall.GetsockoptUcred(int(fd), syscall.SOL_SOCKET, syscall.SO_PEERCRED)
	})
	if err != nil {
		return 0, err
	}
	if socketErr != nil {
		return 0, socketErr
	}
	if credentials == nil || credentials.Pid <= 0 {
		return 0, fmt.Errorf("controller peer credentials did not include a PID")
	}
	return int(credentials.Pid), nil
}
