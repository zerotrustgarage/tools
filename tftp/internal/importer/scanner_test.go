package importer

import (
	"strings"
	"testing"
)

func TestScannerAllowlist(t *testing.T) {
	v, e := Decode(strings.NewReader(`{"tool":"ZTG Scanner","hosts":[{"ip":"192.168.1.2","status":"up"},{"ip":"192.168.1.2","status":"up"},{"ip":"::1","status":"up"},{"ip":"192.168.1.3","status":"down"}]}`))
	if e != nil || len(v) != 2 || v[0].String() != "192.168.1.2/32" || v[1].String() != "::1/128" {
		t.Fatal(v, e)
	}
	for _, bad := range []string{`{}`, `{"tool":"ZTG Scanner","hosts":[]}`, `{"tool":"ZTG Scanner","hosts":[{"ip":"bad","status":"up"}]}`} {
		if _, e = Decode(strings.NewReader(bad)); e == nil {
			t.Fatal(bad)
		}
	}
}
func FuzzScanner(f *testing.F) {
	f.Add([]byte(`{"tool":"ZTG Scanner","hosts":[]}`))
	f.Fuzz(func(t *testing.T, b []byte) {
		v, e := Decode(strings.NewReader(string(b)))
		if e == nil {
			for _, p := range v {
				if !p.IsValid() || p.Bits() != p.Addr().BitLen() {
					t.Fatal(p)
				}
			}
		}
	})
}
