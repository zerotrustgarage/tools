package metrics

import (
	"strings"
	"testing"
)

func scanAll(chunks ...string) []event {
	var events []event
	s := scanner{emit: func(e event) { events = append(events, e) }}
	for _, c := range chunks {
		s.write(c)
	}
	s.flush()
	return events
}

// kinds lists the event kinds; path updates are left out because where a write was
// split decides whether one is needed.
func kinds(events []event) []eventKind {
	var out []eventKind
	for _, e := range events {
		if e.kind != evFilePath {
			out = append(out, e.kind)
		}
	}
	return out
}

func withoutPaths(events []event) []event {
	var out []event
	for _, e := range events {
		if e.kind != evFilePath {
			out = append(out, e)
		}
	}
	return out
}

func TestScannerReadsRC0Shapes(t *testing.T) {
	// The Server 2025 native log fixture shape: file line, then the percentage after a carriage return.
	text := fileLine("New File", 81920, `C:\src\café-日本語-😀.txt`) + "\r100%  \r\n" + dirLine(0, `C:\src\empty\`) + rule + summaryRows(2, 1, 81920)
	events := scanAll(text)
	want := []eventKind{evFile, evPercent, evDir, evRule, evSummaryRow, evSummaryRow, evSummaryRow}
	events = withoutPaths(events)
	if got := kinds(events); len(got) != len(want) {
		t.Fatalf("events %v want %v", got, want)
	}
	for i, k := range want {
		if events[i].kind != k {
			t.Fatalf("event %d is %v want %v", i, events[i].kind, k)
		}
	}
	if events[0].size != 81920 || events[0].path != `C:\src\café-日本語-😀.txt` || events[0].label != "New File" {
		t.Fatalf("file line: %+v", events[0])
	}
	if events[1].percent != 100 {
		t.Fatalf("percent: %+v", events[1])
	}
}

func TestScannerFileLineArrivesInPiecesLikeTheConsole(t *testing.T) {
	// RC0 timing capture: the size and the path are separate writes and the
	// percentages arrive later with no line feed.
	events := scanAll("\t    New File  \t\t ", "4194304\t", `C:\src\slow.bin`, "\r  6%  ", "\r 12%", "  ", "\r100%  ", "\n")
	if got := kinds(events); len(got) != 4 || got[0] != evFile || got[1] != evPercent || got[2] != evPercent || got[3] != evPercent {
		t.Fatalf("events %v", got)
	}
	events = withoutPaths(events)
	if events[0].size != 4194304 || events[0].path != `C:\src\slow.bin` {
		t.Fatalf("file: %+v", events[0])
	}
	if events[1].percent != 6 || events[2].percent != 12 || events[3].percent != 100 {
		t.Fatalf("percentages: %+v", events[1:])
	}
}

func TestScannerAnnouncesAFileOnceEvenWhenItsPathArrivesLate(t *testing.T) {
	events := scanAll("\t    New File  \t\t 4194304\tC:\\src\\sl", "ow.bin\r\n")
	if len(events) != 2 || events[0].kind != evFile || events[0].size != 4194304 || events[1].kind != evFilePath || events[1].path != `C:\src\slow.bin` {
		t.Fatalf("events %+v", events)
	}
}

func TestScannerEverySplitGivesTheSameEvents(t *testing.T) {
	text := rule + dirLine(1, `C:\src\`) + copied(1000, `C:\src\a.bin`) + fileLine("same", 5, `C:\src\b.bin`) + "\r\n" + errorHeader(32, `C:\src\c.bin`) + copied(7, `C:\src\d.bin`) + rule + summaryRows(1, 3, 1012)
	want := withoutPaths(scanAll(text))
	for i := 0; i <= len(text); i++ {
		got := withoutPaths(scanAll(text[:i], text[i:]))
		if len(got) != len(want) {
			t.Fatalf("split at %d: %d events, want %d", i, len(got), len(want))
		}
		for k := range want {
			if got[k].kind != want[k].kind || got[k].size != want[k].size || got[k].percent != want[k].percent {
				t.Fatalf("split at %d event %d: %+v want %+v", i, k, got[k], want[k])
			}
		}
	}
}

func TestScannerIgnoresWhatItCannotRead(t *testing.T) {
	// Localized words, headers, odd percentages and tab-lookalikes are not events.
	for _, text := range []string{
		"  Started : Thursday, October 22, 2026 6:48:32 PM\r\n",
		"   Source : C:\\a\\\r\n",
		"\t    New File  \t\t abc\tC:\\x\r\n",
		"\t    New File  \t\t 12\t\r\n",
		"\t    New File  \t\t 99999999999999999999999\tC:\\x\r\n",
		"\r150%  \r\n",
		"\r-5%  \r\n",
		"   Times :   0:00:03   0:00:03                       0:00:00   0:00:00\r\n",
		"   Speed :           1,119,675 Bytes/sec.\r\n",
		"2026/10/29 12:00:05 ERROR 32 (0x00000021) Copying File x\r\n",
		"2026/10/29 12:00:05 ERROR 0 (0x00000000) nothing\r\n",
		"---\r\n",
	} {
		if events := scanAll(text); len(events) != 0 {
			t.Errorf("%q gave %+v", text, events)
		}
	}
}

func TestScannerErrorHeaderIsLanguageNeutral(t *testing.T) {
	// A German label: only the timestamp and the agreeing codes count.
	events := scanAll("2026/10/29 12:00:05 FEHLER 5 (0x00000005) Datei kopieren C:\\x\r\n")
	if len(events) != 1 || events[0].kind != evError || events[0].code != 5 {
		t.Fatalf("events %+v", events)
	}
}

func TestScannerDropsOversizeSegmentsAndKeepsLaterLines(t *testing.T) {
	events := scanAll(strings.Repeat("x", 3*maxSegment), "\r\n", copied(9, `C:\ok.bin`))
	// The file line and the markers 0, 50 and 100.
	if got := kinds(events); len(got) != 4 || got[0] != evFile || got[1] != evPercent || got[3] != evPercent {
		t.Fatalf("events %v", got)
	}
}

func FuzzMetricsScanner(f *testing.F) {
	f.Add(copied(4194304, `C:\src\slow.bin`) + rule + summaryRows(1, 1, 4194304))
	f.Add("\t    New File  \t\t 4194304\t")
	f.Add("\r  6%  \r 12%  \r100%")
	f.Add(errorHeader(5, `C:\x`) + "\t  New Dir          1\tC:\\a\r\n")
	f.Add("\xff\xfe\x00\t\t\t\t\t\t\r\r\n\n%%%")
	f.Fuzz(func(t *testing.T, text string) {
		var counted int
		s := scanner{emit: func(e event) {
			counted++
			if e.kind == evPercent && (e.percent < 0 || e.percent > 100) {
				t.Fatalf("percent out of range: %d", e.percent)
			}
			if e.kind == evFile && e.path == "" {
				t.Fatal("file event without a path")
			}
		}}
		// Feed in odd pieces; the buffer must stay bounded.
		for i := 0; i < len(text); i += 7 {
			end := i + 7
			if end > len(text) {
				end = len(text)
			}
			s.write(text[i:end])
			if len(s.buf) > maxSegment {
				t.Fatalf("buffer grew to %d", len(s.buf))
			}
		}
		s.flush()
		// The tracker must also survive any input.
		tr := NewTracker(Config{Phase: "copy", Planned: &Planned{Files: 3, Bytes: 100}})
		tr.Log(text)
		snap := tr.Snapshot()
		if snap.Completed.Bytes > snap.Seen.Bytes || snap.Completed.Files > snap.Seen.Files {
			t.Fatalf("completed %+v exceeds announced %+v", snap.Completed, snap.Seen)
		}
		tr.Finish(nil)
	})
}
