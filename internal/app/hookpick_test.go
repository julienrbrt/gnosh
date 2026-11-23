package app

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/gnosh-ai/gnosh/internal/app/apptest"
)

// The /hooks browser's choices are the hooks in force, by moment, each
// with its command, how it runs and the file it is written in.
func TestHookChoices(t *testing.T) {
	w := apptest.NewWorld(t)
	w.Settings(`{"hooks": {
		"PreToolUse": [{"matcher": "Bash", "hooks": [{"type": "command", "command": "true", "if": "Bash(git push *)", "timeout": 5}]}],
		"Stop": [{"hooks": [{"type": "command", "command": "echo done", "async": true}]}]}}`)
	var got []HookChoice
	var note string
	_, r := attach(t, w, nil, func(ctx context.Context, c SessionControls, f *scripted) int {
		got, note = c.HookChoices()
		return 0
	})
	if r.code != 0 {
		t.Fatalf("exit code %d", r.code)
	}
	if len(got) != 2 || note != "" {
		t.Fatalf("got %+v, note %q", got, note)
	}
	pre, stop := got[0], got[1]
	if pre.Event != "PreToolUse" || pre.Command != "true" || !slices.Equal(pre.Details, []string{"Matches: Bash", "Only if: Bash(git push *)", "Timeout: 5s"}) {
		t.Errorf("PreToolUse: %+v", pre)
	}
	if stop.Event != "Stop" || stop.Command != "echo done" || !slices.Equal(stop.Details, []string{"Runs in the background"}) {
		t.Errorf("Stop: %+v", stop)
	}
	for _, h := range got {
		if !strings.Contains(h.Source, "gnosh.json:") {
			t.Errorf("%s: no file named (%q)", h.Event, h.Source)
		}
	}
}
