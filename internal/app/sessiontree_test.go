package app

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/gnosh-ai/gnosh/internal/app/apptest"
)

// gnosh --worktree name works in a git work tree of the repository's own, in
// .gnosh/worktrees/name on the branch worktree-name: what it writes lands
// there, not in the person's checkout, and the work tree, named, is kept
// with a line that says how to come back to it. --worktree name again
// opens it.
func TestWorktreeFlag(t *testing.T) {
	w := repoWorld(t,
		apptest.Reply{Calls: []apptest.Call{{ID: "w", Name: "Write", Args: `{"file_path":"made.txt","content":"in the work tree\n"}`}}},
		apptest.Reply{Text: "Made it."},
		apptest.Reply{Text: "Still here."},
	)
	tree := filepath.Join(w.Dir, ".gnosh", "worktrees", "feature-x")
	r := run(t, w, "--trust-folder", "--worktree", "feature-x", "--permission-mode", "acceptEdits", "-p", "make it").wantCode(t, 0)
	has(t, "stderr", r.err, "gnosh works in the git work tree "+tree+", made, on the branch worktree-feature-x",
		"the work tree "+tree+", on the branch worktree-feature-x, is kept (it holds 1 file changed or made and not committed", "gnosh --worktree feature-x works in it again")
	if got, err := os.ReadFile(filepath.Join(tree, "made.txt")); err != nil || string(got) != "in the work tree\n" {
		t.Errorf("made.txt in the work tree: %q, %v", got, err)
	}
	if w.Exists("made.txt") {
		t.Errorf("the session wrote into the person's checkout")
	}
	if branch := gitIn(t, w.Dir, "branch", "--list", "worktree-feature-x"); !strings.Contains(branch, "worktree-feature-x") {
		t.Errorf("no branch worktree-feature-x: %q", branch)
	}
	// Again: it is opened, not made.
	r = run(t, w, "--trust-folder", "--worktree", "feature-x", "-p", "still there?").wantCode(t, 0)
	has(t, "stderr", r.err, "gnosh works in the git work tree "+tree+", opened again, on the branch worktree-feature-x")
}

// Without a name, the work tree's name is made up, and one left as it was
// made goes with its branch when the session ends.
func TestWorktreeFlagUnnamed(t *testing.T) {
	w := repoWorld(t, apptest.Reply{Text: "Nothing to do."})
	r := run(t, w, "--trust-folder", "--worktree", "-p", "look").wantCode(t, 0)
	m := regexp.MustCompile(`work tree (\S+), made, on the branch (worktree-\S+) at`).FindStringSubmatch(r.err)
	if m == nil {
		t.Fatalf("no work tree was made:\n%s", r.err)
	}
	has(t, "stderr", r.err, "the work tree "+m[1]+" was left as it was made, and is removed with its branch")
	if fileExists(m[1]) {
		t.Errorf("the work tree %s is left", m[1])
	}
	if branch := gitIn(t, w.Dir, "branch", "--list", m[2]); branch != "" {
		t.Errorf("its branch is left: %q", branch)
	}
}

// The session's edit of a file in the person's checkout is refused, with
// the same file in the work tree named.
func TestWorktreeFlagKeepsToItsTree(t *testing.T) {
	w := repoWorld(t)
	target := filepath.Join(w.Dir, "notes.txt")
	w.Model.Add(apptest.Reply{Calls: []apptest.Call{{ID: "c1", Name: "Write", Args: fmt.Sprintf(`{"file_path":%q,"content":"x\n"}`, target)}}})
	w.Model.Add(apptest.Reply{Text: "It was refused."})
	run(t, w, "--trust-folder", "--worktree", "iso", "--permission-mode", "acceptEdits", "-p", "write it").wantCode(t, 0)
	if w.Exists("notes.txt") {
		t.Errorf("the session wrote into the person's checkout")
	}
	has(t, "the refusal", toolResult(t, w, 1, "c1"), "The same file in your work tree is "+filepath.Join(w.Dir, ".gnosh", "worktrees", "iso", "notes.txt"))
}

// A hard link to a file of the checkout, made in the work tree, is no way
// to write that file, by a command or by Write; nor is a line that makes a
// link and goes on through it (review, 16 November). Permissions are
// skipped, so the work tree is all that stands in the way.
func TestWorktreeFlagLinks(t *testing.T) {
	w := repoWorld(t)
	tree := filepath.Join(w.Dir, ".gnosh", "worktrees", "links")
	w.Model.Add(apptest.Reply{Calls: []apptest.Call{{ID: "c1", Name: "Bash", Args: fmt.Sprintf(`{"command":%q}`, "ln "+filepath.Join(w.Dir, "main.go")+" hl.go")}}})
	w.Model.Add(apptest.Reply{Calls: []apptest.Call{{ID: "c2", Name: "Bash", Args: `{"command":"echo changed > hl.go"}`}}})
	w.Model.Add(apptest.Reply{Calls: []apptest.Call{{ID: "c3", Name: "Write", Args: fmt.Sprintf(`{"file_path":%q,"content":"changed\n"}`, filepath.Join(tree, "hl.go"))}}})
	w.Model.Add(apptest.Reply{Calls: []apptest.Call{{ID: "c4", Name: "Bash", Args: fmt.Sprintf(`{"command":%q}`, "ln -s "+w.Dir+" r && cd r && touch x")}}})
	w.Model.Add(apptest.Reply{Text: "Done."})
	run(t, w, "--trust-folder", "--worktree", "links", "-y", "-p", "go").wantCode(t, 0)
	has(t, "a write through a hard link", toolResult(t, w, 2, "c2"), "This call did not run", "is a hard link")
	has(t, "Write through a hard link", toolResult(t, w, 3, "c3"), "This call did not run", "is a hard link")
	has(t, "a link and more", toolResult(t, w, 4, "c4"), "This call did not run", "makes a link and goes on")
	if got := w.Read("main.go"); got != "package main\n" {
		t.Errorf("the checkout's main.go changed: %q", got)
	}
	if w.Exists("x") {
		t.Errorf("a file was made in the checkout through the link")
	}
}

// git in a session's own work tree may not change what the work tree
// shares with the person's checkout: the branches by name, the git
// settings, a push into the repository itself. git that keeps to the work
// tree runs. Permissions are skipped, so the work tree is all that stands
// in the way (Claude Code 2.1.326 refuses the like for its sub-agents).
func TestWorktreeFlagSharedGit(t *testing.T) {
	w := repoWorld(t)
	before := gitIn(t, w.Dir, "rev-parse", "main")
	// A folder outside the work tree whose .git file names the checkout's
	// git folder: git there opens the checkout's repository (review, 17
	// November).
	elsewhere := t.TempDir()
	if err := os.WriteFile(filepath.Join(elsewhere, ".git"), []byte("gitdir: "+filepath.Join(w.Dir, ".git")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tree := filepath.Join(w.Dir, ".gnosh", "worktrees", "shared")
	cmds := []string{
		"git update-ref refs/heads/main HEAD",
		"git config core.fsmonitor 'touch fsmonitor-ran'",
		"git branch -m main trunk",
		"git push . HEAD:main",
		"git branch --show-current && git config --get-regexp '^core\\.'",
		"git -C " + elsewhere + " update-ref refs/heads/main HEAD",
		"printf 'gitdir: " + filepath.Join(w.Dir, ".git") + "\\n' > .git",
		"git status --short && git log --oneline -1",
	}
	for i, cmd := range cmds {
		w.Model.Add(apptest.Reply{Calls: []apptest.Call{{ID: fmt.Sprintf("c%d", i+1), Name: "Bash", Args: fmt.Sprintf(`{"command":%q}`, cmd)}}})
	}
	w.Model.Add(apptest.Reply{Calls: []apptest.Call{{ID: "c9", Name: "Write", Args: fmt.Sprintf(`{"file_path":%q,"content":"gitdir: %s\n"}`, filepath.Join(tree, ".git"), filepath.Join(w.Dir, ".git"))}}})
	w.Model.Add(apptest.Reply{Text: "Done."})
	run(t, w, "--trust-folder", "--worktree", "shared", "-y", "-p", "go").wantCode(t, 0)
	has(t, "update-ref", toolResult(t, w, 1, "c1"), "This call did not run", "may change what your work tree shares with the person's checkout", "git update-ref is not one gnosh lets run in a work tree of your own")
	has(t, "a setting", toolResult(t, w, 2, "c2"), "This call did not run", "git config writes the repository's git settings", "git -c user.name=... -c user.email=... commit")
	has(t, "a rename", toolResult(t, w, 3, "c3"), "This call did not run", "git branch: its option -m is not one")
	has(t, "a push into itself", toolResult(t, w, 4, "c4"), "This call did not run", "git push to . may push into a repository on this machine")
	if got := toolResult(t, w, 5, "c5"); strings.Contains(got, "did not run") || !strings.Contains(got, "worktree-shared") {
		t.Errorf("git that keeps to the work tree: %q", got)
	}
	has(t, "a .git file that names the checkout's", toolResult(t, w, 6, "c6"), "This call did not run", "would open the repository at", "not your work tree's own")
	has(t, "a write of the work tree's .git", toolResult(t, w, 7, "c7"), "This call did not run", "is a .git file or folder")
	if got := toolResult(t, w, 8, "c8"); strings.Contains(got, "did not run") {
		t.Errorf("reading git in the work tree: %q", got)
	}
	has(t, "Write to the work tree's .git", toolResult(t, w, 9, "c9"), "This call did not run", "is a .git file or folder")
	if after := gitIn(t, w.Dir, "rev-parse", "main"); after != before {
		t.Errorf("the checkout's branch moved: %s, then %s", before, after)
	}
	if got := w.Read(".git/config"); strings.Contains(got, "fsmonitor") {
		t.Errorf("a setting went into the checkout's git settings:\n%s", got)
	}
	if branch := gitIn(t, w.Dir, "branch", "--show-current"); branch != "main" {
		t.Errorf("the checkout's branch is %q, want main", branch)
	}
}

// A skill's own commands (!`cmd`) in a session with a work tree of its own
// keep to the work tree as its tool calls do (review, 16 November).
func TestWorktreeFlagSkillCommands(t *testing.T) {
	w := repoWorld(t, apptest.Reply{Text: "Ran."})
	w.Write(w.ConfigDir()+"/skills/poke/SKILL.md", "---\ndescription: Pokes.\n---\n!`echo poked > "+filepath.Join(w.Dir, "poked.txt")+"`\n")
	r := run(t, w, "--trust-folder", "--worktree", "skill", "-y", "-p", "/poke").wantCode(t, 1)
	has(t, "stderr", r.err, "Shell command permission check failed", "You work in a git work tree of your own", "this command writes")
	if w.Exists("poked.txt") {
		t.Errorf("the skill's command wrote into the person's checkout")
	}
}

// A work tree whose own configuration has a mistake (its commit's, which
// the checkout has fixed since) starts nothing, says why, and goes when
// nobody named it.
func TestWorktreeFlagConfigMistake(t *testing.T) {
	w := repoWorld(t)
	w.Write("gnosh.json", `{"permissions": {"allow": ["NoSuchTool"]}}`+"\n")
	gitIn(t, w.Dir, "add", "gnosh.json")
	gitIn(t, w.Dir, "commit", "-q", "-m", "a bad rule")
	w.Write("gnosh.json", `{"permissions": {"allow": ["Read"]}}`+"\n")
	r := run(t, w, "--trust-folder", "--worktree", "-p", "hi").wantCode(t, 1)
	has(t, "stderr", r.err, "the configuration has a mistake, so nothing was started", "NoSuchTool", "was left as it was made, and is removed with its branch")
	if trees, _ := filepath.Glob(filepath.Join(w.Dir, ".gnosh", "worktrees", "*-*")); len(trees) != 0 {
		t.Errorf("work trees left: %q", trees)
	}
}

// --agent with a kind that asks for a work tree of its own runs with
// --worktree, which gives the session one, and is refused without it.
func TestRunAsIsolatedAgent(t *testing.T) {
	w := repoWorld(t, apptest.Reply{Text: "Isolated."})
	w.Write(w.ConfigDir()+"/agents/iso.md", "---\nname: iso\ndescription: Works apart.\nisolation: worktree\n---\nYou work apart.\n")
	has(t, "without --worktree", run(t, w, "-p", "--agent", "iso", "x").wantCode(t, 1).err, "asks for isolation worktree", "start it with --worktree as well")
	has(t, "with --worktree", run(t, w, "--trust-folder", "--worktree", "apart", "-p", "--agent", "iso", "x").wantCode(t, 0).out, "Isolated.")
}

// --worktree needs a trusted folder in a git repository, and a name that can be a
// folder's and a branch's.
func TestWorktreeFlagRefusals(t *testing.T) {
	w := repoWorld(t)
	has(t, "an untrusted folder", run(t, w, "--worktree", "x", "-p", "hi").wantCode(t, 1).err, "--worktree: a work tree is made with git, and this folder is not trusted")
	has(t, "a bad name", run(t, w, "--trust-folder", "--worktree", "fix the bug", "-p", "hi").wantCode(t, 1).err, `"fix the bug" cannot name a work tree`, "put the prompt first or another flag between them")
	has(t, "a name that climbs out", run(t, w, "--trust-folder", "--worktree", "../x", "-p", "hi").wantCode(t, 1).err, `"../x" cannot name a work tree`)
	plain := apptest.NewWorld(t)
	has(t, "no repository", run(t, plain, "--trust-folder", "--worktree", "x", "-p", "hi").wantCode(t, 1).err, "is not in one")
}

// A command of a --worktree session runs in its work tree: one that would
// run in the person's checkout is refused, and so is one whose folder
// cannot be told from its text; one that stays runs.
func TestWorktreeFlagCommandsStay(t *testing.T) {
	w := repoWorld(t)
	w.Model.Add(apptest.Reply{Calls: []apptest.Call{{ID: "c1", Name: "Bash", Args: fmt.Sprintf(`{"command":%q}`, "cd "+w.Dir+" && ls")}}})
	w.Model.Add(apptest.Reply{Calls: []apptest.Call{{ID: "c2", Name: "Bash", Args: `{"command":"cd $X && ls"}`}}})
	w.Model.Add(apptest.Reply{Calls: []apptest.Call{{ID: "c3", Name: "Bash", Args: `{"command":"ls"}`}}})
	w.Model.Add(apptest.Reply{Calls: []apptest.Call{{ID: "c4", Name: "Bash", Args: fmt.Sprintf(`{"command":%q}`, "git -C "+w.Dir+" status")}}})
	w.Model.Add(apptest.Reply{Calls: []apptest.Call{{ID: "c5", Name: "Bash", Args: `{"command":"mem.Show()"}`}}})
	w.Model.Add(apptest.Reply{Calls: []apptest.Call{{ID: "c6", Name: "Bash", Args: fmt.Sprintf(`{"command":%q}`, "echo x > "+filepath.Join(w.Dir, "out.txt"))}}})
	w.Model.Add(apptest.Reply{Calls: []apptest.Call{{ID: "c7", Name: "Bash", Args: fmt.Sprintf(`{"command":%q}`, "cp "+filepath.Join(w.Dir, "main.go")+" copy.go")}}})
	w.Model.Add(apptest.Reply{Text: "Done."})
	run(t, w, "--trust-folder", "--worktree", "cmds", "-p", "look").wantCode(t, 0)
	tree := filepath.Join(w.Dir, ".gnosh", "worktrees", "cmds")
	has(t, "the first", toolResult(t, w, 1, "c1"), "This call did not run", "this command would run in "+w.Dir+", in the person's checkout", "the same folder there is "+tree)
	has(t, "the second", toolResult(t, w, 2, "c2"), "This call did not run", "gnosh cannot tell where this command runs: a cd in it goes to a place only known when it runs")
	if got := toolResult(t, w, 3, "c3"); strings.Contains(got, "did not run") || !strings.Contains(got, "main.go") {
		t.Errorf("ls in the work tree: %q", got)
	}
	has(t, "git pointed at the checkout", toolResult(t, w, 4, "c4"), "This call did not run", "this command points git at "+w.Dir)
	// Qwen once wrote mem's call as a shell command (box 1).
	has(t, "mem as a shell command", toolResult(t, w, 5, "c5"), "cannot be read as a shell command", "it is called with gnovm_run")
	// A write into the checkout by the path a command names is refused;
	// reading from the checkout into the work tree is not.
	has(t, "a write by path", toolResult(t, w, 6, "c6"), "This call did not run", "this command writes "+filepath.Join(w.Dir, "out.txt"), "in your work tree, "+filepath.Join(tree, "out.txt"))
	if w.Exists("out.txt") {
		t.Errorf("the command wrote into the person's checkout")
	}
	if got := toolResult(t, w, 7, "c7"); strings.Contains(got, "work tree of your own") {
		t.Errorf("a copy from the checkout into the work tree was held to the work tree: %q", got)
	}
}

// A --worktree session's new work tree gets the copies .worktreeinclude
// names, and says so; left as it was made, copies and all, it goes at the
// end.
func TestWorktreeFlagInclude(t *testing.T) {
	w := repoWorld(t, apptest.Reply{Text: "Nothing to do."})
	w.Write(".gitignore", ".env\n")
	w.Write(".worktreeinclude", ".env\n")
	gitIn(t, w.Dir, "add", ".gitignore", ".worktreeinclude")
	gitIn(t, w.Dir, "commit", "-q", "-m", "ignore")
	w.Write(".env", "TOKEN=kz-1\n")
	r := run(t, w, "--trust-folder", "--worktree", "-p", "look").wantCode(t, 0)
	m := regexp.MustCompile(`work tree (\S+), made, on the branch worktree-\S+ at \S+, with copies of \.env \(\.worktreeinclude\)`).FindStringSubmatch(r.err)
	if m == nil {
		t.Fatalf("no copies were said:\n%s", r.err)
	}
	has(t, "stderr", r.err, "the work tree "+m[1]+" was left as it was made, and is removed with its branch")
	if fileExists(m[1]) {
		t.Errorf("the work tree %s is left", m[1])
	}
}

// /restart in a work tree --worktree made under a made-up name starts
// again in it, and it is still one made here: left as it was made, it goes
// when the session ends (the cd review of 2026-11-19: found again by its
// name, it was taken for one named and kept for good).
func TestRestartKeepsAMadeUpTreeOneMadeHere(t *testing.T) {
	w := repoWorld(t)
	var started, envs []string
	s := sitWith(t, w, func(o *Options) {
		o.Relaunch = func(args, env []string) error {
			started, envs = args, env
			return os.ErrPermission
		}
	}, "--trust-folder", "--worktree")
	s.see("Type a prompt and Enter.")
	m := regexp.MustCompile(`work tree (\S+), made, on the branch`).FindStringSubmatch(s.errs.String() + s.out.String())
	if m == nil {
		t.Fatalf("no work tree was made:\n%s%s", s.errs.String(), s.out.String())
	}
	tree := strings.TrimSuffix(m[1], ",")
	s.typeLine("/restart")
	s.see("gnosh starts again")
	s.exit()
	if !fileExists(tree) {
		t.Fatalf("the work tree %s went at the restart", tree)
	}
	var markers []string
	for _, kv := range envs {
		if strings.HasPrefix(kv, RestartedEnv+"=") || strings.HasPrefix(kv, TreeMoveEnv+"=") {
			markers = append(markers, kv)
		}
	}
	if !slices.ContainsFunc(markers, func(kv string) bool { return strings.Contains(kv, `"made"`) }) {
		t.Fatalf("the start /restart makes is not told the tree was made here: %q", markers)
	}
	s = sitWith(t, w, func(o *Options) { o.Environ = append(slices.Clone(o.Environ), markers...) }, started...)
	s.see("Type a prompt and Enter.")
	s.typeLine("/exit")
	s.exit()
	if fileExists(tree) {
		t.Errorf("the work tree %s, made under a made-up name and left as made, is kept after the restarted session", tree)
	}
}
