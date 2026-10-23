// The bound model has no Wails imports, so lifecycle and safety can be tested on Linux.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/zerotrustgarage/tools/copy/internal/catalog"
	"github.com/zerotrustgarage/tools/copy/internal/guard"
	"github.com/zerotrustgarage/tools/copy/internal/job"
	"github.com/zerotrustgarage/tools/copy/internal/private"
	copyrun "github.com/zerotrustgarage/tools/copy/internal/run"
	"github.com/zerotrustgarage/tools/copy/internal/template"
)

var version = "0.1.0-preview"

type Platform interface {
	Directory(string) (string, error)
	OpenFile(string) (string, error)
	SaveFile(string) (string, error)
	RevealDirectory(string) error
}

type executeFunc func(context.Context, job.Job, string, string, bool, io.Writer) (copyrun.Record, error)

type App struct {
	mu          sync.Mutex
	ctx         context.Context
	platform    Platform
	execute     executeFunc
	records     string
	templates   string
	state       State
	cancel      context.CancelFunc
	done        chan struct{}
	approved    string
	sequence    uint64
}

type View struct {
	Job        job.Job     `json:"job"`
	Command    string      `json:"command"`
	Issues     []job.Issue `json:"issues"`
	CanRun     bool        `json:"can_run"`
	Restriction string    `json:"restriction,omitempty"`
}

type State struct {
	ID       uint64          `json:"id"`
	Running  bool            `json:"running"`
	Mode     string          `json:"mode"`
	Phase    string          `json:"phase"`
	Started  time.Time       `json:"started"`
	Messages []string        `json:"messages"`
	Phases   []copyrun.Phase `json:"phases"`
	Record   *copyrun.Record `json:"record,omitempty"`
	Error    string          `json:"error,omitempty"`
}

type CatalogEntry struct {
	catalog.Option
	Available bool `json:"available"`
}

type Bootstrap struct {
	Version    string           `json:"version"`
	Catalog    []CatalogEntry  `json:"catalog"`
	Records    string           `json:"records"`
	Templates  string           `json:"templates"`
	DefaultJob job.Job          `json:"default_job"`
}

func NewApp() *App {
	records, _ := copyrun.DefaultRecords()
	templates, _ := template.DefaultDir()
	return &App{ctx: context.Background(), execute: copyrun.Execute, records: records, templates: templates, state: State{Messages: []string{}}}
}

func (a *App) setPlatform(ctx context.Context, p Platform) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.ctx, a.platform = ctx, p
}

func (a *App) Bootstrap() Bootstrap {
	a.mu.Lock()
	defer a.mu.Unlock()
	entries := []CatalogEntry{}
	for _, option := range catalog.All() {
		entries = append(entries, CatalogEntry{Option: option, Available: guard.OptionSupported(option.Token)})
	}
	return Bootstrap{Version: version, Catalog: entries, Records: a.records, Templates: a.templates, DefaultJob: job.Job{Schema: 1, Options: []string{"/E", "/MT:8", "/DCOPY:DAT", "/R:2", "/W:5", "/XJ"}}}
}

func describe(j job.Job) View {
	command, err := j.Command()
	issues := j.Issues()
	if issues == nil {
		issues = []job.Issue{}
	}
	if err == nil {
		parts := []string{"& (Join-Path ([Environment]::SystemDirectory) 'Robocopy.exe')"}
		for _, arg := range j.Arguments() {
			parts = append(parts, "'"+strings.ReplaceAll(arg, "'", "''")+"'")
		}
		command = strings.Join(parts, " ")
	}
	v := View{Job: j, Command: command, Issues: issues}
	if err != nil {
		v.Restriction = err.Error()
	} else if err := guard.Policy(j); err != nil {
		v.Restriction = err.Error()
	} else {
		v.CanRun = true
	}
	return v
}

func (a *App) Describe(j job.Job) View { return describe(j) }

func (a *App) Explain(command string) (View, error) {
	j, err := job.Import(command)
	if err != nil {
		return View{}, err
	}
	return describe(j), nil
}

func (a *App) nativePlatform() (Platform, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.platform == nil {
		return nil, errors.New("native dialogs are unavailable")
	}
	return a.platform, nil
}

func (a *App) PickDirectory(kind string) (string, error) {
	if kind != "source" && kind != "destination" {
		return "", errors.New("unknown endpoint")
	}
	p, err := a.nativePlatform()
	if err != nil {
		return "", err
	}
	return p.Directory("Choose " + kind + " folder")
}

func (a *App) ChooseRecordsDirectory() (string, error) {
	p, err := a.nativePlatform()
	if err != nil {
		return "", err
	}
	dir, err := p.Directory("Choose a records folder outside both copy trees")
	if err != nil || dir == "" {
		return "", err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.state.Running {
		return "", errors.New("stop the current operation before changing records location")
	}
	a.records, a.approved = dir, ""
	return dir, nil
}

func (a *App) ImportJob() (View, error) {
	p, err := a.nativePlatform()
	if err != nil {
		return View{}, err
	}
	name, err := p.OpenFile("Import a Copy job JSON file")
	if err != nil || name == "" {
		return View{}, err
	}
	f, err := os.Open(name)
	if err != nil {
		return View{}, err
	}
	defer f.Close()
	j, err := job.Decode(f)
	if err != nil {
		return View{}, err
	}
	return describe(j), nil
}

func (a *App) ExportJob(j job.Job) (string, error) {
	if j.Schema != job.Schema {
		return "", errors.New("unsupported job schema")
	}
	if _, err := j.Command(); err != nil {
		return "", err
	}
	p, err := a.nativePlatform()
	if err != nil {
		return "", err
	}
	name, err := p.SaveFile("copy-job.json")
	if err != nil || name == "" {
		return "", err
	}
	return name, private.WriteNew(name, j)
}

func fingerprint(j job.Job) string {
	data, _ := json.Marshal(j)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// Start requires a completed preview of the identical job before the GUI can copy.
// The shared runner also performs a fresh preview immediately before every copy.
func (a *App) Start(j job.Job, copyData bool) (uint64, error) {
	if err := guard.Policy(j); err != nil {
		return 0, err
	}
	j.Files = append([]string(nil), j.Files...)
	j.Options = append([]string(nil), j.Options...)
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.state.Running {
		return 0, errors.New("an operation is already running")
	}
	if a.records == "" {
		return 0, errors.New("choose a local records directory first")
	}
	if copyData && a.approved != fingerprint(j) {
		return 0, errors.New("preview this exact job before starting a copy")
	}
	a.approved = ""
	a.sequence++
	mode := "plan"
	if copyData {
		mode = "run"
	}
	a.state = State{ID: a.sequence, Running: true, Mode: mode, Phase: "Checking folders", Started: time.Now().UTC(), Messages: []string{}}
	ctx, cancel := context.WithCancel(a.ctx)
	a.cancel = cancel
	done := make(chan struct{})
	a.done = done
	root, execute, id := a.records, a.execute, a.sequence
	go func() {
		defer close(done)
		defer cancel()
		record, err := execute(ctx, j, root, version, copyData, &progressWriter{app: a, id: id})
		a.mu.Lock()
		defer a.mu.Unlock()
		a.state.Running = false
		a.cancel = nil
		a.state.Record = &record
		a.state.Phase = record.State
		if err != nil {
			a.state.Error = err.Error()
			a.state.Phase = "error"
		}
		// A stop remains a stop even if an injected runner ignores cancellation.
		if ctx.Err() != nil {
			record.State, record.NormalizedExit = "canceled", 3
			a.state.Record = &record
			a.state.Phase = "canceled"
		}
		if err == nil && ctx.Err() == nil && !copyData && record.State == "planned" && record.NormalizedExit == 0 {
			a.approved = fingerprint(j)
		}
	}()
	return id, nil
}

type progressWriter struct {
	app *App
	id  uint64
}

func (w *progressWriter) Write(data []byte) (int, error) {
	w.app.mu.Lock()
	defer w.app.mu.Unlock()
	if w.id != w.app.state.ID {
		return len(data), nil
	}
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if line == "" {
			continue
		}
		if len(line) > 4096 {
			line = line[:4096] + "…"
		}
		w.app.state.Messages = append(w.app.state.Messages, line)
		if len(w.app.state.Messages) > 200 {
			w.app.state.Messages = w.app.state.Messages[len(w.app.state.Messages)-200:]
		}
		switch line {
		case "validate...":
			w.app.state.Phase = "Validating options"
		case "preview...":
			w.app.state.Phase = "Previewing"
		case "copy...":
			w.app.state.Phase = "Copying"
		case "convergence...":
			w.app.state.Phase = "Checking convergence"
		}
	}
	return len(data), nil
}

func (w *progressWriter) PhaseStarted(name string) {
	w.app.mu.Lock()
	defer w.app.mu.Unlock()
	if w.id != w.app.state.ID {
		return
	}
	if label, ok := map[string]string{"validate": "Validating options", "preview": "Previewing", "copy": "Copying", "convergence": "Checking convergence"}[name]; ok {
		w.app.state.Phase = label
	}
}

func (w *progressWriter) PhaseFinished(p copyrun.Phase) {
	w.app.mu.Lock()
	defer w.app.mu.Unlock()
	if w.id == w.app.state.ID {
		w.app.state.Phases = append(w.app.state.Phases, p)
	}
}

func (a *App) Status() State {
	a.mu.Lock()
	defer a.mu.Unlock()
	state := a.state
	state.Messages = append([]string{}, state.Messages...)
	state.Phases = append([]copyrun.Phase{}, state.Phases...)
	// Completed records are immutable after publication.
	return state
}

func (a *App) Stop() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.approved = ""
	if a.cancel != nil {
		a.state.Phase = "Stopping"
		a.cancel()
	}
}

func (a *App) Shutdown() {
	a.Stop()
	a.mu.Lock()
	done := a.done
	a.mu.Unlock()
	if done != nil {
		select {
		case <-done:
		case <-time.After(10 * time.Second):
		}
	}
}

type TemplateView struct {
	Template template.Template `json:"template"`
	View     View              `json:"view"`
}

func (a *App) templateDirectory() (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.templates == "" {
		return "", errors.New("per-user template storage is unavailable")
	}
	return a.templates, nil
}

func (a *App) Templates() ([]TemplateView, error) {
	dir, err := a.templateDirectory()
	if err != nil {
		return nil, err
	}
	ids, err := template.List(dir)
	if err != nil {
		return nil, err
	}
	result := []TemplateView{}
	for _, id := range ids {
		t, err := template.Load(dir, id)
		if err != nil {
			return nil, fmt.Errorf("template %s: %w", id, err)
		}
		result = append(result, TemplateView{t, describe(t.Job)})
	}
	return result, nil
}

func (a *App) SaveTemplate(id, description string, j job.Job, askEndpoints bool) error {
	dir, err := a.templateDirectory()
	if err != nil {
		return err
	}
	if _, err := j.Command(); err != nil {
		return err
	}
	t := template.Template{Schema: 1, CatalogVersion: 1, ID: id, Description: description, Job: j}
	if askEndpoints {
		t.Fields = []template.Field{{Name: "source", Kind: "path", Description: "Source folder"}, {Name: "destination", Kind: "path", Description: "Destination folder"}}
		t.Job.Source, t.Job.Destination = "{source}", "{destination}"
	}
	return template.Save(dir, t)
}

func (a *App) ResolveTemplate(id string, values map[string]string) (View, error) {
	dir, err := a.templateDirectory()
	if err != nil {
		return View{}, err
	}
	t, err := template.Load(dir, id)
	if err != nil {
		return View{}, err
	}
	host, err := os.Hostname()
	if err != nil {
		return View{}, err
	}
	j, err := t.Resolve(values, time.Now(), host, os.Getenv("USERNAME"))
	if err != nil {
		return View{}, err
	}
	return describe(j), nil
}

func (a *App) InspectTemplate() (*TemplateView, error) {
	p, err := a.nativePlatform()
	if err != nil {
		return nil, err
	}
	name, err := p.OpenFile("Review a template JSON file")
	if err != nil || name == "" {
		return nil, err
	}
	f, err := os.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var t template.Template
	if err := job.DecodeStrict(f, &t); err != nil {
		return nil, err
	}
	if err := t.Validate(); err != nil {
		return nil, err
	}
	return &TemplateView{t, describe(t.Job)}, nil
}

// The renderer presents the complete template and asks for explicit acceptance.
// Execution still requires resolution, a preview and the same runner guards.
func (a *App) AcceptTemplate(t template.Template, accepted bool) error {
	if !accepted {
		return errors.New("review and accept the imported template before saving")
	}
	dir, err := a.templateDirectory()
	if err != nil {
		return err
	}
	return template.Save(dir, t)
}

func (a *App) ExportTemplate(id string) (string, error) {
	dir, err := a.templateDirectory()
	if err != nil {
		return "", err
	}
	t, err := template.Load(dir, id)
	if err != nil {
		return "", err
	}
	p, err := a.nativePlatform()
	if err != nil {
		return "", err
	}
	name, err := p.SaveFile(id + ".template.json")
	if err != nil || name == "" {
		return "", err
	}
	return name, private.WriteNew(name, t)
}

var recordID = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}/[a-f0-9]{24}$`)

type HistoryItem struct {
	ID          string    `json:"id"`
	Started     time.Time `json:"started"`
	State       string    `json:"state"`
	Mode        string    `json:"mode"`
	Source      string    `json:"source"`
	Destination string    `json:"destination"`
	Exit        int       `json:"exit"`
}

func (a *App) recordPath(id string) (string, error) {
	if !recordID.MatchString(id) {
		return "", errors.New("invalid record id")
	}
	a.mu.Lock()
	root := a.records
	a.mu.Unlock()
	if root == "" {
		return "", errors.New("records storage is unavailable")
	}
	dir := filepath.Join(root, filepath.FromSlash(id))
	// Never follow a user-created directory link out of the run library.
	for _, name := range []string{root, filepath.Dir(dir), dir, filepath.Join(dir, "run.json")} {
		info, err := os.Lstat(name)
		if err != nil {
			return "", err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return "", errors.New("record links are refused")
		}
	}
	return dir, nil
}

func (a *App) readRecord(id string) (copyrun.Record, error) {
	dir, err := a.recordPath(id)
	if err != nil {
		return copyrun.Record{}, err
	}
	f, err := os.Open(filepath.Join(dir, "run.json"))
	if err != nil {
		return copyrun.Record{}, err
	}
	defer f.Close()
	var r copyrun.Record
	if err := job.DecodeStrict(f, &r); err != nil {
		return r, err
	}
	if r.Schema != 1 {
		return r, errors.New("unsupported record schema")
	}
	return r, nil
}

func (a *App) History() ([]HistoryItem, error) {
	a.mu.Lock()
	root := a.records
	a.mu.Unlock()
	result := []HistoryItem{}
	if root == "" {
		return result, nil
	}
	days, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return result, nil
	}
	if err != nil {
		return nil, err
	}
	sort.Slice(days, func(i, j int) bool { return days[i].Name() > days[j].Name() })
	for _, day := range days {
		if !day.IsDir() || day.Type()&os.ModeSymlink != 0 {
			continue
		}
		entries, err := os.ReadDir(filepath.Join(root, day.Name()))
		if err != nil {
			continue
		}
		for _, entry := range entries {
			id := day.Name() + "/" + entry.Name()
			if !entry.IsDir() || !recordID.MatchString(id) {
				continue
			}
			r, err := a.readRecord(id)
			if err != nil {
				continue
			}
			result = append(result, HistoryItem{ID: id, Started: r.Started, State: r.State, Mode: r.Mode, Source: r.Job.Source, Destination: r.Job.Destination, Exit: r.NormalizedExit})
		}
		if len(result) >= 200 {
			break
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Started.After(result[j].Started) })
	if len(result) > 200 {
		result = result[:200]
	}
	return result, nil
}

func (a *App) HistoryJob(id string) (View, error) {
	r, err := a.readRecord(id)
	if err != nil {
		return View{}, err
	}
	return describe(r.Job), nil
}

func (a *App) RevealRecord(id string) error {
	dir, err := a.recordPath(id)
	if err != nil {
		return err
	}
	p, err := a.nativePlatform()
	if err != nil {
		return err
	}
	return p.RevealDirectory(dir)
}
