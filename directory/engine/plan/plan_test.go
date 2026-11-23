package plan_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/zerotrustgarage/tools/directory/engine/ad"
	"github.com/zerotrustgarage/tools/directory/engine/demo"
	"github.com/zerotrustgarage/tools/directory/engine/plan"
)

var ctx = context.Background()

const fin = "OU=Finance,OU=Staff,OU=Contoso Labs," + demo.Base

func build(t *testing.T, f func(b *plan.Builder)) plan.Plan {
	t.Helper()
	b := plan.NewBuilder(demo.NewDirectory(), "t", "T-1", plan.Options{})
	f(b)
	p, err := b.Build()
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func warned(p plan.Plan, sub string, blocking bool) bool {
	for _, w := range p.Warnings {
		if strings.Contains(w.Message, sub) && w.Blocking == blocking {
			return true
		}
	}
	return false
}

func TestTier0Detection(t *testing.T) {
	cases := map[string]string{
		"CN=krbtgt,CN=Users," + demo.Base:                            "krbtgt",
		"CN=DC01,OU=Domain Controllers," + demo.Base:                 "domain controller",
		"CN=Admin Riley Chen,OU=Admins,OU=Contoso Labs," + demo.Base: "adminCount",
		"CN=Domain Admins,CN=Users," + demo.Base:                     "privileged group",
	}
	for dn, reason := range cases {
		p := build(t, func(b *plan.Builder) { b.Set(ctx, dn, "description", []string{"x"}) })
		if !p.Tier0 || !warned(p, reason, false) {
			t.Errorf("%s: want Tier 0 (%s), got %+v", dn, reason, p.Warnings)
		}
	}
	p := build(t, func(b *plan.Builder) { b.Set(ctx, "CN=Bea Okafor,"+fin, "title", []string{"x"}) })
	if p.Tier0 {
		t.Fatal("ordinary user marked Tier 0")
	}
}

func TestBlockedAttributesAndNoOps(t *testing.T) {
	bea := "CN=Bea Okafor," + fin
	for _, a := range []string{"unicodePwd", "memberOf", "objectSid", "msLAPS-Password", "userAccountControl"} {
		p := build(t, func(b *plan.Builder) { b.Set(ctx, bea, a, []string{"x"}) })
		if !p.Blocked() || len(p.Changes) != 0 {
			t.Errorf("%s should be refused: %+v", a, p)
		}
	}
	p := build(t, func(b *plan.Builder) {
		b.Set(ctx, bea, "department", []string{"Finance"}) // already set
		b.SetEnabled(ctx, bea, true)                       // already enabled
		b.Unlock(ctx, bea)                                 // not locked
	})
	if len(p.Changes) != 0 || !warned(p, "not locked", false) {
		t.Fatalf("no-ops produced changes: %+v", p)
	}
}

func TestRenameMoveExpiresAndHash(t *testing.T) {
	bea := "CN=Bea Okafor," + fin
	p := build(t, func(b *plan.Builder) {
		b.Rename(ctx, bea, "Bea O'Kafor, CPA")
		b.SetAccountExpires(ctx, bea, time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC))
		b.Move(ctx, bea, "OU=Sales,OU=Staff,OU=Contoso Labs,"+demo.Base)
	})
	if len(p.Changes) != 3 || p.Changes[0].Target != `CN=Bea O'Kafor\, CPA` {
		t.Fatalf("changes: %+v", p.Changes)
	}
	if !warned(p, "sync scope", false) {
		t.Fatal("move should warn about the sync scope")
	}
	q := p
	q.Ticket = "T-2"
	if p.Hash() == q.Hash() {
		t.Fatal("hash must cover the ticket")
	}
}

func TestRefusedWhereRightsAreMissing(t *testing.T) {
	d := demo.NewDirectory()
	d.RightsFunc = func(dn string) ad.Rights {
		return ad.Rights{Attributes: []string{"title"}, ChildClasses: []string{"user"}}
	}
	b := plan.NewBuilder(d, "t", "", plan.Options{})
	bea := "CN=Bea Okafor," + fin
	b.Set(ctx, bea, "title", []string{"ok"})
	b.Set(ctx, bea, "department", []string{"denied"})
	b.AddMember(ctx, "CN=Project Atlas,OU=Groups,OU=Contoso Labs,"+demo.Base, bea)
	b.Create(ctx, "CN=New Group,"+fin, "group", nil, false, false)
	p, _ := b.Build()
	if len(p.Changes) != 1 || !p.Blocked() {
		t.Fatalf("only the title change should pass: %+v", p)
	}
	for _, sub := range []string{"cannot change department", "cannot change member", "cannot create group"} {
		if !warned(p, sub, true) {
			t.Errorf("missing refusal %q in %+v", sub, p.Warnings)
		}
	}
}

func TestMissingObjectIsBlocking(t *testing.T) {
	p := build(t, func(b *plan.Builder) { b.Set(ctx, "CN=Nobody,"+fin, "title", []string{"x"}) })
	if !p.Blocked() || !warned(p, "not found", true) {
		t.Fatalf("missing object: %+v", p.Warnings)
	}
}
