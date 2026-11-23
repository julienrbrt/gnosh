package app

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gnosh-ai/gnosh/internal/app/apptest"
)

// A PreToolUse hook that blocks a call: the call does not run, the model
// reads the hook's reason, and the person is told on standard error.
func TestHookBlocksTool(t *testing.T) {
	w := apptest.NewWorld(t,
		apptest.Reply{Calls: []apptest.Call{{ID: "c1", Name: "Bash", Args: `{"command":"touch made.txt"}`}}},
		apptest.Reply{Text: "The hook refused it."})
	hook := w.Script(w.Home+"/hooks/no-shell.sh", `echo "no shell today: policy 7" >&2; exit 2`)
	w.Hook("PreToolUse", "Bash", hook)
	r := run(t, w, "-y", "-p", "make the file").wantCode(t, 0)
	if w.Exists("made.txt") {
		t.Errorf("the command ran although the hook blocked it")
	}
	has(t, "the request after the block", requestText(t, w, 1), "no shell today: policy 7")
	has(t, "stderr", r.err, "PreToolUse", "no shell today: policy 7")
	w.Model.Done()
}

// A UserPromptSubmit hook's words go to the model with the prompt, marked
// as gnosh's and not the person's.
func TestHookAddsContext(t *testing.T) {
	w := apptest.NewWorld(t, apptest.Reply{Text: "Noted."})
	hook := w.Script(w.Home+"/hooks/context.sh", `echo "the build is green as of 14:02"`)
	w.Hook("UserPromptSubmit", "", hook)
	run(t, w, "-p", "how is the build?").wantCode(t, 0)
	msgs := w.Model.Requests()[0].Messages()
	last := apptest.Text(msgs[len(msgs)-1])
	has(t, "the prompt's message", last, "the build is green as of 14:02", "how is the build?")
	if i, j := strings.Index(last, "the build is green"), strings.LastIndex(last[:strings.Index(last, "the build is green")+1], "<gnosh>"); i < 0 || j < 0 {
		t.Errorf("the hook's words are not inside gnosh's own marks: %s", clip(last))
	}
}

// A PostToolUse hook's updatedToolOutput is what the model reads in place
// of what the tool returned: a redaction keeps the secret out of the
// conversation. A replacement gnosh cannot read as text withholds the
// output rather than show it (where Claude Code shows the original).
func TestHookReplacesToolOutput(t *testing.T) {
	w := apptest.NewWorld(t,
		apptest.Reply{Calls: []apptest.Call{{ID: "c1", Name: "Bash", Args: `{"command":"printf 'token=sk-%s\\n' SECRET-123"}`}}},
		apptest.Reply{Text: "Done."},
		apptest.Reply{Calls: []apptest.Call{{ID: "c2", Name: "Bash", Args: `{"command":"printf 'token=sk-%s\\n' SECRET-456"}`}}},
		apptest.Reply{Text: "Done again."})
	redact := w.Script(w.Home+"/hooks/redact.sh", `cat > /dev/null; echo '{"hookSpecificOutput": {"hookEventName": "PostToolUse", "updatedToolOutput": {"stdout": "token=[redacted]", "stderr": "", "interrupted": false, "isImage": false}}}'`)
	w.Hook("PostToolUse", "Bash", redact)
	run(t, w, "-y", "-p", "show the token").wantCode(t, 0)
	got := toolResult(t, w, 1, "c1")
	has(t, "what the model read", got, "token=[redacted]")
	hasNot(t, "what the model read", got, "sk-SECRET-123")
	for _, req := range w.Model.Requests() {
		hasNot(t, "every request", string(req.Body), "sk-SECRET-123")
	}

	odd := w.Script(w.Home+"/hooks/odd.sh", `cat > /dev/null; echo '{"hookSpecificOutput": {"hookEventName": "PostToolUse", "updatedToolOutput": {"lines": 3}}}'`)
	w.Hook("PostToolUse", "Bash", odd)
	run(t, w, "-y", "-c", "-p", "show it again").wantCode(t, 0)
	got = toolResult(t, w, 3, "c2")
	has(t, "what the model read", got, "withheld")
	hasNot(t, "what the model read", got, "sk-SECRET-456")
}

// A hook that cannot run does not pass: the call it guards is not run.
func TestHookThatCannotRunFailsClosed(t *testing.T) {
	w := apptest.NewWorld(t,
		apptest.Reply{Calls: []apptest.Call{{ID: "c1", Name: "Bash", Args: `{"command":"touch made.txt"}`}}},
		apptest.Reply{Text: "It was not run."})
	w.Hook("PreToolUse", "Bash", w.Home+"/hooks/is-not-there.sh")
	r := run(t, w, "-y", "-p", "make the file").wantCode(t, 0)
	if w.Exists("made.txt") {
		t.Errorf("the command ran although its check could not")
	}
	has(t, "stderr", r.err, "is-not-there.sh")
	if n := len(w.Model.Requests()); n != 2 {
		t.Fatalf("the model was sent %d requests, want 2\nstderr:\n%s", n, r.err)
	}
	has(t, "the request after the check failed", requestText(t, w, 1), "did not run")
}

// A Stop hook sends the model back to work twice, and then lets the turn
// end. Each time the model reads the hook's reason.
func TestStopHookSendsBackTwice(t *testing.T) {
	w := apptest.NewWorld(t,
		apptest.Reply{Text: "Done, I think."},
		apptest.Reply{Text: "Ran the tests."},
		apptest.Reply{Text: "Now it is done."})
	hook := w.Script(w.Home+"/hooks/stop.sh", `
n=$(cat "$HOME/stop-count" 2>/dev/null || echo 0)
n=$((n+1))
echo $n > "$HOME/stop-count"
if [ $n -le 2 ]; then
  echo "the tests have not been run (objection $n)" >&2
  exit 2
fi
exit 0`)
	w.Hook("Stop", "", hook)
	r := run(t, w, "-p", "finish the work").wantCode(t, 0)
	if r.out != "Done, I think.\n\nRan the tests.\n\nNow it is done.\n" {
		t.Errorf("stdout = %q", r.out)
	}
	has(t, "the second request", requestText(t, w, 1), "the tests have not been run (objection 1)")
	has(t, "the third request", requestText(t, w, 2), "the tests have not been run (objection 2)")
	has(t, "stderr", r.err, "objection 1", "objection 2")
	if got := strings.TrimSpace(w.Read(w.Home + "/stop-count")); got != "3" {
		t.Errorf("the Stop hook ran %s times, want 3", got)
	}
	w.Model.Done()
}

// Every moment a hook can be put on fires, in order, with gnosh's
// variables set: the session's start before the first prompt, the tool
// events around the call, the turn's end, and the session's end last.
func TestHookEventsInOrder(t *testing.T) {
	w := apptest.NewWorld(t,
		apptest.Reply{Calls: []apptest.Call{{ID: "c1", Name: "Bash", Args: `{"command":"true"}`}}},
		apptest.Reply{Text: "Done."})
	hook := w.Script(w.Home+"/hooks/log.sh", `
cat > /dev/null
echo "$GNOSH_HOOK_EVENT_NAME session=$GNOSH_SESSION_ID project=$GNOSH_PROJECT_DIR claude=$CLAUDE_PROJECT_DIR agent=$AGENT tool=$GNOSH_TOOL_NAME source=$GNOSH_SESSION_START_SOURCE reason=$GNOSH_SESSION_END_REASON" >> "$HOME/events.log"`)
	for _, e := range []string{"SessionStart", "UserPromptSubmit", "PreToolUse", "PostToolUse", "Stop", "SessionEnd"} {
		w.Hook(e, "", hook)
	}
	r := run(t, w, "-y", "-p", "--output-format", "json", "do it").wantCode(t, 0)
	id := jsonLines(t, r.out)[0]["session_id"].(string)
	data, err := os.ReadFile(w.Home + "/events.log")
	if err != nil {
		t.Fatalf("no hook ran: %v\nstderr:\n%s", err, r.err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	var events []string
	for _, l := range lines {
		event, _, _ := strings.Cut(l, " ")
		events = append(events, event)
		has(t, "a hook's variables", l, "session="+id, "project="+w.Dir, "claude="+w.Dir, "agent=gnosh")
	}
	if got, want := strings.Join(events, " "), "SessionStart UserPromptSubmit PreToolUse PostToolUse Stop SessionEnd"; got != want {
		t.Errorf("the hooks ran in the order %q, want %q", got, want)
	}
	has(t, "the SessionStart line", lines[0], "source=startup")
	has(t, "the PreToolUse line", lines[2], "tool=Bash")
	has(t, "the SessionEnd line", lines[len(lines)-1], "reason=other")
}

// --max-turns ends a run after that many replies of the model: no further
// request is sent, the result says why, and the run exits 1.
func TestMaxTurns(t *testing.T) {
	w := apptest.NewWorld(t,
		apptest.Reply{Calls: []apptest.Call{{ID: "c1", Name: "Bash", Args: `{"command":"true"}`}}},
		apptest.Reply{Calls: []apptest.Call{{ID: "c2", Name: "Bash", Args: `{"command":"true"}`}}},
		apptest.Reply{Calls: []apptest.Call{{ID: "c3", Name: "Bash", Args: `{"command":"true"}`}}},
		apptest.Reply{Text: "never reached"})
	r := run(t, w, "-y", "-p", "--max-turns", "2", "--output-format", "json", "loop").wantCode(t, 1)
	if n := len(w.Model.Requests()); n != 2 {
		t.Errorf("the model was sent %d requests, want 2", n)
	}
	obj := jsonLines(t, r.out)[0]
	if obj["subtype"] != "error_max_turns" || obj["is_error"] != true || obj["num_turns"] != float64(2) {
		t.Errorf("result: %s", r.out)
	}
	has(t, "stderr", r.err, "--max-turns")
	checkSession(t, w, w.Sessions()[0])
}

// eventLog puts a hook on each of the events that appends the event's
// name, and what gnosh says of it in its variables, to a file.
func eventLog(w *apptest.World, events ...string) {
	hook := w.Script(w.Home+"/hooks/log.sh", `
cat > /dev/null
echo "$GNOSH_HOOK_EVENT_NAME $GNOSH_TOOL_NAME$GNOSH_STOP_FAILURE_ERROR$GNOSH_PRE_COMPACT_TRIGGER$GNOSH_POST_COMPACT_TRIGGER$GNOSH_INSTRUCTIONS_LOAD_REASON$GNOSH_NOTIFICATION_TYPE$GNOSH_SESSION_START_SOURCE" >> "$HOME/events.log"`)
	for _, e := range events {
		w.Hook(e, "", hook)
	}
}

// loggedEvents returns the lines the hooks of eventLog wrote.
func loggedEvents(t *testing.T, w *apptest.World) []string {
	t.Helper()
	data, err := os.ReadFile(w.Home + "/events.log")
	if err != nil {
		return nil
	}
	return strings.Split(strings.TrimSpace(string(data)), "\n")
}

// The moments that are not part of every turn fire too: a tool that
// failed, a turn that failed, the two sides of a summary, an instruction
// file being loaded, and the two notifications of a session that waits
// for its person.
func TestTheRarerHookEventsFire(t *testing.T) {
	t.Run("a failed tool call and a loaded instruction file", func(t *testing.T) {
		w := apptest.NewWorld(t,
			apptest.Reply{Calls: []apptest.Call{{ID: "c1", Name: "Read", Args: `{"file_path":"is-not-there.txt"}`}}},
			apptest.Reply{Text: "It is not there."})
		w.Write("AGENTS.md", "A rule.\n")
		eventLog(w, "PostToolUseFailure", "PostToolUse", "InstructionsLoaded")
		run(t, w, "-p", "read it").wantCode(t, 0)
		got := strings.Join(loggedEvents(t, w), "\n")
		has(t, "the hooks that ran", got, "PostToolUseFailure Read", "InstructionsLoaded session_start")
		hasNot(t, "the hooks that ran", got, "PostToolUse Read")
	})
	t.Run("a failed turn", func(t *testing.T) {
		w := apptest.NewWorld(t, apptest.Reply{Status: 401, Body: `{"error":{"message":"Invalid API key"}}`})
		eventLog(w, "StopFailure", "Stop")
		run(t, w, "-p", "hello").wantCode(t, 1)
		if got := strings.Join(loggedEvents(t, w), "\n"); got != "StopFailure authentication_failed" {
			t.Errorf("the hooks that ran: %q", got)
		}
	})
	t.Run("a summary", func(t *testing.T) {
		w := apptest.NewWorld(t, apptest.Reply{Text: "The first answer."}, apptest.Reply{Text: "SUMMARY: nothing much."})
		run(t, w, "-p", "remember this").wantCode(t, 0)
		eventLog(w, "PreCompact", "PostCompact", "SessionStart")
		run(t, w, "-c", "-p", "/compact").wantCode(t, 0)
		if got := strings.Join(loggedEvents(t, w), "\n"); got != "PreCompact manual\nPostCompact manual\nSessionStart compact" {
			t.Errorf("the hooks that ran: %q", got)
		}
	})
	t.Run("instruction files given again after a summary", func(t *testing.T) {
		w := apptest.NewWorld(t, apptest.Reply{Text: "The first answer."}, apptest.Reply{Text: "SUMMARY: nothing much."})
		w.Write("AGENTS.md", "A rule.\n")
		eventLog(w, "InstructionsLoaded")
		s := sit(t, w)
		s.see("Type a prompt and Enter.")
		s.typeLine("remember this")
		s.see("The first answer.")
		s.typeLine("/compact")
		s.see("The conversation was summarized")
		// (The hooks are not waited for: a session that ends at once may
		// end before one has run.)
		waitFor(t, "InstructionsLoaded after the summary", func() bool {
			return strings.Contains(strings.Join(loggedEvents(t, w), "\n"), "InstructionsLoaded compact")
		})
		has(t, "the hooks that ran", strings.Join(loggedEvents(t, w), "\n"), "InstructionsLoaded session_start")
		s.typeLine("/exit")
		s.exit()
	})
	t.Run("a session that waits for its person", func(t *testing.T) {
		defer func(a, i time.Duration) { askNotifyAfter, idleNotifyAfter = a, i }(askNotifyAfter, idleNotifyAfter)
		askNotifyAfter, idleNotifyAfter = 50*time.Millisecond, 50*time.Millisecond
		w := apptest.NewWorld(t,
			apptest.Reply{Calls: []apptest.Call{{ID: "c1", Name: "Bash", Args: `{"command":"touch made.txt"}`}}},
			apptest.Reply{Text: "Made."})
		eventLog(w, "Notification")
		s := sit(t, w)
		s.see("Type a prompt and Enter.")
		s.typeLine("make it")
		s.see("? Allow Bash?")
		waitFor(t, "the notification that a question waits", func() bool {
			return strings.Contains(strings.Join(loggedEvents(t, w), "\n"), "Notification permission_prompt")
		})
		s.typeLine("1")
		s.see("Made.")
		waitFor(t, "the notification that the session is idle", func() bool {
			return strings.Contains(strings.Join(loggedEvents(t, w), "\n"), "Notification idle_prompt")
		})
		s.typeLine("/exit")
		s.exit()
	})
}
