package run

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestOlderRecordWithoutProbeLoads(t *testing.T) {
	var r Record
	if err := json.Unmarshal([]byte(`{"schema":1,"version":"0.1","mode":"plan","engine":{"path":"p","sha256":"s"},"phases":[],"state":"complete"}`), &r); err != nil {
		t.Fatal(err)
	}
	if r.Probe != nil || r.Engine.Path != "p" {
		t.Fatalf("%+v", r)
	}
	out, err := json.Marshal(r)
	if err != nil || strings.Contains(string(out), `"probe"`) {
		t.Fatalf("an absent probe must stay absent: %s, %v", out, err)
	}
}

func TestRecordWithProbeRoundTrips(t *testing.T) {
	in := `{"schema":1,"probe":{"file_version":{"value":"10.0.1.2","source":"s"},"elevation":{"type":"limited","elevated":false,"source":"s"},"privileges":{"source":"s","unavailable":"API error: x"},"signature":{"decision":"catalog","source":"s"}}}`
	var r Record
	if err := json.Unmarshal([]byte(in), &r); err != nil {
		t.Fatal(err)
	}
	if r.Probe == nil || r.Probe.FileVersion.Value != "10.0.1.2" || r.Probe.Signature.Decision != "catalog" || *r.Probe.Elevation.Elevated {
		t.Fatalf("%+v", r.Probe)
	}
}
