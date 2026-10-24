package main

import (
	"bytes"
	"net/netip"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/zerotrustgarage/tools/scanner/internal/scan"
)

const savedScan = `{"tool":"ztg-scanner","version":"1.0","site":"Old Site","hosts":[{"ip":"192.0.2.1","status":"up","name":"printer"},{"ip":"192.0.2.2","status":"down","name":"offline"}]}`

func TestScanValidatesFlagsAndSiteBeforeTargets(t *testing.T) {
	for _, args := range [][]string{
		{"--json", "--csv", "invalid-hostname.invalid"},
		{"--html", "--json", "invalid-hostname.invalid"},
		{"--html", "--csv", "invalid-hostname.invalid"},
		{"--site", "bad\nlabel", "invalid-hostname.invalid"},
		{"--site", strings.Repeat("é", 101), "invalid-hostname.invalid"},
	} {
		err := cmdScan(args)
		if err == nil || strings.Contains(err.Error(), "invalid-hostname") {
			t.Fatalf("cmdScan(%q) error = %v, want early flag/site validation", args, err)
		}
	}
}

func TestReportRendersSavedScanOfflineIncludingDownHosts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "scan.json")
	if err := os.WriteFile(path, []byte(savedScan), 0600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := cmdReport([]string{path}, &out); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	for _, want := range []string{"Old Site", "192.0.2.1", "192.0.2.2", "Observed unresponsive", "offline"} {
		if !strings.Contains(got, want) {
			t.Errorf("HTML missing %q", want)
		}
	}
	if strings.Contains(got, "ZTG_SNMP") {
		t.Fatal("HTML disclosed credential request data")
	}
}

func TestReportSiteOverrideDoesNotModifySource(t *testing.T) {
	path := filepath.Join(t.TempDir(), "scan.json")
	if err := os.WriteFile(path, []byte(savedScan), 0600); err != nil {
		t.Fatal(err)
	}
	var overridden bytes.Buffer
	if err := cmdReport([]string{"--site", " New Site ", path}, &overridden); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(overridden.String(), "New Site") || strings.Contains(overridden.String(), "Old Site") {
		t.Fatal("site override was not applied")
	}
	var cleared bytes.Buffer
	if err := cmdReport([]string{"--site=", path}, &cleared); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(cleared.String(), "Network inventory") || strings.Contains(cleared.String(), "Old Site") {
		t.Fatal("explicit empty site override was not applied")
	}
	assertContents(t, path, savedScan)
}

func TestReportInvalidInputAndSameFilePreserveExistingFile(t *testing.T) {
	dir := t.TempDir()
	dest := filepath.Join(dir, "destination.html")
	if err := os.WriteFile(dest, []byte("keep me"), 0600); err != nil {
		t.Fatal(err)
	}
	bad := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(bad, []byte(`{"tool":"ztg-scanner","unexpected":true}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := cmdReport([]string{"-o", dest, bad}, &bytes.Buffer{}); err == nil {
		t.Fatal("invalid source accepted")
	}
	assertContents(t, dest, "keep me")
	if err := cmdReport([]string{"-o", dest, filepath.Join(dir, "missing.json")}, &bytes.Buffer{}); err == nil {
		t.Fatal("missing source accepted")
	}
	assertContents(t, dest, "keep me")
	if err := os.WriteFile(bad, []byte(savedScan), 0600); err != nil {
		t.Fatal(err)
	}
	if err := cmdReport([]string{"-o", bad, bad}, &bytes.Buffer{}); err == nil {
		t.Fatal("same source/output accepted")
	}
	assertContents(t, bad, savedScan)
}

func TestReportRequiresInputWithoutWritingOutput(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "existing.html")
	if err := os.WriteFile(dest, []byte("unchanged"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := cmdReport([]string{"-o", dest}, &bytes.Buffer{}); err == nil {
		t.Fatal("missing input accepted")
	}
	assertContents(t, dest, "unchanged")
}

func assertContents(t *testing.T, path, want string) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != want {
		t.Fatalf("%s = %q, want %q", path, b, want)
	}
}

func TestGentleModeRejectsMalformedSettings(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"--mode", "stealth", "10.0.0.1"}, "unknown scan mode"},
		{[]string{"--mode", "gentle", "--concurrency", "64", "10.0.0.1"}, "at most 4 hosts in parallel"},
		{[]string{"--mode", "gentle", "--ports", "1-100", "10.0.0.1"}, "at most 16 ports"},
		{[]string{"--mode", "gentle", "--ports", "22,23", "10.0.0.1"}, "port 23 is not one of them"},
		{[]string{"--mode", "gentle", "--ports", "x", "10.0.0.1"}, "bad port"},
		{[]string{"--mode", "gentle", "--snmp-user", "noc", "10.0.0.1"}, "does not use SNMP"},
		{[]string{"--mode", "gentle", "--timeout", "-1s", "10.0.0.1"}, "must not be negative"},
	} {
		err := cmdScan(tc.args)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%v: error %v, want %q", tc.args, err, tc.want)
		}
	}
}

func TestGentlePlanFromFlagsMatchesTheSharedDefinition(t *testing.T) {
	targets := []netip.Addr{netip.MustParseAddr("10.0.0.1")}
	req, err := planRequest(planFlags{mode: scan.ModeGentle, concurrency: 128, timeout: 800 * time.Millisecond}, targets)
	if err != nil {
		t.Fatal(err)
	}
	got, err := scan.BuildPlan(req)
	if err != nil {
		t.Fatal(err)
	}
	// Flag defaults (128 hosts, 800ms) were not set by the user, so gentle's
	// own settings apply. The app test asserts this identical Plan.
	want := scan.Plan{
		Mode: scan.ModeGentle, Targets: targets, Ports: []int{22, 80, 443, 445, 515, 631, 8080, 9100},
		Probes: scan.Probes{Ports: true, ReverseDNS: true}, Concurrency: 4, PortConcurrency: 2, PingAttempts: 1,
		ConnectTimeout: 1500 * time.Millisecond, ProbeInterval: 100 * time.Millisecond,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("plan %+v\nwant %+v", got, want)
	}
}

func TestDefaultPlanFromFlagsIsUnchanged(t *testing.T) {
	targets := []netip.Addr{netip.MustParseAddr("10.0.0.1")}
	req, err := planRequest(planFlags{mode: scan.ModeDefault, concurrency: 128, timeout: 800 * time.Millisecond}, targets)
	if err != nil {
		t.Fatal(err)
	}
	p, err := scan.BuildPlan(req)
	if err != nil {
		t.Fatal(err)
	}
	want := scan.Config{Targets: targets, Probes: scan.DefaultProbes, Concurrency: 128, ConnectTimeout: 800 * time.Millisecond}
	if got := p.Config(); !reflect.DeepEqual(got, want) {
		t.Fatalf("default config %+v, want %+v", got, want)
	}
}

func TestScanHelpDescribesGentleTradeoff(t *testing.T) {
	h := gentleHelp()
	for _, want := range []string{"--mode gentle", scan.GentleSummary(), scan.GentleTradeoff, "does not remove it"} {
		if !strings.Contains(h, want) {
			t.Errorf("help missing %q", want)
		}
	}
	if strings.Contains(strings.ToLower(h), "harmless") || strings.Contains(h, "\u2014") {
		t.Error("help overclaims or uses an em dash")
	}
}
