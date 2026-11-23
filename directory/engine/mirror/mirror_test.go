package mirror

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/zerotrustgarage/tools/directory/engine/ad"
	"github.com/zerotrustgarage/tools/directory/engine/m365"
)

var (
	lastSync = time.Date(2026, 11, 23, 14, 35, 0, 0, time.UTC)
	guid     = "6f9619ff-8b86-d011-b42d-00c04fc964ff"
	guidID   = "/xmWb4aLEdC0LQDAT8lk/w=="
	sid      = "S-1-5-21-1-2-3-1104"
)

func ptr[T any](v T) *T { return &v }

func adUser(changed time.Time) ad.Object {
	return ad.Object{DN: "CN=Bea Okafor,OU=Finance,DC=lab,DC=example", Attributes: map[string][]string{
		"objectclass": {"top", "person", "organizationalPerson", "user"}, "displayname": {"Bea Okafor"}, "givenname": {"Bea"}, "sn": {"Okafor"},
		"userprincipalname": {"bea.okafor@contoso-labs.example"}, "mail": {"bea.okafor@contoso-labs.example"},
		"proxyaddresses":  {"SMTP:bea.okafor@contoso-labs.example", "smtp:bea@contoso-labs.example"},
		"telephonenumber": {"+1 555 0101"}, "title": {"Accountant"}, "department": {"Finance"}, "company": {"Contoso Labs"},
		"co": {"United States"}, "c": {"US"}, "employeeid": {"E1002"}, "msexchusagelocation": {"US"}, "useraccountcontrol": {"512"},
		"extensionattribute3": {"cost-centre-12"}, "manager": {"CN=Avery Lindqvist,OU=Finance,DC=lab,DC=example"},
		"whenchanged": {changed.UTC().Format("20060102150405.0Z")}, "whencreated": {"20250101000000.0Z"},
		"objectguid": {guid}, "objectsid": {sid},
	}}
}

func cloudUser() m365.User {
	return m365.User{ID: "u1", UserPrincipalName: "Bea.Okafor@contoso-labs.example", DisplayName: "Bea Okafor", GivenName: "Bea", Surname: "Okafor",
		Mail: "BEA.OKAFOR@contoso-labs.example", ProxyAddresses: []string{"smtp:Bea@Contoso-Labs.example", "SMTP:bea.okafor@contoso-labs.example", "smtp:bea.okafor@contoso.mail.onmicrosoft.com"},
		BusinessPhones: []string{"+1 555 0101"}, JobTitle: "Accountant", Department: "Finance", CompanyName: "Contoso Labs", Country: "US",
		EmployeeID: "E1002", UsageLocation: "us", AccountEnabled: ptr(true), OnPremisesSyncEnabled: ptr(true), OnPremisesImmutableID: guidID,
		OnPremisesSecurityIdentifier: sid, OnPremisesLastSyncDateTime: ptr(lastSync), CreatedDateTime: ptr(lastSync.AddDate(-1, 0, 0)),
		OnPremisesExtensionAttrs: map[string]string{"extensionAttribute3": "cost-centre-12"}, ManagerUPN: "avery.lindqvist@contoso-labs.example",
		Licenses: []m365.License{{SkuID: "sku", SkuPartNumber: "SPE_E3", ViaGroup: true}}}
}

func tenant(users ...m365.User) *m365.FakeTenant {
	t := m365.NewFakeTenant("tech@contoso-labs.example")
	t.Users = users
	t.Groups["u1"] = []m365.Group{{DisplayName: "Finance Team", Kind: "synced"}, {DisplayName: "All Company", Kind: "microsoft365"}, {DisplayName: "Odd", Kind: "x"}}
	t.Devices["u1"] = []m365.Device{{DisplayName: "WS-FIN-02", TrustType: "ServerAd"}}
	t.Methods["u1"] = []string{"microsoftAuthenticator", "fido2"}
	return t
}

func resolver(ctx context.Context, dn string) (string, error) {
	return "avery.lindqvist@contoso-labs.example", nil
}

func rowOf(t *testing.T, v View, attr string) Row {
	t.Helper()
	for _, r := range v.Rows {
		if r.Attribute == attr {
			return r
		}
	}
	t.Fatalf("no row %s in %+v", attr, v.Rows)
	return Row{}
}

func TestLinkedBySourceAnchorAllMatch(t *testing.T) {
	ctx := context.Background()
	v, err := Mirror(ctx, tenant(cloudUser()), adUser(lastSync.Add(-time.Hour)), WithManagerResolver(resolver))
	if err != nil {
		t.Fatal(err)
	}
	if v.Match != MatchAnchor || v.Anchor.Attribute != "objectGUID" || v.Anchor.ImmutableID != guidID || v.User == nil || v.User.Source != "Active Directory" {
		t.Fatalf("match: %+v", v)
	}
	if v.Differences != 0 || v.Waiting != 0 {
		for _, r := range v.Rows {
			if r.Status != StatusMatch {
				t.Errorf("row %s: %s %q vs %q: %s", r.Attribute, r.Status, r.AD, r.Cloud, r.Note)
			}
		}
		t.Fatal("expected no differences")
	}
	if r := rowOf(t, v, "proxyAddresses"); !strings.Contains(r.Note, "onmicrosoft.com") {
		t.Fatalf("ignored routing address not noted: %+v", r)
	}
	if r := rowOf(t, v, "co"); r.Status != StatusMatch || !strings.Contains(r.Note, "country code") {
		t.Fatalf("country: %+v", r)
	}
	if r := rowOf(t, v, "extensionAttribute3"); r.Property != "onPremisesExtensionAttributes.extensionAttribute3" {
		t.Fatalf("extension row: %+v", r)
	}
	for _, r := range v.Rows {
		if r.Attribute == "extensionAttribute1" {
			t.Fatal("empty extension attribute rows should be left out")
		}
	}
	if len(v.Licenses) != 1 || !v.Licenses[0].ViaGroup || len(v.Devices) != 1 || len(v.Methods) != 2 {
		t.Fatalf("sections: %+v", v)
	}
	if len(v.Groups) != 3 || v.Groups[0].Kind != "synced" || v.Groups[1].Kind != "microsoft365" || v.Groups[2].Kind != "other" {
		t.Fatalf("group sets: %+v", v.Groups)
	}
	if v.User.LastSync != "2026-11-23T14:35:00Z" || v.Account != "tech@contoso-labs.example" {
		t.Fatalf("summary: %+v", v.User)
	}
}

func TestConsistencyGuidFirst(t *testing.T) {
	o := adUser(lastSync.Add(-time.Hour))
	o.Attributes["ms-ds-consistencyguid"] = []string{"00000000-0000-0000-0000-000000000001"}
	u := cloudUser()
	cid, _ := ad.ImmutableID("00000000-0000-0000-0000-000000000001")
	u.OnPremisesImmutableID = cid
	v, err := Mirror(context.Background(), tenant(u), o)
	if err != nil || v.Match != MatchAnchor || v.Anchor.Attribute != "mS-DS-ConsistencyGuid" || v.Anchor.ImmutableID != cid {
		t.Fatalf("consistency GUID: %+v %v", v, err)
	}
	// Falls back to objectGUID when the tenant anchored on it.
	v, err = Mirror(context.Background(), tenant(cloudUser()), o)
	if err != nil || v.Match != MatchAnchor || v.Anchor.Attribute != "objectGUID" {
		t.Fatalf("objectGUID fallback: %+v %v", v, err)
	}
	// A malformed consistency GUID is noted, not fatal.
	o.Attributes["ms-ds-consistencyguid"] = []string{"invalid"}
	v, err = Mirror(context.Background(), tenant(cloudUser()), o)
	if err != nil || v.Match != MatchAnchor || !strings.Contains(v.Anchor.Note, "not a 16-byte GUID") {
		t.Fatalf("invalid consistency GUID: %+v %v", v, err)
	}
	if r := rowOf(t, v, "manager"); r.Status != StatusNotCompared {
		t.Fatalf("manager without resolver: %+v", r)
	}
}

func TestLinkedBySID(t *testing.T) {
	u := cloudUser()
	u.OnPremisesImmutableID = "c29tZXRoaW5nIGVsc2U="
	v, err := Mirror(context.Background(), tenant(u), adUser(lastSync.Add(-time.Hour)))
	if err != nil || v.Match != MatchSID || !strings.Contains(v.Reason, "c29tZXRoaW5nIGVsc2U=") || v.User == nil {
		t.Fatalf("SID: %+v %v", v, err)
	}
}

func TestPossibleMatchAndNone(t *testing.T) {
	cloudOnly := m365.User{ID: "c1", UserPrincipalName: "bea.okafor@contoso-labs.example", Mail: "bea.okafor@contoso-labs.example"}
	other := m365.User{ID: "c2", UserPrincipalName: "b.okafor@contoso-labs.example", Mail: "BEA.OKAFOR@contoso-labs.example", OnPremisesImmutableID: "b3RoZXI="}
	v, err := Mirror(context.Background(), tenant(cloudOnly, other), adUser(lastSync))
	if err != nil || v.Match != MatchPossible || len(v.Candidates) != 2 || v.User != nil || len(v.Rows) != 0 {
		t.Fatalf("possible: %+v %v", v, err)
	}
	if r := strings.Join(v.Candidates[0].Reasons, " "); !strings.Contains(r, "userPrincipalName and mail") || !strings.Contains(r, "cloud-only") {
		t.Fatalf("cloud-only reasons: %s", r)
	}
	if r := strings.Join(v.Candidates[1].Reasons, " "); !strings.Contains(r, "Its mail equals the AD userPrincipalName") || !strings.Contains(r, "another AD object") {
		t.Fatalf("other reasons: %s", r)
	}

	ft := tenant()
	ft.Org = m365.Organization{OnPremisesSyncEnabled: true, OnPremisesLastSyncDateTime: ptr(lastSync)}
	v, err = Mirror(context.Background(), ft, adUser(lastSync))
	if err != nil || v.Match != MatchNone || !strings.HasPrefix(v.Hint, "Out of sync scope?") {
		t.Fatalf("none: %+v %v", v, err)
	}
	admin := adUser(lastSync)
	admin.Attributes["admincount"] = []string{"1"}
	if v, _ = Mirror(context.Background(), ft, admin); !strings.Contains(v.Hint, "protected account") {
		t.Fatalf("admin hint: %q", v.Hint)
	}
	fresh := adUser(lastSync)
	fresh.Attributes["whencreated"] = []string{lastSync.Add(time.Minute).Format("20060102150405.0Z")}
	if v, _ = Mirror(context.Background(), ft, fresh); !strings.Contains(v.Hint, "after the tenant's last sync") {
		t.Fatalf("new object hint: %q", v.Hint)
	}
}

func TestStatuses(t *testing.T) {
	u := cloudUser()
	u.JobTitle = "Junior Accountant"
	u.AccountEnabled = ptr(false)
	u.ProxyAddresses = []string{"SMTP:bea.okafor@contoso-labs.example"}
	u.ProvisioningErrors = []m365.ProvisioningError{{Category: "PropertyConflict", PropertyName: "ProxyAddresses", Value: "smtp:bea@contoso-labs.example"}}
	// AD changed before the last sync: out of step.
	v, err := Mirror(context.Background(), tenant(u), adUser(lastSync.Add(-time.Hour)), WithManagerResolver(resolver))
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range []string{"title", "userAccountControl", "proxyAddresses"} {
		if r := rowOf(t, v, a); r.Status != StatusOutOfStep {
			t.Fatalf("%s: %+v", a, r)
		}
	}
	if r := rowOf(t, v, "proxyAddresses"); !strings.Contains(r.Note, "Only in AD: smtp:bea@contoso-labs.example") || !strings.Contains(r.Note, "PropertyConflict") {
		t.Fatalf("proxy note: %q", r.Note)
	}
	if v.Differences != 3 || v.Waiting != 0 {
		t.Fatalf("counts: %d %d", v.Differences, v.Waiting)
	}
	// AD changed after the last sync (same second counts): waiting for sync.
	for _, ch := range []time.Time{lastSync.Add(10 * time.Minute), lastSync.Add(300 * time.Millisecond)} {
		v, _ = Mirror(context.Background(), tenant(u), adUser(ch), WithManagerResolver(resolver))
		if r := rowOf(t, v, "title"); r.Status != StatusWaiting || !strings.Contains(r.Note, "after the last sync") {
			t.Fatalf("waiting at %v: %+v", ch, r)
		}
		if v.Waiting != 3 {
			t.Fatalf("waiting count %d", v.Waiting)
		}
	}
	// No last sync time: out of step.
	u.OnPremisesLastSyncDateTime = nil
	v, _ = Mirror(context.Background(), tenant(u), adUser(lastSync))
	if r := rowOf(t, v, "title"); r.Status != StatusOutOfStep || !strings.Contains(r.Note, "no last sync time") {
		t.Fatalf("no sync time: %+v", r)
	}
	// Manager mismatch and the primary address changing case of SMTP.
	u = cloudUser()
	u.ManagerUPN = "someone@contoso-labs.example"
	u.ProxyAddresses = []string{"smtp:bea.okafor@contoso-labs.example", "SMTP:bea@contoso-labs.example"}
	v, _ = Mirror(context.Background(), tenant(u), adUser(lastSync.Add(-time.Hour)), WithManagerResolver(resolver))
	if r := rowOf(t, v, "manager"); r.Status != StatusOutOfStep || r.AD[0] != "avery.lindqvist@contoso-labs.example" {
		t.Fatalf("manager: %+v", r)
	}
	if r := rowOf(t, v, "proxyAddresses"); r.Status != StatusOutOfStep {
		t.Fatalf("primary address swap should differ: %+v", r)
	}
}

func TestNormalization(t *testing.T) {
	for _, c := range []struct {
		kind  compareKind
		a, c  []string
		equal bool
	}{
		{fold, []string{"A@B.example"}, []string{"a@b.example"}, true},
		{exact, []string{"Accountant"}, []string{"accountant"}, false},
		{exact, []string{" Accountant "}, []string{"Accountant"}, true},
		{exact, nil, []string{""}, true},
		{proxies, []string{"SMTP:a@b.example", "smtp:c@b.example"}, []string{"smtp:C@B.EXAMPLE", "SMTP:A@b.example"}, true},
		{proxies, []string{"SMTP:a@b.example"}, []string{"smtp:a@b.example"}, false},
		{proxies, []string{"X500:/o=Org/cn=A"}, []string{"x500:/O=org/CN=a"}, true},
		{proxies, []string{"SMTP:a@b.example"}, []string{"SMTP:a@b.example", "smtp:a@t.mail.onmicrosoft.com"}, true},
		{proxies, []string{"SMTP:a@b.example"}, []string{"SMTP:a@b.example", "smtp:x@b.example"}, false},
		{cloudOK, nil, []string{"US"}, true},
		{cloudOK, []string{"US"}, []string{"GB"}, false},
	} {
		if got, _ := same(c.kind, c.a, c.c); got != c.equal {
			t.Errorf("same(%v, %q, %q) = %v", c.kind, c.a, c.c, got)
		}
	}
}

func TestForbiddenSections(t *testing.T) {
	ft := tenant(cloudUser())
	ft.Forbid = map[string]bool{"UserGroups": true, "UserDevices": true, "AuthMethods": true}
	v, err := Mirror(context.Background(), ft, adUser(lastSync))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(v.GroupsNote, "GroupMember.Read.All") || !strings.Contains(v.DevicesNote, "Device.Read.All") ||
		!strings.Contains(v.MethodsNote, "UserAuthenticationMethod.Read.All") || v.Groups != nil || len(v.Rows) == 0 {
		t.Fatalf("notes: %+v", v)
	}
	ft.Forbid = map[string]bool{"UserByImmutableID": true}
	if _, err := Mirror(context.Background(), ft, adUser(lastSync)); !errors.Is(err, m365.ErrForbidden) {
		t.Fatalf("lookup refusal should fail the view: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Mirror(ctx, tenant(cloudUser()), adUser(lastSync)); err == nil {
		t.Fatal("cancelled context accepted")
	}
}

func TestUnsupportedKinds(t *testing.T) {
	g := ad.Object{DN: "CN=G,DC=lab,DC=example", Attributes: map[string][]string{"objectclass": {"top", "group"}}}
	v, err := Mirror(context.Background(), tenant(), g)
	if err != nil || v.Match != MatchUnsupported || v.Kind != "group" {
		t.Fatalf("group: %+v %v", v, err)
	}
}

func TestFind(t *testing.T) {
	ctx := context.Background()
	if u, err := Find(ctx, tenant(cloudUser()), adUser(lastSync)); err != nil || u == nil || u.ID != "u1" {
		t.Fatalf("find by anchor: %+v %v", u, err)
	}
	cloudOnly := m365.User{ID: "c1", UserPrincipalName: "bea.okafor@contoso-labs.example"}
	if u, err := Find(ctx, tenant(cloudOnly), adUser(lastSync)); err != nil || u != nil {
		t.Fatalf("a possible match is not linked: %+v %v", u, err)
	}
}
