package metrics

import "time"

// Step is one recorded input at an offset from the start of a replay: decoded
// log text, an I/O reading, or both.
type Step struct {
	At   time.Duration
	Text string
	IO   *IOSample
}

// Replay drives a Tracker with a FakeClock from recorded steps, so a captured
// run gives the same readings every time. Between steps it ticks, as the live
// monitor does, so quiet stretches and timelines behave as in a real run.
type Replay struct {
	Clock   *FakeClock
	Tracker *Tracker
	start   time.Time
	tick    time.Duration
	at      time.Duration
}

// NewReplay starts a tracker at a fixed instant. tick defaults to the live
// monitor's 500 ms.
func NewReplay(cfg Config, tick time.Duration) *Replay {
	if tick <= 0 {
		tick = 500 * time.Millisecond
	}
	start := time.Date(2026, time.October, 29, 12, 0, 0, 0, time.UTC)
	clock := NewFakeClock(start)
	cfg.Clock = clock
	return &Replay{Clock: clock, Tracker: NewTracker(cfg), start: start, tick: tick}
}

// AdvanceTo moves time to the offset, ticking on every multiple of the tick
// interval on the way (so ticks fall on the same offsets whatever the steps are).
// observe, if set, is called after each tick and at the offset reached.
func (r *Replay) AdvanceTo(at time.Duration, observe func(time.Duration)) {
	for next := (r.at/r.tick + 1) * r.tick; next <= at; next += r.tick {
		r.at = next
		r.Clock.Set(r.start.Add(r.at))
		r.Tracker.Tick()
		if observe != nil {
			observe(r.at)
		}
	}
	if at > r.at {
		r.at = at
		r.Clock.Set(r.start.Add(at))
		r.Tracker.Tick()
		if observe != nil {
			observe(at)
		}
	}
}

// Apply advances to the step's offset and feeds it.
func (r *Replay) Apply(s Step, observe func(time.Duration)) {
	r.AdvanceTo(s.At, observe)
	if s.Text != "" {
		r.Tracker.Log(s.Text)
	}
	if s.IO != nil {
		r.Tracker.IO(*s.IO)
	}
	if observe != nil {
		observe(r.at)
	}
}

// Run applies all steps in order.
func (r *Replay) Run(steps []Step, observe func(time.Duration)) {
	for _, s := range steps {
		r.Apply(s, observe)
	}
}

// Offset is the replay's current offset from its start.
func (r *Replay) Offset() time.Duration { return r.at }
