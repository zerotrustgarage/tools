package record

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func clock(start time.Time) func() time.Time {
	t := start
	return func() time.Time { t = t.Add(time.Minute); return t }
}

func TestAppendChainsAndVerifies(t *testing.T) {
	dir := t.TempDir()
	l, err := Open(dir, "srv-01", clock(time.Date(2026, 11, 30, 23, 58, 0, 0, time.UTC)))
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 4; i++ { // crosses into December
		if _, err := l.Append(Entry{Kind: KindChange, DN: "CN=A", Technician: `LAB\tech`}); err != nil {
			t.Fatal(err)
		}
	}
	files, _ := filepath.Glob(filepath.Join(dir, "srv-01", "*.jsonl"))
	if len(files) != 2 {
		t.Fatalf("want two month files, got %v", files)
	}
	if p, err := Verify(dir); err != nil || len(p) != 0 {
		t.Fatalf("verify: %v %v", p, err)
	}
	// Reopen continues the chain.
	l2, err := Open(dir, "srv-01", clock(time.Date(2026, 12, 2, 0, 0, 0, 0, time.UTC)))
	if err != nil {
		t.Fatal(err)
	}
	e, err := l2.Append(Entry{Kind: KindSignOut})
	if err != nil || e.Seq != 5 {
		t.Fatalf("seq %d err %v", e.Seq, err)
	}
	if p, _ := Verify(dir); len(p) != 0 {
		t.Fatalf("verify after reopen: %v", p)
	}
}

func TestVerifyDetectsTamperingAndDeletion(t *testing.T) {
	dir := t.TempDir()
	l, _ := Open(dir, "ws", clock(time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)))
	for i := 0; i < 3; i++ {
		l.Append(Entry{Kind: KindChange, DN: "CN=A", After: []string{"x"}})
	}
	path := filepath.Join(dir, "ws", "2026-11.jsonl")
	raw, _ := os.ReadFile(path)
	os.WriteFile(path, []byte(strings.Replace(string(raw), `"after":["x"]`, `"after":["y"]`, 1)), 0o640)
	p, _ := Verify(dir)
	if len(p) == 0 || !strings.Contains(p[0].Reason, "hash mismatch") {
		t.Fatalf("tampering not found: %v", p)
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	os.WriteFile(path, []byte(lines[0]+"\n"+lines[2]+"\n"), 0o640)
	p, _ = Verify(dir)
	if len(p) == 0 || !strings.Contains(p[0].Reason, "chain is broken") {
		t.Fatalf("deletion not found: %v", p)
	}
}

func TestReadMergesWritersAndFilters(t *testing.T) {
	dir := t.TempDir()
	a, _ := Open(dir, "server", clock(time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)))
	b, _ := Open(dir, "laptop/../x", clock(time.Date(2026, 11, 1, 0, 0, 30, 0, time.UTC)))
	a.Append(Entry{Kind: KindChange, DN: "CN=Alex", Technician: `LAB\ann`, Ticket: "T-1"})
	b.Append(Entry{Kind: KindSignIn, Technician: `LAB\bob`})
	a.Append(Entry{Kind: KindChange, DN: "CN=Sam", Technician: `LAB\ann`})
	all, err := Read(dir, Filter{})
	if err != nil || len(all) != 3 || all[0].DN != "CN=Alex" || all[1].Kind != KindSignIn {
		t.Fatalf("merge order: %+v %v", all, err)
	}
	if b.Writer() != "laptop_.._x" {
		t.Fatalf("writer name not sanitized: %q", b.Writer())
	}
	got, _ := Read(dir, Filter{Text: "t-1"})
	if len(got) != 1 {
		t.Fatalf("text filter: %+v", got)
	}
	got, _ = Read(dir, Filter{Account: "bob", Kind: KindSignIn})
	if len(got) != 1 {
		t.Fatalf("account filter: %+v", got)
	}
	got, _ = Read(dir, Filter{Limit: 1})
	if len(got) != 1 || got[0].DN != "CN=Sam" {
		t.Fatalf("limit keeps newest: %+v", got)
	}
}
