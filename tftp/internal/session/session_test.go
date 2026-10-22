package session

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/zerotrustgarage/tools/tftp/internal/client"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"
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
