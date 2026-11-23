package fstools

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gnosh-ai/gnosh/internal/core"
)

func TestReadShowsNumberedLines(t *testing.T) {
	f := newFixture(t, Options{})
	p := f.file("a.txt", "alpha\nbeta\n\ngamma\n")
	res := f.run(f.read, in{"file_path": p})
	if res.IsError {
		t.Fatalf("failed: %s", core.TextOf(res.Content))
	}
	want := "1\talpha\n2\tbeta\n3\t\n4\tgamma\n"
	if got := core.TextOf(res.Content); got != want {
		t.Errorf("text = %q, want %q", got, want)
	}
	if got := metaOf(t, res, "type"); got != "text" {
		t.Errorf("Meta type = %v", got)
	}
	if got := metaOf(t, res, "file", "filePath"); got != p {
		t.Errorf("Meta file.filePath = %v, want %s", got, p)
	}
	for key, want := range map[string]int{"numLines": 4, "startLine": 1, "totalLines": 4} {
		if got := metaOf(t, res, "file", key); got != want {
			t.Errorf("Meta file.%s = %v, want %d", key, got, want)
		}
	}
	if _, ok := res.Meta["truncated"]; ok {
		t.Error("Meta says truncated for a file shown whole")
	}
	if res.Display != nil {
		t.Error("Read set a Display")
	}
}

func TestReadOffsetAndLimit(t *testing.T) {
	f := newFixture(t, Options{})
	p := f.file("ten.txt", manyLines(10))
	tests := []struct {
		name  string
		input in
		want  string // the numbered lines
		note  string // a piece of the note, or "" for no note at all
	}{
		{"whole file", in{"file_path": p}, numberedText(1, strings.Split(strings.TrimSuffix(manyLines(10), "\n"), "\n")...), ""},
		{"offset zero means the top", in{"file_path": p, "offset": 0, "limit": 2}, numberedText(1, "line 1", "line 2"), "Showing lines 1-2 of 10. To read on, call Read again with offset=3."},
		{"limit only", in{"file_path": p, "limit": 3}, numberedText(1, "line 1", "line 2", "line 3"), "Showing lines 1-3 of 10. To read on, call Read again with offset=4."},
		{"offset only", in{"file_path": p, "offset": 8}, numberedText(8, "line 8", "line 9", "line 10"), "Showing lines 8-10 of 10: this is the end of the file."},
		{"offset and limit", in{"file_path": p, "offset": 4, "limit": 2}, numberedText(4, "line 4", "line 5"), "Showing lines 4-5 of 10. To read on, call Read again with offset=6."},
		{"limit past the end", in{"file_path": p, "offset": 9, "limit": 50}, numberedText(9, "line 9", "line 10"), "Showing lines 9-10 of 10: this is the end of the file."},
		{"limit zero means the default", in{"file_path": p, "limit": 0}, numberedText(1, strings.Split(strings.TrimSuffix(manyLines(10), "\n"), "\n")...), ""},
		{"last line exactly", in{"file_path": p, "offset": 10, "limit": 1}, numberedText(10, "line 10"), "Showing lines 10-10 of 10: this is the end of the file."},
		{"numbers as strings", in{"file_path": p, "offset": "2", "limit": "1"}, numberedText(2, "line 2"), "Showing lines 2-2 of 10."},
		{"numbers with an empty fraction", in{"file_path": p, "offset": 2.0, "limit": 1.0}, numberedText(2, "line 2"), "Showing lines 2-2 of 10."},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := f.ok(f.read, tt.input)
			if !strings.HasPrefix(got, tt.want) {
				t.Fatalf("text = %q, want it to start with %q", got, tt.want)
			}
			rest := got[len(tt.want):]
			if tt.note == "" {
				if rest != "" {
					t.Errorf("unexpected note: %q", rest)
				}
				return
			}
			if !strings.HasPrefix(rest, "\n[") || !strings.HasSuffix(rest, "]\n") || !strings.Contains(rest, tt.note) {
				t.Errorf("note = %q, want a bracketed note holding %q", rest, tt.note)
			}
		})
	}
}

func TestReadOffsetPastTheEnd(t *testing.T) {
	f := newFixture(t, Options{})
	p := f.file("three.txt", "a\nb\nc\n")
	res := f.run(f.read, in{"file_path": p, "offset": 7})
	if res.IsError {
		t.Fatal("an offset past the end is a notice, not an error")
	}
	mustContain(t, core.TextOf(res.Content), "offset 7 is past the end", "3 lines")
	if got := metaOf(t, res, "file", "numLines"); got != 0 {
		t.Errorf("numLines = %v, want 0", got)
	}
	// A read that showed nothing does not count as having read the file.
	f.fails(f.edit, in{"file_path": p, "old_string": "a", "new_string": "x"}, "has not read")
}

func TestReadEmptyFile(t *testing.T) {
	f := newFixture(t, Options{})
	p := f.file("empty", "")
	res := f.run(f.read, in{"file_path": p})
	if res.IsError {
		t.Fatal("an empty file is a notice, not an error")
	}
	mustContain(t, core.TextOf(res.Content), "exists but is empty")
	if st := States(f.env); len(st) != 1 || !st[0].Full || st[0].Path != p {
		t.Errorf("States = %+v, want the empty file seen in full", st)
	}

	// A file that holds a byte order mark and nothing else has no text.
	bom := f.file("bom-only", utf8BOM)
	res = f.run(f.read, in{"file_path": bom})
	if res.IsError {
		t.Fatal("a file with only a byte order mark is a notice, not an error")
	}
	mustContain(t, core.TextOf(res.Content), "no text in it", "byte order mark")
}

func TestReadDefaultLineCapAndPaging(t *testing.T) {
	f := newFixture(t, Options{})
	p := f.file("long.txt", manyLines(4500))
	text := f.ok(f.read, in{"file_path": p})
	if n := strings.Count(text, "\tline "); n != 2000 {
		t.Fatalf("showed %d lines, want the default 2000", n)
	}
	mustContain(t, text, "2000\tline 2000\n", "[Showing lines 1-2000 of 4500. To read on, call Read again with offset=2001.]")
	mustNotContain(t, text, "2001\t")
	if st := States(f.env); len(st) != 1 || st[0].Full {
		t.Fatalf("States = %+v, want the file seen in part", st)
	}
	// Paging through to the end adds up to having seen it all.
	f.ok(f.read, in{"file_path": p, "offset": 2001})
	if st := States(f.env); st[0].Full {
		t.Fatal("seen in full after two of three pages")
	}
	text = f.ok(f.read, in{"file_path": p, "offset": 4001})
	mustContain(t, text, "4500\tline 4500\n", "Showing lines 4001-4500 of 4500: this is the end of the file.")
	if st := States(f.env); !st[0].Full {
		t.Fatal("not seen in full after reading every page")
	}
	// An explicit limit may ask for more than the default.
	text = f.ok(f.read, in{"file_path": p, "limit": 3000})
	if n := strings.Count(text, "\tline "); n != 3000 {
		t.Errorf("limit 3000 showed %d lines", n)
	}
}

func TestReadByteCap(t *testing.T) {
	f := newFixture(t, Options{ReadMaxBytes: 100})
	// Each line is 10 bytes with its line break: ten fit in 100 bytes.
	p := f.file("bytes.txt", strings.Repeat("123456789\n", 50))
	res := f.run(f.read, in{"file_path": p})
	text := core.TextOf(res.Content)
	if n := strings.Count(text, "\t123456789\n"); n != 10 {
		t.Fatalf("showed %d lines, want 10 (100 bytes)", n)
	}
	mustContain(t, text, "Showing lines 1-10 of 50.", "stopped at the 100 bytes one call may show", "offset=11")
	if metaOf(t, res, "truncated") != true {
		t.Error("Meta does not say truncated")
	}
	// The cap holds whatever limit is asked for.
	text = f.ok(f.read, in{"file_path": p, "offset": 11, "limit": 40})
	if n := strings.Count(text, "\t123456789\n"); n != 10 {
		t.Errorf("with limit 40 showed %d lines, want 10", n)
	}
	mustContain(t, text, "Showing lines 11-20 of 50.")
	// One line is always shown, however long.
	big := f.file("one.txt", strings.Repeat("x", 500)+"\nnext\n")
	text = f.ok(f.read, in{"file_path": big})
	mustContain(t, text, "1\t"+strings.Repeat("x", 500)+"\n", "Showing lines 1-1 of 2.")
}

func TestReadCutsLongLines(t *testing.T) {
	f := newFixture(t, Options{ReadMaxLineChars: 10})
	p := f.file("wide.txt", "short\n"+strings.Repeat("a", 25)+"\nexactly-10\n"+strings.Repeat("日", 12)+"\nlast\n")
	text := f.ok(f.read, in{"file_path": p})
	want := "1\tshort\n" +
		"2\taaaaaaaaaa [... 15 more characters on this line]\n" +
		"3\texactly-10\n" +
		"4\t" + strings.Repeat("日", 10) + " [... 2 more characters on this line]\n" +
		"5\tlast\n"
	if text != want {
		t.Errorf("text =\n%q\nwant\n%q", text, want)
	}
	// A line cut short still counts as seen.
	if st := States(f.env); !st[0].Full {
		t.Error("a file with a cut line is not counted as seen in full")
	}
}

func TestReadHugeLine(t *testing.T) {
	f := newFixture(t, Options{})
	// One line of 5 MiB, then two ordinary lines. The long line must not
	// be held whole to be shown.
	var b bytes.Buffer
	b.WriteString("first\n")
	b.Write(bytes.Repeat([]byte("0123456789abcdef"), 5<<20/16))
	b.WriteString("\nlast\n")
	p := f.file("huge-line.txt", b.String())
	text := f.ok(f.read, in{"file_path": p})
	if len(text) > 4000 {
		t.Fatalf("the result is %d bytes; the long line was not cut", len(text))
	}
	mustContain(t, text, "1\tfirst\n", fmt.Sprintf("[... %d more characters on this line]", 5<<20-2000), "3\tlast\n")
}

func TestReadHugeFile(t *testing.T) {
	if testing.Short() {
		t.Skip("writes a 40 MiB file")
	}
	// Forty MiB of numbered lines, each 32 bytes.
	const lines = 40 << 20 / 32
	var b bytes.Buffer
	b.Grow(lines * 32)
	for i := 1; i <= lines; i++ {
		fmt.Fprintf(&b, "%-31d\n", i)
	}
	f := newFixture(t, Options{})
	p := f.file("big.txt", b.String())

	text := f.ok(f.read, in{"file_path": p})
	mustContain(t, text, "1\t1 ", fmt.Sprintf("[Showing lines 1-2000 of %d. To read on, call Read again with offset=2001.]", lines))
	// Deep in the file.
	text = f.ok(f.read, in{"file_path": p, "offset": lines - 1, "limit": 5})
	mustContain(t, text, fmt.Sprintf("%d\t%d", lines-1, lines-1), fmt.Sprintf("%d\t%d", lines, lines), "this is the end of the file")

	// With a small counting allowance the lines past the page are not
	// counted, and the note says so instead of guessing.
	small := newFixture(t, Options{ReadCountBytes: 1 << 20})
	text = small.ok(small.read, in{"file_path": p})
	mustContain(t, text, "Showing lines 1-2000; the file goes on (it is 40 MiB, and its lines were not all counted).", "offset=2001")
	res := small.run(small.read, in{"file_path": p, "limit": 1})
	if _, ok := metaOf(t, res, "file").(map[string]any)["totalLines"]; ok {
		t.Error("Meta gives a totalLines that was never counted")
	}
	// With none, it stops at the page.
	none := newFixture(t, Options{ReadCountBytes: -1})
	text = none.ok(none.read, in{"file_path": p, "limit": 3})
	mustContain(t, text, "Showing lines 1-3; the file goes on")
}

func TestReadNoTrailingLineBreak(t *testing.T) {
	f := newFixture(t, Options{})
	p := f.file("a.txt", "one\ntwo")
	res := f.run(f.read, in{"file_path": p})
	if got, want := core.TextOf(res.Content), "1\tone\n2\ttwo\n"; got != want {
		t.Errorf("text = %q, want %q", got, want)
	}
	if got := metaOf(t, res, "file", "totalLines"); got != 2 {
		t.Errorf("totalLines = %v, want 2", got)
	}
	one := f.file("b.txt", "only")
	if got, want := f.ok(f.read, in{"file_path": one}), "1\tonly\n"; got != want {
		t.Errorf("text = %q, want %q", got, want)
	}
}

func TestReadLineEndingsAndMarks(t *testing.T) {
	f := newFixture(t, Options{})
	t.Run("CRLF", func(t *testing.T) {
		p := f.file("crlf.txt", "one\r\ntwo\r\nthree\r\n")
		text := f.ok(f.read, in{"file_path": p})
		mustContain(t, text, "1\tone\n2\ttwo\n3\tthree\n", "Lines end with CRLF", "Edit matches line breaks either way")
		mustNotContain(t, text, "\r")
	})
	t.Run("mixed", func(t *testing.T) {
		p := f.file("mixed.txt", "one\r\ntwo\nthree\r\n")
		text := f.ok(f.read, in{"file_path": p})
		mustContain(t, text, "1\tone\n2\ttwo\n3\tthree\n", "Line endings are mixed")
		mustNotContain(t, text, "\r")
	})
	t.Run("a carriage return inside a line stays", func(t *testing.T) {
		p := f.file("cr.txt", "a\rb\nc\n")
		text := f.ok(f.read, in{"file_path": p})
		if text != "1\ta\rb\n2\tc\n" {
			t.Errorf("text = %q", text)
		}
	})
	t.Run("byte order mark", func(t *testing.T) {
		p := f.file("bom.txt", utf8BOM+"first\nsecond\n")
		text := f.ok(f.read, in{"file_path": p})
		mustContain(t, text, "1\tfirst\n2\tsecond\n", "byte order mark")
		// The note is about the top of the file only.
		text = f.ok(f.read, in{"file_path": p, "offset": 2})
		mustNotContain(t, text, "byte order mark")
	})
	t.Run("invalid UTF-8", func(t *testing.T) {
		p := f.file("latin1.txt", "caf\xe9 au lait\nplain\n\xff\xfe\xfdx\n")
		text := f.ok(f.read, in{"file_path": p})
		mustContain(t, text, "1\tcaf"+replacement+" au lait\n2\tplain\n3\t"+replacement+"x\n", "in 2 places", "not valid UTF-8", "U+FFFD")
	})
	t.Run("a NUL byte past the start is replaced", func(t *testing.T) {
		p := f.file("nul.txt", strings.Repeat("text line\n", 1000)+"bad\x00byte\n")
		text := f.ok(f.read, in{"file_path": p, "offset": 1001})
		mustContain(t, text, "1001\tbad"+replacement+"byte\n")
	})
}

func TestReadRefusesWhatIsNotText(t *testing.T) {
	f := newFixture(t, Options{})
	t.Run("binary", func(t *testing.T) {
		p := f.file("prog.bin", "\x7fELF\x02\x01\x01\x00\x00\x00\x00binary stuff")
		f.fails(f.read, in{"file_path": p}, "looks like a binary file", "not shown as text", "shell command")
		// Having been told what it is, the model may replace it.
		f.ok(f.write, in{"file_path": p, "content": "now text\n"})
	})
	t.Run("UTF-16", func(t *testing.T) {
		p := f.file("wide.txt", "\xff\xfeh\x00i\x00\n\x00")
		f.fails(f.read, in{"file_path": p}, "looks like a binary file", "UTF-16", "iconv")
	})
	t.Run("pages on a file that is not a PDF", func(t *testing.T) {
		p := f.file("plain.txt", "text\n")
		f.fails(f.read, in{"file_path": p, "pages": "1-3"}, "pages is for PDF files", "offset and limit")
	})
	t.Run("a notebook that is not one", func(t *testing.T) {
		p := f.file("nb.ipynb", `{"worksheets": []}`)
		f.fails(f.read, in{"file_path": p}, "could not be read as a notebook", "nbformat 3")
		f.fails(f.read, in{"file_path": f.file("bad.ipynb", `{"cells": 3}`)}, "no list of cells")
	})
	t.Run("directory", func(t *testing.T) {
		d := f.mkdir("some/dir")
		f.fails(f.read, in{"file_path": d}, d+" is a directory", "Glob")
	})
}

func TestReadImages(t *testing.T) {
	f := newFixture(t, Options{ReadMaxImageBytes: 4096})
	images := map[string]struct{ data, media, kind string }{
		"a.png":         {"\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00", "image/png", "PNG"},
		"b.jpg":         {"\xff\xd8\xff\xe0\x00\x10JFIF\x00\x01", "image/jpeg", "JPEG"},
		"c.gif":         {"GIF89a\x01\x00\x01\x00\x80\x00\x00", "image/gif", "GIF"},
		"old.gif":       {"GIF87a\x01\x00\x01\x00\x80\x00\x00", "image/gif", "GIF"},
		"d.webp":        {"RIFF\x24\x00\x00\x00WEBPVP8 \x18\x00\x00\x00", "image/webp", "WEBP"},
		"wrong-name.md": {"\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR", "image/png", "PNG"},
	}
	for name, img := range images {
		t.Run(name, func(t *testing.T) {
			p := f.file(name, img.data)
			res := f.run(f.read, in{"file_path": p})
			if res.IsError {
				t.Fatalf("failed: %s", core.TextOf(res.Content))
			}
			if len(res.Content) != 2 || res.Content[0].Type != core.BlockText || res.Content[1].Type != core.BlockImage {
				t.Fatalf("content = %+v, want a text block and an image block", res.Content)
			}
			mustContain(t, res.Content[0].Text, p, img.kind+" image")
			blk := res.Content[1]
			if blk.MediaType != img.media || string(blk.Data) != img.data {
				t.Errorf("image block: media %q, %d bytes; want %q, %d bytes", blk.MediaType, len(blk.Data), img.media, len(img.data))
			}
			if got := metaOf(t, res, "file", "type"); got != img.media {
				t.Errorf("Meta file.type = %v", got)
			}
			if got := metaOf(t, res, "file", "originalSize"); got != len(img.data) {
				t.Errorf("Meta file.originalSize = %v", got)
			}
		})
	}
	t.Run("too large", func(t *testing.T) {
		p := f.file("big.png", "\x89PNG\r\n\x1a\n"+strings.Repeat("\x00", 5000))
		f.fails(f.read, in{"file_path": p}, "is an image of", "more than the 4 KiB", "smaller copy")
	})
	t.Run("exactly at the limit", func(t *testing.T) {
		p := f.file("fits.png", "\x89PNG\r\n\x1a\n"+strings.Repeat("\x00", 4096-8))
		res := f.run(f.read, in{"file_path": p})
		if res.IsError || len(res.Content) != 2 || len(res.Content[1].Data) != 4096 {
			t.Errorf("an image of exactly the limit was not returned whole: %s", core.TextOf(res.Content))
		}
	})
	t.Run("a name alone does not make an image", func(t *testing.T) {
		p := f.file("fake.png", "just text\n")
		if got := f.ok(f.read, in{"file_path": p}); got != "1\tjust text\n" {
			t.Errorf("text = %q", got)
		}
	})
	t.Run("larger than one read", func(t *testing.T) {
		big := newFixture(t, Options{})
		data := "\x89PNG\r\n\x1a\n" + strings.Repeat("\x01\x02\x03\x04", 50000)
		p := big.file("large.png", data)
		res := big.run(big.read, in{"file_path": p})
		if res.IsError || len(res.Content) != 2 || string(res.Content[1].Data) != data {
			t.Errorf("a 200 KB image was not returned whole")
		}
	})
}

func TestReadMissingFile(t *testing.T) {
	f := newFixture(t, Options{})
	f.file("src/Main.go", "package main\n")
	f.file("src/util.txt", "x\n")
	t.Run("plain", func(t *testing.T) {
		f.fails(f.read, in{"file_path": f.path("src/nothing-like-it")}, f.path("src/nothing-like-it")+" does not exist.")
	})
	t.Run("same name, other extension", func(t *testing.T) {
		msg := f.fails(f.read, in{"file_path": f.path("src/util.go")}, "does not exist", "In that directory there is: util.txt")
		mustNotContain(t, msg, "Main.go")
	})
	t.Run("missing directory", func(t *testing.T) {
		f.fails(f.read, in{"file_path": f.path("src/deep/er/file.go")},
			"does not exist", "there is no directory "+f.path("src/deep/er"), "the path exists as far as "+f.path("src"))
	})
	t.Run("a file on the way", func(t *testing.T) {
		f.fails(f.read, in{"file_path": f.path("src/util.txt/inner")}, "cannot exist", f.path("src/util.txt"), "is a file, not a directory")
	})
}

func TestReadFollowsLinks(t *testing.T) {
	f := newFixture(t, Options{})
	target := f.file("real/target.txt", "through the link\n")
	t.Run("to a file", func(t *testing.T) {
		link := f.symlink("real/target.txt", "link.txt")
		res := f.run(f.read, in{"file_path": link})
		if got := core.TextOf(res.Content); got != "1\tthrough the link\n" {
			t.Fatalf("text = %q", got)
		}
		if got := metaOf(t, res, "file", "filePath"); got != link {
			t.Errorf("Meta file.filePath = %v, want the path as asked, %s", got, link)
		}
		if got := metaOf(t, res, "realPath"); got != target {
			t.Errorf("Meta realPath = %v, want %s", got, target)
		}
		// What was read is the file, under whatever name: an edit by the
		// real path needs no second read.
		f.ok(f.edit, in{"file_path": target, "old_string": "through", "new_string": "past"})
	})
	t.Run("through a linked directory", func(t *testing.T) {
		f.symlink("real", "dirlink")
		if got := f.ok(f.read, in{"file_path": f.path("dirlink/target.txt")}); !strings.Contains(got, "the link") {
			t.Errorf("text = %q", got)
		}
	})
	t.Run("a chain with .. in it", func(t *testing.T) {
		f.mkdir("deep/down")
		f.symlink("../../real/target.txt", "deep/down/up.txt")
		f.symlink("deep/down/up.txt", "chain.txt")
		if got := f.ok(f.read, in{"file_path": f.path("chain.txt")}); !strings.Contains(got, "the link") {
			t.Errorf("text = %q", got)
		}
	})
	t.Run("to a directory", func(t *testing.T) {
		link := f.symlink("real", "to-dir")
		f.fails(f.read, in{"file_path": link}, "is a directory")
	})
	t.Run("broken", func(t *testing.T) {
		link := f.symlink("nowhere/at/all", "broken")
		f.fails(f.read, in{"file_path": link}, "does not exist", link+" is a symbolic link to "+f.path("nowhere/at/all"))
	})
	t.Run("loop", func(t *testing.T) {
		f.symlink("loop-b", "loop-a")
		f.symlink("loop-a", "loop-b")
		f.fails(f.read, in{"file_path": f.path("loop-a")}, "cannot be followed", "loop")
		f.symlink("self", "self")
		f.fails(f.read, in{"file_path": f.path("self/x")}, "cannot be followed", "loop")
	})
}

func TestReadArguments(t *testing.T) {
	f := newFixture(t, Options{})
	p := f.file("a.txt", "hello\n")
	tests := []struct {
		name  string
		input in
		wants []string
	}{
		{"no path", in{}, []string{"file_path is required"}},
		{"null path", in{"file_path": nil}, []string{"file_path is required"}},
		{"empty path", in{"file_path": ""}, []string{"file_path is empty"}},
		{"path of the wrong kind", in{"file_path": 12}, []string{"file_path must be a string, not a number"}},
		{"path as a list", in{"file_path": []string{p}}, []string{"file_path must be a string, not a list"}},
		{"unknown argument", in{"file_path": p, "lines": 3}, []string{`takes no argument called "lines"`, "file_path, offset, limit"}},
		{"negative offset", in{"file_path": p, "offset": -1}, []string{"offset must not be negative"}},
		{"negative limit", in{"file_path": p, "limit": -5}, []string{"limit must not be negative"}},
		{"fractional offset", in{"file_path": p, "offset": 1.5}, []string{"offset must be a whole number"}},
		{"offset in words", in{"file_path": p, "offset": "ten"}, []string{"offset must be a whole number"}},
		{"offset as true", in{"file_path": p, "offset": true}, []string{"offset must be a whole number"}},
		{"tilde", in{"file_path": "~/notes.txt"}, []string{`starts with "~"`, "not expanded", "absolute path"}},
		{"tilde user", in{"file_path": "~root/notes.txt"}, []string{"not expanded"}},
		{"NUL byte", in{"file_path": p + "\x00.png"}, []string{"NUL byte"}},
		{"trailing slash", in{"file_path": p + "/"}, []string{"ends with a slash", "directory"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f.fails(f.read, tt.input, tt.wants...)
			// What Run refuses, Describe refuses in the same words.
			_, err := f.describe(f.read, tt.input)
			if err == nil {
				t.Fatal("Describe accepted the call")
			}
			for _, want := range tt.wants {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("Describe error %q does not mention %q", err, want)
				}
			}
		})
	}
	t.Run("a relative path is taken from the working directory", func(t *testing.T) {
		f.file("sub/b.txt", "relative\n")
		if got := f.ok(f.read, in{"file_path": "sub/b.txt"}); got != "1\trelative\n" {
			t.Errorf("text = %q", got)
		}
		if got := f.ok(f.read, in{"file_path": "./sub/../sub/./b.txt"}); got != "1\trelative\n" {
			t.Errorf("text = %q", got)
		}
	})
	t.Run("a relative path with no working directory", func(t *testing.T) {
		res, err := f.read.Run(context.Background(), call(in{"file_path": "a.txt"}), &core.ToolEnv{Store: f.env.Store})
		if err != nil || !res.IsError || !strings.Contains(core.TextOf(res.Content), "no working directory") {
			t.Errorf("got %v, %q", err, core.TextOf(res.Content))
		}
	})
	t.Run("paths are cleaned", func(t *testing.T) {
		if got := f.ok(f.read, in{"file_path": f.dir + "//sub/../a.txt"}); got != "1\thello\n" {
			t.Errorf("text = %q", got)
		}
	})
	t.Run("the home directory, when one is given", func(t *testing.T) {
		home := newFixture(t, Options{HomeDir: f.dir})
		if got := home.ok(home.read, in{"file_path": "~/a.txt"}); got != "1\thello\n" {
			t.Errorf("text = %q", got)
		}
		home.fails(home.read, in{"file_path": "~other/a.txt"}, "not expanded")
	})
	t.Run("allow_large without the room to read more", func(t *testing.T) {
		small := newFixture(t, Options{ReadDefaultLines: 2})
		big := small.file("b.txt", manyLines(5))
		text := small.ok(small.read, in{"file_path": big, "allow_large": true})
		mustContain(t, text, "Showing lines 1-2 of 5.", "allow_large finds no more room than that here")
	})
	t.Run("no arguments at all", func(t *testing.T) {
		for _, raw := range []string{"", "{}", "null", " "} {
			res, err := f.read.Run(context.Background(), core.ToolCall{Input: []byte(raw)}, f.env)
			if err != nil || !res.IsError || !strings.Contains(core.TextOf(res.Content), "file_path is required") {
				t.Errorf("input %q: got %v, %q", raw, err, core.TextOf(res.Content))
			}
		}
		res, err := f.read.Run(context.Background(), core.ToolCall{Input: []byte(`["a"]`)}, f.env)
		if err != nil || !res.IsError || !strings.Contains(core.TextOf(res.Content), "not one JSON object") {
			t.Errorf("a list as input: got %v, %q", err, core.TextOf(res.Content))
		}
	})
}

func TestReadDescribe(t *testing.T) {
	f := newFixture(t, Options{})
	p := f.file("real/a.txt", "x\n")
	act, err := f.describe(f.read, in{"file_path": p})
	if err != nil {
		t.Fatal(err)
	}
	if act.Kind != core.ActionRead || len(act.Paths) != 1 || act.Paths[0] != p || act.Summary != "Read "+p || act.Preview != nil {
		t.Errorf("action = %+v", act)
	}
	act, _ = f.describe(f.read, in{"file_path": p, "offset": 5, "limit": 20})
	if act.Summary != "Read "+p+", 20 lines from line 5" {
		t.Errorf("summary = %q", act.Summary)
	}
	// A file that is not there is still described: whether it exists is
	// for the call to find out, once it is allowed to look.
	missing := f.path("not/there.txt")
	act, err = f.describe(f.read, in{"file_path": missing})
	if err != nil || len(act.Paths) != 1 || act.Paths[0] != missing {
		t.Errorf("missing file: action %+v, err %v", act, err)
	}
	// Through a link, both the path asked for and where it leads.
	link := f.symlink("real/a.txt", "link.txt")
	act, err = f.describe(f.read, in{"file_path": link})
	if err != nil || len(act.Paths) != 2 || act.Paths[0] != link || act.Paths[1] != p {
		t.Errorf("linked file: paths %v, err %v; want [%s %s]", act.Paths, err, link, p)
	}
	// A path that cannot be followed is described by the path as written.
	f.symlink("round", "round")
	act, err = f.describe(f.read, in{"file_path": f.path("round")})
	if err != nil || len(act.Paths) != 1 || act.Paths[0] != f.path("round") {
		t.Errorf("looping link: action %+v, err %v", act, err)
	}
}

func TestReadStopsWhenCancelled(t *testing.T) {
	f := newFixture(t, Options{})
	p := f.file("big.txt", strings.Repeat("some line of text\n", 200000))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := f.read.Run(ctx, call(in{"file_path": p}), f.env)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	mustContain(t, err.Error(), "Read of "+p+" stopped")
	if len(States(f.env)) != 0 {
		t.Error("a cancelled read was recorded as a read")
	}
}

func TestReadKernelFile(t *testing.T) {
	// Files the kernel makes up on the spot say they are empty and are
	// not. Read must go by what comes out of them.
	const p = "/proc/self/status"
	if _, err := os.Stat(p); err != nil {
		t.Skip("no /proc here")
	}
	f := newFixture(t, Options{})
	text := f.ok(f.read, in{"file_path": p})
	mustContain(t, text, "1\tName:")
}

func TestReadNamesWithOddCharacters(t *testing.T) {
	f := newFixture(t, Options{})
	for _, name := range []string{"with space.txt", "ünï-cødé.txt", "日本語.txt", "#hash", "~tilde-inside", "a'b\"c.txt", "new\nline.txt"} {
		p := f.file(filepath.Join("odd", name), "ok\n")
		if got := f.ok(f.read, in{"file_path": p}); got != "1\tok\n" {
			t.Errorf("%q: text = %q", name, got)
		}
	}
}

// allow_large reads the whole file in one call, as far as the context has
// room for (Options.ReadRoom), as Claude Code's does; without the room, or
// without the argument, the usual limits hold.
func TestReadAllowLarge(t *testing.T) {
	room, used := 50_000, []int{}
	f := newFixture(t, Options{ReadMaxBytes: 1000, ReadRoom: func(context.Context) (int, func(int)) {
		return room, func(n int) { used = append(used, n) }
	}})
	p := f.file("big.txt", manyLines(1500))
	got := f.ok(f.read, in{"file_path": p})
	mustContain(t, got, "This call stopped at the 1000 bytes one call may show")
	got = f.ok(f.read, in{"file_path": p, "allow_large": true})
	mustContain(t, got, "1500\tline 1500\n")
	if strings.Contains(got, "To read on") {
		t.Errorf("a whole read with allow_large says to read on: %s", got[len(got)-200:])
	}
	// What the call used is handed back, line numbers and all, so the
	// rest of the room can go to another call.
	if body, _, _ := strings.Cut(got, "\n["); len(used) != 1 || used[0] != len(body) {
		t.Errorf("used = %v, want [%d]", used, len(body))
	}
	// A limit given still holds.
	got = f.ok(f.read, in{"file_path": p, "allow_large": true, "limit": 10})
	mustContain(t, got, "10\tline 10\n", "Showing lines 1-10")
	// Less room than the file: as much as there is room for.
	room = 5000
	got = f.ok(f.read, in{"file_path": p, "allow_large": true})
	mustContain(t, got, "the context has room for now, with allow_large", "To read on")
	// No room given: the usual limits, and it says so.
	g := newFixture(t, Options{ReadMaxBytes: 1000})
	q := g.file("big.txt", manyLines(1500))
	mustContain(t, g.ok(g.read, in{"file_path": q, "allow_large": true}), "allow_large finds no more room than that here")
}

// A Read never returns more than the loop lets through whole
// (Options.ReadResultMost), with allow_large or without, counting the line
// numbers: a longer result would be cut on its way to the model while
// Read recorded every line as read, and a whole-file Write would then drop
// the lines the model never saw (the review of 2026-11-20).
func TestReadStopsShortOfWhatTheLoopCuts(t *testing.T) {
	const most = 3000
	f := newFixture(t, Options{
		ReadMaxBytes:   100_000,
		ReadRoom:       func(context.Context) (int, func(int)) { return 1 << 20, func(int) {} },
		ReadResultMost: func(context.Context) int { return most },
	})
	// Short lines, so that the line numbers weigh: 1500 lines of two
	// bytes are 4,500 bytes of text, and more than 10,000 shown.
	var b strings.Builder
	for range 1500 {
		b.WriteString("x\n")
	}
	p := f.file("short.txt", b.String())
	for _, large := range []bool{false, true} {
		got := f.ok(f.read, in{"file_path": p, "allow_large": large})
		body, _, _ := strings.Cut(got, "\n[")
		if len(body) > most || len(body) < most-10 {
			t.Errorf("allow_large %v: %d bytes of lines, want just under %d", large, len(body), most)
		}
		mustContain(t, got, "one result may hold for this model", "To read on")
		// Only what was shown counts as read: the file is not replaced
		// whole.
		f.fails(f.write, in{"file_path": p, "content": "y\n"}, "would be replaced whole")
	}
	if f.content(p) != b.String() {
		t.Error("the file was written")
	}
}
