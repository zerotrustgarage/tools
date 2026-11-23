package console

import (
	"context"
	"errors"
	"strings"
	"sync"

	"github.com/zerotrustgarage/tools/directory/engine/ad"
	"github.com/zerotrustgarage/tools/directory/engine/auth"
)

// LiveBackend signs in to the customer's domain through engine/auth: the DC
// checks the account, roles come from its groups, and the second factor is the
// customer's own (design 6.5).
type LiveBackend struct {
	mu       sync.Mutex
	settings auth.Settings
	open     auth.Opener
	second   auth.SecondFactor
	sess     *auth.Session
	baseDN   string
}

// NewLiveBackend validates settings and opens the second factor client.
func NewLiveBackend(s auth.Settings, open auth.Opener) (*LiveBackend, error) {
	if err := s.Validate(); err != nil {
		return nil, err
	}
	second, err := auth.OpenSecondFactor(s)
	if err != nil {
		return nil, err
	}
	return &LiveBackend{settings: s, open: open, second: second}, nil
}

func (l *LiveBackend) BaseDN() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.baseDN
}

func (l *LiveBackend) Status() Status {
	l.mu.Lock()
	defer l.mu.Unlock()
	s := l.settings
	st := Status{Mode: "live", Domain: firstNonEmpty(s.Domain, s.Server), BaseDN: l.baseDN, SecondFactor: s.SecondFactor,
		PasswordOnly: s.SecondFactor == "", Notice: s.Notice, IdleMinutes: s.IdleLockMinutes}
	if l.sess != nil {
		st.SignedIn = true
		st.Locked = l.sess.Locked() || l.sess.Expired()
		st.Account, st.DN, st.DisplayName, st.Method, st.DC = l.sess.Account, l.sess.DN, l.sess.DisplayName, l.sess.Method, l.sess.DC()
		for _, r := range l.sess.Roles() {
			st.Roles = append(st.Roles, string(r))
		}
	}
	return st
}

func (l *LiveBackend) SignIn(ctx context.Context, user, domain string, password, factor []byte) error {
	u, d := auth.ParseAccount(user)
	if d == "" {
		d = domain
	}
	cred := &ad.Credential{User: u, Domain: d, Password: append([]byte(nil), password...)}
	var f []byte
	if len(factor) > 0 {
		f = append([]byte(nil), factor...)
	}
	sess, err := auth.SignIn(ctx, l.settings, cred, f, l.open, l.second)
	if err != nil {
		return friendly(err)
	}
	l.mu.Lock()
	if l.sess != nil {
		l.sess.Close()
	}
	l.sess = sess
	l.baseDN = l.settings.BaseDN
	if l.baseDN == "" {
		l.baseDN = domainPart(sess.DN)
	}
	l.mu.Unlock()
	return nil
}

func (l *LiveBackend) SignOut() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.sess == nil {
		return nil
	}
	err := l.sess.Close()
	l.sess = nil
	return err
}

func (l *LiveBackend) Lock() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.sess != nil {
		l.sess.Lock()
	}
}

func (l *LiveBackend) Unlock(ctx context.Context, password []byte) error {
	l.mu.Lock()
	sess := l.sess
	l.mu.Unlock()
	if sess == nil {
		return errors.New("sign in first")
	}
	return friendly(sess.Unlock(ctx, password))
}

func (l *LiveBackend) Touch() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.sess != nil {
		l.sess.Touch()
	}
}

func (l *LiveBackend) Writer() (ad.Writer, error) {
	l.mu.Lock()
	sess := l.sess
	l.mu.Unlock()
	if sess == nil {
		return nil, errors.New("sign in first")
	}
	w, err := sess.Writer()
	return w, friendly(err)
}

func friendly(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, ad.ErrInvalidCredentials):
		return errors.New("the user name or password is incorrect, or the account is disabled, expired or locked out")
	case errors.Is(err, auth.ErrNotAuthorized):
		return errors.New("this account is not a member of the ZTG Directory Operators or Approvers group")
	case errors.Is(err, auth.ErrLocked):
		return errors.New("the session is locked; enter your password to continue")
	case errors.Is(err, auth.ErrExpired):
		return errors.New("the session reached its maximum length; sign in again")
	case errors.Is(err, auth.ErrRejected):
		return errors.New("the second factor was declined")
	}
	return err
}

func domainPart(dn string) string {
	up := strings.ToUpper(dn)
	if i := strings.Index(up, "DC="); i >= 0 {
		return dn[i:]
	}
	return ""
}

var _ Backend = (*LiveBackend)(nil)
var _ Backend = (*DemoBackend)(nil)
