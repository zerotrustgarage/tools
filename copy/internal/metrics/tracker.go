package metrics

import (
	"math"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/zerotrustgarage/tools/copy/internal/job"
	"github.com/zerotrustgarage/tools/copy/internal/parse"
)

// Windows and thresholds. Each is a default of Config; design section 6 sets the
// first four, the rest are decisions recorded in design section 6 and rc1.md.
const (
	// DefaultCurrentWindow is the sliding window of the current speed (design: last 5 seconds).
	DefaultCurrentWindow = 5 * time.Second
	// DefaultFilesWindow is the window of files per second (design: last 30 seconds).
	DefaultFilesWindow = 30 * time.Second
	// DefaultFitWindow is the window the ETA is fitted over (design: last 60 seconds).
	DefaultFitWindow = 60 * time.Second
	// DefaultStallAfter is the quiet time that raises the stall status (design: 60 seconds).
	// It is longer than a default retry wait and than the pauses of large directory
	// enumeration, antivirus scans and slow share reconnects, so a healthy copy rarely
	// shows it, and short enough to notice a hung share within a minute.
	DefaultStallAfter = 60 * time.Second
	// DefaultRetryWait is Robocopy's own default for /W.
	DefaultRetryWait = 30 * time.Second
	// RetryGrace is added to the retry wait: the engine writes the error, waits, then
	// retries, and the log is read on a poll.
	RetryGrace = 10 * time.Second
	// MinRateSpan is the shortest window a rate is reported over. Anything shorter
	// is noise, so no rate is shown yet.
	MinRateSpan = time.Second
	// DefaultETAMinElapsed is the copy time before an ETA may appear (roadmap: after the first minute).
	DefaultETAMinElapsed = 60 * time.Second
	// DefaultETAMinFiles is the number of files that must have finished inside the fit window.
	DefaultETAMinFiles = 5
	// TimelineCap bounds the saved timeline of one phase.
	TimelineCap = 240

	maxStalls     = 32
	maxPathRunes  = 1024
	timelineFirst = time.Second
)

// Status names. A stall is a status, never a failure by itself.
const (
	StatusStarting = "starting"
	StatusActive   = "active"
	StatusRetrying = "retrying"
	StatusStalled  = "stalled"
	StatusFinished = "finished"
)

// Planned is the preview's total of what a copy should do.
type Planned struct {
	Files uint64 `json:"files"`
	Bytes uint64 `json:"bytes"`
}

type Config struct {
	Phase string
	// List marks a list-only phase (preview, convergence): no file is copied, so
	// nothing is ever completed.
	List bool
	// Concurrent marks /MT: file lines and percentages of several files interleave
	// and nothing says which file finished.
	Concurrent bool
	// Planned holds the preview totals for the copy phase, or nil without a preview.
	Planned *Planned
	// RetryWait is the job's /W; zero means no retry wait.
	RetryWait time.Duration
	// AccountingLimit, when not empty, says why file completion cannot be read from
	// this job's output (for example /NP removes the percentages).
	AccountingLimit string
	Clock           Clock

	CurrentWindow time.Duration
	FilesWindow   time.Duration
	FitWindow     time.Duration
	StallAfter    time.Duration
	// AllowETA is false in production until an ETA is measured within 15% on real
	// jobs longer than a minute (rc1.md). Tests turn it on to measure.
	AllowETA      bool
	ETAMinElapsed time.Duration
	ETAMinFiles   int
}

func (c Config) withDefaults() Config {
	if c.Clock == nil {
		c.Clock = SystemClock{}
	}
	if c.CurrentWindow <= 0 {
		c.CurrentWindow = DefaultCurrentWindow
	}
	if c.FilesWindow <= 0 {
		c.FilesWindow = DefaultFilesWindow
	}
	if c.FitWindow <= 0 {
		c.FitWindow = DefaultFitWindow
	}
	if c.StallAfter <= 0 {
		c.StallAfter = DefaultStallAfter
	}
	if c.ETAMinElapsed <= 0 {
		c.ETAMinElapsed = DefaultETAMinElapsed
	}
	if c.ETAMinFiles <= 0 {
		c.ETAMinFiles = DefaultETAMinFiles
	}
	return c
}

// ConfigFor reads the job options that change what the output can prove: /MT,
// the retry wait and the options that hide file lines or percentages.
func ConfigFor(j job.Job, phase string, list bool, planned *Planned) Config {
	cfg := Config{Phase: phase, List: list, Planned: planned, RetryWait: DefaultRetryWait}
	noRetries := false
	for _, arg := range j.Options {
		if !strings.HasPrefix(arg, "/") {
			continue
		}
		key := job.Key(arg)
		switch key {
		case "/MT":
			cfg.Concurrent = true
		case "/W":
			if n, ok := smallNumber(arg); ok {
				cfg.RetryWait = time.Duration(n) * time.Second
			}
		case "/R":
			if n, ok := smallNumber(arg); ok && n == 0 {
				noRetries = true
			}
		case "/NP", "/NFL", "/NC", "/NS":
			if cfg.AccountingLimit == "" {
				cfg.AccountingLimit = "The job uses " + key + ", which removes file lines or percentages that finished-file accounting reads."
			}
		}
	}
	if noRetries {
		cfg.RetryWait = 0
	}
	return cfg
}

func smallNumber(arg string) (int64, bool) {
	_, value, ok := strings.Cut(arg, ":")
	if !ok || value == "" || len(value) > 9 || strings.Trim(value, "0123456789") != "" {
		return 0, false
	}
	var n int64
	for _, r := range value {
		n = n*10 + int64(r-'0')
	}
	return n, true
}

// IOSample is one reading of the process I/O counters (GetProcessIoCounters).
// Only ReadBytes feeds the activity rate: writes include the log Robocopy writes.
type IOSample struct {
	ReadBytes uint64
}

type openFile struct {
	path    string
	size    uint64
	percent int
	open    bool
	sawDone bool
	errored bool
}

type stall struct {
	start, end time.Time
}

type Tracker struct {
	mu    sync.Mutex
	cfg   Config
	start time.Time
	now   time.Time
	scan  scanner

	files, dirs, seenBytes uint64
	doneFiles, doneBytes   uint64
	errorLines             uint64
	cur                    *openFile
	lastMarker             int

	bytesS, filesS, readS *series
	ioSeen                bool

	lastProgress time.Time
	everProgress bool
	retryUntil   time.Time
	// Sequence numbers order a progress signal and an error header that share one
	// instant (one chunk of log text carries both).
	seq, progressSeq, errorSeq uint64
	stalls                     []stall
	stallsOmit                 int
	stalledTotal               time.Duration

	peakRate, peakActivity float64

	timeline  []Point
	nextPoint time.Time
	interval  time.Duration

	finished   bool
	finishedAt time.Time
	final      *Final
}

func NewTracker(cfg Config) *Tracker {
	cfg = cfg.withDefaults()
	now := cfg.Clock.Now()
	t := &Tracker{cfg: cfg, start: now, now: now, lastProgress: now, lastMarker: -1, interval: timelineFirst}
	keep := cfg.FitWindow
	if cfg.FilesWindow > keep {
		keep = cfg.FilesWindow
	}
	t.bytesS, t.filesS = newSeries(now, keep+time.Second), newSeries(now, keep+time.Second)
	t.readS = newSeries(now, cfg.CurrentWindow+time.Second)
	t.scan.emit = t.handle
	t.nextPoint = now
	return t
}

// completedKnown says whether finished-file accounting can be read from this
// phase's output at all.
func (t *Tracker) completedKnown() bool {
	return !t.cfg.List && !t.cfg.Concurrent && t.cfg.AccountingLimit == ""
}

func (t *Tracker) clockNow() time.Time {
	now := t.cfg.Clock.Now()
	if now.Before(t.now) {
		return t.now
	}
	t.now = now
	return now
}

// Log feeds decoded native-log text, in pieces of any size.
func (t *Tracker) Log(text string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.finished {
		return
	}
	now := t.clockNow()
	t.scan.write(text)
	t.touch(now)
}

// IO feeds one process I/O reading.
func (t *Tracker) IO(sample IOSample) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.finished {
		return
	}
	now := t.clockNow()
	t.ioSeen = true
	if sample.ReadBytes > t.readS.latest() {
		t.readS.add(now, sample.ReadBytes)
		t.progress(now)
	}
	t.touch(now)
}

// Tick lets time pass without new input, so a quiet run can be seen as quiet.
func (t *Tracker) Tick() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.finished {
		return
	}
	t.touch(t.clockNow())
}

// handle is the scanner callback; t.mu is held by the caller and t.now is current.
func (t *Tracker) handle(ev event) {
	now := t.now
	switch ev.kind {
	case evFile:
		t.settle(now)
		t.files++
		t.seenBytes += ev.size
		t.cur = &openFile{path: ev.path, size: ev.size, percent: -1, open: true}
		t.progress(now)
	case evFilePath:
		if t.cur != nil && t.cur.open && t.cur.size == ev.size {
			t.cur.path = ev.path
		}
	case evDir:
		t.settle(now)
		t.dirs++
		t.progress(now)
	case evPercent:
		c := t.cur
		if c != nil && c.open && t.completedKnown() {
			if ev.percent != c.percent {
				c.percent = ev.percent
				t.progress(now)
			}
			if ev.percent == 100 {
				c.sawDone = true
			}
			return
		}
		// Under /MT, or in a list-only phase, a marker cannot be tied to a file. A
		// changed value still shows Robocopy is working.
		if ev.percent != t.lastMarker {
			t.progress(now)
		}
		t.lastMarker = ev.percent
	case evError:
		t.errorLines++
		t.seq++
		t.errorSeq = t.seq
		t.retryUntil = now.Add(t.cfg.RetryWait + RetryGrace)
		if t.cur != nil && t.cur.open {
			t.cur.errored = true
		}
	case evRule, evSummaryRow:
		t.settle(now)
	}
}

// settle closes the open file when something after it proves Robocopy moved on:
// the next file or directory line, the rule before the summary, the summary, or
// the end of the run. A file counts as completed only if Robocopy also wrote its
// own 100% for it and no error header came in between. Serial copies only.
func (t *Tracker) settle(now time.Time) {
	c := t.cur
	if c == nil || !c.open {
		return
	}
	c.open = false
	if !t.completedKnown() || !c.sawDone || c.errored {
		return
	}
	t.doneFiles++
	t.doneBytes += c.size
	t.bytesS.add(now, t.doneBytes)
	t.filesS.add(now, t.doneFiles)
	t.progress(now)
}

// progress records a progress signal: a file or directory announcement, a changed
// percentage, finished-file accounting or a growing read counter. Error headers,
// rules and unrecognized lines are not progress.
func (t *Tracker) progress(now time.Time) {
	t.closeStall(now)
	t.seq++
	t.progressSeq = t.seq
	t.lastProgress = now
	t.everProgress = true
}

// errorPending is true when an error header came after the last progress signal.
func (t *Tracker) errorPending() bool { return t.errorSeq > t.progressSeq }

// reference is the instant the quiet time is counted from: the last progress
// signal, or the end of the retry wait when an error came after it.
func (t *Tracker) reference() time.Time {
	ref := t.lastProgress
	if t.errorPending() && t.retryUntil.After(ref) {
		ref = t.retryUntil
	}
	return ref
}

func (t *Tracker) retrying(now time.Time) bool {
	return t.errorPending() && now.Before(t.retryUntil)
}

// stallStart reports whether the run is stalled at now, and since when.
func (t *Tracker) stallStart(now time.Time) (time.Time, bool) {
	if t.retrying(now) {
		return time.Time{}, false
	}
	begin := t.reference().Add(t.cfg.StallAfter)
	return begin, !now.Before(begin)
}

// closeStall ends a stall that new progress or the end of the run interrupts.
func (t *Tracker) closeStall(now time.Time) {
	begin, ok := t.stallStart(now)
	if !ok {
		return
	}
	t.stalledTotal += now.Sub(begin)
	if len(t.stalls) < maxStalls {
		t.stalls = append(t.stalls, stall{begin, now})
	} else {
		t.stallsOmit++
	}
}

// touch updates peaks and the timeline after any input or tick.
func (t *Tracker) touch(now time.Time) {
	if t.completedKnown() {
		if r, ok := t.bytesS.rate(now, t.cfg.CurrentWindow, t.cfg.CurrentWindow); ok && r > t.peakRate {
			t.peakRate = r
		}
	}
	if t.ioSeen {
		if r, ok := t.readS.rate(now, t.cfg.CurrentWindow, t.cfg.CurrentWindow); ok && r > t.peakActivity {
			t.peakActivity = r
		}
	}
	if !now.Before(t.nextPoint) {
		t.timeline = append(t.timeline, t.point(now))
		t.nextPoint = now.Add(t.interval)
		if len(t.timeline) > TimelineCap {
			kept := t.timeline[:0]
			for i := 0; i < len(t.timeline); i += 2 {
				kept = append(kept, t.timeline[i])
			}
			t.timeline = kept
			t.interval *= 2
			t.nextPoint = now.Add(t.interval)
		}
	}
}

func (t *Tracker) status(now time.Time) (string, string) {
	if t.finished {
		return StatusFinished, ""
	}
	if t.retrying(now) {
		return StatusRetrying, "Robocopy reported an error and is waiting to retry. This is not a stall."
	}
	if _, stalled := t.stallStart(now); stalled {
		quiet := int64(now.Sub(t.reference()) / time.Second)
		return StatusStalled, "No progress signal for " + itoa(quiet) + " seconds. Robocopy may be waiting on a slow or disconnected share, a locked file or a scan. This is a status, not a failure."
	}
	if !t.everProgress {
		return StatusStarting, "Waiting for the first sign of activity."
	}
	return StatusActive, ""
}

func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

func round(v float64) uint64 {
	if v <= 0 || math.IsNaN(v) || math.IsInf(v, 0) {
		return 0
	}
	return uint64(math.Round(v))
}

func ptr(v uint64) *uint64 { return &v }

func twoDecimals(v float64) float64 { return math.Round(v*100) / 100 }

func truncatePath(p string) string {
	p = strings.ToValidUTF8(p, "�")
	if utf8.RuneCountInString(p) <= maxPathRunes {
		return p
	}
	return string([]rune(p)[:maxPathRunes]) + "…"
}

// Snapshot reads the current state without changing any signal.
func (t *Tracker) Snapshot() Snapshot {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := t.now
	if !t.finished {
		now = t.clockNow()
		t.touch(now)
	} else {
		now = t.finishedAt
	}
	return t.snapshot(now)
}

func (t *Tracker) snapshot(now time.Time) Snapshot {
	status, note := t.status(now)
	elapsed := now.Sub(t.start)
	quiet := now.Sub(t.reference())
	if quiet < 0 || t.finished {
		quiet = 0
	}
	s := Snapshot{
		Phase: t.cfg.Phase, Status: status, StatusNote: note,
		ElapsedMS: elapsed.Milliseconds(), QuietMS: quiet.Milliseconds(),
		Concurrent: t.cfg.Concurrent, ListOnly: t.cfg.List,
		Seen:       Seen{Files: t.files, Bytes: t.seenBytes, Dirs: t.dirs},
		ErrorLines: t.errorLines,
	}
	known := t.completedKnown()
	s.Completed = Completed{Known: known, Files: t.doneFiles, Bytes: t.doneBytes, Note: t.completedNote()}
	if !known {
		s.Completed.Files, s.Completed.Bytes = 0, 0
	}
	if t.cur != nil {
		c := &Current{Path: truncatePath(t.cur.path), Size: t.cur.size, Announced: true}
		if known && t.cur.open && t.cur.percent >= 0 {
			p := t.cur.percent
			c.Percent = &p
		}
		s.Current = c
	}
	if known {
		if r, ok := t.bytesS.rate(now, t.cfg.CurrentWindow, MinRateSpan); ok {
			s.Rate.CompletedBytesPerSec = ptr(round(r))
		}
		if elapsed >= MinRateSpan {
			s.Rate.AverageBytesPerSec = ptr(round(float64(t.doneBytes) / elapsed.Seconds()))
		}
		if t.peakRate > 0 {
			s.Rate.PeakBytesPerSec = ptr(round(t.peakRate))
		}
		if r, ok := t.filesS.rate(now, t.cfg.FilesWindow, MinRateSpan); ok {
			f := twoDecimals(r)
			s.Rate.FilesPerSec = &f
		}
	}
	s.Rate.WindowSeconds = int(t.cfg.CurrentWindow / time.Second)
	s.Activity = Activity{Available: t.ioSeen, ReadBytes: t.readS.latest(), Note: activityNote}
	if t.ioSeen {
		if r, ok := t.readS.rate(now, t.cfg.CurrentWindow, MinRateSpan); ok {
			s.Activity.BytesPerSec = ptr(round(r))
		}
		if elapsed >= MinRateSpan {
			s.Activity.AverageBytesPerSec = ptr(round(float64(t.readS.latest()) / elapsed.Seconds()))
		}
		if t.peakActivity > 0 {
			s.Activity.PeakBytesPerSec = ptr(round(t.peakActivity))
		}
	}
	s.Progress, s.ProgressNote = t.progressReading()
	if !t.finished {
		s.ETA, s.ETANote = t.eta(now)
	} else {
		s.ETANote = "The phase has finished."
	}
	return s
}

const activityNote = "Bytes Robocopy has read, from the process I/O counters. This is transfer activity, not completed bytes: it can lead the copy, repeat reads, and miss offloaded or cloned data."

func (t *Tracker) completedNote() string {
	switch {
	case t.cfg.List:
		return "A list-only phase copies nothing, so nothing is completed."
	case t.cfg.Concurrent:
		return "With /MT Robocopy does not say when each file finishes. Completed totals appear when the phase ends."
	case t.cfg.AccountingLimit != "":
		return t.cfg.AccountingLimit + " Completed totals appear when the phase ends."
	}
	return "Files count as completed when Robocopy has written 100% for them and moved on. Final totals come from Robocopy's summary."
}

// progressReading gives the percentage of the preview totals, only when finished
// files can be counted and a preview supplied totals.
func (t *Tracker) progressReading() (*Progress, string) {
	switch {
	case t.cfg.List:
		return nil, "No percentage in a list-only phase."
	case !t.completedKnown():
		return nil, "No percentage: finished files cannot be counted while this runs."
	case t.cfg.Planned == nil:
		return nil, "No percentage: there are no preview totals for this run."
	case t.cfg.Planned.Bytes == 0 && t.cfg.Planned.Files == 0:
		return nil, "No percentage: the preview found nothing to copy."
	}
	p := &Progress{PlannedFiles: t.cfg.Planned.Files, PlannedBytes: t.cfg.Planned.Bytes}
	if t.cfg.Planned.Bytes > 0 {
		v := 100 * float64(t.doneBytes) / float64(t.cfg.Planned.Bytes)
		if v > 100 {
			v, p.ExceededPlan = 100, true
		}
		p.BytesPercent = ptrFloat(twoDecimals(v))
	}
	if t.cfg.Planned.Files > 0 {
		v := 100 * float64(t.doneFiles) / float64(t.cfg.Planned.Files)
		if v > 100 {
			v, p.ExceededPlan = 100, true
		}
		p.FilesPercent = ptrFloat(twoDecimals(v))
	}
	return p, "Share of the preview totals that finished. A file in progress is not counted, and zero-byte files are counted when Robocopy's summary arrives."
}

func ptrFloat(v float64) *float64 { return &v }

// Finish ends the phase. A summary, when present, supplies the final totals (the
// exit code and summary decide outcomes; the log accounting only informs the live
// view). The returned record is also kept for Snapshot.
func (t *Tracker) Finish(summary *parse.Summary) Final {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.finished {
		return *t.final
	}
	now := t.clockNow()
	t.scan.flush()
	t.settle(now)
	t.closeStall(now)
	t.touch(now)
	t.finished, t.finishedAt = true, now
	f := t.buildFinal(now, summary)
	t.final = &f
	last := t.pointFinal(now, &f)
	if len(t.timeline) >= TimelineCap {
		t.timeline[len(t.timeline)-1] = last
	} else {
		t.timeline = append(t.timeline, last)
	}
	f.Timeline = append([]Point(nil), t.timeline...)
	t.final = &f
	return f
}
