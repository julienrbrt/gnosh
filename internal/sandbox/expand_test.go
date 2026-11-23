package sandbox

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// A pattern names the paths that match it now: ** for any number of
// folders (none among them), *, ? and [...] within a name; a link to a
// folder is not walked into; a path that is no pattern is itself; a walk
// that runs out of its budget says so.
func TestExpand(t *testing.T) {
	root := t.TempDir()
	for _, f := range []string{".env", "a/.env", "a/b/c/.env", "a/key.pem", "b/x.pem", "conf/one/secret1", "conf/two/secret2", "conf/two/other", "a.txt", "b.txt", "c.txt"} {
		p := filepath.Join(root, f)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// (A link to a folder above: walked into, the walk would not end.)
	if err := os.Symlink(root, filepath.Join(root, "a", "loop")); err != nil {
		t.Fatal(err)
	}
	rel := func(paths []string) string {
		var out []string
		for _, p := range paths {
			r, _ := filepath.Rel(root, p)
			out = append(out, r)
		}
		return strings.Join(out, " ")
	}
	for pattern, want := range map[string]string{
		"**/.env":         ".env a/.env a/b/c/.env",
		"**/*.pem":        "a/key.pem b/x.pem",
		"*/*.pem":         "a/key.pem b/x.pem",
		"conf/*/secret?":  "conf/one/secret1 conf/two/secret2",
		"[ab].txt":        "a.txt b.txt",
		"a/**":            "a",
		"nothing/**/here": "",
	} {
		budget := ExpandBudget
		got, done := Expand(filepath.Join(root, pattern), &budget)
		if rel(got) != want || !done {
			t.Errorf("%s: %q (done %v), want %q", pattern, rel(got), done, want)
		}
	}
	if got, done := Expand("/no/such/path", new(int)); !slices.Equal(got, []string{"/no/such/path"}) || !done {
		t.Errorf("a path that is no pattern: %q %v", got, done)
	}
	budget := 3
	if _, done := Expand(filepath.Join(root, "**/.env"), &budget); done {
		t.Error("a walk past its budget said it was done")
	}
}
