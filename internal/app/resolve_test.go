package app

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/gnosh-ai/gnosh/internal/app/apptest"
)

// A link that leads nowhere is read from the folder it is in as the
// system reads it, past the links on the way to that folder: here d leads
// to s, so d/dang's "../.." starts at s, not at the text's a/b/d (the
// security review of 2026-11-18 found the text's reading).
func TestResolveADanglingLinkThroughALinkedFolder(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{"proj/a/b", "proj/s", "outside"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink("../../s", filepath.Join(root, "proj/a/b/d")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../../outside/x", filepath.Join(root, "proj/s/dang")); err != nil {
		t.Fatal(err)
	}
	got, err := resolvePath(filepath.Join(root, "proj/a/b/d/dang"))
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(root, "outside/x"); got != want {
		t.Errorf("resolved to %s, want %s", got, want)
	}
}

// A link's target is walked piece by piece from where the link is, as the
// system walks it: "a/../hooks/pre-commit", with a a link to .git/info,
// lands in .git/hooks, not beside the link (the review of 2026-11-20: a
// repository could have cp write a git hook so, unasked in acceptEdits).
func TestResolveALinkTargetThatClimbsThroughALink(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{"proj/notes", "proj/.git/info", "proj/.git/hooks"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink("../.git/info", filepath.Join(root, "proj/notes/a")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("a/../hooks/pre-commit", filepath.Join(root, "proj/notes/x.md")); err != nil {
		t.Fatal(err)
	}
	got, err := resolvePath(filepath.Join(root, "proj/notes/x.md"))
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(root, "proj/.git/hooks/pre-commit"); got != want {
		t.Errorf("resolved to %s, want %s", got, want)
	}
}

// So a cp onto such a link is a write into .git, which acceptEdits does
// not let run unasked: with nobody to ask, it does not run.
func TestCpThroughAClimbingLinkAsks(t *testing.T) {
	w := apptest.NewWorld(t,
		apptest.Reply{Calls: []apptest.Call{{ID: "c1", Name: "Bash", Args: `{"command":"cp tools/payload notes/x.md"}`}}},
		apptest.Reply{Text: "Done."})
	w.Write("tools/payload", "#!/bin/sh\necho planted\n")
	w.Write(".git/info/exclude", "")
	if err := os.MkdirAll(w.Path(".git/hooks"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(w.Path("notes"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../.git/info", w.Path("notes/a")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("a/../hooks/pre-commit", w.Path("notes/x.md")); err != nil {
		t.Fatal(err)
	}
	run(t, w, "--trust-folder", "--permission-mode", "acceptEdits", "-p", "copy it").wantCode(t, 0)
	if w.Exists(".git/hooks/pre-commit") {
		t.Errorf("cp wrote a git hook through the link, unasked")
	}
	has(t, "what the model was told", toolResult(t, w, 1, "c1"), ".git")
	w.Model.Done()
}
