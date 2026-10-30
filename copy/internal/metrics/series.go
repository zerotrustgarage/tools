package metrics

import (
	"sort"
	"time"
)

type point struct {
	t time.Time
	v uint64
}

// series is a non-decreasing cumulative counter observed over time. Between
// observations the value is a step (the last observation holds), so a rate
// never invents progress that was not seen.
type series struct {
	start time.Time
	keep  time.Duration
	pts   []point
}

const (
	coalesce   = 100 * time.Millisecond
	maxSamples = 8192
)

func newSeries(start time.Time, keep time.Duration) *series {
	return &series{start: start, keep: keep, pts: []point{{start, 0}}}
}

func (s *series) latest() uint64 { return s.pts[len(s.pts)-1].v }

// add records v at t. A value below the latest is ignored (counters only grow).
// An unchanged value adds nothing: the step already holds it.
func (s *series) add(t time.Time, v uint64) {
	last := s.pts[len(s.pts)-1]
	if v <= last.v || t.Before(last.t) {
		return
	}
	if t.Sub(last.t) < coalesce && len(s.pts) > 1 {
		s.pts[len(s.pts)-1] = point{t, v}
	} else {
		s.pts = append(s.pts, point{t, v})
	}
	// Keep one anchor at or before the oldest time a window can ask about.
	cut := 0
	for cut+1 < len(s.pts) && !s.pts[cut+1].t.After(t.Add(-s.keep)) {
		cut++
	}
	if over := len(s.pts) - maxSamples; over > cut {
		cut = over
	}
	if cut > 0 {
		s.pts = append(s.pts[:0:0], s.pts[cut:]...)
	}
}

// at returns the value in force at t: the last observation at or before t, or
// zero before the first one.
func (s *series) at(t time.Time) uint64 {
	i := sort.Search(len(s.pts), func(i int) bool { return s.pts[i].t.After(t) })
	if i == 0 {
		return 0
	}
	return s.pts[i-1].v
}

// rate is the mean rate over the last window ending at now, in units per
// second. The window is clipped to the start of the series, and a span shorter
// than minSpan gives no rate rather than a noisy one.
func (s *series) rate(now time.Time, window, minSpan time.Duration) (float64, bool) {
	from := now.Add(-window)
	if from.Before(s.start) {
		from = s.start
	}
	span := now.Sub(from)
	if span < minSpan || span <= 0 {
		return 0, false
	}
	return float64(s.latest()-s.at(from)) / span.Seconds(), true
}
