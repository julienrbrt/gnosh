package prompt

import (
	"strings"
	"unicode/utf8"
)

// The marker. Everything gnosh itself says on the user's side of a
// conversation stands between Open and Close, so that a model can tell the
// harness speaking from the person typing. It is the same every time, and
// the first part of the system prompt tells the model what it means.
const (
	Open  = "<" + tagName + ">"
	Close = "</" + tagName + ">"
)

// tagName is the name inside the marker's brackets.
const tagName = "gnosh"

// memTag is the name inside the brackets the mem code puts around what it
// tells a session: the digest and what it recalls. Those blocks are
// gnosh's own words too, made elsewhere and sent as they are.
const memTag = "mem"

// agentMemoryTag is the block a sub-agent's MEMORY.md is given in
// (AgentMemory): text from outside can neither open nor close one.
const agentMemoryTag = "agent-memory"

// Wrap marks text as gnosh's own words: it trims it and puts it between
// Open and Close. Text that is empty once trimmed gives an empty string,
// so that a caller can leave an empty line out.
//
// The text goes through Untrusted first. Whatever it quotes (a command, a
// hook's reason, a file name) can therefore neither end the marker early
// nor start a second one, and cannot carry a control sequence: what Wrap
// returns holds Open once, at its start, and Close once, at its end.
//
// Every line this package makes for the user's side is already wrapped.
// Wrap is for text of gnosh's that comes from elsewhere and goes out the
// same way, such as the goal directive the mem code writes. A block the
// mem code has already put between its own tags is not wrapped: it goes
// out as it is, beside the lines.
func Wrap(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	return Open + Untrusted(text) + Close
}

// EstimateTokens says about how many tokens s is: one for every four
// bytes, rounded up. It is the measure the budgets in this package's tests
// are written in; a model's own tokenizer will differ by a little.
func EstimateTokens(s string) int {
	return (len(s) + 3) / 4
}

// oneLine makes s fit in the middle of a sentence: every run of blanks and
// line ends becomes one space, and what is longer than max bytes is cut
// and ends in "…".
func oneLine(s string, max int) string {
	return clip(strings.Join(strings.Fields(s), " "), max)
}

// Lines makes every line break in s a line feed: a carriage return (alone
// or before a line feed), the next-line character, and the line and
// paragraph separators, which a model may read as breaks of their own.
func Lines(s string) string {
	if !strings.ContainsAny(s, "\r\u0085\u2028\u2029") {
		return s
	}
	return strings.NewReplacer("\r\n", "\n", "\r", "\n", "\u0085", "\n", "\u2028", "\n", "\u2029", "\n").Replace(s)
}

// Indented is s with every line set two spaces in: a block of what the
// agent wrote, put under a heading of gnosh's in a judge's request, where
// no line of it may stand as a line of gnosh's own ("The end of the
// conversation:", "the user: …").
func Indented(s string) string {
	s = strings.TrimSpace(Lines(s))
	if s == "" {
		return ""
	}
	return "  " + strings.ReplaceAll(s, "\n", "\n  ")
}

// lines takes the blank lines off both ends of s and the blanks off its
// end, and cuts what is longer than max bytes, ending the cut in "…". The
// line ends inside s stay, and so does the indent of its first line, which
// in a listing such as a git status means something.
func lines(s string, max int) string {
	return clip(strings.TrimRight(strings.TrimLeft(s, "\r\n"), " \t\r\n"), max)
}

// clip cuts s to at most max bytes, at a character boundary, and ends a
// cut text with "…". A text that fits comes back as it was.
func clip(s string, max int) string {
	const dots = "…"
	if len(s) <= max {
		return s
	}
	cut := max - len(dots)
	if cut < 0 {
		cut = 0
	}
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + dots
}

// clipChars cuts s to at most max characters and ends a cut text with "…".
func clipChars(s string, max int) string {
	n := 0
	for i := range s {
		if n == max {
			return s[:i] + "…"
		}
		n++
	}
	return s
}
