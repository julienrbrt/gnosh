package app

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/gnosh-ai/gnosh/internal/core"
)

// A notification's text is one line without the characters a terminal
// acts on or that turn text around, cut to 200 characters.
func TestPushText(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"build done:\n0 failures", "build done: 0 failures"},
		{"a\x1b]9;forged\x07b", "a ]9;forged b"},
		{"x\u009dy\u0085z", "x y z"},
		{"pay \u202eexe.txt\u202c now", "pay exe.txt now"},
		{"a bad \xff byte", "a bad byte"},
		{" \t\x00 ", ""},
	} {
		if got := pushText(c.in); got != c.want {
			t.Errorf("pushText(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	if got := pushText(strings.Repeat("é", 300)); got != strings.Repeat("é", 199)+"…" {
		t.Errorf("a long message is cut to %d characters: %q", len([]rune(got)), got)
	}
}

// A notification is sent once in a minute at most.
func TestPushOncePerMinute(t *testing.T) {
	l := &live{p: &program{now: time.Now}}
	then := time.Now().Add(-30 * time.Second)
	l.pushLast.Store(&then)
	res, err := (&pushTool{l: l}).Run(context.Background(), core.ToolCall{Input: []byte(`{"message":"done"}`)}, nil)
	if err != nil || !strings.Contains(core.TextOf(res.Content), "one was sent less than a minute ago") {
		t.Errorf("a second notification in a minute: %v, %q", err, core.TextOf(res.Content))
	}
}
