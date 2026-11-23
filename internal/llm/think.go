package llm

import "strings"

const (
	thinkOpen  = "<think>"
	thinkClose = "</think>"
)

// thinkSplitter separates reasoning that a model wrote inline, between
// <think> and </think>, from the answer around it. Text is fed to it in
// whatever pieces the server sends; it hands each piece on as thinking or
// as text, and holds back only what might still turn out to be part of a
// tag, so a tag cut in two by the server is still seen.
//
// The rules:
//
//   - A <think> block is only looked for at the start of the answer, after
//     any white space, and again right after a block that just closed. Once
//     the answer's own words have begun, a "<think>" in them is text: a
//     model that explains think tags must not have the rest of its answer
//     swallowed.
//   - White space around a block's contents is dropped, and so is the white
//     space between a block's end and the answer. When two blocks follow
//     each other, the second's thinking is set off by a blank line.
//   - A block that never closes is thinking to the end of the stream.
//   - Text with no block at its start passes through exactly as it came.
//     A </think> before the answer's first word closes a block the server's
//     chat template opened (Qwen with a picture in the prompt and thinking
//     off writes one), and is dropped with the white space after it. One
//     later is left alone, because by the time it shows up the text before
//     it has been handed on.
type thinkSplitter struct {
	state   int
	hold    string // fed but not handed on yet
	lead    bool   // inside a block before its first word: white space is dropped
	after   bool   // a block has closed: white space before the answer is dropped
	thought bool   // some thinking was handed on
	gap     bool   // the next thinking handed on begins another block
}

const (
	thinkAtStart = iota // before the answer's first word
	thinkInside         // inside a <think> block
	thinkInText         // in the answer: everything is text from here on
)

// feed takes the next piece of the answer and calls emit, in order, for
// each stretch that can be handed on.
func (t *thinkSplitter) feed(s string, emit func(thinking bool, s string)) {
	if s == "" {
		return
	}
	if t.state == thinkInText {
		emit(false, s)
		return
	}
	t.hold += s
	t.run(emit, false)
}

// flush hands on whatever is still held back. It is called when the answer
// is over.
func (t *thinkSplitter) flush(emit func(thinking bool, s string)) {
	t.run(emit, true)
}

// run hands on as much of what is held as can be told apart. With final
// set nothing more is coming, and everything held is handed on.
func (t *thinkSplitter) run(emit func(thinking bool, s string), final bool) {
	for {
		switch t.state {
		case thinkAtStart:
			rest := strings.TrimLeft(t.hold, thinkSpace)
			if rest == "" && !final {
				return // Nothing but white space yet.
			}
			if strings.HasPrefix(rest, thinkOpen) {
				t.hold = rest[len(thinkOpen):]
				t.state = thinkInside
				t.lead = true
				t.gap = t.thought
				continue
			}
			if strings.HasPrefix(rest, thinkClose) {
				t.hold = rest[len(thinkClose):]
				t.after = true
				continue
			}
			if !final && (strings.HasPrefix(thinkOpen, rest) || strings.HasPrefix(thinkClose, rest)) {
				return // This may be the first half of a tag.
			}
			text := t.hold
			if t.after {
				text = rest
			}
			t.hold = ""
			t.state = thinkInText
			if text != "" {
				emit(false, text)
			}
			return

		case thinkInside:
			if t.lead {
				t.hold = strings.TrimLeft(t.hold, thinkSpace)
				if t.hold == "" {
					return
				}
				t.lead = false
			}
			if i := strings.Index(t.hold, thinkClose); i >= 0 {
				t.think(emit, strings.TrimRight(t.hold[:i], thinkSpace))
				t.hold = t.hold[i+len(thinkClose):]
				t.state = thinkAtStart
				t.after = true
				continue
			}
			if final {
				t.think(emit, strings.TrimRight(t.hold, thinkSpace))
				t.hold = ""
				return
			}
			// Hand on everything but what could be the first half of
			// the closing tag, and the white space before it, which is
			// dropped if the tag does come next.
			body := t.hold[:len(t.hold)-partialSuffix(t.hold, thinkClose)]
			body = strings.TrimRight(body, thinkSpace)
			t.think(emit, body)
			t.hold = t.hold[len(body):]
			return

		default:
			return
		}
	}
}

// think hands on a stretch of thinking.
func (t *thinkSplitter) think(emit func(thinking bool, s string), s string) {
	if s == "" {
		return
	}
	if t.gap {
		t.gap = false
		s = "\n\n" + s
	}
	t.thought = true
	emit(true, s)
}

// thinkSpace is the white space that is dropped around a think block.
const thinkSpace = " \t\r\n"

// partialSuffix returns the length of the longest ending of s that is a
// beginning of tag without being all of it.
func partialSuffix(s, tag string) int {
	for k := min(len(tag)-1, len(s)); k > 0; k-- {
		if strings.HasSuffix(s, tag[:k]) {
			return k
		}
	}
	return 0
}
