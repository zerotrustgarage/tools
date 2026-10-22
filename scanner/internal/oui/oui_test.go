package oui

import (
	"bytes"
	"compress/gzip"
	"testing"
)

func testDB(t *testing.T) *DB {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	zw.Write([]byte("# comment\n001B21\tIntel Corporate\n70B3D5\tIEEE Registration Authority\n70B3D5123\tSmall Block Co\nAABBCC1\tMedium Block Co\n"))
	zw.Close()
	db, err := Load(&buf)
	if err != nil {
		t.Fatal(err)
	}
	return db
}

func TestLongestPrefixWins(t *testing.T) {
	db := testDB(t)
	cases := map[string]string{
		"00:1b:21:aa:bb:cc": "Intel Corporate",
		"70-B3-D5-12-34-56": "Small Block Co",              // MA-S beats the MA-L umbrella
		"70b3.d5ff.0000":    "IEEE Registration Authority", // falls back to MA-L
		"a8:bb:cc:1f:00:01": "",                            // globally assigned but not in the list
	}
	for mac, want := range cases {
		got, err := db.Lookup(mac)
		if err != nil {
			t.Fatalf("%s: %v", mac, err)
		}
		if got.Vendor != want {
			t.Errorf("%s: vendor %q, want %q", mac, got.Vendor, want)
		}
	}
}

func TestMediumBlock(t *testing.T) {
	db := testDB(t)
	// 0xAA has the locally administered bit set, so even a matching prefix
	// must not be reported: such addresses have no IEEE assignee.
	got, _ := db.Lookup("aa:bb:cc:1d:ee:ff")
	if got.Vendor != "" || !got.Randomized {
		t.Fatalf("got %+v", got)
	}
}

func TestRandomizedAndMulticast(t *testing.T) {
	db := testDB(t)
	got, _ := db.Lookup("da:a1:19:00:00:01")
	if !got.Randomized || got.Vendor != "" {
		t.Fatalf("randomized: %+v", got)
	}
	got, _ = db.Lookup("01:00:5e:00:00:fb")
	if !got.Multicast {
		t.Fatalf("multicast: %+v", got)
	}
}

func TestRejectsBadMAC(t *testing.T) {
	if _, err := testDB(t).Lookup("not-a-mac"); err == nil {
		t.Fatal("want error")
	}
}

func TestEmbeddedDatabase(t *testing.T) {
	db, err := Default()
	if err != nil {
		t.Fatal(err)
	}
	if db.Len() < 40000 {
		t.Fatalf("embedded database has only %d prefixes", db.Len())
	}
	got, _ := db.Lookup("00:1b:21:00:00:01")
	if got.Vendor != "Intel Corporate" {
		t.Fatalf("00:1b:21 -> %q", got.Vendor)
	}
}
