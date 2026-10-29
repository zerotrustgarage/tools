package inventory

import (
	"reflect"
	"testing"

	"github.com/zerotrustgarage/tools/scanner/internal/probe"
	"github.com/zerotrustgarage/tools/scanner/internal/scan"
)

func sc11Base() scan.Host {
	h := host("10.0.0.1", "00:11:22:00:00:01")
	h.LLMNR = &probe.LLMNRInfo{Names: []string{"PC-1"}}
	h.WSD = &probe.WSDInfo{Types: []string{"wsdp:Device", "pub:Computer"}}
	h.SMB = &probe.SMBInfo{Dialect: "3.1.1", SigningEnabled: true, SystemTime: "2026-10-29T12:00:00Z", ServerGUID: "g1", Capabilities: []string{"dfs"}}
	h.Services = []probe.Service{{Port: 80, Name: "http", Favicon: &probe.FaviconInfo{SHA256: "aaaa", MMH3: 1}}}
	return h
}

func TestCompareSC11NoiseIsNotAChange(t *testing.T) {
	a, b := sc11Base(), sc11Base()
	b.SMB = &probe.SMBInfo{Dialect: "3.1.1", SigningEnabled: true, SystemTime: "2026-10-30T09:30:00Z", ServerGUID: "g2", Capabilities: []string{"dfs", "leasing"}}
	b.WSD = &probe.WSDInfo{Types: []string{"pub:Computer", "wsdp:Device"}} // order differs
	b.Services[0].Favicon = &probe.FaviconInfo{SHA256: "aaaa", MMH3: 1, Size: 5}
	if c := Compare([]scan.Host{a}, []scan.Host{b}, false); len(c) != 1 || c[0].Kind != "unchanged" {
		t.Fatalf("clock, GUID, capability or ordering noise reported: %+v", c)
	}
}

func TestCompareSC11ReportsRealChanges(t *testing.T) {
	cases := map[string]struct {
		change func(h *scan.Host)
		field  string
	}{
		"llmnr name": {func(h *scan.Host) { h.LLMNR = &probe.LLMNRInfo{Names: []string{"PC-2"}} }, "llmnr_name"},
		"wsd types":  {func(h *scan.Host) { h.WSD = &probe.WSDInfo{Types: []string{"pub:Computer"}} }, "wsd_types"},
		"smb dialect": {func(h *scan.Host) {
			h.SMB = &probe.SMBInfo{Dialect: "2.1", SigningEnabled: true}
		}, "smb_dialect"},
		"smb signing": {func(h *scan.Host) {
			h.SMB = &probe.SMBInfo{Dialect: "3.1.1", SigningEnabled: true, SigningRequired: true}
		}, "smb_signing"},
		"favicon": {func(h *scan.Host) {
			h.Services = []probe.Service{{Port: 80, Name: "http", Favicon: &probe.FaviconInfo{SHA256: "bbbb"}}}
		}, "favicon"},
	}
	for name, c := range cases {
		a, b := sc11Base(), sc11Base()
		c.change(&b)
		got := Compare([]scan.Host{a}, []scan.Host{b}, false)
		if len(got) != 1 || got[0].Kind != "changed" || !reflect.DeepEqual(got[0].Fields, []string{c.field}) {
			t.Errorf("%s: %+v", name, got)
		}
	}
}

func TestCompareSC11OnlyWhereBothScansHaveTheEvidence(t *testing.T) {
	// A probe that was off, filtered or lost a datagram in one scan is not a change.
	a := sc11Base()
	b := sc11Base()
	b.LLMNR, b.WSD, b.SMB = nil, nil, nil
	b.Services = []probe.Service{{Port: 80, Name: "http"}}
	if c := Compare([]scan.Host{a}, []scan.Host{b}, false); len(c) != 1 || c[0].Kind != "unchanged" {
		t.Fatalf("%+v", c)
	}
	if c := Compare([]scan.Host{b}, []scan.Host{a}, false); len(c) != 1 || c[0].Kind != "unchanged" {
		t.Fatalf("%+v", c)
	}
	// Different favicon ports do not compare against each other.
	x, y := sc11Base(), sc11Base()
	y.Services = []probe.Service{{Port: 8080, Name: "http", Favicon: &probe.FaviconInfo{SHA256: "zzzz"}}}
	if c := Compare([]scan.Host{x}, []scan.Host{y}, false); len(c[0].Fields) != 1 || c[0].Fields[0] != "services" {
		t.Fatalf("%+v", c[0].Fields)
	}
}
