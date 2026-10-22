package probe

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"sort"
	"strings"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

// MDNSInfo is what a host's multicast DNS responder says about itself.
type MDNSInfo struct {
	Hostname string `json:"hostname,omitempty"`
	// Services are the DNS-SD service types it advertises, for example
	// "_ipp._tcp" (a printer) or "_googlecast._tcp" (a Chromecast).
	Services []string `json:"services,omitempty"`
}

// MDNSQuery sends legacy unicast queries (RFC 6762 section 6.7) straight
// to addr:5353 from an ordinary port: its reverse name and its DNS-SD
// service list. Apple devices, Linux with Avahi, printers and many IoT
// devices answer.
func MDNSQuery(ctx context.Context, addr netip.Addr, timeout time.Duration) (*MDNSInfo, error) {
	c, err := net.DialUDP("udp4", nil, &net.UDPAddr{IP: addr.AsSlice(), Port: 5353})
	if err != nil {
		return nil, err
	}
	defer c.Close()
	deadline := time.Now().Add(timeout)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	_ = c.SetDeadline(deadline)

	reverse, err := reverseName(addr)
	if err != nil {
		return nil, err
	}
	for i, q := range []string{reverse, "_services._dns-sd._udp.local."} {
		msg, err := mdnsQuestion(uint16(0x5a00+i), q)
		if err != nil {
			return nil, err
		}
		if _, err := c.Write(msg); err != nil {
			return nil, err
		}
	}

	info := &MDNSInfo{}
	seen := map[string]bool{}
	buf := make([]byte, 9000)
	for {
		n, err := c.Read(buf)
		if err != nil {
			break // deadline: done collecting
		}
		mergeMDNS(buf[:n], reverse, info, seen)
		if info.Hostname != "" && len(info.Services) > 0 {
			break
		}
	}
	sort.Strings(info.Services)
	if info.Hostname == "" && len(info.Services) == 0 {
		return nil, fmt.Errorf("no mDNS answer")
	}
	return info, nil
}

func reverseName(a netip.Addr) (string, error) {
	if !a.Is4() {
		return "", fmt.Errorf("IPv4 only")
	}
	b := a.As4()
	return fmt.Sprintf("%d.%d.%d.%d.in-addr.arpa.", b[3], b[2], b[1], b[0]), nil
}

func mdnsQuestion(id uint16, name string) ([]byte, error) {
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

func mergeMDNS(b []byte, reverse string, info *MDNSInfo, seen map[string]bool) {
	var m dnsmessage.Message
	if err := m.Unpack(b); err != nil {
		return
	}
	for _, rrs := range [][]dnsmessage.Resource{m.Answers, m.Additionals} {
		for _, rr := range rrs {
			ptr, ok := rr.Body.(*dnsmessage.PTRResource)
			if !ok {
				continue
			}
			owner := strings.ToLower(rr.Header.Name.String())
			target := ptr.PTR.String()
			switch {
			case owner == strings.ToLower(reverse) && info.Hostname == "":
				info.Hostname = strings.TrimSuffix(target, ".")
			case owner == "_services._dns-sd._udp.local.":
				svc := strings.TrimSuffix(strings.TrimSuffix(target, "."), ".local")
				if !seen[svc] {
					seen[svc] = true
					info.Services = append(info.Services, svc)
				}
			}
		}
	}
}
