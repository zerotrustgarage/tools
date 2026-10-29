// Package targets parses the address specifications a technician types
// ("10.0.0.0/24", "192.168.1.10-50", "10.0.0.1-10.0.1.20", "printer.lan")
// into a bounded, de-duplicated list of IPv4 addresses, and enforces the
// scope guard: by default only private, carrier-grade NAT, link-local and
// loopback space may be scanned.
package targets

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"sort"
	"strconv"
	"strings"
)

// DefaultMaxHosts bounds a single scan to a /16. Larger sweeps are almost
// always a typo, and an accidental /8 would run for hours.
const DefaultMaxHosts = 65536

// Options controls expansion and the scope guard.
type Options struct {
	// MaxHosts caps the number of addresses after expansion. Zero means
	// DefaultMaxHosts.
	MaxHosts int
	// AllowPublic permits addresses outside the private ranges. The caller
	// is responsible for confirming the operator is authorized to scan them.
	AllowPublic bool
	// KeepNetworkAndBroadcast keeps the first and last address of CIDR
	// blocks shorter than /31. They are dropped by default.
	KeepNetworkAndBroadcast bool
	// Resolver resolves hostnames. Nil uses net.DefaultResolver.
	Resolver *net.Resolver
}

// ErrTooMany is returned when expansion exceeds MaxHosts.
var ErrTooMany = errors.New("target list exceeds the maximum host count")

// ScopeError reports an address the scope guard refused.
type ScopeError struct {
	Spec string
	Addr netip.Addr
}

func (e *ScopeError) Error() string {
	return fmt.Sprintf("%s: %s is public address space; scanning it needs --allow-public and authorization from its owner", e.Spec, e.Addr)
}

var privatePrefixes = []netip.Prefix{
	netip.MustParsePrefix("10.0.0.0/8"),
	netip.MustParsePrefix("172.16.0.0/12"),
	netip.MustParsePrefix("192.168.0.0/16"),
	netip.MustParsePrefix("100.64.0.0/10"),  // carrier-grade NAT (RFC 6598)
	netip.MustParsePrefix("169.254.0.0/16"), // link-local
	netip.MustParsePrefix("127.0.0.0/8"),    // loopback
}

// internalPrefixes6 is the IPv6 space that is internal by its nature: link-local
// (fe80::/10), unique local (fc00::/7, RFC 4193) and loopback. Any other global
// unicast address is internal only inside a prefix the scanner itself is on.
var internalPrefixes6 = []netip.Prefix{
	netip.MustParsePrefix("fe80::/10"),
	netip.MustParsePrefix("fc00::/7"),
	netip.MustParsePrefix("::1/128"),
}

// MinOwnPrefixBits is the shortest on-link global prefix IsInternalOn honors.
// A real on-link prefix is a /64 or longer; a caller that passes something
// much wider (a /0 by mistake) must not turn the whole internet internal.
const MinOwnPrefixBits = 48

// IsInternal reports whether addr is in the default-permitted scope: for IPv4
// the private, CGNAT, link-local and loopback ranges, for IPv6 link-local,
// unique local and loopback space. A zone (fe80::1%eth0) is ignored. A global
// IPv6 address is not internal here; see IsInternalOn.
func IsInternal(addr netip.Addr) bool { return IsInternalOn(addr, nil) }

// IsInternalOn is IsInternal plus the scanner's own on-link global IPv6
// prefixes (own), which count as internal because a host there is on the
// network the technician is attached to. Every other global IPv6 address is
// public and needs the same authorization as public IPv4 space.
func IsInternalOn(addr netip.Addr, own []netip.Prefix) bool {
	addr = addr.Unmap().WithZone("")
	if !addr.IsValid() {
		return false
	}
	if addr.Is4() {
		for _, p := range privatePrefixes {
			if p.Contains(addr) {
				return true
			}
		}
		return false
	}
	for _, p := range internalPrefixes6 {
		if p.Contains(addr) {
			return true
		}
	}
	if !addr.IsGlobalUnicast() {
		return false
	}
	for _, p := range own {
		if p.Addr().Is6() && !p.Addr().Is4In6() && p.Bits() >= MinOwnPrefixBits && p.Masked().Contains(addr) {
			return true
		}
	}
	return false
}

// Expand parses each spec and returns the sorted, de-duplicated addresses.
// Specs may be separated by commas or whitespace inside one string.
func Expand(ctx context.Context, specs []string, opt Options) ([]netip.Addr, error) {
	max := opt.MaxHosts
	if max <= 0 {
		max = DefaultMaxHosts
	}
	seen := make(map[netip.Addr]struct{})
	var out []netip.Addr
	add := func(spec string, a netip.Addr) error {
		if !opt.AllowPublic && !IsInternal(a) {
			return &ScopeError{Spec: spec, Addr: a}
		}
		if _, dup := seen[a]; dup {
			return nil
		}
		if len(out) >= max {
			return fmt.Errorf("%w (%d); narrow the range or raise the limit", ErrTooMany, max)
		}
		seen[a] = struct{}{}
		out = append(out, a)
		return nil
	}

	for _, raw := range specs {
		for _, spec := range splitSpecs(raw) {
			if err := expandOne(ctx, spec, opt, max, add); err != nil {
				return nil, err
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Less(out[j]) })
	return out, nil
}

func splitSpecs(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool {
		return r == ',' || r == ' ' || r == '\t' || r == '\n' || r == '\r' || r == ';'
	})
}

func expandOne(ctx context.Context, spec string, opt Options, max int, add func(string, netip.Addr) error) error {
	// CIDR block.
	if strings.Contains(spec, "/") {
		p, err := netip.ParsePrefix(spec)
		if err != nil {
			return fmt.Errorf("%s: not a valid CIDR block", spec)
		}
		if !p.Addr().Is4() {
			return fmt.Errorf("%s: only IPv4 ranges can be swept (IPv6 neighbors on the local link are found by neighbor discovery, not by sweeping a prefix)", spec)
		}
		p = p.Masked()
		size := uint64(1) << (32 - p.Bits())
		if size > uint64(max)+2 {
			return fmt.Errorf("%w (%d): %s holds %d addresses", ErrTooMany, max, spec, size)
		}
		first, last := p.Addr(), lastInPrefix(p)
		if p.Bits() < 31 && !opt.KeepNetworkAndBroadcast {
			first, last = first.Next(), last.Prev()
		}
		return addRange(spec, first, last, add)
	}

	// Range: a.b.c.d-e or a.b.c.d-w.x.y.z.
	if i := strings.IndexByte(spec, '-'); i > 0 {
		left, right := spec[:i], spec[i+1:]
		start, err := netip.ParseAddr(left)
		if err == nil && start.Is4() {
			var end netip.Addr
			if n, convErr := strconv.Atoi(right); convErr == nil {
				if n < 0 || n > 255 {
					return fmt.Errorf("%s: last octet must be 0 to 255", spec)
				}
				b := start.As4()
				b[3] = byte(n)
				end = netip.AddrFrom4(b)
			} else if end, err = netip.ParseAddr(right); err != nil || !end.Is4() {
				return fmt.Errorf("%s: not a valid IPv4 range", spec)
			}
			if end.Less(start) {
				return fmt.Errorf("%s: range ends before it starts", spec)
			}
			if span := toUint(end) - toUint(start) + 1; span > uint32(max) {
				return fmt.Errorf("%w (%d): %s holds %d addresses", ErrTooMany, max, spec, span)
			}
			return addRange(spec, start, end, add)
		}
		// Not an address on the left: fall through, it may be a hostname
		// that contains a hyphen.
	}

	// Single address.
	if a, err := netip.ParseAddr(spec); err == nil {
		if !a.Unmap().Is4() {
			return fmt.Errorf("%s: only IPv4 targets can be given (IPv6 neighbors on the local link are found by neighbor discovery)", spec)
		}
		return add(spec, a.Unmap())
	}

	// Hostname.
	if !validHostname(spec) {
		return fmt.Errorf("%s: not an address, range, CIDR block or hostname", spec)
	}
	r := opt.Resolver
	if r == nil {
		r = net.DefaultResolver
	}
	addrs, err := r.LookupNetIP(ctx, "ip4", spec)
	if err != nil {
		return fmt.Errorf("%s: %w", spec, err)
	}
	for _, a := range addrs {
		if err := add(spec, a.Unmap()); err != nil {
			return err
		}
	}
	return nil
}

func addRange(spec string, first, last netip.Addr, add func(string, netip.Addr) error) error {
	for a := first; ; a = a.Next() {
		if err := add(spec, a); err != nil {
			return err
		}
		if a == last {
			return nil
		}
	}
}

func lastInPrefix(p netip.Prefix) netip.Addr {
	v := toUint(p.Addr()) | (uint32(0xffffffff) >> p.Bits())
	return fromUint(v)
}

func toUint(a netip.Addr) uint32 {
	b := a.As4()
	return uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3])
}

func fromUint(v uint32) netip.Addr {
	return netip.AddrFrom4([4]byte{byte(v >> 24), byte(v >> 16), byte(v >> 8), byte(v)})
}

func validHostname(s string) bool {
	if len(s) == 0 || len(s) > 253 {
		return false
	}
	for _, label := range strings.Split(strings.TrimSuffix(s, "."), ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, r := range label {
			if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-') {
				return false
			}
		}
	}
	return true
}
