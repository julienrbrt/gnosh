package config

import (
	"slices"
	"strings"
)

// This file is Claude Code's two limits on http hooks: allowedHttpHookUrls,
// the URL patterns an http hook may post to, and httpHookAllowedEnvVars,
// the variables an http hook's headers may take. Each is a list that the
// files' lists are merged into, and in force once any file gives it, even
// as an empty list (no http hook runs; no header takes a variable). A
// project's waits for the folder's trust, as its disableAllHooks does
// (KindHookOff): it would switch off the person's own http hooks, a guard
// among them, which would then let through what it guards (the review of
// 2026-11-20).

// allowList is one of them, as the builder gathers it.
type allowList struct {
	// from is every place that gives the key.
	from  []Origin
	items []ListItem
}

// add puts one file's list in.
func (a *allowList) add(items []string, o Origin) {
	a.from = append(a.from, o)
	for _, it := range items {
		if !slices.ContainsFunc(a.items, func(have ListItem) bool { return have.V == it }) {
			a.items = append(a.items, ListItem{V: it, Origin: o})
		}
	}
}

// drop takes out what the places from picks gave, and reports whether
// there was any.
func (a *allowList) drop(from func(Origin) bool) bool {
	n := len(a.from)
	a.from = slices.DeleteFunc(a.from, from)
	a.items = slices.DeleteFunc(a.items, func(it ListItem) bool { return from(it.Origin) })
	return len(a.from) != n
}

// list is the merged list: nil when no file gives the key, and empty but
// not nil when the files give only empty lists.
func (a *allowList) list() []ListItem {
	if len(a.from) == 0 {
		return nil
	}
	return append(make([]ListItem, 0, len(a.items)), a.items...)
}

// hookAllowList reads allowedHttpHookUrls or httpHookAllowedEnvVars: a
// list of strings, URL patterns with * standing for any run of characters,
// or the names of variables.
func (s *source) hookAllowList(f jfield) {
	urls := f.key == "allowedHttpHookUrls"
	what := "URL patterns an http hook may post to, with * standing for any run of characters"
	if !urls {
		what = "names of the variables an http hook's headers may take"
	}
	if f.val.kind != jArray {
		s.errorf(f.line, "%q is a list of the %s, and here it is %s.", f.key, what, f.val.kind.word())
		return
	}
	var items []string
	for _, it := range f.val.items {
		v, got := "", shown(it)
		if it.kind == jString {
			v, got = strings.TrimSpace(it.str), "empty"
		}
		if v == "" {
			s.errorf(it.line, "%q is a list of the %s, and one of its items is %s.", f.key, what, got)
			return
		}
		items = append(items, v)
	}
	o := s.at(f.line)
	s.add(op{kind: KindHookOff, text: f.key, origin: o, feature: FeatureHooks, what: "a hook setting",
		apply: func(b *builder) {
			if urls {
				b.hookURLs.add(items, o)
			} else {
				b.hookEnvVars.add(items, o)
			}
		}})
}
