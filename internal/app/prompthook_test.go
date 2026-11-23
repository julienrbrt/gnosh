package app

import (
	"os"
	"strings"
	"testing"

	"github.com/gnosh-ai/gnosh/internal/app/apptest"
)

// A Stop hook of type "prompt" asks the model whether the turn may end:
// its "ok": false keeps the turn going with the reason as the next
// instruction, and once it says "ok": true the turn ends. The question
// goes with the judge's own instructions, and the event put in.
func TestPromptHookOnStop(t *testing.T) {
	w := apptest.NewWorld(t,
		apptest.Reply{Text: "Done."},
		apptest.Reply{Text: `{"ok": false, "reason": "Run the tests before you stop."}`},
		apptest.Reply{Text: "Ran the tests: they pass."},
		apptest.Reply{Text: `{"ok": true}`},
	)
	w.Settings(`{"hooks": {"Stop": [{"hooks": [{"type": "prompt", "prompt": "Did the agent run the tests? $ARGUMENTS"}]}]}}`)
	r := run(t, w, "-p", "fix it")
	r.wantCode(t, 0)
	if !strings.Contains(r.out, "Ran the tests: they pass.") {
		t.Errorf("the turn did not go on after the hook's ok: false:\n%s%s", r.out, r.err)
	}
	reqs := w.Model.Requests()
	if len(reqs) != 4 {
		t.Fatalf("the model was asked %d times, want 4 (two turns' replies, two judgements)", len(reqs))
	}
	judge := string(reqs[1].Body)
	has(t, "the judgement", judge, "You judge one hook for gnosh", "Did the agent run the tests?", `\"hook_event_name\":\"Stop\"`)
	has(t, "the request after ok: false", string(reqs[2].Body), "Run the tests before you stop.")
}

// A Stop hook of type "agent" hands its task to a sub-agent of its own,
// which may look first (here it reads a file) and answers as a prompt
// hook's model does: its "ok": false keeps the turn going with the reason,
// and its "ok": true lets the turn end. Inside that sub-agent no agent
// hook runs and no SubagentStop hook, while a PreToolUse command hook
// still guards its calls.
func TestAgentHookOnStop(t *testing.T) {
	w := apptest.NewWorld(t)
	notes := w.Path("TESTS.md")
	w.Write("TESTS.md", "the tests were not run\n")
	w.Model.Add(
		apptest.Reply{Text: "Done."},
		apptest.Reply{Calls: []apptest.Call{{ID: "h1", Name: "Read", Args: `{"file_path": "` + notes + `"}`}}},
		apptest.Reply{Text: `I read TESTS.md. {"ok": false, "reason": "TESTS.md says the tests were not run."}`},
		apptest.Reply{Text: "Ran the tests: they pass."},
		apptest.Reply{Text: `{"ok": true}`},
	)
	log := w.Home + "/hook-log"
	guard := w.Script(w.Home+"/hooks/guard.sh", "cat > /dev/null\necho read >> "+log)
	stopped := w.Script(w.Home+"/hooks/stopped.sh", "cat > /dev/null\necho subagent-stop >> "+log)
	w.Settings(`{"hooks": {
		"Stop": [{"hooks": [{"type": "agent", "prompt": "Check TESTS.md says the tests ran. $ARGUMENTS"}]}],
		"PreToolUse": [{"matcher": "Read", "hooks": [{"type": "command", "command": "` + guard + `"}, {"type": "agent", "prompt": "Is this read safe? $ARGUMENTS"}]}],
		"SubagentStop": [{"hooks": [{"type": "command", "command": "` + stopped + `"}]}]
	}}`)
	r := run(t, w, "-p", "fix it")
	r.wantCode(t, 0)
	if !strings.Contains(r.out, "Ran the tests: they pass.") {
		t.Errorf("the turn did not go on after the agent hook's ok: false:\n%s%s", r.out, r.err)
	}
	reqs := w.Model.Requests()
	if len(reqs) != 5 {
		t.Fatalf("the model was asked %d times, want 5 (two turns' replies, the hook's sub-agent three times)", len(reqs))
	}
	has(t, "the hook's sub-agent's task", string(reqs[1].Body), "You check one hook for gnosh", "Check TESTS.md says the tests ran.", `\"hook_event_name\":\"Stop\"`)
	has(t, "what the sub-agent read", string(reqs[2].Body), "the tests were not run")
	has(t, "the request after ok: false", string(reqs[3].Body), "TESTS.md says the tests were not run.")
	data, _ := os.ReadFile(log)
	if got := strings.TrimSpace(string(data)); got != "read" {
		t.Errorf("the hooks inside the agent hook's sub-agent: %q, want only the PreToolUse command hook", got)
	}
}

// In the plain session a Stop hook's reason is said once: in the hook's
// record, and the note that the turn goes on points at it.
func TestStopHookReasonSaidOnce(t *testing.T) {
	w := apptest.NewWorld(t, apptest.Reply{Text: "Done."}, apptest.Reply{Text: "Wrote it."})
	once := w.Home + "/hook-ran"
	hook := w.Script(w.Home+"/hooks/stop.sh", "cat > /dev/null\n[ -e "+once+" ] && exit 0\ntouch "+once+"\necho 'write done.txt first' >&2\nexit 2")
	w.Settings(`{"hooks": {"Stop": [{"hooks": [{"type": "command", "command": "` + hook + `"}]}]}}`)
	s := sit(t, w)
	s.see("Type a prompt and Enter.")
	s.typeLine("finish up")
	s.see("blocked")
	s.see("write done.txt first")
	s.see("The turn goes on: stop-hook objects, as the hook above says.")
	s.see("Wrote it.")
	s.typeLine("/exit")
	if code := s.exit(); code != 0 {
		t.Errorf("exit code %d", code)
	}
}
