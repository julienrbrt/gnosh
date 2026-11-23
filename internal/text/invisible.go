package text

import (
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// This file takes out of a text the characters a terminal draws as
// nothing, as Claude Code does to a prompt before it is sent: text copied
// from a page or a message can carry them, and with them words or an order
// of words the person never saw.
//
// What is taken out:
//
//   - tag characters (U+E0000 to U+E007F), which spell text no terminal
//     draws, except in the three subdivision flags emoji have (England,
//     Scotland, Wales), and the code points after them that nothing is
//     assigned to;
//   - the controls that change the order text is drawn in (U+202A to
//     U+202E, U+2066 to U+2069) and the marks that nudge it (U+200E,
//     U+200F, U+061C);
//   - zero-width spaces and joiners (U+200B, U+2060 to U+2064, U+FEFF,
//     U+180E), the soft hyphen, the combining grapheme joiner, the old
//     format controls (U+206A to U+206F), the Hangul fillers, and the
//     format controls of interlinear notes and of music (U+FFF9 to
//     U+FFFB, U+1D173 to U+1D17A);
//   - the zero-width joiner and non-joiner (U+200D, U+200C), except
//     between two characters that are not ASCII, where Persian and the
//     scripts of India write them and emoji are joined with them;
//   - variation selectors (U+FE00 to U+FE0F), except one right after a
//     symbol, a mark of punctuation or a Han character, or after a digit,
//     # or * that a keycap mark (U+20E3) follows, which is how emoji are
//     written; and those
//     from U+E0100 to U+E01EF, except one right after a Han character,
//     which is how its variants are written.

// Invisible is what CleanInvisible took out of a text.
type Invisible struct {
	// Removed counts the characters taken out.
	Removed int
	// Hiding says some of them can hide text or change its order: tag
	// characters, the controls of the drawing order, or three or more
	// such characters in a row, the way hidden bytes are spelled. What
	// was seen may then not be what was there, so a person should read
	// the text again before it goes anywhere.
	Hiding bool
}

// CleanInvisible returns s without the characters a terminal draws as
// nothing (see above), and what it took out.
func CleanInvisible(s string) (string, Invisible) {
	out, _, found := clean(s, nil)
	return out, found
}

// CleanInvisibleAt is CleanInvisible that also says where offsets into s
// land in what it returns: each byte offset in at, which must stand on
// character boundaries, comes back as the offset of the same place in the
// result. It is how a paste inside a prompt keeps its bounds when the
// whole prompt is cleaned at once. When s is not UTF-8, the places cannot
// be told and the offsets come back nil.
func CleanInvisibleAt(s string, at []int) (string, []int, Invisible) {
	return clean(s, at)
}

// clean is CleanInvisible and CleanInvisibleAt.
func clean(s string, at []int) (string, []int, Invisible) {
	var found Invisible
	if !mayHoldInvisible(s) {
		return s, at, found
	}
	src := s
	if !utf8.ValidString(src) {
		// Bytes that are not UTF-8 stand as the replacement character, as
		// they are drawn: taking out what stands between two such pieces
		// would join them into a character, which may be one of these
		// ("\xe2", a zero-width space, "\x80\x8b" is another one).
		src = strings.ToValidUTF8(src, string(utf8.RuneError))
		at = nil
	}
	var b strings.Builder
	b.Grow(len(src))
	var cuts [][2]int // the byte ranges of src taken out
	run := 0          // invisible characters taken out in a row
	prev := '\n'      // the last character kept
	for i := 0; i < len(src); {
		r, n := utf8.DecodeRuneInString(src[i:])
		if r == 0x1F3F4 {
			// The flags of England, Scotland and Wales: a black flag, tag
			// letters, and the cancel tag that ends them. Kept whole.
			if end := flagTags(src, i+n); end > 0 {
				b.WriteString(src[i:end])
				i, prev, run = end, 0xE007F, 0
				continue
			}
		}
		next := rune(-1)
		if i+n < len(src) {
			next, _ = utf8.DecodeRuneInString(src[i+n:])
		}
		cut, hiding := invisible(r, prev, next)
		if !cut {
			b.WriteString(src[i : i+n])
			prev, run = r, 0
			i += n
			continue
		}
		cuts = append(cuts, [2]int{i, i + n})
		found.Removed++
		run++
		found.Hiding = found.Hiding || hiding || run >= 3
		i += n
	}
	if found.Removed == 0 {
		return s, at, found
	}
	var moved []int
	if at != nil {
		moved = make([]int, len(at))
		for k, off := range at {
			gone := 0
			for _, c := range cuts {
				if c[0] >= off {
					break
				}
				gone += min(c[1], off) - c[0]
			}
			moved[k] = off - gone
		}
	}
	return b.String(), moved, found
}

// ShowInvisible returns s with each character CleanInvisible would take
// out written as its code point, <U+202E>: where a person must read what
// is there, the command they are asked to let run, nothing in it may be
// drawn as nothing or turn the order of what is drawn around.
func ShowInvisible(s string) string {
	if !mayHoldInvisible(s) {
		return s
	}
	src := strings.ToValidUTF8(s, string(utf8.RuneError))
	var b strings.Builder
	b.Grow(len(src) + 16)
	prev := '\n' // the last character kept as it is
	for i := 0; i < len(src); {
		r, n := utf8.DecodeRuneInString(src[i:])
		if r == 0x1F3F4 {
			if end := flagTags(src, i+n); end > 0 {
				b.WriteString(src[i:end])
				i, prev = end, 0xE007F
				continue
			}
		}
		next := rune(-1)
		if i+n < len(src) {
			next, _ = utf8.DecodeRuneInString(src[i+n:])
		}
		if cut, _ := invisible(r, prev, next); cut {
			b.WriteString("<U+")
			hex := strings.ToUpper(strconv.FormatInt(int64(r), 16))
			b.WriteString(strings.Repeat("0", max(4-len(hex), 0)) + hex)
			b.WriteString(">")
		} else {
			b.WriteString(src[i : i+n])
			prev = r
		}
		i += n
	}
	return b.String()
}

// mayHoldInvisible reports whether s has a character beyond ASCII at all:
// most prompts have none, and are passed back untouched.
func mayHoldInvisible(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= utf8.RuneSelf {
			return true
		}
	}
	return false
}

// flagTags returns where an emoji flag's tags end when the text from at
// holds the tags of one of the three subdivision flags emoji have (gbeng,
// gbsct, gbwls) and the cancel tag after them. It returns 0 for any other
// run of tags, which is text hidden behind a black flag.
func flagTags(s string, at int) int {
	var letters []byte
	for i := at; i < len(s); {
		r, n := utf8.DecodeRuneInString(s[i:])
		switch {
		case r >= 0xE0061 && r <= 0xE007A && len(letters) < 5:
			letters = append(letters, byte(r-0xE0000))
		case r == 0xE007F:
			switch string(letters) {
			case "gbeng", "gbsct", "gbwls":
				return i + n
			}
			return 0
		default:
			return 0
		}
		i += n
	}
	return 0
}

// invisible reports whether r is taken out, standing between prev (the
// last character kept, '\n' at the start) and next (-1 at the end), and
// whether it is one of those that can hide text or change its order.
func invisible(r, prev, next rune) (cut, hiding bool) {
	switch {
	case r < 0xAD:
		return false, false
	case r >= 0xE0000 && r <= 0xE007F, r >= 0x202A && r <= 0x202E, r >= 0x2066 && r <= 0x2069:
		return true, true // tag characters; embeddings, overrides and isolates
	case alwaysCut(r):
		return true, false
	case joiner(r):
		// One, between two characters that are drawn and are not ASCII
		// (an emoji's selector may stand before it).
		return !((base(prev) || selector(prev)) && base(next)), false
	case r >= 0xFE00 && r <= 0xFE0F:
		keycap := (prev >= '0' && prev <= '9' || prev == '#' || prev == '*') && next == 0x20E3
		return !(base(prev) && selectable(prev) || keycap), false
	case r >= 0xE0100 && r <= 0xE01EF:
		return !han(prev), false // the variants of Han characters
	}
	return false, false
}

// alwaysCut reports whether r is taken out wherever it stands: the marks
// that nudge the drawing order, the zero-width spaces, the soft hyphen
// and the format controls of interlinear notes and of music.
func alwaysCut(r rune) bool {
	return r == 0x200E || r == 0x200F || r == 0x061C || r == 0x200B || r >= 0x2060 && r <= 0x2064 ||
		r == 0xFEFF || r == 0x180E || r == 0xAD || r >= 0xFFF9 && r <= 0xFFFB || r >= 0x1D173 && r <= 0x1D17A ||
		r >= 0xE0000 && r <= 0xE007F || r >= 0x202A && r <= 0x202E || r >= 0x2066 && r <= 0x2069 ||
		r >= 0x206A && r <= 0x206F || r == 0x034F || r == 0x115F || r == 0x1160 || r == 0x3164 || r == 0xFFA0 ||
		r >= 0xE0080 && r <= 0xE00FF || r >= 0xE01F0 && r <= 0xE0FFF
}

// joiner reports whether r is the zero-width joiner or non-joiner.
func joiner(r rune) bool { return r == 0x200C || r == 0x200D }

// selector reports whether r is a variation selector.
func selector(r rune) bool {
	return r >= 0xFE00 && r <= 0xFE0F || r >= 0xE0100 && r <= 0xE01EF
}

// base reports whether r is a character beyond ASCII that is drawn: not a
// blank, and none of the characters above. A joiner or a selector may
// follow one.
func base(r rune) bool {
	switch {
	case r < utf8.RuneSelf, r == 0x85, r == 0xA0, r == 0x3000, r >= 0x2000 && r <= 0x200A, r == 0x2028, r == 0x2029:
		return false
	}
	return !alwaysCut(r) && !joiner(r) && !selector(r)
}

// selectable reports whether r is a character a variation selector from
// U+FE00 to U+FE0F may follow: a symbol or a mark of punctuation (the
// emoji, the copyright sign, a double exclamation mark), a Han character,
// or U+2139, the one letter among the emoji. A selector after a letter or
// a digit draws nothing and changes nothing,
// and one after each letter of a text is a way to hide bytes in it.
func selectable(r rune) bool {
	return r == 0x2139 || han(r) || unicode.IsSymbol(r) || unicode.IsPunct(r)
}

// han reports whether r is a Han character, which the selectors from
// U+E0100 on may follow.
func han(r rune) bool {
	return r >= 0x3400 && r <= 0x4DBF || r >= 0x4E00 && r <= 0x9FFF || r >= 0xF900 && r <= 0xFAFF || r >= 0x20000 && r <= 0x3FFFF
}
