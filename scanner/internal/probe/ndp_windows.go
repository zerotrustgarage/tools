//go:build windows

package probe

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"time"
	"unsafe"
)

var (
	procGetIpNetTable2 = iphlpapi.NewProc("GetIpNetTable2")
	procFreeMibTable   = iphlpapi.NewProc("FreeMibTable")
)

// winNeighborTable6 calls GetIpNetTable2 with AF_INET6. It needs no
// administrator rights, sends nothing and returns what the stack has learned.
type winNeighborTable6 struct{}

func newNeighborTable6() (NeighborTable6, error) {
	if err := procGetIpNetTable2.Find(); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	return winNeighborTable6{}, nil
}

func (winNeighborTable6) Method() string { return "ndp-win32" }

func (winNeighborTable6) Read() ([]Neighbor6, error) {
	var table unsafe.Pointer
	// NETIO_STATUS GetIpNetTable2(ADDRESS_FAMILY Family, PMIB_IPNET_TABLE2 *Table)
	ret, _, _ := procGetIpNetTable2.Call(uintptr(winAFInet6), uintptr(unsafe.Pointer(&table)))
	if ret != 0 {
		return nil, fmt.Errorf("GetIpNetTable2: error %d", ret)
	}
	if table == nil {
		return nil, errors.New("GetIpNetTable2 returned no table")
	}
	defer procFreeMibTable.Call(uintptr(table))
	n := int(*(*uint32)(table))
	if n < 0 || n > maxNeighborRows {
		return nil, errShortTable
	}
	// Copy out of the system's buffer before FreeMibTable releases it.
	raw := make([]byte, ipNetTable2Header+n*ipNetRow2Size)
	copy(raw, unsafe.Slice((*byte)(table), len(raw)))
	return parseIPNetTable2(raw)
}

var (
	procIcmp6CreateFile = iphlpapi.NewProc("Icmp6CreateFile")
	procIcmp6SendEcho2  = iphlpapi.NewProc("Icmp6SendEcho2")
)

// icmp6Echo sends one ICMPv6 Echo Request through Icmp6SendEcho2 (the helper API
// the Windows ping uses, so no raw socket and no administrator rights) and, in
// this synchronous form, waits up to timeout for the first reply. dst may be a
// unicast address or a link-local multicast group; scope is the interface index
// for a link-local or multicast destination. It returns the reply buffer, the
// number of replies, or an error when the call could not be made.
func icmp6Echo(dst netip.Addr, scope uint32, timeout time.Duration) (reply []byte, replies uint32, err error) {
	if err := procIcmp6CreateFile.Find(); err != nil {
		return nil, 0, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	h, _, cerr := procIcmp6CreateFile.Call()
	if h == 0 || h == ^uintptr(0) {
		return nil, 0, fmt.Errorf("Icmp6CreateFile: %w", cerr)
	}
	defer procIcmpCloseHandle.Call(h)

	src := sockaddrIn6(netip.IPv6Unspecified(), scope) // any source; the stack chooses
	d := sockaddrIn6(dst, scope)
	payload := []byte("ztg-scanner")
	reply = make([]byte, 64+len(payload)+8)
	ms := uint32(timeout / time.Millisecond)
	if ms == 0 {
		ms = 1
	}
	// DWORD Icmp6SendEcho2(IcmpHandle, Event, ApcRoutine, ApcContext, SourceAddress,
	//   DestinationAddress, RequestData, RequestSize, RequestOptions, ReplyBuffer,
	//   ReplySize, Timeout). No event and no APC: the call is synchronous.
	n, _, _ := procIcmp6SendEcho2.Call(
		h, 0, 0, 0,
		uintptr(unsafe.Pointer(&src[0])),
		uintptr(unsafe.Pointer(&d[0])),
		uintptr(unsafe.Pointer(&payload[0])),
		uintptr(len(payload)),
		0,
		uintptr(unsafe.Pointer(&reply[0])),
		uintptr(len(reply)),
		uintptr(ms),
	)
	return reply, uint32(n), nil
}

func multicastPing6(ctx context.Context, ifIndex int, _ string, timeout time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	// The first reply ends the call; any reply or none is fine, because the
	// result is the neighbor table, not the echo.
	_, _, err := icmp6Echo(AllNodes, uint32(ifIndex), timeout)
	return err
}

// ping6 is a unicast echo to an IPv6 neighbor. The reply must name the address
// asked, report success, and be long enough to decode; anything else counts as
// no reply, so a misread buffer can only lose evidence, never invent it.
func ping6(ctx context.Context, addr netip.Addr, timeout time.Duration) (EchoReply, error) {
	if err := ctx.Err(); err != nil {
		return EchoReply{}, err
	}
	scope := uint32(0)
	if z := addr.Zone(); z != "" {
		scope = uint32(ZoneIndex(z))
	}
	reply, n, err := icmp6Echo(addr, scope, timeout)
	if err != nil {
		return EchoReply{}, err
	}
	if n == 0 {
		return EchoReply{}, ErrNoReply
	}
	from, status, rtt, ok := icmp6EchoReply(reply)
	if !ok || status != ipSuccess || from != addr.WithZone("") {
		return EchoReply{}, ErrNoReply
	}
	return EchoReply{RTT: time.Duration(rtt) * time.Millisecond}, nil
}
