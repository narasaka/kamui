//go:build !darwin && !linux

package askpass

import (
	"errors"
	"os"
)

func disableEcho(*os.File) (func(), error) {
	return nil, errors.New("hidden terminal input is not supported on this platform")
}
