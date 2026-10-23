package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBitsCLIExportsUnicodeScriptWithoutRunningOrOverwriting(t *testing.T) {
	dir := t.TempDir()
	jobFile, scriptFile := filepath.Join(dir, "bits.json"), filepath.Join(dir, "bits.ps1")
	input := `{"schema":1,"engine":"bits","transfers":[{"source":"https://example.net/日本語.bin","destination":"C:\\Downloads\\日本語.bin"}],"options":[{"name":"Priority","values":["Normal"]}]}`
	if err := os.WriteFile(jobFile, []byte(input), 0600); err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	if err := run(context.Background(), []string{"bits", "command", jobFile}, &out, &errOut); err != nil || !strings.Contains(out.String(), `BitsTransfer\Start-BitsTransfer`) {
		t.Fatalf("%v: %s", err, out.String())
	}
	if err := run(context.Background(), []string{"bits", "export", "--output", scriptFile, jobFile}, &out, &errOut); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(scriptFile)
	if err != nil || !bytes.HasPrefix(data, []byte{0xef, 0xbb, 0xbf}) || !bytes.Contains(data, []byte("日本語")) {
		t.Fatalf("script encoding: %q, %v", data, err)
	}
	if err := run(context.Background(), []string{"bits", "export", "--output", scriptFile, jobFile}, &out, &errOut); err == nil {
		t.Fatal("existing script replaced")
	}
	if err := run(context.Background(), []string{"bits", "run", jobFile}, &out, &errOut); err == nil {
		t.Fatal("unexpected BITS execution interface")
	}
}
