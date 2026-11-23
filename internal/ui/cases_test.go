package ui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/gnosh-ai/gnosh/internal/agent"
	"github.com/gnosh-ai/gnosh/internal/app"
	"github.com/gnosh-ai/gnosh/internal/core"
	"github.com/gnosh-ai/gnosh/internal/hooks"
	"github.com/gnosh-ai/gnosh/internal/text"
)

// Several calls run side by side and end in another order than they
// began. Each is in the live region while it runs and in the scrollback
// once, when it ends.
func TestParallelToolCalls(t *testing.T) {
	each(t, func(t *testing.T, cols int) {
		g := newRig(t, cols, 24, text.NoColor)
		g.keys(`"look around" <Enter>`).start()
		read := func(p string) map[string]any { return map[string]any{"file_path": "/home/jae/src/proj/" + p} }
		grep := map[string]any{"pattern": "func New", "path": "/home/jae/src/proj/internal"}
		g.ev(said("Looking.\n"), toolCall("c1", "Read"), toolCall("c2", "Read"), toolCall("c3", "Grep"), replyEnd(core.StopToolUse))
		g.ev(toolStart("c1", "Read", read("a.go")), toolStart("c2", "Read", read("b.go")), toolStart("c3", "Grep", grep))
		g.liveHas("Read a.go", "Read b.go", "Grep func New under internal", "running 3 tool calls: Read, Read, Grep")
		g.committedLacks("Read a.go", "Read b.go", "Grep")
		g.ev(toolEnd("c3", "Grep", grep, core.ToolResult{Content: []core.Block{core.Text("a.go:3:func New()\n")},
			Meta: map[string]any{"numFiles": 2, "numMatches": 7}}, 0))
		g.once("Grep func New under internal").committedHas("7 matches in 2 files").liveLacks("Grep").liveHas("Read a.go", "Read b.go")
		g.ev(toolEnd("c1", "Read", read("a.go"), core.ToolResult{Content: []core.Block{core.Text("x\n")},
			Meta: map[string]any{"file": map[string]any{"numLines": 120, "totalLines": 400}}}, 0))
		g.ev(toolEnd("c2", "Read", read("b.go"), core.ToolResult{Content: []core.Block{core.Text("no such file")}, IsError: true}, 0))
		g.once("Read a.go", "Read b.go").committedHas("120 lines of 400", "failed", "no such file")
		g.liveLacks("Read a.go", "Read b.go")
		// Calls that are their heading alone stand one under the other,
		// with no blank line between.
		from, to := -1, -1
		for i, l := range g.committed {
			if strings.Contains(l, "Grep func New") {
				from = i
			}
			if strings.Contains(l, "Read a.go") {
				to = i
			}
		}
		for _, l := range g.committed[from:to] {
			if l == "" {
				t.Errorf("there is a blank line between calls that are one line each:\n%s", strings.Join(g.committed[from:to+1], "\n"))
			}
		}
		g.show("parallel calls")
	})
}

// An edit is shown as a diff: the path, the counts, and every changed
// line with its sign.
func TestEditDiff(t *testing.T) {
	before := "package a\n\nfunc Old() int {\n\treturn 1\n}\n"
	after := "package a\n\nfunc New() int {\n\treturn 2\n}\n\nfunc Other() {}\n"
	each(t, func(t *testing.T, cols int) {
		for _, profile := range []text.Profile{text.NoColor, text.TrueColor} {
			g := newRig(t, cols, 30, profile)
			g.keys(`"rename it" <Enter>`).start()
			args := map[string]any{"file_path": "/home/jae/src/proj/a.go", "old_string": "Old", "new_string": "New"}
			g.ev(toolCall("c1", "Edit"), replyEnd(core.StopToolUse), toolStart("c1", "Edit", args))
			g.liveHas("Edit a.go")
			g.ev(toolEnd("c1", "Edit", args, core.ToolResult{
				Content: []core.Block{core.Text("edited")},
				Display: &core.Display{Kind: "diff", Path: "/home/jae/src/proj/a.go", Before: before, After: after},
			}, 0))
			g.once("Edit a.go +4 -2").committedHas("- func Old() int {", "+ func New() int {", "+ func Other() {}")
			g.committedLacks("/home/jae/src/proj/a.go")
			g.liveLacks("Edit a.go")
			g.show("an edit")
		}
	})
}

// A command that prints a flood: the live region shows its last lines and
// stays small; the scrollback gets the end of the output, a count of what
// was left out, and how the command ended.
func TestBashLongOutput(t *testing.T) {
	each(t, func(t *testing.T, cols int) {
		g := newRig(t, cols, 24, text.NoColor)
		g.keys(`"run the tests" <Enter>`).start()
		args := map[string]any{"command": "go test ./... 2>&1\necho done", "description": "run the tests"}
		g.ev(toolCall("c1", "Bash"), replyEnd(core.StopToolUse), toolStart("c1", "Bash", args))
		var all strings.Builder
		for i := 0; i < 3000; i++ {
			line := fmt.Sprintf("ok  \texample.com/pkg%04d\t0.0%ds\n", i, i%10)
			all.WriteString(line)
			g.s.handle(progress("c1", line))
			if i%500 == 499 {
				g.frame()
				g.liveHas(fmt.Sprintf("pkg%04d", i)).liveLacks(fmt.Sprintf("pkg%04d", i-5))
			}
		}
		if n := len(g.live); n > 12 {
			t.Fatalf("the live region has %d rows while a command floods\n%s", n, g.dump())
		}
		g.committedLacks("pkg0001")
		g.ev(toolEnd("c1", "Bash", args, core.ToolResult{
			Content: []core.Block{core.Text(all.String() + "done\n")},
			Meta:    map[string]any{"exitCode": 0, "durationMs": 4200},
		}, 4200*time.Millisecond))
		g.once("$ go test ./... 2>&1", "echo done", "pkg2999", "exit 0 · 4s").committedHas("2,996 earlier lines not shown")
		g.committedLacks("pkg2990")
		g.liveLacks("pkg2999")

		// A command that fails says so in words.
		args = map[string]any{"command": "false"}
		g.ev(toolStart("c2", "Bash", args), toolEnd("c2", "Bash", args, core.ToolResult{
			Content: []core.Block{core.Text("[exit code 1]")}, IsError: true, Meta: map[string]any{"exitCode": 1},
		}, 0))
		g.once("$ false").committedHas("failed · exit 1")
		g.committedLacks("[exit code 1]")
		// And one that printed nothing has its end on its own line.
		args = map[string]any{"command": "mkdir -p out"}
		g.ev(toolStart("c3", "Bash", args), toolEnd("c3", "Bash", args, core.ToolResult{
			Content: []core.Block{core.Text("")}, Meta: map[string]any{"exitCode": 0},
		}, 0))
		g.once("$ mkdir -p out · exit 0")
		g.show("commands")
	})
}

// A Gno call is headed by its code, whole, and shows mem's receipt.
func TestGnoCall(t *testing.T) {
	g := newRig(t, 80, 30, text.NoColor)
	g.keys(`"plan it" <Enter>`).start()
	code := "t := mem.Todo(\"write the tests\")\nmem.Push(t)\nprintln(mem.Next())"
	args := map[string]any{"name": "mem", "code": code}
	g.ev(toolCall("c1", "gnovm_run"), toolInput("c1", `{"name":"mem","code":"t := mem.Todo(\"write the tests\")\nmem.Pu`))
	g.liveHas("gnovm_run", "mem.Pu")
	g.ev(replyEnd(core.StopToolUse), toolStart("c1", "gnovm_run", args), toolEnd("c1", "gnovm_run", args,
		core.ToolResult{Content: []core.Block{core.Text("write the tests\nok: 1 todo pushed, entry 12, 3ms\n")}}, 0))
	g.once("t := mem.Todo(\"write the tests\")", "mem.Push(t)", "println(mem.Next())", "ok: 1 todo pushed")
	g.committedLacks("gnovm_run mem")
}

// permission is a question as the gate puts it.
func permission(id string) agent.Question {
	return agent.Question{
		ID: id, Kind: "permission", Title: "Allow Bash?",
		Text: "  $ rm -rf build\nWhy gnosh asks: the command changes files, and no rule allows it.",
		Choices: []agent.Choice{
			{Key: "yes", Label: "Yes, this once"},
			{Key: "always:0", Label: "Yes, and don't ask again for Bash(rm -rf build) (kept in .gnosh/settings.local.json)"},
			{Key: "no", Label: "No (alone it stops the turn; with words after it, the model reads them and carries on)"},
		},
		CallID: "c1", Tool: "Bash",
		Action: &core.Action{Kind: core.ActionExecute, Command: "rm -rf build"},
	}
}

// A permission question is answered each way it can be. A draft that was
// in the box when the question came is still there afterwards, and keys
// typed before the question could be read do not answer it.
func TestPermissionQuestion(t *testing.T) {
	cases := []struct {
		name   string
		keys   string
		choice string
		words  string
		stops  int
		record string
	}{
		{"yes with Enter", `<Enter>`, "yes", "", 0, "? Allow Bash? Yes, this once"},
		{"yes by number", `"1"`, "yes", "", 0, "? Allow Bash? Yes, this once"},
		{"and do not ask again", `"2"`, "always:0", "", 0, "don't ask again for Bash(rm -rf build)"},
		{"no", `"3"`, "no", "", 0, "? Allow Bash? No"},
		{"no with Esc", `<Esc>`, "no", "", 0, "? Allow Bash? No"},
		{"no with words", `"4" "use the trash instead" <Enter>`, "no", "use the trash instead", 0, "No: use the trash instead"},
		{"no with a note", `<Down> <Down> <Tab> "ask me first" <Enter>`, "no", "ask me first", 0, "No: ask me first"},
	}
	each(t, func(t *testing.T, cols int) {
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				g := newRig(t, cols, 24, text.NoColor)
				g.keys(`"clean up" <Enter>`).start()
				args := map[string]any{"command": "rm -rf build"}
				g.ev(toolCall("c1", "Bash"), replyEnd(core.StopToolUse), toolStart("c1", "Bash", args))
				g.keys(`"half a thought"`)
				g.ev(&agent.QuestionEvent{Question: permission("q1")})
				g.liveHas("Allow Bash?", "$ rm -rf build", "Why gnosh asks", "Yes, this once", "No, and tell the model", "half a thought")
				g.liveHas("* waiting for your answer")
				g.show("a permission question")
				// Typed at once: it was on its way before the question
				// was drawn, and belongs to the draft.
				g.keys(`"1"`)
				if len(g.c.answers) != 0 {
					t.Fatalf("a key typed before the question could be read answered it: %+v", g.c.answers)
				}
				if g.box() != "half a thought1" {
					t.Fatalf("the box holds %q", g.box())
				}
				g.keys(`<Backspace>`)
				g.wait(2 * time.Second)
				g.keys(tc.keys)
				if len(g.c.answers) != 1 {
					t.Fatalf("%d answers were given, want one\n%s", len(g.c.answers), g.dump())
				}
				if got := g.c.answers[0]; got.id != "q1" || got.ans.Choice != tc.choice || got.ans.Text != tc.words {
					t.Fatalf("answered %+v, want choice %q with %q", got, tc.choice, tc.words)
				}
				g.liveLacks("Allow Bash?", "Yes, this once")
				g.once(tc.record)
				g.ev(&agent.AnswerEvent{ID: "q1", Answer: g.c.answers[0].ans})
				// The draft is as it was, and the keys go to it again.
				if g.box() != "half a thought" {
					t.Fatalf("after the question the box holds %q", g.box())
				}
				g.keys(`" more"`)
				if g.box() != "half a thought more" {
					t.Fatalf("the box does not take keys after the question: %q", g.box())
				}
				if g.c.stops != tc.stops {
					t.Errorf("the turn was stopped %d times, want %d", g.c.stops, tc.stops)
				}
			})
		}
	})
}

// A question about an edit shows the change as a diff, and Shift+Tab on
// it accepts edits for the rest of the session.
func TestEditQuestion(t *testing.T) {
	q := agent.Question{
		ID: "q1", Kind: "permission", Title: "Allow Edit?",
		Text: "  /home/jae/src/proj/a.go\nWhy gnosh asks: the file would be changed.",
		Choices: []agent.Choice{
			{Key: "yes", Label: "Yes, this once"},
			{Key: "always:0", Label: "Yes, and don't ask again this session for Edit(/a.go)"},
			{Key: "always:1", Label: "Yes, and switch to acceptEdits for the rest of the session"},
			{Key: "no", Label: "No"},
		},
		Action: &core.Action{Kind: core.ActionEdit, Paths: []string{"/home/jae/src/proj/a.go"},
			Preview: &core.Display{Kind: "diff", Path: "a.go", Before: "one\ntwo\n", After: "one\nthree\n"}},
	}
	each(t, func(t *testing.T, cols int) {
		g := newRig(t, cols, 30, text.NoColor)
		g.keys(`"edit" <Enter>`).start()
		g.ev(&agent.QuestionEvent{Question: q})
		g.liveHas("Allow Edit?", "- two", "+ three", "a.go", "+1 -1")
		g.wait(time.Second).keys(`<Shift+Tab>`)
		if len(g.c.answers) != 1 || g.c.answers[0].ans.Choice != "always:1" {
			t.Fatalf("Shift+Tab answered %+v", g.c.answers)
		}
		g.once("switch to acceptEdits for the rest of the session")
	})
}

// A note typed under "No" stays when the window is resized: the question
// with a diff is laid out again for the new width, and the note was lost,
// so Enter sent a bare no and stopped the turn (the TUI review of
// 2026-11-19).
func TestNoteOnNoOutlivesAResize(t *testing.T) {
	q := agent.Question{
		ID: "q1", Kind: "permission", Title: "Allow Edit?",
		Text: "  /home/jae/src/proj/a.go\nWhy gnosh asks: the file would be changed.",
		Choices: []agent.Choice{
			{Key: "yes", Label: "Yes, this once"},
			{Key: "no", Label: "No"},
		},
		Action: &core.Action{Kind: core.ActionEdit, Paths: []string{"/home/jae/src/proj/a.go"},
			Preview: &core.Display{Kind: "diff", Path: "a.go", Before: "one\ntwo\n", After: "one\nthree\n"}},
	}
	g := newRig(t, 100, 30, text.NoColor)
	g.keys(`"edit" <Enter>`).start()
	g.ev(&agent.QuestionEvent{Question: q})
	g.liveHas("Allow Edit?", "+ three")
	g.wait(time.Second).keys(`<Down> <Tab> "use a map instead" <Esc>`)
	g.liveHas("use a map instead")
	g.resize(80, 30)
	g.liveHas("Allow Edit?", "use a map instead")
	g.keys(`<Enter>`)
	if len(g.c.answers) != 1 || g.c.answers[0].ans.Choice != "no" || g.c.answers[0].ans.Text != "use a map instead" {
		t.Fatalf("after the resize Enter answered %+v", g.c.answers)
	}
}

// A question whose title fills the row is recorded with its title on a row
// of its own and the answer whole under it: beside it, the answer lost its
// first letters (the TUI review of 2026-11-19: "es: use this folder's
// settings").
func TestLongTitleKeepsItsAnswerWhole(t *testing.T) {
	title := "Trust /home/jae/src/a-folder-with-a-long-name? The session moves there only." // 76 cells
	g := newRig(t, 80, 30, text.NoColor)
	g.keys(`"move" <Enter>`).start()
	g.ev(&agent.QuestionEvent{Question: agent.Question{ID: "q1", Kind: "trust", Title: title, Text: "Do you trust it?",
		Choices: []agent.Choice{{Key: "no", Label: "No: stay where you are"}, {Key: "yes", Label: "Yes: use this folder's settings, and move there"}}}})
	g.wait(time.Second).keys(`<Down> <Enter>`)
	g.committedHas("? "+title, "  Yes: use this folder's settings, and move there")
}

// A paste while a question or the transcript view has the keys goes into
// the prompt box and says so: it vanished (the TUI review of 2026-11-19).
func TestPasteWhileSomethingElseHasTheKeys(t *testing.T) {
	g := newRig(t, 80, 30, text.NoColor)
	g.free = true // (the transcript view takes the whole screen)
	g.keys(`"go" <Enter>`).start()
	g.ev(&agent.QuestionEvent{Question: permission("q1")})
	g.wait(time.Second)
	g.s.input(pasteOf("the log says"))
	g.frame()
	if got := g.box(); got != "the log says" {
		t.Fatalf("a paste at the question left the box %q", got)
	}
	g.liveHas("Allow Bash?", "the log says", "The paste went into the prompt box")
	if len(g.c.answers) != 0 {
		t.Fatalf("the paste answered the question: %+v", g.c.answers)
	}
	g.keys(`<Enter>`)
	g.ev(&agent.AnswerEvent{ID: "q1"})
	g.keys(`<Ctrl+U> <Ctrl+O>`)
	g.s.input(pasteOf("a note"))
	g.frame()
	g.keys(`<Esc>`)
	if got := g.box(); got != "a note" {
		t.Fatalf("a paste in the transcript view left the box %q", got)
	}
}

// A command holding a character drawn as nothing, or one that turns the
// drawing order around, shows it as its code point in the question that
// asks to let it run (the TUI review of 2026-11-19).
func TestQuestionShowsInvisibleCharacters(t *testing.T) {
	q := permission("q1")
	q.Text = "  $ cat /tmp/\u202etxt.exe\u200b\nWhy gnosh asks: no rule allows it."
	g := newRig(t, 80, 30, text.NoColor)
	g.keys(`"go" <Enter>`).start()
	g.ev(&agent.QuestionEvent{Question: q})
	g.liveHas("$ cat /tmp/<U+202E>txt.exe<U+200B>")
}

// A question that is taken back, because the turn was stopped or nobody
// answered in time, leaves the screen and says so.
func TestQuestionWithdrawn(t *testing.T) {
	g := newRig(t, 80, 24, text.NoColor)
	g.keys(`"go" <Enter>`).start()
	g.ev(&agent.QuestionEvent{Question: permission("q1")})
	g.liveHas("Allow Bash?")
	g.ev(&agent.AnswerEvent{ID: "q1", Withdrawn: true})
	g.liveLacks("Allow Bash?").once("? Allow Bash? taken back unanswered: the turn was stopped")

	// One with a time limit counts down, starts on no, and says why it went.
	q := permission("q2")
	q.Data = deadline(g.now.Add(2 * time.Minute))
	g.ev(&agent.QuestionEvent{Question: q})
	g.liveHas("answered as no in 2m 00s")
	g.wait(30 * time.Second).liveHas("answered as no in 1m 30s")
	g.keys(`<Enter>`)
	if len(g.c.answers) != 1 || g.c.answers[0].ans.Choice != "no" {
		t.Fatalf("Enter on a question with a time limit answered %+v, want no", g.c.answers)
	}
	g.ev(&agent.QuestionEvent{Question: q})
	g.wait(121 * time.Second)
	g.ev(&agent.AnswerEvent{ID: "q2", Withdrawn: true})
	g.committedHas("taken back unanswered: nobody answered in time")
}

// A question answered from elsewhere (a channel that relays permission
// questions) leaves the screen in one line that says who answered and
// what; a key pressed a moment after does not count, and is not written
// as the answer.
func TestQuestionAnsweredByAChannel(t *testing.T) {
	g := newRig(t, 80, 24, text.NoColor)
	g.keys(`"go" <Enter>`).start()
	g.ev(&agent.QuestionEvent{Question: permission("q1")})
	g.liveHas("Allow Bash?")
	g.ev(&agent.AnswerEvent{ID: "q1", Answer: agent.Answer{Choice: "yes", By: "the channel hook"}})
	g.liveLacks("Allow Bash?").once("? Allow Bash? answered by the channel hook: Yes, this once")

	// The channel's answer counted first; the person's no, a moment
	// later, is not written as what was answered.
	g.ev(&agent.QuestionEvent{Question: permission("q2")})
	g.c.mu.Lock()
	g.c.answerErr = errors.New("no such question")
	g.c.mu.Unlock()
	g.keys(`<Esc>`)
	g.committedLacks("Allow Bash? No")
	g.ev(&agent.AnswerEvent{ID: "q2", Answer: agent.Answer{Choice: "yes", By: "the channel hook"}})
	if n := strings.Count(g.scroll(), "answered by the channel hook: Yes, this once"); n != 2 {
		t.Errorf("the channel's answers were written %d times, want 2:\n%s", n, g.scroll())
	}
}

// deadline is the Data of a question with a time limit.
type deadline time.Time

func (d deadline) Deadline() time.Time { return time.Time(d) }

// The model's own questions are a form with one tab each; a secret is a
// masked line that leaves nothing on the screen.
func TestFormAndSecret(t *testing.T) {
	g := newRig(t, 80, 30, text.NoColor)
	g.keys(`"ask me" <Enter>`).start()
	data := app.QuestionForm{Questions: []app.FormQuestion{
		{Question: "Which database?", Header: "Database", Options: []app.FormOption{{Label: "SQLite", Description: "one file"}, {Label: "Postgres", Description: "a server"}}},
		{Question: "Which checks?", Header: "Checks", Options: []app.FormOption{{Label: "vet"}, {Label: "race"}, {Label: "fuzz"}}, MultiSelect: true},
	}}
	g.ev(&agent.QuestionEvent{Question: agent.Question{ID: "q1", Kind: "question", Title: "Questions", Data: data}})
	g.liveHas("Which database?", "SQLite", "Postgres", "Database", "Checks")
	g.show("the model's questions")
	g.wait(time.Second)
	g.keys(`<Down> <Enter>`)                 // Postgres, and on to the next
	g.keys(`<Space> <Down> <Space> <Enter>`) // vet and race, and on to the review
	g.liveHas("Review your answers")
	g.keys(`<Enter>`)
	if len(g.c.answers) != 1 {
		t.Fatalf("%d answers", len(g.c.answers))
	}
	got, _ := g.c.answers[0].ans.Data.(map[string]string)
	if got["Which database?"] != "Postgres" || got["Which checks?"] != "vet, race" {
		t.Fatalf("the form answered %v", got)
	}
	g.once("Database: Postgres", "Checks: vet, race")

	g.ev(&agent.AnswerEvent{ID: "q1"})
	g.ev(&agent.QuestionEvent{Question: agent.Question{ID: "q2", Kind: "secret", Title: "Password for the key test1", Text: "It signs: gnokey maketx call ..."}})
	g.wait(time.Second)
	g.keys(`"hunter2"`)
	g.liveHas("Password for the key test1", "*******").liveLacks("hunter2")
	g.keys(`<Enter>`)
	if n := len(g.c.answers); n != 2 || g.c.answers[1].ans.Text != "hunter2" {
		t.Fatalf("the secret was answered as %+v", g.c.answers)
	}
	g.committedLacks("hunter2").once("answered (what was typed is not shown)")
	if strings.Contains(g.scr.Dump(), "hunter2") {
		t.Fatalf("the secret is on the screen\n%s", g.dump())
	}

	// Esc on a question that has no "no" stops the turn.
	g.ev(&agent.AnswerEvent{ID: "q2"})
	g.ev(&agent.QuestionEvent{Question: agent.Question{ID: "q3", Kind: "question", Title: "Go on?", Choices: []agent.Choice{{Key: "a", Label: "This way"}, {Key: "b", Label: "That way"}}}})
	g.wait(time.Second).keys(`<Esc>`)
	if g.c.stops != 1 || len(g.c.answers) != 2 {
		t.Fatalf("Esc on a question: %d stops, %d answers", g.c.stops, len(g.c.answers))
	}
	g.liveHas("stopping the turn")
}

// A prompt sent while a turn runs is queued and shown. Up in an empty box
// takes the queue back for editing; Enter sends it again; and a prompt
// taken back can be dropped.
func TestQueue(t *testing.T) {
	each(t, func(t *testing.T, cols int) {
		g := newRig(t, cols, 24, text.NoColor)
		g.keys(`"first" <Enter>`).start().ev(said("Working on it. "))
		g.keys(`"second thing" <Enter>`)
		g.liveHas("queued: second thing")
		g.c.queue = queueAs("p2", "second thing")
		g.ev(&agent.QueueEvent{Queued: g.c.queue})
		g.liveHas("queued: second thing")
		if n := strings.Count(g.region(), "queued: second thing"); n != 1 {
			t.Fatalf("the queued prompt is shown %d times\n%s", n, g.dump())
		}
		g.keys(`"/compact" <Enter>`)
		g.liveHas("queued: /compact")
		if len(g.c.commands) != 0 {
			t.Fatalf("a command typed during a turn ran at once: %q", g.c.commands)
		}
		// Up takes all of it back, the prompts first.
		g.keys(`<Up>`)
		if want := "second thing\n\n/compact"; g.box() != want {
			t.Fatalf("the box holds %q, want %q", g.box(), want)
		}
		g.liveLacks("queued:")
		g.ev(queued())
		// Edited and sent again as one prompt.
		g.keys(`<Ctrl+U> <Backspace> <Backspace> " and a third" <Enter>`)
		if got := g.c.sent(); len(got) != 3 || got[2] != "second thing and a third" {
			t.Fatalf("submitted %q", got)
		}
		// Taken back once more and dropped: nothing is sent, and the
		// draft can still be had with Up.
		g.c.queue = queueAs("p3", "second thing and a third")
		g.ev(&agent.QueueEvent{Queued: g.c.queue})
		g.keys(`<Up>`).ev(queued())
		g.keys(`<Ctrl+C>`) // busy: stops the turn, and leaves the box alone
		if g.box() != "second thing and a third" || g.c.stops != 1 {
			t.Fatalf("Ctrl+C while busy: the box holds %q, %d stops", g.box(), g.c.stops)
		}
		g.ev(turnEnd(agent.EndStopped), idle())
		g.once("stopped by you")
		g.keys(`<Esc> <Esc>`)
		if g.box() != "" || len(g.c.sent()) != 3 {
			t.Fatalf("after Esc Esc the box holds %q and %d prompts were sent", g.box(), len(g.c.sent()))
		}
		g.keys(`<Up>`)
		if g.box() != "second thing and a third" {
			t.Fatalf("Up after clearing brought back %q", g.box())
		}
	})
}

// A prompt that comes back, because a hook refused it, is in the box
// again, in front of what was typed since, with the reason in the
// scrollback.
func TestPromptReturned(t *testing.T) {
	g := newRig(t, 80, 24, text.NoColor)
	g.keys(`"deploy it" <Enter>`)
	g.keys(`"and then"`)
	g.ev(busy(), &agent.PromptReturnedEvent{ID: "p1", Prompt: agent.Prompt{Text: "deploy it"}, Why: agent.ReturnRefused,
		Reason: "your UserPromptSubmit hook refused it: no deploys on Friday"}, idle())
	if want := "deploy it\n\nand then"; g.box() != want {
		t.Fatalf("the box holds %q, want %q", g.box(), want)
	}
	g.once("Your prompt was not sent: your UserPromptSubmit hook refused it: no deploys on Friday", "It is back in the prompt box.")
	g.liveLacks("queued:")
	g.committedLacks("> deploy it")

	// A prompt the session would not take at all comes back at once.
	g.keys(`<Ctrl+U>*4 <Ctrl+C>`)
	g.c.submitErr = errors.New("the session is closing")
	g.keys(`"one more" <Enter>`)
	if g.box() != "one more" {
		t.Fatalf("the box holds %q", g.box())
	}
	g.committedHas("The prompt was not sent: the session is closing")
}

// Esc stops the turn at once at every point of it, and says so. What was
// typed stays.
func TestEscEverywhere(t *testing.T) {
	bash := map[string]any{"command": "sleep 100"}
	points := map[string][]any{
		"waiting for the model": {},
		"thinking":              {thought("Hmm, let me see. ")},
		"writing":               {said("The answer begins ")},
		"writing a call":        {toolCall("c1", "Bash"), toolInput("c1", `{"command":"sle`)},
		"running a tool":        {toolCall("c1", "Bash"), replyEnd(core.StopToolUse), toolStart("c1", "Bash", bash), progress("c1", "tick\n")},
		"waiting to retry":      {&agent.RetryEvent{Purpose: "main", Attempt: 1, Wait: 8 * time.Second, Kind: core.ErrNetwork, Message: "local/qwen3.8-27b could not be reached. Trying again in 8s."}},
		"a summary":             {&agent.SummaryEvent{Phase: "start", Trigger: "auto", PreTokens: 250000}},
	}
	for name, evs := range points {
		t.Run(name, func(t *testing.T) {
			g := newRig(t, 80, 24, text.NoColor)
			g.keys(`"go" <Enter>`).start().ev(evs...)
			g.keys(`"keep this"`)
			g.keys(`<Esc>`)
			if g.c.stops != 1 {
				t.Fatalf("Esc stopped the turn %d times", g.c.stops)
			}
			g.liveHas("* stopping the turn")
			if g.box() != "keep this" {
				t.Fatalf("Esc changed the box to %q", g.box())
			}
			// What the session does when it is stopped.
			if name == "running a tool" {
				end := toolEnd("c1", "Bash", bash, core.ToolResult{Content: []core.Block{core.Text("tick\n")}, IsError: true}, 2*time.Second)
				end.Interrupted = true
				g.ev(end)
				g.once("$ sleep 100").committedHas("stopped")
			}
			if name == "writing" || name == "writing a call" || name == "thinking" {
				g.ev(replyEnd(core.StopCancelled))
			}
			g.ev(turnEnd(agent.EndStopped), idle())
			g.once("stopped by you")
			g.committedLacks("context canceled", "context cancelled")
			g.liveLacks("stopping the turn", "* ").liveHas("idle")
			if name == "writing" {
				g.once("The answer begins")
			}
			if name == "writing a call" {
				g.committedHas("not run: the reply was cut off before this call was complete")
			}
			if g.box() != "keep this" {
				t.Fatalf("after the turn was stopped the box holds %q", g.box())
			}
		})
	}

	// Idle: the first Esc warns, the second clears, and nothing is stopped.
	g := newRig(t, 80, 24, text.NoColor)
	g.keys(`"some words" <Esc>`)
	g.liveHas("Esc again to clear the box")
	g.keys(`<Esc>`)
	if g.box() != "" || g.c.stops != 0 {
		t.Fatalf("Esc Esc when idle: the box holds %q, %d stops", g.box(), g.c.stops)
	}
	// Esc Esc on an empty box asks for a rewind, which this build has not.
	g.keys(`<Esc> <Esc>`)
	g.liveHas("not joined to this build yet")
}

// Ctrl+C and Ctrl+D leave, on their second press and not on their first.
func TestLeaving(t *testing.T) {
	g := newRig(t, 80, 24, text.NoColor)
	g.keys(`"draft" <Ctrl+C>`)
	if g.s.done || g.box() != "" {
		t.Fatalf("the first Ctrl+C: done %v, the box holds %q", g.s.done, g.box())
	}
	g.liveHas("Ctrl+C again to leave gnosh")
	g.wait(2 * time.Second).liveLacks("Ctrl+C again")
	g.keys(`<Ctrl+C>`)
	if g.s.done {
		t.Fatal("a Ctrl+C long after the first left gnosh")
	}
	g.keys(`<Ctrl+C>`)
	if !g.s.done || g.s.code != 0 || g.s.why != "prompt_input_exit" {
		t.Fatalf("Ctrl+C Ctrl+C: done %v, code %d, reason %q", g.s.done, g.s.code, g.s.why)
	}

	g = newRig(t, 80, 24, text.NoColor)
	g.keys(`<Ctrl+D>`).liveHas("Ctrl+D again to leave gnosh")
	if g.s.done {
		t.Fatal("the first Ctrl+D left gnosh")
	}
	g.keys(`<Ctrl+D>`)
	if !g.s.done {
		t.Fatal("Ctrl+D Ctrl+D did not leave gnosh")
	}

	// With commands running in the background, the first press says they
	// stop with gnosh.
	g = newRig(t, 100, 24, text.NoColor)
	g.ev(facts{"background": "2"})
	g.keys(`<Ctrl+D>`).liveHas("Ctrl+D again to leave gnosh: 2 commands running in the background stop with it")
	g = newRig(t, 100, 24, text.NoColor)
	g.ev(facts{"background": "1"})
	g.keys(`<Ctrl+C>`).liveHas("Ctrl+C again to leave gnosh: 1 command running in the background stops with it")

	g = newRig(t, 80, 24, text.NoColor)
	g.keys(`"/exit" <Enter>`)
	if !g.s.done {
		t.Fatal("/exit did not leave gnosh")
	}
}

// A model server that fails: the first wait is written into the
// scrollback, the line above the box counts down, the reason is not
// repeated while it stays the same, and when the server answers again
// that is said.
func TestRetries(t *testing.T) {
	each(t, func(t *testing.T, cols int) {
		g := newRig(t, cols, 24, text.NoColor)
		g.keys(`"go" <Enter>`).start()
		msg := "local/qwen3.8-27b at http://203.0.113.7:8080 could not be reached (connection refused). Trying again in 8s; it may be restarting or loading a model, and gnosh waits up to 5m for it to come back. Esc stops the wait."
		retry := func(wait time.Duration, discarded bool) any {
			return &agent.RetryEvent{Purpose: "main", Attempt: 1, Wait: wait, Kind: core.ErrNetwork, Message: msg, Discarded: discarded}
		}
		g.ev(retry(8*time.Second, false))
		g.once("warning: local/qwen3.8-27b at http://203.0.113.7:8080")
		g.liveHas("again in 8s")
		g.wait(3 * time.Second)
		if cols >= 80 {
			g.liveHas("again in 5s · 3s")
		}
		g.wait(5*time.Second).ev(request(1), retry(16*time.Second, false))
		g.once("warning: local/qwen3.8-27b at http://203.0.113.7:8080")
		g.liveHas("again in 16s (try 3)")
		g.wait(16*time.Second).ev(request(1), said("Hello "))
		g.once("qwen3.8-27b answers again, after 2 failed tries and 24s.")
		// A reply that had begun is thrown away by the session; what was
		// shown of it stays, with a line that says what it is.
		g.ev(said("there, this is half of an ans"), retry(time.Second, true))
		g.once("Hello there, this is half of an ans").once("The reply above was cut off when the request failed")
		g.liveLacks("half of an ans")
		g.wait(time.Second).ev(request(1), said("Hello there, this is the whole answer.\n"), replyEnd(core.StopEndTurn), turnEnd(agent.EndCompleted), idle())
		g.once("this is the whole answer")

		// A server that stays gone ends the turn with its own words.
		g.keys(`"again" <Enter>`).start()
		g.ev(retry(time.Second, false), &agent.TurnEndEvent{Result: agent.TurnResult{Reason: agent.EndFailed,
			Err: errors.New("local/qwen3.8-27b at http://203.0.113.7:8080 is not running: it has not answered once in 1m 30s. Start it, or pick another model with /model. The turn has ended; the conversation is kept, and what you queued still runs.")}}, idle())
		g.once("error: local/qwen3.8-27b at http://203.0.113.7:8080 is not running")
		g.liveHas("idle").liveLacks("* ")
		g.show("retries")
	})
}

// A turn that goes on with a fallback model says so once and waits for
// nothing: no countdown, and no word later that the fallback "answers
// again", since it never failed. What had arrived of the failed reply is
// marked as cut off.
func TestFallbackSwitch(t *testing.T) {
	g := newRig(t, 100, 24, text.NoColor)
	g.keys(`"go" <Enter>`).start()
	msg := "local/qwen3.8-27b at http://203.0.113.7:8080 could not be reached (connection refused). This turn goes on with the fallback model box2 (qwen3.8-27b); the next turn asks local/qwen3.8-27b first again."
	g.ev(request(1), said("half of a"), &agent.RetryEvent{Purpose: "main", Attempt: 1, Kind: core.ErrNetwork, Message: msg, Discarded: true, FellBack: "box2 (qwen3.8-27b)"})
	g.once("warning: local/qwen3.8-27b at http://203.0.113.7:8080 could not be reached", "This turn goes on with the fallback model box2",
		"The reply above was cut off when the request failed")
	g.liveLacks("again in", "try 2")
	g.ev(request(2), said("Hello from the fallback.\n"), replyEnd(core.StopEndTurn), turnEnd(agent.EndCompleted), idle())
	g.once("Hello from the fallback.")
	if strings.Contains(flat(g.scroll()), "answers again") {
		t.Errorf("the fallback was said to answer again:\n%s", g.dump())
	}
}

// Every way a turn can end is said in plain words.
func TestTurnEnds(t *testing.T) {
	g := newRig(t, 100, 40, text.NoColor)
	g.keys(`"go" <Enter>`).start()
	g.ev(&agent.TurnEndEvent{Result: agent.TurnResult{Reason: agent.EndHalted, Detail: "a PreToolUse hook the user set up ended the turn: no pushes", Duration: 3 * time.Second}})
	g.once("warning: The turn was ended in 3s: a PreToolUse hook the user set up ended the turn: no pushes.")
	g.ev(&agent.TurnEndEvent{Result: agent.TurnResult{Reason: agent.EndModelStopped, Detail: "the test cannot pass without network access",
		Driver: "stop-hook", Objection: "the tests do not pass yet", Duration: time.Minute}})
	g.once("The model chose to stop in 1m 00s. Its reason: the test cannot pass without network access",
		"It had been sent back by stop-hook: the tests do not pass yet")
	g.ev(&agent.DriverEvent{Name: "stop-hook", Action: agent.Continue, Objection: "the tests do not pass yet", Streak: 1})
	g.once("note: The turn goes on: stop-hook objects: the tests do not pass yet")
	// The hook's record said the reason whole: the note does not say it
	// again.
	g.ev(&app.HookEvent{Event: "Stop", Records: []hooks.Record{{Event: "Stop", Command: "check.sh", Result: hooks.ResultBlocked, Reason: "done.txt is missing"}}})
	g.ev(&agent.DriverEvent{Name: "stop-hook", Action: agent.Continue, Objection: "done.txt is missing", Streak: 1})
	g.once("done.txt is missing", "note: The turn goes on: stop-hook objects, as the hook above says.")
	g.ev(&agent.SummaryEvent{Phase: "start", Trigger: "auto", PreTokens: 250000})
	g.liveHas("* writing a summary of the conversation, to make room")
	g.ev(&agent.SummaryEvent{Phase: "end", Trigger: "auto", PreTokens: 250000, PostTokens: 4500})
	g.once("The conversation was summarized: about 250k tokens before, about 4.5k after.")
	g.ev(&agent.RoomEvent{Freed: 41000, Notice: "popped `write the tests`: about 41k tokens left the model's view"})
	g.once("note: popped `write the tests`: about 41k tokens left the model's view")
	// One of gnosh's own prompts is not shown; the line that says why a
	// turn started comes from the loop as a notice, once.
	g.ev(&agent.PromptEvent{ID: "h1", Prompt: agent.Prompt{Hidden: true, Text: "<system>...</system>", Notice: "A background command ended: go test (exit 0)"}},
		&agent.NoticeEvent{Level: agent.LevelInfo, Source: "task-notification", Text: "A background command ended: go test (exit 0)"})
	g.once("note: A background command ended: go test (exit 0)").committedLacks("<system>")
	g.ev(&agent.LoopEvent{On: true}, &agent.LoopEvent{On: false, Turns: 3, Why: "the model showed the task is done"})
	g.once("/loop is on", "/loop is off after 3 turns: the model showed the task is done.")
}

// The hooks that ran on a call stand under it; the others are notices
// when they had something to say.
func TestHooks(t *testing.T) {
	g := newRig(t, 100, 30, text.NoColor)
	g.keys(`"go" <Enter>`).start()
	args := map[string]any{"command": "git push"}
	g.ev(toolCall("c1", "Bash"), replyEnd(core.StopToolUse), toolStart("c1", "Bash", args))
	g.ev(hookEvent("PreToolUse", "c1", "Bash", "approved", "none"))
	g.ev(toolEnd("c1", "Bash", args, core.ToolResult{Content: []core.Block{core.Text("ok")}, Meta: map[string]any{"exitCode": 0}}, 0))
	g.once("$ git push", "hook: PreToolUse hook `check.sh` approved", "1 hook ran on PreToolUse and had nothing to say")
	g.ev(hookEvent("Stop", "", "", "could_not_evaluate"))
	g.once("warning: hook: Stop hook `check.sh` could not evaluate")
	g.ev(hookEvent("SessionStart", "", "", "none"))
	g.committedLacks("SessionStart hook")
	// With detail on (the verbose option), the silent ones are shown too.
	g.s.detail = true
	g.ev(hookEvent("SessionStart", "", "", "none"))
	g.once("note: hook: SessionStart hook `check.sh` had nothing to say")
}

// Slash commands: one runs when it is typed, an unknown one comes back,
// and a path is a prompt.
func TestSlashCommands(t *testing.T) {
	g := newRig(t, 80, 24, text.NoColor)
	g.keys(`"/st"`)
	g.liveHas("/status")
	g.keys(`<Tab> <Enter>`)
	if len(g.c.commands) != 1 || g.c.commands[0] != "/status" {
		t.Fatalf("ran %q", g.c.commands)
	}
	g.once("> /status", "ran /status")

	g.keys(`"/nosuch thing" <Enter>`)
	if g.box() != "/nosuch thing" {
		t.Fatalf("an unknown command left the box holding %q", g.box())
	}
	g.once("There is no command /nosuch.")
	g.keys(`<Ctrl+U> <Backspace>`)

	g.keys(`"/usr/local/bin is on my path?" <Enter>`)
	if got := g.c.sent(); len(got) != 1 || got[0] != "/usr/local/bin is on my path?" {
		t.Fatalf("submitted %q", got)
	}

	// A command that takes its time does not freeze the box, and Esc stops it.
	g.hold = true
	stopped := false
	g.c.command = func(ctx context.Context, line string) (app.CommandResult, error) {
		stopped = ctx.Err() != nil
		return app.CommandResult{Handled: true, Name: "compact", Text: "summarized"}, nil
	}
	g.keys(`"/compact" <Enter>`)
	g.liveHas("* running /compact")
	g.keys(`"typing meanwhile"`)
	if g.box() != "typing meanwhile" {
		t.Fatalf("the box holds %q while a command runs", g.box())
	}
	g.keys(`<Esc>`)
	g.release()
	if !stopped {
		t.Error("Esc did not stop the command")
	}
	g.once("> /compact", "summarized").liveLacks("running /compact")

	// A shell line is not joined yet: it comes back, and says so.
	g.keys(`<Ctrl+U> "!ls -la" <Enter>`)
	if g.box() != "!ls -la" {
		t.Fatalf("a shell line left the box holding %q", g.box())
	}
	g.committedHas("Shell lines (a line that begins with !) are not joined to this build yet")
}

// Shift+Tab goes through the permission modes, and the status line names
// the mode at all times, in words.
func TestModes(t *testing.T) {
	g := newRig(t, 100, 24, text.NoColor)
	g.liveHas("manual mode")
	g.keys(`<Shift+Tab>`).liveHas("accept edits").liveLacks("manual mode")
	g.keys(`<Shift+Tab>`).liveHas("plan mode")
	g.keys(`<Shift+Tab>`).liveHas("manual mode") // skipping permissions is not within reach
	if g.c.mode != "default" {
		t.Fatalf("the mode is %q", g.c.mode)
	}

	g = newRig(t, 100, 24, text.NoColor, func(c *controls) { c.bypass = true })
	g.keys(`<Shift+Tab>*3`)
	if g.c.mode != "bypassPermissions" {
		t.Fatalf("the mode is %q", g.c.mode)
	}
	g.liveHas("[BYPASS PERMISSIONS]").once("warning: The permission mode is now BYPASS PERMISSIONS")
	g.keys(`<Shift+Tab>`).liveHas("manual mode").liveLacks("BYPASS")

	// Narrow: the mode is the last thing to go.
	g = newRig(t, 24, 24, text.NoColor, func(c *controls) { c.mode, c.bypass = "bypassPermissions", true })
	g.liveHas("[BYPASS PERMISSIONS]")
}

// The status line: what it holds, what the session can add to it, and
// that it is one row at every width.
func TestStatusLine(t *testing.T) {
	g := newRig(t, 200, 24, text.NoColor)
	g.ev(facts{"frame": "write the tests", "background": "2", "agents": "1 run needs input"})
	g.ev(&agent.LoopEvent{On: true})
	g.liveHas("idle", "manual mode", "qwen3.8-27b", "thinking on", "context 11%", "/loop on", "~/src/proj (main)",
		"in write the tests", "2 in the background", "1 run needs input: gnosh agents")
	g.liveLacks("tasks ")
	g.ev(&app.TasksEvent{Tasks: []app.Task{
		{Content: "Read the spec", Status: app.TaskCompleted},
		{Content: "Wire the gate", ActiveForm: "Wiring the gate", Status: app.TaskInProgress},
		{Content: "Test it", Status: app.TaskPending},
	}})
	g.liveHas("tasks 1/3").liveLacks("tasks 1/3: Wiring the gate")
	g.keys(`<Ctrl+T>`).liveHas("tasks 1/3: Wiring the gate")
	g.keys(`<Ctrl+T>`).wait(5 * time.Second)
	// The figure follows the session: it drops after a pop.
	g.ev(&agent.ContextEvent{Context: agent.ContextStatus{Window: 262144, Known: true, Point: 242144, Used: 236000}})
	g.liveHas("context 90%")
	g.ev(&agent.ContextEvent{Context: agent.ContextStatus{Window: 262144, Known: true, Point: 242144, Used: 52000}})
	g.liveHas("context 19%")
	g.ev(&agent.ContextEvent{Context: agent.ContextStatus{Window: 32768, Known: false, Point: 26214, Used: 3000}})
	g.liveHas("context 9% of an assumed 33k")
	for _, cols := range []int{150, 100, 70, 50, 30, 12, 5, 1} {
		g.resize(cols, 24)
		status := g.live[len(g.live)-1]
		if w := text.Width(status); w > cols {
			t.Errorf("at %d columns the status line is %d cells wide: %q", cols, w, status)
		}
		if cols >= 12 && !strings.Contains(status, "manual mode") {
			t.Errorf("at %d columns the status line does not name the mode: %q", cols, status)
		}
	}
}

// A configured status line command's lines take the place of gnosh's own
// status line, made safe to draw, with the permission mode above them when
// it is not the default; no lines give gnosh's own line back.
func TestCustomStatusLine(t *testing.T) {
	g := newRig(t, 100, 24, text.NoColor)
	g.ev(&app.StatusLineEvent{Lines: []string{"\x1b[32mmain\x1b[0m | ctx 11%\x1b[2J", "second\tline\x07"}, Padding: 2})
	g.liveHas("  main | ctx 11%", "  second line").liveLacks("idle", "manual mode")
	for _, l := range g.live {
		if strings.Contains(l, "\x1b[2J") || strings.Contains(l, "\x07") {
			t.Errorf("a control sequence reached the screen: %q", l)
		}
	}
	g.keys(`<Shift+Tab>`)
	g.liveHas("accept edits", "  main | ctx 11%")
	g.ev(&app.StatusLineEvent{})
	g.liveHas("idle").liveLacks("main | ctx")
	// Where there is no colour, the command's colours go too; where there
	// is, they stay. (The screen the rig reads holds no escapes, so the
	// lines are asked for.)
	g.ev(&app.StatusLineEvent{Lines: []string{"\x1b[31mred\x1b[0m line"}})
	if got := strings.Join(g.s.customLines(100), "\n"); strings.Contains(got, "\x1b") || !strings.Contains(got, "red line") {
		t.Errorf("without colour, the lines are %q", got)
	}
	coloured := newRig(t, 100, 24, text.TrueColor)
	coloured.ev(&app.StatusLineEvent{Lines: []string{"\x1b[31mred\x1b[0m line"}})
	if got := strings.Join(coloured.s.customLines(100), "\n"); !strings.Contains(got, "\x1b[31mred") {
		t.Errorf("with colour, the lines are %q", got)
	}

	// What is kept and what goes.
	for in, want := range map[string]string{
		"\x1b[1;32mok\x1b[0m":                                   "\x1b[1;32mok\x1b[0m\x1b[0m",
		"\x1b[31mred":                                           "\x1b[31mred\x1b[0m",
		"a\x1b[Hb\x1b]0;title\x07c":                             "abc",
		"\x1b]8;;https://example.org\x1b\\link":                 "\x1b]8;;https://example.org\x1b\\link\x1b]8;;\x1b\\",
		"\x1b]8;;https://example.org\x07link\x1b]8;;\x07 after": "\x1b]8;;https://example.org\x07link\x1b]8;;\x07 after",
		"tab\there\r":                                           "tab here",
		// A link with a control or C1 character inside goes whole, and so
		// does a mark that turns the text around (the status line review
		// of 2026-11-19).
		"\x1b]8;;x\u009c\v\v\x1b\\link": "link",
		"a\u202eb":                      "ab",
	} {
		if got := statusText(in); got != want {
			t.Errorf("statusText(%q) = %q, want %q", in, got, want)
		}
	}
}

// The terminal's title is the session's name, once it has one.
func TestTerminalTitle(t *testing.T) {
	g := newRig(t, 100, 24, text.NoColor)
	g.ev(facts{"frame": "x"})
	if len(g.titles) != 0 {
		t.Errorf("a title was set before the session had a name: %q", g.titles)
	}
	g.ev(facts{"session": "fix the parser"})
	g.ev(facts{"session": "fix the parser"})
	g.ev(facts{"session": "ship it"})
	if strings.Join(g.titles, "|") != "gnosh: fix the parser|gnosh: ship it" {
		t.Errorf("the titles set: %q", g.titles)
	}
}

// A command that asks for text on the clipboard (/copy) has it written
// through the terminal.
func TestCopyToClipboard(t *testing.T) {
	g := newRig(t, 100, 24, text.NoColor)
	g.c.command = func(ctx context.Context, line string) (app.CommandResult, error) {
		return app.CommandResult{Handled: true, Name: "copy", Text: "The reply is on its way to the clipboard.", Copy: "the reply"}, nil
	}
	g.keys(`"/copy" <Enter>`)
	g.committedHas("The reply is on its way to the clipboard.")
	if len(g.copied) != 1 || g.copied[0] != "the reply" {
		t.Errorf("the clipboard was given %q", g.copied)
	}
}

// What is typed after "/mem redact" is a text the person wants gone. It
// reaches the session, and nothing of it is written into the scrollback,
// shown while the command waits or runs, or kept in the history.
func TestRedactLine(t *testing.T) {
	g := newRig(t, 100, 24, text.NoColor)
	const secret = "hunter2-the-passphrase"
	var got []string
	g.c.command = func(ctx context.Context, line string) (app.CommandResult, error) {
		got = append(got, line)
		return app.CommandResult{Handled: true, Name: "mem", Text: "mem: taken out of 2 places"}, nil
	}
	g.hold = true
	// Typed while another command runs: it waits, and then runs.
	g.keys(`"/compact" <Enter>`)
	g.keys(`"/mem redact ` + secret + `" <Enter>`)
	g.liveHas("/mem redact (what was typed after it is not shown)").liveLacks(secret)
	g.release()
	g.liveHas("running /mem redact (what was typed after it is not shown)").liveLacks(secret)
	g.release()
	if len(got) != 2 || got[1] != "/mem redact "+secret {
		t.Fatalf("the session was given %q", got)
	}
	g.once("> /mem redact (what was typed after it is not shown)")
	g.committedHas("mem: taken out of 2 places").committedLacks(secret).liveLacks(secret)
	g.keys(`<Up>`)
	if strings.Contains(g.box(), secret) {
		t.Fatalf("the history kept the text: the box holds %q", g.box())
	}
	// Other commands, and /mem's other words, are shown as typed.
	for line, want := range map[string]bool{
		"/mem redact": false, "/mem show": false, "/mem find redact x": false, "/model redact x y": false,
		"/mem redact x": true, "/MEM  Redact  a b": true,
	} {
		if _, held := shownCommand(line); held != want {
			t.Errorf("shownCommand(%q) holds something back: %v, want %v", line, held, want)
		}
	}
}

// The session's task list stands above the prompt box, the way the
// session last set it: what is done, what is in hand, what is to do.
// Ctrl+T puts it out of sight and brings it back, and nothing of it is
// ever written into the scrollback.
func TestTaskList(t *testing.T) {
	g := newRig(t, 120, 24, text.NoColor)
	g.keys(`<Ctrl+T>`).liveHas("There is no task list to show")
	list := []app.Task{
		{Content: "Read the spec", Status: app.TaskCompleted},
		{Content: "Wire the gate", ActiveForm: "Wiring the gate", Status: app.TaskInProgress},
		{Content: "Test it\x1b[2J", Status: app.TaskPending},
	}
	g.ev(&app.TasksEvent{Tasks: list})
	g.liveHas("  [x] Read the spec", "  [>] Wiring the gate", "  [ ] Test it^[[2J", "tasks 1/3")
	// Above the box, which the status line follows: the box and its
	// line are the region's last rows, as in Claude Code (Jae,
	// 2026-11-21: "the todos should be above the prompt not below it").
	places := func() (working, first, box, status int) {
		working, first, box, status = -1, -1, -1, -1
		for i, l := range g.live {
			switch {
			case strings.HasPrefix(l, "* "):
				working = i
			case strings.HasPrefix(l, ">"):
				box = i
			case strings.Contains(l, "Read the spec"):
				first = i
			case strings.Contains(l, "manual mode"):
				status = i
			}
		}
		return working, first, box, status
	}
	if _, first, box, status := places(); !(first >= 0 && first+2 < box && box+1 == status) {
		t.Fatalf("the list begins on row %d, the box is on row %d, the status line is row %d:\n%s", first, box, status, strings.Join(g.live, "\n"))
	}
	// It stays where it is while a turn runs and while the person types,
	// under the line that says what gnosh is doing.
	g.keys(`"go" <Enter>`).ev(busy(), turnStart(), promptOut("p1", "go"), request(1), said("Working on it.\n"))
	g.liveHas("[>] Wiring the gate", "* ")
	if working, first, box, status := places(); !(working >= 0 && working+1 == first && first+2 < box && box+1 == status) {
		t.Fatalf("the working line is row %d, the list begins on row %d, the box is on row %d, the status line is row %d:\n%s", working, first, box, status, strings.Join(g.live, "\n"))
	}
	list[1].Status, list[2].Status = app.TaskCompleted, app.TaskInProgress
	g.ev(&app.TasksEvent{Tasks: list})
	g.liveHas("[x] Wire the gate", "[>] Test it", "tasks 2/3").liveLacks("Wiring the gate")
	g.ev(replyEnd(core.StopEndTurn), turnEnd(agent.EndCompleted), idle())

	g.keys(`<Ctrl+T>`).liveLacks("[x] Read the spec").liveHas("The task list is out of sight", "tasks 2/3: Test it")
	g.keys(`<Ctrl+T>`).liveHas("[x] Read the spec", "[>] Test it")

	// A list longer than its rows shows the tasks around the one in hand.
	long := make([]app.Task, 30)
	for i := range long {
		long[i] = app.Task{Content: fmt.Sprintf("Step %d", i+1), Status: app.TaskPending}
		if i < 12 {
			long[i].Status = app.TaskCompleted
		}
	}
	long[12].Status = app.TaskInProgress
	g.ev(&app.TasksEvent{Tasks: long})
	g.liveHas("[x] Step 12", "[>] Step 13", "[ ] Step 14", "and 23 more tasks (12 of 30 done)", "tasks 12/30")
	g.liveLacks("Step 11", "Step 19")
	// In a terminal with few rows the list gives way to the box; in a
	// narrow one the status line keeps what matters more.
	g.resize(120, 4)
	g.liveHas("manual mode").liveLacks("Step 13")
	g.resize(60, 24)
	g.liveHas("[>] Step 13", "manual mode").liveLacks("tasks 12/30")
	g.resize(120, 24)

	// An empty list means there is none now; and nothing of the list was
	// ever committed.
	g.ev(&app.TasksEvent{})
	g.liveLacks("Step", "tasks ")
	g.committedLacks("Step 13", "Read the spec")

	// A session that already has a list when it is opened shows it.
	g = newRig(t, 120, 24, text.NoColor, func(c *controls) {
		c.tasks = []app.Task{{Content: "Carry on", ActiveForm: "Carrying on", Status: app.TaskInProgress}}
	})
	g.liveHas("[>] Carrying on", "tasks 0/1")
}

// facts is an event of the session's that puts facts on the status line.
type facts map[string]string

func (f facts) StatusFacts() map[string]string { return f }

// The model's reasoning is folded to one line unless detail is on (the
// verbose option), when it is written out as it arrives.
func TestThinking(t *testing.T) {
	each(t, func(t *testing.T, cols int) {
		g := newRig(t, cols, 24, text.NoColor)
		g.keys(`"go" <Enter>`).start()
		g.ev(thought("First I should read the file.\nThen I should "), thought("change the one line that matters.\n"))
		g.liveHas("~ thinking", "change the one line")
		g.committedLacks("First I should read the file")
		g.wait(4*time.Second).ev(said("Done.\n"), replyEnd(core.StopEndTurn))
		g.once("~ thought for 4s · Ctrl+O shows it").committedLacks("First I should read the file")
		// With detail on it is written out as it arrives.
		g.s.detail = true
		g.ev(request(2), thought("Now the second step.\nAnd a third "))
		g.once("Now the second step.").committedLacks("And a third")
		g.liveHas("And a third")
		g.ev(said("Finished.\n"), replyEnd(core.StopEndTurn), turnEnd(agent.EndCompleted), idle())
		g.once("And a third", "Finished.")
	})
}

// Completion: commands by their names, files by directory, with what the
// ignore rules leave out not offered.
func TestCompletion(t *testing.T) {
	g := newRig(t, 80, 24, text.NoColor)
	g.files["/home/jae/src/proj"] = []fileEntry{{name: ".env"}, {name: "internal", dir: true}, {name: "main.go"}, {name: "Makefile"}}
	g.files["/home/jae/src/proj/internal"] = []fileEntry{{name: "app", dir: true}, {name: "ui", dir: true}}
	g.keys(`"look at @"`)
	g.liveHas("internal/", "main.go", "Makefile").liveLacks(".env")
	g.keys(`"ma"`)
	g.liveHas("main.go", "Makefile").liveLacks("internal/")
	g.keys(`<Tab>`)
	if g.box() != "look at @main.go" {
		t.Fatalf("the box holds %q", g.box())
	}
	g.keys(`" and @int" <Tab>`)
	if g.box() != "look at @main.go and @internal/" {
		t.Fatalf("the box holds %q", g.box())
	}
	g.liveHas("app/", "ui/")
	g.keys(`"u" <Tab> <Esc>`)
	if g.box() != "look at @main.go and @internal/ui/" {
		t.Fatalf("the box holds %q", g.box())
	}
	// An exact command wins over one that merely resembles it.
	g.keys(`<Ctrl+U> "/no_think" <Enter>`)
	if len(g.c.commands) != 1 || g.c.commands[0] != "/no_think" {
		t.Fatalf("ran %q", g.c.commands)
	}
}

// The person's own editor: the box's text goes out and what was saved
// comes back.
func TestExternalEditor(t *testing.T) {
	g := newRig(t, 80, 24, text.NoColor)
	g.keys(`"a draft" <Ctrl+G>`)
	g.liveHas("Set VISUAL or EDITOR")
	g.editor = func(s string) (string, error) { return s + "\nwith a second line\n", nil }
	g.keys(`<Ctrl+X> <Ctrl+E>`)
	if want := "a draft\nwith a second line"; g.box() != want {
		t.Fatalf("the box holds %q, want %q", g.box(), want)
	}
}

// Ctrl+L draws the live region again, Ctrl+Z suspends, and neither loses
// what is in the box.
func TestRedrawAndSuspend(t *testing.T) {
	g := newRig(t, 80, 24, text.NoColor)
	g.keys(`"kept" <Ctrl+L>`)
	if g.box() != "kept" {
		t.Fatalf("the box holds %q", g.box())
	}
	// Put aside and brought back.
	g.keys(`<Ctrl+S>`).liveHas("Put aside.")
	if g.box() != "" {
		t.Fatalf("after Ctrl+S the box holds %q", g.box())
	}
	g.keys(`<Ctrl+S>`)
	if g.box() != "kept" {
		t.Fatalf("Ctrl+S on an empty box brought back %q", g.box())
	}
	g.keys(`<Ctrl+Z>`)
	if g.suspends != 1 {
		t.Fatalf("suspended %d times", g.suspends)
	}
	g.liveHas("> kept")
	// With no shell that could bring gnosh back, nothing is stopped, and
	// the key says so.
	g.suspendErr = errNoShell
	g.keys(`<Ctrl+Z>`)
	g.liveHas("> kept", "Ctrl+Z: gnosh was not started from a shell that could bring it back, so it was not stopped.")
	g.suspendErr = nil
	// Stopped from outside: the live region was erased before the
	// terminal was given up, and is drawn afresh when it comes back.
	g.s.r.Clear()
	g.keys(`<Resume>`)
	g.liveHas("> kept")
	g.keys(`"?"`) // not on an empty box: it is typed
	if g.box() != "kept?" {
		t.Fatalf("the box holds %q", g.box())
	}
	g.keys(`<Ctrl+U> "?"`)
	g.once("Keys").committedHas("Shift+Tab", "the next permission mode", "Not joined to this build yet")
}

// A large paste is folded in the box and in the scrollback, and sent
// whole.
func TestPaste(t *testing.T) {
	g := newRig(t, 80, 24, text.NoColor)
	pasted := strings.Repeat("a line of the log\n", 40)
	g.keys(`"what went wrong here: "`)
	g.s.input(pasteOf(pasted))
	g.frame().liveHas("[pasted 40 lines]")
	g.keys(`<Enter>`)
	if got := g.c.sent(); len(got) != 1 || got[0] != "what went wrong here: "+pasted {
		t.Fatalf("submitted %d bytes", len(strings.Join(got, "")))
	}
	g.start()
	g.once("> what went wrong here: [pasted 40 lines]").committedLacks("a line of the log")
	g.keys(`<Up>`)
	g.liveHas("[pasted 40 lines]")
}

// In a terminal that takes desktop notifications, the same moments raise
// one, with no Notification hook configured; in one that does not, the
// clock asks for nothing.
func TestDesktopNotifications(t *testing.T) {
	g := newRig(t, 80, 24, text.NoColor)
	g.takesNotes = true
	g.keys(`"go" <Enter>`).start()
	g.ev(&agent.QuestionEvent{Question: permission("q1")})
	g.wait(7 * time.Second)
	g.s.tick()
	if len(g.notes) != 1 || !strings.Contains(g.notes[0], "gnosh is waiting for an answer") {
		t.Fatalf("after 7 quiet seconds the desktop was told %q", g.notes)
	}
	g.keys(`<Up> <Enter>`).ev(&agent.AnswerEvent{ID: "q1"}, turnEnd(agent.EndCompleted), idle())
	g.wait(61 * time.Second)
	g.s.tick()
	if len(g.notes) != 2 || g.notes[1] != "gnosh has finished and waits for your next prompt" {
		t.Fatalf("after a minute of idling the desktop was told %q", g.notes)
	}
	if len(g.c.notified) != 0 {
		t.Errorf("hooks were told with none configured: %q", g.c.notified)
	}

	quiet := newRig(t, 80, 24, text.NoColor)
	quiet.keys(`"go" <Enter>`).start().ev(turnEnd(agent.EndCompleted), idle())
	if at := quiet.s.wake(); !at.IsZero() && at.Sub(quiet.now) > 2*time.Second {
		t.Errorf("with nothing to tell, the session asks to be woken at %v", at.Sub(quiet.now))
	}
}

// The person's Notification hooks are told when a question has waited six
// seconds with no key pressed, and when a turn ended a minute ago and
// nothing was typed since. The clock never makes the loop spin.
func TestNotifications(t *testing.T) {
	g := newRig(t, 80, 24, text.NoColor, func(c *controls) { c.info.NotifyHooks = true })
	settled := func() {
		t.Helper()
		g.s.tick()
		if at := g.s.wake(); !at.IsZero() && !at.After(g.now) {
			t.Fatalf("the session asks to be woken at %v, which is not after now (%v): the loop would spin", at, g.now)
		}
	}
	g.keys(`"go" <Enter>`).start()
	g.ev(&agent.QuestionEvent{Question: permission("q1")})
	g.wait(5 * time.Second)
	settled()
	if len(g.c.notified) != 0 {
		t.Fatalf("told after 5 s: %q", g.c.notified)
	}
	g.keys(`<Down>`) // a key: the person is there
	g.wait(5 * time.Second)
	settled()
	if len(g.c.notified) != 0 {
		t.Fatalf("told 5 s after a key: %q", g.c.notified)
	}
	g.wait(2 * time.Second)
	settled()
	if len(g.c.notified) != 1 || g.c.notified[0] != "permission_prompt" {
		t.Fatalf("after 7 quiet seconds the hooks were told %q", g.c.notified)
	}
	g.wait(time.Minute)
	settled()
	if len(g.c.notified) != 1 {
		t.Fatalf("told again: %q", g.c.notified)
	}
	g.keys(`<Up> <Enter>`).ev(&agent.AnswerEvent{ID: "q1"}, turnEnd(agent.EndCompleted), idle())
	g.wait(59 * time.Second)
	settled()
	if len(g.c.notified) != 1 {
		t.Fatalf("told before a minute of idling: %q", g.c.notified)
	}
	g.wait(2 * time.Second)
	settled()
	if len(g.c.notified) != 2 || g.c.notified[1] != "idle_prompt" {
		t.Fatalf("after a minute of idling the hooks were told %q", g.c.notified)
	}
	// A turn after which something is typed: nobody is away.
	g.keys(`"more" <Enter>`).start().ev(turnEnd(agent.EndCompleted), idle())
	g.wait(time.Second).keys(`"x"`)
	g.wait(2 * time.Minute)
	settled()
	if len(g.c.notified) != 2 {
		t.Fatalf("told while the person types: %q", g.c.notified)
	}
}

// Nothing looks busy with nothing in flight. Should the session go idle
// without the screen hearing of it, the screen asks the session once
// nothing has been heard for a few seconds, and believes it. While the
// session says it is busy the line stays, however long the silence.
func TestNothingSpinsWithNothingInFlight(t *testing.T) {
	g := newRig(t, 80, 24, text.NoColor)
	g.keys(`"go" <Enter>`).start()
	g.c.state = agent.State{Busy: true, Phase: agent.PhaseRequest, Waiting: "waiting for local/qwen3.8-27b to start its reply"}
	g.wait(10 * time.Minute).liveHas("* waiting for qwen3.8-27b to start its reply · 10m 00s")
	g.c.state = agent.State{Phase: agent.PhaseIdle}
	g.ev(said("A late word. "))
	g.wait(2 * time.Second).liveHas("* qwen3.8-27b is writing")
	g.wait(2 * time.Second).liveLacks("* ").liveHas("idle")
	g.once("A late word.")
}

// A question that comes while the box is empty: the box is not drawn
// under it, the cursor is hidden until a key is typed, and a key typed
// before the question could be read goes to the box, which then shows.
func TestQuestionOverAnEmptyBox(t *testing.T) {
	g := newRig(t, 80, 24, text.NoColor)
	g.keys(`"go" <Enter>`).start()
	g.ev(&agent.QuestionEvent{Question: permission("q1")})
	g.liveHas("Allow Bash?").liveLacks("Type to queue")
	if g.liveRow >= 0 {
		t.Fatalf("the cursor is shown on row %d while the question waits for the keys\n%s", g.liveRow, g.dump())
	}
	g.keys(`"y"`)
	if len(g.c.answers) != 0 || g.box() != "y" {
		t.Fatalf("a key typed at once: %d answers, the box holds %q", len(g.c.answers), g.box())
	}
	g.liveHas("> y")
	g.wait(time.Second).liveHas("> y")
	g.keys(`"1"`)
	if len(g.c.answers) != 1 || g.c.answers[0].ans.Choice != "yes" {
		t.Fatalf("answered %+v", g.c.answers)
	}
	g.liveHas("> y").liveLacks("Allow Bash?")
}

// The first lines of a result under its call leave its blank lines out:
// a sub-agent's report or a page is shown without empty rows.
func TestResultPreviewHasNoBlankRows(t *testing.T) {
	g := newRig(t, 80, 24, text.NoColor)
	in := map[string]any{"url": "https://example.com/page"}
	res := core.ToolResult{Content: []core.Block{core.Text("Report:\n\n- one\n   \n- two\n\nthree\n")}}
	g.ev(toolStart("c1", "WebFetch", in), toolEnd("c1", "WebFetch", in, res, time.Second))
	all := strings.Join(g.all(), "\n")
	if !strings.Contains(all, "  Report:\n  - one\n  - two\n  1 more line not shown") {
		t.Errorf("the result's lines, without its blank ones, are not under the call:\n%s", all)
	}
}

// The channel the person chose (preferredNotifChannel) is the one the
// notes go by; notifications_disabled sends none.
func TestNotificationChannel(t *testing.T) {
	off := newRig(t, 80, 24, text.NoColor, func(c *controls) { c.info.NotifChannel = "notifications_disabled" })
	off.takesNotes = true
	off.keys(`"go" <Enter>`).start().ev(turnEnd(agent.EndCompleted), idle())
	off.wait(61 * time.Second)
	off.s.tick()
	if len(off.notes) != 0 {
		t.Errorf("with notifications off the desktop was told %q", off.notes)
	}
}

// What each channel writes, in tmux and out of it.
func TestNoteSeq(t *testing.T) {
	plain := func(string) string { return "" }
	tmux := func(k string) string {
		if k == "TMUX" {
			return "/tmp/tmux-0/default,1,0"
		}
		return ""
	}
	iterm := func(k string) string {
		if k == "TERM_PROGRAM" {
			return "iTerm.app"
		}
		return ""
	}
	overSSH := func(k string) string {
		if k == "LC_TERMINAL" {
			return "iTerm2" // TERM_PROGRAM stays behind
		}
		return ""
	}
	for _, c := range []struct {
		channel string
		env     func(string) string
		want    string
	}{
		{"auto", plain, ""},
		{"auto", iterm, "\x1b]9;done\x1b\\"},
		{"auto", overSSH, "\x1b]9;done\x1b\\"},
		{"auto", tmux, ""},
		{"terminal_bell", plain, "\a"},
		{"terminal_bell", tmux, "\a"},
		{"iterm2", plain, "\x1b]9;done\x1b\\"},
		{"iterm2", tmux, "\x1bPtmux;\x1b\x1b]9;done\x1b\x1b\\\x1b\\"},
		{"iterm2_with_bell", plain, "\x1b]9;done\x1b\\\a"},
		{"kitty", plain, "\x1b]99;;done\x1b\\"},
		{"ghostty", plain, "\x1b]9;done\x1b\\"},
		{"notifications_disabled", iterm, ""},
	} {
		if got := noteSeq(c.channel, "done", c.env); got != c.want {
			t.Errorf("%s: %q, want %q", c.channel, got, c.want)
		}
	}
}

// A notification the model sends (PushNotification) goes to the hooks only
// when the person seems away: within half a minute of a key it is not sent,
// and the model is told why.
func TestPushNotification(t *testing.T) {
	g := newRig(t, 80, 24, text.NoColor, func(c *controls) { c.info.NotifyHooks = true })
	g.keys(`"build it and tell me" <Enter>`).start()
	reply := make(chan string, 1)
	g.ev(&app.PushEvent{Message: "build done: 0 failures", Reply: reply})
	if said := <-reply; !strings.HasPrefix(said, "not sent: the user is at the terminal") || len(g.c.notified) != 0 {
		t.Fatalf("right after a key: %q, hooks told %q", said, g.c.notified)
	}
	g.wait(31 * time.Second)
	g.ev(&app.PushEvent{Message: "build done: 0 failures", Reply: reply})
	if said := <-reply; said != "sent" || len(g.c.notified) != 1 || g.c.notified[0] != "push_notification" {
		t.Fatalf("after half a minute away: %q, hooks told %q", said, g.c.notified)
	}
}

// A form an MCP server puts shows the server's message above its
// questions, and Esc on it answers "cancel" and leaves the turn running:
// the server is told and decides what follows.
func TestServerFormEscCancels(t *testing.T) {
	g := newRig(t, 80, 30, text.NoColor)
	g.keys(`"deploy it" <Enter>`).start()
	data := app.QuestionForm{Lead: "The MCP server deploy asks: Where should it go?", EscCancels: true, Questions: []app.FormQuestion{
		{Question: "Region", Header: "region", Options: []app.FormOption{{Label: "US East"}, {Label: "EU West"}}, NoOther: true},
	}}
	g.ev(&agent.QuestionEvent{Question: agent.Question{ID: "q1", Kind: "question", Title: "The MCP server deploy asks for something", Data: data}})
	g.liveHas("The MCP server deploy asks: Where should it go?", "Region", "EU West").liveLacks("in your own words")
	g.wait(time.Second)
	g.keys(`<Esc>`)
	if len(g.c.answers) != 1 || g.c.answers[0].ans.Choice != "cancel" {
		t.Fatalf("Esc answered %+v", g.c.answers)
	}
	g.c.mu.Lock()
	stops := g.c.stops
	g.c.mu.Unlock()
	if stops != 0 {
		t.Errorf("Esc on a server's form stopped the turn")
	}
}

// A key's password typed at once after the command was approved goes
// neither to the prompt box nor, by Enter, to the model: what was typed
// between the approval and the masked question is taken out of the box
// again, and the masked question takes keys the moment it is drawn. A
// draft from before the approval stays.
func TestPasswordTypedEarly(t *testing.T) {
	g := newRig(t, 100, 24, text.NoColor)
	g.keys(`"sign it" <Enter>`).start()
	args := map[string]any{"command": "gnokey maketx call -func Inc k"}
	g.ev(toolCall("c1", "gnokey"), replyEnd(core.StopToolUse), toolStart("c1", "gnokey", args))
	g.keys(`"a draft"`)
	g.ev(&agent.QuestionEvent{Question: permission("q1")})
	g.wait(time.Second).keys(`<Enter>`)
	g.ev(&agent.AnswerEvent{ID: "q1"})
	// The password, typed straight away: the masked question is not up yet.
	g.keys(`"hunt"`)
	if g.box() != "a draft"+"hunt" {
		t.Fatalf("the box holds %q", g.box())
	}
	g.ev(&agent.QuestionEvent{Question: agent.Question{ID: "q2", Kind: "secret", Title: "Password for the key k", Text: "gnokey is to sign with the key k"}})
	if g.box() != "a draft" {
		t.Fatalf("after the masked question opened the box holds %q", g.box())
	}
	g.keys(`"er22" <Enter>`)
	if n := len(g.c.answers); n != 2 || g.c.answers[1].ans.Text != "er22" {
		t.Fatalf("the masked question was answered as %+v", g.c.answers)
	}
	if strings.Contains(g.scr.Dump(), "hunt") || strings.Contains(g.scr.Dump(), "er22") {
		t.Fatalf("the password is on the screen\n%s", g.dump())
	}
}

// Esc with a key typed straight after it arrives from many terminals as
// that key with Alt: at a question with no use for it, it declines as the
// Esc it began with (Claude Code 2.1.328); before, it was lost.
func TestEscTypedWithAKeyDeclines(t *testing.T) {
	for _, keys := range []string{`<Alt+x>`, `<Alt+Esc>`} {
		g := newRig(t, 80, 30, text.NoColor)
		g.keys(`"go" <Enter>`).start()
		g.ev(&agent.QuestionEvent{Question: permission("q1")})
		g.liveHas("Allow Bash?")
		g.wait(time.Second).keys(keys)
		if len(g.c.answers) != 1 || g.c.answers[0].ans.Choice != "no" {
			t.Errorf("%s at a question answered %+v, want no", keys, g.c.answers)
		}
	}
}

// A Read of a PDF says which of its pages it read, and of how many.
func TestReadOfAPDFSaysItsPages(t *testing.T) {
	g := newRig(t, 80, 24, text.NoColor)
	g.keys(`"read the paper" <Enter>`).start()
	part := map[string]any{"file_path": "/home/jae/src/proj/paper.pdf", "pages": "3-5"}
	whole := map[string]any{"file_path": "/home/jae/src/proj/short.pdf"}
	g.ev(toolCall("c1", "Read"), toolCall("c2", "Read"), replyEnd(core.StopToolUse))
	g.ev(toolStart("c1", "Read", part), toolStart("c2", "Read", whole))
	g.ev(toolEnd("c1", "Read", part, core.ToolResult{Content: []core.Block{core.Text("[page 3]\nx\n")},
		Meta: map[string]any{"type": "pdf", "file": map[string]any{"numPages": 12, "firstPage": 3, "lastPage": 5}}}, 0))
	g.ev(toolEnd("c2", "Read", whole, core.ToolResult{Content: []core.Block{core.Text("[page 1]\nx\n")},
		Meta: map[string]any{"type": "pdf", "file": map[string]any{"numPages": 4, "firstPage": 1, "lastPage": 4}}}, 0))
	g.once("Read paper.pdf (pages 3-5)").committedHas("pages 3-5 of 12", "4 pages of a PDF")
}

// A note that travels with a call's result (the language server's count
// of new problems after an edit) is drawn under the call's block, never
// above it; and a question to the language server is headed by what it
// asks and where.
func TestNotesUnderTheCall(t *testing.T) {
	each(t, func(t *testing.T, cols int) {
		g := newRig(t, cols, 30, text.NoColor)
		g.keys(`"rename it" <Enter>`).start()
		args := map[string]any{"file_path": "/home/jae/src/proj/a.go", "old_string": "Old", "new_string": "New"}
		g.ev(toolCall("c1", "Edit"), replyEnd(core.StopToolUse), toolStart("c1", "Edit", args))
		g.ev(toolEnd("c1", "Edit", args, core.ToolResult{
			Content: []core.Block{core.Text("edited")},
			Display: &core.Display{Kind: "diff", Path: "/home/jae/src/proj/a.go", Before: "func Old() {}\n", After: "func New() {}\n"},
			Notes:   []string{"Found 1 new diagnostic issue in 1 file (gopls)."},
		}, 0))
		g.committedHas("+ func New() {}", "Found 1 new diagnostic issue in 1 file (gopls).")
		all := g.scroll()
		if strings.Index(all, "Found 1 new") < strings.Index(all, "+ func New() {}") {
			t.Errorf("the note is above the call's block:\n%s", all)
		}
		lsp := map[string]any{"operation": "hover", "filePath": "/home/jae/src/proj/a.go", "line": 12, "character": 5}
		g.ev(toolCall("c2", "LSP"), replyEnd(core.StopToolUse), toolStart("c2", "LSP", lsp))
		g.liveHas("LSP hover a.go:12:5")
		g.ev(toolEnd("c2", "LSP", lsp, core.ToolResult{Content: []core.Block{core.Text("func New()")}}, 0))
		g.committedLacks("Ask the language server")
		g.show("a note under an edit")
	})
}

// The mode the session changes itself in the middle of a turn (a plan
// approved) shows at once, not when the turn ends: the screen said plan
// mode while it asked about an edit after the approval.
func TestModeChangedByTheSession(t *testing.T) {
	g := newRig(t, 100, 24, text.NoColor)
	g.keys(`<Shift+Tab> <Shift+Tab>`).liveHas("plan mode")
	g.keys(`"plan it" <Enter>`).start()
	_ = g.c.SetMode("default")
	g.ev(&app.ModeEvent{Mode: "default"})
	// (The status line's; the hint Shift+Tab left still names plan mode.)
	g.liveHas("manual mode ·").liveLacks("plan mode ·")
}

// An approved plan's call says so in the person's words, and where the
// plan is kept: the model's text ("carry the plan out") is not theirs.
func TestPlanApprovedLine(t *testing.T) {
	g := newRig(t, 100, 24, text.NoColor)
	g.keys(`"plan it" <Enter>`).start()
	args := map[string]any{"plan": "1. Do it."}
	g.ev(toolCall("c1", "ExitPlanMode"), replyEnd(core.StopToolUse), toolStart("c1", "ExitPlanMode", args))
	g.ev(toolEnd("c1", "ExitPlanMode", args, core.ToolResult{
		Content: []core.Block{core.Text("The user approved the plan. The session is in the default mode now: carry the plan out.")},
		Meta:    map[string]any{"approved": true, "mode": "default", "planFile": "/home/jae/.local/share/gnosh/plans/1.md"},
	}, 0))
	g.committedHas("approved · manual mode", "the plan is kept in ~/.local/share/gnosh/plans/1.md").committedLacks("carry the plan out")
}

// An answered AskUserQuestion's call says "answered": the answers stand in
// the question's own record above it, and the result's words for the
// model said them again, cut short.
func TestAskUserQuestionAnsweredLine(t *testing.T) {
	g := newRig(t, 100, 24, text.NoColor)
	g.keys(`"set it up" <Enter>`).start()
	args := map[string]any{"questions": []any{map[string]any{"question": "Which database?", "header": "Database", "options": []any{}}}}
	g.ev(toolCall("c1", "AskUserQuestion"), replyEnd(core.StopToolUse), toolStart("c1", "AskUserQuestion", args))
	g.ev(toolEnd("c1", "AskUserQuestion", args, core.ToolResult{
		Content: []core.Block{core.Text("The user answered:\n1. Which database?\n   Answer: Postgres\n2. Which checks?\n   Answer: Unit tests")},
	}, 0))
	g.committedHas("answered").committedLacks("The user answered", "more lines not shown")
}

// A provider that answers again after failed tries is said to once on the
// screen, with the tries counted: the agent's own notice of it is left out
// (it was said twice).
func TestProviderBackSaidOnce(t *testing.T) {
	g := newRig(t, 100, 24, text.NoColor)
	g.keys(`"hello" <Enter>`).start()
	g.ev(&agent.RetryEvent{Purpose: "main", Attempt: 1, Kind: core.ErrServer, Status: 500, Message: "the server failed on its own side (HTTP 500). Trying again in 5s."})
	g.ev(&agent.NoticeEvent{Level: agent.LevelInfo, Source: agent.SourceProviderBack, Text: "the server is answering again, 5s after it stopped."})
	g.ev(said("Back.\n"), replyEnd(core.StopEndTurn), turnEnd(agent.EndCompleted), idle())
	g.committedHas("answers again, after 1 failed try").committedLacks("is answering again")
}

// A Bash call that came whole with no command is named as any call is,
// its failure on its line, not drawn as a prompt with nothing after it.
func TestBashCallWithNoCommand(t *testing.T) {
	g := newRig(t, 100, 24, text.NoColor)
	g.keys(`"look" <Enter>`).start()
	args := map[string]any{"cmd": "ls"}
	g.ev(toolCall("c1", "Bash"), replyEnd(core.StopToolUse), toolStart("c1", "Bash", args))
	g.ev(toolEnd("c1", "Bash", args, core.ToolResult{Content: []core.Block{core.Text("This call did not run: command is missing: give the shell command to run.")}, IsError: true}, 0))
	g.committedHas("Bash", "command is missing").committedLacks("✗ $")
}

// An MCP tool's call is named as Claude Code names it: the server and the
// tool, then the first arguments as they were written.
func TestMCPCallHeading(t *testing.T) {
	g := newRig(t, 100, 24, text.NoColor)
	g.keys(`"echo it" <Enter>`).start()
	raw := []byte(`{"text": "hello from the model", "times": 2, "nested": {"a": 1}}`)
	g.ev(toolCall("c1", "mcp__echo__say_it"), replyEnd(core.StopToolUse), &agent.ToolStartEvent{CallID: "c1", Name: "mcp__echo__say_it", Input: raw})
	g.liveHas(`echo - say_it (MCP) text: "hello from the model", times: 2`).liveLacks("mcp__echo__say_it")
	if s, tl, ok := app.MCPToolParts("mcp__a_b__c__d"); !ok || s != "a_b" || tl != "c__d" {
		t.Errorf("MCPToolParts: %q %q %v", s, tl, ok)
	}
	if _, _, ok := app.MCPToolParts("mcp__nothing"); ok {
		t.Error("a name with no tool was taken")
	}
}

// A command started in the background, and a Monitor, are shown in the
// person's words: where the output goes, and the id; not the model's
// ("Read that file... You will be told when it ends", "comes to you as
// notices... end your reply"), which the screen showed, and then said again.
func TestBackgroundStartsInThePersonsWords(t *testing.T) {
	g := newRig(t, 120, 30, text.NoColor)
	g.keys(`"start them" <Enter>`).start()
	bash := map[string]any{"command": "make watch", "run_in_background": true}
	g.ev(toolCall("c1", "Bash"), replyEnd(core.StopToolUse), toolStart("c1", "Bash", bash))
	g.ev(toolEnd("c1", "Bash", bash, core.ToolResult{
		Content: []core.Block{core.Text("Started in the background as bg1 (process group 42).\nIts output is collected in /home/jae/.local/share/gnosh/x/bg1.out. Read that file to see it; TaskStop stops the command. You will be told when it ends.")},
		Meta:    map[string]any{"backgroundTaskId": "bg1", "persistedOutputPath": "/home/jae/.local/share/gnosh/x/bg1.out"},
	}, 0))
	mon := map[string]any{"command": "tail -f log", "description": "watch the log"}
	g.ev(toolCall("c2", "Monitor"), replyEnd(core.StopToolUse), toolStart("c2", "Monitor", mon))
	g.ev(toolEnd("c2", "Monitor", mon, core.ToolResult{
		Content: []core.Block{core.Text("Monitor bg2 watches: watch the log. What the command prints comes to you as notices from gnosh. Go on with other work, or end your reply to wait for the lines.")},
		Meta:    map[string]any{"taskId": "bg2", "persistedOutputPath": "/home/jae/.local/share/gnosh/x/bg2.out"},
	}, 0))
	sub := map[string]any{"description": "survey the tests", "prompt": "Survey the tests.", "run_in_background": true}
	g.ev(toolCall("c3", "Agent"), replyEnd(core.StopToolUse), toolStart("c3", "Agent", sub))
	g.ev(toolEnd("c3", "Agent", sub, core.ToolResult{
		Content: []core.Block{core.Text("The sub-agent is working in the background on: survey the tests. Its id is a7. Its report will come to you as a notice from gnosh when it ends; until then it is still running, so do not report what it found before that notice arrives.")},
		Meta:    map[string]any{"status": "async_launched", "agentId": "a7", "agentType": "general-purpose"},
	}, 0))
	// A report shown without the id that ends it for the model.
	fg := map[string]any{"description": "count the tests", "prompt": "Count them."}
	g.ev(toolCall("c4", "Agent"), replyEnd(core.StopToolUse), toolStart("c4", "Agent", fg))
	g.ev(toolEnd("c4", "Agent", fg, core.ToolResult{
		Content: []core.Block{core.Text("There are 12 tests.\nAll of them pass.\n\n[agent id: a9]")},
		Meta:    map[string]any{"status": "completed", "agentId": "a9"},
	}, 0))
	g.committedHas("its output goes to ~/.local/share/gnosh/x/bg1.out", "running in the background as bg1",
		"watching as bg2", "its output goes to ~/.local/share/gnosh/x/bg2.out",
		"Agent survey the tests · running in the background as a7", "There are 12 tests.", "All of them pass.").
		committedLacks("Read that file", "You will be told", "comes to you as notices", "end your reply", "do not report what it found", "[agent id: a9]")
}

// EnterPlanMode and PushNotification say what came of them in the
// person's words, not the model's.
func TestPlanModeAndNotificationLines(t *testing.T) {
	g := newRig(t, 120, 30, text.NoColor)
	g.keys(`"go" <Enter>`).start()
	g.ev(toolCall("c1", "EnterPlanMode"), replyEnd(core.StopToolUse), toolStart("c1", "EnterPlanMode", map[string]any{}))
	g.ev(toolEnd("c1", "EnterPlanMode", map[string]any{}, core.ToolResult{
		Content: []core.Block{core.Text("Plan mode is on: read, search and think, and change nothing. When the plan is ready, present it with ExitPlanMode.")},
		Meta:    map[string]any{"planMode": true}}, 0))
	push := map[string]any{"message": "done"}
	g.ev(toolCall("c2", "PushNotification"), replyEnd(core.StopToolUse), toolStart("c2", "PushNotification", push))
	g.ev(toolEnd("c2", "PushNotification", push, core.ToolResult{
		Content: []core.Block{core.Text("Notification not sent: the user is at the terminal and reads what you write.")},
		Meta:    map[string]any{"notSent": "you were at the terminal"}}, 0))
	g.committedHas("plan mode on", "not sent: you were at the terminal").committedLacks("present it with ExitPlanMode", "reads what you write")
}

// A skill loaded, a search and a fetch say what came of them in the
// person's words (seen on the devbox's screen: the skill's lead for the
// model, tags and all; the search server's address; the smaller model's
// answer about the page).
func TestSkillSearchAndFetchLines(t *testing.T) {
	g := newRig(t, 120, 40, text.NoColor)
	g.keys(`"go" <Enter>`).start()
	sk := map[string]any{"skill": "greet", "args": "warmly"}
	g.ev(toolCall("c1", "Skill"), replyEnd(core.StopToolUse), toolStart("c1", "Skill", sk))
	g.ev(toolEnd("c1", "Skill", sk, core.ToolResult{
		Content: []core.Block{core.Text("<gnosh>The skill greet is loaded. What follows is its text: the user's own instructions for this kind of task. Carry them out.</gnosh>\n\nSay hello.")},
		Meta:    map[string]any{"skill": "greet", "source": "/p/.gnosh/skills/greet/SKILL.md", "loaded": "now"}}, 0))
	search := map[string]any{"query": "terminal agent"}
	g.ev(toolCall("c2", "WebSearch"), replyEnd(core.StopToolUse), toolStart("c2", "WebSearch", search))
	g.ev(toolEnd("c2", "WebSearch", search, core.ToolResult{
		Content: []core.Block{core.Text("Results for \"terminal agent\", from 127.0.0.1:\n1. A terminal agent\n   https://agent.test/one\n")},
		Meta: map[string]any{"query": "terminal agent", "results": []any{
			map[string]any{"title": "A terminal agent", "url": "https://agent.test/one"},
			map[string]any{"title": "https://bare.test/x", "url": "https://bare.test/x"},
			map[string]any{"title": "Third", "url": "https://third.test/3"},
			map[string]any{"title": "Fourth", "url": "https://fourth.test/4"}}}}, 0))
	fetch := map[string]any{"url": "https://example.com", "prompt": "what is it"}
	g.ev(toolCall("c3", "WebFetch"), replyEnd(core.StopToolUse), toolStart("c3", "WebFetch", fetch))
	g.ev(toolEnd("c3", "WebFetch", fetch, core.ToolResult{
		Content: []core.Block{core.Text("It is a page kept for use in examples.")},
		Meta:    map[string]any{"bytes": 1256, "code": 200, "codeText": "OK", "url": "https://example.com"}}, 0))
	moved := map[string]any{"url": "https://a.test/", "prompt": "x"}
	g.ev(toolCall("c4", "WebFetch"), replyEnd(core.StopToolUse), toolStart("c4", "WebFetch", moved))
	g.ev(toolEnd("c4", "WebFetch", moved, core.ToolResult{
		Content: []core.Block{core.Text("https://a.test/ redirects to https://b.test/, which is on another host, and WebFetch does not follow that.")},
		Meta:    map[string]any{"bytes": 0, "code": 301, "codeText": "Moved Permanently", "redirect": "https://b.test/"}}, 0))
	g.committedHas("Skill greet warmly · loaded",
		"WebSearch terminal agent · 4 results", "A terminal agent · agent.test", "https://bare.test/x", "Third · third.test", "1 more line not shown",
		"WebFetch https://example.com · received 1.2 KB (200 OK)",
		"redirects to https://b.test/, on another host: not followed").
		committedLacks("Carry them out", "gnosh>", "127.0.0.1", "kept for use in examples", "x · bare.test")
}

// A self-paced /loop's run that schedules the next says when, with the
// model's reason under the call, and ends as done: not "The turn was
// ended" as a warning, after a notice that said the time a third time.
func TestLoopWakeupLines(t *testing.T) {
	g := newRig(t, 120, 30, text.NoColor)
	g.keys(`"go" <Enter>`).start()
	in := map[string]any{"delaySeconds": 600, "reason": "nothing changes before then", "prompt": "check the clock"}
	g.ev(toolCall("c1", "ScheduleWakeup"), replyEnd(core.StopToolUse), toolStart("c1", "ScheduleWakeup", in))
	g.ev(toolEnd("c1", "ScheduleWakeup", in, core.ToolResult{
		Content: []core.Block{core.Text("The loop's next run is at 19:20, in 10 minutes. This turn ends here: the run starts by itself then.")},
		Meta:    map[string]any{"nextRun": "19:20", "wait": "10 minutes"},
		Notes:   []string{"nothing changes before then"}}, 0))
	g.ev(&agent.TurnEndEvent{Result: agent.TurnResult{Reason: agent.EndHalted, Driver: "ScheduleWakeup", Planned: true,
		Detail: "the loop's next run is at 19:20", Rounds: 1, Duration: 2 * time.Second}})
	g.committedHas("ScheduleWakeup · next run at 19:20, in 10 minutes", "nothing changes before then", "done in 2s").
		committedLacks("This turn ends here", "The turn was ended", "check the clock")
}

// A call of a tool there is not says so in one sentence on the screen: the
// list of the tools there are is the model's.
func TestUnknownToolLine(t *testing.T) {
	g := newRig(t, 100, 24, text.NoColor)
	g.keys(`"go" <Enter>`).start()
	args := map[string]any{"x": 1}
	g.ev(toolCall("c1", "Foo"), replyEnd(core.StopToolUse), toolStart("c1", "Foo", args))
	g.ev(toolEnd("c1", "Foo", args, core.ToolResult{
		Content: []core.Block{core.Text(`There is no tool named "Foo", so nothing ran. The tools you have are: Bash, Edit, Read.`)},
		IsError: true, Meta: map[string]any{"unknownTool": true}}, 0))
	g.committedHas(`There is no tool named "Foo", so nothing ran.`).committedLacks("The tools you have are")
}
