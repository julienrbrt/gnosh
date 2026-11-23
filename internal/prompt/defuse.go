package prompt

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// Mark is the one character Defuse puts into a control sequence to make it
// harmless: U+200B, the zero-width space. It takes no room on a screen, a
// model reads past it, and a model server that looks for a control
// sequence byte for byte no longer finds one.
const Mark = "\u200b"

// fullBar is the full-width bar some chat templates write their control
// tokens with, as in <｜User｜>.
const fullBar = "\uff5c"

// Defuse makes the control sequences in s harmless and returns the result.
// It is for every text that is not gnosh's own words, on its way to a
// model: what a tool returned, what a file holds, what a person typed.
//
// A model server reads some sequences of plain characters as structure
// rather than as text: the tokens a chat template ends one message and
// starts the next with, and llama.cpp's marker for an image. A web page or
// a file that holds one could end the message it arrives in and start
// another, or make the server refuse the whole conversation. Every such
// sequence begins with "<", and Defuse puts one Mark right after that "<"
// wherever one of these follows it:
//
//   - a bar, as in <|im_start|>, <|im_end|>, <|endoftext|>, <|eot_id|>,
//     <|channel|>, and the same with the full-width bar, as in <｜User｜>;
//   - "__media", the stem of llama.cpp's media marker, both the fixed
//     <__media__> and the one a server makes up as it starts,
//     <__media_ and some random letters and __>;
//   - one of the words chat templates use without a bar, with or without a
//     "/" before it: <tool_call>, <tool_response>, <think>,
//     <start_of_turn>, <end_of_turn>.
//
// Nothing else changes, so text that holds none of these comes back as it
// was, without being copied. The work is one pass over the "<" characters
// of s. Defusing twice is the same as defusing once.
//
// Defuse works on the text it is given: a sequence that only comes
// together when two texts are joined is not seen. Defuse what is sent, not
// its pieces.
//
// Defuse leaves gnosh's own tags alone, so it is safe on a whole message
// that holds lines of gnosh's own. Untrusted is the stricter form.
func Defuse(s string) string {
	return defuse(s, false)
}

// Untrusted is Defuse for text that came from outside gnosh and outside
// the person: a tool's output, a file, a web page. Besides what Defuse
// does, it breaks gnosh's own two tags the same way: the marker, and the
// <mem> tag the mem code puts around what it tells a session, which holds
// the user's standing rules. Such text then cannot carry a line that reads
// as gnosh speaking, or a block that reads as the user's rules. It finds a
// tag in any letter case, opening or closing, and with blanks inside the
// brackets.
//
// It must not be used on what gnosh itself made (what Wrap returned, a
// block from the mem code), or on a message that holds one: it would break
// that tag too. Defuse is the one for whole messages.
func Untrusted(s string) string {
	return defuse(s, true)
}

// defuse puts a Mark after every "<" of s that opens a control sequence,
// and with own set, after every "<" that opens one of gnosh's own tags.
func defuse(s string, own bool) string {
	var b strings.Builder
	kept := 0 // s[:kept] has been written to b
	for i := 0; ; {
		at := strings.IndexByte(s[i:], '<')
		if at < 0 {
			break
		}
		i += at + 1
		if !opens(s[i:], own) {
			continue
		}
		if kept == 0 {
			b.Grow(len(s) + 16*len(Mark))
		}
		b.WriteString(s[kept:i])
		b.WriteString(Mark)
		kept = i
	}
	if kept == 0 {
		return s
	}
	b.WriteString(s[kept:])
	return b.String()
}

// Undefuse takes out again the Marks that Defuse and Untrusted put in. It
// is for what a model writes back: a model that copies a line out of a
// defused file into an edit or a command copies the Mark with it, and the
// file or the command must get the sequence as it was.
//
// It removes a Mark only where one would have been put: right after a "<"
// and before what makes a control sequence or one of gnosh's tags. So
// Undefuse(Defuse(s)) is s, unless s already held a Mark in exactly such a
// place.
func Undefuse(s string) string {
	const lead = "<" + Mark
	at := strings.Index(s, lead)
	if at < 0 {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	kept := 0
	for i := 0; at >= 0; at = strings.Index(s[i:], lead) {
		i += at + len(lead)
		if opens(s[i:], true) {
			b.WriteString(s[kept : i-len(Mark)])
			kept = i
		}
	}
	b.WriteString(s[kept:])
	return b.String()
}

// opens reports whether rest, the text that follows a "<", goes on as a
// control sequence, or with own set, as one of gnosh's own tags.
func opens(rest string, own bool) bool {
	if rest == "" {
		return false
	}
	switch rest[0] {
	case '|':
		return true
	case 0xEF: // the first byte of the full-width bar
		if strings.HasPrefix(rest, fullBar) {
			return true
		}
	case '_':
		return strings.HasPrefix(rest, "__media")
	}
	return templateTag(rest) || own && ownTag(rest)
}

// templateTag reports whether rest goes on as one of the bar-less control
// words of chat templates, closed by ">", with or without a "/" first.
func templateTag(rest string) bool {
	if rest[0] == '/' {
		rest = rest[1:]
	}
	if rest == "" {
		return false
	}
	switch rest[0] {
	case 't':
		return strings.HasPrefix(rest, "tool_call>") || strings.HasPrefix(rest, "tool_response>") || strings.HasPrefix(rest, "think>")
	case 's':
		return strings.HasPrefix(rest, "start_of_turn>")
	case 'e':
		return strings.HasPrefix(rest, "end_of_turn>")
	}
	return false
}

// ownTag reports whether rest goes on as one of gnosh's own tags.
func ownTag(rest string) bool {
	return tagNamed(rest, tagName) || tagNamed(rest, memTag) || tagNamed(rest, agentMemoryTag)
}

// tagNamed reports whether rest, the text that follows a "<", goes on as a
// tag called name, opening or closing, in any letter case: blanks, an
// optional "/", blanks, the name, blanks and the closing ">", as in
// "< / gnosh >"; or, with nothing between the "<" or "</" and the name,
// the name and a blank or a "/", as a tag with more in it begins:
// <gnosh source="x">, <gnosh/>. A blank between the "<" and a name that
// some other character follows is a comparison, as in "if used < mem {".
// After the name, a blank is any space Unicode has (a no-break space, a
// vertical tab) or a character that cannot be seen: a model reads past
// them, and <gnosh` + bs + `u00a0source="x"> reads as gnosh's tag.
func tagNamed(rest, name string) bool {
	i := skipBlanks(rest, 0)
	tight := i == 0
	if i < len(rest) && rest[i] == '/' {
		j := skipBlanks(rest, i+1)
		tight = tight && j == i+1
		i = j
	}
	s := rest[i:]
	if !hasName(s, name) {
		return false
	}
	after := s[len(name):]
	if j := skipWide(after); j < len(after) && after[j] == '>' {
		return true
	}
	if !tight || after == "" {
		return false
	}
	r, _ := utf8.DecodeRuneInString(after)
	return r == '/' || wideBlank(r)
}

// wideBlank reports whether r is a blank after a tag's name: a space of
// any kind Unicode has, or a character that takes no room and is not seen.
func wideBlank(r rune) bool {
	return unicode.IsSpace(r) || unicode.Is(unicode.Zs, r) || unicode.Is(unicode.Cf, r)
}

// skipWide returns the index of the first rune of s that is not a
// wideBlank.
func skipWide(s string) int {
	for i, r := range s {
		if !wideBlank(r) {
			return i
		}
	}
	return len(s)
}

// hasName reports whether s begins with name, in any letter case.
func hasName(s, name string) bool {
	if len(s) < len(name) {
		return false
	}
	for j := 0; j < len(name); j++ {
		c := s[j]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		if c != name[j] {
			return false
		}
	}
	return true
}

// skipBlanks returns the index of the first byte of s at or after i that
// is not a space, a tab or a line end.
func skipBlanks(s string, i int) int {
	for i < len(s) && (s[i] == ' ' || s[i] == '\t' || s[i] == '\n' || s[i] == '\r') {
		i++
	}
	return i
}
