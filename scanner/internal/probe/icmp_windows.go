//go:build windows

package probe

import (
	"context"
	"encoding/binary"
	"fmt"
	"net/netip"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	iphlpapi            = windows.NewLazySystemDLL("iphlpapi.dll")
	procIcmpCreateFile  = iphlpapi.NewProc("IcmpCreateFile")
	procIcmpCloseHandle = iphlpapi.NewProc("IcmpCloseHandle")
	procIcmpSendEcho    = iphlpapi.NewProc("IcmpSendEcho")
	procSendARP         = iphlpapi.NewProc("SendARP")
)

// ipOptionInformation mirrors IP_OPTION_INFORMATION.
type ipOptionInformation struct {
	TTL         uint8
	TOS         uint8
	Flags       uint8
	OptionsSize uint8
	OptionsData uintptr
}

// icmpEchoReply mirrors ICMP_ECHO_REPLY (the pointer-sized Data field makes
// the layout differ between 32- and 64-bit; Go's alignment matches C's).
type icmpEchoReply struct {
	Address       uint32
	Status        uint32
	RoundTripTime uint32
	DataSize      uint16
	Reserved      uint16
	Data          uintptr
	Options       ipOptionInformation
}

const ipSuccess = 0

// winPinger uses the ICMP helper API, which needs no administrator rights
// and no packet capture driver.
type winPinger struct{}

func newPinger() (Pinger, error) {
	if err := procIcmpSendEcho.Find(); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	return winPinger{}, nil
}

func (winPinger) Method() string { return "icmp-win32" }
func (winPinger) Close() error   { return nil }

// inAddr converts an IPv4 address to the IPAddr (in_addr as ULONG) the
// helper API expects: the address bytes in network order in memory.
func inAddr(a netip.Addr) uint32 {
	b := a.As4()
	return binary.LittleEndian.Uint32(b[:])
}

func (winPinger) Ping(ctx context.Context, addr netip.Addr, timeout time.Duration) (EchoReply, error) {
	if err := ctx.Err(); err != nil {
		return EchoReply{}, err
	}
	if addr.Is6() && !addr.Is4In6() {
		return ping6(ctx, addr, timeout)
	}
	h, _, err := procIcmpCreateFile.Call()
	if windows.Handle(h) == windows.InvalidHandle {
		return EchoReply{}, fmt.Errorf("IcmpCreateFile: %w", err)
	}
	defer procIcmpCloseHandle.Call(h)

	payload := []byte("ztg-scanner")
	reply := make([]byte, int(unsafe.Sizeof(icmpEchoReply{}))+len(payload)+64)
	ms := uint32(timeout / time.Millisecond)
	if ms == 0 {
		ms = 1
	}
	n, _, _ := procIcmpSendEcho.Call(
		h,
		uintptr(inAddr(addr)),
		uintptr(unsafe.Pointer(&payload[0])),
		uintptr(len(payload)),
		0,
		uintptr(unsafe.Pointer(&reply[0])),
		uintptr(len(reply)),
		uintptr(ms),
	)
	if n == 0 {
		return EchoReply{}, ErrNoReply
	}
	r := (*icmpEchoReply)(unsafe.Pointer(&reply[0]))
	if r.Status != ipSuccess {
		return EchoReply{}, ErrNoReply
	}
	return EchoReply{RTT: time.Duration(r.RoundTripTime) * time.Millisecond, TTL: int(r.Options.TTL)}, nil
}
