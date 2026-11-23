package auth

import (
	"bytes"
	"testing"
)

func TestProtectRevealRoundTrip(t *testing.T) {
	for _, pw := range []string{"", "a", "exactly-16-bytes", "a much longer password of 33 chars"} {
		blob, n, err := protectPassword([]byte(pw))
		if err != nil || n != len(pw) || len(blob)%protectBlock != 0 || len(blob) <= len(pw) {
			t.Fatalf("protect %q: %d %d %v", pw, n, len(blob), err)
		}
		got, err := revealPassword(blob, n)
		if err != nil || !bytes.Equal(got, []byte(pw)) || cap(got) != len(pw) {
			t.Fatalf("reveal %q: %q %v", pw, got, err)
		}
	}
	if _, err := revealPassword(make([]byte, 15), 1); err == nil {
		t.Fatal("partial block accepted")
	}
}
