package job

import (
	"reflect"
	"strings"
	"testing"
)

func TestImportPreservesOptions(t *testing.T) {
	j, err := Import(`robocopy "C:\source space\" "\\nas\share" *.txt /MIR /XD cache "old files" /FUTURE:7`)
	// A Windows quoted trailing backslash must be doubled; reject ambiguous input.
	if err == nil {
		t.Fatal("accepted unclosed Windows quoting")
	}
	j, err = Import(`robocopy "C:\source space\\" "\\nas\share" *.txt /MIR /XD cache "old files" /FUTURE:7`)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"/MIR", "/XD", "cache", "old files", "/FUTURE:7"}
	if !reflect.DeepEqual(j.Options, want) || len(j.Issues()) != 1 || j.Issues()[0].Argument != "/FUTURE:7" {
		t.Fatalf("%+v, %+v", j, j.Issues())
	}
}

func TestValidation(t *testing.T) {
	base := Job{Schema: 1, Source: `C:\data`, Destination: `\\nas\share\data`}
	if len(base.Issues()) != 0 {
		t.Fatal(base.Issues())
	}
	for _, opts := range [][]string{{"/MT:0"}, {"/MT:129"}, {"/MT:8", "/IPG:10"}, {"/SECFIX"}, {"/XD"}, {"/R:2", "/R:3"}, {"/E:yes"}, {"/W:-1"}} {
		j := base
		j.Options = opts
		if len(j.Issues()) == 0 {
			t.Errorf("accepted %q", opts)
		}
	}
	for _, p := range []string{`C:relative`, `\rooted`, `\\?\C:\data`, `\\nas`, `C:\wild*`} {
		if AbsoluteWindowsPath(p) {
			t.Errorf("accepted path %q", p)
		}
	}
	for _, data := range []string{`{"schema":1,"password":"secret"}`, `{"schema":9}`, `{"schema":1} {}`} {
		if _, err := Decode(strings.NewReader(data)); err == nil {
			t.Errorf("accepted document %q", data)
		}
	}
}

func TestDefaultsDoNotOverride(t *testing.T) {
	j := Job{Schema: 1, Source: `C:\a`, Destination: `D:\b`, Options: []string{"/S", "/R:0", "/W:0", "/NODCOPY"}}
	got := strings.Join(j.Arguments(), "|")
	if strings.Contains(got, "|/E|") || strings.Contains(got, "/R:2") || strings.Contains(got, "/DCOPY:") || !strings.Contains(got, "/XJ") {
		t.Fatal(got)
	}
}
