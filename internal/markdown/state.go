package markdown

import (
	"strings"
	"unicode/utf8"
)

// The kinds of container a line can stand in.
const (
	fQuote uint8 = iota // a block quote
	fItem               // a list item
)

// What the last block to end at some level was, which decides whether a
// blank line goes in front of the next one.
const (
	lastNone  uint8 = iota
	lastBlock       // anything but the two below
	lastList        // a list item
	lastDef         // a link reference definition
)

// A level is what a container, or the document itself, remembers about the
// blocks directly inside it.
type level struct {
	child   bool  // a block has been opened here
	emitted bool  // a line has been written here
	last    uint8 // what the last block here was
}

// A frame is an open container: a block quote or a list item.
type frame struct {
	kind   uint8
	indent int    // item: the columns of indentation a line needs to go on in it
	first  string // item: its marker, drawn, with the space after it
	plain  string // item: its marker as plain text
	rest   string // what stands in front of every other line inside it
	width  int    // the cells first and rest take
	marked bool   // item: the marker has been written
	bare   bool   // item: it began with an empty line and nothing has come since
	lvl    level
}

// A state is everything a Renderer knows about the text it is in the
// middle of: the part of the input that no block has taken yet, the open
// containers, the open leaf block, and the lines that became final in the
// current call. All of it is a function of the text written so far (and of
// the widths it was written at), never of how the text was cut into
// pieces: that is what makes the pieces not matter.
type state struct {
	carry []byte // the end of the input that cannot be read yet: half a character, or a CR that may belong to a CRLF
	cur   string // the current line so far, while it is not known what it is
	taken bool   // the open leaf is taking the current line's text as it arrives

	frames []frame
	root   level
	leaf   leaf

	prevBlank bool // the line before the current one was blank
	gap       bool // a blank line is due before the next line written
	gapDepth  int  // the number of frames whose marks that blank line carries

	refs      map[string]linkRef // link reference definitions seen so far
	refsOwned bool               // refs is this state's own to write to

	out []string

	trace *[]string // for tests: the blocks as they open and close
	code  *[][]byte // for CodeBlocks: the code of each block, as written
	langs *[]string // and the language named on each
}

// note records, for the tests that ask for it, that a block has opened or
// a container has closed.
func (st *state) note(event string) {
	if st.trace != nil {
		*st.trace = append(*st.trace, event)
	}
}

// clone returns a copy of the state that can be finished without changing
// the original. It copies what is live, which is small.
func (st *state) clone() state {
	c := *st
	c.out = nil
	c.carry = append([]byte(nil), st.carry...)
	c.frames = append([]frame(nil), st.frames...)
	c.leaf = st.leaf.clone()
	c.refsOwned = false
	c.trace, c.code, c.langs = nil, nil, nil
	return c
}

// feed takes the next piece of the text. With eof it also ends the text.
func (st *state) feed(cfg *config, data string, eof bool) {
	if len(st.carry) > 0 {
		data = string(st.carry) + data
		st.carry = st.carry[:0]
	}
	if !eof {
		if keep := heldTail(data); keep > 0 {
			st.carry = append(st.carry, data[len(data)-keep:]...)
			data = data[:len(data)-keep]
		}
	}
	for data != "" {
		k := strings.IndexAny(data, "\n\r")
		if k < 0 {
			st.lineBytes(cfg, data)
			break
		}
		line, next := data[:k], k+1
		if data[k] == '\r' && next < len(data) && data[next] == '\n' {
			next++
		}
		data = data[next:]
		if st.taken || st.cur != "" {
			st.lineBytes(cfg, line)
			st.lineEnd(cfg, st.cur)
		} else {
			st.lineEnd(cfg, line)
		}
	}
	if eof {
		if st.taken || st.cur != "" {
			st.lineEnd(cfg, st.cur)
		}
		st.closeTo(cfg, 0)
	}
}

// heldTail returns the number of bytes at the end of data that cannot be
// read before more arrives: a carriage return, which may be half of a
// CRLF, or the first bytes of a character.
func heldTail(data string) int {
	n := len(data)
	if n == 0 {
		return 0
	}
	if data[n-1] == '\r' {
		return 1
	}
	for back := 1; back <= 3 && back <= n; back++ {
		c := data[n-back]
		if c < 0x80 {
			return 0
		}
		if c >= 0xc0 {
			size := 2
			if c >= 0xf0 {
				size = 4
			} else if c >= 0xe0 {
				size = 3
			}
			if back < size && !utf8.FullRuneInString(data[n-back:]) {
				return back
			}
			return 0
		}
	}
	return 0
}

// lineBytes takes more bytes of the current line. While it is not yet
// known what the line is they are kept; once it is, the open leaf has them.
func (st *state) lineBytes(cfg *config, seg string) {
	if seg == "" {
		return
	}
	if st.taken {
		st.leafText(cfg, seg)
		return
	}
	st.cur += seg
	p := st.classify(cfg, st.cur, false)
	if p.need {
		return
	}
	st.apply(cfg, &p, st.cur)
	st.cur = ""
}

// lineEnd ends the current line. line is the whole of it when no leaf has
// taken it yet.
func (st *state) lineEnd(cfg *config, line string) {
	if !st.taken {
		p := st.classify(cfg, line, true)
		st.apply(cfg, &p, line)
	}
	if st.taken {
		switch st.leaf.kind {
		case lPara:
			st.leaf.para.endLine(st, cfg)
		case lHead:
			st.leaf.head.finish(st, cfg)
			st.leaf = leaf{}
		case lCode:
			st.leaf.code.endLine(st, cfg)
		}
	}
	st.cur = ""
	st.taken = false
}

// leafText hands more of the current line to the open leaf.
func (st *state) leafText(cfg *config, s string) {
	switch st.leaf.kind {
	case lPara:
		st.leaf.para.text(st, cfg, s)
	case lHead:
		st.leaf.head.text(st, cfg, s)
	case lCode:
		st.leaf.code.text(st, cfg, s)
	}
}

// tip returns the level that a block opened now would stand in.
func (st *state) tip() *level {
	return st.levelAt(len(st.frames))
}

// levelAt returns the level inside the first depth frames.
func (st *state) levelAt(depth int) *level {
	depth = min(depth, len(st.frames))
	if depth == 0 {
		return &st.root
	}
	return &st.frames[depth-1].lvl
}

// budget is the most cells the marks of the containers may take of a line.
// What is nested deeper than that is not set in any further. It leaves
// half the line, or sixteen cells, to the text, and never less than the
// two cells a wide character needs.
func (cfg *config) budget() int {
	return max(cfg.width-max(2, min(cfg.width/2, 16)), 0)
}

// room returns the number of cells a line of the open leaf has, after the
// marks of the containers it stands in.
func (st *state) room(cfg *config) int {
	used, budget := 0, cfg.budget()
	for i := range st.frames {
		if w := st.frames[i].width; used+w <= budget {
			used += w
		}
	}
	return max(cfg.width-used, 1)
}

// rests returns what stands in front of a line inside the first depth
// frames that is not the first line of any of them.
func (st *state) rests(cfg *config, depth int) string {
	var b strings.Builder
	used, budget := 0, cfg.budget()
	for i := 0; i < depth && i < len(st.frames); i++ {
		if f := &st.frames[i]; used+f.width <= budget {
			b.WriteString(f.rest)
			used += f.width
		}
	}
	return b.String()
}

// emit writes one line of the open leaf: the blank line that is due before
// it, if one is, then the marks of the containers, then content. A list
// item's marker goes on the first line written inside the item.
func (st *state) emit(cfg *config, content string) {
	if st.gap {
		st.gap = false
		if st.levelAt(st.gapDepth).emitted {
			st.out = append(st.out, trimRight(st.rests(cfg, st.gapDepth)))
		}
	}
	st.root.emitted = true
	var b strings.Builder
	used, budget := 0, cfg.budget()
	for i := range st.frames {
		f := &st.frames[i]
		f.lvl.emitted = true
		fits := used+f.width <= budget
		switch {
		case f.kind == fItem && !f.marked:
			f.marked = true
			if fits {
				b.WriteString(f.first)
				used += f.width
				break
			}
			// So deep that there is no room to set the item in: its marker
			// gets a line to itself.
			cfg.cutRows(cleanString(f.plain), cfg.width-used, true, func(row string) {
				st.out = append(st.out, b.String()+cfg.theme.ListMark.Render(row))
			})
			b.Reset()
			b.WriteString(st.rests(cfg, i))
		case fits:
			b.WriteString(f.rest)
			used += f.width
		}
	}
	b.WriteString(content)
	line := b.String()
	if content == "" {
		line = trimRight(line)
	}
	st.out = append(st.out, line)
}

// popFrame closes the innermost container.
func (st *state) popFrame(cfg *config) {
	n := len(st.frames) - 1
	f := &st.frames[n]
	if f.kind == fItem {
		if !f.marked {
			st.emit(cfg, "") // an item with nothing in it still shows its marker
		}
		st.levelAt(n).last = lastList
		st.note("</item>")
	} else {
		st.note("</quote>")
	}
	st.frames = st.frames[:n]
}

// closeTo ends the open leaf and every container but the first keep.
func (st *state) closeTo(cfg *config, keep int) {
	st.closeLeaf(cfg)
	for len(st.frames) > keep {
		st.popFrame(cfg)
	}
}

// closeLeaf ends the open leaf block, writing what it still holds.
func (st *state) closeLeaf(cfg *config) {
	switch st.leaf.kind {
	case lPara:
		st.leaf.para.finish(st, cfg)
	case lHead:
		st.leaf.head.finish(st, cfg)
	case lTable:
		st.leaf.table.finish(st, cfg)
	}
	st.leaf = leaf{}
}

// planGap decides whether a blank line goes in front of a block that is
// about to open in the innermost container. Blocks are a blank line apart,
// with three exceptions that follow what was written instead: inside a
// list item, between the items of a list, and between link reference
// definitions, there is a blank line only where the text has one. The
// first block in a container never has one.
func (st *state) planGap(item, def bool) {
	lvl := st.tip()
	inItem := len(st.frames) > 0 && st.frames[len(st.frames)-1].kind == fItem
	switch {
	case !lvl.child:
		// The first block in its container stands on the container's first
		// line. A blank line that is due before the container stays due.
		return
	case inItem, item && lvl.last == lastList, def && lvl.last == lastDef:
		st.gap = st.prevBlank
	default:
		st.gap = true
	}
	st.gapDepth = len(st.frames)
}

// opened notes that a block has been opened in the innermost container.
func (st *state) opened(last uint8) {
	lvl := st.tip()
	lvl.child = true
	lvl.last = last
	if n := len(st.frames); n > 0 {
		st.frames[n-1].bare = false
	}
}

// define records a link reference definition. The first definition of a
// label wins, as in CommonMark.
func (st *state) define(label string, ref linkRef) {
	key := normalLabel(label)
	if _, ok := st.refs[key]; ok {
		return
	}
	if !st.refsOwned {
		refs := make(map[string]linkRef, len(st.refs)+1)
		for k, v := range st.refs {
			refs[k] = v
		}
		st.refs, st.refsOwned = refs, true
	}
	st.refs[key] = ref
}

// quoted reports whether a block quote is open around what comes next.
func (st *state) quoted() bool {
	for i := range st.frames {
		if st.frames[i].kind == fQuote {
			return true
		}
	}
	return false
}
