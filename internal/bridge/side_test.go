package bridge

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/gnosh-ai/gnosh/internal/core"
	"github.com/gnosh-ai/gnosh/internal/transcript"
)

// fakeTurns asks the host's model as its turn would end, and sends the
// model back when the answer is not "yes".
type fakeTurns struct {
	prompts []string
	synced  [][]transcript.Entry
	called  []string
	asked   []error
}

func (f *fakeTurns) Prompt(_ context.Context, session, text string) {
	f.prompts = append(f.prompts, session+":"+text)
}
func (f *fakeTurns) Sync(_ string, e []transcript.Entry) { f.synced = append(f.synced, e) }
func (f *fakeTurns) Calling(_ string, c core.ToolCall)   { f.called = append(f.called, "calling "+c.ID) }
func (f *fakeTurns) Called(_ context.Context, _ string, c core.ToolCall, _ core.ToolResult) {
	f.called = append(f.called, "called "+c.ID)
}
func (f *fakeTurns) Notices(string) []string { return nil }
func (f *fakeTurns) End(ctx context.Context, _ string, reply core.Message, _ bool) EndVerdict {
	ans, err := Ask(ctx, SideRequest{Purpose: "goal-judge", System: "judge", Prompt: "is it done? " + reply.Text(), MaxTokens: 300})
	f.asked = append(f.asked, err)
	if err != nil {
		return EndVerdict{Notices: []string{err.Error()}}
	}
	if ans.Text == "yes" {
		return EndVerdict{}
	}
	return EndVerdict{Continue: true, Send: "not met: " + ans.Text, Driver: "mem-goal"}
}

func serveRaw(t *testing.T, s *Server, lines ...string) []map[string]json.RawMessage {
	t.Helper()
	var out bytes.Buffer
	if err := s.Serve(context.Background(), strings.NewReader(strings.Join(lines, "\n")+"\n"), &out); err != nil {
		t.Fatal(err)
	}
	var got []map[string]json.RawMessage
	for _, l := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		var m map[string]json.RawMessage
		if err := json.Unmarshal([]byte(l), &m); err != nil {
			t.Fatalf("line %q: %v", l, err)
		}
		got = append(got, m)
	}
	return got
}

// bridge.md §11.1: while it answers end, the bridge writes a side request
// and reads the host's answer; a request the host wrote before answering
// waits its turn, and an answer to no waiting request is an error reply.
func TestSpec11SideRequest(t *testing.T) {
	s, _ := newTest(t)
	ft := &fakeTurns{}
	s.opts.Turns = ft
	got := serveRaw(t, s,
		`{"op":"hello","id":"h","protocol":1,"side":true}`,
		`{"op":"end","id":"e","session":"S","reply":{"role":"assistant","content":[{"type":"text","text":"all done"}]},"finished":true}`,
		`{"op":"tools","id":"t"}`,
		`{"op":"side_answer","side_id":1,"answer":"no, the tests fail","stop":"end_turn"}`,
		`{"op":"side_answer","id":"late","side_id":7,"answer":"x"}`,
	)
	if len(got) != 5 {
		t.Fatalf("%d lines: %v", len(got), got)
	}
	var side SideRequest
	if err := json.Unmarshal(got[1]["side"], &side); err != nil || side.ID != 1 || side.Purpose != "goal-judge" || side.System != "judge" || !strings.Contains(side.Prompt, "all done") || side.MaxTokens != 300 {
		t.Fatalf("side line: %s (%v)", got[1]["side"], err)
	}
	var end Reply
	raw, _ := json.Marshal(got[2])
	_ = json.Unmarshal(raw, &end)
	if !end.OK || string(end.ID) != `"e"` || end.Action != "continue" || end.Send != "not met: no, the tests fail" || end.Driver != "mem-goal" {
		t.Fatalf("end: %+v", end)
	}
	if string(got[3]["id"]) != `"t"` || string(got[3]["ok"]) != "true" {
		t.Fatalf("the request written meanwhile: %v", got[3])
	}
	if string(got[4]["ok"]) != "false" {
		t.Fatalf("a side_answer nothing waits on: %v", got[4])
	}
}

// Without side in hello there is no model to ask, and the check says so;
// an answer cut off at its length is none.
func TestSpec11NoSide(t *testing.T) {
	s, _ := newTest(t)
	ft := &fakeTurns{}
	s.opts.Turns = ft
	got := serveRaw(t, s, hello, `{"op":"end","session":"S"}`)
	if len(got) != 2 || len(ft.asked) != 1 || !errors.Is(ft.asked[0], ErrNoSide) || string(got[1]["action"]) != `"end"` {
		t.Fatalf("%v %v", got, ft.asked)
	}

	s, _ = newTest(t)
	ft = &fakeTurns{}
	s.opts.Turns = ft
	serveRaw(t, s, `{"op":"hello","protocol":1,"side":true}`, `{"op":"end","session":"S"}`,
		`{"op":"side_answer","side_id":1,"answer":"ye","stop":"max_tokens"}`)
	if len(ft.asked) != 1 || ft.asked[0] == nil || !strings.Contains(ft.asked[0].Error(), "cut off") {
		t.Fatalf("a cut-off answer: %v", ft.asked)
	}
}

// bridge.md §11: a call carries the conversation through its reply, which
// Turns is given before the call runs; prompt with text starts a turn.
func TestSpec11CallAndPrompt(t *testing.T) {
	s, _ := newTest(t)
	ft := &fakeTurns{}
	s.opts.Turns = ft
	reps, err := serve(t, s, hello,
		`{"op":"prompt","session":"S","text":"fix it"}`,
		`{"op":"call","session":"S","call_id":"c1","name":"t","input":{},"entries":[{"type":"user","uuid":"u1","message":{"role":"user","content":[{"type":"text","text":"fix it"}]}}]}`,
		`{"op":"call","session":"S","call_id":"c2","name":"t","input":{},"entries":[{"type":"user","uuid":"u1"},{"type":"user","uuid":"u1"}]}`,
	)
	if err != nil || len(reps) != 4 || !reps[1].OK || !reps[2].OK {
		t.Fatalf("%v %+v", err, reps)
	}
	if reps[3].OK || reps[3].Error.Code != CodeBadRequest {
		t.Errorf("entries with a uuid twice: %+v", reps[3])
	}
	if strings.Join(ft.prompts, ",") != "S:fix it" || len(ft.synced) != 1 || len(ft.synced[0]) != 1 || strings.Join(ft.called, ",") != "calling c1,called c1" {
		t.Errorf("turns saw prompts %v, synced %d, calls %v", ft.prompts, len(ft.synced), ft.called)
	}
}
