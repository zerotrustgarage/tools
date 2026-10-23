package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/zerotrustgarage/tools/scanner/internal/scan"
)

type targetListPlatform struct {
	*fakePlatform
	path                   string
	title, filter, pattern string
	dialogErr              error
}

func (p *targetListPlatform) OpenFile(title, filter, pattern string) (string, error) {
	p.title, p.filter, p.pattern = title, filter, pattern
	return p.path, p.dialogErr
}

func TestValidateTargetsIsPureSyntaxNormalization(t *testing.T) {
	a := NewApp()
	a.lastReq = []string{"existing"}
	a.allowPub = true
	a.hosts = []scan.Host{{IP: "10.0.0.1"}}
	a.summary = &scan.Summary{Total: 1}
	beforeHosts, beforeReq, beforeSummary := a.hosts, a.lastReq, a.summary

	got, err := a.ValidateTargets("8.8.8.8; 203.0.113.1 # public")
	if err != nil || got != "8.8.8.8, 203.0.113.1" {
		t.Fatalf("ValidateTargets() = %q, %v", got, err)
	}
	if a.hosts[0].IP != beforeHosts[0].IP || a.lastReq[0] != beforeReq[0] || !a.allowPub || a.summary != beforeSummary {
		t.Fatal("ValidateTargets mutated app state")
	}
}

func TestImportTargetsReadsBoundedTextAndDoesNotMutate(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "targets.txt")
	if err := os.WriteFile(path, []byte("\uFEFF10.0.0.1, printer.lan # room\n"), 0600); err != nil {
		t.Fatal(err)
	}
	a := NewApp()
	a.lastReq = []string{"old"}
	a.allowPub = true
	a.hosts = []scan.Host{{IP: "10.0.0.2"}}
	p := &targetListPlatform{fakePlatform: newFake(""), path: path}
	a.setPlatform(context.Background(), p)

	got, err := a.ImportTargets()
	if err != nil || got != "10.0.0.1, printer.lan" {
		t.Fatalf("ImportTargets() = %q, %v", got, err)
	}
	if p.title != "Import target list" || p.filter != "Text files (*.txt)" || p.pattern != "*.txt" {
		t.Fatalf("unexpected file dialog settings: %q %q %q", p.title, p.filter, p.pattern)
	}
	if len(a.hosts) != 1 || a.hosts[0].IP != "10.0.0.2" || a.lastReq[0] != "old" || !a.allowPub {
		t.Fatal("ImportTargets mutated app state")
	}
}

func TestImportTargetsCancellationAndFailures(t *testing.T) {
	a := NewApp()
	p := &targetListPlatform{fakePlatform: newFake(""), path: ""}
	a.setPlatform(context.Background(), p)
	if got, err := a.ImportTargets(); err != nil || got != "" {
		t.Fatalf("cancel = %q, %v", got, err)
	}

	missing := &targetListPlatform{fakePlatform: newFake(""), path: filepath.Join(t.TempDir(), "missing.txt")}
	a.setPlatform(context.Background(), missing)
	if _, err := a.ImportTargets(); err == nil {
		t.Fatal("missing file accepted")
	}

	dirPath := t.TempDir()
	directory := &targetListPlatform{fakePlatform: newFake(""), path: dirPath}
	a.setPlatform(context.Background(), directory)
	if _, err := a.ImportTargets(); err == nil {
		t.Fatal("directory accepted")
	}

	largePath := filepath.Join(t.TempDir(), "large.txt")
	if err := os.WriteFile(largePath, make([]byte, 64*1024+1), 0600); err != nil {
		t.Fatal(err)
	}
	large := &targetListPlatform{fakePlatform: newFake(""), path: largePath}
	a.setPlatform(context.Background(), large)
	if _, err := a.ImportTargets(); err == nil {
		t.Fatal("oversized file accepted")
	}

	badPath := filepath.Join(t.TempDir(), "bad.txt")
	if err := os.WriteFile(badPath, []byte("https://example.com"), 0600); err != nil {
		t.Fatal(err)
	}
	bad := &targetListPlatform{fakePlatform: newFake(""), path: badPath}
	a.setPlatform(context.Background(), bad)
	if _, err := a.ImportTargets(); err == nil {
		t.Fatal("invalid target file accepted")
	}

	dialog := &targetListPlatform{fakePlatform: newFake(""), dialogErr: os.ErrPermission}
	a.setPlatform(context.Background(), dialog)
	if _, err := a.ImportTargets(); err == nil {
		t.Fatal("dialog error ignored")
	}
}

func TestPublicTargetsDoNotAuthorizeStartScan(t *testing.T) {
	path := filepath.Join(t.TempDir(), "public.txt")
	if err := os.WriteFile(path, []byte("8.8.8.8"), 0600); err != nil {
		t.Fatal(err)
	}
	a := NewApp()
	p := &targetListPlatform{fakePlatform: newFake(""), path: path}
	a.setPlatform(context.Background(), p)
	validated, err := a.ValidateTargets("8.8.8.8")
	if err != nil || validated != "8.8.8.8" {
		t.Fatalf("ValidateTargets() = %q, %v", validated, err)
	}
	imported, err := a.ImportTargets()
	if err != nil || imported != "8.8.8.8" {
		t.Fatalf("ImportTargets() = %q, %v", imported, err)
	}
	if _, err := a.StartScan(ScanRequest{Targets: imported}); err == nil {
		t.Fatal("imported public target started without a fresh authorization")
	}
	if a.allowPub || a.running || len(a.hosts) != 0 {
		t.Fatal("target validation or import changed scan authorization or state")
	}
}
