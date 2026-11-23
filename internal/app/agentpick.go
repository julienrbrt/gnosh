package app

import (
	"fmt"
	"strings"

	"github.com/gnosh-ai/gnosh/internal/app/agentdef"
)

// AgentChoice is a kind of sub-agent as the /agents browser shows it
// (/agents with nothing after it, as Claude Code's opens its agents): its
// name, what it is for, the file it is written in, and the rest a line
// each.
type AgentChoice struct {
	Name, Description, Source string
	Details                   []string
}

// AgentChoices lists the kinds of sub-agent, and whether the model can
// start them in this session (the Agent tool is on).
func (c *controls) AgentChoices() ([]AgentChoice, bool) { return c.cur().agentChoices() }

// agentChoices is SessionControls.AgentChoices.
func (l *live) agentChoices() ([]AgentChoice, bool) {
	var out []AgentChoice
	for _, d := range l.agentDefs().All() {
		out = append(out, AgentChoice{Name: d.Name, Description: d.Description, Source: firstOf(l.tilde(d.Source), d.Scope), Details: agentDetails(d)})
	}
	on := false
	for _, n := range l.toolNames() {
		on = on || n == agentToolName
	}
	return out, on
}

// agentDetails are the lines that say what a kind of sub-agent is given
// and how it runs, beside its name and its file.
func agentDetails(d agentdef.Def) []string {
	out := []string{"Tools: every tool a sub-agent may have"}
	if d.ToolsSet {
		out[0] = "Tools: " + firstOf(strings.Join(d.Tools, ", "), "none")
	}
	if len(d.Disallowed) > 0 {
		out = append(out, "Not: "+strings.Join(d.Disallowed, ", "))
	}
	out = append(out, "Model: "+firstOf(d.Model, "the session's"))
	if d.Effort != "" {
		out = append(out, "Effort: "+d.Effort)
	}
	if d.PermissionMode != "" {
		out = append(out, "Permission mode: "+d.PermissionMode)
	}
	if d.MaxTurns > 0 {
		out = append(out, fmt.Sprintf("At most %d replies", d.MaxTurns))
	}
	if len(d.MCPServers) > 0 {
		var names []string
		for _, m := range d.MCPServers {
			if m.Inline != nil {
				names = append(names, m.Name+" (its own)")
			} else {
				names = append(names, m.Name+" (the session's)")
			}
		}
		out = append(out, "MCP servers: "+strings.Join(names, ", "))
	}
	if len(d.Skills) > 0 {
		out = append(out, "Skills it starts with: "+strings.Join(d.Skills, ", "))
	}
	if d.Isolation == "worktree" {
		out = append(out, "Works in a git work tree of its own")
	}
	if d.Background {
		out = append(out, "Runs in the background")
	}
	if d.Memory != "" {
		out = append(out, "Keeps a memory of its own: "+memoryScopeWords(d.Memory)+" ("+d.Memory+")")
	}
	switch {
	case d.CompactWindow > 0:
		out = append(out, fmt.Sprintf("Makes room at %s tokens of context, or the session's point where earlier (autoCompactWindow)", thousands(d.CompactWindow)))
	case d.CompactWindowUnused != "":
		out = append(out, fmt.Sprintf("Its autoCompactWindow, %s, is not used: it is a whole number of tokens, %s or more", d.CompactWindowUnused, thousands(agentdef.MinCompactWindow)))
	}
	if d.Hooks != nil {
		out = append(out, "Has hooks of its own")
	}
	if d.OmitInstructions {
		out = append(out, "Starts without the instruction files")
	}
	return out
}
