package ui

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/gnosh-ai/gnosh/internal/app"
	"github.com/gnosh-ai/gnosh/internal/text"
	"github.com/gnosh-ai/gnosh/internal/ui/editor"
)

// Shell mode's Tab: where a command goes (the line's first word, after an
// operator, after sudo and its like), the shell's own commands and the
// programs on the PATH; anywhere else, files; nothing for an option or a
// quoted, computed or pattern word.
func TestCompleteShell(t *testing.T) {
	s := &session{cwd: "/proj", home: "/home/u",
		listDir: func(dir string) []fileEntry {
			switch dir {
			case "/proj":
				return []fileEntry{{name: "docs", dir: true}, {name: "main.go"}, {name: "Makefile"}, {name: "scripts", dir: true}}
			case "/proj/scripts":
				return []fileEntry{{name: "build.sh"}}
			}
			return nil
		},
		programs: func() []string { return []string{"apt", "cat", "echo", "git", "gitk", "grep", "make", "true"} },
	}
	offered := func(text string) string {
		var out []string
		for _, c := range s.complete(text, len(text)) {
			// It completes the word it starts at.
			if c.Start > len(text) || !strings.HasPrefix(strings.ToLower(c.Text), strings.ToLower(text[c.Start:])) {
				t.Errorf("%q: candidate %+v", text, c)
			}
			out = append(out, c.Text)
		}
		return strings.Join(out, ",")
	}
	for text, want := range map[string]string{
		"!gi":            "git ,gitk ",
		"! gi":           "git ,gitk ",
		"!e":             "echo ,eval ,exec ,export ",
		"!ls && ca":      "cat ",
		"!ls&&ca":        "cat ",
		"!ls | gr":       "grep ",
		"!make; ec":      "echo ",
		"!(ca":           "cat ",
		"!cmd 2>&1 | gr": "grep ",
		"!sleep 1 & ec":  "echo ",
		"!sudo ap":       "apt ",
		"!if tr":         "true ",
		"!ls\nma":        "make ",
		"!ls ma":         "main.go,Makefile",
		"!ls \\\nma":     "main.go,Makefile",
		"!cd do":         "docs/",
		"!echo x >ma":    "main.go,Makefile",
		"!echo x >&ma":   "main.go,Makefile",
		"!git sudo ma":   "main.go,Makefile",
		"!./scr":         "./scripts/",
		"!sh scripts/b":  "scripts/build.sh",
		"!echo $HO":      "",
		"!ls -l":         "",
		"!ls 'ma":        "",
		"!ls *.g":        "",
		"!":              "",
		"!ls ":           "docs/,main.go,Makefile,scripts/",
		"!ls && ":        "",
	} {
		if got := offered(text); got != want {
			t.Errorf("%q: offered %q, want %q", text, got, want)
		}
	}
	// A line of shell mode is not a prompt: no mention is offered in it.
	if got := offered("!cat @ma"); got != "" {
		t.Errorf("a mention was offered in a shell line: %q", got)
	}
}

// Shell mode's Tab offers the commands run before that begin with the
// line typed so far, the newest first and each once, ahead of what
// completes the word; a single one is taken at once. A prompt that was
// not a shell line is not offered, and the list never opens by itself
// while a shell line is typed, so Enter runs what was typed.
func TestShellTabOffersEarlierCommands(t *testing.T) {
	g := newRig(t, 80, 24, text.NoColor)
	for _, h := range []string{"!git status", "git stash this prompt", "!git stash list", "!git status"} {
		g.s.ed.AddHistory(editor.Draft{Text: h})
	}
	g.files["/home/jae/src/proj"] = []fileEntry{{name: "internal", dir: true}, {name: "stuff.txt"}}
	g.keys(`"!git st"`)
	g.liveLacks("run before", "stuff.txt")
	g.keys(`<Tab>`)
	g.liveHas("git status", "git stash list", "run before", "stuff.txt").liveLacks("git stash this prompt")
	at := -1
	for _, item := range []string{"git status", "git stash list", "stuff.txt"} {
		i := slices.IndexFunc(g.live, func(l string) bool { return strings.Contains(l, item) })
		if i <= at {
			t.Fatalf("%q is not listed after the one before it:\n%s", item, strings.Join(g.live, "\n"))
		}
		at = i
	}
	g.keys(`<Tab>`)
	if g.box() != "!git status" {
		t.Fatalf("the box holds %q", g.box())
	}

	// Enter on the open list runs the line as typed: an older command is
	// taken only when it was chosen with the arrows. (This interface runs
	// no shell lines, so the line comes back to the box.)
	g = newRig(t, 80, 24, text.NoColor)
	g.s.ed.AddHistory(editor.Draft{Text: "!cd /srv/app && git reset --hard"})
	g.files["/"] = []fileEntry{{name: "srv", dir: true}, {name: "tmp", dir: true}}
	g.keys(`"!cd /"`)
	g.liveLacks("run before")
	g.keys(`<Tab>`)
	g.liveHas("cd /srv/app && git reset --hard")
	g.keys(`<Enter>`)
	if g.box() != "!cd /" {
		t.Fatalf("Enter ran %q, not the line as typed", g.box())
	}
	g.keys(`<Ctrl+U> <Ctrl+U> "!cd /" <Tab> <Down> <Up> <Enter>`)
	if g.box() != "!cd /srv/app && git reset --hard" {
		t.Fatalf("the command chosen with the arrows was not taken: the box holds %q", g.box())
	}

	g = newRig(t, 80, 24, text.NoColor)
	g.s.ed.AddHistory(editor.Draft{Text: "!git stash list"})
	g.keys(`"!git stas" <Tab>`)
	if g.box() != "!git stash list" {
		t.Fatalf("a single earlier command was not taken at once: the box holds %q", g.box())
	}

	// A path typed in a shell line opens nothing until Tab.
	g = newRig(t, 80, 24, text.NoColor)
	g.files["/home/jae/src/proj/internal"] = []fileEntry{{name: "app", dir: true}, {name: "ui", dir: true}}
	g.keys(`"!ls internal/"`)
	g.liveLacks("app/", "ui/")
	g.keys(`<Tab>`)
	g.liveHas("app/", "ui/")
}

// The programs of a PATH: files that may be run, each name once, in name
// order; directories, files that may not be run and relative PATH entries
// are passed over.
func TestPathPrograms(t *testing.T) {
	one, two := t.TempDir(), t.TempDir()
	write := func(dir, name string, mode os.FileMode) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"), mode); err != nil {
			t.Fatal(err)
		}
	}
	write(one, "gnoshy", 0o755)
	write(one, "gnosh-data", 0o644)
	write(two, "gnoshy", 0o755)
	write(two, "gnoshctl", 0o700)
	write(two, "other", 0o755)
	if err := os.Mkdir(filepath.Join(two, "gnoshdir"), 0o755); err != nil {
		t.Fatal(err)
	}
	path := strings.Join([]string{one, "relative/bin", two}, string(os.PathListSeparator))
	got := pathPrograms(path)
	if strings.Join(got, ",") != "gnoshctl,gnoshy,other" {
		t.Errorf("found %q", got)
	}
	if got := pathPrograms(""); len(got) != 0 {
		t.Errorf("an empty PATH gave %q", got)
	}
}

// Completion, of a prompt, a command line or a shell line, never panics,
// and each candidate starts at or before the cursor where a character
// begins, ends inside the text, and begins with what it replaces (in any
// case of the letters).
func FuzzComplete(f *testing.F) {
	for _, seed := range []string{
		"!ls && ca", "! gi", "!echo x >ma", "!ls \\\nma", "!sudo ap", "!(ca", "!./scr", "!cd do",
		"then /re", "then /", "/re", "/cd do", "/loop 5m /re", "look at @ma", "@agent-", "~/co",
		"!git st", "!git status ", "!ünï /", "then /st/x", "!a\nb && c", "!\\\n",
	} {
		f.Add(seed, len(seed))
	}
	ed := editor.New(editor.Options{})
	for _, h := range []string{"!git status", "!git stash list", "a prompt", "! ls -la", "!echo ü"} {
		ed.AddHistory(editor.Draft{Text: h})
	}
	s := &session{cwd: "/proj", home: "/home/u", ed: ed,
		info: app.SessionInfo{
			Commands:    []string{"help", "review", "recap", "status"},
			CommandList: []app.CommandInfo{{Name: "exit", Aliases: []string{"quit"}}},
			Skills:      []app.CommandInfo{{Name: "release-notes"}},
		},
		listDir: func(dir string) []fileEntry {
			switch dir {
			case "/proj", "/":
				return []fileEntry{{name: "docs", dir: true}, {name: "main.go"}, {name: "Makefile"}, {name: "scripts", dir: true}, {name: "ünïcode.txt"}}
			case "/proj/scripts", "/home/u":
				return []fileEntry{{name: "build.sh"}, {name: "code", dir: true}}
			}
			return nil
		},
		programs: func() []string { return []string{"apt", "cat", "echo", "git", "gitk", "grep", "make", "true"} },
	}
	f.Fuzz(func(t *testing.T, text string, cursor int) {
		if !utf8.ValidString(text) || len(text) > 400 {
			return
		}
		cursor = min(max(cursor, 0), len(text))
		// (The editor's cursor is always where a character begins.)
		for cursor > 0 && cursor < len(text) && !utf8.RuneStart(text[cursor]) {
			cursor--
		}
		for _, c := range s.complete(text, cursor) {
			end := max(c.End, cursor)
			if c.Start < 0 || c.Start > cursor || end > len(text) {
				t.Fatalf("%q at %d: candidate %+v is out of place", text, cursor, c)
			}
			if c.Start < len(text) && !utf8.RuneStart(text[c.Start]) {
				t.Fatalf("%q at %d: candidate %+v starts inside a character", text, cursor, c)
			}
			if replaced := text[c.Start:end]; !strings.HasPrefix(strings.ToLower(c.Text), strings.ToLower(replaced)) {
				t.Fatalf("%q at %d: candidate %q does not begin with %q, which it replaces", text, cursor, c.Text, replaced)
			}
		}
	})
}
