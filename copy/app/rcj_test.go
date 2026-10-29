package main

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/zerotrustgarage/tools/copy/internal/job"
	"github.com/zerotrustgarage/tools/copy/internal/rcj"
	copyrun "github.com/zerotrustgarage/tools/copy/internal/run"
)

// dialogPlatform counts the save dialogs it is asked for.
type dialogPlatform struct {
	fakePlatform
	saves *atomic.Int32
}

func (p dialogPlatform) SaveFile(name string) (string, error) {
	p.saves.Add(1)
	return p.fakePlatform.SaveFile(name)
}

// plainPlatform has no RCJ dialog at all.
type plainPlatform struct{}

func (plainPlatform) Directory(string) (string, error) { return "", nil }
func (plainPlatform) OpenFile(string) (string, error)  { return "", nil }
func (plainPlatform) SaveFile(string) (string, error)  { return "", nil }
func (plainPlatform) RevealDirectory(string) error     { return nil }

func writeFile(t *testing.T, dir, name string, data []byte) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func listing(t *testing.T, dir string) []string {
	t.Helper()
	var names []string
	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		names = append(names, p)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return names
}

const hostileRCJ = "/SD:C:\\source\r\n/DD:D:\\dest\r\n/MIR\r\n/REG:7\r\n/FUTURE:9\r\nstray\r\n/XD\r\n\tC:\\skip\r\n\t\"<img src=x onerror=alert(1)>\"\r\n"

func TestImportRCJExplainsWithoutSideEffects(t *testing.T) {
	a := testApp(t)
	var runs atomic.Int32
	a.execute = func(context.Context, job.Job, string, string, bool, io.Writer) (copyrun.Record, error) {
		runs.Add(1)
		return copyrun.Record{}, nil
	}
	dir := t.TempDir()
	name := writeFile(t, dir, `<img src=x onerror=alert(1)>.rcj`, []byte(hostileRCJ))
	before := listing(t, dir)
	a.setPlatform(context.Background(), fakePlatform{open: name})
	got, err := a.ImportRCJ()
	if err != nil || got == nil {
		t.Fatalf("%+v %v", got, err)
	}
	if got.File != name || got.Encoding != rcj.UTF8 || got.Bytes != len(hostileRCJ) {
		t.Fatalf("%+v", got)
	}
	if got.View.CanRun || !got.NeedsReview || got.View.Job.Source != `C:\source` || got.View.Job.Destination != `D:\dest` {
		t.Fatalf("unsafe import was not held for review: %+v", got.View)
	}
	byArg := map[string]RCJSwitch{}
	for _, s := range got.Switches {
		byArg[s.Argument] = s
	}
	if s := byArg["/MIR"]; !s.Known || !s.Destructive || s.Available || s.Help == "" {
		t.Errorf("/MIR: %+v", s)
	}
	if s := byArg["/REG:7"]; !s.Known || !s.Hidden {
		t.Errorf("/REG not flagged hidden: %+v", s)
	}
	if s := byArg["/FUTURE:9"]; s.Known || s.Hidden {
		t.Errorf("unknown switch: %+v", s)
	}
	if s := byArg["/XD"]; !s.Known || !reflect.DeepEqual(s.Items, []string{`C:\skip`, "<img src=x onerror=alert(1)>"}) {
		t.Errorf("/XD items: %+v", s)
	}
	if s, ok := byArg["stray"]; !ok || s.Known {
		t.Errorf("stray argument dropped or trusted: %+v", got.Switches)
	}
	// Nothing was dropped: every imported option appears in the explanation.
	var listed int
	for _, s := range got.Switches {
		listed += 1 + len(s.Items)
	}
	if listed != len(got.View.Job.Options) {
		t.Errorf("explained %d of %d arguments", listed, len(got.View.Job.Options))
	}
	var sawHidden, sawUnknown, sawDestructive bool
	for _, i := range got.View.Issues {
		sawHidden = sawHidden || strings.Contains(i.Message, "hidden option")
		sawUnknown = sawUnknown || strings.Contains(i.Message, "not recognized")
	}
	for _, n := range got.Notes {
		sawDestructive = sawDestructive || strings.Contains(n.Message, "destructive")
	}
	if !sawHidden || !sawUnknown || !sawDestructive {
		t.Errorf("flags missing: hidden=%v unknown=%v destructive=%v", sawHidden, sawUnknown, sawDestructive)
	}
	// No side effects: no new files, no templates or records, nothing run, no state.
	if !reflect.DeepEqual(before, listing(t, dir)) {
		t.Error("import changed the file's folder")
	}
	for _, d := range []string{a.templates, a.records} {
		if _, err := os.Stat(d); !os.IsNotExist(err) {
			t.Errorf("import created %s", d)
		}
	}
	if runs.Load() != 0 || a.Status().ID != 0 || a.Status().Running {
		t.Error("import ran something")
	}
	if _, err := a.Start(got.View.Job, true); err == nil {
		t.Error("imported job started without preview")
	}
	if runs.Load() != 0 {
		t.Error("destructive import reached the runner")
	}
}

func TestImportRCJCleanJobAndNames(t *testing.T) {
	a := testApp(t)
	name := writeFile(t, t.TempDir(), "clean.rcj", []byte("::\r\n:: ZTG-Copy-Name: Nightly\r\n\t/SD:C:\\a b\r\n\t/DD:D:\\c\r\n\t/E\r\n\t/XD\r\n\t\tC:\\tmp\r\n"))
	a.setPlatform(context.Background(), fakePlatform{open: name})
	got, err := a.ImportRCJ()
	if err != nil || got == nil {
		t.Fatal(got, err)
	}
	if got.NeedsReview || !got.View.CanRun || got.View.Job.Name != "Nightly" || got.View.Job.Source != `C:\a b` || len(got.Switches) != 2 {
		t.Fatalf("%+v", got)
	}
	if got.Notes == nil || got.View.Issues == nil {
		t.Error("lists must be empty arrays, not null, for the renderer")
	}
}

func TestImportRCJRefusals(t *testing.T) {
	dir := t.TempDir()
	a := testApp(t)
	big := writeFile(t, dir, "big.rcj", []byte(strings.Repeat("::x\r\n", rcj.MaxBytes/5+10)))
	bare := writeFile(t, dir, "nobom16.rcj", []byte("/\x00S\x00\n\x00"))
	cr := writeFile(t, dir, "cr.rcj", []byte("/S\r/E\n"))
	quote := writeFile(t, dir, "quote.rcj", []byte("/XD \"abc\n"))
	for name, file := range map[string]string{"oversized": big, "utf-16 without mark": bare, "bare CR": cr, "open quote": quote,
		"missing": filepath.Join(dir, "missing.rcj"), "directory": dir} {
		a.setPlatform(context.Background(), fakePlatform{open: file})
		if got, err := a.ImportRCJ(); err == nil || got != nil {
			t.Errorf("%s accepted: %+v", name, got)
		}
	}
	a.setPlatform(context.Background(), fakePlatform{})
	if got, err := a.ImportRCJ(); got != nil || err != nil {
		t.Errorf("cancelled dialog: %+v %v", got, err)
	}
	a.setPlatform(context.Background(), plainPlatform{})
	if _, err := a.ImportRCJ(); err == nil {
		t.Error("platform without an RCJ dialog accepted")
	}
	if _, err := NewApp().ImportRCJ(); err == nil {
		t.Error("import without a native platform")
	}
}

func TestExportRCJRefusalsComeBeforeTheDialog(t *testing.T) {
	a := testApp(t)
	dir := t.TempDir()
	var saves atomic.Int32
	a.setPlatform(context.Background(), dialogPlatform{fakePlatform{save: filepath.Join(dir, "out.rcj")}, &saves})
	bad := map[string]job.Job{
		"/REG":          {Schema: 1, Source: `C:\a`, Destination: `D:\b`, Options: []string{"/REG:7"}},
		"unknown":       {Schema: 1, Source: `C:\a`, Destination: `D:\b`, Options: []string{"/FUTURE:9"}},
		"no source":     {Schema: 1, Destination: `D:\b`},
		"bad value":     {Schema: 1, Source: `C:\a`, Destination: `D:\b`, Options: []string{"/MT:999"}},
		"schema":        {Schema: 2, Source: `C:\a`, Destination: `D:\b`},
		"/QUIT":         {Schema: 1, Source: `C:\a`, Destination: `D:\b`, Options: []string{"/QUIT"}},
		"/JOB":          {Schema: 1, Source: `C:\a`, Destination: `D:\b`, Options: []string{"/JOB:x.rcj"}},
		"stray list":    {Schema: 1, Source: `C:\a`, Destination: `D:\b`, Options: []string{"stray"}},
		"long job name": {Schema: 1, Name: strings.Repeat("n", 300), Source: `C:\a`, Destination: `D:\b`},
	}
	for name, j := range bad {
		if got, err := a.ExportRCJ(j, "auto"); err == nil || got != nil {
			t.Errorf("%s exported: %+v", name, got)
		}
		if _, err := a.PreviewRCJExport(j, "auto"); err == nil {
			t.Errorf("%s previewed", name)
		}
	}
	if _, err := a.ExportRCJ(validJob(), "latin1"); err == nil {
		t.Error("unknown encoding accepted")
	}
	if saves.Load() != 0 {
		t.Errorf("a refused export opened %d save dialogs", saves.Load())
	}
	if len(listing(t, dir)) != 1 {
		t.Errorf("a refused export wrote files: %v", listing(t, dir))
	}
	if _, err := NewApp().ExportRCJ(validJob(), ""); err == nil {
		t.Error("export without a native platform")
	}
}

func TestExportRCJWritesOwnerOnlyAndReadsBack(t *testing.T) {
	dir := t.TempDir()
	j := job.Job{Schema: 1, Name: "Nightly copy", Source: `C:\my data`, Destination: `\\nas\share`, Files: []string{"*.txt"}, Options: []string{"/XD", `C:\cache dir`, "tmp", "/MT:8"}}
	for mode, want := range map[string]string{"": "ASCII", "auto": "ASCII", "utf8": "UTF-8 without", "utf16": "UTF-16LE"} {
		a := testApp(t)
		out := filepath.Join(dir, "m-"+mode+".rcj")
		a.setPlatform(context.Background(), fakePlatform{save: out})
		got, err := a.ExportRCJ(j, mode)
		if err != nil || got == nil {
			t.Fatalf("%q: %+v %v", mode, got, err)
		}
		if got.Path != out || !strings.HasPrefix(got.Encoding, want) || !strings.Contains(got.Check, "robocopy /JOB:FILE /QUIT") || !strings.Contains(got.Check, "No credentials") {
			t.Errorf("%q: %+v", mode, got)
		}
		info, err := os.Stat(out)
		if err != nil || info.Size() != int64(got.Bytes) {
			t.Fatalf("%q: %v", mode, err)
		}
		if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
			t.Errorf("%q: file mode %v is not owner-only", mode, info.Mode().Perm())
		}
		back, err := explainRCJFile(out)
		if err != nil || back.NeedsReview || !reflect.DeepEqual(back.View.Job.Arguments(), j.Arguments()) || back.View.Job.Name != j.Name {
			t.Errorf("%q: does not read back: %+v %v", mode, back, err)
		}
	}
	// Non-ASCII text under auto switches to UTF-16LE with a byte order mark.
	a := testApp(t)
	out := filepath.Join(dir, "unicode.rcj")
	a.setPlatform(context.Background(), fakePlatform{save: out})
	u := j
	u.Source = `C:\café 日本語`
	got, err := a.ExportRCJ(u, "auto")
	if err != nil || !strings.HasPrefix(got.Encoding, "UTF-16LE") {
		t.Fatalf("%+v %v", got, err)
	}
	if data, _ := os.ReadFile(out); len(data) < 2 || data[0] != 0xFF || data[1] != 0xFE {
		t.Error("no byte order mark")
	}
	if back, err := explainRCJFile(out); err != nil || back.View.Job.Source != u.Source {
		t.Errorf("%+v %v", back, err)
	}
	// The preview shows the same text and size, and never touches disk.
	preview, err := a.PreviewRCJExport(j, "")
	if err != nil || !strings.Contains(preview.Text, "/SD:\"C:\\my data\"") || preview.Bytes == 0 {
		t.Fatalf("%+v %v", preview, err)
	}
	if strings.Contains(strings.ToLower(preview.Text), "password") {
		t.Error("export text mentions a password")
	}
	for _, line := range strings.Split(preview.Text, "\r\n") {
		if k := job.Key(strings.TrimSpace(line)); k == "/REG" || k == "/JOB" || k == "/SAVE" {
			t.Errorf("export text holds %q", line)
		}
	}
}

func TestExportRCJNeverOverwritesAndCancelWritesNothing(t *testing.T) {
	dir := t.TempDir()
	existing := writeFile(t, dir, "existing.rcj", []byte("keep me"))
	a := testApp(t)
	a.setPlatform(context.Background(), fakePlatform{save: existing})
	got, err := a.ExportRCJ(validJob(), "auto")
	if err == nil || got != nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("%+v %v", got, err)
	}
	if data, _ := os.ReadFile(existing); string(data) != "keep me" {
		t.Fatal("existing file was replaced")
	}
	a.setPlatform(context.Background(), fakePlatform{})
	if got, err := a.ExportRCJ(validJob(), "auto"); got != nil || err != nil {
		t.Errorf("cancelled save: %+v %v", got, err)
	}
	if len(listing(t, dir)) != 2 {
		t.Errorf("cancelled save left files: %v", listing(t, dir))
	}
	// A path in a missing folder fails cleanly and leaves nothing behind.
	a.setPlatform(context.Background(), fakePlatform{save: filepath.Join(dir, "missing", "x.rcj")})
	if _, err := a.ExportRCJ(validJob(), "auto"); err == nil {
		t.Error("export into a missing folder succeeded")
	}
}
