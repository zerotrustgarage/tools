//go:build !windows

package probe

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"os"
	"sync"
	"time"

	"golang.org/x/net/icmp"
	"golang.org/x/net/ipv4"
)

type replyKey struct {
	addr netip.Addr
	seq  int
}

type replyMsg struct {
	at  time.Time
	ttl int
}

// unixPinger shares one socket across all concurrent pings and routes
// replies to waiters by (source address, sequence number).
type unixPinger struct {
	conn   *icmp.PacketConn
	pc     *ipv4.PacketConn
	raw    bool
	id     int
	mu     sync.Mutex
	seq    int
	wait   map[replyKey]chan replyMsg
	closed chan struct{}
}

func newPinger() (Pinger, error) {
	// Unprivileged ICMP datagram socket (Linux with net.ipv4.ping_group_range,
	// macOS). The kernel owns the echo identifier.
	conn, err := icmp.ListenPacket("udp4", "0.0.0.0")
	raw := false
	if err != nil {
		// Raw socket: needs root or CAP_NET_RAW.
		var rawErr error
		conn, rawErr = icmp.ListenPacket("ip4:icmp", "0.0.0.0")
		if rawErr != nil {
			return nil, fmt.Errorf("%w: datagram socket: %v; raw socket: %v", ErrUnavailable, err, rawErr)
		}
		raw = true
	}
	p := &unixPinger{
		conn:   conn,
		pc:     conn.IPv4PacketConn(),
		raw:    raw,
		id:     os.Getpid() & 0xffff,
		wait:   map[replyKey]chan replyMsg{},
		closed: make(chan struct{}),
	}
	if p.pc != nil {
		_ = p.pc.SetControlMessage(ipv4.FlagTTL, true) // best effort
	}
	go p.readLoop()
	return p, nil
}

func (p *unixPinger) Method() string {
	if p.raw {
		return "icmp-raw"
	}
	return "icmp-dgram"
}

func (p *unixPinger) Close() error {
	select {
	case <-p.closed:
		return nil
	default:
		close(p.closed)
	}
	return p.conn.Close()
}

func (p *unixPinger) readLoop() {
	buf := make([]byte, 1500)
	for {
		var (
			n   int
			ttl int
			src net.Addr
			err error
		)
		if p.pc != nil {
			var cm *ipv4.ControlMessage
			n, cm, src, err = p.pc.ReadFrom(buf)
			if cm != nil {
				ttl = cm.TTL
			}
		} else {
			n, src, err = p.conn.ReadFrom(buf)
		}
		if err != nil {
			select {
			case <-p.closed:
				return
			case <-time.After(10 * time.Millisecond): // do not spin on a persistent error
				continue
			}
		}
		at := time.Now()
		msg, err := icmp.ParseMessage(1, buf[:n])
		if err != nil || msg.Type != ipv4.ICMPTypeEchoReply {
			continue
		}
		echo, ok := msg.Body.(*icmp.Echo)
		if !ok {
			continue
		}
		if p.raw && echo.ID != p.id {
			continue // another process's ping
		}
		addr, ok := addrOf(src)
		if !ok {
			continue
		}
		p.mu.Lock()
		ch := p.wait[replyKey{addr, echo.Seq}]
		p.mu.Unlock()
		if ch != nil {
			select {
			case ch <- replyMsg{at: at, ttl: ttl}:
			default:
			}
		}
	}
}

func addrOf(a net.Addr) (netip.Addr, bool) {
	switch v := a.(type) {
	case *net.UDPAddr:
		ip, ok := netip.AddrFromSlice(v.IP)
		return ip.Unmap(), ok
	case *net.IPAddr:
		ip, ok := netip.AddrFromSlice(v.IP)
		return ip.Unmap(), ok
	}
	return netip.Addr{}, false
}

func (p *unixPinger) Ping(ctx context.Context, addr netip.Addr, timeout time.Duration) (EchoReply, error) {
	p.mu.Lock()
	p.seq = (p.seq + 1) & 0xffff
	key := replyKey{addr, p.seq}
	ch := make(chan replyMsg, 1)
	p.wait[key] = ch
	p.mu.Unlock()
	defer func() {
		p.mu.Lock()
		delete(p.wait, key)
		p.mu.Unlock()
	}()

	msg := icmp.Message{
		Type: ipv4.ICMPTypeEcho,
		Body: &icmp.Echo{ID: p.id, Seq: key.seq, Data: []byte("ztg-scanner")},
	}
	b, err := msg.Marshal(nil)
	if err != nil {
		return EchoReply{}, err
	}
	var dst net.Addr = &net.IPAddr{IP: addr.AsSlice()}
	if !p.raw {
		dst = &net.UDPAddr{IP: addr.AsSlice()}
	}
	sent := time.Now()
	if _, err := p.conn.WriteTo(b, dst); err != nil {
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
