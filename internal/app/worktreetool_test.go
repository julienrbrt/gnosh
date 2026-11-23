package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/gnosh-ai/gnosh/internal/app/apptest"
	"github.com/gnosh-ai/gnosh/internal/core"
	"github.com/gnosh-ai/gnosh/internal/transcript"
	"github.com/gnosh-ai/gnosh/internal/trust"
)

// EnterWorktree, asked for, makes a git work tree of the repository's and
// moves the session into it: the turn ends, the session's file goes with
// the tree's sessions, and gnosh starts again with --worktree and the
// session, told what moved. That gnosh works in the tree and goes on by
// itself, and its ExitWorktree takes the session back, removing the tree
// it left as made; the gnosh that starts then goes on in the checkout.
func TestEnterAndExitWorktree(t *testing.T) {
	w := repoWorld(t,
		apptest.Reply{Calls: []apptest.Call{{ID: "c1", Name: "EnterWorktree", Args: `{"name":"feat"}`}}},
		apptest.Reply{Text: "In the tree now."},
		apptest.Reply{Calls: []apptest.Call{{ID: "c2", Name: "ExitWorktree", Args: `{"action":"remove"}`}}},
		apptest.Reply{Text: "Back in the checkout."},
	)
	var started, envs [][]string
	relaunch := func(o *Options) {
		o.Relaunch = func(args, env []string) error {
			started, envs = append(started, args), append(envs, env)
			return os.ErrPermission
		}
	}
	s := sitWith(t, w, relaunch, "--trust-folder")
	s.see("Type a prompt and Enter.")
	s.typeLine("work in a worktree")
	s.see("The session moves into the git work tree")
	if code := s.exit(); code != 1 {
		t.Errorf("exit code %d, want 1 for a start that failed", code)
	}
	tree := filepath.Join(w.Dir, ".gnosh", "worktrees", "feat")
	if real, err := filepath.EvalSymlinks(tree); err != nil || !fileExists(filepath.Join(real, "main.go")) {
		t.Fatalf("no work tree at %s (%v)", tree, err)
	}
	if len(started) != 1 {
		t.Fatalf("gnosh started again %d times", len(started))
	}
	id := strings.TrimSuffix(filepath.Base(w.Sessions()[0]), ".jsonl")
	if !slices.Contains(started[0], "--worktree=feat") || !slices.Contains(started[0], "--resume="+id) || !slices.Contains(started[0], "--trust-folder") {
		t.Errorf("gnosh started again with %q, want --worktree=feat and --resume=%s among them", started[0], id)
	}
	st := &transcript.Store{Dir: w.DataDir()}
	if list, _ := st.List(context.Background(), tree); len(list) != 1 {
		t.Errorf("the tree lists %d sessions, want the one that moved", len(list))
	}
	if !slices.ContainsFunc(envs[0], func(kv string) bool {
		return strings.HasPrefix(kv, TreeMoveEnv+"=") && strings.Contains(kv, `"entered"`)
	}) {
		t.Errorf("the gnosh EnterWorktree starts is not told what moved: %q", envs[0])
	}

	// That gnosh works in the tree and goes on by itself; asked, it takes
	// the session back out.
	s = sitWith(t, w, func(o *Options) {
		relaunch(o)
		o.Environ = append(slices.Clone(o.Environ), envs[0][len(envs[0])-2:]...)
	}, started[0]...)
	s.see("In the tree now.")
	s.see("-- done in ")
	// A /restart there carries the tree over, quietly: nothing moved.
	s.typeLine("/restart")
	s.see("gnosh starts again, in this session.")
	s.exit()
	if len(started) != 2 || !slices.Contains(started[1], "--worktree=feat") ||
		!slices.ContainsFunc(envs[1], func(kv string) bool {
			return strings.HasPrefix(kv, TreeMoveEnv+"=") && strings.Contains(kv, `"carried":true`)
		}) {
		t.Fatalf("/restart in the tree started gnosh again with %q, %q", started[1:], envs[1:])
	}
	asked := len(w.Model.Requests())
	s = sitWith(t, w, func(o *Options) {
		relaunch(o)
		o.Environ = append(slices.Clone(o.Environ), envs[1][len(envs[1])-2:]...)
	}, started[1]...)
	s.see("Type a prompt and Enter.")
	s.typeLine("leave it")
	s.see("The session goes back to")
	s.exit()
	reqs := w.Model.Requests()
	has(t, "the request after the move", string(reqs[1].Body), "The session works in the git work tree", "on the branch worktree-feat", "Go on with the task")
	if len(reqs) != asked+1 || strings.Contains(string(reqs[asked].Body), "Go on with the task where you left off.\"}],\"role\":\"user\"}]") {
		t.Errorf("after /restart the model was asked %d times, want once, for the prompt typed", len(reqs)-asked)
	}
	if fileExists(tree) {
		t.Errorf("the tree left as it was made is still there")
	}
	if len(started) != 3 || slices.ContainsFunc(started[2], func(a string) bool { return strings.HasPrefix(a, "--worktree") }) {
		t.Fatalf("after ExitWorktree, gnosh started again with %q", started)
	}

	// The gnosh that starts then goes on in the checkout.
	s = sitWith(t, w, func(o *Options) {
		o.Environ = append(slices.Clone(o.Environ), envs[2][len(envs[2])-2:]...)
	}, started[2]...)
	s.see("Back in the checkout.")
	s.typeLine("/exit")
	s.exit()
	reqs = w.Model.Requests()
	has(t, "the request after going back", string(reqs[len(reqs)-1].Body), "The session works in "+w.Dir+" again", "was removed with its branch worktree-feat")
	if list, _ := st.List(context.Background(), w.Dir); len(list) != 1 || list[0].ID != id {
		t.Errorf("after going back, the checkout lists %+v", list)
	}
}

// Neither tool is offered where it cannot work: in a run, in a folder not
// trusted, out of a git repository, or with nobody to start gnosh again;
// and ExitWorktree only in a tree EnterWorktree moved the session into.
func TestWorktreeToolsOffered(t *testing.T) {
	w := repoWorld(t, apptest.Reply{Text: "ok"}, apptest.Reply{Text: "ok"}, apptest.Reply{Text: "ok"})
	offered := func(i int) string { return string(w.Model.Requests()[i].Body) }
	run(t, w, "--trust-folder", "-p", "hi").wantCode(t, 0)
	if strings.Contains(offered(0), `"EnterWorktree"`) {
		t.Errorf("a run was offered EnterWorktree")
	}
	s := sitWith(t, w, func(o *Options) { o.Relaunch = func(args, env []string) error { return nil } }, "--trust-folder")
	s.see("Type a prompt and Enter.")
	s.typeLine("hi")
	s.see("ok")
	s.typeLine("/exit")
	s.exit()
	if body := offered(1); !strings.Contains(body, `"EnterWorktree"`) || strings.Contains(body, `"ExitWorktree"`) {
		t.Errorf("an interactive session in a trusted repository: EnterWorktree %v, ExitWorktree %v", strings.Contains(body, `"EnterWorktree"`), strings.Contains(body, `"ExitWorktree"`))
	}
	s = sit(t, w, "--trust-folder")
	s.see("Type a prompt and Enter.")
	s.typeLine("hi")
	s.see("ok")
	s.typeLine("/exit")
	s.exit()
	if strings.Contains(offered(2), `"EnterWorktree"`) {
		t.Errorf("a gnosh that cannot start itself again was offered EnterWorktree")
	}
}

// A work tree opened again (by its name, or by a new start after
// /restart) counts what was committed in it before as its work: removing
// it would lose that, so it is not taken for one left as made.
func TestReopenedTreeKeepsItsCommits(t *testing.T) {
	w := repoWorld(t)
	env := []string{"HOME=" + w.Dir, "PATH=" + os.Getenv("PATH"), "GIT_CONFIG_NOSYSTEM=1"}
	folder, err := trust.Resolve(w.Dir, w.Home)
	if err != nil {
		t.Fatal(err)
	}
	tree, _, err := makeTree(t.Context(), env, folder, filepath.Join(w.Dir, ".gnosh"), "feat", true)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tree.path, "work.txt"), []byte("done\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, tree.path, "add", "work.txt")
	gitIn(t, tree.path, "-c", "user.name=t", "-c", "user.email=t@example.com", "commit", "-q", "-m", "work")
	again, _, err := makeTree(t.Context(), env, folder, filepath.Join(w.Dir, ".gnosh"), "feat", true)
	if err != nil {
		t.Fatal(err)
	}
	what, err := treeHolds(t.Context(), env, &again.worktree)
	if err != nil || len(what) == 0 || !strings.Contains(strings.Join(what, " "), "1 commit") {
		t.Errorf("the tree opened again holds %q, %v; want its commit counted", what, err)
	}
}

// ExitWorktree that would remove a tree with the work in it
// (discard_changes) is put to the person first, whatever the mode: the
// work is lost. Removing a tree that holds none, and keeping one, are not.
func TestExitWorktreeDiscardAsks(t *testing.T) {
	tool := &exitTreeTool{}
	for in, want := range map[string]bool{
		`{"action":"remove","discard_changes":true}`: true,
		`{"action":"remove"}`:                        false,
		`{"action":"keep","discard_changes":true}`:   false,
	} {
		a, err := tool.Describe(core.ToolCall{Name: "ExitWorktree", Input: json.RawMessage(in)}, nil)
		if err != nil || a.Confirm != want {
			t.Errorf("ExitWorktree %s: Confirm %v (%v), want %v", in, a.Confirm, err, want)
		}
	}
}
