package probe

import (
	"context"
	"encoding/binary"
	"errors"
	"net"
	"net/netip"
	"strings"
	"time"
)

// NetBIOSInfo is a node status (NBSTAT) answer: the names a Windows or
// Samba host has registered, as RFC 1002 section 4.2.18 defines them.
type NetBIOSInfo struct {
	Computer  string        `json:"computer,omitempty"`
	Workgroup string        `json:"workgroup,omitempty"`
	MAC       string        `json:"mac,omitempty"`
	Names     []NetBIOSName `json:"names,omitempty"`
}

// NetBIOSName is one registered name.
type NetBIOSName struct {
	Name   string `json:"name"`
	Suffix byte   `json:"suffix"`
	Group  bool   `json:"group"`
}

// nbstatQuery is a node status request for the wildcard name "*".
func nbstatQuery(txid uint16) []byte {
	b := make([]byte, 0, 50)
	b = binary.BigEndian.AppendUint16(b, txid)
	b = append(b, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00) // flags, QD=1, AN=NS=AR=0
	b = append(b, 0x20)
	name := [16]byte{'*'}
	for _, c := range name {
		b = append(b, 'A'+(c>>4), 'A'+(c&0x0f))
	}
	b = append(b, 0x00, 0x00, 0x21, 0x00, 0x01) // end of name, QTYPE NBSTAT, QCLASS IN
	return b
}

// NetBIOSStatus asks addr for its NetBIOS name table on UDP 137.
func NetBIOSStatus(ctx context.Context, addr netip.Addr, timeout time.Duration) (*NetBIOSInfo, error) {
	if !addr.Is4() {
		return nil, errors.New("NetBIOS name service is IPv4 only")
	}
	c, err := net.DialUDP("udp4", nil, &net.UDPAddr{IP: addr.AsSlice(), Port: 137})
	if err != nil {
		return nil, err
	}
	defer c.Close()
	deadline := time.Now().Add(timeout)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	_ = c.SetDeadline(deadline)
	txid := uint16(time.Now().UnixNano())
	if _, err := c.Write(nbstatQuery(txid)); err != nil {
		return nil, err
	}
	buf := make([]byte, 2048)
	for {
		n, err := c.Read(buf)
		if err != nil {
			return nil, err
		}
		if n >= 2 && binary.BigEndian.Uint16(buf) == txid {
			return parseNBSTAT(buf[:n])
		}
	}
}

var errShort = errors.New("truncated response")

func parseNBSTAT(b []byte) (*NetBIOSInfo, error) {
	if len(b) < 12 {
		return nil, errShort
	}
	if binary.BigEndian.Uint16(b[6:8]) == 0 {
		return nil, errors.New("no answer records")
	}
	off, err := skipDNSName(b, 12)
	if err != nil {
		return nil, err
	}
	if off+10 > len(b) {
		return nil, errShort
	}
	if binary.BigEndian.Uint16(b[off:]) != 0x21 {
		return nil, errors.New("not a node status answer")
	}
	off += 10 // type, class, ttl, rdlength
	if off >= len(b) {
		return nil, errShort
	}
	count := int(b[off])
	off++
	info := &NetBIOSInfo{}
	for i := 0; i < count; i++ {
		if off+18 > len(b) {
			return nil, errShort
		}
		name := strings.TrimRight(string(b[off:off+15]), " \x00")
		suffix := b[off+15]
		flags := binary.BigEndian.Uint16(b[off+16:])
		group := flags&0x8000 != 0
		off += 18
		info.Names = append(info.Names, NetBIOSName{Name: name, Suffix: suffix, Group: group})
		switch {
		case !group && (suffix == 0x00 || suffix == 0x20) && info.Computer == "":
			info.Computer = name
		case group && suffix == 0x00 && info.Workgroup == "":
			info.Workgroup = name
		}
	}
	if off+6 <= len(b) {
		mac := net.HardwareAddr(b[off : off+6])
		if !isZeroMAC(mac) {
			info.MAC = mac.String()
		}
	}
	return info, nil
}

// skipDNSName returns the offset just past the (possibly compressed) name at off.
func skipDNSName(b []byte, off int) (int, error) {
	for {
		if off >= len(b) {
			return 0, errShort
		}
		l := int(b[off])
		switch {
		case l == 0:
			return off + 1, nil
		case l&0xc0 == 0xc0:
			return off + 2, nil
		default:
			off += 1 + l
		}
	}
}
