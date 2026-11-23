package text

import (
	"strings"
	"unicode/utf8"
)

// Safe returns s as it may be written to a terminal when it comes from what
// gnosh does not trust, a model's reply or the input a model wrote for a
// tool: its styles (SGR) stay, and every other escape sequence and every
// control character but the tab and the line break is taken out, the C1
// controls (U+0080 to U+009F) among them, which some terminals read as the
// escape sequences they stand for. Such text can then change the colours
// of what it writes and nothing else: not the cursor, the screen, the
// window's title, the clipboard (OSC 52) or the terminal's modes. Of the
// styles, "conceal" is taken out, which hides words that are still copied
// with the rest; and text that opened a style ends with one that closes
// it, so that nothing after it, gnosh's own lines or the person's shell,
// takes the style on (the terminal review of 2026-11-19).
func Safe(s string) string {
	if safeAlready(s) {
		return s
	}
	out, open := safe(s)
	if open {
		out += sgrReset
	}
	return out
}

// sgrReset is the style that closes every other.
const sgrReset = "\x1b[0m"

// safe is Safe without the closing style; open says a style it kept is
// still in force at the end.
func safe(s string) (string, bool) {
	var b strings.Builder
	b.Grow(len(s))
	open := false
	for s != "" {
		var tok Token
		tok, s = NextToken(s)
		switch {
		case tok.Kind == Plain:
			for _, r := range tok.Text {
				if r == '\n' || r == '\t' || r >= 0x20 && r != 0x7f && (r < 0x80 || r > 0x9f) {
					b.WriteRune(r)
				}
			}
		case tok.IsSGR():
			if seq := withoutConceal(tok.Text); seq != "" {
				b.WriteString(seq)
				open = !closesAll(seq)
			}
		}
	}
	return b.String(), open
}

// closesAll reports whether an SGR sequence is the one that closes every
// style: no parameter, or zeros alone.
func closesAll(seq string) bool {
	return strings.Trim(seq[2:len(seq)-1], "0;") == ""
}

// withoutConceal is an SGR sequence with "conceal" (8) taken out of its
// parameters, or "" when nothing is left of it. The arguments of a colour
// given by number (38, 48 or 58, then 5;n or 2;r;g;b) are kept as they are,
// though one of them may be 8.
func withoutConceal(seq string) string {
	if !strings.HasPrefix(seq, "\x1b[") || !strings.HasSuffix(seq, "m") {
		return seq
	}
	ps := strings.Split(seq[2:len(seq)-1], ";")
	out := make([]string, 0, len(ps))
	for i := 0; i < len(ps); i++ {
		switch p := ps[i]; {
		case p == "38" || p == "48" || p == "58":
			n := 0
			if i+1 < len(ps) {
				switch ps[i+1] {
				case "5":
					n = 2
				case "2":
					n = 4
				}
			}
			end := min(i+1+n, len(ps))
			out = append(out, ps[i:end]...)
			i = end - 1
		case strings.TrimLeft(p, "0") == "8":
		default:
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		return ""
	}
	return "\x1b[" + strings.Join(out, ";") + "m"
}

// safeAlready reports whether s holds nothing Safe takes out, so that most
// text is passed back as it is.
func safeAlready(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '\n' || c == '\t':
		case c < 0x20 || c == 0x7f:
			return false
		case c == 0xc2 && i+1 < len(s) && s[i+1] >= 0x80 && s[i+1] <= 0x9f:
			return false // a C1 control, written in UTF-8
		case c >= utf8.RuneSelf && !utf8.FullRuneInString(s[i:]):
			return false
		}
	}
	return utf8.ValidString(s)
}

// SafeStream is Safe for text that arrives in pieces, such as a reply as
// the model writes it: an escape sequence cut by the end of a piece is held
// back until the rest of it comes, so that a style is kept whole and no
// half of another sequence slips through as text. The zero value is ready
// to use.
type SafeStream struct {
	held string
	open bool
}

// Write takes the next piece and returns what of it may be written now.
// A style it opens is closed by End.
func (st *SafeStream) Write(piece string) string {
	s := st.held + piece
	st.held = ""
	if i := strings.LastIndexByte(s, escapeByte); i >= 0 {
		if tok, rest := NextToken(s[i:]); !tok.Complete && rest == "" && len(tok.Text) < 4096 {
			st.held, s = s[i:], s[:i]
		}
	}
	if safeAlready(s) {
		return s
	}
	out, open := safe(s)
	if strings.Contains(out, "\x1b[") {
		st.open = open
	}
	return out
}

// End drops what was held back, as Reset does, and returns the style that
// closes what the text left open, or "".
func (st *SafeStream) End() string {
	st.held = ""
	if !st.open {
		return ""
	}
	st.open = false
	return sgrReset
}

// Reset drops what was held back: the text ended inside a sequence, which
// is not written.
func (st *SafeStream) Reset() { st.held = "" }
