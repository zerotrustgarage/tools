package m365

// Delegated sign-in as the technician (design 6.2a): OAuth 2.0 authorization
// code grant (RFC 6749) for a public client with PKCE S256 (RFC 7636), in the
// system browser, with a loopback redirect (RFC 8252). The app registration's
// redirect URI is http://localhost on the "Mobile and desktop applications"
// platform; the Microsoft identity platform ignores the port when it matches a
// localhost redirect URI, so a random port per sign-in works
// (https://learn.microsoft.com/en-us/entra/identity-platform/reply-url#localhost-exceptions).
// Tokens are held in this process's memory only and are wiped on SignOut.

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
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

// graphScopes are the delegated Microsoft Graph permissions the console uses
// (design 6.2a). They are requested by name, not with /.default, so consent is
// predictable.
var graphScopes = []string{
	"User.Read.All",
	"Organization.Read.All",
	"GroupMember.Read.All",
	"Device.Read.All",
	"UserAuthenticationMethod.Read.All",
	"AuditLog.Read.All",
}

// oidcScopes are OpenID Connect scopes: openid and profile return an id_token
// with preferred_username (Account), offline_access a refresh token.
var oidcScopes = []string{"openid", "profile", "offline_access"}

// Scopes returns the scopes requested at sign-in for s. Graph permissions are
// prefixed with the cloud's Graph host, because a scope without a resource is
// taken to be the commercial https://graph.microsoft.com
// (https://learn.microsoft.com/en-us/entra/identity-platform/scopes-oidc).
func Scopes(s Settings) ([]string, error) {
	ep, err := EndpointsFor(s.Cloud)
	if err != nil {
		return nil, err
	}
	names := append([]string(nil), graphScopes...)
	if s.CloudSync {
		names = append(names, "Synchronization.ReadWrite.All")
	}
	out := make([]string, 0, len(names)+len(oidcScopes))
	for _, n := range names {
		out = append(out, ep.Graph+"/"+n)
	}
	return append(out, oidcScopes...), nil
}

// SessionOptions configure a Session. Only OpenBrowser is normally set; the
// endpoint and client fields exist so tests can run a fake identity provider.
type SessionOptions struct {
	// OpenBrowser opens the sign-in page in the system browser. The desktop
	// app passes Wails' BrowserOpenURL. Nil uses DefaultOpenBrowser.
	OpenBrowser func(url string) error
	// LoginHint pre-fills the account name on the sign-in page (optional).
	LoginHint string
	// Timeout bounds the wait for the browser to come back (default 5 minutes).
	Timeout time.Duration
	// HTTPClient redeems and refreshes tokens (default: 30 second timeout).
	HTTPClient *http.Client
	// AuthorizeURL and TokenURL override {login}/{tenant}/oauth2/v2.0/...
	AuthorizeURL string
	TokenURL     string
}

// Session is one technician's delegated Microsoft 365 sign-in. It is safe for
// concurrent use. Tokens live only in this process's memory: never on disk, in
// a log or in an error.
type Session struct {
	clientID     string
	scope        string
	authorizeURL string
	tokenURL     string
	openBrowser  func(string) error
	loginHint    string
	timeout      time.Duration
	client       *http.Client
	now          func() time.Time

	refreshMu sync.Mutex // one refresh at a time

	mu        sync.Mutex
	access    []byte
	refresh   []byte
	expires   time.Time
	account   string
	gen       uint64 // incremented by SignOut, so a refresh in flight is discarded
	cancelIn  context.CancelFunc
	signingIn bool
}

// refreshSkew is how long before expiry an access token is refreshed.
const refreshSkew = 5 * time.Minute

// maxTokenResponse bounds a token endpoint response.
const maxTokenResponse = 1 << 20

// NewSession prepares a sign-in for s. It does not contact Microsoft.
func NewSession(s Settings, o SessionOptions) (*Session, error) {
	if err := s.Validate(); err != nil {
		return nil, err
	}
	ep, _ := EndpointsFor(s.Cloud)
	scopes, err := Scopes(s)
	if err != nil {
		return nil, err
	}
	tenant := url.PathEscape(strings.TrimSpace(s.TenantID))
	ss := &Session{
		clientID:     strings.TrimSpace(s.ClientID),
		scope:        strings.Join(scopes, " "),
		authorizeURL: ep.Login + "/" + tenant + "/oauth2/v2.0/authorize",
		tokenURL:     ep.Login + "/" + tenant + "/oauth2/v2.0/token",
		openBrowser:  o.OpenBrowser,
		loginHint:    o.LoginHint,
		timeout:      o.Timeout,
		client:       o.HTTPClient,
		now:          time.Now,
	}
	if o.AuthorizeURL != "" {
		ss.authorizeURL = o.AuthorizeURL
	}
	if o.TokenURL != "" {
		ss.tokenURL = o.TokenURL
	}
	if ss.openBrowser == nil {
		ss.openBrowser = DefaultOpenBrowser
	}
	if ss.timeout <= 0 {
		ss.timeout = 5 * time.Minute
	}
	if ss.client == nil {
		ss.client = &http.Client{Timeout: 30 * time.Second}
	}
	return ss, nil
}

// Account names the signed-in account: the preferred_username claim of the
// id_token. The id_token is decoded WITHOUT signature validation, for display
// only; it never decides access (Graph and Entra do).
func (s *Session) Account() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.account
}

// SignedIn reports whether the session holds tokens.
func (s *Session) SignedIn() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.access) > 0
}

// SignOut wipes the tokens from memory and cancels a sign-in in progress.
// Byte slices are zeroed; copies already handed out as strings by Token cannot
// be zeroed (Go strings are immutable), so wiping is best effort.
func (s *Session) SignOut() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.wipeLocked()
	if s.cancelIn != nil {
		s.cancelIn()
	}
}

func (s *Session) wipeLocked() {
	clear(s.access)
	clear(s.refresh)
	s.access, s.refresh = nil, nil
	s.expires = time.Time{}
	s.account = ""
	s.gen++
}

// Token returns a current access token, refreshing it first when it expires
// within five minutes.
func (s *Session) Token(ctx context.Context) (string, error) {
	s.mu.Lock()
	if len(s.access) == 0 {
		s.mu.Unlock()
		return "", ErrNotSignedIn
	}
	if s.now().Add(refreshSkew).Before(s.expires) {
		t := string(s.access)
		s.mu.Unlock()
		return t, nil
	}
	s.mu.Unlock()
	return s.renew(ctx, "")
}

// Renew is called after Graph rejected the access token rejected (HTTP 401).
// It refreshes unless another caller already replaced that token.
func (s *Session) Renew(ctx context.Context, rejected string) (string, error) {
	if rejected == "" {
		return "", errors.New("m365: Renew needs the rejected token")
	}
	return s.renew(ctx, rejected)
}

// renew refreshes the access token. With rejected empty it refreshes only if
// the token is still near expiry once the lock is held; with rejected set, only
// if the current token is still that one.
func (s *Session) renew(ctx context.Context, rejected string) (string, error) {
	s.refreshMu.Lock()
	defer s.refreshMu.Unlock()

	s.mu.Lock()
	if len(s.refresh) == 0 {
		s.mu.Unlock()
		return "", ErrNotSignedIn
	}
	current := string(s.access)
	fresh := s.now().Add(refreshSkew).Before(s.expires)
	if (rejected == "" && fresh) || (rejected != "" && current != rejected) {
		s.mu.Unlock()
		return current, nil
	}
	gen := s.gen
	form := url.Values{
		"grant_type":    {"refresh_token"},
		"client_id":     {s.clientID},
		"refresh_token": {string(s.refresh)},
		"scope":         {s.scope},
	}
	s.mu.Unlock()

	tr, err := s.redeem(ctx, form)
	if err != nil {
		var te *tokenError
		if errors.As(err, &te) && te.Code == "invalid_grant" {
			s.mu.Lock()
			if s.gen == gen {
				s.wipeLocked()
			}
			s.mu.Unlock()
			return "", fmt.Errorf("%w: the Microsoft 365 sign-in has ended (%v); sign in again", ErrNotSignedIn, err)
		}
		return "", err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.gen != gen {
		tr.wipe()
		return "", ErrNotSignedIn
	}
	s.storeLocked(tr)
	return string(s.access), nil
}

// SignIn runs the authorization code flow in the system browser and waits for
// the browser to come back, for the timeout or for ctx.
func (s *Session) SignIn(ctx context.Context) error {
	s.mu.Lock()
	if s.signingIn {
		s.mu.Unlock()
		return errors.New("a Microsoft 365 sign-in is already in progress")
	}
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	s.signingIn, s.cancelIn = true, cancel
	gen := s.gen
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.signingIn, s.cancelIn = false, nil
		s.mu.Unlock()
	}()

	verifier, err := randomToken(32)
	if err != nil {
		return err
	}
	state, err := randomToken(32)
	if err != nil {
		return err
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return fmt.Errorf("cannot listen on the loopback address for the sign-in redirect: %w", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	redirectURI := "http://localhost:" + strconv.Itoa(port)

	lb := &loopback{state: state, port: port, done: make(chan callback, 1)}
	srv := &http.Server{
		Handler:           lb,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		MaxHeaderBytes:    16 << 10,
	}
	go func() { _ = srv.Serve(ln) }()
	defer srv.Close()

	q := url.Values{
		"client_id":             {s.clientID},
		"response_type":         {"code"},
		"redirect_uri":          {redirectURI},
		"response_mode":         {"query"},
		"scope":                 {s.scope},
		"state":                 {state},
		"code_challenge":        {pkceChallenge(verifier)},
		"code_challenge_method": {"S256"},
		"prompt":                {"select_account"},
	}
	if s.loginHint != "" {
		q.Set("login_hint", s.loginHint)
	}
	authURL := s.authorizeURL + "?" + q.Encode()
	if err := s.openBrowser(authURL); err != nil {
		return fmt.Errorf("cannot open the browser for the Microsoft 365 sign-in: %w", err)
	}

	var cb callback
	select {
	case cb = <-lb.done:
	case <-ctx.Done():
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return errors.New("the Microsoft 365 sign-in timed out waiting for the browser")
		}
		return fmt.Errorf("the Microsoft 365 sign-in was cancelled: %w", ctx.Err())
	}
	if cb.err != nil {
		return cb.err
	}

	tr, err := s.redeem(ctx, url.Values{
		"grant_type":    {"authorization_code"},
		"client_id":     {s.clientID},
		"code":          {cb.code},
		"redirect_uri":  {redirectURI},
		"code_verifier": {verifier},
		"scope":         {s.scope},
	})
	if err != nil {
		return err
	}
	if tr.RefreshToken == "" {
		tr.wipe()
		return errors.New("Microsoft 365 returned no refresh token (offline_access not granted)")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.gen != gen {
		tr.wipe()
		return errors.New("the Microsoft 365 sign-in was cancelled")
	}
	account := idTokenAccount(tr.IDToken)
	s.storeLocked(tr) // wipes tr
	s.account = account
	return nil
}

func (s *Session) storeLocked(tr *tokenResponse) {
	clear(s.access)
	s.access = []byte(tr.AccessToken)
	if tr.RefreshToken != "" { // refresh tokens rotate; keep the old one if none came back
		clear(s.refresh)
		s.refresh = []byte(tr.RefreshToken)
	}
	s.expires = s.now().Add(time.Duration(tr.ExpiresIn) * time.Second)
	tr.wipe()
}

// callback is what the loopback listener received from the browser.
type callback struct {
	code string
	err  error
}

// loopback answers the browser's redirect on 127.0.0.1. It accepts one
// result; a state mismatch or an error from the identity provider ends the
// sign-in. The page it shows is static and echoes nothing.
type loopback struct {
	state string
	port  int
	once  sync.Once
	done  chan callback
}

const loopbackPage = `<!doctype html>
<html lang="en"><head><meta charset="utf-8"><title>ZTG Directory</title></head>
<body style="font-family:system-ui,sans-serif;margin:3rem">
<p>%s</p><p>You can close this window and return to ZTG Directory.</p>
</body></html>`

func (l *loopback) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Cache-Control", "no-store")
	h.Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("X-Content-Type-Options", "nosniff")
	// Only our own redirect: GET / on this port, by its loopback name (a
	// rebinding DNS name in Host is refused).
	host := r.Host
	if r.Method != http.MethodGet || r.URL.Path != "/" ||
		(host != "localhost:"+strconv.Itoa(l.port) && host != "127.0.0.1:"+strconv.Itoa(l.port)) {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	q := r.URL.Query()
	code, state, idpErr := q.Get("code"), q.Get("state"), q.Get("error")
	if code == "" && idpErr == "" {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	var cb callback
	switch {
	case subtle.ConstantTimeCompare([]byte(state), []byte(l.state)) != 1:
		cb.err = errors.New("the Microsoft 365 sign-in was refused: the answer did not match this sign-in (state mismatch)")
	case idpErr != "":
		cb.err = fmt.Errorf("the Microsoft 365 sign-in failed: %s", describeIdP(idpErr, q.Get("error_description")))
	default:
		cb.code = code
	}
	msg := "Sign-in finished."
	if cb.err != nil {
		msg = "Sign-in did not complete. ZTG Directory shows the reason."
	}
	delivered := false
	l.once.Do(func() { l.done <- cb; delivered = true })
	if !delivered {
		msg = "This sign-in has already been answered."
	}
	fmt.Fprintf(w, loopbackPage, msg)
}

// tokenResponse is the token endpoint's answer.
type tokenResponse struct {
	AccessToken  string  `json:"access_token"`
	RefreshToken string  `json:"refresh_token"`
	IDToken      string  `json:"id_token"`
	TokenType    string  `json:"token_type"`
	ExpiresIn    flexInt `json:"expires_in"`
}

func (t *tokenResponse) wipe() { *t = tokenResponse{} }

// tokenError is an OAuth error response (RFC 6749 section 5.2).
type tokenError struct {
	Code        string `json:"error"`
	Description string `json:"error_description"`
}

func (e *tokenError) Error() string { return describeIdP(e.Code, e.Description) }

func (s *Session) redeem(ctx context.Context, form url.Values) (*tokenResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("cannot reach the Microsoft 365 sign-in service: %w", stripURL(err))
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxTokenResponse+1))
	if err != nil {
		return nil, fmt.Errorf("reading the Microsoft 365 token response: %w", err)
	}
	if len(body) > maxTokenResponse {
		return nil, errors.New("the Microsoft 365 token response is too large")
	}
	if resp.StatusCode != http.StatusOK {
		te := &tokenError{}
		if json.Unmarshal(body, te) != nil || te.Code == "" {
			return nil, fmt.Errorf("the Microsoft 365 token request failed with HTTP %d", resp.StatusCode)
		}
		return nil, te
	}
	tr := &tokenResponse{}
	if err := json.Unmarshal(body, tr); err != nil {
		return nil, errors.New("the Microsoft 365 token response is not valid JSON")
	}
	clear(body)
	if tr.AccessToken == "" {
		return nil, errors.New("the Microsoft 365 token response has no access token")
	}
	if tr.TokenType != "" && !strings.EqualFold(tr.TokenType, "Bearer") {
		tr.wipe()
		return nil, errors.New("the Microsoft 365 token response has an unexpected token type")
	}
	if tr.ExpiresIn <= 0 {
		tr.ExpiresIn = 3600
	}
	return tr, nil
}

// flexInt accepts a JSON number or a numeric string.
type flexInt int64

func (f *flexInt) UnmarshalJSON(b []byte) error {
	s := strings.Trim(string(b), `"`)
	if s == "" || s == "null" {
		*f = 0
		return nil
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return err
	}
	*f = flexInt(n)
	return nil
}

// describeIdP formats an identity provider error code and description, both
// from Microsoft (AADSTS codes help the customer's administrator), trimmed.
func describeIdP(code, desc string) string {
	code = sanitize(code, 80)
	desc = sanitize(firstLine(desc), 300)
	if desc == "" {
		return code
	}
	return code + ": " + desc
}

func firstLine(s string) string {
	s = strings.ReplaceAll(s, `\r\n`, "\n") // descriptions sometimes carry escaped newlines
	if i := strings.IndexAny(s, "\r\n"); i >= 0 {
		return s[:i]
	}
	return s
}

// sanitize keeps printable characters and truncates to n runes.
func sanitize(s string, n int) string {
	var b strings.Builder
	count := 0
	for _, r := range s {
		if count >= n {
			b.WriteString("...")
			break
		}
		if r < 0x20 || r == 0x7f {
			r = ' '
		}
		b.WriteRune(r)
		count++
	}
	return strings.TrimSpace(b.String())
}

// stripURL removes the request URL from a transport error, so the message
// stays short and never repeats query values.
func stripURL(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		return ue.Err
	}
	return err
}

func randomToken(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("no randomness for the sign-in: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// pkceChallenge is BASE64URL(SHA256(ASCII(verifier))) (RFC 7636 section 4.2).
func pkceChallenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// idTokenAccount reads preferred_username (or upn) from an id_token's payload
// without validating the signature: display only.
func idTokenAccount(idToken string) string {
	parts := strings.Split(idToken, ".")
	if len(parts) != 3 || len(parts[1]) > 16<<10 {
		return ""
	}
	payload, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(parts[1], "="))
	if err != nil {
		return ""
	}
	var claims struct {
		PreferredUsername string `json:"preferred_username"`
		UPN               string `json:"upn"`
	}
	if json.Unmarshal(payload, &claims) != nil {
		return ""
	}
	if claims.PreferredUsername != "" {
		return sanitize(claims.PreferredUsername, 256)
	}
	return sanitize(claims.UPN, 256)
}
