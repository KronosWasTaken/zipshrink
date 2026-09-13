package main

import (
	"fmt"
	"os"
	"strings"
	"time"
)

const barWidth = 28

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

func (p *progressBar) update(done, total int64) {
	if !p.tty || total <= 0 {
		return
	}
	p.shown = true

	frac := float64(done) / float64(total)
	filled := int(frac * barWidth)
	bar := strings.Repeat("=", filled) + strings.Repeat(" ", barWidth-filled)

	elapsed := time.Since(p.start).Seconds()
	rate := float64(done) / elapsed
	eta := "--:--"
	if rate > 0 && done < total {
		remaining := time.Duration(float64(total-done)/rate) * time.Second
		eta = fmt.Sprintf("%d:%02d", int(remaining.Minutes()), int(remaining.Seconds())%60)
	}

	fmt.Printf("\r  [%s] %3.0f%%  %s/s  %s / %s  ETA %s ",
		bar, frac*100, formatBytes(int64(rate)), formatBytes(done), formatBytes(total), eta)
}

// clear wipes the line so other output is not written over the bar.
func (p *progressBar) clear() {
	if p.tty && p.shown {
		fmt.Printf("\r%s\r", strings.Repeat(" ", barWidth+58))
	}
}

func (p *progressBar) done() {
	p.clear()
	p.shown = false
}
