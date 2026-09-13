package truncator

import (
	"errors"
	"io"
	"os"
)

const (
	DefaultChunkSize = 100 * 1024 * 1024
	shiftBufSize     = 4 * 1024 * 1024
)

var ErrClosed = errors.New("truncator: closed")

// Truncator wraps a file opened for deletion (del=true) and releases the
// bytes behind the read position once chunk of them accumulate, shrinking
// its disk usage as it is read. With del=false it is a plain, unmodifying
// reader.
//
// Where the filesystem supports sparse files the consumed prefix is punched
// out in place, which costs nothing and leaves the read position untouched.
// Otherwise it falls back to copying the remainder to the front of the file
// and truncating, which is correct but rewrites the tail on every reclaim.
type Truncator struct {
	f        *os.File
	path     string
	chunk    int64
	del      bool
	onShift  func(freed, remaining int64)
	size     int64
	consumed int64 // bytes read since the last reclaim
	offset   int64 // absolute read position, sparse mode only
	punched  int64 // bytes already released, sparse mode only
	sparse   bool
	closed   bool
	failed   bool
	shiftBuf []byte
}

func Open(path string, chunk int64, del bool, onShift func(int64, int64)) (*Truncator, error) {
	if chunk <= 0 {
		chunk = DefaultChunkSize
	}
	f, err := openSequential(path)
	if err != nil {
		return nil, err
	}
	fi, err := f.Stat()
	if err != nil {
		_ = f.Close() // the stat failure is what the caller needs
		return nil, err
	}

	t := &Truncator{f: f, path: path, chunk: chunk, del: del, onShift: onShift, size: fi.Size()}
	if del {
		t.sparse = enableSparse(f) == nil
	}
	return t, nil
}

func (t *Truncator) Sparse() bool { return t.sparse }

func (t *Truncator) Read(p []byte) (int, error) {
	if t.closed {
		return 0, ErrClosed
	}
	if !t.del {
		return t.f.Read(p)
	}
	if t.consumed >= t.chunk {
		if err := t.reclaim(); err != nil {
			t.failed = true
			return 0, err
		}
	}

	limit := min(len(p), int(t.chunk-t.consumed))
	n, err := t.f.Read(p[:limit])
	if n > 0 {
		t.consumed += int64(n)
		t.offset += int64(n)
	}
	if err != nil && !errors.Is(err, io.EOF) {
		t.failed = true
	}
	return n, err
}

func (t *Truncator) reclaim() error {
	if t.sparse {
		return t.punch()
	}
	return t.shift()
}

// punch releases the consumed prefix without moving data, so the read
// position stays valid and the cost is independent of the file size.
func (t *Truncator) punch() error {
	freed := t.offset - t.punched
	if freed <= 0 {
		t.consumed = 0
		return nil
	}
	if err := punchHole(t.f, t.punched, freed); err != nil {
		// Fall back permanently; the file is still intact at this point.
		t.sparse = false
		return t.shift()
	}
	t.punched = t.offset
	t.consumed = 0

	if t.onShift != nil {
		t.onShift(freed, t.size-t.punched)
	}
	return nil
}

func (t *Truncator) shift() error {
	fi, err := t.f.Stat()
	if err != nil {
		return err
	}
	sz, amt := fi.Size(), t.consumed
	if sz <= amt {
		if err := t.f.Truncate(0); err != nil {
			return err
		}
		if _, err := t.f.Seek(0, io.SeekStart); err != nil {
			return err
		}
		t.consumed, t.offset = 0, 0
		if t.onShift != nil {
			t.onShift(amt, 0)
		}
		return nil
	}

	if t.shiftBuf == nil {
		t.shiftBuf = make([]byte, shiftBufSize)
	}

	rOff, wOff := amt, int64(0)
	for rOff < sz {
		toRead := min(int64(len(t.shiftBuf)), sz-rOff)
		if _, err := t.f.Seek(rOff, io.SeekStart); err != nil {
			return err
		}
		nr, readErr := io.ReadFull(t.f, t.shiftBuf[:toRead])
		if nr == 0 {
			break
		}
		if _, err := t.f.Seek(wOff, io.SeekStart); err != nil {
			return err
		}
		if _, err := t.f.Write(t.shiftBuf[:nr]); err != nil {
			return err
		}
		rOff += int64(nr)
		wOff += int64(nr)
		if readErr != nil && !errors.Is(readErr, io.EOF) && !errors.Is(readErr, io.ErrUnexpectedEOF) {
			return readErr
		}
	}

	newSize := sz - amt
	if err := t.f.Truncate(newSize); err != nil {
		return err
	}
	if _, err := t.f.Seek(0, io.SeekStart); err != nil {
		return err
	}
	t.consumed, t.offset = 0, 0

	if t.onShift != nil {
		t.onShift(amt, newSize)
	}
	return nil
}

// Close closes the underlying file and optionally removes it from disk.
func (t *Truncator) Close() error {
	if t.closed {
		return nil
	}
	t.closed = true
	err := t.f.Close()
	if t.del && !t.failed {
		if rmErr := os.Remove(t.path); rmErr != nil && err == nil {
			err = rmErr
		}
	}
	return err
}
