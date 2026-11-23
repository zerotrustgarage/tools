package m365

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeIdP is a Microsoft identity platform stand-in: /authorize redirects
// to the loopback with a code, /token redeems codes once and refresh tokens.
type fakeIdP struct {
	t   *testing.T
	srv *httptest.Server

	mu          sync.Mutex
	authQuery   url.Values
	code        string
	codeUsed    bool
	redemptions int
	refreshes   int
	verifier    string
	wrongState  bool
	idpError    string
	refreshDead bool
	issued      int
}

func newFakeIdP(t *testing.T) *fakeIdP {
	f := &fakeIdP{t: t, code: "code-1"}
	mux := http.NewServeMux()
	mux.HandleFunc("/authorize", f.authorize)
	mux.HandleFunc("/token", f.token)
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeIdP) authorize(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	q := r.URL.Query()
	f.authQuery = q
	back := url.Values{"state": {q.Get("state")}}
	if f.wrongState {
		back.Set("state", "forged")
	}
	if f.idpError != "" {
		back.Set("error", f.idpError)
		back.Set("error_description", "AADSTS50105: not assigned.\r\nTrace ID: x")
	} else {
		back.Set("code", f.code)
	}
	http.Redirect(w, r, q.Get("redirect_uri")+"/?"+back.Encode(), http.StatusFound)
}

func idToken(upn string) string {
	enc := base64.RawURLEncoding
	payload, _ := json.Marshal(map[string]string{"preferred_username": upn})
	return enc.EncodeToString([]byte(`{"alg":"none"}`)) + "." + enc.EncodeToString(payload) + ".sig"
}

func (f *fakeIdP) token(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", 400)
		return
	}
	fail := func(code string) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(400)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": code, "error_description": "refused by fake"})
	}
	if r.PostForm.Get("client_id") != "client-1" {
		fail("invalid_client")
		return
	}
	switch r.PostForm.Get("grant_type") {
	case "authorization_code":
		f.redemptions++
		if r.PostForm.Get("code") != f.code || f.codeUsed {
			fail("invalid_grant")
			return
		}
		f.codeUsed = true
		if r.PostForm.Get("redirect_uri") != f.authQuery.Get("redirect_uri") {
			fail("invalid_grant")
			return
		}
		f.verifier = r.PostForm.Get("code_verifier")
		if pkceChallenge(f.verifier) != f.authQuery.Get("code_challenge") {
			fail("invalid_grant")
			return
		}
	case "refresh_token":
		f.refreshes++
		if f.refreshDead || r.PostForm.Get("refresh_token") != "rt-"+itoa(f.issued) {
			fail("invalid_grant")
			return
		}
	default:
		fail("unsupported_grant_type")
		return
	}
	f.issued++
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"token_type":    "Bearer",
		"access_token":  "at-" + itoa(f.issued),
		"refresh_token": "rt-" + itoa(f.issued),
		"id_token":      idToken("tech@contoso.example"),
		"expires_in":    3600,
	})
}

func itoa(n int) string { return strconv.Itoa(n) }

// snapshot reads the fake's records under its lock (the race detector does
// not see the TCP round trip as synchronization).
func (f *fakeIdP) snapshot() (q url.Values, verifier string, redemptions, refreshes int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.authQuery, f.verifier, f.redemptions, f.refreshes
}

// browser follows the authorize URL like a browser: GET it, then follow the
// redirect to the loopback (on 127.0.0.1 only). repeat replays the final
// redirect to check that a code is taken once.
type browser struct {
	lb     *loopbackOnly
	repeat bool
	pages  []string
}

func (b *browser) open(authURL string) error {
	c := b.lb.client()
	resp, err := c.Get(authURL)
	if err != nil {
		return err
	}
	resp.Body.Close()
	loc := resp.Header.Get("Location")
	n := 1
	if b.repeat {
		n = 2
	}
	for i := 0; i < n; i++ {
		r2, err := c.Get(loc)
		if err != nil {
			return err
		}
		body, _ := io.ReadAll(r2.Body)
		r2.Body.Close()
		b.pages = append(b.pages, string(body))
	}
	return nil
}

func newTestSession(t *testing.T, idp *fakeIdP, b *browser, s Settings) *Session {
	t.Helper()
	if s.TenantID == "" {
		s.TenantID = "contoso.example"
	}
	s.ClientID = "client-1"
	ss, err := NewSession(s, SessionOptions{
		OpenBrowser:  b.open,
		Timeout:      5 * time.Second,
		HTTPClient:   b.lb.client(),
		AuthorizeURL: idp.srv.URL + "/authorize",
		TokenURL:     idp.srv.URL + "/token",
	})
	if err != nil {
		t.Fatal(err)
	}
	return ss
}

func TestSignInPKCEAndAccount(t *testing.T) {
	idp := newFakeIdP(t)
	lb := &loopbackOnly{}
	b := &browser{lb: lb, repeat: true}
	s := newTestSession(t, idp, b, Settings{})
	if err := s.SignIn(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer lb.check(t)

	q, verifier, redemptions, _ := idp.snapshot()
	if q.Get("code_challenge_method") != "S256" || q.Get("response_type") != "code" || q.Get("client_id") != "client-1" {
		t.Fatalf("authorize query: %v", q)
	}
	if len(verifier) < 43 {
		t.Fatalf("verifier too short: %d", len(verifier))
	}
	if q.Get("code_challenge") != pkceChallenge(verifier) || q.Get("code_challenge") == verifier {
		t.Fatal("code_challenge is not BASE64URL(SHA256(verifier))")
	}
	if len(q.Get("state")) < 32 {
		t.Fatal("state too short")
	}
	if !strings.HasPrefix(q.Get("redirect_uri"), "http://localhost:") {
		t.Fatalf("redirect_uri = %q", q.Get("redirect_uri"))
	}
	if !strings.Contains(q.Get("scope"), "https://graph.microsoft.com/User.Read.All") ||
		!strings.Contains(q.Get("scope"), "offline_access") || strings.Contains(q.Get("scope"), "Synchronization") {
		t.Fatalf("scope = %q", q.Get("scope"))
	}
	// Code redeemed once, even though the browser replayed the redirect.
	if redemptions != 1 {
		t.Fatalf("code redeemed %d times", redemptions)
	}
	if len(b.pages) != 2 || !strings.Contains(b.pages[0], "Sign-in finished") || !strings.Contains(b.pages[1], "already been answered") {
		t.Fatalf("pages: %q", b.pages)
	}
	for _, p := range b.pages {
		if strings.Contains(p, "code-1") || strings.Contains(p, q.Get("state")) {
			t.Fatal("the loopback page echoed request data")
		}
	}
	if !s.SignedIn() || s.Account() != "tech@contoso.example" {
		t.Fatalf("signed in %v as %q", s.SignedIn(), s.Account())
	}
	tok, err := s.Token(context.Background())
	if err != nil || tok != "at-1" {
		t.Fatalf("Token = %q, %v", tok, err)
	}
}

func TestSignInStateMismatch(t *testing.T) {
	idp := newFakeIdP(t)
	idp.wrongState = true
	lb := &loopbackOnly{}
	s := newTestSession(t, idp, &browser{lb: lb}, Settings{})
	err := s.SignIn(context.Background())
	if err == nil || !strings.Contains(err.Error(), "state mismatch") {
		t.Fatalf("err = %v", err)
	}
	if _, _, redemptions, _ := idp.snapshot(); redemptions != 0 || s.SignedIn() {
		t.Fatal("a code was redeemed after a state mismatch")
	}
	lb.check(t)
}

func TestSignInIdPError(t *testing.T) {
	idp := newFakeIdP(t)
	idp.idpError = "access_denied"
	lb := &loopbackOnly{}
	s := newTestSession(t, idp, &browser{lb: lb}, Settings{})
	err := s.SignIn(context.Background())
	if err == nil || !strings.Contains(err.Error(), "access_denied: AADSTS50105") || strings.Contains(err.Error(), "Trace") {
		t.Fatalf("err = %v", err)
	}
}

func TestSignInCancelAndTimeout(t *testing.T) {
	idp := newFakeIdP(t)
	lb := &loopbackOnly{}
	s := newTestSession(t, idp, &browser{lb: lb}, Settings{})
	s.openBrowser = func(string) error { return nil } // the user never finishes
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(50 * time.Millisecond); cancel() }()
	if err := s.SignIn(ctx); err == nil || !strings.Contains(err.Error(), "cancelled") {
		t.Fatalf("cancel: %v", err)
	}
	s.timeout = 50 * time.Millisecond
	if err := s.SignIn(context.Background()); err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("timeout: %v", err)
	}
}

func TestRefreshBeforeExpiryAndSignOut(t *testing.T) {
	idp := newFakeIdP(t)
	lb := &loopbackOnly{}
	s := newTestSession(t, idp, &browser{lb: lb}, Settings{})
	base := time.Now()
	var mu sync.Mutex
	now := base
	s.now = func() time.Time { mu.Lock(); defer mu.Unlock(); return now }
	if err := s.SignIn(context.Background()); err != nil {
		t.Fatal(err)
	}
	set := func(d time.Duration) { mu.Lock(); now = base.Add(d); mu.Unlock() }

	set(30 * time.Minute)
	if tok, _ := s.Token(context.Background()); tok != "at-1" {
		t.Fatalf("refreshed too early: %q", tok)
	}
	set(56 * time.Minute) // within five minutes of the one-hour expiry
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if tok, err := s.Token(context.Background()); err != nil || tok != "at-2" {
				t.Errorf("Token = %q, %v", tok, err)
			}
		}()
	}
	wg.Wait()
	if _, _, _, n := idp.snapshot(); n != 1 {
		t.Fatalf("refreshes = %d, want 1", n)
	}
	// Renew after a 401 refreshes once; a stale rejected token does not.
	if tok, _ := s.Renew(context.Background(), "at-2"); tok != "at-3" {
		t.Fatalf("Renew = %q", tok)
	}
	if tok, _ := s.Renew(context.Background(), "at-2"); tok != "at-3" {
		t.Fatalf("stale Renew: %q", tok)
	}
	if _, _, _, n := idp.snapshot(); n != 2 {
		t.Fatalf("stale Renew refreshed again: %d", n)
	}

	s.SignOut()
	if s.SignedIn() || s.Account() != "" || s.access != nil || s.refresh != nil {
		t.Fatal("tokens remain after SignOut")
	}
	if _, err := s.Token(context.Background()); !errors.Is(err, ErrNotSignedIn) {
		t.Fatalf("Token after SignOut: %v", err)
	}
	lb.check(t)
}

func TestRefreshRejectedEndsSession(t *testing.T) {
	idp := newFakeIdP(t)
	lb := &loopbackOnly{}
	s := newTestSession(t, idp, &browser{lb: lb}, Settings{})
	if err := s.SignIn(context.Background()); err != nil {
		t.Fatal(err)
	}
	idp.mu.Lock()
	idp.refreshDead = true
	idp.mu.Unlock()
	if _, err := s.Renew(context.Background(), "at-1"); !errors.Is(err, ErrNotSignedIn) {
		t.Fatalf("err = %v", err)
	}
	if s.SignedIn() {
		t.Fatal("still signed in after invalid_grant")
	}
}

func TestScopesPerCloud(t *testing.T) {
	cases := []struct {
		cloud Cloud
		host  string
		login string
	}{
		{CloudCommercial, "https://graph.microsoft.com", "https://login.microsoftonline.com"},
		{CloudGCCHigh, "https://graph.microsoft.us", "https://login.microsoftonline.us"},
		{CloudDoD, "https://dod-graph.microsoft.us", "https://login.microsoftonline.us"},
	}
	for _, c := range cases {
		for _, sync := range []bool{false, true} {
			sc, err := Scopes(Settings{Cloud: c.cloud, CloudSync: sync})
			if err != nil {
				t.Fatal(err)
			}
			joined := " " + strings.Join(sc, " ") + " "
			for _, p := range graphScopes {
				if !strings.Contains(joined, " "+c.host+"/"+p+" ") {
					t.Errorf("%s: missing %s/%s in %v", c.cloud, c.host, p, sc)
				}
			}
			for _, p := range []string{"Application.Read.All", "Synchronization.Read.All", "Synchronization.ReadWrite.All"} {
				if strings.Contains(joined, " "+c.host+"/"+p+" ") != sync {
					t.Errorf("%s CloudSync=%v: %s in %v", c.cloud, sync, p, sc)
				}
			}
			for _, o := range []string{" openid ", " profile ", " offline_access "} {
				if !strings.Contains(joined, o) {
					t.Errorf("%s: missing %s", c.cloud, o)
				}
			}
			for _, x := range sc {
				if strings.Contains(x, "/") && !strings.HasPrefix(x, c.host+"/") {
					t.Errorf("%s: scope on another host: %s", c.cloud, x)
				}
			}
		}
		s, err := NewSession(Settings{TenantID: "t1", ClientID: "c1", Cloud: c.cloud}, SessionOptions{OpenBrowser: func(string) error { return nil }})
		if err != nil {
			t.Fatal(err)
		}
		if s.authorizeURL != c.login+"/t1/oauth2/v2.0/authorize" || s.tokenURL != c.login+"/t1/oauth2/v2.0/token" {
			t.Errorf("%s endpoints: %s %s", c.cloud, s.authorizeURL, s.tokenURL)
		}
	}
}

func TestLoopbackRefusesOtherHosts(t *testing.T) {
	l := &loopback{state: "s", port: 5555, done: make(chan callback, 1)}
	for _, host := range []string{"evil.example:5555", "localhost:1", "127.0.0.1"} {
		r := httptest.NewRequest("GET", "/?code=c&state=s", nil)
		r.Host = host
		w := httptest.NewRecorder()
		l.ServeHTTP(w, r)
		if w.Code != http.StatusNotFound {
			t.Errorf("host %s: %d", host, w.Code)
		}
	}
	select {
	case <-l.done:
		t.Fatal("a request with a foreign Host was taken as the answer")
	default:
	}
}

func TestBrowserURLCheck(t *testing.T) {
	for _, bad := range []string{"file:///C:/Windows/notepad.exe", "http://login.microsoftonline.com/x", "calc", "https://user@host/"} {
		if checkBrowserURL(bad) == nil {
			t.Errorf("accepted %q", bad)
		}
	}
	if checkBrowserURL("https://login.microsoftonline.us/t/oauth2/v2.0/authorize?x=1") != nil {
		t.Error("refused an https sign-in URL")
	}
}
