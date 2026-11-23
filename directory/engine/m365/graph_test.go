package m365

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeTokens is a TokenSource that counts renewals.
type fakeTokens struct {
	mu      sync.Mutex
	tok     string
	renewed int
}

func (f *fakeTokens) Account() string { return "tech@contoso.example" }
func (f *fakeTokens) Token(context.Context) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.tok, nil
}
func (f *fakeTokens) Renew(_ context.Context, rejected string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if rejected == f.tok {
		f.renewed++
		f.tok = "token-" + itoa(f.renewed+1)
	}
	return f.tok, nil
}

// fakeGraph serves Graph paths from a map of handlers and records every
// request; each handler sees only requests that carry the current token.
type fakeGraph struct {
	t        *testing.T
	srv      *httptest.Server
	tokens   *fakeTokens
	lb       *loopbackOnly
	g        *Graph
	mu       sync.Mutex
	hits     map[string]int
	handlers map[string]http.HandlerFunc
	slept    []time.Duration
}

func newFakeGraph(t *testing.T) *fakeGraph {
	f := &fakeGraph{t: t, tokens: &fakeTokens{tok: "token-1"}, lb: &loopbackOnly{}, hits: map[string]int{}, handlers: map[string]http.HandlerFunc{}}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	g, err := NewGraph(f.tokens, f.srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	g.HTTPClient = f.lb.client()
	g.sleep = func(_ context.Context, d time.Duration) error {
		f.mu.Lock()
		f.slept = append(f.slept, d)
		f.mu.Unlock()
		return nil
	}
	f.g = g
	t.Cleanup(func() { f.lb.check(t) })
	return f
}

func (f *fakeGraph) handle(path string, h http.HandlerFunc) { f.handlers[path] = h }

func (f *fakeGraph) count(path string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.hits[path]
}

func (f *fakeGraph) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.hits[r.URL.Path]++
	f.mu.Unlock()
	if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer token-") {
		w.WriteHeader(401)
		return
	}
	h, ok := f.handlers[r.URL.Path]
	if !ok {
		writeErr(w, 404, "Request_ResourceNotFound", "no such path in the fake")
		return
	}
	h(w, r)
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, code, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"code": code, "message": msg}})
}

func page(values ...any) map[string]any { return map[string]any{"value": values} }

func TestFilterEscaping(t *testing.T) {
	f := newFakeGraph(t)
	evil := "o'brien' or accountEnabled eq true or 'x@contoso.example&$top=1"
	var got string
	f.handle("/v1.0/users", func(w http.ResponseWriter, r *http.Request) {
		got = r.URL.Query().Get("$filter")
		if r.URL.Query().Get("$top") != "" || strings.Contains(r.URL.RawQuery, "+") {
			t.Errorf("query was altered: %s", r.URL.RawQuery)
		}
		writeJSON(w, page())
	})
	if _, err := f.g.UsersByName(context.Background(), evil); err != nil {
		t.Fatal(err)
	}
	lit := "'o''brien'' or accountEnabled eq true or ''x@contoso.example&$top=1'"
	if want := "userPrincipalName eq " + lit + " or mail eq " + lit; got != want {
		t.Fatalf("filter\n got %s\nwant %s", got, want)
	}
	f.handle("/v1.0/users", func(w http.ResponseWriter, r *http.Request) {
		got = r.URL.Query().Get("$filter")
		writeJSON(w, page())
	})
	if u, err := f.g.UserByImmutableID(context.Background(), "ab'c=="); err != nil || u != nil {
		t.Fatalf("%v %v", u, err)
	}
	if got != "onPremisesImmutableId eq 'ab''c=='" {
		t.Fatalf("filter %s", got)
	}
}

func TestPagingAndCap(t *testing.T) {
	f := newFakeGraph(t)
	f.handle("/v1.0/users/u1/authentication/methods", func(w http.ResponseWriter, r *http.Request) {
		n := r.URL.Query().Get("page")
		next := ""
		switch n {
		case "":
			next = f.srv.URL + "/v1.0/users/u1/authentication/methods?page=2"
		case "2":
			next = f.srv.URL + "/v1.0/users/u1/authentication/methods?page=3"
		}
		writeJSON(w, map[string]any{
			"value":           []any{map[string]string{"@odata.type": "#microsoft.graph.phoneAuthenticationMethod"}},
			"@odata.nextLink": next,
		})
	})
	kinds, err := f.g.AuthMethods(context.Background(), "u1")
	if err != nil || len(kinds) != 3 {
		t.Fatalf("%v %v", kinds, err)
	}
	f.g.MaxPages = 2
	if _, err := f.g.AuthMethods(context.Background(), "u1"); err == nil || !strings.Contains(err.Error(), "more than 2 pages") {
		t.Fatalf("cap: %v", err)
	}
	// A nextLink to another host is never followed (the token would go with it).
	f.g.MaxPages = 0
	f.handle("/v1.0/users/u2/authentication/methods", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{"value": []any{}, "@odata.nextLink": "https://evil.example/v1.0/users"})
	})
	if _, err := f.g.AuthMethods(context.Background(), "u2"); err == nil || !strings.Contains(err.Error(), "outside the Microsoft Graph root") {
		t.Fatalf("foreign nextLink: %v", err)
	}
}

func TestThrottlingRetryAfter(t *testing.T) {
	f := newFakeGraph(t)
	calls := 0
	f.handle("/v1.0/organization", func(w http.ResponseWriter, r *http.Request) {
		calls++
		switch calls {
		case 1:
			w.Header().Set("Retry-After", "7")
			writeErr(w, 429, "TooManyRequests", "slow down")
		case 2:
			w.Header().Set("Retry-After", "3600")
			writeErr(w, 503, "serviceNotAvailable", "busy")
		default:
			writeJSON(w, page(map[string]any{
				"id": "org1", "displayName": "Contoso", "onPremisesSyncEnabled": true,
				"onPremisesLastSyncDateTime": "2026-11-23T10:00:00Z",
				"verifiedDomains":            []any{map[string]any{"name": "contoso.example"}, map[string]any{"name": "contoso.onmicrosoft.com"}},
			}))
		}
	})
	org, err := f.g.Organization(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !org.OnPremisesSyncEnabled || org.OnPremisesLastSyncDateTime == nil || len(org.VerifiedDomains) != 2 || org.DisplayName != "Contoso" {
		t.Fatalf("org = %+v", org)
	}
	if len(f.slept) != 2 || f.slept[0] != 7*time.Second || f.slept[1] != 30*time.Second {
		t.Fatalf("waits = %v (want 7s, then capped 30s)", f.slept)
	}
	// Retries are bounded.
	f.handle("/v1.0/organization", func(w http.ResponseWriter, r *http.Request) { writeErr(w, 429, "TooManyRequests", "no") })
	if _, err := f.g.Organization(context.Background()); err == nil || !strings.Contains(err.Error(), "429") {
		t.Fatalf("err = %v", err)
	}
}

func TestForbiddenAndUnauthorized(t *testing.T) {
	f := newFakeGraph(t)
	f.handle("/v1.0/users/u1/registeredDevices/microsoft.graph.device", func(w http.ResponseWriter, r *http.Request) {
		writeErr(w, 403, "Authorization_RequestDenied", "Insufficient privileges to complete the operation.")
	})
	_, err := f.g.UserDevices(context.Background(), "u1")
	if !errors.Is(err, ErrForbidden) || !strings.Contains(err.Error(), "Authorization_RequestDenied") || strings.Contains(err.Error(), "token-") {
		t.Fatalf("err = %v", err)
	}
	// 401 renews the token once and retries.
	f.handle("/v1.0/users/u1/authentication/methods", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer token-2" {
			writeErr(w, 401, "InvalidAuthenticationToken", "expired")
			return
		}
		writeJSON(w, page())
	})
	if _, err := f.g.AuthMethods(context.Background(), "u1"); err != nil {
		t.Fatal(err)
	}
	if f.tokens.renewed != 1 {
		t.Fatalf("renewed %d", f.tokens.renewed)
	}
	// A second 401 after renewal is not retried forever.
	f.handle("/v1.0/users/u1/authentication/methods", func(w http.ResponseWriter, r *http.Request) {
		writeErr(w, 401, "InvalidAuthenticationToken", "nope")
	})
	if _, err := f.g.AuthMethods(context.Background(), "u1"); !errors.Is(err, ErrNotSignedIn) {
		t.Fatalf("err = %v", err)
	}
}

func userJSON(extra map[string]any) map[string]any {
	u := map[string]any{
		"id": "u1", "userPrincipalName": "ann@contoso.example", "displayName": "Ann",
		"accountEnabled": true, "onPremisesSyncEnabled": true, "onPremisesImmutableId": "AAA=",
		"onPremisesSecurityIdentifier":  "S-1-5-21-1-2-3-1104",
		"onPremisesExtensionAttributes": map[string]any{"extensionAttribute1": "cost-100", "extensionAttribute2": nil},
		"onPremisesProvisioningErrors":  []any{map[string]any{"category": "PropertyConflict", "propertyCausingError": "ProxyAddresses", "value": "smtp:ann@contoso.example", "occurredDateTime": "2026-11-01T00:00:00Z"}},
		"assignedLicenses": []any{
			map[string]any{"skuId": "05E9A617-0261-4CEE-BB44-138D3EF5D965", "disabledPlans": []string{"plan-a"}},
			map[string]any{"skuId": "4b585984-651b-448a-9e53-3b10f069cf7f", "disabledPlans": []string{}},
		},
		"licenseAssignmentStates": []any{
			map[string]any{"skuId": "05e9a617-0261-4cee-bb44-138d3ef5d965", "assignedByGroup": "g-lic", "state": "Active"},
			map[string]any{"skuId": "4b585984-651b-448a-9e53-3b10f069cf7f", "assignedByGroup": nil, "state": "Active"},
		},
	}
	for k, v := range extra {
		u[k] = v
	}
	return u
}

func skuHandler(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, page(
		map[string]any{"skuId": "05e9a617-0261-4cee-bb44-138d3ef5d965", "skuPartNumber": "SPE_E3"},
		map[string]any{"skuId": "4b585984-651b-448a-9e53-3b10f069cf7f", "skuPartNumber": "DESKLESSPACK"},
	))
}

func TestUserLicensesManagerAndSignInFallback(t *testing.T) {
	f := newFakeGraph(t)
	withSignIn := 0
	f.handle("/v1.0/users", func(w http.ResponseWriter, r *http.Request) {
		sel := r.URL.Query().Get("$select")
		if r.Header.Get("ConsistencyLevel") != "" {
			t.Error("a default-supported filter was sent as an advanced query")
		}
		if strings.Contains(sel, "signInActivity") {
			withSignIn++
			writeErr(w, 403, "Authentication_RequestFromNonPremiumTenantOrB2CTenant", "Neither tenant is B2C or tenant doesn't have premium license")
			return
		}
		for _, want := range []string{"onPremisesImmutableId", "assignedLicenses", "licenseAssignmentStates", "onPremisesExtensionAttributes"} {
			if !strings.Contains(sel, want) {
				t.Errorf("$select lacks %s", want)
			}
		}
		writeJSON(w, page(userJSON(nil)))
	})
	f.handle("/v1.0/subscribedSkus", skuHandler)
	f.handle("/v1.0/users/u1/manager", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{"userPrincipalName": "boss@contoso.example"})
	})

	u, err := f.g.UserByImmutableID(context.Background(), "AAA=")
	if err != nil || u == nil {
		t.Fatalf("%v %v", u, err)
	}
	if u.LastSuccessfulSignIn != nil || u.ManagerUPN != "boss@contoso.example" || u.OnPremisesSecurityIdentifier != "S-1-5-21-1-2-3-1104" {
		t.Fatalf("user = %+v", u)
	}
	if len(u.OnPremisesExtensionAttrs) != 1 || u.OnPremisesExtensionAttrs["extensionAttribute1"] != "cost-100" {
		t.Fatalf("extension attributes = %v", u.OnPremisesExtensionAttrs)
	}
	if len(u.ProvisioningErrors) != 1 || u.ProvisioningErrors[0].PropertyName != "ProxyAddresses" {
		t.Fatalf("errors = %+v", u.ProvisioningErrors)
	}
	if len(u.Licenses) != 2 ||
		u.Licenses[0].SkuPartNumber != "SPE_E3" || !u.Licenses[0].ViaGroup || len(u.Licenses[0].DisabledPlans) != 1 ||
		u.Licenses[1].SkuPartNumber != "DESKLESSPACK" || u.Licenses[1].ViaGroup {
		t.Fatalf("licenses = %+v", u.Licenses)
	}
	// Second lookup: signInActivity is not asked again, SKUs are cached.
	if _, err := f.g.UserBySID(context.Background(), "S-1-5-21-1-2-3-1104"); err != nil {
		t.Fatal(err)
	}
	if withSignIn != 1 || f.count("/v1.0/subscribedSkus") != 1 {
		t.Fatalf("signInActivity asked %d times, subscribedSkus read %d times", withSignIn, f.count("/v1.0/subscribedSkus"))
	}
}

func TestUserSignInAndNoManager(t *testing.T) {
	f := newFakeGraph(t)
	f.handle("/v1.0/users", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, page(userJSON(map[string]any{
			"assignedLicenses": []any{}, "licenseAssignmentStates": []any{},
			"signInActivity": map[string]any{"lastSuccessfulSignInDateTime": "2026-11-20T08:00:00Z"},
		})))
	})
	f.handle("/v1.0/users/u1/manager", func(w http.ResponseWriter, r *http.Request) {
		writeErr(w, 404, "Request_ResourceNotFound", "Resource 'manager' does not exist")
	})
	us, err := f.g.UsersByName(context.Background(), "ann@contoso.example")
	if err != nil || len(us) != 1 {
		t.Fatalf("%v %v", us, err)
	}
	if us[0].LastSuccessfulSignIn == nil || !us[0].LastSuccessfulSignIn.Equal(time.Date(2026, 11, 20, 8, 0, 0, 0, time.UTC)) || us[0].ManagerUPN != "" {
		t.Fatalf("user = %+v", us[0])
	}
	if f.count("/v1.0/subscribedSkus") != 0 {
		t.Fatal("SKUs read for a user without licenses")
	}
}

func TestGroupKinds(t *testing.T) {
	f := newFakeGraph(t)
	f.handle("/v1.0/users/u1/memberOf/microsoft.graph.group", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("ConsistencyLevel") != "eventual" || r.URL.Query().Get("$count") != "true" {
			t.Error("OData cast on memberOf needs ConsistencyLevel: eventual and $count=true")
		}
		writeJSON(w, page(
			map[string]any{"id": "g1", "displayName": "Synced", "securityEnabled": true, "onPremisesSyncEnabled": true, "groupTypes": []string{}},
			map[string]any{"id": "g2", "displayName": "Team", "mailEnabled": true, "mail": "team@contoso.example", "groupTypes": []string{"Unified"}, "onPremisesSyncEnabled": nil},
			map[string]any{"id": "g3", "displayName": "Admins", "securityEnabled": true, "isAssignableToRole": true, "groupTypes": []string{}},
			map[string]any{"id": "g4", "displayName": "Cloud", "securityEnabled": true, "groupTypes": []string{"DynamicMembership"}},
		))
	})
	gs, err := f.g.UserGroups(context.Background(), "u1")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"synced", "microsoft365", "role", "cloud"}
	if len(gs) != 4 {
		t.Fatalf("groups = %+v", gs)
	}
	for i, g := range gs {
		if g.Kind != want[i] {
			t.Errorf("%s: kind %s, want %s", g.DisplayName, g.Kind, want[i])
		}
	}
	if gs[1].Mail != "team@contoso.example" || !gs[0].OnPremisesSyncEnabled || gs[1].OnPremisesSyncEnabled {
		t.Fatalf("groups = %+v", gs)
	}
}

func TestDevicesDeduplicated(t *testing.T) {
	f := newFakeGraph(t)
	f.handle("/v1.0/users/u1/registeredDevices/microsoft.graph.device", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, page(
			map[string]any{"id": "d1", "displayName": "LAPTOP-1", "trustType": "ServerAd", "isManaged": true, "isCompliant": true, "accountEnabled": true},
			map[string]any{"id": "d2", "displayName": "PHONE", "trustType": "Workplace", "isCompliant": nil},
		))
	})
	f.handle("/v1.0/users/u1/ownedDevices/microsoft.graph.device", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, page(
			map[string]any{"id": "D1", "displayName": "LAPTOP-1"},
			map[string]any{"id": "d3", "displayName": "DESKTOP", "approximateLastSignInDateTime": "2026-11-22T12:00:00Z"},
		))
	})
	ds, err := f.g.UserDevices(context.Background(), "u1")
	if err != nil {
		t.Fatal(err)
	}
	if len(ds) != 3 || ds[0].ID != "d1" || ds[1].ID != "d2" || ds[2].ID != "d3" {
		t.Fatalf("devices = %+v", ds)
	}
	if !ds[0].IsManaged || ds[0].IsCompliant == nil || !*ds[0].IsCompliant || ds[1].IsCompliant != nil || ds[2].LastSignIn == nil {
		t.Fatalf("devices = %+v", ds)
	}
}

func TestAuthMethodKinds(t *testing.T) {
	f := newFakeGraph(t)
	f.handle("/v1.0/users/u1/authentication/methods", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, page(
			map[string]any{"@odata.type": "#microsoft.graph.passwordAuthenticationMethod"},
			map[string]any{"@odata.type": "#microsoft.graph.microsoftAuthenticatorAuthenticationMethod"},
			map[string]any{"@odata.type": "#microsoft.graph.phoneAuthenticationMethod"},
			map[string]any{"@odata.type": "#microsoft.graph.phoneAuthenticationMethod"},
			map[string]any{"@odata.type": "#microsoft.graph.fido2AuthenticationMethod"},
			map[string]any{"@odata.type": "#microsoft.graph.futureThingAuthenticationMethod"},
		))
	})
	got, err := f.g.AuthMethods(context.Background(), "u1")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"Authenticator app", "Phone", "Phone", "FIDO2 security key or passkey", "futureThing"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("kinds = %q", got)
	}
}

func TestDo(t *testing.T) {
	f := newFakeGraph(t)
	f.handle("/v1.0/servicePrincipals/sp1/synchronization/jobs/job1/provisionOnDemand", func(w http.ResponseWriter, r *http.Request) {
		var in map[string]any
		if r.Method != "POST" || r.Header.Get("Content-Type") != "application/json" || json.NewDecoder(r.Body).Decode(&in) != nil || in["parameters"] == nil {
			t.Errorf("bad request %s", r.Method)
		}
		writeJSON(w, map[string]any{"id": "result-1"})
	})
	var out struct {
		ID string `json:"id"`
	}
	err := f.g.Do(context.Background(), "POST", "/servicePrincipals/sp1/synchronization/jobs/job1/provisionOnDemand",
		map[string]any{"parameters": []any{}}, &out)
	if err != nil || out.ID != "result-1" {
		t.Fatalf("%v %+v", err, out)
	}
	for _, bad := range []string{"https://evil.example/v1.0/users", "//evil.example/x", "/users/../../beta/users", "", "/users//x", `/users\x`} {
		if err := f.g.Do(context.Background(), "GET", bad, nil, nil); err == nil || !strings.Contains(err.Error(), "m365:") {
			t.Errorf("Do accepted %q: %v", bad, err)
		}
	}
	if err := f.g.Do(context.Background(), "GET", "/nothing", nil, nil); err == nil || errors.Is(err, ErrForbidden) {
		t.Fatalf("404: %v", err)
	}
}

func TestNewGraphRoots(t *testing.T) {
	ts := &fakeTokens{tok: "token-1"}
	for _, ok := range []string{"https://graph.microsoft.com", "https://graph.microsoft.us/", "https://dod-graph.microsoft.us", "http://127.0.0.1:8080"} {
		if _, err := NewGraph(ts, ok); err != nil {
			t.Errorf("%s: %v", ok, err)
		}
	}
	for _, bad := range []string{"http://graph.microsoft.com", "https://graph.microsoft.com/v1.0", "ftp://x", ""} {
		if _, err := NewGraph(ts, bad); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
}

func TestRetryAfterParsing(t *testing.T) {
	now := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
	if d := retryAfter("5", 0, now); d != 5*time.Second {
		t.Error(d)
	}
	if d := retryAfter(now.Add(9*time.Second).Format(http.TimeFormat), 0, now); d != 9*time.Second {
		t.Error(d)
	}
	if d := retryAfter("", 2, now); d != 4*time.Second {
		t.Error(d)
	}
}
