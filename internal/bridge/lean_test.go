package bridge

import (
	"strings"
	"testing"

	"github.com/gnosh-ai/gnosh/internal/prompt"
)

// withTexts gives a server gnosh's own words, so prompt and step have
// something to hand out.
func withTexts(t *testing.T) *Server {
	t.Helper()
	s, _ := newTest(t)
	s.opts.Texts = prompt.Builtin()
	return s
}

// bridge.md §10: lean leaves out the per-prompt nudges (the mem line and
// the frame line) but keeps the system memory text. Without lean they are
// all present.
func TestLeanPromptDropsNudges(t *testing.T) {
	full := withTexts(t)
	lean := withTexts(t)
	fr, ferr := serve(t, full, hello, `{"op":"prompt","id":1,"session":"S","first":true,"text":"go"}`)
	lr, lerr := serve(t, lean, hello, `{"op":"prompt","id":1,"session":"S","first":true,"text":"go","lean":true}`)
	if ferr != nil || lerr != nil {
		t.Fatalf("errs %v %v", ferr, lerr)
	}
	f, l := fr[1], lr[1]
	if f.System == "" || l.System == "" || f.System != l.System {
		t.Errorf("both must carry the same system memory text; full=%d lean=%d", len(f.System), len(l.System))
	}
	if len(f.Parts) == 0 {
		t.Errorf("full prompt should carry the mem line and more, got none")
	}
	if len(l.Parts) != 0 {
		t.Errorf("lean prompt should carry no nudges, got %q", l.Parts)
	}
}

// Under lean, step leaves out the large-step line and the mem reminder.
// The frame must be entered (a push) and have grown past half its room for
// the large-step line to be due without lean.
func TestLeanStepDropsLargeAndReminder(t *testing.T) {
	// A frame "f": the call c1 that entered it, its result, and the push.
	// Each entry carries the session id the view reads.
	e := func(ss ...string) string { return "[" + strings.Join(ss, ",") + "]" }
	user := `{"type":"user","uuid":"u1","sessionId":"S","message":{"role":"user","content":[{"type":"text","text":"go"}]}}`
	asst := `{"type":"assistant","uuid":"a1","sessionId":"S","stop":"tool_use","message":{"role":"assistant","content":[{"type":"tool_use","id":"c1","name":"t","input":{}}]}}`
	res := `{"type":"user","uuid":"r1","sessionId":"S","source":"tool_results","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"c1","content":[{"type":"text","text":"in f"}]}]}}`
	frame := `{"type":"frame","uuid":"fr1","sessionId":"S","op":"push","frame":"f","label":"do f","toolUseId":"c1"}`
	entries := e(user, asst, res, frame)
	push := `{"op":"call","session":"S","call_id":"c1","name":"t","input":{"frames":[{"kind":"push","id":"f","session":"S"}]},"entries":` + e(user, asst) + `}`

	run := func(lean bool) Reply {
		s := withTexts(t)
		// A step sets the server's token count, so the push records where
		// the frame was entered; then a step well past the point is due the
		// large-step line (without lean).
		setUsed := `{"op":"step","id":1,"session":"S","used":1000,"point":96000,"entries":` + entries + `}`
		step := `{"op":"step","id":2,"session":"S","used":90000,"point":96000,"entries":` + entries + `}`
		if lean {
			step = `{"op":"step","id":2,"session":"S","used":90000,"point":96000,"lean":true,"entries":` + entries + `}`
		}
		reps, err := serve(t, s, hello, setUsed, push, step)
		if err != nil {
			t.Fatalf("serve: %v", err)
		}
		return reps[3]
	}
	full := run(false)
	lean := run(true)
	joined := strings.Join(full.Parts, "\n")
	if joined == "" {
		t.Fatalf("full step should carry the large-step line, got none")
	}
	if len(lean.Parts) != 0 {
		t.Errorf("lean step should carry no large-step line or reminder, got %q", lean.Parts)
	}
}
