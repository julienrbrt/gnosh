package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gnosh-ai/gnosh/internal/app/apptest"
)

// systemText returns the system prompt of a request.
func systemText(t *testing.T, req apptest.Request) string {
	t.Helper()
	msgs := req.Messages()
	if len(msgs) == 0 || msgs[0]["role"] != "system" {
		t.Fatalf("the request has no system message")
	}
	return apptest.Text(msgs[0])
}

// The flags that replace gnosh's instructions or add to them, typed or
// from a file.
func TestSystemPromptFlags(t *testing.T) {
	const own = "You are the coding agent inside gnosh"
	one := func(t *testing.T, args ...string) string {
		w := apptest.NewWorld(t, apptest.Reply{Text: "ok"})
		w.Write("replace.txt", "You are a reviewer of poems.\n")
		w.Write("more.txt", "Answer in one line.\n")
		run(t, w, append(args, "-p", "hello")...).wantCode(t, 0)
		return systemText(t, w.Model.Requests()[0])
	}
	if got := one(t, "--system-prompt", "You only count."); !strings.Contains(got, "You only count.") || strings.Contains(got, own) {
		t.Errorf("--system-prompt: %s", clip(got))
	}
	if got := one(t, "--append-system-prompt", "Always answer in French."); !strings.Contains(got, own) || !strings.HasSuffix(got, "Always answer in French.") {
		t.Errorf("--append-system-prompt: %s", clip(got))
	}
	if got := one(t, "--system-prompt-file", "replace.txt"); !strings.Contains(got, "You are a reviewer of poems.") || strings.Contains(got, own) {
		t.Errorf("--system-prompt-file: %s", clip(got))
	}
	if got := one(t, "--append-system-prompt-file", "more.txt", "--append-system-prompt", "And be kind."); !strings.HasSuffix(got, "Answer in one line.\n\nAnd be kind.") {
		t.Errorf("--append-system-prompt-file with --append-system-prompt: ...%s", got[max(0, len(got)-200):])
	}
	w := apptest.NewWorld(t)
	r := run(t, w, "--system-prompt-file", "is-not-there.txt", "-p", "hello").wantCode(t, 1)
	has(t, "stderr", r.err, "--system-prompt-file", "is-not-there.txt", "could not be read")
}

// Skipping permissions can be switched to only in a session that was
// started with it, or with it put within reach: --allow-dangerously-skip-
// permissions starts the session in the mode it would have had and lets an
// interface (Shift+Tab) go on to skipping later.
func TestSkippingWithinReach(t *testing.T) {
	for _, tc := range []struct {
		args  []string
		start string
		reach bool
	}{
		{nil, "default", false},
		{[]string{"--permission-mode", "plan"}, "plan", false},
		{[]string{"--allow-dangerously-skip-permissions"}, "default", true},
		{[]string{"--allow-dangerously-skip-permissions", "--permission-mode", "acceptEdits"}, "acceptEdits", true},
		{[]string{"-y"}, "bypassPermissions", true},
	} {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			w := apptest.NewWorld(t)
			var start, after string
			var err error
			_, r := attach(t, w, nil, func(ctx context.Context, c SessionControls, f *scripted) int {
				start = c.Mode()
				err = c.SetMode("bypassPermissions")
				after = c.Mode()
				return 0
			}, tc.args...)
			r.wantCode(t, 0)
			if start != tc.start {
				t.Errorf("the session started in %s, want %s", start, tc.start)
			}
			switch {
			case tc.reach && (err != nil || after != "bypassPermissions"):
				t.Errorf("skipping was within reach, and the switch to it came to %v, in the mode %s", err, after)
			case !tc.reach && (err == nil || after != tc.start):
				t.Errorf("skipping was not within reach, and the switch to it came to %v, in the mode %s", err, after)
			case !tc.reach && !strings.Contains(err.Error(), "--allow-dangerously-skip-permissions"):
				t.Errorf("the refusal does not name the flag that puts it within reach: %v", err)
			}
		})
	}
}

// In acceptEdits a cp inside the project runs without a question, onto a
// file that is there and into a folder alike. It asks where the copy would
// land on a link that leads out of the project: cp writes through such a
// link. This is the engine with the real file system behind it, where a
// path through a file is an error and not a place.
func TestCopyInAcceptEdits(t *testing.T) {
	w := apptest.NewWorld(t,
		apptest.Reply{Calls: []apptest.Call{{ID: "c1", Name: "Bash", Args: `{"command":"cp a.txt b.txt"}`}}},
		apptest.Reply{Calls: []apptest.Call{{ID: "c2", Name: "Bash", Args: `{"command":"cp a.txt dir"}`}}},
		apptest.Reply{Calls: []apptest.Call{{ID: "c3", Name: "Bash", Args: `{"command":"cp a.txt new.txt"}`}}},
		apptest.Reply{Calls: []apptest.Call{{ID: "c4", Name: "Bash", Args: `{"command":"cp a.txt trap"}`}}},
		apptest.Reply{Calls: []apptest.Call{{ID: "c5", Name: "Bash", Args: `{"command":"cp -R tree/. dir"}`}}},
		apptest.Reply{Calls: []apptest.Call{{ID: "c6", Name: "Bash", Args: `{"command":"cp -R tree/. deep"}`}}},
		apptest.Reply{Text: "Copied."})
	w.Write("a.txt", "the new text\n")
	w.Write("b.txt", "the old text\n")
	w.Write("dir/keep.txt", "kept\n")
	outside := filepath.Join(w.Home, "outside.txt")
	w.Write(outside, "not the project's\n")
	if err := os.MkdirAll(w.Path("trap"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, w.Path("trap/a.txt")); err != nil {
		t.Fatal(err)
	}
	// A folder copied with all that is in it, into a folder that holds
	// files and no link, and into one with a link two folders down that
	// leads out.
	w.Write("tree/t.txt", "from the tree\n")
	w.Write("tree/in/x", "x from the tree\n")
	w.Write("deep/in/keep.txt", "kept\n")
	other := filepath.Join(w.Home, "other.txt")
	w.Write(other, "not the project's either\n")
	if err := os.Symlink(other, w.Path("deep/in/x")); err != nil {
		t.Fatal(err)
	}
	run(t, w, "--permission-mode", "acceptEdits", "-p", "copy it about").wantCode(t, 0)
	if w.Exists("deep/t.txt") {
		t.Errorf("the copy into the folder with a link that leads out ran")
	}
	has(t, "what the model read of the copy into the folder with a link", toolResult(t, w, 6, "c6"), "nobody is here to give it", "leads out of the working directories")
	for name, want := range map[string]string{"b.txt": "the new text\n", "dir/a.txt": "the new text\n", "new.txt": "the new text\n", outside: "not the project's\n",
		"dir/t.txt": "from the tree\n", "dir/in/x": "x from the tree\n", "dir/keep.txt": "kept\n", other: "not the project's either\n"} {
		if !w.Exists(name) {
			t.Errorf("%s is not there", name)
		} else if got := w.Read(name); got != want {
			t.Errorf("%s holds %q, want %q", name, got, want)
		}
	}
	has(t, "what the model read of the copy onto the link", toolResult(t, w, 4, "c4"), "nobody is here to give it", "leads out of the working directories")
}

// The permission modes a run can be started in.
func TestPermissionModes(t *testing.T) {
	script := func() []apptest.Reply {
		return []apptest.Reply{
			{Calls: []apptest.Call{{ID: "c1", Name: "Read", Args: `{"file_path":"a.txt"}`}}},
			{Calls: []apptest.Call{
				{ID: "c2", Name: "Edit", Args: `{"file_path":"a.txt","old_string":"two plus","new_string":"three plus"}`},
				{ID: "c3", Name: "Bash", Args: `{"command":"curl -s http://203.0.113.7/x"}`},
			}},
			{Text: "Over."},
		}
	}
	for _, tc := range []struct {
		mode   string
		edited bool
		edit   string // what the model reads of the edit
		bash   string // and of the command
	}{
		{"default", false, "nobody is here to give it", "nobody is here to give it"},
		{"acceptEdits", true, "Edited", "nobody is here to give it"},
		{"dontAsk", false, "dontAsk", "dontAsk"},
		{"plan", false, "Plan mode", "Plan mode"},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			w := apptest.NewWorld(t, script()...)
			w.Write("a.txt", "two plus two\n")
			run(t, w, "--permission-mode", tc.mode, "-p", "change it").wantCode(t, 0)
			if got := w.Read("a.txt") == "three plus two\n"; got != tc.edited {
				t.Errorf("the file was edited: %v, want %v", got, tc.edited)
			}
			has(t, "what the model read of the edit", toolResult(t, w, 2, "c2"), tc.edit)
			has(t, "what the model read of the command", toolResult(t, w, 2, "c3"), tc.bash)
		})
	}
	t.Run("a mode that does not exist", func(t *testing.T) {
		w := apptest.NewWorld(t)
		r := run(t, w, "--permission-mode", "sometimes", "-p", "hello").wantCode(t, 1)
		has(t, "stderr", r.err, "sometimes")
	})
}

// --add-dir lets the tools read another folder without a question.
func TestAddDir(t *testing.T) {
	script := func() []apptest.Reply {
		return []apptest.Reply{
			{Calls: []apptest.Call{{ID: "c1", Name: "Read", Args: `{"file_path":"../other/notes.txt"}`}}},
			{Text: "Over."},
		}
	}
	w := apptest.NewWorld(t, script()...)
	w.Write(w.Root+"/other/notes.txt", "notes from the other folder\n")
	run(t, w, "-p", "read the notes").wantCode(t, 0)
	hasNot(t, "what the model read without --add-dir", toolResult(t, w, 1, "c1"), "notes from the other folder")

	w = apptest.NewWorld(t, script()...)
	w.Write(w.Root+"/other/notes.txt", "notes from the other folder\n")
	run(t, w, "--add-dir", "../other", "-p", "read the notes").wantCode(t, 0)
	has(t, "what the model read with --add-dir", toolResult(t, w, 1, "c1"), "notes from the other folder")
}

// Safe mode: no hook runs and no instruction file is read, and the person
// is told that it is on.
func TestSafeMode(t *testing.T) {
	prep := func(w *apptest.World) {
		hook := w.Script(w.Home+"/hooks/no-shell.sh", `echo "no shell today" >&2; exit 2`)
		w.Hook("PreToolUse", "Bash", hook)
		w.Write("AGENTS.md", "Always sign your answers with a rose.\n")
	}
	script := func() []apptest.Reply {
		return []apptest.Reply{
			{Calls: []apptest.Call{{ID: "c1", Name: "Bash", Args: `{"command":"echo it ran"}`}}},
			{Text: "Over."},
		}
	}
	w := apptest.NewWorld(t, script()...)
	prep(w)
	run(t, w, "-y", "-p", "run it").wantCode(t, 0)
	has(t, "the first request", requestText(t, w, 0), "sign your answers with a rose")
	has(t, "what the model read, with the hook in force", toolResult(t, w, 1, "c1"), "no shell today")

	w = apptest.NewWorld(t, script()...)
	prep(w)
	r := run(t, w, "--safe-mode", "-y", "-p", "run it").wantCode(t, 0)
	hasNot(t, "the first request in safe mode", requestText(t, w, 0), "sign your answers with a rose")
	has(t, "what the model read in safe mode", toolResult(t, w, 1, "c1"), "it ran")
	has(t, "stderr", r.err, "Safe mode is on")
}

// --tools chooses which tools the model is offered.
func TestToolsFlag(t *testing.T) {
	w := apptest.NewWorld(t, apptest.Reply{Text: "ok"}, apptest.Reply{Text: "ok"})
	run(t, w, "--tools", "Read,Grep", "-p", "hello").wantCode(t, 0)
	first := requestText(t, w, 0)
	has(t, "the request", first, `"name":"Read"`, `"name":"Grep"`)
	hasNot(t, "the request", first, `"name":"Bash"`, `"name":"Edit"`, `"name":"Write"`)
	run(t, w, "--tools", "", "-p", "hello").wantCode(t, 0)
	if _, ok := w.Model.Requests()[1].JSON["tools"]; ok {
		t.Errorf(`--tools "" still offers tools`)
	}
	r := run(t, w, "--tools", "Read,Fly", "-p", "hello").wantCode(t, 1)
	has(t, "stderr", r.err, "Fly")
}

// A session is named with -n and resumed by its name; --session-id gives
// a new session the id it is to have.
func TestNameAndSessionID(t *testing.T) {
	w := apptest.NewWorld(t, apptest.Reply{Text: "one"}, apptest.Reply{Text: "two"}, apptest.Reply{Text: "three"})
	const id = "0190a2b4-7c1d-7e3f-9a5b-0123456789ab"
	run(t, w, "-n", "Nightly Check", "--session-id", id, "-p", "first").wantCode(t, 0)
	if s := w.Sessions(); len(s) != 1 || filepath.Base(s[0]) != id+".jsonl" {
		t.Fatalf("session files: %v", s)
	}
	r := run(t, w, "-r", "nightly check", "-p", "--output-format", "json", "second").wantCode(t, 0)
	if got := jsonLines(t, r.out)[0]["session_id"]; got != id {
		t.Errorf("-r by name went on in %v", got)
	}
	if users := userTexts(w.Model.Requests()[1]); len(users) != 2 {
		t.Errorf("the resumed session's user messages: %q", users)
	}
	r = run(t, w, "--session-id", id, "-p", "third").wantCode(t, 1)
	has(t, "stderr", r.err, id)
}

// --verbose prints one line for every finished tool call on standard
// error and leaves standard output alone; --debug-file says where the log
// goes.
func TestVerboseAndDebugFile(t *testing.T) {
	w := apptest.NewWorld(t,
		apptest.Reply{Calls: []apptest.Call{
			{ID: "c1", Name: "Read", Args: `{"file_path":"a.txt"}`},
			{ID: "c2", Name: "Bash", Args: `{"command":"echo one; echo two; exit 3"}`},
		}},
		apptest.Reply{Text: "Over."})
	w.Write("a.txt", "hello\n")
	logFile := filepath.Join(w.Root, "debug.log")
	r := run(t, w, "-y", "--verbose", "--debug", "--debug-file", logFile, "-p", "look").wantCode(t, 0)
	if r.out != "Over.\n" {
		t.Errorf("stdout = %q", r.out)
	}
	has(t, "stderr", r.err, "Read: a.txt", "Bash: echo one; echo two; exit 3", "exit code 3")
	data, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatalf("the debug file was not written: %v", err)
	}
	has(t, "the debug file", string(data), "startup", "session", "Bash")

	// Without the flag the log lies beside the project's sessions.
	w = apptest.NewWorld(t, apptest.Reply{Text: "ok"})
	run(t, w, "-p", "hello").wantCode(t, 0).wantNoErr(t)
	if logs, _ := filepath.Glob(filepath.Join(w.DataDir(), "projects", "*", "gnosh.log")); len(logs) != 1 {
		t.Errorf("the project's log: %v", logs)
	}
}

// The thinking level for one run, and the working directory for one run.
func TestEffortAndCwd(t *testing.T) {
	w := apptest.NewWorld(t, apptest.Reply{Text: "ok"})
	w.Write("sub/dir/a.txt", "deep\n")
	run(t, w, "--reasoning-effort", "off", "--cwd", "sub/dir", "-p", "hello").wantCode(t, 0)
	first := requestText(t, w, 0)
	has(t, "the request", first, `"enable_thinking":false`, "Working directory: "+w.Path("sub/dir"))
	// The choice of one run is not kept for the next.
	if w.Exists(w.DataDir() + "/state.json") {
		t.Errorf("--reasoning-effort was saved: %s", w.Read(w.DataDir()+"/state.json"))
	}
	r := run(t, w, "--cwd", "is/not/there", "-p", "hello").wantCode(t, 1)
	has(t, "stderr", r.err, "is not a directory that is there")
}

// After a summary the system prompt is made again, as at the start, so
// what it is made of now is what the model is told: here the file of
// --append-system-prompt-file, changed during the session. Claude Code
// 2.1.328 does so after an automatic summary as after /compact.
func TestSummaryMakesTheSystemPromptAgain(t *testing.T) {
	w := apptest.NewWorld(t, apptest.Reply{Text: "First."}, apptest.Reply{Text: "SUMMARY: little."}, apptest.Reply{Text: "Second."})
	w.Write("more.txt", "FIRST EXTRA\n")
	s := sit(t, w, "--append-system-prompt-file", "more.txt")
	s.see("Type a prompt and Enter.")
	s.typeLine("hello")
	s.see("First.")
	w.Write("more.txt", "SECOND EXTRA\n")
	s.typeLine("/compact")
	s.see("The conversation was summarized")
	s.typeLine("go on")
	s.see("Second.")
	reqs := w.Model.Requests()
	if first, last := systemText(t, reqs[0]), systemText(t, reqs[len(reqs)-1]); !strings.HasSuffix(first, "FIRST EXTRA") || !strings.HasSuffix(last, "SECOND EXTRA") {
		t.Errorf("the system prompt before the summary ends ...%s, after ...%s", first[max(0, len(first)-40):], last[max(0, len(last)-40):])
	}
	s.typeLine("/exit")
	if code := s.exit(); code != 0 {
		t.Errorf("exit code %d", code)
	}
}
