// Package probe holds the individual network probes the scan engine
// combines: ICMP echo, ARP resolution, TCP connect, service banners,
// NetBIOS, mDNS and SSDP. Each probe is passive or a single standard
// protocol exchange; none authenticates, writes, or exploits anything.
package probe

import (
	"context"
	"errors"
	"net/netip"
	"time"
)

// ErrUnavailable means the platform does not let this process send the
// probe (for example ICMP without a ping socket or raw socket permission).
var ErrUnavailable = errors.New("probe unavailable on this host")

// EchoReply is a successful ICMP echo.
type EchoReply struct {
	RTT time.Duration
	// TTL is the IP time-to-live of the reply, zero when the platform does
	// not report it. It hints at the operating system family.
	TTL int
}

// Pinger sends ICMP echo requests. Implementations are safe for
// concurrent use.
type Pinger interface {
	// Ping returns the reply, or an error when none arrived in timeout.
	Ping(ctx context.Context, addr netip.Addr, timeout time.Duration) (EchoReply, error)
	// Method names the mechanism, for diagnostics ("icmp-dgram", "icmp-raw", "icmp-win32").
	Method() string
	Close() error
}

// ErrNoReply is returned when an echo request timed out.
var ErrNoReply = errors.New("no echo reply")

// NewPinger returns the best ICMP mechanism this host allows, or
// ErrUnavailable.
func NewPinger() (Pinger, error) { return newPinger() }

// OSHintFromTTL maps an observed reply TTL to the conventional initial TTL
// family. It is an inference: TTLs are configurable and routers decrement
// them.
func OSHintFromTTL(ttl int) string {
	switch {
	case ttl <= 0:
		return ""
	case ttl <= 64:
		return "Linux, macOS, BSD or embedded (initial TTL 64)"
	case ttl <= 128:
		return "Windows (initial TTL 128)"
	default:
		return "Network equipment or Solaris (initial TTL 255)"
	}
}
