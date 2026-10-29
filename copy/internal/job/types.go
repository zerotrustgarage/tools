package job

import (
	"fmt"
	"math/bits"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/zerotrustgarage/tools/copy/internal/catalog"
)

// Value types come from the catalog (design section 4). CheckValue returns an
// empty string when the value is acceptable and a plain message otherwise. It is
// static: the installed Robocopy still has the last word (/QUIT, design section 5).

var (
	sizePattern   = regexp.MustCompile(`^([0-9]{1,19})([kKmMgG]?)$`)
	windowPattern = regexp.MustCompile(`^([01][0-9]|2[0-3])[0-5][0-9]-([01][0-9]|2[0-3])[0-5][0-9]$`)
)

// numberRange gives the documented bounds for numeric options; any other
// number must still be a decimal integer that fits in 63 bits.
func numberRange(token string) (uint64, uint64) {
	switch token {
	case "/MT":
		return 1, 128
	case "/R":
		return 0, 1000000
	case "/W":
		return 0, 86400
	case "/LEV", "/MON", "/MOT":
		return 1, 1<<31 - 1
	}
	return 0, 1<<63 - 1
}

func CheckValue(o catalog.Option, value string) string {
	switch o.Type {
	case "number":
		min, max := numberRange(o.Token)
		n, err := strconv.ParseUint(value, 10, 64)
		if err != nil || strings.Trim(value, "0123456789") != "" || n < min || n > max {
			return fmt.Sprintf("expected an integer from %d to %d", min, max)
		}
	case "size":
		m := sizePattern.FindStringSubmatch(value)
		if m == nil {
			return "expected a number with an optional K, M or G unit"
		}
		n, _ := strconv.ParseUint(m[1], 10, 64)
		shift := map[string]uint{"": 0, "k": 10, "m": 20, "g": 30}[strings.ToLower(m[2])]
		if n != 0 && uint(bits.Len64(n))+shift > 63 {
			return "size is too large"
		}
	case "age":
		return checkAge(value)
	case "attributes":
		allowed := "RASHCNETO"
		if o.Token == "/A+" {
			allowed = "RASHCNET"
		}
		return checkLetters(value, allowed, "attribute")
	case "copyflags":
		return checkLetters(value, "DATXSOU", "copy flag")
	case "dirflags":
		return checkLetters(value, "DATEX", "directory copy flag")
	case "window":
		if !windowPattern.MatchString(value) {
			return "expected a run window as hhmm-hhmm on a 24 hour clock"
		}
	case "yesno":
		if v := strings.ToLower(value); v != "y" && v != "n" {
			return "expected y or n"
		}
	case "path":
		if !textOK(value) || strings.ContainsAny(value, "\"*?<>|") || strings.HasPrefix(value, `\\?\`) || strings.HasPrefix(value, `\\.\`) {
			return "expected a file path without wildcards, quotes or device prefixes"
		}
	}
	return ""
}

func checkLetters(value, allowed, kind string) string {
	seen := map[rune]bool{}
	for _, r := range strings.ToUpper(value) {
		if !strings.ContainsRune(allowed, r) {
			return fmt.Sprintf("%q is not a %s; allowed letters are %s", string(r), kind, allowed)
		}
		if seen[r] {
			return fmt.Sprintf("%s %q is repeated", kind, string(r))
		}
		seen[r] = true
	}
	if len(seen) == 0 {
		return "at least one letter is required"
	}
	return ""
}

// checkAge follows Robocopy's rule for /MAXAGE and friends: a number below
// 1900 is a count of days; eight digits are a calendar date (YYYYMMDD).
func checkAge(value string) string {
	if value == "" || strings.Trim(value, "0123456789") != "" {
		return "expected a number of days or a date as YYYYMMDD"
	}
	if len(value) == 8 {
		if _, err := time.Parse("20060102", value); err != nil {
			return "not a valid calendar date (YYYYMMDD)"
		}
		return ""
	}
	n, err := strconv.ParseUint(value, 10, 16)
	if err != nil || n >= 1900 {
		return "a day count must be below 1900; use YYYYMMDD for a date"
	}
	return ""
}
