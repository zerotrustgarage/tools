package console

import (
	"bytes"
	"context"
	"errors"
	"strings"

	"github.com/zerotrustgarage/tools/directory/engine/dirsync"
	"github.com/zerotrustgarage/tools/directory/engine/mirror"
	"github.com/zerotrustgarage/tools/directory/engine/record"
	"github.com/zerotrustgarage/tools/directory/engine/template"
	"github.com/zerotrustgarage/tools/directory/internal/update"
)

// API is the exact surface the window binds (Wails binds every exported
// method of a bound struct). The desktop app and the UI test server both bind
// an API, so the frontend is tested against the real console logic.
type API struct {
	s      *Service
	wiring *LiveWiring
}

// NewAPI wraps a Service. wiring enables the first-run setup (nil disables it,
// as in demo mode).
func NewAPI(s *Service, wiring *LiveWiring) *API { return &API{s: s, wiring: wiring} }

// Setup saves the sign-in settings and switches to the live domain.
func (a *API) Setup(r SetupRequest) (Status, error) {
	if a.wiring == nil {
		return a.s.Status(), errors.New("setup is not available in this mode")
	}
	return a.s.Configure(*a.wiring, r)
}

// CheckUpdates is the explicit update check (repository rule 7). Until the
// first signed release it reports that this build is unreleased and makes no
// network request.
func (a *API) CheckUpdates() (string, error) {
	var out, errOut bytes.Buffer
	err := update.DirectoryCommand(context.Background(), a.s.cfg.Version, nil, &out, &errOut)
	return strings.TrimSpace(out.String()), err
}

func (a *API) Status() Status                                { return a.s.Status() }
func (a *API) SignIn(r SignInRequest) (Status, error)        { return a.s.SignIn(r) }
func (a *API) SignOut() Status                               { return a.s.SignOut() }
func (a *API) Lock() Status                                  { return a.s.Lock() }
func (a *API) Unlock(password string) (Status, error)        { return a.s.Unlock(password) }
func (a *API) Search(q SearchQuery) (SearchResponse, error)  { return a.s.Search(q) }
func (a *API) Tree(dn string) ([]TreeNode, error)            { return a.s.Tree(dn) }
func (a *API) Lookup(text, class string) ([]Row, error)      { return a.s.Lookup(text, class) }
func (a *API) Object(dn string) (ObjectView, error)          { return a.s.Object(dn) }
func (a *API) EffectiveGroups(dn string) ([]Ref, error)      { return a.s.EffectiveGroups(dn) }
func (a *API) Deleted(text string) ([]Row, error)            { return a.s.Deleted(text) }
func (a *API) PlanEdits(r EditRequest) (PlanView, error)     { return a.s.PlanEdits(r) }
func (a *API) Templates() TemplateList                       { return a.s.TemplateList() }
func (a *API) Apply(r ApplyRequest) (ApplyResponse, error)   { return a.s.Apply(r) }
func (a *API) Records(q RecordQuery) ([]record.Entry, error) { return a.s.Records(q) }
func (a *API) VerifyRecords() ([]record.Problem, error)      { return a.s.VerifyRecords() }
func (a *API) RecordsFolder() string                         { return a.s.RecordsFolder() }
func (a *API) PreviewTemplate(name string, form map[string]string) (template.Preview, error) {
	return a.s.PreviewTemplate(name, form)
}
func (a *API) PlanTemplate(name string, form map[string]string, ticket string) (PlanView, error) {
	return a.s.PlanTemplate(name, form, ticket)
}
func (a *API) SaveTemplate(t template.Template) (TemplateInfo, error) { return a.s.SaveTemplate(t) }

// Microsoft 365 and directory synchronization (cloud.go).
func (a *API) CloudStatus() CloudStatus                { return a.s.CloudStatus() }
func (a *API) CloudSignIn() (CloudStatus, error)       { return a.s.CloudSignIn() }
func (a *API) CloudSignOut() CloudStatus               { return a.s.CloudSignOut() }
func (a *API) Mirror(dn string) (mirror.View, error)   { return a.s.Mirror(dn) }
func (a *API) SyncHealth() (dirsync.Health, error)     { return a.s.SyncHealth() }
func (a *API) SyncNow(dn string) (SyncResponse, error) { return a.s.SyncNow(dn) }
