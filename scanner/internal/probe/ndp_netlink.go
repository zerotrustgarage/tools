package probe

import (
	"encoding/binary"
	"errors"
	"net"
	"net/netip"
)

// Linux reports its neighbor table over rtnetlink (RTM_GETNEIGH), because there
// is no IPv6 file under /proc/net the way /proc/net/arp serves IPv4. The decoder
// is plain byte parsing with no build tag, so it is tested on any platform; only
// the socket that fetches the bytes is Linux-specific (ndp_linux.go). Netlink
// integers are in the host's byte order.

const (
	nlmsgHdrLen   = 16 // nlmsghdr: len u32, type u16, flags u16, seq u32, pid u32
	nlmsgError    = 2
	nlmsgDone     = 3
	rtmNewNeigh   = 28
	ndmsgLen      = 12 // ndmsg: family u8, pad u8, pad u16, ifindex i32, state u16, flags u8, type u8
	ndaDst        = 1
	ndaLLAddr     = 2
	nudIncomplete = 0x01
	nudReachable  = 0x02
	nudStale      = 0x04
	nudDelay      = 0x08
	nudProbe      = 0x10
	nudFailed     = 0x20
	nudNoARP      = 0x40
	nudPermanent  = 0x80
	afInet6       = 10 // AF_INET6 on Linux
)

// parseNeighMessages decodes a dump of RTM_NEWNEIGH messages for AF_INET6 into
// neighbor entries. Entries that failed or have no link-layer address are
// skipped. A kernel error message fails the whole parse.
func parseNeighMessages(b []byte) ([]Neighbor6, error) {
	order := binary.NativeEndian
	var out []Neighbor6
	for len(b) >= nlmsgHdrLen {
		total := int(order.Uint32(b[0:4]))
		typ := order.Uint16(b[4:6])
		if total < nlmsgHdrLen || total > len(b) {
			return out, errors.New("truncated netlink message")
		}
		body := b[nlmsgHdrLen:total]
		// Messages are padded to 4 bytes.
		next := (total + 3) &^ 3
		switch typ {
		case nlmsgDone:
			return out, nil
		case nlmsgError:
			return out, errors.New("the kernel refused the neighbor table request")
		case rtmNewNeigh:
			if n, ok := parseNeigh(body, order); ok {
				out = append(out, n)
			}
		}
		if next >= len(b) {
			break
		}
		b = b[next:]
	}
	return out, nil
}

func parseNeigh(body []byte, order binary.ByteOrder) (Neighbor6, bool) {
	if len(body) < ndmsgLen || body[0] != afInet6 {
		return Neighbor6{}, false
	}
	ifindex := int(int32(order.Uint32(body[4:8])))
	state := order.Uint16(body[8:10])
	var name string
	switch {
	case state&nudReachable != 0:
		name = "reachable"
	case state&nudStale != 0:
		name = "stale"
	case state&nudDelay != 0:
		name = "delay"
	case state&nudProbe != 0:
		name = "probe"
	case state&nudPermanent != 0:
		name = "permanent"
	default: // incomplete, failed, noarp: no usable evidence
		return Neighbor6{}, false
	}
	var dst netip.Addr
	var mac net.HardwareAddr
	attrs := body[ndmsgLen:]
	for len(attrs) >= 4 {
		l := int(order.Uint16(attrs[0:2]))
		t := order.Uint16(attrs[2:4])
		if l < 4 || l > len(attrs) {
			break
		}
		val := attrs[4:l]
		switch t {
		case ndaDst:
			if a, ok := netip.AddrFromSlice(val); ok && len(val) == 16 {
				dst = a
			}
		case ndaLLAddr:
			if len(val) == 6 {
				mac = append(net.HardwareAddr(nil), val...)
			}
		}
		next := (l + 3) &^ 3
		if next >= len(attrs) {
			break
		}
		attrs = attrs[next:]
	}
	if !dst.IsValid() || mac == nil {
		return Neighbor6{}, false
	}
	return Neighbor6{Addr: dst, MAC: mac, IfIndex: ifindex, State: name}, true
}
