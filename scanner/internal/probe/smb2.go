package probe

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"time"
)

// SMBInfo is what a server says in its SMB2 NEGOTIATE response, the first
// message any SMB client sends and the only one Scanner sends. It never goes
// further: no SESSION_SETUP, no NTLM, no tree connect, nothing authenticated.
//
// The operating system build and version are not here on purpose. SMB carries
// them only inside the NTLMSSP challenge of a session setup, and Scanner does
// not start one. What the negotiate does show is the highest dialect the
// server supports, which dates the operating system roughly (see EraHint), the
// signing policy, capabilities, the server's GUID and its clock.
type SMBInfo struct {
	Dialect         string   `json:"dialect"`                // for example "3.1.1"
	SigningEnabled  bool     `json:"signing_enabled"`        // server will sign if asked
	SigningRequired bool     `json:"signing_required"`       // server refuses unsigned sessions
	Capabilities    []string `json:"capabilities,omitempty"` // SMB2 global capability names
	ServerGUID      string   `json:"server_guid,omitempty"`
	SystemTime      string   `json:"system_time,omitempty"` // server clock, RFC 3339 UTC
	StartTime       string   `json:"start_time,omitempty"`  // only when the server reports one
	// EraHint is an inference from the dialect, never a measurement.
	EraHint string `json:"era_hint,omitempty"`
}

// The dialects Scanner offers. The server picks the highest one it supports, so
// the answer is the server's own maximum up to 3.1.1.
const (
	smb2Dialect202 = 0x0202
	smb2Dialect210 = 0x0210
	smb2Dialect300 = 0x0300
	smb2Dialect302 = 0x0302
	smb2Dialect311 = 0x0311

	smbPort        = 445
	smbMaxResponse = 16 * 1024
	smbHeaderLen   = 64
	smbBodyFixed   = 64 // fixed part of a NEGOTIATE response body, StructureSize 65 counting one buffer byte
)

var smbDialectName = map[uint16]string{
	smb2Dialect202: "2.0.2", smb2Dialect210: "2.1", smb2Dialect300: "3.0", smb2Dialect302: "3.0.2", smb2Dialect311: "3.1.1",
}

// smbEra maps the highest negotiated dialect to the operating system generation
// that introduced it, from Microsoft's public documentation. Samba, NAS and
// other implementations can report any dialect, so these are hints.
var smbEra = map[uint16]string{
	smb2Dialect202: "Highest dialect SMB 2.0.2: Windows Vista or Server 2008 era, or an older appliance",
	smb2Dialect210: "Highest dialect SMB 2.1: Windows 7 or Server 2008 R2 era, or an older appliance",
	smb2Dialect300: "Highest dialect SMB 3.0: Windows 8 or Server 2012 era, or a Samba or NAS release of that age",
	smb2Dialect302: "Highest dialect SMB 3.0.2: Windows 8.1 or Server 2012 R2 era, or a Samba or NAS release of that age",
	smb2Dialect311: "Highest dialect SMB 3.1.1: Windows 10 or Server 2016 or newer, or a current Samba or NAS release",
}

var smbCapabilities = []struct {
	bit  uint32
	name string
}{
	{0x01, "dfs"}, {0x02, "leasing"}, {0x04, "large_mtu"}, {0x08, "multi_channel"},
	{0x10, "persistent_handles"}, {0x20, "directory_leasing"}, {0x40, "encryption"},
}

// smb2NegotiateRequest builds the NEGOTIATE request: a 64-byte SMB2 header
// (command 0, message ID 0, no session or tree) and the NEGOTIATE body offering
// 2.0.2 through 3.1.1, signing enabled and not required. 3.1.1 requires the
// pre-authentication integrity context, so one is included (SHA-512 and a
// random salt); it is part of the negotiation, not of any session.
// The request is framed for SMB over TCP: a zero byte and a 24-bit length.
func smb2NegotiateRequest(clientGUID [16]byte, salt [32]byte) []byte {
	dialects := []uint16{smb2Dialect202, smb2Dialect210, smb2Dialect300, smb2Dialect302, smb2Dialect311}

	h := make([]byte, smbHeaderLen)
	copy(h, []byte{0xfe, 'S', 'M', 'B'})
	binary.LittleEndian.PutUint16(h[4:], smbHeaderLen) // StructureSize
	// CreditCharge 0, Status 0, Command 0 (NEGOTIATE)
	binary.LittleEndian.PutUint16(h[14:], 1) // CreditRequest
	// Flags, NextCommand, MessageId, ProcessId, TreeId, SessionId, Signature: zero

	const fixed = 36
	dialectsEnd := smbHeaderLen + fixed + 2*len(dialects)
	ctxOffset := (dialectsEnd + 7) &^ 7 // contexts start 8-byte aligned, from the header start
	body := make([]byte, fixed)
	binary.LittleEndian.PutUint16(body[0:], fixed)
	binary.LittleEndian.PutUint16(body[2:], uint16(len(dialects)))
	binary.LittleEndian.PutUint16(body[4:], 0x0001) // SMB2_NEGOTIATE_SIGNING_ENABLED
	// Reserved and Capabilities zero
	copy(body[12:28], clientGUID[:])
	binary.LittleEndian.PutUint32(body[28:], uint32(ctxOffset)) // NegotiateContextOffset
	binary.LittleEndian.PutUint16(body[32:], 1)                 // NegotiateContextCount
	for _, d := range dialects {
		body = binary.LittleEndian.AppendUint16(body, d)
	}
	for len(h)+len(body) < ctxOffset {
		body = append(body, 0)
	}
	// SMB2_PREAUTH_INTEGRITY_CAPABILITIES: one hash (SHA-512), a 32-byte salt.
	data := binary.LittleEndian.AppendUint16(nil, 1)
	data = binary.LittleEndian.AppendUint16(data, uint16(len(salt)))
	data = binary.LittleEndian.AppendUint16(data, 0x0001)
	data = append(data, salt[:]...)
	body = binary.LittleEndian.AppendUint16(body, 0x0001) // ContextType
	body = binary.LittleEndian.AppendUint16(body, uint16(len(data)))
	body = binary.LittleEndian.AppendUint32(body, 0) // Reserved
	body = append(body, data...)

	msg := append(h, body...)
	out := []byte{0, byte(len(msg) >> 16), byte(len(msg) >> 8), byte(len(msg))}
	return append(out, msg...)
}

// SMBNegotiate connects to addr:445, sends one SMB2 NEGOTIATE and parses the
// answer. It must only be called for a host where the scan already found the
// port open.
func SMBNegotiate(ctx context.Context, addr netip.Addr, timeout time.Duration) (*SMBInfo, error) {
	return smbNegotiate(ctx, addr, smbPort, timeout)
}

func smbNegotiate(ctx context.Context, addr netip.Addr, port int, timeout time.Duration) (*SMBInfo, error) {
	var guid [16]byte
	var salt [32]byte
	if _, err := rand.Read(guid[:]); err != nil {
		return nil, err
	}
	if _, err := rand.Read(salt[:]); err != nil {
		return nil, err
	}
	c, err := DialTCP(ctx, addr, port, timeout)
	if err != nil {
		return nil, err
	}
	defer c.Close()
	deadline := time.Now().Add(timeout)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	_ = c.SetDeadline(deadline)
	if _, err := c.Write(smb2NegotiateRequest(guid, salt)); err != nil {
		return nil, err
	}
	var th [4]byte
	if _, err := io.ReadFull(c, th[:]); err != nil {
		return nil, err
	}
	n := int(th[1])<<16 | int(th[2])<<8 | int(th[3])
	if th[0] != 0 || n < smbHeaderLen+smbBodyFixed || n > smbMaxResponse {
		return nil, errors.New("not an SMB2 response")
	}
	resp := make([]byte, n)
	if _, err := io.ReadFull(c, resp); err != nil {
		return nil, err
	}
	return parseSMB2Negotiate(resp)
}

var errSMB = errors.New("not an SMB2 NEGOTIATE response")

// parseSMB2Negotiate reads a NEGOTIATE response, without the 4-byte transport
// header. It reads only the fixed fields. The security buffer (the SPNEGO token
// that follows) and the negotiate contexts are never parsed or kept.
func parseSMB2Negotiate(b []byte) (*SMBInfo, error) {
	if len(b) < smbHeaderLen+smbBodyFixed {
		return nil, errSMB
	}
	if b[0] != 0xfe || b[1] != 'S' || b[2] != 'M' || b[3] != 'B' || binary.LittleEndian.Uint16(b[4:]) != smbHeaderLen {
		return nil, errSMB
	}
	if binary.LittleEndian.Uint32(b[8:]) != 0 { // Status must be STATUS_SUCCESS
		return nil, errSMB
	}
	if binary.LittleEndian.Uint16(b[12:]) != 0 { // Command must be NEGOTIATE
		return nil, errSMB
	}
	if binary.LittleEndian.Uint32(b[16:])&1 == 0 { // SMB2_FLAGS_SERVER_TO_REDIR
		return nil, errSMB
	}
	body := b[smbHeaderLen:]
	if binary.LittleEndian.Uint16(body[0:]) != 65 {
		return nil, errSMB
	}
	mode := binary.LittleEndian.Uint16(body[2:])
	dialect := binary.LittleEndian.Uint16(body[4:])
	name, ok := smbDialectName[dialect]
	if !ok {
		return nil, errSMB // a dialect Scanner did not offer, or the 0x02FF wildcard
	}
	info := &SMBInfo{
		Dialect:         name,
		SigningEnabled:  mode&0x0001 != 0,
		SigningRequired: mode&0x0002 != 0,
		ServerGUID:      guidText(body[8:24]),
		EraHint:         smbEra[dialect],
	}
	caps := binary.LittleEndian.Uint32(body[24:])
	for _, c := range smbCapabilities {
		if caps&c.bit != 0 {
			info.Capabilities = append(info.Capabilities, c.name)
		}
	}
	info.SystemTime = filetimeText(binary.LittleEndian.Uint64(body[40:]))
	info.StartTime = filetimeText(binary.LittleEndian.Uint64(body[48:]))
	return info, nil
}

// guidText formats 16 bytes as a Windows GUID: the first three fields are
// little-endian.
func guidText(g []byte) string {
	return fmt.Sprintf("%08x-%04x-%04x-%x-%x",
		binary.LittleEndian.Uint32(g[0:]), binary.LittleEndian.Uint16(g[4:]), binary.LittleEndian.Uint16(g[6:]), g[8:10], g[10:16])
}

// filetimeText converts a Windows FILETIME (100 ns ticks since 1601) to RFC
// 3339 UTC. Zero, and anything before 1970 or after 2200, gives "".
func filetimeText(ft uint64) string {
	const epochDiff = 11644473600 // seconds from 1601 to 1970
	secs := ft / 10_000_000
	if secs <= epochDiff || secs-epochDiff > 7_258_118_400 { // 2200-01-01
		return ""
	}
	return time.Unix(int64(secs-epochDiff), int64(ft%10_000_000)*100).UTC().Format(time.RFC3339)
}

// Sanitize bounds a value read from a saved file: it is as untrusted as a reply.
func (s *SMBInfo) Sanitize() {
	if _, ok := dialectByName(s.Dialect); !ok {
		s.Dialect = ""
	}
	s.EraHint = CleanText(s.EraHint, 200)
	s.ServerGUID = CleanText(s.ServerGUID, 36)
	s.SystemTime = CleanText(s.SystemTime, 40)
	s.StartTime = CleanText(s.StartTime, 40)
	var caps []string
	for _, c := range s.Capabilities {
		if len(caps) < len(smbCapabilities) && qnameChars(c) && len(c) <= 24 {
			caps = append(caps, c)
		}
	}
	s.Capabilities = caps
}

func dialectByName(n string) (uint16, bool) {
	for d, name := range smbDialectName {
		if name == n {
			return d, true
		}
	}
	return 0, false
}

// LegacyWindowsDialect reports whether the negotiated dialect is one only
// Windows of the 2008 to 2012 R2 generations (and old appliances) stop at.
func (s *SMBInfo) LegacyWindowsDialect() bool {
	switch s.Dialect {
	case "2.0.2", "2.1", "3.0", "3.0.2":
		return true
	}
	return false
}
