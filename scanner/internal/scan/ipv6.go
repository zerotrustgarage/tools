package scan

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"sort"
	"sync"
	"time"

	"github.com/zerotrustgarage/tools/scanner/internal/probe"
	"github.com/zerotrustgarage/tools/scanner/internal/targets"
)

// IPv6 neighbor discovery (SC3). A /64 cannot be swept, so Scanner does not
// try: it reads the neighbor table the operating system keeps for the local link
// (which holds exactly the IPv6 hosts that have talked to this computer or
// answered it), optionally after one multicast echo to ff02::1 has made the
// link's hosts announce themselves. Each neighbor is then linked to the IPv4
// device with the same MAC address, or becomes a device of its own.
const (
	// maxIPv6Devices bounds the IPv6-only devices one scan adds. A hostile
	// router can flood a neighbor table; a scan stays bounded whatever it holds.
	maxIPv6Devices = 1024
	// maxIPv6Interfaces bounds the multicast echoes: one per interface.
	maxIPv6Interfaces = 8
	// ndpSettle is how long the link gets to answer the multicast echo before
	// the table is read.
	ndpSettle = 1500 * time.Millisecond
)

// MaxIPv6PerDevice bounds the addresses listed for one device (privacy
// extensions give a host a new temporary address every day). Saved files are
// held to it when they are read.
const MaxIPv6PerDevice = 16

// ParseHostAddr parses the address of a recorded host: canonical IPv4, or
// canonical IPv6 (never IPv4-mapped, unspecified or multicast) whose zone, if
// any, is on a link-local address and is one Scanner would write. Files are
// untrusted, so anything else is refused rather than repaired.
func ParseHostAddr(s string) (netip.Addr, bool) {
	a, err := netip.ParseAddr(s)
	if err != nil || a.String() != s {
		return netip.Addr{}, false
	}
	switch {
	case a.Is4():
		return a, true
	case a.Is6() && !a.Is4In6() && !a.IsUnspecified() && !a.IsMulticast():
		if z := a.Zone(); z != "" && (!a.IsLinkLocalUnicast() || !probe.ValidZone(z)) {
			return netip.Addr{}, false
		}
		return a, true
	}
	return netip.Addr{}, false
}

// ipv6Device is one device seen in the neighbor table: every internal address
// that resolved to the same MAC address.
type ipv6Device struct {
	mac     net.HardwareAddr
	addrs   []netip.Addr // link-local first, then unique local, then global
	ifIndex int
}

func (d *ipv6Device) strings() []string {
	out := make([]string, len(d.addrs))
	for i, a := range d.addrs {
		out[i] = a.String()
	}
	return out
}

// primary is the address a scan connects to: link-local when the device has one
// (it exists on every IPv6 host and does not change with the network's prefix),
// else a unique local, else a global address.
func (d *ipv6Device) primary() netip.Addr { return d.addrs[0] }

type ipv6State struct {
	mu     sync.Mutex
	byMAC  map[string]*ipv6Device
	order  []string // MAC keys, sorted
	linked map[string]bool
}

var uniqueLocal6 = netip.MustParsePrefix("fc00::/7")

// addrRank orders a device's addresses: link-local, unique local, global.
func addrRank(a netip.Addr) int {
	switch {
	case a.IsLinkLocalUnicast():
		return 0
	case uniqueLocal6.Contains(a.WithZone("")):
		return 1
	}
	return 2
}

// ipv6Interfaces returns the interfaces (index to name) that hold an IPv4
// address with a target on its network: the links the scan is about. IPv6
// neighbors elsewhere on the machine are not the technician's subject.
func (e *engine) ipv6Interfaces() map[int]string {
	out := map[int]string{}
	for _, l := range e.locals {
		if !l.addr.Is4() || l.addr.IsLoopback() || l.prefix.Bits() >= 32 || l.ifIndex <= 0 {
			continue
		}
		for _, t := range e.cfg.Targets {
			if l.prefix.Contains(t) {
				out[l.ifIndex] = l.ifName
				break
			}
		}
	}
	return out
}

// ownPrefixes6 are the scanner's own on-link global prefixes: the networks of
// the global IPv6 addresses its interfaces hold. A neighbor inside one is on the
// network the technician is attached to, so it counts as internal.
func (e *engine) ownPrefixes6() []netip.Prefix {
	var out []netip.Prefix
	for _, l := range e.locals {
		if l.addr.Is6() && l.addr.IsGlobalUnicast() && l.prefix.IsValid() && l.prefix.Bits() < 128 {
			out = append(out, l.prefix)
		}
	}
	return out
}

func (e *engine) discoverIPv6(ctx context.Context, sum *Summary) {
	ifs := e.ipv6Interfaces()
	if len(ifs) == 0 {
		return // no target is on a directly connected network: nothing on a link to read
	}
	open := e.hk.table6
	if open == nil {
		open = probe.NewNeighborTable6
	}
	table, err := open()
	if err != nil {
		sum.Warnings = append(sum.Warnings, "IPv6 neighbor discovery is not available ("+err.Error()+"); this scan is IPv4 only.")
		return
	}

	if e.cfg.Probes.IPv6Ping {
		ping := e.hk.ping6
		if ping == nil {
			ping = probe.MulticastPing6
		}
		indexes := make([]int, 0, len(ifs))
		for idx := range ifs {
			indexes = append(indexes, idx)
		}
		sort.Ints(indexes)
		if len(indexes) > maxIPv6Interfaces {
			indexes = indexes[:maxIPv6Interfaces]
		}
		sent := 0
		var pingErr error
		for _, idx := range indexes {
			if !e.pace(ctx) {
				break
			}
			if err := ping(ctx, idx, ifs[idx], e.cfg.PingTimeout); err != nil {
				pingErr = err
				continue
			}
			sent++
		}
		if sent > 0 {
			sum.Methods = append(sum.Methods, "icmp6-multicast")
			wait := e.hk.settle
			if wait <= 0 {
				wait = ndpSettle
			}
			t := time.NewTimer(wait)
			select {
			case <-t.C:
			case <-ctx.Done():
			}
			t.Stop()
		} else if pingErr != nil {
			sum.Warnings = append(sum.Warnings, "The IPv6 multicast echo could not be sent ("+pingErr.Error()+"); only neighbors the system already knew are listed.")
		}
	}
	if ctx.Err() != nil {
		return
	}

	raw, err := table.Read()
	if err != nil {
		sum.Warnings = append(sum.Warnings, "The IPv6 neighbor table could not be read ("+err.Error()+"); this scan is IPv4 only.")
		return
	}
	sum.Methods = append(sum.Methods, table.Method())
	e.v6 = e.groupNeighbors6(raw, ifs, sum)
}

// groupNeighbors6 applies the scope guard to the table and groups what is left
// by MAC address.
func (e *engine) groupNeighbors6(raw []probe.Neighbor6, ifs map[int]string, sum *Summary) *ipv6State {
	own := e.ownPrefixes6()
	selfMAC := map[string]bool{}
	selfAddr := map[netip.Addr]bool{}
	for _, l := range e.locals {
		if len(l.mac) > 0 {
			selfMAC[l.mac.String()] = true
		}
		if l.addr.Is6() {
			selfAddr[l.addr] = true
		}
	}
	state := &ipv6State{byMAC: map[string]*ipv6Device{}, linked: map[string]bool{}}
	public := 0
	for _, n := range probe.CleanNeighbors6(raw, func(idx int) string { return probe.ZoneName(idx, ifs[idx]) }) {
		if _, onScan := ifs[n.IfIndex]; !onScan {
			continue
		}
		if selfMAC[n.MAC.String()] || selfAddr[n.Addr.WithZone("")] {
			continue
		}
		// Scope guard: link-local, unique local and the scanner's own global
		// prefixes are internal. Any other global address is public space and
		// needs the same authorization as public IPv4 targets.
		if !targets.IsInternalOn(n.Addr, own) && !e.cfg.AllowPublic {
			public++
			continue
		}
		key := n.MAC.String()
		d := state.byMAC[key]
		if d == nil {
			d = &ipv6Device{mac: n.MAC, ifIndex: n.IfIndex}
			state.byMAC[key] = d
		}
		d.addrs = append(d.addrs, n.Addr)
	}
	if public > 0 {
		sum.Warnings = append(sum.Warnings, fmt.Sprintf("%d IPv6 neighbor address(es) outside the link-local, unique local and on-link prefixes were not used; public addresses need --allow-public and authorization from their owner.", public))
	}
	for key, d := range state.byMAC {
		sort.Slice(d.addrs, func(i, j int) bool {
			ri, rj := addrRank(d.addrs[i]), addrRank(d.addrs[j])
			if ri != rj {
				return ri < rj
			}
			return d.addrs[i].Less(d.addrs[j])
		})
		if len(d.addrs) > MaxIPv6PerDevice {
			d.addrs = d.addrs[:MaxIPv6PerDevice]
		}
		state.order = append(state.order, key)
	}
	sort.Strings(state.order)
	if len(state.order) == 0 {
		return nil
	}
	return state
}

// linkIPv6 returns the IPv6 addresses of the device with this MAC address and
// marks the device as linked to an IPv4 host, so it is not scanned again as an
// IPv6-only device.
func (e *engine) linkIPv6(mac net.HardwareAddr) []string {
	if e.v6 == nil || len(mac) != 6 {
		return nil
	}
	key := mac.String()
	e.v6.mu.Lock()
	defer e.v6.mu.Unlock()
	d := e.v6.byMAC[key]
	if d == nil {
		return nil
	}
	e.v6.linked[key] = true
	return d.strings()
}

// unlinkedIPv6 are the devices no IPv4 host claimed, in a stable order and
// bounded; dropped counts the ones the bound cut off.
func (e *engine) unlinkedIPv6() (out []*ipv6Device, dropped int) {
	if e.v6 == nil {
		return nil, 0
	}
	e.v6.mu.Lock()
	defer e.v6.mu.Unlock()
	for _, key := range e.v6.order {
		if !e.v6.linked[key] {
			out = append(out, e.v6.byMAC[key])
		}
	}
	if len(out) > maxIPv6Devices {
		dropped = len(out) - maxIPv6Devices
		out = out[:maxIPv6Devices]
	}
	return out, dropped
}
