package probe

import (
	"context"
	"net"
	"net/netip"
	"sort"
	"strconv"
	"time"
)

// Neighbor6 is one entry of the operating system's IPv6 neighbor table: an
// address on the local link and the hardware address it resolved to. Reading
// the table sends nothing. The entries are what the host has itself learned
// from Neighbor Discovery (RFC 4861), so they are on-link by construction.
type Neighbor6 struct {
	// Addr is the neighbor's address. A link-local address carries the zone of
	// its interface (fe80::1%eth0), because it means nothing without one.
	Addr    netip.Addr
	MAC     net.HardwareAddr
	IfIndex int
	// State is the table's own word for the entry: "reachable", "stale",
	// "delay", "probe" or "permanent". Unreachable and incomplete entries, and
	// entries with no usable hardware address, are dropped by the reader.
	State string
}

// NeighborTable6 reads the IPv6 neighbor table and, optionally, makes the
// local link populate it with one multicast echo. Implementations are the
// platform's own (see ndp_windows.go, ndp_linux.go); tests substitute fakes.
type NeighborTable6 interface {
	// Read returns the current entries, unfiltered by scope.
	Read() ([]Neighbor6, error)
	// Method names the mechanism for the scan summary.
	Method() string
}

// NewNeighborTable6 returns the platform's reader, or ErrUnavailable.
func NewNeighborTable6() (NeighborTable6, error) { return newNeighborTable6() }

// MulticastPing6 sends exactly one ICMPv6 Echo Request to ff02::1 (all nodes
// on the link) out of the interface with index ifIndex, so that hosts which
// answer teach this computer's neighbor table about themselves. It does not
// wait for or read the replies (the table is the result); timeout bounds the
// call. It needs no raw socket on Windows (Icmp6SendEcho2) and an unprivileged
// ping socket on Linux.
func MulticastPing6(ctx context.Context, ifIndex int, ifName string, timeout time.Duration) error {
	return multicastPing6(ctx, ifIndex, ifName, timeout)
}

// AllNodes is the link-local all-nodes multicast group, ff02::1.
var AllNodes = netip.MustParseAddr("ff02::1")

// ZoneName is how an interface appears after the % in an address: its name when
// that is short and plain (eth0, en0, Ethernet), else its decimal index. Both
// are accepted by Go's resolver on every platform, and the index also works for
// the friendly names Windows gives adapters ("Ethernet 2", "Wi-Fi") that would
// otherwise carry spaces into exports. The result never needs escaping.
func ZoneName(ifIndex int, name string) string {
	if safeZone(name) {
		return name
	}
	return strconv.Itoa(ifIndex)
}

// safeZone allows ASCII letters, digits, dot, underscore and hyphen, at most 15
// characters (the Linux interface-name limit).
func safeZone(s string) bool {
	if s == "" || len(s) > 15 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '.' || c == '_' || c == '-') {
			return false
		}
	}
	return true
}

// ValidZone reports whether z is a zone Scanner would write: what ZoneName can
// return. Saved files are re-checked with it.
func ValidZone(z string) bool {
	if safeZone(z) {
		return true
	}
	n, err := strconv.Atoi(z)
	return err == nil && n > 0 && n < 1<<31 && strconv.Itoa(n) == z
}

// CleanNeighbors6 turns raw table rows into the entries a scan may use. It
// keeps only unicast addresses (no multicast, unspecified, loopback or IPv4
// mapped addresses) with a usable six-byte unicast hardware address, drops
// repeats, sets the zone of link-local addresses from zoneOf, and sorts the
// result by address. zoneOf maps an interface index to a zone name; a nil
// function falls back to the decimal index.
func CleanNeighbors6(in []Neighbor6, zoneOf func(ifIndex int) string) []Neighbor6 {
	seen := map[netip.Addr]bool{}
	out := make([]Neighbor6, 0, len(in))
	for _, n := range in {
		a := n.Addr.WithZone("")
		if !a.IsValid() || !a.Is6() || a.Is4In6() || a.IsUnspecified() || a.IsMulticast() || a.IsLoopback() {
			continue
		}
		if len(n.MAC) != 6 || isZeroMAC(n.MAC) || n.MAC[0]&1 != 0 {
			continue
		}
		if a.IsLinkLocalUnicast() {
			zone := strconv.Itoa(n.IfIndex)
			if zoneOf != nil {
				if z := zoneOf(n.IfIndex); ValidZone(z) {
					zone = z
				}
			}
			if n.IfIndex <= 0 {
				continue // a link-local address with no interface cannot be reached
			}
			a = a.WithZone(zone)
		}
		if seen[a] {
			continue
		}
		seen[a] = true
		mac := make(net.HardwareAddr, 6)
		copy(mac, n.MAC)
		out = append(out, Neighbor6{Addr: a, MAC: mac, IfIndex: n.IfIndex, State: n.State})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Addr.Less(out[j].Addr) })
	return out
}

// ZoneIndex resolves a zone written after % to an interface index: an interface
// name first, then a decimal index. It returns 0 when the zone names nothing.
func ZoneIndex(zone string) int {
	if zone == "" {
		return 0
	}
	if ifc, err := net.InterfaceByName(zone); err == nil {
		return ifc.Index
	}
	if n, err := strconv.Atoi(zone); err == nil && n > 0 {
		return n
	}
	return 0
}
