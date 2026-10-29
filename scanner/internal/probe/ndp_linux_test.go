//go:build linux

package probe

import (
	"context"
	"net/netip"
	"testing"
	"time"
)

// Reading the kernel's neighbor table is a local netlink request that sends
// nothing. The table may be empty; the call must not fail on a normal host.
func TestLinuxNeighborTableRead(t *testing.T) {
	tbl, err := newNeighborTable6()
	if err != nil {
		t.Fatal(err)
	}
	if tbl.Method() != "ndp-netlink" {
		t.Fatal(tbl.Method())
	}
	rows, err := tbl.Read()
	if err != nil {
		t.Skip("netlink is not available in this environment:", err)
	}
	for _, n := range CleanNeighbors6(rows, nil) {
		if !n.Addr.Is6() || len(n.MAC) != 6 {
			t.Fatalf("bad entry %+v", n)
		}
	}
}

// An echo to the IPv6 loopback goes through the same pinger as IPv4. Where the
// host refuses unprivileged ICMPv6 sockets (as hosted CI runners do) there is
// nothing to test.
func TestPingIPv6Loopback(t *testing.T) {
	p, err := NewPinger()
	if err != nil {
		t.Skip("no ICMP here:", err)
	}
	defer p.Close()
	r, err := p.Ping(context.Background(), netip.MustParseAddr("::1"), time.Second)
	if err != nil {
		t.Skip("ICMPv6 echo is not available here:", err)
	}
	if r.RTT < 0 {
		t.Fatalf("rtt %v", r.RTT)
	}
}
