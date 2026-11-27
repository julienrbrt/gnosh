package bridge

import (
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/gnosh-ai/gnosh/internal/core"
	"github.com/gnosh-ai/gnosh/internal/transcript"
)

// standing is a conversation in frame "f", entered by call c1, with
// rounds of work after it that kept nothing in mem (unless kept is set, in
// which case one round calls mem.Think).
func standing(kept bool) string {
	rounds := []string{
		`{"type":"user","uuid":"u1","sessionId":"S","message":{"role":"user","content":[{"type":"text","text":"go"}]}}`,
		`{"type":"assistant","uuid":"a1","sessionId":"S","stop":"tool_use","message":{"role":"assistant","content":[{"type":"tool_use","id":"c1","name":"gnovm_run","input":{"name":"mem","code":"mem.Todo(\"t\").Push()"}}]}}`,
		`{"type":"user","uuid":"r1","sessionId":"S","source":"tool_results","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"c1","content":[{"type":"text","text":"in f"}]}]}}`,
		`{"type":"frame","uuid":"fr1","sessionId":"S","op":"push","frame":"f","label":"do f","toolUseId":"c1"}`,
	}
	for i, n := range []string{"2", "3", "4"} {
		call := `{"type":"tool_use","id":"c` + n + `","name":"Bash","input":{"command":"grep -rn needle /app/src"}}`
		if kept && i == 1 {
			call = `{"type":"tool_use","id":"c` + n + `","name":"gnovm_run","input":{"name":"mem","code":"mem.Think(\"x\")"}}`
		}
		rounds = append(rounds,
			`{"type":"assistant","uuid":"a`+n+`","sessionId":"S","stop":"tool_use","message":{"role":"assistant","content":[{"type":"text","text":"looking for the needle"},`+call+`]}}`,
			`{"type":"user","uuid":"r`+n+`","sessionId":"S","source":"tool_results","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"c`+n+`","content":[{"type":"text","text":"/app/src/a.py:12: needle = 42\n/app/src/b.py:7: needle = 43\n`+strings.Repeat("x", 800)+`"}]}]}}`)
	}
	rounds = append(rounds, `{"type":"assistant","uuid":"a9","sessionId":"S","stop":"tool_use","message":{"role":"assistant","content":[{"type":"text","text":"the latest reply, kept"},{"type":"tool_use","id":"c9","name":"Bash","input":{"command":"ls"}}]}}`)
	return "[" + strings.Join(rounds, ",") + "]"
}

// autoServer is a server whose mem keeps are the fake tool's calls.
func autoServer(t *testing.T, failKeep bool) (*Server, *fakeTool) {
	t.Helper()
	s, ft := newTest(t)
	s.opts.RecordsMem = func(call, _ core.Block) bool { return strings.Contains(string(call.Input), "mem.Think") }
	s.opts.KeepCall = func(text string) (string, json.RawMessage) {
		in, _ := json.Marshal(map[string]any{"text": text, "fail": failKeep})
		return "t", in
	}
	push := `{"op":"call","session":"S","call_id":"c1","name":"t","input":{"frames":[{"kind":"push","id":"f","session":"S"}]},"entries":[]}`
	if _, err := serve(t, s, hello, push); err != nil {
		t.Fatal(err)
	}
	ft.calls = nil
	return s, ft
}

func room(auto bool, entries string) string {
	a := ""
	if auto {
		a = `"autokeep":true,`
	}
	return `{"op":"room","session":"S","need":100,"covered":true,` + a + `"entries":` + entries + `}`
}

// §10.7: with covered alone, a cut over work mem has no record of is left to
// the host's summary, as before.
func TestRoomUncoveredRefusedWithoutAutokeep(t *testing.T) {
	s, ft := autoServer(t, false)
	reps, err := serve(t, s, hello, room(false, standing(false)))
	if err != nil {
		t.Fatal(err)
	}
	r := reps[1]
	if r.Freed != 0 || len(r.NewSlides) != 0 || !strings.Contains(r.Notice, "no record of") {
		t.Errorf("want a refusal, got freed=%d slides=%d notice=%q", r.Freed, len(r.NewSlides), r.Notice)
	}
	if len(ft.calls) != 0 {
		t.Errorf("nothing may be written to mem without autokeep, got %d calls", len(ft.calls))
	}
}

// §10.7: with autokeep the bridge keeps a digest of the uncovered work in
// mem as a thought, then slides, and tells the model what it kept.
func TestRoomAutokeepKeepsThenSlides(t *testing.T) {
	s, ft := autoServer(t, false)
	reps, err := serve(t, s, hello, room(true, standing(false)))
	if err != nil {
		t.Fatal(err)
	}
	r := reps[1]
	if r.Freed == 0 || len(r.NewSlides) == 0 {
		t.Fatalf("want a slide, got freed=%d slides=%d notice=%q", r.Freed, len(r.NewSlides), r.Notice)
	}
	if len(ft.calls) != 1 {
		t.Fatalf("want one keep call, got %d", len(ft.calls))
	}
	kept := string(ft.calls[0].Input)
	for _, want := range []string{"looking for the needle", "grep -rn needle /app/src", "needle = 42"} {
		if !strings.Contains(kept, want) {
			t.Errorf("the kept digest lacks %q: %s", want, kept)
		}
	}
	// The newest round stays in the view and is not in the digest.
	if strings.Contains(kept, "the latest reply") {
		t.Errorf("the digest holds a round that did not leave: %s", kept)
	}
	if note := strings.Join(r.Parts, "\n"); !strings.Contains(note, "gnosh kept this much") || !strings.Contains(note, "needle = 42") {
		t.Errorf("the model must be told what was kept, got %q", r.Parts)
	}
	if !strings.Contains(r.Notice, "kept a digest") {
		t.Errorf("notice should say a digest was kept: %q", r.Notice)
	}
}

// §10.7: work the model kept itself is covered; autokeep writes nothing.
func TestRoomAutokeepLeavesCoveredWorkAlone(t *testing.T) {
	s, ft := autoServer(t, false)
	reps, err := serve(t, s, hello, room(true, standing(true)))
	if err != nil {
		t.Fatal(err)
	}
	if reps[1].Freed == 0 {
		t.Fatalf("want a slide, got notice %q", reps[1].Notice)
	}
	if len(ft.calls) != 0 {
		t.Errorf("covered work needs no keep, got %d calls", len(ft.calls))
	}
	if strings.Contains(strings.Join(reps[1].Parts, "\n"), "gnosh kept this much") {
		t.Errorf("no digest note for covered work: %q", reps[1].Parts)
	}
}

// §10.7: work is never dropped that is kept nowhere. When mem does not take
// the digest, nothing slides.
func TestRoomAutokeepFailureSlidesNothing(t *testing.T) {
	s, _ := autoServer(t, true)
	reps, err := serve(t, s, hello, room(true, standing(false)))
	if err != nil {
		t.Fatal(err)
	}
	r := reps[1]
	if r.Freed != 0 || len(r.NewSlides) != 0 || len(r.Parts) != 0 || !strings.Contains(r.Notice, "no record of") {
		t.Errorf("want a refusal, got freed=%d slides=%d parts=%q notice=%q", r.Freed, len(r.NewSlides), r.Parts, r.Notice)
	}
}

// §10.7: autokeep does not lift the slide limit of §10.4.
func TestRoomAutokeepKeepsTheSlideLimit(t *testing.T) {
	s, _ := autoServer(t, false)
	s.session("S").slides = map[string]int{"f": maxFrameSlides}
	reps, err := serve(t, s, hello, room(true, standing(false)))
	if err != nil {
		t.Fatal(err)
	}
	if r := reps[1]; r.Freed != 0 || !strings.Contains(r.Notice, "without being popped") {
		t.Errorf("want the slide limit to hold, got freed=%d notice=%q", r.Freed, r.Notice)
	}
}

// §10.7: the digest is bounded however much the work was.
func TestDigestIsBounded(t *testing.T) {
	var parts, removed []string
	for i := 0; i < 400; i++ {
		id := "c" + strings.Repeat("9", 1) + string(rune('a'+i%26)) + strings.Repeat("z", i/26)
		u1, u2 := "a"+id, "r"+id
		parts = append(parts,
			`{"type":"assistant","uuid":"`+u1+`","message":{"role":"assistant","content":[{"type":"tool_use","id":"`+id+`","name":"Bash","input":{"command":"echo `+strings.Repeat("y", 500)+`"}}]}}`,
			`{"type":"user","uuid":"`+u2+`","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"`+id+`","content":[{"type":"text","text":"`+strings.Repeat("w", 900)+`"}]}]}}`)
		removed = append(removed, u1, u2)
	}
	entries := entriesFrom(t, "["+strings.Join(parts, ",")+"]")
	d := digestOf(entries, removed)
	if d == "" || len(d) > digestMaxChars+400 {
		t.Errorf("digest is %d bytes, want 1..%d", len(d), digestMaxChars+400)
	}
	if !strings.Contains(d, "rounds left out") {
		t.Errorf("a digest that leaves rounds out must say so")
	}
}

// §10.7: the todo line, at two and at three quarters of the way to the point
// with the session in no todo, once each, and again after the context stood
// low; never inside a todo, and never without the host asking.
func TestStepNudgesWorkInNoTodo(t *testing.T) {
	root := `[{"type":"user","uuid":"u1","sessionId":"S","message":{"role":"user","content":[{"type":"text","text":"go"}]}}]`
	step := func(used int, extra string) string {
		return `{"op":"step","session":"S","used":` + itoa(used) + `,"point":100000,"lean":true,` + extra + `"entries":` + root + `}`
	}
	s := withTexts(t)
	reps, err := serve(t, s, hello,
		step(30000, `"nudge":true,`), // below: nothing
		step(45000, `"nudge":true,`), // first line
		step(50000, `"nudge":true,`), // already said
		step(80000, `"nudge":true,`), // second line
		step(90000, `"nudge":true,`), // already said twice
		step(10000, `"nudge":true,`), // stood low: resets
		step(45000, `"nudge":true,`), // first line again
		step(80000, ``),              // not asked for
	)
	if err != nil {
		t.Fatal(err)
	}
	has := func(i int) bool { return strings.Contains(strings.Join(reps[i].Parts, "\n"), "working in no todo") }
	want := []bool{false, true, false, true, false, false, true}
	for i, w := range want {
		if has(i+1) != w {
			t.Errorf("step %d: nudge=%v, want %v (%q)", i+1, has(i+1), w, reps[i+1].Parts)
		}
	}
	s2 := withTexts(t)
	reps, _ = serve(t, s2, hello, step(80000, ``))
	if strings.Contains(strings.Join(reps[1].Parts, "\n"), "working in no todo") {
		t.Errorf("no todo line without nudge")
	}
}

// §10.7: no todo line while the session stands in a todo.
func TestStepNoNudgeInsideTodo(t *testing.T) {
	s := withTexts(t)
	reps, err := serve(t, s, hello, `{"op":"step","session":"S","used":80000,"point":100000,"nudge":true,"entries":`+standing(true)+`}`)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.Join(reps[1].Parts, "\n"), "working in no todo") {
		t.Errorf("a session in a todo must not get the todo line: %q", reps[1].Parts)
	}
}

func itoa(n int) string { return strconv.Itoa(n) }

// entriesFrom reads entries as a host sends them.
func entriesFrom(t *testing.T, js string) []transcript.Entry {
	t.Helper()
	var es []transcript.Entry
	if err := json.Unmarshal([]byte(js), &es); err != nil {
		t.Fatal(err)
	}
	return es
}

// §10.7: mem refuses text that looks like a secret, a long hash or key; the
// digest is then kept again with long runs cut short, and the slide goes on.
func TestAutokeepRetriesShortened(t *testing.T) {
	s, ft := newTest(t)
	s.opts.RecordsMem = func(call, _ core.Block) bool { return strings.Contains(string(call.Input), "mem.Think") }
	long := regexp.MustCompile(`\S{20,}`)
	s.opts.KeepCall = func(text string) (string, json.RawMessage) {
		in, _ := json.Marshal(map[string]any{"text": text, "fail": long.MatchString(text)})
		return "t", in
	}
	push := `{"op":"call","session":"S","call_id":"c1","name":"t","input":{"frames":[{"kind":"push","id":"f","session":"S"}]},"entries":[]}`
	if _, err := serve(t, s, hello, push); err != nil {
		t.Fatal(err)
	}
	ft.calls = nil
	entries := strings.Replace(standing(false), "needle = 43", "needle = 43 deadbeefdeadbeefdeadbeefdeadbeef", 1)
	reps, err := serve(t, s, hello, room(true, entries))
	if err != nil {
		t.Fatal(err)
	}
	r := reps[1]
	if r.Freed == 0 {
		t.Fatalf("want a slide with the shortened digest, got notice %q", r.Notice)
	}
	if len(ft.calls) != 2 {
		t.Fatalf("want the refused keep and its retry, got %d calls", len(ft.calls))
	}
	if kept := string(ft.calls[1].Input); strings.Contains(kept, "deadbeefdeadbeefdeadbeef") || !strings.Contains(kept, "shortened") {
		t.Errorf("the retry must cut the long run short and say so: %s", kept)
	}
	if note := strings.Join(r.Parts, "\n"); strings.Contains(note, "deadbeefdeadbeefdeadbeef") {
		t.Errorf("the model is shown what was kept, shortened: %q", note)
	}
}
