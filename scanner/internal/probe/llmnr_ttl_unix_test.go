//go:build !windows

package probe

import (
	"context"
	"net"
	"syscall"
	"testing"
)

// RFC 4795 section 2.5: a TCP LLMNR query must leave with an IPv4 TTL of 1.
func TestLLMNRSocketTTLIsOne(t *testing.T) {
	l, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Skip(err)
	}
	defer l.Close()
	go func() {
		c, err := l.Accept()
		if err == nil {
			c.Close()
		}
	}()
	var ttl int
	d := net.Dialer{Control: func(_, _ string, c syscall.RawConn) error {
		return c.Control(func(fd uintptr) {
			if err := setTTLOne(fd); err != nil {
				t.Error(err)
			}
			ttl, err = syscall.GetsockoptInt(int(fd), syscall.IPPROTO_IP, syscall.IP_TTL)
			if err != nil {
				t.Error(err)
			}
		})
	}}
	c, err := d.DialContext(context.Background(), "tcp4", l.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	c.Close()
	if ttl != 1 {
		t.Fatalf("TTL %d, want 1", ttl)
	}
}
