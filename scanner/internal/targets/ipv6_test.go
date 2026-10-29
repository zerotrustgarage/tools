package targets

import (
	"context"
	"net/netip"
	"strings"
	"testing"
)

func TestIsInternalClassifiesIPv6(t *testing.T) {
	own := []netip.Prefix{netip.MustParsePrefix("2001:db8:1::/64")}
	cases := []struct {
		addr string
		own  []netip.Prefix
		want bool
		why  string
	}{
		{"fe80::1", nil, true, "link-local"},
		{"fe80::1%eth0", nil, true, "link-local with a zone: the zone is ignored"},
		{"fe80::1%12", nil, true, "link-local with a numeric zone"},
		{"febf:ffff::1", nil, true, "top of fe80::/10"},
		{"fec0::1", nil, false, "site-local was deprecated by RFC 3879 and is not link-local"},
		{"fc00::1", nil, true, "unique local fc00::/8"},
		{"fd12:3456:789a::1", nil, true, "unique local fd00::/8"},
		{"fe00::1", nil, false, "just outside fc00::/7"},
		{"::1", nil, true, "loopback"},
		{"::", nil, false, "unspecified"},
		{"ff02::1", nil, false, "multicast is not a host"},
		{"ff0e::1", nil, false, "global multicast"},
		{"2001:db8:1::77", nil, false, "global, and no own prefix given"},
		{"2001:db8:1::77", own, true, "global inside the scanner's own on-link prefix"},
		{"2001:db8:1:0:ffff:ffff:ffff:ffff", own, true, "last address of the own prefix"},
		{"2001:db8:2::1", own, false, "global in another prefix"},
		{"2001:db8:1::77%eth0", own, true, "zoned global inside the own prefix"},
		{"2606:4700:4700::1111", own, false, "a public resolver"},
		{"2002:c000:204::1", own, false, "6to4 is public"},
		{"64:ff9b::c000:201", own, false, "NAT64 prefix is not internal"},
		{"::ffff:10.0.0.1", nil, true, "IPv4-mapped private address is judged as IPv4"},
		{"::ffff:8.8.8.8", nil, false, "IPv4-mapped public address"},
		{"10.0.0.1", nil, true, "IPv4 private"},
		{"100.64.0.1", nil, true, "IPv4 CGNAT"},
		{"169.254.1.1", nil, true, "IPv4 link-local"},
		{"8.8.8.8", own, false, "IPv4 public"},
	}
	for _, c := range cases {
		if got := IsInternalOn(netip.MustParseAddr(c.addr), c.own); got != c.want {
			t.Errorf("IsInternalOn(%s) = %v, want %v (%s)", c.addr, got, c.want, c.why)
		}
	}
	if IsInternal(netip.Addr{}) {
		t.Error("the zero address is internal")
	}
	if !IsInternal(netip.MustParseAddr("fe80::1")) || IsInternal(netip.MustParseAddr("2001:db8::1")) {
		t.Error("IsInternal is IsInternalOn with no own prefixes")
	}
}

func TestOwnPrefixesAreBounded(t *testing.T) {
	// A caller that hands over a prefix far wider than any real link must not
	// turn the internet internal.
	wide := []netip.Prefix{netip.MustParsePrefix("::/0"), netip.MustParsePrefix("2000::/3"), netip.MustParsePrefix("2001:db8::/32")}
	if IsInternalOn(netip.MustParseAddr("2001:db8::1"), wide) {
		t.Error("a prefix shorter than /48 was honored")
	}
	if !IsInternalOn(netip.MustParseAddr("2001:db8:0:1::1"), []netip.Prefix{netip.MustParsePrefix("2001:db8:0:1::/64")}) {
		t.Error("a /64 was refused")
	}
	if !IsInternalOn(netip.MustParseAddr("2001:db8::1"), []netip.Prefix{netip.MustParsePrefix("2001:db8::/48")}) {
		t.Error("a /48 is the widest allowed and was refused")
	}
	// An IPv4 prefix in the list means nothing for an IPv6 address.
	if IsInternalOn(netip.MustParseAddr("2001:db8::1"), []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")}) {
		t.Error("an IPv4 own prefix matched an IPv6 address")
	}
}

// IPv6 targets are still refused on input: a prefix cannot be swept, and the
// local link's IPv6 hosts are found through the neighbor table instead.
func TestTypedIPv6TargetsAreStillRefused(t *testing.T) {
	for _, spec := range []string{"fe80::1", "fe80::1%eth0", "fd00::/64", "2001:db8::1", "::1"} {
		_, err := Expand(context.Background(), []string{spec}, Options{AllowPublic: true})
		if err == nil || !strings.Contains(err.Error(), "neighbor discovery") {
			t.Errorf("%s: %v", spec, err)
		}
	}
}
