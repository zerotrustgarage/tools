package report

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/zerotrustgarage/tools/scanner/internal/scan"
)

func ipv6Doc() Document {
	return Document{
		Tool: "ztg-scanner", Version: "test", Targets: []string{"192.168.1.0/24"},
		Summary: scan.Summary{Total: 254, Up: 2, IPv6Only: 1, Methods: []string{"arp-win32", "ndp-win32", "icmp6-multicast"}},
		Hosts: []scan.Host{
			{IP: "fe80::2a%Ethernet", Status: "up", MAC: "02:aa:bb:cc:dd:01", Evidence: []string{"ndp", "icmp6"},
				IPv6: []string{"fe80::2a%Ethernet", "fd00::2a"}, Type: "Unknown"},
			{IP: "192.168.1.5", Status: "up", MAC: "00:1b:21:aa:bb:cc", IPv6: []string{"fe80::5%Ethernet", "2001:db8:1::5"}},
		},
	}
}

func TestIPv6RoundTripsThroughJSON(t *testing.T) {
	var buf bytes.Buffer
	if err := JSON(&buf, ipv6Doc()); err != nil {
		t.Fatal(err)
	}
	got, err := Read(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if got.Summary.IPv6Only != 1 || len(got.Hosts) != 2 || got.Hosts[0].IP != "fe80::2a%Ethernet" || strings.Join(got.Hosts[1].IPv6, " ") != "fe80::5%Ethernet 2001:db8:1::5" {
		t.Fatalf("%+v", got)
	}
}

func TestOldFilesStillReadAndNewFieldsStayOptional(t *testing.T) {
	f, err := os.Open("testdata/client-report.json")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	old, err := Read(f)
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := JSON(&out, old); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "ipv6") {
		t.Fatal("a scan with no IPv6 evidence must serialize exactly as before")
	}
	// A hand-written older file with no IPv6 fields at all.
	min := `{"tool":"ztg-scanner","version":"1","targets":["10.0.0.0/30"],"summary":{"started":"2026-01-01T00:00:00Z","finished":"2026-01-01T00:00:01Z","total":2,"up":1,"methods":["tcp-connect"]},"hosts":[{"ip":"10.0.0.1","status":"up","scanned_at":"2026-01-01T00:00:00Z"}]}`
	if _, err := Read(strings.NewReader(min)); err != nil {
		t.Fatal(err)
	}
}

func TestReadRefusesBadIPv6Records(t *testing.T) {
	mutate := func(f func(d *Document)) error {
		d := ipv6Doc()
		f(&d)
		var b bytes.Buffer
		if err := JSON(&b, d); err != nil {
			t.Fatal(err)
		}
		_, err := Read(&b)
		return err
	}
	cases := map[string]func(d *Document){
		"upper case":                 func(d *Document) { d.Hosts[0].IP = "FE80::2A%Ethernet" },
		"not canonical":              func(d *Document) { d.Hosts[0].IP = "fe80:0:0:0:0:0:0:2a%Ethernet" },
		"zone with a space":          func(d *Document) { d.Hosts[0].IP = "fe80::2a%Ether net" },
		"zone with markup":           func(d *Document) { d.Hosts[0].IP = "fe80::2a%<b>" },
		"zone on a global address":   func(d *Document) { d.Hosts[0].IP = "2001:db8::1%eth0" },
		"empty zone":                 func(d *Document) { d.Hosts[0].IP = "fe80::2a%" },
		"ipv4-mapped":                func(d *Document) { d.Hosts[0].IP = "::ffff:10.0.0.1" },
		"multicast":                  func(d *Document) { d.Hosts[0].IP = "ff02::1" },
		"unspecified":                func(d *Document) { d.Hosts[0].IP = "::" },
		"duplicate host":             func(d *Document) { d.Hosts[1].IP = d.Hosts[0].IP },
		"ipv4 address in the list":   func(d *Document) { d.Hosts[1].IPv6[0] = "10.0.0.5" },
		"bad address in the list":    func(d *Document) { d.Hosts[1].IPv6[0] = "fe80::5%a b" },
		"duplicate in the list":      func(d *Document) { d.Hosts[1].IPv6 = []string{"fd00::1", "fd00::1"} },
		"markup in the list":         func(d *Document) { d.Hosts[1].IPv6 = []string{"<script>"} },
		"too many addresses":         func(d *Document) { d.Hosts[1].IPv6 = manyULA(scan.MaxIPv6PerDevice + 1) },
		"impossible IPv6-only count": func(d *Document) { d.Summary.IPv6Only = 99 },
		"negative IPv6-only count":   func(d *Document) { d.Summary.IPv6Only = -1 },
	}
	for name, f := range cases {
		if err := mutate(f); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
	if err := mutate(func(d *Document) { d.Hosts[1].IPv6 = manyULA(scan.MaxIPv6PerDevice) }); err != nil {
		t.Errorf("the limit itself must be accepted: %v", err)
	}
}

func manyULA(n int) []string {
	var out []string
	for i := 0; i < n; i++ {
		out = append(out, fmt.Sprintf("fd00::%x", i+1))
	}
	return out
}

func TestCSVCarriesIPv6LastAndGuardsFormulas(t *testing.T) {
	if CSVHeader[len(CSVHeader)-1] != "ipv6" || CSVHeader[len(CSVHeader)-2] != "favicon_sha256" {
		t.Fatalf("header %v", CSVHeader)
	}
	d := ipv6Doc()
	var buf bytes.Buffer
	if err := CSV(&buf, d.Hosts); err != nil {
		t.Fatal(err)
	}
	rows, err := csv.NewReader(&buf).ReadAll()
	if err != nil || len(rows) != 3 {
		t.Fatalf("%v %d", err, len(rows))
	}
	last := len(CSVHeader) - 1
	if rows[1][0] != "fe80::2a%Ethernet" || rows[1][last] != "fe80::2a%Ethernet fd00::2a" || rows[2][last] != "fe80::5%Ethernet 2001:db8:1::5" {
		t.Fatalf("rows %q", rows)
	}
	// Every cell still goes through the formula guard.
	h := scan.Host{IP: "10.0.0.1", Status: "up", IPv6: []string{"=cmd|' /C calc'!A0"}}
	buf.Reset()
	if err := CSV(&buf, []scan.Host{h}); err != nil {
		t.Fatal(err)
	}
	rows, _ = csv.NewReader(&buf).ReadAll()
	if !strings.HasPrefix(rows[1][last], "'=") {
		t.Fatalf("formula in the IPv6 column was not neutralized: %q", rows[1][last])
	}
}

func TestSortPutsIPv4FirstThenIPv6(t *testing.T) {
	hosts := []scan.Host{{IP: "fe80::2%eth1"}, {IP: "fd00::1"}, {IP: "10.0.0.9"}, {IP: "fe80::2%eth0"}, {IP: "10.0.0.10"}, {IP: "fe80::1%eth0"}}
	SortHosts(hosts)
	var got []string
	for _, h := range hosts {
		got = append(got, h.IP)
	}
	want := "10.0.0.9 10.0.0.10 fd00::1 fe80::1%eth0 fe80::2%eth0 fe80::2%eth1"
	if strings.Join(got, " ") != want {
		t.Fatalf("%v", got)
	}
}

func TestTableAndHTMLShowIPv6(t *testing.T) {
	d := ipv6Doc()
	var buf bytes.Buffer
	if err := Table(&buf, d.Hosts, true); err != nil || !strings.Contains(buf.String(), "fe80::2a%Ethernet") {
		t.Fatalf("%v\n%s", err, buf.String())
	}
	buf.Reset()
	if err := HTML(&buf, d); err != nil {
		t.Fatal(err)
	}
	page := buf.String()
	for _, want := range []string{"fe80::2a%Ethernet", "fd00::2a", "2001:db8:1::5", "IPv6-only devices", "IPv6 addresses (neighbor table", "ndp", "icmp6"} {
		if !strings.Contains(page, want) {
			t.Errorf("report lacks %q", want)
		}
	}
	// An address is text, never markup, even a hostile one.
	d.Hosts[1].IPv6 = []string{"<script>alert(1)</script>"}
	buf.Reset()
	if err := HTML(&buf, d); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(buf.String(), "<script>alert") {
		t.Fatal("an IPv6 list entry reached the page unescaped")
	}
	// A report with no IPv6 evidence carries none of the IPv6 sections.
	buf.Reset()
	plain := Document{Tool: "ztg-scanner", Hosts: []scan.Host{{IP: "10.0.0.1", Status: "up"}}}
	if err := HTML(&buf, plain); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(buf.String(), "IPv6") {
		t.Fatal("an IPv4-only report mentions IPv6")
	}
}

func TestJSONOmitsEmptyIPv6(t *testing.T) {
	b, _ := json.Marshal(scan.Host{IP: "10.0.0.1", Status: "up"})
	if strings.Contains(string(b), "ipv6") {
		t.Fatal(string(b))
	}
	s, _ := json.Marshal(scan.Summary{Total: 1})
	if strings.Contains(string(s), "ipv6") {
		t.Fatal(string(s))
	}
}
