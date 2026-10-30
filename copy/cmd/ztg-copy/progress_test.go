package main

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/zerotrustgarage/tools/copy/internal/metrics"
	copyrun "github.com/zerotrustgarage/tools/copy/internal/run"
)

type phaseSink struct {
	bytes.Buffer
	started  []string
	finished int
}

func (s *phaseSink) PhaseStarted(name string)    { s.started = append(s.started, name) }
func (s *phaseSink) PhaseFinished(copyrun.Phase) { s.finished++ }

func snapshot(t *testing.T, cfg metrics.Config, at time.Duration, text string, io *metrics.IOSample) metrics.Snapshot {
	t.Helper()
	r := metrics.NewReplay(cfg, 0)
	r.Apply(metrics.Step{At: at, Text: text, IO: io}, nil)
	return r.Tracker.Snapshot()
}

const file10 = "\t    New File  \t\t 10485760\tC:\\src\\a.bin\r  0%  \r100%  \r\n"

func TestProgressPrinterPrintsOnStatusChangeAndThenEveryInterval(t *testing.T) {
	var sink phaseSink
	p := newProgressPrinter(&sink)
	clock := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)
	p.now = func() time.Time { return clock }
	p.PhaseStarted("copy")
	cfg := metrics.Config{Phase: "copy", Planned: &metrics.Planned{Files: 4, Bytes: 40 << 20}}
	// Starting is not printed.
	p.MetricsUpdated("copy", metrics.NewTracker(cfg).Snapshot())
	if sink.Len() != 0 {
		t.Fatalf("printed while starting: %q", sink.String())
	}
	active := snapshot(t, cfg, 4*time.Second, file10+file10+file10, &metrics.IOSample{ReadBytes: 30 << 20})
	p.MetricsUpdated("copy", active)
	if !strings.Contains(sink.String(), "copy: active") || !strings.Contains(sink.String(), "2 files") {
		t.Fatalf("first reading: %q", sink.String())
	}
	lines := strings.Count(sink.String(), "\n")
	clock = clock.Add(3 * time.Second)
	p.MetricsUpdated("copy", active)
	if strings.Count(sink.String(), "\n") != lines {
		t.Fatal("printed again inside the interval")
	}
	clock = clock.Add(8 * time.Second)
	p.MetricsUpdated("copy", active)
	if strings.Count(sink.String(), "\n") != lines+1 {
		t.Fatalf("did not print after the interval: %q", sink.String())
	}
	// A stall prints at once, with its explanation, and never as a failure.
	r := metrics.NewReplay(cfg, 0)
	r.Apply(metrics.Step{At: time.Second, Text: file10}, nil)
	r.AdvanceTo(90*time.Second, nil)
	p.MetricsUpdated("copy", r.Tracker.Snapshot())
	out := sink.String()
	if !strings.Contains(out, "copy: stalled") || !strings.Contains(out, "not a failure") || strings.Contains(strings.ToLower(out), "failed") {
		t.Fatalf("stall line: %q", out)
	}
	// A finished reading prints nothing; the runner's own totals line says it.
	n := sink.Len()
	r.Tracker.Finish(nil)
	p.MetricsUpdated("copy", r.Tracker.Snapshot())
	if sink.Len() != n {
		t.Fatal("printed the finished reading")
	}
	// Runner lines and phase hooks pass through.
	p.Write([]byte("copy...\n"))
	p.PhaseFinished(copyrun.Phase{})
	if !strings.HasSuffix(sink.String(), "copy...\n") || len(sink.started) != 1 || sink.finished != 1 {
		t.Fatalf("pass-through: %q %v %d", sink.String(), sink.started, sink.finished)
	}
}

func TestProgressLinesClaimNothingUnderMTAndNeverShowAnETA(t *testing.T) {
	var sink phaseSink
	p := newProgressPrinter(&sink)
	cfg := metrics.Config{Phase: "copy", Concurrent: true, Planned: &metrics.Planned{Files: 4, Bytes: 40 << 20}}
	s := snapshot(t, cfg, 70*time.Second, file10, &metrics.IOSample{ReadBytes: 500 << 20})
	p.MetricsUpdated("copy", s)
	line := sink.String()
	for _, want := range []string{"copy: active", "announced", "completed totals at the end of the phase", "transfer activity"} {
		if !strings.Contains(line, want) {
			t.Errorf("line %q lacks %q", line, want)
		}
	}
	for _, bad := range []string{"%", "remaining", "ETA"} {
		if strings.Contains(line, bad) {
			t.Errorf("line %q contains %q", line, bad)
		}
	}
}
