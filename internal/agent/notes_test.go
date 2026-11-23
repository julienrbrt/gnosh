package agent

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/gnosh-ai/gnosh/internal/core"
)

func TestDuplicateKey(t *testing.T) {
	cases := []struct {
		in  string
		key string
		dup bool
	}{
		{`{}`, "", false},
		{`{"a":1,"b":2}`, "", false},
		{`{"a":1,"a":2}`, "a", true},
		{`{"command":"ls","timeout":5,"command":"rm -rf /"}`, "command", true},
		{`{"a":1,"a":2}`, "a", true},
		{`{"a":{"x":1,"y":2},"b":{"x":1}}`, "", false},
		{`{"a":{"x":1,"x":2}}`, "x", true},
		{`{"list":[{"k":1},{"k":2}]}`, "", false},
		{`{"list":[{"k":1,"k":2}]}`, "k", true},
		{`{"a":"a","b":"a"}`, "", false},
		{`{"a":["a","a"],"b":null,"c":true,"d":1.5}`, "", false},
		{`{"a":[1,2],"a":3}`, "a", true},
		{`{"a":{"b":1},"a":2}`, "a", true},
		{`{"n":1E1000,"a":1,"a":2}`, "a", true},
		{`[1,2,3]`, "", false},
		{`{"cut": tr`, "", false},
		{``, "", false},
	}
	for _, c := range cases {
		key, dup := duplicateKey(json.RawMessage(c.in))
		if key != c.key || dup != c.dup {
			t.Errorf("duplicateKey(%s) = %q, %v; want %q, %v", c.in, key, dup, c.key, c.dup)
		}
	}
}

// A call whose arguments name a key twice is refused before any gate or
// tool sees it: a guard and the tool must never read different values.
func TestDuplicateKeysAreRefusedBeforeTheGate(t *testing.T) {
	var mu sync.Mutex
	var gated, ran []string
	r := newRig(t, func(o *Options) {
		o.Tools = []core.Tool{tool("Bash", func(_ context.Context, call core.ToolCall, _ *core.ToolEnv) (core.ToolResult, error) {
			mu.Lock()
			ran = append(ran, call.ID)
			mu.Unlock()
			return core.TextResult("ran"), nil
		})}
		o.Gate = GateFunc(func(_ context.Context, req GateRequest) (Decision, error) {
			mu.Lock()
			gated = append(gated, req.Call.ID)
			mu.Unlock()
			if req.Call.ID == "rewritten" {
				return Decision{Allow: true, Input: json.RawMessage(`{"command":"ls","command":"rm -rf /"}`)}, nil
			}
			return Decision{Allow: true}, nil
		})
	})
	r.model.push(
		use(tc("twice", "Bash", `{"command":"ls","command":"rm -rf /"}`),
			tc("nested", "Bash", `{"command":"ls","env":{"A":"1","A":"2"}}`),
			tc("rewritten", "Bash", `{"command":"ls"}`),
			tc("fine", "Bash", `{"command":"ls"}`)),
		say("ok"),
	)
	if res := r.run("run them"); res.Reason != EndCompleted {
		t.Fatalf("result = %+v", res)
	}
	got, _ := toolResults(r.model.requests()[1])
	if !strings.Contains(got["twice"], `name the key "command" twice`) || !strings.Contains(got["nested"], `name the key "A" twice`) {
		t.Errorf("results = %q, %q", got["twice"], got["nested"])
	}
	if !strings.Contains(got["rewritten"], `names the key "command" twice`) || got["fine"] != "ran" {
		t.Errorf("results = %q, %q", got["rewritten"], got["fine"])
	}
	mu.Lock()
	defer mu.Unlock()
	if !slices.Equal(gated, []string{"rewritten", "fine"}) {
		t.Errorf("the gate saw %v", gated)
	}
	if !slices.Equal(ran, []string{"fine"}) {
		t.Errorf("the tool ran %v", ran)
	}
}

// A tool may find that a call cannot run as asked and still say what it
// was aimed at. The gate is asked about that first, so a refused path
// does not give away whether it exists; only then does the model read the
// tool's error.
func TestDescribeErrorGoesThroughTheGateFirst(t *testing.T) {
	var mu sync.Mutex
	var seen []GateRequest
	var asked []error
	ran := 0
	edit := &descTool{
		fakeTool: fakeTool{name: "Edit", run: func(context.Context, core.ToolCall, *core.ToolEnv) (core.ToolResult, error) {
			ran++
			return core.TextResult("edited"), nil
		}},
		describe: func(call core.ToolCall, _ *core.ToolEnv) (core.Action, error) {
			var args struct {
				Path string `json:"file_path"`
			}
			json.Unmarshal(call.Input, &args)
			act := core.Action{Kind: core.ActionEdit, Paths: []string{args.Path}, Summary: "edit " + args.Path}
			if args.Path == "" {
				return core.Action{}, errors.New("file_path is missing")
			}
			return act, errors.New("the file has not been read yet; read it first")
		},
	}
	r := newRig(t, func(o *Options) {
		o.Tools = []core.Tool{edit}
		o.Gate = GateFunc(func(ctx context.Context, req GateRequest) (Decision, error) {
			mu.Lock()
			seen = append(seen, req)
			mu.Unlock()
			for _, p := range req.Action.Paths {
				if strings.HasPrefix(p, "/secret/") {
					return Decision{Reason: "Editing under /secret is not allowed."}, nil
				}
			}
			// A gate that would ask a person is told there is no point.
			_, err := req.Ask(ctx, Question{Text: "edit it?"})
			mu.Lock()
			asked = append(asked, err)
			mu.Unlock()
			return Decision{Allow: true}, nil
		})
	})
	r.model.push(
		use(tc("secret", "Edit", `{"file_path":"/secret/key.pem"}`),
			tc("unread", "Edit", `{"file_path":"/work/main.go"}`),
			tc("empty", "Edit", `{}`)),
		say("ok"),
	)
	if res := r.run("edit them"); res.Reason != EndCompleted {
		t.Fatalf("result = %+v", res)
	}
	got, _ := toolResults(r.model.requests()[1])
	if got["secret"] != "Editing under /secret is not allowed." {
		t.Errorf("the refused path's result = %q: it must be the refusal, not the tool's own error", got["secret"])
	}
	if got["unread"] != "This call did not run: the file has not been read yet; read it first." {
		t.Errorf("result = %q", got["unread"])
	}
	if got["empty"] != "This call did not run: file_path is missing." {
		t.Errorf("result = %q", got["empty"])
	}
	if ran != 0 {
		t.Errorf("a call that cannot run ran %d times", ran)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(seen) != 3 {
		t.Fatalf("the gate was asked %d times", len(seen))
	}
	for _, req := range seen {
		if req.CannotRun == nil {
			t.Errorf("the gate was not told that %s cannot run", req.Call.ID)
		}
	}
	if seen[0].Action.Kind != core.ActionEdit || seen[0].Action.Paths[0] != "/secret/key.pem" {
		t.Errorf("the gate was not given what the call was aimed at: %+v", seen[0].Action)
	}
	if seen[2].Action.Kind != core.ActionOther {
		t.Errorf("a call with nothing worked out about it = %+v", seen[2].Action)
	}
	if len(asked) != 2 || !errors.Is(asked[0], ErrCannotRun) {
		t.Errorf("asking about a call that cannot run returned %v", asked)
	}
	if len(eventsOf[*QuestionEvent](r.rec)) != 0 {
		t.Error("a person was asked about a call that cannot run")
	}
	start := eventsOf[*ToolStartEvent](r.rec)[0]
	if start.Action.Summary != "edit /secret/key.pem" {
		t.Errorf("tool start = %+v", start)
	}
	denied := 0
	for _, e := range eventsOf[*ToolEndEvent](r.rec) {
		if e.Denied {
			denied++
		}
		if e.Ran {
			t.Errorf("%s is said to have run", e.CallID)
		}
	}
	if denied != 1 {
		t.Errorf("%d calls marked denied", denied)
	}
}

// Tell is the one way in for what the harness has to say to the model
// that is not a prompt, such as the end of a background command. While a
// turn runs it goes out with the next request; in an idle session it
// starts nothing and waits for the next request there is.
func TestTell(t *testing.T) {
	bash, started, letGo := heldTool("Bash")
	r := newRig(t, func(o *Options) { o.Tools = []core.Tool{bash} })
	note := Note{Text: "<note>background job 3 ended with exit code 0</note>", Source: "task-notification", Notice: "Background job 3 ended."}

	// Busy: between two rounds.
	r.model.push(use(tc("b", "Bash", `{}`)), say("I see the job ended"))
	done := make(chan TurnResult, 1)
	go func() { done <- r.run("build it") }()
	<-started
	if err := r.a.Tell(note); err != nil {
		t.Fatal(err)
	}
	if n := r.a.State().Notes; n != 1 {
		t.Errorf("%d notes wait", n)
	}
	letGo()
	if res := <-done; res.Reason != EndCompleted {
		t.Fatalf("result = %+v", res)
	}
	r.idle()
	if got, want := userBlocks(r.model.requests()[1]), []string{"result:b", note.Text}; !slices.Equal(got, want) {
		t.Errorf("the model was sent %q, want %q", got, want)
	}
	if n := eventsOf[*NoteEvent](r.rec); len(n) != 1 || n[0].Note.Source != "task-notification" {
		t.Errorf("note events = %+v", n)
	}
	if n := notices(r.rec, LevelInfo); len(n) != 1 || n[0] != "Background job 3 ended." {
		t.Errorf("the quiet line = %v", n)
	}
	stored := r.log.of(EntryNote)
	if len(stored) != 1 || stored[0].Source != "task-notification" || stored[0].Prompt != "" {
		t.Errorf("note entries = %+v", stored)
	}
	if n := len(eventsOf[*TurnStartEvent](r.rec)); n != 1 {
		t.Errorf("%d turns", n)
	}

	// Idle: it starts nothing, and stopping does not lose it.
	if err := r.a.Tell(Note{Text: "<note>job 4 ended</note>"}); err != nil {
		t.Fatal(err)
	}
	r.a.Interrupt()
	r.idle()
	if st := r.a.State(); st.Busy || st.Notes != 1 || r.model.count() != 2 {
		t.Fatalf("a note started something in an idle session: %+v, %d requests", st, r.model.count())
	}
	r.model.push(say("noted"))
	r.run("anything new?")
	if got, want := userBlocks(r.model.requests()[2]), []string{"anything new?", "<note>job 4 ended</note>"}; !slices.Equal(got, want) {
		t.Errorf("the next prompt went out as %q, want %q", got, want)
	}
	if r.a.State().Notes != 0 {
		t.Error("the note still waits")
	}

	if err := r.a.Tell(Note{Text: "  "}); !errors.Is(err, ErrBlank) {
		t.Errorf("an empty note: %v", err)
	}
	r.checkLog()
}

// Record puts a note in the conversation without starting a turn. In an
// idle session it is written to the log at once, between turns, so it
// stands before the next prompt (and is there for a session opened
// again); while a turn runs it goes out with the next request, as Tell's
// do.
func TestRecord(t *testing.T) {
	bash, started, letGo := heldTool("Bash")
	r := newRig(t, func(o *Options) { o.Tools = []core.Tool{bash} })

	// Idle: in the log at once, before the prompt that follows.
	if err := r.a.Record(Note{Text: "<note>$ make\nok</note>", Source: "shell", Notice: "The command is in the conversation."}); err != nil {
		t.Fatal(err)
	}
	if st := r.a.State(); st.Busy || st.Notes != 0 || r.model.count() != 0 {
		t.Fatalf("a recorded note started something or waits: %+v, %d requests", st, r.model.count())
	}
	r.idle()
	stored := r.log.of(EntryNote)
	if len(stored) != 1 || stored[0].Source != "shell" || stored[0].Turn != "" {
		t.Fatalf("note entries = %+v", stored)
	}
	if n := eventsOf[*NoteEvent](r.rec); len(n) != 1 || n[0].Note.Source != "shell" {
		t.Errorf("note events = %+v", n)
	}
	if n := notices(r.rec, LevelInfo); len(n) != 1 || n[0] != "The command is in the conversation." {
		t.Errorf("the quiet line = %v", n)
	}
	r.model.push(say("it built"))
	r.run("did it build?")
	if got, want := userBlocks(r.model.requests()[0]), []string{"<note>$ make\nok</note>", "did it build?"}; !slices.Equal(got, want) {
		t.Errorf("the model was sent %q, want %q", got, want)
	}

	// Busy: it waits for the turn's next request.
	r.model.push(use(tc("b", "Bash", `{}`)), say("seen"))
	done := make(chan TurnResult, 1)
	go func() { done <- r.run("build it") }()
	<-started
	if err := r.a.Record(Note{Text: "<note>$ ls\na.go</note>", Source: "shell"}); err != nil {
		t.Fatal(err)
	}
	if n := r.a.State().Notes; n != 1 {
		t.Errorf("%d notes wait", n)
	}
	letGo()
	if res := <-done; res.Reason != EndCompleted {
		t.Fatalf("result = %+v", res)
	}
	r.idle()
	if got, want := userBlocks(r.model.requests()[2]), []string{"result:b", "<note>$ ls\na.go</note>"}; !slices.Equal(got, want) {
		t.Errorf("the model was sent %q, want %q", got, want)
	}
	if err := r.a.Record(Note{Text: " "}); !errors.Is(err, ErrBlank) {
		t.Errorf("an empty note: %v", err)
	}
	r.checkLog()
}

// A note recorded while a turn writes its last reply, which no request of
// that turn takes, is written to the log as the turn ends, between turns:
// before the next prompt, and kept when the session ends before one.
func TestRecordWhileTheLastReplyIsWritten(t *testing.T) {
	r := newRig(t, nil)
	reached, release := make(chan struct{}), make(chan struct{})
	last := say("all done")
	last.at, last.wait = once(reached), release
	r.model.push(last)
	done := make(chan TurnResult, 1)
	go func() { done <- r.run("finish up") }()
	<-reached
	if err := r.a.Record(Note{Text: "<note>$ late\nok</note>", Source: "shell"}); err != nil {
		t.Fatal(err)
	}
	close(release)
	if res := <-done; res.Reason != EndCompleted {
		t.Fatalf("result = %+v", res)
	}
	r.idle()
	stored := r.log.of(EntryNote)
	if len(stored) != 1 || stored[0].Turn != "" || r.a.State().Notes != 0 {
		t.Fatalf("note entries = %+v, %d waiting", stored, r.a.State().Notes)
	}
	r.model.push(say("it printed ok"))
	r.run("what did it print?")
	if got, want := userBlocks(r.model.requests()[1]), []string{"<note>$ late\nok</note>", "what did it print?"}; !slices.Equal(got[len(got)-2:], want) {
		t.Errorf("the model was sent %q, ending with %q", got, want)
	}

	// Recorded during a turn that the session's end stops: in the log.
	r2 := newRig(t, nil)
	reached2 := make(chan struct{})
	r2.model.push(step{at: once(reached2), stall: true})
	go func() { _ = r2.run("long work") }()
	<-reached2
	if err := r2.a.Record(Note{Text: "<note>$ kept\nok</note>", Source: "shell"}); err != nil {
		t.Fatal(err)
	}
	if err := r2.a.End("prompt_input_exit"); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range r2.log.of(EntryNote) {
		found = found || slices.ContainsFunc(e.Blocks, func(b core.Block) bool { return strings.Contains(b.Text, "$ kept") })
	}
	if !found {
		t.Errorf("the note recorded before the end was lost: %+v", r2.log.of(EntryNote))
	}
}

// A note that is to wake the session starts a turn in an idle one. When
// the person stops that turn before the note went out, the note is kept
// and waits like any other.
func TestTellWake(t *testing.T) {
	r := newRig(t, nil)
	r.model.push(say("the job ended; shall I go on?"))
	if err := r.a.Tell(Note{Text: "<note>job 5 ended</note>", Source: "task-notification", Notice: "Background job 5 ended.", Wake: true}); err != nil {
		t.Fatal(err)
	}
	r.idle()
	if n := len(eventsOf[*TurnEndEvent](r.rec)); n != 1 || r.model.count() != 1 {
		t.Fatalf("%d turns, %d requests", n, r.model.count())
	}
	if p := r.log.of(EntryPrompt); len(p) != 1 || !p[0].Hidden || p[0].Source != "task-notification" {
		t.Errorf("prompt entries = %+v", p)
	}

	// Busy, then stopped before it went out.
	reached := make(chan struct{})
	r.model.push(step{at: once(reached), stall: true}, say("ok"))
	first := make(chan TurnResult, 1)
	go func() { first <- r.run("long work") }()
	<-reached
	r.a.Tell(Note{Text: "<note>job 6 ended</note>", Wake: true})
	waitQueue(t, r, "<note>job 6 ended</note>")
	r.a.Interrupt()
	if res := <-first; res.Reason != EndStopped {
		t.Fatalf("result = %+v", res)
	}
	r.idle()
	if st := r.a.State(); st.Notes != 1 || st.Busy {
		t.Fatalf("after the stop: %+v", st)
	}
	if n := len(eventsOf[*PromptReturnedEvent](r.rec)); n != 0 {
		t.Errorf("the note was reported as dropped %d times", n)
	}
	r.run("what happened meanwhile?")
	reqs := r.model.requests()
	if text := lastUserText(reqs[len(reqs)-1]); !strings.Contains(text, "job 6 ended") {
		t.Errorf("the note never reached the model: %q", text)
	}
	r.a.Close()
	if err := r.a.Tell(Note{Text: "late"}); !errors.Is(err, ErrClosed) {
		t.Errorf("after Close: %v", err)
	}
	r.checkLog()
}

// A turn that ends on a failure is its own moment, told before the
// general one. The reply that failed is stored, with what had arrived,
// under the reason "error".
func TestTurnFailedMoment(t *testing.T) {
	var mu sync.Mutex
	var trace []string
	r := newRig(t, func(o *Options) {
		o.Moments.TurnFailed = func(_ context.Context, res TurnResult) {
			mu.Lock()
			trace = append(trace, "failed:"+res.ErrorKind)
			mu.Unlock()
		}
		o.Moments.TurnEnded = func(_ context.Context, res TurnResult) {
			mu.Lock()
			trace = append(trace, "ended:"+string(res.Reason))
			mu.Unlock()
		}
	})
	r.model.push(
		say("fine"),
		step{evs: []core.Event{{Type: core.EventTextDelta, Text: "half an ans"}}, err: &core.ProviderError{Provider: "fake", Kind: core.ErrAuth, Status: 401}},
		broken(core.ErrInvalidRequest, 400),
	)
	r.run("one")
	r.run("two")
	r.run("three")
	r.idle()
	mu.Lock()
	want := []string{"ended:completed", "failed:authentication_failed", "ended:failed", "failed:invalid_request", "ended:failed"}
	if !slices.Equal(trace, want) {
		t.Errorf("moments = %v, want %v", trace, want)
	}
	mu.Unlock()
	as := r.log.of(EntryAssistant)
	if len(as) != 3 {
		t.Fatalf("%d assistant entries", len(as))
	}
	if as[1].StopReason != core.StopError || as[1].Message.Text() != "half an ans" || as[1].Silent {
		t.Errorf("the reply that failed half way = %+v", as[1])
	}
	if as[2].StopReason != core.StopError || !as[2].Silent || len(as[2].Message.Blocks) != 0 {
		t.Errorf("the reply that failed before it began = %+v", as[2])
	}
	replies := eventsOf[*ReplyEvent](r.rec)
	if len(replies) != 3 || replies[1].StopReason != core.StopError || replies[2].StopReason != core.StopError {
		t.Errorf("reply events = %+v", replies)
	}
	checkEvents(t, r.rec.all())
	r.checkLog()
}

func TestFires(t *testing.T) {
	got := Fires()
	for _, name := range []string{"PreToolUse", "PostToolUse", "PostToolUseFailure", "UserPromptSubmit", "SessionStart", "Stop", "SubagentStop", "StopFailure", "PermissionRequest", "PreCompact", "PostCompact", "SessionEnd", "SubagentStart"} {
		if !slices.Contains(got, name) {
			t.Errorf("Fires() does not list %s", name)
		}
	}
	for _, name := range []string{"Notification", "InstructionsLoaded"} {
		if slices.Contains(got, name) {
			t.Errorf("Fires() lists %s, which the loop does not fire", name)
		}
	}
	got[0] = "changed"
	if Fires()[0] == "changed" {
		t.Error("Fires() hands out its own list")
	}
}

// The wiring may count a whole message its own way.
func TestEstimateMessage(t *testing.T) {
	counted := 0
	r := newRig(t, func(o *Options) {
		o.EstimateMessage = func(m core.Message) int {
			counted++
			return 1000
		}
	})
	r.model.push(say("one"), say("two"))
	r.run("first")
	r.run("second")
	if counted == 0 {
		t.Fatal("the wiring's estimate was never asked")
	}
	// Three messages went out with the second request. Its size is the
	// first request's count, as the provider reported it (100), and the
	// wiring's guess for the two messages that are new, scaled by how far
	// its guesses have run off the provider's counts. The provider counted
	// a tenth of the guess; the scale stops at a half.
	if c := r.a.Context(); c.Used != 100+2000/2 {
		t.Errorf("context = %+v", c)
	}
}

// A result with nothing in it says so: no message a model is sent may
// hold an empty text block.
func TestEmptyToolResultSaysSo(t *testing.T) {
	r := newRig(t, func(o *Options) {
		o.Tools = []core.Tool{
			tool("Nothing", func(context.Context, core.ToolCall, *core.ToolEnv) (core.ToolResult, error) {
				return core.ToolResult{}, nil
			}),
			tool("Blank", func(context.Context, core.ToolCall, *core.ToolEnv) (core.ToolResult, error) {
				return core.ToolResult{Content: []core.Block{core.Text(""), core.Text("after a blank")}}, nil
			}),
		}
	})
	r.model.push(use(tc("n", "Nothing", `{}`), tc("b", "Blank", `{}`)), say("ok"))
	r.run("go")
	req := r.model.requests()[1]
	got, _ := toolResults(req)
	if got["n"] != "(no output)" || got["b"] != "after a blank" {
		t.Errorf("results = %q", got)
	}
	if err := checkView(req.Messages); err != nil {
		t.Errorf("the request is not valid: %v", err)
	}
}

func TestCheckView(t *testing.T) {
	user := func(blocks ...core.Block) core.Message { return core.Message{Role: core.RoleUser, Blocks: blocks} }
	asst := func(blocks ...core.Block) core.Message {
		return core.Message{Role: core.RoleAssistant, Blocks: blocks}
	}
	call := func(id string) core.Block { return core.ToolUse(id, "Read", nil) }
	result := func(id string) core.Block { return core.ToolResultBlock(id, core.TextResult("ok")) }
	text := core.Text

	good := [][]core.Message{
		{user(text("hi"))},
		{user(text("hi")), asst(text("hello"))},
		{user(text("hi")), asst(call("a"), call("b")), user(result("a"), result("b"), text("and then")), asst(text("done"))},
		{user(text("hi")), asst(text("x"), call("a")), user(result("a"))},
	}
	for i, msgs := range good {
		if err := checkView(msgs); err != nil {
			t.Errorf("good conversation %d: %v", i, err)
		}
	}
	bad := map[string][]core.Message{
		"it is empty":                           {},
		"does not start with a user message":    {asst(text("hello"))},
		"both from the user":                    {user(text("a")), user(text("b"))},
		"both from the assistant":               {user(text("a")), asst(text("b")), asst(text("c"))},
		"message 1 is empty":                    {user(text("a")), asst()},
		"holds an empty text block":             {user(text(""))},
		"have no results":                       {user(text("a")), asst(call("x"))},
		`"x" (Read) of message 1 has no result`: {user(text("a")), asst(call("x"), call("y")), user(result("y"))},
		"a tool result after other blocks":      {user(text("a")), asst(call("x")), user(text("note"), result("x"))},
		"two results for the tool call":         {user(text("a")), asst(call("x")), user(result("x"), result("x"))},
		"did not call":                          {user(text("a")), asst(text("b")), user(result("x"))},
		`a result for "x", which`:               {user(result("x"))},
	}
	for want, msgs := range bad {
		err := checkView(msgs)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("checkView = %v, want an error that says %q", err, want)
		}
	}
}

// The wiring's filter for arguments runs before anything reads them: the
// gate, the tool's own account of the call and the tool all see what it
// returned, and the log keeps both what the model wrote and what ran.
func TestCleanInput(t *testing.T) {
	var mu sync.Mutex
	gate, describe, ran := map[string]string{}, map[string]string{}, map[string]string{}
	note := func(m map[string]string, call core.ToolCall) {
		mu.Lock()
		m[call.ID] = string(call.Input)
		mu.Unlock()
	}
	edit := &descTool{
		fakeTool: fakeTool{name: "Edit", run: func(_ context.Context, call core.ToolCall, _ *core.ToolEnv) (core.ToolResult, error) {
			note(ran, call)
			return core.TextResult("edited"), nil
		}},
		describe: func(call core.ToolCall, _ *core.ToolEnv) (core.Action, error) {
			note(describe, call)
			return core.Action{Kind: core.ActionEdit}, nil
		},
	}
	r := newRig(t, func(o *Options) {
		o.Tools = []core.Tool{edit}
		o.CleanInput = func(call core.ToolCall) json.RawMessage {
			if call.ID == "broken" {
				return json.RawMessage(`"not an object"`)
			}
			// The model read "<~|" where a file holds "<|", and writes it
			// back the way it read it.
			return json.RawMessage(strings.ReplaceAll(string(call.Input), "<~|", "<|"))
		}
		o.Gate = GateFunc(func(_ context.Context, req GateRequest) (Decision, error) {
			note(gate, req.Call)
			return Decision{Allow: true}, nil
		})
	})
	r.model.push(
		use(tc("turned", "Edit", `{"old_string":"<~|im_end|>"}`), tc("broken", "Edit", `{}`), tc("same", "Edit", `{"a":1}`)),
		say("ok"),
	)
	if res := r.run("edit"); res.Reason != EndCompleted {
		t.Fatalf("result = %+v", res)
	}
	mu.Lock()
	defer mu.Unlock()
	const want = `{"old_string":"<|im_end|>"}`
	if gate["turned"] != want || describe["turned"] != want || ran["turned"] != want {
		t.Errorf("the gate saw %s, the tool's account %s, the tool %s; want %s for all", gate["turned"], describe["turned"], ran["turned"], want)
	}
	if _, ok := gate["broken"]; ok || ran["broken"] != "" {
		t.Error("a call whose arguments could not be made ready went on")
	}
	got, _ := toolResults(r.model.requests()[1])
	if !strings.Contains(got["broken"], "could not be made ready") {
		t.Errorf("result = %q", got["broken"])
	}
	// The model's own words are kept as it wrote them; the result says
	// what the call ran with.
	as := r.log.of(EntryAssistant)[0]
	if string(as.Message.ToolUses()[0].Input) != `{"old_string":"<~|im_end|>"}` {
		t.Errorf("the reply in the log = %s", as.Message.ToolUses()[0].Input)
	}
	for _, e := range r.log.of(EntryResult) {
		switch e.ToolUseID {
		case "turned":
			if string(e.Input) != want {
				t.Errorf("the result entry does not keep what the call ran with: %s", e.Input)
			}
		case "same":
			if e.Input != nil {
				t.Errorf("a call that ran as written has Input %s", e.Input)
			}
		}
	}
	if ev := eventsOf[*ToolStartEvent](r.rec)[0]; string(ev.Input) != want {
		t.Errorf("tool start = %s", ev.Input)
	}
}

// What the loop itself answers a call with goes through the same filter as
// a tool's output, and gnosh's own lines never do.
func TestCleanResultCoversWhatTheLoopAnswers(t *testing.T) {
	var mu sync.Mutex
	var cleaned []string
	r := newRig(t, func(o *Options) {
		o.Tools = []core.Tool{tool("Bash", nil)}
		o.CleanResult = func(call core.ToolCall, res core.ToolResult) core.ToolResult {
			mu.Lock()
			cleaned = append(cleaned, call.ID)
			mu.Unlock()
			for _, b := range res.Content {
				if strings.Contains(b.Text, "<gnosh>") {
					t.Errorf("the filter was given gnosh's own line: %q", b.Text)
				}
			}
			return res
		}
		o.Gate = GateFunc(func(_ context.Context, req GateRequest) (Decision, error) {
			return Decision{Allow: req.Call.ID != "denied", Reason: "not allowed"}, nil
		})
		o.Moments.AfterTool = func(context.Context, ToolDone) (ToolVerdict, error) {
			return ToolVerdict{}, errors.New("hook missing")
		}
	})
	r.model.push(use(tc("ran", "Bash", `{}`), tc("denied", "Bash", `{}`), tc("unknown", "Nope", `{}`)), silence(), say("ok"))
	if res := r.run("go"); res.Reason != EndCompleted {
		t.Fatalf("result = %+v", res)
	}
	mu.Lock()
	defer mu.Unlock()
	slices.Sort(cleaned)
	if !slices.Equal(cleaned, []string{"denied", "ran", "unknown"}) {
		t.Errorf("results that went through the filter: %v", cleaned)
	}
}
