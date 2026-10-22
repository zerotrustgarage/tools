package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zerotrustgarage/tools/tftp/internal/client"
	"github.com/zerotrustgarage/tools/tftp/internal/server"
	"github.com/zerotrustgarage/tools/tftp/internal/store"
	"github.com/zerotrustgarage/tools/tftp/internal/wire"
	"github.com/zerotrustgarage/tools/tftp/internal/xfer"
)

func uploadConfig(t *testing.T) server.Config {
	t.Helper()
	base := t.TempDir()
	root := filepath.Join(base, "images")
	if e := os.Mkdir(root, 0700); e != nil {
		t.Fatal(e)
	}
	return server.Config{Root: root, Upload: filepath.Join(base, "collected"), UploadMax: 4096, FreeFloor: 1}
}
func TestUploadRoundTrip(t *testing.T) {
	for _, bind := range []string{"127.0.0.1", "::1"} {
		for _, single := range []bool{false, true} {
			name := bind
			if single {
				name += "/single"
			}
			t.Run(name, func(t *testing.T) {
				t.Parallel()
				config := uploadConfig(t)
				config.Bind = bind
				config.SinglePort = single
				s, log := start(t, config)
				content := "switch 1 provision c9300\npassword 0 DO-NOT-LOG\nline vty 0 4\n transport input telnet\n"
				var previous string
				for i, body := range []string{content, content, content + "no vstack\n"} {
					stats, e := client.Put(context.Background(), client.Config{Address: s.Addresses()[0], Name: "backup/router.cfg", BlockSize: 8, WindowSize: 4, Timeout: 1}, strings.NewReader(body), int64(len(body)))
					if e != nil {
						t.Fatal(e)
					}
					if stats.Bytes != int64(len(body)) {
						t.Fatal(stats)
					}
					var latest store.Receipt
					if e = store.Rescan(config.Upload, func(r store.Receipt) error {
						if r.Time.After(latest.Time) {
							latest = r
						}
						return nil
					}); e != nil {
						t.Fatal(e)
					}
					data, e := os.ReadFile(filepath.Join(config.Upload, filepath.FromSlash(latest.StoredAs)))
					if e != nil || string(data) != body {
						t.Fatalf("saved: %q %v", data, e)
					}
					if i == 0 {
						if latest.Changed != nil {
							t.Fatal("first upload has prior version")
						}
					} else if latest.Changed == nil || *latest.Changed != (i == 2) || latest.ChangedSince != previous {
						t.Fatalf("change status: %+v", latest)
					}
					previous = latest.StoredAs
				}
				ascii := "1234567\r\npassword 7 DO-NOT-LOG\n"
				if _, e := client.Put(context.Background(), client.Config{Address: s.Addresses()[0], Name: "ascii", Mode: "netascii", BlockSize: 8, WindowSize: 4, Timeout: 1}, strings.NewReader(ascii), int64(len(ascii))); e != nil {
					t.Fatal(e)
				}
				var out bytes.Buffer
				_, e := client.Get(context.Background(), client.Config{Address: s.Addresses()[0], Name: "backup/router.cfg", Timeout: 1}, &out)
				var peer *xfer.PeerError
				if !errors.As(e, &peer) || peer.Code != 1 {
					t.Fatalf("served an upload: %v", e)
				}
				r := records(t, s, log)
				writes := 0
				for _, record := range r {
					if record.Op == "write" {
						writes++
						if record.Result != "complete" || record.StoredAs == "" || len(record.Findings) == 0 {
							t.Fatalf("audit: %+v", record)
						}
					}
				}
				if writes != 4 || bytes.Contains(log.Bytes(), []byte("DO-NOT-LOG")) {
					t.Fatalf("audit: %s", log.Bytes())
				}
			})
		}
	}
}
func TestUploadRefusalsAndCleanup(t *testing.T) {
	config := uploadConfig(t)
	config.UploadMax = 16
	s, log := start(t, config)
	for _, name := range []string{"large", "../escape"} {
		_, e := client.Put(context.Background(), client.Config{Address: s.Addresses()[0], Name: name, Timeout: 1}, strings.NewReader(strings.Repeat("x", 17)), 17)
		var peer *xfer.PeerError
		if !errors.As(e, &peer) {
			t.Fatal("upload refusal missing", e)
		}
	}
	// No tsize: enforce the cap while streaming, before a final ACK can escape.
	_, e := client.Put(context.Background(), client.Config{Address: s.Addresses()[0], Name: "stream", Timeout: 1}, strings.NewReader(strings.Repeat("x", 17)), -1)
	var peer *xfer.PeerError
	if !errors.As(e, &peer) || peer.Code != 3 {
		t.Fatal("stream cap not enforced", e)
	}
	// Abandon an accepted WRQ while it waits for DATA. Session shutdown removes it.
	_, p, _ := request(t, s.Addresses()[0], wire.Packet{Op: wire.WRQ, Name: "abandoned", Mode: "octet", Options: map[string]string{"timeout": "255"}})
	if p.Op != wire.OACK {
		t.Fatal(p)
	}
	r := records(t, s, log)
	if len(r) != 4 {
		t.Fatalf("requests not accounted: %+v", r)
	}
	entries, e := os.ReadDir(filepath.Join(config.Upload, ".incoming"))
	if e != nil || len(entries) != 0 {
		t.Fatal("partial upload left behind", e, len(entries))
	}
	count := 0
	store.Rescan(config.Upload, func(store.Receipt) error { count++; return nil })
	if count != 0 {
		t.Fatal("failed upload committed")
	}
}
func TestUploadFreeSpaceRefusal(t *testing.T) {
	config := uploadConfig(t)
	config.FreeFloor = ^uint64(0)
	s, _ := start(t, config)
	_, e := client.Put(context.Background(), client.Config{Address: s.Addresses()[0], Name: "backup", Timeout: 1}, strings.NewReader("x"), 1)
	var peer *xfer.PeerError
	if !errors.As(e, &peer) || peer.Code != 3 {
		t.Fatal("accepted below floor", e)
	}
}
func TestUploadInterop(t *testing.T) {
	for _, single := range []bool{false, true} {
		t.Run(map[bool]string{false: "standard", true: "single"}[single], func(t *testing.T) {
			t.Parallel()
			config := uploadConfig(t)
			config.SinglePort = single
			s, _ := start(t, config)
			body := "password 0 SECRET\n"
			source := filepath.Join(t.TempDir(), "config")
			os.WriteFile(source, []byte(body), 0600)
			a := netip.MustParseAddrPort(s.Addresses()[0])
			host, port := a.Addr().String(), a.Port()
			clients := 0
			for _, tool := range []string{"curl", "tftp", "atftp"} {
				path, e := exec.LookPath(tool)
				if e != nil {
					continue
				}
				var args []string
				switch tool {
				case "curl":
					args = []string{"--noproxy", "*", "--silent", "--show-error", "--max-time", "10", "-T", source, "tftp://" + a.String() + "/router.cfg"}
				case "tftp":
					args = []string{host, fmt.Sprint(port), "-m", "binary", "-c", "put", source, "router.cfg"}
				case "atftp":
					args = []string{"--put", "--remote-file", "router.cfg", "--local-file", source, host, fmt.Sprint(port)}
				}
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				out, e := exec.CommandContext(ctx, path, args...).CombinedOutput()
				cancel()
				if e != nil {
					t.Fatalf("%s: %v %s", tool, e, out)
				}
				clients++
			}
			s.Stop()
			s.Wait()
			count := 0
			if e := store.Rescan(config.Upload, func(r store.Receipt) error {
				count++
				b, e := os.ReadFile(filepath.Join(config.Upload, filepath.FromSlash(r.StoredAs)))
				if e != nil || string(b) != body {
					return errors.New("interop payload mismatch")
				}
				return nil
			}); e != nil || count != clients {
				t.Fatalf("interop uploads %d/%d: %v", count, clients, e)
			}
		})
	}
}

func TestUploadNoSecretMetadata(t *testing.T) {
	config := uploadConfig(t)
	s, _ := start(t, config)
	if _, e := client.Put(context.Background(), client.Config{Address: s.Addresses()[0], Name: "router.cfg", Timeout: 1}, strings.NewReader("password 0 SENTINEL-SECRET\n"), 27); e != nil {
		t.Fatal(e)
	}
	s.Stop()
	s.Wait()
	fs.WalkDir(os.DirFS(config.Upload), ".", func(name string, entry fs.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if strings.HasSuffix(name, ".json") {
			b, _ := os.ReadFile(filepath.Join(config.Upload, filepath.FromSlash(name)))
			var value any
			if json.Unmarshal(b, &value) != nil {
				t.Error("invalid metadata")
			}
			if bytes.Contains(b, []byte("SENTINEL-SECRET")) {
				t.Error("secret escaped into metadata")
			}
		}
		return nil
	})
}
