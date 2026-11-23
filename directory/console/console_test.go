package console

import (
	"strings"
	"testing"

	"github.com/zerotrustgarage/tools/directory/engine/demo"
)

func newDemo(t *testing.T) (*Service, *DemoBackend) {
	b := NewDemoBackend()
	s, err := New(b, Config{DataDir: t.TempDir(), Device: "test-ws"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SeedTemplates(demo.Templates()); err != nil {
		t.Fatal(err)
	}
	return s, b
}

func TestSignInRolesAndLock(t *testing.T) {
	s, _ := newDemo(t)
	if _, err := s.Search(SearchQuery{Text: "bea"}); err == nil {
		t.Fatal("search allowed before sign-in")
	}
	if _, err := s.SignIn(SignInRequest{User: "ztg.tech", Password: "wrong"}); err == nil {
		t.Fatal("wrong password accepted")
	}
	if _, err := s.SignIn(SignInRequest{User: "bea.okafor", Password: DemoPassword}); err == nil || !strings.Contains(err.Error(), "Operators") {
		t.Fatalf("account outside the groups accepted: %v", err)
	}
	st, err := s.SignIn(SignInRequest{User: `LAB\ztg.tech`, Password: DemoPassword})
	if err != nil || !st.SignedIn || st.Account != `LAB\ztg.tech` || len(st.Roles) != 1 {
		t.Fatalf("sign in: %+v %v", st, err)
	}
	s.Lock()
	if _, err := s.Search(SearchQuery{Text: "bea"}); err == nil {
		t.Fatal("search allowed while locked")
	}
	if _, err := s.Unlock(DemoPassword); err != nil {
		t.Fatal(err)
	}
	recs, _ := s.Records(RecordQuery{})
	kinds := []string{}
	for _, r := range recs {
		kinds = append(kinds, r.Kind)
	}
	if strings.Join(kinds, ",") != "unlock,lock,sign-in,sign-in-failed,sign-in-failed" {
		t.Fatalf("records: %v", kinds)
	}
	for _, r := range recs {
		if strings.Contains(r.Error+r.Note, DemoPassword) {
			t.Fatal("password in a record")
		}
	}
}

func TestSearchFiltersTreeAndObject(t *testing.T) {
	s, _ := newDemo(t)
	s.SignIn(SignInRequest{User: "ztg.tech", Password: DemoPassword})
	res, err := s.Search(SearchQuery{Text: "okafor"})
	if err != nil || len(res.Rows) != 1 || res.Rows[0].Kind != "user" || res.Rows[0].Location != "Contoso Labs / Staff / Finance" {
		t.Fatalf("search: %+v %v", res, err)
	}
	res, _ = s.Search(SearchQuery{Class: "user", Filters: []string{"locked"}})
	if len(res.Rows) != 1 || res.Rows[0].Name != "Caleb Nakamura" || !res.Rows[0].Locked {
		t.Fatalf("locked filter: %+v", res.Rows)
	}
	res, _ = s.Search(SearchQuery{Class: "user", Filters: []string{"disabled"}})
	if len(res.Rows) != 3 { // two leavers and krbtgt
		t.Fatalf("disabled filter: %d", len(res.Rows))
	}
	res, _ = s.Search(SearchQuery{Class: "user", InactiveDays: 90})
	if len(res.Rows) == 0 {
		t.Fatal("inactive filter empty")
	}
	tree, err := s.Tree("")
	if err != nil || len(tree) < 3 || tree[0].Kind != "ou" {
		t.Fatalf("tree: %+v %v", tree, err)
	}
	v, err := s.Object(res.Rows[0].DN)
	if err != nil || len(v.Sections) == 0 || len(v.Timeline) == 0 {
		t.Fatalf("object: %+v %v", v, err)
	}
	caleb, _ := s.Search(SearchQuery{Text: "Caleb"})
	v, _ = s.Object(caleb.Rows[0].DN)
	if !v.Locked || !v.CanUnlock || v.Manager == nil || len(v.MemberOf) != 3 {
		t.Fatalf("caleb view: %+v", v)
	}
	editable := false
	for _, sec := range v.Sections {
		for _, f := range sec.Fields {
			if f.Attribute == "title" && f.Editable && f.Synced {
				editable = true
			}
		}
	}
	if !editable {
		t.Fatal("title should be editable and marked synced")
	}
	admin, _ := s.Search(SearchQuery{Text: "Riley"})
	v, _ = s.Object(admin.Rows[0].DN)
	if v.Privileged == "" || v.CanEnable {
		t.Fatalf("admin view should be Tier 0 and read-only: %+v", v.Badges)
	}
	groups, err := s.EffectiveGroups(caleb.Rows[0].DN)
	if err != nil || len(groups) != 3 {
		t.Fatalf("effective groups: %+v %v", groups, err)
	}
}

func TestEditPlanApplyAndTemplate(t *testing.T) {
	s, _ := newDemo(t)
	s.SignIn(SignInRequest{User: "ztg.tech", Password: DemoPassword})
	caleb, _ := s.Search(SearchQuery{Text: "Caleb"})
	dn := caleb.Rows[0].DN
	pv, err := s.PlanEdits(EditRequest{Ticket: "T-9", Edits: []Edit{
		{Kind: "unlock", DN: dn}, {Kind: "reset-password", DN: dn, MustChange: true},
		{Kind: "set", DN: dn, Attribute: "title", Values: []string{"Senior Payroll Specialist"}},
		{Kind: "expires", DN: dn, Date: "2027-06-30"},
	}})
	if err != nil || pv.Blocked || len(pv.Changes) != 4 {
		t.Fatalf("plan: %+v %v", pv, err)
	}
	out, err := s.Apply(ApplyRequest{PlanID: pv.ID, Hash: pv.Hash})
	if err != nil || !out.OK {
		t.Fatalf("apply: %+v %v", out, err)
	}
	if _, err := s.Apply(ApplyRequest{PlanID: pv.ID, Hash: pv.Hash}); err == nil {
		t.Fatal("a plan ran twice")
	}
	v, _ := s.Object(dn)
	if v.Locked || v.Subtitle != "Senior Payroll Specialist" {
		t.Fatalf("after apply: %+v", v)
	}
	found := false
	for _, e := range v.Timeline {
		found = found || e.Kind == "record"
	}
	if !found {
		t.Fatal("timeline lacks record entries")
	}
	ts, errs := s.Templates()
	if len(errs) != 0 || len(ts) != 6 {
		t.Fatalf("templates: %d %v", len(ts), errs)
	}
	prev, err := s.PreviewTemplate("Sales staff", map[string]string{"givenName": "Jade", "sn": "Mbeki", "title": "AE", "employeeID": "E3001"})
	if err != nil || prev.SAMAccountName != "jade.mbeki2" {
		t.Fatalf("preview: %+v %v", prev, err)
	}
	pv, err = s.PlanTemplate("Sales staff", map[string]string{"givenName": "Rowan", "sn": "Ellis", "title": "AE", "employeeID": "E3002"}, "T-10")
	if err != nil || pv.Blocked {
		t.Fatalf("template plan: %+v %v", pv, err)
	}
	out, err = s.Apply(ApplyRequest{PlanID: pv.ID, Hash: pv.Hash})
	if err != nil || !out.OK || out.Steps[0].Password == "" {
		t.Fatalf("template apply: %+v %v", out, err)
	}
	if probs, _ := s.VerifyRecords(); len(probs) != 0 {
		t.Fatalf("records chain: %v", probs)
	}
}

func TestRefusedOutsideRightsAndDeletedSearch(t *testing.T) {
	s, _ := newDemo(t)
	s.SignIn(SignInRequest{User: "ztg.tech", Password: DemoPassword})
	admin, _ := s.Search(SearchQuery{Text: "Riley"})
	pv, err := s.PlanEdits(EditRequest{Edits: []Edit{{Kind: "set", DN: admin.Rows[0].DN, Attribute: "title", Values: []string{"x"}}}})
	if err != nil || !pv.Blocked || !pv.Tier0 {
		t.Fatalf("expected a blocked Tier 0 plan: %+v %v", pv, err)
	}
	if _, err := s.Apply(ApplyRequest{PlanID: pv.ID, Hash: pv.Hash}); err == nil {
		t.Fatal("blocked plan applied")
	}
	nia, _ := s.Search(SearchQuery{Text: "Nia"})
	pv, _ = s.PlanEdits(EditRequest{Edits: []Edit{{Kind: "delete", DN: nia.Rows[0].DN}}})
	if _, err := s.Apply(ApplyRequest{PlanID: pv.ID, Hash: pv.Hash}); err != nil {
		t.Fatal(err)
	}
	del, err := s.Deleted("Nia")
	if err != nil || len(del) != 1 || del[0].Location != "Contoso Labs / Leavers" {
		t.Fatalf("deleted: %+v %v", del, err)
	}
}
