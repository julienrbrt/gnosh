package markdown

import "strings"

// The limits of a table's layout.
const (
	tableGap    = 2  // the cells between two columns
	tableMinCol = 8  // a column is not squeezed below this many cells,
	tableWord   = 24 // nor below its longest word, unless that is longer than this
	tableBar    = 24 // the most cells of the rule between two records of a stacked table
)

// A cell is one cell of a table: its text with its styles, and the width
// of its longest line.
type cell struct {
	buf lineBuf
	w   int
}

// A tableLeaf is an open table.
//
// A table is the one block whose earlier lines depend on later ones: the
// width of a column is that of its widest cell. So a table stays live,
// rows and all, until it ends. A long one would keep more on screen than
// can be redrawn, so there is a cap: once a table would take more than
// TableLive rows of the terminal, what there is of it is written with the
// widths found so far, the widths are frozen, and every later row is
// written as soon as it is complete, wrapped into those widths.
type tableLeaf struct {
	aligns []byte   // per column: 'l', 'c', 'r', or 0 for none given
	head   []cell   // the header row
	rows   [][]cell // complete rows that are not written yet
	nat    []int    // per column: the width of its widest cell so far
	word   []int    // per column: the width of its longest word so far
	frozen bool     // the widths no longer change, and rows are written as they come
	mark   tableMark
}

// A tableMark is how far the writing of a table has got.
type tableMark struct {
	head  bool // the header is written
	any   bool // a row is written
	multi bool // the last row written took more than one line
}

// clone returns a copy of the table for Live to finish. The cells are
// shared: nothing changes them once they are made.
func (t *tableLeaf) clone() *tableLeaf {
	c := *t
	c.rows = t.rows[:len(t.rows):len(t.rows)]
	c.nat = append([]int(nil), t.nat...)
	c.word = append([]int(nil), t.word...)
	return &c
}

// splitRow cuts a row of a table into the texts of its cells. A pipe ends a
// cell unless a backslash stands before it, in which case it is a pipe in
// the cell's text. The pipes at the two ends of the row are optional.
func splitRow(line string) []string {
	line = strings.TrimSpace(line)
	line = strings.TrimPrefix(line, "|")
	var cells []string
	var cur strings.Builder
	closed := false // the row ended with a pipe
	for k := 0; k < len(line); k++ {
		c := line[k]
		closed = false
		switch {
		case c == '\\' && k+1 < len(line) && line[k+1] == '|':
			cur.WriteByte('|')
			k++
		case c == '|':
			cells = append(cells, strings.TrimSpace(cur.String()))
			cur.Reset()
			closed = true
		default:
			cur.WriteByte(c)
		}
	}
	if !closed {
		cells = append(cells, strings.TrimSpace(cur.String()))
	}
	return cells
}

// parseDelim reads the delimiter row of a table: cells of hyphens, each
// with an optional colon at either end that says how its column is
// aligned. The row must hold a pipe, or a row of plain hyphens under a
// line of text would be a table and not the underline of a heading.
func parseDelim(line string) (aligns []byte, ok bool) {
	if !strings.Contains(line, "|") {
		return nil, false
	}
	for _, c := range splitRow(line) {
		left, right := strings.HasPrefix(c, ":"), strings.HasSuffix(c, ":") && len(c) > 1
		dashes := strings.TrimSuffix(strings.TrimPrefix(c, ":"), ":")
		if dashes == "" || strings.Trim(dashes, "-") != "" {
			return nil, false
		}
		switch {
		case left && right:
			aligns = append(aligns, 'c')
		case right:
			aligns = append(aligns, 'r')
		case left:
			aligns = append(aligns, 'l')
		default:
			aligns = append(aligns, 0)
		}
	}
	return aligns, len(aligns) > 0
}

// newTable starts a table from its header line and its alignments.
func newTable(st *state, cfg *config, header string, aligns []byte) *tableLeaf {
	t := &tableLeaf{aligns: aligns, nat: make([]int, len(aligns)), word: make([]int, len(aligns))}
	t.head = t.cells(st, cfg, header, bTableHead)
	return t
}

// cells reads a row: the text of each cell is inline Markdown. A row with
// too few cells gets empty ones, and cells past the last column are left
// out, as GitHub Flavored Markdown has it.
func (t *tableLeaf) cells(st *state, cfg *config, line string, base uint8) []cell {
	texts := splitRow(line)
	row := make([]cell, len(t.aligns))
	for i := range row {
		if i >= len(texts) {
			continue
		}
		nodes, _, _ := scanInline(cfg, st.refs, texts[i], '\n', false, base, true)
		c := &row[i]
		for k := range nodes {
			if nd := &nodes[k]; nd.brk {
				c.buf.addBreak()
			} else {
				c.buf.add(nd.text, nd.st)
			}
		}
		for _, part := range strings.Split(string(c.buf.txt), "\n") {
			c.w = max(c.w, cfg.cells(part))
		}
		if !t.frozen {
			t.nat[i] = max(t.nat[i], c.w)
			for _, w := range strings.Fields(string(c.buf.txt)) {
				t.word[i] = max(t.word[i], cfg.cells(w))
			}
		}
	}
	return row
}

// row takes a complete row of the table.
func (t *tableLeaf) row(st *state, cfg *config, line string) {
	r := t.cells(st, cfg, line, bText)
	if t.frozen {
		lines, mark := t.draw(cfg, st.room(cfg), [][]cell{r})
		t.mark = mark
		for _, l := range lines {
			st.emit(cfg, l)
		}
		return
	}
	t.rows = append(t.rows, r)
	if lines, mark := t.draw(cfg, st.room(cfg), t.rows); len(lines) > cfg.tableLive {
		// Too tall to stay live: what there is becomes final.
		t.frozen, t.mark, t.rows = true, mark, nil
		for _, l := range lines {
			st.emit(cfg, l)
		}
	}
}

// finish ends the table and writes what is not written yet.
func (t *tableLeaf) finish(st *state, cfg *config) {
	lines, mark := t.draw(cfg, st.room(cfg), t.rows)
	if !mark.any && !t.frozen {
		// A table with no rows: its header alone, or in the stacked layout
		// a single record with nothing in it.
		if _, stacked := t.layout(cfg, st.room(cfg)); stacked {
			lines, mark = t.draw(cfg, st.room(cfg), [][]cell{make([]cell, len(t.aligns))})
		}
	}
	t.mark, t.rows = mark, nil
	for _, l := range lines {
		st.emit(cfg, l)
	}
}

// layout shares room out among the columns. When all of them can have the
// width of their widest cell, they do. Otherwise each gets what it cannot
// do without (its longest word, so that words are not broken, but at
// least tableMinCol cells and at most tableWord for the sake of a word),
// and what room is left goes to the columns that want more, the same to
// each until it has all it wants. stacked is set when there is not even
// room for what the columns cannot do without: the table cannot be read
// as a grid at this width.
func (t *tableLeaf) layout(cfg *config, room int) (widths []int, stacked bool) {
	n := len(t.nat)
	widths = make([]int, n)
	avail := room - tableGap*(n-1)
	total, least := 0, 0
	want := make([]int, n) // what each column would take beyond the least
	for i := range widths {
		full := max(t.nat[i], 1)
		widths[i] = min(full, max(min(t.word[i], tableWord), tableMinCol))
		want[i] = full - widths[i]
		total += full
		least += widths[i]
	}
	if total <= avail {
		for i := range widths {
			widths[i] += want[i]
		}
		return widths, false
	}
	if least > avail {
		return widths, true
	}
	// Hand out what is left a round at a time: a cell to every column that
	// still wants one, in order, would do, but whole shares are quicker.
	for left := avail - least; left > 0; {
		wanting := 0
		for i := range want {
			if want[i] > 0 {
				wanting++
			}
		}
		if wanting == 0 {
			break
		}
		share := max(left/wanting, 1)
		for i := range want {
			if give := min(want[i], share, left); give > 0 {
				widths[i] += give
				want[i] -= give
				left -= give
			}
		}
	}
	return widths, false
}

// draw returns the lines of rows, with the header before them if it is not
// written yet, and how far the writing has then got. It changes nothing.
func (t *tableLeaf) draw(cfg *config, room int, rows [][]cell) ([]string, tableMark) {
	widths, stacked := t.layout(cfg, room)
	mark := t.mark
	var out []string
	if stacked {
		mark.head = true
		for _, r := range rows {
			if mark.any {
				glyph := cfg.theme.RuleChar
				out = append(out, cfg.theme.TableRule.Render(strings.Repeat(glyph, max(min(room, tableBar)/max(cfg.cells(glyph), 1), 1))))
			}
			out = append(out, t.record(cfg, room, r)...)
			mark.any = true
		}
		return out, mark
	}
	rule := t.rule(cfg, widths)
	if !mark.head {
		out = append(out, t.grid(cfg, widths, t.head)...)
		out = append(out, rule)
		mark.head = true
	}
	for _, r := range rows {
		lines := t.grid(cfg, widths, r)
		multi := len(lines) > 1
		if mark.any && (mark.multi || multi) {
			// A row of several lines is fenced off from its neighbours.
			out = append(out, rule)
		}
		out = append(out, lines...)
		mark.any, mark.multi = true, multi
	}
	return out, mark
}

// rule returns the line drawn under the header: a run of the rule
// character under each column.
func (t *tableLeaf) rule(cfg *config, widths []int) string {
	glyph := cfg.theme.RuleChar
	gw := max(cfg.cells(glyph), 1)
	parts := make([]string, len(widths))
	for i, w := range widths {
		parts[i] = cfg.theme.TableRule.Render(strings.Repeat(glyph, w/gw)) + spaces(w-w/gw*gw)
	}
	return trimRight(strings.Join(parts, spaces(tableGap)))
}

// grid returns one row of the table as lines: each cell wrapped to its
// column's width and aligned in it.
func (t *tableLeaf) grid(cfg *config, widths []int, row []cell) []string {
	cols := make([][]string, len(widths))
	height := 1
	for i := range widths {
		cols[i] = cfg.lines(&row[i].buf, widths[i], 0)
		height = max(height, len(cols[i]))
	}
	out := make([]string, height)
	for y := range out {
		var b strings.Builder
		for i, w := range widths {
			if i > 0 {
				b.WriteString(spaces(tableGap))
			}
			s := ""
			if y < len(cols[i]) {
				s = cols[i][y]
			}
			pad := max(w-cfg.m.VisibleWidth(s), 0)
			left := 0
			switch t.aligns[i] {
			case 'r':
				left = pad
			case 'c':
				left = pad / 2
			}
			b.WriteString(spaces(left))
			b.WriteString(s)
			b.WriteString(spaces(pad - left))
		}
		out[y] = trimRight(b.String())
	}
	return out
}

// record returns one row of the table in the stacked layout: a line (or
// several) for each cell, which starts with the column's header.
func (t *tableLeaf) record(cfg *config, room int, row []cell) []string {
	var out []string
	for i := range row {
		var b lineBuf
		b.txt = append(b.txt, t.head[i].buf.txt...)
		b.spans = append(b.spans, t.head[i].buf.spans...)
		if len(b.txt) > 0 {
			b.add(": ", style{base: bTableHead})
		}
		at := len(b.txt)
		b.txt = append(b.txt, row[i].buf.txt...)
		for _, sp := range row[i].buf.spans {
			b.spans = append(b.spans, span{sp.end + at, sp.st})
		}
		if len(b.txt) == 0 {
			continue
		}
		out = append(out, cfg.lines(&b, room, 2)...)
	}
	if len(out) == 0 {
		out = append(out, "")
	}
	return out
}
