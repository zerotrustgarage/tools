// Package console is ZTG Directory's console logic, independent of the window
// it runs in: sign-in state, search, the object review page, plans, apply and
// change records. The Wails desktop app (directory/app) binds a Service; tests
// drive it against the demo directory.
package console

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/zerotrustgarage/tools/directory/engine/ad"
	"github.com/zerotrustgarage/tools/directory/engine/plan"
	"github.com/zerotrustgarage/tools/directory/engine/record"
	"github.com/zerotrustgarage/tools/directory/engine/template"
)

// Status is the session as the console shows it.
type Status struct {
	Mode         string   `json:"mode"` // demo or live
	SignedIn     bool     `json:"signedIn"`
	Locked       bool     `json:"locked"`
	Account      string   `json:"account,omitempty"`
	DisplayName  string   `json:"displayName,omitempty"`
	DN           string   `json:"dn,omitempty"`
	DC           string   `json:"dc,omitempty"`
	Domain       string   `json:"domain,omitempty"`
	BaseDN       string   `json:"baseDn,omitempty"`
	Roles        []string `json:"roles,omitempty"`
	Method       string   `json:"method,omitempty"`
	SecondFactor string   `json:"secondFactor,omitempty"` // "", duo, radius
	PasswordOnly bool     `json:"passwordOnly"`           // sign-ins without MFA are recorded as an open IA.L2-3.5.3 item
	Notice       string   `json:"notice,omitempty"`
	Device       string   `json:"device"`
	IdleMinutes  int      `json:"idleMinutes"`
	Version      string   `json:"version"`
	Hint         string   `json:"hint,omitempty"`
}

// Backend is the sign-in and connection source: the demo directory or a live
// domain through engine/auth.
type Backend interface {
	Status() Status
	SignIn(ctx context.Context, user, domain string, password, factor []byte) error
	SignOut() error
	Lock()
	Unlock(ctx context.Context, password []byte) error
	Touch()
	// Writer returns the signed-in connection, or an error when signed out,
	// locked or expired.
	Writer() (ad.Writer, error)
	BaseDN() string
}

// Config holds the console's folders.
type Config struct {
	DataDir      string
	RecordsDir   string // default DataDir/records
	TemplatesDir string // default DataDir/templates
	Device       string // default the host name
	Version      string
}

// Service is bound to the window. Every exported method is safe to call from
// the UI thread.
type Service struct {
	mu        sync.Mutex
	b         Backend
	cfg       Config
	log       *record.Log
	plans     map[string]plan.Plan
	planOrder []string
}

// New opens the change record and prepares the template folder.
func New(b Backend, cfg Config) (*Service, error) {
	if cfg.Device == "" {
		cfg.Device, _ = os.Hostname()
	}
	if cfg.Device == "" {
		cfg.Device = "console"
	}
	if cfg.RecordsDir == "" {
		cfg.RecordsDir = filepath.Join(cfg.DataDir, "records")
	}
	if cfg.TemplatesDir == "" {
		cfg.TemplatesDir = filepath.Join(cfg.DataDir, "templates")
	}
	if err := os.MkdirAll(cfg.TemplatesDir, 0o750); err != nil {
		return nil, err
	}
	l, err := record.Open(cfg.RecordsDir, cfg.Device, nil)
	if err != nil {
		return nil, fmt.Errorf("open change records: %w", err)
	}
	return &Service{b: b, cfg: cfg, log: l, plans: map[string]plan.Plan{}}, nil
}

// SeedTemplates writes ts to the template folder when it has no templates.
func (s *Service) SeedTemplates(ts []template.Template) error {
	existing, _ := template.LoadDir(s.cfg.TemplatesDir)
	if len(existing) > 0 {
		return nil
	}
	for _, t := range ts {
		t.Version--
		if _, err := template.Save(s.cfg.TemplatesDir, t); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) ctx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 60*time.Second)
}

// Status reports the session.
func (s *Service) Status() Status {
	st := s.b.Status()
	st.Device, st.Version = s.cfg.Device, s.cfg.Version
	return st
}

// SignInRequest carries a sign-in. The password is never logged or recorded.
type SignInRequest struct {
	User     string `json:"user"`
	Domain   string `json:"domain"`
	Password string `json:"password"`
	Factor   string `json:"factor"` // push, phone or a passcode when Duo or RADIUS is configured
}

// SignIn checks the account at a DC and records the attempt.
func (s *Service) SignIn(req SignInRequest) (Status, error) {
	ctx, cancel := s.ctx()
	defer cancel()
	user := strings.TrimSpace(req.User)
	if user == "" || req.Password == "" {
		return s.Status(), errors.New("enter your account and password")
	}
	pw, factor := []byte(req.Password), []byte(req.Factor)
	err := s.b.SignIn(ctx, user, strings.TrimSpace(req.Domain), pw, factor)
	for i := range pw {
		pw[i] = 0
	}
	for i := range factor {
		factor[i] = 0
	}
	st := s.Status()
	acct := user
	if req.Domain != "" && !strings.Contains(user, "@") {
		acct = req.Domain + `\` + user
	}
	if err != nil {
		s.log.Append(record.Entry{Kind: record.KindSignInFailed, Requester: acct, Technician: acct, Device: s.cfg.Device, Error: err.Error()})
		return st, err
	}
	note := ""
	if st.PasswordOnly {
		note = "Signed in without multifactor authentication: open IA.L2-3.5.3 item"
	}
	s.log.Append(record.Entry{Kind: record.KindSignIn, Requester: st.Account, Technician: st.Account, Actor: st.Account, DC: st.DC, Method: st.Method, Device: s.cfg.Device, Note: note})
	return st, nil
}

// SignOut ends the session and erases the credential.
func (s *Service) SignOut() Status {
	st := s.b.Status()
	if st.SignedIn {
		s.log.Append(record.Entry{Kind: record.KindSignOut, Technician: st.Account, DC: st.DC, Device: s.cfg.Device})
	}
	s.b.SignOut()
	s.mu.Lock()
	s.plans, s.planOrder = map[string]plan.Plan{}, nil
	s.mu.Unlock()
	return s.Status()
}

// Lock locks the session (the window also locks after the idle time).
func (s *Service) Lock() Status {
	st := s.b.Status()
	if st.SignedIn && !st.Locked {
		s.b.Lock()
		s.log.Append(record.Entry{Kind: record.KindLock, Technician: st.Account, Device: s.cfg.Device})
	}
	return s.Status()
}

// Unlock re-checks the password at a DC.
func (s *Service) Unlock(password string) (Status, error) {
	ctx, cancel := s.ctx()
	defer cancel()
	pw := []byte(password)
	err := s.b.Unlock(ctx, pw)
	for i := range pw {
		pw[i] = 0
	}
	st := s.Status()
	if err != nil {
		s.log.Append(record.Entry{Kind: record.KindSignInFailed, Technician: st.Account, Device: s.cfg.Device, Note: "unlock", Error: err.Error()})
		return st, err
	}
	s.log.Append(record.Entry{Kind: record.KindUnlock, Technician: st.Account, Device: s.cfg.Device})
	return st, nil
}

func (s *Service) writer() (ad.Writer, error) {
	w, err := s.b.Writer()
	if err != nil {
		return nil, err
	}
	s.b.Touch()
	return w, nil
}

// SearchQuery is a console search.
type SearchQuery struct {
	Text          string   `json:"text"`
	Class         string   `json:"class"`
	Filters       []string `json:"filters"`
	Under         string   `json:"under"`
	OneLevel      bool     `json:"oneLevel"`
	CreatedDays   int      `json:"createdDays"`
	InactiveDays  int      `json:"inactiveDays"`
	Limit         int      `json:"limit"`
	IncludeHidden bool     `json:"includeHidden"`
}

// SearchResponse holds rows and timing.
type SearchResponse struct {
	Rows      []Row `json:"rows"`
	Truncated bool  `json:"truncated"`
	Millis    int64 `json:"millis"`
}

func (q SearchQuery) request(now time.Time) ad.SearchRequest {
	r := ad.SearchRequest{Query: strings.TrimSpace(q.Text), Class: ad.Class(q.Class), Under: q.Under, OneLevel: q.OneLevel, Limit: q.Limit}
	for _, f := range q.Filters {
		r.Filters = append(r.Filters, ad.QuickFilter(f))
	}
	if q.CreatedDays > 0 {
		r.CreatedAfter = now.AddDate(0, 0, -q.CreatedDays)
	}
	if q.InactiveDays > 0 {
		r.InactiveSince = now.AddDate(0, 0, -q.InactiveDays)
	}
	if r.Limit <= 0 || r.Limit > 1000 {
		r.Limit = 200
	}
	return r
}

// Search runs a search and returns rows sorted by name.
func (s *Service) Search(q SearchQuery) (SearchResponse, error) {
	w, err := s.writer()
	if err != nil {
		return SearchResponse{}, err
	}
	ctx, cancel := s.ctx()
	defer cancel()
	start := time.Now()
	res, err := w.Search(ctx, q.request(time.Now()))
	if err != nil {
		return SearchResponse{}, err
	}
	out := SearchResponse{Truncated: res.Truncated, Rows: make([]Row, 0, len(res.Objects))}
	for _, o := range res.Objects {
		if !q.IncludeHidden && q.Text == "" && !q.OneLevel && KindOf(o) == "other" {
			continue
		}
		out.Rows = append(out.Rows, ToRow(o, s.b.BaseDN()))
	}
	sort.SliceStable(out.Rows, func(i, j int) bool {
		ri, rj := kindRank(out.Rows[i].Kind), kindRank(out.Rows[j].Kind)
		if ri != rj {
			return ri < rj
		}
		return strings.ToLower(out.Rows[i].Name) < strings.ToLower(out.Rows[j].Name)
	})
	out.Millis = time.Since(start).Milliseconds()
	return out, nil
}

func kindRank(k string) int {
	switch k {
	case "ou", "container":
		return 0
	case "user":
		return 1
	case "group":
		return 2
	case "contact":
		return 3
	case "computer":
		return 4
	}
	return 5
}

// TreeNode is one container in the OU tree.
type TreeNode struct {
	DN   string `json:"dn"`
	Name string `json:"name"`
	Kind string `json:"kind"`
}

// Tree returns the containers directly under dn ("" for the domain root).
func (s *Service) Tree(dn string) ([]TreeNode, error) {
	w, err := s.writer()
	if err != nil {
		return nil, err
	}
	if dn == "" {
		dn = s.b.BaseDN()
	}
	ctx, cancel := s.ctx()
	defer cancel()
	res, err := w.Search(ctx, ad.SearchRequest{Class: ad.ClassContainer, Under: dn, OneLevel: true, Limit: 1000})
	if err != nil {
		return nil, err
	}
	var out []TreeNode
	for _, o := range res.Objects {
		if strings.EqualFold(o.DN, dn) {
			continue
		}
		out = append(out, TreeNode{DN: o.DN, Name: Name(o), Kind: KindOf(o)})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return out[i].Kind == "ou"
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out, nil
}

// Lookup finds objects for pickers (manager, groups, members, target OU).
func (s *Service) Lookup(text, class string) ([]Row, error) {
	res, err := s.Search(SearchQuery{Text: text, Class: class, Limit: 25})
	if err != nil {
		return nil, err
	}
	return res.Rows, nil
}

// Object builds the review page for dn.
func (s *Service) Object(dn string) (ObjectView, error) {
	w, err := s.writer()
	if err != nil {
		return ObjectView{}, err
	}
	ctx, cancel := s.ctx()
	defer cancel()
	o, err := w.Get(ctx, dn)
	if err != nil {
		return ObjectView{}, err
	}
	rights, rerr := w.Rights(ctx, o.DN)
	v := buildView(o, rights, s.b.BaseDN())
	if rerr != nil {
		v.RightsWarning = "Your effective rights could not be read, so editing is off for this object: " + rerr.Error()
	}
	s.readiness(ctx, w, o, &v)
	if entries, err := record.Read(s.cfg.RecordsDir, record.Filter{Text: o.DN, Kind: record.KindChange, Limit: 50}); err == nil {
		for _, e := range entries {
			if e.Step == 0 || !strings.EqualFold(e.DN, o.DN) {
				continue
			}
			v.Timeline = append(v.Timeline, Event{Time: iso(e.Time), Kind: "record", Label: fmt.Sprintf("%s by %s (%s)", e.Note, e.Technician, e.Result)})
		}
	}
	sort.SliceStable(v.Timeline, func(i, j int) bool { return v.Timeline[i].Time > v.Timeline[j].Time })
	return v, nil
}

// EffectiveGroups lists every group dn belongs to, nesting included; slower
// than the direct list, so the page asks for it separately.
func (s *Service) EffectiveGroups(dn string) ([]Ref, error) {
	w, err := s.writer()
	if err != nil {
		return nil, err
	}
	ctx, cancel := s.ctx()
	defer cancel()
	o, err := w.Get(ctx, dn)
	if err != nil {
		return nil, err
	}
	gs, err := w.EffectiveGroups(ctx, dn)
	if err != nil {
		return nil, err
	}
	direct := map[string]bool{}
	for _, m := range o.Values("memberof") {
		direct[strings.ToLower(m)] = true
	}
	out := make([]Ref, 0, len(gs))
	for _, g := range gs {
		via := "nested"
		if g.Primary {
			via = "primary"
		} else if direct[strings.ToLower(g.DN)] {
			via = "direct"
		}
		name := g.Name
		if !g.Resolved {
			name = g.SID + " (outside the search base)"
		}
		out = append(out, Ref{DN: g.DN, Name: name, Kind: "group", Via: via})
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name) })
	return out, nil
}

// Deleted searches the AD Recycle Bin.
func (s *Service) Deleted(text string) ([]Row, error) {
	w, err := s.writer()
	if err != nil {
		return nil, err
	}
	ctx, cancel := s.ctx()
	defer cancel()
	res, err := w.Deleted(ctx, ad.SearchRequest{Query: text, Limit: 200})
	if err != nil {
		return nil, err
	}
	rows := make([]Row, 0, len(res.Objects))
	for _, o := range res.Objects {
		r := ToRow(o, s.b.BaseDN())
		r.Name = strings.SplitN(r.Name, "\n", 2)[0]
		if p := o.First("lastknownparent"); p != "" {
			r.Location = Location("CN=x,"+p, s.b.BaseDN())
		}
		rows = append(rows, r)
	}
	return rows, nil
}

func buildView(o ad.Object, rights ad.Rights, base string) ObjectView {
	v := ObjectView{DN: o.DN, Name: Name(o), Kind: KindOf(o), Location: Location(o.DN, base),
		CloudNote: "The Microsoft 365 mirror (cloud sign-in state, licenses, mailbox, sync status and Sync now) arrives with the Microsoft 365 connection in slice D3."}
	defs := ouSections
	switch v.Kind {
	case "user":
		defs = userSections
		v.Subtitle = firstNonEmpty(o.First("title"), o.First("userprincipalname"))
	case "group":
		defs = groupSections
		v.Subtitle = groupTypeText(o.First("grouptype"))
	case "computer":
		defs = computerSections
		v.Subtitle = o.First("operatingsystem")
	case "contact":
		defs = contactSections
		v.Subtitle = o.First("mail")
	default:
		v.Subtitle = o.First("description")
	}
	shown := map[string]bool{}
	for _, sec := range defs {
		out := Section{Title: sec.title}
		for _, fd := range sec.fields {
			attr := strings.ToLower(fd.attr)
			shown[attr] = true
			vals := o.Values(attr)
			editable := fd.kind != "readonly" && fd.kind != "date" && rights.CanWrite(attr) && !(fd.kind == "filetime" && attr != "accountexpires")
			if len(vals) == 0 && !editable && fd.kind != "text" {
				continue
			}
			out.Fields = append(out.Fields, Field{Attribute: fd.attr, Label: fd.label, Values: vals, Display: formatValue(fd.kind, attr, vals),
				Kind: fd.kind, Editable: editable, Synced: v.Kind == "user" && syncedUserAttributes[attr], Help: fd.help})
		}
		if v.Kind == "user" && sec.title == "Identity" {
			for i := 1; i <= 15; i++ {
				a := fmt.Sprintf("extensionattribute%d", i)
				shown[a] = true
				if vals := o.Values(a); len(vals) > 0 {
					out.Fields = append(out.Fields, Field{Attribute: fmt.Sprintf("extensionAttribute%d", i), Label: fmt.Sprintf("Extension attribute %d", i),
						Values: vals, Display: vals, Kind: "text", Editable: rights.CanWrite(a), Synced: true})
				}
			}
		}
		if len(out.Fields) > 0 {
			v.Sections = append(v.Sections, out)
		}
	}
	for _, a := range sortedKeys(o.Attributes) {
		vals := o.Values(a)
		kind := "readonly"
		switch a {
		case "whencreated", "whenchanged":
			kind = "date"
		case "lastlogon", "lastlogontimestamp", "pwdlastset", "accountexpires", "lockouttime", "badpasswordtime", "msds-userpasswordexpirytimecomputed":
			kind = "filetime"
		}
		v.Attributes = append(v.Attributes, Field{Attribute: a, Label: a, Values: vals, Display: formatValue(kind, a, vals), Kind: kind})
	}
	// Status, badges and capabilities.
	if e := enabledPtr(o); e != nil && (v.Kind == "user" || v.Kind == "computer") {
		v.Enabled = e
		v.CanEnable = rights.CanWrite("useraccountcontrol")
		if *e {
			v.Badges = append(v.Badges, Badge{"Enabled", "ok"})
		} else {
			v.Badges = append(v.Badges, Badge{"Disabled", "dim"})
		}
	}
	if v.Kind == "user" {
		v.Locked = locked(o)
		v.CanUnlock = v.Locked && rights.CanWrite("lockouttime")
		v.CanExpire = rights.CanWrite("accountexpires")
		if v.Locked {
			v.Badges = append(v.Badges, Badge{"Locked out", "crit"})
		}
		if o.First("pwdlastset") == "0" {
			v.Badges = append(v.Badges, Badge{"Must change password", "warn"})
		}
		if u, _ := uac(o); u&ad.UACDontExpirePassword != 0 {
			v.Badges = append(v.Badges, Badge{"Password never expires", "warn"})
		}
		if exp := ad.FileTime(o.First("accountexpires")); !exp.IsZero() {
			if exp.Before(time.Now()) {
				v.Badges = append(v.Badges, Badge{"Account expired", "crit"})
			} else {
				v.Badges = append(v.Badges, Badge{"Expires " + exp.Format("2006-01-02"), "info"})
			}
		}
		if t := timeOf("lastlogontimestamp", o); t.IsZero() {
			v.Badges = append(v.Badges, Badge{"Never logged on", "dim"})
		} else if time.Since(t) > 90*24*time.Hour {
			v.Badges = append(v.Badges, Badge{"Inactive 90+ days", "warn"})
		}
	}
	if o.First("admincount") == "1" {
		v.Privileged = "Protected account (adminCount = 1): treated as Tier 0."
		v.Badges = append(v.Badges, Badge{"Tier 0", "special"})
	}
	v.CanRename = rights.CanWrite("name") || rights.CanWrite("cn")
	v.CanMembers = v.Kind == "group" && rights.CanWrite("member")
	if m := o.First("manager"); m != "" {
		v.Manager = &Ref{DN: m, Name: rdnValue(m), Kind: "user"}
	}
	for _, r := range o.Values("directreports") {
		v.Reports = append(v.Reports, Ref{DN: r, Name: rdnValue(r), Kind: "user"})
	}
	for _, g := range o.Values("memberof") {
		v.MemberOf = append(v.MemberOf, Ref{DN: g, Name: rdnValue(g), Kind: "group", Via: "direct"})
	}
	for _, m := range o.Values("member") {
		v.Members = append(v.Members, Ref{DN: m, Name: rdnValue(m)})
	}
	sortRefs(v.Reports)
	sortRefs(v.MemberOf)
	sortRefs(v.Members)
	add := func(attr, label string) {
		if t := timeOf(attr, o); !t.IsZero() && !(attr == "pwdlastset" && o.First(attr) == "0") {
			v.Timeline = append(v.Timeline, Event{Time: iso(t), Label: label, Kind: "directory"})
		}
	}
	add("whencreated", "Created in Active Directory")
	add("pwdlastset", "Password set")
	add("lastlogontimestamp", "Last logon (replicated)")
	add("lockouttime", "Locked out")
	add("whenchanged", "Last changed")
	return v
}

func (s *Service) readiness(ctx context.Context, w ad.Writer, o ad.Object, v *ObjectView) {
	if v.Kind != "user" {
		return
	}
	add := func(label, status, detail string) {
		v.Readiness = append(v.Readiness, Check{Label: label, Status: status, Detail: detail})
	}
	upn := o.First("userprincipalname")
	switch {
	case upn == "":
		add("Sign-in name", "warn", "No userPrincipalName: the cloud account would get an onmicrosoft.com name.")
	case nonRoutable(upn):
		add("Sign-in name", "warn", "The UPN suffix "+upn[strings.LastIndexByte(upn, '@'):]+" is not routable on the internet and cannot be verified in Microsoft 365.")
	default:
		add("Sign-in name", "ok", upn)
	}
	mail := o.First("mail")
	var primaries []string
	for _, p := range o.Values("proxyaddresses") {
		if strings.HasPrefix(p, "SMTP:") {
			primaries = append(primaries, p[5:])
		}
	}
	switch {
	case len(primaries) > 1:
		add("Primary email", "warn", "More than one proxyAddresses value is marked primary (SMTP: in capitals).")
	case len(primaries) == 1 && mail != "" && !strings.EqualFold(primaries[0], mail):
		add("Primary email", "warn", "mail ("+mail+") differs from the primary proxy address ("+primaries[0]+").")
	case mail == "" && len(primaries) == 0:
		add("Primary email", "info", "No email address: no mailbox address will be set from AD.")
	default:
		add("Primary email", "ok", firstNonEmpty(mail, strings.Join(primaries, ", ")))
	}
	if o.First("msexchusagelocation") == "" {
		add("Usage location", "warn", "Not set: a Microsoft 365 license cannot be assigned until it is.")
	} else {
		add("Usage location", "ok", o.First("msexchusagelocation"))
	}
	n := 0
	for _, p := range o.Values("proxyaddresses") {
		if n >= 5 {
			break
		}
		n++
		addr := p[strings.IndexByte(p, ':')+1:]
		res, err := w.Search(ctx, ad.SearchRequest{Query: addr, Limit: 10})
		if err != nil {
			continue
		}
		for _, other := range res.Objects {
			if strings.EqualFold(other.DN, o.DN) {
				continue
			}
			for _, op := range other.Values("proxyaddresses") {
				if strings.EqualFold(op[strings.IndexByte(op, ':')+1:], addr) {
					add("Duplicate address", "warn", addr+" is also used by "+Name(other)+"; sync will report a duplicate attribute error.")
				}
			}
		}
	}
}

func nonRoutable(upn string) bool {
	at := strings.LastIndexByte(upn, '@')
	if at < 0 {
		return true
	}
	suffix := strings.ToLower(upn[at+1:])
	for _, bad := range []string{".local", ".lan", ".internal", ".corp", ".localdomain", ".intranet", ".private"} {
		if strings.HasSuffix(suffix, bad) {
			return true
		}
	}
	return !strings.Contains(suffix, ".")
}

func sortRefs(r []Ref) {
	sort.Slice(r, func(i, j int) bool { return strings.ToLower(r[i].Name) < strings.ToLower(r[j].Name) })
}

func sortedKeys(m map[string][]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
