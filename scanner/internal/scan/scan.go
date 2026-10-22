// Package scan runs a network scan: it combines the probes into one pass
// per host, bounded in concurrency, and streams each finished host to the
// caller.
package scan

import (
	"context"
	"net"
	"net/netip"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/zerotrustgarage/tools/scanner/internal/classify"
	"github.com/zerotrustgarage/tools/scanner/internal/oui"
	"github.com/zerotrustgarage/tools/scanner/internal/probe"
)

// DefaultPorts are probed on every live host: the services that identify
// what a LAN device is. Kept short so a /24 finishes in about a minute.
var DefaultPorts = []int{
	21, 22, 23, 25, 53, 80, 81, 88, 110, 135, 139, 143, 389, 443, 445, 515, 548, 554,
	631, 636, 902, 993, 1433, 1883, 3306, 3389, 5000, 5001, 5357, 5900, 5985, 8000,
	8006, 8008, 8080, 8081, 8443, 8888, 9100, 9443,
}

// discoveryPorts are tried on hosts that ignored ICMP and are off-link
// (so ARP cannot vouch for them), to find firewalled but live hosts.
var discoveryPorts = []int{80, 443, 445, 22, 3389, 135, 139, 8080}

// Probes selects the optional probes. The zero value disables everything
// optional; use DefaultProbes.
type Probes struct {
	Ports      bool `json:"ports"`
	Banners    bool `json:"banners"`
	ReverseDNS bool `json:"reverse_dns"`
	NetBIOS    bool `json:"netbios"`
	MDNS       bool `json:"mdns"`
	SSDP       bool `json:"ssdp"`
	// SNMP is opt-in and needs Config.SNMP credentials (SNMPv3 authPriv,
	// read-only GET and GETNEXT).
	SNMP bool `json:"snmp"`
}

// DefaultProbes is the safe default set: identification only, nothing
// that enumerates shares, users or authenticates.
var DefaultProbes = Probes{Ports: true, Banners: true, ReverseDNS: true, NetBIOS: true, MDNS: true, SSDP: true}

// Config describes one scan.
type Config struct {
	Targets         []netip.Addr
	Ports           []int
	Probes          Probes
	Concurrency     int           // hosts in flight, default 128
	PortConcurrency int           // connections per host, default 16
	PingTimeout     time.Duration // default 1s
	ConnectTimeout  time.Duration // default 800ms
	ProbeTimeout    time.Duration // banners and name queries, default 2s
	Resolver        *net.Resolver
	// SNMP holds SNMPv3 credentials when Probes.SNMP is set. They live only
	// as long as the scan and never appear in results.
	SNMP *probe.SNMPCredentials
	// Now is injectable for tests.
	Now func() time.Time
}

// Host is everything learned about one address.
type Host struct {
	IP            string             `json:"ip"`
	Status        string             `json:"status"` // "up" or "down"
	Self          bool               `json:"self,omitempty"`
	Evidence      []string           `json:"evidence,omitempty"`
	RTTMillis     float64            `json:"rtt_ms,omitempty"`
	TTL           int                `json:"ttl,omitempty"`
	OSHint        string             `json:"os_hint,omitempty"`
	MAC           string             `json:"mac,omitempty"`
	Vendor        string             `json:"vendor,omitempty"`
	MACRandomized bool               `json:"mac_randomized,omitempty"`
	Name          string             `json:"name,omitempty"`
	DNSName       string             `json:"dns_name,omitempty"`
	NetBIOS       *probe.NetBIOSInfo `json:"netbios,omitempty"`
	MDNS          *probe.MDNSInfo    `json:"mdns,omitempty"`
	UPnP          *probe.UPnPInfo    `json:"upnp,omitempty"`
	SNMP          *probe.SNMPInfo    `json:"snmp,omitempty"`
	Services      []probe.Service    `json:"services,omitempty"`
	Type          string             `json:"type,omitempty"`
	TypeReason    string             `json:"type_reason,omitempty"`
	Findings      []classify.Finding `json:"findings,omitempty"`
	ScannedAt     time.Time          `json:"scanned_at"`
}

// Summary describes a finished scan.
type Summary struct {
	Started  time.Time `json:"started"`
	Finished time.Time `json:"finished"`
	Total    int       `json:"total"`
	Up       int       `json:"up"`
	Methods  []string  `json:"methods"`
	Warnings []string  `json:"warnings,omitempty"`
	Canceled bool      `json:"canceled,omitempty"`
}

// Observer receives results as they arrive. Calls are serialized.
type Observer interface {
	Host(h Host)
	Progress(done, total int)
}

type localIface struct {
	addr   netip.Addr
	prefix netip.Prefix
	mac    net.HardwareAddr
}

type engine struct {
	cfg      Config
	pinger   probe.Pinger
	arp      probe.Neighbors
	ouiDB    *oui.DB
	locals   []localIface
	ssdp     map[netip.Addr]*probe.UPnPInfo
	ssdpDone chan struct{}
	obsMu    sync.Mutex
	obs      Observer
}

// Run scans cfg.Targets and reports each host to obs. It returns when the
// scan finishes or ctx is canceled.
func Run(ctx context.Context, cfg Config, obs Observer) Summary {
	cfg = withDefaults(cfg)
	sum := Summary{Started: cfg.Now(), Total: len(cfg.Targets)}
	e := &engine{cfg: cfg, obs: obs, ssdpDone: make(chan struct{})}

	if p, err := probe.NewPinger(); err == nil {
		e.pinger = p
		defer p.Close()
		sum.Methods = append(sum.Methods, p.Method())
	} else {
		sum.Warnings = append(sum.Warnings, "ICMP echo is not available ("+err.Error()+"); liveness relies on ARP and TCP.")
	}
	e.arp = probe.NewNeighbors()
	if m := e.arp.Method(); m != "none" {
		sum.Methods = append(sum.Methods, m)
	}
	sum.Methods = append(sum.Methods, "tcp-connect")
	if db, err := oui.Default(); err == nil {
		e.ouiDB = db
	} else {
		sum.Warnings = append(sum.Warnings, "MAC vendor database failed to load: "+err.Error())
	}
	e.locals = localInterfaces()
	if cfg.Probes.SNMP {
		if cfg.SNMP == nil {
			e.cfg.Probes.SNMP = false
			sum.Warnings = append(sum.Warnings, "SNMP was requested without credentials and was skipped.")
		} else if err := cfg.SNMP.Validate(); err != nil {
			e.cfg.Probes.SNMP = false
			sum.Warnings = append(sum.Warnings, "SNMP skipped: "+err.Error())
		} else {
			sum.Methods = append(sum.Methods, "snmpv3-authpriv")
		}
	}

	if cfg.Probes.SSDP && e.anyOnLink() {
		go func() {
			defer close(e.ssdpDone)
			if m, err := probe.SSDPDiscover(ctx, 2500*time.Millisecond); err == nil {
				e.ssdp = m
			}
		}()
	} else {
		close(e.ssdpDone)
	}

	var (
		done atomic.Int64
		up   atomic.Int64
		wg   sync.WaitGroup
		sem  = make(chan struct{}, cfg.Concurrency)
	)
dispatch:
	for _, addr := range cfg.Targets {
		select {
		case <-ctx.Done():
			break dispatch
		case sem <- struct{}{}:
		}
		wg.Add(1)
		go func(addr netip.Addr) {
			defer func() { <-sem; wg.Done() }()
			h := e.scanHost(ctx, addr)
			if h.Status == "up" {
				up.Add(1)
			}
			n := int(done.Add(1))
			e.obsMu.Lock()
			defer e.obsMu.Unlock()
			// After cancel, unfinished hosts come back "down" without having
			// been probed; report only what was actually established.
			if ctx.Err() == nil || h.Status == "up" {
				obs.Host(h)
			}
			obs.Progress(n, len(cfg.Targets))
		}(addr)
	}
	wg.Wait()
	sum.Up = int(up.Load())
	sum.Canceled = ctx.Err() != nil
	sum.Finished = cfg.Now()
	return sum
}

func withDefaults(c Config) Config {
	if c.Concurrency <= 0 {
		c.Concurrency = 128
	}
	if c.PortConcurrency <= 0 {
		c.PortConcurrency = 16
	}
	if c.PingTimeout <= 0 {
		c.PingTimeout = time.Second
	}
	if c.ConnectTimeout <= 0 {
		c.ConnectTimeout = 800 * time.Millisecond
	}
	if c.ProbeTimeout <= 0 {
		c.ProbeTimeout = 2 * time.Second
	}
	if c.Ports == nil {
		c.Ports = DefaultPorts
	}
	if c.Resolver == nil {
		c.Resolver = net.DefaultResolver
	}
	if c.Now == nil {
		c.Now = time.Now
	}
	return c
}

func localInterfaces() []localIface {
	var out []localIface
	ifs, err := net.Interfaces()
	if err != nil {
		return nil
	}
	for _, ifc := range ifs {
		if ifc.Flags&net.FlagUp == 0 {
			continue
		}
		addrs, _ := ifc.Addrs()
		for _, a := range addrs {
			ipn, ok := a.(*net.IPNet)
			if !ok {
				continue
			}
			ip, ok := netip.AddrFromSlice(ipn.IP)
			if !ok || !ip.Unmap().Is4() {
				continue
			}
			ones, _ := ipn.Mask.Size()
			out = append(out, localIface{addr: ip.Unmap(), prefix: netip.PrefixFrom(ip.Unmap(), ones).Masked(), mac: ifc.HardwareAddr})
		}
	}
	return out
}

func (e *engine) onLink(a netip.Addr) bool {
	for _, l := range e.locals {
		if !l.addr.IsLoopback() && l.prefix.Bits() < 32 && l.prefix.Contains(a) {
			return true
		}
	}
	return false
}

func (e *engine) anyOnLink() bool {
	for _, a := range e.cfg.Targets {
		if e.onLink(a) {
			return true
		}
	}
	return false
}

func (e *engine) self(a netip.Addr) (net.HardwareAddr, bool) {
	for _, l := range e.locals {
		if l.addr == a {
			return l.mac, true
		}
	}
	return nil, false
}

func (e *engine) scanHost(ctx context.Context, addr netip.Addr) Host {
	h := Host{IP: addr.String(), Status: "down", ScannedAt: e.cfg.Now().UTC()}
	if ctx.Err() != nil {
		return h
	}
	var mac net.HardwareAddr

	if m, ok := e.self(addr); ok {
		h.Self = true
		mac = m
		h.Evidence = append(h.Evidence, "local-interface")
	}

	// Liveness: ICMP and ARP in parallel.
	var wg sync.WaitGroup
	var echo *probe.EchoReply
	if e.pinger != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for attempt := 0; attempt < 2 && ctx.Err() == nil; attempt++ {
				if r, err := e.pinger.Ping(ctx, addr, e.cfg.PingTimeout); err == nil {
					echo = &r
					return
				}
			}
		}()
	}
	var arpMAC net.HardwareAddr
	if !h.Self && e.onLink(addr) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if m, ok := e.arp.Resolve(ctx, addr, 1500*time.Millisecond); ok {
				arpMAC = m
			}
		}()
	}
	wg.Wait()
	if echo != nil {
		h.Evidence = append(h.Evidence, "icmp")
		h.RTTMillis = float64(echo.RTT.Microseconds()) / 1000
		h.TTL = echo.TTL
		h.OSHint = probe.OSHintFromTTL(echo.TTL)
	}
	if arpMAC != nil {
		h.Evidence = append(h.Evidence, "arp")
		mac = arpMAC
	}

	alive := h.Self || echo != nil || arpMAC != nil
	var open []int
	if e.cfg.Probes.Ports {
		ports := e.cfg.Ports
		if !alive {
			ports = intersect(discoveryPorts, e.cfg.Ports)
		}
		res := probe.ScanPorts(ctx, addr, ports, e.cfg.ConnectTimeout, e.cfg.PortConcurrency)
		open = res.Open
		if len(res.Open) > 0 {
			h.Evidence = append(h.Evidence, "tcp-open")
		} else if res.Refused {
			h.Evidence = append(h.Evidence, "tcp-reset")
		}
		if !alive && (len(res.Open) > 0 || res.Refused) {
			alive = true
			rest := subtract(e.cfg.Ports, ports)
			more := probe.ScanPorts(ctx, addr, rest, e.cfg.ConnectTimeout, e.cfg.PortConcurrency)
			open = mergeSorted(open, more.Open)
		}
	}
	if !alive {
		return h
	}
	h.Status = "up"

	// Identification, all in parallel.
	var mu sync.Mutex
	run := func(f func()) {
		wg.Add(1)
		go func() { defer wg.Done(); f() }()
	}
	if e.cfg.Probes.ReverseDNS {
		run(func() {
			c, cancel := context.WithTimeout(ctx, e.cfg.ProbeTimeout)
			defer cancel()
			if names, err := e.cfg.Resolver.LookupAddr(c, addr.String()); err == nil && len(names) > 0 {
				mu.Lock()
				h.DNSName = strings.TrimSuffix(names[0], ".")
				mu.Unlock()
			}
		})
	}
	if e.cfg.Probes.NetBIOS {
		run(func() {
			if nb, err := probe.NetBIOSStatus(ctx, addr, e.cfg.ProbeTimeout); err == nil {
				mu.Lock()
				h.NetBIOS = nb
				mu.Unlock()
			}
		})
	}
	if e.cfg.Probes.MDNS {
		run(func() {
			if md, err := probe.MDNSQuery(ctx, addr, e.cfg.ProbeTimeout); err == nil {
				mu.Lock()
				h.MDNS = md
				mu.Unlock()
			}
		})
	}
	if e.cfg.Probes.SNMP {
		run(func() {
			if info, err := probe.SNMPQuery(ctx, addr, *e.cfg.SNMP, e.cfg.ProbeTimeout); err == nil {
				mu.Lock()
				h.SNMP = info
				mu.Unlock()
			}
		})
	}
	services := make([]probe.Service, len(open))
	for i, port := range open {
		if !e.cfg.Probes.Banners {
			services[i] = probe.Service{Port: port, Name: nameOf(port)}
			continue
		}
		run(func() { services[i] = probe.Grab(ctx, addr, port, e.cfg.ProbeTimeout) })
	}
	if e.cfg.Probes.SSDP {
		run(func() {
			select {
			case <-e.ssdpDone:
			case <-ctx.Done():
				return
			}
			if info, ok := e.ssdp[addr]; ok {
				cp := *info
				probe.FetchDescription(ctx, &cp, e.cfg.ProbeTimeout)
				mu.Lock()
				h.UPnP = &cp
				mu.Unlock()
			}
		})
	}
	wg.Wait()
	h.Services = services

	if mac == nil && h.NetBIOS != nil && h.NetBIOS.MAC != "" {
		// NetBIOS carries the MAC even across routers (Windows only).
		if m, err := net.ParseMAC(h.NetBIOS.MAC); err == nil {
			mac = m
			h.Evidence = append(h.Evidence, "netbios-mac")
		}
	}
	if mac != nil {
		h.MAC = mac.String()
		if e.ouiDB != nil {
			info := e.ouiDB.LookupHW(mac)
			h.Vendor = info.Vendor
			h.MACRandomized = info.Randomized
		}
	}
	h.Name = bestName(&h)
	e.classify(&h)
	return h
}

func bestName(h *Host) string {
	switch {
	case h.DNSName != "":
		return h.DNSName
	case h.NetBIOS != nil && h.NetBIOS.Computer != "":
		return h.NetBIOS.Computer
	case h.SNMP != nil && h.SNMP.SysName != "":
		return h.SNMP.SysName
	case h.MDNS != nil && h.MDNS.Hostname != "":
		return h.MDNS.Hostname
	case h.UPnP != nil && h.UPnP.FriendlyName != "":
		return h.UPnP.FriendlyName
	}
	return ""
}

func (e *engine) classify(h *Host) {
	ev := classify.Evidence{
		Vendor:        h.Vendor,
		MACRandomized: h.MACRandomized,
		Hostname:      h.Name,
		TTL:           h.TTL,
		OpenPorts:     map[int]bool{},
		NetBIOS:       h.NetBIOS != nil,
	}
	for _, s := range h.Services {
		ev.OpenPorts[s.Port] = true
		if s.HTTP != nil {
			ev.HTTPText = append(ev.HTTPText, s.HTTP.Server, s.HTTP.Title, s.HTTP.Realm, s.HTTP.PoweredBy)
		}
		if s.TLS != nil {
			ev.CertExpiries = append(ev.CertExpiries, s.TLS.NotAfter)
			ev.HTTPText = append(ev.HTTPText, s.TLS.Subject)
		}
		if s.Product != "" {
			ev.HTTPText = append(ev.HTTPText, s.Product)
		}
	}
	if h.MDNS != nil {
		ev.MDNSServices = h.MDNS.Services
	}
	if h.UPnP != nil {
		ev.UPnPType = h.UPnP.DeviceType
		ev.UPnPMaker = h.UPnP.Manufacturer
		ev.UPnPModel = h.UPnP.ModelName + " " + h.UPnP.ModelNumber + " " + h.UPnP.Server
	}
	if h.SNMP != nil {
		ev.SNMPDescr = h.SNMP.SysDescr + " " + h.SNMP.Model
		ev.SNMPMaker = h.SNMP.Manufacturer + " " + h.SNMP.Enterprise
	}
	if h.NetBIOS != nil {
		for _, n := range h.NetBIOS.Names {
			if n.Group {
				ev.NetBIOSGroups = append(ev.NetBIOSGroups, n.Suffix)
			}
		}
	}
	r := classify.Classify(&ev)
	if h.Self {
		r = classify.Result{Type: "This computer", Reason: "address of a local interface"}
	}
	h.Type, h.TypeReason = r.Type, r.Reason
	h.Findings = classify.Findings(&ev, e.cfg.Now())
}

func nameOf(port int) string {
	if n := probe.WellKnown[port]; n != "" {
		return n
	}
	return "unknown"
}

func intersect(a, b []int) []int {
	in := map[int]bool{}
	for _, x := range b {
		in[x] = true
	}
	var out []int
	for _, x := range a {
		if in[x] {
			out = append(out, x)
		}
	}
	return out
}

func subtract(a, b []int) []int {
	in := map[int]bool{}
	for _, x := range b {
		in[x] = true
	}
	var out []int
	for _, x := range a {
		if !in[x] {
			out = append(out, x)
		}
	}
	return out
}

func mergeSorted(a, b []int) []int {
	out := append(append([]int{}, a...), b...)
	sort.Ints(out)
	return out
}
