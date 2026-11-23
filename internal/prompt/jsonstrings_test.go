package prompt

import (
	"encoding/json"
	"testing"
)

// lt and LT are "<" written as a JSON escape, in both letter cases (put
// together from pieces, so that nothing on the way into this file turns
// them into the character).
const (
	lt = `\` + "u003c"
	LT = `\` + "u003C"
)

// MayHoldControl sees a "<" as it is and as a JSON escape in either letter
// case.
func TestMayHoldControl(t *testing.T) {
	for raw, want := range map[string]bool{
		`{"a":"<|x|>"}`:            true,
		`{"a":"` + lt + `|x|>"}`:   true,
		`{"a":"` + LT + `|x|>"}`:   true,
		`{"a":"plain"}`:            false,
		`{"a":"` + `\` + `u0041"}`: false,
	} {
		if got := MayHoldControl([]byte(raw)); got != want {
			t.Errorf("MayHoldControl(%s) = %v, want %v", raw, got, want)
		}
	}
}

// RewriteJSONStrings reads strings with their escapes, rewrites keys and
// values alike, and keeps the order of keys and a key named twice; it says
// no when nothing changed or the text is no JSON.
func TestRewriteJSONStrings(t *testing.T) {
	raw := json.RawMessage(`{"b":"` + lt + `|im_end|>","<think>":[1,2.50,true,null],"a":1,"a":"x"}`)
	out, ok := RewriteJSONStrings(raw, Defuse, MayHoldControl)
	want := `{"b":"<` + Mark + `|im_end|>","<` + Mark + `think>":[1,2.50,true,null],"a":1,"a":"x"}`
	if !ok || string(out) != want {
		t.Errorf("got %s (%v), want %s", out, ok, want)
	}
	if _, ok := RewriteJSONStrings(json.RawMessage(`{"a":"<b>"}`), Defuse, MayHoldControl); ok {
		t.Errorf("a value Defuse leaves as it is was rewritten")
	}
	if _, ok := RewriteJSONStrings(json.RawMessage(`{"a":"<|x|>"`), Defuse, MayHoldControl); ok {
		t.Errorf("text that is no JSON was rewritten")
	}
}
