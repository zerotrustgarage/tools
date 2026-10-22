package main

import (
	"bytes"
	"context"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zerotrustgarage/tools/tftp/internal/server"
)

func TestCLICommands(t *testing.T) {
	for _, args := range [][]string{{}, {"--help"}, {"serve", "--help"}, {"get", "--help"}, {"put", "--help"}, {"version"}} {
		var out bytes.Buffer
		if e := run(context.Background(), args, &out, &out); e != nil || out.Len() == 0 {
			t.Fatalf("%v: %v %q", args, e, out.String())
		}
	}
	for _, args := range [][]string{{"invalid"}, {"serve"}, {"get", "localhost"}, {"put"}, {"get", "--for", "0s", "localhost", "x"}} {
		if e := run(context.Background(), args, io.Discard, io.Discard); e == nil {
			t.Fatalf("accepted %v", args)
		}
	}
}
func TestDownloadCommitAndFailure(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "root")
	os.Mkdir(root, 0700)
	os.WriteFile(filepath.Join(root, "image"), []byte("firmware"), 0600)
	s, e := server.Start(context.Background(), server.Config{Root: root, Bind: "127.0.0.1", Duration: time.Minute, Audit: io.Discard})
	if e != nil {
		t.Fatal(e)
	}
	defer func() { s.Stop(); s.Wait() }()
	host, port, _ := net.SplitHostPort(s.Addresses()[0])
	dest := filepath.Join(base, "download")
	args := []string{"get", "--port", port, "--timeout", "1", host, "image", dest}
	if e = run(context.Background(), args, io.Discard, io.Discard); e != nil {
		t.Fatal(e)
	}
	b, e := os.ReadFile(dest)
	if e != nil || string(b) != "firmware" {
		t.Fatalf("%q %v", b, e)
	}
	if e = run(context.Background(), args, io.Discard, io.Discard); e == nil {
		t.Fatal("overwrote destination")
	}
	b, _ = os.ReadFile(dest)
	if string(b) != "firmware" {
		t.Fatal("changed destination")
	}
	missing := filepath.Join(base, "missing")
	args[len(args)-2] = "absent"
	args[len(args)-1] = missing
	if e = run(context.Background(), args, io.Discard, io.Discard); e == nil {
		t.Fatal("missing file succeeded")
	}
	if _, e = os.Stat(missing); !os.IsNotExist(e) {
		t.Fatal("partial destination exists")
	}
	entries, _ := os.ReadDir(base)
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".ztg-tftp-") {
			t.Fatal("temporary download leaked")
		}
	}
}
func TestAuditContainmentAndFiniteSessions(t *testing.T) {
	root := t.TempDir()
	for _, args := range [][]string{{"serve", "--audit-dir", root, root}, {"serve", "--for", "0s", "--audit-dir", filepath.Join(t.TempDir(), "audit"), root}} {
		if e := run(context.Background(), args, io.Discard, io.Discard); e == nil {
			t.Fatalf("accepted %v", args)
		}
	}
}
