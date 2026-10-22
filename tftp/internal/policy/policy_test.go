package policy

import (
	"net/netip"
	"testing"
	"time"
)

func TestScope(t *testing.T) {
	for _, s := range []string{"10.0.0.1", "172.16.0.1", "192.168.1.2", "100.64.1.1", "169.254.1.2", "127.0.0.1", "::1", "fe80::1", "fc00::1", "::ffff:192.168.1.1"} {
		if !Private(netip.MustParseAddr(s)) {
			t.Fatal(s)
		}
	}
	for _, s := range []string{"0.0.0.0", "::", "8.8.8.8", "224.0.0.1", "ff02::1", "100.128.0.1", "2001:4860:4860::8888"} {
		if Private(netip.MustParseAddr(s)) {
			t.Fatal(s)
		}
	}
	a, p, e := Bindings("127.0.0.1", false)
	if e != nil || len(a) != 1 || !Allowed(a[0], p) {
		t.Fatalf("%v %v %v", a, p, e)
	}
	if _, _, e := Bindings("8.8.8.8", false); e == nil {
		t.Fatal("public bind accepted")
	}
}
func TestLimits(t *testing.T) {
	g := New()
	now := time.Unix(100, 0)
	a := netip.MustParseAddr("10.0.0.1")
	for range 8 {
		if r := g.Acquire(a, now); r != "" {
			t.Fatal(r)
		}
	}
	if r := g.Acquire(a, now); r != "server-busy" {
		t.Fatal(r)
	}
	for range 8 {
		g.Release(a)
	}
	for range 11 {
		if r := g.Acquire(a, now); r != "" {
			t.Fatal(r)
		}
		g.Release(a)
	}
	if r := g.Acquire(a, now); r != "rate-limited" {
		t.Fatal(r)
	}
	if r := g.Acquire(a, now.Add(time.Second)); r != "" {
		t.Fatal(r)
	}
	if !g.Budget(256*1024, now) || g.Budget(1, now) || !g.Budget(1, now.Add(time.Second)) {
		t.Fatal("unverified budget")
	}
	for range 10 {
		if ok, _ := g.Audit(a, now); !ok {
			t.Fatal("early throttle")
		}
	}
	for range 30 {
		if ok, _ := g.Audit(a, now); ok {
			t.Fatal("refusal flood")
		}
	}
	if ok, n := g.Audit(a, now.Add(time.Second)); !ok || n != 30 {
		t.Fatalf("summary: %t %d", ok, n)
	}
	g = New()
	for i := 0; i < 8; i++ {
		a := netip.AddrFrom4([4]byte{10, 0, 0, byte(i)})
		for range 8 {
			if r := g.Acquire(a, now); r != "" {
				t.Fatal(r)
			}
		}
	}
	if r := g.Acquire(netip.MustParseAddr("10.0.1.1"), now); r != "server-busy" {
		t.Fatal("global limit:", r)
	}
}

func TestSourceAccountingBound(t *testing.T) {
	g := New()
	now := time.Unix(100, 0)
	for i := 0; i < 5000; i++ {
		a := netip.AddrFrom4([4]byte{10, byte(i >> 8), byte(i), 1})
		g.Audit(a, now)
	}
	if len(g.sources) != 4096 {
		t.Fatal("unbounded source accounting", len(g.sources))
	}
	if g.Suppressed()[netip.Addr{}] != 904 {
		t.Fatal("untracked refusals missing from summary")
	}
}
