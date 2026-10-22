package probe

import (
	"context"
	"net"
	"net/netip"
	"time"
)

// Neighbors resolves IPv4 addresses on the local link to hardware
// addresses. A successful resolution also proves the host is up, even
// when its firewall drops ICMP and every probed TCP port: hosts answer
// ARP regardless of their firewall.
type Neighbors interface {
	// Resolve returns the MAC address for addr, or ok=false when the
	// address is off-link or did not answer within timeout.
	Resolve(ctx context.Context, addr netip.Addr, timeout time.Duration) (mac net.HardwareAddr, ok bool)
	Method() string
}

// NewNeighbors returns the platform's ARP mechanism.
func NewNeighbors() Neighbors { return newNeighbors() }

func isZeroMAC(m net.HardwareAddr) bool {
	for _, b := range m {
		if b != 0 {
			return false
		}
	}
	return true
}
