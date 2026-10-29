package probe

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"net/url"
	"sort"
	"strings"
	"time"
)

// WSDInfo is what a device announces in a WS-Discovery ProbeMatch: the
// device types it implements (for example an ONVIF camera's
// NetworkVideoTransmitter or a printer's PrintDeviceType), its scopes, and the
// transport addresses of its metadata endpoint. Everything in it comes from the
// device and is untrusted text. The addresses are recorded, never contacted.
type WSDInfo struct {
	Types  []string `json:"types,omitempty"`
	Scopes []string `json:"scopes,omitempty"`
	XAddrs []string `json:"xaddrs,omitempty"`
	// Name and Hardware come from the ONVIF scopes
	// onvif://www.onvif.org/name/... and .../hardware/..., when present.
	Name     string `json:"name,omitempty"`
	Hardware string `json:"hardware,omitempty"`
}

// Bounds on what one responder and one scan may contribute. A hostile or
// broken device cannot make a scan store or parse more than this.
const (
	wsdPort          = 3702
	wsdMaxDatagram   = 32 * 1024 // a ProbeMatch is one UDP datagram; real ones are 1 to 3 KiB
	wsdMaxDatagrams  = 8192      // datagrams read in one discovery
	wsdMaxResponders = 2048
	wsdMaxTokens     = 4000 // XML tokens in one message
	wsdMaxDepth      = 16
	wsdMaxTypes      = 16
	wsdMaxScopes     = 24
	wsdMaxXAddrs     = 4
	wsdTypeLen       = 120
	wsdScopeLen      = 200
	wsdXAddrLen      = 200
	wsdNameLen       = 80
)

var wsdGroup = &net.UDPAddr{IP: net.IPv4(239, 255, 255, 250), Port: wsdPort}

const (
	wsdProbeAction = "http://schemas.xmlsoap.org/ws/2005/04/discovery/Probe"
	wsdMatchesTail = "/ProbeMatches"
)

// wsdProbe builds the standard WS-Discovery 1.0 Probe: SOAP 1.2 over UDP to the
// discovery multicast group, with no Types or Scopes filter, so every
// discoverable device may answer. It is the same message Windows sends when it
// browses the network.
func wsdProbe(messageID string) []byte {
	return []byte(`<?xml version="1.0" encoding="utf-8"?>` +
		`<soap:Envelope xmlns:soap="http://www.w3.org/2003/05/soap-envelope" ` +
		`xmlns:wsa="http://schemas.xmlsoap.org/ws/2004/08/addressing" ` +
		`xmlns:wsd="http://schemas.xmlsoap.org/ws/2005/04/discovery">` +
		`<soap:Header><wsa:To>urn:schemas-xmlsoap-org:ws:2005:04:discovery</wsa:To>` +
		`<wsa:Action>` + wsdProbeAction + `</wsa:Action>` +
		`<wsa:MessageID>` + messageID + `</wsa:MessageID></soap:Header>` +
		`<soap:Body><wsd:Probe/></soap:Body></soap:Envelope>`)
}

func newMessageID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	b[6] = b[6]&0x0f | 0x40 // version 4
	b[8] = b[8]&0x3f | 0x80 // RFC 4122 variant
	return fmt.Sprintf("urn:uuid:%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:]), nil
}

// WSDDiscover sends one multicast WS-Discovery Probe on the local segment (UDP
// 3702) and collects ProbeMatches for wait. Like SSDPDiscover it leaves through
// the default interface and reaches only that link. Replies are matched to the
// Probe by their RelatesTo, size-limited and parsed strictly. A caller decides
// which responders it cares about; nothing here contacts any address a reply names.
func WSDDiscover(ctx context.Context, wait time.Duration) (map[netip.Addr]*WSDInfo, error) {
	return wsdDiscover(ctx, wsdGroup, wait)
}

func wsdDiscover(ctx context.Context, group *net.UDPAddr, wait time.Duration) (map[netip.Addr]*WSDInfo, error) {
	id, err := newMessageID()
	if err != nil {
		return nil, err
	}
	c, err := net.ListenUDP("udp4", &net.UDPAddr{})
	if err != nil {
		return nil, err
	}
	defer c.Close()
	stop := context.AfterFunc(ctx, func() { _ = c.SetReadDeadline(time.Now()) })
	defer stop()
	if _, err := c.WriteToUDP(wsdProbe(id), group); err != nil {
		return nil, err
	}
	deadline := time.Now().Add(wait)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	_ = c.SetReadDeadline(deadline)
	out := map[netip.Addr]*WSDInfo{}
	buf := make([]byte, wsdMaxDatagram+1)
	for i := 0; i < wsdMaxDatagrams; i++ {
		n, src, err := c.ReadFromUDP(buf)
		if err != nil {
			break
		}
		if n > wsdMaxDatagram {
			continue // larger than any legitimate ProbeMatch
		}
		addr, ok := netip.AddrFromSlice(src.IP)
		if !ok {
			continue
		}
		addr = addr.Unmap()
		info, err := parseProbeMatches(buf[:n], id)
		if err != nil {
			continue
		}
		if cur := out[addr]; cur != nil {
			cur.merge(info)
		} else if len(out) < wsdMaxResponders {
			out[addr] = info
		}
	}
	return out, nil
}

var errWSD = errors.New("not a ProbeMatches message")

// parseProbeMatches reads one ProbeMatches message. It refuses documents with a
// DTD or entity declaration (SOAP forbids a DTD, and refusing it removes the
// entity-expansion class of attacks outright; encoding/xml does not resolve
// external entities and expands no custom ones in any case), anything over the
// size, token or depth bound, and, when relatesTo is not empty, a message that
// answers some other Probe. It keeps only text that passes the checks in
// cleanTypes, cleanScopes and cleanXAddrs.
func parseProbeMatches(b []byte, relatesTo string) (*WSDInfo, error) {
	if len(b) == 0 || len(b) > wsdMaxDatagram {
		return nil, errWSD
	}
	upper := bytes.ToUpper(b)
	if bytes.Contains(upper, []byte("<!DOCTYPE")) || bytes.Contains(upper, []byte("<!ENTITY")) {
		return nil, errors.New("DTD or entity declaration refused")
	}
	dec := xml.NewDecoder(bytes.NewReader(b))
	dec.Strict = true
	var (
		path                  []string
		action, relates       string
		types, scopes, xaddrs []string
		text                  strings.Builder
		collecting            string // element whose text is being read
		inMatch               int    // depth of the open ProbeMatch, 0 if none
		sawMatch              bool
		tokens                int
	)
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		if tokens++; tokens > wsdMaxTokens {
			return nil, errors.New("too many XML tokens")
		}
		switch t := tok.(type) {
		case xml.StartElement:
			path = append(path, t.Name.Local)
			if len(path) > wsdMaxDepth {
				return nil, errors.New("XML nested too deeply")
			}
			text.Reset()
			collecting = ""
			switch name := t.Name.Local; {
			case name == "ProbeMatch":
				inMatch = len(path)
				sawMatch = true
			case (name == "Action" || name == "RelatesTo") && inMatch == 0:
				collecting = name
			case (name == "Types" || name == "Scopes" || name == "XAddrs") && inMatch > 0:
				collecting = name
			}
		case xml.CharData:
			if collecting != "" && text.Len() <= wsdMaxDatagram {
				text.Write(t)
			}
		case xml.EndElement:
			if collecting == t.Name.Local {
				switch collecting {
				case "Action":
					action = strings.TrimSpace(text.String())
				case "RelatesTo":
					relates = strings.TrimSpace(text.String())
				case "Types":
					types = append(types, strings.Fields(text.String())...)
				case "Scopes":
					scopes = append(scopes, strings.Fields(text.String())...)
				case "XAddrs":
					xaddrs = append(xaddrs, strings.Fields(text.String())...)
				}
			}
			collecting = ""
			text.Reset()
			if inMatch > 0 && len(path) == inMatch {
				inMatch = 0
			}
			if len(path) > 0 {
				path = path[:len(path)-1]
			}
		}
	}
	if !sawMatch || !strings.HasSuffix(action, wsdMatchesTail) {
		return nil, errWSD
	}
	if relatesTo != "" && relates != relatesTo {
		return nil, errors.New("reply to a different probe")
	}
	info := &WSDInfo{Types: cleanTypes(types), Scopes: cleanScopes(scopes), XAddrs: cleanXAddrs(xaddrs)}
	info.deriveONVIF()
	if len(info.Types) == 0 && len(info.Scopes) == 0 && len(info.XAddrs) == 0 {
		return nil, errWSD
	}
	return info, nil
}

// cleanTypes keeps xs:QName-shaped words (letters, digits and . _ - :) only.
func cleanTypes(in []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, s := range in {
		if len(out) >= wsdMaxTypes {
			break
		}
		if len(s) == 0 || len(s) > wsdTypeLen || seen[s] || !qnameChars(s) {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

func qnameChars(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		ok := c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '.' || c == '_' || c == '-' || c == ':'
		if !ok {
			return false
		}
	}
	return true
}

// cleanScopes keeps printable-ASCII URIs, which is what scopes are.
func cleanScopes(in []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, s := range in {
		if len(out) >= wsdMaxScopes {
			break
		}
		if !asciiToken(s, wsdScopeLen) || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

// cleanXAddrs keeps http and https URLs with a host, as text. They are never
// followed.
func cleanXAddrs(in []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, s := range in {
		if len(out) >= wsdMaxXAddrs {
			break
		}
		if !asciiToken(s, wsdXAddrLen) || seen[s] {
			continue
		}
		u, err := url.Parse(s)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

const (
	onvifNamePrefix     = "onvif://www.onvif.org/name/"
	onvifHardwarePrefix = "onvif://www.onvif.org/hardware/"
)

func (w *WSDInfo) deriveONVIF() {
	for _, s := range w.Scopes {
		if rest, ok := strings.CutPrefix(s, onvifNamePrefix); ok && w.Name == "" {
			w.Name = scopeText(rest)
		}
		if rest, ok := strings.CutPrefix(s, onvifHardwarePrefix); ok && w.Hardware == "" {
			w.Hardware = scopeText(rest)
		}
	}
}

func scopeText(s string) string {
	if dec, err := url.PathUnescape(s); err == nil {
		s = dec
	}
	return CleanText(s, wsdNameLen)
}

func (w *WSDInfo) merge(o *WSDInfo) {
	w.Types = cleanTypes(append(w.Types, o.Types...))
	w.Scopes = cleanScopes(append(w.Scopes, o.Scopes...))
	w.XAddrs = cleanXAddrs(append(w.XAddrs, o.XAddrs...))
	w.deriveONVIF()
}

// Sanitize re-applies every bound and character check, for a value read back
// from a saved file, which is as untrusted as a reply.
func (w *WSDInfo) Sanitize() {
	w.Types = cleanTypes(w.Types)
	w.Scopes = cleanScopes(w.Scopes)
	w.XAddrs = cleanXAddrs(w.XAddrs)
	w.Name = CleanText(w.Name, wsdNameLen)
	w.Hardware = CleanText(w.Hardware, wsdNameLen)
}

// TypeNames returns the local part of each advertised type, lower-cased and
// sorted ("dn:NetworkVideoTransmitter" becomes "networkvideotransmitter"). The
// prefix is whatever the device chose, so only the local part is meaningful.
func (w *WSDInfo) TypeNames() []string {
	if w == nil {
		return nil
	}
	out := make([]string, 0, len(w.Types))
	for _, t := range w.Types {
		if i := strings.LastIndexByte(t, ':'); i >= 0 {
			t = t[i+1:]
		}
		out = append(out, strings.ToLower(t))
	}
	sort.Strings(out)
	return out
}

// HasONVIFScope reports whether the device announced any ONVIF scope.
func (w *WSDInfo) HasONVIFScope() bool {
	if w == nil {
		return false
	}
	for _, s := range w.Scopes {
		if strings.HasPrefix(s, "onvif://www.onvif.org/") {
			return true
		}
	}
	return false
}
