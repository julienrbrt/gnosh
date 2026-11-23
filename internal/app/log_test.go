package app

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/gnosh-ai/gnosh/internal/agent"
	"github.com/gnosh-ai/gnosh/internal/core"
	"github.com/gnosh-ai/gnosh/internal/instructions"
	"github.com/gnosh-ai/gnosh/internal/prompt"
	"github.com/gnosh-ai/gnosh/internal/transcript"
)

// newTestLog makes a session file in a temporary store, with the loop's
// log over it.
func newTestLog(t *testing.T) (*sessionLog, *transcript.Session, *transcript.Store) {
	t.Helper()
	store := transcript.New(t.TempDir())
	ts, err := store.Create("/work/app")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ts.Close() })
	return newSessionLog(ts, prompt.Builtin()), ts, store
}

func promptEntry(id, turn, text string) agent.Entry {
	return agent.Entry{Kind: agent.EntryPrompt, ID: id, Turn: turn, Blocks: []core.Block{core.Text(text)}}
}

func callEntry(turn, id, tool, input string) agent.Entry {
	return agent.Entry{Kind: agent.EntryAssistant, Turn: turn, Model: "m", Provider: "p", StopReason: core.StopToolUse,
		Message: core.Message{Role: core.RoleAssistant, Blocks: []core.Block{core.ToolUse(id, tool, json.RawMessage(input))}},
		Usage:   core.Usage{InputTokens: 100, OutputTokens: 9}}
}

func resultEntry(turn, id, tool, text string) agent.Entry {
	return agent.Entry{Kind: agent.EntryResult, Turn: turn, ToolUseID: id, ToolName: tool, Result: core.TextResult(text)}
}

func answerEntry(turn, text string) agent.Entry {
	return agent.Entry{Kind: agent.EntryAssistant, Turn: turn, Model: "m", Provider: "p", StopReason: core.StopEndTurn,
		Message: core.Message{Role: core.RoleAssistant, Blocks: []core.Block{core.Text(text)}},
		Usage:   core.Usage{InputTokens: 120, OutputTokens: 4}}
}

// shape writes a conversation as one line per message: its role and what
// its blocks are.
func shape(msgs []core.Message) string {
	var lines []string
	for _, m := range msgs {
		var blocks []string
		for _, b := range m.Blocks {
			switch b.Type {
			case core.BlockText:
				blocks = append(blocks, "text("+b.Text+")")
			case core.BlockToolUse:
				blocks = append(blocks, "call("+b.ID+")")
			case core.BlockToolResult:
				blocks = append(blocks, "result("+b.ToolUseID+": "+core.TextOf(b.Content)+")")
			default:
				blocks = append(blocks, string(b.Type))
			}
		}
		lines = append(lines, string(m.Role)+": "+strings.Join(blocks, " "))
	}
	return strings.Join(lines, "\n")
}

// What the loop appends is what the model reads, what the loop reads back
// when it settles an old session, and what a later process reads from the
// file: the same messages, in the same order.
func TestLogRoundTrip(t *testing.T) {
	ctx := context.Background()
	log, ts, store := newTestLog(t)
	entries := []agent.Entry{
		promptEntry("p1", "t1", "what is in a.txt?"),
		{Kind: agent.EntryNote, Turn: "t1", Prompt: "p1", Source: "hook", Blocks: []core.Block{core.Text(prompt.Wrap("the build is green"))}},
		{Kind: agent.EntryTurn, Turn: "t1"},
		callEntry("t1", "c1", "Read", `{"file_path":"a.txt"}`),
		resultEntry("t1", "c1", "Read", "1\thello"),
		{Kind: agent.EntryNotice, Turn: "t1", Source: "loop", Blocks: []core.Block{core.Text("a line for the person")}},
		answerEntry("t1", "It says hello."),
	}
	for _, e := range entries {
		if err := log.Append(ctx, e); err != nil {
			t.Fatalf("Append(%s): %v", e.Kind, err)
		}
	}
	v, err := log.View(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := "user: text(what is in a.txt?) text(<gnosh>the build is green</gnosh>)\n" +
		"assistant: call(c1)\n" +
		"user: result(c1: 1\thello)\n" +
		"assistant: text(It says hello.)"
	if got := shape(v.Messages); got != want {
		t.Errorf("the view:\n%s\nwant:\n%s", got, want)
	}
	if err := transcript.Check(v.Messages); err != nil {
		t.Errorf("the view is not a conversation a provider accepts: %v", err)
	}

	// Read back, newest first, as they were appended.
	var kinds []string
	for e, err := range log.Recent(ctx) {
		if err != nil {
			t.Fatal(err)
		}
		kinds = append(kinds, string(e.Kind))
		switch e.Kind {
		case agent.EntryPrompt:
			if e.ID != "p1" || e.Turn != "t1" || core.TextOf(e.Blocks) != "what is in a.txt?" {
				t.Errorf("the prompt read back: %+v", e)
			}
		case agent.EntryResult:
			if e.ToolUseID != "c1" || e.ToolName != "Read" || core.TextOf(e.Result.Content) != "1\thello" {
				t.Errorf("the result read back: %+v", e)
			}
		case agent.EntryAssistant:
			if e.Turn != "t1" || e.Model != "m" || e.Provider != "p" || e.Usage.InputTokens == 0 {
				t.Errorf("a reply read back: %+v", e)
			}
		}
	}
	if got, want := strings.Join(kinds, " "), "assistant notice result assistant turn note prompt"; got != want {
		t.Errorf("read back as %q, want %q", got, want)
	}

	// Usage is recorded after each reply of the session's own, and only
	// there.
	usage := 0
	for _, e := range ts.Entries() {
		if e.Type == transcript.TypeUsage {
			usage++
		}
	}
	if usage != 2 {
		t.Errorf("the file holds %d usage entries, want 2 (one for each reply)", usage)
	}

	// A later process reads the same conversation from the file.
	if err := ts.Close(); err != nil {
		t.Fatal(err)
	}
	again, err := store.Open(ctx, ts.ID())
	if err != nil {
		t.Fatal(err)
	}
	defer again.Close()
	if w := again.Warnings(); len(w) != 0 {
		t.Errorf("the file needed mending: %v", w)
	}
	v2, _ := newSessionLog(again, prompt.Builtin()).View(ctx)
	if !reflect.DeepEqual(v.Messages, v2.Messages) {
		t.Errorf("the view read from the file differs from the one that was sent:\n%s\n---\n%s", shape(v.Messages), shape(v2.Messages))
	}
}

// A reply that was cut short and holds nothing is kept all the same: it is
// what ends its turn. It is not part of what the model reads.
func TestLogKeepsAnEmptyCutReply(t *testing.T) {
	ctx := context.Background()
	for _, stop := range []core.StopReason{core.StopCancelled, core.StopError} {
		log, ts, _ := newTestLog(t)
		if err := log.Append(ctx,
			promptEntry("p1", "t1", "go"),
			agent.Entry{Kind: agent.EntryAssistant, Turn: "t1", Model: "m", Provider: "p", StopReason: stop, Silent: true,
				Message: core.Message{Role: core.RoleAssistant}},
		); err != nil {
			t.Fatal(err)
		}
		var found *transcript.Entry
		for _, e := range ts.Entries() {
			if e.Type == transcript.TypeAssistant {
				found = &e
			}
		}
		if found == nil || found.Stop != stop || len(found.Message.Blocks) != 0 {
			t.Fatalf("%s: the file holds no empty reply with that end: %+v", stop, found)
		}
		v, _ := log.View(ctx)
		if got := shape(v.Messages); got != "user: text(go)" {
			t.Errorf("%s: the view: %s", stop, got)
		}
		var back *agent.Entry
		for e := range log.Recent(ctx) {
			if e.Kind == agent.EntryAssistant {
				back = &e
			}
		}
		if back == nil || back.StopReason != stop || !back.Silent || back.Turn != "t1" {
			t.Errorf("%s: read back: %+v", stop, back)
		}
	}
}

// A summary stands for everything before it: the view starts there.
func TestLogSummaryStartsTheView(t *testing.T) {
	ctx := context.Background()
	log, _, _ := newTestLog(t)
	if err := log.Append(ctx,
		promptEntry("p1", "t1", "first"),
		answerEntry("t1", "one"),
		agent.Entry{Kind: agent.EntrySummary, Turn: "", Trigger: "manual", PreTokens: 900,
			Blocks: []core.Block{core.Text(prompt.Wrap("What follows is a summary.")), core.Text("The user said first; the answer was one.")}},
		agent.Entry{Kind: agent.EntryNote, Source: "summary", Blocks: []core.Block{core.Text(prompt.Wrap("Carry on."))}},
		promptEntry("p2", "t2", "second"),
	); err != nil {
		t.Fatal(err)
	}
	v, _ := log.View(ctx)
	got := shape(v.Messages)
	if strings.Contains(got, "text(first)") || strings.Contains(got, "text(one)") {
		t.Errorf("what came before the summary is still in the view:\n%s", got)
	}
	has(t, "the view", got, "The user said first; the answer was one.", "text(second)")
	if len(v.Messages) != 1 || v.Messages[0].Role != core.RoleUser {
		t.Errorf("the view after a summary is not one user message:\n%s", got)
	}
	var summary *agent.Entry
	for e := range log.Recent(ctx) {
		if e.Kind == agent.EntrySummary {
			summary = &e
		}
	}
	if summary == nil || summary.Trigger != "manual" || summary.PreTokens != 900 {
		t.Errorf("the summary read back: %+v", summary)
	}
}

// A session that stands in a frame: what a feature writes beside the
// loop's entries lands in the file with them, and when room is asked for,
// the oldest part of the frame slides out of the view without a summary.
// The slide is recorded, the feature is told what went, and the person
// gets a line that names the frame.
func TestLogSlideMakesRoom(t *testing.T) {
	ctx := context.Background()
	log, ts, _ := newTestLog(t)
	// A feature such as mem: it writes the push beside the result of the
	// call that made it.
	log.stored = append(log.stored, func(e agent.Entry) []transcript.Entry {
		if e.Kind == agent.EntryResult && e.ToolUseID == "c0" {
			return []transcript.Entry{transcript.PushEntry("big-job", "the big job", "c0")}
		}
		return nil
	})
	var told [][]transcript.Cut
	log.slid = append(log.slid, func(ctx context.Context, cuts []transcript.Cut) { told = append(told, cuts) })
	cutSeen := 0
	log.onCut = func() { cutSeen++ }

	if err := log.Append(ctx,
		promptEntry("p1", "t1", "do the big job"),
		callEntry("t1", "c0", "gnovm_run", `{"name":"mem","code":"mem.Todo(\"the big job\").Push()"}`),
		resultEntry("t1", "c0", "gnovm_run", "in `big-job`"),
	); err != nil {
		t.Fatal(err)
	}
	if stack := ts.Stack(); len(stack) != 1 || stack[0].ID != "big-job" {
		t.Fatalf("the feature's push is not in the file: stack %+v", stack)
	}
	filler := strings.Repeat("output of a long step. ", 80)
	for i := 1; i <= 12; i++ {
		id := fmt.Sprintf("c%d", i)
		if err := log.Append(ctx, callEntry("t1", id, "Bash", `{"command":"step"}`), resultEntry("t1", id, "Bash", fmt.Sprintf("step %d: %s", i, filler))); err != nil {
			t.Fatal(err)
		}
	}
	before, _ := log.View(ctx)

	estimate := func(m core.Message) int { return transcript.EstimateTokens(m) }
	room, err := log.MakeRoom(ctx, agent.RoomRequest{
		Need: 2000, Moment: agent.RoomMidTurn, Protect: "p1",
		Estimate: func(s string) int { return len(s) / 4 }, EstimateMessage: estimate,
	})
	if err != nil {
		t.Fatal(err)
	}
	if room.Freed < 2000 {
		t.Fatalf("MakeRoom freed %d tokens, want at least the 2000 asked for", room.Freed)
	}
	has(t, "the line for the person", room.Notice, "big-job", "instead of compacting")
	if room.Reminder == "" || !strings.Contains(room.Reminder, "big-job") {
		t.Errorf("the model is not told of the slide: %q", room.Reminder)
	}
	if len(told) != 1 || len(told[0]) == 0 || told[0][0].Tokens == 0 {
		t.Errorf("the feature was not told what the slide removed: %+v", told)
	}
	if cutSeen != 1 {
		t.Errorf("the cut was noted %d times, want 1", cutSeen)
	}

	after, _ := log.View(ctx)
	if len(after.Messages) >= len(before.Messages) {
		t.Errorf("the view has %d messages after the slide and had %d before", len(after.Messages), len(before.Messages))
	}
	got := shape(after.Messages)
	has(t, "the view after the slide", got, "do the big job", "step 12:")
	hasNot(t, "the view after the slide", got, "step 1:")
	if err := transcript.Check(after.Messages); err != nil {
		t.Errorf("the view after the slide is not a conversation a provider accepts: %v", err)
	}
	// The cut does not move: asked again, the view is the same.
	again, _ := log.View(ctx)
	if !reflect.DeepEqual(after.Messages, again.Messages) {
		t.Errorf("the view changed between two looks")
	}
	// The slide is in the file, so a later process cuts at the same place.
	raw, _ := os.ReadFile(ts.Path())
	has(t, "the session's file", string(raw), `"slide"`)

	// With no frame to slide in, there is no room to give: the loop then
	// goes on to a summary.
	plain, _, _ := newTestLog(t)
	plain.Append(ctx, promptEntry("p1", "t1", "hello"), answerEntry("t1", strings.Repeat("words ", 2000)))
	if room, err := plain.MakeRoom(ctx, agent.RoomRequest{Need: 500, Moment: agent.RoomBeforePrompt, EstimateMessage: estimate}); err != nil || room.Freed != 0 {
		t.Errorf("a session without frames gave room: %+v, %v", room, err)
	}
}

// When a command leaves the session's shell in another directory, the
// entries written after it are stamped with that directory. The session
// still belongs to the folder it was started in.
func TestLogStampsTheWorkingDirectory(t *testing.T) {
	ctx := context.Background()
	log, ts, _ := newTestLog(t)
	moved := resultEntry("t1", "c1", "Bash", "(no output)")
	moved.Result.Cwd = "/work/app/sub"
	if err := log.Append(ctx,
		promptEntry("p1", "t1", "go into sub"),
		callEntry("t1", "c1", "Bash", `{"command":"cd sub"}`),
		moved,
	); err != nil {
		t.Fatal(err)
	}
	// The loop stores the next reply in a write of its own.
	if err := log.Append(ctx, answerEntry("t1", "I am in sub now.")); err != nil {
		t.Fatal(err)
	}
	entries := ts.Entries()
	if first, last := entries[0].Cwd, entries[len(entries)-1].Cwd; first != "/work/app" || last != "/work/app/sub" {
		t.Errorf("the first entry is stamped %q and the last %q", first, last)
	}
	if ts.StartCwd() != "/work/app" {
		t.Errorf("the session now belongs to %q", ts.StartCwd())
	}
}

// After a cut, only the files given along the way that left the
// conversation with it are forgotten, to be given again; one still in
// what the model reads is not given a second time.
func TestCutForgetsOnlyTheFilesItTook(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	for name, text := range map[string]string{"api/CLAUDE.md": "api rules\n", "web/CLAUDE.md": "web rules\n", "api/x.go": "", "web/y.go": ""} {
		if err := os.MkdirAll(filepath.Join(dir, filepath.Dir(name)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	instr, err := instructions.Load(ctx, instructions.Options{Cwd: dir, ProjectNames: []string{"CLAUDE.md"}, Trusted: true})
	if err != nil {
		t.Fatal(err)
	}
	api, _ := instr.ForPath(ctx, filepath.Join(dir, "api/x.go"))
	web, _ := instr.ForPath(ctx, filepath.Join(dir, "web/y.go"))
	if len(api) != 1 || len(web) != 1 {
		t.Fatalf("late files: %v %v", api, web)
	}
	log, _, _ := newTestLog(t)
	// What the model reads after the cut holds web's file, not api's.
	if err := log.Append(ctx, promptEntry("p1", "t1", "go on\n"+instructions.RenderFiles(web))); err != nil {
		t.Fatal(err)
	}
	forgetGone(log, instr)
	if again, _ := instr.ForPath(ctx, filepath.Join(dir, "api/x.go")); len(again) != 1 {
		t.Errorf("the file the cut took is not given again: %v", again)
	}
	if again, _ := instr.ForPath(ctx, filepath.Join(dir, "web/y.go")); len(again) != 0 {
		t.Errorf("the file still in view is given a second time: %v", again)
	}
}
