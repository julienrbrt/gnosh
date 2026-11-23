package editor

import (
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/gnosh-ai/gnosh/internal/term"
)

// This file is the box's vim keys: Claude Code's editorMode "vim", off until
// SetVim switches them on. INSERT mode has the box's own keys, but Esc goes
// to NORMAL mode. NORMAL and VISUAL modes take vim's motions, operators and
// text objects, as Claude Code's documentation lists them, with counts (3w,
// d2w, 2dd). Each change is one step of undo, an INSERT session with the
// command that began it included, and "." makes the last one again.

// VimMode is the mode of the box's vim keys.
type VimMode int

const (
	// VimOff: the box has its ordinary keys.
	VimOff VimMode = iota
	// VimInsert: typing goes into the box.
	VimInsert
	// VimNormal: keys are commands.
	VimNormal
	// VimVisual: a selection, by characters.
	VimVisual
	// VimVisualLine: a selection, by whole lines.
	VimVisualLine
)

// String names the mode as vim's status line does: "INSERT", "NORMAL",
// "VISUAL", "VISUAL LINE", or "" when the vim keys are off.
func (m VimMode) String() string {
	switch m {
	case VimInsert:
		return "INSERT"
	case VimNormal:
		return "NORMAL"
	case VimVisual:
		return "VISUAL"
	case VimVisualLine:
		return "VISUAL LINE"
	}
	return ""
}

// visual reports whether the mode is one of the VISUAL ones.
func (m VimMode) visual() bool { return m == VimVisual || m == VimVisualLine }

// vimRemapWait is how soon the second key of an INSERT-mode pair (jj) must
// follow the first to stand for Esc.
const vimRemapWait = time.Second

// vimIndent is what >> puts in front of a line, and the most << takes away.
const vimIndent = "  "

// vimState is where the vim keys stand.
type vimState struct {
	mode VimMode
	// keys are the keys typed so far of a NORMAL or VISUAL command that is
	// not whole yet: "2d", "f", "di".
	keys string
	// find is the last f, F, t or T and its character, for ; and ,.
	find     rune
	findChar string
	// reg is the register: what d, c, y, x and the rest took last, as
	// the editor keeps text, and whether it was taken by whole lines.
	reg      string
	regLines bool
	// anchor is the end of a VISUAL selection the cursor is not at.
	anchor pos
	// last are the keys of the last change, for "."; rec gathers the keys
	// of the change being made, while recording is set.
	last, rec []term.Key
	recording bool
	replaying bool
	// What the box was when the change being made began, so that the
	// change becomes one step of undo when it ends (group).
	grouping  bool
	undoLen   int
	before    string
	beforeCur pos
	// remaps are the INSERT-mode pairs that stand for Esc ("jj"); pend is
	// the first character of one, just typed, with when and where.
	remaps  []string
	pend    string
	pendAt  time.Time
	pendPos pos
}

// SetVim switches the vim keys on, in INSERT mode, or off.
func (e *Editor) SetVim(on bool) {
	if e.plain {
		return
	}
	switch {
	case on && e.vim.mode == VimOff:
		e.vim.mode = VimInsert
		e.vimGroupBegin()
	case !on:
		e.vimGroupEnd()
		e.vim = vimState{remaps: e.vim.remaps}
	}
}

// Vim returns the mode of the vim keys, VimOff when they are off.
func (e *Editor) Vim() VimMode { return e.vim.mode }

// SetVimRemaps sets the INSERT-mode pairs that stand for Esc: each two
// characters typed one after the other within a second (Claude Code's
// vimInsertModeRemaps). A pair that is not two printable characters is
// left out.
func (e *Editor) SetVimRemaps(pairs []string) {
	e.vim.remaps = nil
	for _, p := range pairs {
		if utf8.RuneCountInString(p) == 2 && Visible(p) == p && strings.TrimSpace(p) == p {
			e.vim.remaps = append(e.vim.remaps, p)
		}
	}
}

// vimKey handles a key while the vim keys are on. done is false when the
// key keeps the meaning the box gives it (Enter, the arrows, Ctrl keys, and
// everything in INSERT mode but Esc).
func (e *Editor) vimKey(k term.Key, cmd command, armed press) (Result, bool) {
	var res Result
	var done bool
	if e.vim.mode == VimInsert {
		res, done = e.vimInsertKey(k, cmd, armed)
	} else {
		res, done = e.vimNormalKey(k, cmd, armed)
	}
	e.vimSnap()
	return res, done
}

// vimSnap puts the cursor on the start of the item it is in. The vim keys
// work out places by bytes in a few spots (a quote's inside, a line's
// indent), and a character that joins what stands before it (a combining
// accent after a quote or a space) can make such a place the middle of
// an item.
func (e *Editor) vimSnap() {
	e.fix()
	s, home := e.lines[e.cur.line].s, e.home(e.cur.line)
	if e.cur.col > home && e.cur.col < len(s) {
		e.cur.col = e.itemBefore(s, e.cur.col, home)
	}
}

// vimInsertKey handles a key in INSERT mode.
func (e *Editor) vimInsertKey(k term.Key, cmd command, armed press) (Result, bool) {
	g := &e.vim
	if g.recording && !g.replaying {
		switch {
		case repeatable(cmd) || cmd == cmdEscape || isCtrlBracket(k):
			g.rec = append(g.rec, k)
		default:
			// A move (or a paste from the ring, a walk through the
			// history) ends what "." makes again: what is typed after it
			// is a change of its own, as an i, as vim has it.
			g.rec = keysOf("i")
		}
	}
	if cmd == cmdEscape || isCtrlBracket(k) {
		e.vimToNormal()
		return Result{Handled: true}, true
	}
	if cmd == cmdInsert && g.pend != "" {
		first := g.pend
		g.pend = ""
		soon := e.now == nil || e.now().Sub(g.pendAt) <= vimRemapWait
		for _, p := range g.remaps {
			if soon && e.cur == g.pendPos && strings.HasPrefix(p, first) && p[len(first):] == k.Text {
				// The first character goes again, and neither stays.
				e.edit(e.leftOf(e.cur), e.cur, "", kindOther)
				if g.recording && !g.replaying {
					g.rec = append(g.rec[:max(len(g.rec)-2, 0)], term.Key{Code: term.KeyEscape})
				}
				e.vimToNormal()
				return Result{Handled: true}, true
			}
		}
	}
	g.pend = ""
	res := e.run(k, cmd, armed)
	if cmd == cmdInsert && res.Action == ActionNone {
		for _, p := range g.remaps {
			if r, _ := utf8.DecodeRuneInString(p); string(r) == k.Text {
				g.pend, g.pendPos = k.Text, e.cur
				if e.now != nil {
					g.pendAt = e.now()
				}
				break
			}
		}
	}
	e.vimAfter(&res)
	return res, true
}

// repeatable reports whether a key of INSERT mode is one "." types again:
// text, and the keys that take text away.
func repeatable(cmd command) bool {
	switch cmd {
	case cmdInsert, cmdNewline, cmdBackspace, cmdDelete, cmdKillWordLeft, cmdKillSpaceLeft, cmdKillWordRight, cmdKillLineEnd, cmdKillLineStart:
		return true
	}
	return false
}

// isCtrlBracket reports whether k is Ctrl+[, which a terminal under the
// kitty keyboard protocol reports as itself and every other sends as Esc.
func isCtrlBracket(k term.Key) bool {
	return k.Code == term.KeyRune && k.Rune == '[' && k.Mod&^(term.ModCapsLock|term.ModNumLock) == term.ModCtrl
}

// vimAfter is what every key does to the vim keys afterwards: a box that
// was sent or cleared starts again in INSERT mode.
func (e *Editor) vimAfter(res *Result) {
	if res.Action == ActionSubmit || res.Cleared {
		e.vimGroupEnd()
		e.vim.mode, e.vim.keys, e.vim.pend = VimInsert, "", ""
		e.vim.recording = false
		e.vimGroupBegin()
	}
}

// vimToNormal leaves INSERT mode: the change that began it ends, and the
// cursor steps back onto the character it was after, as vim's does.
func (e *Editor) vimToNormal() {
	g := &e.vim
	g.pend = ""
	if g.recording && !g.replaying {
		g.last = g.rec
	}
	g.recording, g.rec = false, nil
	g.mode = VimNormal
	if e.cur.col > e.home(e.cur.line) {
		e.cur = e.leftOf(e.cur)
	}
	e.vimGroupEnd()
	e.vimClamp()
}

// vimNormalKey handles a key in NORMAL or VISUAL mode.
func (e *Editor) vimNormalKey(k term.Key, cmd command, armed press) (Result, bool) {
	g := &e.vim
	handled := Result{Handled: true}
	if cmd != cmdInsert {
		switch {
		case cmd == cmdEscape || isCtrlBracket(k):
			switch {
			case g.keys != "":
				g.keys = ""
				return handled, true
			case g.mode.visual():
				g.mode = VimNormal
				e.vimClamp()
				return handled, true
			}
			if isCtrlBracket(k) {
				return handled, true
			}
		case cmd == cmdBackspace:
			// As h.
			k, cmd = term.Key{Code: term.KeyRune, Rune: 'h', Text: "h"}, cmdInsert
		}
		if cmd != cmdInsert {
			g.keys = ""
			res := e.run(k, cmd, armed)
			if !g.mode.visual() || res.Changed {
				e.vimLeaveVisual()
			}
			e.vimClamp()
			e.vimAfter(&res)
			return res, true
		}
	}
	g.keys += k.Text
	c, more, bad := parseVim(g.keys, g.mode.visual())
	if more {
		return handled, true
	}
	keys := g.keys
	g.keys = ""
	if bad {
		return handled, true
	}
	res := e.vimDo(c, keys)
	if g.mode != VimInsert {
		e.vimClamp()
	}
	e.vimAfter(&res)
	return res, true
}

// vimLeaveVisual ends a VISUAL selection, when there is one.
func (e *Editor) vimLeaveVisual() {
	if e.vim.mode.visual() {
		e.vim.mode = VimNormal
	}
}

// vimClamp keeps the cursor on a character in NORMAL and VISUAL mode: at
// the end of a line that has any, it stands on the last one.
func (e *Editor) vimClamp() {
	if e.vim.mode == VimInsert || e.vim.mode == VimOff {
		return
	}
	e.fix()
	s, home := e.lines[e.cur.line].s, e.home(e.cur.line)
	if e.cur.col >= len(s) && len(s) > home {
		e.cur.col = e.itemStart(s, len(s), home)
	}
}

// vimGroupBegin marks the start of a change, which becomes one step of undo
// when vimGroupEnd is called.
func (e *Editor) vimGroupBegin() {
	g := &e.vim
	if g.grouping {
		return
	}
	g.grouping, g.undoLen, g.before, g.beforeCur = true, len(e.undo), e.whole(), e.cur
}

// vimGroupEnd makes everything changed since vimGroupBegin one step of undo:
// the part of the text that differs, replaced as a whole.
func (e *Editor) vimGroupEnd() {
	g := &e.vim
	if !g.grouping {
		return
	}
	g.grouping = false
	n := g.undoLen
	if n < 0 || n > len(e.undo) || len(e.undo)-n < 2 {
		// Nothing changed, or one step already; or the steps were cut
		// into (an undo in between, the oldest dropped at the limit, the
		// box emptied), and they are left as they are.
		return
	}
	before, after := g.before, e.whole()
	p := 0
	for p < len(before) && p < len(after) && before[p] == after[p] {
		p++
	}
	for p > 0 && (p < len(before) && !utf8.RuneStart(before[p]) || p < len(after) && !utf8.RuneStart(after[p])) {
		p--
	}
	s := 0
	for s < len(before)-p && s < len(after)-p && before[len(before)-1-s] == after[len(after)-1-s] {
		s++
	}
	for s > 0 && !utf8.RuneStart(before[len(before)-s]) {
		s--
	}
	wb := e.undo[n].wb
	e.undo = append(e.undo[:n], change{
		at: advance(pos{}, before[:p]), del: before[p : len(before)-s], ins: after[p : len(after)-s],
		cb: g.beforeCur, ca: e.cur, wb: wb, wa: e.walk, kind: kindOther,
	})
	e.redo = nil
}

// vimCmd is a NORMAL or VISUAL command as it was typed.
type vimCmd struct {
	count  int    // the count before it, 0 when none was typed
	op     rune   // the operator: d c y > <, or 0
	count2 int    // the count after the operator
	verb   string // the motion, text object or command: "w", "iw", "gg", "dd", "x"
	arg    string // the character after f F t T and r
}

// times is how many times the command is to be done: its counts
// multiplied, 1 when none was typed.
func (c vimCmd) times() int {
	return min(max(c.count, 1)*max(c.count2, 1), 9999)
}

// The keys of the parts of a command.
const (
	vimOps      = "dcy<>"
	vimMotions  = "hjkl wbeWBE0^$G%;,"
	vimFinds    = "fFtT"
	vimObjects  = "wW\"'`()b[]{}B<>"
	vimCommands = "xXDCsSYpPJu.iIaAoOvV~/"
	vimVisuals  = "dxycspPJouUvV~<>"
	// vimVisualOps are the VISUAL commands that act on the selection.
	vimVisualOps = "dxycspPruU~<>J"
)

// parseVim reads keys as a command. more says it is not whole yet; bad that
// it is no command.
func parseVim(keys string, visual bool) (c vimCmd, more, bad bool) {
	rs := []rune(keys)
	i := 0
	count := func() int {
		n, from := 0, i
		for i < len(rs) && rs[i] >= '0' && rs[i] <= '9' && !(i == from && rs[i] == '0') {
			n = min(n*10+int(rs[i]-'0'), 9999)
			i++
		}
		return n
	}
	next := func() (rune, bool) {
		if i >= len(rs) {
			return 0, false
		}
		i++
		return rs[i-1], true
	}
	// whole is the end of a command: nothing may follow it.
	whole := func(c vimCmd) (vimCmd, bool, bool) {
		return c, false, i != len(rs)
	}
	c.count = count()
	r, ok := next()
	if !ok {
		return c, true, false
	}
	if !visual && strings.ContainsRune(vimOps, r) {
		c.op = r
		c.count2 = count()
		if r, ok = next(); !ok {
			return c, true, false
		}
		if r == c.op {
			c.verb = string([]rune{r, r})
			return whole(c)
		}
	}
	switch {
	case r == 'r' && c.op == 0:
		arg, ok := next()
		if !ok {
			return c, true, false
		}
		c.verb, c.arg = "r", string(arg)
		return whole(c)
	case (r == 'i' || r == 'a') && (c.op != 0 || visual):
		o, ok := next()
		if !ok {
			return c, true, false
		}
		if !strings.ContainsRune(vimObjects, o) {
			return c, false, true
		}
		c.verb = string([]rune{r, o})
		return whole(c)
	case strings.ContainsRune(vimFinds, r):
		arg, ok := next()
		if !ok {
			return c, true, false
		}
		c.verb, c.arg = string(r), string(arg)
		return whole(c)
	case r == 'g':
		r2, ok := next()
		if !ok {
			return c, true, false
		}
		if r2 != 'g' {
			return c, false, true
		}
		c.verb = "gg"
		return whole(c)
	case strings.ContainsRune(vimMotions, r):
		c.verb = string(r)
		return whole(c)
	case c.op != 0:
		return c, false, true
	case visual && strings.ContainsRune(vimVisuals, r):
		c.verb = string(r)
		return whole(c)
	case !visual && strings.ContainsRune(vimCommands, r):
		c.verb = string(r)
		return whole(c)
	}
	return c, false, true
}

// motionKind says how a motion's range is taken: up to the place it reaches
// (exclusive), through the character there (inclusive), or by whole lines.
type motionKind uint8

const (
	exclusive motionKind = iota
	inclusive
	linewise
)

// vimDo carries out a whole command. keys are the keys it was typed with,
// kept for "." when it changes the text.
func (e *Editor) vimDo(c vimCmd, keys string) Result {
	g := &e.vim
	handled := Result{Handled: true}
	n := c.times()
	if g.mode.visual() {
		return e.vimVisual(c, n)
	}
	change := c.op == 'd' || c.op == 'c' || c.op == '<' || c.op == '>' || len(c.verb) == 1 && strings.Contains("xXDCsSpPJr~iIaAoO", c.verb)
	if change && !g.replaying {
		g.last = keysOf(keys)
		g.rec = append([]term.Key(nil), g.last...)
		g.recording = true
	}
	if change {
		e.vimGroupBegin()
	}
	defer func() {
		if g.mode != VimInsert {
			if change && !g.replaying {
				g.recording, g.rec = false, nil
			}
			e.vimGroupEnd()
		}
	}()
	if c.op != 0 {
		e.vimOperate(c, n)
		return handled
	}
	line := e.lines[e.cur.line].s
	home := e.home(e.cur.line)
	switch c.verb {
	case "i":
		g.mode = VimInsert
	case "a":
		if e.cur.col < len(line) {
			e.cur.col = e.itemEnd(line, e.cur.col)
		}
		g.mode = VimInsert
	case "I":
		e.cur.col = e.firstNonBlank(e.cur.line)
		g.mode = VimInsert
	case "A":
		e.cur.col = len(line)
		g.mode = VimInsert
	case "o":
		e.edit(pos{e.cur.line, len(line)}, pos{e.cur.line, len(line)}, "\n", kindOther)
		g.mode = VimInsert
	case "O":
		at := pos{e.cur.line, home}
		e.edit(at, at, "\n", kindOther)
		e.cur = at
		g.mode = VimInsert
	case "v":
		g.mode, g.anchor = VimVisual, e.cur
	case "V":
		g.mode, g.anchor = VimVisualLine, e.cur
	case "/":
		if !e.plain {
			e.openSearch()
		}
	case "u":
		for range n {
			if !e.undoOne() {
				break
			}
		}
	case ".":
		e.vimRepeat(n)
	case "x", "X":
		from, to := e.cur, e.cur
		for range n {
			if c.verb == "x" {
				if to.col >= len(line) {
					break
				}
				to.col = e.itemEnd(line, to.col)
			} else {
				if from.col <= home {
					break
				}
				from.col = e.itemStart(line, from.col, home)
			}
		}
		e.vimTake(from, to, false, true)
		e.cur = from
	case "D":
		e.vimOperate(vimCmd{op: 'd', verb: "$"}, n)
	case "C", "s", "S":
		// These go to INSERT mode even where there is nothing to take
		// (an empty line).
		e.vimOperate(vimCmd{op: 'c', verb: map[string]string{"C": "$", "s": "l", "S": "cc"}[c.verb]}, n)
		g.mode = VimInsert
	case "Y":
		e.vimOperate(vimCmd{op: 'y', verb: "yy"}, n)
	case "p", "P":
		e.vimPut(c.verb == "p", n)
	case "J":
		e.vimJoin(e.cur.line, e.cur.line+max(n, 2)-1)
	case "r":
		to := e.cur
		for range n {
			if to.col >= len(line) {
				return handled // fewer characters than the count: nothing
			}
			to.col = e.itemEnd(line, to.col)
		}
		e.edit(e.cur, to, strings.Repeat(c.arg, n), kindOther)
		e.cur = e.leftOf(e.cur)
	case "~":
		to := e.cur
		for range n {
			if to.col >= len(line) {
				break
			}
			to.col = e.itemEnd(line, to.col)
		}
		e.edit(e.cur, to, toggleCase(e.between(e.cur, to)), kindOther)
	case "j", "k":
		dir := cmdDown
		if c.verb == "k" {
			dir = cmdUp
		}
		for range n {
			if res := e.run(term.Key{}, dir, press{}); res.Action != ActionNone {
				return res
			}
		}
	default:
		if to, _, ok := e.vimMotion(c, n, 0); ok {
			e.cur = to
		}
	}
	return handled
}

// keysOf turns the text of a command into the keys that type it.
func keysOf(s string) []term.Key {
	var out []term.Key
	for _, r := range s {
		out = append(out, term.Key{Code: term.KeyRune, Rune: r, Text: string(r)})
	}
	return out
}

// vimRepeat makes the last change again, n times: ".".
func (e *Editor) vimRepeat(n int) {
	g := &e.vim
	if len(g.last) == 0 || g.replaying {
		return
	}
	g.replaying = true
	defer func() { g.replaying = false }()
	keys := append([]term.Key(nil), g.last...)
	changed := e.changed
	defer func() { e.changed = e.changed || changed }()
	for range n {
		for _, k := range keys {
			// Each key as an event of its own, as far as what one event
			// tells the next (a deletion that joins the one before it).
			e.begin()
			e.vimKey(k, commandFor(k), press{})
			changed = changed || e.changed
			e.lastKind = e.thisKind
			e.fix()
		}
		if g.mode == VimInsert {
			// A change that was not ended when it was kept ends here.
			e.vimToNormal()
		}
	}
}

// vimMotion works out where a motion goes from the cursor, n times over,
// and how its range is taken. op is the operator it is for, if any: some
// motions reach further under one (an l to the end of a line, a w to the
// end of a word).
func (e *Editor) vimMotion(c vimCmd, n int, op rune) (to pos, kind motionKind, ok bool) {
	p := e.cur
	line := e.lines[p.line].s
	home := e.home(p.line)
	switch c.verb {
	case "h":
		for range n {
			if p.col <= home {
				break
			}
			p.col = e.itemStart(line, p.col, home)
		}
		return p, exclusive, p != e.cur
	case "l", " ":
		for range n {
			if p.col >= len(line) {
				break
			}
			next := e.itemEnd(line, p.col)
			if next >= len(line) && op == 0 {
				break // the cursor stays on the last character
			}
			p.col = next
		}
		return p, exclusive, p != e.cur
	case "j", "k":
		dir := 1
		if c.verb == "k" {
			dir = -1
		}
		l := min(max(p.line+dir*n, 0), len(e.lines)-1)
		if l == p.line {
			return p, linewise, false
		}
		return pos{l, e.home(l)}, linewise, true
	case "w", "W":
		big := c.verb == "W"
		for i := range n {
			q := e.vimWordFwd(p, big)
			if op != 0 && i == n-1 && q.line != p.line {
				// The last word of a line: an operator stops at the line's
				// end, not at the next line's first word.
				q = pos{p.line, len(e.lines[p.line].s)}
			}
			p = q
		}
		if op == 'c' && e.vimClassAt(e.cur, big) != 0 {
			// cw changes to the end of the word, as ce does.
			p = e.cur
			for i := range n {
				p = e.vimWordEnd(p, big, i == 0)
			}
			return p, inclusive, true
		}
		return p, exclusive, p != e.cur
	case "e", "E":
		for range n {
			p = e.vimWordEnd(p, c.verb == "E", false)
		}
		return p, inclusive, p != e.cur
	case "b", "B":
		for range n {
			p = e.vimWordBack(p, c.verb == "B")
		}
		return p, exclusive, p != e.cur
	case "0":
		return pos{p.line, home}, exclusive, true
	case "^":
		return pos{p.line, e.firstNonBlank(p.line)}, exclusive, true
	case "$":
		l := min(p.line+n-1, len(e.lines)-1)
		end := pos{l, len(e.lines[l].s)}
		if op != 0 {
			return end, exclusive, true
		}
		return end, inclusive, true
	case "G", "gg":
		l := len(e.lines) - 1
		if c.verb == "gg" {
			l = 0
		}
		if c.count > 0 || c.count2 > 0 {
			l = min(n, len(e.lines)) - 1
		}
		return pos{l, e.firstNonBlank(l)}, linewise, true
	case "f", "F", "t", "T":
		e.vim.find, e.vim.findChar = []rune(c.verb)[0], c.arg
		q, found := e.vimFind(p, []rune(c.verb)[0], c.arg, n, false)
		return q, findKind(c.verb), found
	case ";", ",":
		if e.vim.find == 0 {
			return p, exclusive, false
		}
		f := e.vim.find
		if c.verb == "," {
			f = reverseFind(f)
		}
		q, found := e.vimFind(p, f, e.vim.findChar, n, true)
		return q, findKind(string(f)), found
	case "%":
		q, found := e.vimMatch(p)
		return q, inclusive, found
	}
	return p, exclusive, false
}

// findKind is how the range of an f, F, t or T is taken.
func findKind(f string) motionKind {
	if f == "f" || f == "t" {
		return inclusive
	}
	return exclusive
}

// reverseFind is the find that goes the other way: , after f is F.
func reverseFind(f rune) rune {
	switch f {
	case 'f':
		return 'F'
	case 'F':
		return 'f'
	case 't':
		return 'T'
	}
	return 't'
}

// vimFind looks along the cursor's line for the n-th ch: f lands on it, t
// just before it, F and T the same going back. again is set for ; and ,,
// for which a t that already stands just before ch looks for the one after.
func (e *Editor) vimFind(p pos, f rune, ch string, n int, again bool) (pos, bool) {
	s, home := e.lines[p.line].s, e.home(p.line)
	at := p.col
	forward := f == 'f' || f == 't'
	if again && (f == 't' || f == 'T') {
		// Step past the character the last t stopped beside.
		switch {
		case forward && at < len(s):
			if next := e.itemEnd(s, at); next < len(s) && s[next:e.itemEnd(s, next)] == ch {
				at = next
			}
		case !forward && at > home:
			if prev := e.itemStart(s, at, home); s[prev:at] == ch {
				at = prev
			}
		}
	}
	for range n {
		found := false
		if forward {
			for q := at; q < len(s); {
				next := e.itemEnd(s, q)
				if q != at && s[q:next] == ch {
					at, found = q, true
					break
				}
				q = next
			}
		} else {
			for q := at; q > home; {
				prev := e.itemStart(s, q, home)
				if s[prev:e.itemEnd(s, prev)] == ch {
					at, found = prev, true
					break
				}
				q = prev
			}
		}
		if !found {
			return p, false
		}
	}
	switch f {
	case 't':
		at = e.itemStart(s, at, home)
	case 'T':
		at = e.itemEnd(s, at)
	}
	return pos{p.line, at}, true
}

// vimMatch finds the bracket that matches the one at the cursor, or the
// first one after it on its line: %.
func (e *Editor) vimMatch(p pos) (pos, bool) {
	s := e.lines[p.line].s
	q := p.col
	for q < len(s) && !strings.ContainsRune("()[]{}", rune(s[q])) {
		q = e.itemEnd(s, q)
	}
	if q >= len(s) {
		return p, false
	}
	open := strings.IndexByte("([{", s[q])
	if open >= 0 {
		return e.vimPair(pos{p.line, q}, "([{"[open], ")]}"[open], 1)
	}
	closeAt := strings.IndexByte(")]}", s[q])
	return e.vimPair(pos{p.line, q}, ")]}"[closeAt], "([{"[closeAt], -1)
}

// vimPair walks from the bracket at p, one way (dir 1 forward, -1 back),
// to the bracket other that matches it, counting the ones it passes.
func (e *Editor) vimPair(p pos, self, other byte, dir int) (pos, bool) {
	depth := 0
	for {
		s := e.lines[p.line].s
		if p.col < len(s) {
			switch s[p.col] {
			case self:
				depth++
			case other:
				depth--
				if depth == 0 {
					return p, true
				}
			}
		}
		var ok bool
		if dir > 0 {
			p, ok = e.vimNext(p)
		} else {
			p, ok = e.vimPrev(p)
		}
		if !ok {
			return p, false
		}
	}
}

// vimNext is the place one item after p, a line's end counted as one, and
// false at the end of the text.
func (e *Editor) vimNext(p pos) (pos, bool) {
	if s := e.lines[p.line].s; p.col < len(s) {
		return pos{p.line, e.itemEnd(s, p.col)}, true
	}
	if p.line+1 < len(e.lines) {
		return pos{p.line + 1, e.home(p.line + 1)}, true
	}
	return p, false
}

// vimPrev is the place one item before p, a line's end counted as one, and
// false at the start of the text.
func (e *Editor) vimPrev(p pos) (pos, bool) {
	if home := e.home(p.line); p.col > home {
		return pos{p.line, e.itemStart(e.lines[p.line].s, p.col, home)}, true
	}
	if p.line > 0 {
		return pos{p.line - 1, len(e.lines[p.line-1].s)}, true
	}
	return p, false
}

// vimClassAt is the kind of character at p, as vim's words are made: 0 for
// a blank (a line's end is one), 1 for a letter, a digit or _, 2 for any
// other character, 3 for a collapsed paste. For a WORD (big) everything
// that is not blank is 1.
func (e *Editor) vimClassAt(p pos, big bool) int {
	s := e.lines[p.line].s
	if p.col >= len(s) {
		return 0
	}
	item := s[p.col:e.itemEnd(s, p.col)]
	if strings.HasPrefix(item, pasteOpen) {
		if big {
			return 1
		}
		return 3
	}
	r, _ := utf8.DecodeRuneInString(item)
	switch {
	case unicode.IsSpace(r):
		return 0
	case big, r == '_', unicode.IsLetter(r), unicode.IsDigit(r):
		return 1
	}
	return 2
}

// inText is p moved into the text as it is now: onto a line there is, and
// onto the start of an item of it.
func (e *Editor) inText(p pos) pos {
	p.line = min(max(p.line, 0), len(e.lines)-1)
	s, home := e.lines[p.line].s, e.home(p.line)
	p.col = e.itemBefore(s, min(max(p.col, home), len(s)), home)
	return p
}

// emptyLine reports whether line l holds nothing the cursor can stand on.
func (e *Editor) emptyLine(l int) bool { return len(e.lines[l].s) <= e.home(l) }

// vimWordFwd is where the next word starts after p: w.
func (e *Editor) vimWordFwd(p pos, big bool) pos {
	from := p
	if c := e.vimClassAt(p, big); c != 0 {
		for {
			q, ok := e.vimNext(p)
			if !ok {
				return q
			}
			p = q
			if p.line != from.line || e.vimClassAt(p, big) != c {
				break
			}
		}
	}
	for e.vimClassAt(p, big) == 0 {
		if p.line != from.line && e.emptyLine(p.line) {
			return p // an empty line is a word
		}
		q, ok := e.vimNext(p)
		if !ok {
			return q
		}
		p = q
	}
	return p
}

// vimWordEnd is where the word after p ends (the last character of it): e.
// stay lets it end where it starts, on the last character of a word, as
// the first word of a cw does.
func (e *Editor) vimWordEnd(p pos, big, stay bool) pos {
	if !stay {
		q, ok := e.vimNext(p)
		if !ok {
			return p
		}
		p = q
	}
	for e.vimClassAt(p, big) == 0 {
		q, ok := e.vimNext(p)
		if !ok {
			return p
		}
		p = q
	}
	c := e.vimClassAt(p, big)
	for {
		q, ok := e.vimNext(p)
		if !ok || q.line != p.line || e.vimClassAt(q, big) != c {
			return p
		}
		p = q
	}
}

// vimWordBack is where the word before p starts: b.
func (e *Editor) vimWordBack(p pos, big bool) pos {
	q, ok := e.vimPrev(p)
	if !ok {
		return p
	}
	p = q
	for e.vimClassAt(p, big) == 0 {
		if e.emptyLine(p.line) {
			return pos{p.line, e.home(p.line)}
		}
		q, ok := e.vimPrev(p)
		if !ok {
			return p
		}
		p = q
	}
	c := e.vimClassAt(p, big)
	for {
		q, ok := e.vimPrev(p)
		if !ok || q.line != p.line || e.vimClassAt(q, big) != c {
			return p
		}
		p = q
	}
}

// firstNonBlank is the place of line l's first character that is not a
// blank, or its end when it has none.
func (e *Editor) firstNonBlank(l int) int {
	s := e.lines[l].s
	col := e.home(l)
	for col < len(s) {
		end := e.itemEnd(s, col)
		if it := s[col:end]; it != " " && it != "\t" {
			// (A space that a combining accent joins is no blank.)
			break
		}
		col = end
	}
	return col
}

// vimObject finds a text object around the cursor: from up to to.
func (e *Editor) vimObject(verb string) (from, to pos, ok bool) {
	around, kind := verb[0] == 'a', verb[1]
	switch kind {
	case 'w', 'W':
		return e.vimWordObject(around, kind == 'W')
	case '"', '\'', '`':
		return e.vimQuoteObject(around, kind)
	}
	pairs := map[byte][2]byte{'(': {'(', ')'}, ')': {'(', ')'}, 'b': {'(', ')'}, '[': {'[', ']'}, ']': {'[', ']'},
		'{': {'{', '}'}, '}': {'{', '}'}, 'B': {'{', '}'}, '<': {'<', '>'}, '>': {'<', '>'}}
	pr, known := pairs[kind]
	if !known {
		return from, to, false
	}
	// The open bracket around the cursor: the one it is on, or the first
	// before it that is not closed before the cursor.
	open, found := e.cur, false
	if s := e.lines[open.line].s; open.col < len(s) && s[open.col] == pr[1] {
		open, found = e.vimPair(open, pr[1], pr[0], -1)
	} else {
		depth := 0
		for p := e.cur; ; {
			s := e.lines[p.line].s
			if p.col < len(s) {
				switch {
				case s[p.col] == pr[0] && depth == 0:
					open, found = p, true
				case s[p.col] == pr[0]:
					depth--
				case s[p.col] == pr[1] && p != e.cur:
					depth++
				}
			}
			if found {
				break
			}
			var ok bool
			if p, ok = e.vimPrev(p); !ok {
				break
			}
		}
	}
	if !found {
		return from, to, false
	}
	closeAt, ok := e.vimPair(open, pr[0], pr[1], 1)
	if !ok {
		return from, to, false
	}
	if around {
		end, _ := e.vimNext(closeAt)
		return open, end, true
	}
	inner, _ := e.vimNext(open)
	return inner, closeAt, true
}

// vimWordObject is iw, aw, iW or aW: the word (or the blanks) the cursor is
// on, and with a the blanks after it, or before it when none follow.
func (e *Editor) vimWordObject(around, big bool) (from, to pos, ok bool) {
	l := e.cur.line
	s, home := e.lines[l].s, e.home(l)
	if e.cur.col >= len(s) {
		return e.cur, e.cur, false
	}
	c := e.vimClassAt(e.cur, big)
	from, to = e.cur, pos{l, e.itemEnd(s, e.cur.col)}
	for from.col > home {
		prev := pos{l, e.itemStart(s, from.col, home)}
		if e.vimClassAt(prev, big) != c {
			break
		}
		from = prev
	}
	for to.col < len(s) && e.vimClassAt(to, big) == c {
		to.col = e.itemEnd(s, to.col)
	}
	if !around {
		return from, to, true
	}
	if c == 0 {
		// Blanks and the word after them.
		if to.col < len(s) {
			c2 := e.vimClassAt(to, big)
			for to.col < len(s) && e.vimClassAt(to, big) == c2 {
				to.col = e.itemEnd(s, to.col)
			}
		}
		return from, to, true
	}
	if to.col < len(s) && e.vimClassAt(to, big) == 0 {
		for to.col < len(s) && e.vimClassAt(to, big) == 0 {
			to.col = e.itemEnd(s, to.col)
		}
		return from, to, true
	}
	for from.col > home {
		prev := pos{l, e.itemStart(s, from.col, home)}
		if e.vimClassAt(prev, big) != 0 {
			break
		}
		from = prev
	}
	return from, to, true
}

// vimQuoteObject is i" a" i' a' i` a`: the quoted text the cursor is in on
// its line, or the first after it.
func (e *Editor) vimQuoteObject(around bool, q byte) (from, to pos, ok bool) {
	l := e.cur.line
	s, home := e.lines[l].s, e.home(l)
	var at []int
	for i := home; i < len(s); i++ {
		if s[i] == '\\' {
			i++
			continue
		}
		if s[i] == q {
			at = append(at, i)
		}
	}
	for i := 0; i+1 < len(at); i += 2 {
		if e.cur.col <= at[i+1] {
			if around {
				return pos{l, at[i]}, pos{l, at[i+1] + 1}, true
			}
			return pos{l, at[i] + 1}, pos{l, at[i+1]}, true
		}
	}
	return e.cur, e.cur, false
}

// vimOperate carries out an operator (d c y > <) over a motion or a text
// object, or over whole lines (dd, cc, yy, >>, <<), n times over.
func (e *Editor) vimOperate(c vimCmd, n int) {
	g := &e.vim
	var from, to pos
	kind := exclusive
	switch {
	case len(c.verb) == 2 && c.verb[0] == c.verb[1] && strings.ContainsRune(vimOps, rune(c.verb[0])):
		kind = linewise
		from, to = e.cur, pos{min(e.cur.line+n-1, len(e.lines)-1), 0}
	case len(c.verb) == 2 && (c.verb[0] == 'i' || c.verb[0] == 'a'):
		var ok bool
		if from, to, ok = e.vimObject(c.verb); !ok {
			return
		}
	default:
		target, k, ok := e.vimMotion(c, n, c.op)
		if !ok {
			return
		}
		kind = k
		from, to = e.cur, target
		if to.before(from) {
			from, to = to, from
		}
		if kind == inclusive {
			to, _ = e.vimNext(to)
		}
	}
	if kind == linewise {
		e.vimLines(c.op, from.line, to.line)
		return
	}
	switch c.op {
	case 'd':
		e.vimTake(from, to, false, true)
		e.cur = from
	case 'c':
		e.vimTake(from, to, false, true)
		e.cur = from
		g.mode = VimInsert
	case 'y':
		e.vimTake(from, to, false, false)
		e.cur = from
	case '>', '<':
		e.vimLines(c.op, from.line, to.line)
	}
}

// vimTake puts the text from a up to b in the register and, when cut is
// set, takes it out of the box.
func (e *Editor) vimTake(a, b pos, lines, cut bool) {
	t := e.between(a, b)
	if t == "" && !lines {
		return
	}
	e.vim.reg, e.vim.regLines = t, lines
	if cut {
		e.edit(a, b, "", kindOther)
	}
}

// vimLines carries out an operator over the whole lines l1 to l2.
func (e *Editor) vimLines(op rune, l1, l2 int) {
	if l2 < l1 {
		l1, l2 = l2, l1
	}
	last := len(e.lines) - 1
	text := e.between(pos{l1, 0}, pos{l2, len(e.lines[l2].s)})
	switch op {
	case 'y':
		e.vim.reg, e.vim.regLines = text, true
		e.cur.line = l1
	case 'd':
		e.vim.reg, e.vim.regLines = text, true
		switch {
		case l2 < last:
			e.edit(pos{l1, 0}, pos{l2 + 1, 0}, "", kindOther)
		case l1 > 0:
			e.edit(pos{l1 - 1, len(e.lines[l1-1].s)}, pos{l2, len(e.lines[l2].s)}, "", kindOther)
			l1--
		default:
			e.edit(pos{}, e.end(), "", kindOther)
		}
		e.cur = pos{l1, e.firstNonBlank(l1)}
	case 'c':
		e.vim.reg, e.vim.regLines = text, true
		indent := e.lines[l1].s[e.home(l1):e.firstNonBlank(l1)]
		e.edit(pos{l1, e.home(l1)}, pos{l2, len(e.lines[l2].s)}, indent, kindOther)
		e.vim.mode = VimInsert
	case '>', '<':
		for l := l1; l <= l2; l++ {
			h := e.home(l)
			s := e.lines[l].s
			if op == '>' {
				if len(s) > h {
					e.edit(pos{l, h}, pos{l, h}, vimIndent, kindOther)
				}
				continue
			}
			cut := 0
			for cut < len(vimIndent) && h+cut < len(s) && s[h+cut] == ' ' {
				cut++
			}
			if cut == 0 && h < len(s) && s[h] == '\t' {
				cut = 1
			}
			if cut > 0 {
				e.edit(pos{l, h}, pos{l, h + cut}, "", kindOther)
			}
		}
		e.cur = pos{l1, e.firstNonBlank(l1)}
	}
}

// vimPutMax is about the most text one put adds: its count is cut so that
// a long register put 9999 times does not take the machine's memory.
const vimPutMax = 4 << 20

// vimPut puts the register in, n times: after the cursor (p) or before it
// (P), or below or above the line for whole lines. The count is cut to
// what vimPutMax allows, and is at least one (the TUI review of
// 2026-11-19: 9999p of a 1 MB line built 10 GB).
func (e *Editor) vimPut(after bool, n int) {
	g := &e.vim
	if g.reg == "" && !g.regLines {
		return
	}
	n = max(min(n, vimPutMax/(len(g.reg)+1)), 1)
	text := strings.Repeat(g.reg+"\n", n)
	text = text[:len(text)-1]
	if g.regLines {
		l := e.cur.line
		if after {
			at := pos{l, len(e.lines[l].s)}
			e.edit(at, at, "\n"+text, kindOther)
			e.cur = pos{l + 1, e.firstNonBlank(l + 1)}
			return
		}
		at := pos{l, 0}
		e.edit(at, at, text+"\n", kindOther)
		e.cur = pos{l, e.firstNonBlank(l)}
		return
	}
	text = strings.Repeat(g.reg, n)
	at := e.cur
	if s := e.lines[at.line].s; after && at.col < len(s) {
		at.col = e.itemEnd(s, at.col)
	}
	e.edit(at, at, text, kindOther)
	// The cursor stands on the last character put in.
	e.cur = e.leftOf(e.cur)
}

// vimJoin joins the lines l1 to l2 into one: each line break, with the
// blanks that begin the next line, becomes one space, or nothing before a
// closing parenthesis or after a line that ends in a blank: J.
func (e *Editor) vimJoin(l1, l2 int) {
	l2 = min(l2, len(e.lines)-1)
	for l := l1; l < l2; l++ {
		s := e.lines[l1].s
		next := e.lines[l1+1].s
		blanks := 0
		for blanks < len(next) && (next[blanks] == ' ' || next[blanks] == '\t') {
			blanks++
		}
		sep := " "
		if next[blanks:] == "" || strings.HasPrefix(next[blanks:], ")") || strings.HasSuffix(s, " ") || strings.HasSuffix(s, "\t") || s == "" {
			sep = ""
		}
		e.edit(pos{l1, len(s)}, pos{l1 + 1, blanks}, sep, kindOther)
		e.cur = pos{l1, len(s)}
	}
}

// toggleCase swaps the case of every letter of s: ~.
func toggleCase(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case unicode.IsUpper(r):
			return unicode.ToLower(r)
		case unicode.IsLower(r):
			return unicode.ToUpper(r)
		}
		return r
	}, s)
}

// vimSelection is the VISUAL selection: from up to to, or whole lines. The
// anchor is kept inside the text, which a paste or a history entry may have
// changed under it.
func (e *Editor) vimSelection() (from, to pos, lines bool) {
	e.vim.anchor = e.inText(e.vim.anchor)
	a, b := e.vim.anchor, e.cur
	if b.before(a) {
		a, b = b, a
	}
	if e.vim.mode == VimVisualLine {
		return pos{a.line, 0}, pos{b.line, len(e.lines[b.line].s)}, true
	}
	if s := e.lines[b.line].s; b.col < len(s) {
		b.col = e.itemEnd(s, b.col)
	} else if b.line+1 < len(e.lines) {
		b = pos{b.line + 1, 0}
	}
	return a, b, false
}

// vimVisual carries out a command in VISUAL mode: a motion moves the cursor
// and with it the selection's end, and an operator acts on the selection.
func (e *Editor) vimVisual(c vimCmd, n int) Result {
	g := &e.vim
	handled := Result{Handled: true}
	switch c.verb {
	case "v", "V":
		want := VimVisual
		if c.verb == "V" {
			want = VimVisualLine
		}
		if g.mode == want {
			g.mode = VimNormal
		} else {
			g.mode = want
		}
		return handled
	case "o":
		g.anchor, e.cur = e.cur, g.anchor
		return handled
	case "j", "k":
		dir := 1
		if c.verb == "k" {
			dir = -1
		}
		for range n {
			if !e.vertical(dir) {
				break
			}
		}
		return handled
	}
	if len(c.verb) == 2 && (c.verb[0] == 'i' || c.verb[0] == 'a') {
		if from, to, ok := e.vimObject(c.verb); ok && from.before(to) {
			g.anchor, e.cur = from, e.leftOf(to)
		}
		return handled
	}
	if len(c.verb) != 1 || !strings.Contains(vimVisualOps, c.verb) {
		if to, _, ok := e.vimMotion(c, n, 0); ok {
			e.cur = to
		}
		return handled
	}
	from, to, lines := e.vimSelection()
	e.vimGroupBegin()
	defer func() {
		if g.mode != VimInsert {
			e.vimGroupEnd()
		}
	}()
	g.mode = VimNormal
	switch c.verb {
	case "d", "x":
		if lines {
			e.vimLines('d', from.line, to.line)
			return handled
		}
		e.vimTake(from, to, false, true)
		e.cur = from
	case "y":
		if lines {
			e.vimLines('y', from.line, to.line)
			return handled
		}
		e.vimTake(from, to, false, false)
		e.cur = from
	case "c", "s":
		if lines {
			e.vimLines('c', from.line, to.line)
			return handled
		}
		e.vimTake(from, to, false, true)
		e.cur = from
		g.mode = VimInsert
	case "p", "P":
		// p keeps what it replaced, as vim's does; P keeps the register.
		old := e.between(from, to)
		e.edit(from, to, g.reg, kindOther)
		if c.verb == "p" {
			g.reg, g.regLines = old, lines
		}
		e.cur = from
	case "r":
		var b strings.Builder
		t := e.between(from, to)
		for _, r := range t {
			if r == '\n' {
				b.WriteRune(r)
			} else {
				b.WriteString(c.arg)
			}
		}
		e.edit(from, to, b.String(), kindOther)
		e.cur = from
	case "~", "u", "U":
		t := e.between(from, to)
		switch c.verb {
		case "~":
			t = toggleCase(t)
		case "u":
			t = strings.ToLower(t)
		case "U":
			t = strings.ToUpper(t)
		}
		e.edit(from, to, t, kindOther)
		e.cur = from
	case ">", "<":
		e.vimLines(rune(c.verb[0]), from.line, to.line)
	case "J":
		e.vimJoin(from.line, max(to.line, from.line+1))
	}
	return handled
}
