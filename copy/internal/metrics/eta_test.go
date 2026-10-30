package metrics

import (
	"fmt"
	"math"
	"strings"
	"testing"
	"time"
)

// serialRun builds the steps of a serial copy in which file i takes
// overhead + size/rate to finish. Robocopy announces the next file the moment a
// file ends, so the finish of file i and the line of file i+1 share one instant.
func serialRun(sizes []uint64, overhead time.Duration, rate func(i int) float64) ([]Step, time.Duration) {
	steps := []Step{{At: 0, Text: rule + fileLine("New File", sizes[0], `C:\f0`)}}
	var at time.Duration
	for i, size := range sizes {
		at += overhead + time.Duration(float64(size)/rate(i)*float64(time.Second))
		text := "\r  0%  \r100%  \r\n"
		if i+1 < len(sizes) {
			text += fileLine("New File", sizes[i+1], fmt.Sprintf(`C:\f%d`, i+1))
		} else {
			text += rule + summaryRows(1, uint64(len(sizes)), 0)
		}
		steps = append(steps, Step{At: at, Text: text})
	}
	return steps, at
}

func totals(sizes []uint64) (uint64, uint64) {
	var b uint64
	for _, s := range sizes {
		b += s
	}
	return uint64(len(sizes)), b
}

func etaConfig(sizes []uint64) Config {
	files, bytes := totals(sizes)
	cfg := serialConfig(files, bytes)
	cfg.AllowETA = true
	return cfg
}

func equalFiles(n int, size uint64) []uint64 {
	s := make([]uint64, n)
	for i := range s {
		s[i] = size
	}
	return s
}

func TestETAIsHiddenByDefaultEvenWhenEverythingElseAllowsIt(t *testing.T) {
	const mib = 1 << 20
	sizes := equalFiles(300, mib)
	steps, _ := serialRun(sizes, 0, func(int) float64 { return mib })
	cfg := etaConfig(sizes)
	cfg.AllowETA = false
	r := NewReplay(cfg, 0)
	var checked int
	r.Run(steps, func(at time.Duration) {
		if at >= 70*time.Second {
			checked++
			if s := r.Tracker.Snapshot(); s.ETA != nil || !strings.Contains(s.ETANote, "not been measured") {
				t.Fatalf("ETA shown at %v: %+v %q", at, s.ETA, s.ETANote)
			}
		}
	})
	if checked == 0 {
		t.Fatal("nothing checked")
	}
}

func TestETAGates(t *testing.T) {
	const mib = 1 << 20
	sizes := equalFiles(300, mib)
	steps, _ := serialRun(sizes, 0, func(int) float64 { return mib })
	files, bytes := totals(sizes)
	cases := []struct {
		name   string
		mutate func(*Config)
		at     time.Duration
		reason string // empty means an estimate is expected
	}{
		{"open", func(*Config) {}, 90 * time.Second, ""},
		{"first minute", func(*Config) {}, 59 * time.Second, "first minute"},
		{"no preview", func(c *Config) { c.Planned = nil }, 90 * time.Second, "no preview totals"},
		{"empty preview", func(c *Config) { c.Planned = &Planned{} }, 90 * time.Second, "no preview totals"},
		{"concurrent", func(c *Config) { c.Concurrent = true }, 90 * time.Second, "cannot be counted"},
		{"list only", func(c *Config) { c.List = true }, 90 * time.Second, "list-only"},
		{"too few files", func(c *Config) { c.ETAMinFiles = 100 }, 90 * time.Second, "too few files"},
		{"plan reached", func(c *Config) { c.Planned = &Planned{Files: 10, Bytes: 10 * mib} }, 90 * time.Second, "totals have been reached"},
	}
	for _, c := range cases {
		cfg := etaConfig(sizes)
		_, _ = files, bytes
		c.mutate(&cfg)
		r := NewReplay(cfg, 0)
		for _, s := range steps {
			if s.At > c.at {
				break
			}
			r.Apply(s, nil)
		}
		r.AdvanceTo(c.at, nil)
		snap := r.Tracker.Snapshot()
		if c.reason == "" {
			if snap.ETA == nil {
				t.Errorf("%s: no ETA, note %q", c.name, snap.ETANote)
			}
			continue
		}
		if snap.ETA != nil || !strings.Contains(strings.ToLower(snap.ETANote), c.reason) {
			t.Errorf("%s: ETA %+v note %q, want reason %q", c.name, snap.ETA, snap.ETANote, c.reason)
		}
	}
}

// measureFloor stops the measurement when this little time really remains: one
// file's quantum is then a large share of what is left, so relative error says nothing.
const measureFloor = 30 * time.Second

// measure replays the run and returns the worst relative ETA error from the first
// minute on, with the number of readings it covers.
func measure(t *testing.T, sizes []uint64, overhead time.Duration, rate func(int) float64) (worst float64, readings int, steady int) {
	t.Helper()
	steps, end := serialRun(sizes, overhead, rate)
	r := NewReplay(etaConfig(sizes), 0)
	r.Run(steps, func(at time.Duration) {
		if at < 60*time.Second || at > end-measureFloor {
			return
		}
		s := r.Tracker.Snapshot()
		if s.ETA == nil {
			t.Fatalf("no ETA at %v: %s", at, s.ETANote)
		}
		actual := (end - at).Seconds()
		if s.ETA.LowSeconds > s.ETA.Seconds || s.ETA.Seconds > s.ETA.HighSeconds {
			t.Fatalf("point outside range: %+v", s.ETA)
		}
		if s.ETA.Steady {
			steady++
		}
		e := math.Abs(float64(s.ETA.Seconds)-actual) / actual
		worst = math.Max(worst, e)
		readings++
	})
	return
}

// These are synthetic jobs. They check the arithmetic of the fit; they are not
// evidence for the roadmap's real-job criterion.
func TestETAFitOnSyntheticSteadyJob(t *testing.T) {
	const mib = 1 << 20
	worst, n, steady := measure(t, equalFiles(400, mib), 0, func(int) float64 { return mib })
	t.Logf("synthetic steady job: worst ETA error %.2f%% over %d readings", 100*worst, n)
	if n < 100 || worst > 0.05 {
		t.Fatalf("worst error %.1f%% over %d readings", 100*worst, n)
	}
	if steady != n {
		t.Fatalf("a steady job should give point estimates: %d of %d", steady, n)
	}
}

func TestETAFitSeparatesPerFileTimeFromByteRate(t *testing.T) {
	const kib = 1 << 10
	// Each cycle: 40 small files, then one large one. Per-file time 0.2 s, 20 MiB/s.
	var sizes []uint64
	for c := 0; c < 12; c++ {
		for i := 0; i < 40; i++ {
			sizes = append(sizes, 4*kib)
		}
		sizes = append(sizes, 256<<20)
	}
	worst, n, _ := measure(t, sizes, 200*time.Millisecond, func(int) float64 { return 20 << 20 })
	t.Logf("synthetic small-and-large job: worst ETA error %.2f%% over %d readings", 100*worst, n)
	if n < 50 || worst > 0.15 {
		t.Fatalf("worst error %.1f%% over %d readings", 100*worst, n)
	}
}

func TestETAIsARangeWhenTheRateIsUnsteady(t *testing.T) {
	const mib = 1 << 20
	sizes := equalFiles(400, mib)
	// 8 MiB/s for the first 150 files, then 1 MiB/s: the recent rate and the run average disagree.
	rate := func(i int) float64 {
		if i < 150 {
			return 8 * mib
		}
		return mib
	}
	steps, end := serialRun(sizes, 0, rate)
	r := NewReplay(etaConfig(sizes), 0)
	var wide int
	r.Run(steps, func(at time.Duration) {
		if at < 150*time.Second || at > end-measureFloor {
			return
		}
		s := r.Tracker.Snapshot()
		if s.ETA == nil {
			return
		}
		if !s.ETA.Steady && s.ETA.LowSeconds < s.ETA.HighSeconds {
			wide++
			actual := int64((end - at).Seconds())
			if s.ETA.LowSeconds > actual || FormatETA(*s.ETA) == "" || !strings.Contains(FormatETA(*s.ETA), " to ") {
				t.Fatalf("range %+v does not describe %d s remaining (%s)", s.ETA, actual, FormatETA(*s.ETA))
			}
		}
	})
	if wide == 0 {
		t.Fatal("an unsteady job never gave a range")
	}
}

func TestEstimateOfAnEmptyOrStuckWindowGivesNothing(t *testing.T) {
	s := newSeries(epoch, 2*time.Minute)
	f := newSeries(epoch, 2*time.Minute)
	plan := Planned{Files: 10, Bytes: 100}
	now := epoch.Add(time.Minute)
	if _, ok, why := estimate(s, f, epoch, now, time.Minute, 1, plan); ok || !strings.Contains(why, "too few files") {
		t.Fatalf("ok=%v why=%q", ok, why)
	}
	// Files finished but no bytes (zero-byte files only).
	for i := 1; i <= 5; i++ {
		f.add(epoch.Add(time.Duration(i)*10*time.Second), uint64(i))
	}
	if _, ok, why := estimate(s, f, epoch, now, time.Minute, 1, plan); ok {
		t.Fatalf("ok=%v why=%q", ok, why)
	}
	// Steady completions, then a file that has been running far longer than any before it.
	for i := 1; i <= 5; i++ {
		s.add(epoch.Add(time.Duration(i)*10*time.Second), uint64(i)*10)
	}
	if est, ok, why := estimate(s, f, epoch, epoch.Add(55*time.Second), time.Minute, 1, plan); !ok || est.Seconds < 40 || est.Seconds > 55 {
		t.Fatalf("est=%+v ok=%v why=%q", est, ok, why)
	}
	if _, ok, why := estimate(s, f, epoch, epoch.Add(5*time.Minute), 10*time.Minute, 1, plan); ok || !strings.Contains(why, "taking much longer") {
		t.Fatalf("ok=%v why=%q", ok, why)
	}
}
