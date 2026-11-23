//go:build unix

package fstools

import (
	"context"
	"io/fs"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/gnosh-ai/gnosh/internal/core"
)

// within runs fn and fails the test if it has not come back in ten
// seconds: a tool that opens a pipe or a device the wrong way hangs.
func within(t *testing.T, what string, fn func()) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		fn()
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatalf("%s did not come back", what)
	}
}

func TestSpecialFilesAreRefused(t *testing.T) {
	f := newFixture(t, Options{})
	fifo := f.path("a-fifo")
	if err := syscall.Mkfifo(fifo, 0o644); err != nil {
		t.Skipf("cannot make a FIFO here: %v", err)
	}
	special := map[string]string{fifo: "a named pipe (FIFO)", os.DevNull: "a character device"}
	// A socket's path may be too long for the system under a deep temp
	// directory; leave it out when it is.
	sock := f.path("s")
	if l, err := net.Listen("unix", sock); err == nil {
		defer l.Close()
		special[sock] = "a socket"
	}
	for path, kind := range special {
		within(t, "Read of "+kind, func() {
			f.fails(f.read, in{"file_path": path}, path+" is "+kind, "not a file with contents to read")
		})
		within(t, "Write to "+kind, func() {
			f.fails(f.write, in{"file_path": path, "content": "x"}, path+" is "+kind, "only writes regular files")
		})
		within(t, "Edit of "+kind, func() {
			f.fails(f.edit, in{"file_path": path, "old_string": "a", "new_string": "b"}, path+" is "+kind, "only writes regular files")
		})
		within(t, "Grep of "+kind, func() {
			f.fails(f.grep, in{"pattern": "x", "path": path}, path+" is "+kind, "cannot be searched")
		})
		within(t, "Glob in "+kind, func() {
			f.fails(f.glob, in{"pattern": "*", "path": path}, path+" is "+kind, "not a directory")
		})
		if err := MarkRead(f.env, path); err == nil {
			t.Errorf("MarkRead accepted %s", kind)
		}
	}
	// Links to them are no better.
	link := f.symlink(fifo, "link-to-fifo")
	within(t, "Read through a link to a FIFO", func() {
		f.fails(f.read, in{"file_path": link}, "a named pipe")
	})
	// A search of the directory passes over them without waiting on them.
	f.file("plain.txt", "needle\n")
	within(t, "Grep over a directory with a FIFO in it", func() {
		if got := f.ok(f.grep, in{"pattern": "needle"}); got != f.path("plain.txt")+"\n" {
			t.Errorf("Grep = %q", got)
		}
	})
	within(t, "Glob over a directory with a FIFO in it", func() {
		got, _ := f.globbed(in{"pattern": "*"})
		if !reflect.DeepEqual(got, []string{"plain.txt"}) {
			t.Errorf("Glob = %v", got)
		}
	})
	if len(States(f.env)) != 0 {
		t.Errorf("a refused special file was recorded: %+v", States(f.env))
	}
}

func TestOpenRegularDoesNotWaitOnAFIFO(t *testing.T) {
	// Should a FIFO take a file's place between the look and the open,
	// opening it must fail at once rather than wait for a writer.
	dir := t.TempDir()
	fifo := filepath.Join(dir, "f")
	if err := syscall.Mkfifo(fifo, 0o644); err != nil {
		t.Skipf("cannot make a FIFO here: %v", err)
	}
	within(t, "openRegular on a FIFO", func() {
		if _, _, fail := openRegular(fifo); fail == nil || !strings.Contains(fail.msg, "a named pipe") {
			t.Errorf("openRegular = %v", fail)
		}
	})
	within(t, "readOnly on a FIFO", func() {
		info, _ := os.Stat(fifo)
		readOnly(&location{abs: fifo, real: fifo, info: info})
	})
}

func TestOwnerIsKept(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("only root can give a file to someone else")
	}
	const uid, gid = 65534, 65534
	f := newFixture(t, Options{})
	p := f.file("theirs.txt", "old\n")
	if err := os.Chown(p, uid, gid); err != nil {
		t.Skipf("cannot change the owner here: %v", err)
	}
	if err := os.Chmod(p, 0o754|os.ModeSetuid|os.ModeSetgid); err != nil {
		t.Fatal(err)
	}
	check := func(when string) {
		t.Helper()
		info, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		st := info.Sys().(*syscall.Stat_t)
		if st.Uid != uid || st.Gid != gid {
			t.Errorf("%s: owner is %d:%d, want %d:%d", when, st.Uid, st.Gid, uid, gid)
		}
		if info.Mode().Perm() != 0o754 {
			t.Errorf("%s: mode is %o, want 754", when, info.Mode().Perm())
		}
	}
	before, _ := os.Stat(p)
	wantBits := before.Mode() & (os.ModeSetuid | os.ModeSetgid)
	if wantBits == 0 {
		t.Log("this file system does not keep set-id bits; only the owner and the mode are checked")
	}
	f.ok(f.read, in{"file_path": p})
	f.ok(f.edit, in{"file_path": p, "old_string": "old", "new_string": "new"})
	check("after Edit")
	f.ok(f.write, in{"file_path": p, "content": "newer\n"})
	check("after Write")
	after, _ := os.Stat(p)
	if got := after.Mode() & (os.ModeSetuid | os.ModeSetgid); got != wantBits {
		t.Errorf("set-id bits are %v, want %v", got, wantBits)
	}
	if f.content(p) != "newer\n" {
		t.Errorf("file holds %q", f.content(p))
	}
}

// permissionTests are the tests that need a user the system can say no
// to. dir is an empty directory that user may write in.
func permissionTests(t *testing.T, dir string) {
	real, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	mk := func(name string) *fixture {
		t.Helper()
		f := newFixture(t, Options{})
		f.dir = filepath.Join(real, name)
		if err := os.Mkdir(f.dir, 0o755); err != nil {
			t.Fatal(err)
		}
		f.env.Cwd = f.dir
		// Give everything back its permissions so that it can be removed.
		t.Cleanup(func() {
			filepath.WalkDir(f.dir, func(p string, d os.DirEntry, err error) error {
				if d != nil && d.IsDir() {
					os.Chmod(p, 0o755)
				}
				return nil
			})
		})
		return f
	}

	t.Run("a file that may not be read", func(t *testing.T) {
		f := mk("unreadable-file")
		p := f.file("secret.txt", "needle\n")
		f.file("open.txt", "needle\n")
		f.ok(f.read, in{"file_path": p})
		if err := os.Chmod(p, 0o000); err != nil {
			t.Fatal(err)
		}
		f.fails(f.read, in{"file_path": p}, p+" cannot be opened: permission denied")
		f.fails(f.edit, in{"file_path": p, "old_string": "needle", "new_string": "x"}, "permission denied")
		f.fails(f.write, in{"file_path": p, "content": "x"}, "permission denied")
		// A search goes on without it and says what it left out.
		got := f.ok(f.grep, in{"pattern": "needle"})
		mustContain(t, got, f.path("open.txt")+"\n", "1 file could not be read and was not searched", p, "permission denied")
		got = f.ok(f.grep, in{"pattern": "needle", "path": p})
		mustContain(t, got, p+" was not searched", "could not be read")
		// Glob only lists names, which it may.
		names, _ := f.globbed(in{"pattern": "*.txt"})
		if len(names) != 2 {
			t.Errorf("Glob listed %v", names)
		}
		os.Chmod(p, 0o644)
		if f.content(p) != "needle\n" {
			t.Error("a refused change went through")
		}
	})

	t.Run("a directory that may not be listed", func(t *testing.T) {
		f := mk("unreadable-dir")
		f.file("open/a.txt", "needle\n")
		inner := f.file("closed/b.txt", "needle\n")
		closed := f.path("closed")
		if err := os.Chmod(closed, 0o000); err != nil {
			t.Fatal(err)
		}
		got := f.ok(f.grep, in{"pattern": "needle"})
		mustContain(t, got, f.path("open/a.txt")+"\n", "1 directory could not be looked into and was left out", closed, "permission denied")
		got = f.ok(f.glob, in{"pattern": "**/*.txt"})
		mustContain(t, got, f.path("open/a.txt")+"\n", "1 directory could not be looked into and was left out", closed)
		mustNotContain(t, got, "b.txt")
		// As the place to search, it is an error, not an empty answer.
		f.fails(f.grep, in{"pattern": "needle", "path": closed}, "Grep could not search "+closed, "permission denied")
		f.fails(f.glob, in{"pattern": "*", "path": closed}, "Glob could not search "+closed, "permission denied")
		// A file inside it cannot be reached, and the answer says where
		// the way is barred.
		f.fails(f.read, in{"file_path": inner}, inner+" cannot be reached", "permission denied in "+closed)
		f.fails(f.write, in{"file_path": f.path("closed/new.txt"), "content": "x"}, "cannot be reached", "permission denied")
		act, err := f.describe(f.read, in{"file_path": inner})
		if err != nil || len(act.Paths) != 1 || act.Paths[0] != inner {
			t.Errorf("Describe = %+v, %v", act, err)
		}
	})

	t.Run("a file marked read-only", func(t *testing.T) {
		f := mk("read-only-file")
		p := f.file("fixed.txt", "old\n")
		f.ok(f.read, in{"file_path": p})
		if err := os.Chmod(p, 0o444); err != nil {
			t.Fatal(err)
		}
		// The directory would allow replacing the file; its own
		// permissions say no, and that is respected.
		f.fails(f.edit, in{"file_path": p, "old_string": "old", "new_string": "new"}, p+" may not be written", "permission denied", "-r--r--r--")
		f.fails(f.write, in{"file_path": p, "content": "new\n"}, "may not be written")
		if _, err := f.describe(f.write, in{"file_path": p, "content": "new\n"}); err == nil {
			t.Error("Describe accepted a write to a read-only file")
		}
		if got := f.content(p); got != "old\n" {
			t.Errorf("a read-only file was changed: %q", got)
		}
		noTempFiles(t, f.dir)
	})

	t.Run("a directory that may not be written in", func(t *testing.T) {
		f := mk("read-only-dir")
		p := f.file("locked/existing.txt", "old\n")
		f.ok(f.read, in{"file_path": p})
		locked := f.path("locked")
		if err := os.Chmod(locked, 0o555); err != nil {
			t.Fatal(err)
		}
		// A new file cannot be made there, and nothing is left behind.
		f.fails(f.write, in{"file_path": f.path("locked/new.txt"), "content": "x"}, "could not be written", "permission denied")
		f.fails(f.write, in{"file_path": f.path("locked/sub/dir/new.txt"), "content": "x"}, "could not be written", "permission denied")
		if names := dirNames(t, locked); len(names) != 1 {
			t.Errorf("left in a directory that may not be written in: %v", names)
		}
		// The file that is there can still be written, in place: its own
		// permissions allow it, and only replacing it is out.
		res := f.run(f.edit, in{"file_path": p, "old_string": "old", "new_string": "new"})
		if res.IsError {
			t.Fatalf("edit failed: %s", core.TextOf(res.Content))
		}
		mustContain(t, core.TextOf(res.Content), "overwritten where it is")
		if metaOf(t, res, "inPlace") != true {
			t.Error("Meta does not say inPlace")
		}
		res = f.run(f.write, in{"file_path": p, "content": "newest\n"})
		if res.IsError || metaOf(t, res, "inPlace") != true {
			t.Fatalf("write: %s", core.TextOf(res.Content))
		}
		if got := f.content(p); got != "newest\n" {
			t.Errorf("file holds %q", got)
		}
	})
}

// permissionDirEnv names the directory the permission tests run in when
// the test binary has started itself again as a user without privileges.
const permissionDirEnv = "GNOSH_FSTOOLS_PERMISSION_DIR"

func TestPermissions(t *testing.T) {
	if os.Getenv(permissionDirEnv) != "" {
		t.Skip("this is the copy that TestPermissionsAsNobody runs")
	}
	if os.Geteuid() != 0 {
		permissionTests(t, t.TempDir())
		return
	}
	// Root is never refused, so the tests would prove nothing. Run them
	// again in a copy of this test binary started as the user nobody.
	exe, err := os.Executable()
	if err != nil {
		t.Skipf("cannot find the test binary: %v", err)
	}
	base, err := os.MkdirTemp("", "fstools-perm-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		filepath.WalkDir(base, func(p string, d os.DirEntry, err error) error {
			if d != nil && d.IsDir() {
				os.Chmod(p, 0o755)
			}
			return nil
		})
		os.RemoveAll(base)
	})
	if err := os.Chmod(base, 0o755); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(base, "fstools.test")
	data, err := os.ReadFile(exe)
	if err != nil {
		t.Skipf("cannot read the test binary: %v", err)
	}
	if err := os.WriteFile(bin, data, 0o755); err != nil {
		t.Fatal(err)
	}
	work := filepath.Join(base, "work")
	if err := os.Mkdir(work, 0o777); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(work, 0o777); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "-test.run=^TestPermissionsAsNobody$", "-test.v")
	cmd.Dir = work
	cmd.Env = []string{permissionDirEnv + "=" + work, "TMPDIR=" + work, "HOME=" + work, "PATH=" + os.Getenv("PATH")}
	cmd.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: 65534, Gid: 65534}}
	out, err := cmd.CombinedOutput()
	if err != nil {
		if strings.Contains(string(out), "operation not permitted") && !strings.Contains(string(out), "--- FAIL") {
			t.Skipf("cannot start a process as another user here: %v\n%s", err, out)
		}
		t.Fatalf("the permission tests failed as the user nobody: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "--- PASS: TestPermissionsAsNobody") {
		t.Fatalf("the permission tests did not run as the user nobody:\n%s", out)
	}
	for _, line := range strings.Split(string(out), "\n") {
		if strings.Contains(line, "--- ") {
			t.Log(strings.TrimSpace(line))
		}
	}
}

func TestPermissionsAsNobody(t *testing.T) {
	dir := os.Getenv(permissionDirEnv)
	if dir == "" {
		t.Skip("run by TestPermissions when the tests run as root")
	}
	if os.Geteuid() == 0 {
		t.Fatal("still root")
	}
	permissionTests(t, dir)
}

// A file with other names, hard links, is written where it is, so that
// they see the new content, and the result says why (the Edit/Write
// review of 2026-11-19: a new file in its place left them the old).
func TestEditWritesAFileWithOtherNamesWhereItIs(t *testing.T) {
	f := newFixture(t, Options{})
	p := f.file("dotfile", "one\n")
	other := f.path("linked")
	if err := os.Link(p, other); err != nil {
		t.Skipf("no hard links here: %v", err)
	}
	f.ok(f.read, in{"file_path": p})
	res := f.run(f.edit, in{"file_path": p, "old_string": "one", "new_string": "two"})
	if res.IsError {
		t.Fatalf("failed: %s", core.TextOf(res.Content))
	}
	mustContain(t, core.TextOf(res.Content), "The file was overwritten where it is, because it has 1 other name by hard links")
	if got := f.content(other); got != "two\n" {
		t.Errorf("the other name holds %q", got)
	}
}

// What a new file in a file's place would lose is said by the file and the
// process: its other names; its owner or its group, when the process may
// not give a new file those; nothing, to root.
func TestReplaceLoses(t *testing.T) {
	info := func(nlink int, uid, gid uint32) fs.FileInfo {
		st := &syscall.Stat_t{Uid: uid, Gid: gid}
		for range nlink {
			st.Nlink++ // (its type is the system's own)
		}
		return statInfo{st}
	}
	for _, c := range []struct {
		old        fs.FileInfo
		euid, egid int
		groups     []int
		want       string
	}{
		{info(2, 1000, 1000), 1000, 1000, nil, "it has 1 other name by hard links"},
		{info(1, 1000, 1000), 1000, 1000, nil, ""},
		{info(1, 1001, 1000), 1000, 1000, nil, "it belongs to another user"},
		{info(1, 1000, 50), 1000, 1000, nil, "its group is not one of yours"},
		{info(1, 1000, 50), 1000, 1000, []int{50}, ""},
		{info(1, 1001, 50), 0, 0, nil, ""},
	} {
		if got := replaceLosesFor(c.old, c.euid, c.egid, c.groups); !strings.HasPrefix(got, c.want) || (c.want == "") != (got == "") {
			t.Errorf("%+v: %q, want %q", c, got, c.want)
		}
	}
}

// statInfo is a file's info made up around a stat record.
type statInfo struct{ st *syscall.Stat_t }

func (s statInfo) Name() string       { return "f" }
func (s statInfo) Size() int64        { return 0 }
func (s statInfo) Mode() fs.FileMode  { return 0o644 }
func (s statInfo) ModTime() time.Time { return time.Time{} }
func (s statInfo) IsDir() bool        { return false }
func (s statInfo) Sys() any           { return s.st }
