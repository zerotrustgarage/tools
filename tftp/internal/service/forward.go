package service

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"sync"
	"time"
)

type Forward struct {
	mu    sync.Mutex
	conn  net.Conn
	event eventSink
	host  string
}
type eventSink interface {
	Write(string) error
	Close() error
}

func NewForward(c Config) (*Forward, error) {
	f := &Forward{host: "-"}
	host, _ := os.Hostname()
	if host != "" && !strings.ContainsAny(host, " \r\n\t") {
		f.host = host
	}
	if c.SyslogTLS != "" {
		host, _, e := net.SplitHostPort(c.SyslogTLS)
		if e != nil {
			return nil, e
		}
		cfg := &tls.Config{MinVersion: tls.VersionTLS12, ServerName: host}
		if c.SyslogCA != "" {
			b, e := os.ReadFile(c.SyslogCA)
			if e != nil {
				return nil, e
			}
			pool, e := x509.SystemCertPool()
			if e != nil {
				pool = x509.NewCertPool()
			}
			if !pool.AppendCertsFromPEM(b) {
				return nil, errors.New("invalid syslog CA")
			}
			cfg.RootCAs = pool
		}
		conn, e := tls.DialWithDialer(&net.Dialer{Timeout: 5 * time.Second}, "tcp", c.SyslogTLS, cfg)
		if e != nil {
			return nil, e
		}
		f.conn = conn
	}
	if c.EventLog {
		e, err := openEventLog()
		if err != nil {
			f.Close()
			return nil, err
		}
		f.event = e
	}
	return f, nil
}
func (f *Forward) Write(b []byte) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(b) > 65536 || !json.Valid(b) {
		return 0, errors.New("invalid audit forwarding record")
	}
	if f.conn != nil {
		msg := fmt.Sprintf("<134>1 %s %s ZTG-TFTP - audit - %s", time.Now().UTC().Format(time.RFC3339Nano), f.host, strings.TrimSpace(string(b)))
		frame := []byte(fmt.Sprintf("%d %s", len(msg), msg))
		f.conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
		n, e := f.conn.Write(frame)
		if e != nil {
			return 0, e
		}
		if n != len(frame) {
			return 0, io.ErrShortWrite
		}
	}
	if f.event != nil {
		if e := f.event.Write(strings.TrimSpace(string(b))); e != nil {
			return 0, e
		}
	}
	return len(b), nil
}
func (f *Forward) Close() error {
	if f.conn != nil {
		f.conn.Close()
	}
	if f.event != nil {
		return f.event.Close()
	}
	return nil
}
