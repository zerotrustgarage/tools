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
	"testing"
	"time"
)

type recorder struct {
	mu    sync.Mutex
	hosts []Host
	last  int
}

func (r *recorder) Host(h Host) { r.mu.Lock(); r.hosts = append(r.hosts, h); r.mu.Unlock() }
func (r *recorder) Progress(done, total int) {
	r.mu.Lock()
	r.last = done
	r.mu.Unlock()
}

func TestRunLoopbackEndToEnd(t *testing.T) {
	web := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Server", "ZTG-Test")
		fmt.Fprint(w, "<title>Synology DiskStation</title>")
	}))
	defer web.Close()
	_, ps, _ := net.SplitHostPort(web.Listener.Addr().String())
	port, _ := strconv.Atoi(ps)

	rec := &recorder{}
	cfg := Config{
		Targets: []netip.Addr{netip.MustParseAddr("127.0.0.1")},
		Ports:   []int{port},
		Probes:  Probes{Ports: true, Banners: true}, // no name queries: keep the test hermetic
	}
	sum := Run(context.Background(), cfg, rec)
	if sum.Total != 1 || sum.Up != 1 || rec.last != 1 || len(rec.hosts) != 1 {
		t.Fatalf("summary %+v, progress %d, hosts %d", sum, rec.last, len(rec.hosts))
	}
	h := rec.hosts[0]
	if h.Status != "up" || !h.Self {
		t.Fatalf("host %+v", h)
	}
	if len(h.Services) != 1 || h.Services[0].HTTP == nil || h.Services[0].HTTP.Title != "Synology DiskStation" {
		t.Fatalf("services %+v", h.Services)
	}
	// Loopback is this machine's own interface, which outranks the evidence.
	if h.Type != "This computer" {
		t.Fatalf("type %q (%s)", h.Type, h.TypeReason)
	}
}

func TestRunReportsDownHosts(t *testing.T) {
	// TEST-NET-1 (RFC 5737) is documentation space with nothing listening;
	// the engine is called directly, so the scope guard does not apply.
	rec := &recorder{}
	cfg := Config{
		Targets:        []netip.Addr{netip.MustParseAddr("192.0.2.123")},
		Ports:          []int{9},
		Probes:         Probes{Ports: true},
		PingTimeout:    200 * time.Millisecond,
		ConnectTimeout: 200 * time.Millisecond,
	}
	sum := Run(context.Background(), cfg, rec)
	if sum.Up != 0 || len(rec.hosts) != 1 || rec.hosts[0].Status != "down" {
		t.Fatalf("summary %+v hosts %+v", sum, rec.hosts)
	}
}

func TestRunCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var targets []netip.Addr
	for i := 1; i < 200; i++ {
		targets = append(targets, netip.AddrFrom4([4]byte{192, 0, 2, byte(i)}))
	}
	rec := &recorder{}
	start := time.Now()
	sum := Run(ctx, Config{Targets: targets, Probes: DefaultProbes}, rec)
	if !sum.Canceled || time.Since(start) > 5*time.Second {
		t.Fatalf("summary %+v after %s", sum, time.Since(start))
	}
	for _, h := range rec.hosts {
		if h.Status != "up" {
			t.Fatalf("unprobed host reported after cancel: %+v", h)
		}
	}
}

func TestPortSetHelpers(t *testing.T) {
	if got := intersect([]int{80, 443, 22}, []int{22, 80}); fmt.Sprint(got) != "[80 22]" {
		t.Fatalf("intersect %v", got)
	}
	if got := subtract([]int{1, 2, 3}, []int{2}); fmt.Sprint(got) != "[1 3]" {
		t.Fatalf("subtract %v", got)
	}
	if got := mergeSorted([]int{80}, []int{22, 443}); fmt.Sprint(got) != "[22 80 443]" {
		t.Fatalf("merge %v", got)
	}
}

func TestParsePorts(t *testing.T) {
	got, err := ParsePorts("80, 443;22,8000-8002,80")
	if err != nil || fmt.Sprint(got) != "[80 443 22 8000 8001 8002]" {
		t.Fatalf("got %v %v", got, err)
	}
	for _, bad := range []string{"", "0", "70000", "90-80", "x", "1-2000"} {
		if _, err := ParsePorts(bad); err == nil {
			t.Errorf("%q: want error", bad)
		}
	}
}

func TestSNMPWithoutCredentialsWarns(t *testing.T) {
	rec := &recorder{}
	sum := Run(context.Background(), Config{
		Targets: []netip.Addr{netip.MustParseAddr("127.0.0.1")},
		Probes:  Probes{SNMP: true},
	}, rec)
	if len(sum.Warnings) == 0 || !strings.Contains(strings.Join(sum.Warnings, " "), "SNMP") {
		t.Fatalf("warnings %v", sum.Warnings)
	}
	for _, m := range sum.Methods {
		if m == "snmpv3-authpriv" {
			t.Fatal("SNMP listed as a method although it was skipped")
		}
	}
}
