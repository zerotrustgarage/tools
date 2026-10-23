package run

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf16"
)

func nativeBytes(text string) []byte {
	data := []byte{0xff, 0xfe}
	for _, unit := range utf16.Encode([]rune(text)) {
		data = binary.LittleEndian.AppendUint16(data, unit)
	}
	return data
}

func TestWholeLogDiagnosticsAndHashDoNotDependOnFooter(t *testing.T) {
	text := "2026/10/23 12:34:56 ERROR 32 (0x00000020) File C:\\source\\locked.txt\n" +
		strings.Repeat("ordinary file announcement\n", 50000) +
		"Dirs : 1 0 1 0 0 0\nFiles : 1 0 0 0 1 0\nBytes : 10 0 0 0 10 0\n"
	data := nativeBytes(text)
	name := filepath.Join(t.TempDir(), "native.log")
	if err := os.WriteFile(name, data, 0600); err != nil {
		t.Fatal(err)
	}
	summary, hash, report, err := readNative(name)
	expected := sha256.Sum256(data)
	if err != nil || hash != hex.EncodeToString(expected[:]) || summary.Files.Failed != 1 {
		t.Fatalf("%+v, %s, %v", summary, hash, err)
	}
	if len(report.Errors) != 1 || report.Errors[0].Code != 32 || report.Errors[0].Count != 1 {
		t.Fatal("lost an error outside the footer", report)
	}
}

func TestDiagnosticsSurviveMissingOrIncompleteSummary(t *testing.T) {
	for _, suffix := range [][]byte{nil, {1}} {
		data := append(nativeBytes("2026/10/23 12:34:56 ERROR 5 (0x00000005) File C:\\protected\n"), suffix...)
		name := filepath.Join(t.TempDir(), "native.log")
		if err := os.WriteFile(name, data, 0600); err != nil {
			t.Fatal(err)
		}
		_, hash, report, err := readNative(name)
		if err == nil || hash == "" || len(report.Errors) != 1 || report.Errors[0].Code != 5 {
			t.Fatalf("%+v, %s, %v", report, hash, err)
		}
	}
}
