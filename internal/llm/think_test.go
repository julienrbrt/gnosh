package llm

import (
	"strings"
	"testing"
)

// splitAll feeds pieces to a splitter and returns the thinking and the
// text it handed on, each joined up, and the order the two kinds came in.
func splitAll(pieces ...string) (thinking, text, order string) {
	var t thinkSplitter
	var th, tx, ord strings.Builder
	emit := func(isThinking bool, s string) {
		if s == "" {
			panic("the splitter handed on an empty piece")
		}
		if isThinking {
			th.WriteString(s)
			ord.WriteByte('T')
		} else {
			tx.WriteString(s)
			ord.WriteByte('t')
		}
	}
	for _, p := range pieces {
		t.feed(p, emit)
	}
	t.flush(emit)
	return th.String(), tx.String(), ord.String()
}

func TestThinkSplitter(t *testing.T) {
	tests := []struct {
		name     string
		pieces   []string
		thinking string
		text     string
	}{
		{"no tags", []string{"Hello, ", "world."}, "", "Hello, world."},
		{"nothing", nil, "", ""},
		{"empty pieces", []string{"", "a", "", "b", ""}, "", "ab"},
		{"only white space", []string{"  \n", "\t"}, "", "  \n\t"},
		{"leading white space is kept without a block", []string{"\n\n  Hello"}, "", "\n\n  Hello"},
		{"a block then the answer", []string{"<think>\nLet me see.\n</think>\n\nThe answer is 4."}, "Let me see.", "The answer is 4."},
		{"white space before the block", []string{"\n \t<think>hm</think>ok"}, "hm", "ok"},
		{"piece by piece", []string{"<think>", "one ", "two", "</think>", "three"}, "one two", "three"},
		{"opening tag cut in two", []string{"<th", "ink>deep</think>done"}, "deep", "done"},
		{"opening tag cut into letters", []string{"<", "t", "h", "i", "n", "k", ">", "x", "</think>", "y"}, "x", "y"},
		{"closing tag cut in two", []string{"<think>deep</th", "ink>done"}, "deep", "done"},
		{"closing tag cut into letters", []string{"<think>x", "<", "/", "t", "h", "i", "n", "k", ">", "y"}, "x", "y"},
		{"what looks like a tag and is not", []string{"<th", "under>storm"}, "", "<thunder>storm"},
		{"what looks like a closing tag and is not", []string{"<think>a </thi", "ng> b</think>c"}, "a </thing> b", "c"},
		{"a tag in the middle of the answer is text", []string{"Use the <think> tag like so: <think>x</think>."}, "", "Use the <think> tag like so: <think>x</think>."},
		{"a tag after the answer began, in another piece", []string{"Well. ", "<think>", "no", "</think>"}, "", "Well. <think>no</think>"},
		{"a lone closing tag is text", []string{"reasoning</think>answer"}, "", "reasoning</think>answer"},
		{"a closing tag before the answer closes the template's block", []string{"</think>\n\nRed."}, "", "Red."},
		{"so after white space", []string{"\n  </think>  Red."}, "", "Red."},
		{"so cut in two", []string{"</thi", "nk> Red"}, "", "Red"},
		{"half a closing tag at the very end", []string{"</thi"}, "", "</thi"},
		{"a block that never closes", []string{"<think>still going", " on"}, "still going on", ""},
		{"a block that never closes, ending in half a tag", []string{"<think>abc</thi"}, "abc</thi", ""},
		{"an empty block", []string{"<think></think>hi"}, "", "hi"},
		{"a block of white space", []string{"<think> \n </think> hi"}, "", "hi"},
		{"white space inside a block is kept", []string{"<think>a\n\n  b", "  \n c</think>d"}, "a\n\n  b  \n c", "d"},
		{"white space at a block's end is dropped", []string{"<think>a  ", "\n", "</think>b"}, "a", "b"},
		{"white space at an unclosed block's end is dropped", []string{"<think>a  ", "\n"}, "a", ""},
		{"two blocks", []string{"<think>one</think>\n<think>two</think>\nanswer"}, "one\n\ntwo", "answer"},
		{"two blocks, the first empty", []string{"<think></think><think>two</think>answer"}, "two", "answer"},
		{"the answer after a block keeps its inner spacing", []string{"<think>x</think>  a  b \n"}, "x", "a  b \n"},
		{"half an opening tag at the very end", []string{"<thi"}, "", "<thi"},
		{"half an opening tag after a block", []string{"<think>x</think>\n<thi"}, "x", "<thi"},
		{"only a block", []string{"<think>x</think>"}, "x", ""},
		{"only a block and white space", []string{"<think>x</think>\n\n"}, "x", ""},
		{"tags are case sensitive", []string{"<THINK>x</THINK>y"}, "", "<THINK>x</THINK>y"},
		{"a tag right after text with no space", []string{"a<think>b</think>"}, "", "a<think>b</think>"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			thinking, text, _ := splitAll(tt.pieces...)
			if thinking != tt.thinking || text != tt.text {
				t.Errorf("pieces %q:\n got thinking %q, text %q\nwant thinking %q, text %q", tt.pieces, thinking, text, tt.thinking, tt.text)
			}
			// The same input in one piece, and one byte at a time, must
			// come out the same.
			whole := strings.Join(tt.pieces, "")
			if th, tx, _ := splitAll(whole); th != tt.thinking || tx != tt.text {
				t.Errorf("in one piece: got thinking %q, text %q", th, tx)
			}
			if th, tx, _ := splitAll(strings.Split(whole, "")...); th != tt.thinking || tx != tt.text {
				t.Errorf("byte by byte: got thinking %q, text %q", th, tx)
			}
		})
	}
}

func TestThinkSplitterOrder(t *testing.T) {
	// Thinking comes out before the text that follows it, and nothing is
	// held back longer than it has to be.
	var s thinkSplitter
	var got []string
	emit := func(thinking bool, piece string) {
		kind := "text"
		if thinking {
			kind = "thinking"
		}
		got = append(got, kind+":"+piece)
	}
	steps := []struct {
		feed string
		want string // what must have come out by now, joined with " | "
	}{
		{"<think>", ""},
		{"first", "thinking:first"},
		{" second ", "thinking:first | thinking: second"},
		{"</think>", "thinking:first | thinking: second"},
		{"\n\n", "thinking:first | thinking: second"},
		{"Answer", "thinking:first | thinking: second | text:Answer"},
		{" <think>", "thinking:first | thinking: second | text:Answer | text: <think>"},
	}
	for _, st := range steps {
		s.feed(st.feed, emit)
		if j := strings.Join(got, " | "); j != st.want {
			t.Fatalf("after feeding %q:\n got %s\nwant %s", st.feed, j, st.want)
		}
	}
}

// referenceSplit is the splitter's rules applied the slow, obvious way to
// the whole answer at once. The fuzz test holds the real splitter to it.
func referenceSplit(s string) (thinking, text string) {
	var blocks []string
	rest := s
	after := false
	for {
		t := strings.TrimLeft(rest, thinkSpace)
		if strings.HasPrefix(t, thinkClose) {
			// A closing tag before the answer's first word: the end of a
			// block the template opened.
			rest = t[len(thinkClose):]
			after = true
			continue
		}
		if !strings.HasPrefix(t, thinkOpen) {
			if after {
				text = t
			} else {
				text = rest
			}
			break
		}
		body := t[len(thinkOpen):]
		i := strings.Index(body, thinkClose)
		if i < 0 {
			if b := strings.Trim(body, thinkSpace); b != "" {
				blocks = append(blocks, b)
			}
			break
		}
		if b := strings.Trim(body[:i], thinkSpace); b != "" {
			blocks = append(blocks, b)
		}
		rest = body[i+len(thinkClose):]
		after = true
	}
	return strings.Join(blocks, "\n\n"), text
}

func FuzzThinkSplitter(f *testing.F) {
	for _, seed := range []string{
		"",
		"plain text",
		"<think>reasoning</think>answer",
		"\n<think>\na\n</think>\n\nb",
		"<think>one</think> <think>two</think> three <think>four</think>",
		"<think>never closed",
		"<thi",
		"<think></thi",
		"a</think>b",
		"<think> </think> <think>\t</think>x",
		"<think>a </thing> b</think>",
		"<<think>>",
		"<think><think>x</think></think>y",
	} {
		f.Add(seed, uint8(1), uint8(3))
	}
	f.Fuzz(func(t *testing.T, s string, a, b uint8) {
		wantThinking, wantText := referenceSplit(s)

		// Cut the answer into pieces three ways: whole, in bytes, and
		// in pieces whose sizes the fuzzer picks.
		var cuts [][]string
		cuts = append(cuts, []string{s}, strings.Split(s, ""))
		var pieces []string
		sizes := []int{int(a)%7 + 1, int(b)%5 + 1}
		for rest, i := s, 0; rest != ""; i++ {
			n := min(sizes[i%2], len(rest))
			pieces = append(pieces, rest[:n])
			rest = rest[n:]
		}
		cuts = append(cuts, pieces)

		for _, cut := range cuts {
			thinking, text, order := splitAll(cut...)
			if thinking != wantThinking || text != wantText {
				t.Fatalf("input %q cut as %q:\n got thinking %q, text %q\nwant thinking %q, text %q", s, cut, thinking, text, wantThinking, wantText)
			}
			// Once text has begun, nothing is thinking any more.
			if i := strings.IndexByte(order, 't'); i >= 0 && strings.ContainsRune(order[i:], 'T') {
				t.Fatalf("input %q cut as %q: thinking came after text (%s)", s, cut, order)
			}
		}
		// Text with no block, and no closing tag, at its start passes
		// through untouched.
		if lead := strings.TrimLeft(s, thinkSpace); !strings.HasPrefix(lead, thinkOpen) && !strings.HasPrefix(lead, thinkClose) && wantText != s {
			t.Fatalf("input %q has no block at its start and came out as %q", s, wantText)
		}
	})
}
