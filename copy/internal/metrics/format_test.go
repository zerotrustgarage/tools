package metrics

import (
	"strings"
	"testing"
	"time"
)

func TestFormatting(t *testing.T) {
	for in, want := range map[uint64]string{0: "0 B", 1023: "1023 B", 1024: "1.0 KiB", 5 << 20: "5.0 MiB", 3 << 30: "3.0 GiB"} {
		if got := FormatBytes(in); got != want {
			t.Errorf("FormatBytes(%d) = %q want %q", in, got, want)
		}
	}
	if got := FormatRate(10 << 20); got != "10.0 MiB/s (84 Mbit/s)" {
		t.Errorf("FormatRate = %q", got)
	}
	if got := FormatETA(ETA{Seconds: 125, LowSeconds: 120, HighSeconds: 130, Steady: true}); got != "about 02:05" {
		t.Errorf("steady ETA %q", got)
	}
	if got := FormatETA(ETA{Seconds: 125, LowSeconds: 100, HighSeconds: 3700}); got != "01:40 to 1:01:40" {
		t.Errorf("range ETA %q", got)
	}
}

func TestLineSaysOnlyWhatTheSignalsSupport(t *testing.T) {
	const mib = 1 << 20
	// /MT: no completed totals, no percentage; activity is named as activity.
	cfg := serialConfig(10, 10*mib)
	cfg.Concurrent = true
	r := NewReplay(cfg, 0)
	r.Apply(Step{At: sec(1), Text: rule + fileLine("New File", mib, `C:\a`)}, nil)
	r.Apply(Step{At: sec(3), IO: &IOSample{ReadBytes: 6 * mib}}, nil)
	line := r.Tracker.Snapshot().Line()
	for _, want := range []string{"copy: active", "files announced", "completed totals at the end of the phase", "transfer activity", "read"} {
		if !strings.Contains(line, want) {
			t.Errorf("line %q lacks %q", line, want)
		}
	}
	for _, bad := range []string{"%", "remaining", "ETA"} {
		if strings.Contains(line, bad) {
			t.Errorf("line %q claims %q", line, bad)
		}
	}
	// Serial with a preview: completed totals and a percentage.
	r = NewReplay(serialConfig(10, 10*mib), 0)
	for i := 0; i < 4; i++ {
		r.Apply(Step{At: sec(float64(i + 1)), Text: copied(mib, `C:\f`)}, nil)
	}
	line = r.Tracker.Snapshot().Line()
	if !strings.Contains(line, "3 files, 3.0 MiB completed") || !strings.Contains(line, "% of bytes") || !strings.Contains(line, "% of files") {
		t.Errorf("line %q", line)
	}
	// A stall is described, and is not called a failure.
	r.AdvanceTo(sec(90), nil)
	line = r.Tracker.Snapshot().Line()
	if !strings.Contains(line, "stalled") || !strings.Contains(line, "not a failure") || strings.Contains(strings.ToLower(line), "failed") {
		t.Errorf("line %q", line)
	}
	// A list-only phase counts listed files.
	r = NewReplay(Config{Phase: "preview", List: true}, time.Second)
	r.Apply(Step{At: sec(1), Text: fileLine("New File", 5, `C:\a`) + "\r\n"}, nil)
	if line = r.Tracker.Snapshot().Line(); !strings.Contains(line, "1 files listed") {
		t.Errorf("line %q", line)
	}
}
