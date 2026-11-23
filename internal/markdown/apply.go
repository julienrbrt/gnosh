package markdown

import (
	"strconv"
	"strings"
)

// apply does what a plan says with a line. When the line's text goes to a
// leaf block that takes text as it arrives, st.taken is set, and the rest
// of the line follows through leafText.
func (st *state) apply(cfg *config, p *plan, line string) {
	lf := &st.leaf
	switch p.act {
	case aText:
		if p.cont {
			lf.para.next(st, cfg)
			lf.para.text(st, cfg, p.text)
			st.taken, st.prevBlank = true, false
			return
		}
	case aCode:
		lf.code.begin(st, cfg)
		lf.code.text(st, cfg, p.text)
		st.taken, st.prevBlank = true, false
		return
	case aCodeBlank:
		lf.code.blanks++
		st.prevBlank = true
		return
	case aFenceClose:
		st.closeLeaf(cfg)
		st.prevBlank = false
		return
	case aRow:
		lf.table.row(st, cfg, p.text)
		st.prevBlank = false
		return
	case aSetext:
		// The paragraph, which is one line that was kept back, is a heading.
		text := string(lf.para.held)
		if st.trace != nil && len(*st.trace) > 0 {
			(*st.trace)[len(*st.trace)-1] = "<h" + strconv.Itoa(p.level) + ">"
		}
		*lf = leaf{kind: lHead}
		lf.head.start(p.level)
		lf.head.setext = true
		lf.head.text(st, cfg, text)
		lf.head.finish(st, cfg)
		*lf = leaf{}
		st.prevBlank = false
		return
	case aTable:
		// The line kept back is the header of a table. The lines of the
		// paragraph before it, if there are any, are a paragraph of their
		// own, and the table is a new block after it.
		header := string(lf.para.held)
		if lf.para.lines > 0 {
			lf.para.holding = false
			lf.para.finish(st, cfg)
			st.planGap(false, false)
			st.opened(lastBlock)
			st.note("<table>")
		} else if st.trace != nil && len(*st.trace) > 0 {
			(*st.trace)[len(*st.trace)-1] = "<table>"
		}
		*lf = leaf{kind: lTable, table: newTable(st, cfg, header, p.aligns)}
		st.prevBlank = false
		return
	}

	// Everything else ends what was open below the containers that stay.
	st.closeTo(cfg, p.keep)
	if p.act == aBlank && len(p.opens) == 0 {
		st.prevBlank = true
		return
	}
	item := len(p.opens) > 0 && p.opens[0].item
	st.planGap(item, p.act == aDef && len(p.opens) == 0)
	for i := range p.opens {
		st.open(cfg, &p.opens[i])
	}
	st.prevBlank = false
	switch p.act {
	case aBlank:
		// The containers the line opened are all there is to it.
	case aText:
		st.opened(lastBlock)
		st.note("<p>")
		*lf = leaf{kind: lPara}
		if st.quoted() {
			lf.para.fl.base = bQuote
		}
		lf.para.next(st, cfg)
		lf.para.text(st, cfg, p.text)
		st.taken = true
	case aHeading:
		st.opened(lastBlock)
		st.note("<h" + strconv.Itoa(p.level) + ">")
		*lf = leaf{kind: lHead}
		lf.head.start(p.level)
		lf.head.text(st, cfg, p.text)
		st.taken = true
	case aThematic:
		st.opened(lastBlock)
		st.note("<hr>")
		glyph := cfg.theme.RuleChar
		n := min(st.room(cfg), ruleMax) / max(cfg.cells(glyph), 1)
		st.emit(cfg, cfg.theme.Rule.Render(strings.Repeat(glyph, max(n, 1))))
	case aFenceOpen:
		st.opened(lastBlock)
		st.note("<code>")
		lang, _, _ := strings.Cut(p.info, " ")
		lang, _, _ = strings.Cut(lang, "\t")
		*lf = leaf{kind: lCode, code: codeLeaf{kind: cFenced, ch: p.ch, n: p.n, ind: p.ind, lang: strings.ToLower(unescape(lang))}}
		st.collect(unescape(lang))
	case aIndented:
		st.opened(lastBlock)
		st.note("<code>")
		*lf = leaf{kind: lCode, code: codeLeaf{kind: cIndented}}
		st.collect("")
		lf.code.begin(st, cfg)
		lf.code.text(st, cfg, p.text)
		st.taken = true
	case aHTML:
		st.opened(lastBlock)
		st.note("<html>")
		*lf = leaf{kind: lCode, code: codeLeaf{kind: cHTML, html: p.level}}
		lf.code.begin(st, cfg)
		lf.code.text(st, cfg, p.text)
		st.taken = true
	case aDef:
		st.opened(lastDef)
		st.note("<def>")
		st.define(p.label, linkRef{url: p.url, title: p.title})
		st.definition(cfg, p)
	}
}

// collect starts the record of a new code block, for CodeBlocks.
func (st *state) collect(lang string) {
	if st.code != nil {
		*st.code = append(*st.code, nil)
		*st.langs = append(*st.langs, lang)
	}
}

// keep adds to the record of the code block that is open, for CodeBlocks.
func (st *state) keep(s string) {
	if st.code != nil && len(*st.code) > 0 {
		last := &(*st.code)[len(*st.code)-1]
		*last = append(*last, s...)
	}
}

// open opens a container.
func (st *state) open(cfg *config, op *opening) {
	if !op.item {
		st.opened(lastBlock)
		st.note("<quote>")
		bar := cfg.theme.QuoteBar
		st.frames = append(st.frames, frame{
			kind:  fQuote,
			rest:  cfg.theme.QuoteMark.Render(bar) + " ",
			width: cfg.cells(bar) + 1,
		})
		return
	}
	lvl := st.tip()
	lvl.child = true
	if n := len(st.frames); n > 0 {
		st.frames[n-1].bare = false
	}
	st.note("<item>")
	depth := 0
	for i := range st.frames {
		if st.frames[i].kind == fItem {
			depth++
		}
	}
	t := &cfg.theme
	var plain, drawn string
	switch {
	case op.ordered:
		// The number is shown as it was written. Standard Markdown counts
		// up from the first item's number instead, but a reader who is
		// told "see step 7" must find a 7.
		plain = op.digits + string(op.delim)
		drawn = t.ListMark.Render(plain)
	case op.task == 0:
		plain = t.Bullets[depth%len(t.Bullets)]
		drawn = t.ListMark.Render(plain)
	}
	if op.task != 0 {
		// A task's box stands where the bullet would; after a number it
		// follows the number.
		box, boxStyle := "[ ]", t.TaskOpen
		if op.task == 2 {
			box, boxStyle = "[x]", t.TaskDone
		}
		if plain != "" {
			plain += " "
			drawn += " "
		}
		plain += box
		drawn += boxStyle.Render(box)
	}
	width := cfg.cells(plain) + 1
	st.frames = append(st.frames, frame{
		kind:   fItem,
		indent: op.indent,
		first:  drawn + " ",
		plain:  plain,
		rest:   spaces(width),
		width:  width,
		bare:   op.bare,
	})
}

// definition writes a link reference definition as the line it is: its
// label, its address and its title. In a terminal there is nowhere else
// for the address to be seen.
func (st *state) definition(cfg *config, p *plan) {
	var b lineBuf
	b.add("["+p.label+"]:", style{bits: sDef})
	b.add(" ", style{})
	link := style{bits: sLink}
	if isURI(p.url) {
		link.link = p.url
	}
	b.add(p.url, link)
	if p.title != "" {
		b.add(` "`+p.title+`"`, style{bits: sDef})
	}
	for _, line := range cfg.lines(&b, st.room(cfg), 4) {
		st.emit(cfg, line)
	}
}
