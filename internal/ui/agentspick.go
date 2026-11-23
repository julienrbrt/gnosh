package ui

import (
	"github.com/gnosh-ai/gnosh/internal/agent"
	"github.com/gnosh-ai/gnosh/internal/app"
	"github.com/gnosh-ai/gnosh/internal/ui/editor"
	"github.com/gnosh-ai/gnosh/internal/ui/menu"
)

// /agents with nothing after it is a browser of the kinds of sub-agent, as
// Claude Code's /agents opens its agents: each kind with what it is for
// and where it is written; Enter on one shows all of it (its tools, model,
// MCP servers, skills and how it runs). It changes nothing: a kind is
// changed in its file. With none, the command says so, as before.

// agentBrowsing is a session whose controls list the kinds of sub-agent
// with what each is given (app.SessionControls).
type agentBrowsing interface {
	AgentChoices() ([]app.AgentChoice, bool)
}

// agentsWords are the lines that open the browser.
var agentsWords = map[string]bool{"/agents": true}

// chooseAgents opens the browser, where the session has kinds to list.
func (s *session) chooseAgents(line string) bool {
	ab, ok := s.c.(agentBrowsing)
	if !ok {
		return false
	}
	kinds, on := ab.AgentChoices()
	if len(kinds) == 0 {
		return false
	}
	s.ed.AddHistory(editor.Draft{Text: line})
	s.agentList(kinds, on, 0)
	return true
}

// agentList lists the kinds, the cursor on the one at index sel.
func (s *session) agentList(kinds []app.AgentChoice, on bool, sel int) {
	var opts []menu.Option
	for _, k := range kinds {
		opts = append(opts, menu.Option{Label: oneLine(k.Name), Description: oneLine(firstNonEmpty(k.Description, "(no description)"))})
	}
	opts = append(opts, menu.Option{Label: "Done"})
	title := "The kinds of sub-agent the model can start. Enter on one shows all of it; a kind is changed in its file."
	if !on {
		title = "The kinds of sub-agent. The Agent tool is off in this session, so the model starts none of them."
	}
	pick := &question{q: agent.Question{Kind: "agents", Title: title}}
	pick.ui = menu.NewSelect(menu.SelectOptions{Title: title, Options: opts, Selected: sel, CancelHint: "done", Theme: s.th.Menu, Metrics: s.m})
	pick.local = func(res menu.Result) {
		if res.Cancelled || res.Index < 0 || res.Index >= len(kinds) {
			return
		}
		s.agentShow(kinds, on, res.Index)
	}
	s.openLocal(pick)
}

// agentShow shows all of the kind at index i.
func (s *session) agentShow(kinds []app.AgentChoice, on bool, i int) {
	k := kinds[i]
	var body []string
	if k.Description != "" {
		body = append(body, safeLine(oneLine(k.Description)))
	}
	for _, d := range k.Details {
		body = append(body, safeLine(d))
	}
	if k.Source != "" {
		body = append(body, safeLine("From: "+k.Source))
	}
	opts := []menu.Option{{Label: "Back"}, {Label: "Done"}}
	pick := &question{q: agent.Question{Kind: "agents", Title: "The sub-agent " + oneLine(k.Name) + "."}}
	pick.ui = menu.NewSelect(menu.SelectOptions{Title: pick.q.Title, Body: body, Options: opts, CancelHint: "done", Theme: s.th.Menu, Metrics: s.m})
	pick.local = func(res menu.Result) {
		if !res.Cancelled && res.Index == 0 {
			s.agentList(kinds, on, i)
		}
	}
	s.openLocal(pick)
}
