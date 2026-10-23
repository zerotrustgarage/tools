package catalog

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

func TestReferenceAndCatalogAgree(t *testing.T) {
	data, err := os.ReadFile("../../docs/flags.md")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{}
	for _, line := range strings.Split(string(data), "\n") {
		if !strings.HasPrefix(line, "| `/") {
			continue
		}
		first := strings.SplitN(line, " | ", 2)[0]
		for _, m := range regexp.MustCompile("`(/[A-Z0-9?+-]+)").FindAllStringSubmatch(first, -1) {
			want[m[1]] = true
		}
	}
	want["/DEBUG"] = true
	seen := map[string]bool{}
	for _, o := range All() {
		if seen[o.Token] || !want[o.Token] || o.Help == "" {
			t.Errorf("invalid or undocumented catalog entry: %s", o.Token)
		}
		seen[o.Token] = true
		for _, c := range o.ConflictsWith {
			other, ok := Lookup(c)
			if !ok || !strings.Contains("|"+strings.Join(other.ConflictsWith, "|")+"|", "|"+o.Token+"|") {
				t.Errorf("asymmetric conflict %s -> %s", o.Token, c)
			}
		}
		for _, implied := range o.Implies {
			if _, ok := Lookup(implied); !ok {
				t.Errorf("unknown implication %s -> %s", o.Token, implied)
			}
		}
	}
	for token := range want {
		if !seen[token] {
			t.Errorf("missing %s", token)
		}
	}
}

func TestRiskAndDetection(t *testing.T) {
	for _, token := range []string{"/MIR", "/PURGE", "/MOVE", "/MOV", "/M"} {
		o, ok := Lookup(token)
		if !ok || !strings.Contains(strings.Join(o.Tags, ","), "destructive") {
			t.Errorf("missing destructive tag: %s", token)
		}
	}
	if len(Search("encryption")) == 0 || len(Search("impossible-token")) != 0 {
		t.Fatal("search does not use help and handling text")
	}
}
