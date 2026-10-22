// Package parse reads correctness facts from Robocopy's native Unicode log.
package parse

import (
	"encoding/binary"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf16"
)

type Counts struct {
	Total    uint64 `json:"total"`
	Copied   uint64 `json:"copied"`
	Skipped  uint64 `json:"skipped"`
	Mismatch uint64 `json:"mismatch"`
	Failed   uint64 `json:"failed"`
	Extras   uint64 `json:"extras"`
}

type Summary struct {
	Directories Counts `json:"directories"`
	Files       Counts `json:"files"`
	Bytes       Counts `json:"bytes"`
}

var numericRow = regexp.MustCompile(`(?m)^[ \t]*[^:\r\n]+:[ \t]+([0-9]+)[ \t]+([0-9]+)[ \t]+([0-9]+)[ \t]+([0-9]+)[ \t]+([0-9]+)[ \t]+([0-9]+)[ \t]*\r?$`)

func Unicode(data []byte) (string, error) {
	if len(data) < 2 || len(data)%2 != 0 || data[0] != 0xff || data[1] != 0xfe {
		return "", errors.New("native log must have a UTF-16LE BOM and complete code units")
	}
	units := make([]uint16, 0, len(data)/2)
	for i := 2; i < len(data); i += 2 {
		u := binary.LittleEndian.Uint16(data[i:])
		if u >= 0xd800 && u <= 0xdbff {
			if i+3 >= len(data) {
				return "", errors.New("incomplete UTF-16 surrogate pair")
			}
			next := binary.LittleEndian.Uint16(data[i+2:])
			if next < 0xdc00 || next > 0xdfff {
				return "", errors.New("invalid UTF-16 surrogate pair")
			}
			units = append(units, u, next)
			i += 2
			continue
		}
		if u >= 0xdc00 && u <= 0xdfff {
			return "", errors.New("unpaired UTF-16 surrogate")
		}
		units = append(units, u)
	}
	return string(utf16.Decode(units)), nil
}

func NativeSummary(data []byte) (Summary, error) {
	text, err := Unicode(data)
	if err != nil {
		return Summary{}, err
	}
	rows := numericRow.FindAllStringSubmatch(text, -1)
	if len(rows) != 3 {
		return Summary{}, fmt.Errorf("expected three unambiguous byte-summary rows, got %d", len(rows))
	}
	counts := make([]Counts, 3)
	for i, row := range rows {
		n := [6]uint64{}
		for k := range n {
			n[k], err = strconv.ParseUint(row[k+1], 10, 64)
			if err != nil {
				return Summary{}, errors.New("summary count overflows uint64")
			}
		}
		counts[i] = Counts{n[0], n[1], n[2], n[3], n[4], n[5]}
	}
	return Summary{counts[0], counts[1], counts[2]}, nil
}

func (s Summary) Converged() bool {
	for _, c := range []Counts{s.Directories, s.Files, s.Bytes} {
		if c.Copied != 0 || c.Mismatch != 0 || c.Failed != 0 || c.Extras != 0 {
			return false
		}
	}
	return true
}

// Exit preserves Robocopy's bitmap semantics. Unknown values are tool errors.
func Exit(native uint32) int {
	if native > 31 {
		return 3
	}
	if native&24 != 0 {
		return 2
	}
	if native&4 != 0 {
		return 1
	}
	return 0
}

func SupportedTokens(data []byte) map[string]bool {
	result := map[string]bool{}
	for _, token := range regexp.MustCompile(`/[A-Za-z][A-Za-z0-9+-]*|/256`).FindAllString(string(data), -1) {
		result[strings.ToUpper(token)] = true
	}
	return result
}
