package inventory

import (
	"reflect"
	"testing"
	"time"

	"github.com/zerotrustgarage/tools/scanner/internal/probe"
	"github.com/zerotrustgarage/tools/scanner/internal/scan"
)

func host(ip, mac string) scan.Host { return scan.Host{IP: ip, MAC: mac, Status: "up", Name: "device"} }

func TestCompareCoverageMovesAndReplacement(t *testing.T) {
	before := []scan.Host{host("10.0.0.1", "00:11:22:00:00:01"), host("10.0.0.2", "00:11:22:00:00:02"), host("10.0.0.3", ""), host("10.0.1.1", "")}
	after := []scan.Host{host("10.0.0.10", before[0].MAC), host("10.0.0.4", ""), {IP: "10.0.0.2", Status: "down"}, host("10.0.0.3", "00:11:22:00:00:03")}
	got := Compare(before, after, false)
	want := map[string]string{"10.0.0.10": "changed", "10.0.0.4": "new", "10.0.0.2": "missing", "10.0.0.3": "changed", "10.0.1.1": "not_scanned"}
	for _, c := range got {
		ip := ""
		if c.After != nil {
			ip = c.After.IP
		} else {
			ip = c.Before.IP
		}
		if want[ip] != c.Kind {
			t.Errorf("%s: %s, want %s", ip, c.Kind, want[ip])
		}
		delete(want, ip)
	}
	if len(want) != 0 {
		t.Fatal(want)
	}
	for _, c := range Compare(before, after, true) {
		if c.Kind == "missing" {
			t.Fatal("canceled scan declared a device missing")
		}
	}
}

func TestCompareIgnoresTimingOrderAndTechnicianNotes(t *testing.T) {
	a := host("10.0.0.1", "00:11:22:00:00:01")
	a.Services = []probe.Service{{Port: 22, Name: "ssh"}, {Port: 80, Name: "http"}}
	b := a
	b.Services = []probe.Service{a.Services[1], a.Services[0]}
	b.RTTMillis = 100
	b.ScannedAt = time.Now()
	b.Notes = "technician edit"
	if c := Compare([]scan.Host{a}, []scan.Host{b}, false); len(c) != 1 || c[0].Kind != "unchanged" {
		t.Fatal(c)
	}
	b.Services = append(b.Services, probe.Service{Port: 23, Name: "telnet"})
	if c := Compare([]scan.Host{a}, []scan.Host{b}, false); !reflect.DeepEqual(c[0].Fields, []string{"services"}) {
		t.Fatal(c)
	}
}

func TestAnnotationsNeverTransferToReplacementOrDuplicateMAC(t *testing.T) {
	a := host("10.0.0.1", "00:11:22:00:00:01")
	note := scan.Annotation{Notes: "owner Alice", Favorite: true}
	notes := map[string]scan.Annotation{AnnotationKey(a): note}
	moved := host("10.0.0.2", a.MAC)
	hs := []scan.Host{moved, host(a.IP, "00:11:22:00:00:03")}
	ApplyAnnotations([]scan.Host{a}, hs, notes)
	if hs[0].Notes != note.Notes || hs[1].Notes != "" {
		t.Fatal(hs)
	}
	updated := note
	updated.Notes = "new owner"
	notes[AnnotationKey(hs[0])] = updated
	returned := []scan.Host{a}
	ApplyAnnotations(hs, returned, notes)
	if returned[0].Notes != updated.Notes {
		t.Fatal("a DHCP return restored obsolete notes")
	}
	duplicates := []scan.Host{host("10.0.0.3", a.MAC), host("10.0.0.4", a.MAC)}
	ApplyAnnotations([]scan.Host{a}, duplicates, notes)
	if duplicates[0].Favorite || duplicates[1].Favorite {
		t.Fatal("duplicate MAC inherited a favorite")
	}
	private := a
	private.MACRandomized = true
	privateMoved := private
	privateMoved.IP = "10.0.0.5"
	if c := Compare([]scan.Host{private}, []scan.Host{privateMoved}, false); len(c) != 2 {
		t.Fatal("randomized MAC inferred a move", c)
	}
}

func TestOfflineFavoritesKeepHistoricalMACAndNotes(t *testing.T) {
	a := host("10.0.0.1", "00:11:22:00:00:01")
	notes := map[string]scan.Annotation{AnnotationKey(a): {Favorite: true, Notes: "owner Alice"}}
	current := []scan.Host{{IP: a.IP, Status: "down"}}
	ApplyAnnotations([]scan.Host{a}, current, notes)
	if current[0].MAC != "" || current[0].LastKnownMAC != a.MAC || !current[0].Favorite {
		t.Fatal(current)
	}
	next := []scan.Host{{IP: a.IP, Status: "down"}}
	ApplyAnnotations(current, next, notes)
	if next[0].Notes != "owner Alice" || next[0].LastKnownMAC != a.MAC {
		t.Fatal("second offline scan lost history", next)
	}
}
