package app

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// A session keeps the copies of its checkpointPrompts most recent prompts:
// older ones are let go, noted as such, and still noted when the session
// is opened again, so that a rewind of the files to before one of them is
// refused instead of putting back too little.
func TestCheckpointsLetGoOfOldPrompts(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "work", "f.txt")
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	clock := time.Date(2026, 11, 18, 0, 0, 0, 0, time.UTC)
	now := func() time.Time { clock = clock.Add(time.Second); return clock }
	store := filepath.Join(dir, "checkpoints")
	c := openCheckpoints(store, now)
	prompt := func(i int) string { return fmt.Sprintf("prompt-%03d", i) }
	for i := 1; i <= checkpointPrompts+2; i++ {
		if err := os.WriteFile(file, []byte(fmt.Sprintf("v%d\n", i)), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := c.keep(prompt(i), file); err != nil {
			t.Fatalf("keep %d: %v", i, err)
		}
	}
	if c.gone([]string{prompt(1)}) == nil || c.gone([]string{prompt(2)}) == nil || c.gone([]string{prompt(3), prompt(checkpointPrompts + 2)}) != nil {
		t.Errorf("let go of: 1 %v, 2 %v, 3 %v", c.gone([]string{prompt(1)}), c.gone([]string{prompt(2)}), c.gone([]string{prompt(3)}))
	}
	blobs, _ := filepath.Glob(filepath.Join(store, "*-*"))
	if len(blobs) != checkpointPrompts {
		t.Errorf("%d copies are kept, want %d", len(blobs), checkpointPrompts)
	}
	// Opened again, the store still knows what it let go, and what it kept.
	again := openCheckpoints(store, now)
	if again.gone([]string{prompt(2)}) == nil || again.gone([]string{prompt(3)}) != nil {
		t.Error("a store opened again forgot which prompts' copies were let go")
	}
	var order []string
	for i := 1; i <= checkpointPrompts+2; i++ {
		order = append(order, prompt(i))
	}
	cps := again.since(order, 2)
	if len(cps) != 1 {
		t.Fatalf("a kept prompt's copy is not found after the store is opened again (%d)", len(cps))
	}
	// (No prompt was looked at as it went out, begin: the oldest copy
	// from the prompt on is what there is.)
	if b, _ := os.ReadFile(filepath.Join(cps[0].dir, cps[0].Blob)); string(b) != "v3\n" {
		t.Errorf("before the oldest prompt kept, the file held %q, want its own copy", b)
	}
}

// A file with another name (a hard link, as pnpm makes them) is left as
// it is: putting it back would cut it off from its other name.
func TestRestoreLeavesHardLinkedFiles(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("hard links are told apart on unix")
	}
	dir := t.TempDir()
	file := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(file, []byte("before\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	c := openCheckpoints(filepath.Join(dir, "checkpoints"), time.Now)
	if err := c.keep("p1", file); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte("after\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(file, filepath.Join(dir, "other-name.txt")); err != nil {
		t.Skip("no hard links here: ", err)
	}
	done, failed := c.restore(c.since([]string{"p1"}, 0), restoreHow{})
	if len(done) != 0 || len(failed) != 1 || !strings.Contains(failed[0], "hard-linked") {
		t.Errorf("done %q, failed %q", done, failed)
	}
	if b, _ := os.ReadFile(file); string(b) != "after\n" {
		t.Errorf("the hard-linked file holds %q", b)
	}
}

// A copy that cannot be made is noted: a later change of the same file
// under the same prompt is not taken for what it held before the first,
// and a rewind says the file cannot be put back.
func TestFailedCopyIsNoted(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(file, []byte("v0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// The store's folder cannot be made: a file stands where it would.
	store := filepath.Join(dir, "checkpoints")
	if err := os.WriteFile(store, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	c := openCheckpoints(store, time.Now)
	if err := c.keep("p1", file); err == nil {
		t.Fatal("a copy that could not be written was taken for made")
	}
	if err := os.WriteFile(file, []byte("v1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_ = c.keep("p1", file)
	_, failed := c.restore(c.since([]string{"p1"}, 0), restoreHow{})
	if len(failed) != 1 || !strings.Contains(failed[0], "could not be kept") {
		t.Errorf("failed %q", failed)
	}
	if b, _ := os.ReadFile(file); string(b) != "v1\n" {
		t.Errorf("the file holds %q", b)
	}
}

// realTemp is a test's own folder, written with every link on the way
// followed, as the file tools give the paths they keep (macOS's /var is
// a link).
func realTemp(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

// writeTo writes a test's file.
func writeTo(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// keepAll keeps the files under the prompt.
func keepAll(t *testing.T, c *checkpoints, prompt string, paths ...string) {
	t.Helper()
	for _, p := range paths {
		if err := c.keep(prompt, p); err != nil {
			t.Fatalf("keep %s: %v", p, err)
		}
	}
}

// A file is put back, or taken away, only where it was: the folders on
// the way to it must be what they were. One removed and made a link in
// its place (to ~/.ssh, say) would have a rewind write the person's
// authorized_keys there, or take away a file of theirs, and say it put
// back a file of the project (the rewind review of 2026-11-19).
func TestRestoreLeavesAFolderThatIsALinkNow(t *testing.T) {
	dir := realTemp(t)
	fx := filepath.Join(dir, "work", "fx")
	keys, made := filepath.Join(fx, "authorized_keys"), filepath.Join(fx, "made.txt")
	writeTo(t, keys, "K\n")
	c := openCheckpoints(filepath.Join(dir, "checkpoints"), time.Now)
	keepAll(t, c, "p2", keys, made)
	writeTo(t, keys, "K, edited\n")
	writeTo(t, made, "made by the model\n")

	elsewhere := filepath.Join(dir, "ssh")
	writeTo(t, filepath.Join(elsewhere, "authorized_keys"), "mine\n")
	writeTo(t, filepath.Join(elsewhere, "made.txt"), "mine too\n")
	if err := os.RemoveAll(fx); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(elsewhere, fx); err != nil {
		t.Skip("no symbolic links here: ", err)
	}
	done, failed := c.restore(c.since([]string{"p2"}, 0), restoreHow{})
	if len(done) != 0 || len(failed) != 2 || !strings.Contains(failed[0], "is a link now") || !strings.Contains(failed[1], "is a link now") {
		t.Errorf("done %q, failed %q", done, failed)
	}
	for name, want := range map[string]string{"authorized_keys": "mine\n", "made.txt": "mine too\n"} {
		if b, err := os.ReadFile(filepath.Join(elsewhere, name)); err != nil || string(b) != want {
			t.Errorf("%s, where the link leads, holds %q (%v), want %q", name, b, err, want)
		}
	}
	// A folder that is gone is made again: the file comes back in it (and
	// the one that was not there is not, as it was).
	if err := os.Remove(fx); err != nil {
		t.Fatal(err)
	}
	done, failed = c.restore(c.since([]string{"p2"}, 0), restoreHow{})
	if len(done) != 1 || len(failed) != 0 {
		t.Errorf("with the folder gone: done %q, failed %q", done, failed)
	}
	if b, _ := os.ReadFile(keys); string(b) != "K\n" {
		t.Errorf("authorized_keys holds %q, want it back", b)
	}
}

// The session can refuse a file (an Edit deny rule added since, or a work
// tree that is gone), and a file that may not be written now is left as
// it is, as Write would leave it; one put back has its set-user-ID and the
// like again.
func TestRestoreRefusalsAndModes(t *testing.T) {
	dir := realTemp(t)
	denied, readonly, setuid := filepath.Join(dir, "denied.txt"), filepath.Join(dir, "readonly.txt"), filepath.Join(dir, "tool")
	writeTo(t, denied, "d0\n")
	writeTo(t, readonly, "r0\n")
	writeTo(t, setuid, "s0\n")
	if err := os.Chmod(setuid, 0o755|fs.ModeSetuid); err != nil {
		t.Fatal(err)
	}
	c := openCheckpoints(filepath.Join(dir, "checkpoints"), time.Now)
	keepAll(t, c, "p1", denied, readonly, setuid)
	writeTo(t, denied, "d1\n")
	writeTo(t, readonly, "r1\n")
	writeTo(t, setuid, "s1\n")
	if err := os.Chmod(readonly, 0o444); err != nil {
		t.Fatal(err)
	}
	how := restoreHow{refuse: func(p string) string {
		if p == denied {
			return "an Edit deny rule covers it now"
		}
		return ""
	}}
	done, failed := c.restore(c.since([]string{"p1"}, 0), how)
	all := strings.Join(failed, "\n")
	if !strings.Contains(all, "denied.txt: an Edit deny rule covers it now, so it is left as it is") {
		t.Errorf("failed %q", failed)
	}
	if b, _ := os.ReadFile(denied); string(b) != "d1\n" {
		t.Errorf("the denied file holds %q", b)
	}
	if os.Geteuid() != 0 {
		// (Root may write anything.)
		if !strings.Contains(all, "readonly.txt: it may not be written now") {
			t.Errorf("failed %q", failed)
		}
		if b, _ := os.ReadFile(readonly); string(b) != "r1\n" {
			t.Errorf("the read-only file holds %q", b)
		}
	}
	if b, _ := os.ReadFile(setuid); string(b) != "s0\n" {
		t.Errorf("the set-user-ID file holds %q (done %q, failed %q)", b, done, failed)
	}
	if info, err := os.Stat(setuid); err != nil || info.Mode()&fs.ModeSetuid == 0 || info.Mode().Perm() != 0o755 {
		t.Errorf("the file put back has the mode %v (%v), want set-user-ID and 0755", info.Mode(), err)
	}
}

// Two spellings of one file (README.md and readme.md, on a disk that
// ignores case as macOS's do; here a second name of the file stands in
// for that) are kept as one file: two copies of it would be put back one
// over the other, and the later would win (the rewind review of
// 2026-11-19).
func TestKeepTakesTwoSpellingsAsOneFile(t *testing.T) {
	dir := realTemp(t)
	upper, lower := filepath.Join(dir, "README.md"), filepath.Join(dir, "readme.md")
	writeTo(t, upper, "v0\n")
	c := openCheckpoints(filepath.Join(dir, "checkpoints"), time.Now)
	keepAll(t, c, "p1", upper)
	writeTo(t, upper, "v1\n")
	if _, err := os.Lstat(lower); err != nil {
		if err := os.Link(upper, lower); err != nil {
			t.Skip("no second name for a file here: ", err)
		}
	}
	keepAll(t, c, "p1", lower)
	writeTo(t, lower, "v2\n")
	keepAll(t, c, "p2", lower)
	cps := c.since([]string{"p1", "p2"}, 0)
	if len(cps) != 1 || cps[0].Path != upper {
		t.Fatalf("kept %+v, want README.md alone", cps)
	}
	if b, _ := os.ReadFile(filepath.Join(cps[0].dir, cps[0].Blob)); string(b) != "v0\n" {
		t.Errorf("the copy holds %q, want what the file held before the first change", b)
	}
}

// A file Write or Edit changed is followed: as each later prompt goes out,
// it is copied again if anything changed it since its newest copy (a shell
// command, say), and a rewind to before a prompt puts it back as it was
// when that prompt went out, as Claude Code rewinds the files it tracks. A
// file that holds now what it held then is not offered, and one only a
// shell command changed is not followed (the rewind review of 2026-11-19).
func TestBeginFollowsChangedFiles(t *testing.T) {
	dir := realTemp(t)
	f, g, shell := filepath.Join(dir, "f.txt"), filepath.Join(dir, "g.txt"), filepath.Join(dir, "shell.txt")
	writeTo(t, f, "v0\n")
	writeTo(t, g, "w0\n")
	writeTo(t, shell, "s0\n")
	c := openCheckpoints(filepath.Join(dir, "checkpoints"), time.Now)
	begin := func(p string) {
		t.Helper()
		if err := c.begin(p); err != nil {
			t.Fatalf("begin %s: %v", p, err)
		}
	}
	order := []string{"p1", "p2", "p3"}
	// p1: Write changes f and g.
	begin("p1")
	keepAll(t, c, "p1", f, g)
	writeTo(t, f, "v1\n")
	writeTo(t, g, "w1\n")
	// p2: a shell command changes f, and shell.txt.
	begin("p2")
	writeTo(t, f, "v2\n")
	writeTo(t, shell, "s1\n")
	// p3: Edit changes f again.
	begin("p3")
	keepAll(t, c, "p3", f)
	writeTo(t, f, "v3\n")
	want := func(at int, files map[string]string) {
		t.Helper()
		got := map[string]string{}
		for _, cp := range c.since(order, at) {
			b, _ := os.ReadFile(filepath.Join(cp.dir, cp.Blob))
			got[filepath.Base(cp.Path)] = string(b)
		}
		if fmt.Sprint(got) != fmt.Sprint(files) {
			t.Errorf("before %s: %q, want %q", order[at], got, files)
		}
	}
	want(0, map[string]string{"f.txt": "v0\n", "g.txt": "w0\n"})
	want(1, map[string]string{"f.txt": "v1\n"})
	want(2, map[string]string{"f.txt": "v2\n"})
	// Changed while the newest prompt's turn runs: offered as the prompt
	// went out.
	writeTo(t, g, "w2, by the shell\n")
	want(2, map[string]string{"f.txt": "v2\n", "g.txt": "w1\n"})

	// And put back so.
	done, failed := c.restore(c.since(order, 1), restoreHow{})
	if len(done) != 2 || len(failed) != 0 {
		t.Errorf("done %q, failed %q", done, failed)
	}
	for p, w := range map[string]string{f: "v1\n", g: "w1\n", shell: "s1\n"} {
		if b, _ := os.ReadFile(p); string(b) != w {
			t.Errorf("%s holds %q, want %q", filepath.Base(p), b, w)
		}
	}
}

// Where the files were not looked at as a prompt went out (a session from
// before that was done, or a prompt sent with copies off), a rewind to
// before it takes the oldest copy from it on, not the newest before it,
// which would undo an earlier prompt's change too (the rewind review of
// 2026-11-19, the day's own commits).
func TestSinceWithoutBeginKeepsTheOldRule(t *testing.T) {
	dir := realTemp(t)
	f := filepath.Join(dir, "main.go")
	writeTo(t, f, "A\n")
	c := openCheckpoints(filepath.Join(dir, "checkpoints"), time.Now)
	keepAll(t, c, "p1", f)
	writeTo(t, f, "B\n")
	keepAll(t, c, "p3", f)
	writeTo(t, f, "C\n")
	cps := c.since([]string{"p1", "p2", "p3"}, 1)
	if len(cps) != 1 {
		t.Fatalf("offered %+v", cps)
	}
	if b, _ := os.ReadFile(filepath.Join(cps[0].dir, cps[0].Blob)); string(b) != "B\n" {
		t.Errorf("before prompt 2 the file held %q, want B (what prompt 1 left)", b)
	}
	// Looked at as prompt 2 went out, it held B then too.
	c2 := openCheckpoints(filepath.Join(dir, "checkpoints2"), time.Now)
	writeTo(t, f, "A\n")
	keepAll(t, c2, "p1", f)
	writeTo(t, f, "B\n")
	if err := c2.begin("p2"); err != nil {
		t.Fatal(err)
	}
	writeTo(t, f, "C\n")
	if cps := c2.since([]string{"p1", "p2"}, 1); len(cps) != 1 {
		t.Fatalf("offered %+v", cps)
	} else if b, _ := os.ReadFile(filepath.Join(cps[0].dir, cps[0].Blob)); string(b) != "B\n" {
		t.Errorf("with begin, before prompt 2 the file held %q, want B", b)
	}
	// And the store opened again still knows prompt 2 was looked at.
	if again := openCheckpoints(filepath.Join(dir, "checkpoints2"), time.Now); !again.begun["p2"] {
		t.Error("a store opened again forgot which prompts were looked at as they went out")
	}
}

// What two files hold alike, or one file at several prompts, is one copy.
func TestCopiesOfTheSameContentAreKeptOnce(t *testing.T) {
	dir := realTemp(t)
	a, b := filepath.Join(dir, "a.txt"), filepath.Join(dir, "b.txt")
	writeTo(t, a, "the same\n")
	writeTo(t, b, "the same\n")
	store := filepath.Join(dir, "checkpoints")
	c := openCheckpoints(store, time.Now)
	keepAll(t, c, "p1", a, b)
	keepAll(t, c, "p2", a)
	blobs, _ := filepath.Glob(filepath.Join(store, "c-*"))
	if len(blobs) != 1 {
		t.Errorf("%d copies of one content: %q", len(blobs), blobs)
	}
	for _, cp := range c.since([]string{"p1", "p2"}, 0) {
		t.Errorf("offered %s, which holds what it held", cp.Path)
	}
}
