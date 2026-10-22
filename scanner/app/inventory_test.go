package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zerotrustgarage/tools/scanner/internal/inventory"
	"github.com/zerotrustgarage/tools/scanner/internal/probe"
	"github.com/zerotrustgarage/tools/scanner/internal/scan"
)

func TestInventoryRoundTripAndInvalidLoadPreservesState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "site.json")
	a, f := NewApp(), newFake(path)
	a.setPlatform(context.Background(), f)
	a.hosts = []scan.Host{{IP: "10.0.0.1", Status: "up", MAC: "00:11:22:33:44:55"}, {IP: "10.0.0.2", Status: "down"}}
	a.lastReq = []string{"10.0.0.0/24"}
	a.summary = &scan.Summary{Finished: time.Now()}
	note := scan.Annotation{Notes: "<b>Owner Alice</b>", Tags: []string{"cui:inside"}, Favorite: true}
	if err := a.SetAnnotation("10.0.0.1", note); err != nil {
		t.Fatal(err)
	}
	if _, err := a.SaveScan("Clinic"); err != nil {
		t.Fatal(err)
	}
	if a.UnsavedChanges() {
		t.Fatal("saved inventory still marked dirty")
	}
	if err := a.NewSite(); err != nil {
		t.Fatal(err)
	}
	if _, err := a.LoadScan(); err != nil {
		t.Fatal(err)
	}
	s := a.Results(true)
	if len(s.Hosts) != 2 || s.Site != "Clinic" || !s.Hosts[0].Favorite || s.Hosts[0].Notes != note.Notes || len(s.Targets) != 1 || a.allowPub {
		t.Fatal(s)
	}
	if a.UnsavedChanges() {
		t.Fatal("loaded inventory marked dirty")
	}
	if _, err := a.CompareScan("Clinic"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.CompareScan("Other client"); err == nil {
		t.Fatal("cross-client comparison accepted")
	}
	if err := os.WriteFile(path, []byte(`{"tool":"ztg-scanner","hosts":[{"ip":"bad"}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := a.LoadScan(); err == nil {
		t.Fatal("invalid scan accepted")
	}
	if len(a.Results(true).Hosts) != 2 {
		t.Fatal("invalid load destroyed results")
	}
	a.running = true
	if err := a.NewSite(); err == nil {
		t.Fatal("cleared an active scan")
	}
	if err := a.SetAnnotation("10.0.0.1", note); err == nil {
		t.Fatal("edited an active scan")
	}
	if _, err := a.LoadScan(); err == nil {
		t.Fatal("replaced an active scan")
	}
}

func TestActionsValidateScopeAndServices(t *testing.T) {
	a, f := NewApp(), newFake("")
	a.setPlatform(context.Background(), f)
	a.hosts = []scan.Host{{IP: "10.0.0.1", Status: "up", MAC: "00:11:22:33:44:55", Services: []probe.Service{{Port: 22}}}, {IP: "8.8.8.8", Status: "up"}}
	for _, ip := range []string{"10.0.0.2", "10.0.0.1 & calc.exe", "-oProxyCommand=whoami"} {
		if _, err := a.DeviceAction("ssh", ip, false); err == nil {
			t.Fatal("unsafe address accepted", ip)
		}
	}
	if _, err := a.DeviceAction("rdp", "10.0.0.1", false); err == nil {
		t.Fatal("unobserved RDP service accepted")
	}
	if _, err := a.DeviceAction("ssh", "10.0.0.1", false); err != nil {
		t.Fatal(err)
	}
	if _, err := a.DeviceAction("ping", "8.8.8.8", false); err == nil || !strings.Contains(err.Error(), "public address space") {
		t.Fatal(err)
	}
	if _, err := a.DeviceAction("ping", "8.8.8.8", true); err != nil {
		t.Fatal(err)
	}
	if _, err := a.DeviceAction("arbitrary", "10.0.0.1", true); err == nil {
		t.Fatal("arbitrary command accepted")
	}
	for _, action := range []string{"ping", "traceroute", "ssh", "rdp"} {
		_, args, err := actionCommand(action, "10.0.0.1")
		if err != nil || len(args) == 0 {
			t.Fatal(action, err)
		}
	}
	a.wake = func(ip, mac string) error {
		if ip != "10.0.0.1" || mac != a.hosts[0].MAC {
			t.Fatal(ip, mac)
		}
		return nil
	}
	if err := a.WakeOnLAN("10.0.0.1"); err != nil {
		t.Fatal(err)
	}
	if err := a.WakeOnLAN("10.0.0.2"); err == nil {
		t.Fatal("unknown device wake accepted")
	}
	if !inventory.ValidKey(inventory.AnnotationKey(a.hosts[0])) {
		t.Fatal("invalid generated annotation key")
	}
}

type blockingPicker struct {
	*fakePlatform
	shown, resume chan struct{}
}

func (f *blockingPicker) SaveFile(string, string, string) (string, error) {
	close(f.shown)
	<-f.resume
	return f.saveTo, nil
}

func TestSaveCannotMarkLaterEditsClean(t *testing.T) {
	path := filepath.Join(t.TempDir(), "site.json")
	f := &blockingPicker{fakePlatform: newFake(path), shown: make(chan struct{}), resume: make(chan struct{})}
	a := NewApp()
	a.setPlatform(context.Background(), f)
	a.hosts = []scan.Host{{IP: "10.0.0.1", Status: "up"}}
	if err := a.SetAnnotation("10.0.0.1", scan.Annotation{Notes: "original"}); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := a.SaveScan("Clinic"); done <- err }()
	select {
	case <-f.shown:
	case <-time.After(3 * time.Second):
		t.Fatal("save dialog not shown")
	}
	if err := a.SetAnnotation("10.0.0.1", scan.Annotation{Notes: "later edit"}); err != nil {
		t.Fatal(err)
	}
	close(f.resume)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if !a.UnsavedChanges() {
		t.Fatal("later edit lost its unsaved marker")
	}
	b, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(b), "original") || strings.Contains(string(b), "later edit") {
		t.Fatal("saved a mixture of revisions", err)
	}
}
