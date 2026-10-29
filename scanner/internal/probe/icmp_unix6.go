//go:build !windows

package probe

import (
	"context"
	"net"
	"net/netip"
	"time"

	"golang.org/x/net/icmp"
	"golang.org/x/net/ipv6"
)

// IPv6 echo shares the IPv4 pinger's reply table and echo identifier. Its
// socket is opened on a best-effort basis: a host that allows IPv4 pings
// but has no IPv6 stack just gets ErrUnavailable for IPv6 addresses.
func (p *unixPinger) start6() {
	conn, err := icmp.ListenPacket("udp6", "::")
	raw := false
	if err != nil {
		var rawErr error
		if conn, rawErr = icmp.ListenPacket("ip6:ipv6-icmp", "::"); rawErr != nil {
			return
		}
		raw = true
	}
	p.conn6, p.raw6 = conn, raw
	if pc := conn.IPv6PacketConn(); pc != nil {
		_ = pc.SetControlMessage(ipv6.FlagHopLimit, true) // best effort
		p.pc6 = pc
	}
	go p.readLoop6()
}

func (p *unixPinger) readLoop6() {
	buf := make([]byte, 1500)
	for {
		var (
			n   int
			hop int
			src net.Addr
			err error
		)
		if p.pc6 != nil {
			var cm *ipv6.ControlMessage
			n, cm, src, err = p.pc6.ReadFrom(buf)
			if cm != nil {
				hop = cm.HopLimit
			}
		} else {
			n, src, err = p.conn6.ReadFrom(buf)
		}
		if err != nil {
			select {
			case <-p.closed:
				return
			case <-time.After(10 * time.Millisecond):
				continue
			}
		}
		at := time.Now()
		msg, err := icmp.ParseMessage(58, buf[:n]) // 58 is IPv6-ICMP
		if err != nil || msg.Type != ipv6.ICMPTypeEchoReply {
			continue
		}
		echo, ok := msg.Body.(*icmp.Echo)
		if !ok {
			continue
		}
		if p.raw6 && echo.ID != p.id {
			continue
		}
		addr, ok := addrOf(src)
		if !ok {
			continue
		}
		p.mu.Lock()
		ch := p.wait[replyKey{addr.WithZone(""), echo.Seq}]
		p.mu.Unlock()
		if ch != nil {
			select {
			case ch <- replyMsg{at: at, ttl: hop}:
			default:
			}
		}
	}
}

func (p *unixPinger) ping6(ctx context.Context, addr netip.Addr, timeout time.Duration) (EchoReply, error) {
	if p.conn6 == nil {
		return EchoReply{}, ErrUnavailable
	}
	p.mu.Lock()
	p.seq = (p.seq + 1) & 0xffff
	key := replyKey{addr.WithZone(""), p.seq}
	ch := make(chan replyMsg, 1)
	p.wait[key] = ch
	p.mu.Unlock()
	defer func() {
		p.mu.Lock()
		delete(p.wait, key)
		p.mu.Unlock()
	}()

	msg := icmp.Message{
		Type: ipv6.ICMPTypeEchoRequest,
		Body: &icmp.Echo{ID: p.id, Seq: key.seq, Data: []byte("ztg-scanner")},
	}
	b, err := msg.Marshal(nil) // the kernel computes the ICMPv6 checksum
	if err != nil {
		return EchoReply{}, err
	}
	var dst net.Addr = &net.IPAddr{IP: addr.AsSlice(), Zone: addr.Zone()}
	if !p.raw6 {
		dst = &net.UDPAddr{IP: addr.AsSlice(), Zone: addr.Zone()}
	}
	sent := time.Now()
	if _, err := p.conn6.WriteTo(b, dst); err != nil {
		return EchoReply{}, err
	}
	t := time.NewTimer(timeout)
	defer t.Stop()
	select {
	case r := <-ch:
		return EchoReply{RTT: r.at.Sub(sent), TTL: r.ttl}, nil
	case <-t.C:
		return EchoReply{}, ErrNoReply
	case <-ctx.Done():
		return EchoReply{}, ctx.Err()
	}
}
