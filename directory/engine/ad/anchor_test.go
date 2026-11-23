package ad

import (
	"crypto/rand"
	"encoding/base64"
	"testing"
)

func TestImmutableID(t *testing.T) {
	// The pair guidText is tested with: the bytes as AD stores objectGUID and
	// their GUID text. The immutable ID is the Base64 of those same bytes.
	stored := []byte{0xff, 0x19, 0x96, 0x6f, 0x86, 0x8b, 0x11, 0xd0, 0xb4, 0x2d, 0x00, 0xc0, 0x4f, 0xc9, 0x64, 0xff}
	want := base64.StdEncoding.EncodeToString(stored)
	if want != "/xmWb4aLEdC0LQDAT8lk/w==" {
		t.Fatalf("fixture Base64 = %q", want)
	}
	for _, in := range []string{"6f9619ff-8b86-d011-b42d-00c04fc964ff", "{6F9619FF-8B86-D011-B42D-00C04FC964FF}", " 6f9619ff-8b86-d011-b42d-00c04fc964ff "} {
		got, err := ImmutableID(in)
		if err != nil || got != want {
			t.Fatalf("ImmutableID(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	// Round trip through guidText for arbitrary bytes.
	for i := 0; i < 50; i++ {
		b := make([]byte, 16)
		rand.Read(b)
		got, err := ImmutableID(guidText(b))
		if err != nil || got != base64.StdEncoding.EncodeToString(b) {
			t.Fatalf("round trip of %x: %q %v", b, got, err)
		}
	}
	for _, bad := range []string{"", "invalid", "6f9619ff-8b86-d011-b42d-00c04fc964f", "6f9619ff8b86d011b42d00c04fc964ff", "zf9619ff-8b86-d011-b42d-00c04fc964ff", "6f9619ff-8b86-d011-b42d00-c04fc964ff"} {
		if _, err := ImmutableID(bad); err == nil {
			t.Errorf("ImmutableID(%q) accepted", bad)
		}
	}
}
