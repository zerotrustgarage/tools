package scan

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zerotrustgarage/tools/scanner/internal/probe"
)

// The IPv6 tests give the engine a fake interface list, a fake ARP resolver, a
// fake neighbor table and fake port results. Nothing is sent to any network, and
// no test needs ICMP: liveness is the fake ARP answer or the neighbor entry, so
// they pass on a runner that refuses unprivileged pings.

type fakeTable6 struct {
	rows []probe.Neighbor6
	err  error
}

func (f fakeTable6) Read() ([]probe.Neighbor6, error) { return f.rows, f.err }
func (fakeTable6) Method() string                     { return "ndp-fake" }

type fakeARP map[netip.Addr]string

func (f fakeARP) Resolve(_ context.Context, a netip.Addr, _ time.Duration) (net.HardwareAddr, bool) {
	if m, ok := f[a]; ok {
		hw, _ := net.ParseMAC(m)
		return hw, true
	}
	return nil, false
}
func (fakeARP) Method() string { return "arp-fake" }

func nb(addr, mac string, ifIndex int) probe.Neighbor6 {
	hw, err := net.ParseMAC(mac)
	if err != nil {
		panic(err)
	}
	return probe.Neighbor6{Addr: netip.MustParseAddr(addr), MAC: hw, IfIndex: ifIndex, State: "stale"}
}

const (
	macV4  = "00:11:22:33:44:55" // the IPv4 host (127.0.0.1 in these tests)
	macV6A = "02:aa:bb:cc:dd:01" // IPv6-only device
	macV6B = "02:aa:bb:cc:dd:02"
	macOwn = "02:00:00:00:00:99" // this computer
)

type v6fakes struct {
	hooks     hooks
	pings     atomic.Int32
	v6LLMNR   atomic.Int32
	pinged    []int
	mu        sync.Mutex
	portCalls []netip.Addr
}

func newV6Fakes(rows []probe.Neighbor6, extraLocals ...localIface) *v6fakes {
	f := &v6fakes{}
	locals := append([]localIface{{
		addr: netip.MustParseAddr("192.0.2.1"), prefix: netip.MustParsePrefix("127.0.0.0/8"),
		mac: mustMAC(macOwn), ifIndex: 2, ifName: "eth0",
	}}, extraLocals...)
	f.hooks = hooks{
		locals: func() []localIface { return locals },
		pinger: func() (probe.Pinger, error) { return nil, probe.ErrUnavailable }, // a runner with no ICMP
		arp:    func() probe.Neighbors { return fakeARP{netip.MustParseAddr("127.0.0.1"): macV4} },
		table6: func() (probe.NeighborTable6, error) { return fakeTable6{rows: rows}, nil },
		settle: time.Nanosecond,
		wsd:    func(context.Context, time.Duration) (map[netip.Addr]*probe.WSDInfo, error) { return nil, nil },
		llmnr: func(context.Context, netip.Addr, time.Duration) (*probe.LLMNRInfo, error) {
			f.pings.Add(100)
			return nil, errors.New("no")
		},
		smb:     func(context.Context, netip.Addr, time.Duration) (*probe.SMBInfo, error) { return nil, errors.New("no") },
		favicon: probe.FetchFavicon,
		ports: func(_ context.Context, a netip.Addr, ports []int, _ time.Duration, _ int) probe.PortResult {
			f.mu.Lock()
			f.portCalls = append(f.portCalls, a)
			f.mu.Unlock()
			if a.Is6() {
				return probe.PortResult{Open: []int{80}}
			}
			return probe.PortResult{}
		},
		ping6: func(_ context.Context, idx int, _ string, _ time.Duration) error {
			f.pings.Add(1)
			f.mu.Lock()
			f.pinged = append(f.pinged, idx)
			f.mu.Unlock()
			return nil
		},
	}
	return f
}

func mustMAC(s string) net.HardwareAddr {
	m, err := net.ParseMAC(s)
	if err != nil {
		panic(err)
	}
	return m
}

func v6Config(p Probes) Config {
	return Config{Targets: []netip.Addr{netip.MustParseAddr("127.0.0.1")}, Probes: p, Ports: []int{80}, ConnectTimeout: 100 * time.Millisecond, PingTimeout: 100 * time.Millisecond, ProbeTimeout: 200 * time.Millisecond}
}

func byIP(hosts []Host) map[string]Host {
	m := map[string]Host{}
	for _, h := range hosts {
		m[h.IP] = h
	}
	return m
}

func TestIPv6LinksToTheIPv4DeviceByMAC(t *testing.T) {
	f := newV6Fakes([]probe.Neighbor6{
		nb("fe80::1", macV4, 2),
		nb("fd00::1", macV4, 2),
		nb("2001:db8::5", macV4, 2), // a global address outside every own prefix: public
	})
	rec := &recorder{}
	sum := run(context.Background(), v6Config(Probes{Ports: true, IPv6: true}), rec, f.hooks)
	if len(rec.hosts) != 1 {
		t.Fatalf("hosts %+v", rec.hosts)
	}
	h := rec.hosts[0]
	if h.IP != "127.0.0.1" || h.Status != "up" || h.MAC != macV4 {
		t.Fatalf("host %+v", h)
	}
	if strings.Join(h.IPv6, " ") != "fe80::1%eth0 fd00::1" {
		t.Fatalf("IPv6 %v (link-local first with its zone, unique local next, public never)", h.IPv6)
	}
	if sum.IPv6Only != 0 || sum.Total != 1 {
		t.Fatalf("summary %+v", sum)
	}
	if !strings.Contains(strings.Join(sum.Warnings, " "), "1 IPv6 neighbor address") {
		t.Fatalf("a skipped public address must be reported: %v", sum.Warnings)
	}
	if !strings.Contains(strings.Join(sum.Methods, " "), "ndp-fake") {
		t.Fatalf("methods %v", sum.Methods)
	}
}

func TestIPv6OnlyDeviceIsItsOwnEntry(t *testing.T) {
	f := newV6Fakes([]probe.Neighbor6{
		nb("fd00::2a", macV6A, 2),
		nb("fe80::2a", macV6A, 2),
		nb("fe80::1", macV4, 2),
	})
	rec := &recorder{}
	sum := run(context.Background(), v6Config(Probes{Ports: true, IPv6: true, LLMNR: true, NetBIOS: true, SNMP: true}), rec, f.hooks)
	hosts := byIP(rec.hosts)
	if len(hosts) != 2 {
		t.Fatalf("hosts %+v", rec.hosts)
	}
	h, ok := hosts["fe80::2a%eth0"]
	if !ok {
		t.Fatalf("the IPv6-only device must be keyed by its link-local address: %v", hosts)
	}
	if h.Status != "up" || h.MAC != macV6A || strings.Join(h.IPv6, " ") != "fe80::2a%eth0 fd00::2a" {
		t.Fatalf("v6 host %+v", h)
	}
	if strings.Join(h.Evidence, ",") != "ndp,tcp-open" {
		t.Fatalf("evidence %v: the neighbor table is the liveness proof here, there is no ICMP", h.Evidence)
	}
	if len(h.Services) != 1 || h.Services[0].Port != 80 {
		t.Fatalf("services %+v", h.Services)
	}
	if sum.IPv6Only != 1 || sum.Total != 1 || sum.Up != 2 {
		t.Fatalf("summary %+v", sum)
	}
	if hosts["127.0.0.1"].IPv6[0] != "fe80::1%eth0" {
		t.Fatalf("the IPv4 device keeps its own neighbor: %+v", hosts["127.0.0.1"])
	}
	// LLMNR and NetBIOS are IPv4-only exchanges; the host was on-link and the
	// probes were on, but the IPv6 entry must not run them.
	if f.v6LLMNR.Load() != 0 {
		t.Fatal("LLMNR ran against an IPv6 device")
	}
}

func TestIPv6PortProbesOnlyForAddressesFoundThisWay(t *testing.T) {
	f := newV6Fakes([]probe.Neighbor6{nb("fe80::2a", macV6A, 2), nb("fd00::2a", macV6A, 2), nb("fe80::3", macV6B, 2)})
	rec := &recorder{}
	run(context.Background(), v6Config(Probes{Ports: true, IPv6: true}), rec, f.hooks)
	var v6 []string
	for _, a := range f.portCalls {
		if a.Is6() {
			v6 = append(v6, a.String())
		}
	}
	// One scan per device, through its primary (link-local) address, never the
	// other addresses of the same device and never an address nobody found.
	if strings.Join(sortedStrings(v6), " ") != "fe80::2a%eth0 fe80::3%eth0" {
		t.Fatalf("IPv6 port probes went to %v", v6)
	}
}

func sortedStrings(s []string) []string {
	out := append([]string(nil), s...)
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j] < out[j-1]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

func TestIPv6ScopeGuardAndOwnPrefixes(t *testing.T) {
	own := localIface{addr: netip.MustParseAddr("2001:db8:1::10"), prefix: netip.MustParsePrefix("2001:db8:1::/64"), ifIndex: 2, ifName: "eth0"}
	rows := []probe.Neighbor6{
		nb("2001:db8:1::77", macV6A, 2), // inside the scanner's own on-link prefix: internal
		nb("2001:db8:2::1", macV6B, 2),  // any other global address: public
	}
	f := newV6Fakes(rows, own)
	rec := &recorder{}
	sum := run(context.Background(), v6Config(Probes{Ports: true, IPv6: true}), rec, f.hooks)
	hosts := byIP(rec.hosts)
	if _, ok := hosts["2001:db8:1::77"]; !ok || len(hosts) != 2 {
		t.Fatalf("hosts %v", hosts)
	}
	if _, ok := hosts["2001:db8:2::1"]; ok {
		t.Fatal("a public IPv6 address was probed without authorization")
	}
	for _, a := range f.portCalls {
		if a.String() == "2001:db8:2::1" {
			t.Fatal("port probe sent to a public IPv6 address")
		}
	}
	if !strings.Contains(strings.Join(sum.Warnings, " "), "--allow-public") {
		t.Fatalf("warnings %v", sum.Warnings)
	}

	// With the technician's authorization the same address is used.
	f = newV6Fakes(rows, own)
	rec = &recorder{}
	cfg := v6Config(Probes{Ports: true, IPv6: true})
	cfg.AllowPublic = true
	run(context.Background(), cfg, rec, f.hooks)
	if _, ok := byIP(rec.hosts)["2001:db8:2::1"]; !ok {
		t.Fatalf("authorized public neighbor missing: %+v", rec.hosts)
	}
}

func TestIPv6OnlyUsesLinksTheScanIsAbout(t *testing.T) {
	f := newV6Fakes([]probe.Neighbor6{
		nb("fe80::2a", macV6A, 2),
		nb("fe80::3", macV6B, 7),              // an interface no target is on
		nb("fe80::4", macOwn, 2),              // this computer's own MAC
		nb("fe80::5", "01:00:5e:00:00:01", 2), // a multicast MAC is not a device
		nb("ff02::1", macV6B, 2),              // a multicast address is not a device
	})
	rec := &recorder{}
	run(context.Background(), v6Config(Probes{Ports: true, IPv6: true}), rec, f.hooks)
	hosts := byIP(rec.hosts)
	if len(hosts) != 2 || hosts["fe80::2a%eth0"].IP == "" {
		t.Fatalf("hosts %v", hosts)
	}
}

func TestIPv6OffWhenNoTargetIsOnLink(t *testing.T) {
	f := newV6Fakes([]probe.Neighbor6{nb("fe80::2a", macV6A, 2)})
	f.hooks.locals = func() []localIface { return nil } // 127.0.0.1 is on no link
	rec := &recorder{}
	sum := run(context.Background(), v6Config(Probes{Ports: true, IPv6: true, IPv6Ping: true}), rec, f.hooks)
	if f.pings.Load() != 0 || sum.IPv6Only != 0 {
		t.Fatalf("IPv6 discovery ran with no on-link target: pings %d summary %+v", f.pings.Load(), sum)
	}
}

func TestIPv6SwitchOffReadsNothing(t *testing.T) {
	var opened atomic.Int32
	f := newV6Fakes([]probe.Neighbor6{nb("fe80::2a", macV6A, 2)})
	f.hooks.table6 = func() (probe.NeighborTable6, error) { opened.Add(1); return fakeTable6{}, nil }
	rec := &recorder{}
	sum := run(context.Background(), v6Config(Probes{Ports: true}), rec, f.hooks)
	if opened.Load() != 0 || f.pings.Load() != 0 || len(rec.hosts[0].IPv6) != 0 || sum.IPv6Only != 0 {
		t.Fatal("the neighbor table was used with the IPv6 switch off")
	}
}

func TestIPv6MulticastEchoIsOneAndOptional(t *testing.T) {
	f := newV6Fakes([]probe.Neighbor6{nb("fe80::2a", macV6A, 2)})
	sum := run(context.Background(), v6Config(Probes{Ports: true, IPv6: true, IPv6Ping: true}), &recorder{}, f.hooks)
	if f.pings.Load() != 1 || len(f.pinged) != 1 || f.pinged[0] != 2 {
		t.Fatalf("multicast echoes: %d to %v, want exactly one on interface 2", f.pings.Load(), f.pinged)
	}
	if !strings.Contains(strings.Join(sum.Methods, " "), "icmp6-multicast") {
		t.Fatalf("methods %v", sum.Methods)
	}

	f = newV6Fakes([]probe.Neighbor6{nb("fe80::2a", macV6A, 2)})
	sum = run(context.Background(), v6Config(Probes{Ports: true, IPv6: true}), &recorder{}, f.hooks)
	if f.pings.Load() != 0 || strings.Contains(strings.Join(sum.Methods, " "), "icmp6-multicast") {
		t.Fatal("the multicast echo was sent with its switch off")
	}

	// A failed send is a warning, and the table is still read.
	f = newV6Fakes([]probe.Neighbor6{nb("fe80::2a", macV6A, 2)})
	f.hooks.ping6 = func(context.Context, int, string, time.Duration) error { return probe.ErrUnavailable }
	rec := &recorder{}
	sum = run(context.Background(), v6Config(Probes{Ports: true, IPv6: true, IPv6Ping: true}), rec, f.hooks)
	if sum.IPv6Only != 1 || !strings.Contains(strings.Join(sum.Warnings, " "), "multicast echo could not be sent") {
		t.Fatalf("summary %+v", sum)
	}
}

func TestIPv6GentleModeNeverSendsTheMulticastEchoAndKeepsItsPace(t *testing.T) {
	f := newV6Fakes([]probe.Neighbor6{nb("fe80::2a", macV6A, 2)})
	pacer := &countingPacer{}
	cfg := v6Config(Probes{Ports: true, IPv6: true, IPv6Ping: true})
	cfg.Mode, cfg.Pacer = ModeGentle, pacer
	rec := &recorder{}
	sum := run(context.Background(), cfg, rec, f.hooks)
	if f.pings.Load() != 0 {
		t.Fatal("gentle mode sent the IPv6 multicast echo")
	}
	if sum.IPv6Only != 1 || sum.Mode != ModeGentle {
		t.Fatalf("summary %+v", sum)
	}
	if pacer.n.Load() == 0 {
		t.Fatal("IPv6 probes did not wait for the gentle pacer")
	}

	// Through the plan, as both front ends build it: the switch survives, the
	// multicast echo does not.
	p, err := BuildPlan(PlanRequest{Mode: ModeGentle, Targets: cfg.Targets, Probes: DefaultProbes})
	if err != nil {
		t.Fatal(err)
	}
	if !p.Probes.IPv6 || p.Probes.IPv6Ping {
		t.Fatalf("gentle plan probes %+v", p.Probes)
	}
}

type countingPacer struct{ n atomic.Int32 }

func (c *countingPacer) Wait(context.Context) error { c.n.Add(1); return nil }

func TestIPv6TableErrorsAreWarningsNotFailures(t *testing.T) {
	f := newV6Fakes(nil)
	f.hooks.table6 = func() (probe.NeighborTable6, error) { return nil, probe.ErrUnavailable }
	rec := &recorder{}
	sum := run(context.Background(), v6Config(Probes{Ports: true, IPv6: true}), rec, f.hooks)
	if len(rec.hosts) != 1 || rec.hosts[0].Status != "up" || !strings.Contains(strings.Join(sum.Warnings, " "), "IPv4 only") {
		t.Fatalf("hosts %+v warnings %v", rec.hosts, sum.Warnings)
	}
	f = newV6Fakes(nil)
	f.hooks.table6 = func() (probe.NeighborTable6, error) { return fakeTable6{err: errors.New("denied")}, nil }
	sum = run(context.Background(), v6Config(Probes{Ports: true, IPv6: true}), &recorder{}, f.hooks)
	if !strings.Contains(strings.Join(sum.Warnings, " "), "could not be read") {
		t.Fatalf("warnings %v", sum.Warnings)
	}
}

func TestIPv6DevicesPerScanAreBounded(t *testing.T) {
	var rows []probe.Neighbor6
	for i := 0; i < maxIPv6Devices+30; i++ {
		rows = append(rows, nb(fmt.Sprintf("fe80::%x", i+1), fmt.Sprintf("06:00:00:00:%02x:%02x", i>>8, i&0xff), 2))
	}
	f := newV6Fakes(rows)
	rec := &recorder{}
	sum := run(context.Background(), v6Config(Probes{IPv6: true}), rec, f.hooks)
	if sum.IPv6Only != maxIPv6Devices || len(rec.hosts) != maxIPv6Devices+1 {
		t.Fatalf("IPv6-only %d, hosts %d", sum.IPv6Only, len(rec.hosts))
	}
	if !strings.Contains(strings.Join(sum.Warnings, " "), "30 more IPv6 neighbor") {
		t.Fatalf("warnings %v", sum.Warnings)
	}
}

func TestIPv6AddressesPerDeviceAreBounded(t *testing.T) {
	var rows []probe.Neighbor6
	for i := 0; i < 40; i++ {
		rows = append(rows, nb(fmt.Sprintf("fd00::%x", i+1), macV6A, 2))
	}
	rows = append(rows, nb("fe80::2a", macV6A, 2))
	f := newV6Fakes(rows)
	rec := &recorder{}
	run(context.Background(), v6Config(Probes{IPv6: true}), rec, f.hooks)
	h := byIP(rec.hosts)["fe80::2a%eth0"]
	if len(h.IPv6) != MaxIPv6PerDevice || h.IPv6[0] != "fe80::2a%eth0" {
		t.Fatalf("addresses %v", h.IPv6)
	}
}

func TestIPv6UsesICMPv6WhenThePingerHasIt(t *testing.T) {
	f := newV6Fakes([]probe.Neighbor6{nb("fe80::2a", macV6A, 2)})
	var asked []netip.Addr
	var mu sync.Mutex
	f.hooks.pinger = func() (probe.Pinger, error) {
		return pingFunc(func(a netip.Addr) (probe.EchoReply, error) {
			mu.Lock()
			asked = append(asked, a)
			mu.Unlock()
			return probe.EchoReply{RTT: 2 * time.Millisecond, TTL: 64}, nil
		}), nil
	}
	rec := &recorder{}
	run(context.Background(), v6Config(Probes{IPv6: true}), rec, f.hooks)
	h := byIP(rec.hosts)["fe80::2a%eth0"]
	if strings.Join(h.Evidence, ",") != "ndp,icmp6" || h.TTL != 64 || h.RTTMillis != 2 {
		t.Fatalf("host %+v", h)
	}
	found := false
	for _, a := range asked {
		if a.String() == "fe80::2a%eth0" {
			found = true // the zone must reach the pinger, or a link-local echo has no interface
		}
	}
	if !found {
		t.Fatalf("pinger was asked %v", asked)
	}
}

type pingFunc func(netip.Addr) (probe.EchoReply, error)

func (f pingFunc) Ping(_ context.Context, a netip.Addr, _ time.Duration) (probe.EchoReply, error) {
	return f(a)
}
func (pingFunc) Method() string { return "icmp-fake" }
func (pingFunc) Close() error   { return nil }

func TestIPv6ScanStopsWhenCanceled(t *testing.T) {
	f := newV6Fakes([]probe.Neighbor6{nb("fe80::2a", macV6A, 2)})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	rec := &recorder{}
	sum := run(ctx, v6Config(Probes{IPv6: true, IPv6Ping: true}), rec, f.hooks)
	if !sum.Canceled || sum.IPv6Only != 0 {
		t.Fatalf("summary %+v", sum)
	}
}
