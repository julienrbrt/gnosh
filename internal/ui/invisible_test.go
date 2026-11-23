package ui

import (
	"strings"
	"testing"

	"github.com/gnosh-ai/gnosh/internal/text"
	"github.com/gnosh-ai/gnosh/internal/ui/editor"
)

// Enter takes the characters a terminal draws as nothing out of a prompt
// before it goes, and says so. When they could hide text, or when what is
// left would run as a command, nothing goes: the prompt is back in the
// box to be read, and Enter sends it as it then stands.
func TestInvisibleCharactersTakenOut(t *testing.T) {
	g := newRig(t, 100, 24, text.NoColor)
	g.s.input(pasteOf("rm\u200B the build"))
	g.frame().keys(`<Enter>`)
	if got := g.c.sent(); len(got) != 1 || got[0] != "rm the build" {
		t.Fatalf("sent %q, want the prompt without the zero-width space", got)
	}
	g.committedHas("Took out 1 invisible character before sending the prompt.")

	// Tag characters spell text nobody saw: the prompt waits in the box.
	g.s.input(pasteOf("hi\U000E0069\U000E0067\U000E006E"))
	g.frame().keys(`<Enter>`)
	if got := g.c.sent(); len(got) != 1 {
		t.Fatalf("a prompt with hidden text was sent: %q", got)
	}
	if g.box() != "hi" {
		t.Fatalf("the box holds %+q, want the prompt without its tags", g.box())
	}
	g.committedHas("Took out 3 invisible characters that could hide text")
	g.keys(`<Enter>`)
	if got := g.c.sent(); len(got) != 2 || got[1] != "hi" {
		t.Fatalf("Enter again sent %q", got)
	}

	// Nothing but invisible characters sends nothing.
	g.s.input(pasteOf("\u200B\u200B"))
	g.frame().keys(`<Enter>`)
	if got := g.c.sent(); len(got) != 2 {
		t.Fatalf("an empty prompt was sent: %q", got)
	}

	// A folded paste is cleaned too, and stays folded.
	paste := strings.Repeat("x", 450) + "\u200B" + strings.Repeat("y", 450)
	g.keys(`"look: "`)
	g.s.input(pasteOf(paste))
	g.frame().keys(`<Enter>`)
	want := "look: " + strings.Repeat("x", 450) + strings.Repeat("y", 450)
	if got := g.c.sent(); len(got) != 3 || got[2] != want {
		t.Fatalf("the folded paste was sent as %d bytes", len(got[len(got)-1]))
	}
	spans := pastesOf(g.c.prompts[2].Meta)
	if len(spans) != 1 || spans[0] != (editor.Span{Start: 6, End: 906}) {
		t.Errorf("the paste is marked at %v, want 6 to 906", spans)
	}

	// A shell line is never run cleaned: what was taken out changes what
	// it runs.
	g.s.input(pasteOf("!rm report\u200B.txt"))
	g.frame().keys(`<Enter>`)
	if got := g.c.sent(); len(got) != 3 || g.box() != "!rm report.txt" {
		t.Fatalf("sent %q, box %+q: want the shell line back in the box", got, g.box())
	}
	g.committedHas("from the shell line, which changes what it runs; nothing was run")
	g.keys(`<Ctrl+U> <Backspace>`)

	// Nor is the word that leaves the session.
	g.s.input(pasteOf("exit\u200B"))
	g.frame().keys(`<Enter>`)
	if g.box() != "exit" {
		t.Fatalf("box %+q: want exit back in the box", g.box())
	}
	g.keys(`<Ctrl+U>`)

	// Taken out, a space that hid a command makes one: it waits too.
	g.s.input(pasteOf("\u200B/exit"))
	g.frame().keys(`<Enter>`)
	if got := g.c.sent(); len(got) != 3 || g.box() != "/exit" {
		t.Fatalf("sent %q, box %+q: want nothing sent and /exit in the box", got, g.box())
	}
	g.committedHas("what is left reads as a command")
}

// cleanDraft cleans what was typed and each paste, and keeps the paste
// where it stands once the text before it is shorter.
func TestCleanDraft(t *testing.T) {
	d := editor.Draft{Text: "a\u200Bb" + "c\u200Bd" + "e", Pastes: []editor.Span{{Start: 5, End: 10}}}
	got, found := cleanDraft(d)
	if got.Text != "abcde" || found.Removed != 2 || len(got.Pastes) != 1 || got.Pastes[0] != (editor.Span{Start: 2, End: 4}) {
		t.Errorf("cleanDraft = %+q %v, %+v", got.Text, got.Pastes, found)
	}
	if same, found := cleanDraft(editor.Draft{Text: "plain"}); same.Text != "plain" || found.Removed != 0 {
		t.Errorf("a plain draft changed: %+q", same.Text)
	}
}

// A joiner beside a paste is judged with its neighbour inside the paste:
// Persian's non-joiner before a pasted word stays.
func TestCleanDraftAcrossAPaste(t *testing.T) {
	paste := strings.Repeat("\u062E", 900)
	d := editor.Draft{Text: "\u0645\u200C" + paste, Pastes: []editor.Span{{Start: 5, End: 5 + len(paste)}}}
	got, found := cleanDraft(d)
	if found.Removed != 0 || got.Text != d.Text {
		t.Errorf("the non-joiner before the paste was taken out (%d removed)", found.Removed)
	}
	d.Text = "x\u200B" + paste + "\u200By"
	d.Pastes = []editor.Span{{Start: 4, End: 4 + len(paste)}}
	got, found = cleanDraft(d)
	if found.Removed != 2 || got.Text != "x"+paste+"y" || len(got.Pastes) != 1 || got.Pastes[0] != (editor.Span{Start: 1, End: 1 + len(paste)}) {
		t.Errorf("cleanDraft = %d removed, pastes %v", found.Removed, got.Pastes)
	}
}
