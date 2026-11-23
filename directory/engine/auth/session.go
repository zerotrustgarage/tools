// Package auth signs a person in to ZTG Directory with their own AD account
// (design 6.5, runbook R120 to R129): the DC checks the password through a
// Negotiate bind, the app takes its roles from the account's groups and fails
// closed, a configured second factor (Duo Authentication Proxy in Duo-only
// RADIUS mode, or another RADIUS MFA server) is then required, and the
// session locks when idle and ends after the maximum session length.
//
// Nothing secret is read from flags, environment variables or files, except
// the optional RADIUS shared secret, which is DPAPI-protected.
package auth

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/zerotrustgarage/tools/directory/engine/ad"
)

// ErrLocked reports a session locked by Lock or by idle time; Unlock with
// the password to continue (AC.L2-3.1.10).
var ErrLocked = errors.New("the session is locked; sign in again to unlock it")

// ErrExpired reports a session past its maximum length; its credential has
// been erased and a new sign-in is required (AC.L2-3.1.11).
var ErrExpired = errors.New("the session has reached its maximum length; sign in again")

// ErrSessionClosed reports a session that was signed out.
var ErrSessionClosed = errors.New("the session is signed out")

// maxChallenges bounds Access-Challenge rounds in one sign-in.
const maxChallenges = 3

// Opener opens a writer bound as cfg.Credential; ad.OpenWriter in the app,
// a fake in tests.
type Opener func(context.Context, ad.Config) (ad.Writer, error)

// Session is one signed-in account. Account, DN, DisplayName, Method and
// Started are fixed at sign-in; roles, the DC and the activity time change on
// Unlock and are read through methods. The session owns the credential and
// erases it in Close, on expiry, and when Unlock replaces it.
type Session struct {
	// Account is DOMAIN\user or the UPN as signed in.
	Account     string
	DN          string
	DisplayName string
	// Method is MethodPassword, MethodPasswordDuo or MethodPasswordRADIUS.
	Method  string
	Started time.Time

	mu         sync.Mutex
	settings   Settings
	open       Opener
	cred       *ad.Credential
	w          ad.Writer
	roles      []Role
	dc         string
	lastActive time.Time
	locked     bool
	expired    bool
	closed     bool
}

// SignIn signs cred in (design 6.5): it opens a writer bound as cred, so the
// DC checks the password and account state; finds the account's own user
// object; grants roles from its groups, failing closed with ErrNotAuthorized;
// and then, when configured, checks factor with second, using the user name
// format in s.RADIUS.UsernameFormat. An Access-Challenge is answered through
// s.Respond.
//
// On success the session owns cred. On any failure the writer is closed and
// cred is wiped. factor is always wiped.
func SignIn(ctx context.Context, s Settings, cred *ad.Credential, factor []byte, open Opener, second SecondFactor) (sess *Session, err error) {
	defer wipe(factor)
	var w ad.Writer
	defer func() {
		if err != nil {
			if w != nil {
				w.Close()
			}
			cred.Wipe()
		}
	}()
	if err := s.Validate(); err != nil {
		return nil, err
	}
	if open == nil {
		return nil, errors.New("no directory opener")
	}
	if cred == nil || strings.TrimSpace(cred.User) == "" || len(cred.Password) == 0 {
		return nil, errors.New("a user name and password are required")
	}
	if cred.Domain == "" {
		cred.User, cred.Domain = ParseAccount(cred.User)
	}
	if s.SecondFactor != SecondFactorNone {
		if second == nil {
			return nil, fmt.Errorf("the %s second factor is configured but not available", s.SecondFactor)
		}
		if len(factor) == 0 {
			return nil, errors.New("the second factor is required")
		}
	} else if second != nil {
		return nil, errors.New("a second factor was supplied but none is configured")
	}

	w, err = open(ctx, s.config(cred))
	if err != nil {
		return nil, fmt.Errorf("sign in as %s: %w", cred.Account(), err)
	}
	acct, roles, err := authorize(ctx, s, cred, w)
	if err != nil {
		return nil, err
	}
	if second != nil {
		user := acct.SAM
		if s.RADIUS.UsernameFormat == UsernameUPN {
			user = acct.UPN
		}
		if user == "" {
			return nil, fmt.Errorf("the account has no %s for the second factor", s.RADIUS.UsernameFormat)
		}
		if err := checkSecondFactor(ctx, s, second, user, factor); err != nil {
			return nil, fmt.Errorf("second factor: %w", err)
		}
	}
	now := s.now()
	return &Session{
		Account:     cred.Account(),
		DN:          acct.DN,
		DisplayName: acct.DisplayName,
		Method:      s.Method(),
		Started:     now,
		settings:    s,
		open:        open,
		cred:        cred,
		w:           w,
		roles:       roles,
		dc:          w.DC(),
		lastActive:  now,
	}, nil
}

func checkSecondFactor(ctx context.Context, s Settings, second SecondFactor, user string, factor []byte) error {
	err := second.Authenticate(ctx, user, factor, nil)
	for round := 0; round < maxChallenges; round++ {
		var ch *ChallengeError
		if !errors.As(err, &ch) {
			return err
		}
		if s.Respond == nil {
			return err
		}
		resp, rerr := s.Respond(ctx, ch.Message)
		if rerr != nil {
			wipe(resp)
			return rerr
		}
		err = second.Authenticate(ctx, user, resp, ch.State)
		wipe(resp)
	}
	var ch *ChallengeError
	if errors.As(err, &ch) {
		return fmt.Errorf("too many challenges: %w", err)
	}
	return err
}

func (s Settings) config(cred *ad.Credential) ad.Config {
	return ad.Config{Server: s.Server, BaseDN: s.BaseDN, Domain: s.Domain, LDAPS: s.LDAPS,
		KerberosOnly: s.KerberosOnly, Credential: cred}
}

// Writer returns the session's writer, or ErrLocked, ErrExpired or
// ErrSessionClosed. It does not count as activity; call Touch on user input.
func (s *Session) Writer() (ad.Writer, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.usableLocked(); err != nil {
		return nil, err
	}
	return s.w, nil
}

// Connect opens an additional writer as the session's account, for example
// to the PDC emulator or another DC; server overrides Settings.Server when
// not empty. The caller closes it.
func (s *Session) Connect(ctx context.Context, server string) (ad.Writer, error) {
	s.mu.Lock()
	if err := s.usableLocked(); err != nil {
		s.mu.Unlock()
		return nil, err
	}
	cfg := s.settings.config(&ad.Credential{User: s.cred.User, Domain: s.cred.Domain,
		Password: append([]byte(nil), s.cred.Password...)})
	open := s.open
	s.mu.Unlock()
	defer cfg.Credential.Wipe()
	if server != "" {
		cfg.Server = server
	}
	return open(ctx, cfg)
}

// usableLocked checks closed, expired and locked, in that order.
func (s *Session) usableLocked() error {
	if s.closed {
		return ErrSessionClosed
	}
	if s.expiredLocked() {
		return ErrExpired
	}
	if s.lockedLocked() {
		return ErrLocked
	}
	return nil
}

// Touch records user activity. It never unlocks: a session already idle
// past the lock time stays locked.
func (s *Session) Touch() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.expiredLocked() || s.lockedLocked() {
		return
	}
	s.lastActive = s.settings.now()
}

// Lock locks the session now.
func (s *Session) Lock() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.locked = true
}

// Locked reports whether the session is locked, by Lock or because it has
// been idle for Settings.IdleLockMinutes.
func (s *Session) Locked() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lockedLocked()
}

func (s *Session) lockedLocked() bool {
	if !s.locked && s.settings.now().Sub(s.lastActive) >= s.settings.IdleLock() {
		s.locked = true
	}
	return s.locked
}

// Expired reports whether the session has reached Settings.MaxSessionHours.
// When it has, the credential is erased and the writer closed.
func (s *Session) Expired() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.expiredLocked()
}

func (s *Session) expiredLocked() bool {
	if s.expired {
		return true
	}
	if s.settings.now().Sub(s.Started) < s.settings.MaxSession() {
		return false
	}
	s.expired = true
	s.endLocked()
	return true
}

// Unlock re-checks the account at a DC with password (the same account,
// nothing else accepted), re-reads its roles, and replaces the writer and
// credential. It fails with ErrExpired after the maximum session, with
// ErrNotAuthorized if the account has lost both roles (the session stays
// locked), or with the bind error. password is wiped.
func (s *Session) Unlock(ctx context.Context, password []byte) error {
	defer wipe(password)
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return ErrSessionClosed
	}
	if s.expiredLocked() {
		s.mu.Unlock()
		return ErrExpired
	}
	user, domain := s.cred.User, s.cred.Domain
	settings, open, dn := s.settings, s.open, s.DN
	s.mu.Unlock()

	if len(password) == 0 {
		return errors.New("the password is required to unlock")
	}
	cred := &ad.Credential{User: user, Domain: domain, Password: append([]byte(nil), password...)}
	w, err := open(ctx, settings.config(cred))
	if err != nil {
		cred.Wipe()
		return fmt.Errorf("unlock as %s: %w", (&ad.Credential{User: user, Domain: domain}).Account(), err)
	}
	fail := func(e error) error { w.Close(); cred.Wipe(); return e }
	acct, roles, err := authorize(ctx, settings, cred, w)
	if err != nil {
		return fail(err)
	}
	if !strings.EqualFold(acct.DN, dn) {
		return fail(errors.New("unlock found a different account than the one signed in"))
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return fail(ErrSessionClosed)
	}
	if s.expiredLocked() {
		return fail(ErrExpired)
	}
	if s.w != nil {
		s.w.Close()
	}
	s.cred.Wipe()
	s.w, s.cred, s.roles, s.dc = w, cred, roles, w.DC()
	s.locked = false
	s.lastActive = s.settings.now()
	return nil
}

// Close signs out: it closes the writer and erases the credential. It is
// safe to call more than once.
func (s *Session) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	return s.endLocked()
}

func (s *Session) endLocked() error {
	var err error
	if s.w != nil {
		err = s.w.Close()
		s.w = nil
	}
	s.cred.Wipe()
	return err
}

// HasRole reports whether the session holds r. A signed-out or expired
// session holds no role.
func (s *Session) HasRole(r Role) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.expiredLocked() {
		return false
	}
	for _, x := range s.roles {
		if x == r {
			return true
		}
	}
	return false
}

// Roles returns the session's roles.
func (s *Session) Roles() []Role {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Role(nil), s.roles...)
}

// DC names the domain controller the session's writer is bound to.
func (s *Session) DC() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.dc
}

// LastActivity returns the time of the last Touch, sign-in or unlock.
func (s *Session) LastActivity() time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastActive
}
