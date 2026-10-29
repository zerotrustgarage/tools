package inventory

import (
	"reflect"
	"testing"

	"github.com/zerotrustgarage/tools/scanner/internal/scan"
)

func v6host(ip, mac string, addrs ...string) scan.Host {
	h := host(ip, mac)
	h.IPv6 = addrs
	return h
}

func kinds(changes []Change) map[string]string {
	out := map[string]string{}
	for _, c := range changes {
		ip := ""
		if c.After != nil {
			ip = c.After.IP
		} else {
			ip = c.Before.IP
		}
		out[ip] = c.Kind
	}
	return out
}

func TestCompareMatchesIPv6OnlyDevicesByMAC(t *testing.T) {
	// The same device, scanned from a computer that names its interface
	// differently: matched by its unique MAC, and the zone alone is no change.
	before := []scan.Host{v6host("fe80::2a%eth0", "02:aa:bb:cc:dd:01", "fe80::2a%eth0", "fd00::2a")}
	after := []scan.Host{v6host("fe80::2a%12", "02:aa:bb:cc:dd:01", "fe80::2a%12", "fd00::2a")}
	got := Compare(before, after, false)
	if len(got) != 1 || got[0].Kind != "unchanged" {
		t.Fatalf("%+v", got)
	}
	// Its link-local address moved: the same MAC still pairs it, and the move is reported.
	after = []scan.Host{v6host("fe80::99%eth0", "02:aa:bb:cc:dd:01", "fe80::99%eth0", "fd00::2a")}
	got = Compare(before, after, false)
	if len(got) != 1 || got[0].Kind != "changed" || !reflect.DeepEqual(got[0].Fields, []string{"ip", "ipv6"}) {
		t.Fatalf("%+v", got)
	}
}

func TestCompareIPv6NoiseIsNotAChange(t *testing.T) {
	a := v6host("10.0.0.1", "00:11:22:00:00:01", "fe80::1%eth0", "2001:db8::aaaa", "fd00::1")
	// Privacy extensions rotate global and unique local addresses; the link-local one is stable.
	b := v6host("10.0.0.1", "00:11:22:00:00:01", "fe80::1%12", "2001:db8::bbbb", "fd00::7")
	if c := Compare([]scan.Host{a}, []scan.Host{b}, false); len(c) != 1 || c[0].Kind != "unchanged" {
		t.Fatalf("rotation reported: %+v", c)
	}
	// IPv6 discovery was off in one scan: that is not a change either way.
	off := host("10.0.0.1", "00:11:22:00:00:01")
	for _, pair := range [][2]scan.Host{{a, off}, {off, a}} {
		if c := Compare([]scan.Host{pair[0]}, []scan.Host{pair[1]}, false); len(c) != 1 || c[0].Kind != "unchanged" {
			t.Fatalf("one-sided IPv6 evidence reported: %+v", c)
		}
	}
	// A device with only a global address in one scan and only a link-local in the other has no common evidence.
	g := v6host("10.0.0.1", "00:11:22:00:00:01", "2001:db8::1")
	if c := Compare([]scan.Host{g}, []scan.Host{a}, false); c[0].Kind != "unchanged" {
		t.Fatalf("%+v", c)
	}
}

func TestCompareReportsAChangedLinkLocalAddress(t *testing.T) {
	a := v6host("10.0.0.1", "00:11:22:00:00:01", "fe80::1%eth0")
	b := v6host("10.0.0.1", "00:11:22:00:00:01", "fe80::2%eth0")
	c := Compare([]scan.Host{a}, []scan.Host{b}, false)
	if len(c) != 1 || c[0].Kind != "changed" || !reflect.DeepEqual(c[0].Fields, []string{"ipv6"}) {
		t.Fatalf("%+v", c)
	}
}

func TestIPv6OnlyDevicesAreNeverCalledMissing(t *testing.T) {
	// Only an address that was visited and silent is missing. An IPv6-only
	// device is found, never visited, so one absent from the later scan is
	// "not assessed", and a new one is new.
	before := []scan.Host{v6host("fe80::2a%eth0", "02:aa:bb:cc:dd:01", "fe80::2a%eth0")}
	after := []scan.Host{v6host("fe80::3b%eth0", "02:aa:bb:cc:dd:02", "fe80::3b%eth0")}
	got := kinds(Compare(before, after, false))
	if got["fe80::2a%eth0"] != "not_scanned" || got["fe80::3b%eth0"] != "new" {
		t.Fatalf("%v", got)
	}
}

func TestCompareSortsIPv4BeforeIPv6WithoutPanicking(t *testing.T) {
	after := []scan.Host{v6host("fe80::2a%eth0", "02:aa:bb:cc:dd:01"), host("10.0.0.9", "00:11:22:00:00:09"), v6host("fd00::5", "02:aa:bb:cc:dd:05")}
	got := Compare(nil, after, false)
	if len(got) != 3 || got[0].After.IP != "10.0.0.9" {
		t.Fatalf("%+v", got)
	}
}

func TestKeysAndValidationForIPv6(t *testing.T) {
	h := v6host("fe80::2a%eth0", "02:aa:bb:cc:dd:01")
	h.MACRandomized = true // a randomized MAC is not stable: the key carries the address
	if got := Key(h); got != "ip:fe80::2a%eth0|02:aa:bb:cc:dd:01" {
		t.Fatal(got)
	}
	if !ValidKey(Key(h)) {
		t.Fatal("a key written for an IPv6 device is refused")
	}
	stable := v6host("fd00::5", "00:11:22:33:44:55")
	if Key(stable) != "mac:00:11:22:33:44:55" || !ValidKey(Key(stable)) {
		t.Fatal(Key(stable))
	}
	for _, bad := range []string{
		"ip:FE80::2A|", "ip:fe80::1%a b|", "ip:2001:db8::1%eth0|", "ip:::ffff:10.0.0.1|", "ip:ff02::1|", "ip:fe80::1|zz", "ip:|",
	} {
		if ValidKey(bad) {
			t.Errorf("accepted %q", bad)
		}
	}
	for _, good := range []string{"ip:fe80::1%eth0|", "ip:fe80::1|02:00:00:00:00:01", "ip:2001:db8::1|", "ip:10.0.0.1|"} {
		if !ValidKey(good) {
			t.Errorf("refused %q", good)
		}
	}
}

func TestAnnotationsFollowAnIPv6OnlyDeviceByMAC(t *testing.T) {
	m := "00:11:22:33:44:55"
	before := []scan.Host{v6host("fe80::2a%eth0", m)}
	notes := map[string]scan.Annotation{AnnotationKey(before[0]): {Notes: "badge reader", Tags: []string{"door"}}}
	after := []scan.Host{v6host("fe80::77%eth0", m)}
	ApplyAnnotations(before, after, notes)
	if after[0].Notes != "badge reader" {
		t.Fatalf("%+v", after[0].Annotation)
	}
}
