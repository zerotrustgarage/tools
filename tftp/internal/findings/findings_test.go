package findings

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestIOSFindings(t *testing.T) {
	config := "switch 1 provision c9300-48p\nusername admin password 0 DO-NOT-LOG\nenable password DO-NOT-LOG\npassword 7 DO-NOT-LOG\nusername old secret 5 DO-NOT-LOG\nsnmp-server community DO-NOT-LOG RO\nip http server\nline vty 0 4\n transport input ssh telnet\n!\ninterface Gi1/0/1\n description normal\n"
	r, e := Scan(strings.NewReader(config))
	if e != nil {
		t.Fatal(e)
	}
	want := map[string][]int{"cleartext-password": {2, 3}, "reversible-password": {4}, "weak-hash": {5}, "snmp-community": {6}, "http-cleartext": {7}, "telnet": {9}, "smart-install": {1}}
	if len(r.Findings) != 8 {
		t.Fatalf("%+v", r)
	}
	for _, f := range r.Findings {
		lines := want[f.Code]
		if len(lines) == 0 || f.Line != lines[0] {
			t.Fatalf("unexpected finding %+v", f)
		}
		want[f.Code] = lines[1:]
		if f.Code == "smart-install" && !f.Verify {
			t.Fatal("Smart Install must request verification")
		}
	}
	b, _ := json.Marshal(r)
	if strings.Contains(string(b), "DO-NOT-LOG") {
		t.Fatal("secret in output")
	}
}
func TestSecureAndScopedConfiguration(t *testing.T) {
	config := "switch 1 provision c9300\nno vstack\nusername admin secret 9 SAFE\nusername other secret 8 SAFE\nno snmp-server community OLD\nno ip http server\nline vty 0 4\n transport input ssh\n!\ninterface Gi1/0/1\n transport input telnet\n"
	r, e := Scan(strings.NewReader(config))
	if e != nil || len(r.Findings) != 0 {
		t.Fatalf("%+v %v", r, e)
	}
	r, _ = Scan(strings.NewReader("hostname router\nversion 17.1\n"))
	if len(r.Findings) != 0 {
		t.Fatal("inferred a switch without platform evidence")
	}
	r, _ = Scan(strings.NewReader("radius-server key 7 REVERSIBLE\nkey-string 7 REVERSIBLE\n"))
	if len(r.Findings) != 2 {
		t.Fatal(r)
	}
}
func TestScanBounds(t *testing.T) {
	r, e := Scan(strings.NewReader("snmp-server community SECRET\n\x00binary"))
	if e != nil || r.Skipped != "binary" || len(r.Findings) != 0 {
		t.Fatalf("binary: %+v %v", r, e)
	}
	r, e = Scan(strings.NewReader(strings.Repeat("x", MaxLine+10) + "\npassword 0 SECRET\n"))
	if e != nil || !r.Truncated || len(r.Findings) != 1 || r.Findings[0].Line != 2 {
		t.Fatalf("long line: %+v %v", r, e)
	}
	r, e = Scan(strings.NewReader(strings.Repeat("password 0 SECRET\n", MaxFindings+10)))
	if e != nil || !r.Truncated || len(r.Findings) != MaxFindings {
		t.Fatal("finding cap", len(r.Findings), e)
	}
	r, e = Scan(strings.NewReader(strings.Repeat("x", MaxBytes+10)))
	if e != nil || !r.Truncated {
		t.Fatal("scan byte cap", e)
	}
}
func FuzzFindings(f *testing.F) {
	for _, s := range []string{"", "password 0 SECRET", "line vty 0 4\n transport input telnet\n", "switch 1 provision c9300\nno vstack\n", "\x00"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		if len(s) > MaxBytes+1 {
			return
		}
		r, e := Scan(strings.NewReader(s))
		if e != nil {
			t.Fatal(e)
		}
		if len(r.Findings) > MaxFindings {
			t.Fatal("unbounded findings")
		}
		for _, f := range r.Findings {
			if f.Line < 1 || f.Line > strings.Count(s, "\n")+1 {
				t.Fatal("invalid line")
			}
			switch f.Code {
			case "cleartext-password", "reversible-password", "snmp-community", "smart-install", "telnet", "http-cleartext", "weak-hash":
			default:
				t.Fatal("input escaped as a rule")
			}
		}
	})
}
