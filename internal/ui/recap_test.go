package ui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/gnosh-ai/gnosh/internal/app"
	"github.com/gnosh-ai/gnosh/internal/core"
	"github.com/gnosh-ai/gnosh/internal/text"
)

// A session opened again shows where it left off: its last prompt and the
// model's last answer to it, once; one whose conversation is still on the
// screen shows nothing of it.
func TestRecapOfASessionOpenedAgain(t *testing.T) {
	trans := []app.TranscriptEntry{
		{Kind: "prompt", Message: core.UserText("an older prompt")},
		{Kind: "reply", Message: core.AssistantText("An older answer.")},
		{Kind: "prompt", Message: core.UserText("why is the test flaky?")},
		{Kind: "reply", Message: core.AssistantText("")},
		{Kind: "results", Message: core.UserText("the results")},
		{Kind: "reply", Message: core.AssistantText("It waits on a **timer**.")},
		{Kind: "prompt", Hidden: true, Message: core.UserText("a goal directive")},
	}
	g := newRig(t, 100, 24, text.NoColor, func(c *controls) {
		c.info.ID, c.info.ShowPast = "s1", true
		c.trans = trans
	})
	g.committedHas("Where this session left off (Ctrl+O shows all of it):", "> why is the test flaky?", "It waits on a timer.")
	g.committedLacks("an older prompt", "a goal directive", "An older answer.")
	g.keys(`"hi" <Enter>`)
	if n := strings.Count(strings.Join(g.committed, "\n"), "Where this session left off"); n != 1 {
		t.Errorf("the recap was shown %d times", n)
	}

	g = newRig(t, 100, 24, text.NoColor, func(c *controls) {
		c.info.ID = "s2"
		c.trans = trans
	})
	g.committedLacks("Where this session left off", "why is the test flaky?")
}

// The recap shows what was typed, as the transcript view does, not what
// came with it, and a long prompt (a big paste) only to its first lines.
func TestRecapOfALongPrompt(t *testing.T) {
	var long []string
	for i := 1; i <= 2000; i++ {
		long = append(long, fmt.Sprintf("pasted line %d", i))
	}
	prompt := core.UserText("look at this log:\n" + strings.Join(long, "\n"))
	prompt.Blocks = append(prompt.Blocks, core.Block{Type: core.BlockText, Text: "what a hook added"})
	g := newRig(t, 100, 24, text.NoColor, func(c *controls) {
		c.info.ID, c.info.ShowPast = "s1", true
		c.trans = []app.TranscriptEntry{
			{Kind: "prompt", Message: prompt},
			{Kind: "reply", Message: core.AssistantText("The log ends early.")},
		}
	})
	g.committedHas("> look at this log:", "pasted line 9", "… (Ctrl+O shows the rest)", "The log ends early.")
	g.committedLacks("pasted line 10", "pasted line 2000", "what a hook added")
}
