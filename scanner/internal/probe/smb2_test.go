package probe

import (
	"context"
	"encoding/binary"
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

// negotiateResponse builds a SMB2 NEGOTIATE response following [MS-SMB2]
// 2.2.4: a 64-byte header with the server-to-redirector flag, then the
// StructureSize-65 body, then a security buffer (here a short fake blob that
// must be ignored).
func negotiateResponse(dialect uint16, mode uint16, caps uint32, guid [16]byte, sysTime, startTime uint64) []byte {
	h := make([]byte, 64)
	copy(h, []byte{0xfe, 'S', 'M', 'B'})
	binary.LittleEndian.PutUint16(h[4:], 64)
	binary.LittleEndian.PutUint16(h[12:], 0)   // NEGOTIATE
	binary.LittleEndian.PutUint16(h[14:], 1)   // credits granted
	binary.LittleEndian.PutUint32(h[16:], 0x1) // SERVER_TO_REDIR
	binary.LittleEndian.PutUint64(h[24:], 0)   // MessageId
	body := make([]byte, 64)
	binary.LittleEndian.PutUint16(body[0:], 65)
	binary.LittleEndian.PutUint16(body[2:], mode)
	binary.LittleEndian.PutUint16(body[4:], dialect)
	copy(body[8:24], guid[:])
	binary.LittleEndian.PutUint32(body[24:], caps)
	binary.LittleEndian.PutUint32(body[28:], 8388608) // MaxTransactSize
	binary.LittleEndian.PutUint32(body[32:], 8388608)
	binary.LittleEndian.PutUint32(body[36:], 8388608)
	binary.LittleEndian.PutUint64(body[40:], sysTime)
	binary.LittleEndian.PutUint64(body[48:], startTime)
	blob := []byte("fake security buffer: never parsed")
	binary.LittleEndian.PutUint16(body[56:], 128)
	binary.LittleEndian.PutUint16(body[58:], uint16(len(blob)))
	return append(append(h, body...), blob...)
}

var testGUID = [16]byte{0x78, 0x56, 0x34, 0x12, 0xbc, 0x9a, 0xf0, 0xde, 0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08}

const filetime20261029 = 134377488000000000 // 2026-10-29T12:00:00Z

func TestParseSMB2Negotiate(t *testing.T) {
	resp := negotiateResponse(smb2Dialect311, 0x0003, 0x7f, testGUID, filetime20261029, 0)
	info, err := parseSMB2Negotiate(resp)
	if err != nil {
		t.Fatal(err)
	}
	if info.Dialect != "3.1.1" || !info.SigningEnabled || !info.SigningRequired {
		t.Fatalf("%+v", info)
	}
	if info.ServerGUID != "12345678-9abc-def0-0102-030405060708" {
		t.Fatalf("GUID %q", info.ServerGUID)
	}
	if info.SystemTime != "2026-10-29T12:00:00Z" || info.StartTime != "" {
		t.Fatalf("times %q %q", info.SystemTime, info.StartTime)
	}
	if got := strings.Join(info.Capabilities, ","); got != "dfs,leasing,large_mtu,multi_channel,persistent_handles,directory_leasing,encryption" {
		t.Fatalf("capabilities %q", got)
	}
	if !strings.Contains(info.EraHint, "Windows 10") || info.LegacyWindowsDialect() {
		t.Fatalf("era %q", info.EraHint)
	}

	for _, c := range []struct {
		dialect uint16
		name    string
		legacy  bool
		era     string
	}{
		{smb2Dialect202, "2.0.2", true, "Vista"},
		{smb2Dialect210, "2.1", true, "Windows 7"},
		{smb2Dialect300, "3.0", true, "Windows 8 "},
		{smb2Dialect302, "3.0.2", true, "8.1"},
	} {
		got, err := parseSMB2Negotiate(negotiateResponse(c.dialect, 0x0001, 0, testGUID, 0, 0))
		if err != nil || got.Dialect != c.name || got.LegacyWindowsDialect() != c.legacy || !strings.Contains(got.EraHint, c.era) {
			t.Errorf("%s: %+v %v", c.name, got, err)
		}
		if got.SigningRequired || !got.SigningEnabled || got.SystemTime != "" {
			t.Errorf("%s: %+v", c.name, got)
		}
	}
}

func TestParseSMB2NegotiateRefusals(t *testing.T) {
	ok := negotiateResponse(smb2Dialect311, 1, 0, testGUID, filetime20261029, 0)
	mut := func(f func(b []byte)) []byte { b := append([]byte{}, ok...); f(b); return b }
	cases := map[string][]byte{
		"smb1 protocol id":  mut(func(b []byte) { b[0] = 0xff }),
		"bad structure":     mut(func(b []byte) { b[4] = 63 }),
		"error status":      mut(func(b []byte) { binary.LittleEndian.PutUint32(b[8:], 0xc0000022) }),
		"wrong command":     mut(func(b []byte) { b[12] = 1 }),
		"request not reply": mut(func(b []byte) { b[16] = 0 }),
		"body size":         mut(func(b []byte) { b[64] = 36 }),
		"wildcard dialect":  mut(func(b []byte) { binary.LittleEndian.PutUint16(b[68:], 0x02ff) }),
		"unoffered dialect": mut(func(b []byte) { binary.LittleEndian.PutUint16(b[68:], 0x0400) }),
		"empty":             nil,
		"header only":       ok[:64],
		"nearly complete":   ok[:127],
	}
	for name, b := range cases {
		if info, err := parseSMB2Negotiate(b); err == nil {
			t.Errorf("%s: accepted %+v", name, info)
		}
	}
	for i := 0; i < len(ok); i++ {
		_, _ = parseSMB2Negotiate(ok[:i])
	}
	// The security buffer and everything after the fixed fields is ignored,
	// even when it is garbage or claims a huge length.
	junk := mut(func(b []byte) {
		binary.LittleEndian.PutUint16(b[64+56:], 0xffff)
		binary.LittleEndian.PutUint16(b[64+58:], 0xffff)
		binary.LittleEndian.PutUint32(b[64+60:], 0xffffffff)
	})
	if _, err := parseSMB2Negotiate(junk); err != nil {
		t.Fatal(err)
	}
}

func TestFiletimeText(t *testing.T) {
	cases := map[uint64]string{
		0:                          "",
		116444736000000000:         "", // exactly the Unix epoch: treated as unset
		116444736010000000:         "1970-01-01T00:00:01Z",
		filetime20261029:           "2026-10-29T12:00:00Z",
		filetime20261029 + 5000000: "2026-10-29T12:00:00Z", // sub-second part dropped by RFC 3339 seconds format
		^uint64(0):                 "",
		200 * 365 * 86400 * 1e7:    "", // before 1970 + years: 1601 + 200
	}
	for in, want := range cases {
		if got := filetimeText(in); got != want {
			t.Errorf("filetimeText(%d) = %q, want %q", in, got, want)
		}
	}
}

func TestSMB2NegotiateRequestShape(t *testing.T) {
	var guid [16]byte
	var salt [32]byte
	for i := range guid {
		guid[i] = byte(i + 1)
	}
	for i := range salt {
		salt[i] = byte(0xa0 + i)
	}
	req := smb2NegotiateRequest(guid, salt)
	// Transport header: zero byte and a 24-bit length of the rest.
	if req[0] != 0 || int(req[1])<<16|int(req[2])<<8|int(req[3]) != len(req)-4 {
		t.Fatalf("transport header % x for %d bytes", req[:4], len(req))
	}
	m := req[4:]
	if string(m[0:4]) != "\xfeSMB" || binary.LittleEndian.Uint16(m[4:]) != 64 {
		t.Fatal("not an SMB2 header")
	}
	if cmd := binary.LittleEndian.Uint16(m[12:]); cmd != 0 {
		t.Fatalf("command %d: only NEGOTIATE (0) may ever be sent, never SESSION_SETUP (1)", cmd)
	}
	for _, f := range []struct {
		name     string
		off, end int
	}{{"flags", 16, 20}, {"next command", 20, 24}, {"message id", 24, 32}, {"tree id", 36, 40}, {"session id", 40, 48}, {"signature", 48, 64}} {
		for _, b := range m[f.off:f.end] {
			if b != 0 {
				t.Fatalf("%s is not zero: no session, tree or signature exists yet", f.name)
			}
		}
	}
	body := m[64:]
	if binary.LittleEndian.Uint16(body[0:]) != 36 {
		t.Fatal("structure size")
	}
	count := int(binary.LittleEndian.Uint16(body[2:]))
	if count != 5 || binary.LittleEndian.Uint16(body[4:]) != 0x0001 {
		t.Fatalf("dialect count %d, security mode %#x (signing enabled, not required)", count, binary.LittleEndian.Uint16(body[4:]))
	}
	if string(body[12:28]) != string(guid[:]) {
		t.Fatal("client GUID")
	}
	var dialects []uint16
	for i := 0; i < count; i++ {
		dialects = append(dialects, binary.LittleEndian.Uint16(body[36+2*i:]))
	}
	want := []uint16{0x0202, 0x0210, 0x0300, 0x0302, 0x0311}
	for i := range want {
		if dialects[i] != want[i] {
			t.Fatalf("dialects % x", dialects)
		}
	}
	// 3.1.1 needs the pre-authentication integrity context, 8-byte aligned.
	off := int(binary.LittleEndian.Uint32(body[28:]))
	if off%8 != 0 || off < 64+36+2*count || binary.LittleEndian.Uint16(body[32:]) != 1 {
		t.Fatalf("context offset %d count %d", off, binary.LittleEndian.Uint16(body[32:]))
	}
	ctx := m[off:]
	if binary.LittleEndian.Uint16(ctx[0:]) != 1 { // SMB2_PREAUTH_INTEGRITY_CAPABILITIES
		t.Fatal("context type")
	}
	dl := int(binary.LittleEndian.Uint16(ctx[2:]))
	if len(ctx) != 8+dl || dl != 6+len(salt) {
		t.Fatalf("context length %d, data %d", len(ctx), dl)
	}
	if binary.LittleEndian.Uint16(ctx[8:]) != 1 || binary.LittleEndian.Uint16(ctx[10:]) != 32 || binary.LittleEndian.Uint16(ctx[12:]) != 0x0001 {
		t.Fatal("hash algorithm count, salt length or SHA-512 id")
	}
	if string(ctx[14:]) != string(salt[:]) {
		t.Fatal("salt")
	}
}

func smbServer(t *testing.T, handle func(c net.Conn, req []byte)) int {
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
				var th [4]byte
				if _, err := io.ReadFull(c, th[:]); err != nil {
					return
				}
				req := make([]byte, int(th[1])<<16|int(th[2])<<8|int(th[3]))
				if _, err := io.ReadFull(c, req); err != nil {
					return
				}
				handle(c, req)
			}()
		}
	}()
	return l.Addr().(*net.TCPAddr).Port
}

func writeSMB(c net.Conn, msg []byte) {
	c.Write(append([]byte{0, byte(len(msg) >> 16), byte(len(msg) >> 8), byte(len(msg))}, msg...))
}

func TestSMBNegotiateOverLoopback(t *testing.T) {
	var sawCommand uint16 = 99
	port := smbServer(t, func(c net.Conn, req []byte) {
		sawCommand = binary.LittleEndian.Uint16(req[12:])
		writeSMB(c, negotiateResponse(smb2Dialect302, 0x0001, 0x2f, testGUID, filetime20261029, 0))
	})
	info, err := smbNegotiate(context.Background(), loopback, port, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if sawCommand != 0 || info.Dialect != "3.0.2" || info.SigningRequired || info.SystemTime != "2026-10-29T12:00:00Z" {
		t.Fatalf("command %d, %+v", sawCommand, info)
	}
}

func TestSMBNegotiateHostileServers(t *testing.T) {
	good := negotiateResponse(smb2Dialect311, 1, 0, testGUID, 0, 0)
	cases := map[string]func(c net.Conn, req []byte){
		"smb1 reply": func(c net.Conn, req []byte) {
			writeSMB(c, append([]byte{0xff, 'S', 'M', 'B', 0x72}, make([]byte, 200)...))
		},
		"error status": func(c net.Conn, req []byte) {
			b := append([]byte{}, good...)
			b[8] = 0x22
			b[11] = 0xc0
			writeSMB(c, b)
		},
		"short message":   func(c net.Conn, req []byte) { writeSMB(c, good[:70]) },
		"huge length":     func(c net.Conn, req []byte) { c.Write([]byte{0, 0xff, 0xff, 0xff}); c.Write(good) },
		"nonzero type":    func(c net.Conn, req []byte) { c.Write(append([]byte{0x81, 0, 0, 4}, 1, 2, 3, 4)) },
		"truncated body":  func(c net.Conn, req []byte) { c.Write([]byte{0, 0, 1, 0}); c.Write(good[:50]) },
		"closes silently": func(c net.Conn, req []byte) {},
	}
	for name, h := range cases {
		port := smbServer(t, h)
		if info, err := smbNegotiate(context.Background(), loopback, port, 400*time.Millisecond); err == nil {
			t.Errorf("%s: accepted %+v", name, info)
		}
	}
	port := smbServer(t, func(c net.Conn, req []byte) { time.Sleep(time.Second) })
	start := time.Now()
	if _, err := smbNegotiate(context.Background(), loopback, port, 300*time.Millisecond); err == nil || time.Since(start) > 2*time.Second {
		t.Fatalf("silent server: %v after %s", err, time.Since(start))
	}
}

func TestSMBSanitizeSavedValue(t *testing.T) {
	s := &SMBInfo{Dialect: "9.9", EraHint: "a\x00b", ServerGUID: strings.Repeat("g", 80), Capabilities: []string{"dfs", "<b>", "x y"}}
	s.Sanitize()
	if s.Dialect != "" || s.EraHint != "ab" || len(s.ServerGUID) != 36 || len(s.Capabilities) != 1 {
		t.Fatalf("%+v", s)
	}
}

func FuzzParseSMB2Negotiate(f *testing.F) {
	f.Add(negotiateResponse(smb2Dialect311, 3, 0x7f, testGUID, filetime20261029, 1))
	f.Add(negotiateResponse(smb2Dialect210, 1, 0, testGUID, 0, 0))
	f.Add([]byte{})
	f.Fuzz(func(t *testing.T, b []byte) {
		info, err := parseSMB2Negotiate(b)
		if err != nil {
			return
		}
		if _, ok := dialectByName(info.Dialect); !ok {
			t.Fatalf("dialect %q", info.Dialect)
		}
		if len(info.Capabilities) > len(smbCapabilities) || len(info.ServerGUID) != 36 {
			t.Fatalf("%+v", info)
		}
	})
}
