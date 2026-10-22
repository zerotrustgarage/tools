//go:build linux

package probe

import (
	"bufio"
	"context"
	"io"
	"net"
	"net/netip"
	"os"
	"strings"
	"time"
)

// linuxNeighbors makes the kernel resolve the address (by sending it one
// UDP datagram to the discard port) and then reads the kernel's neighbor
// table. No raw socket or capability is needed. Off-link addresses never
// appear in the table under their own IP, so they resolve to ok=false.
type linuxNeighbors struct {
	path string
}

func newNeighbors() Neighbors { return linuxNeighbors{path: "/proc/net/arp"} }

func (linuxNeighbors) Method() string { return "arp-kernel" }

func (n linuxNeighbors) Resolve(ctx context.Context, addr netip.Addr, timeout time.Duration) (net.HardwareAddr, bool) {
	if mac, ok := n.lookup(addr); ok {
		return mac, true
	}
	if c, err := net.DialUDP("udp4", nil, &net.UDPAddr{IP: addr.AsSlice(), Port: 9}); err == nil {
		_, _ = c.Write([]byte{0})
		c.Close()
	}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return nil, false
		case <-time.After(100 * time.Millisecond):
		}
		if mac, ok := n.lookup(addr); ok {
			return mac, true
		}
	}
	return nil, false
}

func (n linuxNeighbors) lookup(addr netip.Addr) (net.HardwareAddr, bool) {
	f, err := os.Open(n.path)
	if err != nil {
		return nil, false
	}
	defer f.Close()
	return parseProcARP(f, addr)
}

// parseProcARP finds addr in /proc/net/arp. Only complete entries
// (flag ATF_COM, 0x2) count; incomplete ones mean the host did not answer.
func parseProcARP(r io.Reader, addr netip.Addr) (net.HardwareAddr, bool) {
	sc := bufio.NewScanner(r)
	want := addr.String()
	for first := true; sc.Scan(); first = false {
		if first {
			continue // header
		}
		f := strings.Fields(sc.Text())
		if len(f) < 4 || f[0] != want {
			continue
		}
		if f[2] == "0x0" {
			continue
		}
		mac, err := net.ParseMAC(f[3])
		if err != nil || isZeroMAC(mac) {
			continue
		}
		return mac, true
	}
	return nil, false
}
