package ad

import (
	"fmt"
	"strings"
)

// completeAttribute recognizes the terminal range form that covers all values.
// Partial or nonzero-start ranges must never be mistaken for a complete list.
// https://learn.microsoft.com/openspecs/windows_protocols/ms-adts/e27b48db-6f82-44cd-9038-2e54f790cc1f
func completeAttribute(descriptor string) (string, error) {
	parts := strings.Split(lowerASCII(descriptor), ";")
	for _, option := range parts[1:] {
		if strings.HasPrefix(option, "range=") && option != "range=0-*" {
			return "", fmt.Errorf("%w: AD returned a partial attribute range %q; full range retrieval is not yet supported", ErrIncomplete, descriptor)
		}
	}
	return parts[0], nil
}
