package cli

import (
	"reflect"
	"slices"
	"testing"
)

// RelaunchArgs gives the command line that opens the same session again:
// the flags as they were, written out whole, without the prompt and what
// chose the conversation, and with the session's mode, model, thinking
// and work tree as they stand. Parsed, it asks for what the first line
// asked for but those.
func TestRelaunchArgs(t *testing.T) {
	tab := allBuilt(t)
	const id = "01a1dced-2312-7888-b312-da27ecb33d6d"
	cases := []struct {
		line string
		r    Relaunch
		want []string
	}{
		{`-y -c`, Relaunch{Session: id}, []string{"--dangerously-skip-permissions", "--resume=" + id}},
		{`-yc`, Relaunch{Session: id, Mode: ModeBypassPermissions}, []string{"--dangerously-skip-permissions", "--resume=" + id}},
		// Started skipping permissions, then switched to asking: the new
		// start asks, and can still go back.
		{`-y -c`, Relaunch{Session: id, Mode: ModeDefault}, []string{"--permission-mode=default", "--allow-dangerously-skip-permissions", "--resume=" + id}},
		{`--permission-mode plan "make a plan"`, Relaunch{Session: id, Mode: ModeAcceptEdits}, []string{"--permission-mode=acceptEdits", "--resume=" + id}},
		{`--permission-mode plan`, Relaunch{Session: id}, []string{"--permission-mode=plan", "--resume=" + id}},
		{`--allowedTools Read "Bash(git log:*)" -m qwen/a --effort low --worktree -- "fix the test"`,
			Relaunch{Session: id, Model: "qwen/b", Effort: "high", Tree: "calm-otter"},
			[]string{"--allowedTools=Read", "--allowedTools=Bash(git log:*)", "--model=qwen/b", "--reasoning-effort=high", "--worktree=calm-otter", "--resume=" + id}},
		{`-r old -n "a name" --settings ./s.json --add-dir ../x --fork-session -d hooks "go on"`, Relaunch{Session: id},
			[]string{"--settings=./s.json", "--add-dir=../x", "--debug", "--resume=" + id}},
		{`--debug=api,hooks --tools "" --session-id 6f1c2a9e-4b7d-4c1a-9e2f-3a5b6c7d8e9f`, Relaunch{Session: id},
			[]string{"--debug=api,hooks", "--tools=", "--resume=" + id}},
		{`--system-prompt "-be brief" -C hi`, Relaunch{Session: id}, []string{"--system-prompt=-be brief", "--resume=" + id}},
		// Started in bypassPermissions by its name, then switched to
		// asking: the new start can still go back, as after -y.
		{`--permission-mode bypassPermissions`, Relaunch{Session: id, Mode: ModeDefault},
			[]string{"--permission-mode=default", "--allow-dangerously-skip-permissions", "--resume=" + id}},
		// A model picked as the session started, named by no flag, goes
		// along; -m given twice is written once.
		{`-c`, Relaunch{Session: id, Model: "qwen/b"}, []string{"--model=qwen/b", "--resume=" + id}},
		{`-m qwen/a -m qwen/c`, Relaunch{Session: id, Model: "qwen/b"}, []string{"--model=qwen/b", "--resume=" + id}},
		// /effort auto since --effort high: the flag goes.
		{`--effort high -c`, Relaunch{Session: id, Effort: EffortDefault}, []string{"--resume=" + id}},
		// Folders /add-dir added go along, after the command line's own.
		{`--add-dir ../x -c`, Relaunch{Session: id, AddDirs: []string{"/srv/a", "/srv/b c"}},
			[]string{"--add-dir=../x", "--add-dir=/srv/a", "--add-dir=/srv/b c", "--resume=" + id}},
		// /cd: --cwd names the new folder; --trust-folder, about the old
		// one, goes, or is given again when the new folder was trusted for
		// the session only.
		{`--cwd /work/app --trust-folder -c`, Relaunch{Session: id, Cwd: "/work/lib"}, []string{"--cwd=/work/lib", "--resume=" + id}},
		{`--trust-folder -c`, Relaunch{Session: id, Cwd: "/home/u", TrustFolder: true}, []string{"--cwd=/home/u", "--trust-folder", "--resume=" + id}},
		// EnterWorktree: a session started without --worktree gets it;
		// ExitWorktree: one in a work tree goes back out.
		{`-c`, Relaunch{Session: id, Tree: "calm-otter"}, []string{"--worktree=calm-otter", "--resume=" + id}},
		{`--worktree=calm-otter -c`, Relaunch{Session: id, LeaveTree: true}, []string{"--resume=" + id}},
	}
	for _, c := range cases {
		args := words(t, "gnosh "+c.line)
		got, err := tab.RelaunchArgs(args, c.r)
		if err != nil {
			t.Errorf("%s: %v", c.line, err)
			continue
		}
		if !slices.Equal(got, c.want) {
			t.Errorf("%s:\n got %q\nwant %q", c.line, got, c.want)
			continue
		}
		// Read back, it is the first line's session in all but what moved.
		before, err := tab.Parse(args, tty)
		if err != nil {
			t.Errorf("%s: %v", c.line, err)
			continue
		}
		after, err := tab.Parse(got, tty)
		if err != nil {
			t.Errorf("%s: the relaunch %q does not parse: %v", c.line, got, err)
			continue
		}
		if after.Conversation != ConversationResume || after.Resume != id || after.Prompt != "" {
			t.Errorf("%s: the relaunch opens %v %q with the prompt %q", c.line, after.Conversation, after.Resume, after.Prompt)
		}
		before.Conversation, before.Resume, before.Prompt = after.Conversation, after.Resume, after.Prompt
		before.SessionID, before.ForkSession, before.Name = "", false, ""
		if c.r.Mode != "" {
			before.PermissionMode = c.r.Mode
			before.AllowSkipPermissions = after.AllowSkipPermissions
		}
		if c.r.Model != "" {
			before.Model = c.r.Model
		}
		switch c.r.Effort {
		case "":
		case EffortDefault:
			before.Effort = ""
		default:
			before.Effort = c.r.Effort
		}
		if c.r.Tree != "" {
			before.Worktree, before.WorktreeName = true, c.r.Tree
		}
		if c.r.LeaveTree {
			before.Worktree, before.WorktreeName = false, ""
		}
		before.AddDirs = append(before.AddDirs, c.r.AddDirs...)
		if c.r.Cwd != "" {
			before.Cwd, before.TrustFolder = c.r.Cwd, c.r.TrustFolder
		}
		if !reflect.DeepEqual(before, after) {
			t.Errorf("%s: the relaunch reads otherwise:\n got %+v\nwant %+v", c.line, after, before)
		}
	}

	// A session that has written nothing has no file to resume: the new
	// start makes it under its id. Such a session is one /clear began, so
	// -n's name is not its own and is left out.
	got, err := tab.RelaunchArgs(words(t, `gnosh -c -n "a name" --allowedTools Read`), Relaunch{Session: id, Fresh: true})
	if want := []string{"--allowedTools=Read", "--session-id=" + id}; err != nil || !slices.Equal(got, want) {
		t.Errorf("a fresh session relaunches as %q (%v), want %q", got, err, want)
	} else if inv, err := tab.Parse(got, tty); err != nil || inv.Conversation != ConversationNew || inv.SessionID != id || inv.Name != "" {
		t.Errorf("a fresh session's relaunch reads as %+v (%v)", inv, err)
	}

	// Only a session's line, and only with a session to resume.
	for _, line := range []string{`doctor`, `mem show`, `--help`} {
		if _, err := tab.RelaunchArgs(words(t, "gnosh "+line), Relaunch{Session: id}); err == nil {
			t.Errorf("%s: relaunched as a session", line)
		}
	}
	if _, err := tab.RelaunchArgs(nil, Relaunch{}); err == nil {
		t.Error("relaunched with no session")
	}
}

// Any command line that opens a session has a relaunch that opens the same
// session as it: the same flags in force, the session resumed, no prompt.
// A relaunch that read otherwise would start a /restart in another state,
// or not at all.
func FuzzRelaunchArgs(f *testing.F) {
	tab := allBuilt(f)
	addSeeds(f, func(line string) { f.Add(line) })
	const id = "01a1dced-2312-7888-b312-da27ecb33d6d"
	f.Fuzz(func(t *testing.T, line string) {
		args := cut(line)
		before, err := tab.Parse(args, tty)
		if err != nil || before.Command != CmdInteractive {
			return
		}
		got, err := tab.RelaunchArgs(args, Relaunch{Session: id})
		if err != nil {
			t.Fatalf("%q: no relaunch: %v", args, err)
		}
		after, err := tab.Parse(got, tty)
		if err != nil {
			t.Fatalf("%q: the relaunch %q does not parse: %v", args, got, err)
		}
		if after.Command != CmdInteractive || after.Conversation != ConversationResume || after.Resume != id || after.Prompt != "" {
			t.Fatalf("%q: the relaunch %q opens %v %v %q with the prompt %q", args, got, after.Command, after.Conversation, after.Resume, after.Prompt)
		}
		before.Conversation, before.Resume, before.ResumeIsFile, before.Prompt = after.Conversation, after.Resume, after.ResumeIsFile, after.Prompt
		before.SessionID, before.ForkSession, before.Name = "", false, ""
		if !reflect.DeepEqual(before, after) {
			t.Fatalf("%q: the relaunch %q reads otherwise:\n got %+v\nwant %+v", args, got, after, before)
		}
	})
}

// /background's run is the relaunch with -p and the prompt after --: a
// print run of the same session, on that prompt, whatever it holds.
func TestRelaunchAsARun(t *testing.T) {
	tab := allBuilt(t)
	const id = "01a1dced-2312-7888-b312-da27ecb33d6d"
	args, err := tab.RelaunchArgs(words(t, `gnosh -y --allowedTools Read -c`), Relaunch{Session: id, Mode: ModeAcceptEdits})
	if err != nil {
		t.Fatal(err)
	}
	inv, err := tab.Parse(append(args, "--print", "--", "-p fix the tests; --help"), tty)
	if err != nil {
		t.Fatal(err)
	}
	if inv.Command != CmdPrint || inv.Conversation != ConversationResume || inv.Resume != id || inv.Prompt != "-p fix the tests; --help" || inv.PermissionMode != ModeAcceptEdits {
		t.Errorf("the run reads as %+v", inv)
	}
}
