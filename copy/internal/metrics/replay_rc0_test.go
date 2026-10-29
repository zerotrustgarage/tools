package metrics

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"
)

// The RC0 timing captures (internal/spike/testdata/rc0-*-en/timing-*.events.base64)
// record, with elapsed milliseconds, every console write of a controlled 4 MiB
// copy (throttled with /IoRate) and a 100 ms sample of the process I/O counters.
// They are the recorded inputs of the replays below.

type rc0Event struct {
	Kind      string `json:"kind"`
	ElapsedMS int64  `json:"elapsed_ms"`
	Stream    string `json:"stream"`
	Data      string `json:"data"`
	Counters  struct {
		ReadBytes  uint64 `json:"ReadBytes"`
		WriteBytes uint64 `json:"WriteBytes"`
	} `json:"counters"`
}

const rc0FileSize = 4194304

func loadRC0(t testing.TB, release, mode string) []Step {
	t.Helper()
	path := filepath.Join("..", "spike", "testdata", release, "timing-"+mode+".events.base64")
	encoded, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := base64.StdEncoding.DecodeString(string(bytes.TrimSpace(encoded)))
	if err != nil {
		t.Fatal(err)
	}
	var steps []Step
	scanner := bufio.NewScanner(bytes.NewReader(raw))
	scanner.Buffer(make([]byte, 1<<20), 1<<24)
	for scanner.Scan() {
		var ev rc0Event
		if err := json.Unmarshal(scanner.Bytes(), &ev); err != nil {
			t.Fatal(err)
		}
		at := time.Duration(ev.ElapsedMS) * time.Millisecond
		switch ev.Kind {
		case "io":
			steps = append(steps, Step{At: at, IO: &IOSample{ReadBytes: ev.Counters.ReadBytes}})
		case "pipe":
			data, err := base64.StdEncoding.DecodeString(ev.Data)
			if err != nil {
				t.Fatal(err)
			}
			// The console stream mixes narrow bytes and UTF-16 fragments (RC0 question 2). Only
			// ASCII structure matters to the scanner, so drop the BOM and the NUL halves.
			data = bytes.ReplaceAll(bytes.TrimPrefix(data, []byte{0xff, 0xfe}), []byte{0}, nil)
			steps = append(steps, Step{At: at, Text: string(data)})
		}
	}
	if err := scanner.Err(); err != nil || len(steps) == 0 {
		t.Fatalf("fixture %s: %v (%d steps)", path, err, len(steps))
	}
	sort.SliceStable(steps, func(i, j int) bool { return steps[i].At < steps[j].At })
	return steps
}

var rc0Fixtures = []struct{ release, mode string }{
	{"rc0-2022-en", "serial"}, {"rc0-2022-en", "mt8"}, {"rc0-2025-en", "serial"}, {"rc0-2025-en", "mt8"},
}

func TestRC0SerialReplayCountsTheFileOnlyWhenItIsFinished(t *testing.T) {
	for _, release := range []string{"rc0-2022-en", "rc0-2025-en"} {
		steps := loadRC0(t, release, "serial")
		r := NewReplay(serialConfig(1, rc0FileSize), 0)
		var announcedAt, lastPercent time.Duration
		var maxRead uint64
		r.Run(steps, func(at time.Duration) {
			s := r.Tracker.Snapshot()
			if s.Seen.Files == 1 && announcedAt == 0 {
				announcedAt = at
			}
			if s.Completed.Files == 0 && s.Completed.Bytes != 0 {
				t.Fatalf("%s: bytes without files at %v", release, at)
			}
			if s.Completed.Files == 1 && lastPercent == 0 {
				lastPercent = at
			}
			if s.Activity.ReadBytes > maxRead {
				maxRead = s.Activity.ReadBytes
			}
			// The announcement is at about 15 ms and the finished file is seen only at the end.
			if at < 3500*time.Millisecond && (s.Completed.Files != 0 || s.Completed.Bytes != 0) {
				t.Fatalf("%s: %d bytes completed at %v, before the file could have finished", release, s.Completed.Bytes, at)
			}
			if s.Status == StatusStalled || s.Status == StatusRetrying {
				t.Fatalf("%s: status %s at %v", release, s.Status, at)
			}
		})
		if announcedAt == 0 || announcedAt > 100*time.Millisecond {
			t.Fatalf("%s: file announced at %v", release, announcedAt)
		}
		if lastPercent < 3700*time.Millisecond || lastPercent > 3900*time.Millisecond {
			t.Fatalf("%s: file counted at %v", release, lastPercent)
		}
		if maxRead != rc0FileSize {
			t.Fatalf("%s: read counter peaked at %d", release, maxRead)
		}
		final := r.Tracker.Finish(summaryOf(1, rc0FileSize))
		if final.Source != "summary" || final.LogCompleted == nil || final.LogCompleted.Bytes != rc0FileSize || final.Completed.Bytes != rc0FileSize {
			t.Fatalf("%s: final %+v", release, final)
		}
		if final.ActivityReadBytes != rc0FileSize || final.ActivityPeakBytesPerSec != nil || final.PeakBytesPerSec != nil {
			t.Fatalf("%s: activity %+v", release, final)
		}
		if final.StalledMS != 0 || len(final.Timeline) < 5 || len(final.Timeline) > TimelineCap {
			t.Fatalf("%s: stalled %d timeline %d", release, final.StalledMS, len(final.Timeline))
		}
	}
}

func TestRC0ConcurrentReplayShowsActivityButNoCompletedTotals(t *testing.T) {
	for _, release := range []string{"rc0-2022-en", "rc0-2025-en"} {
		steps := loadRC0(t, release, "mt8")
		cfg := serialConfig(1, rc0FileSize)
		cfg.Concurrent = true
		r := NewReplay(cfg, 0)
		var sawActivity bool
		r.Run(steps, func(at time.Duration) {
			s := r.Tracker.Snapshot()
			if s.Completed.Known || s.Completed.Bytes != 0 || s.Progress != nil || s.Rate.CompletedBytesPerSec != nil {
				t.Fatalf("%s: /MT claimed completion or progress at %v: %+v", release, at, s)
			}
			if s.Activity.BytesPerSec != nil && *s.Activity.BytesPerSec > 0 {
				sawActivity = true
			}
		})
		if !sawActivity {
			t.Fatalf("%s: no transfer activity was seen", release)
		}
		final := r.Tracker.Finish(summaryOf(1, rc0FileSize))
		if final.Completed.Bytes != rc0FileSize || final.LogCompleted != nil || final.Source != "summary" {
			t.Fatalf("%s: final %+v", release, final)
		}
		if final.AverageBytesPerSec == nil || final.ActivityAverageBytesPerSec == nil {
			t.Fatalf("%s: averages missing: %+v", release, final)
		}
	}
}

func TestRC0ActivityRateTracksTheThrottledCopy(t *testing.T) {
	// /IoRate throttled the copy to about 1 MiB per second.
	steps := loadRC0(t, "rc0-2025-en", "serial")
	r := NewReplay(serialConfig(1, rc0FileSize), 0)
	var rate uint64
	r.Run(steps, func(at time.Duration) {
		if at == 3*time.Second {
			s := r.Tracker.Snapshot()
			if s.Activity.BytesPerSec != nil {
				rate = *s.Activity.BytesPerSec
			}
		}
	})
	if rate < 900_000 || rate > 1_300_000 {
		t.Fatalf("activity rate at 3 s is %d bytes per second", rate)
	}
}

// The roadmap asks for an ETA within 15% after the first minute. The RC0 captures are
// single throttled files of about 3.8 seconds, so they cannot support that claim. This
// test records what the replays do show and keeps the ETA hidden.
func TestRC0FixturesCannotSupportTheETAClaimSoItStaysHidden(t *testing.T) {
	for _, fx := range rc0Fixtures {
		steps := loadRC0(t, fx.release, fx.mode)
		duration := steps[len(steps)-1].At
		if duration >= DefaultETAMinElapsed {
			t.Fatalf("%s %s lasts %v: it can now support a minute-long ETA test", fx.release, fx.mode, duration)
		}
		// Production configuration: ETA is never shown.
		cfg := serialConfig(1, rc0FileSize)
		cfg.Concurrent = fx.mode == "mt8"
		r := NewReplay(cfg, 0)
		r.Run(steps, func(time.Duration) {
			if s := r.Tracker.Snapshot(); s.ETA != nil || s.ETANote == "" {
				t.Fatalf("%s %s: ETA shown by default: %+v", fx.release, fx.mode, s.ETA)
			}
		})
		// Even with the gates opened wide, completed-file accounting has no rate to fit.
		open := cfg
		open.AllowETA, open.ETAMinElapsed, open.ETAMinFiles = true, time.Nanosecond, 1
		r = NewReplay(open, 0)
		var estimates int
		r.Run(steps, func(time.Duration) {
			if r.Tracker.Snapshot().ETA != nil {
				estimates++
			}
		})
		if estimates != 0 {
			t.Fatalf("%s %s: %d ETA readings from a one-file job", fx.release, fx.mode, estimates)
		}
	}
}

// TestRC0ActivityETAError measures, and does not claim, what an ETA built from the
// transfer-activity signal would have missed by on these captures. Activity is never
// shown as completed, so this is information for rc1.md only.
func TestRC0ActivityETAError(t *testing.T) {
	for _, fx := range rc0Fixtures {
		steps := loadRC0(t, fx.release, fx.mode)
		end := steps[len(steps)-1].At
		r := NewReplay(serialConfig(1, rc0FileSize), 0)
		var worst, sum float64
		var n int
		r.Run(steps, func(at time.Duration) {
			if at < time.Second || at >= end-500*time.Millisecond {
				return
			}
			s := r.Tracker.Snapshot()
			if s.Activity.BytesPerSec == nil || *s.Activity.BytesPerSec == 0 {
				return
			}
			remaining := float64(rc0FileSize - s.Activity.ReadBytes)
			eta := remaining / float64(*s.Activity.BytesPerSec)
			actual := (end - at).Seconds()
			e := math.Abs(eta-actual) / actual
			sum += e
			n++
			if e > worst {
				worst = e
			}
		})
		if n == 0 {
			t.Fatalf("%s %s: no observations", fx.release, fx.mode)
		}
		t.Logf("%s %s: activity-based ETA over %d readings between 1 s and %.1f s, mean error %.1f%%, worst %.1f%% (job lasts %.2f s, so none is after the first minute)", fx.release, fx.mode, n, (end - 500*time.Millisecond).Seconds(), 100*sum/float64(n), 100*worst, end.Seconds())
	}
}
