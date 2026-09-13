//go:build windows

package truncator

import (
	"os"
	"syscall"
)

const fileFlagSequentialScan = 0x08000000

// openSequential opens the archive with FILE_FLAG_SEQUENTIAL_SCAN, which
// tells the cache manager to read ahead aggressively and to evict the pages
// behind us rather than retaining an archive-sized working set.
func openSequential(path string) (*os.File, error) {
	p, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	h, err := syscall.CreateFile(p,
		syscall.GENERIC_READ|syscall.GENERIC_WRITE,
		syscall.FILE_SHARE_READ,
		nil,
		syscall.OPEN_EXISTING,
		fileFlagSequentialScan,
		0)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	return os.NewFile(uintptr(h), path), nil
}
