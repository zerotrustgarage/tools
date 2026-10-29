package targets

import (
	"errors"
	"net/netip"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	// MaxListBytes bounds both input and normalized target text.
	MaxListBytes = 64 * 1024
	// MaxListTokens bounds the number of independently-entered target specs.
	MaxListTokens = 4096
)

var errInvalidList = errors.New("target list contains an invalid target")

// NormalizeList validates and normalizes target syntax without resolving
// hostnames or checking whether addresses are in scan scope.
func NormalizeList(input string) (string, error) {
	if len(input) > MaxListBytes {
		return "", errors.New("target list exceeds the 64 KiB limit")
	}
	if !utf8.ValidString(input) {
		return "", errors.New("target list must be valid UTF-8")
	}
	if strings.ContainsRune(input, '\x00') {
		return "", errors.New("target list contains a prohibited control character")
	}
	input = strings.TrimPrefix(input, "\uFEFF")
	var specs []string
	seen := make(map[string]struct{})
	tokenCount := 0
	for _, line := range strings.Split(input, "\n") {
		if i := strings.IndexRune(line, '#'); i >= 0 {
			line = line[:i]
		}
		for _, r := range line {
			if unicode.IsControl(r) && r != '\t' && r != '\r' && r != '\n' {
				return "", errors.New("target list contains a prohibited control character")
			}
		}
		for _, spec := range strings.FieldsFunc(line, func(r rune) bool {
			return r == ',' || r == ';' || unicode.IsSpace(r)
		}) {
			tokenCount++
			if tokenCount > MaxListTokens {
				return "", errors.New("target list exceeds the 4096 target limit")
			}
			if err := validateListSpec(spec); err != nil {
				if errors.Is(err, ErrTooMany) {
					return "", errors.New("target list contains a range larger than the 65,536 address limit")
				}
				return "", errInvalidList
			}
			if _, ok := seen[spec]; ok {
				continue
			}
			seen[spec] = struct{}{}
			specs = append(specs, spec)
		}
	}
	if len(specs) == 0 {
		return "", errors.New("target list is empty")
	}
	result := strings.Join(specs, ", ")
	if len(result) > MaxListBytes {
		return "", errors.New("normalized target list exceeds the 64 KiB limit")
	}
	return result, nil
}

func validateListSpec(spec string) error {
	if strings.Contains(spec, "/") {
		p, err := netip.ParsePrefix(spec)
		if err != nil || !p.Addr().Is4() {
			return errInvalidList
		}
		if uint64(1)<<uint(32-p.Bits()) > uint64(DefaultMaxHosts) {
			return ErrTooMany
		}
		return nil
	}
	if i := strings.IndexByte(spec, '-'); i > 0 {
		start, err := netip.ParseAddr(spec[:i])
		if err == nil && start.Is4() {
			var end netip.Addr
			if n, convErr := strconv.Atoi(spec[i+1:]); convErr == nil {
				if n < 0 || n > 255 {
					return errInvalidList
				}
				b := start.As4()
				b[3] = byte(n)
				end = netip.AddrFrom4(b)
			} else {
				end, err = netip.ParseAddr(spec[i+1:])
				if err != nil || !end.Is4() {
					return errInvalidList
				}
			}
			if end.Less(start) {
				return errInvalidList
			}
			if uint64(toUint(end))-uint64(toUint(start))+1 > uint64(DefaultMaxHosts) {
				return ErrTooMany
			}
			return nil
		}
		// A malformed numeric IPv4 left side must not fall through as a
		// syntactically valid hostname containing a hyphen.
		if isNumericDotted(spec[:i]) {
			return errInvalidList
		}
	}
	if addr, err := netip.ParseAddr(spec); err == nil {
		if addr.Is4() {
			return nil
		}
		return errInvalidList
	}
	if isNumericDotted(spec) || !validHostname(spec) {
		return errInvalidList
	}
	return nil
}

func isNumericDotted(s string) bool {
	if !strings.ContainsRune(s, '.') {
		return false
	}
	for _, r := range s {
		if r != '.' && (r < '0' || r > '9') {
			return false
		}
	}
	return true
}

// IsHostnameSpec reports whether a normalized target specification is a host
// name rather than an address, a CIDR block or an address range. It follows the
// same order as expansion, so a spec it calls a name is resolved by DNS.
func IsHostnameSpec(spec string) bool {
	if strings.Contains(spec, "/") {
		return false
	}
	if i := strings.IndexByte(spec, '-'); i > 0 {
		if start, err := netip.ParseAddr(spec[:i]); err == nil && start.Is4() {
			return false
		}
	}
	_, err := netip.ParseAddr(spec)
	return err != nil
}
