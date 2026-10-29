package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zerotrustgarage/tools/scanner/internal/scan"
	"github.com/zerotrustgarage/tools/scanner/internal/schedule"
)

// testClock is the injectable clock of the scheduling tests. Nothing here sleeps.
type testClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *testClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *testClock) advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

// schedEnv is an App whose scans are a fake: no packet leaves the test.
type schedEnv struct {
	t     *testing.T
	a     *App
	f     *fakePlatform
	clock *testClock
	dir   string

	mu      sync.Mutex
	hosts   []scan.Host   // what the next rescan "finds"
	configs []scan.Config // engine settings of every rescan that ran
	block   chan struct{} // when set, rescans wait for it or for cancellation
	started chan struct{} // signalled when a rescan has begun
}

func newSchedEnv(t *testing.T) *schedEnv {
	t.Helper()
	e := &schedEnv{t: t, clock: &testClock{t: time.Date(2026, 10, 29, 9, 0, 0, 0, time.UTC)}, dir: t.TempDir(), started: make(chan struct{}, 8)}
	e.a, e.f = NewApp(), newFake("")
	e.a.setPlatform(context.Background(), e.f)
	e.attach(e.a)
	return e
}

// attach wires an App to the fake scan, the fake clock and the results folder.
// A second App on the same folder stands for the app after a restart.
func (e *schedEnv) attach(a *App) {
	a.sched.tickEvery = 0 // tests call scheduleTick themselves
	a.sched.dir = func() string { return e.dir }
	a.sched.now = e.clock.now
	a.sched.sched = schedule.New(e.clock.now, func(time.Duration) time.Duration { return 0 })
	a.sched.run = e.fakeRun
}

func (e *schedEnv) fakeRun(ctx context.Context, cfg scan.Config, obs scan.Observer) scan.Summary {
	e.mu.Lock()
	e.configs = append(e.configs, cfg)
	hosts := append([]scan.Host(nil), e.hosts...)
	block := e.block
	e.mu.Unlock()
	e.started <- struct{}{}
	canceled := false
	if block != nil {
		select {
		case <-block:
		case <-ctx.Done():
			canceled = true
		}
	}
	up := 0
	for _, h := range hosts {
		obs.Host(h)
		if h.Status == "up" {
			up++
		}
	}
	return scan.Summary{Started: e.clock.now(), Finished: e.clock.now(), Total: len(cfg.Targets), Up: up, Mode: cfg.Mode, Canceled: canceled}
}

func (e *schedEnv) setHosts(hs ...scan.Host) {
	e.mu.Lock()
	e.hosts = hs
	e.mu.Unlock()
}

func (e *schedEnv) runs() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.configs)
}

func dev(ip, mac, name string) scan.Host {
	return scan.Host{IP: ip, MAC: mac, Name: name, Status: "up"}
}

func goodConfig() ScheduleConfig {
	return ScheduleConfig{Site: "Clinic", Name: "Main LAN", Targets: "10.0.0.0/24", IntervalMinutes: 30, DesktopToast: true,
		Scan: ScheduleSettings{Mode: "default", Probes: scan.Probes{Ports: true}}}
}

func (e *schedEnv) enable(cfgs ...ScheduleConfig) []ScheduleStatus {
	e.t.Helper()
	st, err := e.a.SetSchedules(cfgs)
	if err != nil {
		e.t.Fatal(err)
	}
	return st
}

// cycle moves the clock one interval, runs the due rescan and waits for it.
func (e *schedEnv) cycle() (string, bool) {
	e.t.Helper()
	e.clock.advance(30 * time.Minute)
	return e.tickAndWait()
}

func (e *schedEnv) tickAndWait() (string, bool) {
	e.t.Helper()
	key, ok := e.a.scheduleTick()
	if !ok {
		return "", false
	}
	e.a.mu.Lock()
	done := e.a.sched.done
	e.a.mu.Unlock()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		e.t.Fatal("scheduled rescan did not finish")
	}
	return key, true
}

func (e *schedEnv) status(key string) ScheduleStatus {
	for _, s := range e.a.ScheduleStatuses() {
		if s.Key == key {
			return s
		}
	}
	e.t.Fatalf("no status for %q", key)
	return ScheduleStatus{}
}

var clinicKey = schedule.Key("Clinic", "Main LAN")

func TestScheduleRefusesPublicRangesAndNames(t *testing.T) {
	e := newSchedEnv(t)
	for _, targets := range []string{"8.8.8.8", "203.0.113.0/28", "10.0.0.1, 1.1.1.1", "10.255.255.250-11.0.0.5", "printer.lan", "10.0.0.1, host-1"} {
		cfg := goodConfig()
		cfg.Targets = targets
		if err := e.a.ValidateSchedule(cfg); err == nil {
			t.Errorf("ValidateSchedule accepted %q", targets)
		}
		st := e.enable(cfg)
		if len(st) != 1 || st[0].Error == "" || st[0].NextRun != "" {
			t.Fatalf("%q: not refused: %+v", targets, st)
		}
		if strings.Contains(targets, "8.8.8.8") || strings.Contains(targets, "203.") || strings.Contains(targets, "1.1.1.1") || strings.Contains(targets, "11.") {
			if !strings.Contains(st[0].Error, "private and link-local") || !strings.Contains(st[0].Error, "fresh authorization") {
				t.Errorf("%q: refusal does not say why: %q", targets, st[0].Error)
			}
		}
	}
	// A refused schedule never runs, however long we wait.
	e.clock.advance(48 * time.Hour)
	if _, ok := e.a.scheduleTick(); ok || e.runs() != 0 {
		t.Fatal("a refused schedule ran")
	}
	// Private, link-local and loopback ranges are accepted.
	for _, targets := range []string{"10.0.0.0/24", "192.168.1.1-50", "169.254.10.0/28", "172.16.5.0/24"} {
		cfg := goodConfig()
		cfg.Targets = targets
		if st := e.enable(cfg); st[0].Error != "" || st[0].NextRun == "" {
			t.Errorf("%q refused: %+v", targets, st[0])
		}
	}
}

func TestScheduleRunTimeGuardRefusesPublicEvenIfInstalled(t *testing.T) {
	e := newSchedEnv(t)
	// Bypass SetSchedules to prove the scope guard runs again when a rescan starts.
	cfg := goodConfig()
	cfg.Targets = "8.8.8.0/29"
	e.a.sched.cfgs[clinicKey] = cfg
	if err := e.a.sched.sched.Set([]schedule.Item{{Key: clinicKey, Interval: 30 * time.Minute}}); err != nil {
		t.Fatal(err)
	}
	if _, ok := e.cycle(); !ok {
		t.Fatal("rescan did not start")
	}
	if e.runs() != 0 {
		t.Fatal("a public range was scanned")
	}
	if st := e.status(clinicKey); !strings.Contains(st.LastError, "public address space") {
		t.Fatalf("status %+v", st)
	}
	if len(e.f.alerts) != 0 {
		t.Fatal("alert for a refused rescan")
	}
}

func TestScheduleIntervalBoundsAndLimits(t *testing.T) {
	e := newSchedEnv(t)
	for _, minutes := range []int{0, 14, 1441, -5} {
		cfg := goodConfig()
		cfg.IntervalMinutes = minutes
		if st := e.enable(cfg); st[0].Error == "" {
			t.Errorf("%d minutes accepted", minutes)
		}
	}
	for _, minutes := range []int{15, 1440} {
		cfg := goodConfig()
		cfg.IntervalMinutes = minutes
		if st := e.enable(cfg); st[0].Error != "" {
			t.Errorf("%d minutes refused: %s", minutes, st[0].Error)
		}
	}
	bad := goodConfig()
	bad.Name = "bad\nname"
	if st := e.enable(bad); st[0].Error == "" {
		t.Error("a multi-line name was accepted")
	}
	dup := []ScheduleConfig{goodConfig(), goodConfig()}
	dup[1].Site = "CLINIC"
	st := e.enable(dup...)
	if len(st) != 2 || (st[0].Error == "") == (st[1].Error == "") {
		t.Errorf("a duplicate was not refused exactly once: %+v", st)
	}
	var many []ScheduleConfig
	for i := 0; i <= schedule.MaxSchedules; i++ {
		c := goodConfig()
		c.Name = "set " + string(rune('a'+i))
		many = append(many, c)
	}
	if _, err := e.a.SetSchedules(many); err == nil {
		t.Error("too many schedules accepted")
	}
}

func TestScheduleNotAStartupHook(t *testing.T) {
	// Enabling a schedule starts no scan and no goroutine work by itself: the first
	// rescan is one interval away, and with no app calling the tick nothing runs.
	e := newSchedEnv(t)
	st := e.enable(goodConfig())
	if e.runs() != 0 || st[0].NextRun == "" {
		t.Fatalf("enabling started a scan: %+v", st)
	}
	next, err := time.Parse(time.RFC3339, st[0].NextRun)
	if err != nil || next.Sub(e.clock.now()) != 30*time.Minute {
		t.Fatalf("first run %s is not one interval away", st[0].NextRun)
	}
	if _, ok := e.a.scheduleTick(); ok {
		t.Fatal("ran before it was due")
	}
	// Turning the schedule off stops it.
	e.enable()
	e.clock.advance(24 * time.Hour)
	if _, ok := e.a.scheduleTick(); ok || e.runs() != 0 {
		t.Fatal("a disabled schedule ran")
	}
}

func TestScheduledRescanReportsNewDevicesAgainstThePreviousResult(t *testing.T) {
	e := newSchedEnv(t)
	e.enable(goodConfig())
	a1, a2 := dev("10.0.0.1", "00:11:22:00:00:01", "gateway"), dev("10.0.0.2", "00:11:22:00:00:02", "nas")
	e.setHosts(a1, a2)

	// First rescan: a baseline, so it cannot report anything.
	if key, ok := e.cycle(); !ok || key != clinicKey {
		t.Fatal("first rescan did not run")
	}
	if len(e.f.alerts) != 0 || len(e.f.notice) != 0 {
		t.Fatal("the baseline notified")
	}
	if st := e.status(clinicKey); !strings.HasPrefix(st.LastResult, "Baseline recorded") || st.LastError != "" {
		t.Fatalf("status %+v", st)
	}
	if _, err := e.a.ScheduleChanges(clinicKey); err == nil {
		t.Fatal("a baseline has no comparison")
	}

	// Second rescan, same devices: nothing to report.
	e.cycle()
	if len(e.f.alerts) != 0 {
		t.Fatal("notified with no new device")
	}
	if st := e.status(clinicKey); st.LastResult != "2 up, 0 new" {
		t.Fatalf("status %+v", st)
	}

	// Third: one new device whose reported name is hostile, and a known device that changed.
	hostile := dev("10.0.0.50", "00:11:22:00:00:50", "<img src=x onerror=alert(1)>\nPrinter")
	a2.Name = "nas-renamed"
	e.setHosts(a1, a2, hostile)
	e.cycle()
	if len(e.f.alerts) != 1 || len(e.f.notice) != 1 {
		t.Fatalf("alerts %v notices %v", e.f.alerts, e.f.notice)
	}
	al := e.f.alerts[0]
	if al.title != "ZTG Scanner: 1 new device" || al.body != "10.0.0.50" || !al.toast {
		t.Fatalf("alert %+v", al)
	}
	n := e.f.notice[0]
	if n.NewCount != 1 || len(n.Addresses) != 1 || n.Addresses[0] != "10.0.0.50" || n.Up != 3 || n.Site != "Clinic" || n.Name != "Main LAN" {
		t.Fatalf("notice %+v", n)
	}
	for _, text := range []string{al.title, al.body, n.Title, n.Text} {
		if strings.ContainsAny(text, "<>\n") || strings.Contains(text, "Printer") || strings.Contains(text, "renamed") {
			t.Fatalf("device-reported text reached the notification: %q", text)
		}
	}
	cmp, err := e.a.ScheduleChanges(clinicKey)
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[string]string{}
	for _, c := range cmp.Changes {
		h := c.After
		kinds[h.IP] = c.Kind
	}
	if len(cmp.Changes) != 2 || kinds["10.0.0.50"] != "new" || kinds["10.0.0.2"] != "changed" {
		t.Fatalf("comparison %+v", cmp.Changes)
	}
	if !strings.HasPrefix(cmp.Path, "Previous rescan of Clinic") {
		t.Fatalf("comparison label %q", cmp.Path)
	}

	// The device is now part of the baseline: the next rescan does not announce it again.
	e.cycle()
	if len(e.f.alerts) != 1 {
		t.Fatal("the same device was announced twice")
	}
}

func TestScheduledNewDeviceUsesSC6IdentityRules(t *testing.T) {
	e := newSchedEnv(t)
	e.enable(goodConfig())
	mover := dev("10.0.0.1", "00:11:22:00:00:01", "laptop")
	swapped := dev("10.0.0.2", "00:11:22:00:00:02", "printer")
	e.setHosts(mover, swapped)
	e.cycle()

	// A DHCP move of the same MAC, and another device at an address that was occupied, are
	// "changed" in SC6, not "new".
	moved := mover
	moved.IP = "10.0.0.99"
	replaced := dev("10.0.0.2", "00:11:22:00:00:77", "printer")
	e.setHosts(moved, replaced)
	e.cycle()
	if len(e.f.alerts) != 0 {
		t.Fatalf("a moved or replaced device was reported as new: %+v", e.f.alerts)
	}

	// A randomized-MAC device is tied to its address; one at a new address is new.
	phone := dev("10.0.0.120", "da:a1:19:00:00:01", "phone")
	phone.MACRandomized = true
	e.setHosts(moved, replaced, phone)
	e.cycle()
	if len(e.f.alerts) != 1 || e.f.alerts[0].body != "10.0.0.120" {
		t.Fatalf("alerts %+v", e.f.alerts)
	}
	phoneMoved := phone
	phoneMoved.IP = "10.0.0.121"
	e.setHosts(moved, replaced, phoneMoved)
	e.cycle()
	if len(e.f.alerts) != 2 || e.f.alerts[1].body != "10.0.0.121" {
		t.Fatalf("a randomized-MAC device that changed address is a new device: %+v", e.f.alerts)
	}
}

func TestScheduledNotificationNamesFiveAddressesAndTheCount(t *testing.T) {
	e := newSchedEnv(t)
	cfg := goodConfig()
	cfg.DesktopToast = false
	e.enable(cfg)
	e.setHosts(dev("10.0.0.1", "00:11:22:00:00:01", "a"))
	e.cycle()
	hs := []scan.Host{dev("10.0.0.1", "00:11:22:00:00:01", "a")}
	for i := 10; i < 17; i++ {
		hs = append(hs, dev(fmt.Sprintf("10.0.0.%d", i), fmt.Sprintf("00:11:22:00:01:%02d", i), "n"))
	}
	e.setHosts(hs...)
	e.cycle()
	if len(e.f.alerts) != 1 {
		t.Fatalf("alerts %+v", e.f.alerts)
	}
	al := e.f.alerts[0]
	if al.title != "ZTG Scanner: 7 new devices" || al.body != "10.0.0.10, 10.0.0.11, 10.0.0.12, 10.0.0.13, 10.0.0.14 and 2 more" {
		t.Fatalf("alert %+v", al)
	}
	if al.toast {
		t.Fatal("toast requested although the schedule turned it off")
	}
	if n := e.f.notice[0]; n.NewCount != 7 || len(n.Addresses) != 7 {
		t.Fatalf("notice %+v", n)
	}
}

func TestScheduledResultsSurviveARestartAndStayLocal(t *testing.T) {
	e := newSchedEnv(t)
	e.enable(goodConfig())
	known := dev("10.0.0.1", "00:11:22:00:00:01", "gateway")
	e.setHosts(known)
	e.cycle() // baseline, written to the folder

	files, _ := filepath.Glob(filepath.Join(e.dir, "*"))
	if len(files) != 1 || !strings.HasSuffix(files[0], ".json") {
		t.Fatalf("results folder holds %v", files)
	}
	if strings.Contains(filepath.Base(files[0]), "Clinic") {
		t.Fatal("the file name leaks the client label")
	}
	raw, err := os.ReadFile(files[0])
	if err != nil || !strings.Contains(string(raw), `"tool": "ztg-scanner-desktop"`) {
		t.Fatalf("not a Scanner inventory file: %v", err)
	}
	// It is a normal inventory file: the app's own reader accepts it.
	if f, err := os.Open(files[0]); err != nil {
		t.Fatal(err)
	} else {
		defer f.Close()
	}

	// "Restart": a new App, the same folder. Its first rescan compares with the saved result.
	b, fb := NewApp(), newFake("")
	b.setPlatform(context.Background(), fb)
	e.a, e.f = b, fb
	e.attach(b)
	if _, err := b.SetSchedules([]ScheduleConfig{goodConfig()}); err != nil {
		t.Fatal(err)
	}
	e.setHosts(known, dev("10.0.0.30", "00:11:22:00:00:30", "new thing"))
	e.cycle()
	if len(fb.alerts) != 1 || fb.alerts[0].body != "10.0.0.30" {
		t.Fatalf("the restarted app did not compare with the saved result: %+v", fb.alerts)
	}
}

func TestScheduledBaselineResetsWhenTheTargetsOrModeChange(t *testing.T) {
	e := newSchedEnv(t)
	e.enable(goodConfig())
	known := dev("10.0.0.1", "00:11:22:00:00:01", "gateway")
	e.setHosts(known)
	e.cycle()

	// The set now covers more addresses. Devices in the new part are not "new devices": the first
	// rescan of a changed set is a new baseline.
	wider := goodConfig()
	wider.Targets = "10.0.0.0/24, 10.0.1.0/24"
	e.enable(wider)
	e.setHosts(known, dev("10.0.1.5", "00:11:22:00:01:05", "other subnet"))
	e.cycle()
	if len(e.f.alerts) != 0 || !strings.HasPrefix(e.status(clinicKey).LastResult, "Baseline recorded") {
		t.Fatalf("a wider target list notified: %+v %+v", e.f.alerts, e.status(clinicKey))
	}
	// And the same for a different scan mode, which identifies devices differently.
	gentle := wider
	gentle.Scan.Mode = "gentle"
	e.enable(gentle)
	e.setHosts(known, dev("10.0.1.5", "00:11:22:00:01:05", "other subnet"), dev("10.0.1.6", "00:11:22:00:01:06", "x"))
	e.cycle()
	if len(e.f.alerts) != 0 {
		t.Fatalf("a mode change notified: %+v", e.f.alerts)
	}
	e.setHosts(known, dev("10.0.1.5", "00:11:22:00:01:05", "other subnet"), dev("10.0.1.6", "00:11:22:00:01:06", "x"), dev("10.0.1.7", "00:11:22:00:01:07", "y"))
	e.cycle()
	if len(e.f.alerts) != 1 {
		t.Fatalf("expected one alert after the baseline settled: %+v", e.f.alerts)
	}
}

func TestScheduledCanceledRescanIsDiscarded(t *testing.T) {
	e := newSchedEnv(t)
	e.enable(goodConfig())
	known := dev("10.0.0.1", "00:11:22:00:00:01", "gateway")
	e.setHosts(known)
	e.cycle()
	before, _ := os.ReadFile(scheduleFile(e.dir, clinicKey))

	e.block = make(chan struct{})
	e.setHosts(known, dev("10.0.0.40", "00:11:22:00:00:40", "partial"))
	e.clock.advance(30 * time.Minute)
	if _, ok := e.a.scheduleTick(); !ok {
		t.Fatal("did not start")
	}
	<-e.started // the baseline run's signal
	<-e.started
	e.a.StopScheduled()
	e.a.mu.Lock()
	done := e.a.sched.done
	e.a.mu.Unlock()
	<-done
	after, _ := os.ReadFile(scheduleFile(e.dir, clinicKey))
	if string(before) != string(after) {
		t.Fatal("a stopped rescan replaced the previous result")
	}
	if len(e.f.alerts) != 0 {
		t.Fatal("a stopped rescan notified")
	}
	if st := e.status(clinicKey); !strings.HasPrefix(st.LastResult, "Stopped") || st.Running {
		t.Fatalf("status %+v", st)
	}
}

func TestScheduledRescanSkipsWhileAManualScanRuns(t *testing.T) {
	e := newSchedEnv(t)
	e.enable(goodConfig())
	e.setHosts(dev("10.0.0.1", "00:11:22:00:00:01", "gateway"))

	e.a.mu.Lock()
	e.a.running = true // a manual scan is in progress
	e.a.mu.Unlock()
	e.clock.advance(31 * time.Minute)
	if _, ok := e.a.scheduleTick(); ok {
		t.Fatal("a scheduled rescan started during a manual scan")
	}
	st := e.status(clinicKey)
	if st.Skipped != 1 || st.SkipReason != "a manual scan was running" {
		t.Fatalf("skip not reported: %+v", st)
	}
	e.a.mu.Lock()
	e.a.running = false
	e.a.mu.Unlock()
	// The skipped cycle is not made up: the next one is a full interval away.
	if _, ok := e.a.scheduleTick(); ok {
		t.Fatal("the skipped cycle ran after the manual scan ended")
	}
	e.clock.advance(30 * time.Minute)
	if _, ok := e.tickAndWait(); !ok {
		t.Fatal("the next cycle did not run")
	}
	if e.runs() != 1 {
		t.Fatalf("runs = %d", e.runs())
	}
}

func TestScheduledRescanSkipsWhileBatterySaverIsOn(t *testing.T) {
	e := newSchedEnv(t)
	e.enable(goodConfig())
	e.f.mu.Lock()
	e.f.saving = true
	e.f.mu.Unlock()
	e.clock.advance(31 * time.Minute)
	if _, ok := e.a.scheduleTick(); ok || e.runs() != 0 {
		t.Fatal("ran in battery saver")
	}
	if st := e.status(clinicKey); st.Skipped != 1 || st.SkipReason != "battery saver was on" {
		t.Fatalf("status %+v", st)
	}
}

func TestScheduledRescansNeverOverlap(t *testing.T) {
	e := newSchedEnv(t)
	other := goodConfig()
	other.Name = "Guest LAN"
	other.Targets = "10.0.2.0/24"
	e.enable(goodConfig(), other)
	e.block = make(chan struct{})
	e.setHosts(dev("10.0.0.1", "00:11:22:00:00:01", "gateway"))
	e.clock.advance(31 * time.Minute) // both due
	if _, ok := e.a.scheduleTick(); !ok {
		t.Fatal("nothing started")
	}
	<-e.started
	if key, ok := e.a.scheduleTick(); ok {
		t.Fatalf("%q started while another scheduled rescan was running", key)
	}
	if e.runs() != 1 {
		t.Fatalf("runs = %d", e.runs())
	}
	close(e.block)
	e.a.mu.Lock()
	done := e.a.sched.done
	e.a.mu.Unlock()
	<-done
	// The one that had to wait is deferred, not skipped, and runs next.
	if _, ok := e.tickAndWait(); !ok {
		t.Fatal("the waiting schedule did not run")
	}
	for _, s := range e.a.ScheduleStatuses() {
		if s.Skipped != 0 {
			t.Fatalf("a deferred schedule was counted as skipped: %+v", s)
		}
	}
}

func TestManualScanStopsAScheduledRescanAndNeverOverlapsIt(t *testing.T) {
	e := newSchedEnv(t)
	e.enable(goodConfig())
	e.block = make(chan struct{})
	e.setHosts(dev("10.0.0.1", "00:11:22:00:00:01", "gateway"))
	e.clock.advance(31 * time.Minute)
	if _, ok := e.a.scheduleTick(); !ok {
		t.Fatal("did not start")
	}
	<-e.started

	srv, port := webServer(t)
	defer srv.Close()
	// A manual scan of this machine (loopback) while the rescan is mid-run.
	sum := runScan(t, e.a, e.f, ScanRequest{Targets: "127.0.0.1", Ports: port, Probes: scan.Probes{Ports: true}})
	if sum.Up != 1 {
		t.Fatalf("manual scan: %+v", sum)
	}
	e.a.mu.Lock()
	active := e.a.sched.active
	e.a.mu.Unlock()
	if active {
		t.Fatal("the scheduled rescan was still active")
	}
	if st := e.status(clinicKey); !strings.HasPrefix(st.LastResult, "Stopped") {
		t.Fatalf("status %+v", st)
	}
	if len(e.f.alerts) != 0 {
		t.Fatal("alert from a rescan that was stopped")
	}
}

func TestScheduledRescanLeavesTheOpenInventoryAlone(t *testing.T) {
	e := newSchedEnv(t)
	e.enable(goodConfig())
	mine := scan.Host{IP: "10.9.9.9", Status: "up", MAC: "00:aa:bb:cc:dd:ee"}
	e.a.hosts = []scan.Host{mine}
	e.a.lastReq = []string{"10.9.9.0/24"}
	e.a.site = "Another client"
	e.setHosts(dev("10.0.0.1", "00:11:22:00:00:01", "gateway"))
	e.cycle()
	e.cycle()
	if len(e.a.hosts) != 1 || e.a.hosts[0].IP != "10.9.9.9" || e.a.site != "Another client" || e.a.lastReq[0] != "10.9.9.0/24" {
		t.Fatal("a scheduled rescan changed the inventory on screen")
	}
	if e.a.UnsavedChanges() {
		t.Fatal("a scheduled rescan marked the inventory unsaved")
	}
	if e.f.events[EventHost] != 0 || e.f.events[EventDone] != 0 || e.f.events[EventProgress] != 0 {
		t.Fatalf("scan events reached the grid: %v", e.f.events)
	}
}

func TestScheduledRescanUsesTheScanPlanAndNeverSNMP(t *testing.T) {
	e := newSchedEnv(t)
	cfg := goodConfig()
	cfg.Scan = ScheduleSettings{Mode: "gentle", Probes: scan.Probes{Ports: true, ReverseDNS: true}}
	e.enable(cfg)
	e.setHosts(dev("10.0.0.1", "00:11:22:00:00:01", "gateway"))
	e.cycle()
	got := e.configs[0]
	if got.Mode != scan.ModeGentle || got.Pacer == nil || got.Concurrency != scan.GentleConcurrency || got.SNMP != nil || got.Probes.SNMP {
		t.Fatalf("gentle plan not used: %+v", got)
	}
	if len(got.Targets) != 254 {
		t.Fatalf("targets %d", len(got.Targets))
	}

	// A default-mode schedule that was saved with SNMP switched on still never uses it.
	d := goodConfig()
	d.Scan = ScheduleSettings{Mode: "default", Probes: scan.Probes{Ports: true, SNMP: true}, Concurrency: 64}
	e.enable(d)
	e.cycle()
	got = e.configs[1]
	if got.Mode == scan.ModeGentle || got.SNMP != nil || got.Probes.SNMP || got.Concurrency != 64 {
		t.Fatalf("default plan: %+v", got)
	}
	// Settings the plan refuses are refused when the schedule is saved.
	bad := goodConfig()
	bad.Scan = ScheduleSettings{Mode: "gentle", Ports: "23", Probes: scan.Probes{Ports: true}}
	if st := e.enable(bad); st[0].Error == "" {
		t.Error("a gentle schedule with a port outside the gentle list was accepted")
	}
	bad.Scan = ScheduleSettings{Mode: "turbo"}
	if st := e.enable(bad); st[0].Error == "" {
		t.Error("an unknown mode was accepted")
	}
}

func TestDisablingAScheduleRemovesItsResultFile(t *testing.T) {
	e := newSchedEnv(t)
	e.enable(goodConfig())
	e.setHosts(dev("10.0.0.1", "00:11:22:00:00:01", "gateway"))
	e.cycle()
	if _, err := os.Stat(scheduleFile(e.dir, clinicKey)); err != nil {
		t.Fatal(err)
	}
	if st := e.enable(); len(st) != 0 {
		t.Fatalf("statuses after disabling: %+v", st)
	}
	if _, err := os.Stat(scheduleFile(e.dir, clinicKey)); !os.IsNotExist(err) {
		t.Fatal("the result file outlived its schedule")
	}
	// Enabling it again starts over from a baseline.
	e.enable(goodConfig())
	e.cycle()
	if len(e.f.alerts) != 0 || !strings.HasPrefix(e.status(clinicKey).LastResult, "Baseline recorded") {
		t.Fatal("re-enabled schedule did not start from a baseline")
	}
}

func TestScheduleStatusesCarryTheFolderForTheUI(t *testing.T) {
	e := newSchedEnv(t)
	st := e.enable(goodConfig())
	if st[0].Folder != e.dir || st[0].Key != clinicKey || st[0].IntervalMinutes != 30 {
		t.Fatalf("%+v", st[0])
	}
	if len(e.f.status) == 0 {
		t.Fatal("no status event")
	}
}
