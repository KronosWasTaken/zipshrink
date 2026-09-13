package extractor

import (
	"io"
	"time"
)

const progressInterval = 100 * time.Millisecond

// progressReader counts archive bytes as they are consumed. It sits above the
// truncator so it measures real progress through the archive even when the
// source is being kept and nothing is reclaimed.
type progressReader struct {
	r     io.Reader
	total int64
	done  int64
	last  time.Time
	fn    func(done, total int64)
}

func newProgressReader(r io.Reader, total int64, fn func(done, total int64)) io.Reader {
	if fn == nil {
		return r
	}
	return &progressReader{r: r, total: total, fn: fn, last: time.Now()}
}

func (p *progressReader) Read(b []byte) (int, error) {
	n, err := p.r.Read(b)
	p.done += int64(n)
	if err != nil || time.Since(p.last) >= progressInterval {
		p.last = time.Now()
		p.fn(p.done, p.total)
	}
	return n, err
}
