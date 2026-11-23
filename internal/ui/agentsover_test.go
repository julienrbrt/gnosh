package ui

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/gnosh-ai/gnosh/internal/agent"
	"github.com/gnosh-ai/gnosh/internal/app"
	"github.com/gnosh-ai/gnosh/internal/text"
)

// agentControls are a test's controls that offer the agent view over the
// session, of the runs src has.
type agentControls struct {
	*controls
	src app.AgentSource
}

func (c agentControls) AgentView() (app.AgentViewInfo, error) {
	return app.AgentViewInfo{Source: c.src, Cwd: "/home/jae/src/app", Home: "/home/jae"}, nil
}

// ← in the empty box, as Claude Code's: the first says that a second
// opens the agent view, and the second opens it over the session, which
// goes on underneath: what it says meanwhile goes into the scrollback when
// Esc gives the screen back. Another key between the two starts over. A
// question of the session's closes the view, so that it is seen. Enter on
// a run that is over opens its session in place of this one (/resume).
// The status line counts the runs that wait on the person with a ←.
func TestAgentViewOverASession(t *testing.T) {
	g := newRig(t, 100, 24, text.NoColor)
	g.free = true
	src := newAgentRig(t).src
	g.s.c = agentControls{g.c, src}

	// For its first ten seconds the session says how to see the runs.
	g.frame().liveHas("← for agents")
	g.wait(11 * time.Second).liveLacks("for agents")
	g.ev(facts{"agents": "1 run needs input"})
	g.liveHas("← 1 run needs input").liveLacks("gnosh agents")

	g.keys(`<Left>`)
	g.liveHas("Press ← again for the agent view")
	if g.s.agents != nil {
		t.Fatal("one ← opened the agent view")
	}
	// Another key between them starts over.
	g.keys(`"a" <Backspace> <Left>`)
	if g.s.agents != nil {
		t.Fatal("← after another key opened the agent view")
	}
	g.wait(3 * time.Second)
	g.keys(`<Left> <Left>`)
	if g.s.agents == nil || !g.scr.AltScreenEver() {
		t.Fatal("← twice did not open the agent view")
	}
	g.viewHas("gnosh agents · 2 working · 2 completed", "run the tests and fix w", "deploy it")

	// The session goes on: what it says waits for the scrollback.
	g.ev(&agent.NoticeEvent{Level: agent.LevelWarning, Source: "test", Text: "a background command ended"})
	if strings.Contains(strings.Join(g.all(), "\n"), "a background command ended") {
		t.Fatalf("the notice was written while the view had the screen:\n%s", strings.Join(g.all(), "\n"))
	}
	g.keys(`<Esc>`)
	if g.s.agents != nil {
		t.Fatal("Esc did not close the agent view")
	}
	if all := strings.Join(g.all(), "\n"); !strings.Contains(all, "a background command ended") {
		t.Errorf("what the session said meanwhile is not in the scrollback:\n%s", all)
	}

	// A question closes it.
	g.wait(3 * time.Second)
	g.keys(`<Left> <Left>`)
	if g.s.agents == nil {
		t.Fatal("← twice did not open the agent view again")
	}
	g.ev(&agent.QuestionEvent{Question: permission("p1")})
	if g.s.agents != nil || g.s.q == nil {
		t.Fatal("a question did not close the agent view")
	}
	g.s.q = nil

	// Enter on a run that is over: its session, in place of this one.
	g.wait(3 * time.Second)
	g.keys(`<Left> <Left> <End> <Enter>`)
	if g.s.agents != nil {
		t.Fatal("Enter on a run that is over did not close the agent view")
	}
	g.c.mu.Lock()
	commands := slices.Clone(g.c.commands)
	g.c.mu.Unlock()
	if !slices.Contains(commands, "/resume "+runShip) {
		t.Errorf("the commands run: %q, want /resume %s", commands, runShip)
	}
}

// A custom status line (Claude Code's statusLine) keeps the runs that wait
// on the person in sight, on the line of the permission mode, as Claude
// Code's footer stays beside its status line; with no agent view to open,
// gnosh agents answers them.
func TestAgentsBesideACustomStatusLine(t *testing.T) {
	g := newRig(t, 100, 24, text.NoColor)
	g.s.custom = []string{"my status"}
	g.ev(facts{"agents": "1 run needs input"})
	g.liveHas("1 run needs input: gnosh agents", "my status")
	g.s.c = agentControls{g.c, newAgentRig(t).src}
	g.frame().liveHas("← 1 run needs input", "my status")
	g.ev(facts{"agents": ""})
	g.liveLacks("run needs input")
}
