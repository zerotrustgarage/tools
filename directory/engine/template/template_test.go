package template_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zerotrustgarage/tools/directory/engine/apply"
	"github.com/zerotrustgarage/tools/directory/engine/demo"
	"github.com/zerotrustgarage/tools/directory/engine/plan"
	"github.com/zerotrustgarage/tools/directory/engine/record"
	"github.com/zerotrustgarage/tools/directory/engine/template"
)

var ctx = context.Background()

func finance(t *testing.T) template.Template {
	for _, tp := range demo.Templates() {
		if tp.Name == "Finance staff" {
			return tp
		}
	}
	t.Fatal("no finance template")
	return template.Template{}
}

func TestPreviewNamesFoldingAndCollisions(t *testing.T) {
	d := demo.NewDirectory()
	tp := finance(t)
	p, err := template.Build(ctx, d, tp, template.Form{"givenName": "Zoë", "sn": "Ångström-Müller", "title": "Analyst", "employeeID": "E2001"})
	if err != nil || len(p.Problems) > 0 {
		t.Fatalf("%v %v", err, p.Problems)
	}
	if p.SAMAccountName != "zoe.angstrom-muller" || p.UserPrincipalName != "zoe.angstrom-muller@contoso-labs.example" || p.CN != "Zoë Ångström-Müller" {
		t.Fatalf("names: %+v", p)
	}
	// Bea Okafor exists: the second Bea gets number 2 everywhere.
	p, _ = template.Build(ctx, d, tp, template.Form{"givenName": "Bea", "sn": "Okafor", "title": "Analyst", "employeeID": "E2002"})
	if p.SAMAccountName != "bea.okafor2" || p.Mail != "bea.okafor2@contoso-labs.example" || p.CN != "Bea Okafor 2" || len(p.Notes) == 0 {
		t.Fatalf("collision: %+v", p)
	}
	// Long names fit the 20-character sAMAccountName limit.
	p, _ = template.Build(ctx, d, tp, template.Form{"givenName": "Maximiliana", "sn": "Vanderbiltsworth", "title": "A", "employeeID": "E2003"})
	if len(p.SAMAccountName) > 20 {
		t.Fatalf("sAMAccountName too long: %q", p.SAMAccountName)
	}
}

func TestRequiredAndPatternFields(t *testing.T) {
	d := demo.NewDirectory()
	p, _ := template.Build(ctx, d, finance(t), template.Form{"givenName": "A", "sn": "B", "employeeID": "123", "manager": "CN=Nobody," + demo.Base})
	got := strings.Join(p.Problems, "|")
	for _, want := range []string{"Job title is required", "Employee ID is not in the expected format", "Manager was not found"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing problem %q in %q", want, got)
		}
	}
}

func TestPlanCreatesUserWithGroups(t *testing.T) {
	d := demo.NewDirectory()
	mgr := "CN=Avery Lindqvist,OU=Finance,OU=Staff,OU=Contoso Labs," + demo.Base
	pl, prev, err := template.Plan(ctx, d, finance(t), template.Form{"givenName": "Quinn", "sn": "Avery", "title": "Analyst", "employeeID": "E2010", "manager": mgr}, "T-7", plan.Options{})
	if err != nil || pl.Blocked() {
		t.Fatalf("plan: %v %+v", err, pl.Warnings)
	}
	if len(pl.Changes) != 4 { // create + 3 groups
		t.Fatalf("changes: %+v", pl.Changes)
	}
	l, _ := record.Open(t.TempDir(), "t", nil)
	res, err := apply.Run(ctx, d, pl, apply.Options{Log: l})
	if err != nil || !res.OK {
		t.Fatalf("apply: %v %+v", err, res.Steps)
	}
	u, err := d.Get(ctx, prev.DN)
	if err != nil || u.First("department") != "Finance" || u.First("manager") != mgr || len(u.Values("memberof")) != 3 || u.First("useraccountcontrol") != "512" {
		t.Fatalf("user: %+v %v", u, err)
	}
	m, _ := d.Get(ctx, mgr)
	found := false
	for _, r := range m.Values("directreports") {
		found = found || strings.EqualFold(r, prev.DN)
	}
	if !found {
		t.Fatal("manager's direct reports not updated")
	}
}

func TestGroupTemplateAndSaveLoad(t *testing.T) {
	d := demo.NewDirectory()
	var g template.Template
	for _, tp := range demo.Templates() {
		if tp.Class == "group" {
			g = tp
		}
	}
	pl, prev, err := template.Plan(ctx, d, g, template.Form{"name": "Payroll Approvers", "description": "Approve payroll runs"}, "", plan.Options{})
	if err != nil || len(pl.Changes) != 1 || prev.DN != "CN=Payroll Approvers,OU=Groups,OU=Contoso Labs,"+demo.Base {
		t.Fatalf("group plan: %v %+v %+v", err, pl, prev)
	}
	dir := t.TempDir()
	saved, err := template.Save(dir, g)
	if err != nil || saved.Version != 2 {
		t.Fatalf("save: %v %d", err, saved.Version)
	}
	all, errs := template.LoadDir(dir)
	if len(errs) != 0 || len(all) != 1 || all[0].Name != "Security group" {
		t.Fatalf("load: %v %v", all, errs)
	}
	os.WriteFile(filepath.Join(dir, "bad.json"), []byte(`{"name":"x","ou":"OU=a","bogus":1}`), 0o600)
	if _, errs = template.LoadDir(dir); len(errs) != 1 {
		t.Fatalf("unknown field accepted: %v", errs)
	}
	bad := g
	bad.Attributes = map[string]string{"unicodePwd": "x"}
	if bad.Validate() == nil {
		t.Fatal("template allowed to set a password")
	}
}
