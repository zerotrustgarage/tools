package console

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/zerotrustgarage/tools/directory/engine/apply"
	"github.com/zerotrustgarage/tools/directory/engine/plan"
	"github.com/zerotrustgarage/tools/directory/engine/record"
	"github.com/zerotrustgarage/tools/directory/engine/template"
)

// Edit is one requested change from the console.
type Edit struct {
	// Kind: set, add-member, remove-member, move, rename, enable, disable,
	// unlock, reset-password, expires, delete, undelete.
	Kind       string   `json:"kind"`
	DN         string   `json:"dn"`
	Attribute  string   `json:"attribute,omitempty"`
	Values     []string `json:"values,omitempty"`
	Target     string   `json:"target,omitempty"` // member DN, new parent OU, new name, or restore DN
	MustChange bool     `json:"mustChange,omitempty"`
	Date       string   `json:"date,omitempty"` // YYYY-MM-DD for expires; empty clears
}

// EditRequest asks for a plan.
type EditRequest struct {
	Title  string `json:"title"`
	Ticket string `json:"ticket"`
	Edits  []Edit `json:"edits"`
}

// ChangeView is one planned change for review.
type ChangeView struct {
	Kind      string   `json:"kind"`
	Summary   string   `json:"summary"`
	DN        string   `json:"dn"`
	Attribute string   `json:"attribute,omitempty"`
	Before    []string `json:"before,omitempty"`
	After     []string `json:"after,omitempty"`
}

// PlanView is a plan ready for review.
type PlanView struct {
	ID         string         `json:"id"`
	Hash       string         `json:"hash"`
	Title      string         `json:"title"`
	Ticket     string         `json:"ticket,omitempty"`
	DC         string         `json:"dc"`
	Technician string         `json:"technician"`
	Changes    []ChangeView   `json:"changes"`
	Warnings   []plan.Warning `json:"warnings"`
	Objects    int            `json:"objects"`
	TypedCount bool           `json:"typedCount"`
	Tier0      bool           `json:"tier0"`
	Blocked    bool           `json:"blocked"`
}

func view(p plan.Plan) PlanView {
	v := PlanView{ID: p.ID, Hash: p.Hash(), Title: p.Title, Ticket: p.Ticket, DC: p.DC, Technician: p.Technician, Warnings: p.Warnings,
		Objects: p.Objects, TypedCount: p.TypedCount, Tier0: p.Tier0, Blocked: p.Blocked()}
	if v.Warnings == nil {
		v.Warnings = []plan.Warning{}
	}
	for _, c := range p.Changes {
		cv := ChangeView{Kind: string(c.Kind), Summary: c.Summary, DN: c.DN, Attribute: c.Attribute, Before: c.Before, After: c.After}
		kind := "text"
		switch strings.ToLower(c.Attribute) {
		case "accountexpires":
			kind = "filetime"
		case "manager", "managedby":
			kind = "dn"
		}
		if kind != "text" {
			cv.Before, cv.After = formatValue(kind, c.Attribute, c.Before), formatValue(kind, c.Attribute, c.After)
		}
		if c.Kind == plan.KindCreate {
			for _, a := range c.Attributes {
				if strings.EqualFold(a.Name, "objectClass") {
					continue
				}
				cv.After = append(cv.After, a.Name+": "+strings.Join(a.Values, "; "))
			}
		}
		v.Changes = append(v.Changes, cv)
	}
	if v.Changes == nil {
		v.Changes = []ChangeView{}
	}
	return v
}

func (s *Service) keep(p plan.Plan) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.plans[p.ID] = p
	s.planOrder = append(s.planOrder, p.ID)
	for len(s.planOrder) > 50 {
		delete(s.plans, s.planOrder[0])
		s.planOrder = s.planOrder[1:]
	}
}

// PlanEdits builds a plan from console edits. Nothing is written.
func (s *Service) PlanEdits(req EditRequest) (PlanView, error) {
	w, err := s.writer()
	if err != nil {
		return PlanView{}, err
	}
	if len(req.Edits) == 0 {
		return PlanView{}, errors.New("there is nothing to change")
	}
	ctx, cancel := s.ctx()
	defer cancel()
	title := strings.TrimSpace(req.Title)
	if title == "" {
		title = fmt.Sprintf("%d change(s)", len(req.Edits))
	}
	b := plan.NewBuilder(w, title, req.Ticket, plan.Options{})
	for _, e := range req.Edits {
		switch e.Kind {
		case "set":
			b.Set(ctx, e.DN, e.Attribute, e.Values)
		case "add-member":
			b.AddMember(ctx, e.DN, e.Target)
		case "remove-member":
			b.RemoveMember(ctx, e.DN, e.Target)
		case "move":
			b.Move(ctx, e.DN, e.Target)
		case "rename":
			b.Rename(ctx, e.DN, e.Target)
		case "enable":
			b.SetEnabled(ctx, e.DN, true)
		case "disable":
			b.SetEnabled(ctx, e.DN, false)
		case "unlock":
			b.Unlock(ctx, e.DN)
		case "reset-password":
			b.ResetPassword(ctx, e.DN, e.MustChange)
		case "expires":
			var end time.Time
			if strings.TrimSpace(e.Date) != "" {
				end, err = time.Parse("2006-01-02", e.Date)
				if err != nil {
					return PlanView{}, fmt.Errorf("the end date must be YYYY-MM-DD")
				}
				end = end.Add(24 * time.Hour) // AD expires at the start of the day after
			}
			b.SetAccountExpires(ctx, e.DN, end)
		case "delete":
			b.Delete(ctx, e.DN)
		case "undelete":
			b.Undelete(ctx, e.DN, e.Target)
		default:
			return PlanView{}, fmt.Errorf("unknown change %q", e.Kind)
		}
	}
	p, err := b.Build()
	if err != nil {
		return PlanView{}, err
	}
	s.keep(p)
	return view(p), nil
}

// TemplateInfo describes a template for the New menu.
type TemplateInfo struct {
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Class       string            `json:"class"`
	OU          string            `json:"ou"`
	Version     int               `json:"version"`
	Groups      []string          `json:"groups"`
	Fields      []template.Field  `json:"fields"`
	Attributes  map[string]string `json:"attributes"`
	Enable      bool              `json:"enable"`
}

// TemplateList is the template folder's content.
type TemplateList struct {
	Templates []TemplateInfo `json:"templates"`
	Problems  []string       `json:"problems"`
	Folder    string         `json:"folder"`
}

// TemplateList returns Templates as one value (for the window binding).
func (s *Service) TemplateList() TemplateList {
	ts, errs := s.Templates()
	if errs == nil {
		errs = []string{}
	}
	return TemplateList{Templates: ts, Problems: errs, Folder: s.cfg.TemplatesDir}
}

// Templates lists the templates and any files that failed to load.
func (s *Service) Templates() ([]TemplateInfo, []string) {
	ts, errs := template.LoadDir(s.cfg.TemplatesDir)
	out := make([]TemplateInfo, 0, len(ts))
	for _, t := range ts {
		class := t.Class
		if class == "" {
			class = "user"
		}
		out = append(out, TemplateInfo{Name: t.Name, Description: t.Description, Class: class, OU: t.OU, Version: t.Version,
			Groups: t.Groups, Fields: t.Fields, Attributes: t.Attributes, Enable: t.Enable})
	}
	var msgs []string
	for _, e := range errs {
		msgs = append(msgs, e.Error())
	}
	return out, msgs
}

func (s *Service) template(name string) (template.Template, error) {
	ts, _ := template.LoadDir(s.cfg.TemplatesDir)
	for _, t := range ts {
		if t.Name == name {
			return t, nil
		}
	}
	return template.Template{}, fmt.Errorf("template %q was not found", name)
}

// PreviewTemplate computes names and attributes as the form is filled.
func (s *Service) PreviewTemplate(name string, form map[string]string) (template.Preview, error) {
	w, err := s.writer()
	if err != nil {
		return template.Preview{}, err
	}
	t, err := s.template(name)
	if err != nil {
		return template.Preview{}, err
	}
	ctx, cancel := s.ctx()
	defer cancel()
	return template.Build(ctx, w, t, template.Form(form))
}

// PlanTemplate builds the creation plan for a filled template form.
func (s *Service) PlanTemplate(name string, form map[string]string, ticket string) (PlanView, error) {
	w, err := s.writer()
	if err != nil {
		return PlanView{}, err
	}
	t, err := s.template(name)
	if err != nil {
		return PlanView{}, err
	}
	ctx, cancel := s.ctx()
	defer cancel()
	p, _, err := template.Plan(ctx, w, t, template.Form(form), ticket, plan.Options{})
	if err != nil {
		return PlanView{}, err
	}
	s.keep(p)
	return view(p), nil
}

// SaveTemplate validates and saves a template as a new version, and records it.
func (s *Service) SaveTemplate(t template.Template) (TemplateInfo, error) {
	st := s.b.Status()
	if !st.SignedIn || st.Locked {
		return TemplateInfo{}, errors.New("sign in to edit templates")
	}
	if old, err := s.template(t.Name); err == nil {
		t.Version = old.Version
	}
	saved, err := template.Save(s.cfg.TemplatesDir, t)
	if err != nil {
		return TemplateInfo{}, err
	}
	s.log.Append(record.Entry{Kind: record.KindTemplate, Technician: st.Account, Device: s.cfg.Device, Note: fmt.Sprintf("Template %s saved as version %d", saved.Name, saved.Version)})
	class := saved.Class
	if class == "" {
		class = "user"
	}
	return TemplateInfo{Name: saved.Name, Description: saved.Description, Class: class, OU: saved.OU, Version: saved.Version, Groups: saved.Groups, Fields: saved.Fields, Attributes: saved.Attributes, Enable: saved.Enable}, nil
}

// ApplyRequest confirms a reviewed plan.
type ApplyRequest struct {
	PlanID       string `json:"planId"`
	Hash         string `json:"hash"`
	TypedCount   int    `json:"typedCount"`
	ConfirmTier0 bool   `json:"confirmTier0"`
}

// StepView is one step's outcome.
type StepView struct {
	Summary  string `json:"summary"`
	Status   string `json:"status"`
	Error    string `json:"error,omitempty"`
	Password string `json:"password,omitempty"`
	DN       string `json:"dn"`
}

// ApplyResponse is the outcome of a run.
type ApplyResponse struct {
	OK    bool       `json:"ok"`
	Steps []StepView `json:"steps"`
	Job   string     `json:"job"`
}

// Apply runs a reviewed plan.
func (s *Service) Apply(req ApplyRequest) (ApplyResponse, error) {
	w, err := s.writer()
	if err != nil {
		return ApplyResponse{}, err
	}
	s.mu.Lock()
	p, ok := s.plans[req.PlanID]
	if ok {
		delete(s.plans, req.PlanID)
	}
	s.mu.Unlock()
	if !ok {
		return ApplyResponse{}, errors.New("this plan is no longer available; build it again")
	}
	ctx, cancel := s.ctx()
	defer cancel()
	st := s.b.Status()
	res, err := apply.Run(ctx, w, p, apply.Options{Log: s.log, ReviewedHash: req.Hash, TypedCount: req.TypedCount, ConfirmTier0: req.ConfirmTier0,
		Requester: st.Account, Method: st.Method, Device: s.cfg.Device})
	out := ApplyResponse{OK: res.OK, Job: p.ID}
	for _, step := range res.Steps {
		out.Steps = append(out.Steps, StepView{Summary: step.Change.Summary, Status: step.Status, Error: step.Error, Password: step.Password, DN: step.Change.DN})
	}
	if err != nil {
		// Checks that failed before anything was written leave the plan reusable.
		if len(res.Steps) == 0 {
			s.keep(p)
		}
		return out, err
	}
	return out, nil
}

// RecordQuery filters the change record view.
type RecordQuery struct {
	Text  string `json:"text"`
	Kind  string `json:"kind"`
	Days  int    `json:"days"`
	Limit int    `json:"limit"`
}

// Records returns change records, newest first.
func (s *Service) Records(q RecordQuery) ([]record.Entry, error) {
	f := record.Filter{Text: q.Text, Kind: q.Kind, Limit: q.Limit}
	if f.Limit <= 0 || f.Limit > 2000 {
		f.Limit = 500
	}
	if q.Days > 0 {
		f.Since = time.Now().AddDate(0, 0, -q.Days)
	}
	es, err := record.Read(s.cfg.RecordsDir, f)
	if err != nil {
		return nil, err
	}
	for i, j := 0, len(es)-1; i < j; i, j = i+1, j-1 {
		es[i], es[j] = es[j], es[i]
	}
	if es == nil {
		es = []record.Entry{}
	}
	return es, nil
}

// VerifyRecords checks every chain.
func (s *Service) VerifyRecords() ([]record.Problem, error) {
	p, err := record.Verify(s.cfg.RecordsDir)
	if p == nil {
		p = []record.Problem{}
	}
	return p, err
}

// RecordsFolder is where records are kept, for display.
func (s *Service) RecordsFolder() string { return s.cfg.RecordsDir }
