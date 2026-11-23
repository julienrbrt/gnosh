package memjoin

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/gnosh-ai/gnosh/internal/app/apptest"
)

// mem written as Gno and sent to the shell runs in mem, with gnosh's word
// that it did and what to call next time; and mem's code sent to gnovm_run
// with no VM named runs in mem, not in the scratch VM, where mem is no
// name. Refused, the first taught a small model nothing: in 19 of 45
// FrontierHarness trials on box 2 Qwen's first mem call went to the
// shell, and in one 315 of its 320 calls did (2026-11-19).
func TestMemCodeSentElsewhereRunsInMem(t *testing.T) {
	nameless, _ := json.Marshal(map[string]string{"code": `mem.Fact("port", "the server listens on 8080")`})
	w := newWorld(t,
		reply(call("c1", "Bash", map[string]any{"command": "mem.Fact(\"ship\", \"ship it on Friday\")\nmem.Show()"})),
		reply(apptest.Call{ID: "c2", Name: "gnovm_run", Args: string(nameless)}),
		reply(call("c3", "Bash", map[string]any{"command": "mem.Show() && rm -rf build"})),
		says("Done."),
	)
	r := w.run("-y", "-p", "plan the release")
	r.wantCode(t, 0)
	reqs := w.Model.Requests()
	if len(reqs) != 4 {
		t.Fatalf("the model was asked %d times, want 4", len(reqs))
	}
	first := lastResult(t, reqs[1])
	has(t, "the shell command that was mem's", first,
		"That was Gno for mem, which no shell runs, so gnosh ran it with gnovm_run", "ship")
	hasNot(t, "the shell command that was mem's", first, "is no program")
	second := lastResult(t, reqs[2])
	hasNot(t, "mem's code with no VM named", second, "not declared")
	has(t, "mem's code with no VM named", second, "port")
	// A line with more than calls on mem is no Gno: it is the shell's,
	// which says why it cannot run, and nothing of it ran in mem.
	third := lastResult(t, reqs[3])
	hasNot(t, "a line with more than mem", third, "That was Gno for mem")
	if strings.Contains(third, "ran it with gnovm_run") {
		t.Errorf("a line with more than calls on mem was run in mem:\n%s", third)
	}
}
