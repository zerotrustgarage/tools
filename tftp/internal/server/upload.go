package server

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/zerotrustgarage/tools/tftp/internal/audit"
	"github.com/zerotrustgarage/tools/tftp/internal/store"
	"github.com/zerotrustgarage/tools/tftp/internal/wire"
	"github.com/zerotrustgarage/tools/tftp/internal/xfer"
)

func (s *Server) write(ctx context.Context, l *listener, t *transport, p wire.Packet) {
	started := time.Now()
	record := audit.Record{Session: s.id, Transfer: ID(), Op: "write", Source: t.peer.String(), Local: l.conn.LocalAddr().String(), Name: p.Name, Result: "refused"}
	defer func() {
		record.DurationMS = time.Since(started).Milliseconds()
		if record.Result == "refused" {
			ok, n := s.guard.Audit(t.peer.Addr().Unmap(), time.Now())
			if !ok {
				return
			}
			record.Suppressed = n
		}
		s.record(record)
	}()
	fail := func(reason string, code uint16) {
		record.Reason = reason
		t.Send(wire.Encode(wire.Packet{Op: wire.ERROR, Code: code, Message: reason}))
	}
	if _, e := store.Name(p.Name); e != nil {
		fail("name-policy", 2)
		return
	}
	if p.Mode == "netascii" {
		delete(p.Options, "tsize")
	}
	params, opts, e := wire.Negotiate(p.Options, s.config.MaxBlock, s.config.MaxWindow, 0, false)
	if e != nil {
		fail("option-negotiation", 8)
		return
	}
	record.Options = opts
	size := int64(-1)
	if v, ok := opts["tsize"]; ok {
		size, _ = strconv.ParseInt(v, 10, 64)
	}
	upload, e := s.drop.Begin(t.peer.Addr(), p.Name, size)
	if e != nil {
		reason := "upload-unavailable"
		code := uint16(2)
		if errors.Is(e, store.ErrTooLarge) {
			reason = "too-large"
			code = 3
		}
		if errors.Is(e, store.ErrSpace) {
			reason = "disk-space-floor"
			code = 3
		}
		fail(reason, code)
		return
	}
	defer upload.Close()
	initial := wire.Packet{Op: wire.ACK, Block: 0}
	if len(opts) > 0 {
		initial = wire.Packet{Op: wire.OACK, Options: opts}
	}
	var receipt store.Receipt
	committed := false
	stats, e := xfer.ReceiveCommit(t, xfer.RealClock{}, s.progressWriter(upload, record), params, p.Mode, nil, wire.Encode(initial), func(stats xfer.Stats) error {
		if e := ctx.Err(); e != nil {
			return e
		}
		var err error
		receipt, err = upload.Commit(stats.Bytes, stats.SHA256)
		committed = err == nil
		return err
	})
	record.Bytes, record.SHA256 = stats.Bytes, stats.SHA256
	if committed {
		record.Result = "complete"
		record.StoredAs = receipt.StoredAs
		record.Findings = receipt.Scan.Codes()
		record.FindingsTruncated = receipt.Scan.Truncated
		record.ChangedSince = receipt.ChangedSince
		record.Changed = receipt.Changed
		return
	}
	switch {
	case ctx.Err() != nil:
		record.Result = "aborted"
		record.Reason = "session-ended"
	case xfer.TimedOut(e):
		record.Result = "timeout"
	case errors.Is(e, store.ErrTooLarge):
		record.Result = "refused"
		fail("too-large", 3)
	case errors.Is(e, store.ErrSpace):
		record.Result = "refused"
		fail("disk-space-floor", 3)
	default:
		var peer *xfer.PeerError
		if errors.As(e, &peer) {
			record.Result = "peer-error"
		} else {
			record.Result = "aborted"
			fail("upload-failed", 3)
		}
	}
}
