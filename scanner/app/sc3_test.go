package main

import (
	"context"
	"encoding/json"
	"net/netip"
	"strings"
	"testing"

	"github.com/zerotrustgarage/tools/scanner/internal/probe"
	"github.com/zerotrustgarage/tools/scanner/internal/scan"
)

func svc(port int, tls bool) probe.Service {
	s := probe.Service{Port: port, Name: "http"}
	if tls {
		s.TLS = &probe.TLSInfo{}
	}
	return s
}

func TestDefaultsOfferIPv6NeighborDiscovery(t *testing.T) {
	d := (&App{}).Defaults()
	keys := func(p scan.Probes) map[string]bool {
		b, _ := json.Marshal(p)
		var m map[string]bool
		_ = json.Unmarshal(b, &m)
		return m
	}
	def, gentle := keys(d.Probes), keys(d.Gentle.Probes)
	for _, k := range []string{"ipv6", "ipv6_ping"} {
		if v, ok := def[k]; !ok || !v {
			t.Errorf("default probe %q = %v (present %v)", k, v, ok)
		}
	}
	// Gentle reads the table the system already has and never sends the echo.
	if v, ok := gentle["ipv6"]; !ok || !v {
		t.Errorf("gentle ipv6 = %v (present %v)", v, ok)
	}
	if v, ok := gentle["ipv6_ping"]; !ok || v {
		t.Errorf("gentle ipv6_ping = %v (present %v): must be listed and off", v, ok)
	}
}

func TestSettingsSavedBeforeSC3KeepIPv6Off(t *testing.T) {
	old := `{"mode":"default","ports":"22,80","probes":{"ports":true,"banners":true,"reverse_dns":true,"netbios":true,"mdns":true,"ssdp":true,"wsd":true,"llmnr":true,"smb":true,"favicon":true,"snmp":false},"concurrency":64,"timeout_ms":800}`
	var s ScheduleSettings
	if err := json.Unmarshal([]byte(old), &s); err != nil {
		t.Fatal(err)
	}
	if s.Probes.IPv6 || s.Probes.IPv6Ping {
		t.Fatalf("%+v", s.Probes)
	}
	p, err := planFromRequest(ScanRequest{Mode: s.Mode, Ports: s.Ports, Probes: s.Probes, Concurrency: s.Concurrency, TimeoutMs: s.TimeoutMs}, []netip.Addr{netip.MustParseAddr("10.0.0.1")})
	if err != nil || p.Probes.IPv6 || p.Probes.IPv6Ping {
		t.Fatalf("%+v %v", p.Probes, err)
	}
}

func TestGentleRequestDropsOnlyTheIPv6Echo(t *testing.T) {
	p, err := planFromRequest(ScanRequest{Mode: scan.ModeGentle, Probes: scan.DefaultProbes}, []netip.Addr{netip.MustParseAddr("10.0.0.1")})
	if err != nil {
		t.Fatal(err)
	}
	if !p.Probes.IPv6 || p.Probes.IPv6Ping {
		t.Fatalf("%+v", p.Probes)
	}
}

func TestPublicAuthorizationReachesTheEngineForIPv6(t *testing.T) {
	targets := []netip.Addr{netip.MustParseAddr("10.0.0.1")}
	for _, allow := range []bool{false, true} {
		p, err := planFromRequest(ScanRequest{Probes: scan.DefaultProbes, AllowPublic: allow}, targets)
		if err != nil || p.AllowPublic != allow || p.Config().AllowPublic != allow {
			t.Fatalf("allow %v: %+v %v", allow, p, err)
		}
	}
	// A scheduled rescan can never carry the authorization.
	req := scheduleRequest(ScheduleConfig{Scan: ScheduleSettings{Probes: scan.DefaultProbes}}, []string{"10.0.0.0/30"})
	if req.AllowPublic {
		t.Fatal("a schedule carried public authorization")
	}
}

func TestOpenWebHandlesIPv6Addresses(t *testing.T) {
	a, f := NewApp(), newFake("")
	a.setPlatform(context.Background(), f)
	a.hosts = []scan.Host{
		{IP: "fd00::5", Status: "up", Services: nil},
		{IP: "fe80::2a%eth0", Status: "up"},
	}
	a.hosts[0].Services = append(a.hosts[0].Services, svc(8443, true))
	a.hosts[1].Services = append(a.hosts[1].Services, svc(80, false))
	if err := a.OpenWeb("fd00::5", 8443); err != nil {
		t.Fatal(err)
	}
	if len(f.opened) != 1 || f.opened[0] != "https://[fd00::5]:8443/" {
		t.Fatalf("opened %v", f.opened)
	}
	err := a.OpenWeb("fe80::2a%eth0", 80)
	if err == nil || !strings.Contains(err.Error(), "link-local") || len(f.opened) != 1 {
		t.Fatalf("a zoned address must not reach the browser: %v %v", err, f.opened)
	}
	if err := a.OpenWeb("fd00::6", 80); err == nil {
		t.Fatal("an address not in the results was accepted")
	}
}

func TestIPv6DevicesHaveNoConnectionActionsYet(t *testing.T) {
	for _, ip := range []string{"fe80::1%eth0", "fd00::5", "2001:db8::1", "::1"} {
		for _, action := range []string{"ssh", "rdp", "ping", "traceroute"} {
			if _, _, err := actionCommand(action, ip); err == nil {
				t.Errorf("%s %s was accepted", action, ip)
			}
		}
	}
	a := NewApp()
	a.hosts = []scan.Host{{IP: "fd00::5", Status: "up", MAC: "00:11:22:33:44:55"}}
	called := false
	a.wake = func(string, string) error { called = true; return nil }
	if err := a.WakeOnLAN("fd00::5"); err == nil || called {
		t.Fatalf("Wake-on-LAN for an IPv6 address: %v", err)
	}
}

func TestIPv6OnlyHostsFlowIntoResultsAndExports(t *testing.T) {
	a := NewApp()
	a.hosts = []scan.Host{
		{IP: "fe80::2a%eth0", Status: "up", MAC: "02:aa:bb:cc:dd:01", IPv6: []string{"fe80::2a%eth0", "fd00::2a"}},
		{IP: "10.0.0.9", Status: "up", IPv6: []string{"fe80::9%eth0"}},
	}
	snap := a.Results(false)
	if len(snap.Hosts) != 2 || snap.Hosts[0].IP != "10.0.0.9" || snap.Hosts[1].IP != "fe80::2a%eth0" {
		t.Fatalf("%+v", snap.Hosts)
	}
	doc := a.documentLocked(true)
	b, err := json.Marshal(doc)
	if err != nil || !strings.Contains(string(b), `"ipv6":["fe80::2a%eth0","fd00::2a"]`) {
		t.Fatalf("%v %s", err, b)
	}
}
