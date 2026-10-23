package server

import (
	"github.com/zerotrustgarage/tools/tftp/internal/store"
	"testing"
)

func TestProvisioningRules(t *testing.T) {
	s := &Server{config: Config{Remap: []Remap{{"boot", "images/boot"}}, Templates: []Template{{"SEP{mac}.cnf.xml", "phones/{mac}.xml"}}}}
	if e := ValidateRules(s.config.Remap, s.config.Templates); e != nil {
		t.Fatal(e)
	}
	for input, want := range map[string]string{"boot": "images/boot", "SEP001122AABBCC.cnf.xml": "phones/001122aabbcc.xml", "other": "other"} {
		got, e := s.resolve(input)
		if e != nil || got != want {
			t.Fatal(input, got, e)
		}
	}
	if _, e := s.resolve("../boot"); e == nil {
		t.Fatal("traversal accepted")
	}
	if e := ValidateRules([]Remap{{"boot", "../escape"}}, nil); e == nil {
		t.Fatal("unsafe remap")
	}
}

func FuzzResolve(f *testing.F) {
	f.Add("SEP001122aabbcc.cnf.xml")
	f.Add("../boot")
	f.Fuzz(func(t *testing.T, name string) {
		s := &Server{config: Config{Templates: []Template{{"SEP{mac}.cnf.xml", "phones/{mac}.xml"}}}}
		got, e := s.resolve(name)
		if e == nil {
			if _, e := store.Name(got); e != nil {
				t.Fatal(got, e)
			}
		}
	})
}
