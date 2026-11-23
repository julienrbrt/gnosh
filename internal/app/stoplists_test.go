package app

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gnosh-ai/gnosh/internal/app/apptest"
)

// A Stop hook is told what still runs in the background and what the
// session has scheduled, as Claude Code's Stop input has them
// (background_tasks, session_crons): a command started in the background
// while it runs, and both lists empty, not left out, once nothing is.
func TestStopHookIsToldWhatRuns(t *testing.T) {
	w := apptest.NewWorld(t,
		apptest.Reply{Calls: []apptest.Call{{ID: "c1", Name: "Bash", Args: `{"command":"sleep 2","description":"wait a while","run_in_background":true}`}}},
		apptest.Reply{Text: "Started."},
		apptest.Reply{Text: "It ended."})
	w.Hook("Stop", "", w.Script(w.Home+"/hooks/stop.sh", `cat >> "$HOME/stop.jsonl"; echo >> "$HOME/stop.jsonl"`))
	run(t, w, "-y", "-p", "start it").wantCode(t, 0)
	lines := jsonLines(t, w.Read(w.Home+"/stop.jsonl"))
	if len(lines) != 2 {
		t.Fatalf("the Stop hook ran %d times, want 2:\n%s", len(lines), w.Read(w.Home+"/stop.jsonl"))
	}
	tasks, _ := lines[0]["background_tasks"].([]any)
	crons, ok := lines[0]["session_crons"].([]any)
	if len(tasks) != 1 || !ok || len(crons) != 0 {
		t.Fatalf("the first Stop input: background_tasks %v, session_crons %v", lines[0]["background_tasks"], lines[0]["session_crons"])
	}
	task := tasks[0].(map[string]any)
	for k, want := range map[string]string{"type": "shell", "status": "running", "description": "wait a while", "command": "sleep 2"} {
		if task[k] != want {
			t.Errorf("the task's %s is %v, want %q", k, task[k], want)
		}
	}
	if id, _ := task["id"].(string); id == "" {
		t.Errorf("the task has no id: %v", task)
	}
	if _, has := task["agent_type"]; has {
		t.Errorf("a shell task has an agent_type: %v", task)
	}
	tasks, ok = lines[1]["background_tasks"].([]any)
	crons, ok2 := lines[1]["session_crons"].([]any)
	if !ok || !ok2 || len(tasks) != 0 || len(crons) != 0 {
		t.Errorf("the last Stop input: background_tasks %v, session_crons %v", lines[1]["background_tasks"], lines[1]["session_crons"])
	}
	w.Model.Done()
}

// What the session has scheduled is in the list: its schedule, whether
// it comes again, and its prompt.
func TestStopHookIsToldWhatIsScheduled(t *testing.T) {
	w := apptest.NewWorld(t,
		apptest.Reply{Calls: []apptest.Call{{ID: "c1", Name: "CronCreate", Args: `{"cron":"*/5 * * * *","prompt":"check the build"}`}}},
		apptest.Reply{Text: "Scheduled."})
	w.Hook("Stop", "", w.Script(w.Home+"/hooks/stop.sh", `cat >> "$HOME/stop.jsonl"; echo >> "$HOME/stop.jsonl"`))
	clk := &clock{now: time.Date(2026, 11, 18, 10, 0, 30, 0, time.Local)}
	s := sitWith(t, w, func(o *Options) { o.Now = clk.Now })
	s.see("Type a prompt and Enter.")
	s.typeLine("check the build every five minutes")
	s.see("Scheduled.")
	// (The reply is on the screen before the turn's end runs the hook.)
	waitFor(t, "the Stop hook", func() bool {
		data, err := os.ReadFile(w.Home + "/stop.jsonl")
		return err == nil && strings.HasSuffix(string(data), "\n")
	})
	s.typeLine("/exit")
	s.exit()
	lines := jsonLines(t, w.Read(w.Home+"/stop.jsonl"))
	if len(lines) != 1 {
		t.Fatalf("the Stop hook ran %d times, want 1", len(lines))
	}
	crons, _ := lines[0]["session_crons"].([]any)
	if len(crons) != 1 {
		t.Fatalf("session_crons: %v", lines[0]["session_crons"])
	}
	c := crons[0].(map[string]any)
	if c["schedule"] != "*/5 * * * *" || c["recurring"] != true || c["prompt"] != "check the build" || c["id"] == "" {
		t.Errorf("the scheduled prompt: %v", c)
	}
}

// A long description, command or prompt is cut, with how much more there
// was said.
func TestStopListsCutLongText(t *testing.T) {
	long := strings.Repeat("é", stopListMost+7)
	if got := clipped(long); got != strings.Repeat("é", stopListMost)+"… [+7 chars]" {
		t.Errorf("clipped = …%q", got[len(got)-20:])
	}
	if got := clipped("short"); got != "short" {
		t.Errorf("clipped(short) = %q", got)
	}
}

// A folder whose instruction files bring many warnings shows the first
// ten and the count of the rest, not all of them (the instruction-files
// review of 2026-11-20: a file of half a million imports flooded it).
func TestInstructionWarningsAreCounted(t *testing.T) {
	w := apptest.NewWorld(t, apptest.Reply{Text: "Hi."})
	var b strings.Builder
	for i := range 15 {
		fmt.Fprintf(&b, "@missing/%d.md\n", i)
	}
	w.Write("AGENTS.md", b.String())
	r := run(t, w, "-p", "hi").wantCode(t, 0)
	if n := strings.Count(r.err, " is not there."); n != warningsShown {
		t.Errorf("%d import warnings shown, want %d:\n%s", n, warningsShown, r.err)
	}
	has(t, "stderr", r.err, "…and 5 more warnings about instruction files")
}

// After a summary the instruction files are read again when one has
// changed, as Claude Code reads them after /compact: the model is given
// the file as it is now, not the copy read at the start.
func TestSummaryReadsChangedInstructionsAgain(t *testing.T) {
	w := apptest.NewWorld(t, apptest.Reply{Text: "First."}, apptest.Reply{Text: "SUMMARY: little."}, apptest.Reply{Text: "Second."})
	w.Write("AGENTS.md", "Sign each answer with a rose.\n")
	s := sit(t, w)
	s.see("Type a prompt and Enter.")
	s.typeLine("hello")
	s.see("First.")
	w.Write("AGENTS.md", "Sign each answer with a yellow tulip.\n")
	s.typeLine("/compact")
	s.see("The conversation was summarized")
	s.typeLine("go on")
	s.see("Second.")
	s.typeLine("/exit")
	s.exit()
	reqs := w.Model.Requests()
	has(t, "the first request", string(reqs[0].Body), "with a rose")
	last := string(reqs[len(reqs)-1].Body)
	has(t, "the request after the summary", last, "with a yellow tulip")
	hasNot(t, "the request after the summary", last, "with a rose")
}
