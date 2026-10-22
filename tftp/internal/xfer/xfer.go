// Package xfer implements bounded, windowed TFTP transfers. The transport and
// clock are injectable, so loss and rollover tests do not require real time.
package xfer

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"time"

	"github.com/zerotrustgarage/tools/tftp/internal/wire"
)

var ErrTimeout = errors.New("transfer timeout")
var ErrBudget = errors.New("unverified-peer budget exhausted")

type PeerError struct{ Code uint16 }

func (e *PeerError) Error() string { return fmt.Sprintf("peer error %d", e.Code) }

// Deliberately omit the peer-supplied error message from errors and audit logs.

type Transport interface {
	Send([]byte) error
	Receive(time.Time) ([]byte, error)
	Verify()
	Verified() bool
}
type Clock interface{ Now() time.Time }
type RealClock struct{}

func (RealClock) Now() time.Time { return time.Now() }
func TimedOut(e error) bool {
	var n net.Error
	return errors.Is(e, ErrTimeout) || (errors.As(e, &n) && n.Timeout())
}

type Stats struct {
	Bytes  int64  `json:"bytes"`
	SHA256 string `json:"sha256"`
}
type counter struct{ n int64 }

func (c *counter) Write(b []byte) (int, error) { c.n += int64(len(b)); return len(b), nil }

func valid(p wire.Params) error {
	if p.BlockSize < 8 || p.BlockSize > wire.MaxBlock || p.WindowSize < 1 || p.WindowSize > 64 || p.Timeout < 1 || p.Timeout > 255 {
		return errors.New("invalid transfer parameters")
	}
	return nil
}

// Exchange retransmits a request or OACK only on timeout. Unrelated or malformed
// packets never reset the deadline or the retry budget.
func Exchange(t Transport, c Clock, request []byte, timeout time.Duration, retries int, accept func(wire.Packet) bool) (wire.Packet, error) {
	for attempt := 0; attempt <= retries; attempt++ {
		if e := t.Send(request); e != nil {
			return wire.Packet{}, e
		}
		deadline := c.Now().Add(timeout)
		for {
			b, e := t.Receive(deadline)
			if TimedOut(e) {
				break
			}
			if e != nil {
				return wire.Packet{}, e
			}
			p, e := wire.Decode(b)
			if e != nil {
				continue
			}
			if p.Op == wire.ERROR {
				return p, &PeerError{p.Code}
			}
			if accept(p) {
				return p, nil
			}
		}
	}
	return wire.Packet{}, ErrTimeout
}

func Send(t Transport, c Clock, r io.Reader, p wire.Params, mode string) (stats Stats, err error) {
	if e := valid(p); e != nil {
		return stats, e
	}
	h := sha256.New()
	count := &counter{}
	r = io.TeeReader(r, io.MultiWriter(h, count))
	defer func() { stats = Stats{count.n, hex.EncodeToString(h.Sum(nil))} }()
	if mode == "netascii" {
		r = &asciiReader{r: bufio.NewReader(r)}
	}
	var seq uint64 = 1
	verified := t.Verified()
	for {
		var frames [][]byte
		last := false
		for range p.WindowSize {
			data := make([]byte, p.BlockSize)
			n, e := io.ReadFull(r, data)
			if e != nil && e != io.EOF && e != io.ErrUnexpectedEOF {
				return stats, e
			}
			frames = append(frames, wire.Encode(wire.Packet{Op: wire.DATA, Block: uint16(seq + uint64(len(frames))), Data: data[:n]}))
			if n < p.BlockSize {
				last = true
				break
			}
		}
		retries := 0
		transmit := func() error {
			for _, f := range frames {
				if e := t.Send(f); e != nil {
					return e
				}
			}
			return nil
		}
		if err = transmit(); err != nil {
			return stats, err
		}
		deadline := c.Now().Add(time.Duration(p.Timeout) * time.Second)
		for len(frames) > 0 {
			b, e := t.Receive(deadline)
			if TimedOut(e) {
				limit := 2
				if verified {
					limit = 5
				}
				if retries >= limit {
					return stats, ErrTimeout
				}
				retries++
				if e = transmit(); e != nil {
					return stats, e
				}
				deadline = c.Now().Add(time.Duration(p.Timeout) * time.Second)
				continue
			}
			if e != nil {
				return stats, e
			}
			packet, e := wire.Decode(b)
			if e != nil {
				continue
			}
			if packet.Op == wire.ERROR {
				return stats, &PeerError{packet.Code}
			}
			if packet.Op != wire.ACK {
				continue
			}
			// A window is at most 64 blocks, so modular subtraction is unambiguous,
			// including block 65535 -> 0. Duplicate ACKs cannot trigger DATA.
			delta := uint16(packet.Block - uint16(seq))
			if int(delta) >= len(frames) {
				continue
			}
			n := int(delta) + 1
			seq += uint64(n)
			frames = frames[n:]
			verified = true
			t.Verify()
			retries = 0
			deadline = c.Now().Add(time.Duration(p.Timeout) * time.Second)
		}
		if last {
			return stats, nil
		}
	}
}

// Receive acknowledges only bytes committed to w. first is an already received
// DATA packet (for peers declining options); initial is ACK 0 or an initial OACK.
func Receive(t Transport, c Clock, w io.Writer, p wire.Params, mode string, first *wire.Packet, initial []byte) (stats Stats, err error) {
	return ReceiveCommit(t, c, w, p, mode, first, initial, nil)
}

// ReceiveCommit lets a server validate and publish an upload before acknowledging
// its final DATA. A failed commit is never reported to the peer as success.
func ReceiveCommit(t Transport, c Clock, w io.Writer, p wire.Params, mode string, first *wire.Packet, initial []byte, commit func(Stats) error) (stats Stats, err error) {
	if e := valid(p); e != nil {
		return stats, e
	}
	h := sha256.New()
	count := &counter{}
	w = io.MultiWriter(w, h, count)
	defer func() { stats = Stats{count.n, hex.EncodeToString(h.Sum(nil))} }()
	var ascii *asciiWriter
	if mode == "netascii" {
		ascii = &asciiWriter{w: w}
		w = ascii
	}
	lastReply := initial
	if len(initial) > 0 {
		if e := t.Send(initial); e != nil {
			return stats, e
		}
	}
	expected := uint64(1)
	inWindow := 0
	retries := 0
	deadline := c.Now().Add(time.Duration(p.Timeout) * time.Second)
	for {
		var packet wire.Packet
		if first != nil {
			packet = *first
			first = nil
		} else {
			b, e := t.Receive(deadline)
			if TimedOut(e) {
				limit := 2
				if expected > 1 {
					limit = 5
				}
				if retries >= limit {
					return stats, ErrTimeout
				}
				retries++
				if expected > 1 {
					lastReply = wire.Encode(wire.Packet{Op: wire.ACK, Block: uint16(expected - 1)})
				}
				if len(lastReply) > 0 {
					if e = t.Send(lastReply); e != nil {
						return stats, e
					}
				}
				deadline = c.Now().Add(time.Duration(p.Timeout) * time.Second)
				continue
			}
			if e != nil {
				return stats, e
			}
			packet, e = wire.Decode(b)
			if e != nil {
				continue
			}
		}
		if packet.Op == wire.ERROR {
			return stats, &PeerError{packet.Code}
		}
		if packet.Op == wire.OACK && expected == 1 {
			if len(lastReply) > 0 {
				if e := t.Send(lastReply); e != nil {
					return stats, e
				}
			}
			continue
		}
		if packet.Op != wire.DATA || len(packet.Data) > p.BlockSize {
			continue
		}
		if packet.Block != uint16(expected) {
			// A gap produces one cumulative ACK. The sender's timeout restarts
			// at the first unacknowledged block instead of duplicating DATA.
			if e := t.Send(wire.Encode(wire.Packet{Op: wire.ACK, Block: uint16(expected - 1)})); e != nil {
				return stats, e
			}
			continue
		}
		n, e := w.Write(packet.Data)
		if e != nil {
			return stats, e
		}
		if n != len(packet.Data) {
			return stats, io.ErrShortWrite
		}
		expected++
		inWindow++
		retries = 0
		t.Verify()
		final := len(packet.Data) < p.BlockSize
		if final && ascii != nil && ascii.cr {
			return stats, errors.New("truncated netascii escape")
		}
		if final && commit != nil {
			if e := commit(Stats{Bytes: count.n, SHA256: hex.EncodeToString(h.Sum(nil))}); e != nil {
				return stats, e
			}
		}
		if inWindow == p.WindowSize || final {
			lastReply = wire.Encode(wire.Packet{Op: wire.ACK, Block: packet.Block})
			if e := t.Send(lastReply); e != nil {
				return stats, e
			}
			inWindow = 0
		}
		deadline = c.Now().Add(time.Duration(p.Timeout) * time.Second)
		if final {
			// Dally for one fixed timeout to recover a lost final ACK. Bound
			// replies too; duplicates cannot extend the completed transfer.
			for replies := 0; replies < 5; {
				b, e := t.Receive(deadline)
				if TimedOut(e) {
					return stats, nil
				}
				if e != nil {
					return stats, e
				}
				q, e := wire.Decode(b)
				if e == nil && q.Op == wire.DATA && q.Block == packet.Block {
					replies++
					if e = t.Send(lastReply); e != nil {
						return stats, e
					}
				}
			}
			return stats, nil
		}
	}
}
