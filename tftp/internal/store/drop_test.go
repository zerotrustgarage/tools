package store

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io/fs"
	"net/netip"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

func newDrop(t *testing.T, maxBytes int64) (*Drop, string, string) {
	t.Helper()
	base := t.TempDir()
	served := filepath.Join(base, "served")
	collected := filepath.Join(base, "collected")
	if e := os.Mkdir(served, 0700); e != nil {
		t.Fatal(e)
	}
	d, e := OpenDrop(collected, served, maxBytes, 1)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { d.Close() })
	return d, served, collected
}
func commit(t *testing.T, d *Drop, name, body string) Receipt {
	t.Helper()
	u, e := d.Begin(netip.MustParseAddr("10.20.0.31"), name, int64(len(body)))
	if e != nil {
		t.Fatal(e)
	}
	defer u.Close()
	if _, e = u.Write([]byte(body)); e != nil {
		t.Fatal(e)
	}
	hash := sha256.Sum256([]byte(body))
	r, e := u.Commit(int64(len(body)), hex.EncodeToString(hash[:]))
	if e != nil {
		t.Fatal(e)
	}
	return r
}
func TestDropCommitHistoryAndRescan(t *testing.T) {
	d, served, collected := newDrop(t, 4096)
	body := "password 0 KEEP-SECRET\n"
	first := commit(t, d, "configs/router.cfg", body)
	if first.Changed != nil || first.ChangedSince != "" || len(first.Scan.Findings) != 1 {
		t.Fatalf("first: %+v", first)
	}
	second := commit(t, d, "configs/router.cfg", body)
	if second.StoredAs == first.StoredAs || second.Changed == nil || *second.Changed || second.ChangedSince != first.StoredAs {
		t.Fatalf("second: %+v", second)
	}
	if e := d.Close(); e != nil {
		t.Fatal(e)
	}
	var e error
	d, e = OpenDrop(collected, served, 4096, 1)
	if e != nil {
		t.Fatal(e)
	}
	defer d.Close()
	third := commit(t, d, "configs/router.cfg", body+"ip http server\n")
	if third.Changed == nil || !*third.Changed || third.ChangedSince != second.StoredAs {
		t.Fatalf("restart: %+v", third)
	}
	for _, r := range []Receipt{first, second, third} {
		b, e := os.ReadFile(filepath.Join(collected, filepath.FromSlash(r.StoredAs)))
		if e != nil || !strings.HasPrefix(string(b), body) {
			t.Fatalf("saved: %q %v", b, e)
		}
		if runtime.GOOS != "windows" {
			info, _ := os.Stat(filepath.Join(collected, filepath.FromSlash(r.StoredAs)))
			if info.Mode().Perm() != 0600 {
				t.Fatal("public upload permissions")
			}
		}
	}
	count := 0
	if e = Rescan(collected, func(r Receipt) error {
		count++
		if len(r.Scan.Findings) == 0 {
			t.Fatal("no findings")
		}
		return nil
	}); e != nil || count != 3 {
		t.Fatalf("rescan: %d %v", count, e)
	}
	fs.WalkDir(os.DirFS(collected), ".", func(name string, entry fs.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if strings.HasSuffix(name, ".json") {
			b, _ := os.ReadFile(filepath.Join(collected, filepath.FromSlash(name)))
			if strings.Contains(string(b), "KEEP-SECRET") {
				t.Error("secret in metadata")
			}
		}
		return nil
	})
}
func TestDropLimitsAndCleanup(t *testing.T) {
	d, _, _ := newDrop(t, 16)
	source := netip.MustParseAddr("::1")
	if _, e := d.Begin(source, "config", 17); !errors.Is(e, ErrTooLarge) {
		t.Fatal("declared cap", e)
	}
	u, e := d.Begin(source, "config", -1)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = u.Write([]byte(strings.Repeat("x", 17))); !errors.Is(e, ErrTooLarge) {
		t.Fatal("stream cap", e)
	}
	u.Close()
	u, e = d.Begin(source, "config", 8)
	if e != nil {
		t.Fatal(e)
	}
	u.Write([]byte("short"))
	if _, e = u.Commit(5, "hash"); e == nil {
		t.Fatal("size mismatch committed")
	}
	u.Close()
	entries, e := d.root.Open(".incoming")
	if e != nil {
		t.Fatal(e)
	}
	list, e := entries.ReadDir(-1)
	entries.Close()
	if e != nil || len(list) != 0 {
		t.Fatal("abandoned upload", e, len(list))
	}
	d.space = func(*os.File) (uint64, error) { return 16, nil }
	d.FreeFloor = 8
	if _, e = d.Begin(source, "config", 9); !errors.Is(e, ErrSpace) {
		t.Fatal("free space floor", e)
	}
	u, e = d.Begin(source, "config", 8)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = d.Begin(source, "second", 1); !errors.Is(e, ErrSpace) {
		t.Fatal("concurrent reservation", e)
	}
	d.space = func(*os.File) (uint64, error) { return 8, nil }
	if _, e = u.Write([]byte("x")); !errors.Is(e, ErrSpace) {
		t.Fatal("stream free space", e)
	}
	u.Close()
	if d.reserved != 0 {
		t.Fatal("reservation leaked")
	}
}
func TestDropSeparationAndLock(t *testing.T) {
	d, served, collected := newDrop(t, 4096)
	if other, e := OpenDrop(collected, served, 4096, 1); e == nil {
		other.Close()
		t.Fatal("second writer acquired the drop box")
	}
	for _, pair := range [][2]string{{served, served}, {served, filepath.Join(served, "child")}, {collected, filepath.Dir(collected)}} {
		os.MkdirAll(pair[1], 0700)
		if e := Separate(pair[0], pair[1]); e == nil {
			t.Fatal("overlap", pair)
		}
	}
	for _, name := range []string{"../escape", `..\escape`, "CON.txt", "a:stream"} {
		if _, e := d.Begin(netip.MustParseAddr("127.0.0.1"), name, 0); e == nil {
			t.Fatal("accepted", name)
		}
	}
}
func TestDropConcurrentNames(t *testing.T) {
	d, _, _ := newDrop(t, 4096)
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() { defer wg.Done(); commit(t, d, "same.cfg", "password 0 SECRET\n") }()
	}
	wg.Wait()
	seen := map[string]bool{}
	if e := Rescan(d.root.Name(), func(r Receipt) error {
		if seen[r.StoredAs] {
			t.Error("upload collision")
		}
		seen[r.StoredAs] = true
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	if len(seen) != 8 {
		t.Fatal("lost upload", len(seen))
	}
}

func TestDropRecovery(t *testing.T) {
	d, served, collected := newDrop(t, 4096)
	first := commit(t, d, "router.cfg", "password 0 SECRET\n")
	if e := d.root.RemoveAll(".index"); e != nil {
		t.Fatal(e)
	}
	if e := d.root.Mkdir(".incoming/abandoned", 0700); e != nil {
		t.Fatal(e)
	}
	if e := d.root.WriteFile(".incoming/abandoned/partial", []byte("SECRET"), 0600); e != nil {
		t.Fatal(e)
	}
	d.Close()
	reopened, e := OpenDrop(collected, served, 4096, 1)
	if e != nil {
		t.Fatal(e)
	}
	defer reopened.Close()
	second := commit(t, reopened, "router.cfg", "password 0 SECRET\n")
	if second.ChangedSince != first.StoredAs || second.Changed == nil || *second.Changed {
		t.Fatalf("lost recovered history: %+v", second)
	}
	if _, e = reopened.root.Stat(".incoming/abandoned"); !errors.Is(e, os.ErrNotExist) {
		t.Fatal("abandoned staging data not removed", e)
	}
}
