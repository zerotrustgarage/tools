package auth

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zerotrustgarage/tools/directory/engine/ad"
)

const (
	alexDN       = "CN=Alex Admin,OU=Admins,DC=example,DC=com"
	goodPassword = "Correct horse 1"
)

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *clock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// directory is a fake domain: an opener that checks the password the way a
// DC bind would and returns a fresh FakeClient per bind.
type directory struct {
	mu        sync.Mutex
	password  string
	groups    []ad.Group
	badGroups bool
	opened    []*ad.FakeClient
	configs   []ad.Config
}

func (d *directory) open(_ context.Context, cfg ad.Config) (ad.Writer, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	c := cfg
	c.Credential = nil
	d.configs = append(d.configs, c)
	if cfg.Credential == nil || string(cfg.Credential.Password) != d.password {
		return nil, errors.New("LDAP bind failed: invalid credentials (49)")
	}
	f := ad.NewFake([]ad.Object{
		{DN: alexDN, Attributes: map[string][]string{"objectClass": {"top", "person", "organizationalPerson", "user"},
			"sAMAccountName": {"a-alex"}, "userPrincipalName": {"a-alex@example.com"}, "displayName": {"Alex Admin (admin)"}}},
		{DN: "CN=Alex Smith,OU=Staff,DC=example,DC=com", Attributes: map[string][]string{"objectClass": {"top", "person", "organizationalPerson", "user"},
			"sAMAccountName": {"a-alex2"}, "userPrincipalName": {"a-alex2@example.com"}}},
	})
	f.Groups[strings.ToLower(alexDN)] = append([]ad.Group(nil), d.groups...)
	f.DCName = "dc1.example.com"
	d.opened = append(d.opened, f)
	if d.badGroups {
		return brokenGroups{f}, nil
	}
	return f, nil
}

func (d *directory) last() *ad.FakeClient {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.opened[len(d.opened)-1]
}

type brokenGroups struct{ *ad.FakeClient }

func (brokenGroups) EffectiveGroups(context.Context, string) ([]ad.Group, error) {
	return nil, errors.New("tokenGroups: insufficient access")
}

func isClosed(c *ad.FakeClient) bool {
	_, err := c.Search(context.Background(), ad.SearchRequest{})
	return errors.Is(err, ad.ErrClosed)
}

type fakeFactor struct {
	mu      sync.Mutex
	users   []string
	factors []string
	states  [][]byte
	reply   func(n int, factor string, state []byte) error
}

func (f *fakeFactor) Authenticate(_ context.Context, user string, factor []byte, state []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.users = append(f.users, user)
	f.factors = append(f.factors, string(factor))
	f.states = append(f.states, append([]byte(nil), state...))
	if f.reply == nil {
		return nil
	}
	return f.reply(len(f.users), string(factor), state)
}

func operators() []ad.Group {
	return []ad.Group{{Name: "Domain Users", Primary: true, Resolved: true},
		{DN: "CN=ZTG Directory Operators,OU=Groups,DC=example,DC=com", Name: "ztg directory OPERATORS", Resolved: true}}
}

func newCred(user, domain, password string) *ad.Credential {
	return &ad.Credential{User: user, Domain: domain, Password: []byte(password)}
}

func setup(t *testing.T) (Settings, *directory, *clock) {
	t.Helper()
	clk := &clock{t: time.Date(2026, 11, 23, 9, 0, 0, 0, time.UTC)}
	s := validDuo()
	s.Now = clk.now
	return s, &directory{password: goodPassword, groups: operators()}, clk
}

func TestSignInSuccess(t *testing.T) {
	s, dir, clk := setup(t)
	f := &fakeFactor{}
	cred := newCred("a-alex", "EXAMPLE", goodPassword)
	factor := []byte("push")
	sess, err := SignIn(context.Background(), s, cred, factor, dir.open, f)
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	if sess.Account != `EXAMPLE\a-alex` || sess.DN != alexDN || sess.DisplayName != "Alex Admin (admin)" ||
		sess.Method != MethodPasswordDuo || sess.DC() != "dc1.example.com" || !sess.Started.Equal(clk.now()) {
		t.Fatalf("session: %+v dc %s", sess, sess.DC())
	}
	if !sess.HasRole(RoleOperator) || sess.HasRole(RoleApprover) {
		t.Fatalf("roles %v", sess.Roles())
	}
	if len(f.users) != 1 || f.users[0] != "a-alex" || f.factors[0] != "push" || len(f.states[0]) != 0 {
		t.Fatalf("second factor called with %v %v", f.users, f.factors)
	}
	if !bytes.Equal(factor, make([]byte, 4)) {
		t.Fatal("factor not wiped")
	}
	if got := sessionPassword(t, sess); got != goodPassword {
		t.Fatal("the session must keep the credential while it lasts")
	}
	cfg := dir.configs[0]
	if cfg.Domain != "example.com" || cfg.BaseDN != "DC=example,DC=com" || !cfg.KerberosOnly {
		t.Fatalf("config %+v", cfg)
	}
	w, err := sess.Writer()
	if err != nil || w != ad.Writer(dir.last()) {
		t.Fatalf("writer %v %v", w, err)
	}
}

func TestSignInForms(t *testing.T) {
	s, dir, _ := setup(t)
	s.RADIUS.UsernameFormat = UsernameUPN
	dir.groups = append(operators(), ad.Group{Name: "ZTG Directory Approvers"})
	for _, cred := range []*ad.Credential{newCred(`EXAMPLE\a-alex`, "", goodPassword), newCred("A-ALEX@example.com", "", goodPassword)} {
		f := &fakeFactor{}
		sess, err := SignIn(context.Background(), s, cred, []byte("123456"), dir.open, f)
		if err != nil {
			t.Fatalf("%s: %v", cred.User, err)
		}
		if f.users[0] != "a-alex@example.com" || !sess.HasRole(RoleOperator) || !sess.HasRole(RoleApprover) {
			t.Fatalf("user %q roles %v", f.users[0], sess.Roles())
		}
		sess.Close()
	}
}

func TestSignInFailuresWipeAndClose(t *testing.T) {
	rejected := &fakeFactor{reply: func(int, string, []byte) error { return ErrRejected }}
	cases := []struct {
		name     string
		mutate   func(*Settings, *directory)
		password string
		second   SecondFactor
		want     error
		opened   bool
	}{
		{"wrong password", nil, "wrong", &fakeFactor{}, nil, false},
		{"in neither group", func(_ *Settings, d *directory) { d.groups = []ad.Group{{Name: "Domain Admins"}} }, goodPassword, &fakeFactor{}, ErrNotAuthorized, true},
		{"groups unreadable", func(_ *Settings, d *directory) { d.badGroups = true }, goodPassword, &fakeFactor{}, ErrNotAuthorized, true},
		{"unresolved groups only", func(_ *Settings, d *directory) { d.groups = []ad.Group{{SID: "S-1-5-21-1-2-3-1111"}} }, goodPassword, &fakeFactor{}, ErrNotAuthorized, true},
		{"second factor rejected", nil, goodPassword, rejected, ErrRejected, true},
		{"second factor missing", nil, goodPassword, nil, nil, false},
		{"password only not allowed", func(s *Settings, _ *directory) { s.SecondFactor = "" }, goodPassword, nil, ErrPasswordOnlyNotAllowed, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, dir, _ := setup(t)
			if tc.mutate != nil {
				tc.mutate(&s, dir)
			}
			cred := newCred("a-alex", "EXAMPLE", tc.password)
			pw := cred.Password
			sess, err := SignIn(context.Background(), s, cred, []byte("push"), dir.open, tc.second)
			if err == nil || sess != nil {
				t.Fatal("sign-in succeeded")
			}
			if tc.want != nil && !errors.Is(err, tc.want) {
				t.Fatalf("got %v, want %v", err, tc.want)
			}
			if cred.Password != nil || cred.User != "" || !bytes.Equal(pw, make([]byte, len(pw))) {
				t.Fatal("credential not wiped")
			}
			if tc.opened != (len(dir.opened) == 1) {
				t.Fatalf("opened %d writers", len(dir.opened))
			}
			if tc.opened && !isClosed(dir.last()) {
				t.Fatal("writer not closed")
			}
		})
	}
}

func TestSignInPasswordOnlyAllowed(t *testing.T) {
	s, dir, _ := setup(t)
	s.SecondFactor, s.AllowPasswordOnly = "", true
	sess, err := SignIn(context.Background(), s, newCred("a-alex", "EXAMPLE", goodPassword), nil, dir.open, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	if sess.Method != MethodPassword {
		t.Fatalf("method %s", sess.Method)
	}
	if _, err := SignIn(context.Background(), s, newCred("a-alex", "EXAMPLE", goodPassword), nil, dir.open, &fakeFactor{}); err == nil {
		t.Fatal("an unconfigured second factor was accepted")
	}
}

func TestSignInChallenge(t *testing.T) {
	s, dir, _ := setup(t)
	s.SecondFactor = SecondFactorRADIUS
	f := &fakeFactor{reply: func(n int, factor string, state []byte) error {
		if n == 1 {
			return &ChallengeError{Message: "Enter the code from the app", State: []byte("s1")}
		}
		if factor == "654321" && string(state) == "s1" {
			return nil
		}
		return ErrRejected
	}}
	var asked string
	s.Respond = func(_ context.Context, msg string) ([]byte, error) { asked = msg; return []byte("654321"), nil }
	sess, err := SignIn(context.Background(), s, newCred("a-alex", "EXAMPLE", goodPassword), []byte("x"), dir.open, f)
	if err != nil {
		t.Fatal(err)
	}
	sess.Close()
	if asked != "Enter the code from the app" || sess.Method != MethodPasswordRADIUS {
		t.Fatalf("asked %q method %s", asked, sess.Method)
	}

	s.Respond = nil
	f.users = nil
	_, err = SignIn(context.Background(), s, newCred("a-alex", "EXAMPLE", goodPassword), []byte("x"), dir.open, f)
	var ch *ChallengeError
	if !errors.As(err, &ch) {
		t.Fatalf("challenge without Respond: %v", err)
	}
}

func signedIn(t *testing.T) (*Session, *ad.Credential, *directory, *clock) {
	t.Helper()
	s, dir, clk := setup(t)
	cred := newCred("a-alex", "EXAMPLE", goodPassword)
	sess, err := SignIn(context.Background(), s, cred, []byte("push"), dir.open, &fakeFactor{})
	if err != nil {
		t.Fatal(err)
	}
	return sess, cred, dir, clk
}

func TestIdleLock(t *testing.T) {
	sess, _, _, clk := signedIn(t)
	defer sess.Close()
	clk.advance(14 * time.Minute)
	sess.Touch()
	clk.advance(14 * time.Minute)
	if sess.Locked() {
		t.Fatal("locked although active")
	}
	clk.advance(time.Minute)
	if !sess.Locked() {
		t.Fatal("not locked after 15 idle minutes")
	}
	sess.Touch()
	if !sess.Locked() {
		t.Fatal("Touch unlocked an idle-locked session")
	}
	if _, err := sess.Writer(); !errors.Is(err, ErrLocked) {
		t.Fatalf("writer while locked: %v", err)
	}
	if _, err := sess.Connect(context.Background(), ""); !errors.Is(err, ErrLocked) {
		t.Fatalf("connect while locked: %v", err)
	}
}

func TestUnlock(t *testing.T) {
	sess, cred, dir, clk := signedIn(t)
	defer sess.Close()
	first := dir.last()
	sess.Lock()
	if !sess.Locked() {
		t.Fatal("Lock did not lock")
	}
	wrong := []byte("nope")
	if err := sess.Unlock(context.Background(), wrong); err == nil {
		t.Fatal("wrong password unlocked")
	}
	if !bytes.Equal(wrong, make([]byte, 4)) || !sess.Locked() || sessionPassword(t, sess) != goodPassword {
		t.Fatal("wrong password: not wiped, unlocked, or the old credential changed")
	}
	clk.advance(30 * time.Minute)
	good := []byte(goodPassword)
	if err := sess.Unlock(context.Background(), good); err != nil {
		t.Fatal(err)
	}
	if sess.Locked() || !bytes.Equal(good, make([]byte, len(good))) {
		t.Fatal("not unlocked, or password not wiped")
	}
	if cred.Password != nil || !isClosed(first) {
		t.Fatal("old credential not wiped or old writer not closed")
	}
	w, err := sess.Writer()
	if err != nil || w != ad.Writer(dir.last()) || dir.last() == first {
		t.Fatalf("writer after unlock: %v", err)
	}
	if !sess.LastActivity().Equal(clk.now()) {
		t.Fatal("unlock did not count as activity")
	}

	// An account removed from both groups cannot unlock.
	sess.Lock()
	dir.groups = nil
	if err := sess.Unlock(context.Background(), []byte(goodPassword)); !errors.Is(err, ErrNotAuthorized) || !sess.Locked() {
		t.Fatalf("unlock without roles: %v", err)
	}
	if !isClosed(dir.last()) {
		t.Fatal("refused unlock left its writer open")
	}
}

func TestMaxSession(t *testing.T) {
	sess, cred, dir, clk := signedIn(t)
	defer sess.Close()
	for i := 0; i < 8*4-1; i++ {
		clk.advance(15*time.Minute - time.Second)
		sess.Touch()
	}
	if sess.Expired() {
		t.Fatal("expired early")
	}
	clk.advance(time.Hour)
	if !sess.Expired() {
		t.Fatal("not expired after 8 hours")
	}
	if cred.Password != nil || !isClosed(dir.last()) {
		t.Fatal("expiry did not erase the credential and close the writer")
	}
	if _, err := sess.Writer(); !errors.Is(err, ErrExpired) {
		t.Fatalf("writer after expiry: %v", err)
	}
	if err := sess.Unlock(context.Background(), []byte(goodPassword)); !errors.Is(err, ErrExpired) {
		t.Fatalf("unlock after expiry: %v", err)
	}
	if sess.HasRole(RoleOperator) {
		t.Fatal("an expired session holds a role")
	}
}

func TestClose(t *testing.T) {
	sess, cred, dir, _ := signedIn(t)
	pw := cred.Password
	if err := sess.Close(); err != nil {
		t.Fatal(err)
	}
	if cred.Password != nil || !bytes.Equal(pw, make([]byte, len(pw))) || !isClosed(dir.last()) {
		t.Fatal("Close did not wipe the credential and close the writer")
	}
	if _, err := sess.Writer(); !errors.Is(err, ErrSessionClosed) {
		t.Fatalf("writer after close: %v", err)
	}
	if err := sess.Unlock(context.Background(), []byte(goodPassword)); !errors.Is(err, ErrSessionClosed) {
		t.Fatalf("unlock after close: %v", err)
	}
	if sess.HasRole(RoleOperator) || sess.Close() != nil {
		t.Fatal("closed session holds a role, or second Close failed")
	}
}

func TestConnect(t *testing.T) {
	sess, _, dir, _ := signedIn(t)
	defer sess.Close()
	w, err := sess.Connect(context.Background(), "pdc.example.com")
	if err != nil {
		t.Fatal(err)
	}
	w.Close()
	if got := dir.configs[len(dir.configs)-1]; got.Server != "pdc.example.com" {
		t.Fatalf("server %q", got.Server)
	}
	if sessionPassword(t, sess) != goodPassword {
		t.Fatal("Connect changed the session credential")
	}
}

func TestSessionConcurrentUse(t *testing.T) {
	sess, _, _, clk := signedIn(t)
	defer sess.Close()
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				sess.Touch()
				sess.Locked()
				sess.HasRole(RoleOperator)
				sess.Writer()
				clk.advance(time.Millisecond)
			}
		}()
	}
	wg.Add(1)
	go func() { defer wg.Done(); sess.Unlock(context.Background(), []byte(goodPassword)) }()
	wg.Wait()
}

// sessionPassword decrypts the session's protected credential (memprotect.go).
func sessionPassword(t *testing.T, s *Session) string {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	pw, err := revealPassword(s.cred.Password, s.pwLen)
	if err != nil {
		t.Fatal(err)
	}
	defer wipe(pw)
	return string(pw)
}
