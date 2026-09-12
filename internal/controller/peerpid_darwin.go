//go:build darwin

package controller

import (
	"net"
	"syscall"
)

const (
	darwinSOLLocal     = 0
	darwinLocalPeerPID = 2
)

func peerProcessID(connection net.Conn) (int, error) {
	unixConnection := connection.(*net.UnixConn)
	raw, err := unixConnection.SyscallConn()
	if err != nil {
		return 0, err
	}
	var pid int
	var socketErr error
	err = raw.Control(func(fd uintptr) {
		pid, socketErr = syscall.GetsockoptInt(int(fd), darwinSOLLocal, darwinLocalPeerPID)
	})
	if err != nil {
		return 0, err
	}
	return pid, socketErr
}
