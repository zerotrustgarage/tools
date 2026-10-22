package report

import (
	"bytes"
	"encoding/csv"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zerotrustgarage/tools/scanner/internal/scan"
)

func TestReadAndAnnotatedCSV(t *testing.T) {
	d := Document{Tool: "ztg-scanner-desktop", Site: "Clinic", Hosts: []scan.Host{{IP: "10.0.0.1", Status: "up", Type: "=type()", Annotation: scan.Annotation{Notes: "=notes()", Tags: []string{"owner:Alice", "cui:inside"}, Favorite: true}}}}
	var b bytes.Buffer
	if err := JSON(&b, d); err != nil {
		t.Fatal(err)
	}
	r, err := Read(&b)
	if err != nil || r.Site != d.Site || !r.Hosts[0].Favorite {
		t.Fatalf("%+v %v", r, err)
	}
	b.Reset()
	if err := CSV(&b, r.Hosts); err != nil {
		t.Fatal(err)
	}
	rows, err := csv.NewReader(&b).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	for i, col := range rows[0] {
		if col == "notes" && rows[1][i] != "'=notes()" {
			t.Fatal(rows)
		}
		if col == "type" && rows[1][i] != "'=type()" {
			t.Fatal(rows)
		}
	}
}

func TestReadRefusesBadFiles(t *testing.T) {
	for _, input := range []string{
		`{"tool":"other","hosts":[]}`, `{"tool":"ztg-scanner","snmp_password":"do not keep"}`,
		`{"tool":"ztg-scanner","hosts":[{"ip":"10.0.0.1 & whoami","status":"up"}]}`,
		`{"tool":"ztg-scanner","hosts":[{"ip":"10.0.0.1","status":"up"},{"ip":"10.0.0.1","status":"up"}]}`,
		`{"tool":"ztg-scanner","hosts":[{"ip":"10.0.0.1","status":"up","services":[{"port":0}]}]}`,
		`{"tool":"ztg-scanner","annotations":{"arbitrary":{"notes":"bad"}}}`,
		`{"tool":"ztg-scanner"} {"second":true}`, `[]`, `{"tool":"ztg-scanner","hosts":[{"ip":"10.0.0.1","status":"other"}]}`,
	} {
		if _, err := Read(strings.NewReader(input)); err == nil {
			t.Errorf("accepted %s", input)
		}
	}
	if _, err := Read(io.LimitReader(zeroReader{}, MaxDocumentBytes+1)); err == nil || !strings.Contains(err.Error(), "64 MiB") {
		t.Fatal(err)
	}
}

type zeroReader struct{}

func (zeroReader) Read(b []byte) (int, error) { clear(b); return len(b), nil }

func TestFailedWritePreservesOriginal(t *testing.T) {
	path := filepath.Join(t.TempDir(), "site.json")
	if err := os.WriteFile(path, []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	err := WriteFile(path, func(w io.Writer) error { _, _ = io.WriteString(w, "partial"); return errors.New("disk failure") })
	b, _ := os.ReadFile(path)
	if err == nil || string(b) != "original" {
		t.Fatalf("%s %v", b, err)
	}
	if err := WriteFile(path, func(w io.Writer) error { _, err := io.WriteString(w, "complete"); return err }); err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile(path)
	if string(b) != "complete" {
		t.Fatal(string(b))
	}
	entries, _ := os.ReadDir(filepath.Dir(path))
	if len(entries) != 1 {
		t.Fatal("temporary file leaked")
	}
	err = WriteFile(path, func(w io.Writer) error { return JSONBounded(w, Document{Tool: "ztg-scanner"}, 10) })
	b, _ = os.ReadFile(path)
	if err == nil || string(b) != "complete" {
		t.Fatal("oversized save replaced the original", err)
	}
}
