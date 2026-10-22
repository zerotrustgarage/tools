package parse

import (
	"encoding/base64"
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf16"
)

func TestNativeCaptures(t *testing.T) {
	for _, release := range []string{"2022", "2025"} {
		t.Run(release, func(t *testing.T) {
			b64, err := os.ReadFile(filepath.Join("../spike/testdata", "rc0-"+release+"-en", "native-log.base64"))
			if err != nil {
				t.Fatal(err)
			}
			data, err := base64.StdEncoding.Strict().DecodeString(strings.TrimSpace(string(b64)))
			if err != nil {
				t.Fatal(err)
			}
			s, err := NativeSummary(data)
			if err != nil || s.Files.Copied != 1 || s.Bytes.Copied != 81920 || s.Converged() {
				t.Fatalf("%+v %v", s, err)
			}
			text, err := Unicode(data)
			if err != nil || !strings.Contains(text, "café-日本語-😀.txt") {
				t.Fatal("lost Unicode filename")
			}
		})
	}
}

func encoded(text string) []byte {
	result := []byte{0xff, 0xfe}
	for _, u := range utf16.Encode([]rune(text)) {
		result = binary.LittleEndian.AppendUint16(result, u)
	}
	return result
}

func TestSummaryRefusesGuessing(t *testing.T) {
	for _, data := range [][]byte{[]byte("not UTF16"), {0xff, 0xfe, 1}, {0xff, 0xfe, 0, 0xd8}, encoded(strings.Repeat("fake : 1 1 0 0 0 0\n", 4)), encoded("Dirs : 1 0 1 0 0 0\nFiles : 1 0 1 0 0 0\nBytes : 18446744073709551616 0 1 0 0 0\n")} {
		if _, err := NativeSummary(data); err == nil {
			t.Fatal("accepted invalid or ambiguous log")
		}
	}
	for code := uint32(0); code < 32; code++ {
		want := 0
		if code&24 != 0 {
			want = 2
		} else if code&4 != 0 {
			want = 1
		}
		if Exit(code) != want {
			t.Fatalf("exit %d", code)
		}
	}
	if Exit(256) != 3 {
		t.Fatal("unknown exit was success")
	}
}

func FuzzNativeSummary(f *testing.F) {
	f.Add(encoded("Dirs : 1 0 1 0 0 0\nFiles : 1 0 1 0 0 0\nBytes : 1 0 1 0 0 0\n"))
	f.Add([]byte{0xff, 0xfe, 0, 0xd8})
	f.Fuzz(func(t *testing.T, data []byte) { _, _ = NativeSummary(data) })
}
