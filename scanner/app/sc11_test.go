package main

import (
	"encoding/json"
	"net/netip"
	"testing"

	"github.com/zerotrustgarage/tools/scanner/internal/scan"
)

func TestDefaultsOfferTheSC11Probes(t *testing.T) {
	d := (&App{}).Defaults()
	b, err := json.Marshal(d.Probes)
	if err != nil {
		t.Fatal(err)
	}
	var keys map[string]bool
	if err := json.Unmarshal(b, &keys); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"wsd", "llmnr", "smb", "favicon"} {
		if v, ok := keys[k]; !ok || !v {
			t.Errorf("default probe %q = %v (present %v)", k, v, ok)
		}
	}
	gb, _ := json.Marshal(d.Gentle.Probes)
	var gk map[string]bool
	_ = json.Unmarshal(gb, &gk)
	for _, k := range []string{"wsd", "llmnr", "smb", "favicon"} {
		if v, ok := gk[k]; !ok || v {
			t.Errorf("gentle probe %q = %v (present %v): must be listed and off", k, v, ok)
		}
	}
}

func TestSettingsSavedBeforeSC11KeepTheNewProbesOff(t *testing.T) {
	// Schedules and scan requests written by an earlier version carry no keys
	// for the new probes. They decode to off, so an old schedule keeps behaving
	// as it did until the technician re-saves it with the new settings.
	old := `{"mode":"default","ports":"22,80","probes":{"ports":true,"banners":true,"reverse_dns":true,"netbios":true,"mdns":true,"ssdp":true,"snmp":false},"concurrency":64,"timeout_ms":800}`
	var s ScheduleSettings
	if err := json.Unmarshal([]byte(old), &s); err != nil {
		t.Fatal(err)
	}
	if s.Probes.WSD || s.Probes.LLMNR || s.Probes.SMB || s.Probes.Favicon || !s.Probes.SSDP {
		t.Fatalf("%+v", s.Probes)
	}
	p, err := planFromRequest(ScanRequest{Mode: s.Mode, Ports: s.Ports, Probes: s.Probes, Concurrency: s.Concurrency, TimeoutMs: s.TimeoutMs}, []netip.Addr{netip.MustParseAddr("10.0.0.1")})
	if err != nil {
		t.Fatal(err)
	}
	if p.Probes.WSD || p.Probes.LLMNR || p.Probes.SMB || p.Probes.Favicon {
		t.Fatalf("%+v", p.Probes)
	}
}

func TestGentleRequestDropsTheSC11Probes(t *testing.T) {
	p, err := planFromRequest(ScanRequest{Mode: scan.ModeGentle, Probes: scan.DefaultProbes}, []netip.Addr{netip.MustParseAddr("10.0.0.1")})
	if err != nil {
		t.Fatal(err)
	}
	if p.Probes.WSD || p.Probes.LLMNR || p.Probes.SMB || p.Probes.Favicon {
		t.Fatalf("gentle plan from the desktop request has %+v", p.Probes)
	}
}
