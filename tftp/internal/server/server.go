// Package server owns listeners, session lifetime and policy gates.
package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"net"
	"net/netip"
	"os"
	"sync"
	"time"

	"github.com/zerotrustgarage/tools/tftp/internal/audit"
	"github.com/zerotrustgarage/tools/tftp/internal/policy"
	"github.com/zerotrustgarage/tools/tftp/internal/store"
	"github.com/zerotrustgarage/tools/tftp/internal/wire"
	"github.com/zerotrustgarage/tools/tftp/internal/xfer"
)

type Config struct {
	Root, Bind                        string
	Upload                            string
	UploadMax                         int64
	FreeFloor                         uint64
	Port                              int
	AllowPublic, SinglePort, FoldCase bool
	Allow                             []netip.Prefix
	Duration                          time.Duration
	MaxBlock, MaxWindow               int
	Audit                             io.Writer
}
type Server struct {
	config    Config
	store     *store.Store
	drop      *store.Drop
	log       *audit.Log
	guard     *policy.Guard
	id        string
	listeners []*listener
	cancel    context.CancelFunc
	done      chan struct{}
	wg        sync.WaitGroup
	mu        sync.Mutex
	err       error
}
type listener struct {
	conn  *net.UDPConn
	mu    sync.Mutex
	peers map[netip.AddrPort]*transport
}
type transport struct {
	ctx      context.Context
	conn     *net.UDPConn
	listener *net.UDPConn
	peer     netip.AddrPort
	queue    chan []byte
	guard    *policy.Guard
	verified bool
}

func (t *transport) Verify()        { t.verified = true }
func (t *transport) Verified() bool { return t.verified }
func (t *transport) Send(b []byte) error {
	if e := t.ctx.Err(); e != nil {
		return e
	}
	if !t.verified && !t.guard.Budget(len(b), time.Now()) {
		return xfer.ErrBudget
	}
	c := t.conn
	if c == nil {
		c = t.listener
	}
	_, e := c.WriteToUDPAddrPort(b, t.peer)
	return e
}
func (t *transport) Receive(deadline time.Time) ([]byte, error) {
	if e := t.ctx.Err(); e != nil {
		return nil, e
	}
	if t.conn == nil {
		timer := time.NewTimer(time.Until(deadline))
		defer timer.Stop()
		select {
		case <-t.ctx.Done():
			return nil, t.ctx.Err()
		case <-timer.C:
			return nil, xfer.ErrTimeout
		case b := <-t.queue:
			return b, nil
		}
	}
	if e := t.conn.SetReadDeadline(deadline); e != nil {
		return nil, e
	}
	b := make([]byte, wire.MaxBlock+5)
	for {
		n, peer, e := t.conn.ReadFromUDPAddrPort(b)
		if e != nil {
			return nil, e
		}
		if peer != t.peer {
			continue
		}
		return b[:n], nil
	}
}

func ID() string {
	var b [16]byte
	if _, e := rand.Read(b[:]); e != nil {
		panic(e)
	}
	return hex.EncodeToString(b[:])
}

func Start(parent context.Context, c Config) (*Server, error) {
	if c.Duration <= 0 {
		return nil, errors.New("session duration must be positive")
	}
	if c.Port < 0 || c.Port > 65535 {
		return nil, errors.New("invalid port")
	}
	if c.Bind == "" {
		c.Bind = "0.0.0.0"
	}
	if c.MaxBlock == 0 {
		c.MaxBlock = 1468
	}
	if c.MaxWindow == 0 {
		c.MaxWindow = 16
	}
	if c.MaxBlock < 8 || c.MaxBlock > wire.MaxBlock || c.MaxWindow < 1 || c.MaxWindow > 64 {
		return nil, errors.New("invalid block or window cap")
	}
	if c.Audit == nil {
		return nil, errors.New("audit output is required")
	}
	addrs, subnets, e := policy.Bindings(c.Bind, c.AllowPublic)
	if e != nil {
		return nil, e
	}
	if len(c.Allow) == 0 {
		c.Allow = subnets
	}
	for _, p := range c.Allow {
		if !p.IsValid() {
			return nil, errors.New("invalid allowlist prefix")
		}
	}
	root, e := store.Open(c.Root, c.FoldCase)
	if e != nil {
		return nil, e
	}
	var drop *store.Drop
	if c.Upload != "" {
		if c.UploadMax == 0 {
			c.UploadMax = store.DefaultUploadMax
		}
		if c.FreeFloor == 0 {
			c.FreeFloor = store.DefaultFreeFloor
		}
		drop, e = store.OpenDrop(c.Upload, c.Root, c.UploadMax, c.FreeFloor)
		if e != nil {
			root.Close()
			return nil, e
		}
	}
	ctx, cancel := context.WithTimeout(parent, c.Duration)
	s := &Server{config: c, store: root, drop: drop, guard: policy.New(), log: audit.New(c.Audit), id: ID(), cancel: cancel, done: make(chan struct{})}
	for _, a := range addrs {
		conn, e := net.ListenUDP("udp", net.UDPAddrFromAddrPort(netip.AddrPortFrom(a, uint16(c.Port))))
		if e != nil {
			for _, l := range s.listeners {
				l.conn.Close()
			}
			root.Close()
			if drop != nil {
				drop.Close()
			}
			cancel()
			return nil, e
		}
		s.listeners = append(s.listeners, &listener{conn: conn, peers: map[netip.AddrPort]*transport{}})
	}
	for _, l := range s.listeners {
		s.wg.Add(1)
		go s.listen(ctx, l)
	}
	go func() {
		<-ctx.Done()
		for _, l := range s.listeners {
			l.conn.Close()
			l.mu.Lock()
			for _, t := range l.peers {
				if t.conn != nil {
					t.conn.Close()
				}
			}
			l.mu.Unlock()
		}
		s.wg.Wait()
		for a, n := range s.guard.Suppressed() {
			source := a.String()
			if !a.IsValid() {
				source = "untracked-sources"
			}
			s.record(audit.Record{Session: s.id, Op: "summary", Source: source, Result: "refused", Reason: "refusal-summary", Suppressed: n})
		}
		s.store.Close()
		if s.drop != nil {
			s.drop.Close()
		}
		close(s.done)
	}()
	return s, nil
}
func (s *Server) Addresses() []string {
	out := []string{}
	for _, l := range s.listeners {
		out = append(out, l.conn.LocalAddr().String())
	}
	return out
}
func (s *Server) Stop()       { s.cancel() }
func (s *Server) Wait() error { <-s.done; s.mu.Lock(); defer s.mu.Unlock(); return s.err }
func (s *Server) record(r audit.Record) {
	if e := s.log.Write(r); e != nil {
		s.mu.Lock()
		if s.err == nil {
			s.err = e
		}
		s.mu.Unlock()
		s.cancel()
	}
}

func (s *Server) refuse(l *listener, peer netip.AddrPort, p wire.Packet, reason string, code uint16, reply bool) {
	allowed, n := s.guard.Audit(peer.Addr().Unmap(), time.Now())
	if allowed {
		op := "read"
		if p.Op == wire.WRQ {
			op = "write"
		}
		s.record(audit.Record{Session: s.id, Transfer: ID(), Op: op, Source: peer.String(), Local: l.conn.LocalAddr().String(), Name: p.Name, Result: "refused", Reason: reason, Suppressed: n})
	}
	// Refusals share the global unverified-peer budget and are rate limited.
	if reply && allowed {
		b := wire.Encode(wire.Packet{Op: wire.ERROR, Code: code, Message: reason})
		if s.guard.Budget(len(b), time.Now()) {
			l.conn.WriteToUDPAddrPort(b, peer)
		}
	}
}

func (s *Server) listen(ctx context.Context, l *listener) {
	defer s.wg.Done()
	b := make([]byte, wire.MaxBlock+5)
	for {
		n, peer, e := l.conn.ReadFromUDPAddrPort(b)
		if e != nil {
			if ctx.Err() == nil {
				s.mu.Lock()
				s.err = e
				s.mu.Unlock()
				s.cancel()
			}
			return
		}
		if ctx.Err() != nil {
			return
		}
		if !policy.Allowed(peer.Addr(), s.config.Allow) {
			s.refuse(l, peer, wire.Packet{}, "outside-allowlist", 2, false)
			continue
		}
		p, e := wire.Decode(b[:n])
		l.mu.Lock()
		active := l.peers[peer]
		l.mu.Unlock()
		if s.config.SinglePort && active != nil && (e != nil || (p.Op != wire.RRQ && p.Op != wire.WRQ)) {
			select {
			case active.queue <- append([]byte(nil), b[:n]...):
			default:
			}
			continue
		}
		if e != nil || p.Op != wire.RRQ && p.Op != wire.WRQ {
			s.refuse(l, peer, wire.Packet{}, "malformed-request", 4, true)
			continue
		}
		if active != nil { // The transfer timer handles retransmitted requests.
			s.refuse(l, peer, p, "transfer-already-active", 0, false)
			continue
		}
		if reason := s.guard.Acquire(peer.Addr().Unmap(), time.Now()); reason != "" {
			s.refuse(l, peer, p, reason, 0, true)
			continue
		}
		if p.Op == wire.WRQ && s.drop == nil {
			s.guard.Release(peer.Addr().Unmap())
			s.refuse(l, peer, p, "write-disabled", 2, true)
			continue
		}
		t := &transport{ctx: ctx, listener: l.conn, peer: peer, guard: s.guard, queue: make(chan []byte, s.config.MaxWindow+2)}
		stopClose := func() bool { return false }
		if !s.config.SinglePort {
			a := l.conn.LocalAddr().(*net.UDPAddr).AddrPort()
			t.conn, e = net.ListenUDP("udp", net.UDPAddrFromAddrPort(netip.AddrPortFrom(a.Addr(), 0)))
			if e != nil {
				s.guard.Release(peer.Addr().Unmap())
				s.refuse(l, peer, p, "socket-unavailable", 0, true)
				continue
			}
		}
		if t.conn != nil {
			stopClose = context.AfterFunc(ctx, func() { t.conn.Close() })
		}
		l.mu.Lock()
		l.peers[peer] = t
		l.mu.Unlock()
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			defer stopClose()
			defer s.guard.Release(peer.Addr().Unmap())
			defer func() {
				l.mu.Lock()
				delete(l.peers, peer)
				l.mu.Unlock()
				if t.conn != nil {
					t.conn.Close()
				}
			}()
			if p.Op == wire.WRQ {
				s.write(ctx, l, t, p)
			} else {
				s.read(ctx, l, t, p)
			}
		}()
	}
}

func (s *Server) read(ctx context.Context, l *listener, t *transport, p wire.Packet) {
	started := time.Now()
	r := audit.Record{Session: s.id, Transfer: ID(), Op: "read", Source: t.peer.String(), Local: l.conn.LocalAddr().String(), Name: p.Name, Result: "refused"}
	defer func() {
		r.DurationMS = time.Since(started).Milliseconds()
		if r.Result == "refused" {
			ok, n := s.guard.Audit(t.peer.Addr().Unmap(), time.Now())
			if !ok {
				return
			}
			r.Suppressed = n
		}
		s.record(r)
	}()
	f, e := s.store.Read(p.Name)
	if e != nil {
		r.Reason = "access-denied"
		code := uint16(2)
		if errors.Is(e, os.ErrNotExist) {
			r.Reason = "not-found"
			code = 1
		}
		if _, err := store.Name(p.Name); err != nil {
			r.Reason = "name-policy"
		}
		t.Send(wire.Encode(wire.Packet{Op: wire.ERROR, Code: code, Message: r.Reason}))
		return
	}
	defer f.Close()
	info, e := f.Stat()
	if e != nil {
		r.Reason = "access-denied"
		return
	}
	// RFC 2349 defines tsize for octet only. Ignore it in netascii mode.
	if p.Mode == "netascii" {
		delete(p.Options, "tsize")
	}
	params, opts, e := wire.Negotiate(p.Options, s.config.MaxBlock, s.config.MaxWindow, info.Size(), true)
	if e != nil {
		r.Reason = "option-negotiation"
		t.Send(wire.Encode(wire.Packet{Op: wire.ERROR, Code: 8, Message: r.Reason}))
		return
	}
	r.Options = opts
	if len(opts) > 0 {
		_, e = xfer.Exchange(t, xfer.RealClock{}, wire.Encode(wire.Packet{Op: wire.OACK, Options: opts}), time.Duration(params.Timeout)*time.Second, 2, func(q wire.Packet) bool { return q.Op == wire.ACK && q.Block == 0 })
		if e == nil {
			t.Verify()
		}
	}
	var stats xfer.Stats
	if e == nil {
		stats, e = xfer.Send(t, xfer.RealClock{}, f, params, p.Mode)
	}
	r.Bytes, r.SHA256 = stats.Bytes, stats.SHA256
	switch {
	case e == nil:
		r.Result = "complete"
	case ctx.Err() != nil:
		r.Result = "aborted"
		r.Reason = "session-ended"
	case xfer.TimedOut(e):
		r.Result = "timeout"
	default:
		var peer *xfer.PeerError
		if errors.As(e, &peer) {
			r.Result = "peer-error"
		} else {
			r.Result = "aborted"
			r.Reason = "transfer-failed"
		}
	}
}
