package scan

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zerotrustgarage/tools/scanner/internal/probe"
)

// The SC11 probes are link-local, so the engine only runs them for hosts it
// considers on-link. These tests give the engine a fake local interface whose
// prefix covers loopback, which makes 127.0.0.1 "on-link", and replace the
// network exchanges with counting fakes. Nothing leaves the machine.
var fakeLocals = func() []localIface {
	return []localIface{{addr: netip.MustParseAddr("192.0.2.1"), prefix: netip.MustParsePrefix("127.0.0.0/8")}}
}

// loopbackPinger answers every echo, so 127.0.0.1 is alive on runners that
// refuse unprivileged ICMP (the fake interface list makes loopback "on-link"
// rather than this computer, so it needs liveness evidence of its own).
type loopbackPinger struct{}

func (loopbackPinger) Ping(context.Context, netip.Addr, time.Duration) (probe.EchoReply, error) {
	return probe.EchoReply{RTT: time.Millisecond, TTL: 64}, nil
}
func (loopbackPinger) Method() string { return "icmp-fake" }
func (loopbackPinger) Close() error   { return nil }

type sc11Fakes struct {
	wsdCalls, llmnrCalls, smbCalls, faviconCalls atomic.Int32
	hooks                                        hooks
}

func newFakes(t *testing.T) *sc11Fakes {
	f := &sc11Fakes{}
	f.hooks = hooks{
		locals: fakeLocals,
		wsd: func(ctx context.Context, wait time.Duration) (map[netip.Addr]*probe.WSDInfo, error) {
			f.wsdCalls.Add(1)
			return map[netip.Addr]*probe.WSDInfo{
				netip.MustParseAddr("127.0.0.1"): {Types: []string{"wsdp:Device", "pri:PrintDeviceType"}, XAddrs: []string{"http://127.0.0.1:5357/x"}},
				netip.MustParseAddr("8.8.8.8"):   {Types: []string{"dn:NetworkVideoTransmitter"}},
			}, nil
		},
		llmnr: func(ctx context.Context, a netip.Addr, d time.Duration) (*probe.LLMNRInfo, error) {
			f.llmnrCalls.Add(1)
			return &probe.LLMNRInfo{Names: []string{"LAB-PC"}}, nil
		},
		smb: func(ctx context.Context, a netip.Addr, d time.Duration) (*probe.SMBInfo, error) {
			f.smbCalls.Add(1)
			return &probe.SMBInfo{Dialect: "2.1", SigningEnabled: true}, nil
		},
		favicon: probe.FetchFavicon,
		pinger:  func() (probe.Pinger, error) { return loopbackPinger{}, nil },
	}
	return f
}

func webPort(t *testing.T) (int, func()) {
	web := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/favicon.ico" {
			w.Header().Set("Content-Type", "image/x-icon")
			w.Write([]byte{0, 0, 1, 0, 1, 0, 16, 16})
			return
		}
		fmt.Fprint(w, "<title>Lab Device</title>")
	}))
	_, ps, _ := net.SplitHostPort(web.Listener.Addr().String())
	port, _ := strconv.Atoi(ps)
	return port, web.Close
}

func loopbackConfig(ports []int, p Probes) Config {
	return Config{Targets: []netip.Addr{netip.MustParseAddr("127.0.0.1")}, Ports: ports, Probes: p, ConnectTimeout: 300 * time.Millisecond, PingTimeout: 200 * time.Millisecond, ProbeTimeout: time.Second}
}

func TestSC11ProbesFlowIntoTheHostRecord(t *testing.T) {
	port, closeWeb := webPort(t)
	defer closeWeb()
	f := newFakes(t)
	rec := &recorder{}
	cfg := loopbackConfig([]int{port}, Probes{Ports: true, Banners: true, WSD: true, LLMNR: true, SMB: true, Favicon: true})
	sum := run(context.Background(), cfg, rec, f.hooks)
	if len(rec.hosts) != 1 || rec.hosts[0].Status != "up" {
		t.Fatalf("hosts %+v", rec.hosts)
	}
	h := rec.hosts[0]
	if h.WSD == nil || strings.Join(h.WSD.TypeNames(), ",") != "device,printdevicetype" {
		t.Fatalf("WSD %+v", h.WSD)
	}
	if h.LLMNR == nil || h.LLMNR.Primary() != "LAB-PC" || h.Name != "LAB-PC" {
		t.Fatalf("LLMNR %+v name %q", h.LLMNR, h.Name)
	}
	if h.Type != "Printer" || !strings.Contains(h.TypeReason, "WS-Discovery") {
		t.Fatalf("type %q (%s)", h.Type, h.TypeReason)
	}
	if len(h.Services) != 1 || h.Services[0].Favicon == nil || h.Services[0].Favicon.Size != 8 || h.Services[0].Favicon.SHA256 == "" {
		t.Fatalf("services %+v", h.Services)
	}
	if h.SMB != nil || f.smbCalls.Load() != 0 {
		t.Fatal("SMB negotiate ran on a host whose port 445 was not open")
	}
	if f.wsdCalls.Load() != 1 || f.llmnrCalls.Load() != 1 {
		t.Fatalf("WS-Discovery sent %d times, LLMNR asked %d times", f.wsdCalls.Load(), f.llmnrCalls.Load())
	}
	got := strings.Join(sum.Methods, " ")
	for _, m := range []string{"ws-discovery", "llmnr", "http-favicon"} {
		if !strings.Contains(got, m) {
			t.Errorf("method %q missing from %q", m, got)
		}
	}
	if strings.Contains(got, "smb2-negotiate") {
		t.Errorf("SMB2 listed as a method although no host had port 445 open: %q", got)
	}
}

func TestSC11SMBOnlyWhereTCP445WasFoundOpen(t *testing.T) {
	l, err := net.Listen("tcp4", "127.0.0.1:445")
	if err != nil {
		t.Skip("cannot bind 127.0.0.1:445 here:", err)
	}
	defer l.Close()
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			c.Close()
		}
	}()
	f := newFakes(t)
	rec := &recorder{}
	sum := run(context.Background(), loopbackConfig([]int{445}, Probes{Ports: true, SMB: true}), rec, f.hooks)
	if len(rec.hosts) != 1 || rec.hosts[0].SMB == nil || rec.hosts[0].SMB.Dialect != "2.1" || f.smbCalls.Load() != 1 {
		t.Fatalf("calls %d hosts %+v", f.smbCalls.Load(), rec.hosts)
	}
	if !strings.Contains(strings.Join(sum.Methods, " "), "smb2-negotiate") {
		t.Fatalf("methods %v", sum.Methods)
	}
}

func TestSC11NothingRunsWhenTheTogglesAreOff(t *testing.T) {
	port, closeWeb := webPort(t)
	defer closeWeb()
	f := newFakes(t)
	failing := f.hooks
	failing.favicon = func(context.Context, netip.Addr, int, bool, time.Duration) (*probe.FaviconInfo, error) {
		t.Error("favicon fetched with the toggle off")
		return nil, nil
	}
	rec := &recorder{}
	sum := run(context.Background(), loopbackConfig([]int{port}, Probes{Ports: true, Banners: true}), rec, failing)
	h := rec.hosts[0]
	if f.wsdCalls.Load()+f.llmnrCalls.Load()+f.smbCalls.Load() != 0 || h.WSD != nil || h.LLMNR != nil || h.SMB != nil || h.Services[0].Favicon != nil {
		t.Fatalf("a new probe ran with its toggle off: %+v", h)
	}
	if strings.Contains(strings.Join(sum.Methods, " "), "ws-discovery") {
		t.Fatal("method listed for a probe that did not run")
	}
}

func TestSC11FaviconNeedsAnIdentifiedWebService(t *testing.T) {
	port, closeWeb := webPort(t)
	defer closeWeb()
	f := newFakes(t)
	f.hooks.favicon = func(context.Context, netip.Addr, int, bool, time.Duration) (*probe.FaviconInfo, error) {
		t.Error("favicon fetched without a banner-identified web service")
		return nil, nil
	}
	rec := &recorder{}
	sum := run(context.Background(), loopbackConfig([]int{port}, Probes{Ports: true, Favicon: true}), rec, f.hooks)
	if len(rec.hosts[0].Services) != 1 || rec.hosts[0].Services[0].Favicon != nil {
		t.Fatalf("%+v", rec.hosts[0].Services)
	}
	if strings.Contains(strings.Join(sum.Methods, " "), "http-favicon") {
		t.Fatal("favicon listed as a method without banners")
	}
}

func TestSC11FaviconTargetsAreChosenFromIdentifiedWebServices(t *testing.T) {
	type call struct {
		port int
		tls  bool
	}
	var mu sync.Mutex
	var calls []call
	f := newFakes(t)
	f.hooks.favicon = func(_ context.Context, _ netip.Addr, port int, useTLS bool, _ time.Duration) (*probe.FaviconInfo, error) {
		mu.Lock()
		calls = append(calls, call{port, useTLS})
		mu.Unlock()
		return &probe.FaviconInfo{SHA256: strings.Repeat("a", 64), Size: 1}, nil
	}
	e := &engine{hk: f.hooks, cfg: Config{ProbeTimeout: time.Second}}
	web := func(port int, tls bool) probe.Service {
		s := probe.Service{Port: port, Name: "http", HTTP: &probe.HTTPInfo{Status: 200}}
		if tls {
			s.TLS = &probe.TLSInfo{}
		}
		return s
	}
	services := []probe.Service{{Port: 22, Name: "ssh"}, web(80, false), web(443, true), web(8080, false), web(8081, false), web(8082, false), web(8083, false)}
	e.fingerprintFavicons(context.Background(), netip.MustParseAddr("127.0.0.1"), services)
	if len(calls) != maxFaviconsPerHost {
		t.Fatalf("%d fetches, want %d: %+v", len(calls), maxFaviconsPerHost, calls)
	}
	for _, c := range calls {
		if c.tls != (c.port == 443) || c.port == 22 {
			t.Fatalf("bad call %+v", c)
		}
	}
	if services[0].Favicon != nil || services[1].Favicon == nil || services[2].Favicon == nil || services[6].Favicon != nil {
		t.Fatalf("favicons attached to the wrong services: %+v", services)
	}
}

func TestSC11NoOnLinkTargetsMeansNoLinkLocalTraffic(t *testing.T) {
	port, closeWeb := webPort(t)
	defer closeWeb()
	f := newFakes(t)
	f.hooks.locals = func() []localIface { return nil } // no directly connected network
	rec := &recorder{}
	sum := run(context.Background(), loopbackConfig([]int{port}, Probes{Ports: true, Banners: true, WSD: true, LLMNR: true}), rec, f.hooks)
	if f.wsdCalls.Load() != 0 || f.llmnrCalls.Load() != 0 || rec.hosts[0].WSD != nil || rec.hosts[0].LLMNR != nil {
		t.Fatal("WS-Discovery or LLMNR ran with no on-link target")
	}
	if got := strings.Join(sum.Methods, " "); strings.Contains(got, "ws-discovery") || strings.Contains(got, "llmnr") {
		t.Fatalf("methods %q", got)
	}
}

func TestSC11PublicRespondersAreDropped(t *testing.T) {
	// A public address is never on-link and internal at once, so it can neither
	// start the multicast nor have a reply kept. The multicast answer map is
	// filtered by the scope guard's own predicate before any host reads it.
	f := newFakes(t)
	e := &engine{hk: f.hooks, cfg: Config{Targets: []netip.Addr{netip.MustParseAddr("8.8.8.8")}}, locals: fakeLocals()}
	if e.linkLocalScope(netip.MustParseAddr("8.8.8.8")) || e.anyOnLinkInternal() {
		t.Fatal("a public address was treated as link-local scope")
	}
	if !e.linkLocalScope(netip.MustParseAddr("127.0.0.5")) {
		t.Fatal("test setup: the fake interface should cover loopback")
	}
	kept := keepInternal(map[netip.Addr]*probe.WSDInfo{
		netip.MustParseAddr("8.8.8.8"):     {},
		netip.MustParseAddr("192.168.1.5"): {},
		netip.MustParseAddr("169.254.1.1"): {},
		netip.MustParseAddr("100.64.0.9"):  {},
		netip.MustParseAddr("203.0.113.7"): {},
	})
	if len(kept) != 3 || kept[netip.MustParseAddr("8.8.8.8")] != nil || kept[netip.MustParseAddr("203.0.113.7")] != nil {
		t.Fatalf("kept %v", kept)
	}
}

func TestSC11GentleModeDisablesEveryNewProbe(t *testing.T) {
	port, closeWeb := webPort(t)
	defer closeWeb()
	fail := func(name string) { t.Errorf("%s ran in gentle mode", name) }
	hk := hooks{
		locals: fakeLocals,
		wsd: func(context.Context, time.Duration) (map[netip.Addr]*probe.WSDInfo, error) {
			fail("WS-Discovery")
			return nil, nil
		},
		llmnr: func(context.Context, netip.Addr, time.Duration) (*probe.LLMNRInfo, error) {
			fail("LLMNR")
			return nil, nil
		},
		smb: func(context.Context, netip.Addr, time.Duration) (*probe.SMBInfo, error) {
			fail("SMB2 negotiate")
			return nil, nil
		},
		favicon: func(context.Context, netip.Addr, int, bool, time.Duration) (*probe.FaviconInfo, error) {
			fail("favicon")
			return nil, nil
		},
	}
	target := []netip.Addr{netip.MustParseAddr("127.0.0.1")}
	// Through the plan, asking for everything.
	p, err := BuildPlan(PlanRequest{Mode: ModeGentle, Targets: target, Probes: DefaultProbes})
	if err != nil {
		t.Fatal(err)
	}
	if p.Probes.WSD || p.Probes.LLMNR || p.Probes.SMB || p.Probes.Favicon {
		t.Fatalf("gentle plan enables SC11 probes: %+v", p.Probes)
	}
	cfg := p.Config()
	cfg.Ports = []int{port}
	cfg.Pacer = &countPacer{}
	rec := &recorder{}
	sum := run(context.Background(), cfg, rec, hk)
	if len(rec.hosts) != 1 || rec.hosts[0].WSD != nil || rec.hosts[0].LLMNR != nil || rec.hosts[0].SMB != nil {
		t.Fatalf("%+v", rec.hosts)
	}
	for _, m := range sum.Methods {
		if m == "ws-discovery" || m == "llmnr" || m == "smb2-negotiate" || m == "http-favicon" {
			t.Fatalf("gentle summary lists %q", m)
		}
	}
	// A hand-built gentle Config that tries to turn them on is masked by the engine.
	raw := loopbackConfig([]int{port}, DefaultProbes)
	raw.Mode = ModeGentle
	raw.Pacer = &countPacer{}
	run(context.Background(), raw, &recorder{}, hk)
}

func TestSC11DefaultProbesEnableTheNewOnesAndGentleListsStayOff(t *testing.T) {
	if !DefaultProbes.WSD || !DefaultProbes.LLMNR || !DefaultProbes.SMB || !DefaultProbes.Favicon {
		t.Fatalf("defaults %+v", DefaultProbes)
	}
	if GentleProbes.WSD || GentleProbes.LLMNR || GentleProbes.SMB || GentleProbes.Favicon {
		t.Fatalf("gentle %+v", GentleProbes)
	}
	info := GentleInfo()
	if info.Probes.WSD || info.Probes.LLMNR || info.Probes.SMB || info.Probes.Favicon {
		t.Fatalf("gentle info %+v", info.Probes)
	}
	for _, word := range []string{"WS-Discovery", "LLMNR", "SMB", "favicon"} {
		if !strings.Contains(info.Summary, word) || !strings.Contains(info.Tradeoff, word) {
			t.Errorf("gentle text does not mention %s", word)
		}
	}
}
