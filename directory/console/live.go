package console

import (
	"context"
	"errors"
	"strings"
	"sync"

	"github.com/zerotrustgarage/tools/directory/engine/ad"
	"github.com/zerotrustgarage/tools/directory/engine/auth"
	"github.com/zerotrustgarage/tools/directory/engine/dirsync"
	"github.com/zerotrustgarage/tools/directory/engine/m365"
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

	// Microsoft 365 and sync (cloud.go). The tenant exists only while the
	// technician is signed in to Microsoft 365; tokens stay in memory.
	cloud    CloudSettings
	cwiring  CloudWiring
	tenant   m365.Tenant
	cloudEnd func()
	// cloudSync is the Cloud Sync driver over the signed-in Graph client;
	// nil while signed out (or when the tenant is not a Graph client).
	cloudSync dirsync.Syncer
	// cloudSignIn runs the delegated sign-in; tests replace it.
	cloudSignIn func(ctx context.Context, s m365.Settings, w CloudWiring) (m365.Tenant, func(), error)
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
	return &LiveBackend{settings: s, open: open, second: second, cloudSignIn: graphSignIn}, nil
}

// UseCloud sets the Microsoft 365 and sync settings (LoadCloudSettings) and
// the app's wiring for them. It ends any Microsoft 365 sign-in.
func (l *LiveBackend) UseCloud(c CloudSettings, w CloudWiring) error {
	if err := c.Validate(); err != nil {
		return err
	}
	l.CloudSignOut()
	l.mu.Lock()
	defer l.mu.Unlock()
	if c.Sync.Kind == dirsync.KindCloud && c.M365 != nil && !c.M365.CloudSync {
		m := *c.M365
		m.CloudSync = true // request the Cloud Sync permissions at sign-in
		c.M365 = &m
	}
	if w.SyncRunner == nil {
		w.SyncRunner = dirsync.PowerShellRunner{}
	}
	l.cloud, l.cwiring = c, w
	return nil
}

// graphSignIn is the delegated sign-in through the system browser and the
// Graph client that runs as the signed-in technician (engine/m365).
func graphSignIn(ctx context.Context, s m365.Settings, w CloudWiring) (m365.Tenant, func(), error) {
	sess, err := m365.NewSession(s, m365.SessionOptions{OpenBrowser: w.OpenBrowser})
	if err != nil {
		return nil, nil, err
	}
	if err := sess.SignIn(ctx); err != nil {
		return nil, nil, err
	}
	ep, err := m365.EndpointsFor(s.Cloud)
	if err != nil {
		sess.SignOut()
		return nil, nil, err
	}
	g, err := m365.NewGraph(sess, ep.Graph)
	if err != nil {
		sess.SignOut()
		return nil, nil, err
	}
	return g, sess.SignOut, nil
}

// adSessionLocked returns the AD session a cloud call runs under.
func (l *LiveBackend) adSessionLocked() (*auth.Session, error) {
	switch {
	case l.sess == nil:
		return nil, errors.New("sign in first")
	case l.sess.Expired():
		l.cloudSignOutLocked()
		return nil, friendly(auth.ErrExpired)
	case l.sess.Locked():
		return nil, friendly(auth.ErrLocked)
	}
	return l.sess, nil
}

func (l *LiveBackend) CloudStatus() CloudStatus {
	l.mu.Lock()
	defer l.mu.Unlock()
	c := l.cloud
	cs := CloudStatus{Configured: c.M365 != nil, SyncKind: string(c.Sync.Kind), SyncServer: c.Sync.Server}
	if c.M365 != nil {
		cs.TenantID, cs.Cloud = c.M365.TenantID, string(c.M365.Cloud)
		if cs.Cloud == "" {
			cs.Cloud = string(m365.CloudCommercial)
		}
	}
	cs.SyncReady = (c.Sync.Kind == dirsync.KindConnect && l.cwiring.SyncRunner != nil) ||
		(c.Sync.Kind == dirsync.KindCloud && l.tenant != nil && l.cloudSync != nil)
	switch {
	case c.M365 == nil:
		cs.Note = "Microsoft 365 is not set up for this console: add the tenant ID and the app registration's client ID in the setup."
	case l.tenant != nil && l.sess != nil:
		cs.SignedIn, cs.Account = true, l.tenant.Account()
		cs.Note = "Signed in to Microsoft 365 as " + cs.Account + "; Microsoft 365 applies that account's own roles."
	default:
		cs.Note = "Sign in to Microsoft 365 to see the cloud side of each object. The sign-in opens in your browser."
	}
	switch c.Sync.Kind {
	case dirsync.KindNone:
		cs.Note += " No sync method is configured."
	case dirsync.KindConnect:
		cs.Note += " Sync now starts a Connect Sync delta cycle on " + c.Sync.Server + " as your AD account."
	case dirsync.KindCloud:
		if cs.SyncReady {
			cs.Note += " Sync now provisions the object on demand with Cloud Sync."
		} else {
			cs.Note += " Cloud Sync (Sync now and sync health) needs the Microsoft 365 sign-in."
		}
	}
	return cs
}

func (l *LiveBackend) CloudSignIn(ctx context.Context) error {
	l.mu.Lock()
	adSess, err := l.adSessionLocked()
	settings, w, signIn := l.cloud.M365, l.cwiring, l.cloudSignIn
	l.mu.Unlock()
	if err != nil {
		return err
	}
	if settings == nil {
		return errors.New("Microsoft 365 is not set up for this console: add the tenant ID and the app registration's client ID in the setup")
	}
	t, end, err := signIn(ctx, *settings, w)
	if err != nil {
		return err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.sess == nil || l.sess != adSess { // the AD session ended or changed while the browser was open
		if end != nil {
			end()
		}
		return errors.New("sign in first")
	}
	l.cloudSignOutLocked()
	l.tenant, l.cloudEnd = t, end
	if g, ok := t.(dirsync.GraphDoer); ok {
		l.cloudSync = dirsync.NewCloud(g)
	}
	return nil
}

func (l *LiveBackend) CloudSignOut() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.cloudSignOutLocked()
}

func (l *LiveBackend) cloudSignOutLocked() {
	if l.cloudEnd != nil {
		l.cloudEnd()
	}
	l.tenant, l.cloudEnd, l.cloudSync = nil, nil, nil
}

func (l *LiveBackend) Tenant() (m365.Tenant, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if _, err := l.adSessionLocked(); err != nil {
		return nil, err
	}
	if l.tenant == nil {
		return nil, m365.ErrNotSignedIn
	}
	return l.tenant, nil
}

// Syncer returns Connect Sync, driven through PowerShell remoting to the sync
// server as the signed-in AD account, or Cloud Sync, driven through Graph as
// the technician signed in to Microsoft 365.
func (l *LiveBackend) Syncer() (dirsync.Syncer, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	sess, err := l.adSessionLocked()
	if err != nil {
		return nil, err
	}
	switch l.cloud.Sync.Kind {
	case dirsync.KindConnect:
		return dirsync.NewConnect(l.cloud.Sync.Server, l.cwiring.SyncRunner, sess), nil
	case dirsync.KindCloud:
		if l.tenant == nil {
			return nil, errors.New("Cloud Sync runs through Microsoft 365: sign in to Microsoft 365 first")
		}
		if l.cloudSync == nil {
			return nil, errors.New("Cloud Sync needs the Microsoft Graph connection, which this sign-in does not provide")
		}
		return l.cloudSync, nil
	}
	return nil, dirsync.ErrNotConfigured
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
	l.cloudSignOutLocked()
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
	l.cloudSignOutLocked()
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
