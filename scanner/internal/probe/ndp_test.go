package probe

import (
	"context"
	"encoding/binary"
	"net"
	"net/netip"
	"strings"
	"testing"
	"time"
)

func mac(s string) net.HardwareAddr {
	m, err := net.ParseMAC(s)
	if err != nil {
		panic(err)
	}
	return m
}

func a6(s string) netip.Addr { return netip.MustParseAddr(s) }

func TestZoneNameAndValidity(t *testing.T) {
	cases := []struct {
		idx   int
		name  string
		zone  string
		valid bool
	}{
		{2, "eth0", "eth0", true},
		{4, "en0", "en0", true},
		{12, "Ethernet", "Ethernet", true},
		{12, "Ethernet 2", "12", true}, // Windows friendly names carry spaces
		{7, "Wi-Fi", "Wi-Fi", true},
		{9, "", "9", true},
		{3, "a very long interface name", "3", true},
		{5, "eth0%evil", "5", true},
		{6, "eth0\n", "6", true},
	}
	for _, c := range cases {
		got := ZoneName(c.idx, c.name)
		if got != c.zone || !ValidZone(got) {
			t.Errorf("ZoneName(%d, %q) = %q, want %q", c.idx, c.name, got, c.zone)
		}
	}
	for _, bad := range []string{"", "eth 0", "a%b", "x\x00", "eth0\n", "é0", strings.Repeat("a", 16)} {
		if ValidZone(bad) {
			t.Errorf("ValidZone(%q) accepted", bad)
		}
	}
}

func TestCleanNeighbors6(t *testing.T) {
	zone := func(i int) string { return map[int]string{2: "eth0", 3: "Ethernet 2"}[i] }
	in := []Neighbor6{
		{Addr: a6("fe80::2"), MAC: mac("02:00:00:00:00:02"), IfIndex: 2, State: "stale"},
		{Addr: a6("fe80::1"), MAC: mac("02:00:00:00:00:01"), IfIndex: 3, State: "reachable"},
		{Addr: a6("fe80::1"), MAC: mac("02:00:00:00:00:01"), IfIndex: 3, State: "stale"}, // repeat
		{Addr: a6("fe80::1%eth0"), MAC: mac("02:00:00:00:00:09"), IfIndex: 2},            // zone from the input is replaced
		{Addr: a6("2001:db8::1"), MAC: mac("02:00:00:00:00:03"), IfIndex: 2},             // global: no zone
		{Addr: a6("ff02::1"), MAC: mac("33:33:00:00:00:01"), IfIndex: 2},                 // multicast
		{Addr: a6("ff02::2"), MAC: mac("02:00:00:00:00:04"), IfIndex: 2},                 // multicast address, unicast MAC
		{Addr: a6("::"), MAC: mac("02:00:00:00:00:05"), IfIndex: 2},                      // unspecified
		{Addr: a6("::1"), MAC: mac("02:00:00:00:00:06"), IfIndex: 2},                     // loopback
		{Addr: a6("::ffff:10.0.0.1"), MAC: mac("02:00:00:00:00:07"), IfIndex: 2},         // IPv4-mapped
		{Addr: a6("10.0.0.1"), MAC: mac("02:00:00:00:00:08"), IfIndex: 2},                // not IPv6
		{Addr: a6("fe80::7"), MAC: mac("00:00:00:00:00:00"), IfIndex: 2},                 // no hardware address
		{Addr: a6("fe80::8"), MAC: mac("01:00:5e:00:00:01"), IfIndex: 2},                 // multicast MAC
		{Addr: a6("fe80::9"), MAC: net.HardwareAddr{1, 2, 3}, IfIndex: 2},                // short MAC
		{Addr: a6("fe80::a"), MAC: mac("02:00:00:00:00:0a"), IfIndex: 0},                 // link-local with no interface
		{Addr: netip.Addr{}, MAC: mac("02:00:00:00:00:0b"), IfIndex: 2},                  // invalid
	}
	got := CleanNeighbors6(in, zone)
	var names []string
	for _, n := range got {
		names = append(names, n.Addr.String())
	}
	want := "2001:db8::1 fe80::1%3 fe80::1%eth0 fe80::2%eth0"
	// fe80::1 on interface 3 has a friendly name with a space: the index is used.
	if strings.Join(names, " ") != want {
		t.Fatalf("got %v\nwant %v", names, want)
	}
	// Input rows are not aliased: changing the result leaves the input alone.
	got[0].MAC[0] = 0xff
	if in[4].MAC[0] == 0xff {
		t.Fatal("result shares the input's hardware address bytes")
	}
	// With no zone function the decimal index is used.
	z := CleanNeighbors6([]Neighbor6{{Addr: a6("fe80::5"), MAC: mac("02:00:00:00:00:05"), IfIndex: 12}}, nil)
	if len(z) != 1 || z[0].Addr.String() != "fe80::5%12" {
		t.Fatalf("%+v", z)
	}
}

// buildNetTable2 constructs a MIB_IPNET_TABLE2 the way GetIpNetTable2 lays it out.
func buildNetTable2(rows ...[]byte) []byte {
	b := make([]byte, ipNetTable2Header)
	binary.LittleEndian.PutUint32(b, uint32(len(rows)))
	for _, r := range rows {
		b = append(b, r...)
	}
	return b
}

func netRow(family uint16, addr string, ifIndex uint32, macBytes []byte, state uint32) []byte {
	r := make([]byte, ipNetRow2Size)
	binary.LittleEndian.PutUint16(r[0:2], family)
	if family == winAFInet6 {
		a := netip.MustParseAddr(addr).As16()
		copy(r[8:24], a[:])
		binary.LittleEndian.PutUint32(r[24:28], ifIndex) // sin6_scope_id
	} else {
		a := netip.MustParseAddr(addr).As4()
		copy(r[4:8], a[:]) // sin_addr
	}
	binary.LittleEndian.PutUint32(r[28:32], ifIndex)
	copy(r[40:72], macBytes)
	binary.LittleEndian.PutUint32(r[72:76], uint32(len(macBytes)))
	binary.LittleEndian.PutUint32(r[76:80], state)
	return r
}

func TestParseIPNetTable2(t *testing.T) {
	m := []byte{0x02, 0xaa, 0xbb, 0xcc, 0xdd, 0x01}
	table := buildNetTable2(
		netRow(winAFInet6, "fe80::1", 12, m, nlnsReachable),
		netRow(winAFInet6, "fe80::2", 12, m, nlnsStale),
		netRow(winAFInet6, "fd00::3", 7, m, nlnsDelay),
		netRow(winAFInet6, "fd00::4", 7, m, nlnsProbe),
		netRow(winAFInet6, "fd00::5", 7, m, nlnsPermanent),
		netRow(winAFInet6, "fe80::6", 12, m, nlnsUnreachable),
		netRow(winAFInet6, "fe80::7", 12, nil, nlnsIncomplete),
		netRow(winAFInet6, "fe80::8", 12, m, 99),                      // a state this code does not know
		netRow(winAFInet6, "fe80::9", 12, []byte{1, 2, 3}, nlnsStale), // short hardware address
		netRow(2, "192.0.2.1", 12, m, nlnsReachable),                  // AF_INET row
		netRow(winAFInet6, "ff02::1", 12, []byte{0x33, 0x33, 0, 0, 0, 1}, nlnsPermanent),
	)
	got, err := parseIPNetTable2(table)
	if err != nil {
		t.Fatal(err)
	}
	var s []string
	for _, n := range got {
		s = append(s, n.Addr.String()+"/"+n.State+"/"+n.MAC.String())
	}
	want := "fe80::1/reachable/02:aa:bb:cc:dd:01 fe80::2/stale/02:aa:bb:cc:dd:01 fd00::3/delay/02:aa:bb:cc:dd:01 " +
		"fd00::4/probe/02:aa:bb:cc:dd:01 fd00::5/permanent/02:aa:bb:cc:dd:01 ff02::1/permanent/33:33:00:00:00:01"
	if strings.Join(s, " ") != want {
		t.Fatalf("got  %v\nwant %v", strings.Join(s, " "), want)
	}
	if got[0].IfIndex != 12 || got[2].IfIndex != 7 {
		t.Fatalf("interface indexes %d %d", got[0].IfIndex, got[2].IfIndex)
	}
	// The scope filter then drops the multicast row.
	for _, n := range CleanNeighbors6(got, nil) {
		if n.Addr.IsMulticast() {
			t.Fatal("multicast neighbor survived")
		}
	}
}

func TestParseIPNetTable2RefusesBadBuffers(t *testing.T) {
	m := []byte{0x02, 0xaa, 0xbb, 0xcc, 0xdd, 0x01}
	good := buildNetTable2(netRow(winAFInet6, "fe80::1", 12, m, nlnsStale))
	if _, err := parseIPNetTable2(good[:len(good)-1]); err == nil {
		t.Error("a table one byte short was accepted")
	}
	if _, err := parseIPNetTable2(good[:4]); err == nil {
		t.Error("a table with no header was accepted")
	}
	huge := make([]byte, ipNetTable2Header)
	binary.LittleEndian.PutUint32(huge, 0xffffffff)
	if _, err := parseIPNetTable2(huge); err == nil {
		t.Error("an absurd entry count was accepted")
	}
	empty, err := parseIPNetTable2(buildNetTable2())
	if err != nil || len(empty) != 0 {
		t.Errorf("empty table: %v %v", empty, err)
	}
}

func TestSockaddrIn6AndEchoReplyLayout(t *testing.T) {
	sa := sockaddrIn6(a6("ff02::1%eth0"), 12)
	if binary.LittleEndian.Uint16(sa[0:2]) != 23 || binary.LittleEndian.Uint16(sa[2:4]) != 0 || binary.LittleEndian.Uint32(sa[4:8]) != 0 {
		t.Fatalf("family, port or flow information wrong: % x", sa[:8])
	}
	if netip.AddrFrom16([16]byte(sa[8:24])) != a6("ff02::1") || binary.LittleEndian.Uint32(sa[24:28]) != 12 {
		t.Fatalf("address or scope id wrong: % x", sa)
	}
	if len(sa) != 28 {
		t.Fatalf("SOCKADDR_IN6 is 28 bytes, got %d", len(sa))
	}

	// ICMPV6_ECHO_REPLY: packed 26-byte IPV6_ADDRESS_EX, then Status and RTT.
	r := make([]byte, 36)
	from := a6("fe80::2a").As16()
	copy(r[6:22], from[:])
	binary.LittleEndian.PutUint32(r[22:26], 12)
	binary.LittleEndian.PutUint32(r[28:32], 0)
	binary.LittleEndian.PutUint32(r[32:36], 7)
	addr, status, rtt, ok := icmp6EchoReply(r)
	if !ok || addr != a6("fe80::2a") || status != 0 || rtt != 7 {
		t.Fatalf("%v %d %d %v", addr, status, rtt, ok)
	}
	if _, _, _, ok := icmp6EchoReply(r[:35]); ok {
		t.Fatal("a short reply buffer was decoded")
	}
}

// ndmsg builds one RTM_NEWNEIGH message in the host byte order.
func neighMsg(family byte, ifindex int32, state uint16, attrs ...[]byte) []byte {
	o := binary.NativeEndian
	body := make([]byte, ndmsgLen)
	body[0] = family
	o.PutUint32(body[4:8], uint32(ifindex))
	o.PutUint16(body[8:10], state)
	for _, a := range attrs {
		body = append(body, a...)
	}
	return nlmsg(rtmNewNeigh, body)
}

func nlmsg(typ uint16, body []byte) []byte {
	o := binary.NativeEndian
	h := make([]byte, nlmsgHdrLen)
	o.PutUint32(h[0:4], uint32(nlmsgHdrLen+len(body)))
	o.PutUint16(h[4:6], typ)
	b := append(h, body...)
	for len(b)%4 != 0 {
		b = append(b, 0)
	}
	return b
}

func rtattr(typ uint16, val []byte) []byte {
	o := binary.NativeEndian
	b := make([]byte, 4, 4+len(val)+3)
	o.PutUint16(b[0:2], uint16(4+len(val)))
	o.PutUint16(b[2:4], typ)
	b = append(b, val...)
	for len(b)%4 != 0 {
		b = append(b, 0)
	}
	return b
}

func TestParseNeighMessages(t *testing.T) {
	dst := func(s string) []byte { a := a6(s).As16(); return rtattr(ndaDst, a[:]) }
	ll := func(m string) []byte { return rtattr(ndaLLAddr, mac(m)) }
	var b []byte
	b = append(b, neighMsg(afInet6, 2, nudReachable, dst("fe80::1"), ll("02:aa:bb:cc:dd:01"))...)
	b = append(b, neighMsg(afInet6, 2, nudStale, dst("fd00::2"), ll("02:aa:bb:cc:dd:02"))...)
	b = append(b, neighMsg(afInet6, 3, nudDelay|nudProbe, dst("fd00::3"), ll("02:aa:bb:cc:dd:03"))...)
	b = append(b, neighMsg(afInet6, 3, nudPermanent, dst("fd00::4"), ll("02:aa:bb:cc:dd:04"))...)
	b = append(b, neighMsg(afInet6, 2, nudIncomplete, dst("fe80::5"))...)                                     // no lladdr
	b = append(b, neighMsg(afInet6, 2, nudFailed, dst("fe80::6"), ll("02:aa:bb:cc:dd:06"))...)                // failed
	b = append(b, neighMsg(afInet6, 2, nudStale, dst("fe80::7"))...)                                          // no lladdr at all
	b = append(b, neighMsg(2, 2, nudStale, rtattr(ndaDst, []byte{192, 0, 2, 1}), ll("02:aa:bb:cc:dd:08"))...) // AF_INET
	b = append(b, neighMsg(afInet6, 2, nudStale, rtattr(ndaDst, []byte{1, 2, 3, 4}), ll("02:aa:bb:cc:dd:09"))...)
	b = append(b, nlmsg(nlmsgDone, make([]byte, 4))...)
	b = append(b, neighMsg(afInet6, 2, nudStale, dst("fe80::99"), ll("02:aa:bb:cc:dd:99"))...) // after DONE: ignored
	got, err := parseNeighMessages(b)
	if err != nil {
		t.Fatal(err)
	}
	var s []string
	for _, n := range got {
		s = append(s, n.Addr.String()+"/"+n.State+"/"+n.MAC.String()+"/"+string(rune('0'+n.IfIndex)))
	}
	want := "fe80::1/reachable/02:aa:bb:cc:dd:01/2 fd00::2/stale/02:aa:bb:cc:dd:02/2 fd00::3/delay/02:aa:bb:cc:dd:03/3 fd00::4/permanent/02:aa:bb:cc:dd:04/3"
	if strings.Join(s, " ") != want {
		t.Fatalf("got  %v\nwant %v", strings.Join(s, " "), want)
	}
}

func TestParseNeighMessagesRefusesDamage(t *testing.T) {
	good := neighMsg(afInet6, 2, nudStale, rtattr(ndaDst, func() []byte { a := a6("fe80::1").As16(); return a[:] }()), rtattr(ndaLLAddr, mac("02:aa:bb:cc:dd:01")))
	if _, err := parseNeighMessages(good[:len(good)-6]); err == nil {
		t.Error("a truncated message was accepted")
	}
	if _, err := parseNeighMessages(nlmsg(nlmsgError, make([]byte, 20))); err == nil {
		t.Error("a kernel error was ignored")
	}
	// An attribute that claims to be longer than the message ends the walk safely.
	bad := neighMsg(afInet6, 2, nudStale, []byte{0xff, 0x7f, 1, 0, 9, 9, 9, 9})
	if got, err := parseNeighMessages(bad); err != nil || len(got) != 0 {
		t.Errorf("bad attribute: %v %v", got, err)
	}
	if got, err := parseNeighMessages(nil); err != nil || len(got) != 0 {
		t.Errorf("nil: %v %v", got, err)
	}
	// A zero-length message must not loop forever.
	if _, err := parseNeighMessages(make([]byte, 16)); err == nil {
		t.Error("a zero-length netlink message was accepted")
	}
}

func TestReverseNameIPv6(t *testing.T) {
	got, err := reverseName(a6("2001:db8::567:89ab"))
	want := "b.a.9.8.7.6.5.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.8.b.d.0.1.0.0.2.ip6.arpa."
	if err != nil || got != want {
		t.Fatalf("got %q (%v)\nwant %q", got, err, want)
	}
	z, _ := reverseName(a6("fe80::1%eth0"))
	if strings.Contains(z, "eth0") || !strings.HasSuffix(z, ".ip6.arpa.") || len(z) != 32*2+len("ip6.arpa.") {
		t.Fatalf("zone leaked into the reverse name: %q", z)
	}
	v4, _ := reverseName(a6("192.0.2.7"))
	if v4 != "7.2.0.192.in-addr.arpa." {
		t.Fatalf("%q", v4)
	}
	if m, _ := reverseName(a6("::ffff:192.0.2.7")); m != v4 {
		t.Fatalf("mapped: %q", m)
	}
}

func TestIPv6DialHelpers(t *testing.T) {
	if tcpNetwork(a6("fe80::1%eth0")) != "tcp6" || tcpNetwork(a6("192.0.2.1")) != "tcp4" || tcpNetwork(a6("::ffff:192.0.2.1")) != "tcp4" {
		t.Fatal("address family")
	}
	if hostHeader(a6("fe80::1%eth0")) != "[fe80::1]" || hostHeader(a6("2001:db8::1")) != "[2001:db8::1]" || hostHeader(a6("192.0.2.1")) != "192.0.2.1" {
		t.Fatalf("host header %q %q", hostHeader(a6("fe80::1%eth0")), hostHeader(a6("2001:db8::1")))
	}
	// The protocols that exist only over IPv4 refuse an IPv6 address.
	if _, err := NetBIOSStatus(context.Background(), a6("fe80::1%eth0"), time.Millisecond); err == nil {
		t.Error("NetBIOS accepted an IPv6 address")
	}
	if _, err := LLMNRQuery(context.Background(), a6("fe80::1%eth0"), time.Millisecond); err == nil {
		t.Error("LLMNR accepted an IPv6 address")
	}
}

func TestDialTCPOverIPv6Loopback(t *testing.T) {
	l, err := net.Listen("tcp6", "[::1]:0")
	if err != nil {
		t.Skip("no IPv6 loopback here:", err)
	}
	defer l.Close()
	go func() {
		if c, err := l.Accept(); err == nil {
			c.Close()
		}
	}()
	port := l.Addr().(*net.TCPAddr).Port
	res := ScanPorts(context.Background(), a6("::1"), []int{port}, time.Second, 1)
	if len(res.Open) != 1 || res.Open[0] != port {
		t.Fatalf("IPv6 connect scan: %+v", res)
	}
	// A closed IPv6 port counts as a reset, as for IPv4.
	l2, _ := net.Listen("tcp6", "[::1]:0")
	closed := l2.Addr().(*net.TCPAddr).Port
	l2.Close()
	res = ScanPorts(context.Background(), a6("::1"), []int{closed}, time.Second, 1)
	if len(res.Open) != 0 || !res.Refused {
		t.Fatalf("closed IPv6 port: %+v", res)
	}
}
