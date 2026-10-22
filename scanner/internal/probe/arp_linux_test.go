//go:build linux

package probe

import (
	"net/netip"
	"strings"
	"testing"
)

func TestParseProcARP(t *testing.T) {
	table := `IP address       HW type     Flags       HW address            Mask     Device
192.168.1.1      0x1         0x2         a0:63:91:00:00:01     *        eth0
192.168.1.50     0x1         0x0         00:00:00:00:00:00     *        eth0
192.168.1.77     0x1         0x2         00:1b:21:12:34:56     *        eth0
`
	mac, ok := parseProcARP(strings.NewReader(table), netip.MustParseAddr("192.168.1.77"))
	if !ok || mac.String() != "00:1b:21:12:34:56" {
		t.Fatalf("got %v %v", mac, ok)
	}
	if _, ok := parseProcARP(strings.NewReader(table), netip.MustParseAddr("192.168.1.50")); ok {
		t.Fatal("incomplete entry accepted")
	}
	if _, ok := parseProcARP(strings.NewReader(table), netip.MustParseAddr("192.168.1.9")); ok {
		t.Fatal("missing entry accepted")
	}
}
