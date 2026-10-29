package report

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/zerotrustgarage/tools/scanner/internal/probe"
	"github.com/zerotrustgarage/tools/scanner/internal/scan"
)

const sc11SHA = "33d499a752b8bfaddefd2358485f5e2014da2570d1e5f8b2e94b5cd87c0b6cb8"

func sc11Host() scan.Host {
	return scan.Host{
		IP: "10.0.0.20", Status: "up", Name: "LAB-PC", Type: "Printer", MAC: "00:1b:21:aa:bb:cc",
		LLMNR: &probe.LLMNRInfo{Names: []string{"LAB-PC"}},
		WSD: &probe.WSDInfo{Types: []string{"dn:NetworkVideoTransmitter"}, Scopes: []string{"onvif://www.onvif.org/name/Cam"},
			XAddrs: []string{"http://10.0.0.20/onvif/device_service"}, Name: "Cam", Hardware: "M3045-V"},
		SMB: &probe.SMBInfo{Dialect: "3.0.2", SigningEnabled: true, Capabilities: []string{"dfs", "leasing"},
			ServerGUID: "12345678-9abc-def0-0102-030405060708", SystemTime: "2026-10-29T12:00:00Z", EraHint: "Highest dialect SMB 3.0.2: Windows 8.1 or Server 2012 R2 era, or a Samba or NAS release of that age"},
		Services: []probe.Service{
			{Port: 80, Name: "http", HTTP: &probe.HTTPInfo{Status: 200}, Favicon: &probe.FaviconInfo{SHA256: sc11SHA, MMH3: -1338840234, Size: 200, ContentType: "image/x-icon", Format: "ico"}},
			{Port: 445, Name: "smb"},
		},
	}
}

func TestCSVCarriesSC11ColumnsAfterTheExistingOnes(t *testing.T) {
	// The original 28 columns keep their positions; new ones are appended.
	orig := []string{"ip", "status", "name", "type", "mac", "vendor", "mac_randomized", "dns_name", "netbios_name", "workgroup", "mdns_name", "manufacturer", "model", "open_ports", "services", "rtt_ms", "ttl", "os_hint", "findings", "evidence", "serial", "firmware", "location", "snmp_descr", "notes", "tags", "favorite", "last_known_mac"}
	for i, c := range orig {
		if CSVHeader[i] != c {
			t.Fatalf("column %d is %q, was %q", i, CSVHeader[i], c)
		}
	}
	var buf bytes.Buffer
	if err := CSV(&buf, []scan.Host{sc11Host()}); err != nil {
		t.Fatal(err)
	}
	rows, err := csv.NewReader(&buf).ReadAll()
	if err != nil || len(rows) != 2 || len(rows[1]) != len(CSVHeader) {
		t.Fatalf("%v rows %d", err, len(rows))
	}
	col := map[string]string{}
	for i, name := range CSVHeader {
		col[name] = rows[1][i]
	}
	want := map[string]string{
		"llmnr_name": "LAB-PC", "wsd_types": "dn:NetworkVideoTransmitter", "wsd_name": "Cam M3045-V",
		"smb_dialect": "3.0.2", "smb_signing": "enabled, not required",
		"favicon_mmh3": "80:-1338840234", "favicon_sha256": "80:" + sc11SHA, "model": "M3045-V",
	}
	for k, v := range want {
		if col[k] != v {
			t.Errorf("%s = %q, want %q", k, col[k], v)
		}
	}
}

func TestCSVNeutralizesSC11Text(t *testing.T) {
	h := sc11Host()
	h.LLMNR = &probe.LLMNRInfo{Names: []string{"=cmd"}}
	h.WSD = &probe.WSDInfo{Types: []string{"@x:y"}, Name: "=HYPERLINK(1)", Hardware: "+1"}
	h.SMB = &probe.SMBInfo{Dialect: "3.1.1"}
	var buf bytes.Buffer
	if err := CSV(&buf, []scan.Host{h}); err != nil {
		t.Fatal(err)
	}
	rows, _ := csv.NewReader(&buf).ReadAll()
	for i, cell := range rows[1] {
		if cell != "" && strings.ContainsRune("=+-@\t\r", rune(cell[0])) {
			t.Errorf("column %s starts with a formula character: %q", CSVHeader[i], cell)
		}
	}
}

func TestHTMLShowsSC11EvidenceEscaped(t *testing.T) {
	h := sc11Host()
	h.LLMNR = &probe.LLMNRInfo{Names: []string{"lab"}}
	h.WSD.Name = `<img src=x onerror=alert(1)>`
	h.WSD.XAddrs = []string{"http://10.0.0.20/<script>"}
	h.SMB.EraHint = `<b>era</b>`
	var b bytes.Buffer
	if err := HTML(&b, Document{Tool: "ztg-scanner", Hosts: []scan.Host{h}}); err != nil {
		t.Fatal(err)
	}
	s := b.String()
	for _, want := range []string{"WS-Discovery types: dn:NetworkVideoTransmitter", "SMB2 negotiate: dialect 3.0.2, signing enabled, not required", "SMB2 server clock (UTC): 2026-10-29T12:00:00Z",
		"Favicon on port 80: SHA-256 " + sc11SHA, "MurmurHash3 -1338840234", "announced by the device, not contacted", "&lt;b&gt;era&lt;/b&gt;", "&lt;img src=x onerror=alert(1)&gt;"} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q", want)
		}
	}
	for _, bad := range []string{"<img src=x", "<script>", "<b>era</b>"} {
		if strings.Contains(s, bad) {
			t.Errorf("unescaped %q", bad)
		}
	}
	if strings.Contains(s, `href="http://10.0.0.20`) {
		t.Error("a device-announced address became a link")
	}
}

func TestJSONRoundTripKeepsSC11AndPlainHostsStayUnchanged(t *testing.T) {
	doc := Document{Tool: "ztg-scanner", Version: "1", Summary: scan.Summary{Total: 2, Up: 2}, Hosts: []scan.Host{sc11Host(), {IP: "10.0.0.9", Status: "up"}}}
	var buf bytes.Buffer
	if err := JSON(&buf, doc); err != nil {
		t.Fatal(err)
	}
	// A host without SC11 data writes none of the new keys: old consumers and
	// the SC6 comparison see exactly what they saw before.
	var raw struct{ Hosts []map[string]any }
	if err := json.Unmarshal(buf.Bytes(), &raw); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"wsd", "llmnr", "smb"} {
		if _, ok := raw.Hosts[1][k]; ok {
			t.Errorf("plain host has key %q", k)
		}
	}
	back, err := Read(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	h := back.Hosts[0]
	if h.WSD == nil || h.WSD.Name != "Cam" || h.LLMNR.Primary() != "LAB-PC" || h.SMB == nil || h.SMB.Dialect != "3.0.2" ||
		h.Services[0].Favicon == nil || h.Services[0].Favicon.SHA256 != sc11SHA || h.Services[0].Favicon.MMH3 != -1338840234 {
		t.Fatalf("round trip lost data: %+v", h)
	}
	if back.Hosts[1].WSD != nil || back.Hosts[1].LLMNR != nil || back.Hosts[1].SMB != nil {
		t.Fatal("plain host gained SC11 data")
	}
}

func TestOldFilesStillRead(t *testing.T) {
	f, err := os.Open("testdata/client-report.json")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	d, err := Read(f)
	if err != nil || len(d.Hosts) == 0 {
		t.Fatalf("existing export no longer reads: %v", err)
	}
}

func TestReadSanitizesHostileSC11Fields(t *testing.T) {
	h := sc11Host()
	h.LLMNR = &probe.LLMNRInfo{Names: []string{"bad name\x1b[2J", "ok"}}
	h.WSD.Types = append([]string{"<script>"}, h.WSD.Types...)
	h.WSD.Name = "a\x00b‮evil"
	h.WSD.XAddrs = []string{"javascript:alert(1)"}
	h.SMB.EraHint = "x\x07y"
	h.SMB.Dialect = "3.0.2"
	h.Services[0].Favicon.ContentType = "image/\x00x"
	var buf bytes.Buffer
	if err := JSON(&buf, Document{Tool: "ztg-scanner", Hosts: []scan.Host{h}}); err != nil {
		t.Fatal(err)
	}
	d, err := Read(&buf)
	if err != nil {
		t.Fatal(err)
	}
	got := d.Hosts[0]
	if got.LLMNR.Names[0] != "ok" || len(got.LLMNR.Names) != 1 {
		t.Errorf("LLMNR %+v", got.LLMNR)
	}
	if len(got.WSD.Types) != 1 || got.WSD.Name != "abevil" || len(got.WSD.XAddrs) != 0 {
		t.Errorf("WSD %+v", got.WSD)
	}
	if got.SMB.EraHint != "xy" || got.Services[0].Favicon.ContentType != "image/x" {
		t.Errorf("SMB %+v favicon %+v", got.SMB, got.Services[0].Favicon)
	}
	// Records that sanitize down to nothing are dropped, not kept empty.
	h2 := scan.Host{IP: "10.0.0.5", Status: "up", LLMNR: &probe.LLMNRInfo{Names: []string{"bad name"}}, SMB: &probe.SMBInfo{Dialect: "9.9"},
		WSD: &probe.WSDInfo{Types: []string{"<x>"}}, Services: []probe.Service{{Port: 80, Name: "http", Favicon: &probe.FaviconInfo{SHA256: "nothex"}}}}
	buf.Reset()
	JSON(&buf, Document{Tool: "ztg-scanner", Hosts: []scan.Host{h2}})
	d, err = Read(&buf)
	if err != nil {
		t.Fatal(err)
	}
	g := d.Hosts[0]
	if g.LLMNR != nil || g.SMB != nil || g.WSD != nil || g.Services[0].Favicon != nil {
		t.Fatalf("empty records kept: %+v", g)
	}
}
