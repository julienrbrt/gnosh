package markdown

import (
	"strings"
	"unicode/utf8"

	"github.com/gnosh-ai/gnosh/internal/text"
)

// The marks a piece of inline text can carry. Each stands for one style of
// the Theme, and a piece may carry several: strong text inside a link has
// both.
const (
	sEm     uint16 = 1 << iota // emphasis
	sStrong                    // strong emphasis
	sStrike                    // strikethrough
	sCode                      // a code span
	sLink                      // the text of a link
	sAddr                      // an address printed after a link or image
	sImage                     // the alternative text of an image
	sHTML                      // raw HTML
	sMark                      // the "#" in front of a heading
	sDef                       // the label or title of a link reference definition
)

// The kinds of block a piece of inline text can stand in, which decide the
// style under its marks.
const (
	bText      uint8 = iota // prose
	bHeading                // a heading
	bTableHead              // the header row of a table
	bQuote                  // prose inside a block quote
)

// A style is the look of one piece of inline text: the block it stands in,
// its marks, and the address it links to, if any. Styles compare with ==.
type style struct {
	bits uint16
	base uint8
	link string
}

// A styleKey names a style apart from its link.
type styleKey struct {
	bits uint16
	base uint8
}

// seq returns the escape sequence that turns the style st on. It is empty
// under NoColor and for prose in a theme that leaves prose alone.
func (cfg *config) seq(st style) string {
	key := styleKey{st.bits, st.base}
	if s, ok := cfg.seqs[key]; ok {
		return s
	}
	t := &cfg.theme
	var b strings.Builder
	if st.base == bQuote {
		b.WriteString(t.Quote.Sequence())
	} else {
		b.WriteString(t.Text.Sequence())
	}
	switch st.base {
	case bHeading:
		b.WriteString(t.Heading.Sequence())
	case bTableHead:
		b.WriteString(t.TableHeader.Sequence())
	}
	// From the outside in: what is written later wins where two disagree.
	for _, layer := range []struct {
		bit   uint16
		style text.Style
	}{
		{sMark, t.HeadingMark}, {sDef, t.Definition}, {sLink, t.Link}, {sImage, t.Image},
		{sAddr, t.LinkAddress}, {sHTML, t.HTML}, {sEm, t.Emphasis}, {sStrong, t.Strong},
		{sStrike, t.Strikethrough}, {sCode, t.Code},
	} {
		if st.bits&layer.bit != 0 {
			b.WriteString(layer.style.Sequence())
		}
	}
	s := b.String()
	cfg.seqs[key] = s
	return s
}

// paint returns s, which must be clean, drawn in the style st: wrapped in
// the style's sequence and a reset, and in a hyperlink when the style has
// an address and the terminal shows links.
func (cfg *config) paint(st style, s string) string {
	if s == "" {
		return ""
	}
	if seq := cfg.seq(st); seq != "" {
		s = seq + s + text.Reset
	}
	if st.link != "" && cfg.hyper {
		s = text.Hyperlink(st.link, s)
	}
	return s
}

// clean appends s to dst as text that is safe and has a known width on a
// terminal. A model's text may hold anything, and none of it may reach the
// terminal as a control: a tab or a newline becomes a space, every other
// control character becomes the picture Unicode has for it (ESC shows as
// "␛"), and bytes that are not UTF-8 become U+FFFD. Nothing is dropped, so
// a reader sees that something was there.
func clean(dst []byte, s string) []byte {
	plain := true
	for i := 0; i < len(s); i++ {
		if c := s[i]; c < 0x20 || c >= 0x7f {
			plain = false
			break
		}
	}
	if plain {
		return append(dst, s...)
	}
	for i := 0; i < len(s); {
		c := s[i]
		switch {
		case c == '\t' || c == '\n':
			dst = append(dst, ' ')
			i++
		case c < 0x20:
			dst = utf8.AppendRune(dst, 0x2400+rune(c))
			i++
		case c == 0x7f:
			dst = utf8.AppendRune(dst, 0x2421)
			i++
		case c < utf8.RuneSelf:
			dst = append(dst, c)
			i++
		default:
			r, size := utf8.DecodeRuneInString(s[i:])
			if r == utf8.RuneError && size == 1 || r >= 0x80 && r < 0xa0 {
				r = utf8.RuneError
			}
			dst = utf8.AppendRune(dst, r)
			i += size
		}
	}
	return dst
}

// cleanString is clean for a whole string. It returns s itself when there
// is nothing to change.
func cleanString(s string) string {
	for i := 0; i < len(s); i++ {
		if c := s[i]; c < 0x20 || c >= 0x7f {
			return string(clean(make([]byte, 0, len(s)+8), s))
		}
	}
	return s
}

// spaces returns n spaces.
func spaces(n int) string {
	const blank = "                                                                "
	if n <= 0 {
		return ""
	}
	if n <= len(blank) {
		return blank[:n]
	}
	return strings.Repeat(" ", n)
}

// trimRight returns s without the spaces at its end.
func trimRight(s string) string {
	return strings.TrimRight(s, " ")
}
