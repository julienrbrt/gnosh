package ui

import (
	"github.com/gnosh-ai/gnosh/internal/agent"
	"github.com/gnosh-ai/gnosh/internal/app"
	"github.com/gnosh-ai/gnosh/internal/ui/editor"
	"github.com/gnosh-ai/gnosh/internal/ui/menu"
)

// /mcp with nothing after it is a picker of the session's MCP servers, as
// Claude Code's /mcp opens its servers: each with how it stands; Enter on
// one offers what can be done to it (reconnect it, disable it for the
// session, or enable it again; sign in to one that wants it, or sign out),
// which runs as /mcp reconnect, disable, enable, login or logout and its
// name. With no server configured, the command says how to
// add one, as before.

// mcpLister is a session whose controls list its MCP servers
// (app.SessionControls).
type mcpLister interface {
	MCPChoices() []app.MCPChoice
}

// mcpWords are the lines that open the picker.
var mcpWords = map[string]bool{"/mcp": true}

// statusWords say how a server stands, in a word or two.
var statusWords = map[string]string{"connected": "connected", "failed": "not connected", "needs-auth": "wants a sign-in", "disabled": "disabled", "pending": "connecting"}

// chooseMCP opens the picker, where the session has servers to list.
func (s *session) chooseMCP(line string) bool {
	ml, ok := s.c.(mcpLister)
	if !ok {
		return false
	}
	servers := ml.MCPChoices()
	if len(servers) == 0 {
		return false
	}
	s.ed.AddHistory(editor.Draft{Text: line})
	var opts []menu.Option
	for _, sv := range servers {
		opts = append(opts, menu.Option{Label: oneLine(sv.Name) + "  " + firstNonEmpty(statusWords[sv.Status], sv.Status), Description: oneLine(sv.About)})
	}
	opts = append(opts, menu.Option{Label: "Done"})
	pick := &question{q: agent.Question{Kind: "mcp", Title: "The MCP servers. Enter on one: reconnect, disable or enable it."}}
	pick.ui = menu.NewSelect(menu.SelectOptions{Title: pick.q.Title, Options: opts, CancelHint: "done", Theme: s.th.Menu, Metrics: s.m})
	pick.local = func(res menu.Result) {
		if res.Cancelled || res.Index < 0 || res.Index >= len(servers) {
			return
		}
		s.mcpActions(servers[res.Index])
	}
	s.openLocal(pick)
	return true
}

// mcpActions asks what is to be done to a server, and does it.
func (s *session) mcpActions(sv app.MCPChoice) {
	type action struct{ label, verb string }
	actions := []action{{"Reconnect it", "reconnect"}, {"Disable it for this session", "disable"}}
	switch {
	case sv.Status == "disabled":
		actions = []action{{"Enable it", "enable"}}
	case sv.Status == "needs-auth":
		actions = []action{{"Sign in", "login"}, {"Disable it for this session", "disable"}}
	}
	if sv.SignedIn {
		actions = append(actions, action{"Sign out (forget the sign-in)", "logout"})
	}
	var opts []menu.Option
	for _, a := range actions {
		opts = append(opts, menu.Option{Label: a.label})
	}
	opts = append(opts, menu.Option{Label: "Never mind"})
	pick := &question{q: agent.Question{Kind: "mcp", Title: oneLine(sv.Name) + ": " + firstNonEmpty(statusWords[sv.Status], sv.Status) + ". What now?"}}
	pick.ui = menu.NewSelect(menu.SelectOptions{Title: pick.q.Title, Options: opts, CancelHint: "never mind", Theme: s.th.Menu, Metrics: s.m})
	pick.local = func(res menu.Result) {
		if res.Cancelled || res.Index < 0 || res.Index >= len(actions) {
			return
		}
		s.run("/mcp " + actions[res.Index].verb + " " + sv.Name)
	}
	s.openLocal(pick)
}
