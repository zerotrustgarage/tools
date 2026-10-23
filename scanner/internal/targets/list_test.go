package targets

import (
	"fmt"
	"strings"
	"testing"
)

func TestNormalizeListSyntax(t *testing.T) {
	got, err := NormalizeList("\uFEFF# office\n10.0.0.1, 10.0.0.2-10; 10.0.0.8-10.0.0.12\nprinter.lab 10.0.0.0/24 # note\n")
	if err != nil {
		t.Fatal(err)
	}
	want := "10.0.0.1, 10.0.0.2-10, 10.0.0.8-10.0.0.12, printer.lab, 10.0.0.0/24"
	if got != want {
		t.Fatalf("NormalizeList() = %q, want %q", got, want)
	}
}

func TestNormalizeListAllowsPublicSyntaxWithoutResolving(t *testing.T) {
	got, err := NormalizeList("8.8.8.8, 203.0.113.0/30, example.invalid")
	if err != nil || got != "8.8.8.8, 203.0.113.0/30, example.invalid" {
		t.Fatalf("NormalizeList() = %q, %v", got, err)
	}
}

func TestNormalizeListRangeBoundariesAndDeduplication(t *testing.T) {
	for _, input := range []string{"10.0.0.0/16", "10.0.0.0/31", "10.0.0.0-10.0.255.255"} {
		if got, err := NormalizeList(input); err != nil || got != input {
			t.Errorf("NormalizeList(%q) = %q, %v", input, got, err)
		}
	}
	got, err := NormalizeList("# one\n10.0.0.1, 10.0.0.2; 10.0.0.1 # duplicate\n")
	if err != nil || got != "10.0.0.1, 10.0.0.2" {
		t.Fatalf("NormalizeList() = %q, %v", got, err)
	}
}

func TestNormalizeListRejectsInvalidSyntax(t *testing.T) {
	for _, input := range []string{
		"", " # comment only", "https://example.com", "user:pass@example.com",
		"10.0.0.1:443", "2001:db8::1", "999.1.2.3", "1.2.3.4.5",
		"999.1.2.3-10", "10.0.0.999-12", "10.0.0.1-10.0.0.0", "10.0.0.0/15", "10.0.0.1-10.1.0.1",
		"name..lab", "bad/name", "bad\x01name", "10.0.0.1\x00",
	} {
		if got, err := NormalizeList(input); err == nil {
			t.Errorf("NormalizeList(%q) = %q, want error", input, got)
		}
	}
}

func TestNormalizeListLimits(t *testing.T) {
	if _, err := NormalizeList(strings.Repeat("x", MaxListBytes+1)); err == nil {
		t.Fatal("oversized input accepted")
	}
	if _, err := NormalizeList(strings.Repeat("a ", MaxListTokens+1)); err == nil {
		t.Fatal("too many tokens accepted")
	}
	if _, err := NormalizeList(string([]byte{'1', '0', '.', '0', '.', '0', '.', '1', 0xff})); err == nil {
		t.Fatal("invalid UTF-8 accepted")
	}

	// Comma-space normalization can grow a valid input by one byte per item.
	var b strings.Builder
	for i := 0; i < MaxListTokens; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(fmt.Sprintf("h%014d", i))
	}
	if b.Len() > MaxListBytes {
		t.Fatalf("test input unexpectedly has %d bytes", b.Len())
	}
	if _, err := NormalizeList(b.String()); err == nil {
		t.Fatal("normalized output exceeding byte limit accepted")
	}
}
