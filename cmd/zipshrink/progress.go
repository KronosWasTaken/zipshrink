package main

import (
	"fmt"
	"os"
	"strings"
	"time"
)

// Narrow enough that the whole line fits an 80 column terminal, since the bar
// is redrawn with a carriage return and wrapping would leave a trail.
const barWidth = 22

// progressBar renders a single updating line. It is silent when stdout is not
// a terminal, so redirected output and CI logs stay clean.
type progressBar struct {
	start time.Time
	tty   bool
	shown bool
}

func newProgressBar() *progressBar {
	fi, err := os.Stdout.Stat()
	tty := err == nil && fi.Mode()&os.ModeCharDevice != 0
	return &progressBar{start: time.Now(), tty: tty}
}

// update reports both rates: in is archive consumed, out is data written.
// They differ because extraction writes more bytes than it reads, so archive
// throughput alone understates the work being done.
func (p *progressBar) update(done, total, extracted int64) {
	elapsed := time.Since(p.start).Seconds()
	if !p.tty || total <= 0 || elapsed <= 0 {
		return
	}
	p.shown = true

	frac := float64(done) / float64(total)
	filled := int(frac * barWidth)
	bar := strings.Repeat("=", filled) + strings.Repeat(" ", barWidth-filled)

	in := float64(done) / elapsed
	eta := "--:--"
	if in > 0 && done < total {
		remaining := time.Duration(float64(total-done)/in) * time.Second
		eta = fmt.Sprintf("%d:%02d", int(remaining.Minutes()), int(remaining.Seconds())%60)
	}

	fmt.Printf("\r  [%s] %3.0f%%  in %s/s  out %s/s  ETA %s ",
		bar, frac*100, formatBytes(int64(in)), formatBytes(int64(float64(extracted)/elapsed)), eta)
}

// clear wipes the line so other output is not written over the bar.
func (p *progressBar) clear() {
	if p.tty && p.shown {
		fmt.Printf("\r%s\r", strings.Repeat(" ", barWidth+56))
	}
}

func (p *progressBar) done() {
	p.clear()
	p.shown = false
}
