package client

import (
	"bytes"
	"context"
	"net"
	"testing"
	"time"

	"github.com/zerotrustgarage/tools/tftp/internal/wire"
)

// A minimal independent RFC 1350 peer checks client fallback without reusing xfer.
func TestLegacyPeer(t *testing.T) {
	for _, op := range []uint16{wire.RRQ, wire.WRQ} {
		t.Run(map[uint16]string{wire.RRQ: "get", wire.WRQ: "put"}[op], func(t *testing.T) {
			t.Parallel()
			conn, e := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
			if e != nil {
				t.Fatal(e)
			}
			defer conn.Close()
			conn.SetDeadline(time.Now().Add(5 * time.Second))
			errCh := make(chan error, 1)
			go func() {
				b := make([]byte, 2048)
				n, peer, e := conn.ReadFromUDP(b)
				if e != nil {
					errCh <- e
					return
				}
				p, e := wire.Decode(b[:n])
				if e != nil || p.Op != op {
					errCh <- context.Canceled
					return
				}
				if op == wire.RRQ {
					conn.WriteToUDP(wire.Encode(wire.Packet{Op: wire.DATA, Block: 1, Data: []byte("firmware")}), peer)
					n, _, e = conn.ReadFromUDP(b)
					if e == nil {
						p, e = wire.Decode(b[:n])
						if p.Op != wire.ACK || p.Block != 1 {
							e = context.Canceled
						}
					}
				} else {
					conn.WriteToUDP(wire.Encode(wire.Packet{Op: wire.ACK, Block: 0}), peer)
					n, _, e = conn.ReadFromUDP(b)
					if e == nil {
						p, e = wire.Decode(b[:n])
						if p.Op != wire.DATA || p.Block != 1 || string(p.Data) != "firmware" {
							e = context.Canceled
						}
						conn.WriteToUDP(wire.Encode(wire.Packet{Op: wire.ACK, Block: 1}), peer)
					}
				}
				errCh <- e
			}()
			c := Config{Address: conn.LocalAddr().String(), Name: "image", Timeout: 1}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if op == wire.RRQ {
				var b bytes.Buffer
				_, e = Get(ctx, c, &b)
				if b.String() != "firmware" {
					t.Fatal(b.String())
				}
			} else {
				_, e = Put(ctx, c, bytes.NewBufferString("firmware"), 8)
			}
			if e != nil {
				t.Fatal(e)
			}
			if e = <-errCh; e != nil {
				t.Fatal(e)
			}
		})
	}
}
