//go:build !windows && !linux

package truncator

import (
	"errors"
	"os"
)

var errNoSparse = errors.New("truncator: sparse files unsupported on this platform")

func enableSparse(*os.File) error { return errNoSparse }

func punchHole(*os.File, int64, int64) error { return errNoSparse }
