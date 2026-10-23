package main

import (
	"context"
	"github.com/zerotrustgarage/tools/tftp/internal/session"
	"os"
	"path/filepath"
	"testing"
)

func TestWindowShutdownAndExport(t *testing.T) {
	a := NewApp()
	ctx, cancel := context.WithCancel(context.Background())
	a.ctx = ctx
	root := filepath.Join(t.TempDir(), "images")
	os.Mkdir(root, 0700)
	if e := a.Start(session.Request{Root: root, Bind: "127.0.0.1", Port: 0, Minutes: 1}); e != nil {
		t.Fatal(e)
	}
	cancel()
	a.Stop()
	if a.Snapshot().Running {
		t.Fatal("window close left server running")
	}
	if _, e := a.ExportAudit(); e == nil {
		t.Fatal("missing dialog accepted")
	}
}
