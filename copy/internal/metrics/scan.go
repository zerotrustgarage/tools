package metrics

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/zerotrustgarage/tools/copy/internal/parse"
)

// The scanner reads the structure of Robocopy's file list, never its localized
// words. What it relies on (RC0 fixtures, English Server 2022 and 2025):
//
//   - a file line is tab separated: empty, label, empty, size, path (with /BYTES
//     and /FP); a directory line is empty, "label count", path;
//   - the per-file percentage is written after the file line, separated by a
//     carriage return, as "NN%" padded with spaces; a copied file ends with 100%;
//   - error headers carry a timestamp and a decimal and hexadecimal Windows code.
//
// A file line is an announcement. It never proves that a file finished (RC0
// question 1), so the scanner only reports what was written, and the tracker
// decides what counts as completed.

type eventKind int

const (
	evFile eventKind = iota + 1
	evDir
	evPercent
	evError
	evRule
	evSummaryRow
	// evFilePath updates the path of a file line that was announced before its
	// path had fully arrived.
	evFilePath
)

type event struct {
	kind    eventKind
	label   string
	size    uint64
	path    string
	percent int
	code    uint32
}

// maxSegment bounds memory for hostile or damaged output; a longer segment is
// dropped up to its terminator.
const maxSegment = 1 << 16

var (
	percentPattern = regexp.MustCompile(`^[0-9]{1,3}%$`)
	dirFieldRegexp = regexp.MustCompile(`^\s*(\S.*?)\s+(-?[0-9]+)\s*$`)
	summaryRow     = regexp.MustCompile(`^[ \t]*[^:\r\n]+:[ \t]+[0-9]+([ \t]+[0-9]+){5}[ \t]*$`)
)

type scanner struct {
	buf       []byte
	skipping  bool
	announced bool
	lastPath  string
	emit      func(event)
}

// write consumes decoded text in arbitrary pieces. Carriage return and line
// feed both end a segment, so the percentage markers that follow a file line
// are separate segments.
func (s *scanner) write(text string) {
	for i := 0; i < len(text); i++ {
		b := text[i]
		if b == '\n' || b == '\r' {
			s.finish()
			continue
		}
		if s.skipping {
			continue
		}
		if len(s.buf) >= maxSegment {
			s.buf, s.skipping = s.buf[:0], true
			continue
		}
		s.buf = append(s.buf, b)
	}
	s.early()
}

// early reports what an unterminated segment already proves. A percentage is
// complete as soon as its percent sign arrives. A file or directory line is
// announced once its size or count and a path start have arrived, because
// Robocopy can write a large file's line and then stay silent until the file
// ends. The path of an early announcement can be a prefix; sizes cannot.
func (s *scanner) early() {
	if s.skipping || len(s.buf) == 0 {
		return
	}
	text := string(s.buf)
	if trimmed := strings.TrimSpace(text); percentPattern.MatchString(trimmed) {
		s.emitPercent(trimmed)
		s.buf = s.buf[:0]
		return
	}
	if s.announced {
		s.updatePath(text)
		return
	}
	if ev, ok := classifyList(text); ok {
		s.announced = true
		s.lastPath = ev.path
		s.emit(ev)
	}
}

func (s *scanner) updatePath(text string) {
	if ev, ok := classifyList(text); ok && ev.kind == evFile && ev.path != s.lastPath {
		s.lastPath = ev.path
		s.emit(event{kind: evFilePath, size: ev.size, path: ev.path})
	}
}

func (s *scanner) finish() {
	if s.skipping {
		s.buf, s.skipping, s.announced, s.lastPath = s.buf[:0], false, false, ""
		return
	}
	text := string(s.buf)
	announced := s.announced
	if announced {
		s.updatePath(text)
	}
	s.buf, s.announced, s.lastPath = s.buf[:0], false, ""
	if announced {
		return
	}
	if ev, ok := classify(text); ok {
		s.emit(ev)
	}
}

func (s *scanner) flush() { s.finish() }

func (s *scanner) emitPercent(trimmed string) {
	n, err := strconv.Atoi(strings.TrimSuffix(trimmed, "%"))
	if err == nil && n >= 0 && n <= 100 {
		s.emit(event{kind: evPercent, percent: n})
	}
}

func classify(segment string) (event, bool) {
	trimmed := strings.TrimSpace(segment)
	if trimmed == "" {
		return event{}, false
	}
	if percentPattern.MatchString(trimmed) {
		n, err := strconv.Atoi(strings.TrimSuffix(trimmed, "%"))
		if err != nil || n > 100 {
			return event{}, false
		}
		return event{kind: evPercent, percent: n}, true
	}
	if ev, ok := classifyList(segment); ok {
		return ev, true
	}
	if code, ok := parse.ErrorHeader(segment); ok && code != 0 {
		return event{kind: evError, code: code}, true
	}
	if len(trimmed) >= 20 && strings.Trim(trimmed, "-") == "" {
		return event{kind: evRule}, true
	}
	if summaryRow.MatchString(segment) {
		return event{kind: evSummaryRow}, true
	}
	return event{}, false
}

func classifyList(segment string) (event, bool) {
	if !strings.HasPrefix(segment, "\t") {
		return event{}, false
	}
	fields := strings.Split(segment, "\t")
	switch len(fields) {
	case 5:
		digits := strings.TrimSpace(fields[3])
		path := fields[4]
		if digits == "" || strings.Trim(digits, "0123456789") != "" || path == "" || strings.TrimSpace(fields[0]) != "" {
			return event{}, false
		}
		size, err := strconv.ParseUint(digits, 10, 64)
		if err != nil {
			return event{}, false
		}
		return event{kind: evFile, label: strings.TrimSpace(fields[1]), size: size, path: path}, true
	case 3:
		match := dirFieldRegexp.FindStringSubmatch(fields[1])
		if match == nil || fields[2] == "" || strings.TrimSpace(fields[0]) != "" {
			return event{}, false
		}
		return event{kind: evDir, label: match[1], path: fields[2]}, true
	}
	return event{}, false
}
