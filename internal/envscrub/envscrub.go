// Package envscrub takes the credentials out of the environment of the
// programs gnosh starts, as Claude Code's CLAUDE_CODE_SUBPROCESS_ENV_SCRUB
// does when it is 1: commands, hooks, MCP and LSP servers and the rest see
// no API key, token or password that gnosh was started with, which a
// prompt injection would have them read; gnosh keeps its own for its own
// requests (spec/DECISIONS.md 384).
//
// A variable is a credential by its name (ANTHROPIC_API_KEY, NPM_TOKEN,
// DB_PASSWORD) or by the shape of its value (a key a service hands out, a
// private key, a URL with a password in it), so the scrub is one layer
// beside narrow permission rules, not the only one. As in Claude Code, the
// GitHub tokens, the proxies and git's GIT_CONFIG_* stay, whatever they
// hold; a package index's URL (PIP_INDEX_URL, NPM_CONFIG_REGISTRY) stays
// without the name and password in it; CLAUDE_CONFIG_DIR, and gnosh's
// GNOSH_CONFIG_DIR and GNOSH_DATA_DIR, which say where gnosh's own
// credentials are kept, go.
package envscrub

import (
	"net/url"
	"strings"
)

// Var is the variable that turns the scrub on, Claude Code's.
const Var = "CLAUDE_CODE_SUBPROCESS_ENV_SCRUB"

// On reports whether the environment turns the scrub on: Var set to 1
// (or true, yes, on, as gnosh takes its other switches), the last setting
// winning.
func On(environ []string) bool { return Switch(environ, Var) }

// Env is environ without its credentials, in its order; a package index's
// URL keeps its place without the name and password in it.
func Env(environ []string) []string {
	out := make([]string, 0, len(environ))
	for _, kv := range environ {
		name, value, ok := strings.Cut(kv, "=")
		if !ok {
			out = append(out, kv)
			continue
		}
		switch {
		case kept(name):
			out = append(out, kv)
		case dropped(name, value):
		case indexURL(name):
			out = append(out, name+"="+withoutUser(value))
		case urlWithPassword(value):
		default:
			out = append(out, kv)
		}
	}
	return out
}

// kept reports whether a variable stays whatever it holds: the GitHub
// tokens (gh and scripts that call GitHub's API keep working), the
// proxies, which may hold a name and password of their own, git's settings
// from the environment, and the switch itself, which the programs gnosh
// starts pass on.
func kept(name string) bool {
	switch strings.ToUpper(name) {
	case "GITHUB_TOKEN", "GH_TOKEN", "GH_ENTERPRISE_TOKEN", "GITHUB_ENTERPRISE_TOKEN",
		"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "NO_PROXY", "GIT_CONFIG_COUNT", Var:
		return true
	}
	return strings.HasPrefix(name, "GIT_CONFIG_KEY_") || strings.HasPrefix(name, "GIT_CONFIG_VALUE_")
}

// dropped reports whether a variable is a credential by its name, or by
// the shape of its value.
func dropped(name, value string) bool {
	upper := strings.ToUpper(name)
	switch upper {
	case "CLAUDE_CONFIG_DIR", "GNOSH_CONFIG_DIR", "GNOSH_DATA_DIR":
		return true
	}
	if value == "" || pathLike(value) {
		// (A path is where a credential is kept, not the credential:
		// GOOGLE_APPLICATION_CREDENTIALS, PASSWORD_STORE_DIR.)
		return false
	}
	words := strings.FieldsFunc(upper, func(r rune) bool { return r == '_' || r == '-' || r == '.' })
	for i, w := range words {
		switch w {
		case "TOKEN", "SECRET", "PASSWORD", "PASSWD", "PASSPHRASE", "APIKEY", "CREDENTIAL", "CREDENTIALS":
			return true
		case "KEY":
			// API_KEY, ACCESS_KEY, PRIVATE_KEY, AUTH_KEY, or a name that
			// ends in KEY (OPENAI_KEY) with a value longer than a few
			// letters (SORT_KEY=name).
			if i > 0 && (words[i-1] == "API" || words[i-1] == "ACCESS" || words[i-1] == "PRIVATE" || words[i-1] == "AUTH") {
				return true
			}
			if i == len(words)-1 && len(value) >= 16 {
				return true
			}
		}
	}
	return secretShaped(value)
}

// pathLike reports whether a value is a file's path, with no blank in it.
func pathLike(v string) bool {
	return (strings.HasPrefix(v, "/") || strings.HasPrefix(v, "~/") || strings.HasPrefix(v, "./")) && !strings.ContainsAny(v, " \t\n")
}

// prefixes begin the keys and tokens services hand out.
var prefixes = []string{"sk-", "sk_live_", "sk_test_", "rk_live_", "pk_live_", "xoxb-", "xoxp-", "xoxa-", "xoxs-",
	"ghp_", "gho_", "ghu_", "ghs_", "ghr_", "github_pat_", "glpat-", "npm_", "hf_", "AIza", "AKIA", "ASIA"}

// secretShaped reports whether a value looks like a key or token a
// service handed out (one of prefixes, then at least 12 more characters
// with a digit among them, and no blank), a private key, or a JSON web
// token.
func secretShaped(value string) bool {
	v := strings.TrimSpace(value)
	if strings.Contains(v, "-----BEGIN") && strings.Contains(v, "PRIVATE KEY") {
		return true
	}
	if strings.ContainsAny(v, " \t\n") {
		return false
	}
	for _, p := range prefixes {
		if rest, ok := strings.CutPrefix(v, p); ok && len(rest) >= 12 && strings.ContainsAny(rest, "0123456789") {
			return true
		}
	}
	if parts := strings.Split(v, "."); len(parts) == 3 && strings.HasPrefix(v, "eyJ") && len(parts[1]) >= 8 && len(parts[2]) >= 8 {
		return true
	}
	return false
}

// indexURL reports whether a variable names a package index or registry,
// whose URL stays without the name and password in it.
func indexURL(name string) bool {
	upper := strings.ToUpper(name)
	return strings.HasSuffix(upper, "INDEX_URL") || strings.HasSuffix(upper, "_REGISTRY") || strings.HasSuffix(upper, "_INDEX")
}

// urlWithPassword reports whether a value holds a URL with a password in
// it (postgres://app:hunter2@db/app), among words it may be one of.
func urlWithPassword(value string) bool {
	for _, w := range strings.Fields(value) {
		if u, err := url.Parse(w); err == nil && u.User != nil && u.Host != "" {
			if _, set := u.User.Password(); set {
				return true
			}
		}
	}
	return false
}

// withoutUser is value with the name and password taken out of each URL
// among its words.
func withoutUser(value string) string {
	words := strings.Fields(value)
	changed := false
	for i, w := range words {
		if u, err := url.Parse(w); err == nil && u.User != nil && u.Host != "" {
			u.User = nil
			words[i], changed = u.String(), true
		}
	}
	if !changed {
		return value
	}
	return strings.Join(words, " ")
}

// AllowlistVar is Claude Code's switch for the stdio MCP servers'
// environment: with it on (1, true, yes, on), a server starts from
// Baseline alone, its own env from its settings laid over it, in place of
// the environment gnosh was started with.
const AllowlistVar = "CLAUDE_CODE_MCP_ALLOWLIST_ENV"

// Baseline is the part of environ a program needs to run as the person
// and nothing more: where programs are (PATH), who the person is and
// where their home is, their shell, terminal, language and time zone,
// the temporary folder, and the XDG folders.
func Baseline(environ []string) []string {
	var out []string
	for _, kv := range environ {
		name, _, ok := strings.Cut(kv, "=")
		if !ok {
			continue
		}
		switch name {
		case "PATH", "HOME", "USER", "LOGNAME", "SHELL", "TERM", "LANG", "LANGUAGE", "TZ", "TMPDIR", "TMP", "TEMP",
			"XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_CACHE_HOME", "XDG_STATE_HOME", "XDG_RUNTIME_DIR", "SYSTEMROOT", "USERPROFILE":
			out = append(out, kv)
		default:
			if strings.HasPrefix(name, "LC_") {
				out = append(out, kv)
			}
		}
	}
	return out
}

// Switch reports whether the variable name is on in environ: 1, true,
// yes or on, the last setting winning.
func Switch(environ []string, name string) bool {
	on := false
	for _, kv := range environ {
		if n, value, ok := strings.Cut(kv, "="); ok && n == name {
			switch strings.ToLower(strings.TrimSpace(value)) {
			case "1", "true", "yes", "on":
				on = true
			default:
				on = false
			}
		}
	}
	return on
}
