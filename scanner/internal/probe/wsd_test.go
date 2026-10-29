package probe

import (
	"context"
	"encoding/xml"
	"net"
	"net/netip"
	"regexp"
	"strings"
	"testing"
	"time"
)

const probeID = "urn:uuid:0e0a6a0a-1111-4222-8333-444455556666"

// matchesMessage builds a ProbeMatches message shaped like the ones devices
// send (SOAP 1.2, WS-Discovery 1.0, WS-Addressing 2004/08).
func matchesMessage(relatesTo, action, matches string) string {
	return `<?xml version="1.0" encoding="UTF-8"?>` +
		`<SOAP-ENV:Envelope xmlns:SOAP-ENV="http://www.w3.org/2003/05/soap-envelope" ` +
		`xmlns:wsa="http://schemas.xmlsoap.org/ws/2004/08/addressing" ` +
		`xmlns:d="http://schemas.xmlsoap.org/ws/2005/04/discovery" ` +
		`xmlns:dn="http://www.onvif.org/ver10/network/wsdl" ` +
		`xmlns:wsdp="http://schemas.xmlsoap.org/ws/2006/02/devprof" ` +
		`xmlns:pri="http://schemas.microsoft.com/windows/2006/08/wdp/print">` +
		`<SOAP-ENV:Header><wsa:MessageID>urn:uuid:aaaaaaaa-0000-4000-8000-000000000001</wsa:MessageID>` +
		`<wsa:RelatesTo>` + relatesTo + `</wsa:RelatesTo>` +
		`<wsa:To>http://schemas.xmlsoap.org/ws/2004/08/addressing/role/anonymous</wsa:To>` +
		`<wsa:Action>` + action + `</wsa:Action></SOAP-ENV:Header>` +
		`<SOAP-ENV:Body>` + matches + `</SOAP-ENV:Body></SOAP-ENV:Envelope>`
}

const matchAction = "http://schemas.xmlsoap.org/ws/2005/04/discovery/ProbeMatches"

func onvifMatch(types, scopes, xaddrs string) string {
	return `<d:ProbeMatches><d:ProbeMatch><wsa:EndpointReference><wsa:Address>urn:uuid:11111111-2222-4333-8444-555555555555</wsa:Address></wsa:EndpointReference>` +
		`<d:Types>` + types + `</d:Types><d:Scopes>` + scopes + `</d:Scopes><d:XAddrs>` + xaddrs + `</d:XAddrs>` +
		`<d:MetadataVersion>1</d:MetadataVersion></d:ProbeMatch></d:ProbeMatches>`
}

func cameraMessage() string {
	return matchesMessage(probeID, matchAction, onvifMatch(
		"dn:NetworkVideoTransmitter",
		"onvif://www.onvif.org/type/video_encoder onvif://www.onvif.org/Profile/Streaming onvif://www.onvif.org/name/AXIS%20M3045-V onvif://www.onvif.org/hardware/M3045-V onvif://www.onvif.org/location/office",
		"http://192.0.2.10/onvif/device_service"))
}

func printerMessage() string {
	return matchesMessage(probeID, matchAction, onvifMatch(
		"wsdp:Device pri:PrintDeviceType", "",
		"http://192.0.2.20:80/WSD/DEVICE http://[fe80::1]:80/WSD/DEVICE"))
}

func TestParseProbeMatchesONVIFCamera(t *testing.T) {
	info, err := parseProbeMatches([]byte(cameraMessage()), probeID)
	if err != nil {
		t.Fatal(err)
	}
	if len(info.Types) != 1 || info.Types[0] != "dn:NetworkVideoTransmitter" {
		t.Fatalf("types %q", info.Types)
	}
	if info.Name != "AXIS M3045-V" || info.Hardware != "M3045-V" {
		t.Fatalf("name %q hardware %q", info.Name, info.Hardware)
	}
	if len(info.Scopes) != 5 || len(info.XAddrs) != 1 || info.XAddrs[0] != "http://192.0.2.10/onvif/device_service" {
		t.Fatalf("scopes %q xaddrs %q", info.Scopes, info.XAddrs)
	}
	if !info.HasONVIFScope() {
		t.Fatal("ONVIF scope not recognized")
	}
	if got := strings.Join(info.TypeNames(), ","); got != "networkvideotransmitter" {
		t.Fatalf("type names %q", got)
	}
}

func TestParseProbeMatchesPrinter(t *testing.T) {
	info, err := parseProbeMatches([]byte(printerMessage()), probeID)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(info.TypeNames(), ","); got != "device,printdevicetype" {
		t.Fatalf("type names %q", got)
	}
	if len(info.XAddrs) != 2 || info.HasONVIFScope() || info.Name != "" {
		t.Fatalf("%+v", info)
	}
}

func TestParseProbeMatchesRefusals(t *testing.T) {
	good := onvifMatch("dn:NetworkVideoTransmitter", "", "http://192.0.2.10/x")
	cases := map[string]string{
		"different probe": matchesMessage("urn:uuid:other", matchAction, good),
		"not matches":     matchesMessage(probeID, "http://schemas.xmlsoap.org/ws/2005/04/discovery/Hello", good),
		"no match body":   matchesMessage(probeID, matchAction, `<d:ProbeMatches/>`),
		"empty match":     matchesMessage(probeID, matchAction, onvifMatch("", "", "")),
		"control char":    matchesMessage(probeID, matchAction, onvifMatch("dn:A", "onvif://x/\x01y", "")),
		"not xml":         "GET / HTTP/1.1\r\n\r\n",
		"empty":           "",
		"not closed":      matchesMessage(probeID, matchAction, good)[:200],
		"dtd":             `<?xml version="1.0"?><!DOCTYPE x [<!ENTITY a "b">]>` + matchesMessage(probeID, matchAction, good),
		"lowercase dtd":   `<!doctype x>` + matchesMessage(probeID, matchAction, good),
		"entity":          `<!ENTITY a "b">` + matchesMessage(probeID, matchAction, good),
		"unknown entity":  matchesMessage(probeID, matchAction, onvifMatch("dn:A&xxe;", "", "http://192.0.2.10/")),
		"utf-16 label":    `<?xml version="1.0" encoding="UTF-16"?>` + matchesMessage(probeID, matchAction, good),
	}
	for name, msg := range cases {
		if info, err := parseProbeMatches([]byte(msg), probeID); err == nil {
			t.Errorf("%s: accepted %+v", name, info)
		}
	}
	// With no expected ID (the fuzz and parse-only use) a stranger's match parses.
	if _, err := parseProbeMatches([]byte(matchesMessage("urn:uuid:other", matchAction, good)), ""); err != nil {
		t.Fatal(err)
	}
}

func TestParseProbeMatchesEntityExpansion(t *testing.T) {
	// The classic exponential-expansion document must be refused before any
	// parsing, and quickly.
	bomb := `<?xml version="1.0"?><!DOCTYPE lolz [<!ENTITY lol "lol"><!ENTITY lol2 "&lol;&lol;&lol;&lol;&lol;&lol;&lol;&lol;&lol;&lol;"><!ENTITY lol3 "&lol2;&lol2;&lol2;&lol2;&lol2;&lol2;&lol2;&lol2;&lol2;&lol2;">]>` +
		matchesMessage(probeID, matchAction, onvifMatch("&lol3;", "", ""))
	start := time.Now()
	if _, err := parseProbeMatches([]byte(bomb), probeID); err == nil {
		t.Fatal("entity bomb accepted")
	}
	if time.Since(start) > time.Second {
		t.Fatal("entity bomb took too long")
	}
	// External entity references: refused by the DTD rule, and encoding/xml
	// would not resolve them anyway.
	xxe := `<!DOCTYPE d [<!ENTITY x SYSTEM "file:///etc/passwd">]>` + matchesMessage(probeID, matchAction, onvifMatch("&x;", "", ""))
	if _, err := parseProbeMatches([]byte(xxe), probeID); err == nil {
		t.Fatal("external entity accepted")
	}
}

func TestParseProbeMatchesSizeAndDepthLimits(t *testing.T) {
	big := matchesMessage(probeID, matchAction, onvifMatch("dn:A", strings.Repeat("onvif://x/y ", 4000), ""))
	if len(big) <= wsdMaxDatagram {
		t.Fatal("test message is not oversized")
	}
	if _, err := parseProbeMatches([]byte(big), probeID); err == nil {
		t.Fatal("oversized message accepted")
	}
	deep := strings.Repeat("<a>", 40) + strings.Repeat("</a>", 40)
	if _, err := parseProbeMatches([]byte(matchesMessage(probeID, matchAction, deep+onvifMatch("dn:A", "", ""))), probeID); err == nil {
		t.Fatal("deeply nested message accepted")
	}
	flood := strings.Repeat("<a/>", 7000) // 28 KiB, under the size limit, over the token limit
	if len(flood) >= wsdMaxDatagram {
		t.Fatal("flood is oversized")
	}
	if _, err := parseProbeMatches([]byte(matchesMessage(probeID, matchAction, flood+onvifMatch("dn:A", "", ""))), probeID); err == nil {
		t.Fatal("token flood accepted")
	}
}

func TestParseProbeMatchesHostileText(t *testing.T) {
	var types, scopes, xaddrs []string
	for i := 0; i < 50; i++ {
		types = append(types, "t"+string(rune('a'+i%26))+strings.Repeat("x", i))
		scopes = append(scopes, "onvif://www.onvif.org/location/"+strings.Repeat("s", i))
	}
	types = append(types, "&lt;script&gt;alert(1)&lt;/script&gt;", "a/b", "name\u202e", strings.Repeat("L", 500))
	scopes = append(scopes, "onvif://www.onvif.org/name/Cam%0A%1B%E2%80%AEevil", "café://x")
	xaddrs = append(xaddrs, "javascript:alert(1)", "ftp://192.0.2.9/", "http://user:pw@192.0.2.9/", "http:///nohost", "file:///etc/passwd", "http://192.0.2.9/a", "https://192.0.2.9/b")
	msg := matchesMessage(probeID, matchAction, onvifMatch(strings.Join(types, " "), strings.Join(scopes, " "), strings.Join(xaddrs, " ")))
	info, err := parseProbeMatches([]byte(msg), probeID)
	if err != nil {
		t.Fatal(err)
	}
	if len(info.Types) != wsdMaxTypes || len(info.Scopes) != wsdMaxScopes {
		t.Fatalf("types %d scopes %d", len(info.Types), len(info.Scopes))
	}
	for _, ty := range info.Types {
		if !qnameChars(ty) || len(ty) > wsdTypeLen {
			t.Fatalf("type %q slipped through", ty)
		}
	}
	for _, s := range info.Scopes {
		if !asciiToken(s, wsdScopeLen) {
			t.Fatalf("scope %q slipped through", s)
		}
	}
	if got := strings.Join(info.XAddrs, " "); got != "http://192.0.2.9/a https://192.0.2.9/b" {
		t.Fatalf("xaddrs %q", got)
	}
	// Name came from a scope with LF, ESC and a right-to-left override.
	msg = matchesMessage(probeID, matchAction, onvifMatch("dn:NetworkVideoTransmitter", "onvif://www.onvif.org/name/Cam%0A%1B%E2%80%AEevil", ""))
	info, err = parseProbeMatches([]byte(msg), probeID)
	if err != nil {
		t.Fatal(err)
	}
	if info.Name != "Cam evil" {
		t.Fatalf("name %q", info.Name)
	}
}

func TestParseProbeMatchesTruncatedNeverPanics(t *testing.T) {
	for _, msg := range []string{cameraMessage(), printerMessage()} {
		for i := 0; i < len(msg); i++ {
			_, _ = parseProbeMatches([]byte(msg[:i]), probeID)
		}
	}
}

func TestWSDProbeIsStandard(t *testing.T) {
	id, err := newMessageID()
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`^urn:uuid:[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`).MatchString(id) {
		t.Fatalf("message ID %q", id)
	}
	msg := wsdProbe(id)
	var action, messageID, to string
	var inProbe bool
	dec := xml.NewDecoder(strings.NewReader(string(msg)))
	var cur string
	for {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		switch tk := tok.(type) {
		case xml.StartElement:
			cur = tk.Name.Local
			if cur == "Probe" {
				inProbe = true
			}
		case xml.CharData:
			switch cur {
			case "Action":
				action += string(tk)
			case "MessageID":
				messageID += string(tk)
			case "To":
				to += string(tk)
			}
		case xml.EndElement:
			cur = ""
		}
	}
	if action != wsdProbeAction || messageID != id || to != "urn:schemas-xmlsoap-org:ws:2005:04:discovery" || !inProbe {
		t.Fatalf("probe is malformed: %q %q %q %v", action, messageID, to, inProbe)
	}
	if strings.Contains(string(msg), "Types") || strings.Contains(string(msg), "Scopes") {
		t.Fatal("probe must not filter by type or scope")
	}
}

// fakeWSDDevice listens on loopback, plays a device that answers a Probe, and
// first sends two replies a scanner must ignore.
func fakeWSDDevice(t *testing.T, reply func(id string) []string) *net.UDPAddr {
	t.Helper()
	pc, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Skip("cannot listen on loopback UDP:", err)
	}
	t.Cleanup(func() { pc.Close() })
	go func() {
		buf := make([]byte, 4096)
		n, from, err := pc.ReadFromUDP(buf)
		if err != nil {
			return
		}
		m := regexp.MustCompile(`<wsa:MessageID>([^<]+)</wsa:MessageID>`).FindSubmatch(buf[:n])
		if m == nil || !strings.Contains(string(buf[:n]), wsdProbeAction) {
			return
		}
		for _, r := range reply(string(m[1])) {
			pc.WriteToUDP([]byte(r), from)
		}
	}()
	return pc.LocalAddr().(*net.UDPAddr)
}

func TestWSDDiscoverOverLoopback(t *testing.T) {
	group := fakeWSDDevice(t, func(id string) []string {
		return []string{
			"\x00\x01garbage",
			matchesMessage("urn:uuid:someone-else", matchAction, onvifMatch("dn:NetworkVideoTransmitter", "", "http://192.0.2.99/")),
			strings.Replace(cameraMessage(), probeID, id, 1),
			strings.Replace(printerMessage(), probeID, id, 1), // a second message from the same source merges
		}
	})
	out, err := wsdDiscover(context.Background(), group, 700*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	info := out[netip.MustParseAddr("127.0.0.1")]
	if info == nil || len(out) != 1 {
		t.Fatalf("got %+v", out)
	}
	if got := strings.Join(info.TypeNames(), ","); got != "device,networkvideotransmitter,printdevicetype" {
		t.Fatalf("type names %q", got)
	}
	if info.Name != "AXIS M3045-V" {
		t.Fatalf("name %q", info.Name)
	}
	for _, x := range info.XAddrs {
		if strings.Contains(x, "192.0.2.99") {
			t.Fatal("a reply to someone else's probe was used")
		}
	}
}

func TestWSDDiscoverStopsOnCancel(t *testing.T) {
	group := fakeWSDDevice(t, func(string) []string { return nil })
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(50 * time.Millisecond); cancel() }()
	start := time.Now()
	out, err := wsdDiscover(ctx, group, 5*time.Second)
	if err != nil || len(out) != 0 {
		t.Fatalf("%v %v", out, err)
	}
	if time.Since(start) > time.Second {
		t.Fatalf("cancel took %s", time.Since(start))
	}
}

func TestWSDDiscoverBoundsResponders(t *testing.T) {
	// One source can only ever hold one entry; a flood of datagrams ends at the
	// datagram bound without growing memory beyond the caps.
	group := fakeWSDDevice(t, func(id string) []string {
		var r []string
		for i := 0; i < 200; i++ {
			r = append(r, strings.Replace(cameraMessage(), probeID, id, 1))
		}
		return r
	})
	out, err := wsdDiscover(context.Background(), group, 500*time.Millisecond)
	if err != nil || len(out) != 1 {
		t.Fatalf("%v %v", out, err)
	}
	if len(out[netip.MustParseAddr("127.0.0.1")].Types) > wsdMaxTypes {
		t.Fatal("types not bounded")
	}
}

func TestWSDSanitizeSavedValue(t *testing.T) {
	w := &WSDInfo{Types: []string{"ok:Type", "<b>"}, Scopes: []string{"onvif://a/b", "café"}, XAddrs: []string{"http://192.0.2.1/", "javascript:1"}, Name: "a\x00b\u202e", Hardware: strings.Repeat("h", 500)}
	w.Sanitize()
	if len(w.Types) != 1 || len(w.Scopes) != 1 || len(w.XAddrs) != 1 || w.Name != "ab" || len(w.Hardware) != wsdNameLen {
		t.Fatalf("%+v", w)
	}
}

func FuzzParseProbeMatches(f *testing.F) {
	f.Add([]byte(cameraMessage()))
	f.Add([]byte(printerMessage()))
	f.Add([]byte(`<!DOCTYPE a [<!ENTITY b "c">]><a/>`))
	f.Add([]byte(""))
	f.Fuzz(func(t *testing.T, b []byte) {
		info, err := parseProbeMatches(b, "")
		if err != nil {
			return
		}
		if len(info.Types) > wsdMaxTypes || len(info.Scopes) > wsdMaxScopes || len(info.XAddrs) > wsdMaxXAddrs {
			t.Fatal("bounds exceeded")
		}
		for _, s := range append(append(append([]string{}, info.Types...), info.Scopes...), info.XAddrs...) {
			if !asciiToken(s, wsdScopeLen) {
				t.Fatalf("unsafe text kept: %q", s)
			}
		}
		for _, s := range []string{info.Name, info.Hardware} {
			if s != CleanText(s, wsdNameLen) {
				t.Fatalf("uncleaned text %q", s)
			}
		}
	})
}
