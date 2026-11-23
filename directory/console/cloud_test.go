package console

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zerotrustgarage/tools/directory/engine/ad"
	"github.com/zerotrustgarage/tools/directory/engine/auth"
	"github.com/zerotrustgarage/tools/directory/engine/demo"
	"github.com/zerotrustgarage/tools/directory/engine/dirsync"
	"github.com/zerotrustgarage/tools/directory/engine/m365"
	"github.com/zerotrustgarage/tools/directory/engine/mirror"
	"github.com/zerotrustgarage/tools/directory/engine/record"
)

func dnOf(t *testing.T, s *Service, text string) string {
	t.Helper()
	res, err := s.Search(SearchQuery{Text: text, Class: "user"})
	if err != nil || len(res.Rows) != 1 {
		t.Fatalf("find %s: %+v %v", text, res.Rows, err)
	}
	return res.Rows[0].DN
}

func rowStatus(v mirror.View, attr string) string {
	for _, r := range v.Rows {
		if r.Attribute == attr {
			return r.Status
		}
	}
	return ""
}

func TestDemoCloudStatusAndSignIn(t *testing.T) {
	s, _ := newDemo(t)
	a := NewAPI(s, nil)
	if cs := a.CloudStatus(); !cs.Configured || cs.SignedIn || !cs.SyncReady || cs.SyncKind != "connect" {
		t.Fatalf("before sign-in: %+v", cs)
	}
	if _, err := a.CloudSignIn(); err == nil {
		t.Fatal("cloud sign-in allowed without an AD session")
	}
	if _, err := a.Mirror("CN=x"); err == nil {
		t.Fatal("mirror allowed before sign-in")
	}
	s.SignIn(SignInRequest{User: "ztg.tech", Password: DemoPassword})
	if cs := a.CloudStatus(); !cs.SignedIn || cs.Account != demo.CloudAccount {
		t.Fatalf("demo cloud sign-in: %+v", cs)
	}
	bea := dnOf(t, s, "okafor")
	v, _ := s.Object(bea)
	if !strings.Contains(v.CloudNote, "Signed in to Microsoft 365 as "+demo.CloudAccount) {
		t.Fatalf("cloud note: %q", v.CloudNote)
	}
	if cs := a.CloudSignOut(); cs.SignedIn {
		t.Fatalf("cloud sign-out: %+v", cs)
	}
	if _, err := a.Mirror(bea); err == nil || !strings.Contains(err.Error(), "sign in to Microsoft 365") {
		t.Fatalf("mirror after cloud sign-out: %v", err)
	}
	if v, _ = s.Object(bea); !strings.Contains(v.CloudNote, "not signed in") {
		t.Fatalf("cloud note signed out: %q", v.CloudNote)
	}
	if cs, err := a.CloudSignIn(); err != nil || !cs.SignedIn {
		t.Fatalf("cloud sign-in: %+v %v", cs, err)
	}
	recs, _ := s.Records(RecordQuery{})
	var notes []string
	for _, r := range recs {
		if strings.Contains(r.Note, "Microsoft 365") {
			notes = append(notes, r.Kind+":"+r.Actor)
		}
	}
	if strings.Join(notes, ",") != "sign-in:"+demo.CloudAccount+",sign-out:"+demo.CloudAccount {
		t.Fatalf("cloud sign-in records: %v", notes)
	}
	s.Lock()
	if _, err := a.Mirror(bea); err == nil {
		t.Fatal("mirror while locked")
	}
	s.Unlock(DemoPassword)
	s.SignOut()
	if cs := a.CloudStatus(); cs.SignedIn {
		t.Fatal("AD sign-out must end the Microsoft 365 sign-in")
	}
}

func TestDemoMirrorScenarios(t *testing.T) {
	s, _ := newDemo(t)
	s.SignIn(SignInRequest{User: "ztg.tech", Password: DemoPassword})
	a := NewAPI(s, nil)
	v, err := a.Mirror(dnOf(t, s, "Elio"))
	if err != nil || v.Match != mirror.MatchAnchor || rowStatus(v, "title") != mirror.StatusOutOfStep {
		t.Fatalf("out of step: %+v %v", v, err)
	}
	if rowStatus(v, "manager") != mirror.StatusMatch {
		t.Fatalf("manager resolved through AD: %+v", v.Rows)
	}
	if v, _ = a.Mirror(dnOf(t, s, "Ivan")); rowStatus(v, "title") != mirror.StatusWaiting {
		t.Fatalf("waiting: %+v", v.Rows)
	}
	if v, _ = a.Mirror(dnOf(t, s, "Farah")); v.Match != mirror.MatchPossible {
		t.Fatalf("possible match: %+v", v)
	}
	if v, _ = a.Mirror(dnOf(t, s, "Riley")); v.Match != mirror.MatchNone || v.Hint == "" {
		t.Fatalf("Tier 0 not synced: %+v", v)
	}
	h, err := a.SyncHealth()
	if err != nil || h.Kind != dirsync.KindConnect || h.Server == "" || h.LastCycle == nil {
		t.Fatalf("health: %+v %v", h, err)
	}
}

func TestDemoSyncNowClearsADifference(t *testing.T) {
	s, b := newDemo(t)
	s.SignIn(SignInRequest{User: "ztg.tech", Password: DemoPassword})
	a := NewAPI(s, nil)
	bea := dnOf(t, s, "okafor")
	v, _ := a.Mirror(bea)
	if v.Differences != 0 {
		t.Fatalf("Bea should start in step: %+v", v.Rows)
	}
	pv, err := s.PlanEdits(EditRequest{Edits: []Edit{{Kind: "set", DN: bea, Attribute: "title", Values: []string{"Senior Accountant"}}}})
	if err != nil {
		t.Fatal(err)
	}
	if out, err := s.Apply(ApplyRequest{PlanID: pv.ID, Hash: pv.Hash}); err != nil || !out.OK {
		t.Fatalf("apply: %+v %v", out, err)
	}
	v, _ = a.Mirror(bea)
	if rowStatus(v, "title") != mirror.StatusWaiting || v.Waiting != 1 {
		t.Fatalf("after the AD edit: %+v", v.Rows)
	}
	before := v.User.LastSync
	res, err := a.SyncNow(bea)
	if err != nil || !res.Watch || res.LastSyncBefore == "" || res.Kind != "connect" || res.Message == "" || res.PollSeconds <= 0 {
		t.Fatalf("sync now: %+v %v", res, err)
	}
	v, _ = a.Mirror(bea)
	if v.Differences != 0 || v.User.LastSync <= before {
		t.Fatalf("after sync now: last sync %s (before %s), %+v", v.User.LastSync, before, v.Rows)
	}
	recs, _ := s.Records(RecordQuery{})
	var sync *record.Entry
	for i := range recs {
		if recs[i].Kind == record.KindSync {
			sync = &recs[i]
			break
		}
	}
	if sync == nil || sync.Technician != `LAB\ztg.tech` || sync.DC != "dc01.lab.example" || sync.DN != bea || sync.Result != "started" || sync.Method != "connect" {
		t.Fatalf("sync record: %+v", sync)
	}
	// A failed sync is recorded too.
	b.syncer.FailErr = dirsync.ErrBusy
	if _, err := a.SyncNow(bea); !errors.Is(err, dirsync.ErrBusy) {
		t.Fatalf("failed sync: %v", err)
	}
	recs, _ = s.Records(RecordQuery{})
	if recs[0].Kind != record.KindSync || recs[0].Result != "failed" || recs[0].Error == "" {
		t.Fatalf("failed sync record: %+v", recs[0])
	}
	if probs, _ := s.VerifyRecords(); len(probs) != 0 {
		t.Fatalf("records chain: %v", probs)
	}
}

func TestDemoForbiddenSectionsBecomeNotes(t *testing.T) {
	s, b := newDemo(t)
	s.SignIn(SignInRequest{User: "ztg.tech", Password: DemoPassword})
	b.DemoTenant().Forbid = map[string]bool{"UserDevices": true}
	v, err := s.Mirror(dnOf(t, s, "okafor"))
	if err != nil || v.DevicesNote == "" || len(v.Groups) == 0 {
		t.Fatalf("forbidden devices: %+v %v", v, err)
	}
}

func liveWithCloud(t *testing.T, c CloudSettings, w CloudWiring) (*Service, *LiveBackend, *bool) {
	t.Helper()
	dir := demo.NewDirectory()
	st := auth.DefaultSettings()
	st.Domain, st.AllowPasswordOnly = "lab.example", true
	open := func(ctx context.Context, cfg ad.Config) (ad.Writer, error) {
		if cfg.Credential == nil || string(cfg.Credential.Password) != "Right-Pass-1" {
			return nil, ad.ErrInvalidCredentials
		}
		dir.Account = cfg.Credential.Account()
		return dir, nil
	}
	b, err := NewLiveBackend(st, open)
	if err != nil {
		t.Fatal(err)
	}
	if err := b.UseCloud(c, w); err != nil {
		t.Fatal(err)
	}
	ended := new(bool)
	tenant := demo.NewTenant(dir)
	b.cloudSignIn = func(ctx context.Context, s m365.Settings, w CloudWiring) (m365.Tenant, func(), error) {
		if s.TenantID != "contoso-labs.example" {
			t.Errorf("settings not passed: %+v", s)
		}
		*ended = false
		return tenant, func() { *ended = true }, nil
	}
	svc, err := New(b, Config{DataDir: t.TempDir(), Device: "ws"})
	if err != nil {
		t.Fatal(err)
	}
	return svc, b, ended
}

// fakeRunner stands in for PowerShell remoting to the sync server.
type fakeRunner struct{ calls int }

func (r *fakeRunner) Run(context.Context, string, []byte) ([]byte, error) {
	r.calls++
	return nil, errors.New("remoting refused in tests")
}

// graphTenant is a fake tenant that is also a Graph client, as *m365.Graph is.
type graphTenant struct {
	*m365.FakeTenant
	calls int
}

func (g *graphTenant) Do(ctx context.Context, method, path string, body, out any) error {
	g.calls++
	return m365.ErrForbidden
}

func TestLiveCloudWithConnectSync(t *testing.T) {
	cfg := CloudSettings{M365: &m365.Settings{TenantID: "contoso-labs.example", ClientID: "11111111-2222-3333-4444-555555555555"},
		Sync: SyncSettings{Kind: dirsync.KindConnect, Server: "srv-entra01.lab.example"}}
	runner := &fakeRunner{}
	s, b, ended := liveWithCloud(t, cfg, CloudWiring{SyncRunner: runner})
	cs := s.CloudStatus()
	if !cs.Configured || cs.SignedIn || !cs.SyncReady || cs.Cloud != "commercial" || !strings.Contains(cs.Note, "srv-entra01.lab.example") {
		t.Fatalf("live status: %+v", cs)
	}
	if _, err := s.CloudSignIn(); err == nil {
		t.Fatal("cloud sign-in without an AD session")
	}
	if _, err := s.SignIn(SignInRequest{User: `LAB\ztg.tech`, Password: "Right-Pass-1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Tenant(); !errors.Is(err, m365.ErrNotSignedIn) {
		t.Fatalf("tenant before cloud sign-in: %v", err)
	}
	if cs, err := s.CloudSignIn(); err != nil || !cs.SignedIn || cs.Account != demo.CloudAccount {
		t.Fatalf("live cloud sign-in: %+v %v", cs, err)
	}
	elio := "CN=Elio Brandt,OU=Engineering,OU=Staff,OU=Contoso Labs," + demo.Base
	v, err := s.Mirror(elio)
	if err != nil || rowStatus(v, "title") != mirror.StatusOutOfStep {
		t.Fatalf("live mirror: %+v %v", v, err)
	}
	sy, err := b.Syncer()
	if err != nil || sy.Kind() != dirsync.KindConnect {
		t.Fatalf("connect syncer: %v %v", sy, err)
	}
	// Sync now goes through the runner as the AD account; its failure is
	// returned and recorded.
	if _, err := s.SyncNow(elio); err == nil || runner.calls == 0 {
		t.Fatalf("sync now through the runner: %v (%d calls)", err, runner.calls)
	}
	recs, _ := s.Records(RecordQuery{})
	if recs[0].Kind != record.KindSync || recs[0].Result != "failed" || recs[0].Technician != `LAB\ztg.tech` {
		t.Fatalf("failed sync record: %+v", recs[0])
	}
	if _, err := s.SyncHealth(); err == nil {
		t.Fatal("health should report the refused remoting")
	}
	s.SignOut()
	if !*ended || s.CloudStatus().SignedIn {
		t.Fatal("AD sign-out must end the Microsoft 365 sign-in")
	}
	if _, err := b.Syncer(); err == nil {
		t.Fatal("syncer after sign-out")
	}
}

func TestLiveCloudSyncNeedsGraph(t *testing.T) {
	cfg := CloudSettings{M365: &m365.Settings{TenantID: "contoso-labs.example", ClientID: "11111111-2222-3333-4444-555555555555"},
		Sync: SyncSettings{Kind: dirsync.KindCloud}}
	s, b, _ := liveWithCloud(t, cfg, CloudWiring{SyncRunner: &fakeRunner{}})
	if !b.cloud.M365.CloudSync || cfg.M365.CloudSync {
		t.Fatal("Cloud Sync must request its permissions without changing the caller's settings")
	}
	if _, err := s.SignIn(SignInRequest{User: `LAB\ztg.tech`, Password: "Right-Pass-1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Syncer(); err == nil || !strings.Contains(err.Error(), "sign in to Microsoft 365") {
		t.Fatalf("cloud sync before cloud sign-in: %v", err)
	}
	if s.CloudStatus().SyncReady {
		t.Fatal("Cloud Sync ready without a sign-in")
	}
	// A tenant that is not a Graph client (the fake) cannot drive Cloud Sync.
	s.CloudSignIn()
	if _, err := b.Syncer(); err == nil || !strings.Contains(err.Error(), "Graph") {
		t.Fatalf("cloud sync without Graph: %v", err)
	}
	g := &graphTenant{FakeTenant: m365.NewFakeTenant(demo.CloudAccount)}
	b.cloudSignIn = func(context.Context, m365.Settings, CloudWiring) (m365.Tenant, func(), error) { return g, nil, nil }
	if _, err := s.CloudSignIn(); err != nil {
		t.Fatal(err)
	}
	sy, err := b.Syncer()
	if err != nil || sy.Kind() != dirsync.KindCloud || !s.CloudStatus().SyncReady {
		t.Fatalf("cloud syncer: %v %v", sy, err)
	}
	if _, err := s.SyncHealth(); err == nil || g.calls == 0 {
		t.Fatalf("cloud health through Graph: %v (%d calls)", err, g.calls)
	}
	// No sync method configured.
	b.UseCloud(CloudSettings{}, CloudWiring{})
	if _, err := b.Syncer(); !errors.Is(err, dirsync.ErrNotConfigured) {
		t.Fatalf("unconfigured sync: %v", err)
	}
	if cs := s.CloudStatus(); cs.Configured || !strings.Contains(cs.Note, "not set up") {
		t.Fatalf("unconfigured status: %+v", cs)
	}
}

func TestCloudSettingsFileAndSetup(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cloud.json")
	if c, err := LoadCloudSettings(path); err != nil || c.M365 != nil || c.Sync.Kind != "" {
		t.Fatalf("missing file: %+v %v", c, err)
	}
	os.WriteFile(path, []byte(`{"sync":{"kind":"connect","server":"s"},"secret":"x"}`), 0o600)
	if _, err := LoadCloudSettings(path); err == nil {
		t.Fatal("unknown field accepted")
	}
	for _, bad := range []CloudSettings{{Sync: SyncSettings{Kind: "other"}}, {Sync: SyncSettings{Kind: dirsync.KindConnect}},
		{Sync: SyncSettings{Kind: dirsync.KindCloud}}, {M365: &m365.Settings{TenantID: "t"}}} {
		if bad.Validate() == nil {
			t.Errorf("accepted %+v", bad)
		}
	}

	svc, err := New(SetupBackend{}, Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if cs := svc.CloudStatus(); cs.Configured || cs.Note == "" {
		t.Fatalf("setup cloud status: %+v", cs)
	}
	settings := filepath.Join(dir, "settings.json")
	open := func(ctx context.Context, c ad.Config) (ad.Writer, error) { return nil, ad.ErrInvalidCredentials }
	a := NewAPI(svc, &LiveWiring{SettingsPath: settings, Open: open})
	if _, err := a.Setup(SetupRequest{Domain: "lab.example", AllowPasswordOnly: true, SyncKind: "cloud"}); err == nil {
		t.Fatal("Cloud Sync without a tenant accepted")
	}
	if _, err := os.Stat(settings); err == nil {
		t.Fatal("settings.json written although the cloud settings were invalid")
	}
	st, err := a.Setup(SetupRequest{Domain: "lab.example", AllowPasswordOnly: true, TenantID: "contoso-labs.example",
		ClientID: "11111111-2222-3333-4444-555555555555", Cloud: "gcchigh", SyncKind: "connect", SyncServer: "srv-entra01.lab.example"})
	if err != nil || st.Mode != "live" {
		t.Fatalf("setup: %+v %v", st, err)
	}
	c, err := LoadCloudSettings(CloudSettingsPath(settings))
	if err != nil || c.M365 == nil || c.M365.Cloud != m365.CloudGCCHigh || c.Sync.Server != "srv-entra01.lab.example" {
		t.Fatalf("saved cloud settings: %+v %v", c, err)
	}
	if _, err := auth.LoadSettings(settings); err != nil {
		t.Fatalf("settings.json format changed: %v", err)
	}
	if cs := a.CloudStatus(); !cs.Configured || cs.Cloud != "gcchigh" || cs.SyncKind != "connect" {
		t.Fatalf("live cloud status after setup: %+v", cs)
	}
}
