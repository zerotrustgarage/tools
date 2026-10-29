package probe

import (
	"encoding/binary"
	"errors"
	"net"
	"net/netip"
)

// The Windows structures are decoded from bytes by hand, in files with no build
// tag, so the layout is tested on any platform with constructed buffers. All
// integers in these structures are little-endian on every Windows target, and
// the layout below is identical for 32-bit and 64-bit builds (the 64-bit
// NET_LUID inside the row has 8-byte alignment on both).

const (
	// MIB_IPNET_TABLE2: ULONG NumEntries, then (aligned to 8) MIB_IPNET_ROW2[].
	ipNetTable2Header = 8
	// MIB_IPNET_ROW2 is 88 bytes:
	//   0  SOCKADDR_INET Address (28 bytes; family at 0, SOCKADDR_IN6 address at 8)
	//   28 NET_IFINDEX InterfaceIndex
	//   32 NET_LUID InterfaceLuid
	//   40 UCHAR PhysicalAddress[32]
	//   72 ULONG PhysicalAddressLength
	//   76 NL_NEIGHBOR_STATE State
	//   80 flags (IsRouter, IsUnreachable)
	//   84 ULONG ReachabilityTime
	ipNetRow2Size = 88

	winAFInet6 = 23

	// NL_NEIGHBOR_STATE
	nlnsUnreachable = 0
	nlnsIncomplete  = 1
	nlnsProbe       = 2
	nlnsDelay       = 3
	nlnsStale       = 4
	nlnsReachable   = 5
	nlnsPermanent   = 6

	// A table is capped so a corrupt count cannot make a loop run away.
	maxNeighborRows = 1 << 16
)

var errShortTable = errors.New("neighbor table buffer is shorter than its entry count")

// parseIPNetTable2 decodes a MIB_IPNET_TABLE2 as GetIpNetTable2 returns it.
// Rows that are not IPv6, that are unreachable or incomplete, or that carry no
// hardware address are skipped; CleanNeighbors6 does the scope filtering.
func parseIPNetTable2(b []byte) ([]Neighbor6, error) {
	if len(b) < ipNetTable2Header {
		return nil, errShortTable
	}
	n := int(binary.LittleEndian.Uint32(b[0:4]))
	if n < 0 || n > maxNeighborRows || len(b) < ipNetTable2Header+n*ipNetRow2Size {
		return nil, errShortTable
	}
	var out []Neighbor6
	for i := 0; i < n; i++ {
		row := b[ipNetTable2Header+i*ipNetRow2Size : ipNetTable2Header+(i+1)*ipNetRow2Size]
		if binary.LittleEndian.Uint16(row[0:2]) != winAFInet6 {
			continue
		}
		state := ""
		switch binary.LittleEndian.Uint32(row[76:80]) {
		case nlnsReachable:
			state = "reachable"
		case nlnsStale:
			state = "stale"
		case nlnsDelay:
			state = "delay"
		case nlnsProbe:
			state = "probe"
		case nlnsPermanent:
			state = "permanent"
		case nlnsUnreachable, nlnsIncomplete:
			continue
		default:
			continue
		}
		macLen := int(binary.LittleEndian.Uint32(row[72:76]))
		if macLen != 6 {
			continue
		}
		addr, ok := netip.AddrFromSlice(row[8:24])
		if !ok {
			continue
		}
		mac := make(net.HardwareAddr, 6)
		copy(mac, row[40:46])
		out = append(out, Neighbor6{Addr: addr, MAC: mac, IfIndex: int(binary.LittleEndian.Uint32(row[28:32])), State: state})
	}
	return out, nil
}

// sockaddrIn6 encodes a SOCKADDR_IN6 (28 bytes) for the ICMPv6 helper API: the
// family, a zero port and flow information, the address, and the scope id.
func sockaddrIn6(a netip.Addr, scope uint32) [28]byte {
	var sa [28]byte
	binary.LittleEndian.PutUint16(sa[0:2], winAFInet6)
	b := a.WithZone("").As16()
	copy(sa[8:24], b[:])
	binary.LittleEndian.PutUint32(sa[24:28], scope)
	return sa
}

// icmp6EchoReply decodes the part of ICMPV6_ECHO_REPLY this scanner uses. The
// structure starts with an IPV6_ADDRESS_EX that is packed to 26 bytes (port 2,
// flow information 4, address 16, scope id 4), followed by ULONG Status at the
// next 4-byte boundary (28) and the round trip time in milliseconds (32).
// ok is false when the buffer is too short.
func icmp6EchoReply(b []byte) (from netip.Addr, status uint32, rttMillis uint32, ok bool) {
	if len(b) < 36 {
		return netip.Addr{}, 0, 0, false
	}
	from, _ = netip.AddrFromSlice(b[6:22])
	return from, binary.LittleEndian.Uint32(b[28:32]), binary.LittleEndian.Uint32(b[32:36]), true
}
