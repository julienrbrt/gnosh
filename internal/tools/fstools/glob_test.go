package fstools

import (
	"context"
	"errors"
	"fmt"
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/gnosh-ai/gnosh/internal/core"
)

// globTree makes the tree most Glob tests search. Every file gets its own
// modification time, a second apart, in the order listed: the first is
// the oldest.
func globTree(f *fixture) {
	f.t.Helper()
	files := []string{
		"main.go", "README.md", "src/a.go", "src/b.ts", "src/deep/c.go", "src/deep/er/d.go",
		"docs/guide.md", ".hidden/h.go", ".env", ".gitignore", "debug.log", "build/out.o",
		"node_modules/pkg/index.js", ".git/config", ".git/objects/x.go",
	}
	for i, name := range files {
		content := "x\n"
		if name == ".gitignore" {
			content = "node_modules/\n*.log\n"
		}
		f.setTime(f.file(name, content), i)
	}
}

// globbed runs Glob and returns the paths it listed, relative to the
// fixture's directory, in the order listed, and the notes after them.
func (f *fixture) globbed(input in) (paths []string, notes string) {
	f.t.Helper()
	text := f.ok(f.glob, input)
	body, notes, _ := strings.Cut(text, "\n[")
	for _, line := range strings.Split(strings.TrimSpace(body), "\n") {
		if line == "" || strings.HasPrefix(line, "No files match") {
			continue
		}
		rel := strings.TrimPrefix(line, f.dir+"/")
		if rel == line {
			f.t.Errorf("Glob listed %q, which is not a full path under %s", line, f.dir)
		}
		paths = append(paths, rel)
	}
	return paths, notes
}

func sorted(s []string) []string {
	out := append([]string(nil), s...)
	sort.Strings(out)
	return out
}

func TestGlobPatterns(t *testing.T) {
	f := newFixture(t, Options{})
	globTree(f)
	everything := []string{".env", ".gitignore", ".hidden/h.go", "README.md", "build/out.o", "docs/guide.md", "main.go", "src/a.go", "src/b.ts", "src/deep/c.go", "src/deep/er/d.go"}
	tests := []struct {
		name  string
		input in
		want  []string
	}{
		{"top level only", in{"pattern": "*.go"}, []string{"main.go"}},
		{"any depth", in{"pattern": "**/*.go"}, []string{".hidden/h.go", "main.go", "src/a.go", "src/deep/c.go", "src/deep/er/d.go"}},
		{"under a directory", in{"pattern": "src/**/*.go"}, []string{"src/a.go", "src/deep/c.go", "src/deep/er/d.go"}},
		{"one directory", in{"pattern": "src/*.go"}, []string{"src/a.go"}},
		{"alternatives", in{"pattern": "*.{go,md}"}, []string{"README.md", "main.go"}},
		{"alternatives at any depth", in{"pattern": "**/*.{ts,md}"}, []string{"README.md", "docs/guide.md", "src/b.ts"}},
		{"alternative directories", in{"pattern": "{src,docs}/*"}, []string{"docs/guide.md", "src/a.go", "src/b.ts"}},
		{"everything below", in{"pattern": "src/deep/**"}, []string{"src/deep/c.go", "src/deep/er/d.go"}},
		{"a name at any depth", in{"pattern": "**/d.go"}, []string{"src/deep/er/d.go"}},
		{"question mark and set", in{"pattern": "s?c/[ab].*"}, []string{"src/a.go", "src/b.ts"}},
		{"a set turned round", in{"pattern": "src/[!a].*"}, []string{"src/b.ts"}},
		{"two stars alone", in{"pattern": "**"}, everything},
		{"a plain name", in{"pattern": "README.md"}, []string{"README.md"}},
		{"a plain path", in{"pattern": "src/deep/c.go"}, []string{"src/deep/c.go"}},
		{"dot files match a star", in{"pattern": "*"}, []string{".env", ".gitignore", "README.md", "main.go"}},
		{"dot directories are entered", in{"pattern": "**/h.go"}, []string{".hidden/h.go"}},
		{"ignored files are left out", in{"pattern": "**/*.js"}, nil},
		{"ignored files are left out by name", in{"pattern": "*.log"}, nil},
		// A name spelled out is listed whatever the rules say (the
		// search-tools review of 2026-11-20).
		{"an ignored file spelled out is listed", in{"pattern": "debug.log"}, []string{"debug.log"}},
		{"an ignored file spelled out at any depth", in{"pattern": "**/debug.log"}, []string{"debug.log"}},
		{"an ignored file spelled out with its path", in{"pattern": "{debug.log,main.go}"}, []string{"debug.log", "main.go"}},
		{"an ignored directory spelled out on the way", in{"pattern": "**/node_modules/pkg/index.js"}, []string{"node_modules/pkg/index.js"}},
		{"an ignored directory named in the pattern is searched", in{"pattern": "node_modules/**/*.js"}, []string{"node_modules/pkg/index.js"}},
		{"an ignored directory named in path is searched", in{"pattern": "**/*.js", "path": f.path("node_modules")}, []string{"node_modules/pkg/index.js"}},
		{"nothing in .git", in{"pattern": "**/x.go"}, nil},
		{".git named in the pattern is searched", in{"pattern": ".git/*"}, []string{".git/config"}},
		{"a path argument", in{"pattern": "**/*.go", "path": f.path("src")}, []string{"src/a.go", "src/deep/c.go", "src/deep/er/d.go"}},
		{"a relative path argument", in{"pattern": "*.go", "path": "src/deep"}, []string{"src/deep/c.go"}},
		{"an absolute pattern", in{"pattern": f.dir + "/src/*.go"}, []string{"src/a.go"}},
		{"an absolute pattern goes its own way", in{"pattern": f.dir + "/src/**/*.go", "path": f.path("docs")}, []string{"src/a.go", "src/deep/c.go", "src/deep/er/d.go"}},
		{"a pattern that starts with ./", in{"pattern": "./src/*.go"}, []string{"src/a.go"}},
		{"a pattern that goes up", in{"pattern": "../*.md", "path": f.path("src")}, []string{"README.md"}},
		{"a directory in the pattern that is not there", in{"pattern": "nowhere/**/*.go"}, nil},
		{"letter case matters", in{"pattern": "*.GO"}, nil},
		{"directories are not listed", in{"pattern": "src/*"}, []string{"src/a.go", "src/b.ts"}},
		{"three stars are two", in{"pattern": "***/c.go"}, []string{"src/deep/c.go"}},
		{"stars inside a name stay in it", in{"pattern": "src/**.go"}, []string{"src/a.go"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, _ := f.globbed(tt.input)
			if !reflect.DeepEqual(sorted(got), sorted(tt.want)) {
				t.Errorf("Glob(%v) = %v, want %v", tt.input, sorted(got), sorted(tt.want))
			}
			if len(tt.want) == 0 {
				mustContain(t, f.ok(f.glob, tt.input), "No files match")
			}
		})
	}
}

func TestGlobNewestFirst(t *testing.T) {
	f := newFixture(t, Options{})
	globTree(f)
	got, notes := f.globbed(in{"pattern": "**/*.go"})
	want := []string{".hidden/h.go", "src/deep/er/d.go", "src/deep/c.go", "src/a.go", "main.go"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("order = %v, want newest first: %v", got, want)
	}
	if notes != "" {
		t.Errorf("unexpected notes: %q", notes)
	}
	// Files changed at the same moment come in path order.
	for _, name := range []string{"src/a.go", "main.go", "src/deep/c.go"} {
		f.setTime(f.path(name), 100)
	}
	got, _ = f.globbed(in{"pattern": "**/*.go"})
	want = []string{"main.go", "src/a.go", "src/deep/c.go", ".hidden/h.go", "src/deep/er/d.go"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("order = %v, want %v", got, want)
	}
}

func TestGlobResultCap(t *testing.T) {
	f := newFixture(t, Options{GlobMaxResults: 3})
	globTree(f)
	res := f.run(f.glob, in{"pattern": "**/*.go"})
	text := core.TextOf(res.Content)
	want := f.path(".hidden/h.go") + "\n" + f.path("src/deep/er/d.go") + "\n" + f.path("src/deep/c.go") + "\n" +
		"\n[Showing the 3 most recently changed of 5 matching files; 2 more are not shown. Narrow the pattern or the path to see them.]\n"
	if text != want {
		t.Errorf("text =\n%s\nwant\n%s", text, want)
	}
	if got := metaOf(t, res, "numFiles"); got != 3 {
		t.Errorf("Meta numFiles = %v", got)
	}
	if got := metaOf(t, res, "totalMatches"); got != 5 {
		t.Errorf("Meta totalMatches = %v", got)
	}
	if got := metaOf(t, res, "truncated"); got != true {
		t.Errorf("Meta truncated = %v", got)
	}
	if got := metaOf(t, res, "filenames").([]string); len(got) != 3 || got[0] != f.path(".hidden/h.go") {
		t.Errorf("Meta filenames = %v", got)
	}
	// Exactly at the cap nothing is left out.
	res = f.run(f.glob, in{"pattern": "src/**/*.go"})
	if metaOf(t, res, "truncated") != false || strings.Contains(core.TextOf(res.Content), "[") {
		t.Errorf("three matches with a cap of three: %s", core.TextOf(res.Content))
	}
	// The default is one hundred.
	big := newFixture(t, Options{})
	for i := 0; i < 130; i++ {
		big.setTime(big.file(fmt.Sprintf("many/f%03d.txt", i), "x"), i)
	}
	got, notes := big.globbed(in{"pattern": "many/*.txt"})
	if len(got) != 100 || got[0] != "many/f129.txt" || got[99] != "many/f030.txt" {
		t.Errorf("listed %d files, from %s to %s; want the 100 newest", len(got), got[0], got[len(got)-1])
	}
	mustContain(t, notes, "Showing the 100 most recently changed of 130 matching files; 30 more are not shown.")
}

func TestGlobOptions(t *testing.T) {
	t.Run("ignore rules turned off", func(t *testing.T) {
		f := newFixture(t, Options{GlobNoIgnore: true})
		globTree(f)
		got, _ := f.globbed(in{"pattern": "**/*.{js,log}"})
		if want := []string{"debug.log", "node_modules/pkg/index.js"}; !reflect.DeepEqual(sorted(got), want) {
			t.Errorf("got %v, want %v", sorted(got), want)
		}
		// .git stays out all the same.
		if got, _ := f.globbed(in{"pattern": "**/x.go"}); len(got) != 0 {
			t.Errorf("listed files inside .git: %v", got)
		}
	})
	t.Run("hidden names passed over", func(t *testing.T) {
		f := newFixture(t, Options{GlobSkipHidden: true})
		globTree(f)
		got, _ := f.globbed(in{"pattern": "**"})
		for _, p := range got {
			if strings.HasPrefix(p, ".") || strings.Contains(p, "/.") {
				t.Errorf("listed the hidden %s", p)
			}
		}
		if len(got) != 8 {
			t.Errorf("listed %d files, want 8: %v", len(got), got)
		}
	})
	t.Run("other ignore files", func(t *testing.T) {
		f := newFixture(t, Options{IgnoreFiles: []string{".gitignore", ".ignore"}})
		globTree(f)
		f.file(".ignore", "docs/\n!debug.log\n")
		got, _ := f.globbed(in{"pattern": "**/*.{md,log}"})
		if want := []string{"README.md", "debug.log"}; !reflect.DeepEqual(sorted(got), want) {
			t.Errorf("got %v, want %v", sorted(got), want)
		}
	})
	t.Run("info/exclude", func(t *testing.T) {
		f := newFixture(t, Options{})
		globTree(f)
		f.file(".git/info/exclude", "*.md\n")
		if got, _ := f.globbed(in{"pattern": "**/*.md"}); len(got) != 0 {
			t.Errorf("listed files that info/exclude leaves out: %v", got)
		}
		off := newFixture(t, Options{NoGitExclude: true})
		globTree(off)
		off.file(".git/info/exclude", "*.md\n")
		if got, _ := off.globbed(in{"pattern": "**/*.md"}); len(got) != 2 {
			t.Errorf("with info/exclude turned off, got %v", got)
		}
	})
	t.Run("other names for the tool", func(t *testing.T) {
		tools := New(Options{GlobName: "FindFiles", GrepName: "SearchText"})
		if got := tools[3].Spec().Name; got != "FindFiles" {
			t.Errorf("name = %q", got)
		}
		mustContain(t, tools[3].Spec().Description, "use SearchText")
	})
}

func TestGlobLinks(t *testing.T) {
	f := newFixture(t, Options{})
	f.setTime(f.file("real/file.txt", "x"), 50)
	f.setTime(f.file("real/sub/inner.txt", "x"), 10)
	f.symlink("real/file.txt", "link-to-file.txt")
	f.symlink("real", "link-to-dir")
	f.symlink("nowhere.txt", "broken.txt")
	f.symlink("..", "real/sub/up")
	got, _ := f.globbed(in{"pattern": "**/*.txt"})
	// A link to a file is a file; a link to a directory is not entered (so
	// nothing is listed twice and a loop cannot trap the walk); a link to
	// nothing is nothing.
	want := []string{"link-to-file.txt", "real/file.txt", "real/sub/inner.txt"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
	// A linked directory named as the place to search is searched.
	got, _ = f.globbed(in{"pattern": "*.txt", "path": f.path("link-to-dir")})
	if want := []string{"link-to-dir/file.txt"}; !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
	act, err := f.describe(f.glob, in{"pattern": "*.txt", "path": f.path("link-to-dir")})
	if err != nil || len(act.Paths) != 2 || act.Paths[0] != f.path("link-to-dir") || act.Paths[1] != f.path("real") {
		t.Errorf("Describe paths = %v, err %v", act.Paths, err)
	}
}

func TestGlobOddNames(t *testing.T) {
	f := newFixture(t, Options{})
	for _, name := range []string{"we[i]rd/x.go", "star*/y.go", "brace{a,b}/z.go", "sp ace/w.go", "日本/語.go", "q?/v.go"} {
		f.file(name, "x")
	}
	tests := []struct {
		pattern string
		want    []string
	}{
		{`we\[i\]rd/*.go`, []string{"we[i]rd/x.go"}},
		{`star\*/*.go`, []string{"star*/y.go"}},
		{`brace\{a,b\}/*.go`, []string{"brace{a,b}/z.go"}},
		{"sp ace/*.go", []string{"sp ace/w.go"}},
		{"日本/?.go", []string{"日本/語.go"}},
		{`q\?/v.go`, []string{"q?/v.go"}},
		{"*/[xyz].go", []string{"brace{a,b}/z.go", "star*/y.go", "we[i]rd/x.go"}},
	}
	for _, tt := range tests {
		got, _ := f.globbed(in{"pattern": tt.pattern})
		if !reflect.DeepEqual(sorted(got), tt.want) {
			t.Errorf("Glob(%q) = %v, want %v", tt.pattern, sorted(got), tt.want)
		}
	}
}

func TestGlobRefusals(t *testing.T) {
	f := newFixture(t, Options{})
	globTree(f)
	tests := []struct {
		name  string
		input in
		wants []string
	}{
		{"no pattern", in{}, []string{"pattern is required"}},
		{"empty pattern", in{"pattern": ""}, []string{"pattern is empty"}},
		{"pattern of the wrong kind", in{"pattern": 5}, []string{"pattern must be a string"}},
		{"unknown argument", in{"pattern": "*", "recursive": true}, []string{`takes no argument called "recursive"`, "pattern, path"}},
		{"unclosed bracket", in{"pattern": "src/[ab.go"}, []string{`the pattern "src/[ab.go" cannot be used`, "a [ has no closing ]"}},
		{"unclosed brace", in{"pattern": "*.{go,md"}, []string{"cannot be used", "a { has no closing }"}},
		{"trailing backslash", in{"pattern": "src\\"}, []string{"a \\ has nothing after it"}},
		{"too many alternatives", in{"pattern": strings.Repeat("{a,b}", 12)}, []string{"stand for more than 1024 patterns"}},
		{"NUL in the pattern", in{"pattern": "a\x00b"}, []string{"NUL byte"}},
		{"NUL in the path", in{"pattern": "*", "path": f.dir + "\x00"}, []string{"NUL byte"}},
		{"tilde in the pattern", in{"pattern": "~/src/*.go"}, []string{`starts with "~"`, "not expanded"}},
		{"tilde in the path", in{"pattern": "*.go", "path": "~/src"}, []string{"not expanded"}},
		{"only a slash", in{"pattern": "/"}, []string{"names a directory and nothing in it"}},
		{"a pattern that ends in a slash", in{"pattern": "src/"}, []string{`the pattern "src/" names a directory and nothing in it`, "src/* for the files in it", "src/** for everything below it"}},
		{"path of the wrong kind", in{"pattern": "*", "path": true}, []string{"path must be a string"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f.fails(f.glob, tt.input, tt.wants...)
			if _, err := f.describe(f.glob, tt.input); err == nil {
				t.Error("Describe accepted the call")
			}
		})
	}
	// What is wrong with the place to search is for the call to say, not
	// for Describe.
	onDisk := []struct {
		name  string
		input in
		wants []string
	}{
		{"a path that is not there", in{"pattern": "*", "path": f.path("missing")}, []string{f.path("missing") + " does not exist", "nothing to search"}},
		{"a path that is a file", in{"pattern": "*", "path": f.path("main.go")}, []string{f.path("main.go") + " is a file, not a directory"}},
	}
	for _, tt := range onDisk {
		t.Run(tt.name, func(t *testing.T) {
			f.fails(f.glob, tt.input, tt.wants...)
			act, err := f.describe(f.glob, tt.input)
			if err != nil || len(act.Paths) != 1 || act.Paths[0] != tt.input["path"] {
				t.Errorf("Describe = %+v, %v; want the path and no error", act, err)
			}
		})
	}
	t.Run("the home directory, when one is given", func(t *testing.T) {
		home := newFixture(t, Options{HomeDir: f.dir})
		got := home.ok(home.glob, in{"pattern": "~/src/*.go"})
		if got != f.path("src/a.go")+"\n" {
			t.Errorf("text = %q", got)
		}
	})
}

func TestGlobDescribe(t *testing.T) {
	f := newFixture(t, Options{})
	globTree(f)
	tests := []struct {
		input in
		path  string
	}{
		{in{"pattern": "**/*.go"}, f.dir},
		{in{"pattern": "src/**/*.go"}, f.path("src")},
		{in{"pattern": "src/deep/c.go"}, f.path("src/deep")},
		{in{"pattern": "*.go", "path": f.path("docs")}, f.path("docs")},
		{in{"pattern": "{src,docs}/*.md"}, f.dir},
		{in{"pattern": "/etc/*.conf"}, "/etc"},
		{in{"pattern": "nowhere/*.go"}, f.path("nowhere")},
	}
	for _, tt := range tests {
		act, err := f.describe(f.glob, tt.input)
		if err != nil {
			t.Errorf("Describe(%v): %v", tt.input, err)
			continue
		}
		if act.Kind != core.ActionRead || act.Paths[0] != tt.path || act.Preview != nil {
			t.Errorf("Describe(%v) = %+v, want a read of %s", tt.input, act, tt.path)
		}
		want := fmt.Sprintf("Find files matching %s in %s", tt.input["pattern"], tt.path)
		if act.Summary != want {
			t.Errorf("summary = %q, want %q", act.Summary, want)
		}
	}
}

func TestGlobManyDirectories(t *testing.T) {
	f := newFixture(t, Options{GlobMaxResults: 100000})
	want := 0
	for d := 0; d < 60; d++ {
		for s := 0; s < 4; s++ {
			for i := 0; i < 5; i++ {
				f.file(fmt.Sprintf("d%02d/s%d/f%d.txt", d, s, i), "x")
				want++
			}
			f.file(fmt.Sprintf("d%02d/s%d/other.dat", d, s), "x")
		}
	}
	// Several times over, since the walk runs on many goroutines.
	for round := 0; round < 5; round++ {
		res := f.run(f.glob, in{"pattern": "**/*.txt"})
		if got := metaOf(t, res, "totalMatches"); got != want {
			t.Fatalf("round %d: found %v files, want %d", round, got, want)
		}
		names := metaOf(t, res, "filenames").([]string)
		seen := make(map[string]bool, len(names))
		for _, n := range names {
			if seen[n] {
				t.Fatalf("%s listed twice", n)
			}
			seen[n] = true
		}
		if len(names) != want {
			t.Fatalf("listed %d files, want %d", len(names), want)
		}
	}
	// A pattern that names its directories does not look elsewhere.
	res := f.run(f.glob, in{"pattern": "d07/s?/f[01].txt"})
	if got := metaOf(t, res, "totalMatches"); got != 8 {
		t.Errorf("found %v files, want 8", got)
	}
}

func TestGlobStopsWhenCancelled(t *testing.T) {
	f := newFixture(t, Options{})
	globTree(f)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := f.glob.Run(ctx, call(in{"pattern": "**/*.go"}), f.env)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	mustContain(t, err.Error(), "Glob in "+f.dir+" stopped")
}

func TestGlobTimeLimit(t *testing.T) {
	f := newFixture(t, Options{SearchTimeout: time.Nanosecond})
	globTree(f)
	// Out of time is an answer for the model, in words, not a harness
	// error; and it is never a partial list.
	msg := f.fails(f.glob, in{"pattern": "**/*.go"}, "ran out of time", "there is no result", "says nothing about whether there are matches")
	mustNotContain(t, msg, "main.go")
	// The caller's own deadline is the caller's business.
	ctx, cancel := context.WithTimeout(context.Background(), time.Nanosecond)
	defer cancel()
	<-ctx.Done()
	_, err := newFixture(t, Options{}).glob.Run(ctx, call(in{"pattern": "*", "path": f.dir}), f.env)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("err = %v, want context.DeadlineExceeded", err)
	}
}

func TestGlobEmptyDirectory(t *testing.T) {
	f := newFixture(t, Options{})
	res := f.run(f.glob, in{"pattern": "**"})
	if res.IsError {
		t.Fatal("no matches is not an error")
	}
	if got, want := core.TextOf(res.Content), "No files match ** under "+f.dir+".\n"; got != want {
		t.Errorf("text = %q, want %q", got, want)
	}
	if got := metaOf(t, res, "numFiles"); got != 0 {
		t.Errorf("Meta numFiles = %v", got)
	}
	// A directory that vanishes is not there to search.
	gone := f.mkdir("gone")
	os.Remove(gone)
	f.fails(f.glob, in{"pattern": "*", "path": gone}, "does not exist")
}
