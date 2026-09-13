package extractor

import (
	"io"
	"sync/atomic"
	"time"
)

const progressInterval = 100 * time.Millisecond

// written counts bytes that have reached disk. Decoding and writing fan out
// across goroutines, so every write path adds to the same counter.
type written struct{ n atomic.Int64 }

func (w *written) add(n int64) { w.n.Add(n) }

func (w *written) load() int64 { return w.n.Load() }

// Write lets the counter join an io.MultiWriter, so the decode paths update it
// as bytes flow rather than only when a file finishes.
func (w *written) Write(p []byte) (int, error) {
	w.add(int64(len(p)))
	return len(p), nil
}

// progressReader counts archive bytes as they are consumed. It sits above the
// truncator so it measures real progress through the archive even when the
// source is being kept and nothing is reclaimed. Bytes extracted come from the
// write paths instead, since output is the work the caller is waiting on and
// there is more of it than there is archive.
type progressReader struct {
	r     io.Reader
	out   *written
	total int64
	done  int64
	last  time.Time
	fn    func(done, total, extracted int64)
}

func newProgressReader(r io.Reader, total int64, out *written, fn func(done, total, extracted int64)) io.Reader {
	if fn == nil {
		return r
	}
	return &progressReader{r: r, out: out, total: total, fn: fn, last: time.Now()}
}

func (p *progressReader) Read(b []byte) (int, error) {
	n, err := p.r.Read(b)
	p.done += int64(n)
	if err != nil || time.Since(p.last) >= progressInterval {
		p.last = time.Now()
		p.fn(p.done, p.total, p.out.load())
	}
	return n, err
}
