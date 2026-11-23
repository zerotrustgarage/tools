package console

import (
	"github.com/zerotrustgarage/tools/directory/engine/record"
	"github.com/zerotrustgarage/tools/directory/engine/template"
)

// API is the exact surface the window binds (Wails binds every exported
// method of a bound struct). The desktop app and the UI test server both bind
// an API, so the frontend is tested against the real console logic.
type API struct{ s *Service }

// NewAPI wraps a Service.
func NewAPI(s *Service) *API { return &API{s: s} }

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
