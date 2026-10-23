package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

func TestWindowsPromotionStagingAliases(t *testing.T) {
	root := t.TempDir()
	stage := filepath.Join(root, ".ztg-promote-private")
	if e := os.Mkdir(stage, 0700); e != nil {
		t.Fatal(e)
	}
	name := filepath.Join(stage, "payload")
	if e := os.WriteFile(name, []byte("private"), 0600); e != nil {
		t.Fatal(e)
	}
	f, e := os.Open(name)
	if e != nil {
		t.Fatal(e)
	}
	if e = checkReadable(f); e == nil {
		f.Close()
		t.Fatal("normalized staging path accepted")
	}
	f.Close()
	b := make([]uint16, 32768)
	p, _ := windows.UTF16PtrFromString(stage)
	n, e := windows.GetShortPathName(p, &b[0], uint32(len(b)))
	if e != nil {
		t.Fatal(e)
	}
	short := filepath.Base(windows.UTF16ToString(b[:n]))
	if strings.EqualFold(short, filepath.Base(stage)) {
		t.Log("8.3 aliases disabled on this volume")
		return
	}
	s, e := Open(root, false)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	if f, e := s.Read(short + "/payload"); e == nil {
		f.Close()
		t.Fatal("staging payload exposed through NTFS short alias")
	}
}
