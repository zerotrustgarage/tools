package ad

import (
	"bytes"
	"encoding/hex"
	"testing"
)

func TestPrimaryGroupSIDRetainsDomain(t *testing.T) {
	// S-1-5-21-100-200-300-1100 becomes S-1-5-21-100-200-300-513.
	user, _ := hex.DecodeString("01050000000000051500000064000000c80000002c0100004c040000")
	original := append([]byte(nil), user...)
	group, err := primaryGroupSID(user, 513)
	if err != nil {
		t.Fatal(err)
	}
	if got := sidText(group); got != "S-1-5-21-100-200-300-513" {
		t.Fatalf("primary SID = %q", got)
	}
	if !bytes.Equal(user, original) {
		t.Fatal("primary group derivation changed the user's SID")
	}
	if got := sidFilterValue([]byte{1, 0, 0xff}); got != `\01\00\ff` {
		t.Fatalf("binary filter = %q", got)
	}
}

func TestSIDRejectsMalformedData(t *testing.T) {
	for _, raw := range []string{"", "0100000000000005", "020100000000000500000000", "010200000000000500000000", "010100000000000500000000ff", "0110000000000005"} {
		sid, _ := hex.DecodeString(raw)
		if _, err := primaryGroupSID(sid, 513); err == nil {
			t.Errorf("accepted malformed user SID %q", raw)
		}
	}
}
