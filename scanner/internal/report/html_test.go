package report

import (
	"bytes"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/zerotrustgarage/tools/scanner/internal/classify"
	"github.com/zerotrustgarage/tools/scanner/internal/inventory"
	"github.com/zerotrustgarage/tools/scanner/internal/probe"
	"github.com/zerotrustgarage/tools/scanner/internal/scan"
)

func TestHTMLEscapesDeviceAndSiteTextAndShowsInventoryEvidence(t *testing.T) {
	d := Document{Tool: "ztg-scanner", Version: "1.2", Site: `<img src=x onerror=alert(1)>`, Targets: []string{"192.0.2.0/30"}, Summary: scan.Summary{Total: 4, Started: time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC), Finished: time.Date(2025, 1, 2, 3, 5, 5, 0, time.UTC), Methods: []string{"ARP", "TCP"}}, Hosts: []scan.Host{
		{Annotation: scan.Annotation{Notes: `<script>alert(1)</script>`, Tags: []string{"tag <b>"}}, IP: "192.0.2.2", Status: "down", LastKnownMAC: "00:11:22:33:44:55"},
		{IP: "192.0.2.1", Status: "up", Name: `<svg/onload=alert(1)>`, Type: "router", TypeReason: "device said <b>router</b>", MAC: "00:aa:bb:cc:dd:ee", Vendor: "Vendor & Sons", Evidence: []string{"tcp-reset"}, Findings: []classify.Finding{{Severity: "high", Code: "<img>", Message: `<a href="javascript:alert(1)">unsafe</a>`}}, NetBIOS: &probe.NetBIOSInfo{Computer: "pc<1>", Workgroup: "WORK"}, MDNS: &probe.MDNSInfo{Hostname: "mdns.local"}, SNMP: &probe.SNMPInfo{Manufacturer: "Acme", Model: "M1", Serial: "S1", Firmware: "F1", SysLocation: "Room 4"}, Annotation: scan.Annotation{Tags: []string{"tag <b>"}}, Services: []probe.Service{{Port: 23, Name: "telnet", Banner: "RAW SHOULD NOT APPEAR"}}},
	}}
	var b bytes.Buffer
	if err := HTML(&b, d); err != nil {
		t.Fatal(err)
	}
	s := b.String()
	for _, want := range []string{"Content-Security-Policy", "Complete", "Observed responsive", "Observed unresponsive", "192.0.2.1", "Historical MAC", "mdns.local", "Room 4", "device said &lt;b&gt;router&lt;/b&gt;", "&lt;script&gt;alert(1)&lt;/script&gt;", "High findings", "ARP"} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q", want)
		}
	}
	for _, bad := range []string{"<script>alert(1)</script>", `href="javascript:alert(1)"`, "<img src=x onerror=alert(1)>", "RAW SHOULD NOT APPEAR"} {
		if strings.Contains(s, bad) {
			t.Errorf("unsafe or unnecessary data %q", bad)
		}
	}
	if strings.Index(s, "192.0.2.1") > strings.Index(s, "192.0.2.2") {
		t.Fatal("hosts are not stably sorted by address")
	}
}

func TestHTMLPartialZeroTimesCountersAndDoesNotMutate(t *testing.T) {
	hosts := []scan.Host{{IP: "10.0.0.2", Status: "down"}, {IP: "10.0.0.1", Status: "up"}}
	before := append([]scan.Host(nil), hosts...)
	d := Document{Hosts: hosts, Summary: scan.Summary{Total: 9, Canceled: true}}
	var b bytes.Buffer
	if err := HTML(&b, d); err != nil {
		t.Fatal(err)
	}
	s := b.String()
	if !strings.Contains(s, "Canceled / partial") || !strings.Contains(s, "Not recorded") || !strings.Contains(s, "<strong>9</strong>") || !strings.Contains(s, "<strong>2</strong>") || !strings.Contains(s, "<strong>1</strong>") {
		t.Fatal("partial state or counters missing")
	}
	for i := range hosts {
		if hosts[i].IP != before[i].IP {
			t.Fatal("input host slice mutated")
		}
	}
	d.Summary.Canceled = false
	b.Reset()
	if err := HTML(&b, d); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(b.String(), "Incomplete / partial snapshot") {
		t.Fatal("zero finish should be incomplete")
	}
}

func TestHTMLSortsIPv4Numerically(t *testing.T) {
	d := Document{Hosts: []scan.Host{{IP: "10.0.0.10", Status: "up"}, {IP: "10.0.0.2", Status: "up"}}}
	var b bytes.Buffer
	if err := HTML(&b, d); err != nil {
		t.Fatal(err)
	}
	s := b.String()
	if strings.Index(s, "10.0.0.2") > strings.Index(s, "10.0.0.10") {
		t.Fatal("IPv4 addresses are not sorted numerically")
	}
}

func TestHTMLAnnotationMapFallbackPrecedenceAndNoMutation(t *testing.T) {
	legacy := scan.Host{IP: "10.0.0.9", Status: "down", LastKnownMAC: "00:11:22:33:44:55"}
	current := scan.Host{IP: "10.0.0.2", Status: "up", MAC: "00:aa:bb:cc:dd:ee", Annotation: scan.Annotation{Notes: "host note", Tags: []string{"host-tag"}}}
	hosts := []scan.Host{legacy, current}
	legacyNote := scan.Annotation{Notes: "saved note", Tags: []string{"saved-tag"}, Favorite: true}
	precedenceNote := scan.Annotation{Notes: "map note must not replace host note", Tags: []string{"map-tag"}}
	annotations := map[string]scan.Annotation{
		inventory.AnnotationKey(legacy):  legacyNote,
		inventory.AnnotationKey(current): precedenceNote,
	}
	originalHosts := append([]scan.Host(nil), hosts...)
	originalAnnotations := map[string]scan.Annotation{
		inventory.AnnotationKey(legacy):  legacyNote,
		inventory.AnnotationKey(current): precedenceNote,
	}
	var b bytes.Buffer
	if err := HTML(&b, Document{Hosts: hosts, Annotations: annotations}); err != nil {
		t.Fatal(err)
	}
	s := b.String()
	for _, want := range []string{"saved note", "saved-tag", "Favorite", "Historical MAC: 00:11:22:33:44:55", "host note", "host-tag"} {
		if !strings.Contains(s, want) {
			t.Errorf("missing annotation or historical evidence %q", want)
		}
	}
	if strings.Contains(s, "map note must not replace host note") || strings.Contains(s, "map-tag") {
		t.Fatal("annotation map overrode a populated host annotation")
	}
	if !reflect.DeepEqual(hosts, originalHosts) || !reflect.DeepEqual(annotations, originalAnnotations) {
		t.Fatal("HTML rendering mutated source hosts or annotations")
	}
}

type failWriter struct{}

func (failWriter) Write([]byte) (int, error) { return 0, errors.New("writer failed") }
func TestHTMLWriterErrorsAndCaps(t *testing.T) {
	if err := HTML(failWriter{}, Document{}); err == nil || !strings.Contains(err.Error(), "writer failed") {
		t.Fatalf("writer error = %v", err)
	}
	var empty bytes.Buffer
	if err := HTML(&empty, Document{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(empty.String(), "No findings recorded.") {
		t.Fatal("empty findings overview is unclear")
	}
	d := Document{Hosts: make([]scan.Host, 65537)}
	if err := HTML(io.Discard, d); err == nil || !strings.Contains(err.Error(), "65536") {
		t.Fatalf("host limit error = %v", err)
	}
	d = Document{Hosts: []scan.Host{{IP: "1.2.3.4", Annotation: scan.Annotation{Notes: strings.Repeat("x", MaxDocumentBytes)}}}}
	if err := HTML(io.Discard, d); err == nil || !strings.Contains(err.Error(), "size limit") {
		t.Fatalf("output cap error = %v", err)
	}
}
