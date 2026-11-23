package demo

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/zerotrustgarage/tools/directory/engine/ad"
	"github.com/zerotrustgarage/tools/directory/engine/mirror"
)

func find(t *testing.T, dir *ad.FakeClient, name string) ad.Object {
	t.Helper()
	res, err := dir.Search(context.Background(), ad.SearchRequest{Query: name, Class: ad.ClassUser})
	if err != nil || len(res.Objects) != 1 {
		t.Fatalf("find %s: %d %v", name, len(res.Objects), err)
	}
	return res.Objects[0]
}

func view(t *testing.T, dir *ad.FakeClient, name string) mirror.View {
	t.Helper()
	ten := NewTenant(dir)
	v, err := mirror.Mirror(context.Background(), ten, find(t, dir, name), mirror.WithManagerResolver(func(ctx context.Context, dn string) (string, error) {
		o, err := dir.Get(ctx, dn)
		return o.First("userprincipalname"), err
	}))
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return v
}

func status(v mirror.View, attr string) string {
	for _, r := range v.Rows {
		if r.Attribute == attr {
			return r.Status
		}
	}
	return ""
}

func TestDemoTenantScenarios(t *testing.T) {
	dir := NewDirectory()
	v := view(t, dir, "Bea Okafor")
	if v.Match != mirror.MatchAnchor || v.Differences != 0 || len(v.Licenses) != 1 || !v.Licenses[0].ViaGroup || v.Licenses[0].SkuPartNumber != "SPE_E3" ||
		len(v.Devices) != 1 || len(v.Groups) == 0 {
		t.Fatalf("Bea: %+v", v)
	}
	if v := view(t, dir, OutOfStepUser); status(v, "title") != mirror.StatusOutOfStep || v.Differences != 1 {
		t.Fatalf("out of step: %+v", v.Rows)
	}
	if v := view(t, dir, WaitingUser); status(v, "title") != mirror.StatusWaiting || v.Waiting != 1 {
		t.Fatalf("waiting: %+v", v.Rows)
	}
	v = view(t, dir, ConflictUser)
	if status(v, "proxyAddresses") != mirror.StatusOutOfStep || len(v.User.ProvisioningErrors) != 1 {
		t.Fatalf("conflict: %+v", v)
	}
	v = view(t, dir, PossibleMatchUser)
	if v.Match != mirror.MatchPossible || len(v.Candidates) != 1 || !strings.Contains(strings.Join(v.Candidates[0].Reasons, " "), "cloud-only") {
		t.Fatalf("possible match: %+v", v)
	}
	for _, leaver := range []string{"Marco Rossi", "Nia Johnson"} {
		v = view(t, dir, leaver)
		if v.User == nil || v.User.SignInEnabled == nil || *v.User.SignInEnabled || v.Differences != 0 || len(v.Licenses) != 0 {
			t.Fatalf("leaver %s: %+v", leaver, v)
		}
	}
	v = view(t, dir, "Admin Riley Chen")
	if v.Match != mirror.MatchNone || !strings.Contains(v.Hint, "protected account") {
		t.Fatalf("Tier 0 admin: %+v", v)
	}
	if v := view(t, dir, "Gus Whitfield"); len(v.Methods) != 0 || v.MethodsNote != "" {
		t.Fatalf("Gus methods: %+v", v.Methods)
	}
}

func TestDemoSyncerClearsDifferences(t *testing.T) {
	dir := NewDirectory()
	clock := Now
	dir.Now = func() time.Time { return clock }
	ten := NewTenant(dir)
	syncer := NewSyncer(dir, ten)
	h, err := syncer.Health(context.Background())
	if err != nil || h.Kind != "connect" || h.VersionStatus == "" || h.Server == "" {
		t.Fatalf("health: %+v %v", h, err)
	}
	ctx := context.Background()
	for _, name := range []string{OutOfStepUser, WaitingUser, ConflictUser} {
		o := find(t, dir, name)
		if _, err := syncer.SyncNow(ctx, o); err != nil {
			t.Fatal(err)
		}
		v, err := mirror.Mirror(ctx, ten, o)
		if err != nil {
			t.Fatal(err)
		}
		want := 0
		if name == ConflictUser {
			want = 1 // the conflicting address stays out until the conflict is resolved
		}
		if v.Differences != want || v.User.LastSync != Now.Format(time.RFC3339) {
			t.Fatalf("%s after sync: %d differences, last sync %s, %+v", name, v.Differences, v.User.LastSync, v.Rows)
		}
	}
	// A cycle for an object outside the scope does nothing and does not fail.
	if _, err := syncer.SyncNow(ctx, find(t, dir, "Admin Riley Chen")); err != nil {
		t.Fatal(err)
	}
}
