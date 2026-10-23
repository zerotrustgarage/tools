package ad

import (
	"errors"
	"testing"
)

func TestAttributeRangesCannotLookComplete(t *testing.T) {
	for _, descriptor := range []string{"memberOf;range=0-1499", "tokenGroups;range=1500-*", "memberOf;range=invalid"} {
		if _, err := completeAttribute(descriptor); !errors.Is(err, ErrIncomplete) {
			t.Errorf("%q should report incomplete, got %v", descriptor, err)
		}
	}
	for _, descriptor := range []string{"memberOf", "MEMBEROF;range=0-*"} {
		if name, err := completeAttribute(descriptor); err != nil || name != "memberof" {
			t.Errorf("%q: name=%q err=%v", descriptor, name, err)
		}
	}
}
