package session

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zerotrustgarage/tools/tftp/internal/client"
)

func TestLifecycleAndExport(t *testing.T) {
	root := filepath.Join(t.TempDir(), "images")
	os.Mkdir(root, 0700)
	os.WriteFile(filepath.Join(root, "boot"), []byte("abc"), 0600)
	s := New()
	if e := s.Start(context.Background(), Request{Root: root, Bind: "127.0.0.1", Port: 0, Minutes: 1}); e != nil {
		t.Fatal(e)
	}
	defer s.Stop()
	before := s.Snapshot().Deadline
	if e := s.Extend(15); e != nil {
		t.Fatal(e)
	}
	if s.Snapshot().Deadline.Sub(before) != 15*time.Minute {
		t.Fatal("extension")
	}
	var got bytes.Buffer
	_, e := client.Get(context.Background(), client.Config{Address: s.Snapshot().Addresses[0], Name: "boot", Timeout: 1}, &got)
	if e != nil {
		t.Fatal(e)
	}
	s.Stop()
	v := s.Snapshot()
	if v.Running || len(v.Records) == 0 || len(v.Active) != 0 {
		t.Fatalf("snapshot %+v", v)
	}
	var out bytes.Buffer
	if e = s.Export(&out); e != nil {
		t.Fatal(e)
	}
	if !bytes.Contains(out.Bytes(), []byte(`"result":"complete"`)) {
		t.Fatal(out.String())
	}
	dec := json.NewDecoder(bytes.NewReader(out.Bytes()))
	for {
		var rec map[string]any
		e := dec.Decode(&rec)
		if e == io.EOF {
			break
		}
		if e != nil {
			t.Fatal("export is not complete JSONL", e)
		}
	}
	if e = s.Extend(1); e == nil {
		t.Fatal("extended stopped session")
	}
	if e = s.Start(context.Background(), Request{Root: root, Minutes: 0}); e == nil {
		t.Fatal("unlimited interactive session")
	}
}

func TestPromoteCannotPublishAfterAuditClosed(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "images")
	upload := filepath.Join(base, "collected")
	if e := os.Mkdir(root, 0700); e != nil {
		t.Fatal(e)
	}
	s := New()
	if e := s.Start(context.Background(), Request{Root: root, Upload: upload, Bind: "127.0.0.1", Port: 0, Minutes: 1}); e != nil {
		t.Fatal(e)
	}
	addr := s.Snapshot().Addresses[0]
	if _, e := client.Put(context.Background(), client.Config{Address: addr, Name: "router.cfg", Timeout: 1}, strings.NewReader("hostname edge\n"), 14); e != nil {
		s.Stop()
		t.Fatal(e)
	}
	collected, e := s.Collected()
	if e != nil || len(collected) != 1 {
		s.Stop()
		t.Fatalf("collected uploads: %v, %v", collected, e)
	}
	s.Stop()

	// This is the short state between the wait goroutine closing the audit and
	// publishing Running=false. Promotion must treat the closed writer as final.
	s.mu.Lock()
	s.snapshot.Running = true
	s.mu.Unlock()
	if e = s.Promote(collected[0].StoredAs, "router.cfg"); e == nil {
		t.Fatal("promoted after the session audit had closed")
	}
	if _, e = os.Stat(filepath.Join(root, "router.cfg")); !os.IsNotExist(e) {
		t.Fatalf("promotion was published without an audit record: stat error %v", e)
	}
}
