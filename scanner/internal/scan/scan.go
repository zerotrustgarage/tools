// Package scan runs a network scan: it combines the probes into one pass
// per host, bounded in concurrency, and streams each finished host to the
// caller.
package scan

import (
	"context"
	"fmt"
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
	"github.com/zerotrustgarage/tools/scanner/internal/targets"
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
	// WSD is one WS-Discovery multicast Probe per scan (UDP 3702), for the
	// device types printers, ONVIF cameras and Windows hosts announce.
	WSD bool `json:"wsd"`
	// LLMNR is one unicast TCP name query (RFC 4795) to each on-link host.
	LLMNR bool `json:"llmnr"`
	// SMB is one SMB2 NEGOTIATE to a host whose TCP 445 the scan found open.
	SMB bool `json:"smb"`
	// Favicon is one GET of /favicon.ico on a web port the scan found open and
	// identified as HTTP or HTTPS. It needs Banners, which finds those ports.
	Favicon bool `json:"favicon"`
	// SNMP is opt-in and needs Config.SNMP credentials (SNMPv3 authPriv,
	// read-only GET and GETNEXT).
	SNMP bool `json:"snmp"`
	// IPv6 reads the operating system's IPv6 neighbor table for the links the
	// targets are on, links each neighbor to the IPv4 device with the same MAC
	// address, and scans neighbors that match no IPv4 device as devices of their
	// own (SC3). It never sweeps an IPv6 prefix and sends nothing by itself.
	IPv6 bool `json:"ipv6"`
	// IPv6Ping adds one ICMPv6 Echo Request to ff02::1 per on-link interface
	// before the table is read, so that hosts which answer appear in it. It needs
	// IPv6, and gentle mode never sends it.
	IPv6Ping bool `json:"ipv6_ping"`
}

// DefaultProbes is the safe default set: identification only, nothing
// that enumerates shares, users or authenticates.
var DefaultProbes = Probes{Ports: true, Banners: true, ReverseDNS: true, NetBIOS: true, MDNS: true, SSDP: true, WSD: true, LLMNR: true, SMB: true, Favicon: true, IPv6: true, IPv6Ping: true}

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
	// PingAttempts is echo requests per host, default 2.
	PingAttempts int
	// Pacer, when set, gates the start of every probe (echo, ARP, TCP
	// connect, name query, banner grab). Nil means unpaced, the default.
	Pacer Pacer
	// Mode labels the scan in its summary; set only for non-default modes.
	Mode string
	// AllowPublic is the technician's authorization for public address space. The
	// caller has already applied it to Targets; the engine applies it again to
	// the IPv6 neighbors it finds, so a public IPv6 address on the link is never
	// probed without it.
	AllowPublic bool
	// Now is injectable for tests.
	Now func() time.Time
}

// Host is everything learned about one address.
type Host struct {
	Annotation
	IP            string             `json:"ip"`
	Status        string             `json:"status"` // "up" or "down"
	Self          bool               `json:"self,omitempty"`
	Evidence      []string           `json:"evidence,omitempty"`
	RTTMillis     float64            `json:"rtt_ms,omitempty"`
	TTL           int                `json:"ttl,omitempty"`
	OSHint        string             `json:"os_hint,omitempty"`
	MAC           string             `json:"mac,omitempty"`
	LastKnownMAC  string             `json:"last_known_mac,omitempty"` // historical inventory only; never fresh probe evidence
	Vendor        string             `json:"vendor,omitempty"`
	MACRandomized bool               `json:"mac_randomized,omitempty"`
	Name          string             `json:"name,omitempty"`
	DNSName       string             `json:"dns_name,omitempty"`
	NetBIOS       *probe.NetBIOSInfo `json:"netbios,omitempty"`
	MDNS          *probe.MDNSInfo    `json:"mdns,omitempty"`
	UPnP          *probe.UPnPInfo    `json:"upnp,omitempty"`
	WSD           *probe.WSDInfo     `json:"wsd,omitempty"`
	LLMNR         *probe.LLMNRInfo   `json:"llmnr,omitempty"`
	SMB           *probe.SMBInfo     `json:"smb,omitempty"`
	SNMP          *probe.SNMPInfo    `json:"snmp,omitempty"`
	Services      []probe.Service    `json:"services,omitempty"`
	// IPv6 lists the device's IPv6 addresses seen in the neighbor table with the
	// same MAC address (SC3), canonical, link-local ones with their zone. For a
	// device found only over IPv6 it includes IP.
	IPv6       []string           `json:"ipv6,omitempty"`
	Type       string             `json:"type,omitempty"`
	TypeReason string             `json:"type_reason,omitempty"`
	Findings   []classify.Finding `json:"findings,omitempty"`
	ScannedAt  time.Time          `json:"scanned_at"`
}

// Annotation is supplied by the technician, never by a probe.
type Annotation struct {
	Notes    string   `json:"notes,omitempty"`
	Tags     []string `json:"tags,omitempty"`
	Favorite bool     `json:"favorite,omitempty"`
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
	// Mode is set only for a non-default mode, so default output is unchanged.
	Mode string `json:"mode,omitempty"`
	// IPv6Only counts devices found only in the IPv6 neighbor table (they are
	// not among the requested addresses, so Total does not include them).
	IPv6Only int `json:"ipv6_only,omitempty"`
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
	// ifIndex and ifName identify the interface; IPv6 neighbor discovery uses
	// them to pick the links to read and to write link-local zones. Zero and ""
	// when unknown (the fakes in the SC11 tests leave them out).
	ifIndex int
	ifName  string
}

// hooks are the SC11 network exchanges and the local interface list. Production
// code uses the real probes (defaultHooks); tests substitute fakes so the
// engine's gating can be checked without a real LAN.
type hooks struct {
	wsd     func(ctx context.Context, wait time.Duration) (map[netip.Addr]*probe.WSDInfo, error)
	llmnr   func(ctx context.Context, addr netip.Addr, timeout time.Duration) (*probe.LLMNRInfo, error)
	smb     func(ctx context.Context, addr netip.Addr, timeout time.Duration) (*probe.SMBInfo, error)
	favicon func(ctx context.Context, addr netip.Addr, port int, useTLS bool, timeout time.Duration) (*probe.FaviconInfo, error)
	locals  func() []localIface
	// pinger opens the ICMP pinger; nil means probe.NewPinger. Tests set it so
	// liveness does not depend on whether the runner allows unprivileged ICMP.
	pinger func() (probe.Pinger, error)
	// table6 opens the IPv6 neighbor table and ping6 sends the one multicast echo
	// (SC3); nil means the platform's own. settle is how long to wait after that
	// echo before reading the table; zero means ndpSettle.
	table6 func() (probe.NeighborTable6, error)
	// arp and ports replace the ARP resolver and the TCP connect scan, so a test
	// can give a host hardware evidence and open ports without a network.
	arp    func() probe.Neighbors
	ports  func(ctx context.Context, addr netip.Addr, ports []int, timeout time.Duration, parallel int) probe.PortResult
	ping6  func(ctx context.Context, ifIndex int, ifName string, timeout time.Duration) error
	settle time.Duration
}

func defaultHooks() hooks {
	return hooks{wsd: probe.WSDDiscover, llmnr: probe.LLMNRQuery, smb: probe.SMBNegotiate, favicon: probe.FetchFavicon, locals: localInterfaces}
}

type engine struct {
	hk       hooks
	cfg      Config
	pinger   probe.Pinger
	arp      probe.Neighbors
	ouiDB    *oui.DB
	locals   []localIface
	ssdp     map[netip.Addr]*probe.UPnPInfo
	ssdpDone chan struct{}
	wsd      map[netip.Addr]*probe.WSDInfo
	wsdDone  chan struct{}
	// ran records which SC11 probes sent at least one exchange, so the summary's
	// methods list what actually happened and not what was merely enabled.
	ranWSD, ranLLMNR, ranSMB, ranFavicon atomic.Bool
	obsMu                                sync.Mutex
	obs                                  Observer
	v6                                   *ipv6State // nil unless IPv6 neighbor discovery found anything (ipv6.go)
}

// Run scans cfg.Targets and reports each host to obs. It returns when the
// scan finishes or ctx is canceled.
func Run(ctx context.Context, cfg Config, obs Observer) Summary {
	return run(ctx, cfg, obs, defaultHooks())
}

func run(ctx context.Context, cfg Config, obs Observer, hk hooks) Summary {
	cfg = withDefaults(cfg)
	if cfg.Mode == ModeGentle {
		// Gentle mode never runs the SC11 probes. BuildPlan already leaves
		// them out; this keeps a hand-built gentle Config honest too.
		cfg.Probes.WSD, cfg.Probes.LLMNR, cfg.Probes.SMB, cfg.Probes.Favicon = false, false, false, false
		// The multicast echo is a packet to every host on the link; gentle mode
		// only reads the table the system already has.
		cfg.Probes.IPv6Ping = false
	}
	sum := Summary{Started: cfg.Now(), Total: len(cfg.Targets), Mode: cfg.Mode}
	e := &engine{hk: hk, cfg: cfg, obs: obs, ssdpDone: make(chan struct{}), wsdDone: make(chan struct{})}

	newPinger := probe.NewPinger
	if hk.pinger != nil {
		newPinger = hk.pinger
	}
	if p, err := newPinger(); err == nil {
		e.pinger = p
		defer p.Close()
		sum.Methods = append(sum.Methods, p.Method())
	} else {
		sum.Warnings = append(sum.Warnings, "ICMP echo is not available ("+err.Error()+"); liveness relies on ARP and TCP.")
	}
	if hk.arp != nil {
		e.arp = hk.arp()
	} else {
		e.arp = probe.NewNeighbors()
	}
	if m := e.arp.Method(); m != "none" {
		sum.Methods = append(sum.Methods, m)
	}
	sum.Methods = append(sum.Methods, "tcp-connect")
	if db, err := oui.Default(); err == nil {
		e.ouiDB = db
	} else {
		sum.Warnings = append(sum.Warnings, "MAC vendor database failed to load: "+err.Error())
	}
	e.locals = hk.locals()
	if cfg.Probes.IPv6 {
		e.discoverIPv6(ctx, &sum)
	}
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

	// WS-Discovery is one multicast Probe on the local link, so it is sent only
	// when some target is an on-link address in the default-permitted scope, and
	// only answers from such addresses are kept.
	if cfg.Probes.WSD && e.anyOnLinkInternal() {
		e.ranWSD.Store(true)
		go func() {
			defer close(e.wsdDone)
			if m, err := e.hk.wsd(ctx, 2500*time.Millisecond); err == nil {
				e.wsd = keepInternal(m)
			}
		}()
	} else {
		close(e.wsdDone)
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
	if only, dropped := e.unlinkedIPv6(); len(only) > 0 && ctx.Err() == nil {
		sum.IPv6Only = len(only)
		if dropped > 0 {
			sum.Warnings = append(sum.Warnings, fmt.Sprintf("%d more IPv6 neighbor device(s) were not scanned; one scan adds at most %d.", dropped, maxIPv6Devices))
		}
		total := len(cfg.Targets) + len(only)
	dispatch6:
		for _, dev := range only {
			select {
			case <-ctx.Done():
				break dispatch6
			case sem <- struct{}{}:
			}
			wg.Add(1)
			go func(dev *ipv6Device) {
				defer func() { <-sem; wg.Done() }()
				h := e.scanHost6(ctx, dev)
				if h.Status == "up" {
					up.Add(1)
				}
				n := int(done.Add(1))
				e.obsMu.Lock()
				defer e.obsMu.Unlock()
				if ctx.Err() == nil || h.Status == "up" {
					obs.Host(h)
				}
				obs.Progress(n, total)
			}(dev)
		}
		wg.Wait()
	}
	sum.Up = int(up.Load())
	for _, m := range []struct {
		ran  *atomic.Bool
		name string
	}{{&e.ranWSD, "ws-discovery"}, {&e.ranLLMNR, "llmnr"}, {&e.ranSMB, "smb2-negotiate"}, {&e.ranFavicon, "http-favicon"}} {
		if m.ran.Load() {
			sum.Methods = append(sum.Methods, m.name)
		}
	}
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
	if c.PingAttempts <= 0 {
		c.PingAttempts = 2
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
			if !ok {
				continue
			}
			ones, _ := ipn.Mask.Size()
			if ip = ip.Unmap(); !ip.Is4() {
				// IPv6 addresses are kept for self detection and for the on-link
				// global prefixes IPv6 neighbor discovery treats as internal.
				if ip.IsLoopback() {
					continue
				}
				out = append(out, localIface{addr: ip.WithZone(""), prefix: netip.PrefixFrom(ip.WithZone(""), ones).Masked(), mac: ifc.HardwareAddr, ifIndex: ifc.Index, ifName: ifc.Name})
				continue
			}
			out = append(out, localIface{addr: ip, prefix: netip.PrefixFrom(ip, ones).Masked(), mac: ifc.HardwareAddr, ifIndex: ifc.Index, ifName: ifc.Name})
		}
	}
	return out
}

func (e *engine) onLink(a netip.Addr) bool {
	for _, l := range e.locals {
		if l.addr.Is4() && !l.addr.IsLoopback() && l.prefix.Bits() < 32 && l.prefix.Contains(a) {
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

// linkLocalScope reports whether a is on a directly connected network and in
// the scope guard's default-permitted ranges: the only hosts link-local
// protocols (WS-Discovery, LLMNR) are used for.
func (e *engine) linkLocalScope(a netip.Addr) bool {
	return e.onLink(a) && targets.IsInternal(a)
}

// keepInternal drops multicast answers from addresses outside the scope guard's
// default-permitted ranges.
func keepInternal(m map[netip.Addr]*probe.WSDInfo) map[netip.Addr]*probe.WSDInfo {
	for a := range m {
		if !targets.IsInternal(a) {
			delete(m, a)
		}
	}
	return m
}

func (e *engine) anyOnLinkInternal() bool {
	for _, a := range e.cfg.Targets {
		if e.linkLocalScope(a) {
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
	return e.scanAddr(ctx, addr, nil)
}

// scanHost6 scans an IPv6-only device (SC3) through its primary address. The
// neighbor table already proved the device exists and gave its MAC address, so
// the hardware evidence comes from there.
func (e *engine) scanHost6(ctx context.Context, dev *ipv6Device) Host {
	return e.scanAddr(ctx, dev.primary(), dev)
}

// scanAddr is the per-host pipeline for an IPv4 target (seed nil) or an IPv6
// neighbor (seed set). IPv6 hosts skip the exchanges that exist only over IPv4
// (ARP, NetBIOS, LLMNR, the SSDP and WS-Discovery multicast answers) and SNMP,
// which Scanner has not been validated against over IPv6.
func (e *engine) scanAddr(ctx context.Context, addr netip.Addr, seed *ipv6Device) Host {
	h := Host{IP: addr.String(), Status: "down", ScannedAt: e.cfg.Now().UTC()}
	if ctx.Err() != nil {
		return h
	}
	var mac net.HardwareAddr
	v6 := seed != nil
	if v6 {
		mac = seed.mac
		h.IPv6 = seed.strings()
		h.Evidence = append(h.Evidence, "ndp")
	}

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
			for attempt := 0; attempt < e.cfg.PingAttempts && ctx.Err() == nil; attempt++ {
				if !e.pace(ctx) {
					return
				}
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
			if !e.pace(ctx) {
				return
			}
			if m, ok := e.arp.Resolve(ctx, addr, 1500*time.Millisecond); ok {
				arpMAC = m
			}
		}()
	}
	wg.Wait()
	if echo != nil {
		if v6 {
			h.Evidence = append(h.Evidence, "icmp6")
		} else {
			h.Evidence = append(h.Evidence, "icmp")
		}
		h.RTTMillis = float64(echo.RTT.Microseconds()) / 1000
		h.TTL = echo.TTL
		h.OSHint = probe.OSHintFromTTL(echo.TTL)
	}
	if arpMAC != nil {
		h.Evidence = append(h.Evidence, "arp")
		mac = arpMAC
	}

	alive := h.Self || echo != nil || arpMAC != nil || v6
	var open []int
	if e.cfg.Probes.Ports {
		ports := e.cfg.Ports
		if !alive {
			ports = intersect(discoveryPorts, e.cfg.Ports)
		}
		res := e.scanPorts(ctx, addr, ports)
		open = res.Open
		if len(res.Open) > 0 {
			h.Evidence = append(h.Evidence, "tcp-open")
		} else if res.Refused {
			h.Evidence = append(h.Evidence, "tcp-reset")
		}
		if !alive && (len(res.Open) > 0 || res.Refused) {
			alive = true
			rest := subtract(e.cfg.Ports, ports)
			more := e.scanPorts(ctx, addr, rest)
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
			if !e.pace(ctx) {
				return
			}
			c, cancel := context.WithTimeout(ctx, e.cfg.ProbeTimeout)
			defer cancel()
			// A link-local address has no DNS name, so it is not asked about.
			if v6 && addr.IsLinkLocalUnicast() {
				return
			}
			if names, err := e.cfg.Resolver.LookupAddr(c, addr.WithZone("").String()); err == nil && len(names) > 0 {
				mu.Lock()
				h.DNSName = strings.TrimSuffix(names[0], ".")
				mu.Unlock()
			}
		})
	}
	if e.cfg.Probes.NetBIOS && !v6 {
		run(func() {
			if !e.pace(ctx) {
				return
			}
			if nb, err := probe.NetBIOSStatus(ctx, addr, e.cfg.ProbeTimeout); err == nil {
				mu.Lock()
				h.NetBIOS = nb
				mu.Unlock()
			}
		})
	}
	if e.cfg.Probes.MDNS {
		run(func() {
			if !e.pace(ctx) {
				return
			}
			if md, err := probe.MDNSQuery(ctx, addr, e.cfg.ProbeTimeout); err == nil {
				mu.Lock()
				h.MDNS = md
				mu.Unlock()
			}
		})
	}
	if e.cfg.Probes.LLMNR && !v6 && e.linkLocalScope(addr) {
		run(func() {
			if !e.pace(ctx) {
				return
			}
			e.ranLLMNR.Store(true)
			if info, err := e.hk.llmnr(ctx, addr, e.cfg.ProbeTimeout); err == nil {
				mu.Lock()
				h.LLMNR = info
				mu.Unlock()
			}
		})
	}
	if e.cfg.Probes.SMB && containsPort(open, 445) {
		run(func() {
			if !e.pace(ctx) {
				return
			}
			e.ranSMB.Store(true)
			if info, err := e.hk.smb(ctx, addr, e.cfg.ProbeTimeout); err == nil {
				mu.Lock()
				h.SMB = info
				mu.Unlock()
			}
		})
	}
	if e.cfg.Probes.WSD {
		run(func() {
			select {
			case <-e.wsdDone:
			case <-ctx.Done():
				return
			}
			if info, ok := e.wsd[addr]; ok {
				cp := *info
				mu.Lock()
				h.WSD = &cp
				mu.Unlock()
			}
		})
	}
	if e.cfg.Probes.SNMP && !v6 {
		run(func() {
			if !e.pace(ctx) {
				return
			}
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
		run(func() {
			if !e.pace(ctx) {
				services[i] = probe.Service{Port: port, Name: nameOf(port)}
				return
			}
			services[i] = probe.Grab(ctx, addr, port, e.cfg.ProbeTimeout)
		})
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
	if e.cfg.Probes.Favicon {
		e.fingerprintFavicons(ctx, addr, services)
	}
	h.Services = services

	if mac == nil && h.NetBIOS != nil && h.NetBIOS.MAC != "" {
		// NetBIOS carries the MAC even across routers (Windows only).
		if m, err := net.ParseMAC(h.NetBIOS.MAC); err == nil {
			mac = m
			h.Evidence = append(h.Evidence, "netbios-mac")
		}
	}
	if !v6 && mac != nil {
		// A device's IPv6 addresses are the neighbor-table entries with its MAC.
		h.IPv6 = e.linkIPv6(mac)
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

// maxFaviconsPerHost bounds the icons fetched from one host.
const maxFaviconsPerHost = 4

// fingerprintFavicons makes one GET of /favicon.ico on each web service that the
// banner probe already identified as HTTP or HTTPS (at most four per host, lowest
// ports first), over TLS where the service answered a TLS handshake.
func (e *engine) fingerprintFavicons(ctx context.Context, addr netip.Addr, services []probe.Service) {
	var wg sync.WaitGroup
	n := 0
	for i := range services {
		s := &services[i]
		if s.HTTP == nil || n >= maxFaviconsPerHost {
			continue
		}
		n++
		wg.Add(1)
		go func() {
			defer wg.Done()
			if !e.pace(ctx) {
				return
			}
			e.ranFavicon.Store(true)
			if fav, err := e.hk.favicon(ctx, addr, s.Port, s.TLS != nil, e.cfg.ProbeTimeout); err == nil {
				s.Favicon = fav // each goroutine owns one element
			}
		}()
	}
	wg.Wait()
}

func containsPort(ports []int, p int) bool {
	for _, x := range ports {
		if x == p {
			return true
		}
	}
	return false
}

// pace waits for the pacer's next slot; it is a no-op without one. It
// returns false when the scan was canceled while waiting.
func (e *engine) pace(ctx context.Context) bool {
	if e.cfg.Pacer == nil {
		return true
	}
	return e.cfg.Pacer.Wait(ctx) == nil
}

// scanPorts is probe.ScanPorts for an unpaced scan. With a pacer, each
// connect waits for its slot, still bounded by PortConcurrency.
func (e *engine) scanPorts(ctx context.Context, addr netip.Addr, ports []int) probe.PortResult {
	dial := e.hk.ports
	if dial == nil {
		dial = probe.ScanPorts
	}
	if e.cfg.Pacer == nil {
		return dial(ctx, addr, ports, e.cfg.ConnectTimeout, e.cfg.PortConcurrency)
	}
	return scanPortsPaced(ctx, ports, e.cfg.PortConcurrency, e.cfg.Pacer, func(port int) probe.PortResult {
		return dial(ctx, addr, []int{port}, e.cfg.ConnectTimeout, 1)
	})
}

func scanPortsPaced(ctx context.Context, ports []int, parallel int, pacer Pacer, one func(port int) probe.PortResult) probe.PortResult {
	var (
		mu  sync.Mutex
		res probe.PortResult
		wg  sync.WaitGroup
		sem = make(chan struct{}, parallel)
	)
	for _, port := range ports {
		select {
		case <-ctx.Done():
		case sem <- struct{}{}:
		}
		if ctx.Err() != nil {
			break
		}
		wg.Add(1)
		go func(port int) {
			defer func() { <-sem; wg.Done() }()
			if pacer.Wait(ctx) != nil {
				return
			}
			r := one(port)
			mu.Lock()
			defer mu.Unlock()
			res.Open = append(res.Open, r.Open...)
			res.Refused = res.Refused || r.Refused
		}(port)
	}
	wg.Wait()
	sort.Ints(res.Open)
	return res
}

func bestName(h *Host) string {
	switch {
	case h.DNSName != "":
		return h.DNSName
	case h.NetBIOS != nil && h.NetBIOS.Computer != "":
		return h.NetBIOS.Computer
	case h.SNMP != nil && h.SNMP.SysName != "":
		return h.SNMP.SysName
	case h.LLMNR != nil && h.LLMNR.Primary() != "":
		return h.LLMNR.Primary()
	case h.MDNS != nil && h.MDNS.Hostname != "":
		return h.MDNS.Hostname
	case h.UPnP != nil && h.UPnP.FriendlyName != "":
		return h.UPnP.FriendlyName
	case h.WSD != nil && h.WSD.Name != "":
		return h.WSD.Name
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
	if h.WSD != nil {
		ev.WSDTypes = h.WSD.TypeNames()
		ev.WSDONVIF = h.WSD.HasONVIFScope()
	}
	if h.SMB != nil {
		ev.SMBDialect = h.SMB.Dialect
		ev.SMBLegacyWindows = h.SMB.LegacyWindowsDialect()
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
