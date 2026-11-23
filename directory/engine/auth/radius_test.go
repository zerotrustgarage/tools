package auth

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/md5"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

var testSecret = []byte("s3cret-shared")

// RFC 2865 section 7.1: user nemo, password arctangent, secret xyzzy5461.
func TestHidePasswordRFC2865Vector(t *testing.T) {
	var ra [16]byte
	hex.Decode(ra[:], []byte("0f403f9473978057bd83d5cb98f4227a"))
	got := hidePassword([]byte("xyzzy5461"), ra, []byte("arctangent"))
	if hex.EncodeToString(got) != "0dbe708d93d413ce3196e43f782a0aee" {
		t.Fatalf("hidden password %x", got)
	}
	if string(unhidePassword([]byte("xyzzy5461"), ra, got)) != "arctangent" {
		t.Fatal("round trip")
	}
	long := []byte(strings.Repeat("0123456789", 5))
	if string(unhidePassword(testSecret, ra, hidePassword(testSecret, ra, long))) != string(long) {
		t.Fatal("multi-block round trip")
	}
}

// The RFC 2865 7.1 Access-Accept has a correct Response Authenticator but no
// Message-Authenticator, so it passes the first check and fails the second.
func TestRFC2865AcceptWithoutMessageAuthenticatorIsRejected(t *testing.T) {
	var ra [16]byte
	hex.Decode(ra[:], []byte("0f403f9473978057bd83d5cb98f4227a"))
	resp, _ := hex.DecodeString("0200002686fe220e7624ba2a1005f6bf9b55e0b20606000000010f06000000000e06c0a80103")
	c := &RADIUSClient{secret: []byte("xyzzy5461")}
	_, _, err := c.verifyResponse(resp, 0, ra)
	if err == nil || !strings.Contains(err.Error(), "Message-Authenticator missing") {
		t.Fatalf("got %v", err)
	}
	resp[5] ^= 1
	if _, _, err = c.verifyResponse(resp, 0, ra); err == nil || !strings.Contains(err.Error(), "Response Authenticator") {
		t.Fatalf("tampered: got %v", err)
	}
}

func unhidePassword(secret []byte, reqAuth [16]byte, hidden []byte) []byte {
	out := make([]byte, len(hidden))
	prev := reqAuth[:]
	for i := 0; i < len(hidden); i += 16 {
		b := md5.Sum(append(append([]byte(nil), secret...), prev...))
		for j := 0; j < 16; j++ {
			out[i+j] = hidden[i+j] ^ b[j]
		}
		prev = hidden[i : i+16]
	}
	return bytes.TrimRight(out, "\x00")
}

type testRequest struct {
	raw      []byte
	id       byte
	auth     [16]byte
	attrs    []radiusAttr
	password []byte
	firstMA  bool
}

func (r testRequest) attr(typ byte) []byte {
	for _, a := range r.attrs {
		if a.typ == typ {
			return a.value
		}
	}
	return nil
}

type respOpts struct {
	noMA, badMA, badAuth, wrongID bool
}

func buildResponse(secret []byte, code byte, req testRequest, attrs []radiusAttr, o respOpts) []byte {
	pkt := []byte{code, req.id, 0, 0}
	if o.wrongID {
		pkt[1]++
	}
	pkt = append(pkt, req.auth[:]...)
	ma := -1
	if !o.noMA {
		ma = len(pkt) + 2
		pkt = append(pkt, attrMessageAuthenticator, 18)
		pkt = append(pkt, make([]byte, 16)...)
	}
	for _, a := range attrs {
		pkt = appendAttr(pkt, a.typ, a.value)
	}
	binary.BigEndian.PutUint16(pkt[2:4], uint16(len(pkt)))
	if ma >= 0 {
		m := hmac.New(md5.New, secret)
		m.Write(pkt)
		copy(pkt[ma:], m.Sum(nil))
		if o.badMA {
			pkt[ma] ^= 0xff
		}
	}
	h := md5.New()
	h.Write(pkt)
	h.Write(secret)
	copy(pkt[4:20], h.Sum(nil))
	if o.badAuth {
		pkt[4] ^= 0xff
	}
	return pkt
}

// fakeRADIUS is an in-process RADIUS server. handle returns the responses to
// send for each request (none drops it).
type fakeRADIUS struct {
	conn   *net.UDPConn
	secret []byte
	handle func(n int, req testRequest) [][]byte

	mu   sync.Mutex
	reqs []testRequest
	errs []string
	done chan struct{}
}

func newFakeRADIUS(t *testing.T, handle func(n int, req testRequest) [][]byte) *fakeRADIUS {
	t.Helper()
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeRADIUS{conn: conn, secret: testSecret, handle: handle, done: make(chan struct{})}
	go f.serve()
	t.Cleanup(func() {
		conn.Close()
		<-f.done
		f.mu.Lock()
		defer f.mu.Unlock()
		for _, e := range f.errs {
			t.Error("server: " + e)
		}
	})
	return f
}

func (f *fakeRADIUS) addr() string { return f.conn.LocalAddr().String() }

func (f *fakeRADIUS) requests() []testRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]testRequest(nil), f.reqs...)
}

func (f *fakeRADIUS) serve() {
	defer close(f.done)
	buf := make([]byte, 4096)
	for {
		n, from, err := f.conn.ReadFromUDP(buf)
		if err != nil {
			return
		}
		req, perr := f.parse(append([]byte(nil), buf[:n]...))
		f.mu.Lock()
		if perr != "" {
			f.errs = append(f.errs, perr)
		}
		f.reqs = append(f.reqs, req)
		count := len(f.reqs)
		f.mu.Unlock()
		for _, resp := range f.handle(count, req) {
			f.conn.WriteToUDP(resp, from)
		}
	}
}

func (f *fakeRADIUS) parse(pkt []byte) (testRequest, string) {
	req := testRequest{raw: pkt}
	if len(pkt) < 20 || pkt[0] != radiusAccessRequest || int(binary.BigEndian.Uint16(pkt[2:4])) != len(pkt) {
		return req, "malformed request"
	}
	req.id = pkt[1]
	copy(req.auth[:], pkt[4:20])
	ma := -1
	for i := 20; i < len(pkt); {
		l := int(pkt[i+1])
		if pkt[i] == attrMessageAuthenticator {
			ma = i + 2
			req.firstMA = i == 20
		}
		req.attrs = append(req.attrs, radiusAttr{typ: pkt[i], value: pkt[i+2 : i+l]})
		i += l
	}
	if ma < 0 {
		return req, "request without Message-Authenticator"
	}
	work := append([]byte(nil), pkt...)
	clear(work[ma : ma+16])
	m := hmac.New(md5.New, f.secret)
	m.Write(work)
	if !hmac.Equal(m.Sum(nil), pkt[ma:ma+16]) {
		return req, "request Message-Authenticator is wrong"
	}
	req.password = unhidePassword(f.secret, req.auth, req.attr(attrUserPassword))
	return req, ""
}

func newTestClient(t *testing.T, addr string, timeout time.Duration, retries int) *RADIUSClient {
	t.Helper()
	c, err := NewRADIUSClient(addr, testSecret, timeout, retries, "ZTG Directory")
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestRADIUSAccept(t *testing.T) {
	srv := newFakeRADIUS(t, func(_ int, req testRequest) [][]byte {
		return [][]byte{buildResponse(testSecret, radiusAccessAccept, req, nil, respOpts{})}
	})
	c := newTestClient(t, srv.addr(), 2*time.Second, 0)
	factor := []byte("push")
	if err := c.Authenticate(context.Background(), "a-alex", factor, nil); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(factor, make([]byte, 4)) {
		t.Fatal("factor not wiped")
	}
	reqs := srv.requests()
	if len(reqs) != 1 {
		t.Fatalf("%d requests", len(reqs))
	}
	r := reqs[0]
	if string(r.attr(attrUserName)) != "a-alex" || string(r.password) != "push" ||
		string(r.attr(attrNASIdentifier)) != "ZTG Directory" || r.attr(attrState) != nil || !r.firstMA {
		t.Fatalf("request: user %q password %q nas %q firstMA %v", r.attr(attrUserName), r.password, r.attr(attrNASIdentifier), r.firstMA)
	}
	if len(r.attr(attrUserPassword))%16 != 0 {
		t.Fatal("User-Password not padded to 16 octets")
	}
}

func TestRADIUSReject(t *testing.T) {
	srv := newFakeRADIUS(t, func(_ int, req testRequest) [][]byte {
		return [][]byte{buildResponse(testSecret, radiusAccessReject, req,
			[]radiusAttr{{attrReplyMessage, []byte("Login denied.\r\n")}}, respOpts{})}
	})
	err := newTestClient(t, srv.addr(), 2*time.Second, 0).Authenticate(context.Background(), "a-alex", []byte("push"), nil)
	if !errors.Is(err, ErrRejected) || !strings.Contains(err.Error(), "Login denied.") || strings.ContainsAny(err.Error(), "\r\n") {
		t.Fatalf("got %v", err)
	}
}

func TestRADIUSChallengeStateRoundTrip(t *testing.T) {
	srv := newFakeRADIUS(t, func(n int, req testRequest) [][]byte {
		if n == 1 {
			return [][]byte{buildResponse(testSecret, radiusAccessChallenge, req, []radiusAttr{
				{attrReplyMessage, []byte("Enter the code")}, {attrState, []byte{1, 2, 3, 0xfe}}}, respOpts{})}
		}
		if !bytes.Equal(req.attr(attrState), []byte{1, 2, 3, 0xfe}) || string(req.password) != "123456" {
			return [][]byte{buildResponse(testSecret, radiusAccessReject, req, nil, respOpts{})}
		}
		return [][]byte{buildResponse(testSecret, radiusAccessAccept, req, nil, respOpts{})}
	})
	c := newTestClient(t, srv.addr(), 2*time.Second, 0)
	err := c.Authenticate(context.Background(), "a-alex", []byte("phone"), nil)
	var ch *ChallengeError
	if !errors.As(err, &ch) || ch.Message != "Enter the code" {
		t.Fatalf("got %v", err)
	}
	if err := c.Authenticate(context.Background(), "a-alex", []byte("123456"), ch.State); err != nil {
		t.Fatalf("answer to challenge: %v", err)
	}
}

func TestRADIUSInvalidResponsesAreDiscarded(t *testing.T) {
	for name, o := range map[string]respOpts{
		"wrong Response Authenticator":    {badAuth: true},
		"missing Message-Authenticator":   {noMA: true},
		"wrong Message-Authenticator":     {badMA: true},
		"identifier of another request":   {wrongID: true},
		"accept without MA and wrong ids": {noMA: true, wrongID: true},
	} {
		t.Run(name, func(t *testing.T) {
			srv := newFakeRADIUS(t, func(_ int, req testRequest) [][]byte {
				return [][]byte{buildResponse(testSecret, radiusAccessAccept, req, nil, o)}
			})
			err := newTestClient(t, srv.addr(), 150*time.Millisecond, 0).Authenticate(context.Background(), "a-alex", []byte("push"), nil)
			if !errors.Is(err, ErrInvalidResponse) {
				t.Fatalf("an invalid Access-Accept was not refused: %v", err)
			}
		})
	}
}

func TestRADIUSValidResponseAfterForgedOne(t *testing.T) {
	srv := newFakeRADIUS(t, func(_ int, req testRequest) [][]byte {
		return [][]byte{
			buildResponse(testSecret, radiusAccessAccept, req, nil, respOpts{badAuth: true}),
			buildResponse(testSecret, radiusAccessReject, req, nil, respOpts{}),
		}
	})
	err := newTestClient(t, srv.addr(), 2*time.Second, 0).Authenticate(context.Background(), "a-alex", []byte("push"), nil)
	if !errors.Is(err, ErrRejected) {
		t.Fatalf("got %v", err)
	}
}

func TestRADIUSRetriesResendTheSamePacket(t *testing.T) {
	srv := newFakeRADIUS(t, func(n int, req testRequest) [][]byte {
		if n < 3 {
			return nil
		}
		return [][]byte{buildResponse(testSecret, radiusAccessAccept, req, nil, respOpts{})}
	})
	if err := newTestClient(t, srv.addr(), 100*time.Millisecond, 2).Authenticate(context.Background(), "a-alex", []byte("push"), nil); err != nil {
		t.Fatal(err)
	}
	reqs := srv.requests()
	if len(reqs) != 3 || !bytes.Equal(reqs[0].raw, reqs[1].raw) || !bytes.Equal(reqs[0].raw, reqs[2].raw) {
		t.Fatalf("%d requests; retransmissions must be identical", len(reqs))
	}
}

func TestRADIUSTimeout(t *testing.T) {
	srv := newFakeRADIUS(t, func(int, testRequest) [][]byte { return nil })
	start := time.Now()
	err := newTestClient(t, srv.addr(), 80*time.Millisecond, 1).Authenticate(context.Background(), "a-alex", []byte("push"), nil)
	if !errors.Is(err, ErrRADIUSTimeout) {
		t.Fatalf("got %v", err)
	}
	if time.Since(start) < 150*time.Millisecond {
		t.Fatal("returned before both attempts timed out")
	}
	if n := len(srv.requests()); n != 2 {
		t.Fatalf("%d requests, want 2", n)
	}
}

func TestRADIUSContextCancel(t *testing.T) {
	srv := newFakeRADIUS(t, func(int, testRequest) [][]byte { return nil })
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(50*time.Millisecond, cancel)
	err := newTestClient(t, srv.addr(), 10*time.Second, 0).Authenticate(ctx, "a-alex", []byte("push"), nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v", err)
	}
}

func TestRADIUSArguments(t *testing.T) {
	if _, err := NewRADIUSClient("proxy:1812", nil, time.Second, 0, "x"); err == nil {
		t.Error("empty secret accepted")
	}
	if _, err := NewRADIUSClient("proxy", testSecret, time.Second, 0, "x"); err == nil {
		t.Error("server without port accepted")
	}
	if _, err := NewRADIUSClient("proxy:1812", testSecret, 0, 0, "x"); err == nil {
		t.Error("zero timeout accepted")
	}
	if _, err := NewRADIUSClient("proxy:1812", testSecret, time.Second, 0, ""); err == nil {
		t.Error("empty NAS-Identifier accepted")
	}
	c := newTestClient(t, "127.0.0.1:9", time.Second, 0)
	long := bytes.Repeat([]byte("1"), 129)
	if err := c.Authenticate(context.Background(), "a-alex", long, nil); err == nil {
		t.Error("factor over 128 bytes accepted")
	}
	if !bytes.Equal(long, make([]byte, 129)) {
		t.Error("factor not wiped on an argument error")
	}
	if err := c.Authenticate(context.Background(), "", []byte("push"), nil); err == nil {
		t.Error("empty user accepted")
	}
	c.Close()
	if err := c.Authenticate(context.Background(), "a-alex", []byte("push"), nil); err == nil {
		t.Error("closed client used")
	}
}

func TestCleanReply(t *testing.T) {
	if got := cleanReply("a\x00b\x1b[31mc\xff"); got != "a b [31mc?" {
		t.Fatalf("got %q", got)
	}
	if got := cleanReply(strings.Repeat("é", 400)); len(got) > maxReplyMessage || !strings.HasSuffix(got, "é") {
		t.Fatalf("cut inside a rune: %d bytes", len(got))
	}
}
