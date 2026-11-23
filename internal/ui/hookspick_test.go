package ui

import (
	"slices"
	"testing"

	"github.com/gnosh-ai/gnosh/internal/app"
	"github.com/gnosh-ai/gnosh/internal/text"
)

// /hooks with nothing after it browses the hooks in force and runs
// nothing: the moments they run at, a moment's hooks with their files,
// and one hook in full; Back goes up a level and Esc closes it. With no
// hook in force the command runs as before.
func TestHooksBrowser(t *testing.T) {
	g := newRig(t, 120, 30, text.NoColor, func(c *controls) {
		c.hooks = []app.HookChoice{
			{Event: "PreToolUse", Command: "~/.gnosh/hooks/no-rm.sh", Source: "~/.gnosh/settings.json", Details: []string{"Matches: Bash", "Only if: Bash(rm *)", "Timeout: 5s"}},
			{Event: "PreToolUse", Command: "POST http://203.0.113.7/hook", Source: ".gnosh/settings.json"},
			{Event: "Stop", Command: "say done", Source: ".gnosh/settings.local.json", Details: []string{"Runs in the background"}},
		}
		c.hooksNote = "2 more are configured and not in force (a folder that is not trusted, or an event this build does not fire); gnosh doctor says which."
		c.info.Commands = append(c.info.Commands, "hooks")
	})
	ran := func() []string {
		g.c.mu.Lock()
		defer g.c.mu.Unlock()
		return slices.Clone(g.c.commands)
	}
	g.keys(`"/hooks" <Enter>`)
	g.liveHas("The hooks in force", "PreToolUse", "2 hooks", "Stop", "say done", "2 more are configured", "Done")
	g.keys(`<Enter>`)
	g.liveHas("PreToolUse: 2 hooks.", "~/.gnosh/hooks/no-rm.sh", "~/.gnosh/settings.json", "POST http://203.0.113.7/hook", "Back")
	g.keys(`<Enter>`)
	g.liveHas("A PreToolUse hook.", "Runs: ~/.gnosh/hooks/no-rm.sh", "Matches: Bash", "Only if: Bash(rm *)", "Timeout: 5s", "From: ~/.gnosh/settings.json")
	g.keys(`<Enter>`) // Back
	g.liveHas("PreToolUse: 2 hooks.")
	g.keys(`<Down> <Down> <Enter>`) // Back again, to the moments
	g.liveHas("The hooks in force")
	g.keys(`<Down> <Enter> <Enter>`) // Stop, its one hook
	g.liveHas("A Stop hook.", "Runs: say done", "Runs in the background", "From: .gnosh/settings.local.json")
	g.keys(`<Esc>`)
	if g.s.q != nil {
		t.Fatal("Esc left the browser open")
	}
	if got := ran(); len(got) != 0 {
		t.Fatalf("the browser ran %q", got)
	}

	h := newRig(t, 100, 24, text.NoColor)
	h.keys(`"/hooks" <Enter>`)
	if h.s.q != nil {
		t.Fatal("with no hooks /hooks opened a browser")
	}
}
