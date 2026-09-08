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

// Truncator wraps a file opened for deletion (del=true) and shifts unread
// bytes to offset 0 when the chunk threshold is met, shrinking it in place
// as it's read. With del=false it's a plain, unmodifying reader.
type Truncator struct {
	f        *os.File
	path     string
	chunk    int64
	del      bool
	onShift  func(freed, remaining int64)
	consumed int64
	closed   bool
	failed   bool
	shiftBuf []byte
}

// Open initializes a Truncator for the file at path.
func Open(path string, chunk int64, del bool, onShift func(int64, int64)) (*Truncator, error) {
	if chunk <= 0 {
		chunk = DefaultChunkSize
	}
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return nil, err
	}
	return &Truncator{f: f, path: path, chunk: chunk, del: del, onShift: onShift}, nil
}

// Read reads from the file, triggering shift-and-truncate when reaching the
// chunk threshold. If del is false the file is never deleted, so it's also
// never shifted or truncated -- Read is a plain passthrough, leaving the
// source completely unmodified.
func (t *Truncator) Read(p []byte) (int, error) {
	if t.closed {
		return 0, ErrClosed
	}
	if !t.del {
		return t.f.Read(p)
	}
	if t.consumed >= t.chunk {
		if err := t.shift(); err != nil {
			t.failed = true
			return 0, err
		}
	}
	limit := min(len(p), int(t.chunk-t.consumed))
	n, err := t.f.Read(p[:limit])
	if n > 0 {
		t.consumed += int64(n)
	}
	if err != nil && !errors.Is(err, io.EOF) {
		t.failed = true
	}
	return n, err
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
		t.consumed = 0
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
	t.consumed = 0

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
