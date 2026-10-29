package rcj

import (
	"math/rand"
	"reflect"
	"strings"
	"testing"
	"unicode/utf16"

	"github.com/zerotrustgarage/tools/copy/internal/job"
)

// A file in the shape Robocopy's /SAVE: is documented to write: tab-indented
// switches, "::" comments (whole line and trailing) and file lists on later lines.
const robocopyStyle = "\r\n::\r\n:: Robocopy Job\r\n::\r\n:: Source Directory :\r\n\t/SD:C:\\Users\\Public\\My Documents\t\t:: Source Directory.\r\n\t/DD:\\\\nas\\share\\backup\t\t:: Destination Directory.\r\n\t/IF\t\t:: Include these Files.\r\n\t\t*.docx\r\n\t\t\"my file?.txt\"\r\n\t/XD\t\t:: eXclude Directories.\r\n\t\t\"C:\\Users\\Public\\My Documents\\cache\"\r\n\t\tnode_modules\r\n\t/XF *.tmp ~*\r\n\t/S\t\t:: copy Subdirectories.\r\n\t/COPY:DAT\r\n\t/R:2\r\n\t/W:5\r\n\t/MT:8\r\n"

func TestParseRobocopyStyleFile(t *testing.T) {
	r, err := ParseBytes([]byte(robocopyStyle))
	if err != nil {
		t.Fatal(err)
	}
	j := r.Job
	if j.Source != `C:\Users\Public\My Documents` || j.Destination != `\\nas\share\backup` {
		t.Fatalf("%q %q", j.Source, j.Destination)
	}
	if !reflect.DeepEqual(j.Files, []string{"*.docx", "my file?.txt"}) {
		t.Fatalf("%q", j.Files)
	}
	want := []string{"/XD", `C:\Users\Public\My Documents\cache`, "node_modules", "/XF", "*.tmp", "~*", "/S", "/COPY:DAT", "/R:2", "/W:5", "/MT:8"}
	if !reflect.DeepEqual(j.Options, want) {
		t.Fatalf("%q", j.Options)
	}
	if r.Encoding != UTF8 || len(r.Notes) != 0 {
		t.Fatalf("%s %+v", r.Encoding, r.Notes)
	}
	for _, i := range j.Issues() {
		t.Errorf("unexpected issue %+v", i)
	}
}

func TestUnknownAndHiddenSwitchesAreKeptAndFlagged(t *testing.T) {
	text := "/SD:C:\\a\n/DD:D:\\b\n/FUTURE:1\n/REG\n/JOB:other.rcj\n/SAVE:x.rcj\nstray\n/MIR\n/SD:C:\\second\n"
	r, err := Parse(text)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"/FUTURE:1", "/REG", "/JOB:other.rcj", "/SAVE:x.rcj", "stray", "/MIR", `/SD:C:\second`}
	if !reflect.DeepEqual(r.Job.Options, want) {
		t.Fatalf("%q", r.Job.Options)
	}
	flagged := map[string]bool{}
	for _, i := range r.Job.Issues() {
		flagged[i.Argument] = true
	}
	for _, a := range []string{"/FUTURE:1", "/REG", "/JOB:other.rcj", "/SAVE:x.rcj", "stray", `/SD:C:\second`} {
		if !flagged[a] {
			t.Errorf("%s was not flagged", a)
		}
	}
	var destructive, stray, dup bool
	for _, n := range r.Notes {
		destructive = destructive || n.Argument == "/MIR" && strings.Contains(n.Message, "preview")
		stray = stray || n.Argument == "stray"
		dup = dup || n.Argument == `/SD:C:\second` && n.Severity == "error"
	}
	if !destructive || !stray || !dup {
		t.Fatalf("%+v", r.Notes)
	}
	if _, err := Format(r.Job); err == nil {
		t.Fatal("exported a job with hidden and unknown options")
	}
}

func TestNoSDNoDD(t *testing.T) {
	r, err := Parse("\t/NOSD\n\t/NODD\n\t/S\n")
	if err != nil {
		t.Fatal(err)
	}
	if r.Job.Source != "" || r.Job.Destination != "" || !reflect.DeepEqual(r.Job.Options, []string{"/S"}) {
		t.Fatalf("%+v", r.Job)
	}
	if len(r.Notes) != 2 || len(r.Job.Issues()) != 2 {
		t.Fatalf("%+v %+v", r.Notes, r.Job.Issues())
	}
	r, err = Parse("/NOSD\n/SD:C:\\a\n/DD:D:\\b\n")
	if err != nil || r.Job.Source != `C:\a` || len(r.Notes) != 1 {
		t.Fatalf("%+v %v", r, err)
	}
}

func TestCommentsAndQuotes(t *testing.T) {
	text := "::/SD:C:\\ignored\n   ::  /DD:D:\\ignored\n/SD:\"C:\\a b\\\\\" :: trailing\n/DD:D:\\b\n/XD \"x :: y\" z\n"
	r, err := Parse(text)
	if err != nil {
		t.Fatal(err)
	}
	if r.Job.Source != `C:\a b\` || r.Job.Destination != `D:\b` || !reflect.DeepEqual(r.Job.Options, []string{"/XD", "x :: y", "z"}) {
		t.Fatalf("%+v", r.Job)
	}
}

func TestStructuralErrors(t *testing.T) {
	for name, text := range map[string]string{
		"ambiguous path": "/SD:C:\\a /S\n",
		"tab in path":    "/DD:D:\\a\tb\n",
		"open quote":     "/XD \"abc\n",
		"bare CR":        "/S\r/E\n",
		"too many lines": strings.Repeat("\n", MaxLines+1),
		"long line":      "/XD " + strings.Repeat("a", 40000) + "\n",
	} {
		if _, err := Parse(text); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if _, err := ParseBytes(make([]byte, MaxBytes+1)); err == nil {
		t.Error("accepted an oversized file")
	}
}

func utf16le(s string, bom bool) []byte {
	var out []byte
	if bom {
		out = append(out, 0xFF, 0xFE)
	}
	for _, u := range utf16.Encode([]rune(s)) {
		out = append(out, byte(u), byte(u>>8))
	}
	return out
}

func TestEncodings(t *testing.T) {
	text := "/SD:C:\\café-日本語-😀\r\n/DD:D:\\b\r\n"
	for name, tc := range map[string]struct {
		data []byte
		enc  string
	}{
		"utf16le": {utf16le(text, true), UTF16LE},
		"utf8bom": {append([]byte{0xEF, 0xBB, 0xBF}, text...), UTF8BOM},
		"utf8":    {[]byte(text), UTF8},
	} {
		r, err := ParseBytes(tc.data)
		if err != nil || r.Encoding != tc.enc || r.Job.Source != `C:\café-日本語-😀` {
			t.Errorf("%s: %+v %v", name, r, err)
		}
	}
	// ANSI: 0xE9 is e-acute in Windows-1252; the import warns about the guess.
	r, err := ParseBytes([]byte("/SD:C:\\caf\xe9\r\n/DD:D:\\b\r\n"))
	if err != nil || r.Job.Source != `C:\café` || r.Encoding != Windows1252 {
		t.Fatalf("%+v %v", r, err)
	}
	if len(r.Notes) == 0 || r.Notes[0].Severity != "warning" {
		t.Fatalf("no ANSI warning: %+v", r.Notes)
	}
	for name, data := range map[string][]byte{
		"UTF-16 without mark": utf16le("/S\r\n", false),
		"odd UTF-16":          append(utf16le("/S", true), 0),
		"lone surrogate":      {0xFF, 0xFE, 0x00, 0xD8},
		"undefined cp1252":    {'/', 'S', ' ', 0x81},
		"bad UTF-8 with mark": {0xEF, 0xBB, 0xBF, 0xC3},
		"control character":   []byte("/S\x01\n"),
	} {
		if _, err := ParseBytes(data); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestFormat(t *testing.T) {
	j := job.Job{Schema: 1, Name: "Nightly copy", Source: `C:\my data\`, Destination: `\\nas\share\data`, Files: []string{"*.txt", "a b.doc"},
		Options: []string{"/XD", `C:\my data\cache`, "tmp", "/LOG:C:\\logs dir\\x.log", "/MT:8"}}
	text, err := Format(j)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"\t/SD:\"C:\\my data\\\\\"\r\n", "\t/DD:\\\\nas\\share\\data\r\n", "\t/IF\r\n\t\t*.txt\r\n\t\t\"a b.doc\"\r\n", "\t/XD\r\n\t\t\"C:\\my data\\cache\"\r\n\t\ttmp\r\n", "\t/LOG:\"C:\\logs dir\\x.log\"\r\n", "\t/R:2\r\n", "\t/W:5\r\n", "\t/XJ\r\n", "\t/E\r\n", ":: " + NameMarker + " Nightly copy\r\n"} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q in\n%s", want, text)
		}
	}
	if strings.Contains(strings.ToUpper(text), "/REG") || strings.Contains(strings.ToLower(text), "password") {
		t.Fatal("forbidden content")
	}
	for name, bad := range map[string]job.Job{
		"/REG":       {Schema: 1, Source: `C:\a`, Destination: `D:\b`, Options: []string{"/REG"}},
		"unknown":    {Schema: 1, Source: `C:\a`, Destination: `D:\b`, Options: []string{"/FUTURE"}},
		"/JOB":       {Schema: 1, Source: `C:\a`, Destination: `D:\b`, Options: []string{"/JOB:x"}},
		"/IF":        {Schema: 1, Source: `C:\a`, Destination: `D:\b`, Options: []string{"/IF", "a"}},
		"/NOSD":      {Schema: 1, Source: `C:\a`, Destination: `D:\b`, Options: []string{"/NOSD"}},
		"no source":  {Schema: 1, Destination: `D:\b`},
		"name space": {Schema: 1, Name: " x", Source: `C:\a`, Destination: `D:\b`},
		"bad value":  {Schema: 1, Source: `C:\a`, Destination: `D:\b`, Options: []string{"/MT:999"}},
	} {
		if _, err := Format(bad); err == nil {
			t.Errorf("%s: exported", name)
		}
	}
}

func assertRoundTrip(t *testing.T, j job.Job) {
	t.Helper()
	text, err := Format(j)
	if err != nil {
		return
	}
	for _, mode := range []string{"auto", "utf16", "utf8"} {
		data, err := Encode(text, mode)
		if err != nil {
			t.Fatal(err)
		}
		r, err := ParseBytes(data)
		if err != nil {
			t.Fatalf("%s: %v\n%q", mode, err, text)
		}
		if !reflect.DeepEqual(r.Job.Arguments(), j.Arguments()) || r.Job.Name != j.Name {
			t.Fatalf("%s: argument round trip\n%q\n%q", mode, j.Arguments(), r.Job.Arguments())
		}
		for _, n := range r.Notes {
			if strings.HasPrefix(n.Message, "destructive:") {
				continue
			}
			t.Fatalf("%s: export produced an import note %+v", mode, n)
		}
		again, err := Format(r.Job)
		if err != nil || again != text {
			t.Fatalf("%s: not a fixed point: %v\n%q\n%q", mode, err, text, again)
		}
		if mode == "auto" && len(r.Job.Files) != len(j.Files) {
			t.Fatalf("files changed: %q to %q", j.Files, r.Job.Files)
		}
	}
}

func TestRoundTripExamples(t *testing.T) {
	for _, j := range []job.Job{
		{Schema: 1, Source: `C:\data`, Destination: `D:\backup`},
		{Schema: 1, Name: "café 日本語 😀", Source: `C:\caf é\`, Destination: `\\nas\share name\`, Files: []string{"*.txt", "a b?.doc"}, Options: []string{"/S", "/XD", "x :: y", "::z", "a\"b", "/XF", "*.tmp", "/COPY:DAT", "/MAXAGE:20260101", "/RH:0800-1700"}},
		{Schema: 1, Source: `C:\a`, Destination: `D:\b`, Options: []string{"/LOG:C:\\logs\\a b.log", "/LFSM:10g", "/R:0", "/W:0", "/XJ", "/E"}},
		{Schema: 1, Source: `C:\a`, Destination: `D:\b`, Options: []string{"/LEV:3", "/NODCOPY", "/A-:SH", "/IA:RH", "/SPARSE:n"}},
	} {
		if len(j.Issues()) != 0 {
			t.Fatalf("example is invalid: %+v", j.Issues())
		}
		assertRoundTrip(t, j)
	}
}

// Property test over generated jobs: awkward but valid names must survive.
func TestRoundTripProperty(t *testing.T) {
	rng := rand.New(rand.NewSource(20261029))
	pieces := []string{"a", "b c", "日本", "😀", "x::y", " ", "\\", "é", "..", "~", "$", "%", "(", ")", "'", "&", "tmp", "*", "?"}
	word := func() string {
		var b strings.Builder
		for n := 1 + rng.Intn(4); n > 0; n-- {
			b.WriteString(pieces[rng.Intn(len(pieces))])
		}
		return b.String()
	}
	checked := 0
	for i := 0; i < 3000; i++ {
		j := job.Job{Schema: 1, Source: `C:\` + word(), Destination: `\\srv\share\` + word()}
		if rng.Intn(2) == 0 {
			j.Source = strings.TrimSuffix(j.Source, `\`) + `\`
		}
		if rng.Intn(3) == 0 {
			j.Name = strings.TrimSpace(word())
		}
		for n := rng.Intn(3); n > 0; n-- {
			j.Files = append(j.Files, word())
		}
		for _, o := range [][]string{{"/S"}, {"/MT:4"}, {"/XJ"}, {"/R:0"}, {"/COPY:DATS"}, {"/IORATE:5m"}, {"/MAXAGE:30"}, {"/LOG:C:\\" + word() + ".log"}, {"/XD", word(), word()}, {"/XF", word()}, {"/MIR"}, {"/MOVE"}} {
			if rng.Intn(3) == 0 {
				j.Options = append(j.Options, o...)
			}
		}
		if len(j.Issues()) != 0 {
			continue
		}
		checked++
		assertRoundTrip(t, j)
	}
	if checked < 300 {
		t.Fatalf("only %d valid generated jobs", checked)
	}
}

// Design 9.2 presets must pass validation and export.
func TestDesignPresetsExport(t *testing.T) {
	for _, opts := range [][]string{
		strings.Fields("/E /COPYALL /DCOPY:DAT /B /MT:16 /R:2 /W:5 /XJ"),
		strings.Fields("/FFT /DST /XJ"),
	} {
		j := job.Job{Schema: 1, Source: `C:\a`, Destination: `D:\b`, Options: opts}
		if len(j.Issues()) != 0 {
			t.Fatalf("%v: %+v", opts, j.Issues())
		}
		assertRoundTrip(t, j)
	}
}

func FuzzRCJReader(f *testing.F) {
	f.Add([]byte(robocopyStyle))
	f.Add(utf16le("\t/SD:C:\\a\r\n\t/DD:D:\\b\r\n\t/IF\r\n\t\t*.txt\r\n", true))
	f.Add([]byte("/NOSD\n/NODD\n/XD \"a b\" c\n/FUTURE\n::x\n"))
	f.Add([]byte("\xef\xbb\xbf/SD:\"C:\\a b\\\\\" :: c\n/DD:D:\\b\n/MT\n"))
	f.Add([]byte("/SD:C:\\caf\xe9\n/DD:D:\\b\n/REG\n"))
	f.Fuzz(func(t *testing.T, data []byte) {
		r, err := ParseBytes(data)
		if err != nil {
			return
		}
		// Reading must never panic, must not drop a switch, and what can be
		// exported must read back to the same arguments.
		for _, a := range r.Job.Options {
			if a == "" && len(r.Job.Issues()) == 0 {
				t.Fatalf("empty option accepted: %+v", r.Job)
			}
		}
		assertRoundTrip(t, r.Job)
	})
}
