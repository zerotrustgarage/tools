package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// Second factor choices for Settings.SecondFactor (design 6.5 step 5).
const (
	SecondFactorNone   = ""
	SecondFactorDuo    = "duo"
	SecondFactorRADIUS = "radius"
)

// RADIUS user name formats for RADIUSSettings.UsernameFormat. The value must
// match how users are enrolled at the RADIUS server (for Duo, Duo's
// enrollment).
const (
	UsernameSAM = "sam"
	UsernameUPN = "upn"
)

// Sign-in methods recorded on every session (runbook R129).
const (
	MethodPassword       = "password"
	MethodPasswordDuo    = "password+duo"
	MethodPasswordRADIUS = "password+radius"
)

// maxSettingsSize bounds the settings file read.
const maxSettingsSize = 1 << 20

// ErrPasswordOnlyNotAllowed is returned by Validate (and so by LoadSettings
// and SignIn) when no second factor is configured and password-only sign-in
// has not been allowed explicitly.
var ErrPasswordOnlyNotAllowed = errors.New("sign-in is refused: no second factor is configured and password-only sign-in is not allowed; " +
	"set secondFactor to \"duo\" or \"radius\", or set allowPasswordOnly to true and record the missing multifactor authentication " +
	"as an open IA.L2-3.5.3 item on the POA&M")

// RADIUSSettings configures the RADIUS second factor (Duo Authentication
// Proxy in radius_server_duo_only mode, or another RADIUS MFA server).
type RADIUSSettings struct {
	// Server is host:port of the RADIUS server, for example the customer's
	// Duo Authentication Proxy.
	Server string `json:"server"`
	// SecretFile is the path of the DPAPI-protected shared secret
	// (LoadRADIUSSecret). The secret itself is never in this file.
	SecretFile string `json:"secretFile"`
	// TimeoutSeconds is the wait for an answer to each transmission. Duo
	// holds the request open until the user answers a push or call, so this
	// stays at 60 seconds or more for Duo.
	TimeoutSeconds int `json:"timeoutSeconds"`
	// Retries is the number of retransmissions after the first send.
	Retries int `json:"retries"`
	// NASIdentifier is sent as NAS-Identifier (RFC 2865 requires it or
	// NAS-IP-Address in every Access-Request).
	NASIdentifier string `json:"nasIdentifier"`
	// UsernameFormat is UsernameSAM or UsernameUPN.
	UsernameFormat string `json:"usernameFormat"`
}

// Settings is the customer's sign-in configuration (design 6.5). It holds no
// secret: the RADIUS shared secret is in a separate DPAPI-protected file.
type Settings struct {
	Domain string `json:"domain"`
	Server string `json:"server"`
	BaseDN string `json:"baseDN"`
	LDAPS  bool   `json:"ldaps"`
	// KerberosOnly removes NTLM from the Negotiate package list (runbook
	// R121). Default true.
	KerberosOnly bool `json:"kerberosOnly"`
	// SecondFactor is SecondFactorNone, SecondFactorDuo or
	// SecondFactorRADIUS.
	SecondFactor string `json:"secondFactor"`
	// AllowPasswordOnly permits sign-in without a second factor. It applies
	// only when SecondFactor is empty; a configured second factor is always
	// required and never skipped.
	AllowPasswordOnly bool           `json:"allowPasswordOnly"`
	RADIUS            RADIUSSettings `json:"radius"`
	OperatorsGroup    string         `json:"operatorsGroup"`
	ApproversGroup    string         `json:"approversGroup"`
	IdleLockMinutes   int            `json:"idleLockMinutes"`
	MaxSessionHours   int            `json:"maxSessionHours"`
	// Notice is the system use notice shown and acknowledged before the
	// credential prompt (runbook R124). Empty shows none.
	Notice string `json:"notice"`

	// Now is the clock; nil uses time.Now. Tests set it.
	Now func() time.Time `json:"-"`
	// Respond answers a RADIUS Access-Challenge during SignIn: it shows
	// message (untrusted server text) to the user and returns the response,
	// for example a one-time passcode. The returned slice is wiped after
	// use. Nil fails the sign-in on a challenge.
	Respond func(ctx context.Context, message string) ([]byte, error) `json:"-"`
}

// DefaultSettings returns the defaults: Kerberos only, the default group
// names, a 15 minute idle lock and an 8 hour maximum session. The defaults
// alone do not validate: a domain or server is required, and either a second
// factor or an explicit AllowPasswordOnly.
func DefaultSettings() Settings {
	return Settings{
		KerberosOnly:    true,
		OperatorsGroup:  "ZTG Directory Operators",
		ApproversGroup:  "ZTG Directory Approvers",
		IdleLockMinutes: 15,
		MaxSessionHours: 8,
		RADIUS: RADIUSSettings{
			TimeoutSeconds: 60,
			Retries:        1,
			NASIdentifier:  "ZTG Directory",
			UsernameFormat: UsernameSAM,
		},
	}
}

// LoadSettings reads a JSON settings file over DefaultSettings, so absent
// fields keep their defaults. Unknown fields and trailing data are rejected,
// and the result is validated.
func LoadSettings(path string) (Settings, error) {
	f, err := os.Open(path)
	if err != nil {
		return Settings{}, fmt.Errorf("open sign-in settings: %w", err)
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxSettingsSize+1))
	if err != nil {
		return Settings{}, fmt.Errorf("read sign-in settings: %w", err)
	}
	if len(data) > maxSettingsSize {
		return Settings{}, fmt.Errorf("sign-in settings %s are larger than %d bytes", path, maxSettingsSize)
	}
	s, err := parseSettings(data)
	if err != nil {
		return Settings{}, fmt.Errorf("sign-in settings %s: %w", path, err)
	}
	return s, nil
}

func parseSettings(data []byte) (Settings, error) {
	s := DefaultSettings()
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&s); err != nil {
		return Settings{}, err
	}
	if _, err := dec.Token(); err != io.EOF {
		return Settings{}, errors.New("unexpected data after the settings object")
	}
	if err := s.Validate(); err != nil {
		return Settings{}, err
	}
	return s, nil
}

// Validate checks the settings. It returns ErrPasswordOnlyNotAllowed when no
// second factor is configured and password-only sign-in is not allowed.
func (s Settings) Validate() error {
	if strings.TrimSpace(s.Domain) == "" && strings.TrimSpace(s.Server) == "" {
		return errors.New("domain or server is required")
	}
	for name, v := range map[string]string{"domain": s.Domain, "server": s.Server, "baseDN": s.BaseDN,
		"operatorsGroup": s.OperatorsGroup, "approversGroup": s.ApproversGroup} {
		if hasControl(v) {
			return fmt.Errorf("%s contains a control character", name)
		}
	}
	if strings.TrimSpace(s.OperatorsGroup) == "" || strings.TrimSpace(s.ApproversGroup) == "" {
		return errors.New("operatorsGroup and approversGroup must not be empty")
	}
	if strings.EqualFold(strings.TrimSpace(s.OperatorsGroup), strings.TrimSpace(s.ApproversGroup)) {
		return errors.New("operatorsGroup and approversGroup must be different groups (AC.L2-3.1.4)")
	}
	if s.MaxSessionHours < 1 || s.MaxSessionHours > 24 {
		return errors.New("maxSessionHours must be from 1 to 24")
	}
	if s.IdleLockMinutes < 1 || s.IdleLockMinutes >= s.MaxSessionHours*60 {
		return errors.New("idleLockMinutes must be at least 1 and shorter than the maximum session")
	}
	if len(s.Notice) > 16*1024 {
		return errors.New("notice must be 16 KiB or shorter")
	}
	switch s.SecondFactor {
	case SecondFactorNone:
		if !s.AllowPasswordOnly {
			return ErrPasswordOnlyNotAllowed
		}
		return nil
	case SecondFactorDuo, SecondFactorRADIUS:
	default:
		return fmt.Errorf("secondFactor must be empty, %q or %q", SecondFactorDuo, SecondFactorRADIUS)
	}
	if s.AllowPasswordOnly {
		return errors.New("allowPasswordOnly applies only when secondFactor is empty; a configured second factor is always required")
	}
	r := s.RADIUS
	if err := checkHostPort(r.Server); err != nil {
		return fmt.Errorf("radius.server: %w", err)
	}
	if strings.TrimSpace(r.SecretFile) == "" {
		return errors.New("radius.secretFile is required")
	}
	if r.TimeoutSeconds < 1 || r.TimeoutSeconds > 300 {
		return errors.New("radius.timeoutSeconds must be from 1 to 300")
	}
	if r.Retries < 0 || r.Retries > 10 {
		return errors.New("radius.retries must be from 0 to 10")
	}
	if err := checkNASID(r.NASIdentifier); err != nil {
		return fmt.Errorf("radius.nasIdentifier: %w", err)
	}
	if r.UsernameFormat != UsernameSAM && r.UsernameFormat != UsernameUPN {
		return fmt.Errorf("radius.usernameFormat must be %q or %q", UsernameSAM, UsernameUPN)
	}
	return nil
}

// Method names the sign-in method these settings produce, for records.
func (s Settings) Method() string {
	switch s.SecondFactor {
	case SecondFactorDuo:
		return MethodPasswordDuo
	case SecondFactorRADIUS:
		return MethodPasswordRADIUS
	}
	return MethodPassword
}

// IdleLock is the idle time after which a session locks.
func (s Settings) IdleLock() time.Duration { return time.Duration(s.IdleLockMinutes) * time.Minute }

// MaxSession is the maximum session length.
func (s Settings) MaxSession() time.Duration { return time.Duration(s.MaxSessionHours) * time.Hour }

// OpenSecondFactor returns the configured second factor: nil when none is
// configured, otherwise a RADIUSClient with the secret read from
// RADIUS.SecretFile (Windows only).
func OpenSecondFactor(s Settings) (SecondFactor, error) {
	if err := s.Validate(); err != nil {
		return nil, err
	}
	if s.SecondFactor == SecondFactorNone {
		return nil, nil
	}
	secret, err := LoadRADIUSSecret(s.RADIUS.SecretFile)
	if err != nil {
		return nil, err
	}
	defer wipe(secret)
	c, err := NewRADIUSClient(s.RADIUS.Server, secret, time.Duration(s.RADIUS.TimeoutSeconds)*time.Second,
		s.RADIUS.Retries, s.RADIUS.NASIdentifier)
	if err != nil {
		return nil, err
	}
	return c, nil
}

func (s Settings) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func checkHostPort(v string) error {
	host, port, err := net.SplitHostPort(v)
	if err != nil {
		return fmt.Errorf("%q is not host:port", v)
	}
	if host == "" || hasControl(host) || strings.ContainsAny(host, " /") {
		return fmt.Errorf("%q has no valid host", v)
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65535 {
		return fmt.Errorf("%q has no valid port", v)
	}
	return nil
}

func checkNASID(v string) error {
	if v == "" || len(v) > 253 {
		return errors.New("must be 1 to 253 bytes")
	}
	if hasControl(v) {
		return errors.New("contains a control character")
	}
	return nil
}

func hasControl(v string) bool {
	return strings.ContainsFunc(v, unicode.IsControl)
}

// wipe overwrites b with zeros.
func wipe(b []byte) {
	for i := range b {
		b[i] = 0
	}
}
