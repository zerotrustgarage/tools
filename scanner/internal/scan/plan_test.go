package scan

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zerotrustgarage/tools/scanner/internal/probe"
)

func addrs(n int) []netip.Addr {
	var out []netip.Addr
	for i := 1; i <= n; i++ {
		out = append(out, netip.AddrFrom4([4]byte{192, 0, 2, byte(i)}))
	}
	return out
}

func TestDefaultPlanIsUnchanged(t *testing.T) {
	targets := addrs(3)
	for _, mode := range []string{"", ModeDefault} {
		p, err := BuildPlan(PlanRequest{Mode: mode, Targets: targets, Probes: DefaultProbes, Concurrency: 128, ConnectTimeout: 800 * time.Millisecond})
		if err != nil {
			t.Fatal(err)
		}
		// This is the literal the CLI built before modes existed.
		want := Config{Targets: targets, Probes: DefaultProbes, Concurrency: 128, ConnectTimeout: 800 * time.Millisecond}
		if got := p.Config(); !reflect.DeepEqual(got, want) {
			t.Fatalf("mode %q: config %+v, want %+v", mode, got, want)
		}
		d := withDefaults(p.Config())
		if d.PingAttempts != 2 || d.PortConcurrency != 16 || d.Pacer != nil || d.Mode != "" || !reflect.DeepEqual(d.Ports, DefaultPorts) {
			t.Fatalf("default engine settings changed: %+v", d)
		}
	}
	// Custom ports and SNMP pass through untouched.
	cred := &probe.SNMPCredentials{Username: "u"}
	pr := Probes{Ports: true, SNMP: true}
	p, _ := BuildPlan(PlanRequest{Targets: targets, Ports: []int{1, 2, 3000, 4000, 5000, 6000, 7000, 8000, 9000, 9001, 9002, 9003, 9004, 9005, 9006, 9007, 9008}, Probes: pr, SNMP: cred})
	if len(p.Ports) != 17 || p.SNMP != cred || !p.Probes.SNMP {
		t.Fatalf("default plan altered the request: %+v", p)
	}
}

func TestGentlePlanSettings(t *testing.T) {
	targets := addrs(3)
	p, err := BuildPlan(PlanRequest{Mode: ModeGentle, Targets: targets, Probes: DefaultProbes})
	if err != nil {
		t.Fatal(err)
	}
	if p.Mode != ModeGentle || p.Concurrency != 4 || p.PortConcurrency != 2 || p.PingAttempts != 1 ||
		p.ConnectTimeout != 1500*time.Millisecond || p.ProbeInterval != 100*time.Millisecond {
		t.Fatalf("plan %+v", p)
	}
	if fmt.Sprint(p.Ports) != "[22 80 443 445 515 631 8080 9100]" {
		t.Fatalf("ports %v", p.Ports)
	}
	if p.Probes != (Probes{Ports: true, ReverseDNS: true}) {
		t.Fatalf("probes %+v", p.Probes)
	}
	if len(p.Ports) >= len(DefaultPorts) || p.Concurrency >= 128 {
		t.Fatal("gentle must be strictly lighter than default")
	}
	c := p.Config()
	if c.Pacer == nil || c.Mode != ModeGentle || c.Concurrency != 4 {
		t.Fatalf("config %+v", c)
	}
	info := GentleInfo()
	if info.ProbesPerSecond != 10 || info.Concurrency != 4 || !strings.Contains(info.Tradeoff, "does not remove it") {
		t.Fatalf("info %+v", info)
	}
	if strings.Contains(strings.ToLower(info.Tradeoff+info.Summary), "harmless") || strings.Contains(strings.ToLower(info.Tradeoff), "safe for") {
		t.Fatal("gentle text must not claim harmlessness")
	}
}

func TestGentleMayOnlyAskForLess(t *testing.T) {
	base := PlanRequest{Mode: ModeGentle, Targets: addrs(1), Probes: DefaultProbes}
	p, err := BuildPlan(func() PlanRequest {
		r := base
		r.Concurrency = 2
		r.Ports = []int{80}
		r.ConnectTimeout = time.Second
		return r
	}())
	if err != nil || p.Concurrency != 2 || fmt.Sprint(p.Ports) != "[80]" || p.ConnectTimeout != time.Second {
		t.Fatalf("lowering: %+v %v", p, err)
	}
	// Probes can be turned off, never on.
	off := base
	off.Probes = Probes{Ports: true}
	if p, _ := BuildPlan(off); p.Probes != (Probes{Ports: true}) {
		t.Fatalf("probes %+v", p.Probes)
	}
	on := base
	on.Probes = Probes{Ports: true, Banners: true, NetBIOS: true, MDNS: true, SSDP: true, ReverseDNS: true}
	if p, _ := BuildPlan(on); p.Probes != GentleProbes {
		t.Fatalf("gentle enabled extra probes: %+v", p.Probes)
	}
}

func TestMalformedSettingsFailClearly(t *testing.T) {
	long := make([]int, GentleMaxPorts+1)
	for i := range long {
		long[i] = i + 1
	}
	cases := []struct {
		name string
		req  PlanRequest
		want string
	}{
		{"unknown mode", PlanRequest{Mode: "stealth"}, `unknown scan mode "stealth"`},
		{"mode case", PlanRequest{Mode: "Gentle"}, "unknown scan mode"},
		{"too many hosts", PlanRequest{Mode: ModeGentle, Concurrency: 128}, "at most 4 hosts in parallel"},
		{"negative hosts", PlanRequest{Mode: ModeGentle, Concurrency: -1}, "must not be negative"},
		{"negative timeout", PlanRequest{Mode: ModeGentle, ConnectTimeout: -time.Second}, "must not be negative"},
		{"too many ports", PlanRequest{Mode: ModeGentle, Ports: long}, "at most 16 ports"},
		{"port outside gentle list", PlanRequest{Mode: ModeGentle, Ports: []int{80, 23}}, "port 23 is not one of them"},
		{"empty ports", PlanRequest{Mode: ModeGentle, Ports: []int{}}, "no ports"},
		{"snmp flag", PlanRequest{Mode: ModeGentle, Probes: Probes{SNMP: true}}, "does not use SNMP"},
		{"snmp creds", PlanRequest{Mode: ModeGentle, SNMP: &probe.SNMPCredentials{}}, "does not use SNMP"},
	}
	for _, c := range cases {
		_, err := BuildPlan(c.req)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: error %v, want %q", c.name, err, c.want)
		}
	}
}

// fakeClock drives a RateLimiter without real time: sleeping records the
// requested delay and does not block.
type fakeClock struct {
	mu     sync.Mutex
	now    time.Time
	sleeps []time.Duration
}

func (f *fakeClock) Now() time.Time { f.mu.Lock(); defer f.mu.Unlock(); return f.now }
func (f *fakeClock) Sleep(ctx context.Context, d time.Duration) error {
	f.mu.Lock()
	f.sleeps = append(f.sleeps, d)
	f.mu.Unlock()
	return ctx.Err()
}

func TestRateLimiterSpacesProbesAtTheCap(t *testing.T) {
	clk := &fakeClock{now: time.Unix(1000, 0)}
	rl := NewRateLimiterClock(GentleProbeInterval, clk.Now, clk.Sleep)
	const n = 50
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := rl.Wait(context.Background()); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	// With the clock frozen, the k-th probe start must be scheduled k*interval
	// after the first: exactly 10 per second and never a burst.
	sort.Slice(clk.sleeps, func(i, j int) bool { return clk.sleeps[i] < clk.sleeps[j] })
	if len(clk.sleeps) != n-1 {
		t.Fatalf("%d sleeps, want %d", len(clk.sleeps), n-1)
	}
	for k, d := range clk.sleeps {
		if want := time.Duration(k+1) * GentleProbeInterval; d != want {
			t.Fatalf("slot %d delayed %s, want %s", k+1, d, want)
		}
	}
	// 50 probes need at least 4.9 s at 10 per second.
	if last := clk.sleeps[len(clk.sleeps)-1]; last < 4900*time.Millisecond {
		t.Fatalf("last slot at %s", last)
	}
}

func TestRateLimiterDoesNotBankIdleTime(t *testing.T) {
	clk := &fakeClock{now: time.Unix(1000, 0)}
	rl := NewRateLimiterClock(100*time.Millisecond, clk.Now, clk.Sleep)
	_ = rl.Wait(context.Background())
	clk.mu.Lock()
	clk.now = clk.now.Add(time.Hour)
	clk.mu.Unlock()
	_ = rl.Wait(context.Background())
	_ = rl.Wait(context.Background())
	if len(clk.sleeps) != 1 || clk.sleeps[0] != 100*time.Millisecond {
		t.Fatalf("an idle hour must not allow a burst: sleeps %v", clk.sleeps)
	}
}

func TestRateLimiterCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := NewRateLimiter(time.Hour).Wait(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("pre-canceled: %v", err)
	}
	// A real sleep is interrupted by cancel.
	rl := NewRateLimiter(time.Hour)
	_ = rl.Wait(context.Background())
	ctx2, cancel2 := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- rl.Wait(ctx2) }()
	cancel2()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("got %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancel did not interrupt the wait")
	}
}

type countPacer struct{ n atomic.Int64 }

func (c *countPacer) Wait(ctx context.Context) error { c.n.Add(1); return ctx.Err() }

func TestPacedPortScanCapsConnectionsAndPacesEachOne(t *testing.T) {
	var cur, peak atomic.Int64
	pc := &countPacer{}
	ports := []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}
	res := scanPortsPaced(context.Background(), ports, GentlePortConcurrency, pc, func(port int) probe.PortResult {
		n := cur.Add(1)
		for {
			p := peak.Load()
			if n <= p || peak.CompareAndSwap(p, n) {
				break
			}
		}
		time.Sleep(5 * time.Millisecond)
		cur.Add(-1)
		if port%2 == 0 {
			return probe.PortResult{Open: []int{port}}
		}
		return probe.PortResult{Refused: port == 3}
	})
	if peak.Load() > int64(GentlePortConcurrency) || peak.Load() < 2 {
		t.Fatalf("peak concurrent connections %d, want 2", peak.Load())
	}
	if pc.n.Load() != int64(len(ports)) {
		t.Fatalf("%d paced starts for %d ports", pc.n.Load(), len(ports))
	}
	if fmt.Sprint(res.Open) != "[2 4 6 8 10]" || !res.Refused {
		t.Fatalf("result %+v", res)
	}
}

func TestPacedPortScanStopsOnCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	var calls atomic.Int64
	pacer := pacerFunc(func(ctx context.Context) error { cancel(); return ctx.Err() })
	scanPortsPaced(ctx, []int{1, 2, 3, 4, 5, 6}, 2, pacer, func(int) probe.PortResult { calls.Add(1); return probe.PortResult{} })
	if calls.Load() != 0 {
		t.Fatalf("%d connects after cancel", calls.Load())
	}
}

type pacerFunc func(context.Context) error

func (f pacerFunc) Wait(ctx context.Context) error { return f(ctx) }

// fakePinger counts echo requests without touching the network.
type fakePinger struct{ n atomic.Int64 }

func (f *fakePinger) Ping(context.Context, netip.Addr, time.Duration) (probe.EchoReply, error) {
	f.n.Add(1)
	return probe.EchoReply{}, probe.ErrNoReply
}
func (f *fakePinger) Method() string { return "fake" }
func (f *fakePinger) Close() error   { return nil }

func TestEngineGentleReducesProbes(t *testing.T) {
	// A host that ignores ping and ARP: only the port probes run.
	run := func(mode string) (pings, paced int64) {
		req := PlanRequest{Mode: mode, Targets: []netip.Addr{netip.MustParseAddr("192.0.2.9")}, Probes: Probes{Ports: true}, Concurrency: 1, ConnectTimeout: time.Millisecond}
		if mode == ModeGentle {
			req.ConnectTimeout = 0
		}
		p, err := BuildPlan(req)
		if err != nil {
			t.Fatal(err)
		}
		cfg := p.Config()
		pc := &countPacer{}
		cfg.Pacer = pc
		if mode != ModeGentle {
			cfg.Pacer = nil
		}
		cfg.Ports = []int{} // keep the connect phase out of this test
		fp := &fakePinger{}
		e := &engine{cfg: withDefaults(cfg), pinger: fp, arp: noNeighbors{}, obs: nil}
		e.cfg.Probes.Ports = false
		e.scanHost(context.Background(), req.Targets[0])
		return fp.n.Load(), pc.n.Load()
	}
	if pings, paced := run(ModeDefault); pings != 2 || paced != 0 {
		t.Fatalf("default: %d pings, %d paced", pings, paced)
	}
	if pings, paced := run(ModeGentle); pings != 1 || paced != 1 {
		t.Fatalf("gentle: %d pings, %d paced", pings, paced)
	}
}

type noNeighbors struct{}

func (noNeighbors) Resolve(context.Context, netip.Addr, time.Duration) (net.HardwareAddr, bool) {
	return nil, false
}
func (noNeighbors) Method() string { return "none" }

func TestRunGentleOnLoopbackPacesAndSkipsBanners(t *testing.T) {
	web := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "<title>Lab Device</title>")
	}))
	defer web.Close()
	_, ps, _ := net.SplitHostPort(web.Listener.Addr().String())
	port, _ := strconv.Atoi(ps)

	p, err := BuildPlan(PlanRequest{Mode: ModeGentle, Targets: []netip.Addr{netip.MustParseAddr("127.0.0.1")}, Probes: Probes{Ports: true, Banners: true, NetBIOS: true, MDNS: true, SSDP: true}})
	if err != nil {
		t.Fatal(err)
	}
	cfg := p.Config()
	cfg.Ports = []int{port, 1} // loopback test ports; BuildPlan only accepts the gentle list
	pc := &countPacer{}
	cfg.Pacer = pc // fake pacer: no real sleeping
	rec := &recorder{}
	sum := Run(context.Background(), cfg, rec)
	if sum.Mode != ModeGentle || sum.Up != 1 || len(rec.hosts) != 1 {
		t.Fatalf("summary %+v", sum)
	}
	h := rec.hosts[0]
	if len(h.Services) != 1 || h.Services[0].Port != port || h.Services[0].HTTP != nil {
		t.Fatalf("gentle must list the open port without a banner grab: %+v", h.Services)
	}
	if h.NetBIOS != nil || h.MDNS != nil || h.UPnP != nil {
		t.Fatalf("name or discovery probes ran in gentle mode: %+v", h)
	}
	if pc.n.Load() < 2 {
		t.Fatalf("only %d paced probe starts for 2 ports", pc.n.Load())
	}
}

func TestRunGentleCancelStopsPromptly(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	p, _ := BuildPlan(PlanRequest{Mode: ModeGentle, Targets: addrs(100), Probes: DefaultProbes})
	cfg := p.Config()
	cfg.Pacer = pacerFunc(func(ctx context.Context) error { cancel(); return ctx.Err() })
	rec := &recorder{}
	start := time.Now()
	sum := Run(ctx, cfg, rec)
	if !sum.Canceled || time.Since(start) > 10*time.Second {
		t.Fatalf("summary %+v after %s", sum, time.Since(start))
	}
	for _, h := range rec.hosts {
		if h.Status != "up" {
			t.Fatalf("unprobed host reported after cancel: %+v", h)
		}
	}
}
