package markdown

import "github.com/gnosh-ai/gnosh/internal/text"

// Options are the choices a Renderer is made with. The zero value renders
// plain text, 80 columns wide, in the default theme.
type Options struct {
	// Width is the width of the terminal in cells. No line the renderer
	// returns is wider, whatever the nesting, as long as Width is at least
	// two. A Width below one means 80.
	Width int
	// Profile says how much colour the output can take. Under text.NoColor
	// (the zero value) the lines hold no escape sequences at all.
	Profile text.Profile
	// Theme is the look. The zero Theme means DefaultTheme().
	Theme Theme
	// Metrics says how wide the terminal draws ambiguous characters.
	Metrics text.Metrics
	// Hyperlinks says that the terminal shows OSC 8 hyperlinks. A link is
	// then written as one, and its address is not printed. Without it, and
	// always under text.NoColor, the address is printed after the link's
	// text when the two differ.
	Hyperlinks bool
	// Highlight, when set, colours code. It is called with the language
	// named on the fence (lower case, empty when there is none) and one
	// line of the block, without its newline, and returns that line with
	// SGR sequences added, as a slice of one string. A result of any other
	// length, one whose visible text is not the line, and one holding
	// escape sequences other than SGR are ignored and the line is shown
	// plain. It must be a pure function: a line still being written is
	// passed again on every call of Live. Lines of more than 2048 bytes
	// are not passed to it, and under text.NoColor it is not called.
	Highlight func(lang, code string) []string
	// TabWidth is the distance between tab stops in code blocks. A value
	// below one means 4.
	TabWidth int
	// Reach is how far back, in bytes, later text may change the look of
	// earlier text. Emphasis, a code span, a link or raw HTML whose end has
	// not come within Reach bytes of its start is not that construct, and
	// its opening characters are shown as the text they are. A thematic
	// break, a setext underline, a fence, a table's delimiter row and a
	// link reference definition are recognised only on lines of at most
	// Reach bytes. This is what bounds the lines that stay live under an
	// opening mark that never closes. A value below one means 640, about
	// eight rows of an 80-column terminal.
	Reach int
	// TableLive is the number of rows of the terminal a table may take
	// while it is still live. A table that grows past it becomes final as
	// far as it has got, and goes on with the column widths it had then.
	// A value below one means 24.
	TableLive int
}

// The defaults for Options, and the limits that are not options.
const (
	defaultWidth     = 80
	defaultTab       = 4
	defaultReach     = 640
	defaultTableLive = 24

	// headMax is the most bytes a line may have and still become a setext
	// heading, and the offset by which the first pipe of a table's header
	// row must have come. The first headMax bytes of every paragraph line
	// stay live until the line is known to be neither.
	headMax = 80
	// rowMax is the most bytes a row of a table may have. A longer line
	// ends the table.
	rowMax = 8192
	// codeHold is the most bytes of a code line that are kept back for the
	// highlighter, which wants whole lines. A longer line is shown plain,
	// row by row as it arrives.
	codeHold = 2048
	// codeIndent is how far a code block is set in from the text around it.
	codeIndent = 2
	// ruleMax is the most cells a thematic break is drawn across.
	ruleMax = 48
)

// A config is the Options of a Renderer, checked and made ready for use.
// Nothing in it changes while text is rendered except the width.
type config struct {
	width     int
	profile   text.Profile
	theme     Theme // every style already set to the profile
	m         text.Metrics
	hyper     bool
	highlight func(lang, code string) []string
	tab       int
	reach     int
	tableLive int
	seqs      map[styleKey]string // the escape sequence that turns a style on
	work      *int                // for tests: counts the bytes the renderer looks at

	// Scratch space for the inline scanner, which runs often and briefly:
	// its pieces and its stack are used up before the next scan begins.
	nodes []node
	stack []delim
}

// did adds n to the count of bytes looked at, when a test keeps one. The
// tests use it to check that the cost of rendering grows with the length
// of the text and no faster.
func (cfg *config) did(n int) {
	if cfg.work != nil {
		*cfg.work += n
	}
}

// newConfig checks opts and fills in the defaults.
func newConfig(opts Options) config {
	cfg := config{
		width:     opts.Width,
		profile:   min(opts.Profile, text.TrueColor),
		theme:     opts.Theme,
		m:         opts.Metrics,
		highlight: opts.Highlight,
		tab:       opts.TabWidth,
		reach:     opts.Reach,
		tableLive: opts.TableLive,
		seqs:      make(map[styleKey]string),
	}
	if cfg.width < 1 {
		cfg.width = defaultWidth
	}
	if cfg.theme == (Theme{}) || cfg.theme == DefaultTheme() {
		cfg.theme = DefaultTheme()
		if cfg.profile == text.ANSI16 {
			cfg.theme = defaultTheme(text.Magenta, text.BrightMagenta, text.Cyan)
		}
	}
	if cfg.tab < 1 {
		cfg.tab = defaultTab
	}
	if cfg.reach < 1 {
		cfg.reach = defaultReach
	}
	if cfg.tableLive < 1 {
		cfg.tableLive = defaultTableLive
	}
	cfg.hyper = opts.Hyperlinks && cfg.profile != text.NoColor
	if cfg.profile == text.NoColor {
		cfg.highlight = nil // its colours could not be shown
	}
	cfg.theme = cfg.theme.in(cfg.profile)
	return cfg
}

// in returns the theme with every style set to write for the profile p, and
// with the characters it cannot do without filled in.
func (t Theme) in(p text.Profile) Theme {
	for _, s := range []*text.Style{
		&t.Text, &t.Heading, &t.HeadingMark, &t.Emphasis, &t.Strong, &t.Strikethrough,
		&t.Code, &t.CodeBlock, &t.CodeMark, &t.Link, &t.LinkAddress, &t.Image, &t.HTML,
		&t.Quote, &t.QuoteMark, &t.ListMark, &t.TaskOpen, &t.TaskDone, &t.Rule,
		&t.TableHeader, &t.TableRule, &t.Definition,
	} {
		*s = s.In(p)
	}
	def := DefaultTheme()
	for i, b := range t.Bullets {
		if cleanGlyph(b) == "" {
			t.Bullets[i] = def.Bullets[i]
		}
	}
	if cleanGlyph(t.QuoteBar) == "" {
		t.QuoteBar = def.QuoteBar
	}
	if cleanGlyph(t.RuleChar) == "" {
		t.RuleChar = def.RuleChar
	}
	t.WrapMark = cleanGlyph(t.WrapMark)
	return t
}

// cleanGlyph returns s if it can serve as one of the theme's characters:
// printable text with no spaces around it. Anything else gives "".
func cleanGlyph(s string) string {
	if s == "" || s != string(clean(nil, s)) || s[0] == ' ' || s[len(s)-1] == ' ' {
		return ""
	}
	return s
}

// A Renderer turns Markdown into styled terminal lines as the text arrives.
//
// Write takes the next piece of text and returns the lines that have just
// become final: nothing that arrives later can change them, so they can be
// written to the terminal for good. Live returns the lines after those, as
// they would stand if the text ended here; they may still change. Close ends
// the text and returns everything that was still live.
//
// At every point, the lines returned so far by Write followed by the lines
// of Live are exactly what Render gives for the text written so far.
//
// Every line is whole in itself: it holds no newline, its styles end with
// it, and it is at most the width in cells. A Renderer is not safe for use
// by several goroutines at once.
type Renderer struct {
	cfg config
	st  state
}

// New returns a Renderer for one text.
func New(opts Options) *Renderer {
	return &Renderer{cfg: newConfig(opts)}
}

// Write takes the next piece of the text and returns the lines that became
// final with it, which is often none. The text may be cut anywhere, even
// inside a character.
//
// Write takes time proportional to len(delta) plus the size of what is
// still live, which the limits in Options bound; it never goes back over
// text that is final.
func (r *Renderer) Write(delta string) []string {
	r.st.out = nil
	r.st.feed(&r.cfg, delta, false)
	out := r.st.out
	r.st.out = nil
	return out
}

// Live returns the lines that are not final yet: what Close would return if
// the text ended here. It changes nothing; a caller draws these lines below
// the final ones and draws them again after each Write.
//
// Live takes time proportional to the size of what is still live.
func (r *Renderer) Live() []string {
	st := r.st.clone()
	st.feed(&r.cfg, "", true)
	return st.out
}

// Close says that the text is complete and returns the lines that were
// still live, now final. The Renderer is then ready for a new text, with
// the same options and the width it has now.
func (r *Renderer) Close() []string {
	r.st.out = nil
	r.st.feed(&r.cfg, "", true)
	out := r.st.out
	r.st = state{}
	return out
}

// SetWidth tells the Renderer that the terminal is now cols cells wide.
// Lines that are already final stay as they were returned; everything
// after them is laid out for the new width, from the next call of Write,
// Live or Close on. A width below one means 80.
func (r *Renderer) SetWidth(cols int) {
	if cols < 1 {
		cols = defaultWidth
	}
	r.cfg.width = cols
}

// Render returns a whole text as lines. It is New, one Write and Close.
func Render(src string, opts Options) []string {
	r := New(opts)
	out := r.Write(src)
	return append(out, r.Close()...)
}

// A CodeBlock is one code block of a Markdown text.
type CodeBlock struct {
	// Lang is the language named on the block's fence, as it was written,
	// or "" for a fence that names none and for an indented block.
	Lang string
	// Code is the block's code exactly as it was written, each line ended
	// by a newline.
	Code string
}

// CodeBlocks returns the code blocks of a Markdown text in their order.
//
// The lines a Renderer returns are laid out to be read: code is set in
// from the margin, its tabs are spaces, and a line too long for the
// terminal is cut into rows. To copy a block of code exactly, as the /copy
// command does, take it from here.
func CodeBlocks(src string) []CodeBlock {
	var code [][]byte
	var langs []string
	r := New(Options{})
	r.st.code, r.st.langs = &code, &langs
	r.Write(src)
	r.Close()
	blocks := make([]CodeBlock, len(code))
	for i := range code {
		blocks[i] = CodeBlock{Lang: langs[i], Code: string(code[i])}
	}
	return blocks
}
