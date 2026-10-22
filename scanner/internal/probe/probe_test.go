package probe

import (
	"context"
	"crypto/tls"
	"encoding/binary"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

var loopback = netip.MustParseAddr("127.0.0.1")

func portOf(t *testing.T, addr string) int {
	t.Helper()
	_, p, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatal(err)
	}
	n, _ := strconv.Atoi(p)
	return n
}

func TestNBSTATQueryEncoding(t *testing.T) {
	q := nbstatQuery(0x1234)
	if len(q) != 50 {
		t.Fatalf("query length %d, want 50", len(q))
	}
	if string(q[13:15]) != "CK" || string(q[15:17]) != "AA" {
		t.Fatalf("wildcard name encoded as %q", q[13:45])
	}
	if binary.BigEndian.Uint16(q[46:]) != 0x21 {
		t.Fatal("QTYPE is not NBSTAT")
	}
}

// buildNBSTAT builds a node status response like Windows sends.
func buildNBSTAT(txid uint16, names []NetBIOSName, mac []byte) []byte {
	b := binary.BigEndian.AppendUint16(nil, txid)
	b = append(b, 0x84, 0x00, 0, 0, 0, 1, 0, 0, 0, 0)
	b = append(b, nbstatQuery(0)[12:46]...) // echoed name
	b = append(b, 0x00, 0x21, 0x00, 0x01, 0, 0, 0, 0)
	rd := []byte{byte(len(names))}
	for _, n := range names {
		field := []byte(fmt.Sprintf("%-15s", n.Name))
		field = append(field, n.Suffix)
		flags := uint16(0x0400)
		if n.Group {
			flags |= 0x8000
		}
		field = binary.BigEndian.AppendUint16(field, flags)
		rd = append(rd, field...)
	}
	rd = append(rd, mac...)
	rd = append(rd, make([]byte, 40)...) // rest of the statistics block
	b = binary.BigEndian.AppendUint16(b, uint16(len(rd)))
	return append(b, rd...)
}

func TestParseNBSTAT(t *testing.T) {
	pkt := buildNBSTAT(7, []NetBIOSName{
		{Name: "FRONTDESK-PC", Suffix: 0x00},
		{Name: "ACME", Suffix: 0x00, Group: true},
		{Name: "FRONTDESK-PC", Suffix: 0x20},
		{Name: "ACME", Suffix: 0x1c, Group: true},
	}, []byte{0x00, 0x1b, 0x21, 0xaa, 0xbb, 0xcc})
	info, err := parseNBSTAT(pkt)
	if err != nil {
		t.Fatal(err)
	}
	if info.Computer != "FRONTDESK-PC" || info.Workgroup != "ACME" || info.MAC != "00:1b:21:aa:bb:cc" || len(info.Names) != 4 {
		t.Fatalf("got %+v", info)
	}
	// Samba reports a zero unit ID: no MAC.
	info, _ = parseNBSTAT(buildNBSTAT(7, []NetBIOSName{{Name: "NAS", Suffix: 0x20}}, make([]byte, 6)))
	if info.MAC != "" {
		t.Fatalf("zero MAC reported: %q", info.MAC)
	}
	// Truncated packets must not panic.
	for i := 0; i < len(pkt); i++ {
		_, _ = parseNBSTAT(pkt[:i])
	}
}

func TestNetBIOSStatusOverUDP(t *testing.T) {
	// A fake responder on loopback; the probe targets port 137, so exercise
	// the exchange through parse instead when 137 cannot be bound.
	pc, err := net.ListenPacket("udp4", "127.0.0.1:137")
	if err != nil {
		t.Skip("cannot bind UDP 137 here:", err)
	}
	defer pc.Close()
	go func() {
		buf := make([]byte, 512)
		n, from, err := pc.ReadFrom(buf)
		if err != nil || n < 2 {
			return
		}
		resp := buildNBSTAT(binary.BigEndian.Uint16(buf), []NetBIOSName{{Name: "LAB-01", Suffix: 0x00}}, []byte{2, 0, 0, 0, 0, 1})
		pc.WriteTo(resp, from)
	}()
	info, err := NetBIOSStatus(context.Background(), loopback, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if info.Computer != "LAB-01" {
		t.Fatalf("got %+v", info)
	}
}

func TestMergeMDNS(t *testing.T) {
	reverse, _ := reverseName(netip.MustParseAddr("192.168.1.20"))
	if reverse != "20.1.168.192.in-addr.arpa." {
		t.Fatalf("reverse name %q", reverse)
	}
	b := dnsmessage.NewBuilder(nil, dnsmessage.Header{Response: true, Authoritative: true})
	b.StartAnswers()
	ptr := func(owner, target string) {
		b.PTRResource(dnsmessage.ResourceHeader{Name: dnsmessage.MustNewName(owner), Class: dnsmessage.ClassINET, TTL: 120},
			dnsmessage.PTRResource{PTR: dnsmessage.MustNewName(target)})
	}
	ptr(reverse, "Office-Printer.local.")
	ptr("_services._dns-sd._udp.local.", "_ipp._tcp.local.")
	ptr("_services._dns-sd._udp.local.", "_http._tcp.local.")
	ptr("_services._dns-sd._udp.local.", "_ipp._tcp.local.") // duplicate
	msg, err := b.Finish()
	if err != nil {
		t.Fatal(err)
	}
	info := &MDNSInfo{}
	mergeMDNS(msg, reverse, info, map[string]bool{})
	if info.Hostname != "Office-Printer.local" || len(info.Services) != 2 || info.Services[0] != "_ipp._tcp" {
		t.Fatalf("got %+v", info)
	}
	mergeMDNS([]byte{1, 2, 3}, reverse, info, map[string]bool{}) // garbage is ignored
}

func TestSSDPParsing(t *testing.T) {
	server, loc := parseSSDP([]byte("HTTP/1.1 200 OK\r\nCACHE-CONTROL: max-age=1800\r\nLOCATION: http://192.168.1.1:1900/igd.xml\r\nSERVER: Linux/5.4 UPnP/1.0 MiniUPnPd/2.2\r\nST: upnp:rootdevice\r\n\r\n"))
	if server != "Linux/5.4 UPnP/1.0 MiniUPnPd/2.2" || loc != "http://192.168.1.1:1900/igd.xml" {
		t.Fatalf("server %q location %q", server, loc)
	}
	gw := netip.MustParseAddr("192.168.1.1")
	if !locationOnHost(loc, gw) {
		t.Fatal("location on the responder rejected")
	}
	if locationOnHost("http://203.0.113.9/x.xml", gw) || locationOnHost("https://192.168.1.1/x.xml", gw) || locationOnHost("file:///etc/passwd", gw) {
		t.Fatal("location off the responder accepted")
	}
	info := &UPnPInfo{}
	parseDescription(strings.NewReader(`<?xml version="1.0"?><root xmlns="urn:schemas-upnp-org:device-1-0"><device>
		<deviceType>urn:schemas-upnp-org:device:InternetGatewayDevice:1</deviceType>
		<friendlyName>Office Router</friendlyName><manufacturer>NETGEAR</manufacturer>
		<modelName>Nighthawk</modelName><modelNumber>RAX50</modelNumber></device></root>`), info)
	if info.FriendlyName != "Office Router" || info.Manufacturer != "NETGEAR" || info.ModelNumber != "RAX50" || !strings.Contains(info.DeviceType, "InternetGatewayDevice") {
		t.Fatalf("got %+v", info)
	}
}

func TestFetchDescription(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("User-Agent") != UserAgent {
			t.Errorf("User-Agent %q", r.Header.Get("User-Agent"))
		}
		fmt.Fprint(w, `<root><device><friendlyName>NAS</friendlyName><manufacturer>Synology</manufacturer></device></root>`)
	}))
	defer srv.Close()
	info := &UPnPInfo{Location: srv.URL + "/desc.xml"}
	FetchDescription(context.Background(), info, time.Second)
	if info.Manufacturer != "Synology" {
		t.Fatalf("got %+v", info)
	}
}

func TestScanPortsOpenAndRefused(t *testing.T) {
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	open := portOf(t, ln.Addr().String())
	// A port that was just closed is refused (RST) on loopback.
	tmp, _ := net.Listen("tcp4", "127.0.0.1:0")
	closed := portOf(t, tmp.Addr().String())
	tmp.Close()

	res := ScanPorts(context.Background(), loopback, []int{closed, open}, time.Second, 4)
	if len(res.Open) != 1 || res.Open[0] != open || !res.Refused {
		t.Fatalf("got %+v", res)
	}
}

func TestGrabBannerAndHTTP(t *testing.T) {
	// A greeting service on an unknown port is read, not sent HTTP.
	ln, _ := net.Listen("tcp4", "127.0.0.1:0")
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			c.Write([]byte("SSH-2.0-OpenSSH_9.6p1 Ubuntu-3ubuntu13\r\n"))
			c.Close()
		}
	}()
	s := Grab(context.Background(), loopback, portOf(t, ln.Addr().String()), time.Second)
	if s.Name != "ssh" || s.Product != "OpenSSH_9.6p1 Ubuntu-3ubuntu13" {
		t.Fatalf("ssh: %+v", s)
	}

	// A silent service on an unknown port gets one HTTP GET.
	web := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Server", "lighttpd/1.4.59")
		w.Header().Set("WWW-Authenticate", `Basic realm="NETGEAR GS308E"`)
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, "<html><head><TITLE>Switch &amp; Login</TITLE></head></html>")
	}))
	defer web.Close()
	s = Grab(context.Background(), loopback, portOf(t, web.Listener.Addr().String()), time.Second)
	if s.Name != "http" || s.HTTP == nil || s.HTTP.Status != 401 || s.HTTP.Title != "Switch & Login" || s.HTTP.Realm != "NETGEAR GS308E" || s.Product != "lighttpd/1.4.59" {
		t.Fatalf("http: %+v %+v", s, s.HTTP)
	}
}

func TestGrabTLS(t *testing.T) {
	web := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "<title>Device Manager</title>")
	}))
	defer web.Close()
	port := portOf(t, web.Listener.Addr().String())
	var s Service
	raw, err := DialTCP(context.Background(), loopback, port, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	c := tls.Client(raw, &tls.Config{InsecureSkipVerify: true})
	if err := c.Handshake(); err != nil {
		t.Fatal(err)
	}
	s.TLS = describeTLS(c.ConnectionState())
	c.Close()
	if s.TLS.SHA256 == "" || s.TLS.NotAfter.IsZero() || s.TLS.Version == "" {
		t.Fatalf("tls: %+v", s.TLS)
	}
	// Full path through grabTLS with the web check enabled for this port.
	webOverTLS[port], tlsPorts[port] = true, true
	defer func() { delete(webOverTLS, port); delete(tlsPorts, port) }()
	s = Grab(context.Background(), loopback, port, 2*time.Second)
	if s.Name != "https" || s.HTTP == nil || s.HTTP.Title != "Device Manager" || s.TLS == nil {
		t.Fatalf("https: %+v", s)
	}
}

func TestPrintable(t *testing.T) {
	if got := printable([]byte("\xff\xfb\x01\xff\xfb\x03Welcome to switch\r\nlogin:")); got != "Welcome to switch" {
		t.Fatalf("got %q", got)
	}
}

func TestOSHint(t *testing.T) {
	for ttl, want := range map[int]string{0: "", 57: "Linux", 117: "Windows", 250: "Network"} {
		if got := OSHintFromTTL(ttl); !strings.HasPrefix(got, want) {
			t.Errorf("ttl %d: %q", ttl, got)
		}
	}
}

func TestPingLoopback(t *testing.T) {
	p, err := NewPinger()
	if err != nil {
		t.Skip("ICMP not permitted here:", err)
	}
	defer p.Close()
	r, err := p.Ping(context.Background(), loopback, time.Second)
	if err != nil {
		t.Fatalf("%s ping 127.0.0.1: %v", p.Method(), err)
	}
	if r.RTT <= 0 {
		t.Fatalf("rtt %v", r.RTT)
	}
}
