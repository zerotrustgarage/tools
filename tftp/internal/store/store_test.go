package store

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestName(t *testing.T) {
	for _, s := range []string{"", ".", "..", "a/../b", "a/./b", "//host/a", `\\host\a`, "a//b", "C:foo", "file:stream", "a\x00b", "a\nb", "CON.txt", "a/NUL", "LPT1.dat", "COM9", "COM¹.txt", "conin$", "a.", "a ", strings.Repeat("a", 256), strings.Repeat("a/", 512) + "a"} {
		if n, e := Name(s); e == nil {
			t.Fatalf("accepted %q as %q", s, n)
		}
	}
	for s, want := range map[string]string{"/pxelinux.0": "pxelinux.0", `boot\image.bin`: "boot/image.bin", "COM10.txt": "COM10.txt", "valid name": "valid name"} {
		if n, e := Name(s); e != nil || n != want {
			t.Fatalf("%q: %q %v", s, n, e)
		}
	}
}
func TestContainment(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "root")
	if e := os.Mkdir(root, 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(base, "secret"), []byte("outside"), 0600); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(root, "Image.bin"), []byte("inside"), 0600); e != nil {
		t.Fatal(e)
	}
	s, e := Open(root, true)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	f, e := s.Read("/image.BIN")
	if e != nil {
		t.Fatal(e)
	}
	b, _ := io.ReadAll(f)
	f.Close()
	if string(b) != "inside" {
		t.Fatal(string(b))
	}
	if e := os.Symlink("../secret", filepath.Join(root, "escape")); e != nil {
		t.Fatal(e)
	}
	if e := os.Symlink("..", filepath.Join(root, "up")); e != nil {
		t.Fatal(e)
	}
	for _, n := range []string{"escape", "up/secret", "../secret", "/", "."} {
		if f, e := s.Read(n); e == nil {
			f.Close()
			t.Fatalf("escaped via %s", n)
		}
	}
	if e := os.WriteFile(filepath.Join(root, "image.bin"), nil, 0600); e != nil {
		t.Fatal(e)
	}
	if f, e := s.Read("IMAGE.BIN"); e == nil {
		f.Close()
		t.Fatal("ambiguous case accepted")
	}
}
func FuzzName(f *testing.F) {
	base := f.TempDir()
	root := filepath.Join(base, "root")
	os.Mkdir(root, 0700)
	os.WriteFile(filepath.Join(base, "secret"), []byte("outside"), 0600)
	os.WriteFile(filepath.Join(root, "inside"), []byte("inside"), 0600)
	os.Symlink("../secret", filepath.Join(root, "escape"))
	s, e := Open(root, false)
	if e != nil {
		f.Fatal(e)
	}
	defer s.Close()
	for _, n := range []string{"inside", "escape", "../secret", `..\secret`, "/inside", "CON", "a:stream", ""} {
		f.Add(n)
	}
	f.Fuzz(func(t *testing.T, n string) {
		name, e := Name(n)
		if e != nil {
			return
		}
		if !filepath.IsLocal(name) {
			t.Fatalf("nonlocal %q", name)
		}
		file, e := s.Read(n)
		if e != nil {
			return
		}
		defer file.Close()
		b, e := io.ReadAll(file)
		if e != nil || string(b) != "inside" {
			t.Fatalf("containment failed: %q %q %v", n, b, e)
		}
	})
}
