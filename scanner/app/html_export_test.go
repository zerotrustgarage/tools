package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zerotrustgarage/tools/scanner/internal/scan"
)

type exportPlatform struct {
	*fakePlatform
	name, filter, pattern string
	beforeReturn          func()
	err                   error
}

func (p *exportPlatform) SaveFile(name, filter, pattern string) (string, error) {
	p.name, p.filter, p.pattern = name, filter, pattern
	if p.beforeReturn != nil {
		p.beforeReturn()
	}
	return p.saveTo, p.err
}

func seedHTMLInventory(a *App) {
	a.hosts = []scan.Host{
		{IP: "192.0.2.1", Status: "up", Name: "router", Annotation: scan.Annotation{Notes: "technician note", Tags: []string{"inventory"}}},
		{IP: "192.0.2.2", Status: "down", LastKnownMAC: "00:11:22:33:44:55"},
	}
	a.site = "Clinic"
	a.lastReq = []string{"192.0.2.0/24"}
	a.summary = &scan.Summary{Total: 2, Up: 1}
	a.annotations = map[string]scan.Annotation{"key": {Notes: "technician note", Tags: []string{"inventory"}}}
}

func TestHTMLExportIncludesAllAndSnapshotBeforeChooser(t *testing.T) {
	path := filepath.Join(t.TempDir(), "report.html")
	a := NewApp()
	seedHTMLInventory(a)
	p := &exportPlatform{fakePlatform: newFake(path)}
	p.beforeReturn = func() {
		a.mu.Lock()
		a.hosts = []scan.Host{{IP: "198.51.100.1", Status: "up", Name: "new inventory"}}
		a.site = "Other client"
		a.mu.Unlock()
	}
	a.setPlatform(nil, p)

	got, err := a.Export("html", false)
	if err != nil || got != path {
		t.Fatalf("Export = %q, %v", got, err)
	}
	if p.filter != "HTML (*.html)" || p.pattern != "*.html" || !strings.HasSuffix(p.name, ".html") {
		t.Fatalf("unexpected dialog: %q %q %q", p.name, p.filter, p.pattern)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Clinic", "192.0.2.1", "192.0.2.2", "technician note", "inventory"} {
		if !strings.Contains(string(b), want) {
			t.Errorf("HTML report missing %q", want)
		}
	}
	if strings.Contains(string(b), "198.51.100.1") || strings.Contains(string(b), "Other client") {
		t.Fatal("export mixed in inventory changed while the chooser was open")
	}
}

func TestHTMLExportNoResultsCancelAndDialogFailure(t *testing.T) {
	a := NewApp()
	p := &exportPlatform{fakePlatform: newFake("")}
	a.setPlatform(nil, p)
	if _, err := a.Export("html", false); err == nil || p.name != "" {
		t.Fatalf("empty export: dialog=%q error=%v", p.name, err)
	}

	a.mu.Lock()
	seedHTMLInventory(a)
	a.mu.Unlock()
	if path, err := a.Export("html", false); err != nil || path != "" {
		t.Fatalf("canceled chooser: %q %v", path, err)
	}
	p.err = errors.New("chooser failed")
	if _, err := a.Export("html", false); err == nil || err.Error() != "chooser failed" {
		t.Fatalf("dialog failure: %v", err)
	}
}

func TestHTMLExportWriterFailurePreservesExistingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "existing.html")
	const old = "keep this previous report"
	if err := os.WriteFile(path, []byte(old), 0600); err != nil {
		t.Fatal(err)
	}
	a := NewApp()
	a.hosts = []scan.Host{{IP: "192.0.2.1", Status: "up", Annotation: scan.Annotation{Notes: strings.Repeat("x", 65<<20)}}}
	a.setPlatform(nil, newFake(path))
	if _, err := a.Export("html", false); err == nil || !strings.Contains(err.Error(), "size limit") {
		t.Fatalf("expected bounded writer failure, got %v", err)
	}
	b, err := os.ReadFile(path)
	if err != nil || string(b) != old {
		t.Fatalf("existing file changed: %q, %v", b, err)
	}
}

func TestHTMLExportRunningSnapshotMarkedPartial(t *testing.T) {
	path := filepath.Join(t.TempDir(), "partial.html")
	a := NewApp()
	seedHTMLInventory(a)
	a.running = true
	started := time.Date(2026, 10, 23, 10, 11, 12, 0, time.UTC)
	a.summary = &scan.Summary{Started: started, Total: 254}
	a.setPlatform(nil, newFake(path))
	if _, err := a.Export("html", false); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "Canceled / partial") || !strings.Contains(string(b), "Incomplete") {
		t.Fatal("running export did not retain its incomplete state")
	}
	if !strings.Contains(string(b), "10:11:12") || !strings.Contains(string(b), "<strong>254</strong>") {
		t.Fatal("running export lost the requested scope or start time")
	}
}
