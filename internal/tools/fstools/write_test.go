package fstools

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/gnosh-ai/gnosh/internal/core"
)

// noTempFiles fails the test if a temporary file of an unfinished write is
// lying in dir.
func noTempFiles(t *testing.T, dir string) {
	t.Helper()
	for _, name := range dirNames(t, dir) {
		if strings.HasPrefix(name, ".gnosh-") {
			t.Errorf("a temporary file was left behind in %s: %s", dir, name)
		}
	}
}

func TestWriteCreatesAFile(t *testing.T) {
	f := newFixture(t, Options{})
	p := f.path("new/deep/dir/file.txt")
	res := f.run(f.write, in{"file_path": p, "content": "one\ntwo\n"})
	if res.IsError {
		t.Fatalf("failed: %s", core.TextOf(res.Content))
	}
	if got := f.content(p); got != "one\ntwo\n" {
		t.Errorf("file holds %q", got)
	}
	mustContain(t, core.TextOf(res.Content), "Created "+p, "2 lines", "8 bytes")
	if got := metaOf(t, res, "type"); got != "create" {
		t.Errorf("Meta type = %v, want create", got)
	}
	if got := metaOf(t, res, "filePath"); got != p {
		t.Errorf("Meta filePath = %v", got)
	}
	want := core.Display{Kind: "diff", Title: "file.txt", Path: p, Before: "", After: "one\ntwo\n"}
	if res.Display == nil || *res.Display != want {
		t.Errorf("Display = %+v, want %+v", res.Display, want)
	}
	noTempFiles(t, filepath.Dir(p))

	// What the session wrote, it has seen: it may write or edit it again.
	st := States(f.env)
	if len(st) != 1 || st[0].Path != p || !st[0].Full || !st[0].Written || st[0].Size != 8 {
		t.Errorf("States = %+v", st)
	}
	f.ok(f.write, in{"file_path": p, "content": "three\n"})
	f.ok(f.edit, in{"file_path": p, "old_string": "three", "new_string": "four"})
	if got := f.content(p); got != "four\n" {
		t.Errorf("file holds %q", got)
	}
}

func TestWriteContentIsExact(t *testing.T) {
	f := newFixture(t, Options{})
	for name, content := range map[string]string{
		"empty":            "",
		"no-final-newline": "a\nb",
		"crlf":             "a\r\nb\r\n",
		"bom":              utf8BOM + "x\n",
		"unicode":          "héllo 日本語 \U0001F600\n",
		"only-newlines":    "\n\n\n",
		"nul":              "a\x00b",
		"tabs-and-spaces":  "\t  x  \t\n",
	} {
		p := f.path(name)
		f.ok(f.write, in{"file_path": p, "content": content})
		if got := f.content(p); got != content {
			t.Errorf("%s: file holds %q, want %q", name, got, content)
		}
	}
	// content must be there, though it may be empty.
	f.fails(f.write, in{"file_path": f.path("x")}, "content is required")
	f.fails(f.write, in{"file_path": f.path("x"), "content": 5}, "content must be a string")
	f.fails(f.write, in{"content": "x"}, "file_path is required")
	f.fails(f.write, in{"file_path": f.path("x"), "content": "x", "mode": "0644"}, `takes no argument called "mode"`)
	if _, err := os.Stat(f.path("x")); err == nil {
		t.Error("a refused call made the file")
	}
}

func TestWriteNeedsTheFileRead(t *testing.T) {
	f := newFixture(t, Options{})
	p := f.file("existing.txt", "precious\n")

	msg := f.fails(f.write, in{"file_path": p, "content": "clobber\n"}, "already exists", "has not read it", "Nothing was written", "Read")
	mustNotContain(t, msg, "precious")
	if got := f.content(p); got != "precious\n" {
		t.Fatalf("a refused write changed the file: %q", got)
	}
	// Describe refuses in the same words, and still names the path.
	act, err := f.describe(f.write, in{"file_path": p, "content": "clobber\n"})
	if err == nil || !strings.Contains(err.Error(), "has not read it") {
		t.Errorf("Describe error = %v", err)
	}
	if act.Kind != core.ActionEdit || len(act.Paths) != 1 || act.Paths[0] != p || act.Preview != nil {
		t.Errorf("Describe action alongside the error = %+v", act)
	}

	f.ok(f.read, in{"file_path": p})
	res := f.run(f.write, in{"file_path": p, "content": "replaced\nwith two\n"})
	if res.IsError {
		t.Fatalf("failed after reading: %s", core.TextOf(res.Content))
	}
	mustContain(t, core.TextOf(res.Content), "Wrote "+p, "2 lines", "replacing its 1 line")
	if got := metaOf(t, res, "type"); got != "update" {
		t.Errorf("Meta type = %v, want update", got)
	}
	want := core.Display{Kind: "diff", Title: "existing.txt", Path: p, Before: "precious\n", After: "replaced\nwith two\n"}
	if res.Display == nil || *res.Display != want {
		t.Errorf("Display = %+v, want %+v", res.Display, want)
	}
	noTempFiles(t, f.dir)
}

func TestWriteNeedsTheWholeFileSeen(t *testing.T) {
	f := newFixture(t, Options{ReadDefaultLines: 10})
	p := f.file("long.txt", manyLines(25))

	f.ok(f.read, in{"file_path": p})
	f.fails(f.write, in{"file_path": p, "content": "short\n"}, "would be replaced whole", "only been shown lines 1-10", "Nothing was written")
	f.ok(f.read, in{"file_path": p, "offset": 21})
	f.fails(f.write, in{"file_path": p, "content": "short\n"}, "only been shown lines 1-10 and 21-25")
	if got := f.content(p); got != manyLines(25) {
		t.Fatal("a refused write changed the file")
	}
	// The part in between completes it.
	f.ok(f.read, in{"file_path": p, "offset": 11})
	f.ok(f.write, in{"file_path": p, "content": "short\n"})
	if got := f.content(p); got != "short\n" {
		t.Errorf("file holds %q", got)
	}
}

func TestWriteAnEmptyFileNeedsNoRead(t *testing.T) {
	f := newFixture(t, Options{})
	p := f.file("empty.txt", "")
	res := f.run(f.write, in{"file_path": p, "content": "filled\n"})
	if res.IsError {
		t.Fatalf("failed: %s", core.TextOf(res.Content))
	}
	if got := metaOf(t, res, "type"); got != "update" {
		t.Errorf("Meta type = %v, want update", got)
	}
	if got := f.content(p); got != "filled\n" {
		t.Errorf("file holds %q", got)
	}
}

func TestWriteRefusesAFileChangedBehindItsBack(t *testing.T) {
	f := newFixture(t, Options{})
	p := f.file("a.txt", "version one\n")
	f.ok(f.read, in{"file_path": p})

	// Someone else changes the file.
	if err := os.WriteFile(p, []byte("version two, by someone else\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	f.fails(f.write, in{"file_path": p, "content": "mine\n"}, "has changed on disk since this session last read it", "Nothing was written", "Read it again")
	if got := f.content(p); got != "version two, by someone else\n" {
		t.Fatalf("a refused write changed the file: %q", got)
	}
	// Same size, same modification time, other content: the hash tells.
	f.ok(f.read, in{"file_path": p})
	info, _ := os.Stat(p)
	if err := os.WriteFile(p, []byte("VERSION TWO, BY SOMEONE ELSE\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(p, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	f.fails(f.write, in{"file_path": p, "content": "mine\n"}, "has changed on disk")

	// A file that was only touched has not changed: same bytes, new time.
	f.ok(f.read, in{"file_path": p})
	later := time.Now().Add(time.Hour)
	if err := os.Chtimes(p, later, later); err != nil {
		t.Fatal(err)
	}
	f.ok(f.write, in{"file_path": p, "content": "mine\n"})
	if got := f.content(p); got != "mine\n" {
		t.Errorf("file holds %q", got)
	}

	// A file deleted and made again is a new file to the session only if
	// it is gone: here it is back, with other content.
	f.ok(f.read, in{"file_path": p})
	os.Remove(p)
	f.file("a.txt", "reborn\n")
	f.fails(f.write, in{"file_path": p, "content": "mine\n"}, "has changed on disk")
	// Gone altogether, the path is free again.
	os.Remove(p)
	f.ok(f.write, in{"file_path": p, "content": "fresh\n"})
}

func TestWriteSameContentWritesNothing(t *testing.T) {
	f := newFixture(t, Options{})
	p := f.file("same.txt", "unchanged\n")
	f.ok(f.read, in{"file_path": p})
	f.setTime(p, 0)
	f.ok(f.read, in{"file_path": p})
	before, _ := os.Stat(p)

	res := f.run(f.write, in{"file_path": p, "content": "unchanged\n"})
	if res.IsError {
		t.Fatalf("failed: %s", core.TextOf(res.Content))
	}
	mustContain(t, core.TextOf(res.Content), "already holds exactly this content", "Nothing was written")
	if metaOf(t, res, "unchanged") != true {
		t.Error("Meta does not say unchanged")
	}
	after, _ := os.Stat(p)
	if !after.ModTime().Equal(before.ModTime()) || !os.SameFile(before, after) {
		t.Error("the file was rewritten though nothing changed")
	}
	if res.Display == nil || res.Display.Before != res.Display.After {
		t.Errorf("Display = %+v", res.Display)
	}
}

func TestWriteKeepsTheMode(t *testing.T) {
	f := newFixture(t, Options{})
	for _, mode := range []os.FileMode{0o600, 0o640, 0o755, 0o664, 0o700} {
		p := f.file(fmt.Sprintf("mode-%o", mode), "old\n")
		if err := os.Chmod(p, mode); err != nil {
			t.Fatal(err)
		}
		f.ok(f.read, in{"file_path": p})
		f.ok(f.write, in{"file_path": p, "content": "new\n"})
		info, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != mode {
			t.Errorf("mode %o became %o after Write", mode, info.Mode().Perm())
		}
		f.ok(f.edit, in{"file_path": p, "old_string": "new", "new_string": "newer"})
		info, _ = os.Stat(p)
		if info.Mode().Perm() != mode {
			t.Errorf("mode %o became %o after Edit", mode, info.Mode().Perm())
		}
	}
	// A new file gets what the system gives any new file.
	ref, err := os.OpenFile(f.path("reference"), os.O_CREATE|os.O_WRONLY, 0o666)
	if err != nil {
		t.Fatal(err)
	}
	ref.Close()
	want, _ := os.Stat(f.path("reference"))
	f.ok(f.write, in{"file_path": f.path("brand-new"), "content": "x"})
	got, _ := os.Stat(f.path("brand-new"))
	if got.Mode().Perm() != want.Mode().Perm() {
		t.Errorf("a new file has mode %o; the system's default is %o", got.Mode().Perm(), want.Mode().Perm())
	}
	// And so do the directories made on the way.
	if err := os.Mkdir(f.path("refdir"), 0o777); err != nil {
		t.Fatal(err)
	}
	wantDir, _ := os.Stat(f.path("refdir"))
	f.ok(f.write, in{"file_path": f.path("made/on/the/way/f"), "content": "x"})
	gotDir, _ := os.Stat(f.path("made/on"))
	if gotDir.Mode().Perm() != wantDir.Mode().Perm() {
		t.Errorf("a new directory has mode %o; the system's default is %o", gotDir.Mode().Perm(), wantDir.Mode().Perm())
	}
}

func TestWriteThroughALink(t *testing.T) {
	f := newFixture(t, Options{})
	target := f.file("real/target.txt", "old\n")
	link := f.symlink("real/target.txt", "link.txt")

	f.ok(f.read, in{"file_path": link})
	act, err := f.describe(f.write, in{"file_path": link, "content": "new\n"})
	if err != nil {
		t.Fatal(err)
	}
	if len(act.Paths) != 2 || act.Paths[0] != link || act.Paths[1] != target {
		t.Errorf("Describe paths = %v, want the link and its target", act.Paths)
	}
	res := f.run(f.write, in{"file_path": link, "content": "new\n"})
	if res.IsError {
		t.Fatalf("failed: %s", core.TextOf(res.Content))
	}
	mustContain(t, core.TextOf(res.Content), "Wrote "+link, "which leads to "+target)
	if got := metaOf(t, res, "realPath"); got != target {
		t.Errorf("Meta realPath = %v", got)
	}
	if got := f.content(target); got != "new\n" {
		t.Errorf("the target holds %q", got)
	}
	if dest, err := os.Readlink(link); err != nil || dest != "real/target.txt" {
		t.Errorf("the link is no longer a link to its target: %q, %v", dest, err)
	}
	noTempFiles(t, f.dir)
	noTempFiles(t, f.path("real"))

	t.Run("a link to nothing makes the target", func(t *testing.T) {
		dangling := f.symlink("real/not-yet/made.txt", "dangling")
		res := f.run(f.write, in{"file_path": dangling, "content": "made\n"})
		if res.IsError {
			t.Fatalf("failed: %s", core.TextOf(res.Content))
		}
		if got := f.content(f.path("real/not-yet/made.txt")); got != "made\n" {
			t.Errorf("the target holds %q", got)
		}
		if !isLink(dangling) {
			t.Error("the link was replaced by a file")
		}
		if got := metaOf(t, res, "type"); got != "create" {
			t.Errorf("Meta type = %v", got)
		}
	})
	t.Run("through a linked directory", func(t *testing.T) {
		f.symlink("real", "dirlink")
		p := f.path("dirlink/inside.txt")
		f.ok(f.write, in{"file_path": p, "content": "in\n"})
		if got := f.content(f.path("real/inside.txt")); got != "in\n" {
			t.Errorf("the file holds %q", got)
		}
	})
	t.Run("refused when the options say so", func(t *testing.T) {
		strict := newFixture(t, Options{RefuseLinkWrites: true})
		target := strict.file("t.txt", "old\n")
		link := strict.symlink("t.txt", "l.txt")
		strict.ok(strict.read, in{"file_path": link})
		strict.fails(strict.write, in{"file_path": link, "content": "x"}, "is a symbolic link", "does not write through one", "Its target is "+target)
		strict.fails(strict.edit, in{"file_path": link, "old_string": "old", "new_string": "new"}, "is a symbolic link", target)
		if got := strict.content(target); got != "old\n" {
			t.Errorf("a refused write changed the target: %q", got)
		}
		strict.symlink("nowhere.txt", "dangling")
		strict.fails(strict.write, in{"file_path": strict.path("dangling"), "content": "x"}, "is a symbolic link")
		// The target itself, and a path through a linked directory, are
		// fine.
		strict.ok(strict.write, in{"file_path": target, "content": "direct\n"})
		strict.mkdir("d")
		strict.symlink("d", "dl")
		strict.ok(strict.write, in{"file_path": strict.path("dl/x"), "content": "x"})
	})
}

func TestWriteRefusesWhatIsNotAFile(t *testing.T) {
	f := newFixture(t, Options{})
	d := f.mkdir("a-directory")
	f.fails(f.write, in{"file_path": d, "content": "x"}, d+" is a directory")
	f.fails(f.write, in{"file_path": d + "/", "content": "x"}, "ends with a slash")
	file := f.file("plain", "x")
	f.fails(f.write, in{"file_path": filepath.Join(file, "below"), "content": "x"}, "cannot exist", "is a file, not a directory")
	f.fails(f.write, in{"file_path": "~/x", "content": "x"}, "not expanded")
	f.symlink("loop", "loop")
	f.fails(f.write, in{"file_path": f.path("loop"), "content": "x"}, "loop")
	if got := dirNames(t, f.dir); len(got) != 3 {
		t.Errorf("refused writes left something behind: %v", got)
	}
}

func TestWriteLeavesNothingBehindWhenItFails(t *testing.T) {
	f := newFixture(t, Options{})
	sys := f.write.(*writeTool).sys
	sys.rename = func(oldpath, newpath string) error {
		if _, err := os.Stat(oldpath); err != nil {
			t.Errorf("the temporary file is not there at rename time: %v", err)
		}
		if filepath.Dir(oldpath) != filepath.Dir(newpath) {
			t.Errorf("the temporary file %s is not beside its target %s", oldpath, newpath)
		}
		return &os.LinkError{Op: "rename", Old: oldpath, New: newpath, Err: syscall.EXDEV}
	}

	t.Run("a new file", func(t *testing.T) {
		p := f.path("brand/new/tree/file.txt")
		f.fails(f.write, in{"file_path": p, "content": "x"}, p+" could not be written", "cross-device")
		if _, err := os.Stat(f.path("brand")); err == nil {
			t.Error("the directories made for the file were left behind")
		}
		if len(dirNames(t, f.dir)) != 0 {
			t.Errorf("left behind: %v", dirNames(t, f.dir))
		}
		if len(States(f.env)) != 0 {
			t.Error("a failed write was recorded")
		}
	})
	t.Run("a directory that was already there stays", func(t *testing.T) {
		f.mkdir("stays")
		f.fails(f.write, in{"file_path": f.path("stays/sub/file.txt"), "content": "x"}, "could not be written")
		if names := dirNames(t, f.path("stays")); len(names) != 0 {
			t.Errorf("left in the existing directory: %v", names)
		}
	})
	t.Run("an existing file is written in place instead", func(t *testing.T) {
		p := f.file("existing.txt", "old\n")
		before, _ := os.Stat(p)
		f.ok(f.read, in{"file_path": p})
		res := f.run(f.write, in{"file_path": p, "content": "new\n"})
		if res.IsError {
			t.Fatalf("failed: %s", core.TextOf(res.Content))
		}
		mustContain(t, core.TextOf(res.Content), "overwritten where it is")
		if metaOf(t, res, "inPlace") != true {
			t.Error("Meta does not say inPlace")
		}
		if got := f.content(p); got != "new\n" {
			t.Errorf("file holds %q", got)
		}
		after, _ := os.Stat(p)
		if !os.SameFile(before, after) {
			t.Error("the file was replaced, not written in place")
		}
		noTempFiles(t, f.dir)
		// The session knows the file as it now is.
		res = f.run(f.edit, in{"file_path": p, "old_string": "new", "new_string": "newer"})
		if res.IsError {
			t.Fatalf("edit after an in-place write failed: %s", core.TextOf(res.Content))
		}
		mustContain(t, core.TextOf(res.Content), "overwritten where it is")
		noTempFiles(t, f.dir)
	})
}

// A rename refused for want of space is not got around by writing the file
// in place, which fails as well, after cutting the file short; and a write
// in place that fails puts the old content back. A full disk left the file
// empty, or holding the start of the new text (the Edit/Write review of
// 2026-11-19).
func TestWriteInPlaceNeverLosesTheFile(t *testing.T) {
	f := newFixture(t, Options{})
	sys := f.write.(*writeTool).sys
	sys.rename = func(oldpath, newpath string) error {
		return &os.LinkError{Op: "rename", Old: oldpath, New: newpath, Err: syscall.ENOSPC}
	}
	p := f.file("full.txt", "the old content\n")
	f.ok(f.read, in{"file_path": p})
	f.fails(f.write, in{"file_path": p, "content": "a longer new content\n"}, "could not be written", "no space left on device")
	if got := f.content(p); got != "the old content\n" {
		t.Errorf("after a full disk the file holds %q", got)
	}
	noTempFiles(t, f.dir)

	// A rename refused where writing in place gets around it, and the
	// write in place failing half way.
	sys.rename = func(oldpath, newpath string) error {
		return &os.LinkError{Op: "rename", Old: oldpath, New: newpath, Err: syscall.EBUSY}
	}
	failed := false
	sys.writeAt = func(file *os.File, b []byte, off int64) (int, error) {
		if !failed {
			failed = true
			n, _ := file.WriteAt(b[:len(b)/2], off)
			return n, syscall.ENOSPC
		}
		return file.WriteAt(b, off)
	}
	f.fails(f.write, in{"file_path": p, "content": "a much longer new content than before\n"}, "could not be written", "no space left on device", "the file holds its old content")
	if got := f.content(p); got != "the old content\n" {
		t.Errorf("after a failed write in place the file holds %q", got)
	}
	// And one that works says why it was done.
	sys.writeAt = (*os.File).WriteAt
	res := f.run(f.write, in{"file_path": p, "content": "new\n"})
	if res.IsError {
		t.Fatalf("failed: %s", core.TextOf(res.Content))
	}
	mustContain(t, core.TextOf(res.Content), "overwritten where it is, because it could not be replaced in one step: device or resource busy")
	if got := f.content(p); got != "new\n" {
		t.Errorf("file holds %q", got)
	}
}

// Two sessions of one process, a session and its sub-agent, take turns on
// a file. Each passed its check against what it had read, and the later
// write undid the earlier one (the Edit/Write review of 2026-11-19). The
// second edit now waits for the first, then goes in on top of it where
// its old_string still picks out one place, as Claude Code lets an edit
// of a changed file, and says the file had changed; a Write over the
// file by the session that no longer has it as it is is refused.
func TestTwoSessionsTakeTurnsOnAFile(t *testing.T) {
	f := newFixture(t, Options{})
	p := f.file("shared.go", "package x\n\nimport \"fmt\"\n")
	other := &core.ToolEnv{Cwd: f.dir, Roots: []string{f.dir}, SessionID: "sub", Store: &sync.Map{}}
	f.ok(f.read, in{"file_path": p})
	if res, err := f.read.Run(context.Background(), call(in{"file_path": p}), other); err != nil || res.IsError {
		t.Fatalf("the other session's read: %v %s", err, core.TextOf(res.Content))
	}
	sys := f.edit.(*editTool).sys
	held, release := make(chan struct{}), make(chan struct{})
	first := true
	sys.rename = func(oldpath, newpath string) error {
		if first {
			first = false
			close(held)
			<-release
		}
		return os.Rename(oldpath, newpath)
	}
	done := make(chan core.ToolResult, 1)
	go func() {
		done <- f.run(f.edit, in{"file_path": p, "old_string": "import \"fmt\"", "new_string": "import \"fmt\"\nimport \"os\""})
	}()
	<-held
	theirs := make(chan core.ToolResult, 1)
	go func() {
		res, _ := f.edit.Run(context.Background(), call(in{"file_path": p, "old_string": "import \"fmt\"", "new_string": "import \"fmt\"\nimport \"io\""}), other)
		theirs <- res
	}()
	select {
	case res := <-theirs:
		t.Fatalf("the other session's edit ran while the first was writing: %s", core.TextOf(res.Content))
	case <-time.After(300 * time.Millisecond):
	}
	close(release)
	if res := <-done; res.IsError {
		t.Fatalf("the first edit failed: %s", core.TextOf(res.Content))
	}
	res := <-theirs
	if res.IsError {
		t.Fatalf("the other session's edit failed: %s", core.TextOf(res.Content))
	}
	mustContain(t, core.TextOf(res.Content), "had changed on disk since this session last read it")
	if got := f.content(p); !strings.Contains(got, "\"os\"") || !strings.Contains(got, "\"io\"") {
		t.Errorf("one edit undid the other: the file holds %q", got)
	}
	f.fails(f.write, in{"file_path": p, "content": "package x\n"}, "has changed on disk")
}

// A file written whole keeps its own CRLF line endings and byte order
// mark, which Read does not show, when the content given has neither: in
// a Windows-style project every line came out changed (the Edit/Write
// review of 2026-11-19). A file whose endings are mixed is written as given.
func TestWriteKeepsTheFilesLineEndings(t *testing.T) {
	f := newFixture(t, Options{})
	p := f.file("win.txt", "\uFEFFone\r\ntwo\r\n")
	f.ok(f.read, in{"file_path": p})
	res := f.run(f.write, in{"file_path": p, "content": "one\ntwo\nthree\n"})
	if res.IsError {
		t.Fatalf("failed: %s", core.TextOf(res.Content))
	}
	if got := f.content(p); got != "\uFEFFone\r\ntwo\r\nthree\r\n" {
		t.Errorf("the file holds %q", got)
	}
	mustContain(t, core.TextOf(res.Content), "It keeps its byte order mark and its CRLF line endings, which the content given did not have.")
	mixed := f.file("mixed.txt", "a\r\nb\n")
	f.ok(f.read, in{"file_path": mixed})
	f.ok(f.write, in{"file_path": mixed, "content": "a\nb\nc\n"})
	if got := f.content(mixed); got != "a\nb\nc\n" {
		t.Errorf("a file of mixed endings holds %q", got)
	}
}

// An old_string found again where it overlaps itself ("aba" in "ababa") is
// found more than once: the edit is refused, as for two places apart. It
// went to the first place unasked (the Edit/Write review of 2026-11-19).
func TestEditRefusesAnOverlappingRepeat(t *testing.T) {
	f := newFixture(t, Options{})
	p := f.file("pattern.txt", "x ababa y\n")
	f.ok(f.read, in{"file_path": p})
	f.fails(f.edit, in{"file_path": p, "old_string": "aba", "new_string": "ABA"}, "found more than once", "overlap")
	if got := f.content(p); got != "x ababa y\n" {
		t.Errorf("the file holds %q", got)
	}
	f.ok(f.edit, in{"file_path": p, "old_string": "x aba", "new_string": "x ABA"})
}

func TestWriteGuardsDamagedText(t *testing.T) {
	f := newFixture(t, Options{})
	p := f.file("latin1.txt", "caf\xe9\n")
	f.ok(f.read, in{"file_path": p})
	// Writing back what Read showed would turn the one bad byte into three
	// wrong ones for good.
	f.fails(f.write, in{"file_path": p, "content": "caf" + replacement + "\n"}, "not valid UTF-8", "U+FFFD", "Nothing was written")
	if got := f.content(p); got != "caf\xe9\n" {
		t.Fatalf("a refused write changed the file: %q", got)
	}
	// Without the mark it is the model's own text, and the file becomes
	// UTF-8.
	f.ok(f.write, in{"file_path": p, "content": "café\n"})
	if got := f.content(p); got != "café\n" {
		t.Errorf("file holds %q", got)
	}
	// In a file that is valid UTF-8 the character is just a character.
	q := f.file("fine.txt", "ok\n")
	f.ok(f.read, in{"file_path": q})
	f.ok(f.write, in{"file_path": q, "content": "a real " + replacement + "\n"})
}

func TestWriteOverALargeFileShowsNoDiff(t *testing.T) {
	f := newFixture(t, Options{DiffMaxBytes: 64, ReadDefaultLines: 1000})
	p := f.file("big.txt", manyLines(40))
	f.ok(f.read, in{"file_path": p})
	act, err := f.describe(f.write, in{"file_path": p, "content": "tiny\n"})
	if err != nil {
		t.Fatal(err)
	}
	if act.Preview == nil || act.Preview.Kind != "text" || act.Preview.Path != p || !strings.Contains(act.Preview.Text, "too large to show as a diff") {
		t.Errorf("Preview = %+v", act.Preview)
	}
	res := f.run(f.write, in{"file_path": p, "content": "tiny\n"})
	if res.IsError {
		t.Fatalf("failed: %s", core.TextOf(res.Content))
	}
	if res.Display == nil || *res.Display != *act.Preview {
		t.Errorf("Display = %+v, want the preview %+v", res.Display, act.Preview)
	}
	if got := f.content(p); got != "tiny\n" {
		t.Errorf("file holds %q", got)
	}
	// A change behind the session's back is still caught, by size and
	// time, though the content was never loaded.
	f.file("big2.txt", manyLines(40))
	f.ok(f.read, in{"file_path": f.path("big2.txt")})
	f.file("big2.txt", manyLines(41))
	f.fails(f.write, in{"file_path": f.path("big2.txt"), "content": "x"}, "has changed on disk")
}

func TestWriteDescribeMatchesRun(t *testing.T) {
	f := newFixture(t, Options{})
	p := f.file("a.txt", "before\n")
	f.ok(f.read, in{"file_path": p})
	input := in{"file_path": p, "content": "after\nmore\n"}
	act, err := f.describe(f.write, input)
	if err != nil {
		t.Fatal(err)
	}
	if act.Kind != core.ActionEdit || act.Summary != "Overwrite "+p+" (2 lines, was 1 line)" {
		t.Errorf("action = %+v", act)
	}
	if got := f.content(p); got != "before\n" {
		t.Fatal("Describe wrote the file")
	}
	res := f.run(f.write, input)
	if act.Preview == nil || res.Display == nil || *act.Preview != *res.Display {
		t.Errorf("Preview %+v and Display %+v differ", act.Preview, res.Display)
	}
	// A new file.
	q := f.path("fresh.txt")
	act, err = f.describe(f.write, in{"file_path": q, "content": "x\n"})
	if err != nil || act.Summary != "Create "+q+" (1 line)" || act.Preview.Before != "" || act.Preview.After != "x\n" {
		t.Errorf("new file: action %+v, err %v", act, err)
	}
	if _, err := os.Stat(q); err == nil {
		t.Error("Describe made the file")
	}
	// Bad arguments give no paths at all.
	act, err = f.describe(f.write, in{"content": "x"})
	if err == nil || len(act.Paths) != 0 {
		t.Errorf("bad arguments: action %+v, err %v", act, err)
	}
}

func TestWriteStopsWhenCancelled(t *testing.T) {
	f := newFixture(t, Options{})
	p := f.path("never.txt")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := f.write.Run(ctx, call(in{"file_path": p, "content": "x"}), f.env)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if _, err := os.Stat(p); err == nil {
		t.Error("a cancelled write made the file")
	}
}

func TestWritesToDifferentFilesAtOnce(t *testing.T) {
	f := newFixture(t, Options{})
	const n = 64
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Go(func() {
			p := f.path(fmt.Sprintf("dir%d/file%d.txt", i%4, i))
			content := fmt.Sprintf("content of %d\n", i)
			res, err := f.write.Run(context.Background(), call(in{"file_path": p, "content": content}), f.env)
			if err != nil || res.IsError {
				t.Errorf("write %d: %v %s", i, err, core.TextOf(res.Content))
				return
			}
			res, err = f.read.Run(context.Background(), call(in{"file_path": p}), f.env)
			if err != nil || res.IsError || core.TextOf(res.Content) != "1\t"+content {
				t.Errorf("read %d: %v %q", i, err, core.TextOf(res.Content))
			}
			res, err = f.edit.Run(context.Background(), call(in{"file_path": p, "old_string": "content", "new_string": "CONTENT"}), f.env)
			if err != nil || res.IsError {
				t.Errorf("edit %d: %v %s", i, err, core.TextOf(res.Content))
			}
		})
	}
	wg.Wait()
	for i := 0; i < n; i++ {
		p := f.path(fmt.Sprintf("dir%d/file%d.txt", i%4, i))
		if got, want := f.content(p), fmt.Sprintf("CONTENT of %d\n", i); got != want {
			t.Errorf("file %d holds %q, want %q", i, got, want)
		}
	}
	if got := len(States(f.env)); got != n {
		t.Errorf("States lists %d files, want %d", got, n)
	}
	for d := 0; d < 4; d++ {
		noTempFiles(t, f.path(fmt.Sprintf("dir%d", d)))
	}
}
