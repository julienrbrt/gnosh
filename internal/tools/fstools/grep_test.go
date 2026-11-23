package fstools

import (
	"context"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
	"unicode/utf16"

	"github.com/gnosh-ai/gnosh/internal/core"
)

// grepTree makes the small tree most Grep tests search. a.txt is the
// newest file and sub/c.go the oldest.
func grepTree(f *fixture) {
	f.t.Helper()
	f.setTime(f.file("a.txt", "alpha\nbeta\ngamma\nalpha beta\n"), 3)
	f.setTime(f.file("b.txt", "one\ntwo\nthree\n"), 2)
	f.setTime(f.file("sub/c.go", "package c\n\nfunc Alpha() {}\n// alpha\n"), 1)
}

func TestGrepModes(t *testing.T) {
	f := newFixture(t, Options{})
	grepTree(f)
	a, b, c := f.path("a.txt"), f.path("b.txt"), f.path("sub/c.go")
	tests := []struct {
		name  string
		input in
		want  string
	}{
		{"files, the default", in{"pattern": "alpha"}, a + "\n" + c + "\n"},
		{"files, by name", in{"pattern": "alpha", "output_mode": "files_with_matches"}, a + "\n" + c + "\n"},
		{"content", in{"pattern": "alpha", "output_mode": "content"},
			a + "\n1:alpha\n4:alpha beta\n\n" + c + "\n4:// alpha\n"},
		{"count", in{"pattern": "alpha", "output_mode": "count"},
			a + ":2\n" + c + ":1\n\nTotal: 3 matching lines in 2 files.\n"},
		{"count of one", in{"pattern": "gamma", "output_mode": "count"}, a + ":1\n\nTotal: 1 matching line in 1 file.\n"},
		{"ignoring case", in{"pattern": "alpha", "output_mode": "content", "-i": true},
			a + "\n1:alpha\n4:alpha beta\n\n" + c + "\n3:func Alpha() {}\n4:// alpha\n"},
		{"a real pattern", in{"pattern": `^(one|t\w+)$`, "output_mode": "content"}, b + "\n1:one\n2:two\n3:three\n"},
		{"anchors are per line", in{"pattern": "^beta$", "output_mode": "content"}, a + "\n2:beta\n"},
		{"without line numbers", in{"pattern": "alpha", "output_mode": "content", "-n": false},
			a + "\nalpha\nalpha beta\n\n" + c + "\n// alpha\n"},
		{"only the matches", in{"pattern": `al\w+|b\w+`, "output_mode": "content", "-o": true, "path": a},
			a + "\n1:alpha\n2:beta\n4:alpha\n4:beta\n"},
		{"only the matches, without numbers", in{"pattern": `[a-z]+a\b`, "output_mode": "content", "-o": true, "-n": false, "path": a},
			a + "\nalpha\nbeta\ngamma\nalpha\nbeta\n"},
		{"a single file", in{"pattern": "t", "path": b, "output_mode": "content"}, b + "\n2:two\n3:three\n"},
		{"a single file, files mode", in{"pattern": "t", "path": b}, b + "\n"},
		{"a directory below", in{"pattern": "a", "path": f.path("sub")}, c + "\n"},
		{"a relative path", in{"pattern": "a", "path": "sub"}, c + "\n"},
		{"braces need no escape", in{"pattern": `Alpha\(\) {}`, "path": c, "output_mode": "content"}, c + "\n3:func Alpha() {}\n"},
		{"an escaped metacharacter", in{"pattern": `Alpha\(\) \{\}`, "path": c, "output_mode": "content"}, c + "\n3:func Alpha() {}\n"},
		{"an empty line", in{"pattern": "^$", "output_mode": "content"}, c + "\n2:\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := f.ok(f.grep, tt.input); got != tt.want {
				t.Errorf("Grep(%v) =\n%s\nwant\n%s", tt.input, got, tt.want)
			}
		})
	}
}

func TestGrepMeta(t *testing.T) {
	f := newFixture(t, Options{})
	grepTree(f)
	a, c := f.path("a.txt"), f.path("sub/c.go")
	res := f.run(f.grep, in{"pattern": "alpha"})
	if got := metaOf(t, res, "mode"); got != "files_with_matches" {
		t.Errorf("mode = %v", got)
	}
	if got := metaOf(t, res, "filenames"); !reflect.DeepEqual(got, []string{a, c}) {
		t.Errorf("filenames = %v", got)
	}
	if metaOf(t, res, "numFiles") != 2 || metaOf(t, res, "totalFiles") != 2 {
		t.Errorf("Meta = %v", res.Meta)
	}
	if _, ok := res.Meta["appliedOffset"]; ok {
		t.Error("appliedOffset set though no offset was asked for")
	}
	if got := metaOf(t, res, "appliedLimit"); got != 250 {
		t.Errorf("appliedLimit = %v, want the default 250", got)
	}
	res = f.run(f.grep, in{"pattern": "alpha", "output_mode": "content", "head_limit": 2, "offset": 1})
	if metaOf(t, res, "mode") != "content" || metaOf(t, res, "numLines") != 2 || metaOf(t, res, "totalLines") != 3 ||
		metaOf(t, res, "appliedLimit") != 2 || metaOf(t, res, "appliedOffset") != 1 {
		t.Errorf("Meta = %v", res.Meta)
	}
	res = f.run(f.grep, in{"pattern": "alpha", "output_mode": "count", "head_limit": 1})
	if metaOf(t, res, "mode") != "count" || metaOf(t, res, "numFiles") != 2 || metaOf(t, res, "numMatches") != 3 {
		t.Errorf("Meta = %v", res.Meta)
	}
	res = f.run(f.grep, in{"pattern": "alpha", "head_limit": 0})
	if _, ok := res.Meta["appliedLimit"]; ok {
		t.Error("appliedLimit set though the call asked for no limit")
	}
	if res.Display != nil {
		t.Error("Grep set a Display")
	}
}

func TestGrepContext(t *testing.T) {
	f := newFixture(t, Options{})
	var lines []string
	for i := 1; i <= 12; i++ {
		lines = append(lines, fmt.Sprintf("l%d", i))
	}
	p := f.file("f.txt", strings.Join(lines, "\n")+"\n")
	tests := []struct {
		name  string
		input in
		want  string
	}{
		{"around", in{"pattern": "^l(3|9)$", "-C": 1}, "2-l2\n3:l3\n4-l4\n--\n8-l8\n9:l9\n10-l10\n"},
		{"around, by its other name", in{"pattern": "^l(3|9)$", "context": 1}, "2-l2\n3:l3\n4-l4\n--\n8-l8\n9:l9\n10-l10\n"},
		{"-C wins over context", in{"pattern": "^l3$", "-C": 1, "context": 3}, "2-l2\n3:l3\n4-l4\n"},
		{"before", in{"pattern": "^l(3|9)$", "-B": 2}, "1-l1\n2-l2\n3:l3\n--\n7-l7\n8-l8\n9:l9\n"},
		{"after", in{"pattern": "^l(3|9)$", "-A": 2}, "3:l3\n4-l4\n5-l5\n--\n9:l9\n10-l10\n11-l11\n"},
		{"before and after apart", in{"pattern": "^l6$", "-B": 1, "-A": 3}, "5-l5\n6:l6\n7-l7\n8-l8\n9-l9\n"},
		{"-A overrides one side of -C", in{"pattern": "^l6$", "-C": 2, "-A": 0}, "4-l4\n5-l5\n6:l6\n"},
		{"-B overrides one side of -C", in{"pattern": "^l6$", "-C": 2, "-B": 0}, "6:l6\n7-l7\n8-l8\n"},
		{"overlapping context runs together", in{"pattern": "^l(3|5)$", "-C": 1}, "2-l2\n3:l3\n4-l4\n5:l5\n6-l6\n"},
		{"context that just touches runs together", in{"pattern": "^l(3|6)$", "-C": 1}, "2-l2\n3:l3\n4-l4\n5-l5\n6:l6\n7-l7\n"},
		{"a gap of one line is a gap", in{"pattern": "^l(3|7)$", "-C": 1}, "2-l2\n3:l3\n4-l4\n--\n6-l6\n7:l7\n8-l8\n"},
		{"a match inside another's context", in{"pattern": "^l(3|4)$", "-A": 2}, "3:l3\n4:l4\n5-l5\n6-l6\n"},
		{"at the top", in{"pattern": "^l1$", "-C": 2}, "1:l1\n2-l2\n3-l3\n"},
		{"at the bottom", in{"pattern": "^l12$", "-C": 2}, "10-l10\n11-l11\n12:l12\n"},
		{"more context than file", in{"pattern": "^l6$", "-C": 100}, "1-l1\n2-l2\n3-l3\n4-l4\n5-l5\n6:l6\n7-l7\n8-l8\n9-l9\n10-l10\n11-l11\n12-l12\n"},
		{"no context, no separators", in{"pattern": "^l(3|9)$"}, "3:l3\n9:l9\n"},
		{"without numbers", in{"pattern": "^l(3|9)$", "-C": 1, "-n": false}, "l2\nl3\nl4\n--\nl8\nl9\nl10\n"},
		{"only the matches, context ignored", in{"pattern": "l[39]$", "-C": 1, "-o": true}, "3:l3\n9:l9\n"},
		{"numbers as strings", in{"pattern": "^l6$", "-A": "1", "-B": "1"}, "5-l5\n6:l6\n7-l7\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.input["output_mode"] = "content"
			tt.input["path"] = p
			if got, want := f.ok(f.grep, tt.input), p+"\n"+tt.want; got != want {
				t.Errorf("Grep(%v) =\n%s\nwant\n%s", tt.input, got, want)
			}
		})
	}
	// Context does not change which files match or how many lines do.
	if got := f.ok(f.grep, in{"pattern": "^l3$", "path": p, "-C": 5}); got != p+"\n" {
		t.Errorf("files mode with context = %q", got)
	}
	if got := f.ok(f.grep, in{"pattern": "^l(3|9)$", "path": p, "-C": 5, "output_mode": "count"}); !strings.HasPrefix(got, p+":2\n") {
		t.Errorf("count mode with context = %q", got)
	}
	// Each file starts afresh: no separator and no context across files.
	q := f.file("g.txt", "x\nl3\ny\n")
	f.setTime(p, 2)
	f.setTime(q, 1)
	got := f.ok(f.grep, in{"pattern": "^l3$", "-C": 1, "output_mode": "content"})
	want := p + "\n2-l2\n3:l3\n4-l4\n\n" + q + "\n1-x\n2:l3\n3-y\n"
	if got != want {
		t.Errorf("two files =\n%s\nwant\n%s", got, want)
	}
}

func TestGrepMultiline(t *testing.T) {
	f := newFixture(t, Options{})
	p := f.file("m.txt", "start\nfunc a() {\n\treturn 1\n}\nmiddle\nfunc b() {\n\treturn 2\n}\nend\n")
	tests := []struct {
		name  string
		input in
		want  string
	}{
		{"not across lines by default", in{"pattern": `\{\n\treturn`, "output_mode": "content"},
			"No matches found in " + p + ". The pattern has a line break in it, and a match lies within one line unless multiline is true.\n"},
		{"across lines", in{"pattern": `\{\n\treturn 1`, "output_mode": "content", "multiline": true}, p + "\n2:func a() {\n3:\treturn 1\n"},
		{"dot does not cross a line break", in{"pattern": `func a.*return`, "output_mode": "content", "multiline": true}, "No matches found in " + p + ".\n"},
		{"unless the pattern says so", in{"pattern": `(?s)func a.*?\}`, "output_mode": "content", "multiline": true}, p + "\n2:func a() {\n3:\treturn 1\n4:}\n"},
		{"anchors still mean line start and end", in{"pattern": `^middle$`, "output_mode": "content", "multiline": true}, p + "\n5:middle\n"},
		{"two matches", in{"pattern": `\{\n\treturn \d`, "output_mode": "content", "multiline": true}, p + "\n2:func a() {\n3:\treturn 1\n6:func b() {\n7:\treturn 2\n"},
		{"with context", in{"pattern": `\{\n\treturn \d`, "output_mode": "content", "multiline": true, "-C": 1},
			p + "\n1-start\n2:func a() {\n3:\treturn 1\n4-}\n5-middle\n6:func b() {\n7:\treturn 2\n8-}\n"},
		{"with context and a gap", in{"pattern": `start|end`, "output_mode": "content", "multiline": true, "-A": 1, "-B": 1},
			p + "\n1:start\n2-func a() {\n--\n8-}\n9:end\n"},
		{"count counts matches", in{"pattern": `\{\n\treturn \d`, "output_mode": "count", "multiline": true}, p + ":2\n\nTotal: 2 matches in 1 file.\n"},
		{"files", in{"pattern": `1\n\}\nmiddle`, "multiline": true}, p + "\n"},
		{"only the matches", in{"pattern": `return \d\n\}`, "output_mode": "content", "multiline": true, "-o": true}, p + "\n3:return 1\n4:}\n7:return 2\n8:}\n"},
		{"a match that ends with a line break", in{"pattern": `middle\n`, "output_mode": "content", "multiline": true}, p + "\n5:middle\n"},
		{"ignoring case", in{"pattern": `START\nFUNC`, "output_mode": "content", "multiline": true, "-i": true}, p + "\n1:start\n2:func a() {\n"},
		// Nothing is after the last line break: no empty line is there to
		// match (the search-tools review of 2026-11-20).
		{"no empty line after the last", in{"pattern": `^$`, "multiline": true}, "No matches found in " + p + ".\n"},
		{"no blank line after the last", in{"pattern": `^\s*$`, "output_mode": "count", "multiline": true}, "No matches found in " + p + ".\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.input["path"] = p
			if got := f.ok(f.grep, tt.input); got != tt.want {
				t.Errorf("Grep(%v) =\n%s\nwant\n%s", tt.input, got, tt.want)
			}
		})
	}
	t.Run("an empty line is found", func(t *testing.T) {
		q := f.file("gap.txt", "a\n\nb\n")
		if got, want := f.ok(f.grep, in{"pattern": `^$`, "output_mode": "content", "multiline": true, "path": q}), q+"\n2:\n"; got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})
	t.Run("CRLF files", func(t *testing.T) {
		q := f.file("crlf.txt", "one\r\ntwo\r\nthree\r\n")
		got := f.ok(f.grep, in{"pattern": `one\ntwo`, "output_mode": "content", "multiline": true, "path": q})
		if want := q + "\n1:one\n2:two\n"; got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})
	t.Run("a file too large to hold is passed over and said so", func(t *testing.T) {
		small := newFixture(t, Options{GrepMaxMultilineBytes: 64})
		small.file("big.txt", strings.Repeat("needle in a big file\n", 10))
		small.file("little.txt", "needle\n")
		got := small.ok(small.grep, in{"pattern": "needle", "multiline": true})
		mustContain(t, got, small.path("little.txt")+"\n", "1 file larger than 64 bytes was not searched")
		mustNotContain(t, got, "big.txt")
		// Line by line there is no such limit.
		got = small.ok(small.grep, in{"pattern": "needle"})
		mustContain(t, got, small.path("big.txt"))
	})
}

func TestGrepFilters(t *testing.T) {
	f := newFixture(t, Options{})
	for i, name := range []string{
		"main.go", "main_test.go", "lib/util.go", "lib/util.py", "lib/types.pyi", "web/app.ts", "web/view.tsx",
		"web/min/app.min.js", "docs/readme.md", "Makefile", "Dockerfile.dev", "data.json",
	} {
		f.setTime(f.file(name, "needle\n"), 100-i)
	}
	found := func(input in) []string {
		t.Helper()
		input["pattern"] = "needle"
		text := f.ok(f.grep, input)
		var out []string
		for _, line := range strings.Split(strings.TrimSpace(text), "\n") {
			if strings.HasPrefix(line, f.dir+"/") {
				out = append(out, strings.TrimPrefix(line, f.dir+"/"))
			}
		}
		return out
	}
	tests := []struct {
		name  string
		input in
		want  []string
	}{
		{"glob on the name", in{"glob": "*.go"}, []string{"main.go", "main_test.go", "lib/util.go"}},
		{"glob with alternatives", in{"glob": "*.{ts,tsx}"}, []string{"web/app.ts", "web/view.tsx"}},
		{"glob on the path", in{"glob": "lib/*.go"}, []string{"lib/util.go"}},
		{"glob at any depth", in{"glob": "**/*.py"}, []string{"lib/util.py"}},
		{"glob under a directory", in{"glob": "web/**"}, []string{"web/app.ts", "web/view.tsx", "web/min/app.min.js"}},
		{"glob with ./", in{"glob": "./lib/*.py"}, []string{"lib/util.py"}},
		{"glob with a leading slash", in{"glob": "/lib/*.py"}, []string{"lib/util.py"}},
		{"glob turned round", in{"glob": "!*.go"}, []string{"lib/util.py", "lib/types.pyi", "web/app.ts", "web/view.tsx", "web/min/app.min.js", "docs/readme.md", "Makefile", "Dockerfile.dev", "data.json"}},
		{"glob turned round on a path", in{"glob": "!web/**"}, []string{"main.go", "main_test.go", "lib/util.go", "lib/util.py", "lib/types.pyi", "docs/readme.md", "Makefile", "Dockerfile.dev", "data.json"}},
		{"glob on a whole name", in{"glob": "Makefile"}, []string{"Makefile"}},
		{"glob is relative to the path", in{"glob": "*.go", "path": f.path("lib")}, []string{"lib/util.go"}},
		{"type go", in{"type": "go"}, []string{"main.go", "main_test.go", "lib/util.go"}},
		{"type py", in{"type": "py"}, []string{"lib/util.py", "lib/types.pyi"}},
		{"type python", in{"type": "python"}, []string{"lib/util.py", "lib/types.pyi"}},
		{"type ts", in{"type": "ts"}, []string{"web/app.ts", "web/view.tsx"}},
		{"type js", in{"type": "js"}, []string{"web/min/app.min.js"}},
		{"type by another name", in{"type": "javascript"}, []string{"web/min/app.min.js"}},
		{"type in capitals", in{"type": "Go"}, []string{"main.go", "main_test.go", "lib/util.go"}},
		{"type md", in{"type": "md"}, []string{"docs/readme.md"}},
		{"type make", in{"type": "make"}, []string{"Makefile"}},
		{"type docker", in{"type": "docker"}, []string{"Dockerfile.dev"}},
		{"type json", in{"type": "json"}, []string{"data.json"}},
		{"type and glob together", in{"type": "go", "glob": "*_test.go"}, []string{"main_test.go"}},
		{"type and glob that exclude each other", in{"type": "go", "glob": "*.py"}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := found(tt.input); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("found %v, want %v", got, tt.want)
			}
		})
	}
	t.Run("nothing passes the filter", func(t *testing.T) {
		got := f.ok(f.grep, in{"pattern": "needle", "glob": "*.ts,*.tsx"})
		mustContain(t, got, "No matches found: no files were searched under "+f.dir, `None of the 12 files there matches glob "*.ts,*.tsx"`)
		got = f.ok(f.grep, in{"pattern": "needle", "type": "rust", "glob": "*.go"})
		mustContain(t, got, `matches glob "*.go" and type "rust"`)
	})
	t.Run("a file named outright that the filter leaves out", func(t *testing.T) {
		got := f.ok(f.grep, in{"pattern": "needle", "path": f.path("main.go"), "glob": "*.py"})
		mustContain(t, got, f.path("main.go")+" was not searched", `glob "*.py"`)
	})
	t.Run("every type in the table works", func(t *testing.T) {
		for _, name := range fileTypeNames() {
			ft, ok := lookupFileType(name)
			if !ok || len(ft.patterns) != len(fileTypes[name]) || len(ft.patterns) == 0 {
				t.Errorf("type %q: found %v, %d of %d patterns usable", name, ok, len(ft.patterns), len(fileTypes[name]))
			}
		}
		for alias, name := range fileTypeAliases {
			if _, ok := fileTypes[name]; !ok {
				t.Errorf("alias %q points at %q, which is not a type", alias, name)
			}
			if _, ok := fileTypes[alias]; ok {
				t.Errorf("%q is both a type and another name for one", alias)
			}
		}
		for name, want := range map[string]string{"x.gno": "gno", "go.mod": "gomod", "CMakeLists.txt": "cmake", "a.rs": "rust", "script.sh": "sh", ".zshrc": "sh", "a.yml": "yaml", "a.h": "c"} {
			ft, _ := lookupFileType(want)
			if !ft.matches(name) {
				t.Errorf("%s is not of type %s", name, want)
			}
		}
		if ft, _ := lookupFileType("go"); ft.matches("go.mod") || ft.matches("main.gox") || ft.matches("go") {
			t.Error("type go takes files that are not Go source")
		}
	})
}

func TestGrepWhatIsLeftOut(t *testing.T) {
	make := func(f *fixture) {
		f.file(".gitignore", "ignored.txt\nbuild/\n")
		f.file(".git/HEAD", "needle\n")
		f.file("seen.txt", "needle\n")
		f.file("ignored.txt", "needle\n")
		f.file("build/out.txt", "needle\n")
		f.file(".hidden.txt", "needle\n")
		f.file(".config/settings.txt", "needle\n")
		f.file("binary.dat", "needle\x00\x01\x02\n")
		f.file("sub/.gitignore", "*.gen\n!keep.gen\n")
		f.file("sub/made.gen", "needle\n")
		f.file("sub/keep.gen", "needle\n")
	}
	names := func(f *fixture, input in) []string {
		t.Helper()
		var out []string
		for _, line := range strings.Split(f.ok(f.grep, input), "\n") {
			if strings.HasPrefix(line, f.dir+"/") {
				out = append(out, strings.TrimPrefix(line, f.dir+"/"))
			}
		}
		return sorted(out)
	}
	t.Run("by default", func(t *testing.T) {
		f := newFixture(t, Options{})
		make(f)
		if got, want := names(f, in{"pattern": "needle"}), []string{"seen.txt", "sub/keep.gen"}; !reflect.DeepEqual(got, want) {
			t.Errorf("found %v, want %v", got, want)
		}
		// Each of them is searched when its path is given.
		for _, name := range []string{"ignored.txt", "build/out.txt", ".hidden.txt", ".config/settings.txt", "sub/made.gen", ".git/HEAD"} {
			if got := f.ok(f.grep, in{"pattern": "needle", "path": f.path(name)}); got != f.path(name)+"\n" {
				t.Errorf("%s named outright: %q", name, got)
			}
		}
		// So is a directory that is ignored or hidden.
		if got, want := names(f, in{"pattern": "needle", "path": f.path("build")}), []string{"build/out.txt"}; !reflect.DeepEqual(got, want) {
			t.Errorf("an ignored directory named outright: %v", got)
		}
		if got, want := names(f, in{"pattern": "needle", "path": f.path(".config")}), []string{".config/settings.txt"}; !reflect.DeepEqual(got, want) {
			t.Errorf("a hidden directory named outright: %v", got)
		}
		// A binary file named outright is still not searched, and says so.
		got := f.ok(f.grep, in{"pattern": "needle", "path": f.path("binary.dat")})
		mustContain(t, got, f.path("binary.dat")+" looks like a binary file and was not searched")
		// Rules from above still hold below a directory that is searched.
		if got := names(f, in{"pattern": "needle", "path": f.path("sub")}); !reflect.DeepEqual(got, []string{"sub/keep.gen"}) {
			t.Errorf("below sub: %v", got)
		}
		// A glob reaches what it names, hidden or ignored, as ripgrep's
		// does; .git stays out, and a binary file is still not searched
		// (the search-tools review of 2026-11-20).
		for _, c := range []struct {
			glob string
			want []string
		}{
			{"ignored.txt", []string{"ignored.txt"}},
			{".hidden*", []string{".hidden.txt"}},
			{"*.gen", []string{"sub/keep.gen", "sub/made.gen"}},
			{"build/**", []string{"build/out.txt"}},
			{".config/*.txt", []string{".config/settings.txt"}},
			{"HEAD", nil},
			{"binary.dat", nil},
		} {
			if got := names(f, in{"pattern": "needle", "glob": c.glob}); !reflect.DeepEqual(got, c.want) {
				t.Errorf("glob %q found %v, want %v", c.glob, got, c.want)
			}
		}
	})
	t.Run("a glob that any directory could hold enters no hidden or ignored one", func(t *testing.T) {
		f := newFixture(t, Options{})
		f.file(".gitignore", "node_modules/\n")
		f.file("src/a.py", "needle\n")
		f.file(".venv/lib/site.py", "needle\n")
		f.file("node_modules/pkg/x.py", "needle\n")
		if got := names(f, in{"pattern": "needle", "glob": "**/*.py"}); !reflect.DeepEqual(got, []string{"src/a.py"}) {
			t.Errorf("glob **/*.py found %v, want only src/a.py", got)
		}
		// Spelled out, they are entered.
		if got := names(f, in{"pattern": "needle", "glob": "node_modules/**/*.py"}); !reflect.DeepEqual(got, []string{"node_modules/pkg/x.py"}) {
			t.Errorf("glob node_modules/**/*.py found %v", got)
		}
		if got := names(f, in{"pattern": "needle", "glob": ".venv/**"}); !reflect.DeepEqual(got, []string{".venv/lib/site.py"}) {
			t.Errorf("glob .venv/** found %v", got)
		}
	})
	t.Run("a negated glob leaves out directories too", func(t *testing.T) {
		f := newFixture(t, Options{})
		for _, name := range []string{"vendor/lib.go", "src/vendor/x.go", "src/a.go", "app.min.js", "app.js", "vendor.txt"} {
			f.file(name, "needle\n")
		}
		for _, c := range []struct {
			glob string
			want []string
		}{
			{"!vendor", []string{"app.js", "app.min.js", "src/a.go", "vendor.txt"}},
			{"!vendor/", []string{"app.js", "app.min.js", "src/a.go", "vendor.txt"}},
			{"!*.min.js", []string{"app.js", "src/a.go", "src/vendor/x.go", "vendor.txt", "vendor/lib.go"}},
			{"!src/vendor", []string{"app.js", "app.min.js", "src/a.go", "vendor.txt", "vendor/lib.go"}},
		} {
			if got := names(f, in{"pattern": "needle", "glob": c.glob}); !reflect.DeepEqual(got, c.want) {
				t.Errorf("glob %q found %v, want %v", c.glob, got, c.want)
			}
		}
	})
	t.Run("ignore rules off", func(t *testing.T) {
		f := newFixture(t, Options{GrepNoIgnore: true})
		make(f)
		if got, want := names(f, in{"pattern": "needle"}), []string{"build/out.txt", "ignored.txt", "seen.txt", "sub/keep.gen", "sub/made.gen"}; !reflect.DeepEqual(got, want) {
			t.Errorf("found %v, want %v", got, want)
		}
	})
	t.Run("hidden files searched", func(t *testing.T) {
		f := newFixture(t, Options{GrepHidden: true})
		make(f)
		// .git stays out even so; the ignore files themselves are hidden
		// files with no needle in them.
		if got, want := names(f, in{"pattern": "needle"}), []string{".config/settings.txt", ".hidden.txt", "seen.txt", "sub/keep.gen"}; !reflect.DeepEqual(got, want) {
			t.Errorf("found %v, want %v", got, want)
		}
	})
	t.Run("binary files searched", func(t *testing.T) {
		f := newFixture(t, Options{GrepBinary: true})
		make(f)
		if got, want := names(f, in{"pattern": "needle"}), []string{"binary.dat", "seen.txt", "sub/keep.gen"}; !reflect.DeepEqual(got, want) {
			t.Errorf("found %v, want %v", got, want)
		}
		got := f.ok(f.grep, in{"pattern": "needle", "path": f.path("binary.dat"), "output_mode": "content"})
		if want := f.path("binary.dat") + "\n1:needle" + replacement + "\x01\x02\n"; got != want {
			t.Errorf("content of a binary file = %q, want %q", got, want)
		}
	})
	t.Run("only binary files", func(t *testing.T) {
		f := newFixture(t, Options{})
		f.file("a.bin", "needle\x00")
		f.file("b.bin", "needle\x00")
		got := f.ok(f.grep, in{"pattern": "needle"})
		mustContain(t, got, "No matches found: no files were searched", "2 files were passed over as binary")
	})
	t.Run("links are not followed", func(t *testing.T) {
		f := newFixture(t, Options{})
		f.file("real/file.txt", "needle\n")
		f.symlink("real/file.txt", "link.txt")
		f.symlink("real", "dirlink")
		f.symlink("missing", "broken")
		if got, want := names(f, in{"pattern": "needle"}), []string{"real/file.txt"}; !reflect.DeepEqual(got, want) {
			t.Errorf("found %v, want %v", got, want)
		}
		// A link named as the place to search is followed.
		if got := f.ok(f.grep, in{"pattern": "needle", "path": f.path("link.txt")}); got != f.path("link.txt")+"\n" {
			t.Errorf("a linked file named outright: %q", got)
		}
		if got, want := names(f, in{"pattern": "needle", "path": f.path("dirlink")}), []string{"dirlink/file.txt"}; !reflect.DeepEqual(got, want) {
			t.Errorf("a linked directory named outright: %v", got)
		}
	})
}

func TestGrepPaging(t *testing.T) {
	f := newFixture(t, Options{})
	// Five files of four matching lines each; f1 is the newest.
	for i := 1; i <= 5; i++ {
		f.setTime(f.file(fmt.Sprintf("f%d.txt", i), "hit 1\nmiss\nhit 2\nhit 3\nmiss\nhit 4\n"), 10-i)
	}
	p := func(i int) string { return f.path(fmt.Sprintf("f%d.txt", i)) }
	tests := []struct {
		name  string
		input in
		want  string
	}{
		{"files: a limit", in{"pattern": "hit", "head_limit": 2},
			p(1) + "\n" + p(2) + "\n\n[Showing files 1-2 of 5. To see more, use offset=2.]\n"},
		{"files: limit and offset", in{"pattern": "hit", "head_limit": 2, "offset": 2},
			p(3) + "\n" + p(4) + "\n\n[Showing files 3-4 of 5. To see more, use offset=4.]\n"},
		{"files: the last page", in{"pattern": "hit", "head_limit": 2, "offset": 4},
			p(5) + "\n\n[Showing files 5-5 of 5: these are the last.]\n"},
		{"files: offset alone", in{"pattern": "hit", "offset": 3},
			p(4) + "\n" + p(5) + "\n\n[Showing files 4-5 of 5: these are the last.]\n"},
		{"files: a limit that takes them all", in{"pattern": "hit", "head_limit": 5},
			p(1) + "\n" + p(2) + "\n" + p(3) + "\n" + p(4) + "\n" + p(5) + "\n"},
		{"files: past the end", in{"pattern": "hit", "offset": 5},
			"[Nothing to show at offset 5: there are only 5 files. Use a smaller offset.]\n"},
		{"files: far past the end", in{"pattern": "hit", "offset": 99, "head_limit": 3},
			"[Nothing to show at offset 99: there are only 5 files. Use a smaller offset.]\n"},
		{"content: a limit inside a file", in{"pattern": "hit", "output_mode": "content", "head_limit": 3},
			p(1) + "\n1:hit 1\n3:hit 2\n4:hit 3\n\n[Showing result lines 1-3 of 20. To see more, use offset=3.]\n"},
		{"content: across two files", in{"pattern": "hit", "output_mode": "content", "head_limit": 3, "offset": 3},
			p(1) + "\n6:hit 4\n\n" + p(2) + "\n1:hit 1\n3:hit 2\n\n[Showing result lines 4-6 of 20. To see more, use offset=6.]\n"},
		{"content: starting at a file's first line", in{"pattern": "hit", "output_mode": "content", "head_limit": 2, "offset": 8},
			p(3) + "\n1:hit 1\n3:hit 2\n\n[Showing result lines 9-10 of 20. To see more, use offset=10.]\n"},
		{"content: the last lines", in{"pattern": "hit", "output_mode": "content", "offset": 18},
			p(5) + "\n4:hit 3\n6:hit 4\n\n[Showing result lines 19-20 of 20: these are the last.]\n"},
		{"content: past the end", in{"pattern": "hit", "output_mode": "content", "offset": 20},
			"[Nothing to show at offset 20: there are only 20 result lines. Use a smaller offset.]\n"},
		{"content: context lines count", in{"pattern": "hit 1", "output_mode": "content", "-A": 1, "head_limit": 3},
			p(1) + "\n1:hit 1\n2-miss\n\n" + p(2) + "\n1:hit 1\n\n[Showing result lines 1-3 of 10. To see more, use offset=3.]\n"},
		{"content: a page that starts inside a stretch has no separator before it", in{"pattern": "hit [14]", "output_mode": "content", "-A": 1, "offset": 1, "head_limit": 3},
			p(1) + "\n2-miss\n--\n6:hit 4\n\n" + p(2) + "\n1:hit 1\n\n[Showing result lines 2-4 of 15. To see more, use offset=4.]\n"},
		{"count: the total is of everything", in{"pattern": "hit", "output_mode": "count", "head_limit": 2, "offset": 1},
			p(2) + ":4\n" + p(3) + ":4\n\nTotal: 20 matching lines in 5 files.\n\n[Showing files 2-3 of 5. To see more, use offset=3.]\n"},
		{"count: past the end still gives the total", in{"pattern": "hit", "output_mode": "count", "offset": 9},
			"Total: 20 matching lines in 5 files.\n\n[Nothing to show at offset 9: there are only 5 files. Use a smaller offset.]\n"},
		{"no limit", in{"pattern": "hit 4", "output_mode": "content", "head_limit": 0},
			p(1) + "\n6:hit 4\n\n" + p(2) + "\n6:hit 4\n\n" + p(3) + "\n6:hit 4\n\n" + p(4) + "\n6:hit 4\n\n" + p(5) + "\n6:hit 4\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := f.ok(f.grep, tt.input); got != tt.want {
				t.Errorf("Grep(%v) =\n%s\nwant\n%s", tt.input, got, tt.want)
			}
		})
	}
	t.Run("no default limit", func(t *testing.T) {
		open := newFixture(t, Options{GrepHeadLimit: -1})
		open.file("many.txt", strings.Repeat("hit\n", 400))
		got := open.ok(open.grep, in{"pattern": "hit", "output_mode": "content"})
		mustContain(t, got, "400:hit\n")
		mustNotContain(t, got, "Showing")
		got = open.ok(open.grep, in{"pattern": "hit", "output_mode": "content", "head_limit": 2})
		mustContain(t, got, "Showing result lines 1-2 of 400.")
	})
	t.Run("the default limit", func(t *testing.T) {
		small := newFixture(t, Options{GrepHeadLimit: 4})
		small.file("many.txt", strings.Repeat("hit\n", 9))
		got := small.ok(small.grep, in{"pattern": "hit", "output_mode": "content"})
		mustContain(t, got, "4:hit\n\n[Showing result lines 1-4 of 9. To see more, use offset=4.]")
		mustNotContain(t, got, "5:hit")
		got = small.ok(small.grep, in{"pattern": "hit", "output_mode": "content", "head_limit": 0})
		mustContain(t, got, "9:hit\n")
		mustNotContain(t, got, "Showing")
	})
}

func TestGrepOutputSizeCap(t *testing.T) {
	f := newFixture(t, Options{GrepMaxOutputBytes: 300})
	line := strings.Repeat("x", 40)
	p := f.file("wide.txt", strings.Repeat(line+"\n", 30))
	got := f.ok(f.grep, in{"pattern": "x", "output_mode": "content", "head_limit": 0})
	body, note, _ := strings.Cut(got, "\n[")
	if len(body) > 300 {
		t.Errorf("the result is %d bytes before its note; the cap is 300", len(body))
	}
	shown := strings.Count(body, line)
	if shown < 4 || shown > 7 {
		t.Errorf("showed %d lines", shown)
	}
	mustContain(t, note, fmt.Sprintf("Showing result lines 1-%d of 30.", shown), "stopped at the 300 bytes one call may return", fmt.Sprintf("offset=%d", shown))
	// The offset in the note continues exactly where this page stopped.
	next := f.ok(f.grep, in{"pattern": "x", "output_mode": "content", "head_limit": 0, "offset": shown})
	mustContain(t, next, fmt.Sprintf("%s\n%d:%s\n", p, shown+1, line))

	// One entry is always shown, however long.
	one := newFixture(t, Options{GrepMaxOutputBytes: 10})
	q := one.file("a.txt", "a long enough line\n")
	if got := one.ok(one.grep, in{"pattern": "long", "output_mode": "content"}); got != q+"\n1:a long enough line\n" {
		t.Errorf("got %q", got)
	}
	// Lists of files are capped the same way.
	many := newFixture(t, Options{GrepMaxOutputBytes: 400})
	for i := 0; i < 40; i++ {
		many.file(fmt.Sprintf("file-%02d.txt", i), "hit\n")
	}
	got = many.ok(many.grep, in{"pattern": "hit"})
	body, note, _ = strings.Cut(got, "\n[")
	if len(body) > 400 || !strings.Contains(note, "of 40.") || !strings.Contains(note, "stopped at the 400 bytes") {
		t.Errorf("a long list of files: %d bytes, note %q", len(body), note)
	}
}

func TestGrepLongLines(t *testing.T) {
	f := newFixture(t, Options{GrepMaxColumns: 40})
	long := strings.Repeat("a", 100) + "NEEDLE" + strings.Repeat("b", 100)
	p := f.file("long.txt", "short NEEDLE\n"+long+"\nNEEDLE "+strings.Repeat("c", 100)+"\n"+strings.Repeat("d", 60)+"\n")
	got := f.ok(f.grep, in{"pattern": "NEEDLE", "output_mode": "content", "-A": 1})
	want := p + "\n1:short NEEDLE\n" +
		"2:[90 bytes ...] " + strings.Repeat("a", 10) + "NEEDLE" + strings.Repeat("b", 24) + " [... 76 more bytes]\n" +
		"3:NEEDLE " + strings.Repeat("c", 33) + " [... 67 more bytes]\n" +
		"4-" + strings.Repeat("d", 40) + " [... 20 more bytes]\n"
	if got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
	// A cut never falls inside a character.
	q := f.file("wide.txt", strings.Repeat("日", 30)+"NEEDLE"+strings.Repeat("本", 30)+"\n")
	got = f.ok(f.grep, in{"pattern": "NEEDLE", "output_mode": "content", "path": q})
	if strings.Contains(got, replacement) {
		t.Errorf("a character was cut in two: %q", got)
	}
	mustContain(t, got, "NEEDLE")
}

func TestGrepVeryLongLineIsSearchedInPieces(t *testing.T) {
	f := newFixture(t, Options{GrepMaxLineBytes: 1000})
	// Line 2 is 3500 bytes long with a needle in its third piece.
	p := f.file("pieces.txt", "first\n"+strings.Repeat("x", 2500)+"NEEDLE"+strings.Repeat("y", 994)+"\nthird NEEDLE\n")
	got := f.ok(f.grep, in{"pattern": "NEEDLE", "output_mode": "count"})
	mustContain(t, got, p+":2\n")
	got = f.ok(f.grep, in{"pattern": "NEEDLE", "output_mode": "content", "-o": true})
	if want := p + "\n2:NEEDLE\n3:NEEDLE\n"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	got = f.ok(f.grep, in{"pattern": "^third", "output_mode": "content"})
	if want := p + "\n3:third NEEDLE\n"; got != want {
		t.Errorf("line numbers after a long line: got %q, want %q", got, want)
	}
	// A match in two pieces of one line counts that line once, and shows
	// it once (the search-tools review of 2026-11-20).
	q := f.file("twice.txt", strings.Repeat("x", 500)+"NEEDLE"+strings.Repeat("y", 1500)+"NEEDLE\n")
	got = f.ok(f.grep, in{"pattern": "NEEDLE", "output_mode": "count", "path": q})
	mustContain(t, got, q+":1\n")
	got = f.ok(f.grep, in{"pattern": "NEEDLE", "output_mode": "content", "path": q})
	if n := strings.Count("\n"+got, "\n1:"); n != 1 {
		t.Errorf("the line was shown %d times: %q", n, got)
	}
}

func TestGrepTextOddities(t *testing.T) {
	f := newFixture(t, Options{})
	t.Run("CRLF", func(t *testing.T) {
		p := f.file("crlf.txt", "first\r\nsecond\r\nthird\r\n")
		got := f.ok(f.grep, in{"pattern": "d$", "output_mode": "content", "path": p})
		if want := p + "\n2:second\n3:third\n"; got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})
	t.Run("byte order mark", func(t *testing.T) {
		p := f.file("bom.txt", utf8BOM+"first\nsecond\n")
		got := f.ok(f.grep, in{"pattern": "^first", "output_mode": "content", "path": p})
		if want := p + "\n1:first\n"; got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})
	t.Run("no line break at the end", func(t *testing.T) {
		p := f.file("noeol.txt", "one\ntwo")
		got := f.ok(f.grep, in{"pattern": "two$", "output_mode": "content", "path": p})
		if want := p + "\n2:two\n"; got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})
	t.Run("an empty file", func(t *testing.T) {
		p := f.file("empty.txt", "")
		for _, input := range []in{{"pattern": "^"}, {"pattern": "^", "multiline": true}, {"pattern": "x*", "output_mode": "count"}} {
			input["path"] = p
			if got := f.ok(f.grep, input); got != "No matches found in "+p+".\n" {
				t.Errorf("Grep(%v) = %q", input, got)
			}
		}
	})
	t.Run("bytes that are not text", func(t *testing.T) {
		p := f.file("latin1.txt", "caf\xe9 needle\n")
		got := f.ok(f.grep, in{"pattern": "needle", "output_mode": "content", "path": p})
		if want := p + "\n1:caf" + replacement + " needle\n"; got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})
	t.Run("unicode", func(t *testing.T) {
		p := f.file("uni.txt", "naïve café\n日本語のテキスト\n")
		got := f.ok(f.grep, in{"pattern": `caf.$|本.の`, "output_mode": "content", "-o": true, "path": p})
		if want := p + "\n1:café\n2:本語の\n"; got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})
	t.Run("a pattern that matches nothing but emptiness", func(t *testing.T) {
		p := f.file("some.txt", "a\nb\n")
		got := f.ok(f.grep, in{"pattern": "z*", "output_mode": "count", "path": p})
		mustContain(t, got, p+":2\n")
		// With -o there is no text to show for an empty match.
		got = f.ok(f.grep, in{"pattern": "z*", "output_mode": "content", "-o": true, "path": p})
		if want := "The pattern matched in 1 file, but only empty text, so there is nothing to show.\n"; got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})
}

func TestGrepRefusals(t *testing.T) {
	f := newFixture(t, Options{})
	grepTree(f)
	tests := []struct {
		name  string
		input in
		wants []string
	}{
		{"no pattern", in{}, []string{"pattern is required"}},
		{"empty pattern", in{"pattern": ""}, []string{"pattern is empty"}},
		{"pattern of the wrong kind", in{"pattern": []string{"a"}}, []string{"pattern must be a string"}},
		{"unknown argument", in{"pattern": "a", "-v": true}, []string{`takes no argument called "-v"`}},
		{"bad pattern", in{"pattern": "a(b"}, []string{`the pattern "a(b" is not a regular expression Go accepts`, "missing closing )"}},
		{"bad repetition", in{"pattern": "*a"}, []string{"is not a regular expression Go accepts", "missing argument to repetition operator"}},
		{"look-ahead", in{"pattern": "foo(?=bar)"}, []string{"is not a regular expression Go accepts", "no look-ahead, look-behind or backreferences"}},
		{"look-behind", in{"pattern": "(?<!foo)bar"}, []string{"no look-ahead, look-behind or backreferences"}},
		{"backreference", in{"pattern": `(a)\1`}, []string{"no look-ahead, look-behind or backreferences"}},
		{"bad mode", in{"pattern": "a", "output_mode": "lines"}, []string{"output_mode must be files_with_matches, content or count", `"lines"`}},
		{"-o without content", in{"pattern": "a", "-o": true}, []string{"-o shows the text that matched", "needs output_mode content", "files_with_matches"}},
		{"-o with count", in{"pattern": "a", "-o": true, "output_mode": "count"}, []string{"needs output_mode content"}},
		{"unknown type", in{"pattern": "a", "type": "klingon"}, []string{`"klingon" is not a file type it knows`, "go, ", "py, ", "use glob"}},
		{"bad glob", in{"pattern": "a", "glob": "*.{go"}, []string{`the glob "*.{go" cannot be used`, "a { has no closing }"}},
		{"empty glob after its mark", in{"pattern": "a", "glob": "!"}, []string{"names no files"}},
		{"negative context", in{"pattern": "a", "-C": -1}, []string{"-C must not be negative"}},
		{"too much context", in{"pattern": "a", "-C": 5000, "output_mode": "content"}, []string{"5000 lines of context is more than the 1000 a call may ask for", "read it with Read"}},
		{"too much context after", in{"pattern": "a", "-A": 1001}, []string{"1001 lines of context", "-A"}},
		{"context of the wrong kind", in{"pattern": "a", "-A": "lots"}, []string{"-A must be a whole number"}},
		{"negative limit", in{"pattern": "a", "head_limit": -1}, []string{"head_limit must not be negative"}},
		{"negative offset", in{"pattern": "a", "offset": -3}, []string{"offset must not be negative"}},
		{"flag of the wrong kind", in{"pattern": "a", "-i": "sometimes"}, []string{"-i must be true or false"}},
		{"multiline of the wrong kind", in{"pattern": "a", "multiline": 1}, []string{"multiline must be true or false"}},
		{"tilde", in{"pattern": "a", "path": "~/x"}, []string{"not expanded"}},
		{"NUL in the path", in{"pattern": "a", "path": "x\x00y"}, []string{"NUL byte"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f.fails(f.grep, tt.input, tt.wants...)
			if _, err := f.describe(f.grep, tt.input); err == nil {
				t.Error("Describe accepted the call")
			}
		})
	}
	t.Run("a path that is not there", func(t *testing.T) {
		input := in{"pattern": "a", "path": f.path("missing")}
		f.fails(f.grep, input, f.path("missing")+" does not exist", "nothing to search")
		// Whether the path is there is not for Describe to say.
		act, err := f.describe(f.grep, input)
		if err != nil || act.Kind != core.ActionRead || len(act.Paths) != 1 || act.Paths[0] != f.path("missing") {
			t.Errorf("Describe = %+v, %v", act, err)
		}
	})
	t.Run("flags given as words", func(t *testing.T) {
		got := f.ok(f.grep, in{"pattern": "ALPHA", "-i": "true", "multiline": "false", "output_mode": "count"})
		mustContain(t, got, "Total: 4 matching lines in 2 files.")
	})
}

func TestGrepNoMatches(t *testing.T) {
	f := newFixture(t, Options{})
	grepTree(f)
	res := f.run(f.grep, in{"pattern": "zzz"})
	if res.IsError {
		t.Fatal("no matches is not an error")
	}
	if got, want := core.TextOf(res.Content), "No matches found. 3 files were searched under "+f.dir+".\n"; got != want {
		t.Errorf("text = %q, want %q", got, want)
	}
	if metaOf(t, res, "numFiles") != 0 {
		t.Errorf("Meta = %v", res.Meta)
	}
	if got := f.ok(f.grep, in{"pattern": "zzz", "path": f.path("b.txt")}); got != "No matches found in "+f.path("b.txt")+".\n" {
		t.Errorf("text = %q", got)
	}
	empty := f.mkdir("empty")
	got := f.ok(f.grep, in{"pattern": "a", "path": empty})
	mustContain(t, got, "No matches found: no files were searched under "+empty, "It holds no files")
	got = f.ok(f.grep, in{"pattern": "zzz", "path": f.path("sub"), "output_mode": "count"})
	mustContain(t, got, "No matches found. 1 file was searched under "+f.path("sub"))
}

func TestGrepDescribe(t *testing.T) {
	f := newFixture(t, Options{})
	grepTree(f)
	act, err := f.describe(f.grep, in{"pattern": "alpha"})
	if err != nil {
		t.Fatal(err)
	}
	if act.Kind != core.ActionRead || len(act.Paths) != 1 || act.Paths[0] != f.dir || act.Summary != "Search for alpha in "+f.dir || act.Preview != nil {
		t.Errorf("action = %+v", act)
	}
	act, _ = f.describe(f.grep, in{"pattern": "a", "path": f.path("a.txt"), "glob": "*.txt", "type": "txt"})
	if act.Paths[0] != f.path("a.txt") || act.Summary != `Search for a in `+f.path("a.txt")+` (glob "*.txt" and type "txt")` {
		t.Errorf("action = %+v", act)
	}
	link := f.symlink("sub", "link")
	act, _ = f.describe(f.grep, in{"pattern": "a", "path": link})
	if len(act.Paths) != 2 || act.Paths[0] != link || act.Paths[1] != f.path("sub") {
		t.Errorf("paths = %v", act.Paths)
	}
}

func TestGrepWithOneWorker(t *testing.T) {
	// The answer does not depend on how many goroutines search.
	var want string
	for _, workers := range []int{1, 2, 7} {
		f := newFixture(t, Options{SearchWorkers: workers})
		grepTree(f)
		got := f.ok(f.grep, in{"pattern": "a", "output_mode": "content", "-C": 1})
		got = strings.ReplaceAll(got, f.dir, "DIR")
		if want == "" {
			want = got
		} else if got != want {
			t.Errorf("with %d workers:\n%s\nwith one:\n%s", workers, got, want)
		}
		names, _ := f.globbed(in{"pattern": "**"})
		if len(names) != 3 {
			t.Errorf("with %d workers Glob listed %v", workers, names)
		}
	}
}

func TestGrepManyFiles(t *testing.T) {
	f := newFixture(t, Options{})
	const dirs, perDir = 40, 25
	want := 0
	for d := 0; d < dirs; d++ {
		for i := 0; i < perDir; i++ {
			content := "nothing here\n"
			if (d+i)%3 == 0 {
				content = "a needle\nand\nanother needle\n"
				want++
			}
			f.file(fmt.Sprintf("d%02d/f%02d.txt", d, i), content)
		}
	}
	for round := 0; round < 5; round++ {
		res := f.run(f.grep, in{"pattern": "needle", "head_limit": 0})
		names := metaOf(t, res, "filenames").([]string)
		if len(names) != want || metaOf(t, res, "totalFiles") != want {
			t.Fatalf("round %d: found %d files (total %v), want %d", round, len(names), metaOf(t, res, "totalFiles"), want)
		}
		seen := make(map[string]bool)
		for _, n := range names {
			if seen[n] {
				t.Fatalf("%s listed twice", n)
			}
			seen[n] = true
		}
		res = f.run(f.grep, in{"pattern": "needle", "output_mode": "count", "head_limit": 1})
		if got := metaOf(t, res, "numMatches"); got != 2*want {
			t.Fatalf("round %d: counted %v matching lines, want %d", round, got, 2*want)
		}
	}
	got := f.ok(f.grep, in{"pattern": "zzz"})
	mustContain(t, got, fmt.Sprintf("No matches found. %d files were searched", dirs*perDir))
}

func TestGrepStopsWhenCancelled(t *testing.T) {
	f := newFixture(t, Options{})
	grepTree(f)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, input := range []in{{"pattern": "a"}, {"pattern": "a", "path": f.path("a.txt")}, {"pattern": "a", "output_mode": "content"}} {
		_, err := f.grep.Run(ctx, call(input), f.env)
		if !errors.Is(err, context.Canceled) {
			t.Errorf("Grep(%v): err = %v, want context.Canceled", input, err)
		}
	}
}

func TestGrepStopsInTheMiddleOfABigFile(t *testing.T) {
	f := newFixture(t, Options{})
	p := f.file("big.txt", strings.Repeat("just a line of text that does not match\n", 400000))
	ctx, cancel := context.WithCancel(context.Background())
	// The pattern never matches, so the search can only end at the end of
	// the file or by being stopped.
	done := make(chan error, 1)
	go func() {
		_, err := f.grep.Run(ctx, call(in{"pattern": "needle", "path": p}), f.env)
		done <- err
	}()
	cancel()
	select {
	case err := <-done:
		// Either it was stopped, or it had already finished: both are
		// fine, but it must not hang.
		if err != nil && !errors.Is(err, context.Canceled) {
			t.Errorf("err = %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("Grep did not stop")
	}
}

func TestGrepTimeLimit(t *testing.T) {
	f := newFixture(t, Options{SearchTimeout: time.Nanosecond})
	grepTree(f)
	msg := f.fails(f.grep, in{"pattern": "alpha"}, "Grep ran out of time", "there is no result", "says nothing about whether there are matches")
	mustNotContain(t, msg, "a.txt")
	f.fails(f.grep, in{"pattern": "alpha", "path": f.path("a.txt")}, "ran out of time")
}

func TestGrepFileThatVanishes(t *testing.T) {
	// A file can go away between being found and being read. That is not
	// an error; the search goes on.
	f := newFixture(t, Options{})
	grepTree(f)
	spec := &scanSpec{mode: modeFiles, maxLine: 1 << 20, maxColumns: 500}
	if _, err := spec.scan(context.Background(), f.path("gone.txt"), nil); err == nil {
		t.Error("scanning a missing file gave no error")
	}
	os.Remove(f.path("b.txt"))
	if got := f.ok(f.grep, in{"pattern": "alpha"}); got != f.path("a.txt")+"\n"+f.path("sub/c.go")+"\n" {
		t.Errorf("got %q", got)
	}
}

func TestGrepLiteralShortCutChangesNothing(t *testing.T) {
	// A pattern that begins with plain text lets Grep pass over lines and
	// files without that text. The answer must be the one the same
	// pattern gives when it is written so that no such short cut applies.
	f := newFixture(t, Options{})
	var big, bigNone strings.Builder
	for i := 1; i <= 6000; i++ {
		fmt.Fprintf(&bigNone, "line %d with some filler text to make it longer\n", i)
		if i%997 == 0 || i == 1 || i == 6000 {
			fmt.Fprintf(&big, "line %d has a needle %d in it\n", i, i)
		} else if i%1499 == 0 {
			fmt.Fprintf(&big, "LINE %d HAS A NeEdLe %d IN IT\n", i, i)
		} else {
			fmt.Fprintf(&big, "line %d with some filler text to make it longer\n", i)
		}
	}
	if big.Len() < 3*lineReadSize {
		t.Fatalf("the big file is only %d bytes; it must be several reads long", big.Len())
	}
	f.setTime(f.file("big.txt", big.String()), 4)
	f.setTime(f.file("big-none.txt", bigNone.String()), 3)
	f.setTime(f.file("small.txt", "no\nneedle 7 here\nnope\nneedle\n"), 2)
	f.setTime(f.file("small-none.txt", "nothing\nto\nsee\n"), 1)
	f.setTime(f.file("crlf.txt", "a\r\nneedle 1\r\nb\r\n"), 0)

	pairs := [][2]string{
		{"needle", "[n]eedle"},
		{`needle \d+`, `[n]eedle \d+`},
		{`needle \d+ in it$`, `[n]eedle \d+ in it$`},
		{"line 1", "[l]ine 1"},
		{"line 5999 with", "[l]ine 5999 with"},
		{"needle$", "[n]eedle$"},
		{"absent", "[a]bsent"},
		{"nee", "ne{2}"},
		{"Needle", "(?:Needle|zzzz)"},
		{`NEEDLE \d+ in`, `(?:NEEDLE|zzzz) \d+ in`},
		{"has a", "(?:has|zzzz) a"},
	}
	variants := []in{
		{},
		{"output_mode": "count"},
		{"output_mode": "content"},
		{"output_mode": "content", "-C": 2},
		{"output_mode": "content", "-B": 1, "head_limit": 7, "offset": 3},
		{"output_mode": "content", "-o": true},
		{"output_mode": "content", "multiline": true, "-A": 1},
		{"output_mode": "count", "multiline": true},
		{"multiline": true},
		{"-i": true},
		{"-i": true, "output_mode": "count"},
		{"-i": true, "output_mode": "content", "-C": 1},
		{"-i": true, "output_mode": "content", "-o": true},
		{"-i": true, "output_mode": "count", "multiline": true},
	}
	for _, pair := range pairs {
		for _, variant := range variants {
			var got [2]string
			for i, pattern := range pair {
				input := in{"pattern": pattern}
				for k, v := range variant {
					input[k] = v
				}
				got[i] = f.ok(f.grep, input)
			}
			if got[0] != got[1] {
				t.Errorf("%q with %v gives\n%s\nbut %q, which means the same, gives\n%s", pair[0], variant, short(got[0], 1500), pair[1], short(got[1], 1500))
			}
		}
	}
	// And the answers are the right ones.
	got := f.ok(f.grep, in{"pattern": "NEEDLE", "output_mode": "count", "-i": true})
	want := f.path("big.txt") + ":12\n" + f.path("crlf.txt") + ":1\n" + f.path("small.txt") + ":2\n\nTotal: 15 matching lines in 3 files.\n"
	if got != want {
		t.Errorf("count ignoring case =\n%s\nwant\n%s", got, want)
	}
	got = f.ok(f.grep, in{"pattern": "needle", "output_mode": "count"})
	want = f.path("big.txt") + ":8\n" + f.path("crlf.txt") + ":1\n" + f.path("small.txt") + ":2\n\nTotal: 11 matching lines in 3 files.\n"
	if got != want {
		t.Errorf("count =\n%s\nwant\n%s", got, want)
	}
	got = f.ok(f.grep, in{"pattern": `needle \d+ in it$`, "output_mode": "content", "path": f.path("big.txt"), "head_limit": 3, "offset": 1})
	want = f.path("big.txt") + "\n997:line 997 has a needle 997 in it\n1994:line 1994 has a needle 1994 in it\n2991:line 2991 has a needle 2991 in it\n\n[Showing result lines 2-4 of 8. To see more, use offset=4.]\n"
	if got != want {
		t.Errorf("content =\n%s\nwant\n%s", got, want)
	}
}

func TestGrepIgnoringCaseKnowsEveryFormOfALetter(t *testing.T) {
	// Go's regular expressions take the Kelvin sign for a k and the long s
	// for an s when case is ignored. The short cut that looks for a
	// pattern's plain text before trying the pattern must not hide that.
	f := newFixture(t, Options{})
	kelvinSign, longS := "\xe2\x84\xaa", "\xc5\xbf"
	p := f.file("forms.txt", "a kelvin\nA KELVIN\na "+kelvinSign+"ELVIN\nmass\nMASS\nma"+longS+longS+"\nnothing\n")
	for pattern, want := range map[string]string{
		"kelvin": "1:a kelvin\n2:A KELVIN\n3:a " + kelvinSign + "ELVIN\n",
		"mass":   "4:mass\n5:MASS\n6:ma" + longS + longS + "\n",
		"KeLvIn": "1:a kelvin\n2:A KELVIN\n3:a " + kelvinSign + "ELVIN\n",
	} {
		got := f.ok(f.grep, in{"pattern": pattern, "-i": true, "output_mode": "content", "path": p})
		if got != p+"\n"+want {
			t.Errorf("Grep -i %q =\n%s\nwant\n%s", pattern, got, p+"\n"+want)
		}
	}
	for pattern, want := range map[string]string{
		"error": "error", "Kelvin": "elvin", "TODO: fix": "todo: fix", "sass": "", "a": "", "foo|bar": "",
		"x(": "", "s\xc3\xbc\xc3\x9f": "", "Makefile": "efile", "disk": "di", "a.c": "", "ab+c": "ab", "[x]yz": "xyz",
		"risky business": "y bu", "task list": " li", "hello\\.world": "hello.world",
	} {
		if got := string(foldLiteral(pattern, "")); got != want {
			t.Errorf("foldLiteral(%q) = %q, want %q", pattern, got, want)
		}
	}
	if !foldsWithinASCII('a') || !foldsWithinASCII('Z') || !foldsWithinASCII('7') || !foldsWithinASCII('-') {
		t.Error("an ordinary ASCII character is said to have a form outside ASCII")
	}
	for _, r := range "kKsS\xc3\xa9" {
		if foldsWithinASCII(r) {
			t.Errorf("%q is said to have only ASCII forms", r)
		}
	}
}

func FuzzContainsFold(f *testing.F) {
	for _, s := range [][2]string{
		{"Hello World", "world"}, {"aaaaab", "aab"}, {"AAAAAB", "aab"}, {"", "x"}, {"x", ""}, {"abcABC", "cab"},
		{"eEeEeEeEeEeEe error", "error"}, {"ERRO error", "error"}, {"12-34", "2-3"}, {"\xff\xfeab", "ab"}, {"Ab", "abc"},
	} {
		f.Add([]byte(s[0]), s[1])
	}
	f.Fuzz(func(t *testing.T, b []byte, q string) {
		// The text looked for is lower-case ASCII by contract.
		for i := 0; i < len(q); i++ {
			if q[i] >= 0x80 || (q[i] >= 'A' && q[i] <= 'Z') {
				return
			}
		}
		lowered := make([]byte, len(b))
		for i, c := range b {
			if c >= 'A' && c <= 'Z' {
				c += 'a' - 'A'
			}
			lowered[i] = c
		}
		want := strings.Contains(string(lowered), q)
		if got := containsFold(b, []byte(q)); got != want {
			t.Fatalf("containsFold(%q, %q) = %v, want %v", b, q, got, want)
		}
	})
}

// .ignore and .rgignore are read as ripgrep reads them, with more weight
// than .gitignore: what .gitignore leaves out, .rgignore can let in.
func TestSearchesReadIgnoreAndRgignore(t *testing.T) {
	f := newFixture(t, Options{})
	f.file(".gitignore", "kept.txt\n")
	f.file(".ignore", "dropped.txt\n")
	f.file(".rgignore", "!kept.txt\n")
	for _, name := range []string{"kept.txt", "dropped.txt", "plain.txt"} {
		f.file(name, "needle\n")
	}
	got := f.ok(f.grep, in{"pattern": "needle"})
	mustContain(t, got, f.path("kept.txt"), f.path("plain.txt"))
	mustNotContain(t, got, f.path("dropped.txt"))
	if paths, _ := f.globbed(in{"pattern": "*.txt"}); !reflect.DeepEqual(sorted(paths), []string{"kept.txt", "plain.txt"}) {
		t.Errorf("Glob listed %v", paths)
	}
}

// The types the search-tools review of 2026-11-20 found missing or short.
func TestMoreFileTypes(t *testing.T) {
	for _, c := range []struct {
		typ, name string
		want      bool
	}{
		{"docker", "api.Dockerfile", true}, {"docker", "Dockerfile.dev", true}, {"docker", "Dockerfile", true},
		{"java", "index.jsp", true}, {"java", "app.properties", true},
		{"gradle", "build.gradle.kts", true}, {"cuda", "kernel.cu", true}, {"erb", "page.html.erb", true},
		{"ps", "setup.ps1", true}, {"powershell", "mod.psm1", true}, {"sass", "a.sass", true},
		{"java", "Main.kt", false},
	} {
		typ, ok := lookupFileType(c.typ)
		if !ok {
			t.Errorf("no type %q", c.typ)
			continue
		}
		if got := typ.matches(c.name); got != c.want {
			t.Errorf("type %s on %s: %v, want %v", c.typ, c.name, got, c.want)
		}
	}
}

// A file in UTF-16, with its byte order mark, is searched as the text it
// is, as ripgrep searches one, and not passed over as binary.
func TestGrepUTF16(t *testing.T) {
	f := newFixture(t, Options{})
	enc := func(s string, little bool) string {
		var b strings.Builder
		if little {
			b.WriteString("\xff\xfe")
		} else {
			b.WriteString("\xfe\xff")
		}
		for _, u := range utf16.Encode([]rune(s)) {
			if little {
				b.WriteByte(byte(u))
				b.WriteByte(byte(u >> 8))
			} else {
				b.WriteByte(byte(u >> 8))
				b.WriteByte(byte(u))
			}
		}
		return b.String()
	}
	le := f.file("win.txt", enc("first line\r\nthe needle, café\r\n", true))
	be := f.file("mac.txt", enc("no\nneedle here\n", false))
	got := f.ok(f.grep, in{"pattern": "needle", "output_mode": "content", "path": le})
	if want := le + "\n2:the needle, café\n"; got != want {
		t.Errorf("UTF-16LE: got %q, want %q", got, want)
	}
	got = f.ok(f.grep, in{"pattern": "needle", "output_mode": "content", "path": be})
	if want := be + "\n2:needle here\n"; got != want {
		t.Errorf("UTF-16BE: got %q, want %q", got, want)
	}
	mustContain(t, f.ok(f.grep, in{"pattern": "needle"}), le, be)
}
