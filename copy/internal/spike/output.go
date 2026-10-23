package spike

import (
	"encoding/binary"
	"errors"
	"regexp"
	"sort"
	"strings"
	"unicode/utf16"
)

// DecodeUnicode decodes a complete /UNICODE pipe capture. Do not call on chunks:
// reads can split UTF-16 code units and surrogate pairs.
func DecodeUnicode(raw []byte) (string, error) {
	if len(raw)%2 != 0 {
		return "", errors.New("truncated UTF-16LE output")
	}
	if len(raw) >= 2 && raw[0] == 0xfe && raw[1] == 0xff {
		return "", errors.New("unexpected UTF-16BE output")
	}
	if len(raw) >= 2 && raw[0] == 0xff && raw[1] == 0xfe {
		raw = raw[2:]
	}
	units := make([]uint16, len(raw)/2)
	for i := range units {
		units[i] = binary.LittleEndian.Uint16(raw[i*2:])
	}
	for i := 0; i < len(units); i++ {
		u := units[i]
		if u >= 0xd800 && u <= 0xdbff {
			if i+1 >= len(units) || units[i+1] < 0xdc00 || units[i+1] > 0xdfff {
				return "", errors.New("unpaired UTF-16 high surrogate")
			}
			i++
		} else if u >= 0xdc00 && u <= 0xdfff {
			return "", errors.New("unpaired UTF-16 low surrogate")
		}
	}
	return string(utf16.Decode(units)), nil
}

var optionPattern = regexp.MustCompile(`(?i)/([A-Z][A-Z0-9+-]*|256)(?:[:\s\[\]|,]|$)`)

// HelpOptions extracts invariant option tokens, not localized help sentences.
func HelpOptions(help string) []string {
	found := map[string]bool{}
	for _, match := range optionPattern.FindAllStringSubmatch(help, -1) {
		found["/"+strings.ToUpper(match[1])] = true
	}
	options := make([]string, 0, len(found))
	for option := range found {
		options = append(options, option)
	}
	sort.Strings(options)
	return options
}

// NormalizedExit preserves the native code separately in every capture.
func NormalizedExit(native int) int {
	switch {
	case native < 0 || native > 31:
		return 3
	case native >= 8:
		return 2
	case native&4 != 0:
		return 1
	default:
		return 0
	}
}
