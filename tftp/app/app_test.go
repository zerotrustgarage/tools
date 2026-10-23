package main

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/zerotrustgarage/tools/tftp/internal/session"
)

type dialogStub struct{ saveDownloadCalls int }

func (dialogStub) Directory() (string, error) { return "", nil }
func (dialogStub) OpenFile() (string, error)  { return "open.bin", nil }
func (dialogStub) SaveFile(string) (string, error) {
	return "audit.jsonl", nil
}
func (p *dialogStub) SaveDownload() (string, error) {
	p.saveDownloadCalls++
	return "download.bin", nil
}

func TestWindowShutdownAndExport(t *testing.T) {
	a := NewApp()
	ctx, cancel := context.WithCancel(context.Background())
	a.ctx = ctx
	a.cancel()
	a.cancel = cancel
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

func TestTransferRejectsInvalidRequestsBeforeConnecting(t *testing.T) {
	a := NewApp()
	base := TransferRequest{Op: "get", Host: "127.0.0.1", Port: 69, Remote: "boot.bin", Local: "boot.bin"}
	cases := []struct {
		name string
		edit func(*TransferRequest)
	}{
		{"unsupported operation", func(r *TransferRequest) { r.Op = "delete" }},
		{"empty host", func(r *TransferRequest) { r.Host = "" }},
		{"padded host", func(r *TransferRequest) { r.Host = " 127.0.0.1" }},
		{"empty remote name", func(r *TransferRequest) { r.Remote = "" }},
		{"empty local path", func(r *TransferRequest) { r.Local = "" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := base
			tc.edit(&r)
			if err := a.Transfer(r); err == nil {
				t.Fatal("Transfer unexpectedly accepted invalid request")
			}
		})
	}
}

func TestBrowseSaveModeUsesDownloadSaveDialog(t *testing.T) {
	p := &dialogStub{}
	a := NewApp()
	a.platform = p
	path, err := a.Browse("save")
	if err != nil || path != "download.bin" {
		t.Fatalf("Browse(save) = %q, %v", path, err)
	}
	if p.saveDownloadCalls != 1 {
		t.Fatalf("SaveDownload calls = %d, want 1", p.saveDownloadCalls)
	}
}

func TestShutdownCancelsClientAndWaitsForTemporaryFileCleanup(t *testing.T) {
	server, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	if err := server.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}

	a := NewApp()
	localDir := t.TempDir()
	result := make(chan error, 1)
	go func() {
		result <- a.Transfer(TransferRequest{
			Op: "get", Host: "127.0.0.1", Port: server.LocalAddr().(*net.UDPAddr).Port,
			Remote: "boot.bin", Local: filepath.Join(localDir, "boot.bin"),
		})
	}()

	buf := make([]byte, 2048)
	if _, _, err := server.ReadFromUDP(buf); err != nil {
		a.cancel()
		t.Fatalf("client did not send its request: %v", err)
	}
	shutdown := make(chan struct{})
	go func() {
		a.shutdown()
		close(shutdown)
	}()

	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("transfer error = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("client transfer did not stop after window shutdown")
	}
	select {
	case <-shutdown:
	case <-time.After(time.Second):
		t.Fatal("shutdown did not wait for client cleanup")
	}
	entries, err := os.ReadDir(localDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("temporary download remained after shutdown: %v", entries)
	}
	if err := a.Transfer(TransferRequest{Op: "get", Host: "127.0.0.1", Port: 69, Remote: "boot.bin", Local: "boot.bin"}); err == nil {
		t.Fatal("shutdown accepted a new client transfer")
	}
	if err := a.Start(session.Request{}); err == nil {
		t.Fatal("shutdown accepted a new server session")
	}
}
