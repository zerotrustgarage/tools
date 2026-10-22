package spike

import (
	"encoding/base64"
	"encoding/binary"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"
	"unicode/utf16"
)

func TestDecodeUnicode(t *testing.T) {
	want := "café 日本語 😀\r\n"
	raw := []byte{0xff, 0xfe}
	for _, unit := range utf16.Encode([]rune(want)) {
		raw = binary.LittleEndian.AppendUint16(raw, unit)
	}
	got, err := DecodeUnicode(raw)
	if err != nil || got != want {
		t.Fatalf("got %q, %v", got, err)
	}
	for _, bad := range [][]byte{{1}, {0xfe, 0xff}, {0, 0xd8}, {0, 0xdc}, {0, 0xd8, 65, 0}} {
		if _, err := DecodeUnicode(bad); err == nil {
			t.Fatalf("accepted malformed UTF-16: %x", bad)
		}
	}
}

func TestHelpOptions(t *testing.T) {
	got := HelpOptions(" /MT[:n] /R:n /256 /A+:[RASH] /A-:[RASH] /unilog:file /MT\r\n")
	want := []string{"/256", "/A+", "/A-", "/MT", "/R", "/UNILOG"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestNormalizedExit(t *testing.T) {
	for code := 0; code <= 31; code++ {
		want := 0
		if code >= 8 {
			want = 2
		} else if code&4 != 0 {
			want = 1
		}
		if got := NormalizedExit(code); got != want {
			t.Fatalf("native %d: got %d want %d", code, got, want)
		}
	}
	for _, code := range []int{-1, 32, 255} {
		if NormalizedExit(code) != 3 {
			t.Fatalf("unknown native code %d accepted", code)
		}
	}
}

func FuzzDecodeUnicode(f *testing.F) {
	f.Add([]byte{0xff, 0xfe, 65, 0})
	f.Add([]byte{0, 0xd8, 0, 0xdc})
	f.Fuzz(func(t *testing.T, data []byte) {
		_, _ = DecodeUnicode(data)
	})
}

func TestCapturedDefaultHelp(t *testing.T) {
	encoded, err := os.ReadFile("testdata/windows-2025-en/help-default.stdout.base64")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(encoded)))
	if err != nil {
		t.Fatal(err)
	}
	options := HelpOptions(string(raw))
	for _, required := range []string{"/MT", "/COPY", "/UNILOG", "/MIR", "/QUIT", "/256", "/A+", "/A-"} {
		if !slices.Contains(options, required) {
			t.Errorf("real help capture missing %s", required)
		}
	}
}
