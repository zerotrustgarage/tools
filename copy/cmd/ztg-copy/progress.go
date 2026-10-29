package main

import (
	"io"
	"sync"
	"time"

	"github.com/zerotrustgarage/tools/copy/internal/metrics"
	copyrun "github.com/zerotrustgarage/tools/copy/internal/run"
)

// progressEvery is how often a running phase prints a reading. A status change
// (for example active to stalled) prints at once.
const progressEvery = 10 * time.Second

// progressPrinter passes the runner's own lines through and adds a plain reading of
// the running phase: completed totals, rates and transfer activity, a percentage or
// an ETA only when the signals allow one, and the stall or retry status. It is
// written for logs read by people and RMM tools: one line, no cursor control.
type progressPrinter struct {
	out   io.Writer
	every time.Duration
	now   func() time.Time

	mu     sync.Mutex
	last   time.Time
	phase  string
	status string
}

func newProgressPrinter(out io.Writer) *progressPrinter {
	return &progressPrinter{out: out, every: progressEvery, now: time.Now}
}

func (p *progressPrinter) Write(data []byte) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.out.Write(data)
}

// PhaseStarted and PhaseFinished forward to the wrapped writer if it wants them.
func (p *progressPrinter) PhaseStarted(name string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.phase, p.status, p.last = name, "", time.Time{}
	if observer, ok := p.out.(interface{ PhaseStarted(string) }); ok {
		observer.PhaseStarted(name)
	}
}

func (p *progressPrinter) PhaseFinished(phase copyrun.Phase) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if observer, ok := p.out.(interface{ PhaseFinished(copyrun.Phase) }); ok {
		observer.PhaseFinished(phase)
	}
}

// MetricsUpdated prints at most one line per interval, and one on a status change.
func (p *progressPrinter) MetricsUpdated(phase string, s metrics.Snapshot) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if s.Status == metrics.StatusFinished {
		return // the runner prints the phase totals
	}
	now := p.now()
	changed := phase != p.phase || s.Status != p.status
	if !changed && !p.last.IsZero() && now.Sub(p.last) < p.every {
		return
	}
	// The first reading of a phase waits for its first interval unless something notable happened.
	if p.last.IsZero() && s.Status == metrics.StatusStarting {
		p.last = now
		p.phase, p.status = phase, s.Status
		return
	}
	p.phase, p.status, p.last = phase, s.Status, now
	io.WriteString(p.out, s.Line()+"\n")
}
