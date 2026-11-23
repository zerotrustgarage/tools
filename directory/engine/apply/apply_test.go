package apply

import (
	"context"
	"strings"
	"testing"

	"github.com/zerotrustgarage/tools/directory/engine/ad"
	"github.com/zerotrustgarage/tools/directory/engine/demo"
	"github.com/zerotrustgarage/tools/directory/engine/plan"
	"github.com/zerotrustgarage/tools/directory/engine/record"
)

const fin = "OU=Finance,OU=Staff,OU=Contoso Labs," + demo.Base

var ctx = context.Background()

func logFor(t *testing.T) *record.Log {
	l, err := record.Open(t.TempDir(), "test", nil)
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func TestEditsApplyVerifyAndRecord(t *testing.T) {
	d := demo.NewDirectory()
	bea := "CN=Bea Okafor," + fin
	caleb := "CN=Caleb Nakamura," + fin
	b := plan.NewBuilder(d, "Edit Bea", "T-100", plan.Options{})
	b.Set(ctx, bea, "title", []string{"Senior Accountant"})
	b.Set(ctx, bea, "telephoneNumber", nil)
	b.Set(ctx, bea, "department", []string{"Finance"}) // unchanged: no step
	b.AddMember(ctx, "CN=Project Atlas,OU=Groups,OU=Contoso Labs,"+demo.Base, bea)
	b.RemoveMember(ctx, "CN=All Staff,OU=Groups,OU=Contoso Labs,"+demo.Base, bea)
	b.Unlock(ctx, caleb)
	b.ResetPassword(ctx, caleb, true)
	b.Move(ctx, bea, "OU=Sales,OU=Staff,OU=Contoso Labs,"+demo.Base)
	p, err := b.Build()
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Changes) != 7 || p.Blocked() || p.Tier0 {
		t.Fatalf("plan: %+v", p)
	}
	l := logFor(t)
	res, err := Run(ctx, d, p, Options{Log: l, ReviewedHash: p.Hash()})
	if err != nil || !res.OK {
		t.Fatalf("run: %+v %v", res, err)
	}
	moved, err := d.Get(ctx, "CN=Bea Okafor,OU=Sales,OU=Staff,OU=Contoso Labs,"+demo.Base)
	if err != nil || moved.First("title") != "Senior Accountant" || moved.First("telephonenumber") != "" {
		t.Fatalf("after: %+v %v", moved, err)
	}
	if !contains(moved.Values("memberof"), "Project Atlas") || contains(moved.Values("memberof"), "All Staff") {
		t.Fatalf("membership: %v", moved.Values("memberof"))
	}
	c, _ := d.Get(ctx, caleb)
	if c.First("lockouttime") != "0" || c.First("pwdlastset") != "0" {
		t.Fatalf("unlock/reset: %v %v", c.First("lockouttime"), c.First("pwdlastset"))
	}
	var pw string
	for _, s := range res.Steps {
		if s.Change.Kind == plan.KindResetPassword {
			pw = s.Password
		}
	}
	if len(pw) != 19 {
		t.Fatalf("temporary password not returned: %q", pw)
	}
	entries, _ := record.Read(l.Dir(), record.Filter{})
	if len(entries) != 9 { // start, 7 steps, finish
		t.Fatalf("record entries: %d", len(entries))
	}
	for _, e := range entries {
		if strings.Contains(strings.Join(e.After, " "), pw) || e.Technician != `LAB\ztg.tech` || e.DC != "dc01.lab.example" {
			t.Fatalf("record leaks or misattributes: %+v", e)
		}
	}
	if probs, _ := record.Verify(l.Dir()); len(probs) != 0 {
		t.Fatalf("chain: %v", probs)
	}
}

func TestRightsTier0AndBulkGuards(t *testing.T) {
	d := demo.NewDirectory()
	admin := "CN=Admin Riley Chen,OU=Admins,OU=Contoso Labs," + demo.Base
	b := plan.NewBuilder(d, "Admin", "", plan.Options{})
	b.Set(ctx, admin, "title", []string{"x"})
	p, _ := b.Build()
	if !p.Blocked() || !p.Tier0 {
		t.Fatalf("refusal and Tier 0 expected: %+v", p.Warnings)
	}
	if _, err := Run(ctx, d, p, Options{Log: logFor(t)}); err == nil {
		t.Fatal("blocked plan ran")
	}
	// Bulk cap: 3 objects with cap 2.
	b = plan.NewBuilder(d, "Bulk", "", plan.Options{BulkCap: 2})
	for _, n := range []string{"Bea Okafor", "Caleb Nakamura", "Avery Lindqvist"} {
		b.Set(ctx, "CN="+n+","+fin, "physicalDeliveryOfficeName", []string{"Remote"})
	}
	p, _ = b.Build()
	if !p.TypedCount || p.Objects != 3 {
		t.Fatalf("bulk: %+v", p)
	}
	if _, err := Run(ctx, d, p, Options{Log: logFor(t), TypedCount: 2}); err == nil {
		t.Fatal("wrong typed count accepted")
	}
	if res, err := Run(ctx, d, p, Options{Log: logFor(t), TypedCount: 3}); err != nil || !res.OK {
		t.Fatalf("bulk run: %v", err)
	}
}

func TestDriftStopsAndRemainingStepsAreNotRun(t *testing.T) {
	d := demo.NewDirectory()
	bea := "CN=Bea Okafor," + fin
	b := plan.NewBuilder(d, "Drift", "", plan.Options{})
	b.Set(ctx, bea, "title", []string{"A"})
	b.Set(ctx, bea, "department", []string{"B"})
	p, _ := b.Build()
	d.Modify(ctx, bea, []ad.Mod{{Op: ad.ModReplace, Name: "title", Values: []string{"Changed elsewhere"}}})
	res, err := Run(ctx, d, p, Options{Log: logFor(t)})
	if err != nil || res.OK || res.Steps[0].Status != StatusFailed || res.Steps[1].Status != StatusNotRun {
		t.Fatalf("drift: %+v %v", res, err)
	}
}

func TestCreateUserAndPasswordPolicyFailure(t *testing.T) {
	d := demo.NewDirectory()
	dn := "CN=Quinn Avery," + fin
	attrs := []ad.Attribute{{Name: "sAMAccountName", Values: []string{"quinn.avery"}}, {Name: "userPrincipalName", Values: []string{"quinn.avery@contoso-labs.example"}}}
	b := plan.NewBuilder(d, "New user", "", plan.Options{})
	b.Create(ctx, dn, "user", attrs, true, true)
	p, _ := b.Build()
	res, err := Run(ctx, d, p, Options{Log: logFor(t)})
	if err != nil || !res.OK || res.Steps[0].Password == "" {
		t.Fatalf("create: %+v %v", res, err)
	}
	o, _ := d.Get(ctx, dn)
	if o.First("useraccountcontrol") != "512" || o.First("pwdlastset") != "0" {
		t.Fatalf("new user state: %v", o.Attributes)
	}
	// A password the domain rejects leaves the account disabled.
	dn2 := "CN=Rae Lund," + fin
	b = plan.NewBuilder(d, "New user", "", plan.Options{})
	b.Create(ctx, dn2, "user", nil, true, true)
	p, _ = b.Build()
	res, _ = Run(ctx, d, p, Options{Log: logFor(t), Password: func() (string, error) { return "short", nil }})
	o2, _ := d.Get(ctx, dn2)
	if res.OK || !strings.Contains(res.Steps[0].Error, "created disabled") || o2.First("useraccountcontrol") != "514" {
		t.Fatalf("policy failure: %+v %v", res.Steps, o2.Attributes)
	}
}

func TestDeleteAndRestore(t *testing.T) {
	d := demo.NewDirectory()
	nia := "CN=Nia Johnson,OU=Leavers,OU=Contoso Labs," + demo.Base
	b := plan.NewBuilder(d, "Delete", "", plan.Options{})
	b.Delete(ctx, nia)
	p, _ := b.Build()
	if res, err := Run(ctx, d, p, Options{Log: logFor(t)}); err != nil || !res.OK {
		t.Fatalf("delete: %v %+v", err, res)
	}
	del, _ := d.Deleted(ctx, ad.SearchRequest{Query: "Nia"})
	if len(del.Objects) != 1 {
		t.Fatalf("deleted search: %+v", del)
	}
	b = plan.NewBuilder(d, "Restore", "", plan.Options{})
	b.Undelete(ctx, del.Objects[0].DN, nia)
	p, _ = b.Build()
	if res, err := Run(ctx, d, p, Options{Log: logFor(t)}); err != nil || !res.OK {
		t.Fatalf("restore: %v %+v", err, res)
	}
	if _, err := d.Get(ctx, nia); err != nil {
		t.Fatal(err)
	}
}

func TestReviewedHashAndRecordRequired(t *testing.T) {
	d := demo.NewDirectory()
	b := plan.NewBuilder(d, "x", "", plan.Options{})
	b.Set(ctx, "CN=Bea Okafor,"+fin, "title", []string{"Y"})
	p, _ := b.Build()
	if _, err := Run(ctx, d, p, Options{}); err == nil {
		t.Fatal("ran without a record")
	}
	if _, err := Run(ctx, d, p, Options{Log: logFor(t), ReviewedHash: "different"}); err == nil {
		t.Fatal("ran a changed plan")
	}
	d.FailOn = func(op, dn string) error {
		if op == "modify" {
			return ad.ErrInsufficientRights
		}
		return nil
	}
	res, _ := Run(ctx, d, p, Options{Log: logFor(t)})
	if res.OK || !strings.Contains(res.Steps[0].Error, ad.ErrInsufficientRights.Error()) {
		t.Fatalf("failure not reported: %+v", res.Steps)
	}
}

func TestGenerate(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 50; i++ {
		p, err := Generate()
		if err != nil || len(p) != 19 || seen[p] || strings.ContainsAny(p, "0O1lI") {
			t.Fatalf("bad password %q %v", p, err)
		}
		seen[p] = true
	}
}

func contains(list []string, sub string) bool {
	for _, v := range list {
		if strings.Contains(v, sub) {
			return true
		}
	}
	return false
}
