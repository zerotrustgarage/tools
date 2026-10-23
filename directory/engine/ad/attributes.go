package ad

import (
	"fmt"
	"strconv"
	"strings"
)

// attributeDescription is the part of an LDAP attribute description relevant
// to AD's ranged retrieval extension.
type attributeDescription struct {
	name        string
	descriptor  string
	placeholder string
	ranged      bool
	start       int
	end         int
	terminal    bool
}

// parseAttributeDescription recognizes ordinary descriptors and Microsoft's
// documented ;range=low-high / ;range=low-* response descriptors.
// https://learn.microsoft.com/openspecs/windows_protocols/ms-adts/e27b48db-6f82-44cd-9038-2e54f790cc1f
func parseAttributeDescription(descriptor string) (attributeDescription, error) {
	parts := strings.Split(lowerASCII(descriptor), ";")
	if len(parts) == 0 || parts[0] == "" {
		return attributeDescription{}, fmt.Errorf("%w: empty LDAP attribute descriptor", ErrIncomplete)
	}
	d := attributeDescription{name: parts[0], descriptor: lowerASCII(descriptor)}
	for _, option := range parts[1:] {
		if !strings.HasPrefix(option, "range=") || d.ranged {
			return attributeDescription{}, fmt.Errorf("%w: malformed or duplicate LDAP attribute option %q", ErrIncomplete, descriptor)
		}
		bounds := strings.Split(strings.TrimPrefix(option, "range="), "-")
		if len(bounds) != 2 || bounds[0] == "" || bounds[1] == "" {
			return attributeDescription{}, fmt.Errorf("%w: malformed AD range descriptor %q", ErrIncomplete, descriptor)
		}
		if !asciiDigits(bounds[0]) || (bounds[1] != "*" && !asciiDigits(bounds[1])) {
			return attributeDescription{}, fmt.Errorf("%w: malformed AD range bounds %q", ErrIncomplete, descriptor)
		}
		start, err := strconv.Atoi(bounds[0])
		if err != nil || start < 0 {
			return attributeDescription{}, fmt.Errorf("%w: malformed AD range start %q", ErrIncomplete, descriptor)
		}
		d.ranged, d.start = true, start
		if bounds[1] == "*" {
			d.terminal = true
			continue
		}
		end, err := strconv.Atoi(bounds[1])
		if err != nil || end < start || end == int(^uint(0)>>1) {
			return attributeDescription{}, fmt.Errorf("%w: malformed AD range end %q", ErrIncomplete, descriptor)
		}
		d.end = end
	}
	return d, nil
}

func asciiDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// validateRangePage verifies the descriptor/value-count relationship for one
// page. The returned start is the next requested index when done is false.
func validateRangePage(d attributeDescription, requestedStart, valueCount, maxValues int) (nextStart int, done bool, err error) {
	if !d.ranged || d.start != requestedStart || valueCount <= 0 || maxValues <= 0 {
		return 0, false, fmt.Errorf("%w: AD returned a missing, empty, or out-of-order range page", ErrIncomplete)
	}
	if requestedStart > maxValues || valueCount > maxValues-requestedStart {
		return 0, false, fmt.Errorf("%w: AD ranged attribute exceeds the %d value limit", ErrIncomplete, maxValues)
	}
	if !d.terminal {
		if d.end-requestedStart+1 != valueCount {
			return 0, false, fmt.Errorf("%w: AD range descriptor cardinality does not match its values", ErrIncomplete)
		}
		return d.end + 1, false, nil
	}
	return 0, true, nil
}
