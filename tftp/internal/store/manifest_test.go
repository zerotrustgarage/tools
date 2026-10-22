package store

import (
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestApprovedSnapshot(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "boot"), []byte("good"), 0600)
	s, e := Open(dir, false)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	hash := fmt.Sprintf("%x", sha256.Sum256([]byte("good")))
	if e = s.Approve(map[string]string{"boot": hash}); e != nil {
		t.Fatal(e)
	}
	f, e := s.Read("boot")
	if e != nil {
		t.Fatal(e)
	}
	f.Close()
	os.WriteFile(filepath.Join(dir, "boot"), []byte("evil"), 0600)
	f, e = s.Read("boot")
	if e != nil {
		t.Fatal(e)
	}
	b, _ := io.ReadAll(f)
	f.Close()
	if string(b) != "good" {
		t.Fatal("snapshot changed")
	}
	if _, e = s.Read("other"); e == nil {
		t.Fatal("unlisted image served")
	}
}
func TestApprovalMismatch(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "boot"), []byte("evil"), 0600)
	s, _ := Open(dir, false)
	defer s.Close()
	s.Approve(map[string]string{"boot": fmt.Sprintf("%x", sha256.Sum256([]byte("good")))})
	if _, e := s.Read("boot"); e == nil {
		t.Fatal("hash mismatch served")
	}
}
