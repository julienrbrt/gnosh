package config

import (
	"net/url"
	"strings"
)

// secrets remembers every secret value a load came across, so that none of
// them can leave in a message or a dump. Values are kept out of messages by
// how the messages are written; this is the second line of defence.
type secrets struct {
	vals []string
}

// leastSecret is the shortest value scrub looks for. A shorter one would
// match by accident all over ordinary text.
const leastSecret = 6

// add remembers a secret value.
func (s *secrets) add(v string) {
	if s == nil || len(v) < leastSecret {
		return
	}
	for _, have := range s.vals {
		if have == v {
			return
		}
	}
	s.vals = append(s.vals, v)
}

// addURL remembers the parts of an address that can hold a secret: the
// password, and the query string.
func (s *secrets) addURL(raw string) {
	u, err := url.Parse(raw)
	if err != nil {
		return
	}
	if u.User != nil {
		if pw, ok := u.User.Password(); ok {
			s.add(pw)
		}
	}
	for _, vals := range u.Query() {
		for _, v := range vals {
			s.add(v)
		}
	}
}

// scrub replaces every remembered secret in text.
func (s *secrets) scrub(text string) string {
	if s == nil {
		return text
	}
	for _, v := range s.vals {
		if strings.Contains(text, v) {
			text = strings.ReplaceAll(text, v, hidden)
		}
	}
	return text
}

// DisplayURL is an address without the parts that can hold a secret: no
// user name or password, and no query string.
func DisplayURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "(an address that cannot be read)"
	}
	out := u.Scheme + "://" + u.Host + u.EscapedPath()
	if u.RawQuery != "" {
		out += "?…"
	}
	return out
}
