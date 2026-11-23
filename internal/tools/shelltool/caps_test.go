package shelltool

import (
	"strings"
	"testing"

	"github.com/gnosh-ai/gnosh/internal/core"
)

// CLAUDE_CODE_SCRIPT_CAPS: a model's command that holds a capped text
// runs as many times as the cap says, and one more is refused, the text
// matched as written ("deploy.sh $(x)" counts); the person's own and a
// command holding no capped text are not counted.
func TestScriptCaps(t *testing.T) {
	m := newManager(t, Options{ScriptCaps: map[string]int{"deploy.sh": 2, "never.sh": 0}})
	env := newEnv(t)
	ran := func(res core.ToolResult) bool {
		return !strings.Contains(core.TextOf(res.Content), "CLAUDE_CODE_SCRIPT_CAPS")
	}
	for i, cmd := range []string{"echo deploy.sh one", "echo ./deploy.sh $(echo two)"} {
		if res := run(t, t.Context(), m, BashName, env, args{"command": cmd}); !ran(res) {
			t.Fatalf("run %d of a capped script was refused: %s", i+1, core.TextOf(res.Content))
		}
	}
	if res := run(t, t.Context(), m, BashName, env, args{"command": "echo deploy.sh three"}); ran(res) {
		t.Errorf("a third run of a script capped at two was let run: %s", core.TextOf(res.Content))
	}
	if res := run(t, t.Context(), m, BashName, env, args{"command": "echo never.sh"}); ran(res) {
		t.Errorf("a script capped at none was let run: %s", core.TextOf(res.Content))
	}
	if res := run(t, t.Context(), m, BashName, env, args{"command": "echo other"}); !ran(res) {
		t.Errorf("a command holding no capped text was refused: %s", core.TextOf(res.Content))
	}
	input := []byte(`{"command": "echo deploy.sh by the person"}`)
	res, err := m.PersonsBash().Run(t.Context(), core.ToolCall{ID: "p1", Name: BashName, Input: input}, env)
	if err != nil || !ran(res) {
		t.Errorf("the person's own command was counted against the cap: %v %s", err, core.TextOf(res.Content))
	}
}

// Options.Isolate is given the program line of a command that runs
// outside the sandbox, and what it returns runs.
func TestIsolate(t *testing.T) {
	m := newManager(t, Options{Isolate: func(argv []string) []string {
		return append([]string{"/usr/bin/env", "ISOLATED=yes"}, argv...)
	}})
	if res := run(t, t.Context(), m, BashName, newEnv(t), args{"command": "echo [$ISOLATED]"}); !strings.Contains(core.TextOf(res.Content), "[yes]") {
		t.Errorf("the command did not run through Isolate: %s", core.TextOf(res.Content))
	}
}
