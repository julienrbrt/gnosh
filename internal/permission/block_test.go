package permission

import (
	"strings"
	"testing"
)

// blockOptions are the usual test options with the blocks on, an allow
// rule for everything under /etc, one of the person's own additional
// directories (/shared) and one only the project's file adds (/theirs).
func blockOptions(mode Mode, reads, writes bool) Options {
	o := testOptions(mode, rules{allow: []string{"Read(//etc/**)", "Edit(//etc/**)", "Bash"}})
	o.Layers = append(o.Layers,
		Layer{Scope: ScopeCLI, Name: "additional directories", Dirs: []string{"/shared"}},
		Layer{Scope: ScopeCLI, FromProject: true, Name: "the project's additional directories", Dirs: []string{"/theirs"}})
	o.BlockReads, o.BlockWrites = reads, writes
	o.OwnReads = []string{"/home/u/.config/gnosh/skills"}
	o.SettingsFiles = []string{"/home/u/.config/gnosh/gnosh.json", "/proj/gnosh.json", "/proj/.gnosh/settings.local.json"}
	o.SettingsNames = []string{"gnosh.json", ".gnosh/settings.local.json"}
	return o
}

// With blockReadsOutsideWorkingDirectories, Read, Grep and Glob refuse a
// path outside the working directories in every mode, bypassPermissions
// too, whatever an allow rule says; the working directories are the
// primary one and those the person added, not those only the project's
// file adds; gnosh's own files stay readable. Claude Code's
// permissions key of the same name.
func TestReadBlockRefusesFileToolsOutside(t *testing.T) {
	for _, mode := range []Mode{ModeDefault, ModeAcceptEdits, ModeBypass, ModeDontAsk, ModePlan} {
		e := mustEngine(t, blockOptions(mode, true, false))
		e.AddOwnRead("/home/u/.local/share/gnosh/sessions/proj/abc")
		for _, c := range []struct{ tool, path string }{
			{"Read", "/etc/passwd"}, {"Grep", "/etc"}, {"Glob", "/home/u"}, {"Read", "/theirs/a.txt"},
			{"Read", "/home/u/.config/gnosh/gnosh.json"},
		} {
			d := file(e, c.tool, c.path)
			if d.Behavior != Deny || d.Stage != StageBlock || !strings.Contains(d.Reason, "blockReadsOutsideWorkingDirectories") || !strings.Contains(d.Reason, "/add-dir") {
				t.Errorf("%s: %s %s = %+v, want refused by the block", mode, c.tool, c.path, d)
			}
		}
		for _, c := range []struct{ tool, path string }{
			{"Read", "/proj/main.go"}, {"Grep", "/proj"}, {"Read", "/shared/notes.md"},
			{"Read", "/home/u/.local/share/gnosh/sessions/proj/abc/tool-results/out.txt"},
			{"Read", "/home/u/.config/gnosh/skills/deploy/SKILL.md"},
		} {
			// (Not blocked; the mode judges it as before, and dontAsk
			// refuses what would ask.)
			if d := file(e, c.tool, c.path); d.Stage == StageBlock || d.Behavior == Deny && mode != ModeDontAsk {
				t.Errorf("%s: %s %s = %+v, want it not blocked", mode, c.tool, c.path, d)
			}
		}
	}
	// What /add-dir adds counts.
	e := mustEngine(t, blockOptions(ModeBypass, true, false))
	if err := e.AddDir("/etc"); err != nil {
		t.Fatal(err)
	}
	if d := file(e, "Read", "/etc/passwd"); d.Behavior != Allow {
		t.Errorf("after /add-dir /etc: %+v", d)
	}
	// Search results and file discovery leave a blocked file out.
	if !e.ReadDenied("/var/log/syslog") || e.ReadDenied("/proj/a.go") || e.ReadDenied("/home/u/.config/gnosh/skills/x/y.md") {
		t.Error("ReadDenied does not follow the read block")
	}
	// Without the block, nothing of the kind.
	off := mustEngine(t, blockOptions(ModeBypass, false, false))
	if d := file(off, "Read", "/var/log/syslog"); d.Behavior != Allow {
		t.Errorf("no block, bypassPermissions: %+v", d)
	}
}

// A link inside the working directory that leads out of it is outside.
func TestReadBlockFollowsLinks(t *testing.T) {
	o := blockOptions(ModeBypass, true, true)
	fs := fakeFS{links: map[string]string{"/proj/out": "/var/secret"}}
	o.Resolve = fs.resolve
	e := mustEngine(t, o)
	if d := file(e, "Read", "/proj/out/key.pem"); d.Behavior != Deny || d.Stage != StageBlock {
		t.Errorf("a read through a link out: %+v", d)
	}
	if d := file(e, "Write", "/proj/out/key.pem"); d.Behavior != Deny || d.Stage != StageBlock {
		t.Errorf("a write through a link out: %+v", d)
	}
}

// With blockWritesOutsideWorkingDirectories, Write, Edit and NotebookEdit
// refuse a path outside the working directories in every mode, and a
// settings file wherever it is; EnterWorktree and ExitWorktree are off.
// Shell commands are not held by it, as in Claude Code.
func TestWriteBlockRefusesFileToolsOutside(t *testing.T) {
	for _, mode := range []Mode{ModeDefault, ModeAcceptEdits, ModeBypass} {
		e := mustEngine(t, blockOptions(mode, false, true))
		for _, c := range []struct{ tool, path, says string }{
			{"Write", "/etc/hosts", "outside the working directories"},
			{"Edit", "/theirs/x.go", "outside the working directories"},
			{"NotebookEdit", "/tmp/n.ipynb", "outside the working directories"},
			{"Write", "/proj/gnosh.json", "settings file"},
			{"Edit", "/proj/.gnosh/settings.local.json", "settings file"},
			// A subfolder's are the settings of a session started there.
			{"Write", "/proj/sub/gnosh.json", "settings file"},
			{"Write", "/proj/sub/.gnosh/settings.local.json", "settings file"},
		} {
			d := file(e, c.tool, c.path)
			if d.Behavior != Deny || d.Stage != StageBlock || !strings.Contains(d.Reason, c.says) || !strings.Contains(d.Reason, "blockWritesOutsideWorkingDirectories") {
				t.Errorf("%s: %s %s = %+v", mode, c.tool, c.path, d)
			}
		}
		if d := file(e, "Write", "/proj/main.go"); d.Stage == StageBlock {
			t.Errorf("%s: a write inside: %+v", mode, d)
		}
		if d := file(e, "Edit", "/shared/notes.md"); d.Stage == StageBlock {
			t.Errorf("%s: a write in the person's added folder: %+v", mode, d)
		}
		if d := bash(e, "echo hi > /tmp/x"); d.Stage == StageBlock {
			t.Errorf("%s: a shell command was held by the write block: %+v", mode, d)
		}
		for _, tool := range []string{"EnterWorktree", "ExitWorktree"} {
			if e.ToolEnabled(tool) {
				t.Errorf("%s: %s is offered under the write block", mode, tool)
			}
		}
	}
	if e := mustEngine(t, blockOptions(ModeDefault, false, false)); !e.ToolEnabled("EnterWorktree") {
		t.Error("EnterWorktree is off without the block")
	}
}

// Under the read block a shell command that reads outside the working
// directories asks in every mode, bypassPermissions too (and dontAsk
// refuses it), whatever an allow rule says; so does one whose files gnosh
// cannot tell. A command that reads inside runs as before.
func TestReadBlockAsksAboutShellReadsOutside(t *testing.T) {
	for _, mode := range []Mode{ModeDefault, ModeBypass, ModeDontAsk} {
		e := mustEngine(t, blockOptions(mode, true, false))
		want := Ask
		if mode == ModeDontAsk {
			want = Deny
		}
		for _, cmd := range []string{"cat /etc/hosts", "head -5 /var/log/syslog", "grep -r key ~/.ssh", "wc -l < /etc/passwd"} {
			d := bash(e, cmd)
			if d.Behavior != want || d.Stage != StageBlock || !strings.Contains(d.Reason, "blockReadsOutsideWorkingDirectories") {
				t.Errorf("%s: %q = %+v, want %s by the block", mode, cmd, d, want)
			}
		}
		if d := bash(e, `cd "$(cat .where)" && cat notes`); d.Behavior != want || d.Stage != StageBlock || !strings.Contains(d.Reason, "cannot tell") {
			t.Errorf("%s: a command whose folder cannot be told = %+v", mode, d)
		}
		for _, cmd := range []string{"cat main.go", "ls /proj", "head /shared/notes.md"} {
			if d := bash(e, cmd); d.Stage == StageBlock {
				t.Errorf("%s: %q = %+v, want it not blocked", mode, cmd, d)
			}
		}
	}
}

// The read block's question about a shell command covers the folders the
// line runs in, not only the files it names (cd, env -C, git -C, and the
// shell's own folder), as a grep -r or a find with no path reads there
// (the blocks review of 2026-11-20).
func TestReadBlockAsksWhereACommandRuns(t *testing.T) {
	e := mustEngine(t, blockOptions(ModeBypass, true, false))
	for _, cmd := range []string{"cd /home/u/.ssh && grep -r PRIVATE", "env -C /home/u grep -r token", "git -C /etc log", "cd .. && ls"} {
		if d := bash(e, cmd); d.Behavior != Ask || d.Stage != StageBlock || !strings.Contains(d.Reason, "runs in") {
			t.Errorf("%q = %+v, want the block's question about where it runs", cmd, d)
		}
	}
	if d := bash(e, "cd /proj/sub && grep -r x"); d.Stage == StageBlock {
		t.Errorf("a line that stays inside: %+v", d)
	}
	e.SetCwd("/srv")
	if d := bash(e, "grep -r x"); d.Behavior != Ask || d.Stage != StageBlock {
		t.Errorf("a command in a shell that stands outside: %+v", d)
	}
}

// The block's question never softens what the other steps found: a
// dangerous removal stays critical, plan mode's question stays the
// person's, and a deny rule stays a refusal.
func TestReadBlockLeavesStricterAnswers(t *testing.T) {
	e := mustEngine(t, blockOptions(ModeBypass, true, false))
	if d := bash(e, "cat /etc/hostname; rm -rf ~"); !d.Critical {
		t.Errorf("a dangerous removal beside a read outside: %+v", d)
	}
	p := mustEngine(t, blockOptions(ModePlan, true, false))
	if d := bash(p, "cat /etc/x > notes"); d.Behavior != Ask || !d.PersonOnly || d.Stage == StageBlock {
		t.Errorf("plan mode's question: %+v", d)
	}
	o := blockOptions(ModeBypass, true, false)
	o.Layers[0].Deny = []string{"Bash(cat *)"}
	if d := bash(mustEngine(t, o), "cat /etc/hosts"); d.Behavior != Deny || d.Stage != StageDeny {
		t.Errorf("a deny rule: %+v", d)
	}
}

// The folders the blocks count are compared also where their links lead
// (/tmp is /private/tmp on macOS), and the project's added folders stay
// where a change of the settings files leaves the person's.
func TestBlockFoldersFollowLinksAndStay(t *testing.T) {
	o := blockOptions(ModeBypass, true, true)
	o.Layers[1].Dirs = []string{"/tmp/x"}
	o.Resolve = fakeFS{links: map[string]string{"/tmp": "/private/tmp"}}.resolve
	e := mustEngine(t, o)
	for _, tool := range []string{"Read", "Write"} {
		if d := file(e, tool, "/tmp/x/a.txt"); d.Stage == StageBlock {
			t.Errorf("%s in an added folder reached by a link: %+v", tool, d)
		}
	}
	e.RemoveLayers(ScopeProject)
	found := false
	for _, d := range e.Dirs() {
		found = found || d == "/theirs"
	}
	if !found {
		t.Errorf("the project's added folder went with a reload of the settings: %v", e.Dirs())
	}
}
