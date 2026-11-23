package app

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/gnosh-ai/gnosh/internal/app/apptest"
	"github.com/gnosh-ai/gnosh/internal/core"
)

// gitIn runs git in dir for a test, as nobody in particular.
func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@example.com", "-c", "init.defaultBranch=main"}, args...)...)
	cmd.Env = []string{"HOME=" + dir, "PATH=" + os.Getenv("PATH"), "GIT_CONFIG_NOSYSTEM=1"}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// repoWorld is a world whose folder is a git repository with one commit.
func repoWorld(t *testing.T, replies ...apptest.Reply) *apptest.World {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git on this machine")
	}
	w := apptest.NewWorld(t, replies...)
	w.Write("main.go", "package main\n")
	gitIn(t, w.Dir, "init", "-q")
	gitIn(t, w.Dir, "add", "main.go")
	gitIn(t, w.Dir, "commit", "-q", "-m", "first")
	return w
}

// A sub-agent asked for a work tree of its own (isolation worktree) works
// in a git work tree of the repository at its current commit: what it
// writes lands there, without a question in acceptEdits, and not in the
// folder the session works in; the report says where it is kept. One that
// changed nothing is removed with its branch. An untrusted folder, and one
// that is no repository, get no work tree.
func TestSubAgentWorktree(t *testing.T) {
	w := repoWorld(t,
		apptest.Reply{Calls: []apptest.Call{{ID: "c1", Name: "Agent", Args: `{"description":"write it","prompt":"Write new.txt.","isolation":"worktree"}`}}},
		apptest.Reply{Calls: []apptest.Call{{ID: "c1", Name: "Write", Args: `{"file_path":"new.txt","content":"made in the work tree\n"}`}}},
		apptest.Reply{Text: "Wrote new.txt."},
		apptest.Reply{Text: "Done."},
	)
	run(t, w, "--permission-mode", "acceptEdits", "--trust-folder", "-p", "write it").wantCode(t, 0)
	if w.Exists("new.txt") {
		t.Errorf("the sub-agent wrote into the session's own folder")
	}
	report := toolResult(t, w, 3, "c1")
	has(t, "the report", report, "Wrote new.txt.", "It worked in its own git work tree", "on the branch gnosh-agent-", "it holds 1 file changed or made and not committed", "worktree remove --force")
	m := regexp.MustCompile(`work tree, (\S+), on the branch`).FindStringSubmatch(report)
	if m == nil {
		t.Fatalf("the report names no work tree:\n%s", report)
	}
	if got, err := os.ReadFile(filepath.Join(m[1], "new.txt")); err != nil || string(got) != "made in the work tree\n" {
		t.Errorf("the work tree holds %q, %v", got, err)
	}
	if status := gitIn(t, w.Dir, "status", "--porcelain"); strings.Contains(status, "worktrees") || strings.Contains(status, "new.txt") {
		t.Errorf("the repository's status shows the work tree:\n%s", status)
	}

	// One that changed nothing is removed, with its branch.
	w = repoWorld(t,
		apptest.Reply{Calls: []apptest.Call{{ID: "c1", Name: "Agent", Args: `{"description":"look","prompt":"Look.","isolation":"worktree"}`}}},
		apptest.Reply{Text: "Nothing to change."},
		apptest.Reply{Text: "Done."},
	)
	run(t, w, "--trust-folder", "-p", "look").wantCode(t, 0)
	has(t, "the report", toolResult(t, w, 2, "c1"), "Nothing to change.", "changed nothing there; the work tree is removed.")
	if list := gitIn(t, w.Dir, "worktree", "list"); strings.Count(list, "\n") != 0 {
		t.Errorf("a work tree is left:\n%s", list)
	}
	if branches := gitIn(t, w.Dir, "branch", "--list", "gnosh/*"); branches != "" {
		t.Errorf("its branch is left: %s", branches)
	}

	// An untrusted folder gets none, and neither does a folder that is no
	// repository.
	w = repoWorld(t,
		apptest.Reply{Calls: []apptest.Call{{ID: "c1", Name: "Agent", Args: `{"description":"look","prompt":"Look.","isolation":"worktree"}`}}},
		apptest.Reply{Text: "Done."},
	)
	run(t, w, "-p", "look").wantCode(t, 0)
	has(t, "an untrusted folder", toolResult(t, w, 1, "c1"), "No sub-agent was started", "this folder is not trusted")
	w = apptest.NewWorld(t,
		apptest.Reply{Calls: []apptest.Call{{ID: "c1", Name: "Agent", Args: `{"description":"look","prompt":"Look.","isolation":"worktree"}`}}},
		apptest.Reply{Text: "Done."},
	)
	run(t, w, "--trust-folder", "-p", "look").wantCode(t, 0)
	has(t, "no repository", toolResult(t, w, 1, "c1"), "No sub-agent was started", "needs a git repository")
}

// From a folder below the repository's top, the sub-agent starts in the
// same folder of its work tree, and its writes there are a working
// directory's, without a question in acceptEdits. Two sub-agents in one
// reply get work trees of their own: no two share a name.
func TestSubAgentWorktreeFromASubfolder(t *testing.T) {
	w := repoWorld(t,
		apptest.Reply{Calls: []apptest.Call{
			{ID: "c1", Name: "Agent", Args: `{"description":"one","prompt":"Write one.txt.","isolation":"worktree","run_in_background":true}`},
			{ID: "c2", Name: "Agent", Args: `{"description":"two","prompt":"Write two.txt.","isolation":"worktree","run_in_background":true}`},
		}},
	)
	w.Write("sub/keep.txt", "kept\n")
	gitIn(t, w.Dir, "add", "sub/keep.txt")
	gitIn(t, w.Dir, "commit", "-q", "-m", "sub")
	w.Model.Respond(func(n int, r apptest.Request) *apptest.Reply {
		body := string(r.Body)
		if !strings.Contains(body, "You are a sub-agent inside gnosh") {
			if n == 0 {
				return nil // the session's first reply: the script's
			}
			return &apptest.Reply{Text: "Noted."}
		}
		file := "one.txt"
		if strings.Contains(body, "Write two.txt.") {
			file = "two.txt"
		}
		if strings.Contains(body, `"role":"tool"`) {
			return &apptest.Reply{Text: "Wrote " + file + "."}
		}
		return &apptest.Reply{Calls: []apptest.Call{{ID: "w", Name: "Write", Args: fmt.Sprintf(`{"file_path":%q,"content":%q}`, file, file+"\n")}}}
	})
	o := testOptions(w, "--permission-mode", "acceptEdits", "--trust-folder", "-p", "write them")
	o.Cwd = filepath.Join(w.Dir, "sub")
	runWith(t, t.Context(), o).wantCode(t, 0)
	trees, _ := filepath.Glob(filepath.Join(w.Dir, ".gnosh", "worktrees", "agent-*"))
	if len(trees) != 2 {
		t.Fatalf("work trees %q, want two of their own", trees)
	}
	var made []string
	for _, tree := range trees {
		for _, f := range []string{"one.txt", "two.txt"} {
			if data, err := os.ReadFile(filepath.Join(tree, "sub", f)); err == nil && strings.TrimSpace(string(data)) != "" {
				made = append(made, f)
			}
		}
	}
	if strings.Join(made, " ") != "one.txt two.txt" && strings.Join(made, " ") != "two.txt one.txt" {
		t.Errorf("the files written in the work trees' sub folders: %q", made)
	}
	if w.Exists("sub/one.txt") || w.Exists("sub/two.txt") {
		t.Errorf("a sub-agent wrote into the session's own folder")
	}
}

// A sub-agent with a work tree of its own is told where it is, and its
// write into the person's checkout is refused, even in acceptEdits, with
// the same file in its work tree named; it writes that one, and the
// checkout is left as it was.
func TestSubAgentWorktreeKeepsToItsTree(t *testing.T) {
	w := repoWorld(t)
	same := regexp.MustCompile(`The same file in your work tree is (/[^:"\\]+): change that one`)
	var told, refused bool
	w.Model.Respond(func(n int, r apptest.Request) *apptest.Reply {
		body := string(r.Body)
		if !strings.Contains(body, "You are a sub-agent inside gnosh") {
			if strings.Contains(body, `"role":"tool"`) {
				return &apptest.Reply{Text: "Done."}
			}
			return &apptest.Reply{Calls: []apptest.Call{{ID: "c1", Name: "Agent", Args: `{"description":"note it","prompt":"Write notes.txt.","isolation":"worktree"}`}}}
		}
		told = told || strings.Contains(body, "You work in a git work tree of your own, at ")
		if m := same.FindStringSubmatch(body); m != nil {
			refused = true
			if strings.Count(body, `"role":"tool"`) >= 2 {
				return &apptest.Reply{Text: "Wrote notes.txt in the work tree."}
			}
			return &apptest.Reply{Calls: []apptest.Call{{ID: "w2", Name: "Write", Args: fmt.Sprintf(`{"file_path":%q,"content":"noted\n"}`, m[1])}}}
		}
		return &apptest.Reply{Calls: []apptest.Call{{ID: "w1", Name: "Write", Args: fmt.Sprintf(`{"file_path":%q,"content":"noted\n"}`, filepath.Join(w.Dir, "notes.txt"))}}}
	})
	run(t, w, "--permission-mode", "acceptEdits", "--trust-folder", "-p", "note it").wantCode(t, 0)
	if !told {
		t.Errorf("the sub-agent was not told where its work tree is")
	}
	if !refused {
		t.Errorf("the write into the person's checkout was not refused")
	}
	if w.Exists("notes.txt") {
		t.Errorf("the sub-agent wrote into the person's checkout")
	}
	made, _ := filepath.Glob(filepath.Join(w.Dir, ".gnosh", "worktrees", "agent-*", "notes.txt"))
	if len(made) != 1 {
		t.Errorf("notes.txt in the work tree: %q", made)
	}
}

// A sub-agent's work tree made in a --worktree session's stays out of the
// person's checkout too, not only out of the session's work tree it was
// made from.
func TestSubAgentWorktreeInAWorktreeSession(t *testing.T) {
	w := repoWorld(t)
	w.Model.Respond(func(n int, r apptest.Request) *apptest.Reply {
		body := string(r.Body)
		if !strings.Contains(body, "You are a sub-agent inside gnosh") {
			if strings.Contains(body, `"role":"tool"`) {
				return &apptest.Reply{Text: "Done."}
			}
			return &apptest.Reply{Calls: []apptest.Call{{ID: "c1", Name: "Agent", Args: `{"description":"note it","prompt":"Write notes.txt.","isolation":"worktree"}`}}}
		}
		if strings.Contains(body, `"role":"tool"`) {
			return &apptest.Reply{Text: "Refused."}
		}
		return &apptest.Reply{Calls: []apptest.Call{{ID: "w1", Name: "Write", Args: fmt.Sprintf(`{"file_path":%q,"content":"noted\n"}`, filepath.Join(w.Dir, "notes.txt"))}}}
	})
	// In bypassPermissions, where the work tree is all that stands between
	// the sub-agent and the checkout.
	run(t, w, "--trust-folder", "--worktree", "iso", "-y", "-p", "note it").wantCode(t, 0)
	if w.Exists("notes.txt") {
		t.Errorf("the sub-agent wrote into the person's checkout")
	}
	if !anyRequestHas(w, "so that your changes stay apart from the person's checkout at ") {
		t.Errorf("the write was not refused as one into the person's checkout")
	}
}

// What gnosh keeps in a project's data folder is not a work tree's work:
// the folder of work trees, the local settings file and its lock, the
// .gitignore gnosh writes; a folder given whole counts when all in it is
// gnosh's.
func TestGnoshOwn(t *testing.T) {
	tree := t.TempDir()
	for rel, want := range map[string]bool{
		".gnosh/worktrees/.gitignore":     true,
		".gnosh/worktrees/agent-1/x.go":   false,
		".gnosh/settings.local.json":      true,
		".gnosh/settings.local.json.lock": true,
		".gnosh/.gitignore":               true,
		".gnosh/skills/x/SKILL.md":        false,
		"main.go":                         false,
	} {
		if got := gnoshOwn(tree, rel); got != want {
			t.Errorf("gnoshOwn(%q) = %v", rel, got)
		}
	}
	os.MkdirAll(filepath.Join(tree, ".gnosh", "worktrees"), 0o755)
	os.WriteFile(filepath.Join(tree, ".gnosh", "worktrees", ".gitignore"), []byte("*\n"), 0o644)
	os.WriteFile(filepath.Join(tree, ".gnosh", "settings.local.json"), []byte("{}"), 0o600)
	if !gnoshOwn(tree, ".gnosh/") || !gnoshOwn(tree, ".gnosh/worktrees/") {
		t.Errorf("a data folder of gnosh's own files alone counted as work")
	}
	// A work tree kept in the folder of work trees holds work: the tree it
	// lies in must not go with it (review, 16 November).
	os.MkdirAll(filepath.Join(tree, ".gnosh", "worktrees", "agent-1"), 0o755)
	if gnoshOwn(tree, ".gnosh/worktrees/") || gnoshOwn(tree, ".gnosh/") {
		t.Errorf("a folder of work trees with a kept work tree in it was taken for gnosh's alone")
	}
	os.RemoveAll(filepath.Join(tree, ".gnosh", "worktrees", "agent-1"))
	os.MkdirAll(filepath.Join(tree, ".gnosh", "skills"), 0o755)
	if gnoshOwn(tree, ".gnosh/") {
		t.Errorf("a data folder with a skills folder in it was taken for gnosh's alone")
	}
}

// An unnamed --worktree session that changed nothing itself, but whose
// sub-agent's work tree was kept with work in it, keeps its own tree:
// removing it would take the sub-agent's work along (review, 16 November).
func TestWorktreeFlagKeepsASubAgentsKeptTree(t *testing.T) {
	w := repoWorld(t)
	treeRe := regexp.MustCompile(`You work in a git work tree of your own, at (/[^:]+):`)
	w.Model.Respond(func(n int, r apptest.Request) *apptest.Reply {
		body := string(r.Body)
		if strings.Contains(body, "You are a sub-agent inside gnosh") {
			if strings.Contains(body, `"role":"tool"`) {
				return &apptest.Reply{Text: "Wrote it."}
			}
			m := treeRe.FindStringSubmatch(body)
			if m == nil {
				return &apptest.Reply{Text: "No tree."}
			}
			return &apptest.Reply{Calls: []apptest.Call{{ID: "w1", Name: "Write", Args: fmt.Sprintf(`{"file_path":%q,"content":"kept\n"}`, filepath.Join(m[1], "notes.txt"))}}}
		}
		if strings.Contains(body, `"role":"tool"`) {
			return &apptest.Reply{Text: "Done."}
		}
		return &apptest.Reply{Calls: []apptest.Call{{ID: "c1", Name: "Agent", Args: `{"description":"note it","prompt":"Write notes.txt.","isolation":"worktree"}`}}}
	})
	r := run(t, w, "--trust-folder", "--worktree", "-y", "-p", "go").wantCode(t, 0)
	kept, _ := filepath.Glob(filepath.Join(w.Dir, ".gnosh", "worktrees", "*", ".gnosh", "worktrees", "agent-*", "notes.txt"))
	if len(kept) != 1 {
		t.Errorf("the sub-agent's kept work is gone: %q\n%s", kept, r.err)
	}
	has(t, "stderr", r.err, "is kept (it holds")
}

// An unnamed --worktree session that kept a rule in its local settings
// file, and changed nothing else, leaves its work tree as it was made: the
// tree goes at the end.
func TestWorktreeFlagOwnFilesAreNoWork(t *testing.T) {
	w := repoWorld(t)
	r := run(t, w, "--trust-folder", "--worktree", "-p", "/permissions allow Bash(ls *)").wantCode(t, 0)
	has(t, "stderr", r.err, "was left as it was made, and is removed with its branch")
}

// checkoutOf sees the checkouts a work tree stays out of through symbolic
// links and other spellings, and finds the innermost.
func TestCheckoutOf(t *testing.T) {
	root := resolved(t.TempDir())
	repo := filepath.Join(root, "app")
	outer := filepath.Join(repo, ".gnosh", "worktrees", "s")
	tree := filepath.Join(outer, ".gnosh", "worktrees", "a")
	if err := os.MkdirAll(tree, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(repo, link); err != nil {
		t.Fatal(err)
	}
	wt := core.WorkTree{Path: tree, Repo: outer, Outer: []string{repo}}
	for _, c := range []struct{ path, want string }{
		{filepath.Join(repo, "main.go"), repo},
		{filepath.Join(link, "main.go"), repo},
		{filepath.Join(strings.ToUpper(repo), "main.go"), repo},
		{filepath.Join(outer, "main.go"), outer},
		{filepath.Join(tree, "main.go"), ""},
		{filepath.Join(root, "elsewhere", "x"), ""},
	} {
		if got, _ := checkoutOf(wt, c.path); got != c.want {
			t.Errorf("checkoutOf(%q) = %q, want %q", c.path, got, c.want)
		}
	}
	if got := checkoutName(wt, repo); !strings.HasPrefix(got, "the person's checkout at ") {
		t.Errorf("checkoutName(person's) = %q", got)
	}
	if got := checkoutName(wt, outer); !strings.HasPrefix(got, "the work tree at ") {
		t.Errorf("checkoutName(the session's) = %q", got)
	}
	if got := outsideTree(wt, tree, []string{filepath.Join(link, "x.go")}); !strings.Contains(got, "The same file in your work tree is "+filepath.Join(tree, "x.go")) {
		t.Errorf("outsideTree through a link: %q", got)
	}
}

// A session started in a work tree gnosh made for a sub-agent, and kept,
// works on its files as on any project's: the data folder the work tree
// lies in does not make them gnosh's own.
func TestSessionInAKeptWorktree(t *testing.T) {
	w := repoWorld(t,
		apptest.Reply{Calls: []apptest.Call{{ID: "w", Name: "Write", Args: `{"file_path":"kept.txt","content":"carried on\n"}`}}},
		apptest.Reply{Text: "Done."},
	)
	tree := filepath.Join(w.Dir, ".gnosh", "worktrees", "agent-0123456789ab")
	gitIn(t, w.Dir, "worktree", "add", "-q", "-b", "gnosh-agent-0123456789ab", tree)
	o := testOptions(w, "--permission-mode", "acceptEdits", "-p", "carry on")
	o.Cwd = tree
	runWith(t, t.Context(), o).wantCode(t, 0)
	if got, err := os.ReadFile(filepath.Join(tree, "kept.txt")); err != nil || string(got) != "carried on\n" {
		t.Errorf("the write in the kept work tree: %q, %v", got, err)
	}
}

// addTree makes its work tree's folder itself, so that two starts of one
// name at once make one work tree: the second leaves the first's folder
// and branch alone, where it used to take them away as its own leftovers.
// A failed attempt takes away only the branch it made, not one of that
// name that points elsewhere.
func TestAddTreeLeavesAnothersAlone(t *testing.T) {
	w := repoWorld(t)
	gitIn(t, w.Dir, "commit", "-q", "--allow-empty", "-m", "second")
	first := gitIn(t, w.Dir, "rev-parse", "HEAD~1")
	second := gitIn(t, w.Dir, "rev-parse", "HEAD")
	env := []string{"HOME=" + w.Dir, "PATH=" + os.Getenv("PATH"), "GIT_CONFIG_NOSYSTEM=1"}
	trees := filepath.Join(w.Root, "trees")
	if err := os.MkdirAll(trees, 0o755); err != nil {
		t.Fatal(err)
	}

	// The first start made the folder, and works in it.
	taken := &worktree{repo: w.Dir, path: filepath.Join(trees, "same"), branch: "worktree-same", base: second}
	if err := addTree(t.Context(), env, taken, true); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(taken.path, "work.txt"), []byte("the first start's work\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	again := *taken
	err := addTree(t.Context(), env, &again, true)
	if err == nil || !strings.Contains(err.Error(), "made by another start at the same moment") {
		t.Errorf("a second start of the same name: %v", err)
	}
	if data, err := os.ReadFile(filepath.Join(taken.path, "work.txt")); err != nil || string(data) != "the first start's work\n" {
		t.Errorf("the first start's work tree was touched: %q, %v", data, err)
	}
	if got := gitIn(t, w.Dir, "rev-parse", "--verify", "--quiet", "refs/heads/worktree-same"); got != second {
		t.Errorf("the first start's branch is at %q", got)
	}

	// A branch of the name made since the start looked, at another commit:
	// git refuses -b, and the branch stays.
	gitIn(t, w.Dir, "branch", "worktree-late", first)
	late := &worktree{repo: w.Dir, path: filepath.Join(trees, "late"), branch: "worktree-late", base: second}
	if err := addTree(t.Context(), env, late, true); err == nil {
		t.Fatal("git made a branch that was there")
	}
	if got := gitIn(t, w.Dir, "rev-parse", "--verify", "--quiet", "refs/heads/worktree-late"); got != first {
		t.Errorf("someone else's branch was taken away or moved: %q", got)
	}
	if fileExists(late.path) {
		t.Errorf("the failed attempt left its folder: %s", late.path)
	}
}

// .worktreeinclude: a new work tree gets copies of the files it names that
// git ignores, each in its place: not an ignored file it does not name,
// not one it names that git does not ignore. Untouched, the copies are no
// work of the tree's; changed, one is.
func TestWorktreeInclude(t *testing.T) {
	w := repoWorld(t)
	w.Write(".gitignore", ".env\n*.local\nbuild/\nconf/\n")
	w.Write(".worktreeinclude", "# what a checkout lacks\n.env\n*.local\nnotes.txt\nconf/\n")
	gitIn(t, w.Dir, "add", ".gitignore", ".worktreeinclude")
	gitIn(t, w.Dir, "commit", "-q", "-m", "ignore")
	w.Write(".env", "TOKEN=kz-1\n")
	w.Write("sub/x.local", "local\n")
	w.Write("my sp.local", "spaced\n") // git status quotes such names without -z
	w.Write("caf\u00e9.local", "accent\n")
	w.Write("conf/a.json", "{}\n")
	w.Write("build/out", "built\n") // ignored, not named
	w.Write("notes.txt", "mine\n")  // named, not ignored
	env := []string{"HOME=" + w.Dir, "PATH=" + os.Getenv("PATH"), "GIT_CONFIG_NOSYSTEM=1"}
	trees := filepath.Join(w.Root, "trees")
	if err := os.MkdirAll(trees, 0o755); err != nil {
		t.Fatal(err)
	}
	wt := &worktree{repo: w.Dir, path: filepath.Join(trees, "inc"), branch: "worktree-inc", base: gitIn(t, w.Dir, "rev-parse", "HEAD")}
	if err := addTree(t.Context(), env, wt, true); err != nil {
		t.Fatal(err)
	}
	copied, err := copyIncluded(t.Context(), env, wt)
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(copied)
	if want := []string{".env", "caf\u00e9.local", "conf/a.json", "my sp.local", "sub/x.local"}; !slices.Equal(copied, want) {
		t.Errorf("copied %q, want %q", copied, want)
	}
	if data, err := os.ReadFile(filepath.Join(wt.path, ".env")); err != nil || string(data) != "TOKEN=kz-1\n" {
		t.Errorf("the work tree's .env: %q, %v", data, err)
	}
	for _, rel := range []string{"build/out", "notes.txt"} {
		if fileExists(filepath.Join(wt.path, rel)) {
			t.Errorf("%s was copied", rel)
		}
	}
	if what, err := treeHolds(t.Context(), env, wt); err != nil || len(what) != 0 {
		t.Errorf("a work tree that holds only the copies holds %q, %v", what, err)
	}
	if err := os.WriteFile(filepath.Join(wt.path, "conf", "a.json"), []byte("{\"changed\": true}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if what, err := treeHolds(t.Context(), env, wt); err != nil || len(what) != 1 || !strings.Contains(what[0], "1 file") {
		t.Errorf("a changed copy is no work: %q, %v", what, err)
	}

	// Without the file, nothing is copied.
	if err := os.Remove(filepath.Join(w.Dir, includeFile)); err != nil {
		t.Fatal(err)
	}
	other := &worktree{repo: w.Dir, path: filepath.Join(trees, "plain"), branch: "worktree-plain", base: wt.base}
	if err := addTree(t.Context(), env, other, true); err != nil {
		t.Fatal(err)
	}
	if copied, err := copyIncluded(t.Context(), env, other); err != nil || len(copied) != 0 {
		t.Errorf("with no %s: copied %q, %v", includeFile, copied, err)
	}

	// A place in the work tree that a link of its checkout leads out of
	// gets no copy: the commit has cfg as a link out, the person's folder
	// a folder cfg with a .env in it.
	w.Write(includeFile, ".env\n")
	outside := filepath.Join(w.Root, "outside")
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(w.Dir, "cfg")); err != nil {
		t.Fatal(err)
	}
	gitIn(t, w.Dir, "add", "cfg")
	gitIn(t, w.Dir, "commit", "-q", "-m", "link")
	if err := os.Remove(filepath.Join(w.Dir, "cfg")); err != nil {
		t.Fatal(err)
	}
	w.Write("cfg/.env", "TOKEN=kz-3\n")
	linked := &worktree{repo: w.Dir, path: filepath.Join(trees, "linked"), branch: "worktree-linked", base: gitIn(t, w.Dir, "rev-parse", "HEAD")}
	if err := addTree(t.Context(), env, linked, true); err != nil {
		t.Fatal(err)
	}
	copied, err = copyIncluded(t.Context(), env, linked)
	if err == nil || !strings.Contains(err.Error(), "cfg/.env (its folder in the work tree leads out of it)") || slices.Contains(copied, "cfg/.env") {
		t.Errorf("through a link out: copied %q, %v", copied, err)
	}
	if fileExists(filepath.Join(outside, ".env")) {
		t.Errorf("a copy was written out of the work tree")
	}

	// Deeper down, no folder is made out of the work tree either before
	// the copy is refused.
	w.Write("cfg/deep/er/.env", "TOKEN=kz-4\n")
	deeper := &worktree{repo: w.Dir, path: filepath.Join(trees, "deeper"), branch: "worktree-deeper", base: linked.base}
	if err := addTree(t.Context(), env, deeper, true); err != nil {
		t.Fatal(err)
	}
	copied, err = copyIncluded(t.Context(), env, deeper)
	if err == nil || !strings.Contains(err.Error(), "cfg/deep/er/.env (its folder in the work tree leads out of it)") || slices.Contains(copied, "cfg/deep/er/.env") {
		t.Errorf("through a link out, deeper: copied %q, %v", copied, err)
	}
	if fileExists(filepath.Join(outside, "deep")) {
		t.Errorf("a folder was made out of the work tree")
	}
}
