package main

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/zerotrustgarage/tools/scanner/internal/openwith"
	"github.com/zerotrustgarage/tools/scanner/internal/scan"
)

const testPutty = `C:\Program Files\PuTTY\putty.exe`

func openWithApp(t *testing.T, hosts ...scan.Host) (*App, *fakePlatform) {
	t.Helper()
	a := NewApp()
	f := newFake("")
	a.setPlatform(context.Background(), f)
	a.hosts = hosts
	_, err := a.SetOpenWithCommands([]openwith.Command{
		{Name: "PuTTY", Kind: openwith.KindProgram, Program: testPutty, Args: []string{"-ssh", "{hostname}"}},
		{Name: "RDP", Kind: openwith.KindProgram, Program: `C:\Windows\System32\mstsc.exe`, Args: []string{"/v:{ip}"}},
		{Name: "Console", Kind: openwith.KindURL, URL: "https://rmm.example.com/c?ip={ip}&h={hostname}"},
	})
	if err != nil {
		t.Fatal(err)
	}
	return a, f
}

func TestOpenWithLaunchesExactArgv(t *testing.T) {
	a, f := openWithApp(t, scan.Host{IP: "10.0.0.5", Status: "up", DNSName: "srv-01.lab.lan", Name: "ignored"})
	msg, err := a.OpenWith("PuTTY", "10.0.0.5", false)
	if err != nil || !strings.Contains(msg, "10.0.0.5") {
		t.Fatal(msg, err)
	}
	if _, err := a.OpenWith("RDP", "10.0.0.5", false); err != nil {
		t.Fatal(err)
	}
	want := [][]string{{testPutty, "-ssh", "srv-01.lab.lan"}, {`C:\Windows\System32\mstsc.exe`, "/v:10.0.0.5"}}
	if !reflect.DeepEqual(f.programs, want) {
		t.Fatalf("got %q want %q", f.programs, want)
	}
	if len(f.opened) != 0 {
		t.Fatal("a program command opened a URL", f.opened)
	}
	if _, err := a.OpenWith("Console", "10.0.0.5", false); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(f.opened, []string{"https://rmm.example.com/c?ip=10.0.0.5&h=srv-01.lab.lan"}) || len(f.programs) != 2 {
		t.Fatal(f.opened, f.programs)
	}
}

func TestOpenWithUsesBestNameWhenNoDNSName(t *testing.T) {
	a, f := openWithApp(t, scan.Host{IP: "10.0.0.6", Status: "up", Name: "FRONTDESK-PC"})
	if _, err := a.OpenWith("PuTTY", "10.0.0.6", false); err != nil || f.programs[0][2] != "FRONTDESK-PC" {
		t.Fatal(f.programs, err)
	}
}

func TestOpenWithDeviceTextNeverBecomesArguments(t *testing.T) {
	hostile := []string{"-oProxyCommand=calc", "a b", "a;b", "a&b", `a"b`, "hôst", "UPnP Friendly Name", "host\n-x", "x.y."}
	for _, name := range hostile {
		a, f := openWithApp(t, scan.Host{IP: "10.0.0.7", Status: "up", DNSName: name, Name: "fallback"})
		_, err := a.OpenWith("PuTTY", "10.0.0.7", false)
		var ue *openwith.UnavailableError
		if !errors.As(err, &ue) {
			t.Fatalf("%q: want unavailable, got %v", name, err)
		}
		if _, err := a.OpenWith("Console", "10.0.0.7", false); !errors.As(err, &ue) {
			t.Fatalf("%q: URL command not unavailable: %v", name, err)
		}
		if len(f.programs) != 0 || len(f.opened) != 0 {
			t.Fatalf("%q reached the platform: %v %v", name, f.programs, f.opened)
		}
		// A command that does not use {hostname} still works for that device.
		if _, err := a.OpenWith("RDP", "10.0.0.7", false); err != nil {
			t.Fatalf("%q: RDP refused: %v", name, err)
		}
		opts, err := a.OpenWithOptions("10.0.0.7")
		if err != nil || len(opts) != 3 || opts[0].Available || opts[0].Reason == "" || !opts[1].Available || opts[2].Available {
			t.Fatalf("%q: %+v %v", name, opts, err)
		}
	}
}

func TestOpenWithGuards(t *testing.T) {
	a, f := openWithApp(t,
		scan.Host{IP: "10.0.0.5", Status: "up", DNSName: "srv"},
		scan.Host{IP: "8.8.8.8", Status: "up", DNSName: "dns"})
	if _, err := a.OpenWith("PuTTY", "10.0.0.99", false); err == nil {
		t.Fatal("address outside the results accepted")
	}
	if _, err := a.OpenWith("Nope", "10.0.0.5", false); err == nil {
		t.Fatal("unsaved command accepted")
	}
	// A command name is a key, never a command: only saved definitions run.
	if _, err := a.OpenWith(testPutty, "10.0.0.5", false); err == nil {
		t.Fatal("a path was accepted as a command name")
	}
	if _, err := a.OpenWith("PuTTY", "8.8.8.8", false); err == nil || !strings.Contains(err.Error(), "public address space") {
		t.Fatal("public address launched without authorization:", err)
	}
	if len(f.programs) != 0 {
		t.Fatal("launched")
	}
	if _, err := a.OpenWith("PuTTY", "8.8.8.8", true); err != nil {
		t.Fatal(err)
	}
	f.launchErr = errors.New("not found")
	if _, err := a.OpenWith("PuTTY", "10.0.0.5", false); err == nil {
		t.Fatal("launch failure was hidden")
	}
	if _, err := NewApp().OpenWith("PuTTY", "10.0.0.5", false); err == nil {
		t.Fatal("works without a platform")
	}
}

func TestSetOpenWithCommandsRefusesWholeInvalidList(t *testing.T) {
	a, _ := openWithApp(t, scan.Host{IP: "10.0.0.5", Status: "up", DNSName: "srv"})
	_, err := a.SetOpenWithCommands([]openwith.Command{
		{Name: "ok", Kind: openwith.KindURL, URL: "https://rmm.example.com/"},
		{Name: "shell", Kind: openwith.KindProgram, Program: `C:\Windows\System32\cmd.exe`, Args: []string{"/c", "{ip}"}},
	})
	if err == nil {
		t.Fatal("list with a shell accepted")
	}
	opts, _ := a.OpenWithOptions("10.0.0.5")
	if len(opts) != 3 {
		t.Fatalf("previous list was replaced by an invalid one: %+v", opts)
	}
	got, err := a.SetOpenWithCommands(nil)
	if err != nil || len(got) != 0 {
		t.Fatal(got, err)
	}
	if opts, _ := a.OpenWithOptions("10.0.0.5"); len(opts) != 0 {
		t.Fatal("clearing the list failed")
	}
	if _, err := a.OpenWithOptions("10.9.9.9"); err == nil {
		t.Fatal("options for an address outside the results")
	}
}

func TestPreviewOpenWith(t *testing.T) {
	a := NewApp()
	p, err := a.PreviewOpenWith(openwith.Command{Name: "x", Kind: openwith.KindProgram, Program: testPutty, Args: []string{"-ssh", "{hostname}", "{ip}"}}, "10.0.0.5", "srv.lan")
	if err != nil || !reflect.DeepEqual(p.Argv, []string{testPutty, "-ssh", "srv.lan", "10.0.0.5"}) {
		t.Fatal(p, err)
	}
	p, err = a.PreviewOpenWith(openwith.Command{Name: "x", Kind: openwith.KindURL, URL: "https://rmm.example.com/c?h={hostname}"}, "10.0.0.5", "srv.lan")
	if err != nil || p.URL != "https://rmm.example.com/c?h=srv.lan" || p.Argv != nil {
		t.Fatal(p, err)
	}
	if _, err := a.PreviewOpenWith(openwith.Command{Name: "x", Kind: openwith.KindProgram, Program: testPutty, Args: []string{"{hostname}"}}, "10.0.0.5", "-bad"); err == nil {
		t.Fatal("hostile test hostname previewed")
	}
	// A preview starts nothing and needs no platform.
	if a.platform != nil {
		t.Fatal("unexpected platform")
	}
}
