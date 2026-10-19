//go:build !darwin && !linux

package cli

import (
	"errors"
	"os"
)

func makeRaw(_ *os.File) (func(), error) {
	return nil, errors.New("raw mode not supported on this platform")
}
