package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zerotrustgarage/tools/copy/internal/bits"
)

type bitsTestPlatform struct {
	fakePlatform
	save string
}

func (p bitsTestPlatform) SaveFile(string) (string, error) { return p.save, nil }

func TestBitsNativeImportExportAndCredentialRefusal(t *testing.T) {
	a := testApp(t)
	j := bits.Default()
	j.Transfers = []bits.Transfer{{Source: "https://example.net/file.bin", Destination: `C:\Downloads\日本語.bin`}}
	dir := t.TempDir()
	script := filepath.Join(dir, "transfer.ps1")
	a.setPlatform(context.Background(), bitsTestPlatform{save: script})
	if _, err := a.ExportBitsScript(j); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(script)
	if err != nil || !strings.HasPrefix(string(data), "\xef\xbb\xbfImport-Module") || !strings.Contains(string(data), "日本語") {
		t.Fatalf("invalid script: %v, %q", err, data)
	}
	if _, err := a.ExportBitsScript(j); err == nil {
		t.Fatal("script was overwritten")
	}
	jobFile := filepath.Join(dir, "bits.json")
	a.setPlatform(context.Background(), bitsTestPlatform{fakePlatform: fakePlatform{open: jobFile}, save: jobFile})
	if _, err := a.ExportBitsJob(j); err != nil {
		t.Fatal(err)
	}
	view, err := a.ImportBitsJob()
	if err != nil || !view.Valid || view.Job.Transfers[0].Destination != j.Transfers[0].Destination {
		t.Fatalf("%+v, %v", view, err)
	}
	j.Options = append(j.Options, bits.Option{Name: "Credential", Values: []string{"secret"}})
	invalid := filepath.Join(dir, "invalid.json")
	a.setPlatform(context.Background(), bitsTestPlatform{save: invalid})
	if _, err := a.ExportBitsJob(j); err == nil {
		t.Fatal("credential value exported")
	}
	if _, err := os.Stat(invalid); !os.IsNotExist(err) {
		t.Fatal("invalid export created a file")
	}
	document, _ := json.Marshal(map[string]any{"schema": 1, "engine": "bits", "password": "secret"})
	if err := os.WriteFile(jobFile, document, 0600); err != nil {
		t.Fatal(err)
	}
	a.setPlatform(context.Background(), bitsTestPlatform{fakePlatform: fakePlatform{open: jobFile}})
	if _, err := a.ImportBitsJob(); err == nil {
		t.Fatal("password field imported")
	}
}
