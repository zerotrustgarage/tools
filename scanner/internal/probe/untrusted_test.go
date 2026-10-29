package probe

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestCleanText(t *testing.T) {
	cases := []struct {
		name, in string
		max      int
		want     string
	}{
		{"plain", "Office Printer", 80, "Office Printer"},
		{"controls dropped", "a\x00b\x1b[31mc\x7fd", 80, "ab[31mcd"},
		{"whitespace collapsed", "  a \t\r\n b  ", 80, "a b"},
		{"newline is a space", "line1\nline2", 80, "line1 line2"},
		{"bidi override dropped", "name\u202eevil\u2066x", 80, "nameevilx"},
		{"zero width dropped", "a\u200bb\ufeffc", 80, "abc"},
		{"line separator", "a\u2028b", 80, "a b"},
		{"invalid utf8 dropped", "ok\xff\xfe!", 80, "ok!"},
		{"private use dropped", "a\ue000b", 80, "ab"},
		{"truncated on runes", "ééééé", 3, "ééé"},
		{"empty", "", 10, ""},
		{"zero max", "abc", 0, ""},
		{"only controls", "\x00\x01\x02", 10, ""},
	}
	for _, c := range cases {
		got := CleanText(c.in, c.max)
		if got != c.want {
			t.Errorf("%s: CleanText(%q, %d) = %q, want %q", c.name, c.in, c.max, got, c.want)
		}
		if !utf8.ValidString(got) {
			t.Errorf("%s: result is not valid UTF-8", c.name)
		}
	}
}

func TestCleanTextNeverExceedsMax(t *testing.T) {
	in := strings.Repeat("ab cd\n", 100)
	for max := 0; max < 40; max++ {
		if n := utf8.RuneCountInString(CleanText(in, max)); n > max {
			t.Fatalf("max %d returned %d runes", max, n)
		}
	}
}

func FuzzCleanText(f *testing.F) {
	for _, s := range []string{"", "a b", "\x00\u202e\xff", "é\n\t x"} {
		f.Add(s, 10)
	}
	f.Fuzz(func(t *testing.T, s string, max int) {
		if max < 0 || max > 1000 {
			return
		}
		out := CleanText(s, max)
		if !utf8.ValidString(out) || utf8.RuneCountInString(out) > max {
			t.Fatalf("bad output %q", out)
		}
		for _, r := range out {
			if r < 0x20 || r == 0x7f || (r >= 0x80 && r < 0xa0) || r == 0x202e || r == 0x200b {
				t.Fatalf("control character %U survived", r)
			}
		}
		if out != strings.TrimSpace(out) || strings.Contains(out, "  ") {
			t.Fatalf("whitespace not normalized: %q", out)
		}
	})
}
