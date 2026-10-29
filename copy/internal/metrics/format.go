package metrics

import (
	"fmt"
	"strings"
	"time"
)

// FormatBytes writes a byte count in binary units, as the Activity view does.
func FormatBytes(n uint64) string {
	units := []string{"B", "KiB", "MiB", "GiB", "TiB"}
	v, unit := float64(n), 0
	for v >= 1024 && unit < len(units)-1 {
		v /= 1024
		unit++
	}
	if unit == 0 {
		return fmt.Sprintf("%d B", n)
	}
	return fmt.Sprintf("%.1f %s", v, units[unit])
}

// FormatRate writes bytes per second as MiB/s and Mbit/s (decimal megabits, the
// network convention), per design section 6.
func FormatRate(bytesPerSec uint64) string {
	mib := float64(bytesPerSec) / (1 << 20)
	mbit := float64(bytesPerSec) * 8 / 1e6
	return fmt.Sprintf("%.1f MiB/s (%.0f Mbit/s)", mib, mbit)
}

func clockText(d time.Duration) string {
	s := int64(d / time.Second)
	if s >= 3600 {
		return fmt.Sprintf("%d:%02d:%02d", s/3600, s/60%60, s%60)
	}
	return fmt.Sprintf("%02d:%02d", s/60, s%60)
}

// FormatETA writes a time remaining, as a range when the estimates disagree.
func FormatETA(e ETA) string {
	span := func(sec int64) string { return clockText(time.Duration(sec) * time.Second) }
	if e.Steady {
		return "about " + span(e.Seconds)
	}
	return span(e.LowSeconds) + " to " + span(e.HighSeconds)
}

// Line is one plain progress line for the CLI. It says only what the signals
// support: completed totals and a percentage appear only when they are known.
func (s Snapshot) Line() string {
	parts := []string{fmt.Sprintf("%s: %s, elapsed %s", s.Phase, s.Status, clockText(time.Duration(s.ElapsedMS)*time.Millisecond))}
	switch {
	case s.ListOnly:
		parts = append(parts, fmt.Sprintf("%d files listed", s.Seen.Files))
	case s.Completed.Known:
		parts = append(parts, fmt.Sprintf("%d files, %s completed", s.Completed.Files, FormatBytes(s.Completed.Bytes)))
		if s.Rate.CompletedBytesPerSec != nil {
			parts = append(parts, fmt.Sprintf("%s completed over %d s", FormatRate(*s.Rate.CompletedBytesPerSec), s.Rate.WindowSeconds))
		}
	default:
		parts = append(parts, fmt.Sprintf("%d files announced, completed totals at the end of the phase", s.Seen.Files))
	}
	if s.Activity.Available && s.Activity.BytesPerSec != nil {
		parts = append(parts, fmt.Sprintf("transfer activity %s read", FormatRate(*s.Activity.BytesPerSec)))
	}
	if s.Progress != nil {
		var pct []string
		if s.Progress.BytesPercent != nil {
			pct = append(pct, fmt.Sprintf("%.1f%% of bytes", *s.Progress.BytesPercent))
		}
		if s.Progress.FilesPercent != nil {
			pct = append(pct, fmt.Sprintf("%.1f%% of files", *s.Progress.FilesPercent))
		}
		parts = append(parts, strings.Join(pct, ", "))
	}
	if s.ETA != nil {
		parts = append(parts, "remaining "+FormatETA(*s.ETA))
	}
	line := strings.Join(parts, "; ")
	if s.Status == StatusStalled || s.Status == StatusRetrying {
		line += ". " + s.StatusNote
	}
	return line
}
