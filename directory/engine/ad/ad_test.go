package ad

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestEscapeFilterValue(t *testing.T) {
	got := EscapeFilterValue("a*(b)\\c\x00")
	if got != `a\2a\28b\29\5cc\00` {
		t.Fatalf("EscapeFilterValue() = %q", got)
	}
}

func TestEscapeFilterValueEscapesHighBytes(t *testing.T) {
	if got := EscapeFilterValue("é"); got != `\c3\a9` {
		t.Fatalf("escaped UTF-8 = %q", got)
	}
	if got := EscapeFilterValue(string([]byte{0xff})); got != `\ff` {
		t.Fatalf("escaped invalid UTF-8 = %q", got)
	}
}

func TestFileTime(t *testing.T) {
	got := fileTime("132223104000000000")
	want := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Fatalf("fileTime() = %s, want %s", got, want)
	}
	if !fileTime("0").IsZero() || !fileTime("invalid").IsZero() {
		t.Fatal("unset or invalid FILETIME should be zero")
	}
	small := fileTime("1")
	if !small.Equal(time.Unix(-11644473600, 100)) {
		t.Fatalf("small FILETIME = %s", small)
	}
}

func TestFakeSearchCopiesAndTruncates(t *testing.T) {
	f := NewFake([]Object{{DN: "CN=A", Attributes: map[string][]string{"cn": {"A"}}}, {DN: "CN=B", Attributes: map[string][]string{"cn": {"B"}}}})
	got, err := f.Search(context.Background(), SearchRequest{Query: "A", Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if got.Truncated || len(got.Objects) != 1 || got.Objects[0].DN != "CN=A" {
		t.Fatalf("unexpected result: %#v", got)
	}
	got.Objects[0].Attributes["cn"][0] = "changed"
	obj, err := f.Get(context.Background(), "cn=a")
	if err != nil {
		t.Fatal(err)
	}
	if obj.First("CN") != "A" {
		t.Fatal("result mutated fixture")
	}
}

func TestFakeClosedAndNotFound(t *testing.T) {
	f := NewFake(nil)
	if _, err := f.Get(context.Background(), "CN=missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get error = %v", err)
	}
	_ = f.Close()
	if _, err := f.Search(context.Background(), SearchRequest{}); !errors.Is(err, ErrClosed) {
		t.Fatalf("Search error = %v", err)
	}
}
