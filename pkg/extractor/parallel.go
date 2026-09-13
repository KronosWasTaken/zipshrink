package extractor

import (
	"bytes"
	"fmt"
	"hash/crc32"
	"io"
	"os"
	"runtime"
	"sync"
	"time"

	"github.com/klauspost/compress/flate"
	"zipshrink/pkg/streamzip"
)

const (
	// Entries above this stay on the sequential path: buffering them whole
	// would cost more memory than the parallelism is worth.
	maxParallelEntry = 8 << 20
	maxWorkers       = 8
)

type decodeJob struct {
	path string
	mod  time.Time
	crc  uint32
	name string
	buf  []byte
}

// decoders decompress entries on their own goroutines. Reading the archive
// stays strictly sequential; only the decode and the write fan out, which is
// the parallelism a single-pass stream allows.
type decoders struct {
	jobs chan decodeJob
	free chan []byte
	wg   sync.WaitGroup
	mu   sync.Mutex
	err  error
}

func workerCount() int {
	return max(1, min(runtime.GOMAXPROCS(0)-1, maxWorkers))
}

func newDecoders() *decoders {
	n := workerCount()
	d := &decoders{
		jobs: make(chan decodeJob, n),
		free: make(chan []byte, n+2),
	}
	for range n + 2 {
		d.free <- make([]byte, maxParallelEntry)
	}
	d.wg.Add(n)
	for range n {
		go d.worker()
	}
	return d
}

func (d *decoders) worker() {
	defer d.wg.Done()
	fr := flate.NewReader(bytes.NewReader(nil))
	src := bytes.NewReader(nil)

	for job := range d.jobs {
		if d.failed() {
			d.free <- job.buf[:maxParallelEntry]
			continue
		}
		src.Reset(job.buf)
		if err := fr.(flate.Resetter).Reset(src, nil); err != nil {
			d.fail(job.name, err)
		} else if err := writeDecoded(job, fr); err != nil {
			d.fail(job.name, err)
		}
		d.free <- job.buf[:maxParallelEntry]
	}
}

// inflateEntry decodes a compressed entry too large to buffer, in stream
// order. Written synchronously so a checksum failure can still unlink it.
func inflateEntry(path string, e entry, raw io.Reader) (int64, error) {
	fr := flate.NewReader(raw)
	defer func() { _ = fr.Close() }()

	f, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, defaultFileMode)
	if err != nil {
		return 0, err
	}

	sum := crc32.NewIEEE()
	n, copyErr := io.Copy(io.MultiWriter(f, sum), fr)
	closeErr := f.Close()

	switch {
	case copyErr != nil:
		_ = os.Remove(path)
		return n, copyErr
	case closeErr != nil:
		return n, closeErr
	case e.crc != 0 && sum.Sum32() != e.crc:
		_ = os.Remove(path)
		return n, fmt.Errorf("%w: entry %s", streamzip.ErrChecksum, e.Name)
	}

	if !e.Modified.IsZero() {
		_ = os.Chtimes(path, e.Modified, e.Modified)
	}
	return n, nil
}

func writeDecoded(job decodeJob, r io.Reader) error {
	f, err := os.OpenFile(job.path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, defaultFileMode)
	if err != nil {
		return err
	}

	sum := crc32.NewIEEE()
	_, copyErr := io.Copy(io.MultiWriter(f, sum), r)
	closeErr := f.Close()

	switch {
	case copyErr != nil:
		_ = os.Remove(job.path)
		return copyErr
	case closeErr != nil:
		return closeErr
	case job.crc != 0 && sum.Sum32() != job.crc:
		_ = os.Remove(job.path)
		return fmt.Errorf("%w: entry %s", streamzip.ErrChecksum, job.name)
	}

	if !job.mod.IsZero() {
		_ = os.Chtimes(job.path, job.mod, job.mod)
	}
	return nil
}

// submit reads the entry's compressed bytes and queues them, returning false
// when the entry is too large to buffer and must be handled sequentially.
func (d *decoders) submit(path string, e entry, r io.Reader) (bool, error) {
	if e.csize > maxParallelEntry {
		return false, nil
	}
	buf := <-d.free
	n, err := io.ReadFull(r, buf[:e.csize])
	if err != nil {
		d.free <- buf[:maxParallelEntry]
		return false, err
	}
	d.jobs <- decodeJob{path: path, mod: e.Modified, crc: e.crc, name: e.Name, buf: buf[:n]}
	return true, nil
}

func (d *decoders) fail(name string, err error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.err == nil {
		d.err = fmt.Errorf("extractor: %s: %w", name, err)
	}
}

func (d *decoders) failed() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.err != nil
}

func (d *decoders) close() error {
	close(d.jobs)
	d.wg.Wait()
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.err
}
