//go:build windows

package probe

import (
	"context"
	"net"
	"net/netip"
	"time"
	"unsafe"
)

// winNeighbors uses SendARP, which sends a real ARP request for on-link
// addresses without administrator rights or a capture driver. Its timeout
// is fixed by Windows (about three seconds for a silent address); timeout
// is honored only between calls.
type winNeighbors struct{}

func newNeighbors() Neighbors { return winNeighbors{} }

func (winNeighbors) Method() string { return "arp-win32" }

func (winNeighbors) Resolve(ctx context.Context, addr netip.Addr, _ time.Duration) (net.HardwareAddr, bool) {
	if ctx.Err() != nil {
		return nil, false
	}
	var buf [2]uint32 // ULONG[2], room for a 6-byte address
	size := uint32(len(buf) * 4)
	ret, _, _ := procSendARP.Call(
		uintptr(inAddr(addr)),
		0,
		uintptr(unsafe.Pointer(&buf[0])),
		uintptr(unsafe.Pointer(&size)),
	)
	if ret != 0 || size != 6 {
		return nil, false
	}
	b := (*[8]byte)(unsafe.Pointer(&buf[0]))
	mac := make(net.HardwareAddr, 6)
	copy(mac, b[:6])
	for _, x := range mac {
		if x != 0 {
			return mac, true
		}
	}
	return nil, false
}
