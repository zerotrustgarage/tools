package server_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zerotrustgarage/tools/tftp/internal/audit"
	"github.com/zerotrustgarage/tools/tftp/internal/client"
	"github.com/zerotrustgarage/tools/tftp/internal/server"
	"github.com/zerotrustgarage/tools/tftp/internal/wire"
	"github.com/zerotrustgarage/tools/tftp/internal/xfer"
)

func start(t *testing.T, c server.Config) (*server.Server, *bytes.Buffer) {
	t.Helper()
	log := new(bytes.Buffer)
	c.Audit = log
	if c.Root == "" {
		c.Root = t.TempDir()
	}
	if c.Bind == "" {
		c.Bind = "127.0.0.1"
	}
	if c.Duration == 0 {
		c.Duration = time.Minute
	}
	s, e := server.Start(context.Background(), c)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		s.Stop()
		if e := s.Wait(); e != nil {
			t.Error(e)
		}
	})
	return s, log
}
func records(t *testing.T, s *server.Server, log *bytes.Buffer) []audit.Record {
	t.Helper()
	s.Stop()
	if e := s.Wait(); e != nil {
		t.Fatal(e)
	}
	var out []audit.Record
	dec := json.NewDecoder(bytes.NewReader(log.Bytes()))
	for {
		var r audit.Record
		e := dec.Decode(&r)
		if e == io.EOF {
			break
		}
		if e != nil {
			t.Fatal(e)
		}
		out = append(out, r)
	}
	return out
}
func TestRoundTrip(t *testing.T) {
	for _, bind := range []string{"127.0.0.1", "::1"} {
		for _, single := range []bool{false, true} {
			name := bind
			if single {
				name += "/single"
			}
			t.Run(name, func(t *testing.T) {
				t.Parallel()
				root := t.TempDir()
				files := map[string][]byte{"empty": {}, "exact": bytes.Repeat([]byte{'x'}, 64), "firmware": bytes.Repeat([]byte("firmware!"), 10000), "ascii": []byte("1234567\r\nnext\rline\n")}
				for n, b := range files {
					if e := os.WriteFile(filepath.Join(root, n), b, 0600); e != nil {
						t.Fatal(e)
					}
				}
				s, log := start(t, server.Config{Root: root, Bind: bind, SinglePort: single})
				address := s.Addresses()[0]
				for n, b := range files {
					var out bytes.Buffer
					mode := "octet"
					if n == "ascii" {
						mode = "netascii"
					}
					stats, e := client.Get(context.Background(), client.Config{Address: address, Name: "/" + n, Mode: mode, BlockSize: 8, WindowSize: 4, Timeout: 1}, &out)
					if e != nil {
						t.Fatal(e)
					}
					h := sha256.Sum256(b)
					if !bytes.Equal(out.Bytes(), b) || stats.SHA256 != hex.EncodeToString(h[:]) {
						t.Fatalf("mismatch %s %+v", n, stats)
					}
				}
				_, e := client.Put(context.Background(), client.Config{Address: address, Name: "config", Timeout: 1}, strings.NewReader("secret-value"), 12)
				var peer *xfer.PeerError
				if !errors.As(e, &peer) || peer.Code != 2 {
					t.Fatalf("write allowed: %v", e)
				}
				_, e = client.Get(context.Background(), client.Config{Address: address, Name: "../escape", Timeout: 1}, io.Discard)
				if !errors.As(e, &peer) || peer.Code != 2 {
					t.Fatalf("traversal: %v", e)
				}
				r := records(t, s, log)
				complete, refused := 0, 0
				for _, record := range r {
					if record.Protocol == "" || record.Transfer == "" || record.Session == "" {
						t.Fatalf("incomplete audit: %+v", record)
					}
					if record.Result == "complete" {
						complete++
						if record.SHA256 == "" {
							t.Fatal("missing hash")
						}
					}
					if record.Result == "refused" {
						refused++
					}
				}
				if complete != 4 || refused != 2 || bytes.Contains(log.Bytes(), []byte("secret-value")) {
					t.Fatalf("audit: %s", log.Bytes())
				}
			})
		}
	}
}
func request(t *testing.T, address string, p wire.Packet) (*net.UDPConn, wire.Packet, netip.AddrPort) {
	t.Helper()
	a, e := net.ResolveUDPAddr("udp", address)
	if e != nil {
		t.Fatal(e)
	}
	network := "udp4"
	if a.IP.To4() == nil {
		network = "udp6"
	}
	c, e := net.ListenUDP(network, nil)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { c.Close() })
	c.SetDeadline(time.Now().Add(2 * time.Second))
	if _, e = c.WriteToUDP(wire.Encode(p), a); e != nil {
		t.Fatal(e)
	}
	b := make([]byte, 65536)
	n, from, e := c.ReadFromUDPAddrPort(b)
	if e != nil {
		t.Fatal(e)
	}
	q, e := wire.Decode(b[:n])
	if e != nil {
		t.Fatal(e)
	}
	return c, q, from
}
func TestSinglePortAndSilentAllowlist(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "image"), []byte("abc"), 0600)
	for _, single := range []bool{false, true} {
		s, _ := start(t, server.Config{Root: root, SinglePort: single})
		c, p, from := request(t, s.Addresses()[0], wire.Packet{Op: wire.RRQ, Name: "image", Mode: "octet"})
		local, _ := netip.ParseAddrPort(s.Addresses()[0])
		if p.Op != wire.DATA || (from.Port() == local.Port()) != single {
			t.Fatalf("wrong source: %v %v", from, p)
		}
		c.WriteToUDPAddrPort(wire.Encode(wire.Packet{Op: wire.ACK, Block: 1}), from)
		s.Stop()
		s.Wait()
	}
	s, log := start(t, server.Config{Root: root, Allow: []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")}})
	a, _ := net.ResolveUDPAddr("udp", s.Addresses()[0])
	c, e := net.ListenUDP("udp4", nil)
	if e != nil {
		t.Fatal(e)
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(100 * time.Millisecond))
	c.WriteToUDP(wire.Encode(wire.Packet{Op: wire.RRQ, Name: "image", Mode: "octet"}), a)
	if _, _, e = c.ReadFromUDP(make([]byte, 512)); !xfer.TimedOut(e) {
		t.Fatalf("out-of-scope source received reply: %v", e)
	}
	r := records(t, s, log)
	if len(r) != 1 || r[0].Reason != "outside-allowlist" {
		t.Fatalf("audit: %+v", r)
	}
}
func TestSessionStopsActiveTransfer(t *testing.T) {
	for _, single := range []bool{false, true} {
		root := t.TempDir()
		os.WriteFile(filepath.Join(root, "image"), []byte("abc"), 0600)
		s, log := start(t, server.Config{Root: root, Duration: 100 * time.Millisecond, SinglePort: single})
		_, p, _ := request(t, s.Addresses()[0], wire.Packet{Op: wire.RRQ, Name: "image", Mode: "octet", Options: map[string]string{"timeout": "255"}})
		if p.Op != wire.OACK {
			t.Fatal(p)
		}
		done := make(chan error, 1)
		go func() { done <- s.Wait() }()
		select {
		case e := <-done:
			if e != nil {
				t.Fatal(e)
			}
		case <-time.After(time.Second):
			t.Fatal("session failed to stop")
		}
		if !bytes.Contains(log.Bytes(), []byte(`"result":"aborted"`)) {
			t.Fatal(log.String())
		}
	}
}
func TestSparseFileTsizePast4GiB(t *testing.T) {
	root := t.TempDir()
	f, e := os.Create(filepath.Join(root, "large"))
	if e != nil {
		t.Fatal(e)
	}
	if e = f.Truncate(1<<32 + 7); e != nil {
		t.Fatal(e)
	}
	f.Close()
	s, _ := start(t, server.Config{Root: root})
	c, p, from := request(t, s.Addresses()[0], wire.Packet{Op: wire.RRQ, Name: "large", Mode: "octet", Options: map[string]string{"tsize": "0"}})
	if p.Op != wire.OACK || p.Options["tsize"] != "4294967303" {
		t.Fatalf("truncated tsize: %+v", p)
	}
	c.WriteToUDPAddrPort(wire.Encode(wire.Packet{Op: wire.ERROR, Code: 0, Message: "test complete"}), from)
}
func TestCurlInterop(t *testing.T) {
	curl, e := exec.LookPath("curl")
	if e != nil {
		t.Skip("curl not installed")
	}
	for _, single := range []bool{false, true} {
		t.Run(map[bool]string{false: "standard", true: "single"}[single], func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			b := bytes.Repeat([]byte("firmware\x00"), 2000)
			os.WriteFile(filepath.Join(root, "image"), b, 0600)
			s, _ := start(t, server.Config{Root: root, SinglePort: single})
			for _, flags := range [][]string{{"--tftp-blksize", "1024"}, {"--tftp-no-options"}} {
				args := append([]string{"--noproxy", "*", "--silent", "--show-error", "--max-time", "10"}, flags...)
				args = append(args, "tftp://"+s.Addresses()[0]+"/image")
				out, e := exec.Command(curl, args...).Output()
				if e != nil || !bytes.Equal(out, b) {
					t.Fatalf("curl: %v %s", e, out)
				}
			}
			for _, tool := range []string{"tftp", "atftp"} {
				path, e := exec.LookPath(tool)
				if e != nil {
					t.Log(tool + " not installed")
					continue
				}
				host, port, _ := net.SplitHostPort(s.Addresses()[0])
				dest := filepath.Join(t.TempDir(), "image")
				var args []string
				if tool == "tftp" {
					args = []string{host, port, "-m", "binary", "-c", "get", "image", dest}
				} else {
					args = []string{"--get", "--remote-file", "image", "--local-file", dest, host, port}
				}
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				out, e := exec.CommandContext(ctx, path, args...).CombinedOutput()
				cancel()
				if e != nil {
					t.Fatalf("%s: %v %s", tool, e, out)
				}
				got, e := os.ReadFile(dest)
				if e != nil || !bytes.Equal(got, b) {
					t.Fatalf("%s: mismatched file %v", tool, e)
				}
			}
		})
	}
}
