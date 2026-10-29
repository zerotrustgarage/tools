package probe

import (
	"strings"
	"unicode"
)

// CleanText makes device-supplied text safe to store, export and show: it
// replaces invalid UTF-8 with nothing, turns every run of whitespace into one
// space, drops control, format (bidirectional overrides, zero-width marks),
// private-use and surrogate characters, trims, and stops after max runes.
// It never makes text trustworthy: the UI still inserts it with textContent
// and the exports still escape it. It only removes what would let a device
// disguise or reorder what a technician reads.
func CleanText(s string, max int) string {
	s = strings.ToValidUTF8(s, "")
	var sb strings.Builder
	n := 0
	space := false
	for _, r := range s {
		switch {
		case unicode.IsSpace(r) || r == 0x2028 || r == 0x2029:
			space = sb.Len() > 0
			continue
		case unicode.IsControl(r), unicode.Is(unicode.Cf, r), unicode.Is(unicode.Co, r), unicode.Is(unicode.Cs, r):
			continue
		}
		if n >= max {
			break
		}
		if space {
			if n+1 >= max {
				break
			}
			sb.WriteByte(' ')
			n++
			space = false
		}
		sb.WriteRune(r)
		n++
	}
	return sb.String()
}

// asciiToken reports whether s is 1 to max printable ASCII characters with no
// space. Scopes and service addresses are URIs, which are ASCII by definition.
func asciiToken(s string, max int) bool {
	if s == "" || len(s) > max {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < 0x21 || s[i] > 0x7e {
			return false
		}
	}
	return true
}
