package markdown

import (
	"strings"
	"testing"

	"github.com/gnosh-ai/gnosh/internal/text"
)

// A quote's prose is drawn in the theme's Quote style, not in Text: in
// gnosh the answer's prose is green, and what it quotes is someone else's
// words (Jae, 21 November 2026: "the stuff to the right of the purple bar
// shouldn't be green"). Emphasis and code inside it are drawn as anywhere,
// and prose after the quote is Text again.
func TestQuoteProseHasItsOwnStyle(t *testing.T) {
	var plain text.Style
	th := DefaultTheme()
	th.Text = plain.Fg(text.Green)
	th.Quote = plain
	th.QuoteMark = plain.Fg(text.Magenta)
	lines := Render("Before.\n\n> *None of the three.* 'jae' is `site-own`.\n>\n> Held by the site.\n\nAfter.\n", Options{Width: 60, Profile: text.ANSI16, Theme: th})
	green := th.Text.In(text.ANSI16).Sequence()
	bar := th.QuoteMark.In(text.ANSI16).Sequence()
	italic := th.Emphasis.In(text.ANSI16).Sequence()
	code := th.Code.In(text.ANSI16).Sequence()
	var quoted int
	for _, l := range lines {
		switch {
		case strings.Contains(l, "Before.") || strings.Contains(l, "After."):
			if !strings.Contains(l, green) {
				t.Errorf("prose outside the quote is not in Text's colour: %q", l)
			}
		case strings.Contains(l, "None of the three") || strings.Contains(l, "Held by the site"):
			quoted++
			if strings.Contains(l, green) {
				t.Errorf("a quoted line is in Text's colour: %q", l)
			}
			if !strings.Contains(l, bar) {
				t.Errorf("a quoted line has no bar in QuoteMark's colour: %q", l)
			}
		}
	}
	if quoted != 2 {
		t.Fatalf("found %d quoted lines, want 2:\n%q", quoted, lines)
	}
	for _, l := range lines {
		if strings.Contains(l, "None of the three") && (!strings.Contains(l, italic) || !strings.Contains(l, code)) {
			t.Errorf("emphasis or code inside the quote lost its style: %q", l)
		}
	}
	// Under the default theme, where Text and Quote are both the
	// terminal's own colour, a quote is drawn as before.
	def := Render("> words\n", Options{Width: 60, Profile: text.ANSI16, Theme: DefaultTheme()})
	if len(def) != 1 || !strings.Contains(def[0], "words") {
		t.Errorf("a quote under the default theme: %q", def)
	}
}
