package run

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf16"

	"github.com/zerotrustgarage/tools/copy/internal/engine"
	"github.com/zerotrustgarage/tools/copy/internal/job"
	"github.com/zerotrustgarage/tools/copy/internal/metrics"
	"github.com/zerotrustgarage/tools/copy/internal/parse"
)

func utf16le(text string, bom bool) []byte {
	var out []byte
	if bom {
		out = append(out, 0xff, 0xfe)
	}
	for _, u := range utf16.Encode([]rune(text)) {
		out = append(out, byte(u), byte(u>>8))
	}
	return out
}

func appendLog(t *testing.T, path string, data []byte) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.Write(data); err != nil {
		t.Fatal(err)
	}
}

func file(size int, path string) string {
	return "\t    New File  \t\t " + itoa(size) + "\t" + path + "\r  0%  \r100%  \r\n"
}

func itoa(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}

type collector struct {
	mu    sync.Mutex
	snaps []metrics.Snapshot
}

func (c *collector) add(s metrics.Snapshot) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.snaps = append(c.snaps, s)
}

func (c *collector) last() metrics.Snapshot {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.snaps[len(c.snaps)-1]
}

func TestMonitorFollowsTheGrowingLogAndTheEngineCounters(t *testing.T) {
	clock := metrics.NewFakeClock(time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC))
	logPath := filepath.Join(t.TempDir(), "copy.log")
	cfg := metrics.ConfigFor(job.Job{Options: []string{"/E", "/R:2", "/W:5"}}, "copy", false, &metrics.Planned{Files: 3, Bytes: 3 << 20})
	cfg.Clock = clock
	var seen collector
	m := newMonitor(cfg, logPath, seen.add)
	appendLog(t, logPath, utf16le("\r\n-------------------------------------------------------------------------------\r\n", true))
	m.step()
	for i := 1; i <= 3; i++ {
		clock.Advance(2 * time.Second)
		appendLog(t, logPath, utf16le(file(1<<20, `C:\src\café-日本語-😀.bin`), false))
		m.io(engine.IOCounters{ReadBytes: uint64(i) << 20, WriteBytes: uint64(i)<<20 + 4096})
		m.step()
	}
	clock.Advance(time.Second)
	appendLog(t, logPath, utf16le("\r\n-------------------------------------------------------------------------------\r\n", false))
	m.step()
	live := seen.last()
	if live.Completed.Files != 3 || live.Completed.Bytes != 3<<20 || live.Activity.ReadBytes != 3<<20 || live.Status != metrics.StatusActive {
		t.Fatalf("live reading %+v", live)
	}
	if live.Progress == nil || *live.Progress.BytesPercent != 100 {
		t.Fatalf("progress %+v", live.Progress)
	}
	summary := &parse.Summary{Files: parse.Counts{Total: 3, Copied: 3}, Bytes: parse.Counts{Total: 3 << 20, Copied: 3 << 20}}
	final := m.finish(summary)
	if final.Source != "summary" || final.Completed.Bytes != 3<<20 || final.LogCompleted == nil || final.LogCompleted.Bytes != 3<<20 || final.ActivityReadBytes != 3<<20 {
		t.Fatalf("final %+v", final)
	}
	if got := seen.last(); got.Status != metrics.StatusFinished {
		t.Fatalf("the last published reading should be the finished one: %+v", got)
	}
	// Writes are not progress: only reads count as activity.
	if final.ActivityReadBytes == 3<<20+3*4096 {
		t.Fatal("write bytes were counted as activity")
	}
}

func TestMonitorSurvivesALogItCannotReadAndSaysSo(t *testing.T) {
	clock := metrics.NewFakeClock(time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC))
	logPath := filepath.Join(t.TempDir(), "copy.log")
	appendLog(t, logPath, []byte("not a unicode log"))
	cfg := metrics.ConfigFor(job.Job{}, "copy", false, nil)
	cfg.Clock = clock
	m := newMonitor(cfg, logPath, nil)
	m.step()
	clock.Advance(time.Second)
	m.io(engine.IOCounters{ReadBytes: 4096})
	m.step()
	final := m.finish(nil)
	if final.ActivityReadBytes != 4096 {
		t.Fatalf("the I/O signal should still work: %+v", final)
	}
	if !strings.Contains(strings.Join(final.Limits, " "), "could not be read while it was being written") {
		t.Fatalf("limits %v", final.Limits)
	}
}

func TestMonitorGoroutineStopsAndFinishIsRepeatable(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "copy.log")
	var seen collector
	m := newMonitor(metrics.ConfigFor(job.Job{}, "preview", true, nil), logPath, seen.add)
	m.start(5 * time.Millisecond)
	deadline := time.Now().Add(2 * time.Second)
	for {
		seen.mu.Lock()
		n := len(seen.snaps)
		seen.mu.Unlock()
		if n >= 3 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the monitor published nothing")
		}
		time.Sleep(time.Millisecond)
	}
	first := m.finish(nil)
	second := m.finish(nil)
	if first.Phase != "preview" || second.Phase != "preview" || !first.ListOnly {
		t.Fatalf("%+v %+v", first, second)
	}
	seen.mu.Lock()
	n := len(seen.snaps)
	seen.mu.Unlock()
	time.Sleep(30 * time.Millisecond)
	seen.mu.Lock()
	defer seen.mu.Unlock()
	if len(seen.snaps) != n {
		t.Fatalf("the goroutine kept publishing after finish: %d then %d", n, len(seen.snaps))
	}
}

func TestPhaseMetricsRoundTripThroughRunJSONAndOldRecordsStillLoad(t *testing.T) {
	clock := metrics.NewFakeClock(time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC))
	tracker := metrics.NewTracker(metrics.Config{Phase: "copy", Clock: clock, Planned: &metrics.Planned{Files: 1, Bytes: 10}})
	clock.Advance(3 * time.Second)
	tracker.Log("\t    New File  \t\t 10\tC:\\a\r100%  \r\n-------------------------------------------------------------------------------\r\n")
	final := tracker.Finish(&parse.Summary{Files: parse.Counts{Total: 1, Copied: 1}, Bytes: parse.Counts{Total: 10, Copied: 10}})
	record := Record{Schema: 1, State: "copied", Phases: []Phase{{Name: "help"}, {Name: "copy", Metrics: &final}}}
	data, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	var back Record
	if err := job.DecodeStrict(strings.NewReader(string(data)), &back); err != nil {
		t.Fatalf("strict decode (as the history view does): %v", err)
	}
	if back.Phases[0].Metrics != nil || back.Phases[1].Metrics == nil || back.Phases[1].Metrics.Completed.Bytes != 10 || len(back.Phases[1].Metrics.Timeline) == 0 {
		t.Fatalf("round trip: %+v", back.Phases)
	}
	// A record written before metrics existed.
	old := `{"schema":1,"state":"copied","phases":[{"name":"copy","process":{"arguments":null,"started":"0001-01-01T00:00:00Z","elapsed_ms":1,"native_exit":1,"canceled":false,"in_job_before_resume":true}}]}`
	var legacy Record
	if err := job.DecodeStrict(strings.NewReader(old), &legacy); err != nil || legacy.Phases[0].Metrics != nil {
		t.Fatalf("old record: %v %+v", err, legacy)
	}
	if strings.Contains(string(data), "C:\\\\a") {
		t.Fatal("the metrics record holds no file names")
	}
}
