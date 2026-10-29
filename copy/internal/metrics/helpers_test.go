package metrics

import (
	"fmt"
	"strings"
	"time"
)

// Synthetic transcripts use the exact structure captured in RC0 (English Server
// 2022 and 2025 native logs): tab separated file lines with /BYTES and /FP, and
// the per-file percentage after a carriage return. They are test inputs, not
// recordings.

func fileLine(label string, size uint64, path string) string {
	return fmt.Sprintf("\t    %s  \t\t %9d\t%s", label, size, path)
}

// copied is a file line, its percentages and the end of line, as UNILOG holds it.
func copied(size uint64, path string) string {
	return fileLine("New File", size, path) + "\r  0%  \r 50%  \r100%  \r\n"
}

func dirLine(n int, path string) string { return fmt.Sprintf("\t  New Dir     %6d\t%s\r\n", n, path) }

const rule = "\r\n-------------------------------------------------------------------------------\r\n"

func summaryRows(dirs, files, bytes uint64) string {
	return fmt.Sprintf("\r\n               Total    Copied   Skipped  Mismatch    FAILED    Extras\r\n    Dirs :  %8d  %8d         0         0         0         0\r\n   Files :  %8d  %8d         0         0         0         0\r\n   Bytes :  %8d  %8d         0         0         0         0\r\n", dirs, dirs, files, files, bytes, bytes)
}

func errorHeader(code uint32, path string) string {
	return fmt.Sprintf("2026/10/29 12:00:05 ERROR %d (0x%08X) Copying File %s\r\nThe process cannot access the file because it is being used by another process.\r\n", code, code, path)
}

var epoch = time.Date(2026, time.October, 29, 12, 0, 0, 0, time.UTC)

func sec(n float64) time.Duration { return time.Duration(n * float64(time.Second)) }

func serialConfig(files, bytes uint64) Config {
	return Config{Phase: "copy", Planned: &Planned{Files: files, Bytes: bytes}, RetryWait: 5 * time.Second}
}

// steady builds a serial copy of n equal files, one finishing every period.
func steady(n int, size uint64, period time.Duration) []Step {
	steps := []Step{{At: 0, Text: rule}}
	for i := 0; i < n; i++ {
		steps = append(steps, Step{At: time.Duration(i+1) * period, Text: copied(size, fmt.Sprintf(`C:\src\f%04d.bin`, i))})
	}
	return steps
}

func repeat(s string, n int) string { return strings.Repeat(s, n) }
