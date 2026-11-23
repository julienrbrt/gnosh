package editor

import (
	"strings"
	"testing"
	"time"
)

// vimRig is a box with vim's keys, in INSERT mode.
func vimRig(t *testing.T, o Options) *rig {
	t.Helper()
	o.Vim = true
	g := newRig(t, 40, o)
	if g.e.Vim() != VimInsert {
		t.Fatalf("a box made with Vim starts in %v, want INSERT", g.e.Vim())
	}
	return g
}

// vim checks the mode of the vim keys.
func (g *rig) vim(want VimMode) *rig {
	g.t.Helper()
	if got := g.e.Vim(); got != want {
		g.t.Fatalf("the vim keys are in %v, want %v (text %q)", got, want, g.e.Text())
	}
	return g
}

// The commands of NORMAL and VISUAL mode, each from a box holding a text
// typed in INSERT mode and left with Esc.
func TestVimCommands(t *testing.T) {
	const fbb = `"foo bar baz" <Esc>`
	cases := []struct {
		name, setup, keys, text string
		cursor                  int
		mode                    VimMode
	}{
		// Leaving INSERT mode steps back onto the last character.
		{"esc", `"foo"`, `<Esc>`, "foo", 2, VimNormal},
		{"ctrl+[ is esc", `"foo"`, `<Ctrl+[>`, "foo", 2, VimNormal},

		// Motions.
		{"0", fbb, `"0"`, "foo bar baz", 0, VimNormal},
		{"w", fbb, `"0w"`, "foo bar baz", 4, VimNormal},
		{"count w", fbb, `"02w"`, "foo bar baz", 8, VimNormal},
		{"w stops at punctuation", `"a.b c" <Esc>`, `"0w"`, "a.b c", 1, VimNormal},
		{"W does not", `"a.b c" <Esc>`, `"0W"`, "a.b c", 4, VimNormal},
		{"e", fbb, `"0e"`, "foo bar baz", 2, VimNormal},
		{"e twice", fbb, `"0ee"`, "foo bar baz", 6, VimNormal},
		{"b", fbb, `"b"`, "foo bar baz", 8, VimNormal},
		{"count b", fbb, `"2b"`, "foo bar baz", 4, VimNormal},
		{"$", fbb, `"0$"`, "foo bar baz", 10, VimNormal},
		{"^", `"  foo" <Esc>`, `"0^"`, "  foo", 2, VimNormal},
		{"h", fbb, `"h"`, "foo bar baz", 9, VimNormal},
		{"h stops at the line's start", fbb, `"0h"`, "foo bar baz", 0, VimNormal},
		{"l stops on the last character", fbb, `"l"`, "foo bar baz", 10, VimNormal},
		{"space is l", fbb, `"0 "`, "foo bar baz", 1, VimNormal},
		{"backspace is h", fbb, `<Backspace>`, "foo bar baz", 9, VimNormal},
		{"f", fbb, `"0fb"`, "foo bar baz", 4, VimNormal},
		{"f again with ;", fbb, `"0fb;"`, "foo bar baz", 8, VimNormal},
		{", goes back", fbb, `"0fb;,"`, "foo bar baz", 4, VimNormal},
		{"count f", fbb, `"02fb"`, "foo bar baz", 8, VimNormal},
		{"t", fbb, `"0tb"`, "foo bar baz", 3, VimNormal},
		{"t again with ; passes the one it stopped before", fbb, `"0tb;"`, "foo bar baz", 7, VimNormal},
		{"F", fbb, `"Fb"`, "foo bar baz", 8, VimNormal},
		{"T", fbb, `"Tb"`, "foo bar baz", 9, VimNormal},
		{"f that finds nothing", fbb, `"0fq"`, "foo bar baz", 0, VimNormal},
		{"%", `"f(a, b) x" <Esc>`, `"0%"`, "f(a, b) x", 6, VimNormal},
		{"% back", `"f(a, b) x" <Esc>`, `"0%%"`, "f(a, b) x", 1, VimNormal},
		{"gg and G", `"one" <Ctrl+J> "two" <Ctrl+J> "three" <Esc>`, `"gg"`, "one\ntwo\nthree", 0, VimNormal},
		{"G", `"one" <Ctrl+J> "two" <Ctrl+J> "three" <Esc>`, `"ggG"`, "one\ntwo\nthree", 8, VimNormal},
		{"count G", `"one" <Ctrl+J> "two" <Ctrl+J> "three" <Esc>`, `"2G"`, "one\ntwo\nthree", 4, VimNormal},
		{"j and k", `"one" <Ctrl+J> "two" <Ctrl+J> "three" <Esc>`, `"ggjjk"`, "one\ntwo\nthree", 4, VimNormal},
		{"w crosses lines", `"one" <Ctrl+J> "two" <Esc>`, `"ggw"`, "one\ntwo", 4, VimNormal},
		{"w stops at an empty line", `"one" <Ctrl+J> <Ctrl+J> "two" <Esc>`, `"ggw"`, "one\n\ntwo", 4, VimNormal},

		// Operators.
		{"dw", fbb, `"0dw"`, "bar baz", 0, VimNormal},
		{"d2w", fbb, `"0d2w"`, "baz", 0, VimNormal},
		{"2dw", fbb, `"02dw"`, "baz", 0, VimNormal},
		{"dw on the last word takes it to the line's end", `"foo bar" <Ctrl+J> "x" <Esc>`, `"ggwdw"`, "foo \nx", 3, VimNormal},
		{"de", fbb, `"0de"`, " bar baz", 0, VimNormal},
		{"db", fbb, `"db"`, "foo bar z", 8, VimNormal},
		{"d$", fbb, `"0wd$"`, "foo ", 3, VimNormal},
		{"D", fbb, `"0wD"`, "foo ", 3, VimNormal},
		{"d0", fbb, `"0wwd0"`, "baz", 0, VimNormal},
		{"dfb", fbb, `"0dfb"`, "ar baz", 0, VimNormal},
		{"dtb", fbb, `"0dtb"`, "bar baz", 0, VimNormal},
		{"d%", `"f(a, b) x" <Esc>`, `"0f(d%"`, "f x", 1, VimNormal},
		{"dd", `"one" <Ctrl+J> "two" <Ctrl+J> "three" <Esc>`, `"ggjdd"`, "one\nthree", 4, VimNormal},
		{"dd on the last line", `"one" <Ctrl+J> "two" <Esc>`, `"dd"`, "one", 0, VimNormal},
		{"2dd", `"one" <Ctrl+J> "two" <Ctrl+J> "three" <Esc>`, `"gg2dd"`, "three", 0, VimNormal},
		{"dj", `"one" <Ctrl+J> "two" <Ctrl+J> "three" <Esc>`, `"ggdj"`, "three", 0, VimNormal},
		{"dk", `"one" <Ctrl+J> "two" <Ctrl+J> "three" <Esc>`, `"dk"`, "one", 0, VimNormal},
		{"dG", `"one" <Ctrl+J> "two" <Ctrl+J> "three" <Esc>`, `"ggjdG"`, "one", 0, VimNormal},
		{"dgg", `"one" <Ctrl+J> "two" <Ctrl+J> "three" <Esc>`, `"ggjdgg"`, "three", 0, VimNormal},
		{"x", fbb, `"0x"`, "oo bar baz", 0, VimNormal},
		{"count x", fbb, `"03x"`, " bar baz", 0, VimNormal},
		{"x on the last character", fbb, `"x"`, "foo bar ba", 9, VimNormal},
		{"X", fbb, `"X"`, "foo bar bz", 9, VimNormal},
		{"r", fbb, `"0rX"`, "Xoo bar baz", 0, VimNormal},
		{"count r", fbb, `"03rX"`, "XXX bar baz", 2, VimNormal},
		{"~", fbb, `"0~~"`, "FOo bar baz", 2, VimNormal},
		{"J", `"a" <Ctrl+J> "  b" <Esc>`, `"ggJ"`, "a b", 1, VimNormal},
		{"J before a parenthesis", `"f(a" <Ctrl+J> ")" <Esc>`, `"ggJ"`, "f(a)", 3, VimNormal},
		{">>", fbb, `">>"`, "  foo bar baz", 2, VimNormal},
		{"<<", `"    foo" <Esc>`, `"<<"`, "  foo", 2, VimNormal},

		// Text objects.
		{"diw", fbb, `"0wdiw"`, "foo  baz", 4, VimNormal},
		{"daw", fbb, `"0wdaw"`, "foo baz", 4, VimNormal},
		{"daw on the last word", fbb, `"daw"`, "foo bar", 6, VimNormal},
		{"diW", `"a.b c" <Esc>`, `"0diW"`, " c", 0, VimNormal},
		{"di\"", `"say \"hi there\" now" <Esc>`, `"0fhdi\""`, "say \"\" now", 5, VimNormal},
		{"da\"", `"say \"hi there\" now" <Esc>`, `"0fhda\""`, "say  now", 4, VimNormal},
		{"di(", `"f(a, b) x" <Esc>`, `"0fadi("`, "f() x", 2, VimNormal},
		{"da(", `"f(a, b) x" <Esc>`, `"0fada("`, "f x", 1, VimNormal},
		{"di( on the parenthesis", `"f(a, b) x" <Esc>`, `"0f)di("`, "f() x", 2, VimNormal},
		{"dib", `"f(a, (b)) x" <Esc>`, `"0fbdib"`, "f(a, ()) x", 6, VimNormal},
		{"di{ over lines", `"x {" <Ctrl+J> "a" <Ctrl+J> "}" <Esc>`, `"kdi{"`, "x {}", 3, VimNormal},
		{"yiw then P", fbb, `"0wyiwP"`, "foo barbar baz", 6, VimNormal},

		// Changes, into INSERT mode.
		{"cw", fbb, `"0cwqux" <Esc>`, "qux bar baz", 2, VimNormal},
		{"cw on one letter", `"a bc" <Esc>`, `"0cwX" <Esc>`, "X bc", 0, VimNormal},
		{"c2w", fbb, `"0c2wqux" <Esc>`, "qux baz", 2, VimNormal},
		{"ciw", fbb, `"0wciwX" <Esc>`, "foo X baz", 4, VimNormal},
		{"cc", `"one" <Ctrl+J> "  two" <Esc>`, `"ccX" <Esc>`, "one\n  X", 6, VimNormal},
		{"C", fbb, `"0wCX" <Esc>`, "foo X", 4, VimNormal},
		{"s", fbb, `"0sX" <Esc>`, "Xoo bar baz", 0, VimNormal},
		{"S", fbb, `"SX" <Esc>`, "X", 0, VimNormal},
		{"s on an empty line", `<Ctrl+J> "b" <Esc>`, `"ggs"`, "\nb", 0, VimInsert},
		{"i", fbb, `"0wiX" <Esc>`, "foo Xbar baz", 4, VimNormal},
		{"a", fbb, `"0aX" <Esc>`, "fXoo bar baz", 1, VimNormal},
		{"I", `"  foo" <Esc>`, `"IX" <Esc>`, "  Xfoo", 2, VimNormal},
		{"A", fbb, `"0AX" <Esc>`, "foo bar bazX", 11, VimNormal},
		{"o", fbb, `"oline" <Esc>`, "foo bar baz\nline", 15, VimNormal},
		{"O", fbb, `"Oline" <Esc>`, "line\nfoo bar baz", 3, VimNormal},

		// The register.
		{"yy p", fbb, `"yyp"`, "foo bar baz\nfoo bar baz", 12, VimNormal},
		{"yy P", `"one" <Ctrl+J> "two" <Esc>`, `"yyP"`, "one\ntwo\ntwo", 4, VimNormal},
		{"Y p twice", `"ab" <Esc>`, `"Y2p"`, "ab\nab\nab", 3, VimNormal},
		{"dd p moves a line", `"one" <Ctrl+J> "two" <Esc>`, `"ggddp"`, "two\none", 4, VimNormal},
		{"x p swaps two letters", `"ab" <Esc>`, `"0xp"`, "ba", 1, VimNormal},
		{"yw P", fbb, `"0ywP"`, "foo foo bar baz", 3, VimNormal},
		{"count p", `"ab" <Esc>`, `"0yl3p"`, "aaaab", 3, VimNormal},

		// Undo, and . again.
		{"u takes back a change", fbb, `"0dwu"`, "foo bar baz", 0, VimNormal},
		{"u takes back an INSERT session whole", `"foo bar baz"`, `<Esc> "u"`, "", 0, VimNormal},
		{"u takes back a cw whole", fbb, `"0cwqux" <Esc> "u"`, "foo bar baz", 0, VimNormal},
		{". makes the last change again", fbb, `"0dw."`, "baz", 0, VimNormal},
		{". with a count", `"a b c d" <Esc>`, `"0dw2."`, "d", 0, VimNormal},
		{". after an INSERT session", fbb, `"0ciwX" <Esc> "w."`, "X X baz", 2, VimNormal},
		{". after x", fbb, `"0x.."`, " bar baz", 0, VimNormal},
		{". after A", `"a" <Esc>`, `"A!" <Esc> "."`, "a!!", 2, VimNormal},

		// VISUAL mode.
		{"v d", fbb, `"0vwd"`, "ar baz", 0, VimNormal},
		{"v e y P", fbb, `"0veyP"`, "foofoo bar baz", 2, VimNormal},
		{"v c", fbb, `"0vecX" <Esc>`, "X bar baz", 0, VimNormal},
		{"v U", fbb, `"0veU"`, "FOO bar baz", 0, VimNormal},
		{"v ~", fbb, `"0ve~"`, "FOO bar baz", 0, VimNormal},
		{"v r", fbb, `"0verx"`, "xxx bar baz", 0, VimNormal},
		{"v o swaps the ends", fbb, `"0veo"`, "foo bar baz", 0, VimVisual},
		{"v o, then the other end moves", fbb, `"0veold"`, "f bar baz", 1, VimNormal},
		{"v iw", fbb, `"0wviwd"`, "foo  baz", 4, VimNormal},
		{"V d", `"one" <Ctrl+J> "two" <Ctrl+J> "three" <Esc>`, `"ggVjd"`, "three", 0, VimNormal},
		{"V >", `"one" <Ctrl+J> "two" <Esc>`, `"ggVj>"`, "  one\n  two", 2, VimNormal},
		{"V J", `"a" <Ctrl+J> "b" <Ctrl+J> "c" <Esc>`, `"ggVjjJ"`, "a b c", 3, VimNormal},
		{"v p replaces", fbb, `"0yiwwviwp"`, "foo foo baz", 4, VimNormal},
		{"v then V then v", fbb, `"vVv"`, "foo bar baz", 10, VimVisual},
		{"v v leaves", fbb, `"vv"`, "foo bar baz", 10, VimNormal},
		{"esc leaves VISUAL", fbb, `"v" <Esc>`, "foo bar baz", 10, VimNormal},

		// What is no command does nothing, and Esc drops a command half
		// typed.
		{"a key that is no command", fbb, `"q"`, "foo bar baz", 10, VimNormal},
		{"esc drops a half command", fbb, `"0d" <Esc> "w"`, "foo bar baz", 4, VimNormal},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			g := vimRig(t, Options{})
			g.do(c.setup).do(c.keys)
			if got := g.e.Text(); got != c.text {
				t.Fatalf("%s then %s: text is %q, want %q", c.setup, c.keys, got, c.text)
			}
			if got := g.e.Cursor(); got != c.cursor {
				t.Fatalf("%s then %s: cursor is at %d of %q, want %d", c.setup, c.keys, got, c.text, c.cursor)
			}
			g.vim(c.mode)
		})
	}
}

// Esc in NORMAL mode, Enter and Ctrl+C keep their meanings, and the box
// starts in INSERT mode again once it is sent or cleared.
func TestVimBoxKeys(t *testing.T) {
	g := vimRig(t, Options{})
	g.do(`"draft" <Esc>`).vim(VimNormal).action(ActionNone)
	g.do(`<Esc>`)
	if g.last.Hint != HintEscClear {
		t.Fatalf("Esc in NORMAL mode: %+v, want the hint that a second clears", g.last)
	}
	g.do(`<Esc>`).text("").vim(VimInsert)
	if !g.last.Cleared {
		t.Fatalf("Esc twice in NORMAL mode did not clear: %+v", g.last)
	}

	g = vimRig(t, Options{})
	g.do(`"send me" <Esc> "0"`).do(`<Enter>`).action(ActionSubmit).text("").vim(VimInsert)
	g.do(`"x"`).text("x")

	// While a turn runs, Esc in NORMAL mode stops it.
	g = vimRig(t, Options{})
	g.e.SetBusy(true)
	g.do(`"x" <Esc>`).vim(VimNormal).action(ActionNone)
	g.do(`<Esc>`).action(ActionInterrupt)

	// "?" on an empty box shows the keys in INSERT mode, and is no command
	// in NORMAL mode.
	g = vimRig(t, Options{})
	g.do(`"?"`).action(ActionHelp)
	g.do(`<Esc> "?"`).action(ActionNone).text("")

	// History: k on the first line and j on the last walk it.
	g = vimRig(t, Options{})
	g.e.SetHistory([]Draft{{Text: "older"}, {Text: "newer"}})
	g.do(`<Esc> "k"`).text("newer").do(`"k"`).text("older").do(`"j"`).text("newer")

	// / opens the history search.
	g = vimRig(t, Options{})
	g.e.SetHistory([]Draft{{Text: "find me"}})
	g.do(`<Esc> "/"`)
	if !g.e.Searching() {
		t.Fatal("/ in NORMAL mode did not open the history search")
	}

	// Switched off, the box has its own keys again.
	g = vimRig(t, Options{})
	g.do(`"ab" <Esc>`).vim(VimNormal)
	g.e.SetVim(false)
	g.vim(VimOff).do(`"x"`).text("axb")
}

// A pair typed in INSERT mode within a second stands for Esc, and leaves
// neither character; slower, or with another key between, both stay.
func TestVimRemaps(t *testing.T) {
	g := vimRig(t, Options{VimRemaps: []string{"jj", "toolong", "j"}})
	g.do(`"abjj"`).text("ab").vim(VimNormal).cursor(1)
	g.do(`"x"`).text("a")

	g = vimRig(t, Options{VimRemaps: []string{"jj"}})
	g.do(`"abj"`).wait(1100 * time.Millisecond).do(`"j"`).text("abjj").vim(VimInsert)

	g = vimRig(t, Options{VimRemaps: []string{"jk"}})
	g.do(`"aj" <Left> "k"`).text("akj").vim(VimInsert)

	// A pair from a project would be ignored by the application; the
	// editor takes what it is given, and only pairs of two characters.
	g = vimRig(t, Options{VimRemaps: []string{"j", " j", "jjj"}})
	g.do(`"ajj"`).text("ajj").vim(VimInsert)

	// "." after a session left with the pair makes the same change.
	g = vimRig(t, Options{VimRemaps: []string{"jj"}})
	g.do(`"a" <Esc> "Ab" "jj" "."`).text("abb")
}

// A VISUAL selection is drawn in its own look, and the box stays sound
// at every width.
func TestVimVisualDraws(t *testing.T) {
	th := DefaultTheme(0)
	th.Selection = th.Selection.Underline()
	g := newRig(t, 40, Options{Vim: true, Theme: th})
	g.do(`"foo bar baz" <Esc> "0vw"`).vim(VimVisual)
	v := g.e.Render(40)
	if len(v.Lines) == 0 || !strings.Contains(v.Lines[0], "foo b") {
		t.Fatalf("the box while selecting: %q", v.Lines)
	}
	for _, w := range []int{5, 12, 40} {
		checkView(t, g.e, w, "a VISUAL selection")
	}
}

// A put with a count adds at most about vimPutMax of text, and at least
// the register once: 9999p of a long line built gigabytes (the TUI review
// of 2026-11-19).
func TestVimPutCountIsBounded(t *testing.T) {
	g := vimRig(t, Options{})
	long := strings.Repeat("x", 1<<20)
	g.e.Insert(long)
	g.do(`<Esc> "yy9999p"`)
	if n := len(g.e.Text()); n > vimPutMax+2*len(long)+2 {
		t.Fatalf("9999p of a 1 MiB line made %d bytes", n)
	}
	if lines := strings.Count(g.e.Text(), "\n"); lines < 1 {
		t.Fatalf("the line was not put at all: %d line breaks", lines)
	}
	// A short line is put as many times as asked.
	g = vimRig(t, Options{})
	g.do(`"ab" <Esc> "yy9999p"`)
	if lines := strings.Count(g.e.Text(), "ab"); lines != 10000 {
		t.Errorf("a short line stands %d times, want 10000", lines)
	}
}
