//go:build windows

package truncator

import (
	"encoding/binary"
	"errors"
	"os"
	"syscall"
)

const (
	fsctlSetSparse   = 0x000900C4
	fsctlSetZeroData = 0x000980C8
)

func enableSparse(f *os.File) error {
	var n uint32
	return syscall.DeviceIoControl(syscall.Handle(f.Fd()), fsctlSetSparse, nil, 0, nil, 0, &n, nil)
}

// punchHole issues FSCTL_SET_ZERO_DATA, which deallocates the range on NTFS
// rather than writing zeroes over it.
func punchHole(f *os.File, off, length int64) error {
	// This destroys file contents, so refuse a nonsensical range outright
	// rather than let it wrap into an enormous unsigned one.
	if off < 0 || length < 0 {
		return errors.New("truncator: negative hole range")
	}

	var info [16]byte // FILE_ZERO_DATA_INFORMATION{FileOffset, BeyondFinalZero}
	binary.LittleEndian.PutUint64(info[0:8], uint64(off))
	binary.LittleEndian.PutUint64(info[8:16], uint64(off+length))

	var n uint32
	return syscall.DeviceIoControl(syscall.Handle(f.Fd()), fsctlSetZeroData,
		&info[0], uint32(len(info)), nil, 0, &n, nil)
}
