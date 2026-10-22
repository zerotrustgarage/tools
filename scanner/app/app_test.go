package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zerotrustgarage/tools/scanner/internal/scan"
)

type fakePlatform struct {
	mu     sync.Mutex
	events map[string]int
	done   chan scan.Summary
	saveTo string
	opened []string
}

func newFake(saveTo string) *fakePlatform {
	return &fakePlatform{events: map[string]int{}, done: make(chan scan.Summary, 1), saveTo: saveTo}
}

func (f *fakePlatform) Emit(event string, data any) {
	f.mu.Lock()
	f.events[event]++
	f.mu.Unlock()
	if event == EventDone {
		f.done <- data.(scan.Summary)
	}
}
func (f *fakePlatform) SaveFile(string, string, string) (string, error) { return f.saveTo, nil }
func (f *fakePlatform) OpenURL(u string)                                { f.opened = append(f.opened, u) }

func webServer(t *testing.T) (*httptest.Server, string) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "<title>Lab Device</title>")
	}))
	_, port, _ := net.SplitHostPort(srv.Listener.Addr().String())
	return srv, port
}

func runScan(t *testing.T, a *App, f *fakePlatform, req ScanRequest) scan.Summary {
	t.Helper()
	n, err := a.StartScan(req)
	if err != nil {
		t.Fatal(err)
	}
	if n < 1 {
		t.Fatalf("StartScan returned %d targets", n)
	}
	select {
	case s := <-f.done:
		return s
	case <-time.After(30 * time.Second):
		t.Fatal("scan did not finish")
	}
	return scan.Summary{}
}

func TestScanExportAndOpen(t *testing.T) {
	srv, port := webServer(t)
	defer srv.Close()
	out := filepath.Join(t.TempDir(), "out.json")
	a, f := NewApp(), newFake(out)
	a.setPlatform(context.Background(), f)

	sum := runScan(t, a, f, ScanRequest{Targets: "127.0.0.1", Ports: port, Probes: scan.Probes{Ports: true, Banners: true}})
	if sum.Up != 1 {
		t.Fatalf("summary %+v", sum)
	}
	f.mu.Lock()
	hostEvents, progEvents := f.events[EventHost], f.events[EventProgress]
	f.mu.Unlock()
	if hostEvents != 1 || progEvents < 1 {
		t.Fatalf("events host=%d progress=%d", hostEvents, progEvents)
	}
	snap := a.Results(false)
	if snap.Running || len(snap.Hosts) != 1 || snap.Summary == nil {
		t.Fatalf("snapshot %+v", snap)
	}

	// Export JSON through the (fake) save dialog.
	path, err := a.Export("json", false)
	if err != nil || path != out {
		t.Fatalf("export: %q %v", path, err)
	}
	b, _ := os.ReadFile(out)
	var doc struct {
		Hosts []scan.Host `json:"hosts"`
	}
	if err := json.Unmarshal(b, &doc); err != nil || len(doc.Hosts) != 1 {
		t.Fatalf("exported JSON: %v, %s", err, b)
	}
	if _, err := a.Export("xml", false); err == nil {
		t.Fatal("unknown format accepted")
	}

	// Opening a browser is limited to hosts and ports in the results.
	if err := a.OpenWeb("127.0.0.1", mustAtoi(port)); err != nil {
		t.Fatal(err)
	}
	if len(f.opened) != 1 || f.opened[0] != "http://127.0.0.1:"+port+"/" {
		t.Fatalf("opened %v", f.opened)
	}
	if err := a.OpenWeb("127.0.0.1", 1); err == nil {
		t.Fatal("port not in results accepted")
	}
	if err := a.OpenWeb("10.9.9.9", mustAtoi(port)); err == nil {
		t.Fatal("host not in results accepted")
	}
}

func TestStartScanValidation(t *testing.T) {
	a, f := NewApp(), newFake("")
	a.setPlatform(context.Background(), f)
	cases := map[string]ScanRequest{
		"empty":       {Targets: "  "},
		"public":      {Targets: "8.8.8.8"},
		"too big":     {Targets: "10.0.0.0/8"},
		"bad ports":   {Targets: "127.0.0.1", Ports: "99999"},
		"bad address": {Targets: "not_a_host!"},
	}
	for name, req := range cases {
		if _, err := a.StartScan(req); err == nil {
			t.Errorf("%s: want error", name)
		}
	}
}

func TestOneScanAtATime(t *testing.T) {
	a, f := NewApp(), newFake("")
	a.setPlatform(context.Background(), f)
	// TEST-NET-2 addresses never answer, so this scan stays busy until stopped.
	if _, err := a.StartScan(ScanRequest{Targets: "198.51.100.0/28", AllowPublic: true, Probes: scan.Probes{Ports: true}}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.StartScan(ScanRequest{Targets: "127.0.0.1"}); err == nil || !strings.Contains(err.Error(), "already running") {
		t.Fatalf("second scan: %v", err)
	}
	a.StopScan()
	select {
	case s := <-f.done:
		if !s.Canceled {
			t.Fatalf("summary %+v", s)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("stop did not end the scan")
	}
}

func TestDefaultsAndSuggestions(t *testing.T) {
	a := NewApp()
	d := a.Defaults()
	if d.Concurrency != 128 || !d.Probes.Ports || !strings.Contains(d.Ports, "443") {
		t.Fatalf("defaults %+v", d)
	}
	for _, s := range a.Suggestions() {
		if s.Range == "" || s.Address == "" {
			t.Fatalf("suggestion %+v", s)
		}
	}
}

func mustAtoi(s string) int {
	var n int
	fmt.Sscan(s, &n)
	return n
}

func TestSNMPRequestValidation(t *testing.T) {
	a, f := NewApp(), newFake("")
	a.setPlatform(context.Background(), f)
	base := ScanRequest{Targets: "127.0.0.1", Probes: scan.Probes{SNMP: true}}
	if _, err := a.StartScan(base); err == nil || !strings.Contains(err.Error(), "no credentials") {
		t.Fatalf("missing credentials: %v", err)
	}
	weak := base
	weak.SNMP = &SNMPRequest{Username: "noc", AuthProtocol: "MD5", AuthPassphrase: "authpass1", PrivProtocol: "AES", PrivPassphrase: "privpass1"}
	if _, err := a.StartScan(weak); err == nil || !strings.Contains(err.Error(), "MD5") {
		t.Fatalf("MD5: %v", err)
	}
	d := a.Defaults()
	if len(d.SNMPAuth) == 0 || len(d.SNMPPriv) == 0 {
		t.Fatalf("protocol lists missing: %+v", d)
	}
}

func TestExportTable(t *testing.T) {
	out := filepath.Join(t.TempDir(), "view.csv")
	a, f := NewApp(), newFake(out)
	a.setPlatform(context.Background(), f)
	path, err := a.ExportTable([]string{"Name", "IP address"}, [][]string{{"=evil()", "10.0.0.1"}, {"nas", "10.0.0.2"}})
	if err != nil || path != out {
		t.Fatalf("export: %q %v", path, err)
	}
	b, _ := os.ReadFile(out)
	if got := string(b); got != "Name,IP address\n'=evil(),10.0.0.1\nnas,10.0.0.2\n" {
		t.Fatalf("csv %q", got)
	}
	if _, err := a.ExportTable([]string{"a"}, nil); err == nil {
		t.Fatal("empty view accepted")
	}
	bad := filepath.Join(t.TempDir(), "bad.csv")
	f.saveTo = bad
	if _, err := a.ExportTable([]string{"a", "b"}, [][]string{{"x"}}); err == nil {
		t.Fatal("ragged rows accepted")
	}
	if _, err := os.Stat(bad); err == nil {
		t.Fatal("a file was created for an invalid export")
	}
}
