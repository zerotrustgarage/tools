package m365

// Graph implements Tenant on Microsoft Graph v1.0, called as the signed-in
// technician with a delegated token (design 6.2a). Everything read from
// Graph is untrusted directory data: filter values are OData-escaped and the
// client follows @odata.nextLink only on the Graph host it was given.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// TokenSource supplies delegated access tokens to Graph. *Session is one.
type TokenSource interface {
	// Account names the signed-in account.
	Account() string
	// Token returns a current access token.
	Token(ctx context.Context) (string, error)
	// Renew returns a newer token after Graph answered 401 to rejected.
	Renew(ctx context.Context, rejected string) (string, error)
}

var _ TokenSource = (*Session)(nil)

// GraphError is a refusal from Microsoft Graph: the HTTP status and Graph's
// own error code and message. It never carries a token. A 403 unwraps to
// ErrForbidden and a 401 to ErrNotSignedIn.
type GraphError struct {
	Method  string
	Path    string // relative to the version root, without the query
	Status  int
	Code    string
	Message string
}

func (e *GraphError) Error() string {
	s := fmt.Sprintf("Microsoft Graph %s %s: HTTP %d", e.Method, e.Path, e.Status)
	if e.Code != "" {
		s += " " + e.Code
	}
	if e.Message != "" {
		s += ": " + e.Message
	}
	return s
}

func (e *GraphError) Unwrap() error {
	switch e.Status {
	case http.StatusForbidden:
		return ErrForbidden
	case http.StatusUnauthorized:
		return ErrNotSignedIn
	}
	return nil
}

// Graph is a Tenant backed by Microsoft Graph v1.0. Exported fields may be
// set before first use.
type Graph struct {
	tokens TokenSource
	origin *url.URL // scheme and host of the Graph root
	base   string   // origin + "/v1.0"

	// HTTPClient sends the requests (default: 60 second timeout, no redirects).
	HTTPClient *http.Client
	// MaxPages caps @odata.nextLink paging per list (default 50); beyond it
	// the call fails rather than return a silently partial list.
	MaxPages int
	// MaxRetries bounds retries after 429 or 503 (default 3).
	MaxRetries int
	// MaxRetryWait bounds one Retry-After wait (default 30 seconds).
	MaxRetryWait time.Duration

	sleep func(context.Context, time.Duration) error

	mu         sync.Mutex
	skus       map[string]string // lower-case skuId to skuPartNumber
	skusLoaded bool
	noSignIn   bool // Graph refused signInActivity in this session
}

var _ Tenant = (*Graph)(nil)

// maxResponse bounds one Graph response body.
const maxResponse = 16 << 20

// NewGraph returns a Graph client for the Graph root of the customer's cloud
// (Endpoints.Graph, for example https://graph.microsoft.us). Plain http is
// accepted only for a loopback host (tests).
func NewGraph(ts TokenSource, graphRoot string) (*Graph, error) {
	if ts == nil {
		return nil, errors.New("m365: NewGraph needs a token source")
	}
	u, err := url.Parse(strings.TrimRight(graphRoot, "/"))
	if err != nil || u.Host == "" || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.User != nil {
		return nil, fmt.Errorf("m365: %q is not a Microsoft Graph root", graphRoot)
	}
	switch u.Scheme {
	case "https":
	case "http":
		if ip := net.ParseIP(u.Hostname()); u.Hostname() != "localhost" && (ip == nil || !ip.IsLoopback()) {
			return nil, fmt.Errorf("m365: Microsoft Graph must be reached over https")
		}
	default:
		return nil, fmt.Errorf("m365: %q is not a Microsoft Graph root", graphRoot)
	}
	origin := &url.URL{Scheme: u.Scheme, Host: u.Host}
	return &Graph{tokens: ts, origin: origin, base: origin.String() + "/v1.0", sleep: sleepCtx}, nil
}

// Account names the signed-in cloud account.
func (g *Graph) Account() string { return g.tokens.Account() }

func (g *Graph) client() *http.Client {
	if g.HTTPClient != nil {
		return g.HTTPClient
	}
	return defaultGraphClient
}

var defaultGraphClient = &http.Client{
	Timeout:       60 * time.Second,
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
}

func (g *Graph) maxPages() int {
	if g.MaxPages > 0 {
		return g.MaxPages
	}
	return 50
}

func (g *Graph) maxRetries() int {
	if g.MaxRetries > 0 {
		return g.MaxRetries
	}
	return 3
}

func (g *Graph) maxRetryWait() time.Duration {
	if g.MaxRetryWait > 0 {
		return g.MaxRetryWait
	}
	return 30 * time.Second
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Do sends one request to path, relative to the Graph version root (for
// example "/servicePrincipals/{id}/synchronization/jobs/{jobId}/provisionOnDemand";
// the caller escapes identifiers with url.PathEscape). body, when not nil, is
// sent as JSON; out, when not nil, receives the JSON answer. Errors are mapped
// as for every Tenant call (GraphError, ErrForbidden, ErrNotSignedIn).
func (g *Graph) Do(ctx context.Context, method, path string, body any, out any) error {
	u, err := g.resolve(path)
	if err != nil {
		return err
	}
	var payload []byte
	if body != nil {
		if payload, err = json.Marshal(body); err != nil {
			return fmt.Errorf("m365: encoding the request body: %w", err)
		}
	}
	return g.send(ctx, method, u, payload, false, out)
}

// resolve turns a version-relative path into an absolute URL on the Graph
// host. It refuses absolute URLs, dot segments and empty segments, so a path
// cannot leave the version root.
func (g *Graph) resolve(path string) (string, error) {
	p, rawQuery, _ := strings.Cut(path, "?")
	p = strings.TrimPrefix(p, "/")
	if p == "" || strings.Contains(path, "://") || strings.ContainsAny(p, "\\#") {
		return "", fmt.Errorf("m365: %q is not a Graph path", path)
	}
	for _, seg := range strings.Split(p, "/") {
		if seg == "" || seg == "." || seg == ".." {
			return "", fmt.Errorf("m365: %q is not a Graph path", path)
		}
	}
	full := g.base + "/" + p
	if rawQuery != "" {
		full += "?" + rawQuery
	}
	if err := g.checkLink(full); err != nil {
		return "", err
	}
	return full, nil
}

// checkLink accepts only a URL on the Graph origin under /v1.0/, so a token is
// never sent elsewhere (for example by a crafted @odata.nextLink).
func (g *Graph) checkLink(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != g.origin.Scheme || !strings.EqualFold(u.Host, g.origin.Host) ||
		u.User != nil || !strings.HasPrefix(u.EscapedPath(), "/v1.0/") {
		return errors.New("m365: refusing a link outside the Microsoft Graph root")
	}
	for _, seg := range strings.Split(u.Path, "/") {
		if seg == "." || seg == ".." {
			return errors.New("m365: refusing a link outside the Microsoft Graph root")
		}
	}
	return nil
}

// send performs one logical request: one renewal after 401, bounded retries
// after 429 and 503.
func (g *Graph) send(ctx context.Context, method, rawURL string, body []byte, advanced bool, out any) error {
	tok, err := g.tokens.Token(ctx)
	if err != nil {
		return err
	}
	renewed := false
	retries := 0
	for {
		var rdr io.Reader
		if body != nil {
			rdr = bytes.NewReader(body)
		}
		req, err := http.NewRequestWithContext(ctx, method, rawURL, rdr)
		if err != nil {
			return fmt.Errorf("m365: %w", err)
		}
		req.Header.Set("Authorization", "Bearer "+tok)
		req.Header.Set("Accept", "application/json")
		req.Header.Set("User-Agent", "ZTG-Directory")
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		if advanced {
			req.Header.Set("ConsistencyLevel", "eventual")
		}
		resp, err := g.client().Do(req)
		if err != nil {
			return fmt.Errorf("cannot reach Microsoft Graph (%s %s): %w", method, relPath(req.URL), stripURL(err))
		}
		data, readErr := io.ReadAll(io.LimitReader(resp.Body, maxResponse+1))
		resp.Body.Close()
		if readErr != nil {
			return fmt.Errorf("reading Microsoft Graph's answer (%s %s): %w", method, relPath(req.URL), readErr)
		}
		if len(data) > maxResponse {
			return fmt.Errorf("Microsoft Graph's answer to %s %s is too large", method, relPath(req.URL))
		}
		switch {
		case resp.StatusCode >= 200 && resp.StatusCode < 300:
			if out == nil || len(bytes.TrimSpace(data)) == 0 {
				return nil
			}
			if err := json.Unmarshal(data, out); err != nil {
				return fmt.Errorf("Microsoft Graph's answer to %s %s is not the expected JSON: %w", method, relPath(req.URL), err)
			}
			return nil
		case resp.StatusCode == http.StatusUnauthorized && !renewed:
			renewed = true
			if tok, err = g.tokens.Renew(ctx, tok); err != nil {
				return err
			}
			continue
		case (resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusServiceUnavailable) && retries < g.maxRetries():
			wait := retryAfter(resp.Header.Get("Retry-After"), retries, time.Now())
			if wait > g.maxRetryWait() {
				wait = g.maxRetryWait()
			}
			retries++
			if err := g.sleep(ctx, wait); err != nil {
				return err
			}
			continue
		}
		return graphError(method, req.URL, resp.StatusCode, data)
	}
}

// retryAfter reads Retry-After (seconds or an HTTP date); without one it backs
// off 1, 2, 4... seconds.
func retryAfter(h string, attempt int, now time.Time) time.Duration {
	h = strings.TrimSpace(h)
	if n, err := strconv.Atoi(h); err == nil && n >= 0 {
		return time.Duration(n) * time.Second
	}
	if t, err := http.ParseTime(h); err == nil {
		if d := t.Sub(now); d > 0 {
			return d
		}
		return 0
	}
	return time.Second << min(attempt, 5)
}

func graphError(method string, u *url.URL, status int, data []byte) error {
	var body struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	_ = json.Unmarshal(data, &body)
	return &GraphError{
		Method:  method,
		Path:    relPath(u),
		Status:  status,
		Code:    sanitize(body.Error.Code, 100),
		Message: sanitize(body.Error.Message, 400),
	}
}

// relPath is the URL path below /v1.0, without the query (which can hold
// directory values) for error messages.
func relPath(u *url.URL) string {
	return strings.TrimPrefix(u.Path, "/v1.0")
}

// getAll reads every page of a collection, following @odata.nextLink up to
// MaxPages.
func getAll[T any](ctx context.Context, g *Graph, first string, advanced bool) ([]T, error) {
	var out []T
	next := first
	for page := 0; next != ""; page++ {
		if page >= g.maxPages() {
			u, _ := url.Parse(first)
			return nil, fmt.Errorf("Microsoft Graph returned more than %d pages for %s; stopped", g.maxPages(), relPath(u))
		}
		var p struct {
			Value    []T    `json:"value"`
			NextLink string `json:"@odata.nextLink"`
		}
		if err := g.send(ctx, http.MethodGet, next, nil, advanced, &p); err != nil {
			return nil, err
		}
		out = append(out, p.Value...)
		if p.NextLink != "" {
			if err := g.checkLink(p.NextLink); err != nil {
				return nil, err
			}
		}
		next = p.NextLink
	}
	return out, nil
}

// query builds an OData query string from key and value pairs. Keys are
// written as given ($filter keeps its dollar sign); values are percent-encoded
// with spaces as %20.
func query(kv ...string) string {
	var b strings.Builder
	for i := 0; i+1 < len(kv); i += 2 {
		if b.Len() > 0 {
			b.WriteByte('&')
		}
		b.WriteString(kv[i])
		b.WriteByte('=')
		b.WriteString(strings.ReplaceAll(url.QueryEscape(kv[i+1]), "+", "%20"))
	}
	return b.String()
}

// odataString quotes v as an OData string literal: single quotes are doubled,
// so directory data can never end the literal and alter a filter.
func odataString(v string) string {
	return "'" + strings.ReplaceAll(v, "'", "''") + "'"
}

// segment escapes one identifier for a URL path.
func segment(id string) (string, error) {
	id = strings.TrimSpace(id)
	if id == "" || id == "." || id == ".." {
		return "", fmt.Errorf("m365: %q is not an object ID", id)
	}
	return url.PathEscape(id), nil
}

// userSelect is exactly the Graph properties behind User (manager and the
// last sign-in are read separately or added below).
var userSelect = strings.Join([]string{
	"id", "userPrincipalName", "displayName", "givenName", "surname", "mail", "proxyAddresses",
	"businessPhones", "mobilePhone", "jobTitle", "department", "companyName", "officeLocation",
	"streetAddress", "city", "state", "postalCode", "country", "employeeId", "usageLocation",
	"accountEnabled", "createdDateTime", "onPremisesSyncEnabled", "onPremisesImmutableId",
	"onPremisesSecurityIdentifier", "onPremisesSamAccountName", "onPremisesUserPrincipalName",
	"onPremisesDistinguishedName", "onPremisesLastSyncDateTime", "onPremisesExtensionAttributes",
	"onPremisesProvisioningErrors", "assignedLicenses", "licenseAssignmentStates",
}, ",")

// graphUser is a Graph user as selected, before licenses, the last sign-in
// and the manager are resolved into User.
type graphUser struct {
	User
	AssignedLicenses []struct {
		SkuID         string   `json:"skuId"`
		DisabledPlans []string `json:"disabledPlans"`
	} `json:"assignedLicenses"`
	LicenseAssignmentStates []struct {
		SkuID           string  `json:"skuId"`
		AssignedByGroup *string `json:"assignedByGroup"`
	} `json:"licenseAssignmentStates"`
	SignInActivity *struct {
		LastSuccessfulSignInDateTime *time.Time `json:"lastSuccessfulSignInDateTime"`
	} `json:"signInActivity"`
}

// UserByImmutableID finds the user whose onPremisesImmutableId equals id
// ($filter eq works by default for this property, no advanced query).
func (g *Graph) UserByImmutableID(ctx context.Context, id string) (*User, error) {
	if id == "" {
		return nil, nil
	}
	return g.first(ctx, "onPremisesImmutableId eq "+odataString(id))
}

// UserBySID finds the user whose onPremisesSecurityIdentifier equals sid
// ($filter eq works by default for this property).
func (g *Graph) UserBySID(ctx context.Context, sid string) (*User, error) {
	if sid == "" {
		return nil, nil
	}
	return g.first(ctx, "onPremisesSecurityIdentifier eq "+odataString(sid))
}

// UsersByName finds users whose userPrincipalName or mail equals v.
func (g *Graph) UsersByName(ctx context.Context, v string) ([]User, error) {
	if v == "" {
		return nil, nil
	}
	q := odataString(v)
	return g.users(ctx, "userPrincipalName eq "+q+" or mail eq "+q)
}

func (g *Graph) first(ctx context.Context, filter string) (*User, error) {
	us, err := g.users(ctx, filter)
	if err != nil || len(us) == 0 {
		return nil, err
	}
	return &us[0], nil
}

// users lists users matching filter. It asks for signInActivity too; if
// Graph refuses it (no AuditLog.Read.All, no Entra ID P1, or a role that
// cannot read sign-in logs) it asks again without, for the rest of the
// session, and LastSuccessfulSignIn stays nil.
func (g *Graph) users(ctx context.Context, filter string) ([]User, error) {
	g.mu.Lock()
	withSignIn := !g.noSignIn
	g.mu.Unlock()
	list := func(sel string) ([]graphUser, error) {
		return getAll[graphUser](ctx, g, g.base+"/users?"+query("$filter", filter, "$select", sel), false)
	}
	var raw []graphUser
	var err error
	if withSignIn {
		raw, err = list(userSelect + ",signInActivity")
		if err != nil && signInRefused(err) {
			if raw, err = list(userSelect); err == nil {
				g.mu.Lock()
				g.noSignIn = true
				g.mu.Unlock()
			}
		}
	} else {
		raw, err = list(userSelect)
	}
	if err != nil {
		return nil, err
	}
	out := make([]User, 0, len(raw))
	for i := range raw {
		u, err := g.finishUser(ctx, &raw[i])
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, nil
}

// signInRefused reports a refusal that may be about signInActivity: 403, or
// a 400 that names the property, a premium licence or a license.
func signInRefused(err error) bool {
	var ge *GraphError
	if !errors.As(err, &ge) {
		return false
	}
	if ge.Status == http.StatusForbidden {
		return true
	}
	if ge.Status != http.StatusBadRequest {
		return false
	}
	s := strings.ToLower(ge.Code + " " + ge.Message)
	return strings.Contains(s, "signinactivity") || strings.Contains(s, "premium") || strings.Contains(s, "licen")
}

func (g *Graph) finishUser(ctx context.Context, gu *graphUser) (User, error) {
	u := gu.User
	for k, v := range u.OnPremisesExtensionAttrs {
		if v == "" {
			delete(u.OnPremisesExtensionAttrs, k)
		}
	}
	if gu.SignInActivity != nil && gu.SignInActivity.LastSuccessfulSignInDateTime != nil &&
		!gu.SignInActivity.LastSuccessfulSignInDateTime.IsZero() {
		t := *gu.SignInActivity.LastSuccessfulSignInDateTime
		u.LastSuccessfulSignIn = &t
	}
	if len(gu.AssignedLicenses) > 0 {
		names, err := g.skuNames(ctx)
		if err != nil {
			return User{}, err
		}
		u.Licenses = make([]License, 0, len(gu.AssignedLicenses))
		for _, al := range gu.AssignedLicenses {
			l := License{SkuID: al.SkuID, SkuPartNumber: names[strings.ToLower(al.SkuID)], DisabledPlans: al.DisabledPlans}
			for _, st := range gu.LicenseAssignmentStates {
				if strings.EqualFold(st.SkuID, al.SkuID) && st.AssignedByGroup != nil && *st.AssignedByGroup != "" {
					l.ViaGroup = true
				}
			}
			u.Licenses = append(u.Licenses, l)
		}
	}
	mgr, err := g.managerUPN(ctx, u.ID)
	if err != nil {
		return User{}, err
	}
	u.ManagerUPN = mgr
	return u, nil
}

// skuNames maps skuId to skuPartNumber from /subscribedSkus, read once per
// Graph (one sign-in session). If the technician may not read subscriptions
// the names stay empty rather than failing the mirror.
func (g *Graph) skuNames(ctx context.Context) (map[string]string, error) {
	g.mu.Lock()
	if g.skusLoaded {
		m := g.skus
		g.mu.Unlock()
		return m, nil
	}
	g.mu.Unlock()
	type sku struct {
		SkuID         string `json:"skuId"`
		SkuPartNumber string `json:"skuPartNumber"`
	}
	list, err := getAll[sku](ctx, g, g.base+"/subscribedSkus?"+query("$select", "skuId,skuPartNumber"), false)
	m := map[string]string{}
	if err != nil {
		if !errors.Is(err, ErrForbidden) {
			return nil, err
		}
	}
	for _, s := range list {
		m[strings.ToLower(s.SkuID)] = s.SkuPartNumber
	}
	g.mu.Lock()
	g.skus, g.skusLoaded = m, true
	g.mu.Unlock()
	return m, nil
}

// managerUPN reads the direct manager's UPN; 404 means no manager.
func (g *Graph) managerUPN(ctx context.Context, userID string) (string, error) {
	id, err := segment(userID)
	if err != nil {
		return "", err
	}
	var m struct {
		UPN string `json:"userPrincipalName"`
	}
	err = g.send(ctx, http.MethodGet, g.base+"/users/"+id+"/manager?"+query("$select", "userPrincipalName"), nil, false, &m)
	var ge *GraphError
	if errors.As(err, &ge) && ge.Status == http.StatusNotFound {
		return "", nil
	}
	return m.UPN, err
}

// UserGroups lists the user's direct group memberships. The OData cast to
// microsoft.graph.group needs ConsistencyLevel: eventual and $count=true
// (user-list-memberof, request headers).
func (g *Graph) UserGroups(ctx context.Context, userID string) ([]Group, error) {
	id, err := segment(userID)
	if err != nil {
		return nil, err
	}
	type graphGroup struct {
		ID                    string   `json:"id"`
		DisplayName           string   `json:"displayName"`
		Mail                  string   `json:"mail"`
		SecurityEnabled       *bool    `json:"securityEnabled"`
		MailEnabled           *bool    `json:"mailEnabled"`
		OnPremisesSyncEnabled *bool    `json:"onPremisesSyncEnabled"`
		GroupTypes            []string `json:"groupTypes"`
		IsAssignableToRole    *bool    `json:"isAssignableToRole"`
	}
	raw, err := getAll[graphGroup](ctx, g, g.base+"/users/"+id+"/memberOf/microsoft.graph.group?"+query(
		"$count", "true",
		"$select", "id,displayName,mail,securityEnabled,mailEnabled,onPremisesSyncEnabled,groupTypes,isAssignableToRole"), true)
	if err != nil {
		return nil, err
	}
	out := make([]Group, 0, len(raw))
	for _, r := range raw {
		gr := Group{
			ID: r.ID, DisplayName: r.DisplayName, Mail: r.Mail,
			SecurityEnabled:       isTrue(r.SecurityEnabled),
			MailEnabled:           isTrue(r.MailEnabled),
			OnPremisesSyncEnabled: isTrue(r.OnPremisesSyncEnabled),
		}
		gr.Kind = groupKind(gr.OnPremisesSyncEnabled, r.GroupTypes, isTrue(r.IsAssignableToRole))
		out = append(out, gr)
	}
	return out, nil
}

// groupKind: synced from AD first, then Microsoft 365 (Unified), then
// role-assignable, else a cloud security or distribution group.
func groupKind(synced bool, groupTypes []string, roleAssignable bool) string {
	switch {
	case synced:
		return "synced"
	case containsFold(groupTypes, "Unified"):
		return "microsoft365"
	case roleAssignable:
		return "role"
	}
	return "cloud"
}

// UserDevices lists the user's registered and owned devices, each once.
func (g *Graph) UserDevices(ctx context.Context, userID string) ([]Device, error) {
	id, err := segment(userID)
	if err != nil {
		return nil, err
	}
	q := query("$count", "true",
		"$select", "id,displayName,operatingSystem,trustType,isManaged,isCompliant,accountEnabled,approximateLastSignInDateTime")
	var out []Device
	seen := map[string]bool{}
	for _, rel := range []string{"registeredDevices", "ownedDevices"} {
		list, err := getAll[Device](ctx, g, g.base+"/users/"+id+"/"+rel+"/microsoft.graph.device?"+q, true)
		if err != nil {
			return nil, err
		}
		for _, d := range list {
			key := strings.ToLower(d.ID)
			if d.ID == "" || seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, d)
		}
	}
	return out, nil
}

// methodKinds names the authentication method types of Graph v1.0
// (authenticationmethods-overview). Password is excluded by AuthMethods.
var methodKinds = map[string]string{
	"microsoftAuthenticatorAuthenticationMethod":  "Authenticator app",
	"phoneAuthenticationMethod":                   "Phone",
	"fido2AuthenticationMethod":                   "FIDO2 security key or passkey",
	"windowsHelloForBusinessAuthenticationMethod": "Windows Hello for Business",
	"emailAuthenticationMethod":                   "Email",
	"softwareOathAuthenticationMethod":            "Software OATH token",
	"temporaryAccessPassAuthenticationMethod":     "Temporary Access Pass",
	"platformCredentialAuthenticationMethod":      "Platform credential",
	"externalAuthenticationMethod":                "External authentication method",
	"qrCodePinAuthenticationMethod":               "QR code and PIN",
	"hardwareOathAuthenticationMethod":            "Hardware OATH token",
	"x509CertificateAuthenticationMethod":         "Certificate",
}

// AuthMethods names each of the user's registered sign-in methods, password
// excluded; two phones are two entries, so len is the method count.
func (g *Graph) AuthMethods(ctx context.Context, userID string) ([]string, error) {
	id, err := segment(userID)
	if err != nil {
		return nil, err
	}
	type method struct {
		Type string `json:"@odata.type"`
	}
	list, err := getAll[method](ctx, g, g.base+"/users/"+id+"/authentication/methods", false)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(list))
	for _, m := range list {
		name := strings.TrimPrefix(m.Type, "#microsoft.graph.")
		if name == "passwordAuthenticationMethod" {
			continue
		}
		if k, ok := methodKinds[name]; ok {
			out = append(out, k)
			continue
		}
		k := strings.TrimSuffix(name, "AuthenticationMethod")
		if k == "" {
			k = "Unknown method"
		}
		out = append(out, sanitize(k, 80))
	}
	return out, nil
}

// Organization reads the tenant and its directory synchronization state.
func (g *Graph) Organization(ctx context.Context) (Organization, error) {
	type graphOrg struct {
		ID                         string     `json:"id"`
		DisplayName                string     `json:"displayName"`
		OnPremisesSyncEnabled      *bool      `json:"onPremisesSyncEnabled"`
		OnPremisesLastSyncDateTime *time.Time `json:"onPremisesLastSyncDateTime"`
		VerifiedDomains            []struct {
			Name string `json:"name"`
		} `json:"verifiedDomains"`
	}
	list, err := getAll[graphOrg](ctx, g, g.base+"/organization?"+query("$select",
		"id,displayName,onPremisesSyncEnabled,onPremisesLastSyncDateTime,verifiedDomains"), false)
	if err != nil {
		return Organization{}, err
	}
	if len(list) == 0 {
		return Organization{}, errors.New("Microsoft Graph returned no organization")
	}
	o := list[0]
	org := Organization{
		ID: o.ID, DisplayName: o.DisplayName,
		OnPremisesSyncEnabled:      isTrue(o.OnPremisesSyncEnabled),
		OnPremisesLastSyncDateTime: o.OnPremisesLastSyncDateTime,
	}
	for _, d := range o.VerifiedDomains {
		if d.Name != "" {
			org.VerifiedDomains = append(org.VerifiedDomains, d.Name)
		}
	}
	return org, nil
}

func isTrue(b *bool) bool { return b != nil && *b }

func containsFold(list []string, v string) bool {
	for _, s := range list {
		if strings.EqualFold(s, v) {
			return true
		}
	}
	return false
}
