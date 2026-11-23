package instructions

import (
	"strings"
	"testing"
)

func TestImportForms(t *testing.T) {
	o := Options{
		Cwd:          "/work/shop",
		Home:         "/home/ana",
		UserFiles:    []string{"/home/ana/GNOSH.md"},
		ProjectNames: []string{"CLAUDE.md"},
		Trusted:      true,
		FS: tree(map[string]string{
			"/home/ana/GNOSH.md":            "My notes: @~/notes/style.md and @/etc/gnosh/site.md and @extra.md\n",
			"/home/ana/notes/style.md":      "style\n",
			"/etc/gnosh/site.md":            "site\n",
			"/home/ana/extra.md":            "extra\n",
			"/work/shop/CLAUDE.md":          "See @README and @docs/build.md, then @./docs/test.md.\n- spaces: @Design\\ Docs/api.md\n- up and back: @docs/../docs/deep/x.md\n",
			"/work/shop/README":             "readme\n",
			"/work/shop/docs/build.md":      "build; see @deep/x.md and @../README\n",
			"/work/shop/docs/test.md":       "test\n",
			"/work/shop/Design Docs/api.md": "api\n",
			"/work/shop/docs/deep/x.md":     "deep\n",
		}),
	}
	s := load(t, o)
	same(t, "files", brief(s.Files()), []string{
		"/home/ana/GNOSH.md User main session_start",
		"/home/ana/notes/style.md User import include",
		"/etc/gnosh/site.md User import include",
		"/home/ana/extra.md User import include",
		"/work/shop/CLAUDE.md Project main session_start",
		"/work/shop/README Project import include",
		"/work/shop/docs/build.md Project import include",
		"/work/shop/docs/deep/x.md Project import include",
		"/work/shop/docs/test.md Project import include",
		"/work/shop/Design Docs/api.md Project import include",
	})
	same(t, "warnings", warned(s.Warnings()), nil)

	x := find(t, s.Files(), "/work/shop/docs/deep/x.md")
	if x.Parent != "/work/shop/docs/build.md" || x.Depth != 2 || x.Kind != KindImport || x.Base != "" {
		t.Errorf("an import of an import: %+v", x)
	}
	if b := find(t, s.Files(), "/work/shop/docs/build.md"); b.Parent != "/work/shop/CLAUDE.md" || b.Depth != 1 {
		t.Errorf("an import: %+v", b)
	}
	// The import stays in the importing file's text, as written.
	if got := find(t, s.Files(), "/work/shop/docs/build.md").Text; got != "build; see @deep/x.md and @../README\n" {
		t.Errorf("importer text %q", got)
	}
}

func TestImportsInsideCodeAreNotImports(t *testing.T) {
	o := Options{
		Cwd:          "/p",
		ProjectNames: []string{"CLAUDE.md"},
		Trusted:      true,
		FS: tree(map[string]string{
			"/p/CLAUDE.md": "Mention a path literally with `@docs/a.md`.\n\n" +
				"```sh\ncat @docs/b.md\n```\n\n" +
				"~~~\n@docs/c.md\n~~~\n\n" +
				"Mail ana@example.com or see user@docs/d.md.\n\n" +
				"Real: @docs/e.md\n",
			"/p/docs/a.md": "a\n",
			"/p/docs/b.md": "b\n",
			"/p/docs/c.md": "c\n",
			"/p/docs/d.md": "d\n",
			"/p/docs/e.md": "e\n",
		}),
	}
	s := load(t, o)
	same(t, "files", paths(s.Files()), []string{"/p/CLAUDE.md", "/p/docs/e.md"})
	same(t, "warnings", warned(s.Warnings()), nil)
}

func TestImportCyclesAndRepeats(t *testing.T) {
	o := Options{
		Cwd:          "/p",
		ProjectNames: []string{"CLAUDE.md"},
		Trusted:      true,
		FS: tree(map[string]string{
			"/p/CLAUDE.md": "@a.md @b.md @a.md @CLAUDE.md\n",
			"/p/a.md":      "@b.md @a.md\n",
			"/p/b.md":      "@a.md @CLAUDE.md @c.md\n",
			"/p/c.md":      "c\n",
		}),
	}
	s := load(t, o)
	same(t, "files", paths(s.Files()), []string{"/p/CLAUDE.md", "/p/a.md", "/p/b.md", "/p/c.md"})
	same(t, "warnings", warned(s.Warnings()), nil)
	if b := find(t, s.Files(), "/p/b.md"); b.Parent != "/p/a.md" || b.Depth != 2 {
		t.Errorf("b.md came in through a.md, got %+v", b)
	}
}

func TestImportDepth(t *testing.T) {
	fsys := tree(map[string]string{
		"/p/CLAUDE.md": "@1.md\n",
		"/p/1.md":      "@2.md\n",
		"/p/2.md":      "@3.md\n",
		"/p/3.md":      "@4.md\n",
		"/p/4.md":      "@5.md\n",
		"/p/5.md":      "@6.md\n",
		"/p/6.md":      "the end, and @nobody\n",
	})
	for _, c := range []struct {
		depth int
		files []string
		warns []string
	}{
		{0, []string{"/p/CLAUDE.md", "/p/1.md", "/p/2.md", "/p/3.md", "/p/4.md"}, []string{"import_depth /p/5.md"}},
		{2, []string{"/p/CLAUDE.md", "/p/1.md", "/p/2.md"}, []string{"import_depth /p/3.md"}},
		{6, []string{"/p/CLAUDE.md", "/p/1.md", "/p/2.md", "/p/3.md", "/p/4.md", "/p/5.md", "/p/6.md"}, nil},
		{-1, []string{"/p/CLAUDE.md"}, nil},
	} {
		s := load(t, Options{Cwd: "/p", ProjectNames: []string{"CLAUDE.md"}, MaxDepth: c.depth, Trusted: true, FS: fsys})
		same(t, "files", paths(s.Files()), c.files)
		same(t, "warnings", warned(s.Warnings()), c.warns)
		for _, w := range s.Warnings() {
			if w.From == "" || !strings.Contains(w.Text, "imports deep") {
				t.Errorf("depth %d: warning %+v", c.depth, w)
			}
		}
	}
}

func TestMissingImports(t *testing.T) {
	o := Options{
		Cwd:          "/p",
		ProjectNames: []string{"CLAUDE.md"},
		Trusted:      true,
		FS: tree(map[string]string{
			"/p/CLAUDE.md": "See @docs/gone.md and @gone.md. Ask @alice, or use @Override. Also @~/x.md\nInstall @types/node and @vue/cli; mark it @pytest.fixture; see @docs.\n",
		}),
	}
	s := load(t, o)
	same(t, "files", paths(s.Files()), []string{"/p/CLAUDE.md"})
	// A single word with no slash or dot that names no file is a mention,
	// not a broken import; so is a package or a decorator (@types/node,
	// @pytest.fixture), which was warned about at every start.
	same(t, "warnings", warned(s.Warnings()), []string{
		"import_missing /p/docs/gone.md",
		"import_missing /p/gone.md",
		"import_missing ~/x.md",
	})
	w := s.Warnings()[0]
	if w.From != "/p/CLAUDE.md" || w.Text != "/p/CLAUDE.md imports @docs/gone.md, but /p/docs/gone.md is not there." {
		t.Errorf("warning: %+v", w)
	}

	o.Home = "/home/ana"
	s = load(t, o)
	same(t, "warnings with a home", warned(s.Warnings()), []string{
		"import_missing /p/docs/gone.md",
		"import_missing /p/gone.md",
		"import_missing /home/ana/x.md",
	})
}

// asked records what an ApproveImport function was asked.
type asked struct {
	yes   map[string]bool
	calls []string
}

func (a *asked) approve(from, target string) bool {
	a.calls = append(a.calls, from+" -> "+target)
	return a.yes[target]
}

func TestImportsThatLeaveTheProject(t *testing.T) {
	o := Options{
		Cwd:          "/work/shop",
		Home:         "/home/ana",
		UserFiles:    []string{"/home/ana/GNOSH.md"},
		ProjectNames: []string{"CLAUDE.md"},
		Trusted:      true,
		FS: tree(map[string]string{
			"/home/ana/GNOSH.md":      "@~/own.md\n",
			"/home/ana/own.md":        "the person's own files may import anything: @/etc/site.md\n",
			"/etc/site.md":            "site\n",
			"/etc/secret.md":          "secret\n",
			"/home/ana/notes.md":      "notes; @~/more.md\n",
			"/home/ana/more.md":       "more\n",
			"/work/outside.md":        "outside\n",
			"/work/shop/CLAUDE.md":    "@/etc/secret.md @~/notes.md @../outside.md @docs/in.md @docs/link.md\n",
			"/work/shop/docs/in.md":   "in; @/etc/secret.md again\n",
			"/work/shop/docs/link.md": "-> /etc/secret.md",
		}),
	}

	// With nobody to ask, nothing outside the project comes in.
	s := load(t, o)
	same(t, "files", brief(s.Files()), []string{
		"/home/ana/GNOSH.md User main session_start",
		"/home/ana/own.md User import include",
		"/etc/site.md User import include",
		"/work/shop/CLAUDE.md Project main session_start",
		"/work/shop/docs/in.md Project import include",
	})
	same(t, "warnings", warned(s.Warnings()), []string{
		"import_refused /etc/secret.md",
		"import_refused /home/ana/notes.md",
		"import_refused /work/outside.md",
		"import_refused /etc/secret.md",
		"import_refused /etc/secret.md",
	})
	ws := s.Warnings()
	if ws[0].From != "/work/shop/CLAUDE.md" || ws[3].From != "/work/shop/docs/in.md" {
		t.Errorf("who asked: %q and %q", ws[0].From, ws[3].From)
	}
	if !strings.Contains(ws[4].Text, "/work/shop/docs/link.md (which leads, through a link, to /etc/secret.md)") {
		t.Errorf("the link's warning: %s", ws[4])
	}

	// With someone to ask, each target is asked about once, and a file
	// that was let in is asked about its own imports.
	a := &asked{yes: map[string]bool{"/home/ana/notes.md": true}}
	o.ApproveImport = a.approve
	s = load(t, o)
	same(t, "asked", a.calls, []string{
		"/work/shop/CLAUDE.md -> /etc/secret.md",
		"/work/shop/CLAUDE.md -> /home/ana/notes.md",
		"/home/ana/notes.md -> /home/ana/more.md",
		"/work/shop/CLAUDE.md -> /work/outside.md",
	})
	same(t, "files", brief(s.Files()), []string{
		"/home/ana/GNOSH.md User main session_start",
		"/home/ana/own.md User import include",
		"/etc/site.md User import include",
		"/work/shop/CLAUDE.md Project main session_start",
		"/home/ana/notes.md Project import include",
		"/work/shop/docs/in.md Project import include",
	})
}

func TestProjectRootIsTheLimitNotTheWorkingDirectory(t *testing.T) {
	// From a sub-directory, a file may import from anywhere in its
	// project, and no further.
	o := Options{
		Cwd:          "/work/shop/api",
		ProjectRoot:  "/work/shop",
		ProjectNames: []string{"CLAUDE.md"},
		Trusted:      true,
		FS: tree(map[string]string{
			"/work/shop/api/CLAUDE.md": "@../docs/a.md @../../beside.md\n",
			"/work/shop/docs/a.md":     "a\n",
			"/work/beside.md":          "beside\n",
			"/work/CLAUDE.md":          "above the project; @beside.md\n",
		}),
	}
	s := load(t, o)
	same(t, "files", paths(s.Files()), []string{"/work/CLAUDE.md", "/work/shop/api/CLAUDE.md", "/work/shop/docs/a.md"})
	// The file above the project is read, but what it imports from
	// outside the project needs a yes like any other.
	same(t, "warnings", warned(s.Warnings()), []string{"import_refused /work/beside.md", "import_refused /work/beside.md"})
}
