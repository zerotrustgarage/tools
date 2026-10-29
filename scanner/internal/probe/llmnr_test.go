package probe

import (
	"context"
	"encoding/binary"
	"io"
	"net"
	"net/netip"
	"strings"
	"testing"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

const testQName = "20.1.168.192.in-addr.arpa."

// llmnrAnswer builds a response the way a Windows responder does: QR set, no
// other flags, the question echoed, PTR answers with the short name.
func llmnrAnswer(id uint16, qname string, rcode dnsmessage.RCode, owner string, names ...string) []byte {
	b := dnsmessage.NewBuilder(nil, dnsmessage.Header{ID: id, Response: true, Authoritative: false, RCode: rcode})
	b.EnableCompression()
	b.StartQuestions()
	b.Question(dnsmessage.Question{Name: dnsmessage.MustNewName(qname), Type: dnsmessage.TypePTR, Class: dnsmessage.ClassINET})
	b.StartAnswers()
	for _, n := range names {
		b.PTRResource(dnsmessage.ResourceHeader{Name: dnsmessage.MustNewName(owner), Class: dnsmessage.ClassINET, TTL: 30},
			dnsmessage.PTRResource{PTR: dnsmessage.MustNewName(n)})
	}
	out, err := b.Finish()
	if err != nil {
		panic(err)
	}
	return out
}

func TestLLMNRQuestionIsStandard(t *testing.T) {
	q, err := llmnrQuestion(0xbeef, testQName)
	if err != nil {
		t.Fatal(err)
	}
	if binary.BigEndian.Uint16(q[0:]) != 0xbeef {
		t.Fatal("ID not set")
	}
	if flags := binary.BigEndian.Uint16(q[2:]); flags != 0 {
		t.Fatalf("flags %#x: a query has QR, opcode, C, TC, T all clear", flags)
	}
	if binary.BigEndian.Uint16(q[4:]) != 1 || binary.BigEndian.Uint16(q[6:]) != 0 || binary.BigEndian.Uint16(q[8:]) != 0 || binary.BigEndian.Uint16(q[10:]) != 0 {
		t.Fatal("counts are not 1, 0, 0, 0")
	}
	var p dnsmessage.Parser
	if _, err := p.Start(q); err != nil {
		t.Fatal(err)
	}
	qq, err := p.Question()
	if err != nil || qq.Type != dnsmessage.TypePTR || qq.Class != dnsmessage.ClassINET || qq.Name.String() != testQName {
		t.Fatalf("%+v %v", qq, err)
	}
}

func TestParseLLMNRResponse(t *testing.T) {
	info, err := parseLLMNRResponse(llmnrAnswer(7, testQName, dnsmessage.RCodeSuccess, testQName, "FRONTDESK-PC.", "frontdesk-pc.example.com."), 7, testQName)
	if err != nil {
		t.Fatal(err)
	}
	if info.Primary() != "FRONTDESK-PC" || len(info.Names) != 2 || info.Names[1] != "frontdesk-pc.example.com" {
		t.Fatalf("%+v", info)
	}
	// Case-insensitive owner and question names.
	if _, err := parseLLMNRResponse(llmnrAnswer(7, strings.ToUpper(testQName), dnsmessage.RCodeSuccess, strings.ToUpper(testQName), "pc."), 7, testQName); err != nil {
		t.Fatal(err)
	}
	// Duplicates collapse; at most four names are kept.
	many := llmnrAnswer(7, testQName, dnsmessage.RCodeSuccess, testQName, "a.", "A.", "b.", "c.", "d.", "e.", "f.")
	info, err = parseLLMNRResponse(many, 7, testQName)
	if err != nil || len(info.Names) != llmnrMaxNames {
		t.Fatalf("%+v %v", info, err)
	}
}

func TestParseLLMNRResponseRefusals(t *testing.T) {
	ok := llmnrAnswer(7, testQName, dnsmessage.RCodeSuccess, testQName, "pc.")
	cases := map[string][]byte{
		"wrong id":           llmnrAnswer(8, testQName, dnsmessage.RCodeSuccess, testQName, "pc."),
		"name error":         llmnrAnswer(7, testQName, dnsmessage.RCodeNameError, testQName, "pc."),
		"wrong question":     llmnrAnswer(7, "21.1.168.192.in-addr.arpa.", dnsmessage.RCodeSuccess, "21.1.168.192.in-addr.arpa.", "pc."),
		"wrong owner":        llmnrAnswer(7, testQName, dnsmessage.RCodeSuccess, "99.1.168.192.in-addr.arpa.", "pc."),
		"no answers":         llmnrAnswer(7, testQName, dnsmessage.RCodeSuccess, testQName),
		"space in name":      llmnrAnswer(7, testQName, dnsmessage.RCodeSuccess, testQName, "my pc."),
		"control in name":    llmnrAnswer(7, testQName, dnsmessage.RCodeSuccess, testQName, "pc\x1b[2J."),
		"non-ascii name":     llmnrAnswer(7, testQName, dnsmessage.RCodeSuccess, testQName, "pé.local."),
		"markup in name":     llmnrAnswer(7, testQName, dnsmessage.RCodeSuccess, testQName, "<img>."),
		"empty":              nil,
		"header only":        ok[:12],
		"too short":          ok[:5],
		"garbage":            []byte(strings.Repeat("\xff", 64)),
		"oversized":          append(append([]byte{}, ok...), make([]byte, llmnrMaxResponse)...),
		"query not response": func() []byte { q, _ := llmnrQuestion(7, testQName); return q }(),
		"compression loop":   {0, 7, 0x80, 0, 0, 1, 0, 1, 0, 0, 0, 0, 0xc0, 12, 0, 12, 0, 1},
		"huge answer count":  {0, 7, 0x80, 0, 0, 0, 0xff, 0xff, 0, 0, 0, 0},
	}
	for name, b := range cases {
		if info, err := parseLLMNRResponse(b, 7, testQName); err == nil {
			t.Errorf("%s: accepted %+v", name, info)
		}
	}
	for i := 0; i < len(ok); i++ {
		_, _ = parseLLMNRResponse(ok[:i], 7, testQName) // truncation never panics
	}
}

func TestHostnameText(t *testing.T) {
	good := []string{"pc", "PC-01.", "a.b.c", "host_name", "1a", strings.Repeat("a", 63)}
	bad := []string{"", ".", "a..b", "-", "a b", "a\n", "pé", strings.Repeat("a", 64), strings.Repeat("a.", 130), "a/b", "a:b"}
	for _, s := range good {
		if _, ok := hostnameText(s); !ok {
			t.Errorf("%q refused", s)
		}
	}
	for _, s := range bad {
		if _, ok := hostnameText(s); ok {
			t.Errorf("%q accepted", s)
		}
	}
}

// llmnrServer is a loopback TCP responder. handle gets the framed query.
func llmnrServer(t *testing.T, handle func(c net.Conn, query []byte)) int {
	t.Helper()
	l, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Skip("cannot listen on loopback:", err)
	}
	t.Cleanup(func() { l.Close() })
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				var lb [2]byte
				if _, err := io.ReadFull(c, lb[:]); err != nil {
					return
				}
				q := make([]byte, binary.BigEndian.Uint16(lb[:]))
				if _, err := io.ReadFull(c, q); err != nil {
					return
				}
				handle(c, q)
			}()
		}
	}()
	return l.Addr().(*net.TCPAddr).Port
}

func writeFramed(c net.Conn, b []byte) {
	c.Write(binary.BigEndian.AppendUint16(nil, uint16(len(b))))
	c.Write(b)
}

func TestLLMNRQueryOverTCP(t *testing.T) {
	var gotQuestion string
	port := llmnrServer(t, func(c net.Conn, q []byte) {
		var p dnsmessage.Parser
		h, err := p.Start(q)
		if err != nil || h.Response {
			return
		}
		qq, _ := p.Question()
		gotQuestion = qq.Name.String()
		writeFramed(c, llmnrAnswer(h.ID, qq.Name.String(), dnsmessage.RCodeSuccess, qq.Name.String(), "LAB-01."))
	})
	info, err := llmnrQuery(context.Background(), loopback, port, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if info.Primary() != "LAB-01" {
		t.Fatalf("%+v", info)
	}
	if gotQuestion != "1.0.0.127.in-addr.arpa." {
		t.Fatalf("asked %q", gotQuestion)
	}
}

func TestLLMNRQueryHostileServers(t *testing.T) {
	cases := map[string]func(c net.Conn, q []byte){
		"oversized length": func(c net.Conn, q []byte) { c.Write([]byte{0xff, 0xff}); c.Write(make([]byte, 1000)) },
		"zero length":      func(c net.Conn, q []byte) { c.Write([]byte{0, 0}) },
		"short body":       func(c net.Conn, q []byte) { c.Write([]byte{0, 40, 1, 2, 3}) },
		"wrong id": func(c net.Conn, q []byte) {
			writeFramed(c, llmnrAnswer(0, "1.0.0.127.in-addr.arpa.", dnsmessage.RCodeSuccess, "1.0.0.127.in-addr.arpa.", "x."))
		},
		"closes": func(c net.Conn, q []byte) {},
	}
	for name, h := range cases {
		port := llmnrServer(t, h)
		if info, err := llmnrQuery(context.Background(), loopback, port, 500*time.Millisecond); err == nil {
			t.Errorf("%s: accepted %+v", name, info)
		}
	}
	// A server that never answers costs one timeout and no more.
	port := llmnrServer(t, func(c net.Conn, q []byte) { time.Sleep(time.Second) })
	start := time.Now()
	if _, err := llmnrQuery(context.Background(), loopback, port, 300*time.Millisecond); err == nil {
		t.Fatal("silent server produced a result")
	}
	if time.Since(start) > 2*time.Second {
		t.Fatalf("took %s", time.Since(start))
	}
}

func TestLLMNRQueryRefusesNonIPv4(t *testing.T) {
	if _, err := LLMNRQuery(context.Background(), netip.MustParseAddr("fe80::1"), time.Second); err == nil {
		t.Fatal("IPv6 accepted")
	}
}

func TestLLMNRSanitizeSavedValue(t *testing.T) {
	l := &LLMNRInfo{Names: []string{"ok", "bad name", "OK", "x\x00y", "b", "c", "d", "e"}}
	l.Sanitize()
	if len(l.Names) != 4 || l.Names[0] != "ok" || l.Names[1] != "b" {
		t.Fatalf("%+v", l.Names)
	}
}

func FuzzParseLLMNRResponse(f *testing.F) {
	f.Add(llmnrAnswer(7, testQName, dnsmessage.RCodeSuccess, testQName, "FRONTDESK-PC.", "frontdesk-pc.example.com."))
	f.Add(llmnrAnswer(7, testQName, dnsmessage.RCodeNameError, testQName))
	f.Add([]byte{})
	f.Fuzz(func(t *testing.T, b []byte) {
		info, err := parseLLMNRResponse(b, 7, testQName)
		if err != nil {
			return
		}
		if len(info.Names) == 0 || len(info.Names) > llmnrMaxNames {
			t.Fatal("bounds exceeded")
		}
		for _, n := range info.Names {
			if got, ok := hostnameText(n); !ok || got != n {
				t.Fatalf("unsafe name kept: %q", n)
			}
		}
	})
}
