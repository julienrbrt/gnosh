package app

import (
	"encoding/base64"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/gnosh-ai/gnosh/internal/app/apptest"
)

// The slash commands of a session, typed at the terminal: each does what
// it says and says what it did.
func TestSlashCommandsInASession(t *testing.T) {
	w := apptest.NewWorld(t)
	w.Model.SetModels("test-model", "other-model")
	w.Deny("Bash(rm *)")
	hook := w.Script(w.Home+"/hooks/noop.sh", "cat > /dev/null")
	w.Hook("Stop", "", hook)
	turns := 0
	w.Model.Respond(func(n int, r apptest.Request) *apptest.Reply {
		if isSummaryRequest(r) {
			return &apptest.Reply{Text: "SUMMARY: the user asked for the first task, and it is under way."}
		}
		turns++
		switch turns {
		case 1:
			return &apptest.Reply{Text: "Started on the first task."}
		case 2:
			return &apptest.Reply{Text: "One more step is done."}
		case 3:
			return &apptest.Reply{Text: "All of it is done.\nLOOP_DONE"}
		case 4:
			return &apptest.Reply{Text: "The listing shows the three files I made.\nLOOP_DONE"}
		}
		return &apptest.Reply{Text: "An answer from the other model."}
	})
	s := sit(t, w)
	s.see("Type a prompt and Enter.")
	s.typeLine("do the first task")
	s.see("Started on the first task.")
	s.see("-- done in ")

	// /loop keeps the session working until the model shows it is done.
	s.typeLine("/loop")
	s.see("/loop is on")
	s.see("One more step is done.")
	s.see("All of it is done.")
	s.see("The listing shows the three files I made.")
	s.see("/loop is off")
	if turns != 4 {
		t.Errorf("the model was asked %d times under /loop, want 4 in all", turns)
	}

	// /compact makes a summary now, with the person's words for it.
	s.typeLine("/compact keep the file names")
	s.see("The conversation was summarized")
	found := false
	for _, r := range w.Model.Requests() {
		if isSummaryRequest(r) && strings.Contains(string(r.Body), "keep the file names") {
			found = true
		}
	}
	if !found {
		t.Errorf("the summary request does not carry the person's instructions")
	}

	// The reports.
	s.typeLine("/permissions")
	s.see("Permission mode: default")
	s.see("Bash(rm *)")
	s.typeLine("/hooks")
	s.see("Stop:")
	s.see("  " + hook)
	s.see("from " + w.SettingsFile())
	s.typeLine("/doctor")
	s.see("model: local/test-model")
	s.see("A turn can run.")

	// /model lists what there is and switches; the choice is kept.
	s.typeLine("/model")
	s.see("In use: local/test-model")
	s.see("* local/test-model")
	s.see("local/other-model")
	s.typeLine("/model local/other-model")
	s.see("The model is local/other-model")
	s.typeLine("which model are you?")
	s.see("An answer from the other model.")
	s.see("model local/other-model")
	reqs := w.Model.Requests()
	if got := reqs[len(reqs)-1].JSON["model"]; got != "other-model" {
		t.Errorf("the request after /model names the model %v", got)
	}
	has(t, "the state file", w.Read(w.DataDir()+"/state.json"), "local/other-model")
	s.typeLine("/model no-such-model extra words here")
	s.see("error: /model takes a model")

	// /clear starts a fresh session; the one before is kept, under the
	// name given.
	before := w.Sessions()
	s.typeLine("/clear model talk")
	s.see("A new session is open: ")
	s.see(`The one before is kept as "model talk"; gnosh -r "model talk" opens it again.`)
	s.typeLine("hello again")
	s.see("An answer from the other model.")
	last := w.Model.Requests()[len(w.Model.Requests())-1]
	if users := userTexts(last); len(users) != 1 || !strings.Contains(users[0], "hello again") || strings.Contains(string(last.Body), "first task") {
		t.Errorf("the session after /clear is not a fresh one: %q", users)
	}
	s.typeLine("/exit")
	s.exit()
	after := w.Sessions()
	if len(before) != 1 || len(after) != 2 {
		t.Fatalf("session files before /clear: %v, after: %v", before, after)
	}
	for _, path := range after {
		checkSession(t, w, path)
	}
}

// The commands that follow Claude Code's: /add-dir, /cost, /agents,
// /memory, /plan and /init.
func TestClaudeCodeCommands(t *testing.T) {
	w := apptest.NewWorld(t,
		apptest.Reply{Text: "Hello."},
		apptest.Reply{Text: "Here is the plan: read, then change."},
		apptest.Reply{Text: "GNOSH.md is written."},
	)
	w.Write("GNOSH.md", "# the project\n")
	other := w.Home + "/elsewhere"
	w.Write(other+"/notes.txt", "notes\n")
	s := sit(t, w)
	s.see("Type a prompt and Enter.")

	// Nothing used yet, and then a turn's worth.
	s.typeLine("/cost")
	s.see("Nothing has been asked of a model in this session yet.")
	s.typeLine("say hello")
	s.see("Hello.")
	// (What a turn used is counted when it ends.)
	s.see("-- done in ")
	s.typeLine("/usage")
	s.see("This session: 1 request to its model")
	s.see("tokens (")
	s.see("Every request made for it, by model")

	// /add-dir: refused for what is not a directory, then added, and the
	// model is told with the next request.
	s.typeLine("/add-dir " + other + "/notes.txt")
	s.see("is a file; /add-dir takes a directory")
	s.typeLine("/add-dir ~/no-such-folder")
	s.see("does not exist")
	s.typeLine("/add-dir ~/elsewhere")
	s.see(other + " is a working directory for the rest of this session")
	s.typeLine("/add-dir")
	s.see("  " + other + " (added)")
	s.typeLine("/add-dir " + other)
	s.see("is a working directory of this session already")

	// /tasks: nothing yet, and an id that is not one.
	s.typeLine("/tasks")
	s.see("No command has run in the background in this session.")
	s.typeLine("/bashes stop bg9")
	s.see("bg9 is not one of this session's background tasks")

	// /agents and /memory report.
	s.typeLine("/agents")
	s.see("The kinds of sub-agent the model can start:")
	s.see("(built-in)")
	s.typeLine("/memory")
	s.see("The context files the model is given:")
	s.see(w.Dir + "/GNOSH.md (project")
	s.see("/mem shows it")

	// /plan with a task: plan mode, and the task sent.
	s.typeLine("/plan work out the change")
	s.see("Here is the plan: read, then change.")
	s.see("permission mode plan")
	reqs := w.Model.Requests()
	planReq := string(reqs[len(reqs)-1].Body)
	has(t, "the request /plan sent", planReq, "work out the change", other)

	// /export writes the conversation as text, and writes over nothing.
	s.typeLine("/export talk")
	s.see("The conversation is in " + w.Dir + "/talk.txt")
	exported := w.Read("talk.txt")
	has(t, "the exported conversation", exported, "> say hello\nHello.\n", "> work out the change\n", "Here is the plan: read, then change.")
	if strings.Contains(exported, "\n\n") {
		t.Errorf("the export holds an empty line:\n%s", exported)
	}
	if info, err := os.Stat(w.Path("talk.txt")); err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("the export is %v (%v), want the person's alone (0600)", info.Mode().Perm(), err)
	}
	s.typeLine("/export talk.txt")
	s.see("is there already, and /export writes over nothing")
	s.typeLine("/export")
	s.see("The conversation is in " + w.Dir + "/")
	// A folder named gets the file of the usual name in it, not a file
	// beside it (the hosts review of 2026-11-19).
	if err := os.Mkdir(w.Path("exports"), 0o755); err != nil {
		t.Fatal(err)
	}
	s.typeLine("/export exports")
	s.see("The conversation is in " + w.Dir + "/exports/")
	if w.Exists("exports.txt") {
		t.Error("/export of a folder wrote a file beside it")
	}

	// /init asks the model to improve the GNOSH.md there is.
	s.typeLine("/init")
	s.see("GNOSH.md is written.")
	reqs = w.Model.Requests()
	has(t, "the request /init sent", string(reqs[len(reqs)-1].Body), "The user typed /init", "GNOSH.md is there already")
	s.typeLine("/exit")
	s.exit()
}

// /diff: what changed since the last commit, in a trusted folder only.
func TestDiffCommand(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("there is no git on this machine")
	}
	w := apptest.NewWorld(t)
	w.Write("a.txt", "one\ntwo\n")
	gitIn := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", w.Dir, "-c", "user.name=t", "-c", "user.email=t@example.com", "-c", "commit.gpgsign=false"}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "HOME="+w.Home)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	// An untrusted folder: git is not run at all.
	r := run(t, w, "-p", "/diff")
	has(t, "/diff in a folder that is not trusted", r.out+r.err, "this folder is not trusted")

	// Trusted, but no repository.
	r = run(t, w, "--trust-folder", "-p", "/diff").wantCode(t, 0)
	has(t, "/diff outside a repository", r.out, "is not in a git repository")

	// A repository with no commit yet: everything is new.
	gitIn("init", "-q")
	r = run(t, w, "--trust-folder", "-p", "/diff").wantCode(t, 0)
	has(t, "/diff before the first commit", r.out, "New files git does not track yet (1): a.txt")

	gitIn("add", ".")
	gitIn("commit", "-q", "-m", "first")
	r = run(t, w, "--trust-folder", "-p", "/diff").wantCode(t, 0)
	has(t, "/diff with nothing changed", r.out, "Nothing has changed since the last commit.")

	w.Write("a.txt", "one\nTWO\n")
	w.Write("new.txt", "x\n")
	r = run(t, w, "--trust-folder", "-p", "/diff").wantCode(t, 0)
	has(t, "/diff with changes", r.out, "Changed since the last commit:", "a.txt | 2", "New files git does not track yet (1): new.txt", "-two", "+TWO")
}

// A slash command given as the prompt of a run is run, not sent to the
// model, and what it says is the run's result. A command that only makes
// sense with a person there says so.
func TestSlashCommandsAsARunsPrompt(t *testing.T) {
	w := apptest.NewWorld(t, apptest.Reply{Text: "The first answer."})
	run(t, w, "-p", "remember this").wantCode(t, 0)
	w.Model.Respond(func(n int, r apptest.Request) *apptest.Reply {
		if isSummaryRequest(r) {
			return &apptest.Reply{Text: "SUMMARY: the user said to remember this."}
		}
		return nil
	})

	r := run(t, w, "-c", "-p", "/status").wantCode(t, 0)
	has(t, "/status", r.out, "session      ", "model        local/test-model", "permissions  mode default", "The configuration in force")
	r = run(t, w, "-p", "--output-format", "json", "/help").wantCode(t, 0)
	obj := jsonLines(t, r.out)[0]
	if obj["subtype"] != "success" || obj["local_command"] != "help" || !strings.Contains(obj["result"].(string), "/compact [instructions]") {
		t.Errorf("/help as json: %s", clip(r.out))
	}
	r = run(t, w, "-c", "-p", "/compact").wantCode(t, 0)
	has(t, "/compact", r.out, "The conversation was summarized")
	// A run keeps no choice for later sessions, as Claude Code's -p keeps
	// none, and max holds for the session alone (the settings-commands
	// review of 2026-11-20).
	r = run(t, w, "-p", "/no_think").wantCode(t, 0)
	has(t, "/no_think", r.out, "Thinking is off for local/test-model", "in this run only")
	r = run(t, w, "-p", "/effort").wantCode(t, 0)
	has(t, "/effort: the run kept nothing", r.out, "Thinking for local/test-model: on.", "Its levels are off, on")
	r = run(t, w, "-p", "/effort max").wantCode(t, 0)
	has(t, "/effort max", r.out, "Thinking is on for local/test-model", "in this session only")
	r = run(t, w, "-p", "/effort lots").wantCode(t, 1)
	has(t, "/effort lots", r.err, `"lots" is not one of the thinking levels of local/test-model (off, on). Nothing changed`)
	r = run(t, w, "-p", "/effort auto").wantCode(t, 0)
	has(t, "/effort auto", r.out, "Thinking for local/test-model is the model's own default")
	r = run(t, w, "-p", "/reload-skills").wantCode(t, 0)
	has(t, "/reload-skills", r.out, "The skill and command folders were read again, and hold no skill.")
	w.Write(".claude/skills/tidy/SKILL.md", "---\nname: tidy\ndescription: Tidies the imports.\n---\nTidy them.\n")
	r = run(t, w, "--trust-folder", "-p", "/reload-skills").wantCode(t, 0)
	has(t, "/reload-skills with a skill", r.out, "The skill and command folders were read again: 1 skills, tidy.")
	r = run(t, w, "-p", "/exit").wantCode(t, 1)
	has(t, "stderr", r.err, "/exit works in an interactive session")
	if r.out != "" {
		t.Errorf("stdout = %q", r.out)
	}
	// Only the first prompt and the summary went to the model.
	if n := len(w.Model.Requests()); n != 2 {
		t.Errorf("the model was sent %d requests, want 2", n)
	}
	// A prompt that merely begins with a slash is a prompt.
	w.Model.Respond(nil)
	w.Model.Add(apptest.Reply{Text: "That is a path."})
	r = run(t, w, "-p", "/usr/local/bin is what?").wantCode(t, 0)
	if r.out != "That is a path.\n" {
		t.Errorf("stdout = %q", r.out)
	}
	// With --disable-slash-commands the command's name goes to the model.
	w.Model.Add(apptest.Reply{Text: "You typed a slash command."})
	r = run(t, w, "--disable-slash-commands", "-p", "/status").wantCode(t, 0)
	if r.out != "You typed a slash command.\n" {
		t.Errorf("stdout = %q", r.out)
	}
}

// overSSH is a change to a seat's options: gnosh was reached over ssh, so
// the person's terminal is on another machine.
func overSSH(o *Options) {
	o.Environ = append(o.Environ, "SSH_CONNECTION=203.0.113.7 50000 203.0.113.8 22")
}

// /copy puts the model's last reply, or the Nth from the end, on the
// clipboard: over ssh, through the terminal (OSC 52).
func TestCopyCommand(t *testing.T) {
	w := apptest.NewWorld(t, apptest.Reply{Text: "First answer."}, apptest.Reply{Text: "Second answer."})
	s := sitWith(t, w, overSSH)
	s.see("Type a prompt and Enter.")
	s.typeLine("/copy")
	s.see("the model has not replied in this session yet")
	s.typeLine("one")
	s.see("First answer.")
	s.typeLine("two")
	s.see("Second answer.")
	// (A reply counts once its turn has ended.)
	s.see("-- done in ")
	// The sequence goes out first, then the line about it. (The screen
	// the test reads has no escape characters left in it.)
	s.typeLine("/copy")
	s.see("]52;c;" + base64.StdEncoding.EncodeToString([]byte("Second answer.")))
	s.see("The model's last reply (14 characters) is on its way to the clipboard")
	s.typeLine("/copy 2")
	s.see("]52;c;" + base64.StdEncoding.EncodeToString([]byte("First answer.")))
	s.see("The model's reply 2 from the end (13 characters)")
	s.typeLine("/copy 3")
	s.see("the model has replied 2 times in this session, so there is no reply 3 from the end")
	s.typeLine("/copy x")
	s.see("/copy takes nothing, or a number")
	// /export --clipboard, what the interface's /export picker runs: the
	// whole conversation, the same way.
	s.typeLine("/export --clipboard")
	s.see("]52;c;")
	s.see("The conversation (")
	s.typeLine("/exit")
	s.exit()
}

// What goes on the clipboard has no control characters but tab and the
// line breaks: pasted into a terminal next, an escape sequence in a reply
// (ESC [201~ ends a bracketed paste) could have the rest taken as typed
// (the TUI review of 2026-11-19).
func TestCopyLeavesControlCharactersOut(t *testing.T) {
	w := apptest.NewWorld(t, apptest.Reply{Text: "run this:\x1b[201~\rrm -rf ~\r\nok\tdone\x07"})
	got := w.Path("clipboard.txt")
	for _, name := range []string{"pbcopy", "wl-copy", "xclip", "xsel"} {
		w.Script(w.Root+"/clipbin/"+name, `cat > "`+got+`"`)
	}
	w.PathFirst = append(w.PathFirst, w.Root+"/clipbin")
	s := sitWith(t, w, func(o *Options) {
		o.Environ = append(o.Environ, "DISPLAY=:0", "WAYLAND_DISPLAY=wayland-0")
	})
	s.see("Type a prompt and Enter.")
	s.typeLine("hello")
	s.see("-- done in ")
	s.typeLine("/copy")
	s.see("is on the clipboard.")
	if b, err := os.ReadFile(got); err != nil || string(b) != "run this:[201~\nrm -rf ~\nok\tdone" {
		t.Errorf("the clipboard tool was given %q (%v)", b, err)
	}
	s.typeLine("/exit")
	s.exit()
}

// Where the terminal is on this machine, /copy hands the reply to the
// machine's own clipboard tool, and sends nothing through the terminal;
// when the tool fails, it says so and sends the text through the terminal.
func TestCopyCommandHere(t *testing.T) {
	w := apptest.NewWorld(t, apptest.Reply{Text: "An answer."})
	got := w.Path("clipboard.txt")
	// Stand-ins for every tool a machine might have, first on the PATH,
	// so that the test never reaches a real clipboard.
	for _, name := range []string{"pbcopy", "wl-copy", "xclip", "xsel"} {
		w.Script(w.Root+"/clipbin/"+name, `if [ -e "`+got+`.broken" ]; then echo broken >&2; exit 3; fi; cat > "`+got+`"`)
	}
	w.PathFirst = append(w.PathFirst, w.Root+"/clipbin")
	s := sitWith(t, w, func(o *Options) {
		o.Environ = append(o.Environ, "DISPLAY=:0", "WAYLAND_DISPLAY=wayland-0")
	})
	s.see("Type a prompt and Enter.")
	s.typeLine("hello")
	s.see("-- done in ")
	s.typeLine("/copy")
	s.see("The model's last reply (10 characters) is on the clipboard.")
	if b, err := os.ReadFile(got); err != nil || string(b) != "An answer." {
		t.Errorf("the clipboard tool was given %q (%v)", b, err)
	}
	if strings.Contains(s.out.String(), "]52;") {
		t.Errorf("the text went through the terminal too")
	}
	w.Write(got+".broken", "")
	s.typeLine("/copy")
	s.see("]52;c;" + base64.StdEncoding.EncodeToString([]byte("An answer.")))
	s.see("failed: exit status 3)")
	s.typeLine("/exit")
	s.exit()
}
