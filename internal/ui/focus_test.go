package ui

import (
	"slices"
	"testing"
	"time"

	"github.com/gnosh-ai/gnosh/internal/agent"
	"github.com/gnosh-ai/gnosh/internal/core"
	"github.com/gnosh-ai/gnosh/internal/text"
)

// In focus view a run of tool calls is one line in the scrollback, with
// how to see them when that fits beside it: the files read, the patterns
// searched for, the files edited with the lines added and taken out, how
// many failed. The model's words stand as they are, a thought is a line of
// its own only before them, and a call in flight shows in the live region
// under a line that sums up the run so far.
func TestFocusView(t *testing.T) {
	g := newRig(t, 100, 30, text.NoColor)
	g.s.setView("focus")
	g.keys(`"fix it" <Enter>`).start()
	g.ev(thought("look first")).wait(3*time.Second).ev(said("Looking.\n"), toolCall("c1", "Read"), toolCall("c2", "Grep"), replyEnd(core.StopToolUse))
	read := map[string]any{"file_path": "/home/jae/src/proj/a.go"}
	grep := map[string]any{"pattern": "func New"}
	g.ev(toolStart("c1", "Read", read), toolStart("c2", "Grep", grep))
	g.ev(toolEnd("c1", "Read", read, core.ToolResult{Content: []core.Block{core.Text("package a\n")}}, 0))
	g.liveHas("so far: Read 1 file", "Grep")
	g.ev(toolEnd("c2", "Grep", grep, core.ToolResult{Content: []core.Block{core.Text("a.go:3: func New")}}, 0))
	edit := map[string]any{"file_path": "/home/jae/src/proj/a.go", "old_string": "b", "new_string": "c\nd"}
	g.ev(request(2), toolCall("c3", "Edit"), replyEnd(core.StopToolUse), toolStart("c3", "Edit", edit), toolEnd("c3", "Edit", edit, core.ToolResult{
		Content: []core.Block{core.Text("edited")},
		Display: &core.Display{Kind: "diff", Path: "/home/jae/src/proj/a.go", Before: "a\nb\n", After: "a\nc\nd\n"},
	}, 0))
	bash := map[string]any{"command": "go test ./..."}
	g.ev(request(3), toolCall("c4", "Bash"), replyEnd(core.StopToolUse), toolStart("c4", "Bash", bash), toolEnd("c4", "Bash", bash, core.ToolResult{
		Content: []core.Block{core.Text("FAIL\n[exit code 1]")}, IsError: true, Meta: map[string]any{"exitCode": 1},
	}, 0))
	g.committedLacks("Read 1 file")
	g.ev(request(4), said("Fixed it.\n"), replyEnd(core.StopEndTurn), turnEnd(agent.EndCompleted), idle())
	g.once("Thought for 3s · Ctrl+O shows it").once("Looking.").once("Read 1 file, searched for 1 pattern, edited 1 file +2 -1 · 1 failed · Ctrl+O shows them").once("Fixed it.")
	g.committedLacks("Read a.go", "func New", "$ go test", "FAIL")
	g.show("focus view")

	// Out of focus view, the calls are written as they end again.
	g.s.setView("default")
	g.keys(`"again" <Enter>`).start()
	g.ev(toolCall("c5", "Read"), replyEnd(core.StopToolUse), toolStart("c5", "Read", read), toolEnd("c5", "Read", read, core.ToolResult{Content: []core.Block{core.Text("package a\n")}}, 0))
	g.committedHas("Read a.go")
}

// A run whose calls all failed says so; a fold under way is written when
// focus view is turned off, and a bare /focus is sent as /focus on or off
// by what the screen shows.
func TestFocusViewTurnedOver(t *testing.T) {
	g := newRig(t, 80, 24, text.NoColor)
	g.s.setView("focus")
	g.keys(`"try" <Enter>`).start()
	bash := map[string]any{"command": "false"}
	for _, id := range []string{"c1", "c2"} {
		g.ev(toolCall(id, "Bash"), replyEnd(core.StopToolUse), toolStart(id, "Bash", bash), toolEnd(id, "Bash", bash, core.ToolResult{
			Content: []core.Block{core.Text("[exit code 1]")}, IsError: true, Meta: map[string]any{"exitCode": 1},
		}, 0))
	}
	g.s.setView("default")
	g.frame().once("2 calls · all failed · Ctrl+O shows them")
	// Where the hint does not fit beside it, the line goes without.
	n := newRig(t, 40, 24, text.NoColor)
	n.s.setView("focus")
	n.keys(`"try" <Enter>`).start()
	read := map[string]any{"file_path": "/home/jae/src/proj/a.go"}
	n.ev(toolCall("c1", "Read"), replyEnd(core.StopToolUse), toolStart("c1", "Read", read), toolEnd("c1", "Read", read, core.ToolResult{Content: []core.Block{core.Text("x\n")}}, 0),
		toolCall("c2", "Bash"), toolStart("c2", "Bash", bash), toolEnd("c2", "Bash", bash, core.ToolResult{Content: []core.Block{core.Text("ok")}, Meta: map[string]any{"exitCode": 0}}, 0))
	n.s.setView("default")
	n.frame().once("Read 1 file, ran 1 command").committedLacks("Ctrl+O shows")
	g.s.setView("focus")
	g.keys(`"/focus" <Enter>`)
	g.frame()
	g.c.mu.Lock()
	sent := slices.Clone(g.c.commands)
	g.c.mu.Unlock()
	if len(sent) == 0 || sent[len(sent)-1] != "/focus off" {
		t.Errorf("a bare /focus in focus view was sent as %q, want /focus off", sent)
	}
}

// In focus view a call with a note for the person (the new problems an
// edit brought) is written whole, its note under it, not folded away.
func TestFocusViewKeepsNotes(t *testing.T) {
	g := newRig(t, 100, 30, text.NoColor)
	g.s.setView("focus")
	g.keys(`"fix it" <Enter>`).start()
	edit := map[string]any{"file_path": "/home/jae/src/proj/a.go", "old_string": "b", "new_string": "c"}
	g.ev(toolCall("c1", "Edit"), replyEnd(core.StopToolUse), toolStart("c1", "Edit", edit), toolEnd("c1", "Edit", edit, core.ToolResult{
		Content: []core.Block{core.Text("edited")},
		Display: &core.Display{Kind: "diff", Path: "/home/jae/src/proj/a.go", Before: "a\nb\n", After: "a\nc\n"},
		Notes:   []string{"Found 1 new diagnostic issue in 1 file (gopls)."},
	}, 0))
	g.committedHas("Edit a.go", "Found 1 new diagnostic issue in 1 file (gopls).")
}
