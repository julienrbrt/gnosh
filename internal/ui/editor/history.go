package editor

import (
	"strings"
	"unicode"

	"github.com/gnosh-ai/gnosh/internal/term"
)

// entry is one prompt in the history.
type entry struct {
	text string // as the editor keeps it
	// cleared marks a draft the person cleared with Esc Esc or Ctrl+C:
	// kept for this session only, so that Up can bring it back.
	cleared bool
	// gone marks an entry that is no longer offered. Entries are never
	// taken out of the list, so that the places the undo history
	// remembers stay right.
	gone bool
}

// SetHistory gives the editor the prompts that Up and Down walk through,
// oldest first. The application owns the history: it decides what is
// recorded and in what order, and the editor only walks it. Drafts the
// person cleared in this session stay, as the newest entries.
func (e *Editor) SetHistory(prompts []Draft) {
	if e.srch != nil {
		e.cancelSearch()
	}
	hist := make([]entry, 0, len(prompts)+clearedKept)
	for _, d := range prompts {
		if t := internalOf(d); strings.TrimSpace(plain(t)) != "" {
			hist = append(hist, entry{text: t})
		}
	}
	for _, h := range e.hist {
		if h.cleared && !h.gone {
			hist = append(hist, h)
		}
	}
	e.hist = hist
	// Whatever the undo history remembers about entries is about the
	// old list.
	e.walk, e.kept = -1, nil
	for i := range e.undo {
		e.undo[i].wb, e.undo[i].wa = -1, -1
	}
	for i := range e.redo {
		e.redo[i].wb, e.redo[i].wa = -1, -1
	}
}

// AddHistory records a prompt as the newest entry. The same prompt twice
// in a row is recorded once. The editor does not record what it submits
// by itself, because the application may hold a prompt in its queue
// first, and the history must not offer a second copy of a prompt that is
// still waiting there.
func (e *Editor) AddHistory(d Draft) {
	t := internalOf(d)
	if strings.TrimSpace(plain(t)) == "" {
		return
	}
	last := -1
	for i := len(e.hist) - 1; i >= 0 && last < 0; i-- {
		if !e.hist[i].gone {
			last = i
		}
	}
	for i := range e.hist {
		// A cleared draft that was sent after all is an ordinary entry
		// now.
		if e.hist[i].cleared && e.hist[i].text == t {
			e.hist[i].gone = true
		}
	}
	if last >= 0 && !e.hist[last].gone && e.hist[last].text == t {
		return
	}
	e.hist = append(e.hist, entry{text: t})
}

// Sent returns the history's entries that were sent, the newest first,
// each once: without the drafts the person cleared, and without the ones
// that hold a collapsed paste or more than one line, which are not
// offered as a completion.
func (e *Editor) Sent() []string {
	var out []string
	seen := map[string]bool{}
	for i := len(e.hist) - 1; i >= 0; i-- {
		h := e.hist[i]
		if h.gone || h.cleared || seen[h.text] || strings.Contains(h.text, marksLead) || strings.Contains(h.text, "\n") {
			continue
		}
		seen[h.text] = true
		out = append(out, h.text)
	}
	return out
}

// SetSearchHistory gives the editor a wider list for Ctrl+R to search
// than the one Up and Down walk (Claude Code searches the prompts of
// every project), oldest first. Nil means search the same list.
func (e *Editor) SetSearchHistory(prompts []Draft) {
	if e.srch != nil {
		e.cancelSearch()
	}
	if prompts == nil {
		e.found = nil
		return
	}
	e.found = make([]entry, 0, len(prompts))
	for _, d := range prompts {
		if t := internalOf(d); strings.TrimSpace(plain(t)) != "" {
			e.found = append(e.found, entry{text: t})
		}
	}
}

// older shows the entry before the one in the box (Up on the first row).
// Entries that would show the same text again are passed over.
func (e *Editor) older() {
	from := e.walk
	if from < 0 {
		from = len(e.hist)
	}
	now := e.whole()
	for i := from - 1; i >= 0; i-- {
		if !e.hist[i].gone && e.hist[i].text != now {
			e.show(i)
			return
		}
	}
}

// newer shows the entry after the one in the box (Down on the last row),
// and after the newest entry, the draft that was being typed.
func (e *Editor) newer() {
	if e.walk < 0 {
		return
	}
	now := e.whole()
	for i := e.walk + 1; i < len(e.hist); i++ {
		if !e.hist[i].gone && e.hist[i].text != now {
			e.show(i)
			return
		}
	}
	e.show(-1)
}

// show puts history entry i in the box, or the draft for -1. What the
// box held is remembered against the entry it came from, so that every
// entry visited, and the draft, is found again the way it was left.
func (e *Editor) show(i int) {
	if e.kept == nil {
		e.kept = make(map[int]saved)
	}
	e.kept[e.walk] = saved{text: e.whole(), cur: e.cur}
	k, ok := e.kept[i]
	if !ok && i >= 0 {
		k = saved{text: e.hist[i].text, cur: pos{-1, -1}}
	}
	e.replaceAll(k.text, i)
	if k.cur.line >= 0 {
		e.cur = k.cur
	}
}

// Clear empties the box as Esc Esc does, keeping what was in it for Up to
// bring back (the keybindings' chat:clearInput).
func (e *Editor) Clear() { e.clear() }

// clear empties the box (Esc Esc, Ctrl+C). What was in it is kept as a
// history entry for this session, and so is the draft that was put by
// when the person walked into the history, so nothing typed is lost.
func (e *Editor) clear() {
	e.comp = nil
	now := e.whole()
	if e.walk >= 0 {
		if k, ok := e.kept[-1]; ok {
			e.keepCleared(k.text)
		}
	}
	// What was on screen is kept last, so that it is the first thing Up
	// brings back. A history entry shown unchanged is in the history
	// already.
	if e.walk < 0 || e.walk >= len(e.hist) || e.hist[e.walk].text != now {
		e.keepCleared(now)
	}
	e.kept = nil
	e.replaceAll("", -1)
}

// keepCleared adds a cleared draft to the history. Only the newest few
// are offered.
func (e *Editor) keepCleared(t string) {
	if strings.TrimSpace(plain(t)) == "" {
		return
	}
	n := 0
	for i := len(e.hist) - 1; i >= 0; i-- {
		h := &e.hist[i]
		if h.gone || !h.cleared {
			continue
		}
		if h.text == t {
			h.gone = true
			continue
		}
		if n++; n >= clearedKept {
			h.gone = true
		}
	}
	e.hist = append(e.hist, entry{text: t, cleared: true})
}

// stash puts the text aside and empties the box (Ctrl+S); on an empty box
// it brings back the text put aside last, with its cursor.
func (e *Editor) stash() Result {
	if !e.IsEmpty() {
		e.comp = nil
		e.stashed = append(e.stashed, saved{text: e.whole(), cur: e.cur})
		e.replaceAll("", -1)
		return Result{Handled: true, Hint: HintStashed}
	}
	if n := len(e.stashed); n > 0 {
		s := e.stashed[n-1]
		e.stashed = e.stashed[:n-1]
		e.replaceAll(s.text, -1)
		e.cur = s.cur
	}
	return Result{Handled: true}
}

// search is the history search (Ctrl+R) while it is open. The box shows
// the entry found, and goes back to what it held if the search is given
// up.
type search struct {
	query  string
	list   []entry // what is searched, oldest first
	own    bool    // list is the editor's own history, the one Up walks
	idx    int     // the entry shown, or -1 when the box shows what it held
	failed bool    // the last look found nothing
	lower  map[int]string

	// What the box held when the search opened.
	lines []line
	cur   pos
	top   int
}

// openSearch opens the history search.
func (e *Editor) openSearch() {
	e.comp = nil
	s := &search{list: e.hist, own: true, idx: -1, lines: e.lines, cur: e.cur, top: e.top}
	if e.found != nil {
		s.list, s.own = e.found, false
	}
	e.srch = s
}

// cancelSearch closes the search and puts back what the box held.
func (e *Editor) cancelSearch() {
	s := e.srch
	e.srch = nil
	e.lines, e.cur, e.top = s.lines, s.cur, s.top
	// The rows may have been worked out for another width since.
	e.forget()
}

// acceptSearch closes the search and leaves the entry found in the box
// for editing, as one step of the undo history.
func (e *Editor) acceptSearch() {
	s := e.srch
	if s.idx < 0 {
		e.cancelSearch()
		return
	}
	found := s.list[s.idx].text
	e.cancelSearch()
	if s.own {
		// Up and Down go on from the entry found, which is shown as it
		// is in the history, whatever was made of it earlier.
		delete(e.kept, s.idx)
		e.show(s.idx)
		return
	}
	e.replaceAll(found, -1)
}

// matches reports whether entry i of the list holds the search text. A
// search with no capital letter in it ignores case.
func (s *search) matches(i int) bool {
	if s.list[i].gone {
		return false
	}
	if s.query == "" {
		return true
	}
	if strings.IndexFunc(s.query, unicode.IsUpper) >= 0 {
		return strings.Contains(plain(s.list[i].text), s.query)
	}
	low, ok := s.lower[i]
	if !ok {
		if s.lower == nil {
			s.lower = make(map[int]string)
		}
		low = strings.ToLower(plain(s.list[i].text))
		s.lower[i] = low
	}
	return strings.Contains(low, strings.ToLower(s.query))
}

// matchEnd returns where in the text of entry i, which matches, the
// first match ends, as a byte offset; or -1 when that cannot be told.
func (s *search) matchEnd(i int) int {
	if s.query == "" {
		return -1
	}
	t := plain(s.list[i].text)
	if strings.IndexFunc(s.query, unicode.IsUpper) >= 0 {
		return strings.Index(t, s.query) + len(s.query)
	}
	// Lower case can be longer or shorter than the text it came from;
	// its offsets are only good when it is not.
	low, q := s.lower[i], strings.ToLower(s.query)
	if at := strings.Index(low, q); at >= 0 && len(low) == len(t) {
		return at + len(q)
	}
	return -1
}

// seek looks for a match from entry from onwards, towards older entries
// (dir -1) or newer ones (dir 1), passing over entries that would show
// the same text as now, and shows the first it finds.
func (e *Editor) seek(from, dir int) {
	s := e.srch
	now := ""
	if s.idx >= 0 {
		now = s.list[s.idx].text
	}
	for i := from; i >= 0 && i < len(s.list); i += dir {
		if s.matches(i) && (s.idx < 0 || s.list[i].text != now) {
			s.idx, s.failed = i, false
			e.setLines(s.list[i].text)
			// The cursor, which is not shown while searching, goes to
			// the match, so that the rows shown are the ones it is on.
			if at := s.matchEnd(i); at >= 0 {
				p, _ := e.posAt(at)
				p.col = e.itemBefore(e.lines[p.line].s, p.col, e.home(p.line))
				e.cur = p
			}
			return
		}
	}
	s.failed = true
}

// searchType adds typed or pasted text to the search and looks again
// from the newest entry.
func (e *Editor) searchType(t string) {
	s := e.srch
	if i := strings.IndexByte(t, '\n'); i >= 0 {
		t = t[:i]
	}
	if t == "" {
		return
	}
	s.query += t
	e.research()
}

// research looks for the search text afresh, from the newest entry. With
// nothing to look for the box shows what it held.
func (e *Editor) research() {
	s := e.srch
	s.idx, s.failed = -1, false
	if s.query == "" {
		e.lines, e.cur, e.top = s.lines, s.cur, s.top
		e.forget()
		return
	}
	e.seek(len(s.list)-1, -1)
	if s.failed {
		e.lines, e.cur, e.top = s.lines, s.cur, s.top
		e.forget()
	}
}

// searchKey handles a key while the history search is open.
func (e *Editor) searchKey(k term.Key, cmd command) Result {
	s := e.srch
	handled := Result{Handled: true}
	switch cmd {
	case cmdNone:
		return Result{}
	case cmdInsert:
		e.searchType(clean(k.Text))
	case cmdBackspace:
		if s.query == "" {
			e.cancelSearch()
			return handled
		}
		s.query = s.query[:e.itemStart(s.query, len(s.query), 0)]
		e.research()
	case cmdKillLineStart:
		s.query = ""
		e.research()
	case cmdSearch, cmdUp:
		// The next older match.
		from := s.idx - 1
		if s.idx < 0 {
			from = len(s.list) - 1
		}
		e.seek(from, -1)
	case cmdStash, cmdDown:
		// The next newer match.
		if s.idx >= 0 {
			e.seek(s.idx+1, 1)
		}
	case cmdTab, cmdEscape:
		e.acceptSearch()
	case cmdSubmit, cmdSubmitNow:
		if s.idx < 0 {
			// Nothing was found: Enter must not send what the box
			// happened to hold.
			e.cancelSearch()
			return handled
		}
		e.acceptSearch()
		return e.submit(cmd == cmdSubmitNow)
	case cmdInterrupt, cmdEditor:
		e.cancelSearch()
	case cmdEOF, cmdYankPop, cmdRedo:
		// Nothing these could sensibly mean here.
	default:
		// A key that moves or edits: keep the match and act on it.
		e.acceptSearch()
		return e.run(k, cmd, press{})
	}
	return handled
}
