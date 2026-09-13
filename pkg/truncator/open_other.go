//go:build !windows

package truncator

import "os"

func openSequential(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_RDWR, 0)
}
