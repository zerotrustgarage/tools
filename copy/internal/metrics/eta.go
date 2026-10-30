package metrics

import (
	"math"
	"time"
)

// An ETA follows design section 6: the time left is the remaining bytes over the
// byte rate plus the remaining files times a per-file overhead, both fitted over
// the last minute so a tree of small files is not predicted from large-file
// speed. It is shown as a range when the estimates disagree.
//
// Finished files arrive as steps, so the fit works between completions: each
// interval from one completion to the next took exactly the time of the files
// that completed in it, and the time since the last completion is the part of
// the next file already spent. A fit over fixed time buckets would blame a large
// file's time on the bucket where it happened to end.
//
// It appears only when all of these hold, and each reason is reported:
//
//   - the configuration allows it (off until measured on real long jobs);
//   - a preview gave totals and finished files are countable (serial copy);
//   - a minute of copy time has passed and at least ETAMinFiles files finished
//     in the fit window;
//   - there is something left to copy, and the file in progress is not taking
//     much longer than the fit expects.
const steadyBand = 1.25

const etaDisabled = "ETA is not shown yet: it has not been measured within 15% on real copies that run longer than a minute."

func (t *Tracker) eta(now time.Time) (*ETA, string) {
	switch {
	case !t.cfg.AllowETA:
		return nil, etaDisabled
	case t.cfg.List:
		return nil, "No ETA in a list-only phase."
	case t.cfg.Planned == nil || (t.cfg.Planned.Bytes == 0 && t.cfg.Planned.Files == 0):
		return nil, "No ETA: there are no preview totals to count down from."
	case !t.completedKnown():
		return nil, "No ETA: finished files cannot be counted while this runs."
	case now.Sub(t.start) < t.cfg.ETAMinElapsed:
		return nil, "No ETA during the first minute."
	}
	est, ok, why := estimate(t.bytesS, t.filesS, t.start, now, t.cfg.FitWindow, t.cfg.ETAMinFiles, *t.cfg.Planned)
	if !ok {
		return nil, why
	}
	return est, ""
}

// estimate is the pure ETA fit, separated for tests.
func estimate(bytesS, filesS *series, start, now time.Time, window time.Duration, minFiles int, planned Planned) (*ETA, bool, string) {
	doneBytes, doneFiles := bytesS.latest(), filesS.latest()
	remainingBytes, remainingFiles := sub(planned.Bytes, doneBytes), sub(planned.Files, doneFiles)
	if remainingBytes == 0 && remainingFiles == 0 {
		return nil, false, "No ETA: the preview totals have been reached."
	}
	from := now.Add(-window)
	first := 0
	for first < len(bytesS.pts) && bytesS.pts[first].t.Before(from) {
		first++
	}
	if first > 0 {
		first-- // the completion just before the window starts the first interval
	}
	events := bytesS.pts[first:]
	if len(events) < 2 {
		return nil, false, "No ETA: too few files finished in the last minute to fit a rate."
	}
	var sff, sfb, sbb, stf, stb, sumB, sumT, sumF float64
	for i := 1; i < len(events); i++ {
		a, b := events[i-1], events[i]
		dt := b.t.Sub(a.t).Seconds()
		dB := float64(b.v - a.v)
		dF := float64(filesS.at(b.t) - filesS.at(a.t))
		sff += dF * dF
		sfb += dF * dB
		sbb += dB * dB
		stf += dt * dF
		stb += dt * dB
		sumB += dB
		sumF += dF
		sumT += dt
	}
	if int(sumF) < minFiles {
		return nil, false, "No ETA: too few files finished in the last minute to fit a rate."
	}
	if sumB <= 0 || sumT <= 0 {
		return nil, false, "No ETA: no bytes finished in the last minute."
	}
	rb, rf := float64(remainingBytes), float64(remainingFiles)
	bytesOnly := rb * sumT / sumB
	// Least squares for dt = c*files + k*bytes through the origin.
	fit := bytesOnly
	if det := sff*sbb - sfb*sfb; det > 1e-9*sff*sbb && det > 0 {
		c := (stf*sbb - stb*sfb) / det
		k := (stb*sff - stf*sfb) / det
		if c >= 0 && k > 0 && !math.IsNaN(c) && !math.IsNaN(k) {
			fit = rf*c + rb*k
		}
	}
	last := events[len(events)-1].t
	runAverage := rb * last.Sub(start).Seconds() / float64(doneBytes)
	// Time already spent on the file in progress comes off every estimate.
	spent := now.Sub(last).Seconds()
	low, high := math.Inf(1), 0.0
	for _, v := range []float64{fit, bytesOnly, runAverage} {
		if v > 0 && !math.IsInf(v, 0) && !math.IsNaN(v) {
			low, high = math.Min(low, v), math.Max(high, v)
		}
	}
	if math.IsInf(low, 1) || fit <= 0 {
		return nil, false, "No ETA: the recent rate gives no usable estimate."
	}
	if spent > fit {
		return nil, false, "No ETA: the file in progress is taking much longer than the recent files did."
	}
	left := func(v float64) int64 { return int64(math.Round(math.Max(v-spent, 0))) }
	eta := &ETA{
		Seconds: left(fit), LowSeconds: left(low), HighSeconds: left(high),
		Basis: "Remaining bytes over the byte rate plus remaining files times the per-file time, fitted over the last minute.",
	}
	eta.Steady = high <= steadyBand*low
	return eta, true, ""
}

func sub(a, b uint64) uint64 {
	if b >= a {
		return 0
	}
	return a - b
}
