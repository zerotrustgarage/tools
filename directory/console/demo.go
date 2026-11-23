package console

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/zerotrustgarage/tools/directory/engine/ad"
	"github.com/zerotrustgarage/tools/directory/engine/demo"
)

// DemoPassword is the password every demo account accepts.
const DemoPassword = "Demo-Password-1"

// DemoBackend serves the fictional lab.example directory from memory. It
// behaves like a live session (sign-in, roles, idle lock) so the console can
// be tried without a domain controller. Nothing leaves the process.
type DemoBackend struct {
	mu       sync.Mutex
	dir      *ad.FakeClient
	signedIn bool
	locked   bool
	account  string
	dn       string
	display  string
	roles    []string
	last     time.Time
	idle     time.Duration
}

// NewDemoBackend returns a fresh demo directory.
func NewDemoBackend() *DemoBackend {
	return &DemoBackend{dir: demo.NewDirectory(), idle: 15 * time.Minute}
}

// Directory exposes the in-memory directory for tests.
func (d *DemoBackend) Directory() *ad.FakeClient { return d.dir }

func (d *DemoBackend) BaseDN() string { return demo.Base }

func (d *DemoBackend) Status() Status {
	d.mu.Lock()
	defer d.mu.Unlock()
	st := Status{Mode: "demo", Domain: "lab.example", BaseDN: demo.Base, IdleMinutes: int(d.idle / time.Minute), PasswordOnly: true,
		Notice: "Demo mode: a fictional directory held in memory. Changes are real within this window and are recorded, but nothing reaches a domain controller.",
		Hint:   "Sign in as ztg.tech (operator) or kofi.asante (operator and approver) with the password " + DemoPassword + "."}
	if d.signedIn {
		st.SignedIn, st.Account, st.DN, st.DisplayName, st.Roles, st.DC, st.Method = true, d.account, d.dn, d.display, d.roles, d.dir.DC(), "password (demo)"
		st.Locked = d.locked || (d.idle > 0 && time.Since(d.last) > d.idle)
	}
	return st
}

func (d *DemoBackend) SignIn(ctx context.Context, user, domain string, password, factor []byte) error {
	if string(password) != DemoPassword {
		return errors.New("the user name or password is incorrect")
	}
	name := strings.ToLower(user)
	if i := strings.IndexByte(name, '\\'); i >= 0 {
		name = name[i+1:]
	}
	if i := strings.IndexByte(name, '@'); i >= 0 {
		name = name[:i]
	}
	res, err := d.dir.Search(ctx, ad.SearchRequest{Query: name, Class: ad.ClassUser, Limit: 20})
	if err != nil {
		return err
	}
	for _, o := range res.Objects {
		if !strings.EqualFold(o.First("samaccountname"), name) {
			continue
		}
		var roles []string
		for _, g := range o.Values("memberof") {
			switch strings.ToLower(rdnValue(g)) {
			case "ztg directory operators":
				roles = append(roles, "operator")
			case "ztg directory approvers":
				roles = append(roles, "approver")
			}
		}
		if len(roles) == 0 {
			return errors.New("this account is not a member of ZTG Directory Operators or Approvers")
		}
		d.mu.Lock()
		d.signedIn, d.locked, d.account, d.dn, d.display, d.roles, d.last = true, false, `LAB\`+o.First("samaccountname"), o.DN, Name(o), roles, time.Now()
		d.dir.Account = d.account
		d.mu.Unlock()
		return nil
	}
	return errors.New("the user name or password is incorrect")
}

func (d *DemoBackend) SignOut() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.signedIn, d.locked, d.account, d.dn, d.roles = false, false, "", "", nil
	return nil
}

func (d *DemoBackend) Lock() { d.mu.Lock(); d.locked = true; d.mu.Unlock() }

func (d *DemoBackend) Unlock(ctx context.Context, password []byte) error {
	if string(password) != DemoPassword {
		return errors.New("the password is incorrect")
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if !d.signedIn {
		return errors.New("not signed in")
	}
	d.locked, d.last = false, time.Now()
	return nil
}

func (d *DemoBackend) Touch() { d.mu.Lock(); d.last = time.Now(); d.mu.Unlock() }

func (d *DemoBackend) Writer() (ad.Writer, error) {
	st := d.Status()
	switch {
	case !st.SignedIn:
		return nil, errors.New("sign in first")
	case st.Locked:
		return nil, errors.New("the session is locked; enter your password to continue")
	}
	return d.dir, nil
}
