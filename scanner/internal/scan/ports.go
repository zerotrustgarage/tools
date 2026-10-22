package scan

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// ParsePorts parses "22,80,8000-8100" into a de-duplicated list (max 1024).
func ParsePorts(s string) ([]int, error) {
	seen := map[int]bool{}
	var out []int
	for _, part := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ' ' || r == ';' }) {
		lo, hi, isRange := strings.Cut(part, "-")
		a, err := strconv.Atoi(strings.TrimSpace(lo))
		if err != nil {
			return nil, fmt.Errorf("bad port %q", part)
		}
		b := a
		if isRange {
			if b, err = strconv.Atoi(strings.TrimSpace(hi)); err != nil {
				return nil, fmt.Errorf("bad port range %q", part)
			}
		}
		if a < 1 || b > 65535 || b < a {
			return nil, fmt.Errorf("bad port range %q", part)
		}
		for p := a; p <= b; p++ {
			if !seen[p] {
				seen[p] = true
				out = append(out, p)
			}
		}
	}
	if len(out) == 0 {
		return nil, errors.New("no ports given")
	}
	if len(out) > 1024 {
		return nil, fmt.Errorf("%d ports is too many for an inventory scan (max 1024)", len(out))
	}
	return out, nil
}
