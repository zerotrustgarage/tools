package store

import (
	"crypto/sha256"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"testing"
)

func TestPromoteVerifiedNoReplace(t *testing.T) {
	base := t.TempDir()
	served := filepath.Join(base, "images")
	os.Mkdir(served, 0700)
	drop := filepath.Join(base, "drop")
	d, e := OpenDrop(drop, served, 1024, 1)
	if e != nil {
		t.Fatal(e)
	}
	defer d.Close()
	u, e := d.Begin(netip.MustParseAddr("127.0.0.1"), "boot.bin", 3)
	if e != nil {
		t.Fatal(e)
	}
	u.Write([]byte("abc"))
	sum := fmt.Sprintf("%x", sha256.Sum256([]byte("abc")))
	r, e := u.Commit(3, sum)
	if e != nil {
		t.Fatal(e)
	}
	u.Close()
	if h, e := Promote(drop, served, r.StoredAs, "copy.bin"); e != nil || h != sum {
		t.Fatal(h, e)
	}
	if _, e = Promote(drop, served, r.StoredAs, "copy.bin"); e == nil {
		t.Fatal("overwrote destination")
	}
	os.WriteFile(filepath.Join(drop, filepath.FromSlash(r.StoredAs)), []byte("bad"), 0600)
	if _, e = Promote(drop, served, r.StoredAs, "bad.bin"); e == nil {
		t.Fatal("promoted changed payload")
	}
	if _, e = os.Stat(filepath.Join(served, "bad.bin")); !os.IsNotExist(e) {
		t.Fatal("published invalid payload")
	}
	for _, n := range []string{"../escape", ".ztg-promote-test/payload"} {
		if _, e = Promote(drop, served, r.StoredAs, n); e == nil {
			t.Fatal(n)
		}
	}
}
