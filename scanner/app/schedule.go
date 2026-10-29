package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/zerotrustgarage/tools/scanner/internal/inventory"
	"github.com/zerotrustgarage/tools/scanner/internal/report"
	"github.com/zerotrustgarage/tools/scanner/internal/scan"
	"github.com/zerotrustgarage/tools/scanner/internal/schedule"
	"github.com/zerotrustgarage/tools/scanner/internal/targets"
)

// Scheduled rescans (SC7).
//
// A schedule is opt-in per saved target set and runs only while this app is
// open: nothing is installed, registered or started with Windows. The UI keeps
// the schedules in the WebView's local storage beside the saved target sets and
// hands the whole list to SetSchedules, which validates it again, so a refused
// range is refused here whatever the UI says. A scheduled rescan uses the same
// scan plan as a manual one (scan.BuildPlan, so gentle mode paces it exactly as
// it paces a manual scan), never uses SNMP (credentials are never stored, so a
// schedule cannot carry them), and never carries public-range authorization.
//
// A scheduled rescan keeps its own results. It does not touch the inventory on
// screen, its annotations or its unsaved-changes state. Each completed rescan
// replaces one local file per schedule, in the same JSON format as a saved
// scan, and is compared with the file it replaces using the inventory
// comparison of SC6 (internal/inventory), so "new" means what it means there.

// Event names for scheduled rescans. They are separate from the scan events so
// a scheduled rescan never disturbs the grid.
const (
	EventScheduleStatus = "schedule:status"
	EventScheduleNotice = "schedule:notice"
)

const (
	scheduleTickEvery  = 15 * time.Second
	scheduleStopWait   = 10 * time.Second
	noticeListed       = 5  // addresses named in a notification
	noticeAddressesMax = 50 // addresses carried in the in-app notice
	maxLabelSite       = 200
	maxLabelName       = 100
)

// ScheduleSettings is the scan plan a schedule uses: the same fields a manual
// scan request carries, captured from the technician's settings when the
// schedule is saved. SNMP is not part of it.
type ScheduleSettings struct {
	Mode        string      `json:"mode"`
	Ports       string      `json:"ports"`
	Probes      scan.Probes `json:"probes"`
	Concurrency int         `json:"concurrency"`
	TimeoutMs   int         `json:"timeout_ms"`
}

// ScheduleConfig is one schedule: a saved target set (client/site label, name
// and targets) with an interval and the scan plan.
type ScheduleConfig struct {
	Site            string           `json:"site"`
	Name            string           `json:"name"`
	Targets         string           `json:"targets"`
	IntervalMinutes int              `json:"interval_minutes"`
	DesktopToast    bool             `json:"desktop_toast"`
	Scan            ScheduleSettings `json:"scan"`
}

// ScheduleStatus is what the UI shows for one schedule.
type ScheduleStatus struct {
	Key             string `json:"key"`
	Site            string `json:"site"`
	Name            string `json:"name"`
	IntervalMinutes int    `json:"interval_minutes"`
	// Error is set when the schedule was refused; it is then not scheduled.
	Error      string `json:"error,omitempty"`
	NextRun    string `json:"next_run,omitempty"`
	Running    bool   `json:"running"`
	LastRun    string `json:"last_run,omitempty"`
	LastResult string `json:"last_result,omitempty"`
	LastError  string `json:"last_error,omitempty"`
	Skipped    int    `json:"skipped"`
	SkipReason string `json:"skip_reason,omitempty"`
	Folder     string `json:"folder,omitempty"`
}

// ScheduleNotice announces new devices found by a scheduled rescan. Text is
// the notification text: counts and addresses only. Device names are not part
// of it; the technician sees them in the comparison.
type ScheduleNotice struct {
	Key       string   `json:"key"`
	Site      string   `json:"site"`
	Name      string   `json:"name"`
	NewCount  int      `json:"new_count"`
	Addresses []string `json:"addresses"`
	Up        int      `json:"up"`
	At        string   `json:"at"`
	Title     string   `json:"title"`
	Text      string   `json:"text"`
}

type scheduleRun struct {
	at     time.Time
	result string
	err    string
}

type scheduleState struct {
	sched     *schedule.Scheduler
	now       func() time.Time
	dir       func() string
	run       func(context.Context, scan.Config, scan.Observer) scan.Summary
	tickEvery time.Duration

	cfgs    map[string]ScheduleConfig
	invalid map[string]ScheduleStatus
	runs    map[string]scheduleRun
	changes map[string]Comparison

	active bool
	key    string
	cancel context.CancelFunc
	done   chan struct{}
	loopOn bool
}

func (a *App) initSchedules() {
	a.sched = scheduleState{
		sched:     schedule.New(nil, nil),
		now:       time.Now,
		dir:       defaultScheduleDir,
		run:       scan.Run,
		tickEvery: scheduleTickEvery,
		cfgs:      map[string]ScheduleConfig{},
		invalid:   map[string]ScheduleStatus{},
		runs:      map[string]scheduleRun{},
		changes:   map[string]Comparison{},
	}
}

// defaultScheduleDir is where scheduled results are kept: a folder in the
// current user's local application data, which does not roam with the profile.
func defaultScheduleDir() string {
	base, err := os.UserCacheDir()
	if err != nil || base == "" {
		return ""
	}
	return filepath.Join(base, "ZTG Scanner", "scheduled")
}

func scheduleFile(dir, key string) string {
	sum := sha256.Sum256([]byte(key))
	return filepath.Join(dir, "rescan-"+hex.EncodeToString(sum[:16])+".json")
}

func checkLabel(value, field string, limit int) (string, error) {
	if strings.ContainsAny(value, "\r\n\x00") {
		return "", fmt.Errorf("the %s must be on one line without control characters", field)
	}
	v := strings.TrimSpace(value)
	if v == "" {
		return "", fmt.Errorf("enter a %s", field)
	}
	if len(v) > limit {
		return "", fmt.Errorf("the %s must be at most %d bytes", field, limit)
	}
	return v, nil
}

// scheduleRequest is the scan request a schedule runs. Public authorization
// and SNMP are never set: a schedule cannot carry either.
func scheduleRequest(cfg ScheduleConfig, specs []string) ScanRequest {
	probes := cfg.Scan.Probes
	probes.SNMP = false
	return ScanRequest{
		Mode: cfg.Scan.Mode, Targets: strings.Join(specs, ","), Ports: cfg.Scan.Ports, Probes: probes,
		Concurrency: cfg.Scan.Concurrency, TimeoutMs: cfg.Scan.TimeoutMs,
	}
}

// checkSchedule validates one schedule and returns it normalized, with its
// target specs. Nothing is resolved or sent.
func (a *App) checkSchedule(cfg ScheduleConfig) (ScheduleConfig, []string, error) {
	var err error
	if cfg.Site, err = checkLabel(cfg.Site, "client / site label", maxLabelSite); err != nil {
		return cfg, nil, err
	}
	if cfg.Name, err = checkLabel(cfg.Name, "set name", maxLabelName); err != nil {
		return cfg, nil, err
	}
	if _, err = schedule.ValidInterval(cfg.IntervalMinutes); err != nil {
		return cfg, nil, err
	}
	specs, err := schedule.CheckTargets(cfg.Targets)
	if err != nil {
		return cfg, nil, err
	}
	cfg.Targets = strings.Join(specs, ", ")
	// The plan is checked now so a bad mode or port list is refused when the
	// schedule is saved, not at three in the morning.
	if _, err = planFromRequest(scheduleRequest(cfg, specs), nil); err != nil {
		return cfg, nil, err
	}
	cfg.Scan.Probes.SNMP = false
	return cfg, specs, nil
}

// ValidateSchedule checks a schedule without installing it. The UI calls it so
// a refusal (a public range, a host name, an interval out of bounds) is shown
// before anything is saved.
func (a *App) ValidateSchedule(cfg ScheduleConfig) error {
	_, _, err := a.checkSchedule(cfg)
	return err
}

// SetSchedules installs the whole list of enabled schedules. Each is validated;
// a refused one is reported in its status and not scheduled. A schedule that is
// no longer in the list loses its result file, so enabling it again starts a
// fresh baseline. The list is the complete set: an empty list turns every
// schedule off.
func (a *App) SetSchedules(cfgs []ScheduleConfig) ([]ScheduleStatus, error) {
	if len(cfgs) > schedule.MaxSchedules {
		return nil, fmt.Errorf("you can schedule up to %d target sets", schedule.MaxSchedules)
	}
	valid := map[string]ScheduleConfig{}
	invalid := map[string]ScheduleStatus{}
	var items []schedule.Item
	for _, c := range cfgs {
		key := schedule.Key(strings.TrimSpace(c.Site), strings.TrimSpace(c.Name))
		norm, _, err := a.checkSchedule(c)
		if err == nil {
			if _, dup := valid[key]; dup {
				err = errors.New("this client and set name is scheduled twice")
			}
		}
		if err != nil {
			invalid[key] = ScheduleStatus{Key: key, Site: strings.TrimSpace(c.Site), Name: strings.TrimSpace(c.Name), IntervalMinutes: c.IntervalMinutes, Error: err.Error()}
			continue
		}
		valid[key] = norm
		items = append(items, schedule.Item{Key: key, Interval: time.Duration(norm.IntervalMinutes) * time.Minute})
	}
	a.mu.Lock()
	if err := a.sched.sched.Set(items); err != nil {
		a.mu.Unlock()
		return nil, err
	}
	dir := a.sched.dir()
	var drop []string
	for key := range a.sched.cfgs {
		if _, keep := valid[key]; !keep {
			drop = append(drop, key)
			delete(a.sched.runs, key)
			delete(a.sched.changes, key)
		}
	}
	a.sched.cfgs, a.sched.invalid = valid, invalid
	loop := len(valid) > 0
	a.mu.Unlock()
	if dir != "" {
		for _, key := range drop {
			_ = os.Remove(scheduleFile(dir, key))
		}
	}
	if loop {
		a.ensureScheduleLoop()
	}
	st := a.ScheduleStatuses()
	a.emit(EventScheduleStatus, st)
	return st, nil
}

// ScheduleStatuses lists every schedule the app was given, refused ones too.
func (a *App) ScheduleStatuses() []ScheduleStatus {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.statusesLocked()
}

func (a *App) statusesLocked() []ScheduleStatus {
	folder := a.sched.dir()
	out := []ScheduleStatus{}
	for key, cfg := range a.sched.cfgs {
		st := ScheduleStatus{Key: key, Site: cfg.Site, Name: cfg.Name, IntervalMinutes: cfg.IntervalMinutes, Folder: folder}
		if info, ok := a.sched.sched.Info(key); ok {
			st.NextRun = info.Next.UTC().Format(time.RFC3339)
			st.Running = info.Running
			st.Skipped = info.Skipped
			st.SkipReason = info.SkipReason
		}
		if r, ok := a.sched.runs[key]; ok {
			st.LastRun = r.at.UTC().Format(time.RFC3339)
			st.LastResult, st.LastError = r.result, r.err
		}
		out = append(out, st)
	}
	for _, st := range a.sched.invalid {
		out = append(out, st)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

// StopScheduled stops the scheduled rescan that is running, if any. Its
// partial result is discarded and its schedule resumes at the next cycle.
func (a *App) StopScheduled() {
	a.mu.Lock()
	cancel := a.sched.cancel
	a.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// Shutdown stops every scan the app runs. The shell calls it on exit.
func (a *App) Shutdown() {
	a.StopScan()
	a.StopScheduled()
}

// ScheduleChanges returns what the last rescan of a schedule found compared
// with the one before it, for the same comparison view a saved scan uses.
func (a *App) ScheduleChanges(key string) (Comparison, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	c, ok := a.sched.changes[key]
	if !ok {
		return Comparison{}, errors.New("there is no rescan comparison for that schedule yet")
	}
	return c, nil
}

// preemptScheduled stops a running scheduled rescan and waits for it, so a
// manual scan never overlaps one.
func (a *App) preemptScheduled() error {
	a.mu.Lock()
	if !a.sched.active {
		a.mu.Unlock()
		return nil
	}
	cancel, done := a.sched.cancel, a.sched.done
	a.mu.Unlock()
	cancel()
	select {
	case <-done:
		return nil
	case <-time.After(scheduleStopWait):
		return errors.New("a scheduled rescan is still stopping; try again in a moment")
	}
}

func (a *App) ensureScheduleLoop() {
	a.mu.Lock()
	if a.sched.loopOn || a.sched.tickEvery <= 0 {
		a.mu.Unlock()
		return
	}
	a.sched.loopOn = true
	ctx, every := a.ctx, a.sched.tickEvery
	a.mu.Unlock()
	go func() {
		t := time.NewTicker(every)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				a.scheduleTick()
			}
		}
	}()
}

// scheduleTick starts the next due rescan, if scanning is allowed now. It is
// called by the loop and, with a fake clock, by tests. It returns the key it
// started.
func (a *App) scheduleTick() (string, bool) {
	saving := a.platform != nil && a.platform.PowerSaving()
	a.mu.Lock()
	blocked := ""
	switch {
	case a.running:
		blocked = "a manual scan was running"
	case saving:
		blocked = "battery saver was on"
	}
	key, ok := a.sched.sched.Tick(blocked)
	if !ok {
		a.mu.Unlock()
		return "", false
	}
	cfg, found := a.sched.cfgs[key]
	if !found {
		a.sched.sched.Finish(key)
		a.mu.Unlock()
		return "", false
	}
	ctx, cancel := context.WithCancel(a.ctx)
	done := make(chan struct{})
	a.sched.active, a.sched.key, a.sched.cancel, a.sched.done = true, key, cancel, done
	a.mu.Unlock()
	a.emit(EventScheduleStatus, a.ScheduleStatuses())
	go a.runScheduled(ctx, cancel, key, cfg, done)
	return key, true
}

// schedObserver collects a scheduled rescan's hosts. The engine serializes its
// calls and Run returns after the last one.
type schedObserver struct{ hosts []scan.Host }

func (o *schedObserver) Host(h scan.Host)  { o.hosts = append(o.hosts, h) }
func (o *schedObserver) Progress(int, int) {}

func (a *App) runScheduled(ctx context.Context, cancel context.CancelFunc, key string, cfg ScheduleConfig, done chan struct{}) {
	defer close(done)
	defer cancel()
	res, notice, changes := a.executeScheduled(ctx, key, cfg)
	a.mu.Lock()
	a.sched.runs[key] = res
	if changes != nil {
		a.sched.changes[key] = *changes
	}
	a.sched.active, a.sched.key, a.sched.cancel = false, "", nil
	a.sched.sched.Finish(key)
	st := a.statusesLocked()
	a.mu.Unlock()
	a.emit(EventScheduleStatus, st)
	if notice != nil {
		if a.platform != nil {
			a.platform.Alert(notice.Title, notice.Text, cfg.DesktopToast)
		}
		a.emit(EventScheduleNotice, *notice)
	}
}

// executeScheduled runs one rescan and compares it with the previous result.
func (a *App) executeScheduled(ctx context.Context, key string, cfg ScheduleConfig) (scheduleRun, *ScheduleNotice, *Comparison) {
	run := scheduleRun{at: a.sched.now()}
	fail := func(err error) (scheduleRun, *ScheduleNotice, *Comparison) {
		run.err = err.Error()
		return run, nil, nil
	}
	dir := a.sched.dir()
	if dir == "" {
		return fail(errors.New("this computer has no local application data folder to keep results in"))
	}
	specs := schedule.Specs(cfg.Targets)
	// The scope guard runs again here, with public space refused: a schedule
	// cannot carry the fresh authorization a public range needs.
	addrs, err := targets.Expand(ctx, specs, targets.Options{AllowPublic: false})
	if err != nil {
		return fail(err)
	}
	if len(addrs) == 0 {
		return fail(errors.New("the target list is empty"))
	}
	plan, err := planFromRequest(scheduleRequest(cfg, specs), addrs)
	if err != nil {
		return fail(err)
	}
	obs := &schedObserver{}
	sum := a.sched.run(ctx, plan.Config(), obs)
	run.at = a.sched.now()
	if sum.Canceled || ctx.Err() != nil {
		run.result = "Stopped before it finished; the result was not kept"
		return run, nil, nil
	}
	hosts := obs.hosts
	report.SortHosts(hosts)

	path := scheduleFile(dir, key)
	prev, havePrev := a.readBaseline(path, cfg, specs, sum)
	doc := report.Document{Tool: "ztg-scanner-desktop", Version: version, Site: cfg.Site,
		Targets: specs, Summary: sum, Hosts: hosts, Annotations: map[string]scan.Annotation{}}
	var writeErr error
	if err := os.MkdirAll(dir, 0o700); err != nil {
		writeErr = err
	} else {
		writeErr = report.WriteFile(path, func(w io.Writer) error { return report.JSONBounded(w, doc, report.MaxDocumentBytes) })
	}
	if !havePrev {
		run.result = fmt.Sprintf("Baseline recorded: %d up. New devices are reported from the next rescan", sum.Up)
		if writeErr != nil {
			run.err = "could not keep the result: " + writeErr.Error()
		}
		return run, nil, nil
	}
	var newOnes []string
	var kept []inventory.Change
	for _, c := range inventory.Compare(prev.Hosts, hosts, false) {
		if c.Kind == "unchanged" {
			continue
		}
		kept = append(kept, c)
		if c.Kind == "new" && c.After != nil {
			newOnes = append(newOnes, c.After.IP)
		}
	}
	label := "Previous rescan of " + cfg.Site + " · " + cfg.Name
	if !prev.Summary.Finished.IsZero() {
		label += " (" + prev.Summary.Finished.Local().Format("2006-01-02 15:04") + ")"
	}
	cmp := &Comparison{Path: label, Changes: kept}
	run.result = fmt.Sprintf("%d up, %d new", sum.Up, len(newOnes))
	if writeErr != nil {
		run.err = "could not keep the result: " + writeErr.Error()
	}
	if len(newOnes) == 0 {
		return run, nil, cmp
	}
	title, body := schedule.NoticeText(newOnes, noticeListed)
	listed := make([]string, 0, len(newOnes))
	for _, ip := range newOnes {
		if len(listed) == noticeAddressesMax {
			break
		}
		if text, ok := schedule.NoticeAddress(ip); ok {
			listed = append(listed, text)
		}
	}
	return run, &ScheduleNotice{Key: key, Site: cfg.Site, Name: cfg.Name, NewCount: len(newOnes), Addresses: listed, Up: sum.Up,
		At: run.at.UTC().Format(time.RFC3339), Title: title, Text: body}, cmp
}

// readBaseline loads the previous result of this schedule when it can be
// compared with the new one: a completed scan of the same targets, in the same
// mode. Anything else (no file, a different target list, an unreadable file)
// makes this rescan the new baseline, which reports nothing.
func (a *App) readBaseline(path string, cfg ScheduleConfig, specs []string, sum scan.Summary) (report.Document, bool) {
	f, err := os.Open(path)
	if err != nil {
		return report.Document{}, false
	}
	defer f.Close()
	prev, err := report.Read(f)
	if err != nil || prev.Tool != "ztg-scanner-desktop" || prev.Summary.Canceled || prev.Summary.Finished.IsZero() ||
		prev.Summary.Mode != sum.Mode || !strings.EqualFold(prev.Site, cfg.Site) || strings.Join(prev.Targets, "\x00") != strings.Join(specs, "\x00") {
		return report.Document{}, false
	}
	return prev, true
}
