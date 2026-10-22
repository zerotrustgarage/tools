package audit

import (
	"bytes"
	"encoding/json"
	"errors"
	"testing"
)

type broken struct{ writes int }

func (w *broken) Write([]byte) (int, error) { w.writes++; return 0, errors.New("disk full") }
func TestAudit(t *testing.T) {
	var b bytes.Buffer
	l := New(&b)
	if e := l.Write(Record{Name: "untrusted\nname", Result: "refused"}); e != nil {
		t.Fatal(e)
	}
	var r Record
	if e := json.Unmarshal(b.Bytes(), &r); e != nil || r.Name != "untrusted\nname" || r.Protocol == "" || r.Time.IsZero() {
		t.Fatalf("%+v %v", r, e)
	}
	if bytes.Count(b.Bytes(), []byte{'\n'}) != 1 {
		t.Fatal("log injection")
	}
	w := &broken{}
	l = New(w)
	if l.Write(r) == nil || l.Write(r) == nil || l.Err() == nil || w.writes != 1 {
		t.Fatal("audit failure was ignored")
	}
}
