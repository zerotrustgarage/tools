// Package metrics turns Robocopy's live output and process I/O samples into
// activity, rate, stall and (when the signals allow) progress and ETA readings
// (design section 6). Everything is driven by an injectable Clock and by
// explicit inputs, so a recorded run can be replayed deterministically.
package metrics

import (
	"sync"
	"time"
)

// Clock is the only source of time the tracker uses.
type Clock interface {
	Now() time.Time
}

// SystemClock reads the wall and monotonic clocks.
type SystemClock struct{}

func (SystemClock) Now() time.Time { return time.Now() }

// FakeClock is a manually advanced clock for tests and replays. It never moves
// backwards.
type FakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func NewFakeClock(start time.Time) *FakeClock { return &FakeClock{now: start} }

func (c *FakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

// Set moves the clock forward to t; an earlier t is ignored.
func (c *FakeClock) Set(t time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if t.After(c.now) {
		c.now = t
	}
}

func (c *FakeClock) Advance(d time.Duration) {
	if d <= 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}
