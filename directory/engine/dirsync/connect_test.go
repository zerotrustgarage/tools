package dirsync

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/zerotrustgarage/tools/directory/engine/ad"
)

type fakeRunner struct {
	scripts []string
	inputs  []map[string]string
	raw     [][]byte
	out     string
	err     error
}

func (f *fakeRunner) Run(ctx context.Context, script string, input []byte) ([]byte, error) {
	f.scripts = append(f.scripts, script)
	f.raw = append(f.raw, input)
	var m map[string]string
	if err := json.Unmarshal(input, &m); err != nil {
		return nil, err
	}
	f.inputs = append(f.inputs, m)
	return []byte(f.out), f.err
}

type fakeCreds struct {
	err    error
	called int
}

func (f *fakeCreds) WithCredential(fn func(*ad.Credential) error) error {
	if f.err != nil {
		return f.err
	}
	f.called++
	c := &ad.Credential{User: "a-alex", Domain: "EXAMPLE", Password: []byte("Pässw\"rd\\1")}
	defer c.Wipe()
	return fn(c)
}

var testNow = time.Date(2026, 11, 23, 15, 0, 0, 0, time.UTC)

func newTestConnect(out string) (*Connect, *fakeRunner, *fakeCreds) {
	r, cr := &fakeRunner{out: out}, &fakeCreds{}
	c := NewConnect("sync01.example.com", r, cr)
	c.Now = func() time.Time { return testNow }
	return c, r, cr
}

const healthyOut = `WARNING: something noisy
{"syncCycleEnabled":true,"stagingModeEnabled":false,"schedulerSuspended":false,"nextSyncCycleStartTimeInUTC":"2026-11-23T15:20:00.0000000Z","intervalMinutes":30,"configVersion":"2.6.92.0","installedVersion":"2.6.92.0","lastResult":"success","lastConnector":"example.com","lastProfile":"Export","lastStart":"2026-11-23T14:50:00.0000000Z","lastEnd":"2026-11-23T14:51:30.0000000","connectorIdentityType":"Application","warnings":[]}
`

func TestConnectHealth(t *testing.T) {
	c, r, _ := newTestConnect(healthyOut)
	h, err := c.Health(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if r.scripts[0] != HealthScript || r.inputs[0]["operation"] != "health" || r.inputs[0]["server"] != "sync01.example.com" ||
		r.inputs[0]["user"] != "a-alex" || r.inputs[0]["domain"] != "EXAMPLE" || r.inputs[0]["password"] != "Pässw\"rd\\1" {
		t.Fatalf("input %v", r.inputs[0])
	}
	if !bytes.Equal(r.raw[0], make([]byte, len(r.raw[0]))) {
		t.Fatal("input not wiped after the run")
	}
	if h.Kind != KindConnect || h.Server != "sync01.example.com" || h.Version != "2.6.92.0" || h.VersionStatus != VersionCurrent ||
		!h.SchedulerEnabled || h.StagingMode || h.IntervalMinutes != 30 || h.LastResult != "success" ||
		h.AppAuth == nil || !*h.AppAuth || !h.CheckedAt.Equal(testNow) {
		t.Fatalf("health %+v", h)
	}
	if h.NextCycle == nil || !h.NextCycle.Equal(time.Date(2026, 11, 23, 15, 20, 0, 0, time.UTC)) {
		t.Fatalf("next %v", h.NextCycle)
	}
	if h.LastCycle == nil || !h.LastCycle.Equal(time.Date(2026, 11, 23, 14, 51, 30, 0, time.UTC)) {
		t.Fatalf("last %v", h.LastCycle)
	}
	if len(h.Warnings) != 0 {
		t.Fatalf("warnings %q", h.Warnings)
	}
}

func TestConnectHealthWarnings(t *testing.T) {
	out := `{"syncCycleEnabled":true,"stagingModeEnabled":true,"schedulerSuspended":true,"nextSyncCycleStartTimeInUTC":"","intervalMinutes":30,` +
		`"configVersion":"2.5.190.0","installedVersion":"2.6.3.0","lastResult":"completed-export-errors","lastConnector":"tenant - AAD","lastProfile":"Export",` +
		`"lastStart":"2026-11-01T10:00:00Z","lastEnd":"","connectorIdentityType":"ServiceAccount","warnings":["Last run: \u001baccess denied"]}`
	c, _, _ := newTestConnect(out)
	h, err := c.Health(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if h.SchedulerEnabled || !h.StagingMode || h.AppAuth == nil || *h.AppAuth || h.VersionStatus != VersionRetiring || h.Version != "2.6.3.0" {
		t.Fatalf("health %+v", h)
	}
	all := strings.Join(h.Warnings, "\n")
	for _, want := range []string{"differs from the server configuration version", "service account", "suspended", "staging mode",
		"completed-export-errors", "every 7 days", "Last run: access denied"} {
		if !strings.Contains(all, want) {
			t.Errorf("no warning with %q in:\n%s", want, all)
		}
	}

	// No authentication cmdlet: AppAuth stays nil and says why.
	c, _, _ = newTestConnect(`{"syncCycleEnabled":true,"installedVersion":"2.5.79.0","connectorIdentityType":""}`)
	h, err = c.Health(context.Background())
	if err != nil || h.AppAuth != nil || h.VersionStatus != VersionUnsupported || !strings.Contains(strings.Join(h.Warnings, " "), "not available") {
		t.Fatalf("%v %+v", err, h)
	}
}

func TestConnectSyncNow(t *testing.T) {
	c, r, _ := newTestConnect(`{"result":"Success"}`)
	res, err := c.SyncNow(context.Background(), ad.Object{DN: "CN=Ann,OU=Staff,DC=example,DC=com"})
	if err != nil {
		t.Fatal(err)
	}
	if r.scripts[0] != SyncScript || r.inputs[0]["operation"] != "sync" {
		t.Fatal("wrong script or operation")
	}
	if res.Kind != KindConnect || !res.Started.Equal(testNow) ||
		res.Message != "Delta sync cycle started on sync01.example.com; requested for CN=Ann,OU=Staff,DC=example,DC=com" {
		t.Fatalf("result %+v", res)
	}
}

func TestConnectErrors(t *testing.T) {
	c, r, _ := newTestConnect(`{"error":"System.InvalidOperationException: Sync is already running. Cannot start a new run."}`)
	r.err = errors.New("PowerShell exited with code 1")
	if _, err := c.SyncNow(context.Background(), ad.Object{}); !errors.Is(err, ErrBusy) {
		t.Fatalf("busy: %v", err)
	}

	r.out = `{"error":"Connecting to remote server sync01.example.com failed: Access is denied."}`
	_, err := c.Health(context.Background())
	var re *RemoteError
	if !errors.As(err, &re) || !strings.Contains(re.Message, "Access is denied") {
		t.Fatalf("remote: %v", err)
	}

	r.out, r.err = "", errors.New("start PowerShell: boom")
	if _, err := c.Health(context.Background()); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("runner error: %v", err)
	}
	r.out, r.err = "not json", nil
	if _, err := c.Health(context.Background()); err == nil {
		t.Fatal("garbage accepted")
	}

	// Locked session: the runner is never called.
	locked := &fakeCreds{err: errors.New("the session is locked")}
	c2 := NewConnect("sync01.example.com", r, locked)
	n := len(r.scripts)
	if _, err := c2.Health(context.Background()); err == nil || len(r.scripts) != n {
		t.Fatalf("locked: %v", err)
	}

	if _, err := NewConnect("", r, locked).Health(context.Background()); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("unconfigured: %v", err)
	}
	for _, bad := range []string{"10.0.0.5", "sync01;calc", "sync 01", "-x.example.com", "a..b", "sync01.example.com'"} {
		if _, err := NewConnect(bad, r, &fakeCreds{}).Health(context.Background()); err == nil {
			t.Errorf("server %q accepted", bad)
		}
	}
}

func TestBuildInput(t *testing.T) {
	pw := "p\"\\\x01é😀"
	cred := &ad.Credential{User: "a-alex", Domain: "EXAMPLE", Password: []byte(pw)}
	b, err := buildInput("sync01.example.com", opSync, cred)
	if err != nil {
		t.Fatal(err)
	}
	for _, ch := range b {
		if ch >= 0x80 || ch < 0x20 {
			t.Fatalf("non-ASCII or control byte %#x in %s", ch, b)
		}
	}
	var m map[string]string
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	if m["password"] != pw || m["user"] != "a-alex" || m["domain"] != "EXAMPLE" || m["operation"] != "sync" || m["server"] != "sync01.example.com" {
		t.Fatalf("decoded %v", m)
	}
	if !strings.Contains(string(b), `\ud83d\ude00`) {
		t.Fatalf("astral character not escaped as a surrogate pair: %s", b)
	}

	// The buffer never grows, so no stray copy of the password is left.
	worst := &ad.Credential{User: "u", Password: bytes.Repeat([]byte{0x01}, 100)}
	b, err = buildInput("s", opSync, worst)
	if err != nil || len(b) > cap(b) {
		t.Fatal(err)
	}

	if _, err := buildInput("s", opSync, &ad.Credential{User: "u", Password: []byte{0xff}}); err == nil {
		t.Fatal("invalid UTF-8 accepted")
	}
	if _, err := buildInput("s", opSync, &ad.Credential{User: "u"}); err == nil {
		t.Fatal("empty password accepted")
	}
}

func TestScripts(t *testing.T) {
	for name, s := range map[string]string{"health": HealthScript, "sync": SyncScript} {
		got, err := decodeCommand(EncodeCommand(s))
		if err != nil || got != s {
			t.Fatalf("%s: encoding does not round-trip: %v", name, err)
		}
		if n := len(EncodeCommand(s)); n > 30000 {
			t.Fatalf("%s: encoded command is %d characters", name, n)
		}
		for _, want := range []string{"[Console]::In.ReadToEnd()", "ConvertTo-SecureString", "$in.password = $null",
			"-Authentication Kerberos", "Import-Module ADSync", "ConvertTo-Json", "'" + name + "'"} {
			if !strings.Contains(s, want) {
				t.Errorf("%s script lacks %q", name, want)
			}
		}
		if strings.Contains(s, "CredSSP") || strings.Contains(s, "Invoke-Expression") || strings.Contains(s, "\u2014") {
			t.Errorf("%s script contains a forbidden construct", name)
		}
	}
	if !strings.Contains(SyncScript, "Start-ADSyncSyncCycle -PolicyType Delta") {
		t.Error("sync script does not start a delta cycle")
	}
	for _, want := range []string{"Get-ADSyncScheduler", "Get-ADSyncRunProfileResult -NumberRequested 1",
		"Microsoft.Synchronize.ServerConfigurationVersion", "Get-ADSyncEntraConnectorCredential", "AzureADConnect.exe"} {
		if !strings.Contains(HealthScript, want) {
			t.Errorf("health script lacks %q", want)
		}
	}
}

func TestDecodeResult(t *testing.T) {
	var v struct{ A int }
	if err := decodeResult([]byte("\xef\xbb\xbf{\"A\":3}\r\n"), &v); err != nil || v.A != 3 {
		t.Fatalf("%v %v", err, v)
	}
	if err := decodeResult(nil, &v); err == nil {
		t.Fatal("empty output accepted")
	}
	err := decodeResult([]byte(`{"error":""}`), &v)
	var re *RemoteError
	if !errors.As(err, &re) || re.Message == "" {
		t.Fatalf("empty error: %v", err)
	}
}
