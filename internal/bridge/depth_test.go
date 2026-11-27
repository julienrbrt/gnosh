package bridge

import (
	"strings"
	"testing"
)

// §10.8: the todo line is said only to a session that never stood in a todo.
func TestStepNoNudgeAfterATodo(t *testing.T) {
	s := withTexts(t)
	push := `{"op":"call","session":"S","call_id":"c1","name":"t","input":{"frames":[{"kind":"push","id":"f","session":"S"}]},"entries":[]}`
	pop := `{"op":"call","session":"S","call_id":"c2","name":"t","input":{"frames":[{"kind":"pop","id":"f","session":"S"}]},"entries":[]}`
	root := `[{"type":"user","uuid":"u1","sessionId":"S","message":{"role":"user","content":[{"type":"text","text":"go"}]}}]`
	step := `{"op":"step","session":"S","used":80000,"point":100000,"lean":true,"nudge":true,"entries":` + root + `}`
	reps, err := serve(t, s, hello, push, pop, step, step)
	if err != nil {
		t.Fatal(err)
	}
	for _, i := range []int{3, 4} {
		if strings.Contains(strings.Join(reps[i].Parts, "\n"), "working in no todo") {
			t.Errorf("step %d: a session that has popped a todo must not get the todo line: %q", i, reps[i].Parts)
		}
	}
}

// §10.7, §10.8: the todo line asks for one todo, kept shallow, and does not
// say to split the work into pieces.
func TestRootNudgeWording(t *testing.T) {
	n := rootNudge(50)
	if strings.Contains(n, "split") || strings.Contains(n, "mem.Next") {
		t.Errorf("the todo line must not suggest splitting the work: %q", n)
	}
	if !strings.Contains(n, "shallow") || !strings.Contains(n, "Push()") {
		t.Errorf("the todo line should ask for one shallow todo: %q", n)
	}
}

// §10.8: a call that would enter todos past the host's depth limit is not run.
func TestCallRefusedPastDepthLimit(t *testing.T) {
	s, ft := newTest(t)
	var lines []string
	for i, id := range []string{"a", "b", "c", "d"} {
		lines = append(lines, `{"op":"call","session":"S","call_id":"p`+itoa(i)+`","name":"t","input":{"frames":[{"kind":"push","id":"`+id+`","session":"S"}]},"entries":[]}`)
	}
	deeper := func(max, code string) string {
		return `{"op":"call","session":"S","call_id":"x","name":"t","max_depth":` + max + `,"input":{"code":"` + code + `"},"entries":[]}`
	}
	push := `mem.Todo(\"e\").Push()`
	lines = append(lines,
		deeper("4", push),                // 4 deep, one more: refused
		deeper("0", push),                // no limit: runs
		deeper("4", `mem.Pop(\"done\")`), // a pop: runs
		deeper("4", `mem.Think(\"x\")`),  // not entering: runs
		deeper("5", push),                // limit 5: runs
		deeper("4", `mem.Next()`),        // a step into the next todo is an entry too: refused
	)
	reps, err := serve(t, s, append([]string{hello}, lines...)...)
	if err != nil {
		t.Fatal(err)
	}
	after := reps[5:] // hello and the four pushes first
	want := []bool{true, false, false, false, false, true}
	if len(after) != len(want) {
		t.Fatalf("replies = %d, want %d", len(after), len(want))
	}
	for i, w := range want {
		if after[i].IsError != w {
			t.Errorf("call %d: refused=%v, want %v (%+v)", i+1, after[i].IsError, w, after[i].Content)
		}
	}
	if len(ft.calls) != 4+4 {
		t.Errorf("the tool ran %d times, want 8 (4 pushes and 4 allowed calls)", len(ft.calls))
	}
	if txt := after[0].Content[0].Text; !strings.Contains(txt, "limit of 4") || !strings.Contains(txt, "Pop") {
		t.Errorf("the refusal should name the limit and say to pop: %q", txt)
	}
}
