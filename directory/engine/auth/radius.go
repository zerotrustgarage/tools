package auth

import (
	"context"
	"crypto/hmac"
	"crypto/md5"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// RADIUS packet codes and attribute types (RFC 2865, RFC 3579).
const (
	radiusAccessRequest   = 1
	radiusAccessAccept    = 2
	radiusAccessReject    = 3
	radiusAccessChallenge = 11

	attrUserName             = 1
	attrUserPassword         = 2
	attrReplyMessage         = 18
	attrState                = 24
	attrNASIdentifier        = 32
	attrMessageAuthenticator = 80

	radiusHeaderLen  = 20
	radiusMaxPacket  = 4096
	radiusMaxFactor  = 128 // User-Password limit (RFC 2865 5.2)
	radiusMaxAttrVal = 253
	maxReplyMessage  = 512
)

// ErrRejected reports an Access-Reject: the second factor was refused.
var ErrRejected = errors.New("the second factor was rejected")

// ErrInvalidResponse reports that only invalid responses arrived: a wrong
// Response Authenticator, a missing or wrong Message-Authenticator, or a
// malformed packet. Invalid responses are discarded (RFC 2865 3, RFC 3579
// 3.2), so the client keeps waiting for a valid one until the timeout.
var ErrInvalidResponse = errors.New("the RADIUS server sent no valid response")

// ErrRADIUSTimeout reports that no response arrived after every retry.
var ErrRADIUSTimeout = errors.New("the RADIUS server did not answer in time")

// SecondFactor checks a second factor for user after the DC has checked the
// password. RADIUSClient implements it.
type SecondFactor interface {
	Authenticate(ctx context.Context, user string, factor []byte, state []byte) error
}

// ChallengeError reports an Access-Challenge. The caller shows Message
// (untrusted server text, control characters removed) and calls
// Authenticate again with the user's response as the factor and State.
type ChallengeError struct {
	Message string
	State   []byte
}

func (e *ChallengeError) Error() string {
	if e.Message == "" {
		return "the RADIUS server asked for more information"
	}
	return "the RADIUS server asked for more information: " + e.Message
}

// RADIUSClient sends RFC 2865 Access-Requests over UDP with a
// Message-Authenticator (RFC 3579 3.2) and accepts only responses with a
// valid Response Authenticator and a valid Message-Authenticator
// (CVE-2024-3596, Blast-RADIUS; Microsoft KB5040268). It is safe for
// concurrent use; each Authenticate uses its own socket.
type RADIUSClient struct {
	server  string
	secret  []byte
	timeout time.Duration
	retries int
	nasID   string
}

// NewRADIUSClient returns a client for server (host:port). secret is copied;
// the caller may wipe its own copy. timeout is the wait for each
// transmission and retries the number of retransmissions after the first.
func NewRADIUSClient(server string, secret []byte, timeout time.Duration, retries int, nasID string) (*RADIUSClient, error) {
	if err := checkHostPort(server); err != nil {
		return nil, fmt.Errorf("RADIUS server: %w", err)
	}
	if len(secret) == 0 {
		return nil, errors.New("the RADIUS shared secret is empty")
	}
	if timeout <= 0 || timeout > 10*time.Minute {
		return nil, errors.New("the RADIUS timeout must be positive and at most 10 minutes")
	}
	if retries < 0 || retries > 10 {
		return nil, errors.New("RADIUS retries must be from 0 to 10")
	}
	if err := checkNASID(nasID); err != nil {
		return nil, fmt.Errorf("NAS-Identifier: %w", err)
	}
	return &RADIUSClient{server: server, secret: append([]byte(nil), secret...), timeout: timeout, retries: retries, nasID: nasID}, nil
}

// Close wipes the shared secret. The client cannot be used afterwards; do
// not call Close while an Authenticate call is running.
func (c *RADIUSClient) Close() error {
	wipe(c.secret)
	c.secret = nil
	return nil
}

// Authenticate sends one Access-Request for user with factor in
// User-Password and, when not nil, state in State. With Duo's
// radius_server_duo_only mode the factor is "push", "phone", "sms", a
// numbered variant such as "push2", or a passcode; the AD password is never
// sent. It returns nil on Access-Accept, an error wrapping ErrRejected on
// Access-Reject, a *ChallengeError on Access-Challenge, ErrRADIUSTimeout or
// ErrInvalidResponse when no valid answer arrives, or a network or context
// error. factor is wiped before Authenticate returns.
func (c *RADIUSClient) Authenticate(ctx context.Context, user string, factor []byte, state []byte) error {
	defer wipe(factor)
	if len(c.secret) == 0 {
		return errors.New("the RADIUS client is closed")
	}
	if user == "" || len(user) > radiusMaxAttrVal || hasControl(user) {
		return errors.New("the RADIUS user name must be 1 to 253 bytes without control characters")
	}
	if len(factor) == 0 || len(factor) > radiusMaxFactor {
		return fmt.Errorf("the second factor must be 1 to %d bytes", radiusMaxFactor)
	}
	if len(state) > radiusMaxAttrVal {
		return errors.New("the RADIUS State is longer than 253 bytes")
	}
	var head [17]byte // identifier and Request Authenticator
	if _, err := rand.Read(head[:]); err != nil {
		return fmt.Errorf("random RADIUS authenticator: %w", err)
	}
	id := head[0]
	var reqAuth [16]byte
	copy(reqAuth[:], head[1:])
	pkt := c.buildRequest(id, reqAuth, user, factor, state)
	defer wipe(pkt)

	var d net.Dialer
	conn, err := d.DialContext(ctx, "udp", c.server)
	if err != nil {
		return fmt.Errorf("connect to RADIUS server %s: %w", c.server, err)
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()

	buf := make([]byte, radiusMaxPacket+1)
	var invalid error
	for attempt := 0; attempt <= c.retries; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		// A retransmission repeats the packet exactly, with the same
		// Identifier and Request Authenticator (RFC 2865 2.5), so the server
		// can recognize it as a duplicate.
		if _, err := conn.Write(pkt); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return fmt.Errorf("send RADIUS request to %s: %w", c.server, err)
		}
		deadline := time.Now().Add(c.timeout)
		if dl, ok := ctx.Deadline(); ok && dl.Before(deadline) {
			deadline = dl
		}
		if err := conn.SetReadDeadline(deadline); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return err
		}
		for {
			n, err := conn.Read(buf)
			if err != nil {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				var ne net.Error
				if errors.As(err, &ne) && ne.Timeout() {
					break
				}
				return fmt.Errorf("receive RADIUS response from %s: %w", c.server, err)
			}
			code, attrs, err := c.verifyResponse(buf[:n], id, reqAuth)
			if err != nil {
				invalid = err
				continue
			}
			return responseResult(code, attrs)
		}
	}
	if invalid != nil {
		return fmt.Errorf("%w from %s: %v", ErrInvalidResponse, c.server, invalid)
	}
	return fmt.Errorf("%w (%s, %d attempts)", ErrRADIUSTimeout, c.server, c.retries+1)
}

// buildRequest assembles an Access-Request. Message-Authenticator is the
// first attribute, as recommended after CVE-2024-3596, and is computed last
// as HMAC-MD5 over the whole packet with its own value zeroed (RFC 3579 3.2).
func (c *RADIUSClient) buildRequest(id byte, reqAuth [16]byte, user string, factor, state []byte) []byte {
	hidden := hidePassword(c.secret, reqAuth, factor)
	defer wipe(hidden)
	size := radiusHeaderLen + 18 + 2 + len(user) + 2 + len(hidden) + 2 + len(c.nasID)
	if state != nil {
		size += 2 + len(state)
	}
	pkt := make([]byte, 0, size)
	pkt = append(pkt, radiusAccessRequest, id, 0, 0)
	pkt = append(pkt, reqAuth[:]...)
	maOffset := len(pkt) + 2
	pkt = append(pkt, attrMessageAuthenticator, 18)
	pkt = append(pkt, make([]byte, 16)...)
	pkt = appendAttr(pkt, attrUserName, []byte(user))
	pkt = appendAttr(pkt, attrUserPassword, hidden)
	pkt = appendAttr(pkt, attrNASIdentifier, []byte(c.nasID))
	if state != nil {
		pkt = appendAttr(pkt, attrState, state)
	}
	binary.BigEndian.PutUint16(pkt[2:4], uint16(len(pkt)))
	mac := hmac.New(md5.New, c.secret)
	mac.Write(pkt)
	copy(pkt[maOffset:maOffset+16], mac.Sum(nil))
	return pkt
}

func appendAttr(pkt []byte, typ byte, value []byte) []byte {
	pkt = append(pkt, typ, byte(2+len(value)))
	return append(pkt, value...)
}

// hidePassword applies the User-Password hiding of RFC 2865 5.2: the value
// is padded with zeros to a multiple of 16, then c(1) = p(1) XOR MD5(S + RA)
// and c(i) = p(i) XOR MD5(S + c(i-1)).
func hidePassword(secret []byte, reqAuth [16]byte, password []byte) []byte {
	n := (len(password) + 15) / 16 * 16
	if n == 0 {
		n = 16
	}
	out := make([]byte, n)
	copy(out, password)
	h := md5.New()
	prev := reqAuth[:]
	for i := 0; i < n; i += 16 {
		h.Reset()
		h.Write(secret)
		h.Write(prev)
		b := h.Sum(nil)
		for j := 0; j < 16; j++ {
			out[i+j] ^= b[j]
		}
		prev = out[i : i+16]
	}
	return out
}

type radiusAttr struct {
	typ   byte
	value []byte
}

// verifyResponse checks a response against the request: identifier, code,
// length, Response Authenticator (RFC 2865 3) and a Message-Authenticator,
// which is required on every Access-Accept, Access-Reject and
// Access-Challenge (RFC 3579 3.2; CVE-2024-3596).
func (c *RADIUSClient) verifyResponse(pkt []byte, id byte, reqAuth [16]byte) (byte, []radiusAttr, error) {
	if len(pkt) < radiusHeaderLen {
		return 0, nil, errors.New("packet shorter than a RADIUS header")
	}
	length := int(binary.BigEndian.Uint16(pkt[2:4]))
	if length < radiusHeaderLen || length > radiusMaxPacket || length > len(pkt) {
		return 0, nil, errors.New("invalid RADIUS length")
	}
	pkt = pkt[:length] // octets beyond Length are padding (RFC 2865 3)
	if pkt[1] != id {
		return 0, nil, errors.New("identifier does not match the request")
	}
	code := pkt[0]
	if code != radiusAccessAccept && code != radiusAccessReject && code != radiusAccessChallenge {
		return 0, nil, fmt.Errorf("unexpected RADIUS code %d", code)
	}
	h := md5.New()
	h.Write(pkt[:4])
	h.Write(reqAuth[:])
	h.Write(pkt[radiusHeaderLen:])
	h.Write(c.secret)
	if !hmac.Equal(h.Sum(nil), pkt[4:20]) {
		return 0, nil, errors.New("wrong Response Authenticator")
	}
	var attrs []radiusAttr
	maOffset := -1
	for i := radiusHeaderLen; i < len(pkt); {
		if i+2 > len(pkt) {
			return 0, nil, errors.New("truncated attribute")
		}
		l := int(pkt[i+1])
		if l < 2 || i+l > len(pkt) {
			return 0, nil, errors.New("invalid attribute length")
		}
		if pkt[i] == attrMessageAuthenticator {
			if maOffset >= 0 {
				return 0, nil, errors.New("more than one Message-Authenticator")
			}
			if l != 18 {
				return 0, nil, errors.New("invalid Message-Authenticator length")
			}
			maOffset = i + 2
		}
		attrs = append(attrs, radiusAttr{typ: pkt[i], value: pkt[i+2 : i+l]})
		i += l
	}
	if maOffset < 0 {
		return 0, nil, errors.New("Message-Authenticator missing (required, CVE-2024-3596)")
	}
	work := append([]byte(nil), pkt...)
	copy(work[4:20], reqAuth[:])
	clear(work[maOffset : maOffset+16])
	mac := hmac.New(md5.New, c.secret)
	mac.Write(work)
	if !hmac.Equal(mac.Sum(nil), pkt[maOffset:maOffset+16]) {
		return 0, nil, errors.New("wrong Message-Authenticator")
	}
	// Copy values out of the receive buffer, which is reused.
	for i := range attrs {
		attrs[i].value = append([]byte(nil), attrs[i].value...)
	}
	return code, attrs, nil
}

func responseResult(code byte, attrs []radiusAttr) error {
	var msg []string
	var state []byte
	for _, a := range attrs {
		switch a.typ {
		case attrReplyMessage:
			msg = append(msg, string(a.value))
		case attrState:
			state = a.value
		}
	}
	text := cleanReply(strings.Join(msg, " "))
	switch code {
	case radiusAccessAccept:
		return nil
	case radiusAccessReject:
		if text != "" {
			return fmt.Errorf("%w: %s", ErrRejected, text)
		}
		return ErrRejected
	default:
		return &ChallengeError{Message: text, State: state}
	}
}

// cleanReply makes Reply-Message text safe to show: valid UTF-8, no control
// characters, at most maxReplyMessage bytes.
func cleanReply(s string) string {
	s = strings.ToValidUTF8(s, "?")
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, s)
	s = strings.TrimSpace(s)
	if len(s) > maxReplyMessage {
		cut := maxReplyMessage
		for cut > 0 && !utf8.RuneStart(s[cut]) {
			cut--
		}
		s = s[:cut]
	}
	return s
}

var _ SecondFactor = (*RADIUSClient)(nil)
