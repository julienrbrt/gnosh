package instructions

import (
	"context"
	"io"
	"io/fs"
	"strings"
	"testing"
	"testing/fstest"
)

func TestWorkingAtTheTopOfTheDisk(t *testing.T) {
	o := Options{
		Cwd:          "/",
		ProjectNames: []string{"CLAUDE.md"},
		DotDirs:      []string{".claude"},
		Trusted:      true,
		FS: tree(map[string]string{
			"/CLAUDE.md":           "top; @etc/notes.md\n",
			"/etc/notes.md":        "notes\n",
			"/.claude/rules/go.md": "---\npaths: \"sub/*.go\"\n---\ngo\n",
			"/sub/CLAUDE.md":       "sub\n",
			"/sub/x.go":            "",
		}),
	}
	s := load(t, o)
	same(t, "files", paths(s.Files()), []string{"/CLAUDE.md", "/etc/notes.md"})
	same(t, "warnings", warned(s.Warnings()), nil)
	late, _ := touch(s, "sub/x.go")
	same(t, "late", paths(late), []string{"/sub/CLAUDE.md", "/.claude/rules/go.md"})
	if late[1].Base != "/" || late[0].Trigger != "/sub/x.go" {
		t.Errorf("base %q, trigger %q", late[1].Base, late[0].Trigger)
	}
	if again, _ := touch(s, "/"); len(again) != 0 {
		t.Errorf("touching the top gave %v", paths(again))
	}
}

// brokenFS is a file system on which some files can be seen but not opened,
// and some lie about their size.
type brokenFS struct {
	fstest.MapFS
	locked map[string]bool // cannot be opened
	grown  map[string]int  // say they are empty and then yield this many bytes
}

func (b brokenFS) Open(name string) (fs.File, error) {
	if b.locked[name] {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrPermission}
	}
	f, err := b.MapFS.Open(name)
	if err != nil {
		return nil, err
	}
	if n, ok := b.grown[name]; ok {
		return &grownFile{File: f, left: n}, nil
	}
	return f, nil
}

func (b brokenFS) ReadDir(name string) ([]fs.DirEntry, error) {
	if b.locked[name] {
		return nil, &fs.PathError{Op: "readdir", Path: name, Err: fs.ErrPermission}
	}
	return b.MapFS.ReadDir(name)
}

// grownFile is a file that reports a size of zero and then has content, as
// files under /proc do, or a file that is being written to.
type grownFile struct {
	fs.File
	left int
}

func (g *grownFile) Read(p []byte) (int, error) {
	if g.left == 0 {
		return 0, io.EOF
	}
	n := min(len(p), g.left)
	for i := range p[:n] {
		p[i] = 'x'
	}
	g.left -= n
	return n, nil
}

func TestFilesThatCannotBeRead(t *testing.T) {
	fsys := brokenFS{
		MapFS: tree(map[string]string{
			"/p/CLAUDE.md":          "locked\n",
			"/p/GNOSH.md":           "",
			"/p/AGENTS.md":          "fine; @CLAUDE.md\n",
			"/p/.claude/rules/r.md": "rule\n",
			"/p/.gnosh/rules/r.md":  "rule\n",
		}),
		locked: map[string]bool{"p/CLAUDE.md": true, "p/.claude/rules": true},
		grown:  map[string]int{"p/GNOSH.md": 100},
	}
	o := Options{
		Cwd:          "/p",
		ProjectNames: []string{"CLAUDE.md", "GNOSH.md", "AGENTS.md"},
		DotDirs:      []string{".claude", ".gnosh"},
		MaxBytes:     64,
		Trusted:      true,
		FS:           fsys,
	}
	s := load(t, o)
	same(t, "files", paths(s.Files()), []string{"/p/AGENTS.md", "/p/.gnosh/rules/r.md"})
	same(t, "warnings", warned(s.Warnings()), []string{
		"unreadable /p/CLAUDE.md",
		"too_big /p/GNOSH.md",
		"unreadable /p/CLAUDE.md",
		"unreadable /p/.claude/rules",
	})
	ws := s.Warnings()
	if ws[0].Text != "/p/CLAUDE.md could not be read: permission was denied." {
		t.Errorf("warning: %s", ws[0])
	}
	if !strings.Contains(ws[1].Text, "is more than 64 bytes, over the 64 bytes limit") {
		t.Errorf("warning: %s", ws[1])
	}
	if ws[2].From != "/p/AGENTS.md" {
		t.Errorf("the import's warning: %+v", ws[2])
	}
	if !strings.Contains(ws[3].Text, "The rules directory /p/.claude/rules could not be read: permission was denied.") {
		t.Errorf("warning: %s", ws[3])
	}
}

func TestRuleThatChangesBeforeItMatches(t *testing.T) {
	fsys := tree(map[string]string{
		"/p/.claude/rules/gone.md":   "---\npaths: \"*.go\"\n---\ngone\n",
		"/p/.claude/rules/broken.md": "---\npaths: \"*.go\"\n---\nbroken\n",
		"/p/.claude/rules/kept.md":   "---\npaths: \"*.go\"\n---\nkept\n",
		"/p/x.go":                    "",
	})
	s := load(t, Options{Cwd: "/p", DotDirs: []string{".claude"}, Trusted: true, FS: fsys})
	// Between the start and the first Go file, one rule is deleted and one
	// has its header spoiled.
	delete(fsys, "p/.claude/rules/gone.md")
	fsys["p/.claude/rules/broken.md"].Data = []byte("---\npaths: [\n---\nbroken\n")
	late, warns := touch(s, "/p/x.go")
	same(t, "late", paths(late), []string{"/p/.claude/rules/broken.md", "/p/.claude/rules/kept.md"})
	same(t, "warnings", warned(warns), nil)
	if late[0].Text != "---\npaths: [\n---\nbroken\n" || late[1].Text != "kept\n" {
		t.Errorf("texts: %q, %q", late[0].Text, late[1].Text)
	}
	if again, _ := touch(s, "/p/x.go"); len(again) != 0 {
		t.Errorf("given twice: %v", paths(again))
	}
}

func TestChangedStopsWhenCancelled(t *testing.T) {
	fsys := tree(map[string]string{"/p/CLAUDE.md": "one\n"})
	s := load(t, Options{Cwd: "/p", ProjectNames: []string{"CLAUDE.md"}, Trusted: true, FS: fsys})
	fsys["p/CLAUDE.md"].Data = []byte("one, two\n")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	same(t, "cancelled", s.Changed(ctx), nil)
	same(t, "not cancelled", s.Changed(context.Background()), []string{"/p/CLAUDE.md"})
}

func TestSmallHelpers(t *testing.T) {
	for n, want := range map[int64]string{0: "0 bytes", 1023: "1023 bytes", 1024: "1.0 KiB", 1536: "1.5 KiB", 4 << 20: "4.0 MiB", 5<<20 + 1<<19: "5.5 MiB"} {
		if got := sizeText(n); got != want {
			t.Errorf("sizeText(%d) = %q, want %q", n, got, want)
		}
	}
	for _, c := range []struct {
		p, dir string
		in     bool
		rel    string
	}{
		{"/a/b", "/a", true, "b"},
		{"/a/b/c", "/a", true, "b/c"},
		{"/a", "/a", true, ""},
		{"/ab", "/a", false, ""},
		{"/a", "/a/b", false, ""},
		{"/a/b", "/", true, "a/b"},
		{"/", "/", true, ""},
	} {
		if got := within(c.p, c.dir); got != c.in {
			t.Errorf("within(%q, %q) = %v", c.p, c.dir, got)
		}
		if c.in {
			if got := relTo(c.p, c.dir); got != c.rel {
				t.Errorf("relTo(%q, %q) = %q, want %q", c.p, c.dir, got, c.rel)
			}
		}
	}
	same(t, "ancestors", ancestors("/a/b/c"), []string{"/", "/a", "/a/b", "/a/b/c"})
	same(t, "ancestors of the top", ancestors("/"), []string{"/"})
	for written, want := range map[string]bool{
		"alice": false, "types/node": false, "pytest.fixture": false, "docs": false,
		"a.md": true, "docs/a.MD": true, "notes.txt": true, "./a": true, "../a": true, "~/a": true, "/etc/a": true,
	} {
		if meantAsPath(written) != want {
			t.Errorf("meantAsPath(%q) = %v", written, !want)
		}
	}
}

func TestSetsWithEscapes(t *testing.T) {
	for _, pat := range []string{`[a\`, `[a-\`, `[\`, `[a-`} {
		if _, bad := compilePatterns([]string{pat}); len(bad) != 1 {
			t.Errorf("%q compiled", pat)
		}
	}
	// A range may end at an escaped bracket: "+" to "]" takes in the
	// capital letters and not the small ones.
	if !matches(t, `[+-\]]x`, "]x") || !matches(t, `[+-\]]x`, "Ax") || matches(t, `[+-\]]x`, "ax") {
		t.Error(`[+-\]]x`)
	}
}

// lookFS is a file system that notes every name it is asked about.
type lookFS struct {
	fstest.MapFS
	looked []string
}

func (l *lookFS) Open(name string) (fs.File, error) {
	l.looked = append(l.looked, name)
	return l.MapFS.Open(name)
}

func (l *lookFS) Stat(name string) (fs.FileInfo, error) {
	l.looked = append(l.looked, name)
	return l.MapFS.Stat(name)
}

func (l *lookFS) Lstat(name string) (fs.FileInfo, error) {
	l.looked = append(l.looked, name)
	return l.MapFS.Lstat(name)
}

func (l *lookFS) ReadLink(name string) (string, error) {
	l.looked = append(l.looked, name)
	return l.MapFS.ReadLink(name)
}

func (l *lookFS) ReadDir(name string) ([]fs.DirEntry, error) {
	l.looked = append(l.looked, name)
	return l.MapFS.ReadDir(name)
}

// Nothing in a folder the system mounts from the network as it is looked
// at (/net/<host>, /Network) is looked at outside the project and the
// person's home: an import of "@/net/beacon.example/x.md", or a file
// linked there, would have the machine call that host as gnosh starts
// (the skills review of 2026-11-19). A project that lives in one is read
// as ever.
func TestNetworkMountsAreNotLookedAt(t *testing.T) {
	fsys := &lookFS{MapFS: tree(map[string]string{
		"/p/CLAUDE.md":             "See @/net/beacon.example/x.md and @/Network/Servers/b/y.md\n",
		"/p/.claude/CLAUDE.md":     "-> /net/beacon.example/z.md",
		"/net/beacon.example/x.md": "beacon\n",
		"/net/beacon.example/z.md": "beacon\n",
		"/Network/Servers/b/y.md":  "far\n",
		"/net/srv/proj/CLAUDE.md":  "a project that lives on the network\n",
	})}
	s := load(t, Options{Cwd: "/p", Home: "/home/u", ProjectNames: []string{"CLAUDE.md"}, DotDirs: []string{".claude"}, Trusted: true, FS: fsys})
	same(t, "files", paths(s.Files()), []string{"/p/CLAUDE.md"})
	for _, name := range fsys.looked {
		if strings.HasPrefix(name, "net/") || strings.HasPrefix(name, "Network/") {
			t.Errorf("looked at %s", name)
		}
	}
	var said []string
	for _, w := range s.Warnings() {
		said = append(said, w.Text)
	}
	if all := strings.Join(said, "\n"); strings.Count(all, "mounts from the network") != 3 {
		t.Errorf("the warnings:\n%s", all)
	}
	fsys.looked = nil
	s = load(t, Options{Cwd: "/net/srv/proj", ProjectNames: []string{"CLAUDE.md"}, Trusted: true, FS: fsys})
	same(t, "files of a project on the network", paths(s.Files()), []string{"/net/srv/proj/CLAUDE.md"})
}
