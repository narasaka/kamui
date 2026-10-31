//go:build linux && (amd64 || arm64)

package askpass

// ioctlSetTermiosFlush is TCSETSF, which the syscall package omits on amd64.
const ioctlSetTermiosFlush = 0x5404
