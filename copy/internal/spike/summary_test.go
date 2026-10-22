package spike

import (
	"encoding/base64"
	"os"
	"strings"
	"testing"
)

func TestFixtureSummaryIgnoresLabels(t *testing.T) {
	for _, labels := range [][]string{{"Dirs", "Files", "Bytes"}, {"Verzeichnisse", "Dateien", "Bytes"}, {"ディレクトリ", "ファイル", "バイト"}} {
		text := " Total Copied Skipped Mismatch FAILED Extras\r\n" +
			labels[0] + " : 2 2 0 0 0 0\r\n" +
			labels[1] + " : 1 1 0 0 0 0\r\n" +
			labels[2] + " : 81920 81920 0 0 0 0\r\n"
		got, err := fixtureSummary(text)
		want := fixtureCounts{{2, 2, 0, 0, 0, 0}, {1, 1, 0, 0, 0, 0}, {81920, 81920, 0, 0, 0, 0}}
		if err != nil || got != want {
			t.Fatalf("labels %v: got %v, %v", labels, got, err)
		}
	}
}

func TestFixtureSummaryRejectsAmbiguityAndNonByteUnits(t *testing.T) {
	for _, text := range []string{
		"Files : 1 1 0 0 0 0",
		strings.Repeat("fake : 1 1 0 0 0 0\n", 4),
		"Dirs : 1 1 0 0 0 0\nFiles : 1 1 0 0 0 0\nBytes : 80.0 m 80.0 m 0 0 0 0\n",
		"Dirs : 1 1 0 0 0 0\nFiles : 1 1 0 0 0 0\nBytes : 18446744073709551616 1 0 0 0 0\n",
	} {
		if _, err := fixtureSummary(text); err == nil {
			t.Fatalf("accepted ambiguous or invalid summary %q", text)
		}
	}
}

func TestCapturedNativeSummary(t *testing.T) {
	encoded, err := os.ReadFile("testdata/windows-2025-en/native-log.base64")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(encoded)))
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeUnicode(raw)
	if err != nil {
		t.Fatal(err)
	}
	got, err := fixtureSummary(decoded)
	if err != nil || got[1][1] != 1 || got[2][1] != 81920 {
		t.Fatalf("real capture summary: %v, %v", got, err)
	}
}
