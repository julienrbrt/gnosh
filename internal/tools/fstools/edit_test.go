package fstools

import (
	"context"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/gnosh-ai/gnosh/internal/core"
)

// readThenEdit reads a file and edits it, requiring both to work, and
// returns the edit's result.
func (f *fixture) readThenEdit(path, old, new string) core.ToolResult {
	f.t.Helper()
	f.ok(f.read, in{"file_path": path})
	res := f.run(f.edit, in{"file_path": path, "old_string": old, "new_string": new})
	if res.IsError {
		f.t.Fatalf("edit failed: %s", core.TextOf(res.Content))
	}
	return res
}

func TestEditReplacesExactText(t *testing.T) {
	f := newFixture(t, Options{})
	original := "package main\n\nfunc main() {\n\tprintln(\"hello\")\n}\n"
	p := f.file("main.go", original)
	res := f.readThenEdit(p, "println(\"hello\")", "println(\"goodbye\")")
	want := "package main\n\nfunc main() {\n\tprintln(\"goodbye\")\n}\n"
	if got := f.content(p); got != want {
		t.Fatalf("file holds %q, want %q", got, want)
	}
	text := core.TextOf(res.Content)
	mustContain(t, text, "Edited "+p+": 1 replacement, at line 4.", "Lines 1-5 are now:\n",
		"3\tfunc main() {\n4\t\tprintln(\"goodbye\")\n5\t}\n")
	wantDisplay := core.Display{Kind: "diff", Title: "main.go", Path: p, Before: original, After: want}
	if res.Display == nil || *res.Display != wantDisplay {
		t.Errorf("Display = %+v", res.Display)
	}
	if got := metaOf(t, res, "filePath"); got != p {
		t.Errorf("Meta filePath = %v", got)
	}
	if got := metaOf(t, res, "replacements"); got != 1 {
		t.Errorf("Meta replacements = %v", got)
	}
	if got := metaOf(t, res, "replaceAll"); got != false {
		t.Errorf("Meta replaceAll = %v", got)
	}
	if got := metaOf(t, res, "lines"); !reflect.DeepEqual(got, []int{4}) {
		t.Errorf("Meta lines = %v", got)
	}
	noTempFiles(t, f.dir)
	st := States(f.env)
	if len(st) != 1 || !st[0].Full || !st[0].Written {
		t.Errorf("States = %+v", st)
	}
	// An edit counts as a fresh look at the file: the next one needs no
	// read in between.
	f.ok(f.edit, in{"file_path": p, "old_string": "goodbye", "new_string": "again"})
	f.ok(f.edit, in{"file_path": p, "old_string": "package main\n\n", "new_string": "package main\n\nimport \"os\"\n\n"})
	if got, want := f.content(p), "package main\n\nimport \"os\"\n\nfunc main() {\n\tprintln(\"again\")\n}\n"; got != want {
		t.Errorf("file holds %q, want %q", got, want)
	}
}

func TestEditShapesOfChange(t *testing.T) {
	tests := []struct {
		name          string
		file          string
		old, new      string
		want          string
		wantLine      string // "at line N" in the result
		wantInSnippet string
	}{
		{"within a line", "a b c\n", "b", "B", "a B c\n", "at line 1", "1\ta B c\n"},
		{"delete text", "a b c\n", " b", "", "a c\n", "at line 1", "1\ta c\n"},
		{"delete whole lines", "1\n2\n3\n4\n5\n", "2\n3\n", "", "1\n4\n5\n", "at line 2", "1\t1\n2\t4\n3\t5\n"},
		{"insert lines", "1\n2\n", "1\n", "1\na\nb\n", "1\na\nb\n2\n", "at line 1", "1\t1\n2\ta\n3\tb\n4\t2\n"},
		{"join lines", "a\nb\nc\n", "a\nb", "ab", "ab\nc\n", "at line 1", "1\tab\n2\tc\n"},
		{"at the very start", "start middle end", "start", "START", "START middle end", "at line 1", "1\tSTART middle end\n"},
		{"at the very end", "start middle end", "end", "END", "start middle END", "at line 1", "1\tstart middle END\n"},
		{"the whole file", "all\n", "all\n", "everything\nnew\n", "everything\nnew\n", "at line 1", "1\teverything\n2\tnew\n"},
		{"to nothing at all", "all\n", "all\n", "", "", "at line 1", ""},
		{"only a line break", "a\n\nb\n", "\n\n", "\n", "a\nb\n", "at line 1", "1\ta\n2\tb\n"},
		{"unicode", "naïve café 日本\n", "café", "thé", "naïve thé 日本\n", "at line 1", "1\tnaïve thé 日本\n"},
		{"special characters are not a pattern", "x = a.*b + [c] (d) $1 \\n\n", "a.*b + [c] (d) $1 \\n", "$0 \\1 .*", "x = $0 \\1 .*\n", "at line 1", "1\tx = $0 \\1 .*\n"},
		{"leading whitespace counts", "\tif x {\n\t\ty()\n\t}\n", "\t\ty()", "\t\tz()", "\tif x {\n\t\tz()\n\t}\n", "at line 2", "2\t\t\tz()\n"},
		{"deep in a file", manyLines(100), "line 57\n", "LINE 57\n", strings.Replace(manyLines(100), "line 57\n", "LINE 57\n", 1), "at line 57", "54\tline 54\n55\tline 55\n56\tline 56\n57\tLINE 57\n58\tline 58\n59\tline 59\n60\tline 60\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t, Options{})
			p := f.file("f.txt", tt.file)
			res := f.readThenEdit(p, tt.old, tt.new)
			if got := f.content(p); got != tt.want {
				t.Errorf("file holds %q, want %q", got, tt.want)
			}
			text := core.TextOf(res.Content)
			mustContain(t, text, tt.wantLine, tt.wantInSnippet)
			if res.Display.Before != tt.file || res.Display.After != tt.want {
				t.Errorf("Display = %+v", res.Display)
			}
		})
	}
}

func TestEditSnippet(t *testing.T) {
	t.Run("a long replacement is cut", func(t *testing.T) {
		f := newFixture(t, Options{})
		p := f.file("f.txt", "top\nHERE\nbottom\n")
		res := f.readThenEdit(p, "HERE\n", manyLines(40))
		text := core.TextOf(res.Content)
		mustContain(t, text, "Lines 1-25 are now:\n1\ttop\n2\tline 1\n", "25\tline 24\n", "[... 16 more lines of the new text follow.]")
		mustNotContain(t, text, "26\t", "bottom")
	})
	t.Run("turned off", func(t *testing.T) {
		f := newFixture(t, Options{EditContextLines: -1})
		p := f.file("f.txt", "a\nb\n")
		res := f.readThenEdit(p, "a", "A")
		if got, want := core.TextOf(res.Content), "Edited "+p+": 1 replacement, at line 1."; got != want {
			t.Errorf("text = %q, want %q", got, want)
		}
	})
	t.Run("more context", func(t *testing.T) {
		f := newFixture(t, Options{EditContextLines: 1})
		p := f.file("f.txt", manyLines(9))
		res := f.readThenEdit(p, "line 5", "five")
		mustContain(t, core.TextOf(res.Content), "Lines 4-6 are now:\n4\tline 4\n5\tfive\n6\tline 6\n")
	})
	t.Run("long lines are cut as Read cuts them", func(t *testing.T) {
		f := newFixture(t, Options{ReadMaxLineChars: 20})
		p := f.file("f.txt", strings.Repeat("x", 50)+" end\n")
		res := f.readThenEdit(p, "xxxxx end", "yyyyy end")
		mustContain(t, core.TextOf(res.Content), "1\t"+strings.Repeat("x", 20)+" [... 34 more characters on this line]\n")
	})
}

func TestEditNeedsTheFileRead(t *testing.T) {
	f := newFixture(t, Options{})
	p := f.file("a.txt", "hello world\n")
	msg := f.fails(f.edit, in{"file_path": p, "old_string": "hello", "new_string": "bye"}, "has not read "+p, "Nothing was changed", "Read")
	mustNotContain(t, msg, "hello world")
	// Even when the text is not there, the answer gives nothing away about
	// a file that has not been read.
	msg = f.fails(f.edit, in{"file_path": p, "old_string": "absent", "new_string": "x"}, "has not read")
	mustNotContain(t, msg, "not found", "closest")
	act, err := f.describe(f.edit, in{"file_path": p, "old_string": "hello", "new_string": "bye"})
	if err == nil || !strings.Contains(err.Error(), "has not read") {
		t.Errorf("Describe error = %v", err)
	}
	if act.Kind != core.ActionEdit || len(act.Paths) != 1 || act.Paths[0] != p || act.Preview != nil {
		t.Errorf("Describe action alongside the error = %+v", act)
	}
	if got := f.content(p); got != "hello world\n" {
		t.Fatal("a refused edit changed the file")
	}
	// A file that is not there.
	missing := f.path("nope.txt")
	f.fails(f.edit, in{"file_path": missing, "old_string": "a", "new_string": "b"}, missing+" does not exist", "To make a new file, use Write")
}

// A file changed behind the session's back may still be edited where
// old_string matches it as it is now, once, as Claude Code lets it: the
// edit touches that place alone, keeps the other changes, and says they
// are there; what is not found is told to read the file again, and so is
// an edit elsewhere afterwards.
func TestEditOfAFileChangedBehindItsBack(t *testing.T) {
	f := newFixture(t, Options{})
	p := f.file("a.txt", "one two three\n")
	f.ok(f.read, in{"file_path": p})
	if err := os.WriteFile(p, []byte("zero one two three four\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	f.fails(f.edit, in{"file_path": p, "old_string": "five", "new_string": "5"}, "has changed on disk", "Read it again")
	// More than one place, even with replace_all: some may lie in what
	// the session never saw.
	f.fails(f.edit, in{"file_path": p, "old_string": "o", "new_string": "0", "replace_all": true}, "has changed on disk", "Read it again")
	out := f.ok(f.edit, in{"file_path": p, "old_string": "two", "new_string": "2"})
	if !strings.Contains(out, "had changed on disk since this session last read it") {
		t.Errorf("the reply does not say the file had changed: %s", out)
	}
	if got := f.content(p); got != "zero one 2 three four\n" {
		t.Fatalf("the file holds %q", got)
	}

	// Changed after a read of only part of it: the edit could land in
	// lines never shown, so it is to be read again (the review of the
	// day's code, 2026-11-20). Read whole, then changed, it goes through,
	// and afterwards only the edited line is known.
	q := f.file("long.txt", manyLines(30))
	f.ok(f.read, in{"file_path": q, "limit": 5})
	if err := os.WriteFile(q, []byte(manyLines(31)), 0o644); err != nil {
		t.Fatal(err)
	}
	f.fails(f.edit, in{"file_path": q, "old_string": "line 2\n", "new_string": "x\n"}, "has changed on disk", "Read it again")
	f.ok(f.read, in{"file_path": q})
	if err := os.WriteFile(q, []byte(manyLines(32)), 0o644); err != nil {
		t.Fatal(err)
	}
	f.ok(f.edit, in{"file_path": q, "old_string": "line 2\n", "new_string": "x\n"})
	f.fails(f.edit, in{"file_path": q, "old_string": "line 25\n", "new_string": "y\n"}, "has not been shown")
	f.ok(f.read, in{"file_path": q, "offset": 20, "limit": 10})
	f.ok(f.edit, in{"file_path": q, "old_string": "line 25\n", "new_string": "y\n"})
}

func TestEditTextNotFound(t *testing.T) {
	curly := "say “hello” to the ‘world’ — now\n"
	tests := []struct {
		name  string
		file  string
		old   string
		wants []string
	}{
		{
			"indentation",
			"func f() {\n\tif x {\n\t\treturn 1\n\t}\n}\n",
			"    if x {\n        return 1\n    }",
			[]string{"was not found", "nothing was changed", "The closest text is at lines 2-4", "only the indentation differs", "2\t\tif x {\n3\t\t\treturn 1\n4\t\t}\n", "Copy the text exactly"},
		},
		{
			"spaces at the ends of lines",
			"alpha  \nbeta\t\ngamma\n",
			"alpha\nbeta\ngamma",
			[]string{"The closest text is at lines 1-3", "only spaces or tabs at the ends of lines differ", "1\talpha  \n"},
		},
		{
			"spacing inside a line",
			"x := foo(a,  b)\n",
			"foo(a, b)",
			[]string{"The closest text is at line 1", "only the spacing differs", "1\tx := foo(a,  b)\n"},
		},
		{
			"letter case",
			"first\nHello World\n",
			"hello world",
			[]string{"The closest text is at line 2", "only the letter case differs", "2\tHello World\n"},
		},
		{
			"typographic characters",
			curly,
			"say \"hello\" to the 'world' - now",
			[]string{"The closest text is at line 1", "typographic characters", "1\t" + curly},
		},
		{
			"the line numbers Read shows",
			"alpha\nbeta\ngamma\n",
			"1\talpha\n2\tbeta",
			[]string{"was not found", "old_string has the line numbers that Read puts in front of each line", "send the text without them"},
		},
		{
			"line numbers and something else wrong",
			"alpha\nbeta\ngamma\n",
			"1\talpha\n2\tbetta\n3\tgamma",
			[]string{"also has the line numbers Read shows", "The first line of old_string is at line 1", "line 2 of old_string does not match", `old_string has "betta"`, `the file has "beta"`},
		},
		{
			"tab-separated numbers are not line numbers",
			"7\tseven\n9\tnine\n",
			"7\tseven\n9\tnein",
			[]string{"The first line of old_string is at line 1", `old_string has "9\tnein"`},
		},
		{
			"a line break after the last line, which has none",
			"one\ntwo",
			"two\n",
			[]string{"old_string ends with a line break", "the very end of the file", "Leave the last line break out"},
		},
		{
			"line breaks spelt out",
			"a\tb\nc\n",
			`a\tb\nc`,
			[]string{"backslash and n", "real line break"},
		},
		{
			"a later line differs",
			"func a() {\n\treturn 1\n}\n\nfunc b() {\n\treturn 2\n}\n",
			"func b() {\n\treturn 3\n}",
			[]string{"The first line of old_string is at line 5 of the file", "line 2 of old_string does not match", `old_string has "\treturn 3"`, `the file has "\treturn 2"`, "5\tfunc b() {\n6\t\treturn 2\n7\t}\n"},
		},
		{
			"old_string runs past the end",
			"a\nb\n",
			"b\nc\nd",
			[]string{"The first line of old_string is at line 2", `old_string has "c"`, "the file ends before it"},
		},
		{
			"nothing like it",
			"abc\n",
			"zzz",
			[]string{"old_string was not found in", "so nothing was changed", "Read the file again with Read and copy the text exactly"},
		},
		{
			"a first line found twice gives no guess",
			"x\nsame\ny\nsame\nz\n",
			"same\nq",
			[]string{"Read the file again with Read"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t, Options{})
			p := f.file("f.txt", tt.file)
			f.ok(f.read, in{"file_path": p})
			input := in{"file_path": p, "old_string": tt.old, "new_string": "replacement"}
			msg := f.fails(f.edit, input, tt.wants...)
			mustContain(t, msg, p)
			if got := f.content(p); got != tt.file {
				t.Errorf("the failed edit changed the file: %q", got)
			}
			// Describe fails the same way.
			if _, err := f.describe(f.edit, input); err == nil || err.Error() != msg {
				t.Errorf("Describe error = %v, want the same as Run's", err)
			}
			noTempFiles(t, f.dir)
		})
	}
	t.Run("a carriage return that is not a line break", func(t *testing.T) {
		f := newFixture(t, Options{})
		p := f.file("f.txt", "a\r\nb\r\n")
		f.ok(f.read, in{"file_path": p})
		f.fails(f.edit, in{"file_path": p, "old_string": "a\rb", "new_string": "x"}, "carriage return that is not part of a line break")
	})
}

func TestEditMoreThanOneMatch(t *testing.T) {
	f := newFixture(t, Options{})
	original := "x = 1\ny = 2\nx = 1\nz = 3\nx = 1\n"
	p := f.file("f.txt", original)
	f.ok(f.read, in{"file_path": p})
	f.fails(f.edit, in{"file_path": p, "old_string": "x = 1", "new_string": "x = 9"},
		"old_string is found 3 times", "at lines 1, 3 and 5", "Nothing was changed", "surrounding text", "replace_all")
	if got := f.content(p); got != original {
		t.Fatal("a refused edit changed the file")
	}
	// Enough context to pick one out.
	f.ok(f.edit, in{"file_path": p, "old_string": "y = 2\nx = 1", "new_string": "y = 2\nx = 7"})
	if got, want := f.content(p), "x = 1\ny = 2\nx = 7\nz = 3\nx = 1\n"; got != want {
		t.Fatalf("file holds %q, want %q", got, want)
	}
	// Or every one of them.
	res := f.run(f.edit, in{"file_path": p, "old_string": "x = 1", "new_string": "x = 9", "replace_all": true})
	if res.IsError {
		t.Fatalf("failed: %s", core.TextOf(res.Content))
	}
	if got, want := f.content(p), "x = 9\ny = 2\nx = 7\nz = 3\nx = 9\n"; got != want {
		t.Fatalf("file holds %q, want %q", got, want)
	}
	mustContain(t, core.TextOf(res.Content), "2 replacements, at lines 1 and 5", "[The other replacement is not shown.]")
	if got := metaOf(t, res, "replacements"); got != 2 {
		t.Errorf("Meta replacements = %v", got)
	}
	if got := metaOf(t, res, "replaceAll"); got != true {
		t.Errorf("Meta replaceAll = %v", got)
	}
	if got := metaOf(t, res, "lines"); !reflect.DeepEqual(got, []int{1, 5}) {
		t.Errorf("Meta lines = %v", got)
	}

	t.Run("replace_all where lines come and go", func(t *testing.T) {
		q := f.file("g.txt", "a\nMARK\nb\nMARK\nc\nMARK\n")
		f.ok(f.read, in{"file_path": q})
		res := f.run(f.edit, in{"file_path": q, "old_string": "MARK\n", "new_string": "one\ntwo\n", "replace_all": true})
		if got, want := f.content(q), "a\none\ntwo\nb\none\ntwo\nc\none\ntwo\n"; got != want {
			t.Fatalf("file holds %q, want %q", got, want)
		}
		// The lines are those of the file as it now is.
		if got := metaOf(t, res, "lines"); !reflect.DeepEqual(got, []int{2, 5, 8}) {
			t.Errorf("Meta lines = %v, want [2 5 8]", got)
		}
		f.ok(f.edit, in{"file_path": q, "old_string": "one\ntwo\n", "new_string": "", "replace_all": true})
		if got, want := f.content(q), "a\nb\nc\n"; got != want {
			t.Fatalf("file holds %q, want %q", got, want)
		}
	})
	t.Run("matches do not overlap", func(t *testing.T) {
		q := f.file("h.txt", "aaaaa\n")
		f.ok(f.read, in{"file_path": q})
		res := f.run(f.edit, in{"file_path": q, "old_string": "aa", "new_string": "b", "replace_all": true})
		if got, want := f.content(q), "bba\n"; got != want {
			t.Fatalf("file holds %q, want %q", got, want)
		}
		if got := metaOf(t, res, "replacements"); got != 2 {
			t.Errorf("Meta replacements = %v", got)
		}
	})
	t.Run("replace_all with one match is fine", func(t *testing.T) {
		q := f.file("i.txt", "only once\n")
		f.ok(f.read, in{"file_path": q})
		f.ok(f.edit, in{"file_path": q, "old_string": "once", "new_string": "ONCE", "replace_all": "true"})
		if got := f.content(q); got != "only ONCE\n" {
			t.Errorf("file holds %q", got)
		}
	})
	t.Run("many matches are summed up", func(t *testing.T) {
		q := f.file("j.txt", strings.Repeat("dup\n", 20))
		f.ok(f.read, in{"file_path": q})
		f.fails(f.edit, in{"file_path": q, "old_string": "dup", "new_string": "x"}, "found 20 times", "at lines 1, 2, 3, 4, 5, 6, 7, 8 and 12 more")
	})
}

func TestEditKeepsLineEndings(t *testing.T) {
	tests := []struct {
		name     string
		file     string
		old, new string
		want     string
	}{
		{"CRLF file, plain line breaks in the call", "one\r\ntwo\r\nthree\r\n", "one\ntwo", "ONE\nTWO\nextra", "ONE\r\nTWO\r\nextra\r\nthree\r\n"},
		{"CRLF file, CRLF in the call", "one\r\ntwo\r\nthree\r\n", "two\r\nthree\r\n", "2\r\n3\r\n", "one\r\n2\r\n3\r\n"},
		{"CRLF file, one line changed", "one\r\ntwo\r\n", "two", "TWO", "one\r\nTWO\r\n"},
		{"CRLF file, lines added", "one\r\ntwo\r\n", "one", "one\nand a half", "one\r\nand a half\r\ntwo\r\n"},
		{"CRLF file, lines removed", "one\r\ntwo\r\nthree\r\n", "two\n", "", "one\r\nthree\r\n"},
		{"CRLF file, a match that starts at a line break", "a\r\nb\r\nc\r\n", "\nb", "\nB", "a\r\nB\r\nc\r\n"},
		{"CRLF file, a match that ends at a line break", "a\r\nb\r\nc\r\n", "a\n", "A\n", "A\r\nb\r\nc\r\n"},
		{"CRLF file, last line has no line break", "a\r\nb", "b", "b\nc", "a\r\nb\r\nc"},
		{"LF file, CRLF in the call", "a\nb\n", "a", "x\r\ny", "x\ny\nb\n"},
		{"LF file, CRLF in old_string", "a\nb\n", "a\r\nb", "c", "c\n"},
		{"mixed file, at a CRLF line", "crlf\r\nplain\nend\r\n", "crlf", "c1\nc2", "c1\r\nc2\r\nplain\nend\r\n"},
		{"mixed file, at an LF line", "crlf\r\nplain\nend\r\n", "plain", "l1\nl2", "crlf\r\nl1\nl2\nend\r\n"},
		{"mixed file, across both", "crlf\r\nplain\nend\r\n", "crlf\nplain\nend", "x\ny\nz", "x\r\ny\r\nz\r\n"},
		{"one line and no line break", "abc", "b", "b\nB", "ab\nBc"},
		{"a lone carriage return is an ordinary character", "a\rb\n", "a\rb", "a b", "a b\n"},
		{"two carriage returns before a line break", "a\r\r\nb\r\n", "b", "B", "a\r\r\nB\r\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t, Options{})
			p := f.file("f.txt", tt.file)
			res := f.readThenEdit(p, tt.old, tt.new)
			if got := f.content(p); got != tt.want {
				t.Errorf("file holds %q, want %q", got, tt.want)
			}
			if res.Display.Before != tt.file || res.Display.After != tt.want {
				t.Errorf("Display = %+v", res.Display)
			}
		})
	}
	t.Run("a change of line endings alone is no change", func(t *testing.T) {
		f := newFixture(t, Options{})
		p := f.file("f.txt", "a\r\nb\r\n")
		f.ok(f.read, in{"file_path": p})
		f.fails(f.edit, in{"file_path": p, "old_string": "a\r\nb", "new_string": "a\nb"}, "exactly as it is", "differ only in how their lines end")
		if got := f.content(p); got != "a\r\nb\r\n" {
			t.Errorf("file holds %q", got)
		}
	})
	t.Run("replace_all across a mixed file", func(t *testing.T) {
		f := newFixture(t, Options{})
		p := f.file("f.txt", "k\r\nv\r\nk\nv\n")
		f.ok(f.read, in{"file_path": p})
		f.ok(f.edit, in{"file_path": p, "old_string": "k\n", "new_string": "k\nextra\n", "replace_all": true})
		if got, want := f.content(p), "k\r\nextra\r\nv\r\nk\nextra\nv\n"; got != want {
			t.Errorf("file holds %q, want %q", got, want)
		}
	})
}

func TestEditKeepsTheRest(t *testing.T) {
	t.Run("byte order mark", func(t *testing.T) {
		f := newFixture(t, Options{})
		p := f.file("bom.txt", utf8BOM+"first\nsecond\n")
		f.readThenEdit(p, "first", "1st")
		if got, want := f.content(p), utf8BOM+"1st\nsecond\n"; got != want {
			t.Errorf("file holds %q, want %q", got, want)
		}
		// The mark is not text: it cannot be matched or removed by an edit.
		f.fails(f.edit, in{"file_path": p, "old_string": utf8BOM + "1st", "new_string": "x"}, "was not found")
	})
	t.Run("no line break at the end", func(t *testing.T) {
		f := newFixture(t, Options{})
		p := f.file("f.txt", "a\nb")
		f.readThenEdit(p, "a", "A")
		if got := f.content(p); got != "A\nb" {
			t.Errorf("file holds %q", got)
		}
	})
	t.Run("permissions", func(t *testing.T) {
		f := newFixture(t, Options{})
		p := f.file("run.sh", "#!/bin/sh\necho hi\n")
		if err := os.Chmod(p, 0o750); err != nil {
			t.Fatal(err)
		}
		f.readThenEdit(p, "hi", "ho")
		info, _ := os.Stat(p)
		if info.Mode().Perm() != 0o750 {
			t.Errorf("mode = %o, want 750", info.Mode().Perm())
		}
	})
	t.Run("through a link", func(t *testing.T) {
		f := newFixture(t, Options{})
		target := f.file("real.txt", "linked\n")
		link := f.symlink("real.txt", "link.txt")
		res := f.readThenEdit(link, "linked", "LINKED")
		if got := f.content(target); got != "LINKED\n" {
			t.Errorf("the target holds %q", got)
		}
		if !isLink(link) {
			t.Error("the link was replaced by a file")
		}
		mustContain(t, core.TextOf(res.Content), "Edited "+link+" (which leads to "+target+")")
		if got := metaOf(t, res, "realPath"); got != target {
			t.Errorf("Meta realPath = %v", got)
		}
		act, err := f.describe(f.edit, in{"file_path": link, "old_string": "LINKED", "new_string": "x"})
		if err != nil || len(act.Paths) != 2 || act.Paths[0] != link || act.Paths[1] != target {
			t.Errorf("Describe paths = %v, err %v", act.Paths, err)
		}
	})
	t.Run("every other byte", func(t *testing.T) {
		f := newFixture(t, Options{})
		// Odd but valid text around the edit must come through untouched.
		odd := "tabs\t\tand  spaces \n\n\n\x01control\x7f\n" + strings.Repeat("é日\U0001F600", 100) + "\ntrailing   \n\n"
		p := f.file("odd.txt", odd+"TARGET\n"+odd)
		f.readThenEdit(p, "TARGET", "changed")
		if got := f.content(p); got != odd+"changed\n"+odd {
			t.Error("bytes away from the edit were changed")
		}
	})
}

func TestEditRefusals(t *testing.T) {
	f := newFixture(t, Options{EditMaxBytes: 200})
	p := f.file("a.txt", "hello\n")
	f.ok(f.read, in{"file_path": p})
	tests := []struct {
		name  string
		input in
		wants []string
	}{
		{"empty old_string", in{"file_path": p, "old_string": "", "new_string": "x"}, []string{"old_string is empty", "Write"}},
		{"old and new the same", in{"file_path": p, "old_string": "hello", "new_string": "hello"}, []string{"are the same", "nothing to change"}},
		{"no old_string", in{"file_path": p, "new_string": "x"}, []string{"old_string is required"}},
		{"no new_string", in{"file_path": p, "old_string": "hello"}, []string{"new_string is required"}},
		{"no path", in{"old_string": "a", "new_string": "b"}, []string{"file_path is required"}},
		{"path of the wrong kind", in{"file_path": 7, "old_string": "a", "new_string": "b"}, []string{"file_path must be a string"}},
		{"old_string of the wrong kind", in{"file_path": p, "old_string": []string{"a"}, "new_string": "b"}, []string{"old_string must be a string, not a list"}},
		{"replace_all of the wrong kind", in{"file_path": p, "old_string": "hello", "new_string": "b", "replace_all": "yes"}, []string{"replace_all must be true or false"}},
		{"unknown argument", in{"file_path": p, "old_string": "hello", "new_string": "b", "count": 2}, []string{`takes no argument called "count"`}},
		{"a directory", in{"file_path": f.dir, "old_string": "a", "new_string": "b"}, []string{"is a directory"}},
		{"tilde", in{"file_path": "~/a.txt", "old_string": "a", "new_string": "b"}, []string{"not expanded"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f.fails(f.edit, tt.input, tt.wants...)
			if _, err := f.describe(f.edit, tt.input); err == nil {
				t.Error("Describe accepted the call")
			}
		})
	}
	if got := f.content(p); got != "hello\n" {
		t.Fatalf("a refused edit changed the file: %q", got)
	}

	t.Run("not valid UTF-8", func(t *testing.T) {
		q := f.file("latin1.txt", "caf\xe9 cr\xe8me\nplain line\n")
		f.ok(f.read, in{"file_path": q})
		f.fails(f.edit, in{"file_path": q, "old_string": "plain line", "new_string": "x"}, "is not valid UTF-8", "shell command", "encoding")
		if got := f.content(q); got != "caf\xe9 cr\xe8me\nplain line\n" {
			t.Errorf("the file was changed: %q", got)
		}
	})
	t.Run("binary", func(t *testing.T) {
		q := f.file("data.bin", "head\x00\x01\x02tail")
		f.fails(f.read, in{"file_path": q}, "binary")
		f.fails(f.edit, in{"file_path": q, "old_string": "head", "new_string": "x"}, "looks like a binary file", "only changes text")
	})
	t.Run("too large", func(t *testing.T) {
		q := f.file("big.txt", manyLines(40))
		f.ok(f.read, in{"file_path": q})
		f.fails(f.edit, in{"file_path": q, "old_string": "line 3\n", "new_string": "x\n"}, "more than the 200 bytes that Edit takes on", "shell command")
	})
}

func TestEditOnlyTouchesLinesThatWereShown(t *testing.T) {
	f := newFixture(t, Options{ReadDefaultLines: 10})
	p := f.file("long.txt", manyLines(30))
	f.ok(f.read, in{"file_path": p}) // lines 1-10

	// Inside what was shown: fine, though the file was not read whole.
	f.ok(f.edit, in{"file_path": p, "old_string": "line 5\n", "new_string": "line 5a\nline 5b\n"})
	// Outside it: refused, with what to read.
	msg := f.fails(f.edit, in{"file_path": p, "old_string": "line 20\n", "new_string": "x\n"},
		"The text to replace is at line 21 of "+p, "has not been shown", "it has seen lines 1-11", "Nothing was changed", "offset=21")
	mustNotContain(t, msg, "was not found")
	// The edit moved the lines below it down by one, and what was seen
	// moved with them: old line 10 is line 11 now and was seen; old line 11
	// is line 12 and was not.
	f.ok(f.edit, in{"file_path": p, "old_string": "line 10\n", "new_string": "line ten\n"})
	f.fails(f.edit, in{"file_path": p, "old_string": "line 11\n", "new_string": "x\n"}, "at line 12", "it has seen lines 1-11")
	// A match that runs from seen lines into unseen ones is refused too.
	f.fails(f.edit, in{"file_path": p, "old_string": "line ten\nline 11\n", "new_string": "x\n"}, "at lines 11-12")

	// Reading the lines makes them editable.
	f.ok(f.read, in{"file_path": p, "offset": 12, "limit": 5})
	f.ok(f.edit, in{"file_path": p, "old_string": "line 11\n", "new_string": ""})
	// That edit took a line out; what was seen shrinks by one.
	f.fails(f.edit, in{"file_path": p, "old_string": "line 16\n", "new_string": "x\n"}, "at line 16", "it has seen lines 1-15")
	f.ok(f.edit, in{"file_path": p, "old_string": "line 15\n", "new_string": "line fifteen\n"})

	// Write still wants the whole file seen.
	f.fails(f.write, in{"file_path": p, "content": "x"}, "only been shown lines 1-15")

	want := manyLines(30)
	want = strings.Replace(want, "line 5\n", "line 5a\nline 5b\n", 1)
	want = strings.Replace(want, "line 10\n", "line ten\n", 1)
	want = strings.Replace(want, "line 11\n", "", 1)
	want = strings.Replace(want, "line 15\n", "line fifteen\n", 1)
	if got := f.content(p); got != want {
		t.Errorf("file holds\n%q\nwant\n%q", got, want)
	}

	t.Run("replace_all reaches lines that were not shown", func(t *testing.T) {
		q := f.file("vars.txt", "name = 1\n"+manyLines(20)+"name = 2\n")
		f.ok(f.read, in{"file_path": q})
		f.fails(f.edit, in{"file_path": q, "old_string": "name", "new_string": "title", "replace_all": true},
			"The text to replace is at line 22", "has not been shown", "it has seen lines 1-10")
		if got := f.content(q); !strings.HasPrefix(got, "name = 1\n") {
			t.Error("a refused replace_all changed the file")
		}
		// Uniqueness is judged on the whole file, seen or not.
		f.fails(f.edit, in{"file_path": q, "old_string": "name", "new_string": "title"}, "found 2 times", "at lines 1 and 22")
	})
	t.Run("two separate stretches", func(t *testing.T) {
		q := f.file("two.txt", manyLines(40))
		f.ok(f.read, in{"file_path": q, "offset": 5, "limit": 3})
		f.ok(f.read, in{"file_path": q, "offset": 30, "limit": 2})
		f.fails(f.edit, in{"file_path": q, "old_string": "line 20\n", "new_string": "x\n"}, "it has seen lines 5-7 and 30-31")
		// An edit in the first stretch moves the second.
		f.ok(f.edit, in{"file_path": q, "old_string": "line 6\n", "new_string": "6a\n6b\n6c\n"})
		f.ok(f.edit, in{"file_path": q, "old_string": "line 31\n", "new_string": "thirty-one\n"})
		f.fails(f.edit, in{"file_path": q, "old_string": "line 32\n", "new_string": "x\n"}, "at line 34", "it has seen lines 5-9 and 32-33")
	})
}

func TestEditDescribeMatchesRun(t *testing.T) {
	f := newFixture(t, Options{})
	p := f.file("a.txt", "one\ntwo\nthree\ntwo\n")
	f.ok(f.read, in{"file_path": p})
	input := in{"file_path": p, "old_string": "two", "new_string": "2", "replace_all": true}
	act, err := f.describe(f.edit, input)
	if err != nil {
		t.Fatal(err)
	}
	if act.Kind != core.ActionEdit || len(act.Paths) != 1 || act.Paths[0] != p {
		t.Errorf("action = %+v", act)
	}
	if act.Summary != "Edit "+p+": replace 2 occurrences, at lines 2 and 4" {
		t.Errorf("summary = %q", act.Summary)
	}
	want := core.Display{Kind: "diff", Title: "a.txt", Path: p, Before: "one\ntwo\nthree\ntwo\n", After: "one\n2\nthree\n2\n"}
	if act.Preview == nil || *act.Preview != want {
		t.Errorf("Preview = %+v, want %+v", act.Preview, want)
	}
	if got := f.content(p); got != "one\ntwo\nthree\ntwo\n" {
		t.Fatal("Describe changed the file")
	}
	// Describing twice, or describing and never running, changes nothing.
	if act2, _ := f.describe(f.edit, input); *act2.Preview != want {
		t.Error("a second Describe gave another preview")
	}
	res := f.run(f.edit, input)
	if res.Display == nil || *res.Display != want {
		t.Errorf("Display = %+v, want the preview", res.Display)
	}
	act, _ = f.describe(f.edit, in{"file_path": p, "old_string": "three", "new_string": "3"})
	if act.Summary != "Edit "+p+": replace 1 occurrence, at line 3" {
		t.Errorf("summary = %q", act.Summary)
	}
}

func TestEditsOfOneFileAtOnceAreAllKept(t *testing.T) {
	f := newFixture(t, Options{})
	const n = 40
	var b strings.Builder
	for i := 0; i < n; i++ {
		fmt.Fprintf(&b, "<slot %d>\n", i)
	}
	p := f.file("shared.txt", b.String())
	f.ok(f.read, in{"file_path": p})

	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Go(func() {
			input := in{"file_path": p, "old_string": fmt.Sprintf("<slot %d>", i), "new_string": fmt.Sprintf("[done %d]", i)}
			res, err := f.edit.Run(context.Background(), call(input), f.env)
			if err != nil || res.IsError {
				t.Errorf("edit %d: %v %s", i, err, core.TextOf(res.Content))
			}
		})
	}
	wg.Wait()
	got := f.content(p)
	for i := 0; i < n; i++ {
		if !strings.Contains(got, fmt.Sprintf("[done %d]\n", i)) {
			t.Errorf("the edit of slot %d was lost", i)
		}
	}
	if strings.Contains(got, "<slot") {
		t.Errorf("some slots were not edited:\n%s", got)
	}
	noTempFiles(t, f.dir)
}

func TestEditStopsWhenCancelled(t *testing.T) {
	f := newFixture(t, Options{})
	p := f.file("a.txt", "keep\n")
	f.ok(f.read, in{"file_path": p})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := f.edit.Run(ctx, call(in{"file_path": p, "old_string": "keep", "new_string": "lose"}), f.env)
	if err == nil || !strings.Contains(err.Error(), "context canceled") {
		t.Fatalf("err = %v, want a cancellation", err)
	}
	if got := f.content(p); got != "keep\n" {
		t.Errorf("a cancelled edit changed the file: %q", got)
	}
}

// BeforeChange is told of each file Write and Edit are about to change,
// with the file still as it was; its refusal stops the change; and a Write
// that changes nothing tells it nothing.
func TestBeforeChange(t *testing.T) {
	type seen struct{ path, content string }
	var told []seen
	refuse := false
	f := newFixture(t, Options{BeforeChange: func(env *core.ToolEnv, path string) error {
		if refuse {
			return errors.New("it could not be kept")
		}
		data, err := os.ReadFile(path)
		if err != nil {
			data = []byte("(none)")
		}
		told = append(told, seen{path, string(data)})
		return nil
	}})
	a := f.file("a.txt", "one\n")
	f.ok(f.read, in{"file_path": a})
	f.ok(f.edit, in{"file_path": a, "old_string": "one", "new_string": "two"})
	f.ok(f.write, in{"file_path": a, "content": "three\n"})
	f.ok(f.write, in{"file_path": a, "content": "three\n"}) // the same bytes: nothing is written
	n := f.path("new.txt")
	f.ok(f.write, in{"file_path": n, "content": "new\n"})
	want := []seen{{a, "one\n"}, {a, "two\n"}, {n, "(none)"}}
	if len(told) != len(want) {
		t.Fatalf("BeforeChange was told %v, want %v", told, want)
	}
	for i := range want {
		if told[i] != want[i] {
			t.Errorf("call %d: told %v, want %v", i, told[i], want[i])
		}
	}

	// A refusal stops the change, and the model reads why.
	refuse = true
	f.fails(f.write, in{"file_path": a, "content": "four\n"}, "was not changed", "it could not be kept")
	f.fails(f.edit, in{"file_path": a, "old_string": "three", "new_string": "five"}, "was not changed", "it could not be kept")
	if got := f.content(a); got != "three\n" {
		t.Errorf("after the refusals the file holds %q", got)
	}
}
