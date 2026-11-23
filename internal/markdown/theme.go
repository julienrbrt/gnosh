package markdown

import "github.com/gnosh-ai/gnosh/internal/text"

// A Theme is the look of rendered Markdown: one text.Style for each kind of
// element, and the few characters the renderer draws by itself. The zero
// Theme is not useful; start from DefaultTheme and change what should differ.
// A Theme compares with ==.
//
// Styles add up where elements nest: strong text inside a link is drawn in
// the link's style and then the strong style on top of it, so an inner
// colour replaces an outer one and attributes such as bold and underline
// accumulate. The styles need no colour profile of their own: the renderer
// sets each to the profile in its Options.
type Theme struct {
	// Text is ordinary prose. It is the base of everything else outside
	// code blocks, so a dim Text gives a dim document.
	Text text.Style
	// Heading is the text of a heading, at every level.
	Heading text.Style
	// HeadingMark is the row of "#" kept in front of a heading, which is
	// what tells the levels apart.
	HeadingMark text.Style
	// Emphasis, Strong and Strikethrough are *this*, **this** and ~~this~~.
	Emphasis      text.Style
	Strong        text.Style
	Strikethrough text.Style
	// Code is a code span inside a line of prose.
	Code text.Style
	// CodeBlock is a line of a fenced or indented code block.
	CodeBlock text.Style
	// CodeMark is the mark in the margin of a row that continues a code
	// line too long for one row.
	CodeMark text.Style
	// Link is the text of a link, and LinkAddress the address printed after
	// it when the terminal cannot carry the address in a hyperlink.
	Link        text.Style
	LinkAddress text.Style
	// Image is the alternative text of an image.
	Image text.Style
	// HTML is raw HTML, which is shown as the text it is.
	HTML text.Style
	// Quote is the prose of a block quote, in place of Text: what an
	// answer quotes is someone else's words, and does not take the
	// answer's own colour (Jae, 21 November 2026: "the stuff to the right
	// of the purple bar shouldn't be green"). Emphasis, links and code
	// inside it are drawn over it as over Text.
	Quote text.Style
	// QuoteMark is the bar down the left of a block quote.
	QuoteMark text.Style
	// ListMark is a bullet or an item's number.
	ListMark text.Style
	// TaskOpen and TaskDone are the boxes of task list items.
	TaskOpen text.Style
	TaskDone text.Style
	// Rule is a thematic break.
	Rule text.Style
	// TableHeader is the text of a table's header row, and TableRule the
	// lines the table draws under it and between rows.
	TableHeader text.Style
	TableRule   text.Style
	// Definition is the label and title of a link reference definition.
	Definition text.Style

	// Bullets are the bullets of a list at nesting depth one, two and
	// three; deeper lists start again from the first.
	Bullets [3]string
	// QuoteBar is drawn in front of every line of a block quote.
	QuoteBar string
	// RuleChar is repeated to draw thematic breaks and table rules.
	RuleChar string
	// WrapMark stands in the margin of a row that continues a code line.
	WrapMark string
}

// The colours of the default theme. They are picked to read on dark and on
// light backgrounds alike, which rules out anything very light or very dark:
// each has a contrast of at least 3.5 against white and 5 against black.
// The purple and the pink are gnosh's own; they are placeholders until Jae
// picks the final pair (spec/gnosh/harness-open-questions.md, OQ-26), and a
// caller can replace them through the Theme.
var (
	themePurple = text.RGB(0x9D, 0x5C, 0xFF) // structure: marks, links
	themePink   = text.RGB(0xF0, 0x25, 0x7F) // the one accent: headings
	themeLilac  = text.RGB(0x8B, 0x7F, 0xB8) // calm: code, addresses, rules
)

// DefaultTheme returns gnosh's own look.
//
// Colour is used sparingly, and nothing is dimmed, blurred or filled in.
// Prose is the terminal's own colour. A heading is bold in the one accent,
// hot pink, behind its "#" marks. Structure the renderer draws itself
// (bullets, numbers, quote bars, heading marks) is purple, and so are links,
// which are underlined. Code, inline and in blocks, is a calm lilac with no
// background, so that it copies cleanly; a code block is set apart by its
// indentation alone. Strong is bold, emphasis italic, and neither has a
// colour, so they work inside anything.
//
// Under text.NoColor none of this is written and the layout carries the
// meaning: headings keep their "#", lists their bullets and numbers, quotes
// their bar, code blocks their indentation, code spans their backticks.
//
// In a terminal of sixteen colours a Renderer that was given this theme
// uses the terminal's own colours in its place: magenta for the purple,
// bright magenta for the pink and cyan for the lilac. The nearest of the
// sixteen to a soft colour is a grey, which some palettes make
// unreadable, and what a palette calls magenta its owner can read.
func DefaultTheme() Theme {
	return defaultTheme(themePurple, themePink, themeLilac)
}

// defaultTheme is the default theme in the three colours given.
func defaultTheme(purple, pink, lilac text.Color) Theme {
	var plain text.Style
	return Theme{
		Text:          plain,
		Heading:       plain.Fg(pink).Bold(),
		HeadingMark:   plain.Fg(purple),
		Emphasis:      plain.Italic(),
		Strong:        plain.Bold(),
		Strikethrough: plain.Strikethrough(),
		Code:          plain.Fg(lilac),
		CodeBlock:     plain.Fg(lilac),
		CodeMark:      plain.Fg(purple),
		Link:          plain.Fg(purple).Underline(),
		LinkAddress:   plain.Fg(lilac),
		Image:         plain.Italic(),
		HTML:          plain.Fg(lilac),
		Quote:         plain,
		QuoteMark:     plain.Fg(purple),
		ListMark:      plain.Fg(purple),
		TaskOpen:      plain.Fg(purple),
		TaskDone:      plain.Fg(pink).Bold(),
		Rule:          plain.Fg(lilac),
		TableHeader:   plain.Bold(),
		TableRule:     plain.Fg(lilac),
		Definition:    plain.Fg(lilac),

		Bullets:  [3]string{"•", "◦", "▪"},
		QuoteBar: "│",
		RuleChar: "─",
		WrapMark: "↪",
	}
}
