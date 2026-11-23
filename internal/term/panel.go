package term

import (
	"io"
	"strconv"
	"strings"
	"sync"
)

// Panel draws gnosh's interface on the whole screen, with a side panel at
// the right: the way of drawing it that a person turns on (the
// interface's /panel, the sidePanel option). Renderer draws in the
// terminal's own scrollback, where a side panel cannot live: what a
// terminal scrolls is whole rows. So a Panel takes the alternate screen,
// keeps what was committed itself, and draws every row:
//
//	the last committed lines (or earlier       | the panel's lines,
//	ones, with Scroll)                         | from the top
//	the live region                            |
//
// It takes what a Renderer takes (lines committed for good, a live region
// and where its cursor is) and lays both out at its left width, which
// Size gives. While it is open the terminal reports the mouse, so that the
// wheel can scroll back (Scroll); the terminal then selects text with a
// modifier key held. Close puts the terminal back as it was and returns
// what was committed meanwhile, for the terminal's own scrollback, so
// that nothing said is lost when the panel goes.
//
// Every update goes out in a single Write, and only the rows that changed
// are drawn. A Panel may be used from several goroutines.
type Panel struct {
	mu     sync.Mutex
	out    io.Writer
	width  func(string) int
	synced bool

	cols, rows int // the terminal's size
	left       *Renderer
	right      *Renderer
	// hasSide says there is a side panel; a view (OpenView) has none, and
	// its text has the whole width. keep is how many committed rows are
	// kept to scroll back through.
	hasSide bool
	keep    int

	hist  []rline  // what was committed, at the left width
	since []string // what was committed while open, as given
	live  []rline
	side  []rline
	back  int // how many rows the view is scrolled back from the end

	wantRow, wantCol int
	wantShown        bool

	shown  []string // what each row of the screen shows now
	cursor string   // where the cursor was last put, as written
	open   bool
	buf    []byte // what must go out first: a whole clear of the screen
}

// The panel's size, and how much of the past it keeps.
const (
	// PanelMinCols is the narrowest terminal that takes a panel.
	PanelMinCols = 72
	// panelSideMin and panelSideMax bound the panel's width, its border
	// included; between them it takes a third of the terminal.
	panelSideMin = 24
	panelSideMax = 48
	// panelKeep is how many committed rows the panel keeps to scroll back
	// through, and viewKeep how many a view does: a long session's whole
	// transcript.
	panelKeep = 10000
	viewKeep  = 200000
)

// The sequences only the panel writes.
const (
	altScreenOn  = "\x1b[?1049h"
	altScreenOff = "\x1b[?1049l"
	// mouseOn asks for presses and the wheel (mode 1000) in SGR form
	// (mode 1006), which the decoder reads as Mouse events.
	mouseOn  = "\x1b[?1000h\x1b[?1006h"
	mouseOff = "\x1b[?1006l\x1b[?1000l"
	clearAll = "\x1b[H\x1b[2J"
	// screenOn and screenOff take the whole screen and give it back, as
	// the Terminal writes them for a Panel when the terminal is given up
	// and taken back (Terminal.noteScreen).
	screenOn   = altScreenOn + AutoWrapOff + mouseOn
	screenOff  = mouseOff + altScreenOff
	panelLine  = "\x1b[90m│\x1b[39m"
	panelFaint = "\x1b[90m"
)

// screenNoter is what a Panel draws on when it is a Terminal: told when
// the whole screen is taken and given back, it gives it back too whenever
// the terminal is given up.
type screenNoter interface{ noteScreen(taken bool) }

// PanelFits reports whether a terminal of cols columns takes a panel.
func PanelFits(cols int) bool { return cols >= PanelMinCols }

// panelSide is the width the panel takes in a terminal of cols columns,
// its border included.
func panelSide(cols int) int { return min(max(cols/3, panelSideMin), panelSideMax) }

// OpenPanel takes the terminal's whole screen for gnosh, with a panel at
// the right: it switches to the alternate screen, turns the terminal's own
// wrapping off and the mouse on, and draws an empty frame. cols and rows
// are the terminal's size; width is as for NewRenderer.
func OpenPanel(out io.Writer, cols, rows int, width func(string) int, synced bool) (*Panel, error) {
	return openScreen(out, cols, rows, width, synced, true, panelKeep)
}

// OpenView takes the terminal's whole screen for a view with no side
// panel, its text the whole width: the interface's transcript view
// (Ctrl+O). It is a Panel in all else; SetLines gives it what it shows.
func OpenView(out io.Writer, cols, rows int, width func(string) int, synced bool) (*Panel, error) {
	return openScreen(out, cols, rows, width, synced, false, viewKeep)
}

// openScreen is OpenPanel and OpenView.
func openScreen(out io.Writer, cols, rows int, width func(string) int, synced, side bool, keep int) (*Panel, error) {
	p := &Panel{out: out, width: width, synced: synced, open: true, wantShown: true, hasSide: side, keep: keep}
	p.size(cols, rows)
	if _, err := io.WriteString(out, screenOn+clearAll); err != nil {
		return nil, err
	}
	if n, ok := out.(screenNoter); ok {
		n.noteScreen(true)
	}
	return p, p.paintLocked()
}

// size takes in the terminal's size and makes the two halves' preparers
// for it.
func (p *Panel) size(cols, rows int) {
	p.cols, p.rows = saneSize(cols, rows)
	side := 0
	if p.hasSide {
		side = panelSide(p.cols)
	}
	p.left = &Renderer{cols: max(p.cols-side, 1), width: p.width}
	p.right = &Renderer{cols: max(side-2, 1), width: p.width}
}

// Size is the room the panel leaves for the interface: the left part's
// width, and the screen's height.
func (p *Panel) Size() (cols, rows int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.left.cols, p.rows
}

// TextWidth is the room the panel's lines have.
func (p *Panel) TextWidth() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.right.cols
}

// Commit adds lines to what was committed, below the rest, and draws.
// While the view is scrolled back, it stays where it is.
func (p *Panel) Commit(lines ...string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.commitLocked(lines)
	return p.paintLocked()
}

// CommitLive is Commit and then SetLive, drawn once, as
// Renderer.CommitLive is.
func (p *Panel) CommitLive(commit, live []string, cursorRow, cursorCol int) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.commitLocked(commit)
	p.setLiveLocked(live, cursorRow, cursorCol)
	return p.paintLocked()
}

func (p *Panel) commitLocked(lines []string) {
	n := 0
	for _, l := range lines {
		for _, part := range strings.Split(l, "\n") {
			part = strings.TrimSuffix(part, "\r")
			p.hist = append(p.hist, p.left.prepare(part))
			p.since = append(p.since, part)
			n++
		}
	}
	if over := len(p.hist) - p.keep; over > 0 {
		p.hist = append(p.hist[:0], p.hist[over:]...)
	}
	if p.back > 0 {
		p.back += n
	}
}

// SetLines puts lines in place of everything committed, and draws. While
// the view is scrolled back, the row at its top stays at its top: the
// lines of a view that grew at the end are still where they were.
func (p *Panel) SetLines(lines ...string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.setLinesLocked(lines, p.topLocked())
	return p.paintLocked()
}

// setLinesLocked is SetLines but for the drawing: top is the row that
// was at the view's top.
func (p *Panel) setLinesLocked(lines []string, top int) {
	p.hist, p.since = p.hist[:0], p.since[:0]
	for _, l := range lines {
		for _, part := range strings.Split(l, "\n") {
			part = strings.TrimSuffix(part, "\r")
			p.hist = append(p.hist, p.left.prepare(part))
			p.since = append(p.since, part)
		}
	}
	if over := len(p.hist) - p.keep; over > 0 {
		p.hist = append(p.hist[:0], p.hist[over:]...)
	}
	if p.back > 0 {
		p.back = min(max(len(p.hist)-p.viewRows()-top, 0), p.maxBack())
	}
}

// SetView is SetLines and SetLive at once, drawn once: a view that lays
// out the whole screen on every frame (the agent view) draws no frame
// that is half the old one.
func (p *Panel) SetView(lines, live []string, cursorRow, cursorCol int) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	top := p.topLocked()
	p.setLiveLocked(live, cursorRow, cursorCol)
	p.setLinesLocked(lines, top)
	return p.paintLocked()
}

// Top is the row of what was committed that the view shows at its top.
func (p *Panel) Top() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.topLocked()
}

func (p *Panel) topLocked() int { return max(len(p.hist)-p.back-p.viewRows(), 0) }

// ScrollTo moves the view so that row of what was committed is at its
// top, or as near as the end lets it be, and reports whether it moved.
func (p *Panel) ScrollTo(row int) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	was := p.back
	p.back = min(max(len(p.hist)-p.viewRows()-row, 0), p.maxBack())
	if p.back == was {
		return false
	}
	_ = p.paintLocked()
	return true
}

// SetLive replaces the live region, as Renderer.SetLive does.
func (p *Panel) SetLive(lines []string, cursorRow, cursorCol int) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.setLiveLocked(lines, cursorRow, cursorCol)
	return p.paintLocked()
}

func (p *Panel) setLiveLocked(lines []string, cursorRow, cursorCol int) {
	p.live = p.live[:0]
	for _, l := range lines {
		p.live = append(p.live, p.left.prepare(l))
	}
	p.wantShown = cursorRow >= 0
	p.wantRow, p.wantCol = max(cursorRow, 0), max(cursorCol, 0)
}

// SetPanel replaces what the panel shows, a row a line from its top.
// Lines that do not fit are left out at the bottom.
func (p *Panel) SetPanel(lines []string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.side = p.side[:0]
	for _, l := range lines {
		p.side = append(p.side, p.right.prepare(l))
	}
	return p.paintLocked()
}

// Scroll moves the view rows rows back through what was committed (or,
// with a negative number, forward again), and reports whether it moved.
func (p *Panel) Scroll(rows int) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	was := p.back
	p.back = min(max(p.back+rows, 0), p.maxBack())
	if p.back == was {
		return false
	}
	_ = p.paintLocked()
	return true
}

// Page is how many rows a page of scrolling is: the rows that show what
// was committed, less one.
func (p *Panel) Page() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return max(p.viewRows()-1, 1)
}

// Back reports how many rows the view is scrolled back from the end.
func (p *Panel) Back() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.back
}

// Resize takes in the terminal's new size, lays out what it keeps for it
// and draws everything again.
func (p *Panel) Resize(cols, rows int) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.size(cols, rows)
	for i, l := range p.hist {
		p.hist[i] = p.left.prepare(l.raw)
	}
	for i, l := range p.live {
		p.live[i] = p.left.prepare(l.raw)
	}
	for i, l := range p.side {
		p.side[i] = p.right.prepare(l.raw)
	}
	p.back = min(p.back, p.maxBack())
	p.shown = nil
	p.buf = append(p.buf[:0], clearAll...)
	return p.paintLocked()
}

// Clear draws the whole screen again.
func (p *Panel) Clear() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.shown = nil
	p.buf = append(p.buf[:0], clearAll...)
	return p.paintLocked()
}

// Reset forgets what the screen shows, so that the next update draws
// every row.
func (p *Panel) Reset() {
	p.mu.Lock()
	p.shown = nil
	p.mu.Unlock()
}

// SetSynchronized says whether the terminal knows synchronized output, as
// for Renderer.
func (p *Panel) SetSynchronized(on bool) {
	p.mu.Lock()
	p.synced = on
	p.mu.Unlock()
}

// Close gives the screen back: the terminal leaves the alternate screen,
// with what it showed before as it was, and its wrapping, cursor and mouse
// as they were. It returns the lines committed while the panel was open.
func (p *Panel) Close() ([]string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.open {
		return nil, nil
	}
	p.open = false
	_, err := io.WriteString(p.out, mouseOff+ShowCursor+AutoWrapOn+altScreenOff)
	if n, ok := p.out.(screenNoter); ok {
		n.noteScreen(false)
	}
	return p.since, err
}

// maxBack is the furthest back the view can go.
func (p *Panel) maxBack() int { return max(len(p.hist)-p.viewRows(), 0) }

// liveRows is how many rows of the live region show: all of it, but for
// one row kept for what was committed.
func (p *Panel) liveRows() int { return min(len(p.live), p.rows-1) }

// viewRows is how many rows show what was committed.
func (p *Panel) viewRows() int { return p.rows - p.liveRows() }

// paintLocked draws the rows that changed, and puts the cursor where the
// live region wants it. Nothing is written when nothing changed.
func (p *Panel) paintLocked() error {
	if !p.open {
		return nil
	}
	liveN := p.liveRows()
	live := p.live[len(p.live)-liveN:]
	view := p.rows - liveN
	start := len(p.hist) - p.back - view
	if len(p.shown) != p.rows {
		p.shown = make([]string, p.rows)
		for i := range p.shown {
			p.shown[i] = "\x00" // drawn by nothing: every row is drawn
		}
		p.cursor = ""
	}
	var rows []byte
	for y := 0; y < p.rows; y++ {
		var l rline
		switch {
		case y >= view:
			l = live[y-view]
		case p.back > 0 && y == view-1:
			l = p.left.prepare(panelFaint + "↓ " + strconv.Itoa(p.back) + " more rows below: PageDown, or the wheel" + StyleReset)
		case start+y >= 0 && start+y < len(p.hist):
			l = p.hist[start+y]
		}
		row, end := l.text, l.width
		if p.hasSide {
			var r rline
			if y < len(p.side) {
				r = p.side[y]
			}
			row += strings.Repeat(" ", max(p.left.cols-l.width, 0)) + panelLine + " " + r.text
			end = p.left.cols + 2 + r.width
		}
		if row == p.shown[y] {
			continue
		}
		p.shown[y] = row
		rows = append(rows, "\x1b["...)
		rows = strconv.AppendInt(rows, int64(y+1), 10)
		rows = append(rows, ";1H"...)
		rows = append(rows, row...)
		if end < p.cols {
			// (Not after a row that fills the screen: with wrapping off
			// the cursor stays on the last cell, which would be erased.)
			rows = append(rows, EraseToLineEnd...)
		}
	}
	cursor := HideCursor
	if crow := p.wantRow - (len(p.live) - liveN); p.wantShown && crow >= 0 && crow < liveN {
		cursor = "\x1b[" + strconv.Itoa(view+crow+1) + ";" + strconv.Itoa(min(p.wantCol, p.left.cols-1)+1) + "H" + ShowCursor
	}
	if len(p.buf) == 0 && len(rows) == 0 && cursor == p.cursor {
		return nil
	}
	b := p.buf
	if p.synced {
		b = append([]byte(SyncBegin), b...)
	}
	if len(rows) > 0 {
		b = append(b, HideCursor...)
		b = append(b, rows...)
	}
	b = append(b, cursor...)
	p.cursor = cursor
	if p.synced {
		b = append(b, SyncEnd...)
	}
	p.buf = p.buf[:0]
	_, err := p.out.Write(b)
	return err
}
