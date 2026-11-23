package dirsync

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/zerotrustgarage/tools/directory/engine/ad"
)

// Runner runs one of the fixed scripts (psscripts.go) with input on its
// standard input and returns its standard output. PowerShellRunner is the
// production Runner; tests use a fake.
type Runner interface {
	Run(ctx context.Context, script string, input []byte) ([]byte, error)
}

// CredentialSource lends the signed-in AD account's credential for the
// duration of fn; auth.Session satisfies it.
type CredentialSource interface {
	WithCredential(fn func(*ad.Credential) error) error
}

// DefaultConnectTimeout bounds one remoting call when the caller's context
// has no earlier deadline.
const DefaultConnectTimeout = 2 * time.Minute

// staleCycle is Microsoft's limit between delta cycles (scheduler page: "a
// synchronization cycle is run at least once every 7 days").
const staleCycle = 7 * 24 * time.Hour

// Connect drives Microsoft Entra Connect Sync on the sync server through
// PowerShell remoting as the signed-in AD account (design 6.2a, 6.4). The
// account needs remoting access to the server and the right to run the
// ADSync cmdlets there.
type Connect struct {
	server string
	run    Runner
	cred   CredentialSource

	// Now returns the current time; time.Now when nil.
	Now func() time.Time
	// Timeout bounds each call; DefaultConnectTimeout when zero.
	Timeout time.Duration
}

// NewConnect returns a Connect Sync Syncer for the sync server's DNS name.
func NewConnect(server string, run Runner, cred CredentialSource) *Connect {
	return &Connect{server: strings.TrimSpace(server), run: run, cred: cred}
}

var _ Syncer = (*Connect)(nil)

func (c *Connect) Kind() Kind { return KindConnect }

func (c *Connect) now() time.Time {
	if c.Now != nil {
		return c.Now().UTC()
	}
	return time.Now().UTC()
}

// connectHealth is the health script's output.
type connectHealth struct {
	SyncCycleEnabled      bool     `json:"syncCycleEnabled"`
	StagingModeEnabled    bool     `json:"stagingModeEnabled"`
	SchedulerSuspended    bool     `json:"schedulerSuspended"`
	NextSyncCycle         string   `json:"nextSyncCycleStartTimeInUTC"`
	IntervalMinutes       int      `json:"intervalMinutes"`
	ConfigVersion         string   `json:"configVersion"`
	InstalledVersion      string   `json:"installedVersion"`
	LastResult            string   `json:"lastResult"`
	LastConnector         string   `json:"lastConnector"`
	LastProfile           string   `json:"lastProfile"`
	LastStart             string   `json:"lastStart"`
	LastEnd               string   `json:"lastEnd"`
	ConnectorIdentityType string   `json:"connectorIdentityType"`
	Warnings              []string `json:"warnings"`
}

// Health reads the scheduler, version, last run and authentication method
// from the sync server (runbook R28).
func (c *Connect) Health(ctx context.Context) (Health, error) {
	var out connectHealth
	if err := c.call(ctx, HealthScript, opHealth, &out); err != nil {
		return Health{}, err
	}
	return c.health(out), nil
}

func (c *Connect) health(out connectHealth) Health {
	now := c.now()
	h := Health{
		Kind:             KindConnect,
		Server:           c.server,
		StagingMode:      out.StagingModeEnabled,
		SchedulerEnabled: out.SyncCycleEnabled && !out.SchedulerSuspended,
		IntervalMinutes:  out.IntervalMinutes,
		NextCycle:        parseTime(out.NextSyncCycle),
		LastResult:       clean(out.LastResult, 200),
		CheckedAt:        now,
	}
	if h.IntervalMinutes < 0 {
		h.IntervalMinutes = 0
	}
	h.LastCycle = parseTime(out.LastEnd)
	if h.LastCycle == nil {
		h.LastCycle = parseTime(out.LastStart)
	}
	warn := func(format string, a ...any) { h.Warnings = append(h.Warnings, fmt.Sprintf(format, a...)) }

	installed, config := clean(out.InstalledVersion, 40), clean(out.ConfigVersion, 40)
	h.Version = installed
	if h.Version == "" {
		h.Version = config
	}
	h.VersionStatus, h.VersionNote = ClassifyConnectVersion(h.Version, now)
	if installed != "" && config != "" && !sameVersion(installed, config) {
		warn("The installed version %s differs from the server configuration version %s: the upgrade may be incomplete.", installed, config)
	}
	if w := connectTableWarning(now); w != "" {
		warn("%s", w)
	}

	switch strings.ToLower(strings.TrimSpace(out.ConnectorIdentityType)) {
	case "application":
		t := true
		h.AppAuth = &t
	case "serviceaccount":
		f := false
		h.AppAuth = &f
		warn("The server signs in to Microsoft Entra ID with a service account: configure application-based authentication by %s, or synchronization stops.", longDate(connectAppAuthDeadline))
	case "":
		warn("The authentication method could not be read (Get-ADSyncEntraConnectorCredential is not available on this version); application-based authentication is required by %s.", longDate(connectAppAuthDeadline))
	default:
		warn("Unrecognized authentication method %q.", clean(out.ConnectorIdentityType, 40))
	}

	if out.SchedulerSuspended {
		warn("The scheduler is suspended (an upgrade or the Connect wizard is in progress); no cycles run until it resumes.")
	} else if !out.SyncCycleEnabled {
		warn("Scheduled sync cycles are turned off (SyncCycleEnabled is false).")
	}
	if out.StagingModeEnabled {
		warn("This server is in staging mode: it imports and synchronizes but exports nothing to Microsoft Entra ID.")
	}
	if r := strings.ToLower(h.LastResult); r != "" && r != "success" {
		warn("The last run (%s, %s) ended with %q.", clean(out.LastConnector, 100), clean(out.LastProfile, 100), h.LastResult)
	}
	if h.LastCycle != nil && now.Sub(*h.LastCycle) > staleCycle {
		warn("No run since %s: Microsoft requires a delta cycle at least every 7 days.", longDate(*h.LastCycle))
	}
	for _, w := range out.Warnings {
		if w = clean(w, 300); w != "" {
			warn("%s", w)
		}
	}
	return h
}

// SyncNow starts a delta cycle on the sync server (Start-ADSyncSyncCycle
// -PolicyType Delta); obj is only named in the result. A cycle in progress
// returns ErrBusy.
func (c *Connect) SyncNow(ctx context.Context, obj ad.Object) (Result, error) {
	started := c.now()
	var out struct {
		Result string `json:"result"`
	}
	if err := c.call(ctx, SyncScript, opSync, &out); err != nil {
		return Result{}, err
	}
	msg := "Delta sync cycle started on " + c.server
	if r := clean(out.Result, 100); r != "" && !strings.EqualFold(r, "success") {
		msg += " (" + r + ")"
	}
	if obj.DN != "" {
		msg += "; requested for " + obj.DN
	}
	return Result{Kind: KindConnect, Started: started, Message: msg}, nil
}

// call runs script as the signed-in account and decodes its JSON result.
func (c *Connect) call(ctx context.Context, script, op string, out any) error {
	if c.server == "" {
		return ErrNotConfigured
	}
	if err := checkServer(c.server); err != nil {
		return err
	}
	if c.run == nil || c.cred == nil {
		return errors.New("Connect Sync remoting is not available")
	}
	timeout := c.Timeout
	if timeout <= 0 {
		timeout = DefaultConnectTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var res []byte
	var runErr error
	err := c.cred.WithCredential(func(cr *ad.Credential) error {
		input, err := buildInput(c.server, op, cr)
		if err != nil {
			return err
		}
		defer wipeBytes(input)
		res, runErr = c.run.Run(ctx, script, input)
		return nil
	})
	if err != nil {
		return err
	}
	if perr := decodeResult(res, out); perr != nil {
		var re *RemoteError
		if errors.As(perr, &re) || errors.Is(perr, ErrBusy) {
			return fmt.Errorf("sync server %s: %w", c.server, perr)
		}
		if runErr != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return fmt.Errorf("sync server %s: %w", c.server, ctxErr)
			}
			return fmt.Errorf("sync server %s: %w", c.server, runErr)
		}
		return fmt.Errorf("sync server %s: %w", c.server, perr)
	}
	if runErr != nil {
		return fmt.Errorf("sync server %s: %w", c.server, runErr)
	}
	return nil
}

// RemoteError is an error message the script returned ({"error":"..."}).
type RemoteError struct{ Message string }

func (e *RemoteError) Error() string { return e.Message }

// decodeResult finds the last JSON object line in output and decodes it into
// out, or returns the script's error: ErrBusy for "Sync is already running",
// otherwise a *RemoteError.
func decodeResult(output []byte, out any) error {
	line := lastJSONLine(output)
	if line == nil {
		return errors.New("no result from the sync server script")
	}
	var e struct {
		Error *string `json:"error"`
	}
	if err := json.Unmarshal(line, &e); err != nil {
		return fmt.Errorf("unreadable result from the sync server script: %w", err)
	}
	if e.Error != nil {
		msg := clean(*e.Error, 1000)
		if strings.Contains(strings.ToLower(msg), "sync is already running") {
			return ErrBusy
		}
		if msg == "" {
			msg = "the sync server script failed without a message"
		}
		return &RemoteError{Message: msg}
	}
	if err := json.Unmarshal(line, out); err != nil {
		return fmt.Errorf("unreadable result from the sync server script: %w", err)
	}
	return nil
}

func lastJSONLine(output []byte) []byte {
	lines := bytes.Split(output, []byte("\n"))
	for i := len(lines) - 1; i >= 0; i-- {
		l := bytes.TrimSpace(lines[i])
		l = bytes.TrimPrefix(l, []byte("\xef\xbb\xbf"))
		if len(l) > 0 && l[0] == '{' {
			return l
		}
	}
	return nil
}

// buildInput writes the script's JSON input with every non-ASCII character
// escaped, so the console code page cannot alter it. The buffer is sized up
// front so appending never leaves a copy of the password behind; the caller
// wipes the result.
func buildInput(server, op string, c *ad.Credential) ([]byte, error) {
	if c == nil || strings.TrimSpace(c.User) == "" || len(c.Password) == 0 {
		return nil, errors.New("the signed-in account has no password to use for the sync server")
	}
	if !utf8.Valid(c.Password) {
		return nil, errors.New("the password is not valid UTF-8")
	}
	fields := []struct {
		name  string
		value []byte
	}{
		{"server", []byte(server)},
		{"user", []byte(c.User)},
		{"domain", []byte(c.Domain)},
		{"password", c.Password},
		{"operation", []byte(op)},
	}
	size := 2
	for _, f := range fields {
		size += len(f.name) + 6 + 6*len(f.value)
	}
	buf := make([]byte, 0, size)
	buf = append(buf, '{')
	for i, f := range fields {
		if i > 0 {
			buf = append(buf, ',')
		}
		buf = append(buf, '"')
		buf = append(buf, f.name...)
		buf = append(buf, '"', ':')
		buf = appendJSONString(buf, f.value)
	}
	buf = append(buf, '}')
	return buf, nil
}

const hexDigits = "0123456789abcdef"

func appendJSONString(buf, s []byte) []byte {
	buf = append(buf, '"')
	for len(s) > 0 {
		r, n := utf8.DecodeRune(s)
		s = s[n:]
		switch {
		case r == '"' || r == '\\':
			buf = append(buf, '\\', byte(r))
		case r >= 0x20 && r < 0x7f:
			buf = append(buf, byte(r))
		default:
			if r >= 0x10000 {
				r1, r2 := utf16.EncodeRune(r)
				buf = appendUnicodeEscape(buf, r1)
				buf = appendUnicodeEscape(buf, r2)
			} else {
				buf = appendUnicodeEscape(buf, r)
			}
		}
	}
	return append(buf, '"')
}

func appendUnicodeEscape(buf []byte, r rune) []byte {
	return append(buf, '\\', 'u', hexDigits[r>>12&0xf], hexDigits[r>>8&0xf], hexDigits[r>>4&0xf], hexDigits[r&0xf])
}

func wipeBytes(b []byte) {
	for i := range b {
		b[i] = 0
	}
}

// checkServer accepts a DNS host name only: Kerberos needs the name the
// server's SPN is registered under, and the name is passed to Invoke-Command.
func checkServer(s string) error {
	if len(s) > 253 {
		return errors.New("the sync server name is too long")
	}
	if net.ParseIP(s) != nil {
		return fmt.Errorf("use the sync server's DNS name, not the address %s (Kerberos needs the name)", s)
	}
	for _, label := range strings.Split(s, ".") {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return fmt.Errorf("%q is not a valid sync server name", s)
		}
		for i := 0; i < len(label); i++ {
			ch := label[i]
			if !(ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9' || ch == '-') {
				return fmt.Errorf("%q is not a valid sync server name", s)
			}
		}
	}
	return nil
}

// EncodeCommand returns the -EncodedCommand argument for script: Base64 of
// its UTF-16LE text.
func EncodeCommand(script string) string {
	u := utf16.Encode([]rune(script))
	b := make([]byte, 2*len(u))
	for i, v := range u {
		b[2*i] = byte(v)
		b[2*i+1] = byte(v >> 8)
	}
	return base64.StdEncoding.EncodeToString(b)
}

// decodeCommand reverses EncodeCommand.
func decodeCommand(enc string) (string, error) {
	b, err := base64.StdEncoding.DecodeString(enc)
	if err != nil {
		return "", err
	}
	if len(b)%2 != 0 {
		return "", errors.New("odd length")
	}
	u := make([]uint16, len(b)/2)
	for i := range u {
		u[i] = uint16(b[2*i]) | uint16(b[2*i+1])<<8
	}
	return string(utf16.Decode(u)), nil
}

// parseTime reads an ISO 8601 time, taking one without a zone as UTC; empty
// and placeholder years return nil.
func parseTime(s string) *time.Time {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	var t time.Time
	var err error
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04:05.9999999"} {
		if t, err = time.Parse(layout, s); err == nil {
			break
		}
	}
	if err != nil || t.Year() < 2000 {
		return nil
	}
	t = t.UTC()
	return &t
}

func sameVersion(a, b string) bool {
	va, oka := parseVersion(a)
	vb, okb := parseVersion(b)
	if !oka || !okb {
		return strings.EqualFold(a, b)
	}
	return compareVersion(va, vb) == 0
}

// clean trims s, drops control characters and bounds its length, for text
// that came from the sync server or the tenant.
func clean(s string, max int) string {
	s = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			if r == '\n' || r == '\r' || r == '\t' {
				return ' '
			}
			return -1
		}
		return r
	}, s)
	s = strings.Join(strings.Fields(s), " ")
	if utf8.RuneCountInString(s) > max {
		s = string([]rune(s)[:max]) + "..."
	}
	return s
}
