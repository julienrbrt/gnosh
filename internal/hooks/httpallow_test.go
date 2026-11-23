package hooks

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// allowedHttpHookUrls' patterns: * is any run of characters; the scheme
// and host match whatever their case, and a host's trailing dot is no part
// of it; a URL with a user name in it fits none.
func TestURLAllowed(t *testing.T) {
	patterns := []string{"https://hooks.example.com/*", "http://localhost:*", "HTTPS://Upper.Example.org./x"}
	for _, c := range []struct {
		url  string
		want bool
	}{
		{"https://hooks.example.com/check", true},
		{"https://HOOKS.Example.COM./check?a=1", true},
		{"https://hooks.example.com", false},
		{"https://hooks.example.com.evil.example/check", false},
		{"http://hooks.example.com/check", false},
		{"https://hooks.example.com/Check", true},
		{"http://localhost:8080/x", true},
		{"http://localhost/x", false},
		{"http://localhost:80@elsewhere.example/x", false},
		{"https://user@hooks.example.com/check", false},
		{"https://upper.example.org/x", true},
		{"https://upper.example.org/X", false},
		{"not a url", false},
		{"/relative/only", false},
	} {
		if got := URLAllowed(c.url, patterns); got != c.want {
			t.Errorf("URLAllowed(%q) = %v, want %v", c.url, got, c.want)
		}
	}
	if URLAllowed("https://hooks.example.com/check", []string{}) {
		t.Error("an empty list let a URL through")
	}
	// A * in the host stands for host characters alone, unless it ends
	// the pattern (the review of 2026-11-20).
	for _, c := range []struct {
		pattern, url string
		want         bool
	}{
		{"https://*.example.com/*", "https://a.example.com/x", true},
		{"https://*.example.com/*", "https://attacker.test/.example.com/x", false},
		{"https://*.example.com", "https://a.b.example.com", true},
		{"https://*.example.com", "https://attacker.test#.example.com", false},
		{"https://*.example.com", "https://attacker.test?.example.com", false},
		{"http://*.example.com:8080/*", "http://:8080/.example.com:8080/", false},
		{"http://*:8080/*", "http://evil.test:9/:8080/x", false},
		{"http://localhost:*", "http://localhost:8080/x/y?z=1", true},
		{"*", "https://anything.example/x", true},
	} {
		if got := URLAllowed(c.url, []string{c.pattern}); got != c.want {
			t.Errorf("URLAllowed(%q, %q) = %v, want %v", c.url, c.pattern, got, c.want)
		}
	}
	for _, c := range []struct {
		pattern, s string
		want       bool
	}{
		{"*", "", true}, {"a*b*c", "aXbYc", true}, {"a*b*c", "aXbY", false}, {"**", "x", true},
		{"a*a", "aaa", true}, {"a*ab", "aaab", true}, {"", "", true}, {"", "x", false}, {"x*", "x", true},
	} {
		if got := urlMatch(c.pattern, c.s); got != c.want {
			t.Errorf("urlMatch(%q, %q) = %v", c.pattern, c.s, got)
		}
	}
}

// An http hook's address is shown, in the startup check's words and in its
// records, without what can hold a secret: no query, no user, and no long
// piece of its path.
func TestShownURL(t *testing.T) {
	for raw, want := range map[string]string{
		"https://hooks.example.com/check":                               "https://hooks.example.com/check",
		"https://hooks.example.com/check?key=s3cret":                    "https://hooks.example.com/check?…",
		"https://me:pw@hooks.example.com/x":                             "https://hooks.example.com/x",
		"https://hooks.slack.example/services/T0/B0/abcdefghijklmnop0q": "https://hooks.slack.example/services/T0/B0/abcd…",
		"::not a url": "(an address that cannot be read)",
	} {
		if got := ShownURL(raw); got != want {
			t.Errorf("ShownURL(%q) = %q, want %q", raw, got, want)
		}
	}
	h := Hook{Event: PreToolUse, Matcher: "Bash", URL: "http://127.0.0.1:9/check?key=s3cret"}
	if line := h.CommandLine(); line != "POST http://127.0.0.1:9/check?…" {
		t.Errorf("CommandLine = %q", line)
	}
	opts := options(t)
	opts.HTTPURLs = []string{"https://hooks.example.com/*"}
	if problems := Check(Config{Hooks: []Hook{h}}, opts); len(problems) != 1 || strings.Contains(problems[0].Message, "s3cret") {
		t.Errorf("Check: %+v", problems)
	}
}

// An http hook whose URL allowedHttpHookUrls does not let it post to is not
// run, as if it were not there, and Check warns of it; an empty list runs
// no http hook. httpHookAllowedEnvVars narrows the variables its headers
// take to those both lists name.
func TestHTTPHookLimits(t *testing.T) {
	var mu sync.Mutex
	var calls int
	var auth, other string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		mu.Lock()
		calls++
		auth, other = r.Header.Get("Authorization"), r.Header.Get("X-Other")
		mu.Unlock()
		_, _ = io.WriteString(w, `{"hookSpecificOutput": {"hookEventName": "PreToolUse", "permissionDecision": "deny", "permissionDecisionReason": "no"}}`)
	}))
	defer srv.Close()
	opts := options(t)
	env := opts.Environ
	opts.Environ = func() []string { return append(env(), "TOKEN=s3cret", "OTHER=hidden") }
	hook := Hook{Event: PreToolUse, Matcher: "Bash", URL: srv.URL + "/check",
		Headers: map[string]string{"Authorization": "Bearer ${TOKEN}", "X-Other": "[$OTHER]"}, AllowedEnvVars: []string{"TOKEN", "OTHER"}}

	for _, list := range [][]string{{"https://hooks.example.com/*"}, {}} {
		opts.HTTPURLs = list
		problems := Check(Config{Hooks: []Hook{hook}}, opts)
		if len(problems) != 1 || !problems[0].Warning || !strings.Contains(problems[0].Message, "allowedHttpHookUrls does not let an http hook post to, so it never runs") || !strings.Contains(problems[0].Message, "/check") {
			t.Errorf("Check with %q: %+v", list, problems)
		}
		out := run(t, newRunner(t, opts, hook), input(PreToolUse))
		if out.Blocked || out.PermissionDecision != "" || len(out.Records) != 0 {
			t.Errorf("a hook allowedHttpHookUrls %q does not let post ran: %+v", list, out)
		}
	}
	mu.Lock()
	if calls != 0 {
		t.Errorf("the server was called %d times", calls)
	}
	mu.Unlock()

	// A list it fits lets it run; httpHookAllowedEnvVars lets TOKEN alone
	// into its headers.
	opts.HTTPURLs = []string{"http://127.0.0.1:*"}
	opts.HTTPEnvVars = []string{"TOKEN", "UNUSED"}
	if problems := Check(Config{Hooks: []Hook{hook}}, opts); len(problems) != 0 {
		t.Errorf("Check: %+v", problems)
	}
	out := run(t, newRunner(t, opts, hook), input(PreToolUse))
	if out.PermissionDecision != Deny {
		t.Errorf("the hook's answer: %+v", out)
	}
	mu.Lock()
	if calls != 1 || auth != "Bearer s3cret" || other != "[]" {
		t.Errorf("calls %d, headers %q %q", calls, auth, other)
	}
	mu.Unlock()

	// An empty httpHookAllowedEnvVars lets none in.
	opts.HTTPEnvVars = []string{}
	run(t, newRunner(t, opts, hook), input(PreToolUse))
	mu.Lock()
	if calls != 2 || auth != "Bearer" && auth != "Bearer " || other != "[]" {
		t.Errorf("calls %d, headers %q %q", calls, auth, other)
	}
	mu.Unlock()
}
