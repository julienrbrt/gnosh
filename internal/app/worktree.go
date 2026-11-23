package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/gnosh-ai/gnosh/internal/agent"
)

// This file is a sub-agent's own git work tree (isolation: worktree,
// spec/cc/subagents.md 2, tools.md 9). The sub-agent works in a work tree
// made for it, on a branch of its own that starts at the commit the
// repository has checked out, so that what it changes stays out of the
// folder the person works in until they take it. A work tree it changed
// nothing in is removed when it ends, branch and all; one it changed is
// kept, and its report says where it is and on which branch.
//
// The work trees lie in the project's data folder (.gnosh/worktrees), with
// a .gitignore of their own that keeps them out of the repository's
// status; the permission rules judge what is inside one as the project's
// own files (permission.Options.WorkTrees). Making one runs git, which
// gnosh does only in a folder the person has trusted, as /diff does: a
// repository's own settings can name programs git runs.
//
// It starts at HEAD, not at the default branch as Claude Code's does: the
// sub-agent is asked about the code as the person has it, and that is the
// commit they have checked out. What they have not committed is not in it.

// worktreesDir is the folder of work trees in the project's data folder.
const worktreesDir = "worktrees"

// worktree is the work tree of one sub-agent, or of a --worktree session
// (sessionTree, sessiontree.go).
type worktree struct {
	repo   string // the repository's top folder
	path   string // the work tree's folder
	branch string // its branch
	base   string // the commit it started at
}

// gitEnvDrop are the variables that would point git at another repository
// than the one it is run in.
var gitEnvDrop = []string{"GIT_DIR=", "GIT_WORK_TREE=", "GIT_INDEX_FILE=", "GIT_COMMON_DIR=", "GIT_OBJECT_DIRECTORY="}

// git runs git in dir and returns what it printed, without the programs a
// repository's settings could start from the file-system monitor, and
// without the variables that would point it elsewhere. An error says what
// git said.
func (l *live) git(ctx context.Context, dir string, args ...string) (string, error) {
	return runGit(ctx, l.p.childEnviron(), dir, args...)
}

// runGit is live.git for code that has no session yet: git in dir, in
// the environment environ, as live.git runs it.
func runGit(ctx context.Context, environ []string, dir string, args ...string) (string, error) {
	out, err := gitWith(ctx, environ, dir, "", args...)
	return strings.TrimSpace(out), err
}

// gitWith is runGit with stdin as git's input, and what git printed as it
// printed it. An exit with nothing on stderr is git's *exec.ExitError.
func gitWith(ctx context.Context, environ []string, dir, stdin string, args ...string) (string, error) {
	base := []string{"--no-pager", "-c", "core.fsmonitor=false", "-C", dir}
	cmd := exec.CommandContext(ctx, "git", append(base, args...)...)
	env := slices.DeleteFunc(slices.Clone(environ), func(kv string) bool {
		return slices.ContainsFunc(gitEnvDrop, func(p string) bool { return strings.HasPrefix(kv, p) })
	})
	cmd.Env = append(env, "GIT_PAGER=cat", "PAGER=cat", "GIT_TERMINAL_PROMPT=0")
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	// A hook git runs (post-checkout, on worktree add) may leave a program
	// behind that holds git's output open: once git itself has ended, or
	// been stopped, what it printed is not waited for longer than this.
	cmd.WaitDelay = 5 * time.Second
	out, err := cmd.Output()
	if errors.Is(err, exec.ErrWaitDelay) && cmd.ProcessState != nil && cmd.ProcessState.Success() {
		// git itself ended well; what a hook left behind holding its
		// output does not undo that.
		err = nil
	}
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return "", fmt.Errorf("git did not finish in the time it is given (git %s)", strings.Join(args, " "))
		}
		var ee *exec.ExitError
		if errors.As(err, &ee) && len(strings.TrimSpace(string(ee.Stderr))) > 0 {
			return "", errors.New(oneLine(strings.TrimSpace(string(ee.Stderr)), 300))
		}
		return "", err
	}
	return string(out), nil
}

// treeName is the name a sub-agent's work tree and branch take: the random
// end of its id, which no other sub-agent shares (its start is a clock).
func treeName(id string) string {
	hex := strings.ReplaceAll(id, "-", "")
	if len(hex) > 12 {
		hex = hex[len(hex)-12:]
	}
	return "agent-" + hex
}

// makeWorktree makes the work tree of the sub-agent id, in the repository
// the folder cwd is in, and returns it with the folder the sub-agent
// starts in: the one in the work tree that stands where cwd stands in the
// repository. The work tree is a working directory of the session from
// then on, as /add-dir makes one. Its error is worded for the model; what
// a failed attempt left behind is taken away.
func (l *live) makeWorktree(ctx context.Context, cwd, id string) (*worktree, string, error) {
	if !l.st.trusted {
		return nil, "", errors.New("a work tree of its own (isolation worktree) is made with git, and this folder is not trusted: a repository's own settings can name programs git runs. The user can trust it with gnosh trust")
	}
	if _, err := exec.LookPath("git"); err != nil {
		return nil, "", errors.New("a work tree of its own needs git, and there is none on this machine's PATH")
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	top, err := l.git(ctx, cwd, "rev-parse", "--show-toplevel")
	if err != nil {
		return nil, "", fmt.Errorf("a work tree of its own needs a git repository, and %s is not in one", cwd)
	}
	base, err := l.git(ctx, top, "rev-parse", "--verify", "--quiet", "HEAD")
	if err != nil || base == "" {
		return nil, "", errors.New("the repository has no commit yet, so there is nothing to make a work tree of")
	}
	root, err := treesFolder(l.cfg.Paths.DataDir)
	if err != nil {
		return nil, "", err
	}
	name := treeName(id)
	wt := &worktree{repo: top, path: filepath.Join(root, name), branch: "gnosh-" + name, base: base}
	if _, err := l.git(ctx, top, "rev-parse", "--verify", "--quiet", "refs/heads/"+wt.branch); err == nil || fileExists(wt.path) {
		return nil, "", fmt.Errorf("the branch %s or the folder %s is there already, so no work tree was made", wt.branch, wt.path)
	}
	if err := addTree(ctx, l.p.childEnviron(), wt, true); err != nil {
		return nil, "", err
	}
	copied, err := copyIncluded(ctx, l.p.childEnviron(), wt)
	if len(copied) > 0 {
		l.flog.logf("turn", "the work tree %s has copies of %s, as %s names them", wt.path, fewOf(copied, 20), includeFile)
	}
	if err != nil {
		l.notice(agent.LevelWarning, "worktree", err.Error())
	}
	if err := l.engine.AddDir(wt.path); err != nil {
		l.flog.logf("turn", "the work tree %s could not be made a working directory: %v", wt.path, err)
	}
	start := wt.path
	if real, err := filepath.EvalSymlinks(cwd); err == nil {
		if rel, err := filepath.Rel(top, real); err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, "../") {
			if st, err := os.Stat(filepath.Join(wt.path, rel)); err == nil && st.IsDir() {
				start = filepath.Join(wt.path, rel)
			}
		}
	}
	l.flog.logf("turn", "sub-agent %s works in the work tree %s on the branch %s, from %s", shortID(id), wt.path, wt.branch, shortCommit(base))
	return wt, start, nil
}

// endWorktree ends a sub-agent's work tree once it has ended. A work tree
// the sub-agent left as it found it (nothing changed, nothing new, even
// what git ignores; its branch and its HEAD still at the commit it started
// at) is removed, branch and all, and nothing that could hold its work is
// forced away. Any other is kept, and the line for the report says where
// it is, what it holds and how to take it away. kept says it was kept.
func (l *live) endWorktree(wt *worktree) (note string, kept bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	keep := func(why string) (string, bool) {
		return fmt.Sprintf("[It worked in its own git work tree, %s, on the branch %s, which started at %s: %s. None of it is in the folder you work in. Look at it there, and merge the branch or copy what you want; then git -C %s worktree remove --force %s and git -C %s branch -D %s take it away.]",
			wt.path, wt.branch, shortCommit(wt.base), why, wt.repo, wt.path, wt.repo, wt.branch), true
	}
	what, err := treeHolds(ctx, l.p.childEnviron(), wt)
	if err != nil {
		return keep(err.Error() + ", so the work tree is kept")
	}
	if len(what) == 0 {
		if _, err := l.git(ctx, wt.repo, "worktree", "remove", wt.path); err != nil {
			return fmt.Sprintf("[It worked in its own git work tree, %s, and changed nothing there; the work tree could not be removed (%v).]", wt.path, err), true
		}
		if _, err := l.git(ctx, wt.repo, "branch", "-d", wt.branch); err != nil {
			return fmt.Sprintf("[It worked in its own git work tree and changed nothing there; the work tree is removed, and its branch %s is left (%v).]", wt.branch, err), false
		}
		return "[It worked in its own git work tree and changed nothing there; the work tree is removed.]", false
	}
	return keep("it holds " + strings.Join(what, " and "))
}

// treesFolder makes the folder of work trees in the data folder dataDir,
// with a .gitignore that keeps them out of the repository's status, and
// returns it. It refuses a folder that is a link: that would put the work
// trees, and what the permission rules let be changed in them, somewhere
// else.
func treesFolder(dataDir string) (string, error) {
	root := filepath.Join(dataDir, worktreesDir)
	if st, err := os.Lstat(root); err == nil && st.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("%s is a link, and gnosh makes work trees only in a folder of its own there", root)
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return "", fmt.Errorf("the folder for work trees could not be made: %v", err)
	}
	if ignore := filepath.Join(root, ".gitignore"); !fileExists(ignore) {
		_ = os.WriteFile(ignore, []byte("*\n"), 0o644)
	}
	return root, nil
}

// addTree adds the work tree wt to its repository: on a new branch at its
// base when newBranch is set, else on its branch as it stands. What a
// failed attempt left behind (a branch git made, part of the folder) is
// taken away, and only that.
//
// The folder is made first, by this gnosh alone (os.Mkdir fails when it is
// there), and git makes the work tree in it, which git does in an empty
// folder. So two starts of one name at once (gnosh --worktree same, twice)
// make one work tree, and the one that comes second leaves the other's
// alone: it used to run git into the same folder, fail, and take away, as
// its own leftovers, the folder and the branch the first had just made.
func addTree(ctx context.Context, environ []string, wt *worktree, newBranch bool) error {
	if err := os.Mkdir(wt.path, 0o755); err != nil {
		if errors.Is(err, os.ErrExist) {
			return fmt.Errorf("%s was made by another start at the same moment: start again to open it, or name another", wt.path)
		}
		return fmt.Errorf("the folder for the work tree could not be made: %v", err)
	}
	args := []string{"worktree", "add", "--quiet", wt.path, wt.branch}
	if newBranch {
		args = []string{"worktree", "add", "--quiet", "-b", wt.branch, wt.path, wt.base}
	}
	if _, err := runGit(ctx, environ, wt.repo, args...); err != nil {
		clean, done := context.WithTimeout(context.Background(), time.Minute)
		defer done()
		_, _ = runGit(clean, environ, wt.repo, "worktree", "remove", "--force", wt.path)
		_ = os.RemoveAll(wt.path)
		_, _ = runGit(clean, environ, wt.repo, "worktree", "prune")
		if newBranch {
			// The branch this attempt made, which points at the base; one
			// of that name that points elsewhere is someone else's (made
			// since the start looked), and stays.
			_, _ = runGit(clean, environ, wt.repo, "update-ref", "-d", "refs/heads/"+wt.branch, wt.base)
		}
		return fmt.Errorf("git could not make the work tree: %v", err)
	}
	return nil
}

// treeHolds says what the work tree wt holds that its removal would take
// away: files changed or made and not committed (git's ignored ones
// among them), commits on its branch past where it started, a HEAD moved
// elsewhere. Nothing means it is as it was made. An error says what could
// not be told.
func treeHolds(ctx context.Context, environ []string, wt *worktree) ([]string, error) {
	status, err := gitWith(ctx, environ, wt.path, "", "status", "--porcelain", "-z", "--ignored", "--untracked-files=all")
	if err != nil {
		return nil, fmt.Errorf("whether anything was changed there could not be told (%v)", err)
	}
	head, err1 := runGit(ctx, environ, wt.path, "rev-parse", "HEAD")
	commits, err2 := runGit(ctx, environ, wt.repo, "rev-list", "--count", wt.base+"..refs/heads/"+wt.branch)
	n, err3 := strconv.Atoi(commits)
	if err1 != nil || err2 != nil || err3 != nil {
		return nil, errors.New("how far its branch went could not be told")
	}
	var what []string
	// Each entry is "XY path", whatever the path holds (-z); a renamed or
	// copied file's is followed by the name it had. An ignored file that is
	// a copy of the repository's, as .worktreeinclude's copies are while
	// untouched, is no work; those are looked at only when nothing else is
	// work, and only until one is not such a copy.
	lines := 0
	var ignored []string
	entries := strings.Split(status, "\x00")
	for i := 0; i < len(entries); i++ {
		e := entries[i]
		if len(e) < 4 || e[2] != ' ' {
			continue
		}
		xy, path := e[:2], e[3:]
		if xy[0] == 'R' || xy[0] == 'C' {
			i++
		}
		switch {
		case gnoshOwn(wt.path, path):
		case xy == "!!":
			ignored = append(ignored, path)
		default:
			lines++
		}
	}
	if lines > 0 {
		what = append(what, countWords(lines, "file changed or made and not committed", "files changed or made and not committed"))
	} else {
		for _, path := range ignored {
			if !copiedAsIs(ctx, wt, path) {
				what = append(what, "at least 1 file git ignores, changed or made there")
				break
			}
		}
	}
	if n > 0 {
		what = append(what, countWords(n, "commit", "commits"))
	}
	if head != wt.base && n == 0 {
		what = append(what, "a HEAD that is no longer the commit it started at")
	}
	return what, nil
}

// gnoshOwn reports whether rel, a path git status gives in the work tree
// at tree, is only what gnosh itself keeps in a project's data folder
// (.gnosh): the folder of work trees, the local settings file and its
// lock, the .gitignore gnosh writes there. A --worktree session's data
// folder is inside its work tree, and those are not its work. A folder
// git gives whole counts when all that is in it is gnosh's.
func gnoshOwn(tree, rel string) bool {
	rel = strings.TrimSuffix(rel, "/")
	own := func(rel string) bool {
		switch rel {
		case ownDir + "/.gitignore", ownDir + "/settings.local.json", ownDir + "/settings.local.json.lock", ownDir + "/" + worktreesDir + "/.gitignore":
			return true
		case ownDir + "/" + worktreesDir:
			// The folder of work trees is gnosh's only while no work tree is
			// in it: one kept there (a sub-agent's, with its work) would go
			// with the tree it lies in, and git's own check passes over it,
			// since the folder is ignored.
			entries, err := os.ReadDir(filepath.Join(tree, rel))
			if err != nil {
				return false
			}
			for _, e := range entries {
				if e.Name() != ".gitignore" {
					return false
				}
			}
			return true
		}
		return false
	}
	if rel != ownDir {
		return own(rel)
	}
	entries, err := os.ReadDir(filepath.Join(tree, ownDir))
	if err != nil {
		return false
	}
	for _, e := range entries {
		if !own(ownDir + "/" + e.Name()) {
			return false
		}
	}
	return true
}

// fileExists reports whether there is a file or folder at p.
func fileExists(p string) bool {
	_, err := os.Lstat(p)
	return err == nil
}

// shortCommit is a commit's id as a person reads it.
func shortCommit(id string) string {
	if len(id) > 10 {
		return id[:10]
	}
	return id
}

// outerTrees is the folder of work trees a session's project lies in,
// when the project is one of the git work trees gnosh makes, kept after
// its sub-agent ended: <repo>/<data>/worktrees/<name>, whose .git is the
// file a work tree has. Its files are a checkout of the project like any
// other, and the data folder above them must not make them gnosh's own.
// "" otherwise. data is the data folder's name.
func outerTrees(root, data string) string {
	trees := filepath.Dir(root)
	if filepath.Base(trees) != worktreesDir || filepath.Base(filepath.Dir(trees)) != data {
		return ""
	}
	if info, err := os.Lstat(filepath.Join(root, ".git")); err != nil || !info.Mode().IsRegular() {
		return ""
	}
	return trees
}
