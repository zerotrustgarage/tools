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

func TestOptionValueTypes(t *testing.T) {
	base := Job{Schema: 1, Source: `C:\data`, Destination: `D:\data`}
	valid := []string{"/MT:8", "/MT", "/R:0", "/LEV:2", "/MAX:1048576", "/IORATE:512k", "/IOMAXSIZE:4M", "/THRESHOLD:1g", "/LFSM", "/LFSM:10g",
		"/MAXAGE:30", "/MINAGE:20260101", "/MAXLAD:1899", "/A+:RASHCNET", "/A-:SH", "/XA:O", "/IA:rh", "/COPY:DATSOU", "/COPY:datx", "/DCOPY:DAT", "/DCOPY:DATEX",
		"/RH:0000-2359", "/RH:2200-0600", "/SPARSE:y", "/SPARSE:N", "/LOG:C:\\logs\\run.txt", "/UNILOG+:\\\\nas\\logs\\run.txt", "/IPG:100", "/MON:5", "/MOT:10"}
	for _, o := range valid {
		j := base
		j.Options = []string{o}
		for _, i := range j.Issues() {
			t.Errorf("%s: %+v", o, i)
		}
	}
	invalid := []string{"/MT:abc", "/MT:+8", "/R:1000001", "/LEV:0", "/MAX:-1", "/MAX:10k", "/IORATE:5x", "/IORATE:k", "/IOMAXSIZE:99999999999g", "/THRESHOLD:1.5m",
		"/MAXAGE:abc", "/MAXAGE:20261301", "/MAXAGE:20260230", "/MAXAGE:1900", "/MAXAGE:2026010", "/MINLAD:-3", "/A+:O", "/A-:Z", "/XA:RR", "/IA:", "/COPY:DATZ", "/COPY:DD", "/DCOPY:S",
		"/RH:2400-0600", "/RH:0800", "/RH:08:00-17:00", "/SPARSE:maybe", "/LOG:", "/LOG:C:\\a*.txt", "/LOG:\\\\?\\C:\\a.txt", "/JOB:x.rcj", "/SAVE:x.rcj", "/REG", "/DEBUG"}
	for _, o := range invalid {
		j := base
		j.Options = []string{o}
		if len(j.Issues()) == 0 {
			t.Errorf("accepted %s", o)
		}
	}
}

func TestDesignPresetsValidate(t *testing.T) {
	// Option sets named in design section 9.2 (the app presets are not built yet).
	for _, options := range []string{"/E /COPYALL /DCOPY:DAT /B /MT:16 /R:2 /W:5 /XJ", "/FFT /DST /XJ", "/S /MAXAGE:30 /MOV /R:2 /W:5", "/MIR /XX /R:2 /W:5 /XJ"} {
		j := Job{Schema: 1, Source: `C:\a`, Destination: `D:\b`, Options: strings.Fields(options)}
		for _, i := range j.Issues() {
			if i.Severity == "error" {
				t.Errorf("%s: %+v", options, i)
			}
		}
	}
}
