package ui

import (
	"strings"

	"github.com/gnosh-ai/gnosh/internal/agent"
	"github.com/gnosh-ai/gnosh/internal/app"
	"github.com/gnosh-ai/gnosh/internal/ui/editor"
	"github.com/gnosh-ai/gnosh/internal/ui/menu"
)

// /permissions with nothing after it is a picker, as Claude Code's
// /permissions is: a rule typed into one of the first three rows is added
// (/permissions allow, ask or deny, which keeps it in the project's local
// settings file), and a rule picked from the list is taken out, after a
// yes (/permissions remove). A rule from a file gnosh does not write is
// named with its file, and left.

// permissionsLister is a session whose controls list the rules in force
// (app.SessionControls).
type permissionsLister interface {
	PermissionRules() (mode string, rules []app.PermissionRule)
}

// permissionsWords are the lines that open the picker.
var permissionsWords = map[string]bool{"/permissions": true}

// addRows are the picker's first rows: a rule typed into one is added to
// that list.
var addRows = []struct{ list, label string }{
	{"allow", "Add an allow rule: runs without a question"},
	{"ask", "Add an ask rule: always asks"},
	{"deny", "Add a deny rule: never runs"},
}

// choosePermissions opens the picker.
func (s *session) choosePermissions(line string) bool {
	pl, ok := s.c.(permissionsLister)
	if !ok {
		return false
	}
	mode, rules := pl.PermissionRules()
	s.ed.AddHistory(editor.Draft{Text: line})
	var opts []menu.Option
	for _, a := range addRows {
		opts = append(opts, menu.Option{Label: a.label, Text: menu.TextRequired, Placeholder: "a rule, as Bash(go test *) or Read(./docs/**)"})
	}
	for _, r := range rules {
		about := "from " + showPath(s.cwd, s.home, r.Source)
		if r.Held {
			about += s.th.Sep + "waits for the folder to be trusted"
		}
		opts = append(opts, menu.Option{Label: r.List + "  " + oneLine(r.Rule), Description: about})
	}
	opts = append(opts, menu.Option{Label: "Done"})
	// (The mode in the status line's words: "manual mode", not
	// "default".)
	shown, ok := modeNames[mode]
	if !ok {
		shown = oneLine(mode) + " mode"
	}
	title := "Permissions (" + shown + "). Type a rule into one of the first rows to add it, or pick a rule to take it out."
	pick := &question{q: agent.Question{Kind: "permissions", Title: title}}
	pick.ui = menu.NewSelect(menu.SelectOptions{Title: title, Options: opts, CancelHint: "done", Theme: s.th.Menu, Metrics: s.m})
	pick.local = func(res menu.Result) {
		switch i := res.Index; {
		case res.Cancelled || i < 0:
		case i < len(addRows):
			if rule := strings.TrimSpace(res.Text); rule != "" {
				s.run("/permissions " + addRows[i].list + " " + rule)
			}
		case i-len(addRows) < len(rules):
			s.removeRuleMenu(rules[i-len(addRows)])
		}
	}
	s.openLocal(pick)
	return true
}

// removeRuleMenu asks before a rule is taken out, or says where one is
// that gnosh does not write.
func (s *session) removeRuleMenu(r app.PermissionRule) {
	if !r.Removable {
		s.flash("The " + r.List + " rule " + oneLine(r.Rule) + " is in " + showPath(s.cwd, s.home, r.Source) + ", which gnosh does not write: take it out there.")
		return
	}
	title := "Take out the " + r.List + " rule " + oneLine(r.Rule) + "?"
	pick := &question{q: agent.Question{Kind: "permissions", Title: title}}
	pick.ui = menu.NewSelect(menu.SelectOptions{Title: title, Options: []menu.Option{
		{Label: "Yes, take it out", Description: "from " + showPath(s.cwd, s.home, r.Source)},
		{Label: "No, keep it"},
	}, CancelHint: "keep it", Theme: s.th.Menu, Metrics: s.m})
	pick.local = func(res menu.Result) {
		if !res.Cancelled && res.Index == 0 {
			s.run("/permissions remove " + r.List + " " + r.Rule)
		}
	}
	s.openLocal(pick)
}
