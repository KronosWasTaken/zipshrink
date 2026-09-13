package extractor

import (
	"errors"
	"io"
	"os"
	"time"
)

const (
	writeBufSize = 1 << 20
	writeBufs    = 8
)

type writeOp struct {
	path string
	mod  time.Time
	data []byte // nil closes the current file
}

// asyncWriter moves file creation and writing onto its own goroutine so the
// archive keeps being read while the previous entry is still hitting disk.
// Reading the archive is strictly sequential, so this overlap is the only
// parallelism the format allows.
type asyncWriter struct {
	dest string
	ops  chan writeOp
	free chan []byte
	done chan error
	dirs map[string]struct{}
}

func newAsyncWriter(dest string) *asyncWriter {
	w := &asyncWriter{
		dest: dest,
		ops:  make(chan writeOp, writeBufs),
		free: make(chan []byte, writeBufs),
		done: make(chan error, 1),
		dirs: map[string]struct{}{dest: {}},
	}
	for range writeBufs {
		w.free <- make([]byte, writeBufSize)
	}
	go w.loop()
	return w
}

func (w *asyncWriter) loop() {
	var f *os.File
	var cur writeOp

	fail := func(err error) {
		if f != nil {
			_ = f.Close()
			_ = os.Remove(cur.path) // best effort; err below is what matters
			f = nil
		}
		w.done <- err
		for op := range w.ops { // drain so senders never block
			if op.data != nil {
				w.free <- op.data[:writeBufSize]
			}
		}
	}

	for op := range w.ops {
		if op.data == nil {
			if f != nil {
				err := f.Close()
				f = nil
				if err != nil {
					fail(err)
					return
				}
				if !cur.mod.IsZero() {
					_ = os.Chtimes(cur.path, cur.mod, cur.mod)
				}
			}
			continue
		}

		if f == nil {
			cur = op
			// The directory is created by the caller before it enqueues, so
			// this goroutine never touches the dirs cache.
			file, err := os.OpenFile(op.path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, defaultFileMode)
			if err != nil {
				fail(err)
				return
			}
			f = file
		}
		_, err := f.Write(op.data)
		w.free <- op.data[:writeBufSize]
		if err != nil {
			fail(err)
			return
		}
	}
	w.done <- nil
}

func (w *asyncWriter) stream(path string, mod time.Time, r io.Reader) (int64, error) {
	var total int64
	for {
		var buf []byte
		select {
		case buf = <-w.free:
		case err := <-w.done: // writer already failed
			w.done <- err
			return total, err
		}

		n, err := r.Read(buf)
		if n > 0 {
			total += int64(n)
			w.ops <- writeOp{path: path, mod: mod, data: buf[:n]}
		} else {
			w.free <- buf
		}
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return total, err
		}
	}
	w.ops <- writeOp{} // close the file
	return total, nil
}

func (w *asyncWriter) mkdirAll(dir string) error {
	if _, ok := w.dirs[dir]; ok {
		return nil
	}
	if err := os.MkdirAll(dir, defaultDirMode); err != nil {
		return err
	}
	w.dirs[dir] = struct{}{}
	return nil
}

func (w *asyncWriter) close() error {
	close(w.ops)
	return <-w.done
}
