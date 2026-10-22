package main

import (
	"bytes"
	"net/netip"
	"testing"
)

func TestWakePacketAndDirectlyConnectedScope(t *testing.T) {
	packet, err := magicPacket("00:11:22:33:44:55")
	if err != nil {
		t.Fatal(err)
	}
	if len(packet) != 102 || !bytes.Equal(packet[:6], bytes.Repeat([]byte{255}, 6)) || !bytes.Equal(packet[6:], bytes.Repeat([]byte{0, 17, 34, 51, 68, 85}, 16)) {
		t.Fatal("incorrect wake packet")
	}
	for _, mac := range []string{"00:00:00:00:00:00", "ff:ff:ff:ff:ff:ff", "00:11:22", "01:11:22:33:44:55"} {
		if _, err := magicPacket(mac); err == nil {
			t.Fatal("invalid MAC", mac)
		}
	}
	prefixes := []netip.Prefix{netip.MustParsePrefix("10.0.0.10/16"), netip.MustParsePrefix("10.0.0.20/24")}
	local, dest, err := wakeDestination(netip.MustParseAddr("10.0.0.42"), prefixes)
	if err != nil || local.String() != "10.0.0.20" || dest.String() != "10.0.0.255" {
		t.Fatal(local, dest, err)
	}
	if _, _, err := wakeDestination(netip.MustParseAddr("10.1.0.1"), prefixes); err == nil {
		t.Fatal("off-link wake accepted")
	}
	if _, _, err := wakeDestination(netip.MustParseAddr("10.0.0.1"), []netip.Prefix{netip.MustParsePrefix("10.0.0.0/31")}); err == nil {
		t.Fatal("point-to-point broadcast accepted")
	}
}
