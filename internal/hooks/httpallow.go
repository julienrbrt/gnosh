package hooks

import (
	"net/url"
	"strings"
)

// This file is Claude Code's allowedHttpHookUrls (Options.HTTPURLs): the
// URL patterns an http hook may post to.

// urlAllowed reports whether an http hook may post to u: always when no
// list is in force.
func (r *Runner) urlAllowed(u string) bool {
	return r.opts.HTTPURLs == nil || URLAllowed(u, r.opts.HTTPURLs)
}

// URLAllowed reports whether the URL u fits one of the patterns, as
// Claude Code's allowedHttpHookUrls fits them: * stands for any run of
// characters, and anything else for itself; the scheme and the host are
// matched whatever their case, and a host's trailing dot (a fully
// qualified name's) is no part of it, as DNS has it.
//
// A * in the pattern's scheme and host stands only for characters a host
// name has (no /, ?, #, @ or :), so that "https://*.example.com/*" does
// not take "https://elsewhere.test/.example.com/x"; a * that ends the
// pattern stands for the rest of the address, as in "http://localhost:*".
// A URL with a user name in it, or no host name, fits none: in
// "http://localhost:80@elsewhere.example/" the host is elsewhere.example.
func URLAllowed(u string, patterns []string) bool {
	parsed, err := url.Parse(u)
	if err != nil || parsed.User != nil || parsed.Hostname() == "" || len(u) > maxMatchedURL {
		return false
	}
	nu := normalURL(u)
	for _, p := range patterns {
		if len(p) <= maxMatchedURL && urlMatch(normalURL(p), nu) {
			return true
		}
	}
	return false
}

// maxMatchedURL is the longest address or pattern matched: the matching
// takes time in the product of the two lengths.
const maxMatchedURL = 4096

// normalURL lowers the case of a URL's (or a pattern's) scheme and host,
// and takes a trailing dot off the host; the rest is left as it is.
func normalURL(s string) string {
	scheme, rest, ok := strings.Cut(s, "://")
	if !ok {
		return s
	}
	auth, tail := rest, ""
	if i := strings.IndexAny(rest, "/?#"); i >= 0 {
		auth, tail = rest[:i], rest[i:]
	}
	user := ""
	if i := strings.LastIndex(auth, "@"); i >= 0 {
		user, auth = auth[:i+1], auth[i+1:]
	}
	host, port := auth, ""
	if strings.HasPrefix(host, "[") {
		if i := strings.Index(host, "]"); i >= 0 {
			host, port = host[:i+1], host[i+1:]
		}
	} else if i := strings.LastIndex(host, ":"); i >= 0 {
		host, port = host[:i], host[i:]
	}
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	return strings.ToLower(scheme) + "://" + user + host + port + tail
}

// ShownURL is an http hook's address as it is shown and logged: no user
// name or password, no query string, and each long piece of the path cut
// to its start, since a webhook's path is often its secret (Slack's
// /services/T…/B…/<token>, Discord's /api/webhooks/<id>/<token>). The
// whole address went to /status, gnosh doctor, /hooks and the hook records
// (the config review of 2026-11-19), and to the startup check's warnings
// and the hooks' own records until 2026-11-20.
func ShownURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "(an address that cannot be read)"
	}
	parts := strings.Split(u.EscapedPath(), "/")
	for i, p := range parts {
		if len(p) > maxShownPathPiece {
			parts[i] = p[:4] + "…"
		}
	}
	out := u.Scheme + "://" + u.Host + strings.Join(parts, "/")
	if u.RawQuery != "" {
		out += "?…"
	}
	return out
}

// maxShownPathPiece is the longest piece of a hook's path shown whole.
const maxShownPathPiece = 16

// urlMatch reports whether s fits pattern, where * stands for a run of
// characters, none included, and every other byte for itself; a * before
// the end of the pattern's scheme and host (the first /, ? or # after its
// "://") stands for host characters alone, unless it ends the pattern.
func urlMatch(pattern, s string) bool {
	hostEnd := -1
	if i := strings.Index(pattern, "://"); i >= 0 {
		hostEnd = len(pattern)
		if j := strings.IndexAny(pattern[i+3:], "/?#"); j >= 0 {
			hostEnd = i + 3 + j
		}
	}
	takes := func(i int, c byte) bool {
		return i >= hostEnd || i == len(pattern)-1 || !strings.ContainsRune("/?#@:", rune(c))
	}
	// next[j] says whether pattern[i+1:] fits s[j:], for the i being
	// worked out (from the end of the pattern back).
	next := make([]bool, len(s)+1)
	next[len(s)] = true
	for i := len(pattern) - 1; i >= 0; i-- {
		cur := make([]bool, len(s)+1)
		if pattern[i] == '*' {
			cur[len(s)] = next[len(s)]
			for j := len(s) - 1; j >= 0; j-- {
				cur[j] = next[j] || takes(i, s[j]) && cur[j+1]
			}
		} else {
			for j := len(s) - 1; j >= 0; j-- {
				cur[j] = pattern[i] == s[j] && next[j+1]
			}
		}
		next = cur
	}
	return next[0]
}
