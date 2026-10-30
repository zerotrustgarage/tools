package metrics

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zerotrustgarage/tools/copy/internal/job"
	"github.com/zerotrustgarage/tools/copy/internal/parse"
)

func summaryOf(files, bytes uint64) *parse.Summary {
	return &parse.Summary{Files: parse.Counts{Total: files, Copied: files}, Bytes: parse.Counts{Total: bytes, Copied: bytes}}
}

func TestAnnouncementsAreNotCompletedBytes(t *testing.T) {
	r := NewReplay(serialConfig(3, 3000), 0)
	r.Apply(Step{At: sec(1), Text: rule + fileLine("New File", 1000, `C:\a.bin`)}, nil)
	snap := r.Tracker.Snapshot()
	if snap.Completed.Bytes != 0 || snap.Completed.Files != 0 {
		t.Fatalf("announced file counted as completed: %+v", snap.Completed)
	}
	if snap.Seen.Files != 1 || snap.Seen.Bytes != 1000 {
		t.Fatalf("seen %+v", snap.Seen)
	}
	if snap.Current == nil || snap.Current.Path != `C:\a.bin` || !snap.Current.Announced {
		t.Fatalf("current %+v", snap.Current)
	}
	// 100% alone does not complete it either; something after it must show Robocopy moved on.
	r.Apply(Step{At: sec(2), Text: "\r  0%  \r100%  "}, nil)
	if snap = r.Tracker.Snapshot(); snap.Completed.Files != 0 {
		t.Fatalf("100%% alone counted: %+v", snap.Completed)
	}
	if snap.Current.Percent == nil || *snap.Current.Percent != 100 {
		t.Fatalf("file percent %+v", snap.Current)
	}
	r.Apply(Step{At: sec(3), Text: "\r\n" + fileLine("New File", 2000, `C:\b.bin`)}, nil)
	snap = r.Tracker.Snapshot()
	if snap.Completed.Files != 1 || snap.Completed.Bytes != 1000 {
		t.Fatalf("a finished file was not counted once its successor appeared: %+v", snap.Completed)
	}
	if snap.Progress == nil || snap.Progress.BytesPercent == nil || *snap.Progress.BytesPercent != 33.33 {
		t.Fatalf("progress %+v", snap.Progress)
	}
}

func TestLastFileIsSettledByTheSummary(t *testing.T) {
	r := NewReplay(serialConfig(2, 300), 0)
	r.Apply(Step{At: sec(1), Text: copied(100, `C:\a.bin`) + copied(200, `C:\b.bin`)}, nil)
	if got := r.Tracker.Snapshot().Completed; got.Files != 1 {
		t.Fatalf("second file counted before anything followed it: %+v", got)
	}
	r.Apply(Step{At: sec(2), Text: rule + summaryRows(1, 2, 300)}, nil)
	if got := r.Tracker.Snapshot().Completed; got.Files != 2 || got.Bytes != 300 {
		t.Fatalf("completed %+v", got)
	}
	final := r.Tracker.Finish(summaryOf(2, 300))
	if final.Source != "summary" || final.Completed.Bytes != 300 || final.LogCompleted.Bytes != 300 {
		t.Fatalf("final %+v", final)
	}
}

func TestFilesWithoutAFullPercentageOrWithAnErrorAreNotCompleted(t *testing.T) {
	r := NewReplay(serialConfig(5, 5000), 0)
	text := rule +
		fileLine("same", 1000, `C:\skipped.bin`) + "\r\n" + // a skipped file has no percentage
		fileLine("New File", 1000, `C:\locked.bin`) + "\r  0%  \r100%  \r\n" + errorHeader(32, `C:\locked.bin`) + // retried after an error
		fileLine("New File", 1000, `C:\partial.bin`) + "\r 40%  \r\n" + // stopped before 100%
		copied(1000, `C:\good.bin`) + rule + summaryRows(1, 4, 1000)
	r.Apply(Step{At: sec(3), Text: text}, nil)
	snap := r.Tracker.Snapshot()
	if snap.Completed.Files != 1 || snap.Completed.Bytes != 1000 {
		t.Fatalf("completed %+v", snap.Completed)
	}
	if snap.Seen.Files != 4 || snap.ErrorLines != 1 {
		t.Fatalf("seen %+v errors %d", snap.Seen, snap.ErrorLines)
	}
}

func TestConcurrentRunNeverCountsFilesCompleted(t *testing.T) {
	cfg := serialConfig(2, 2000)
	cfg.Concurrent = true
	r := NewReplay(cfg, 0)
	r.Apply(Step{At: sec(1), Text: copied(1000, `C:\a.bin`) + copied(1000, `C:\b.bin`) + rule + summaryRows(1, 2, 2000)}, nil)
	snap := r.Tracker.Snapshot()
	if snap.Completed.Known || snap.Completed.Files != 0 || snap.Completed.Bytes != 0 {
		t.Fatalf("completed under /MT: %+v", snap.Completed)
	}
	if snap.Progress != nil || snap.ProgressNote == "" || snap.Rate.CompletedBytesPerSec != nil || snap.Rate.AverageBytesPerSec != nil {
		t.Fatalf("progress or rate claimed under /MT: %+v %+v", snap.Progress, snap.Rate)
	}
	if snap.Seen.Files != 2 || snap.Current != nil && snap.Current.Percent != nil {
		t.Fatalf("seen %+v current %+v", snap.Seen, snap.Current)
	}
	final := r.Tracker.Finish(summaryOf(2, 2000))
	if final.Source != "summary" || final.Completed.Bytes != 2000 || final.LogCompleted != nil || final.PeakBytesPerSec != nil || final.ProgressAvailable {
		t.Fatalf("final %+v", final)
	}
	if !final.Concurrent || len(final.Limits) == 0 {
		t.Fatalf("limits not recorded: %+v", final)
	}
}

func TestListOnlyPhaseHasNoCompletionOrPercentage(t *testing.T) {
	r := NewReplay(Config{Phase: "preview", List: true}, 0)
	r.Apply(Step{At: sec(1), Text: rule + fileLine("New File", 1000, `C:\a.bin`) + "\r\n" + fileLine("New File", 1000, `C:\b.bin`) + "\r\n" + rule}, nil)
	snap := r.Tracker.Snapshot()
	if snap.Completed.Known || snap.Progress != nil || !snap.ListOnly || snap.Seen.Files != 2 {
		t.Fatalf("snapshot %+v", snap)
	}
	final := r.Tracker.Finish(summaryOf(2, 2000))
	if final.Source != "none" || final.Completed != nil || final.Seen.Files != 2 {
		t.Fatalf("final %+v", final)
	}
}

func TestJobOptionsThatHideTheOutputDisableAccounting(t *testing.T) {
	for _, opt := range []string{"/NP", "/NFL", "/NC", "/NS"} {
		cfg := ConfigFor(job.Job{Options: []string{"/E", opt}}, "copy", false, &Planned{Files: 1, Bytes: 10})
		if cfg.AccountingLimit == "" || !strings.Contains(cfg.AccountingLimit, opt) {
			t.Fatalf("%s: limit %q", opt, cfg.AccountingLimit)
		}
		r := NewReplay(cfg, 0)
		r.Apply(Step{At: sec(1), Text: copied(10, `C:\a`) + rule}, nil)
		if snap := r.Tracker.Snapshot(); snap.Completed.Known || snap.Progress != nil {
			t.Fatalf("%s: %+v", opt, snap)
		}
	}
}

func TestConfigForReadsTheJob(t *testing.T) {
	cfg := ConfigFor(job.Job{Options: []string{"/E", "/mt:16", "/R:2", "/W:7", "/XD", "/not-an-option"}}, "copy", false, nil)
	if !cfg.Concurrent || cfg.RetryWait != 7*time.Second || cfg.AccountingLimit != "" {
		t.Fatalf("%+v", cfg)
	}
	if cfg = ConfigFor(job.Job{}, "copy", false, nil); cfg.Concurrent || cfg.RetryWait != DefaultRetryWait {
		t.Fatalf("defaults %+v", cfg)
	}
	if cfg = ConfigFor(job.Job{Options: []string{"/R:0", "/W:9"}}, "copy", false, nil); cfg.RetryWait != 0 {
		t.Fatalf("no retries means no retry wait: %+v", cfg)
	}
	if cfg = ConfigFor(job.Job{Options: []string{"/W:abc"}}, "preview", true, nil); cfg.RetryWait != DefaultRetryWait || !cfg.List {
		t.Fatalf("%+v", cfg)
	}
}

func TestRatesUseASlidingWindowAndDecayWhenQuiet(t *testing.T) {
	const mib = 1 << 20
	r := NewReplay(serialConfig(100, 100*mib), 0)
	var rateAt = map[time.Duration]Snapshot{}
	obs := func(at time.Duration) {
		if at%sec(1) == 0 {
			rateAt[at] = r.Tracker.Snapshot()
		}
	}
	r.Run(steady(40, mib, sec(1)), obs)
	r.AdvanceTo(sec(60), obs)
	if s := rateAt[sec(1)]; s.Rate.CompletedBytesPerSec != nil && s.ElapsedMS < 1000 {
		t.Fatalf("a rate over less than a second was reported: %+v", s.Rate)
	}
	s := rateAt[sec(20)]
	if s.Rate.CompletedBytesPerSec == nil || *s.Rate.CompletedBytesPerSec < 900_000 || *s.Rate.CompletedBytesPerSec > 1_250_000 {
		t.Fatalf("5 s rate at 20 s: %+v", s.Rate)
	}
	if s.Rate.FilesPerSec == nil || *s.Rate.FilesPerSec < 0.9 || *s.Rate.FilesPerSec > 1.1 {
		t.Fatalf("files per second: %+v", s.Rate.FilesPerSec)
	}
	if s.Rate.AverageBytesPerSec == nil || *s.Rate.AverageBytesPerSec < 900_000 || *s.Rate.AverageBytesPerSec > 1_100_000 {
		t.Fatalf("average: %+v", s.Rate)
	}
	if s.Rate.PeakBytesPerSec == nil || *s.Rate.PeakBytesPerSec < *s.Rate.CompletedBytesPerSec {
		t.Fatalf("peak %+v", s.Rate)
	}
	// After the last file (at 40 s) the 5 s window empties, and the rate falls to zero.
	if q := rateAt[sec(50)]; q.Rate.CompletedBytesPerSec == nil || *q.Rate.CompletedBytesPerSec != 0 {
		t.Fatalf("rate did not decay: %+v", q.Rate)
	}
	if rateAt[sec(50)].Rate.WindowSeconds != 5 {
		t.Fatal("window not reported")
	}
}

func TestActivityIsASeparateSignalNeverCompleted(t *testing.T) {
	const mib = 1 << 20
	r := NewReplay(serialConfig(10, 100*mib), 0)
	r.Apply(Step{At: 0, Text: rule + fileLine("New File", 100*mib, `C:\big.bin`)}, nil)
	for i := 1; i <= 10; i++ {
		r.Apply(Step{At: sec(float64(i)), IO: &IOSample{ReadBytes: uint64(i) * 10 * mib}}, nil)
	}
	snap := r.Tracker.Snapshot()
	if snap.Completed.Bytes != 0 || snap.Completed.Files != 0 {
		t.Fatalf("I/O counters became completed bytes: %+v", snap.Completed)
	}
	if !snap.Activity.Available || snap.Activity.ReadBytes != 100*mib {
		t.Fatalf("activity %+v", snap.Activity)
	}
	if snap.Activity.BytesPerSec == nil || *snap.Activity.BytesPerSec < 9*mib || *snap.Activity.BytesPerSec > 11*mib {
		t.Fatalf("activity rate %+v", snap.Activity.BytesPerSec)
	}
	if snap.Rate.CompletedBytesPerSec == nil || *snap.Rate.CompletedBytesPerSec != 0 {
		t.Fatalf("completed rate should be zero, not borrowed from activity: %+v", snap.Rate)
	}
	if snap.Progress == nil || *snap.Progress.BytesPercent != 0 {
		t.Fatalf("progress moved on activity alone: %+v", snap.Progress)
	}
	if !strings.Contains(snap.Activity.Note, "not completed bytes") {
		t.Fatalf("note %q", snap.Activity.Note)
	}
	if snap.Status != StatusActive {
		t.Fatalf("status %s", snap.Status)
	}
	// A counter that goes backwards (a new process) is ignored, not subtracted.
	r.Apply(Step{At: sec(11), IO: &IOSample{ReadBytes: 5}}, nil)
	if got := r.Tracker.Snapshot().Activity.ReadBytes; got != 100*mib {
		t.Fatalf("counter went backwards: %d", got)
	}
}

func TestStallIsAStatusAfterSixtySecondsWithoutAnySignal(t *testing.T) {
	r := NewReplay(serialConfig(10, 1000), 0)
	r.Apply(Step{At: sec(1), Text: rule + dirLine(1, `C:\a\`)}, nil)
	r.AdvanceTo(sec(60), nil)
	if s := r.Tracker.Snapshot(); s.Status != StatusActive || s.QuietMS != 59_000 {
		t.Fatalf("at 60 s: %s quiet %d", s.Status, s.QuietMS)
	}
	r.AdvanceTo(sec(61), nil)
	s := r.Tracker.Snapshot()
	if s.Status != StatusStalled || !strings.Contains(s.StatusNote, "not a failure") || s.QuietMS != 60_000 {
		t.Fatalf("at 61 s: %+v", s)
	}
	// A read counter that moves is a progress signal and ends the stall.
	r.Apply(Step{At: sec(70), IO: &IOSample{ReadBytes: 4096}}, nil)
	if s := r.Tracker.Snapshot(); s.Status != StatusActive || s.QuietMS != 0 {
		t.Fatalf("after activity: %+v", s)
	}
	final := r.Tracker.Finish(nil)
	if len(final.Stalls) != 1 || final.Stalls[0].StartMS != 61_000 || final.Stalls[0].EndMS != 70_000 || final.StalledMS != 9_000 {
		t.Fatalf("stalls %+v total %d", final.Stalls, final.StalledMS)
	}
}

func TestStallBeforeAnySignalAndAtTheEnd(t *testing.T) {
	r := NewReplay(serialConfig(1, 10), 0)
	if s := r.Tracker.Snapshot(); s.Status != StatusStarting {
		t.Fatalf("status %s", s.Status)
	}
	r.AdvanceTo(sec(30), nil)
	if s := r.Tracker.Snapshot(); s.Status != StatusStarting {
		t.Fatalf("status %s", s.Status)
	}
	r.AdvanceTo(sec(75), nil)
	if s := r.Tracker.Snapshot(); s.Status != StatusStalled {
		t.Fatalf("status %s", s.Status)
	}
	final := r.Tracker.Finish(nil)
	if len(final.Stalls) != 1 || final.Stalls[0].StartMS != 60_000 || final.Stalls[0].EndMS != 75_000 {
		t.Fatalf("an open stall is closed at the end: %+v", final.Stalls)
	}
	if s := r.Tracker.Snapshot(); s.Status != StatusFinished || s.QuietMS != 0 {
		t.Fatalf("finished snapshot %+v", s)
	}
}

func TestRetryWaitIsNotAStall(t *testing.T) {
	cfg := serialConfig(10, 1000)
	cfg.RetryWait = 30 * time.Second
	r := NewReplay(cfg, 0)
	r.Apply(Step{At: sec(1), Text: rule + fileLine("New File", 100, `C:\locked.bin`)}, nil)
	r.Apply(Step{At: sec(2), Text: "\r  0%  \r\n" + errorHeader(32, `C:\locked.bin`)}, nil)
	for _, at := range []float64{10, 30, 41} {
		r.AdvanceTo(sec(at), nil)
		if s := r.Tracker.Snapshot(); s.Status != StatusRetrying || !strings.Contains(s.StatusNote, "not a stall") {
			t.Fatalf("at %v s: %s", at, s.Status)
		}
	}
	// The wait ends 30 s plus the grace after the error; the 60 s clock starts there.
	r.AdvanceTo(sec(2+30+10+59), nil)
	if s := r.Tracker.Snapshot(); s.Status != StatusActive {
		t.Fatalf("status %s", s.Status)
	}
	r.AdvanceTo(sec(2+30+10+60), nil)
	if s := r.Tracker.Snapshot(); s.Status != StatusStalled {
		t.Fatalf("status %s", s.Status)
	}
	// Progress during a retry wait ends the retrying state without any stall.
	r2 := NewReplay(cfg, 0)
	r2.Apply(Step{At: sec(1), Text: rule + fileLine("New File", 100, `C:\x.bin`) + "\r\n" + errorHeader(32, `C:\x.bin`)}, nil)
	r2.Apply(Step{At: sec(6), Text: "\r  0%  \r100%  \r\n"}, nil)
	if s := r2.Tracker.Snapshot(); s.Status != StatusActive {
		t.Fatalf("status %s", s.Status)
	}
	if final := r2.Tracker.Finish(nil); len(final.Stalls) != 0 || final.ErrorLines != 1 {
		t.Fatalf("final %+v", final)
	}
}

func TestProgressVisibilityRules(t *testing.T) {
	steps := []Step{{At: sec(1), Text: copied(100, `C:\a`) + copied(100, `C:\b`) + rule}}
	cases := []struct {
		name  string
		cfg   Config
		shown bool
	}{
		{"preview totals and a serial copy", Config{Phase: "copy", Planned: &Planned{Files: 4, Bytes: 400}}, true},
		{"no preview", Config{Phase: "copy"}, false},
		{"empty preview", Config{Phase: "copy", Planned: &Planned{}}, false},
		{"concurrent", Config{Phase: "copy", Planned: &Planned{Files: 4, Bytes: 400}, Concurrent: true}, false},
		{"list only", Config{Phase: "preview", Planned: &Planned{Files: 4, Bytes: 400}, List: true}, false},
		{"hidden output", Config{Phase: "copy", Planned: &Planned{Files: 4, Bytes: 400}, AccountingLimit: "x"}, false},
	}
	for _, c := range cases {
		r := NewReplay(c.cfg, 0)
		r.Run(steps, nil)
		s := r.Tracker.Snapshot()
		if (s.Progress != nil) != c.shown {
			t.Errorf("%s: progress %+v", c.name, s.Progress)
		}
		if !c.shown && s.ProgressNote == "" {
			t.Errorf("%s: a hidden percentage needs its reason", c.name)
		}
		if s.ETA != nil || s.ETANote == "" {
			t.Errorf("%s: ETA %+v note %q", c.name, s.ETA, s.ETANote)
		}
	}
}

func TestProgressClampsAndFlagsASourceThatGrew(t *testing.T) {
	r := NewReplay(serialConfig(1, 100), 0)
	r.Apply(Step{At: sec(1), Text: copied(100, `C:\a`) + copied(100, `C:\b`) + rule}, nil)
	p := r.Tracker.Snapshot().Progress
	if p == nil || *p.BytesPercent != 100 || *p.FilesPercent != 100 || !p.ExceededPlan {
		t.Fatalf("progress %+v", p)
	}
}

func TestTimelineIsBoundedAndKeepsBothEnds(t *testing.T) {
	r := NewReplay(serialConfig(100000, 1<<40), 0)
	r.Apply(Step{At: 0, Text: rule}, nil)
	for i := 1; i <= 2000; i++ {
		r.Apply(Step{At: sec(float64(i * 10)), Text: copied(1<<20, fmt.Sprintf(`C:\f%d`, i)), IO: &IOSample{ReadBytes: uint64(i) << 20}}, nil)
	}
	final := r.Tracker.Finish(summaryOf(2000, 2000<<20))
	n := len(final.Timeline)
	if n == 0 || n > TimelineCap {
		t.Fatalf("timeline has %d points", n)
	}
	if final.Timeline[0].TMS != 0 {
		t.Fatalf("first point %+v", final.Timeline[0])
	}
	last := final.Timeline[n-1]
	if last.Status != StatusFinished || last.CompletedBytes != 2000<<20 || last.TMS != final.ElapsedMS {
		t.Fatalf("last point %+v elapsed %d", last, final.ElapsedMS)
	}
	for i := 1; i < n; i++ {
		if final.Timeline[i].TMS <= final.Timeline[i-1].TMS || final.Timeline[i].CompletedBytes < final.Timeline[i-1].CompletedBytes {
			t.Fatalf("timeline not monotone at %d", i)
		}
	}
	// Memory in the tracker stays bounded as well.
	if got := len(r.Tracker.bytesS.pts) + len(r.Tracker.filesS.pts) + len(r.Tracker.readS.pts); got > 4*maxSamples {
		t.Fatalf("series grew to %d", got)
	}
}

func TestShortRunStillGetsAFinalPoint(t *testing.T) {
	r := NewReplay(serialConfig(1, 10), 0)
	r.Apply(Step{At: sec(0.25), Text: copied(10, `C:\a`)}, nil)
	final := r.Tracker.Finish(summaryOf(1, 10))
	if n := len(final.Timeline); n < 2 || final.Timeline[n-1].Status != StatusFinished {
		t.Fatalf("timeline %+v", final.Timeline)
	}
	// Finishing twice returns the same record and ignores later input.
	r.Tracker.Log(copied(5, `C:\late`))
	if again := r.Tracker.Finish(nil); !reflect.DeepEqual(again, final) {
		t.Fatal("Finish is not idempotent")
	}
}

func TestFinalRoundTripsThroughJSON(t *testing.T) {
	r := NewReplay(serialConfig(3, 3<<20), 0)
	r.Run(steady(3, 1<<20, sec(2)), nil)
	r.Apply(Step{At: sec(8), IO: &IOSample{ReadBytes: 3 << 20}}, nil)
	r.AdvanceTo(sec(80), nil)
	final := r.Tracker.Finish(summaryOf(3, 3<<20))
	data, err := json.Marshal(final)
	if err != nil {
		t.Fatal(err)
	}
	var back Final
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(back, final) {
		t.Fatalf("round trip changed the record:\n%s", data)
	}
	var generic map[string]any
	_ = json.Unmarshal(data, &generic)
	for _, key := range []string{"phase", "elapsed_ms", "source", "completed", "timeline", "stalled_ms", "activity_read_bytes"} {
		if _, ok := generic[key]; !ok {
			t.Errorf("run.json metrics lack %q", key)
		}
	}
	// A snapshot also survives JSON (the Activity view reads it).
	snapData, _ := json.Marshal(r.Tracker.Snapshot())
	var snap Snapshot
	if err := json.Unmarshal(snapData, &snap); err != nil || snap.Status != StatusFinished {
		t.Fatalf("snapshot: %v %+v", err, snap)
	}
}

func TestFinishWithoutASummaryAdmitsItsLimit(t *testing.T) {
	r := NewReplay(serialConfig(2, 200), 0)
	r.Apply(Step{At: sec(2), Text: copied(100, `C:\a`) + fileLine("New File", 100, `C:\b`) + "\r 30%  "}, nil)
	final := r.Tracker.Finish(nil)
	if final.Source != "log" || final.Completed.Files != 1 || final.Completed.Bytes != 100 {
		t.Fatalf("final %+v", final)
	}
	if len(final.Limits) == 0 || !strings.Contains(strings.Join(final.Limits, " "), "no Robocopy summary") && !strings.Contains(strings.Join(final.Limits, " "), "No Robocopy summary") {
		t.Fatalf("limits %v", final.Limits)
	}
}

func TestClockNeverRunsBackwards(t *testing.T) {
	c := NewFakeClock(epoch)
	c.Advance(sec(10))
	c.Set(epoch)
	c.Advance(-sec(5))
	if c.Now() != epoch.Add(sec(10)) {
		t.Fatalf("clock moved backwards: %v", c.Now())
	}
	if (SystemClock{}).Now().IsZero() {
		t.Fatal("system clock")
	}
}

func TestTrackerIsSafeForConcurrentInputsAndReaders(t *testing.T) {
	tr := NewTracker(Config{Phase: "copy", Planned: &Planned{Files: 500, Bytes: 500 << 10}})
	var wg sync.WaitGroup
	wg.Add(4)
	go func() {
		defer wg.Done()
		for i := 0; i < 500; i++ {
			tr.Log(copied(1<<10, fmt.Sprintf(`C:\f%d`, i)))
		}
	}()
	go func() {
		defer wg.Done()
		for i := 1; i <= 500; i++ {
			tr.IO(IOSample{ReadBytes: uint64(i) << 10})
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 500; i++ {
			tr.Tick()
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 500; i++ {
			if s := tr.Snapshot(); s.Completed.Bytes > s.Seen.Bytes {
				t.Error("completed exceeds announced")
				return
			}
		}
	}()
	wg.Wait()
	final := tr.Finish(nil)
	if final.Completed == nil || final.Completed.Files != 500 {
		t.Fatalf("final %+v", final.Completed)
	}
}
