package sync

import (
	"fmt"
	"io"
	"log"
	"time"
)

// Progress wraps a reader and logs how far it got every couple of seconds, so a long
// transfer does not look hung.
type Progress struct {
	r     io.Reader
	label string
	base  int64 // bytes already held before this reader started (resumed download)
	total int64 // full size, or <= 0 when unknown
	n     int64
	start time.Time
	last  time.Time
}

// NewProgress logs under label. total is the full size (0 or negative if unknown) and
// base the bytes that were already transferred before this reader.
func NewProgress(r io.Reader, label string, base, total int64) *Progress {
	now := time.Now()
	return &Progress{r: r, label: label, base: base, total: total, start: now, last: now}
}

func (p *Progress) Read(b []byte) (int, error) {
	n, err := p.r.Read(b)
	p.n += int64(n)
	if now := time.Now(); now.Sub(p.last) >= 2*time.Second {
		p.last = now
		p.log()
	}
	return n, err
}

func (p *Progress) log() {
	done := p.base + p.n
	rate := float64(p.n) / 1e6 / time.Since(p.start).Seconds()
	if p.total > 0 {
		log.Printf("  %s: %.1f / %.1f MB (%d%%) %.1f MB/s", p.label, mb(done), mb(p.total), done*100/p.total, rate)
	} else {
		log.Printf("  %s: %.1f MB %.1f MB/s", p.label, mb(done), rate)
	}
}

func mb(n int64) float64 { return float64(n) / 1e6 }

// Size renders a byte count for log lines.
func Size(n int64) string { return fmt.Sprintf("%.1f MB", mb(n)) }
