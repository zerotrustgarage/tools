package metrics

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf16"
)

func utf16le(text string, bom bool) []byte {
	var out []byte
	if bom {
		out = append(out, 0xff, 0xfe)
	}
	for _, u := range utf16.Encode([]rune(text)) {
		out = append(out, byte(u), byte(u>>8))
	}
	return out
}

func appendFile(t *testing.T, path string, data []byte) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.Write(data); err != nil {
		t.Fatal(err)
	}
}

func poll(t *testing.T, tl *Tailer) string {
	t.Helper()
	var b strings.Builder
	if err := tl.Poll(func(s string) { b.WriteString(s) }); err != nil {
		t.Fatal(err)
	}
	return b.String()
}

func TestTailerReadsAGrowingUTF16LogAcrossAnyByteBoundary(t *testing.T) {
	path := filepath.Join(t.TempDir(), "copy.log")
	tl := NewTailer(path)
	if got := poll(t, tl); got != "" {
		t.Fatalf("a missing log gave %q", got)
	}
	text := "Started : 日本語 café 😀 end\r\n" + copied(81920, `C:\src\café-日本語-😀.txt`)
	data := utf16le(text, true)
	var got strings.Builder
	// Write one byte at a time: every odd length and every surrogate split is exercised.
	for i := range data {
		appendFile(t, path, data[i:i+1])
		got.WriteString(poll(t, tl))
	}
	if got.String() != text {
		t.Fatalf("decoded %q want %q", got.String(), text)
	}
	if again := poll(t, tl); again != "" {
		t.Fatalf("nothing new gave %q", again)
	}
}

func TestTailerStartsOverWhenTheLogIsReplaced(t *testing.T) {
	path := filepath.Join(t.TempDir(), "copy.log")
	tl := NewTailer(path)
	appendFile(t, path, utf16le("first log with a long first line\r\n", true))
	if got := poll(t, tl); !strings.HasPrefix(got, "first log") {
		t.Fatal(got)
	}
	if err := os.WriteFile(path, utf16le("new\r\n", true), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := poll(t, tl); got != "new\r\n" {
		t.Fatalf("got %q", got)
	}
}

func TestTailerRefusesALogWithoutABOMAndStaysFailed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "copy.log")
	appendFile(t, path, []byte("plain ascii text"))
	tl := NewTailer(path)
	if err := tl.Poll(func(string) {}); err != ErrNotUnicode {
		t.Fatalf("error %v", err)
	}
	if err := tl.Poll(func(string) { t.Fatal("read after failure") }); err != ErrNotUnicode {
		t.Fatalf("error %v", err)
	}
}

func TestTailerReplacesBrokenSurrogates(t *testing.T) {
	path := filepath.Join(t.TempDir(), "copy.log")
	appendFile(t, path, []byte{0xff, 0xfe, 0x00, 0xd8, 'a', 0x00, 0x00, 0xdc, 'b', 0x00})
	got := poll(t, NewTailer(path))
	if !strings.Contains(got, "a") || !strings.Contains(got, "b") || !strings.Contains(got, "\uFFFD") {
		t.Fatalf("got %q", got)
	}
}

// The Server 2025 native log capture (UTF-16LE), fed through the tailer in odd pieces.
func TestNativeLogFixtureThroughTheTailerCountsOneFinishedFile(t *testing.T) {
	encoded, err := os.ReadFile(filepath.Join("..", "spike", "testdata", "rc0-2025-en", "native-log.base64"))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(encoded)))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "copy.log")
	tl := NewTailer(path)
	r := NewReplay(serialConfig(1, 81920), 0)
	for i := 0; i < len(raw); i += 37 {
		end := i + 37
		if end > len(raw) {
			end = len(raw)
		}
		appendFile(t, path, raw[i:end])
		if text := poll(t, tl); text != "" {
			r.Tracker.Log(text)
		}
		r.AdvanceTo(r.Offset()+100_000_000, nil)
	}
	snap := r.Tracker.Snapshot()
	if snap.Completed.Files != 1 || snap.Completed.Bytes != 81920 || snap.Seen.Files != 1 || snap.Seen.Dirs != 2 {
		t.Fatalf("snapshot %+v", snap)
	}
	if snap.Current == nil || !strings.HasSuffix(snap.Current.Path, "café-日本語-😀.txt") {
		t.Fatalf("current %+v", snap.Current)
	}
}
