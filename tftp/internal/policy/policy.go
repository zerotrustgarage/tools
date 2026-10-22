package policy

import (
	"errors"
	"net"
	"net/netip"
	"sync"
	"time"
)

func Private(a netip.Addr) bool {
	a = a.Unmap()
	return a.IsValid() && !a.IsUnspecified() && !a.IsMulticast() && (a.IsPrivate() || a.IsLoopback() || a.IsLinkLocalUnicast() || netip.MustParsePrefix("100.64.0.0/10").Contains(a))
}

// Bindings expands a wildcard into individual addresses, so responses always
// leave from the destination address, including in single-port mode.
func Bindings(bind string, public bool) ([]netip.Addr, []netip.Prefix, error) {
	requested, e := netip.ParseAddr(bind)
	if e != nil {
		return nil, nil, errors.New("bind must be an IP address")
	}
	interfaces, e := net.Interfaces()
	if e != nil {
		return nil, nil, e
	}
	var addrs []netip.Addr
	var subnets []netip.Prefix
	for _, iface := range interfaces {
		if iface.Flags&net.FlagUp == 0 {
			continue
		}
		list, e := iface.Addrs()
		if e != nil {
			return nil, nil, e
		}
		for _, v := range list {
			p, e := netip.ParsePrefix(v.String())
			if e != nil {
				continue
			}
			a := p.Addr().Unmap()
			if a.IsUnspecified() || a.IsMulticast() {
				continue
			}
			if requested.IsUnspecified() {
				if requested.Is4() != a.Is4() {
					continue
				}
			} else if requested.WithZone("") != a {
				continue
			}
			if !public && !Private(a) {
				continue
			}
			if a.Is6() && a.IsLinkLocalUnicast() {
				a = a.WithZone(iface.Name)
				if requested.Zone() != "" && requested.Zone() != iface.Name {
					continue
				}
			}
			addrs = append(addrs, a)
			subnets = append(subnets, p.Masked())
		}
	}
	if len(addrs) == 0 {
		return nil, nil, errors.New("no eligible local addresses (public binds require --allow-public)")
	}
	return addrs, subnets, nil
}

func Allowed(a netip.Addr, prefixes []netip.Prefix) bool {
	a = a.Unmap().WithZone("")
	for _, p := range prefixes {
		if p.Contains(a) {
			return true
		}
	}
	return false
}

type source struct {
	second                     int64
	requests, refusals, active int
	suppressed                 int
}
type Guard struct {
	mu       sync.Mutex
	sources  map[netip.Addr]*source
	active   int
	second   int64
	bytes    int
	overflow int
}

func New() *Guard { return &Guard{sources: map[netip.Addr]*source{}} }
func (g *Guard) entry(a netip.Addr, now time.Time) *source {
	s := g.sources[a]
	if s == nil {
		// Bound tracking even when an attacker rotates source addresses.
		if len(g.sources) >= 4096 {
			for k, v := range g.sources {
				if v.active == 0 && v.second < now.Unix()-1 {
					g.overflow += v.suppressed
					delete(g.sources, k)
				}
			}
		}
		if len(g.sources) >= 4096 {
			return nil
		}
		s = &source{}
		g.sources[a] = s
	}
	if s.second != now.Unix() {
		s.second = now.Unix()
		s.requests = 0
		s.refusals = 0
	}
	return s
}
func (g *Guard) Acquire(a netip.Addr, now time.Time) string {
	g.mu.Lock()
	defer g.mu.Unlock()
	s := g.entry(a, now)
	if s == nil {
		return "rate-limited"
	}
	s.requests++
	if s.requests > 20 {
		return "rate-limited"
	}
	if g.active >= 64 || s.active >= 8 {
		return "server-busy"
	}
	s.active++
	g.active++
	return ""
}
func (g *Guard) Release(a netip.Addr) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if s := g.sources[a]; s != nil && s.active > 0 {
		s.active--
		g.active--
	}
}
func (g *Guard) Budget(n int, now time.Time) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.second != now.Unix() {
		g.second = now.Unix()
		g.bytes = 0
	}
	if n > 256*1024-g.bytes {
		return false
	}
	g.bytes += n
	return true
}

// Audit permits ten refusal lines per source per second. Suppressed counts are
// retained until the next permitted record or session shutdown.
func (g *Guard) Audit(a netip.Addr, now time.Time) (bool, int) {
	g.mu.Lock()
	defer g.mu.Unlock()
	s := g.entry(a, now)
	if s == nil {
		g.overflow++
		return false, 0
	}
	s.refusals++
	if s.refusals > 10 {
		s.suppressed++
		return false, 0
	}
	n := s.suppressed
	s.suppressed = 0
	return true, n
}
func (g *Guard) Suppressed() map[netip.Addr]int {
	g.mu.Lock()
	defer g.mu.Unlock()
	out := map[netip.Addr]int{}
	if g.overflow > 0 {
		out[netip.Addr{}] = g.overflow
		g.overflow = 0
	}
	for a, s := range g.sources {
		if s.suppressed > 0 {
			out[a] = s.suppressed
			s.suppressed = 0
		}
	}
	return out
}
