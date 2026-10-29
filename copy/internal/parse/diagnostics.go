package parse

import (
	"errors"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

const diagnosticLineBytes = 128 << 10
const diagnosticGroups = 32
const diagnosticSamples = 3

// Diagnostics describe recognized native-log error headers, never run outcomes.
// Counts include repeated attempts, and samples are bounded excerpts.
type Diagnostics struct {
	Errors             []ErrorGroup `json:"errors"`
	OmittedOccurrences uint64       `json:"omitted_occurrences,omitempty"`
	SkippedLines       uint64       `json:"skipped_lines,omitempty"`
	Unavailable        string       `json:"unavailable,omitempty"`
}

type ErrorGroup struct {
	Code       uint32   `json:"code"`
	Count      uint64   `json:"count"`
	Cause      string   `json:"cause"`
	Suggestion string   `json:"suggestion"`
	Samples    []string `json:"samples"`
}

// The localized label is deliberately ignored. Require a timestamp and agreeing
// decimal/hex codes so filenames and unrelated numbers do not become errors.
var errorHeader = regexp.MustCompile(`^[ \t]*[0-9]{4}/[0-9]{2}/[0-9]{2}[ \t]+[0-9]{2}:[0-9]{2}:[0-9]{2}[ \t]+[^0-9()\r\n]+([0-9]+)[ \t]+\(0[xX]([0-9a-fA-F]{8})\)([ \t]+.*)?$`)

type headerStatus int

const (
	headerNone headerStatus = iota
	headerDisagrees
	headerOK
)

// errorHeaderCode reads the timestamped native-log error header shape used by
// NativeDiagnostics. The localized label is ignored.
func errorHeaderCode(line string) (uint64, headerStatus) {
	match := errorHeader.FindStringSubmatch(line)
	if match == nil {
		return 0, headerNone
	}
	code, decErr := strconv.ParseUint(match[1], 10, 32)
	hexCode, hexErr := strconv.ParseUint(match[2], 16, 32)
	if decErr != nil || hexErr != nil || code != hexCode {
		return 0, headerDisagrees
	}
	return code, headerOK
}

// ErrorHeader reports whether line is a native-log error header whose decimal and
// hexadecimal Windows codes agree, and returns the code. Code 0 is a header too
// (the engine prints it for some informational entries); callers that count real
// errors should ignore it.
func ErrorHeader(line string) (uint32, bool) {
	code, status := errorHeaderCode(strings.TrimSuffix(line, "\r"))
	return uint32(code), status == headerOK
}

// NativeDiagnostics streams UTF-16LE with bounded memory, even for long lines.
// A malformed or oversized line is skipped and counted; later headers survive.
func NativeDiagnostics(r io.Reader) (Diagnostics, error) {
	report := Diagnostics{Errors: []ErrorGroup{}}
	var bom [2]byte
	if _, err := io.ReadFull(r, bom[:]); err != nil || bom != [2]byte{0xff, 0xfe} {
		return report, errors.New("diagnostic log requires a UTF-16LE BOM")
	}
	groups := map[uint32]int{}
	line := make([]byte, 0, 4096)
	skipping := false
	consume := func() {
		if skipping {
			report.SkippedLines++
			skipping = false
			line = line[:0]
			return
		}
		data := make([]byte, len(line)+2)
		copy(data, bom[:])
		copy(data[2:], line)
		line = line[:0]
		text, err := Unicode(data)
		if err != nil {
			report.SkippedLines++
			return
		}
		code, status := errorHeaderCode(strings.TrimSuffix(text, "\r"))
		if status == headerNone {
			return
		}
		if status == headerDisagrees {
			report.SkippedLines++
			return
		}
		if code == 0 {
			return
		}
		index, exists := groups[uint32(code)]
		if !exists {
			if len(report.Errors) >= diagnosticGroups {
				report.OmittedOccurrences++
				return
			}
			index = len(report.Errors)
			groups[uint32(code)] = index
			cause, suggestion := errorAdvice(uint32(code))
			report.Errors = append(report.Errors, ErrorGroup{Code: uint32(code), Cause: cause, Suggestion: suggestion, Samples: []string{}})
		}
		group := &report.Errors[index]
		group.Count++
		sample := strings.TrimSpace(text)
		runes := []rune(sample)
		if len(runes) > 512 {
			sample = string(runes[:512]) + "…"
		}
		if len(group.Samples) < diagnosticSamples {
			for _, existing := range group.Samples {
				if existing == sample {
					return
				}
			}
			group.Samples = append(group.Samples, sample)
		}
	}
	buffer := make([]byte, 64<<10)
	var first byte
	pending := false
	for {
		n, err := r.Read(buffer)
		for _, b := range buffer[:n] {
			if !pending {
				first, pending = b, true
				continue
			}
			pending = false
			if first == 10 && b == 0 {
				consume()
			} else if !skipping {
				if len(line) >= diagnosticLineBytes {
					line = line[:0]
					skipping = true
				} else {
					line = append(line, first, b)
				}
			}
		}
		if err != nil {
			if !errors.Is(err, io.EOF) {
				return report, err
			}
			break
		}
	}
	if pending {
		skipping = true
	}
	if len(line) > 0 || skipping {
		consume()
	}
	sort.Slice(report.Errors, func(i, j int) bool { return report.Errors[i].Code < report.Errors[j].Code })
	return report, nil
}

func errorAdvice(code uint32) (string, string) {
	switch code {
	case 2:
		return "File not found", "Check whether the source file was moved or deleted while the job was running."
	case 3:
		return "Path not found", "Check that the folders still exist and that any mapped or network path is available."
	case 5:
		return "Access denied", "Check the current account's read permission on the source and write permission on the destination."
	case 32, 33:
		return "File in use", "Close the application holding the file, or retry during a maintenance window."
	case 39, 112:
		return "Disk full", "Free space on the destination volume, then preview the job again."
	case 53, 67:
		return "Network path unavailable", "Check the server name, share name, DNS and network connection."
	case 64:
		return "Network connection lost", "Restore the connection and check the server before retrying."
	case 123:
		return "Invalid path or filename", "Check the reported path for characters or syntax the destination filesystem does not support."
	case 206:
		return "Path too long", "Review the reported path and the long-path limits of the destination and installed Windows version."
	case 995:
		return "Operation canceled", "Review the final run state and native log before retrying an interrupted copy."
	case 1219:
		return "Conflicting network credentials", "Check existing connections to the server and use a consistent account. Copy does not disconnect them automatically."
	case 1326:
		return "Sign-in failed", "Check the account and authentication setup for the endpoint. Do not put passwords in job files."
	default:
		return fmt.Sprintf("Windows error %d", code), "Review the native log and the Windows system error reference for this code."
	}
}
