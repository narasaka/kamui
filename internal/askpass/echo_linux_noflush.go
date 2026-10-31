//go:build linux && !amd64 && !arm64

package askpass

import "syscall"

// Other Linux architectures restore settings without discarding input.
const ioctlSetTermiosFlush = syscall.TCSETS
