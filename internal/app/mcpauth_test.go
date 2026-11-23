package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gnosh-ai/gnosh/internal/app/apptest"
)

// signInServer is an MCP server reached at a URL that wants a sign-in, with
// its authorization server: it publishes the metadata, registers clients,
// signs in at once whoever comes to its authorization endpoint, and gives
// tokens; its MCP endpoint takes only those.
type signInServer struct {
	srv    *httptest.Server
	mu     sync.Mutex
	n      int
	codes  map[string]string // code: redirect address
	tokens map[string]bool
}

func newSignInServer(t *testing.T) *signInServer {
	ss := &signInServer{codes: map[string]string{}, tokens: map[string]bool{}}
	mux := http.NewServeMux()
	jsonOut := func(w http.ResponseWriter, v any) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(v)
	}
	mux.HandleFunc("/.well-known/oauth-protected-resource/mcp", func(w http.ResponseWriter, r *http.Request) {
		jsonOut(w, map[string]any{"resource": ss.srv.URL + "/mcp", "authorization_servers": []string{ss.srv.URL}})
	})
	mux.HandleFunc("/.well-known/oauth-authorization-server", func(w http.ResponseWriter, r *http.Request) {
		jsonOut(w, map[string]any{"issuer": ss.srv.URL, "authorization_endpoint": ss.srv.URL + "/authorize", "token_endpoint": ss.srv.URL + "/token",
			"registration_endpoint": ss.srv.URL + "/register", "code_challenge_methods_supported": []string{"S256"}})
	})
	mux.HandleFunc("/register", func(w http.ResponseWriter, r *http.Request) {
		jsonOut(w, map[string]any{"client_id": "gnosh-client"})
	})
	mux.HandleFunc("/authorize", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		ss.mu.Lock()
		ss.n++
		code := fmt.Sprintf("code-%d", ss.n)
		ss.codes[code] = q.Get("redirect_uri")
		ss.mu.Unlock()
		http.Redirect(w, r, q.Get("redirect_uri")+"?"+url.Values{"code": {code}, "state": {q.Get("state")}}.Encode(), http.StatusFound)
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		ss.mu.Lock()
		defer ss.mu.Unlock()
		if _, ok := ss.codes[r.Form.Get("code")]; !ok || r.Form.Get("code_verifier") == "" {
			http.Error(w, `{"error":"invalid_grant"}`, http.StatusBadRequest)
			return
		}
		delete(ss.codes, r.Form.Get("code"))
		ss.n++
		token := fmt.Sprintf("token-%d", ss.n)
		ss.tokens[token] = true
		jsonOut(w, map[string]any{"access_token": token, "token_type": "Bearer", "expires_in": 3600})
	})
	mux.HandleFunc("/mcp", func(w http.ResponseWriter, r *http.Request) {
		token, _ := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		ss.mu.Lock()
		ok := ss.tokens[token]
		ss.mu.Unlock()
		if !ok {
			w.Header().Set("WWW-Authenticate", `Bearer resource_metadata="`+ss.srv.URL+`/.well-known/oauth-protected-resource/mcp"`)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		body, _ := io.ReadAll(r.Body)
		var m struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		json.Unmarshal(body, &m)
		w.Header().Set("Content-Type", "application/json")
		switch m.Method {
		case "initialize":
			fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%s,"result":{"protocolVersion":"2025-06-18","serverInfo":{"name":"far","version":"1"},"capabilities":{"tools":{}}}}`, m.ID)
		case "tools/list":
			fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%s,"result":{"tools":[{"name":"look","description":"Looks far.","inputSchema":{"type":"object"}}]}}`, m.ID)
		default:
			w.WriteHeader(http.StatusAccepted)
		}
	})
	ss.srv = httptest.NewServer(mux)
	t.Cleanup(ss.srv.Close)
	return ss
}

// signInAddress is the address /mcp login said to sign in at, the last
// time it said one.
var signInAddress = regexp.MustCompile(`(http://127\.0\.0\.1:\d+/authorize\?\S+)`)

// A server at a URL that wants a sign-in is said to; /mcp login says
// where to sign in, and when the browser comes back to this machine the
// sign-in ends by itself and the server is connected, its tools offered;
// /mcp logout forgets it; and over ssh, the address the browser tried to
// go to, pasted after /mcp login, ends the sign-in the same.
func TestMCPSignIn(t *testing.T) {
	ss := newSignInServer(t)
	w := apptest.NewWorld(t)
	w.Model.Respond(func(int, apptest.Request) *apptest.Reply { return &apptest.Reply{Text: "Answered after the sign-in."} })
	w.Write(w.ConfigDir()+"/mcp.json", `{"mcpServers": {"far": {"type": "http", "url": "`+ss.srv.URL+`/mcp"}}}`)
	s := sitWith(t, w, nil)
	s.see("The MCP server far wants you to sign in: /mcp login far starts it.")
	s.typeLine("/mcp login far")
	s.see("Sign in to far at this address:")
	s.see("/mcp login far <that address>")
	m := signInAddress.FindAllStringSubmatch(s.out.String(), -1)
	if len(m) == 0 {
		t.Fatalf("no address to sign in at:\n%s", s.out.String())
	}
	// The browser, on this machine: signed in at once and sent back.
	resp, err := http.Get(m[len(m)-1][1])
	if err != nil {
		t.Fatalf("the browser could not come back: %v", err)
	}
	resp.Body.Close()
	s.see("Signed in to far. far: connected")
	s.typeLine("hi")
	s.see("Answered after the sign-in.")
	if body := string(w.Model.Requests()[0].Body); !strings.Contains(body, "mcp__far__look") {
		t.Errorf("the server's tool was not offered after the sign-in")
	}
	kept := w.Read(w.DataDir() + "/mcp-logins.json")
	if !strings.Contains(kept, ss.srv.URL+"/mcp") || !strings.Contains(kept, "token-") {
		t.Errorf("the sign-in was not kept: %s", kept)
	}

	s.typeLine("/mcp logout far")
	s.see("The sign-in to far is forgotten")
	s.see("far: wants a sign-in")

	// Over ssh: the browser cannot come back, and the address it tried to
	// go to is pasted.
	s.typeLine("/mcp login far")
	before := len(m)
	s.see("Sign in to far at this address:")
	s.see("/mcp login far <that address>")
	m = signInAddress.FindAllStringSubmatch(s.out.String(), -1)
	if len(m) <= before {
		t.Fatalf("no second address to sign in at:\n%s", s.out.String())
	}
	address := m[len(m)-1][1]
	noFollow := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err = noFollow.Get(address)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	back := resp.Header.Get("Location")
	if !strings.Contains(back, "/callback?code=") {
		t.Fatalf("the authorization server sent the browser to %q", back)
	}
	s.typeLine("/mcp login far " + back)
	s.see("far: connected")
	s.typeLine("/exit")
	s.exit()
}

// gnosh mcp login, from a shell: it says where to sign in, and ends when
// the browser comes back to this machine, or when the address the browser
// tried to go to is pasted on a line; gnosh mcp list then finds the server
// connected, and gnosh mcp logout forgets the sign-in.
func TestMCPLoginFromAShell(t *testing.T) {
	ss := newSignInServer(t)
	w := apptest.NewWorld(t)
	w.Write(w.ConfigDir()+"/mcp.json", `{"mcpServers": {"far": {"type": "http", "url": "`+ss.srv.URL+`/mcp"}}}`)
	if r := run(t, w, "mcp", "list"); !strings.Contains(r.out, "far: ") || !strings.Contains(r.out, "wants a sign-in: gnosh mcp login far") {
		t.Errorf("gnosh mcp list before the sign-in:\n%s%s", r.out, r.err)
	}
	// login runs while the test plays the browser and the person.
	login := func(paste bool) {
		t.Helper()
		o := testOptions(w, "mcp", "login", "far", "--no-browser")
		var out, errb safeBuffer
		in, typed := io.Pipe()
		o.Stdout, o.Stderr, o.Stdin = &out, &errb, in
		done := make(chan int, 1)
		go func() { done <- Main(context.Background(), o) }()
		var address string
		for deadline := time.Now().Add(20 * time.Second); address == ""; time.Sleep(10 * time.Millisecond) {
			if m := signInAddress.FindStringSubmatch(out.String()); m != nil {
				address = m[1]
			}
			if time.Now().After(deadline) {
				t.Fatalf("gnosh mcp login said no address:\n%s%s", out.String(), errb.String())
			}
		}
		if paste {
			noFollow := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
			resp, err := noFollow.Get(address)
			if err != nil {
				t.Fatal(err)
			}
			resp.Body.Close()
			fmt.Fprintln(typed, resp.Header.Get("Location"))
		} else if resp, err := http.Get(address); err == nil {
			resp.Body.Close()
		} else {
			t.Fatal(err)
		}
		select {
		case code := <-done:
			if code != 0 || !strings.Contains(out.String(), "Signed in to far: connected") {
				t.Errorf("gnosh mcp login ended %d:\n%s%s", code, out.String(), errb.String())
			}
		case <-time.After(30 * time.Second):
			t.Fatalf("gnosh mcp login did not end:\n%s%s", out.String(), errb.String())
		}
		typed.Close()
	}
	login(false)
	if r := run(t, w, "mcp", "get", "far"); !strings.Contains(r.out, "sign-in: kept") || !strings.Contains(r.out, "status: connected") {
		t.Errorf("gnosh mcp get after the sign-in:\n%s%s", r.out, r.err)
	}
	if r := run(t, w, "mcp", "logout", "far"); !strings.Contains(r.out, "The sign-in to far is forgotten") {
		t.Errorf("gnosh mcp logout:\n%s%s", r.out, r.err)
	}
	login(true)
	if r := run(t, w, "mcp", "list"); !strings.Contains(r.out, "connected (far 1)") {
		t.Errorf("gnosh mcp list after signing in by a pasted address:\n%s%s", r.out, r.err)
	}
}
