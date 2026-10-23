package main

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zerotrustgarage/tools/copy/internal/job"
	"github.com/zerotrustgarage/tools/copy/internal/private"
	copyrun "github.com/zerotrustgarage/tools/copy/internal/run"
	"github.com/zerotrustgarage/tools/copy/internal/template"
)

func validJob() job.Job {
	return job.Job{Schema: 1, Source: `C:\source space`, Destination: `D:\backup`, Options: []string{"/E", "/R:2", "/W:5", "/XJ"}}
}

func testApp(t *testing.T) *App {
	t.Helper()
	a := NewApp()
	a.records, a.templates = filepath.Join(t.TempDir(), "records"), filepath.Join(t.TempDir(), "templates")
	return a
}

func waitDone(t *testing.T, a *App) State {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if s := a.Status(); !s.Running {
			return s
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("operation did not finish")
	return State{}
}

func TestPreviewApprovalCannotBeReusedOrAppliedToAnotherJob(t *testing.T) {
	a := testApp(t)
	var copies atomic.Int32
	a.execute = func(ctx context.Context, j job.Job, root, v string, copy bool, w io.Writer) (copyrun.Record, error) {
		state := "planned"
		if copy {
			copies.Add(1)
			state = "copied"
		}
		return copyrun.Record{State: state, NormalizedExit: 0, Job: j}, nil
	}
	j := validJob()
	if _, err := a.Start(j, true); err == nil {
		t.Fatal("copy started without preview")
	}
	if _, err := a.Start(j, false); err != nil {
		t.Fatal(err)
	}
	waitDone(t, a)
	changed := j
	changed.Destination = `D:\another`
	if _, err := a.Start(changed, true); err == nil {
		t.Fatal("preview approved another job")
	}
	if _, err := a.Start(j, true); err != nil {
		t.Fatal(err)
	}
	waitDone(t, a)
	if copies.Load() != 1 {
		t.Fatal("copy runner was not called exactly once")
	}
	if _, err := a.Start(j, true); err == nil {
		t.Fatal("preview approval was reused")
	}
}

func TestStopKeepsTheOperationBusyUntilCleanupAndRemainsCanceled(t *testing.T) {
	a := testApp(t)
	started, release := make(chan struct{}), make(chan struct{})
	a.execute = func(ctx context.Context, j job.Job, root, v string, copy bool, w io.Writer) (copyrun.Record, error) {
		close(started)
		<-ctx.Done()
		<-release
		return copyrun.Record{State: "planned", NormalizedExit: 0}, nil
	}
	if _, err := a.Start(validJob(), false); err != nil {
		t.Fatal(err)
	}
	<-started
	a.Stop()
	if !a.Status().Running {
		t.Fatal("stop reported completion before cleanup")
	}
	if _, err := a.Start(validJob(), false); err == nil {
		t.Fatal("concurrent operation started during cleanup")
	}
	close(release)
	s := waitDone(t, a)
	if s.Phase != "canceled" || s.Record.NormalizedExit != 3 {
		t.Fatalf("cancellation became success: %+v", s)
	}
	if _, err := a.Start(validJob(), true); err == nil {
		t.Fatal("canceled preview approved a copy")
	}
	a.Shutdown()
}

func TestDesignerKeepsUnknownOptionsAndQuotesPowerShell(t *testing.T) {
	a := testApp(t)
	view, err := a.Explain(`robocopy C:\data D:\backup /FUTURE:7 /MIR`)
	if err != nil || len(view.Job.Options) != 2 || view.CanRun || len(view.Issues) == 0 {
		t.Fatalf("%+v, %v", view, err)
	}
	j := validJob()
	j.Source = `C:\O'Brien\$variable`
	view = a.Describe(j)
	if !view.CanRun || !strings.Contains(view.Command, "'C:\\O''Brien\\$variable'") || !strings.Contains(view.Command, "[Environment]::SystemDirectory") {
		t.Fatalf("unsafe shell rendering: %+v", view)
	}
	for _, option := range a.Bootstrap().Catalog {
		if option.Token == "/REG" && option.Available {
			t.Fatal("registry option offered for copying")
		}
	}
}

type fakePlatform struct{ open string }

func (p fakePlatform) Directory(string) (string, error) { return "", nil }
func (p fakePlatform) OpenFile(string) (string, error)  { return p.open, nil }
func (p fakePlatform) SaveFile(string) (string, error)  { return "", nil }
func (p fakePlatform) RevealDirectory(string) error     { return nil }

func TestTemplateImportIsStrictAndRequiresAcceptance(t *testing.T) {
	a := testApp(t)
	file := filepath.Join(t.TempDir(), "untrusted.json")
	if err := os.WriteFile(file, []byte(`{"schema":1,"password":"never-store"}`), 0600); err != nil {
		t.Fatal(err)
	}
	a.setPlatform(context.Background(), fakePlatform{open: file})
	if _, err := a.InspectTemplate(); err == nil {
		t.Fatal("password field accepted")
	}
	tmpl := template.Template{Schema: 1, CatalogVersion: 1, ID: "copy", Job: validJob()}
	if err := a.AcceptTemplate(tmpl, false); err == nil {
		t.Fatal("saved without acceptance")
	}
	if err := a.SaveTemplate("copy", "A reusable job", validJob(), true); err != nil {
		t.Fatal(err)
	}
	view, err := a.ResolveTemplate("copy", map[string]string{"source": `C:\data space`, "destination": `D:\archive`})
	if err != nil || !view.CanRun {
		t.Fatalf("%+v, %v", view, err)
	}
	if _, err := a.ResolveTemplate("copy", map[string]string{"source": "/MIR", "destination": `D:\archive`}); err == nil {
		t.Fatal("placeholder added an option")
	}
	if _, err := a.Start(view.Job, true); err == nil {
		t.Fatal("template skipped preview")
	}
}

func TestHistoryRefusesTraversalAndLinks(t *testing.T) {
	a := testApp(t)
	id := "2026-10-22/0123456789abcdef01234567"
	dir := filepath.Join(a.records, filepath.FromSlash(id))
	if err := private.EnsureDir(dir); err != nil {
		t.Fatal(err)
	}
	record := copyrun.Record{Schema: 1, State: "copied", Mode: "run", Job: validJob(), Started: time.Now()}
	if err := private.WriteNew(filepath.Join(dir, "run.json"), record); err != nil {
		t.Fatal(err)
	}
	loaded, err := a.HistoryRecord(id)
	if err != nil || loaded.State != "copied" || loaded.Job.Source != record.Job.Source {
		t.Fatalf("history record: %+v, %v", loaded, err)
	}
	items, err := a.History()
	if err != nil || len(items) != 1 || items[0].ID != id {
		t.Fatalf("%+v, %v", items, err)
	}
	for _, id := range []string{"../outside", "2026-10-22/../../file", id + "/../x"} {
		if _, err := a.HistoryRecord(id); err == nil {
			t.Fatalf("traversal accepted: %q", id)
		}
	}
	if err := os.Remove(filepath.Join(dir, "run.json")); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "outside.json")
	if err := private.WriteNew(outside, record); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "run.json")); err != nil {
		t.Skip(err)
	}
	if _, err := a.HistoryJob(id); err == nil {
		t.Fatal("followed a record symlink")
	}
}

func TestRunnerFailureNeverApprovesPreview(t *testing.T) {
	a := testApp(t)
	a.execute = func(context.Context, job.Job, string, string, bool, io.Writer) (copyrun.Record, error) {
		return copyrun.Record{State: "error"}, errors.New("engine unavailable")
	}
	if _, err := a.Start(validJob(), false); err != nil {
		t.Fatal(err)
	}
	s := waitDone(t, a)
	if s.Error != "engine unavailable" {
		t.Fatal(s)
	}
	if _, err := a.Start(validJob(), true); err == nil {
		t.Fatal("failed preview approved a copy")
	}
}

func TestRobocopyCommandUsesLiteralPowerShellQuotes(t *testing.T) {
	j := validJob()
	j.Source = `C:\O'Brien ‘curly’ ‚low‛ $(Get-Date)`
	view := describe(j)
	if !strings.Contains(view.Command, "'C:\\O''Brien ‘‘curly’’ ‚‚low‛‛ $(Get-Date)'") {
		t.Fatal(view.Command)
	}
}
