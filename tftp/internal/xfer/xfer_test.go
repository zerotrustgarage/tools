package xfer

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/zerotrustgarage/tools/tftp/internal/wire"
)

type event struct {
	p wire.Packet
	e error
}
type fake struct {
	now            time.Time
	events         []event
	sent           []wire.Packet
	auto, verified bool
	last           wire.Packet
	dataCount      int
	blocks         map[uint16]bool
	beforeReceive  func(*fake)
}

func (f *fake) Now() time.Time { return f.now }
func (f *fake) Verify()        { f.verified = true }
func (f *fake) Verified() bool { return f.verified }
func (f *fake) Send(b []byte) error {
	p, e := wire.Decode(b)
	if e != nil {
		return e
	}
	f.last = p
	if f.auto {
		f.dataCount++
		if f.blocks != nil {
			f.blocks[p.Block] = true
		}
	} else {
		p.Data = bytes.Clone(p.Data)
		f.sent = append(f.sent, p)
	}
	return nil
}
func (f *fake) Receive(deadline time.Time) ([]byte, error) {
	if f.beforeReceive != nil {
		f.beforeReceive(f)
	}
	if len(f.events) > 0 {
		e := f.events[0]
		f.events = f.events[1:]
		if e.e != nil {
			f.now = deadline
			return nil, e.e
		}
		return wire.Encode(e.p), nil
	}
	if f.auto {
		return wire.Encode(wire.Packet{Op: wire.ACK, Block: f.last.Block}), nil
	}
	f.now = deadline
	return nil, ErrTimeout
}
func ack(n uint16) event { return event{p: wire.Packet{Op: wire.ACK, Block: n}} }
func data(n uint16, s string) event {
	return event{p: wire.Packet{Op: wire.DATA, Block: n, Data: []byte(s)}}
}
func TestSendBoundariesAndRollover(t *testing.T) {
	for _, size := range []int{0, 7, 8, 16, 8 * 65535, 8 * 65536, 8*65536 + 1} {
		f := &fake{auto: true, blocks: map[uint16]bool{}}
		b := bytes.Repeat([]byte{'x'}, size)
		stats, e := Send(f, f, bytes.NewReader(b), wire.Params{BlockSize: 8, WindowSize: 16, Timeout: 1}, "octet")
		h := sha256.Sum256(b)
		if e != nil || stats.Bytes != int64(size) || stats.SHA256 != hex.EncodeToString(h[:]) {
			t.Fatalf("size %d: %+v %v", size, stats, e)
		}
		if f.dataCount != size/8+1 {
			t.Fatalf("size %d: %d DATA", size, f.dataCount)
		}
		if size >= 8*65535 && !f.blocks[0] {
			t.Fatal("no rollover block zero")
		}
	}
}
func TestNoSorcererAndRetransmit(t *testing.T) {
	f := &fake{events: []event{ack(0), ack(0), {e: ErrTimeout}, ack(1)}}
	reads := 0
	f.beforeReceive = func(f *fake) {
		reads++
		want := 1
		if reads == 4 {
			want = 2
		}
		if len(f.sent) != want {
			t.Fatalf("DATA retransmitted on duplicate ACK: %d at receive %d", len(f.sent), reads)
		}
	}
	if _, e := Send(f, f, bytes.NewBufferString("x"), wire.Params{BlockSize: 8, WindowSize: 1, Timeout: 1}, "octet"); e != nil {
		t.Fatal(e)
	}
	if !bytes.Equal(f.sent[0].Data, f.sent[1].Data) {
		t.Fatal("changed retransmission")
	}
}
func TestPartialWindow(t *testing.T) {
	f := &fake{events: []event{ack(1), {e: ErrTimeout}, ack(3)}}
	if _, e := Send(f, f, bytes.NewReader(bytes.Repeat([]byte{'x'}, 18)), wire.Params{BlockSize: 8, WindowSize: 4, Timeout: 1}, "octet"); e != nil {
		t.Fatal(e)
	}
	want := []uint16{1, 2, 3, 2, 3}
	for i, p := range f.sent {
		if p.Block != want[i] {
			t.Fatalf("blocks: %+v", f.sent)
		}
	}
	if len(f.sent) != len(want) {
		t.Fatal(len(f.sent))
	}
}
func TestAmplificationAndVanishedPeer(t *testing.T) {
	for _, verified := range []bool{false, true} {
		f := &fake{verified: verified}
		_, e := Send(f, f, bytes.NewBufferString("firmware"), wire.Params{BlockSize: 512, WindowSize: 1, Timeout: 1}, "octet")
		want := 3
		if verified {
			want = 6
		}
		if !errors.Is(e, ErrTimeout) || len(f.sent) != want {
			t.Fatalf("verified %t: packets %d, %v", verified, len(f.sent), e)
		}
	}
	f := &fake{}
	_, e := Exchange(f, f, wire.Encode(wire.Packet{Op: wire.OACK, Options: map[string]string{"blksize": "1468"}}), time.Second, 2, func(p wire.Packet) bool { return p.Op == wire.ACK && p.Block == 0 })
	if !errors.Is(e, ErrTimeout) || len(f.sent) != 3 {
		t.Fatal("OACK amplification", e, len(f.sent))
	}
	for _, p := range f.sent {
		if p.Op == wire.DATA {
			t.Fatal("unverified DATA after OACK")
		}
	}
}
func TestReceiveLossDuplicationAndFinalACK(t *testing.T) {
	f := &fake{events: []event{data(2, "ijklmnop"), data(1, "abcdefgh"), {e: ErrTimeout}, data(1, "abcdefgh"), data(2, "ijklmnop"), data(2, "ijklmnop"), data(3, "q"), data(3, "q")}}
	var out bytes.Buffer
	stats, e := Receive(f, f, &out, wire.Params{BlockSize: 8, WindowSize: 2, Timeout: 1}, "octet", nil, wire.Encode(wire.Packet{Op: wire.ACK, Block: 0}))
	if e != nil || out.String() != "abcdefghijklmnopq" || stats.Bytes != 17 {
		t.Fatalf("%q %+v %v", out.String(), stats, e)
	}
	seen := map[uint16]int{}
	for _, p := range f.sent {
		seen[p.Block]++
	}
	if seen[1] < 1 || seen[3] != 2 {
		t.Fatalf("ACK recovery: %v", seen)
	}
}
func TestNetascii(t *testing.T) {
	original := []byte("1234567\r\nnext\rline\n")
	f := &fake{events: []event{ack(1), ack(2), ack(3), ack(4)}}
	if _, e := Send(f, f, bytes.NewReader(original), wire.Params{BlockSize: 8, WindowSize: 1, Timeout: 1}, "netascii"); e != nil {
		t.Fatal(e)
	}
	receiver := &fake{}
	for _, p := range f.sent {
		receiver.events = append(receiver.events, event{p: p})
	}
	var out bytes.Buffer
	stats, e := Receive(receiver, receiver, &out, wire.Params{BlockSize: 8, WindowSize: 1, Timeout: 1}, "netascii", nil, nil)
	if e != nil || !bytes.Equal(out.Bytes(), original) || stats.Bytes != int64(len(original)) {
		t.Fatalf("%q %+v %v", out.Bytes(), stats, e)
	}
	for _, s := range []string{"\r", "\rx"} {
		f := &fake{events: []event{data(1, s)}}
		if _, e := Receive(f, f, io.Discard, wire.Params{BlockSize: 8, WindowSize: 1, Timeout: 1}, "netascii", nil, nil); e == nil {
			t.Fatal("invalid netascii accepted")
		}
	}
}
func TestPeerErrors(t *testing.T) {
	for _, op := range []string{"send", "receive", "exchange"} {
		f := &fake{events: []event{{p: wire.Packet{Op: wire.ERROR, Code: 2, Message: "secret-value"}}}}
		var e error
		switch op {
		case "send":
			_, e = Send(f, f, bytes.NewBufferString("x"), wire.Params{BlockSize: 8, WindowSize: 1, Timeout: 1}, "octet")
		case "receive":
			_, e = Receive(f, f, io.Discard, wire.Params{BlockSize: 8, WindowSize: 1, Timeout: 1}, "octet", nil, nil)
		case "exchange":
			_, e = Exchange(f, f, wire.Encode(wire.Packet{Op: wire.ACK}), time.Second, 2, func(wire.Packet) bool { return true })
		}
		var peer *PeerError
		if !errors.As(e, &peer) || e.Error() != "peer error 2" {
			t.Fatalf("%s: %v", op, e)
		}
	}
}

func FuzzNetascii(f *testing.F) {
	f.Add([]byte("hello\r\nworld\r\x00"), uint8(8))
	f.Add([]byte("\r"), uint8(1))
	f.Fuzz(func(t *testing.T, b []byte, chunk uint8) {
		if len(b) > 65536 {
			return
		}
		var out bytes.Buffer
		w := &asciiWriter{w: &out}
		size := int(chunk) + 1
		for at := 0; at < len(b); at += size {
			end := min(at+size, len(b))
			if _, e := w.Write(b[at:end]); e != nil {
				return
			}
		}
		if w.cr {
			return
		}
		r := &asciiReader{r: bufio.NewReader(bytes.NewReader(out.Bytes()))}
		got, e := io.ReadAll(r)
		if e != nil || !bytes.Equal(got, b) {
			t.Fatalf("netascii roundtrip: %q -> %q (%v)", b, got, e)
		}
	})
}

func TestUploadCommitBeforeFinalACK(t *testing.T) {
	for _, fail := range []bool{false, true} {
		f := &fake{events: []event{data(1, "payload"), data(1, "payload")}}
		calls := 0
		failure := errors.New("commit failed")
		_, e := ReceiveCommit(f, f, io.Discard, wire.Params{BlockSize: 8, WindowSize: 1, Timeout: 1}, "octet", nil, wire.Encode(wire.Packet{Op: wire.ACK, Block: 0}), func(stats Stats) error {
			calls++
			if stats.Bytes != 7 || stats.SHA256 == "" {
				t.Fatal("incomplete commit stats", stats)
			}
			for _, p := range f.sent {
				if p.Op == wire.ACK && p.Block == 1 {
					t.Fatal("final ACK preceded commit")
				}
			}
			if fail {
				return failure
			}
			return nil
		})
		if calls != 1 || fail && !errors.Is(e, failure) || !fail && e != nil {
			t.Fatalf("calls %d: %v", calls, e)
		}
		finalACKs := 0
		for _, p := range f.sent {
			if p.Op == wire.ACK && p.Block == 1 {
				finalACKs++
			}
		}
		if fail && finalACKs != 0 || !fail && finalACKs != 2 {
			t.Fatal("incorrect final ACKs", finalACKs)
		}
	}
}
