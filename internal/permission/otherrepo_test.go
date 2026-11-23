package permission

import (
	"strings"
	"testing"
)

// While the shell's folder is in another repository than the working
// directories' (moved there by an earlier command), git commands ask,
// even read-only ones and ones a rule allows: that repository's own
// settings could name programs git runs. A work tree or submodule of the
// project's repository is the project's; a folder in no repository, and
// commands that are not git, are as before; with permissions skipped
// nothing asks.
func TestGitInAnotherRepository(t *testing.T) {
	repos := map[string]string{
		"/proj":                      "/proj/.git",
		"/proj/sub":                  "/proj/.git",
		"/proj/vendor/x":             "/proj/vendor/x/.git",
		"/proj/.gnosh/worktrees/dev": "/proj/.git/worktrees/dev",
		"/proj/mods/lib":             "/proj/.git/modules/lib",
	}
	o := testOptions(ModeDefault, rules{allow: []string{"Bash(git log:*)"}})
	o.RepoOf = func(dir string) string { return repos[dir] }
	e := mustEngine(t, o)
	for _, c := range []struct {
		cur, cmd string
		want     Behavior
	}{
		{"/proj", "git status", Allow},
		{"/proj/sub", "git status", Allow},
		{"/proj/vendor/x", "git status", Ask},
		{"/proj/vendor/x", "git log --oneline", Ask},
		{"/proj/vendor/x", "ls && git diff", Ask},
		{"/proj/vendor/x", "ls", Allow},
		{"/proj/.gnosh/worktrees/dev", "git status", Allow},
		{"/proj/mods/lib", "git status", Allow},
		{"/proj/plain", "git status", Allow},
	} {
		e.SetCwd(c.cur)
		d := bash(e, c.cmd)
		if d.Behavior != c.want {
			t.Errorf("in %s, %q: %s (%s), want %s", c.cur, c.cmd, d.Behavior, d.Reason, c.want)
		}
		if c.want == Ask && !strings.Contains(d.Reason, "another repository") {
			t.Errorf("in %s, %q asks for another reason: %s", c.cur, c.cmd, d.Reason)
		}
	}
	e.SetCwd("/proj/vendor/x")
	if err := e.SetMode(ModeBypass); err != nil {
		t.Fatal(err)
	}
	if d := bash(e, "git status"); d.Behavior != Allow {
		t.Errorf("with permissions skipped: %s (%s)", d.Behavior, d.Reason)
	}
}
