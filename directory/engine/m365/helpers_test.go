package m365

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"sync"
	"testing"
	"time"
)

// loopbackOnly is an http.Client whose dialer refuses every address but
// 127.0.0.1 (localhost is dialed as 127.0.0.1), so no test request can leave
// the machine. It records what was dialed.
type loopbackOnly struct {
	mu     sync.Mutex
	dialed []string
}

func (l *loopbackOnly) client() *http.Client {
	d := &net.Dialer{Timeout: 5 * time.Second}
	tr := &http.Transport{
		Proxy: nil, // never the environment's proxy
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, err
			}
			if host == "localhost" {
				host = "127.0.0.1"
			}
			l.mu.Lock()
			l.dialed = append(l.dialed, host)
			l.mu.Unlock()
			if host != "127.0.0.1" {
				return nil, fmt.Errorf("test refused a dial to %s", addr)
			}
			return d.DialContext(ctx, "tcp4", net.JoinHostPort(host, port))
		},
	}
	return &http.Client{
		Transport:     tr,
		Timeout:       10 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

func (l *loopbackOnly) check(t *testing.T) {
	t.Helper()
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, h := range l.dialed {
		if h != "127.0.0.1" {
			t.Errorf("a request tried to leave 127.0.0.1: %s", h)
		}
	}
}
