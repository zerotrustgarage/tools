package probe

import (
	"context"
	"net"
	"net/netip"
	"strconv"
	"sync"
	"time"
)

// PortResult is the outcome of TCP connect probes against one host.
type PortResult struct {
	Open []int
	// Refused is true when at least one port answered with a reset. A
	// reset proves the host is up even if every probed port is closed.
	Refused bool
}

// DialTCP opens a TCP connection to addr:port. An IPv6 address may carry a zone
// (fe80::1%eth0), which names the interface a link-local address lives on.
func DialTCP(ctx context.Context, addr netip.Addr, port int, timeout time.Duration) (net.Conn, error) {
	d := net.Dialer{Timeout: timeout}
	return d.DialContext(ctx, tcpNetwork(addr), net.JoinHostPort(addr.String(), strconv.Itoa(port)))
}

// tcpNetwork pins the address family, so an IPv4 target never falls back to
// IPv6 and the reverse.
func tcpNetwork(addr netip.Addr) string {
	if addr.Is6() && !addr.Is4In6() {
		return "tcp6"
	}
	return "tcp4"
}

// hostHeader is addr as an HTTP Host value: IPv6 literals are bracketed and
// never carry a zone (RFC 6874 forbids sending one).
func hostHeader(addr netip.Addr) string {
	if addr.Is6() && !addr.Is4In6() {
		return "[" + addr.WithZone("").String() + "]"
	}
	return addr.String()
}

// ScanPorts tries a full TCP connect to each port (no raw packets, no
// half-open scans) with at most parallel connections in flight.
func ScanPorts(ctx context.Context, addr netip.Addr, ports []int, timeout time.Duration, parallel int) PortResult {
	if parallel <= 0 {
		parallel = 16
	}
	var (
		mu  sync.Mutex
		res PortResult
		wg  sync.WaitGroup
		sem = make(chan struct{}, parallel)
	)
	for _, port := range ports {
		if ctx.Err() != nil {
			break
		}
		sem <- struct{}{}
		wg.Add(1)
		go func(port int) {
			defer func() { <-sem; wg.Done() }()
			c, err := DialTCP(ctx, addr, port, timeout)
			mu.Lock()
			defer mu.Unlock()
			if err == nil {
				c.Close()
				res.Open = append(res.Open, port)
				return
			}
			if isRefused(err) {
				res.Refused = true
			}
		}(port)
	}
	wg.Wait()
	sortInts(res.Open)
	return res
}

func sortInts(s []int) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}
