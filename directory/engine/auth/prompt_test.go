package auth

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
	"unicode/utf16"
)

func TestReadSecretLine(t *testing.T) {
	got, err := readSecretLine(strings.NewReader("pässword 1\r\nnext line"))
	if err != nil || string(got) != "pässword 1" {
		t.Fatalf("got %q %v", got, err)
	}
	if got, err = readSecretLine(strings.NewReader("no newline")); err != nil || string(got) != "no newline" {
		t.Fatalf("at EOF: %q %v", got, err)
	}
	if _, err = readSecretLine(strings.NewReader("")); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("empty input: %v", err)
	}
	if _, err = readSecretLine(strings.NewReader(strings.Repeat("x", maxSecretLine+1))); err == nil {
		t.Fatal("over-long password accepted")
	}
	got, _ = readSecretLine(strings.NewReader(strings.Repeat("y", maxSecretLine) + "\n"))
	if len(got) != maxSecretLine || cap(got) != maxSecretLine {
		t.Fatal("buffer reallocated")
	}
}

func TestUTF16ToUTF8(t *testing.T) {
	in := append(utf16.Encode([]rune("Pa$$wörd 🔑")), 0, 'x')
	if got := utf16ToUTF8(in); !bytes.Equal(got, []byte("Pa$$wörd 🔑")) {
		t.Fatalf("got %q", got)
	}
	if got := utf16ToUTF8([]uint16{'a', 0xd800, 'b'}); string(got) != "a�b" {
		t.Fatalf("lone surrogate: %q", got)
	}
}

func TestParseAccount(t *testing.T) {
	for in, want := range map[string][2]string{
		`EXAMPLE\a-alex`:     {"a-alex", "EXAMPLE"},
		"a-alex@example.com": {"a-alex@example.com", ""},
		" a-alex ":           {"a-alex", ""},
	} {
		if u, d := ParseAccount(in); u != want[0] || d != want[1] {
			t.Errorf("%q: got %q %q", in, u, d)
		}
	}
}
