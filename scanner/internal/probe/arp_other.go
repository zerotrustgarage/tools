//go:build !linux && !windows

package probe

import (
	"context"
	"net"
	"net/netip"
	"time"
)

// noNeighbors is used where ARP resolution is not implemented yet (macOS).
// Liveness then comes from ICMP and TCP only.
type noNeighbors struct{}

func newNeighbors() Neighbors { return noNeighbors{} }

func (noNeighbors) Method() string { return "none" }

func (noNeighbors) Resolve(context.Context, netip.Addr, time.Duration) (net.HardwareAddr, bool) {
	return nil, false
}
