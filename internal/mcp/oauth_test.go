package mcp

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeAuth is an authorization server and a resource it guards, in one:
// it publishes the resource's metadata and its own, registers clients,
// signs in at once whoever comes to its authorization endpoint (the
// person's part), holds the code to PKCE and the redirect address, and
// gives tokens and refreshes them.
type fakeAuth struct {
	t   *testing.T
	srv *httptest.Server

	mu      sync.Mutex
	clients map[string][]string // id: redirect addresses
	codes   map[string]grant
	access  map[string]bool
	refresh map[string]bool
	n       int
	// renewRefresh says a refresh gives a new refresh token too.
	renewRefresh bool
	// tokenEndpoint, when set, is the token endpoint the metadata names.
	tokenEndpoint string
}

type grant struct {
	client, redirect, challenge, resource string
}

func newFakeAuth(t *testing.T) *fakeAuth {
	fa := &fakeAuth{t: t, clients: map[string][]string{}, codes: map[string]grant{}, access: map[string]bool{}, refresh: map[string]bool{}}
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/oauth-protected-resource/mcp", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{"resource": fa.srv.URL + "/mcp", "authorization_servers": []string{fa.srv.URL + "/as"}, "scopes_supported": []string{"read", "write"}})
	})
	mux.HandleFunc("/.well-known/oauth-authorization-server/as", func(w http.ResponseWriter, r *http.Request) {
		token := fa.tokenEndpoint
		if token == "" {
			token = fa.srv.URL + "/as/token"
		}
		writeJSON(w, AuthServer{Issuer: fa.srv.URL + "/as", AuthorizationEndpoint: fa.srv.URL + "/as/authorize", TokenEndpoint: token,
			RegistrationEndpoint: fa.srv.URL + "/as/register", CodeChallengeMethods: []string{"S256"}})
	})
	mux.HandleFunc("/as/register", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			RedirectURIs []string `json:"redirect_uris"`
			AuthMethod   string   `json:"token_endpoint_auth_method"`
		}
		if r.Method != http.MethodPost || json.NewDecoder(r.Body).Decode(&in) != nil || len(in.RedirectURIs) == 0 || in.AuthMethod != "none" {
			http.Error(w, `{"error":"invalid_client_metadata"}`, http.StatusBadRequest)
			return
		}
		fa.mu.Lock()
		fa.n++
		id := fmt.Sprintf("client-%d", fa.n)
		fa.clients[id] = in.RedirectURIs
		fa.mu.Unlock()
		w.WriteHeader(http.StatusCreated)
		writeJSON(w, map[string]any{"client_id": id})
	})
	mux.HandleFunc("/as/authorize", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		fa.mu.Lock()
		redirects := fa.clients[q.Get("client_id")]
		fa.mu.Unlock()
		ok := false
		for _, ru := range redirects {
			ok = ok || ru == q.Get("redirect_uri")
		}
		if !ok || q.Get("response_type") != "code" || q.Get("code_challenge_method") != "S256" || q.Get("code_challenge") == "" {
			http.Error(w, "bad authorization request", http.StatusBadRequest)
			return
		}
		fa.mu.Lock()
		fa.n++
		code := fmt.Sprintf("code-%d", fa.n)
		fa.codes[code] = grant{client: q.Get("client_id"), redirect: q.Get("redirect_uri"), challenge: q.Get("code_challenge"), resource: q.Get("resource")}
		fa.mu.Unlock()
		back := url.Values{"code": {code}, "state": {q.Get("state")}, "iss": {fa.srv.URL + "/as"}}
		http.Redirect(w, r, q.Get("redirect_uri")+"?"+back.Encode(), http.StatusFound)
	})
	mux.HandleFunc("/as/token", func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		fa.mu.Lock()
		defer fa.mu.Unlock()
		refuse := func(why string) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			io.WriteString(w, `{"error":"invalid_grant","error_description":"`+why+`"}`)
		}
		fa.n++
		switch r.Form.Get("grant_type") {
		case "authorization_code":
			g, ok := fa.codes[r.Form.Get("code")]
			delete(fa.codes, r.Form.Get("code"))
			sum := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
			switch {
			case !ok:
				refuse("no such code")
				return
			case base64.RawURLEncoding.EncodeToString(sum[:]) != g.challenge:
				refuse("the verifier does not match the challenge")
				return
			case r.Form.Get("redirect_uri") != g.redirect || r.Form.Get("client_id") != g.client:
				refuse("another client or redirect address")
				return
			case r.Form.Get("resource") != g.resource:
				refuse("another resource")
				return
			}
			access, refresh := fmt.Sprintf("access-%d", fa.n), fmt.Sprintf("refresh-%d", fa.n)
			fa.access[access], fa.refresh[refresh] = true, true
			writeJSON(w, map[string]any{"access_token": access, "token_type": "Bearer", "expires_in": 3600, "refresh_token": refresh, "scope": "read"})
		case "refresh_token":
			if !fa.refresh[r.Form.Get("refresh_token")] {
				refuse("no such refresh token")
				return
			}
			access := fmt.Sprintf("access-%d", fa.n)
			fa.access[access] = true
			out := map[string]any{"access_token": access, "token_type": "bearer", "expires_in": 60}
			if fa.renewRefresh {
				delete(fa.refresh, r.Form.Get("refresh_token"))
				refresh := fmt.Sprintf("refresh-%d", fa.n)
				fa.refresh[refresh] = true
				out["refresh_token"] = refresh
			}
			writeJSON(w, out)
		default:
			refuse("unknown grant")
		}
	})
	mux.HandleFunc("/mcp", func(w http.ResponseWriter, r *http.Request) {
		token, _ := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		fa.mu.Lock()
		ok := fa.access[token]
		fa.mu.Unlock()
		if !ok {
			w.Header().Set("WWW-Authenticate", `Bearer resource_metadata="`+fa.srv.URL+`/.well-known/oauth-protected-resource/mcp", scope="read"`)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		writeJSON(w, map[string]any{"ok": true})
	})
	fa.srv = httptest.NewServer(mux)
	t.Cleanup(fa.srv.Close)
	return fa
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

// A whole sign-in: the server's 401 names its metadata, which names its
// authorization server; gnosh registers, sends the browser there with
// PKCE, takes the code the browser brings back to the listener, and trades
// it for a token the server takes; the token is refreshed.
func TestSignIn(t *testing.T) {
	fa := newFakeAuth(t)
	ctx := context.Background()
	hc := fa.srv.Client()
	resp, err := hc.Get(fa.srv.URL + "/mcp")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("the resource answered %s without a token", resp.Status)
	}
	as, scopes, err := Discover(ctx, hc, fa.srv.URL+"/mcp", resp.Header.Get("WWW-Authenticate"), OAuth{})
	if err != nil || as.Issuer != fa.srv.URL+"/as" || scopes != "read" {
		t.Fatalf("Discover: %+v, %q, %v", as, scopes, err)
	}
	var f *Flow
	redirect, back, stop, err := Listen(0, func() string {
		if f == nil {
			return ""
		}
		return f.State()
	})
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	if !strings.HasPrefix(redirect, "http://localhost:") || !strings.HasSuffix(redirect, "/callback") {
		t.Errorf("the redirect address is %s", redirect)
	}
	id, secret, err := Register(ctx, hc, as, redirect)
	if err != nil || id == "" || secret != "" {
		t.Fatalf("Register: %q, %q, %v", id, secret, err)
	}
	f = NewFlow(as, id, "", redirect, scopes, fa.srv.URL+"/mcp")
	u, _ := url.Parse(f.URL())
	if q := u.Query(); q.Get("code_challenge_method") != "S256" || q.Get("resource") != fa.srv.URL+"/mcp" || q.Get("scope") != "read" || q.Get("state") == "" {
		t.Errorf("the sign-in address: %s", f.URL())
	}
	// The browser: signed in at once, and sent back to the listener.
	page, err := http.Get(f.URL())
	if err != nil {
		t.Fatalf("the browser could not follow the sign-in back: %v", err)
	}
	body, _ := io.ReadAll(page.Body)
	page.Body.Close()
	if !strings.Contains(string(body), "Signed in") {
		t.Errorf("the page the browser came back to says %q", body)
	}
	var vals url.Values
	select {
	case vals = <-back:
	case <-time.After(5 * time.Second):
		t.Fatal("the listener never got the browser's answer")
	}
	tok, err := f.Finish(ctx, hc, vals)
	if err != nil || tok.AccessToken == "" || tok.RefreshToken == "" || tok.Expiry.IsZero() {
		t.Fatalf("Finish: %+v, %v", tok, err)
	}
	req, _ := http.NewRequest(http.MethodGet, fa.srv.URL+"/mcp", nil)
	req.Header.Set("Authorization", "Bearer "+tok.AccessToken)
	if resp, err := hc.Do(req); err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("the resource refused the token: %v %v", resp, err)
	}
	// The same code twice is refused.
	if _, err := f.Finish(ctx, hc, vals); err == nil {
		t.Errorf("a code was taken twice")
	}
	// A refresh keeps the refresh token when the server gives none.
	l := Login{Server: fa.srv.URL + "/mcp", Issuer: as.Issuer, TokenEndpoint: as.TokenEndpoint, ClientID: id, Token: tok}
	t2, err := Refresh(ctx, hc, l)
	if err != nil || t2.AccessToken == tok.AccessToken || t2.RefreshToken != tok.RefreshToken {
		t.Fatalf("Refresh: %+v, %v", t2, err)
	}
	fa.renewRefresh = true
	t3, err := Refresh(ctx, hc, l)
	if err != nil || t3.RefreshToken == tok.RefreshToken {
		t.Fatalf("a refresh that gives a new refresh token: %+v, %v", t3, err)
	}
	if _, err := Refresh(ctx, hc, l); err == nil {
		t.Errorf("a refresh token that was replaced was taken again")
	}
}

// The browser's answer is held to the sign-in it belongs to: its state,
// its issuer, and an error it brings is said.
func TestFinishChecks(t *testing.T) {
	as := AuthServer{Issuer: "https://auth.example.com", AuthorizationEndpoint: "https://auth.example.com/authorize", TokenEndpoint: "https://auth.example.com/token"}
	f := NewFlow(as, "c", "", "http://localhost:1/callback", "", "")
	ctx := context.Background()
	for _, c := range []struct {
		back url.Values
		want string
	}{
		{url.Values{"code": {"x"}, "state": {"another"}}, "state differs"},
		{url.Values{"code": {"x"}, "state": {f.state}, "iss": {"https://evil.example.com"}}, "issuer https://evil.example.com"},
		{url.Values{"error": {"access_denied"}, "error_description": {"the person said no\x1b[2J"}, "state": {f.state}}, "access_denied: the person said no?[2J"},
		{url.Values{"error": {"access_denied"}}, "state differs"},
		{url.Values{"state": {f.state}}, "holds no code"},
	} {
		if _, err := f.Finish(ctx, nil, c.back); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("Finish(%v) = %v, want an error about %q", c.back, err, c.want)
		}
	}
}

// Discovery falls back to the server's own origin, and to OpenID
// Connect's place for the metadata; it refuses an authorization server
// whose token endpoint would take credentials over plain http elsewhere,
// or whose issuer is not the one it was found by.
func TestDiscoverFallbacksAndChecks(t *testing.T) {
	var srvURL string
	issuer, token := "", ""
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, AuthServer{Issuer: firstNonEmpty(issuer, srvURL), AuthorizationEndpoint: srvURL + "/authorize", TokenEndpoint: firstNonEmpty(token, srvURL+"/token")})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	srvURL = srv.URL
	ctx := context.Background()
	as, _, err := Discover(ctx, srv.Client(), srv.URL+"/mcp", "", OAuth{})
	if err != nil || as.TokenEndpoint != srv.URL+"/token" {
		t.Fatalf("without resource metadata: %+v, %v", as, err)
	}
	token = "http://auth.example.com/token"
	if _, _, err := Discover(ctx, srv.Client(), srv.URL+"/mcp", "", OAuth{}); err == nil || !strings.Contains(err.Error(), "not served over https") {
		t.Errorf("a token endpoint over plain http elsewhere: %v", err)
	}
	token, issuer = "", "https://other.example.com"
	if _, _, err := Discover(ctx, srv.Client(), srv.URL+"/mcp", "", OAuth{}); err == nil || !strings.Contains(err.Error(), "issuer") {
		t.Errorf("metadata naming another issuer: %v", err)
	}
	if _, _, err := Discover(ctx, srv.Client(), srv.URL+"/mcp", "", OAuth{AuthServerMetadataURL: "http://auth.example.com/meta"}); err == nil || !strings.Contains(err.Error(), "https") {
		t.Errorf("an authServerMetadataUrl over plain http: %v", err)
	}
}

// A Bearer challenge's parameters, quoted or not.
func TestAuthParams(t *testing.T) {
	got := authParams(`Bearer resource_metadata="https://x.example/.well-known/oauth-protected-resource", scope="read write", error=invalid_token`)
	want := map[string]string{"resource_metadata": "https://x.example/.well-known/oauth-protected-resource", "scope": "read write", "error": "invalid_token"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("authParams = %v, want %v", got, want)
	}
	if got := authParams(`Basic realm="x"`); len(got) != 0 {
		t.Errorf("a Basic challenge: %v", got)
	}
	if got := wellKnown("https://h.example/mcp/", "oauth-protected-resource", true); !reflect.DeepEqual(got, []string{"https://h.example/.well-known/oauth-protected-resource/mcp", "https://h.example/.well-known/oauth-protected-resource"}) {
		t.Errorf("wellKnown = %v", got)
	}
}

// What the person pastes: the whole address, or its part after the "?".
func TestCallback(t *testing.T) {
	for _, in := range []string{"http://localhost:8080/callback?code=abc&state=xyz", " code=abc&state=xyz#frag "} {
		v, err := Callback(in)
		if err != nil || v.Get("code") != "abc" || v.Get("state") != "xyz" {
			t.Errorf("Callback(%q) = %v, %v", in, v, err)
		}
	}
	if _, err := Callback("https://example.com/somewhere"); err == nil {
		t.Errorf("an address with no code was taken")
	}
}

// The sign-ins are kept by server, in a file only its owner reads.
func TestLoginStore(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state", "mcp-logins.json")
	s := &LoginStore{Path: path}
	if _, ok := s.Get("https://a.example/mcp"); ok {
		t.Fatal("a sign-in in a store not written yet")
	}
	l := Login{Server: "https://a.example/mcp", ClientID: "c", TokenEndpoint: "https://a.example/token", Token: Token{AccessToken: "t1", RefreshToken: "r1"}}
	if err := s.Put(l); err != nil {
		t.Fatal(err)
	}
	if err := s.Put(Login{Server: "https://b.example/mcp", Token: Token{AccessToken: "t2"}}); err != nil {
		t.Fatal(err)
	}
	if got, ok := s.Get("https://a.example/mcp"); !ok || got.AccessToken != "t1" || got.RefreshToken != "r1" {
		t.Errorf("Get = %+v, %v", got, ok)
	}
	if st, err := os.Stat(path); err != nil || st.Mode().Perm() != 0o600 {
		t.Errorf("the file's mode: %v %v", st.Mode(), err)
	}
	if ok, err := s.Delete("https://a.example/mcp"); !ok || err != nil {
		t.Errorf("Delete: %v %v", ok, err)
	}
	if _, ok := s.Get("https://a.example/mcp"); ok {
		t.Errorf("a deleted sign-in is still there")
	}
	if _, ok := s.Get("https://b.example/mcp"); !ok {
		t.Errorf("the other sign-in went with it")
	}
}

// A server that wants a sign-in: without one, the handshake comes to
// NeedsAuth, with what the server said; with one kept, its token goes with
// every request; a token the server refuses is refreshed once and kept,
// and one that cannot be refreshed is forgotten; a configured
// Authorization header is never replaced.
func TestHTTPSignIn(t *testing.T) {
	fa := newFakeAuth(t)
	far := &fakeHTTP{}
	guard := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token, _ := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		fa.mu.Lock()
		ok := fa.access[token]
		fa.mu.Unlock()
		if !ok {
			w.Header().Set("WWW-Authenticate", `Bearer resource_metadata="https://far.example/.well-known/oauth-protected-resource"`)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		far.ServeHTTP(w, r)
	}))
	defer guard.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	store := &LoginStore{Path: filepath.Join(t.TempDir(), "logins.json")}
	server := guard.URL + "/mcp"
	auth := &Authorizer{Logins: store, Server: server, HTTP: fa.srv.Client()}

	_, err := StartHTTP(ctx, "far", server, nil, Options{Auth: auth})
	var na *NeedsAuth
	if !errors.As(err, &na) || na.Status != http.StatusUnauthorized || !strings.Contains(na.WWWAuthenticate, "resource_metadata") || !errors.Is(err, ErrNeedsAuth) {
		t.Fatalf("without a sign-in: %v", err)
	}

	fa.mu.Lock()
	fa.refresh["r1"] = true
	fa.mu.Unlock()
	if err := store.Put(Login{Server: server, TokenEndpoint: fa.srv.URL + "/as/token", ClientID: "c", Token: Token{AccessToken: "stale", RefreshToken: "r1"}}); err != nil {
		t.Fatal(err)
	}
	c, err := StartHTTP(ctx, "far", server, nil, Options{Auth: auth})
	if err != nil {
		t.Fatalf("with a sign-in whose token is refused and refreshed: %v", err)
	}
	if _, err := c.ListTools(ctx); err != nil {
		t.Errorf("a call after the refresh: %v", err)
	}
	c.Close()
	if l, ok := store.Get(server); !ok || l.AccessToken == "stale" || l.RefreshToken != "r1" {
		t.Errorf("the sign-in after a refresh: %+v, %v", l, ok)
	}

	if err := store.Put(Login{Server: server, TokenEndpoint: fa.srv.URL + "/as/token", ClientID: "c", Token: Token{AccessToken: "stale", RefreshToken: "refused"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := StartHTTP(ctx, "far", server, nil, Options{Auth: auth}); !errors.Is(err, ErrNeedsAuth) {
		t.Errorf("a refresh token refused: %v, want a sign-in needed", err)
	}
	if l, ok := store.Get(server); ok {
		t.Errorf("a sign-in that could not be renewed is kept: %+v", l)
	}

	fa.mu.Lock()
	fa.access["configured"] = true
	fa.mu.Unlock()
	if err := store.Put(Login{Server: server, ClientID: "c", Token: Token{AccessToken: "signed-in"}}); err != nil {
		t.Fatal(err)
	}
	c, err = StartHTTP(ctx, "far", server, map[string]string{"authorization": "Bearer configured"}, Options{Auth: auth})
	if err != nil {
		t.Fatalf("with an Authorization header configured: %v", err)
	}
	c.Close()
	far.mu.Lock()
	last := far.seen[len(far.seen)-1].Get("Authorization")
	far.mu.Unlock()
	if last != "Bearer configured" {
		t.Errorf("the configured Authorization header was replaced: %q", last)
	}
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// Only https, or this machine, carries a sign-in: discovery, the token
// endpoint and the token itself.
func TestSecure(t *testing.T) {
	for addr, want := range map[string]bool{
		"https://auth.example.com/token": true, "http://127.0.0.1:8080/x": true, "http://localhost/x": true, "http://[::1]:9/x": true,
		"http://auth.example.com/token": false, "ftp://example.com": false, "https:///nohost": false,
	} {
		if Secure(addr) != want {
			t.Errorf("Secure(%s) = %v, want %v", addr, !want, want)
		}
	}
	if _, _, err := Discover(context.Background(), nil, "http://far.example.com/mcp", "", OAuth{}); err == nil || !strings.Contains(err.Error(), "plain http") {
		t.Errorf("a server over plain http: %v", err)
	}
	a := &Authorizer{Logins: &LoginStore{Path: filepath.Join(t.TempDir(), "l.json")}, Server: "http://far.example.com/mcp"}
	_ = a.Logins.Put(Login{Server: a.Server, Token: Token{AccessToken: "t"}})
	if got := a.token(); got != "" {
		t.Errorf("a token was given to a server over plain http: %q", got)
	}
}

// A token request follows no redirect: a 307 would carry the code or the
// refresh token on to an address nobody checked.
func TestTokenRequestFollowsNoRedirect(t *testing.T) {
	hit := false
	elsewhere := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hit = true }))
	defer elsewhere.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, elsewhere.URL+"/token", http.StatusTemporaryRedirect)
	}))
	defer srv.Close()
	_, err := Refresh(context.Background(), srv.Client(), Login{Server: srv.URL + "/mcp", TokenEndpoint: srv.URL + "/token", ClientID: "c", Token: Token{RefreshToken: "r"}})
	if err == nil || hit {
		t.Errorf("a token request that was redirected: %v, the other address reached: %v", err, hit)
	}
}

// The redirect address takes only the answer that carries the sign-in's
// state; anything else is turned away and ends nothing.
func TestListenChecksTheState(t *testing.T) {
	redirect, back, stop, err := Listen(0, func() string { return "the-state" })
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	for _, q := range []string{"error=x", "code=c&state=other", "state=the-state"} {
		resp, err := http.Get(redirect + "?" + q)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("?%s was taken: %s", q, resp.Status)
		}
	}
	select {
	case v := <-back:
		t.Fatalf("an answer that was turned away came through: %v", v)
	default:
	}
	resp, err := http.Get(redirect + "?code=c&state=the-state")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	select {
	case v := <-back:
		if v.Get("code") != "c" {
			t.Errorf("the answer taken: %v", v)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the right answer did not come through")
	}
}

// A refresh that fails on the way keeps the sign-in; only invalid_grant
// forgets it. A 403 renews and forgets nothing. Two processes renewing the
// same refused token at once refresh it once between them.
func TestRenew(t *testing.T) {
	var mu sync.Mutex
	status, refreshes := http.StatusInternalServerError, 0
	tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		refreshes++
		switch status {
		case http.StatusOK:
			time.Sleep(50 * time.Millisecond)
			writeJSON(w, map[string]any{"access_token": fmt.Sprintf("fresh-%d", refreshes), "token_type": "Bearer"})
		case http.StatusBadRequest:
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			io.WriteString(w, `{"error":"invalid_grant"}`)
		default:
			w.WriteHeader(status)
		}
	}))
	defer tokenSrv.Close()
	path := filepath.Join(t.TempDir(), "logins.json")
	server := "https://far.example.com/mcp"
	put := func() {
		t.Helper()
		if err := (&LoginStore{Path: path}).Put(Login{Server: server, TokenEndpoint: tokenSrv.URL + "/token", ClientID: "c", Token: Token{AccessToken: "old", RefreshToken: "r"}}); err != nil {
			t.Fatal(err)
		}
	}
	put()
	a := &Authorizer{Logins: &LoginStore{Path: path}, Server: server, HTTP: tokenSrv.Client()}
	if a.renew(context.Background(), "old") {
		t.Errorf("a failed refresh gave a token to try")
	}
	if l, ok := a.Logins.Get(server); !ok || l.AccessToken != "old" || l.RefreshToken != "r" {
		t.Errorf("a refresh that failed on the way forgot the sign-in: %+v %v", l, ok)
	}
	mu.Lock()
	status = http.StatusBadRequest
	mu.Unlock()
	a.renew(context.Background(), "old")
	if _, ok := a.Logins.Get(server); ok {
		t.Errorf("invalid_grant kept the sign-in")
	}

	put()
	mu.Lock()
	status, refreshes = http.StatusOK, 0
	mu.Unlock()
	var wg sync.WaitGroup
	for range 2 {
		b := &Authorizer{Logins: &LoginStore{Path: path}, Server: server, HTTP: tokenSrv.Client()}
		wg.Go(func() {
			if !b.renew(context.Background(), "old") {
				t.Errorf("a renewal found nothing to try")
			}
		})
	}
	wg.Wait()
	if refreshes != 1 {
		t.Errorf("two processes refreshed the same token %d times, want once", refreshes)
	}
}

// A file of sign-ins that cannot be read is set aside, and the store
// starts empty.
func TestLoginStoreSetsADamagedFileAside(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "logins.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	s := &LoginStore{Path: path}
	if err := s.Put(Login{Server: "https://a.example/mcp", Token: Token{AccessToken: "t"}}); err != nil {
		t.Fatalf("a damaged file stopped a sign-in: %v", err)
	}
	if _, ok := s.Get("https://a.example/mcp"); !ok {
		t.Errorf("the sign-in was not kept")
	}
	aside, _ := filepath.Glob(path + ".unreadable-*")
	if len(aside) != 1 {
		t.Errorf("the damaged file was not set aside: %v", aside)
	}
}

// A 403 is a sign-in that is not enough (a scope missing, a firewall): the
// server wants a sign-in, and nothing is renewed or forgotten.
func TestForbiddenRenewsNothing(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("WWW-Authenticate", `Bearer error="insufficient_scope", scope="admin"`)
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()
	store := &LoginStore{Path: filepath.Join(t.TempDir(), "logins.json")}
	_ = store.Put(Login{Server: srv.URL + "/mcp", TokenEndpoint: srv.URL + "/token", ClientID: "c", Token: Token{AccessToken: "kept", RefreshToken: "r"}})
	_, err := StartHTTP(context.Background(), "far", srv.URL+"/mcp", nil, Options{Auth: &Authorizer{Logins: store, Server: srv.URL + "/mcp", HTTP: srv.Client()}})
	var na *NeedsAuth
	if !errors.As(err, &na) || na.Status != http.StatusForbidden || !strings.Contains(na.WWWAuthenticate, "insufficient_scope") {
		t.Errorf("a 403: %v", err)
	}
	if l, ok := store.Get(srv.URL + "/mcp"); !ok || l.AccessToken != "kept" {
		t.Errorf("a 403 forgot the sign-in: %+v %v", l, ok)
	}
}
