//go:build linux

package truncator

import (
	"os"
	"syscall"
)

const (
	fallocFlKeepSize  = 0x01
	fallocFlPunchHole = 0x02
)

func enableSparse(*os.File) error { return nil }

func punchHole(f *os.File, off, length int64) error {
	return syscall.Fallocate(int(f.Fd()), fallocFlKeepSize|fallocFlPunchHole, off, length)
}
