package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The repository of a folder is found as git finds it: the nearest .git
// going up, a folder or a file that names the git directory.
func TestRepoGitDir(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	mk := func(p string) {
		if err := os.MkdirAll(filepath.Join(root, p), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	mk("proj/.git/worktrees/dev")
	mk("proj/sub/deep")
	mk("proj/vendor/x/.git")
	mk("proj/tree")
	mk("plain")
	if err := os.WriteFile(filepath.Join(root, "proj/tree/.git"), []byte("gitdir: ../.git/worktrees/dev\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for dir, want := range map[string]string{
		"proj":          "proj/.git",
		"proj/sub/deep": "proj/.git",
		"proj/vendor/x": "proj/vendor/x/.git",
		"proj/tree":     "proj/.git/worktrees/dev",
	} {
		if got := repoGitDir(filepath.Join(root, dir)); got != filepath.Join(root, want) {
			t.Errorf("repoGitDir(%s) = %q, want %q", dir, got, filepath.Join(root, want))
		}
	}
	// A folder in no repository has none (or the one the temporary
	// folder itself may be in, above it).
	if got := repoGitDir(filepath.Join(root, "plain")); got != "" && strings.HasPrefix(got, root) {
		t.Errorf("repoGitDir(plain) = %q", got)
	}
}
