package parse

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
	"testing/iotest"
)

func TestDiagnosticsUseCodesAcrossLanguagesAndReadBoundaries(t *testing.T) {
	log := "2026/10/23 12:34:56 ERROR 32 (0x00000020) Copying File C:\\source\\日本語-😀.txt\r\n" +
		"2026/10/23 12:34:57 FEHLER 5 (0x00000005) Datei C:\\source\\restricted.txt\r\n" +
		"2026/10/23 12:34:58 ERREUR 32 (0x00000020) Fichier C:\\source\\日本語-😀.txt\r\n" +
		"2026/10/23 12:34:59 エラー 9001 (0x00002329) Unknown error\r\n" +
		"New File 32 (0x00000020) is only a filename\r\n" +
		"2026/10/23 12:35:00 ERROR 5 (0x00000020) mismatched codes\r\n"
	report, err := NativeDiagnostics(iotest.OneByteReader(bytes.NewReader(encoded(log))))
	if err != nil || len(report.Errors) != 3 || report.SkippedLines != 1 {
		t.Fatalf("%+v, %v", report, err)
	}
	if report.Errors[0].Code != 5 || report.Errors[1].Code != 32 || report.Errors[1].Count != 2 || report.Errors[2].Code != 9001 {
		t.Fatal(report)
	}
	if report.Errors[1].Cause != "File in use" || !strings.Contains(report.Errors[1].Samples[0], "日本語-😀") || report.Errors[2].Suggestion == "" {
		t.Fatal(report)
	}
}

func TestDiagnosticsBoundMemoryAndKeepLaterErrors(t *testing.T) {
	var text strings.Builder
	text.WriteString(strings.Repeat("x", diagnosticLineBytes))
	text.WriteByte('\n')
	for n := 1; n <= diagnosticGroups+3; n++ {
		for attempt := 0; attempt < diagnosticSamples+2; attempt++ {
			fmt.Fprintf(&text, "2026/10/23 12:34:56 ERROR %d (0x%08X) %d %s\n", n, n, attempt, strings.Repeat("😀", 600))
		}
	}
	report, err := NativeDiagnostics(bytes.NewReader(encoded(text.String())))
	if err != nil || len(report.Errors) != diagnosticGroups || report.SkippedLines != 1 || report.OmittedOccurrences != 15 {
		t.Fatalf("%+v, %v", report, err)
	}
	for _, group := range report.Errors {
		if group.Count != 5 || len(group.Samples) != diagnosticSamples {
			t.Fatal(group)
		}
		for _, sample := range group.Samples {
			if len([]rune(sample)) > 513 || !strings.HasSuffix(sample, "…") {
				t.Fatal("unbounded sample")
			}
		}
	}
}

func TestMalformedDiagnosticLinesDoNotHideLaterErrors(t *testing.T) {
	data := append(encoded(""), 0, 0xd8, 10, 0)
	data = append(data, encoded("2026/10/23 12:34:56 ERROR 112 (0x00000070) Destination\n")[2:]...)
	data = append(data, 1)
	report, err := NativeDiagnostics(bytes.NewReader(data))
	if err != nil || report.SkippedLines != 2 || len(report.Errors) != 1 || report.Errors[0].Code != 112 {
		t.Fatalf("%+v, %v", report, err)
	}
	if _, err := NativeDiagnostics(strings.NewReader("not UTF-16")); err == nil {
		t.Fatal("accepted missing BOM")
	}
}

func FuzzNativeDiagnostics(f *testing.F) {
	f.Add(encoded("2026/10/23 12:34:56 ERROR 32 (0x00000020) File C:\\data\n"))
	f.Add([]byte{0xff, 0xfe, 0, 0xd8})
	f.Fuzz(func(t *testing.T, data []byte) {
		report, _ := NativeDiagnostics(bytes.NewReader(data))
		if len(report.Errors) > diagnosticGroups {
			t.Fatal("unbounded groups")
		}
		for _, group := range report.Errors {
			if len(group.Samples) > diagnosticSamples {
				t.Fatal("unbounded samples")
			}
		}
	})
}
