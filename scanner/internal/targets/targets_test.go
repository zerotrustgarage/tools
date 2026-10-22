package targets

import (
	"context"
	"errors"
	"net/netip"
	"testing"
)

func expand(t *testing.T, opt Options, specs ...string) []netip.Addr {
	t.Helper()
	got, err := Expand(context.Background(), specs, opt)
	if err != nil {
		t.Fatalf("Expand(%v): %v", specs, err)
	}
	return got
}

func TestCIDRDropsNetworkAndBroadcast(t *testing.T) {
	got := expand(t, Options{}, "192.168.1.0/30")
	if len(got) != 2 || got[0].String() != "192.168.1.1" || got[1].String() != "192.168.1.2" {
		t.Fatalf("got %v", got)
	}
	got = expand(t, Options{KeepNetworkAndBroadcast: true}, "192.168.1.0/30")
	if len(got) != 4 {
		t.Fatalf("got %v", got)
	}
}

func TestCIDRNormalizesHostBits(t *testing.T) {
	got := expand(t, Options{}, "10.1.2.77/24")
	if len(got) != 254 || got[0].String() != "10.1.2.1" || got[253].String() != "10.1.2.254" {
		t.Fatalf("got %d addrs, first %v", len(got), got[0])
	}
}

func TestSlash31And32(t *testing.T) {
	if got := expand(t, Options{}, "10.0.0.0/31"); len(got) != 2 {
		t.Fatalf("/31 got %v", got)
	}
	if got := expand(t, Options{}, "10.0.0.9/32"); len(got) != 1 || got[0].String() != "10.0.0.9" {
		t.Fatalf("/32 got %v", got)
	}
}

func TestRanges(t *testing.T) {
	got := expand(t, Options{}, "192.168.1.10-12")
	if len(got) != 3 || got[2].String() != "192.168.1.12" {
		t.Fatalf("short range got %v", got)
	}
	got = expand(t, Options{}, "10.0.0.254-10.0.1.1")
	want := []string{"10.0.0.254", "10.0.0.255", "10.0.1.0", "10.0.1.1"}
	if len(got) != len(want) {
		t.Fatalf("long range got %v", got)
	}
	for i := range want {
		if got[i].String() != want[i] {
			t.Fatalf("long range got %v", got)
		}
	}
}

func TestMixedListDedupesAndSorts(t *testing.T) {
	got := expand(t, Options{}, "10.0.0.3, 10.0.0.1-3;10.0.0.2")
	if len(got) != 3 || got[0].String() != "10.0.0.1" || got[2].String() != "10.0.0.3" {
		t.Fatalf("got %v", got)
	}
}

func TestScopeGuard(t *testing.T) {
	_, err := Expand(context.Background(), []string{"8.8.8.8"}, Options{})
	var se *ScopeError
	if !errors.As(err, &se) {
		t.Fatalf("want ScopeError, got %v", err)
	}
	if got := expand(t, Options{AllowPublic: true}, "8.8.8.8"); len(got) != 1 {
		t.Fatalf("allow-public got %v", got)
	}
	// Each permitted block.
	for _, s := range []string{"10.9.9.9", "172.31.0.1", "192.168.0.1", "100.64.0.1", "169.254.1.1", "127.0.0.1"} {
		expand(t, Options{}, s)
	}
	// Just outside 172.16.0.0/12 and 100.64.0.0/10.
	for _, s := range []string{"172.32.0.1", "100.128.0.1"} {
		if _, err := Expand(context.Background(), []string{s}, Options{}); !errors.As(err, &se) {
			t.Fatalf("%s: want ScopeError, got %v", s, err)
		}
	}
}

func TestMaxHosts(t *testing.T) {
	if _, err := Expand(context.Background(), []string{"10.0.0.0/8"}, Options{}); !errors.Is(err, ErrTooMany) {
		t.Fatalf("/8 want ErrTooMany, got %v", err)
	}
	if _, err := Expand(context.Background(), []string{"10.0.0.0/24"}, Options{MaxHosts: 100}); !errors.Is(err, ErrTooMany) {
		t.Fatalf("/24 with cap 100 want ErrTooMany, got %v", err)
	}
	if got := expand(t, Options{}, "10.0.0.0/16"); len(got) != 65534 {
		t.Fatalf("/16 got %d", len(got))
	}
}

func TestRejectsGarbage(t *testing.T) {
	for _, s := range []string{"10.0.0.0/33", "10.0.0.5-2", "10.0.0.1-300", "2001:db8::/64", "bad_host!", "-lead.example"} {
		if _, err := Expand(context.Background(), []string{s}, Options{}); err == nil {
			t.Fatalf("%q: want error", s)
		}
	}
}
