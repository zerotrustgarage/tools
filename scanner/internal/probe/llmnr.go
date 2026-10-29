package probe

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"syscall"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

// LLMNRInfo is what a host's Link-Local Multicast Name Resolution responder
// (RFC 4795) says its own name is. Windows hosts answer when LLMNR is enabled
// (hardened networks often disable it, and then there is no answer).
type LLMNRInfo struct {
	// Names are the PTR answers for the host's address, usually one short
	// name, sometimes also a fully qualified one. Untrusted device text.
	Names []string `json:"names,omitempty"`
}

// Primary is the first name, or "".
func (l *LLMNRInfo) Primary() string {
	if l == nil || len(l.Names) == 0 {
		return ""
	}
	return l.Names[0]
}

const (
	llmnrPort        = 5355
	llmnrMaxResponse = 4096 // RFC 4795 allows TCP responses beyond one UDP packet; a name answer is tiny
	llmnrMaxAnswers  = 16
	llmnrMaxNames    = 4
	llmnrNameLen     = 253
)

// LLMNRQuery asks addr for the name it answers to, by the one query RFC 4795
// section 2.4 prescribes for a PTR lookup of a known address: a unicast query
// over TCP port 5355, with the IPv4 TTL set to one (section 2.5), so only a
// host on the same link can answer. (Unicast UDP queries must be discarded by
// responders, so the mDNS-style unicast UDP query would never work.) The
// scanner only asks; it never listens on 5355 and never sends a multicast LLMNR
// query, so it cannot be mistaken for, or used as, a name-poisoning responder.
func LLMNRQuery(ctx context.Context, addr netip.Addr, timeout time.Duration) (*LLMNRInfo, error) {
	return llmnrQuery(ctx, addr, llmnrPort, timeout)
}

func llmnrQuery(ctx context.Context, addr netip.Addr, port int, timeout time.Duration) (*LLMNRInfo, error) {
	if !addr.Is4() {
		return nil, errors.New("LLMNR is queried over IPv4 only")
	}
	qname, err := reverseName(addr)
	if err != nil {
		return nil, err
	}
	var idb [2]byte
	if _, err := rand.Read(idb[:]); err != nil {
		return nil, err
	}
	id := binary.BigEndian.Uint16(idb[:])
	msg, err := llmnrQuestion(id, qname)
	if err != nil {
		return nil, err
	}
	d := net.Dialer{Timeout: timeout, Control: func(_, _ string, c syscall.RawConn) error {
		var serr error
		if err := c.Control(func(fd uintptr) { serr = setTTLOne(fd) }); err != nil {
			return err
		}
		return serr
	}}
	c, err := d.DialContext(ctx, "tcp4", net.JoinHostPort(addr.String(), strconv.Itoa(port)))
	if err != nil {
		return nil, err
	}
	defer c.Close()
	deadline := time.Now().Add(timeout)
	if dl, ok := ctx.Deadline(); ok && dl.Before(deadline) {
		deadline = dl
	}
	_ = c.SetDeadline(deadline)
	frame := binary.BigEndian.AppendUint16(nil, uint16(len(msg))) // DNS-over-TCP framing (RFC 1035 4.2.2)
	if _, err := c.Write(append(frame, msg...)); err != nil {
		return nil, err
	}
	var lb [2]byte
	if _, err := io.ReadFull(c, lb[:]); err != nil {
		return nil, err
	}
	n := int(binary.BigEndian.Uint16(lb[:]))
	if n < 12 || n > llmnrMaxResponse {
		return nil, errors.New("LLMNR response has an unreasonable length")
	}
	resp := make([]byte, n)
	if _, err := io.ReadFull(c, resp); err != nil {
		return nil, err
	}
	return parseLLMNRResponse(resp, id, qname)
}

// llmnrQuestion is a standard query (all flags clear) for a PTR record.
func llmnrQuestion(id uint16, name string) ([]byte, error) {
	n, err := dnsmessage.NewName(name)
	if err != nil {
		return nil, err
	}
	m := dnsmessage.Message{
		Header:    dnsmessage.Header{ID: id},
		Questions: []dnsmessage.Question{{Name: n, Type: dnsmessage.TypePTR, Class: dnsmessage.ClassINET}},
	}
	return m.Pack()
}

var errLLMNR = errors.New("no usable LLMNR answer")

// parseLLMNRResponse accepts only a successful response to this exact query:
// same ID, QR set, RCODE 0, the question echoed, and PTR answers whose owner is
// the question name. Each name must be a plain hostname.
func parseLLMNRResponse(b []byte, id uint16, qname string) (*LLMNRInfo, error) {
	if len(b) < 12 || len(b) > llmnrMaxResponse {
		return nil, errLLMNR
	}
	var p dnsmessage.Parser
	h, err := p.Start(b)
	if err != nil {
		return nil, err
	}
	if !h.Response || h.ID != id || h.RCode != dnsmessage.RCodeSuccess {
		return nil, errLLMNR
	}
	q, err := p.Question()
	if err != nil {
		return nil, errLLMNR
	}
	if q.Type != dnsmessage.TypePTR || q.Class != dnsmessage.ClassINET || !strings.EqualFold(q.Name.String(), qname) {
		return nil, errLLMNR
	}
	if err := p.SkipAllQuestions(); err != nil {
		return nil, errLLMNR
	}
	info := &LLMNRInfo{}
	for i := 0; i < llmnrMaxAnswers; i++ {
		ah, err := p.AnswerHeader()
		if err != nil {
			break
		}
		if ah.Type != dnsmessage.TypePTR || ah.Class != dnsmessage.ClassINET || !strings.EqualFold(ah.Name.String(), qname) {
			if err := p.SkipAnswer(); err != nil {
				break
			}
			continue
		}
		ptr, err := p.PTRResource()
		if err != nil {
			break
		}
		name, ok := hostnameText(ptr.PTR.String())
		if !ok || containsString(info.Names, name) {
			continue
		}
		if info.Names = append(info.Names, name); len(info.Names) >= llmnrMaxNames {
			break
		}
	}
	if len(info.Names) == 0 {
		return nil, errLLMNR
	}
	return info, nil
}

// hostnameText returns name without its trailing dot if it is a plain DNS
// hostname: dot-separated labels of 1 to 63 letters, digits, hyphens (not first or
// last) and underscores. Anything else (spaces, control or non-ASCII characters) is
// refused rather than repaired.
func hostnameText(name string) (string, bool) {
	name = strings.TrimSuffix(name, ".")
	if name == "" || len(name) > llmnrNameLen {
		return "", false
	}
	for _, label := range strings.Split(name, ".") {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return "", false
		}
		for i := 0; i < len(label); i++ {
			c := label[i]
			if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
				return "", false
			}
		}
	}
	return name, true
}

func containsString(list []string, s string) bool {
	for _, x := range list {
		if strings.EqualFold(x, s) {
			return true
		}
	}
	return false
}

// Sanitize re-applies the name checks to a value read from a saved file.
func (l *LLMNRInfo) Sanitize() {
	var out []string
	for _, n := range l.Names {
		if name, ok := hostnameText(n); ok && !containsString(out, name) && len(out) < llmnrMaxNames {
			out = append(out, name)
		}
	}
	l.Names = out
}
