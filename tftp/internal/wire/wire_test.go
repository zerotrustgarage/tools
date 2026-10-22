package wire

import (
	"bytes"
	"reflect"
	"strings"
	"testing"
)

func TestPackets(t *testing.T) {
	for _, p := range []Packet{
		{Op: RRQ, Name: "firmware.bin", Mode: "octet", Options: map[string]string{"blksize": "1468", "tsize": "0"}},
		{Op: WRQ, Name: "config", Mode: "netascii"}, {Op: DATA, Block: 65535, Data: []byte{0, 1, 255}},
		{Op: ACK, Block: 0}, {Op: ERROR, Code: 2, Message: "access denied"}, {Op: OACK, Options: map[string]string{"windowsize": "16"}},
	} {
		b := Encode(p)
		got, e := Decode(b)
		if e != nil || !bytes.Equal(Encode(got), b) {
			t.Fatalf("%+v: %+v %v", p, got, e)
		}
	}
	for _, b := range [][]byte{nil, {0}, {0, 3, 0}, {0, 4, 0, 1, 2}, {0, 5, 0, 1}, {0, 9}, []byte("\x00\x01a\x00mail\x00"), []byte("\x00\x06blksize\x008\x00BLKSIZE\x009\x00"), []byte("\x00\x06blksize\x00"), append([]byte{0, 1}, bytes.Repeat([]byte{'x'}, 2048)...)} {
		if _, e := Decode(b); e == nil {
			t.Fatalf("accepted malformed %q", b)
		}
	}
}
func TestOptions(t *testing.T) {
	opts := map[string]string{"blksize": "65464", "windowsize": "65535", "timeout": "255", "tsize": "0", "unknown": "ignored"}
	p, out, e := Negotiate(opts, 1468, 16, 1<<33, true)
	if e != nil || p != (Params{1468, 16, 255}) || out["tsize"] != "8589934592" || len(out) != 4 {
		t.Fatalf("%+v %+v %v", p, out, e)
	}
	if _, e = AcceptOACK(opts, out); e != nil {
		t.Fatal(e)
	}
	for _, bad := range []map[string]string{{"blksize": "7"}, {"blksize": "65465"}, {"windowsize": "0"}, {"timeout": "256"}, {"tsize": "1"}, {"tsize": "18446744073709551616"}, {"timeout": "+1"}, {"windowsize": "-1"}} {
		if _, _, e := Negotiate(bad, 1468, 16, 0, true); e == nil {
			t.Fatalf("accepted %v", bad)
		}
	}
	for _, got := range []map[string]string{{"unrequested": "1"}, {"blksize": "1500"}, {"windowsize": "9"}, {"timeout": "2"}, {}} {
		if _, e := AcceptOACK(map[string]string{"blksize": "1468", "windowsize": "8", "timeout": "1"}, got); e == nil {
			t.Fatalf("accepted OACK %v", got)
		}
	}
}
func FuzzDecode(f *testing.F) {
	for _, p := range []Packet{{Op: RRQ, Name: "x", Mode: "octet"}, {Op: WRQ, Name: "x", Mode: "netascii"}, {Op: OACK, Options: map[string]string{"tsize": "4294967297"}}, {Op: ACK, Block: 65535}, {Op: DATA, Data: []byte("x")}, {Op: ERROR, Code: 2, Message: "denied"}} {
		f.Add(Encode(p))
	}
	f.Fuzz(func(t *testing.T, b []byte) {
		p, e := Decode(b)
		if e == nil {
			q, e := Decode(Encode(p))
			if e != nil || !reflect.DeepEqual(q, p) {
				t.Fatalf("round trip: %+v %+v %v", p, q, e)
			}
		}
	})
}
func FuzzOptions(f *testing.F) {
	f.Add("blksize\x001468\x00windowsize\x008")
	f.Add("timeout\x00255")
	f.Fuzz(func(t *testing.T, s string) {
		if len(s) > 2048 {
			return
		}
		m, e := ParseOptions(strings.Split(s, "\x00"))
		if e == nil {
			p, out, e := Negotiate(m, 1468, 16, 1<<33, true)
			if e == nil && (p.BlockSize < 8 || p.BlockSize > 1468 || p.WindowSize < 1 || p.WindowSize > 16 || len(out) > 4) {
				t.Fatal("unbounded options")
			}
		}
	})
}
