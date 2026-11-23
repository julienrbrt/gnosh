package ui

import (
	"fmt"
	"strings"

	"github.com/gnosh-ai/gnosh/internal/agent"
	"github.com/gnosh-ai/gnosh/internal/app"
	"github.com/gnosh-ai/gnosh/internal/ui/editor"
	"github.com/gnosh-ai/gnosh/internal/ui/menu"
)

// /hooks with nothing after it is a read-only browser of the hooks in
// force, as Claude Code's /hooks is. It lists the moments hooks run at,
// each with its hooks (or how many); Enter on one lists that moment's
// hooks with the file each is written in, and Enter on a hook shows all
// of it. Nothing is changed from here: a hook is changed in its settings
// file. With no hook in force the command says so, and why, as before.

// hookLister is a session whose controls list its hooks
// (app.SessionControls).
type hookLister interface {
	HookChoices() ([]app.HookChoice, string)
}

// hooksWords are the lines that open the browser.
var hooksWords = map[string]bool{"/hooks": true}

// chooseHooks opens the browser, where the session has hooks in force.
func (s *session) chooseHooks(line string) bool {
	hl, ok := s.c.(hookLister)
	if !ok {
		return false
	}
	list, note := hl.HookChoices()
	if len(list) == 0 {
		return false
	}
	s.ed.AddHistory(editor.Draft{Text: line})
	b := hookBrowser{list: list, note: note}
	seen := map[string]bool{}
	for _, h := range list {
		if !seen[h.Event] {
			seen[h.Event] = true
			b.events = append(b.events, h.Event)
		}
	}
	s.hookEvents(b, 0)
	return true
}

// hookBrowser is what the browser shows: the hooks in force, a note on
// those that are not, and the moments they run at in the order listed.
type hookBrowser struct {
	list   []app.HookChoice
	note   string
	events []string
}

// at lists the hooks that run at a moment.
func (b hookBrowser) at(event string) []app.HookChoice {
	var out []app.HookChoice
	for _, h := range b.list {
		if h.Event == event {
			out = append(out, h)
		}
	}
	return out
}

// hooksCount says how many hooks there are, in words.
func hooksCount(n int) string {
	if n == 1 {
		return "1 hook"
	}
	return fmt.Sprintf("%d hooks", n)
}

// hookEvents lists the moments the hooks run at, the cursor on the one at
// index sel.
func (s *session) hookEvents(b hookBrowser, sel int) {
	var opts []menu.Option
	for _, e := range b.events {
		mine := b.at(e)
		about := hooksCount(len(mine))
		if len(mine) == 1 {
			about = oneLine(mine[0].Command)
		}
		opts = append(opts, menu.Option{Label: e, Description: about})
	}
	opts = append(opts, menu.Option{Label: "Done"})
	var body []string
	if b.note != "" {
		body = []string{oneLine(b.note)}
	}
	pick := &question{q: agent.Question{Kind: "hooks", Title: "The hooks in force, by the moment each runs at. Enter on one lists its hooks; a hook is changed in its settings file."}}
	pick.ui = menu.NewSelect(menu.SelectOptions{Title: pick.q.Title, Body: body, Options: opts, Selected: sel, CancelHint: "done", Theme: s.th.Menu, Metrics: s.m})
	pick.local = func(res menu.Result) {
		if res.Cancelled || res.Index < 0 || res.Index >= len(b.events) {
			return
		}
		s.hookList(b, res.Index, 0)
	}
	s.openLocal(pick)
}

// hookList lists the hooks of the moment at index ei, the cursor on the
// one at index sel.
func (s *session) hookList(b hookBrowser, ei, sel int) {
	mine := b.at(b.events[ei])
	var opts []menu.Option
	for _, h := range mine {
		opts = append(opts, menu.Option{Label: oneLine(h.Command), Description: oneLine(h.Source)})
	}
	opts = append(opts, menu.Option{Label: "Back"})
	pick := &question{q: agent.Question{Kind: "hooks", Title: b.events[ei] + ": " + hooksCount(len(mine)) + ". Enter on one shows all of it."}}
	pick.ui = menu.NewSelect(menu.SelectOptions{Title: pick.q.Title, Options: opts, Selected: sel, CancelHint: "done", Theme: s.th.Menu, Metrics: s.m})
	pick.local = func(res menu.Result) {
		switch {
		case res.Cancelled || res.Index < 0:
		case res.Index < len(mine):
			s.hookShow(b, ei, res.Index)
		default:
			s.hookEvents(b, ei)
		}
	}
	s.openLocal(pick)
}

// hookShow shows all of the hook at index hi of the moment at index ei.
func (s *session) hookShow(b hookBrowser, ei, hi int) {
	h := b.at(b.events[ei])[hi]
	var body []string
	for i, l := range strings.Split(strings.TrimSpace(h.Command), "\n") {
		if strings.TrimSpace(l) == "" {
			continue // (no empty lines on the screen)
		}
		if i == 0 {
			l = "Runs: " + l
		}
		body = append(body, safeLine(l))
	}
	for _, d := range h.Details {
		body = append(body, safeLine(d))
	}
	if h.Source != "" {
		body = append(body, safeLine("From: "+h.Source))
	}
	opts := []menu.Option{{Label: "Back"}, {Label: "Done"}}
	pick := &question{q: agent.Question{Kind: "hooks", Title: "A " + b.events[ei] + " hook."}}
	pick.ui = menu.NewSelect(menu.SelectOptions{Title: pick.q.Title, Body: body, Options: opts, CancelHint: "done", Theme: s.th.Menu, Metrics: s.m})
	pick.local = func(res menu.Result) {
		if !res.Cancelled && res.Index == 0 {
			s.hookList(b, ei, hi)
		}
	}
	s.openLocal(pick)
}
