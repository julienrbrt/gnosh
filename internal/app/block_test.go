package app

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/gnosh-ai/gnosh/internal/app/apptest"
	"github.com/gnosh-ai/gnosh/internal/config"
)

// Claude Code's blocks, set in the person's gnosh.json, hold in
// bypassPermissions (-y): the file tools refuse to read or change a file
// outside the project, and to change a settings file in it, and say why,
// while the project's own files are read as before; the work tree tools
// are not offered.
func TestBlocksOutsideTheWorkingDirectories(t *testing.T) {
	w := apptest.NewWorld(t)
	secret := w.Write(w.Home+"/outside/secret.txt", "hidden\n")
	w.Write("a.txt", "mine\n")
	w.Settings(`{"permissions": {"blockReadsOutsideWorkingDirectories": true, "blockWritesOutsideWorkingDirectories": true}}`)
	w.Model.Respond(func(n int, r apptest.Request) *apptest.Reply {
		if n == 0 {
			return &apptest.Reply{Calls: []apptest.Call{
				{ID: "c1", Name: "Read", Args: `{"file_path":"` + secret + `"}`},
				{ID: "c2", Name: "Write", Args: `{"file_path":"` + w.Home + `/outside/new.txt","content":"x\n"}`},
				{ID: "c3", Name: "Read", Args: `{"file_path":"a.txt"}`},
				{ID: "c4", Name: "Write", Args: `{"file_path":"gnosh.json","content":"{}\n"}`},
			}}
		}
		return &apptest.Reply{Text: "Done."}
	})
	run(t, w, "-y", "-p", "look around").wantCode(t, 0)
	has(t, "the read outside", toolResult(t, w, 1, "c1"), "blockReadsOutsideWorkingDirectories", "/add-dir")
	has(t, "the write outside", toolResult(t, w, 1, "c2"), "blockWritesOutsideWorkingDirectories")
	has(t, "the project's file", toolResult(t, w, 1, "c3"), "mine")
	has(t, "the project's settings file", toolResult(t, w, 1, "c4"), "settings file")
	if w.Exists(w.Home+"/outside/new.txt") || w.Exists("gnosh.json") {
		t.Error("a blocked write was made")
	}
	if body := string(w.Model.Requests()[0].Body); strings.Contains(body, `"name":"EnterWorktree"`) {
		t.Error("EnterWorktree is offered under the write block")
	}
	// /permissions says so.
	r := run(t, w, "-p", "/permissions")
	r.wantCode(t, 0)
	has(t, "/permissions", r.out, "The file tools read or change nothing outside these folders, in every mode", "blockReadsOutsideWorkingDirectories")
}

// The project's folders of skills, sub-agents, commands and rules above
// the working directory count as gnosh's own for the read block only in a
// trusted folder, and only where they really are inside the project: a
// repository's .claude/skills that is a link to / lets nothing through
// (the blocks review of 2026-11-20).
func TestOwnReadsLeaveOutLinksOutOfTheProject(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(root, "sub")
	for _, d := range []string{filepath.Join(root, ".gnosh", "skills"), filepath.Join(root, ".claude"), sub} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink("/", filepath.Join(root, ".claude", "skills")); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{Trusted: true, Paths: config.Paths{Home: t.TempDir(), Cwd: sub, ProjectRoot: root, ConfigDir: t.TempDir()}}
	got := ownReads(cfg)
	if !slices.Contains(got, filepath.Join(root, ".gnosh", "skills")) {
		t.Errorf("the project's own skills folder above the working directory is left out: %v", got)
	}
	if slices.Contains(got, filepath.Join(root, ".claude", "skills")) || slices.Contains(got, "/") {
		t.Errorf("a link out of the project counts as gnosh's own: %v", got)
	}
	cfg.Trusted = false
	if slices.Contains(ownReads(cfg), filepath.Join(root, ".gnosh", "skills")) {
		t.Error("an untrusted project's folders count as gnosh's own")
	}
}
