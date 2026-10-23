package ad

import (
	"errors"
	"testing"
)

func TestAttributeRangesCannotLookComplete(t *testing.T) {
	for _, descriptor := range []string{"memberOf;range=0-1499", "tokenGroups;range=1500-*", "memberOf;range=invalid"} {
		d, err := parseAttributeDescription(descriptor)
		if err != nil {
			continue
		}
		if d.ranged && d.start == 0 && d.terminal {
			t.Errorf("%q should not describe a complete initial page", descriptor)
		}
	}
	for _, descriptor := range []string{"memberOf", "MEMBEROF;range=0-*"} {
		d, err := parseAttributeDescription(descriptor)
		if err != nil || d.name != "memberof" {
			t.Errorf("%q: descriptor=%+v err=%v", descriptor, d, err)
		}
	}
}

func TestRangePageValidation(t *testing.T) {
	tests := []struct {
		name              string
		desc              string
		start, count, max int
		wantNext          int
		wantDone          bool
		wantErr           bool
	}{
		{"first page", "memberOf;range=0-2", 0, 3, 4096, 3, false, false},
		{"terminal page", "memberOf;range=3-*", 3, 2, 4096, 0, true, false},
		{"gap", "memberOf;range=4-*", 3, 2, 4096, 0, false, true},
		{"cardinality", "memberOf;range=0-2", 0, 2, 4096, 0, false, true},
		{"empty", "memberOf;range=0-*", 0, 0, 4096, 0, false, true},
		{"over budget", "memberOf;range=4095-*", 4095, 2, 4096, 0, false, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d, err := parseAttributeDescription(tt.desc)
			if err != nil {
				t.Fatal(err)
			}
			next, done, err := validateRangePage(d, tt.start, tt.count, tt.max)
			if (err != nil) != tt.wantErr || next != tt.wantNext || done != tt.wantDone {
				t.Fatalf("validateRangePage() = (%d, %v, %v)", next, done, err)
			}
		})
	}
}

func TestParseAttributeDescriptionRejectsAmbiguousRanges(t *testing.T) {
	for _, descriptor := range []string{
		"memberOf;range=0-1;range=2-*", "memberOf;binary", "memberOf;range=-1-*",
		"memberOf;range=2-1", "memberOf;range=0-1-extra", "memberOf;range=0-*;binary",
	} {
		if _, err := parseAttributeDescription(descriptor); !errors.Is(err, ErrIncomplete) {
			t.Errorf("%q should be rejected, got %v", descriptor, err)
		}
	}
}
