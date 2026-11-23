package app

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/gnosh-ai/gnosh/internal/app/apptest"
)

// /fewer-permission-prompts lists the allow rules that would have let the
// folder's past calls run without a question, the most used first, and
// keeps the ones picked by number in the project's local settings, as
// /permissions allow does; what is allowed then drops off the list, and a
// read-only command, never asked about, is never on it. A call that did
// not run (refused, here with nobody to ask) counts for nothing, and the
// numbers are those of a list shown in the same session (DECISIONS 376).
func TestFewerPermissionPrompts(t *testing.T) {
	w := apptest.NewWorld(t,
		apptest.Reply{Calls: []apptest.Call{
			{ID: "c1", Name: "Bash", Args: `{"command":"kubectl get pods"}`},
			{ID: "c2", Name: "Bash", Args: `{"command":"kubectl get nodes"}`},
			{ID: "c3", Name: "Bash", Args: `{"command":"ls"}`},
		}},
		apptest.Reply{Calls: []apptest.Call{{ID: "c4", Name: "Bash", Args: `{"command":"terraform plan"}`}}},
		apptest.Reply{Text: "Done."},
		// Refused, as nobody is there to ask.
		apptest.Reply{Calls: []apptest.Call{{ID: "c5", Name: "Bash", Args: `{"command":"docker run --rm busybox"}`}}},
		apptest.Reply{Text: "It was refused."},
		// A sub-agent's call counts too: its session file is kept apart.
		apptest.Reply{Calls: []apptest.Call{{ID: "c6", Name: "Agent", Args: `{"subagent_type":"general-purpose","description":"list releases","prompt":"List the releases."}`}}},
		apptest.Reply{Calls: []apptest.Call{{ID: "s1", Name: "Bash", Args: `{"command":"helm list"}`}}},
		apptest.Reply{Text: "No releases."},
		apptest.Reply{Text: "Delegated."})
	run(t, w, "-y", "-p", "look around").wantCode(t, 0)
	run(t, w, "-p", "run a container").wantCode(t, 0)
	run(t, w, "-y", "-p", "delegate it").wantCode(t, 0)
	few := func(args string) result {
		t.Helper()
		return run(t, w, "--trust-folder", "-p", strings.TrimSpace("/fewer-permission-prompts "+args))
	}
	list := few("").wantCode(t, 0).out
	has(t, "the list", list, "would be asked about now in manual mode", " 1. Bash(kubectl get *) · 2 calls, as kubectl get pods", "Bash(terraform plan) · 1 call", "Bash(helm list) · 1 call", "/fewer-permission-prompts add 1 3")
	hasNot(t, "the list", list, "Bash(ls", "docker")
	// A number with no list shown in the run: not worked out again behind
	// the person's back.
	r := few("add 1")
	has(t, "add with no list", r.out+r.err, "has given none in this session yet")
	local := filepath.Join(w.Dir, ".gnosh", "settings.local.json")
	if w.Exists(local) {
		t.Errorf("a rule was kept with no list shown:\n%s", w.Read(local))
	}
	// In a session: the list, then add.
	s := sitWith(t, w, nil, "--trust-folder")
	s.see("Type a prompt and Enter.")
	s.typeLine("/fewer-permission-prompts")
	s.see(" 1. Bash(kubectl get *)")
	s.typeLine("/fewer-permission-prompts add 1")
	s.see("is in force, and kept in " + local)
	s.typeLine("/fewer-permission-prompts add 9")
	s.see("is not one of the list's numbers")
	s.typeLine("/exit")
	s.exit()
	has(t, "the local file", w.Read(local), `"allow"`, "kubectl get")
	after := few("").wantCode(t, 0).out
	has(t, "the list after", after, "Bash(terraform plan)", "Bash(helm list)")
	hasNot(t, "the list after", after, "kubectl")
	w.Model.Done()
}
