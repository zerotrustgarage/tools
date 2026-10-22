package report

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"strings"
	"testing"

	"github.com/zerotrustgarage/tools/scanner/internal/classify"
	"github.com/zerotrustgarage/tools/scanner/internal/probe"
	"github.com/zerotrustgarage/tools/scanner/internal/scan"
)

func sample() []scan.Host {
	return []scan.Host{
		{IP: "10.0.0.20", Status: "up", Name: "=HYPERLINK(\"http://evil\")", Type: "Printer", MAC: "00:1b:21:aa:bb:cc", Vendor: "Intel Corporate",
			Services: []probe.Service{{Port: 80, Name: "http", HTTP: &probe.HTTPInfo{Status: 200, Title: "+cmd|' /C calc'!A0"}}, {Port: 9100, Name: "jetdirect"}},
			Findings: []classify.Finding{{Severity: "medium", Code: "http-admin"}}},
		{IP: "10.0.0.3", Status: "down"},
		{IP: "10.0.0.100", Status: "up", MACRandomized: true, Type: "Private-MAC device (phone, laptop or VM)"},
	}
}

func TestSortHosts(t *testing.T) {
	h := sample()
	SortHosts(h)
	if h[0].IP != "10.0.0.3" || h[1].IP != "10.0.0.20" || h[2].IP != "10.0.0.100" {
		t.Fatalf("order %s %s %s", h[0].IP, h[1].IP, h[2].IP)
	}
}

func TestCSVNeutralizesFormulas(t *testing.T) {
	var buf bytes.Buffer
	if err := CSV(&buf, sample()); err != nil {
		t.Fatal(err)
	}
	rows, err := csv.NewReader(&buf).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 4 || len(rows[0]) != len(CSVHeader) {
		t.Fatalf("rows %d cols %d", len(rows), len(rows[0]))
	}
	for _, row := range rows[1:] {
		for i, cell := range row {
			if cell != "" && strings.ContainsRune("=+-@", rune(cell[0])) {
				t.Errorf("column %s starts with a formula character: %q", CSVHeader[i], cell)
			}
		}
	}
	if rows[1][13] != "80 9100" {
		t.Fatalf("open_ports %q", rows[1][13])
	}
}

func TestTableAndJSON(t *testing.T) {
	var buf bytes.Buffer
	if err := Table(&buf, sample(), false); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if strings.Contains(out, "10.0.0.3 ") || !strings.Contains(out, "(private MAC)") || !strings.Contains(out, "M:http-admin") {
		t.Fatalf("table:\n%s", out)
	}
	buf.Reset()
	if err := JSON(&buf, Document{Tool: "ztg-scanner", Hosts: sample()}); err != nil {
		t.Fatal(err)
	}
	var back Document
	if err := json.Unmarshal(buf.Bytes(), &back); err != nil || len(back.Hosts) != 3 {
		t.Fatalf("round trip: %v, %d hosts", err, len(back.Hosts))
	}
}
