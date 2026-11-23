package ui

import (
	"slices"
	"testing"

	"github.com/gnosh-ai/gnosh/internal/app"
	"github.com/gnosh-ai/gnosh/internal/text"
)

// /agents with nothing after it browses the kinds of sub-agent and runs
// nothing: each kind with what it is for, and one in full; Back goes up a
// level and Esc closes it. With no kind the command runs as before.
func TestAgentsBrowser(t *testing.T) {
	g := newRig(t, 120, 30, text.NoColor, func(c *controls) {
		c.agents = []app.AgentChoice{
			{Name: "general-purpose", Description: "Works on a task of many steps.", Source: "built-in", Details: []string{"Tools: every tool a sub-agent may have", "Model: the session's"}},
			{Name: "greeter", Description: "Greets people.", Source: "~/.config/gnosh/agents/greeter.md", Details: []string{"Tools: Read, mcp__greet__*", "Model: the session's", "MCP servers: greet (its own)"}},
		}
		c.agentsOn = true
		c.info.Commands = append(c.info.Commands, "agents")
	})
	ran := func() []string {
		g.c.mu.Lock()
		defer g.c.mu.Unlock()
		return slices.Clone(g.c.commands)
	}
	g.keys(`"/agents" <Enter>`)
	g.liveHas("The kinds of sub-agent the model can start", "general-purpose", "Works on a task of many steps.", "greeter", "Greets people.", "Done")
	g.keys(`<Down> <Enter>`)
	g.liveHas("The sub-agent greeter.", "Greets people.", "Tools: Read, mcp__greet__*", "MCP servers: greet (its own)", "From: ~/.config/gnosh/agents/greeter.md", "Back")
	g.keys(`<Enter>`) // Back
	g.liveHas("The kinds of sub-agent the model can start")
	g.keys(`<Esc>`)
	if g.s.q != nil {
		t.Fatal("Esc left the browser open")
	}
	if got := ran(); len(got) != 0 {
		t.Fatalf("the browser ran %q", got)
	}

	h := newRig(t, 100, 24, text.NoColor)
	h.keys(`"/agents" <Enter>`)
	if h.s.q != nil {
		t.Fatal("with no kinds /agents opened a browser")
	}
}
