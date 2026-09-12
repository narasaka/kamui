// Package version contains link-time release metadata and the stable identity
// used to keep the resident controller in step with the invoking executable.
package version

import (
	"crypto/sha256"
	"fmt"
	"os"
)

var (
	Version   = "dev"
	Commit    = "unknown"
	BuildDate = "unknown"
)

// BuildIdentity returns a value that is stable for one installed build. Release
// builds use their link-time metadata. Development builds use the executable
// contents so repeatedly invoking one binary is stable while rebuilding it is
// detected as an upgrade.
func BuildIdentity() string {
	if Version != "dev" || Commit != "unknown" || BuildDate != "unknown" {
		return fmt.Sprintf("release:%s:%s:%s", Version, Commit, BuildDate)
	}
	executable, err := os.Executable()
	if err != nil {
		return "dev:unknown"
	}
	identity, err := ExecutableIdentity(executable)
	if err != nil {
		return "dev:unknown"
	}
	return identity
}

// ExecutableIdentity returns the development identity for a specific binary.
// It is primarily useful when a launcher is about to start that binary.
func ExecutableIdentity(path string) (string, error) {
	contents, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(contents)
	return fmt.Sprintf("dev:%x", digest[:16]), nil
}
