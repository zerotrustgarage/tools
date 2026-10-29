//go:build linux

package probe

import (
	"context"
	"fmt"
	"net"
	"syscall"
	"time"

	"golang.org/x/net/icmp"
	"golang.org/x/net/ipv6"
)

// linuxNeighborTable6 asks the kernel for its IPv6 neighbor table over
// rtnetlink. A dump request needs no privilege.
type linuxNeighborTable6 struct{}

func newNeighborTable6() (NeighborTable6, error) { return linuxNeighborTable6{}, nil }

func (linuxNeighborTable6) Method() string { return "ndp-netlink" }

func (linuxNeighborTable6) Read() ([]Neighbor6, error) {
	raw, err := syscall.NetlinkRIB(rtmGetNeigh, syscall.AF_INET6)
	if err != nil {
		return nil, fmt.Errorf("read neighbor table: %w", err)
	}
	return parseNeighMessages(raw)
}

const rtmGetNeigh = 30 // RTM_GETNEIGH

// multicastPing6 sends one echo request to ff02::1 on the interface through an
// unprivileged ICMPv6 datagram socket (allowed by net.ipv4.ping_group_range, the
// same setting as for IPv4). Without it the call fails and the caller carries on
// with the table as it is.
func multicastPing6(ctx context.Context, ifIndex int, ifName string, timeout time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	conn, err := icmp.ListenPacket("udp6", "::")
	if err != nil {
		return fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	defer conn.Close()
	zone := ifName
	if zone == "" {
		if ifc, err := net.InterfaceByIndex(ifIndex); err == nil {
			zone = ifc.Name
		}
	}
	msg := icmp.Message{Type: ipv6.ICMPTypeEchoRequest, Body: &icmp.Echo{ID: 0, Seq: 1, Data: []byte("ztg-scanner")}}
	b, err := msg.Marshal(nil) // the kernel fills the ICMPv6 checksum on a datagram socket
	if err != nil {
		return err
	}
	_ = conn.SetWriteDeadline(time.Now().Add(timeout))
	_, err = conn.WriteTo(b, &net.UDPAddr{IP: AllNodes.AsSlice(), Zone: zone})
	return err
}
