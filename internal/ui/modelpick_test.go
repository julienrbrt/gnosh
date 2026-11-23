package ui

import (
	"slices"
	"testing"
	"time"

	"github.com/gnosh-ai/gnosh/internal/agent"
	"github.com/gnosh-ai/gnosh/internal/app"
	"github.com/gnosh-ai/gnosh/internal/text"
)

// /model with nothing after it is a picker: the models with the one in
// use marked and the cursor on it; a model with thinking levels then asks
// which; what is picked is switched to with /model provider/model level.
// Esc keeps the model, and /model with a name switches as before.
func TestModelPicker(t *testing.T) {
	g := newRig(t, 100, 30, text.NoColor, func(c *controls) {
		c.models = []app.ModelChoice{
			{Ref: "qwen/qwen3.8-27b", Window: 262144, Levels: []string{"off", "on"}, Level: "off", Current: true},
			{Ref: "big/reasoner", Window: 131072, Levels: []string{"low", "medium", "high"}, Level: "high", State: "unloaded"},
			{Ref: "plain/model"},
		}
		c.info.Commands = append(c.info.Commands, "model")
	})
	ran := func() []string {
		g.c.mu.Lock()
		defer g.c.mu.Unlock()
		return slices.Clone(g.c.commands)
	}
	g.keys(`"/model" <Enter>`)
	g.liveHas("Which model? The choice is saved", "qwen/qwen3.8-27b", "in use · window 262k · thinking off (of off, on)",
		"big/reasoner", "thinking high (of low, medium, high)", "plain/model", "Keep the model as it is")
	// A model the server has not loaded says that a switch waits for it.
	g.liveHas("not loaded: the server loads it first")
	if g.s.q == nil {
		t.Fatal("/model opened no picker")
	}
	// Esc keeps the model: nothing runs.
	g.keys(`<Esc>`)
	if g.s.q != nil || len(ran()) != 0 {
		t.Fatalf("Esc left the picker open or ran %q", ran())
	}
	// The second model, and then its thinking: the cursor starts on the
	// level it had.
	g.keys(`"/model" <Enter> <Down> <Enter>`)
	g.liveHas("How is big/reasoner to think?", "what it would use")
	g.keys(`<Enter>`)
	if got := ran(); len(got) != 1 || got[0] != "/model big/reasoner high" {
		t.Fatalf("the picker ran %q", got)
	}
	// A model with no levels is switched to at once.
	g.keys(`"/model" <Enter> <Down> <Down> <Enter>`)
	if got := ran(); len(got) != 2 || got[1] != "/model plain/model" {
		t.Fatalf("the picker ran %q", got)
	}
	// The model in use, at the level it has: nothing to do.
	g.keys(`"/model" <Enter> <Enter> <Enter>`)
	g.liveHas("is the model in use already, thinking off")
	if got := ran(); len(got) != 2 {
		t.Fatalf("picking what is in use ran %q", got)
	}
	// With a name the command runs as it is typed.
	g.keys(`"/model big/reasoner low" <Enter>`)
	if got := ran(); len(got) != 3 || got[2] != "/model big/reasoner low" {
		t.Fatalf("/model with a name ran %q", got)
	}
}

// A permission question that comes while a picker is up waits behind it,
// and is still in line after the picker's second screen: the first screen's
// close put it up, and the second screen took its place without keeping
// it, so the turn waited for an answer nobody could give (the TUI review
// of 2026-11-19).
func TestQuestionWaitsThroughAPickersSecondScreen(t *testing.T) {
	g := newRig(t, 100, 30, text.NoColor, func(c *controls) {
		c.models = []app.ModelChoice{
			{Ref: "qwen/qwen3.8-27b", Window: 262144, Levels: []string{"off", "on"}, Level: "off", Current: true},
			{Ref: "big/reasoner", Window: 131072, Levels: []string{"low", "medium", "high"}, Level: "high"},
		}
		c.info.Commands = append(c.info.Commands, "model")
	})
	g.keys(`"/model" <Enter>`)
	g.liveHas("Which model? The choice is saved")
	g.ev(&agent.QuestionEvent{Question: permission("p1")})
	g.liveLacks("Allow Bash?")
	g.keys(`<Down> <Enter>`)
	g.liveHas("How is big/reasoner to think?")
	g.keys(`<Enter>`)
	if g.s.q == nil || g.s.q.q.ID != "p1" {
		t.Fatalf("after the picker the screen holds %+v and %d wait", g.s.q, len(g.s.qwait))
	}
	g.liveHas("Allow Bash?")
	g.wait(time.Second).keys(`<Enter>`)
	if len(g.c.answers) != 1 || g.c.answers[0].ans.Choice != "yes" {
		t.Fatalf("answers: %+v", g.c.answers)
	}
}
