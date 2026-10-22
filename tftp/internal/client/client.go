// Package client provides TFTP get and put without replacing local files.
package client

import (
	"context"
	"errors"
	"io"
	"net"
	"net/netip"
	"strconv"
	"time"

	"github.com/zerotrustgarage/tools/tftp/internal/wire"
	"github.com/zerotrustgarage/tools/tftp/internal/xfer"
)

type Config struct {
	Address, Name, Mode            string
	BlockSize, WindowSize, Timeout int
}

func (c Config) request(op uint16, size int64) (wire.Packet, error) {
	if c.Mode == "" {
		c.Mode = "octet"
	}
	if c.Mode != "octet" && c.Mode != "netascii" {
		return wire.Packet{}, errors.New("mode must be octet or netascii")
	}
	if c.Name == "" || len(c.Name) > 1024 {
		return wire.Packet{}, errors.New("invalid remote name")
	}
	if c.BlockSize == 0 {
		c.BlockSize = 1468
	}
	if c.WindowSize == 0 {
		c.WindowSize = 8
	}
	if c.Timeout == 0 {
		c.Timeout = 3
	}
	if c.BlockSize < 8 || c.BlockSize > wire.MaxBlock || c.WindowSize < 1 || c.WindowSize > 64 || c.Timeout < 1 || c.Timeout > 255 {
		return wire.Packet{}, errors.New("invalid options")
	}
	opts := map[string]string{"blksize": strconv.Itoa(c.BlockSize), "windowsize": strconv.Itoa(c.WindowSize), "timeout": strconv.Itoa(c.Timeout)}
	if c.Mode == "octet" && size >= 0 {
		opts["tsize"] = strconv.FormatInt(size, 10)
	}
	p := wire.Packet{Op: op, Name: c.Name, Mode: c.Mode, Options: opts}
	if _, e := wire.Decode(wire.Encode(p)); e != nil {
		return p, e
	}
	return p, nil
}

type transport struct {
	conn      *net.UDPConn
	host      netip.Addr
	peer      netip.AddrPort
	pinned    bool
	requestOp uint16
}

func (t *transport) Verify()             {}
func (t *transport) Verified() bool      { return true }
func (t *transport) Send(b []byte) error { _, e := t.conn.WriteToUDPAddrPort(b, t.peer); return e }
func (t *transport) Receive(deadline time.Time) ([]byte, error) {
	if e := t.conn.SetReadDeadline(deadline); e != nil {
		return nil, e
	}
	b := make([]byte, wire.MaxBlock+5)
	for {
		n, peer, e := t.conn.ReadFromUDPAddrPort(b)
		if e != nil {
			return nil, e
		}
		if peer.Addr().Unmap() != t.host || t.pinned && peer != t.peer {
			continue
		}
		if !t.pinned {
			p, e := wire.Decode(b[:n])
			if e != nil {
				continue
			}
			valid := p.Op == wire.ERROR || p.Op == wire.OACK
			if t.requestOp == wire.RRQ {
				valid = valid || p.Op == wire.DATA && p.Block == 1
			} else {
				valid = valid || p.Op == wire.ACK && p.Block == 0
			}
			if !valid {
				continue
			}
			t.peer = peer
			t.pinned = true
		}
		return b[:n], nil
	}
}
func connect(ctx context.Context, c Config, op uint16) (*transport, func(), error) {
	a, e := net.ResolveUDPAddr("udp", c.Address)
	if e != nil {
		return nil, nil, e
	}
	ap := a.AddrPort()
	ap = netip.AddrPortFrom(ap.Addr().Unmap(), ap.Port())
	network := "udp4"
	if ap.Addr().Is6() {
		network = "udp6"
	}
	conn, e := net.ListenUDP(network, nil)
	if e != nil {
		return nil, nil, e
	}
	t := &transport{conn: conn, host: ap.Addr().Unmap(), peer: ap, requestOp: op}
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	return t, func() { stop(); conn.Close() }, nil
}

func Get(ctx context.Context, c Config, w io.Writer) (xfer.Stats, error) {
	request, e := c.request(wire.RRQ, 0)
	if e != nil {
		return xfer.Stats{}, e
	}
	t, close, e := connect(ctx, c, wire.RRQ)
	if e != nil {
		return xfer.Stats{}, e
	}
	defer close()
	timeout, _ := strconv.Atoi(request.Options["timeout"])
	response, e := xfer.Exchange(t, xfer.RealClock{}, wire.Encode(request), time.Duration(timeout)*time.Second, 2, func(p wire.Packet) bool { return p.Op == wire.OACK || p.Op == wire.DATA && p.Block == 1 })
	if e != nil {
		return xfer.Stats{}, e
	}
	params := wire.Defaults()
	var first *wire.Packet
	var initial []byte
	if response.Op == wire.OACK {
		params, e = wire.AcceptOACK(request.Options, response.Options)
		if e != nil {
			t.Send(wire.Encode(wire.Packet{Op: wire.ERROR, Code: 8, Message: "option-negotiation"}))
			return xfer.Stats{}, e
		}
		initial = wire.Encode(wire.Packet{Op: wire.ACK, Block: 0})
	} else {
		first = &response
	}
	stats, err := xfer.Receive(t, xfer.RealClock{}, w, params, request.Mode, first, initial)
	if err == nil {
		if size, ok := response.Options["tsize"]; ok {
			n, _ := strconv.ParseInt(size, 10, 64)
			if stats.Bytes != n {
				err = errors.New("received size does not match negotiated tsize")
			}
		}
	}
	return stats, err
}
func Put(ctx context.Context, c Config, r io.Reader, size int64) (xfer.Stats, error) {
	request, e := c.request(wire.WRQ, size)
	if e != nil {
		return xfer.Stats{}, e
	}
	t, close, e := connect(ctx, c, wire.WRQ)
	if e != nil {
		return xfer.Stats{}, e
	}
	defer close()
	timeout, _ := strconv.Atoi(request.Options["timeout"])
	response, e := xfer.Exchange(t, xfer.RealClock{}, wire.Encode(request), time.Duration(timeout)*time.Second, 2, func(p wire.Packet) bool { return p.Op == wire.OACK || p.Op == wire.ACK && p.Block == 0 })
	if e != nil {
		return xfer.Stats{}, e
	}
	params := wire.Defaults()
	if response.Op == wire.OACK {
		params, e = wire.AcceptOACK(request.Options, response.Options)
		if e != nil {
			t.Send(wire.Encode(wire.Packet{Op: wire.ERROR, Code: 8, Message: "option-negotiation"}))
			return xfer.Stats{}, e
		}
	}
	stats, err := xfer.Send(t, xfer.RealClock{}, r, params, request.Mode)
	if err == nil && request.Mode == "octet" && size >= 0 && stats.Bytes != size {
		err = errors.New("source size changed during transfer")
	}
	return stats, err
}
