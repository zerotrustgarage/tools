package metrics

import (
	"time"

	"github.com/zerotrustgarage/tools/copy/internal/parse"
)

func (t *Tracker) point(now time.Time) Point {
	p := Point{TMS: now.Sub(t.start).Milliseconds(), ReadBytes: t.readS.latest()}
	p.Status, _ = t.status(now)
	if t.completedKnown() {
		p.CompletedBytes, p.CompletedFiles = t.doneBytes, t.doneFiles
		if r, ok := t.bytesS.rate(now, t.cfg.CurrentWindow, MinRateSpan); ok {
			p.CompletedBytesPerSec = round(r)
		}
	}
	if t.ioSeen {
		if r, ok := t.readS.rate(now, t.cfg.CurrentWindow, MinRateSpan); ok {
			p.ActivityBytesPerSec = round(r)
		}
	}
	return p
}

func (t *Tracker) pointFinal(now time.Time, f *Final) Point {
	p := t.point(now)
	p.Status = StatusFinished
	if f.Completed != nil {
		p.CompletedBytes, p.CompletedFiles = f.Completed.Bytes, f.Completed.Files
	}
	return p
}

func (t *Tracker) buildFinal(now time.Time, summary *parse.Summary) Final {
	elapsed := now.Sub(t.start)
	f := Final{
		Phase: t.cfg.Phase, ElapsedMS: elapsed.Milliseconds(), Source: "none",
		Seen:              Seen{Files: t.files, Bytes: t.seenBytes, Dirs: t.dirs},
		ActivityAvailable: t.ioSeen, ActivityReadBytes: t.readS.latest(),
		ErrorLines: t.errorLines, StalledMS: t.stalledTotal.Milliseconds(), StallsOmitted: t.stallsOmit,
		Concurrent: t.cfg.Concurrent, ListOnly: t.cfg.List,
		WindowSeconds: int(t.cfg.CurrentWindow / time.Second), StallAfterSecs: int(t.cfg.StallAfter / time.Second),
	}
	for _, s := range t.stalls {
		f.Stalls = append(f.Stalls, Stall{StartMS: s.start.Sub(t.start).Milliseconds(), EndMS: s.end.Sub(t.start).Milliseconds()})
	}
	known := t.completedKnown()
	log := &Totals{Files: t.doneFiles, Bytes: t.doneBytes}
	switch {
	case t.cfg.List:
		f.Limits = append(f.Limits, "A list-only phase copies nothing; nothing is completed.")
	case summary != nil:
		f.Source = "summary"
		f.Completed = &Totals{Files: summary.Files.Copied, Bytes: summary.Bytes.Copied}
		if known {
			f.LogCompleted = log
		}
	case known:
		f.Source = "log"
		f.Completed = log
		f.Limits = append(f.Limits, "No Robocopy summary was read, so these totals come from log accounting and may be incomplete.")
	}
	if !t.cfg.List && !known {
		switch {
		case t.cfg.Concurrent:
			f.Limits = append(f.Limits, "With /MT finished files cannot be told apart while the phase runs; no live completed totals, rates or percentages were produced.")
		case t.cfg.AccountingLimit != "":
			f.Limits = append(f.Limits, t.cfg.AccountingLimit)
		}
	}
	if f.Completed != nil && elapsed >= MinRateSpan {
		secs := elapsed.Seconds()
		f.AverageBytesPerSec = ptr(round(float64(f.Completed.Bytes) / secs))
		files := twoDecimals(float64(f.Completed.Files) / secs)
		f.FilesPerSecAverage = &files
	}
	if known && t.peakRate > 0 {
		f.PeakBytesPerSec = ptr(round(t.peakRate))
	}
	if t.ioSeen {
		if elapsed >= MinRateSpan {
			f.ActivityAverageBytesPerSec = ptr(round(float64(t.readS.latest()) / elapsed.Seconds()))
		}
		if t.peakActivity > 0 {
			f.ActivityPeakBytesPerSec = ptr(round(t.peakActivity))
		}
	} else {
		f.Limits = append(f.Limits, "Process I/O counters were not available, so there is no transfer-activity reading.")
	}
	p, _ := t.progressReading()
	f.ProgressAvailable = p != nil
	if t.stallsOmit > 0 {
		f.Limits = append(f.Limits, "More stalls occurred than the record keeps; the stalled total still includes them.")
	}
	return f
}
