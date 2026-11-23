package menu

import (
	"strconv"
	"strings"
	"unicode"

	"github.com/gnosh-ai/gnosh/internal/term"
	"github.com/gnosh-ai/gnosh/internal/text"
	"github.com/gnosh-ai/gnosh/internal/ui/editor"
)

// TextMode says whether an option takes typed text.
type TextMode int

const (
	// NoText is an option that is only chosen.
	NoText TextMode = iota
	// TextOptional is an option that may be given a note: Tab opens a
	// field for it, and choosing the option sends the note along. It is
	// how a yes or a no is given a comment.
	TextOptional
	// TextRequired is an option that is its text: choosing it opens a
	// field, and Enter in the field, with something typed, is the
	// choice. It is how "no, do this instead" is said.
	TextRequired
)

// Option is one thing a Select offers.
type Option struct {
	// Label is what the option says. It is drawn as plain text: a
	// control character in it shows as a stand-in.
	Label string
	// Description is a line about the option, shown dimmed beside or
	// under it: for an option that saves a rule, the rule.
	Description string
	// Key is a character that picks the option at once, shown after its
	// label. Zero is none. A letter is matched in either case. The
	// digits pick by number and are not for use here.
	Key rune
	// Text says whether the option takes typed text.
	Text TextMode
	// Placeholder is shown, dimmed, in the option's empty text field.
	Placeholder string
}

// SelectOptions are what a Select is made from.
type SelectOptions struct {
	// Title is the question.
	Title string
	// Body is shown between the question and the options: the thing
	// being asked about, such as a command or a diff, as lines that are
	// already styled. Lines too long for the terminal are wrapped, and a
	// body too tall for the room scrolls. Colours, styles and hyperlinks
	// in it are kept; any other control character or escape sequence is
	// drawn as a stand-in ("^[") and never sent to the terminal.
	Body []string
	// Options are the choices.
	Options []Option
	// Selected is the option the cursor keys start on.
	Selected int
	// CancelHint is what the line of keys says Esc does: "cancel" when
	// empty. A permission question says "deny".
	CancelHint string
	// NoWrap keeps the cursor keys from going round: Up stops at the
	// first option and Down at the last, so that going on past no never
	// lands on yes (a permission question's). RefuseRow is the number,
	// from one, of the option a number past the last moves the cursor
	// to without choosing it (the no), or zero: such a number is then
	// not taken.
	NoWrap    bool
	RefuseRow int
	// Theme is the look. The zero Theme means DefaultTheme(text.NoColor).
	Theme Theme
	// MaxHeight is the most rows the question takes. Zero means 20.
	MaxHeight int
	// Metrics is how wide characters are counted; it must be the measure
	// the renderer uses.
	Metrics text.Metrics
}

// Select asks a person to choose one of several options. It is what a
// permission question is: allow once, allow and remember, deny, deny and
// say why.
//
// Like the editor it is a plain value: Handle takes the terminal's events
// and Render says what to draw. It is not safe for use by several
// goroutines at once.
type Select struct {
	frame
	title      string
	body       []string
	opts       []Option
	cancelHint string
	noWrap     bool // the cursor keys stop at the ends (SelectOptions.NoWrap)
	refuseRow  int  // where a number past the last moves the cursor, from one

	sel     int              // the option the cursor keys are on
	fields  []*editor.Editor // the text field of each option that takes text, once it has been opened
	typing  bool             // the keys go to the text field of the option chosen
	bodyTop int              // the first row of the body that is shown
	optTop  int              // the first row of the options that is shown
	// bodyPage is how many rows of the body the last Render showed, for
	// scrolling by a page.
	bodyPage int
	bodyRows int
}

// NewSelect makes a question with the choices of o.
func NewSelect(o SelectOptions) *Select {
	s := &Select{
		frame:      newFrame(o.Theme, o.Metrics, o.MaxHeight),
		title:      lines(o.Title),
		body:       styledAll(o.Body),
		opts:       safeOptions(o.Options),
		cancelHint: oneLine(o.CancelHint),
		noWrap:     o.NoWrap,
		refuseRow:  o.RefuseRow,
		sel:        min(max(o.Selected, 0), max(len(o.Options)-1, 0)),
		fields:     make([]*editor.Editor, len(o.Options)),
	}
	if s.cancelHint == "" {
		s.cancelHint = "cancel"
	}
	return s
}

// ConfirmOptions are what a yes-or-no question is made from.
type ConfirmOptions struct {
	// Title is the question, and Body what it is about, as in a Select.
	Title string
	Body  []string
	// Yes and No are the two answers: "Yes" and "No" when empty.
	Yes, No string
	// DefaultYes puts the cursor on Yes to begin with. Without it the
	// cursor begins on No, as it must for anything dangerous.
	DefaultYes bool
	// Theme, MaxHeight and Metrics are as in a Select.
	Theme     Theme
	MaxHeight int
	Metrics   text.Metrics
}

// NewConfirm makes a yes-or-no question: a Select with two options, the
// first of which is yes. The keys y and n pick at once, and Result.Yes
// reads the answer.
func NewConfirm(o ConfirmOptions) *Select {
	yes, no := o.Yes, o.No
	if yes == "" {
		yes = "Yes"
	}
	if no == "" {
		no = "No"
	}
	sel := 1
	if o.DefaultYes {
		sel = 0
	}
	return NewSelect(SelectOptions{
		Title:     o.Title,
		Body:      o.Body,
		Options:   []Option{{Label: yes, Key: 'y'}, {Label: no, Key: 'n'}},
		Selected:  sel,
		Theme:     o.Theme,
		MaxHeight: o.MaxHeight,
		Metrics:   o.Metrics,
	})
}

// Selected returns the option the cursor keys are on.
func (s *Select) Selected() int { return s.sel }

// move moves the cursor by one option, up (-1) or down: round from one
// end to the other, or, with noWrap, not past either.
func (s *Select) move(by int) {
	n := len(s.opts)
	if s.noWrap {
		s.sel = min(max(s.sel+by, 0), n-1)
		return
	}
	s.sel = (s.sel + by + n) % n
}

// Typing reports whether a text field is open and taking the keys.
func (s *Select) Typing() bool { return s.typing }

// TakeTexts gives s the text fields of old, a Select with the same options
// laid out for another width, so that what was typed under an option
// stays with it when the question is laid out again.
func (s *Select) TakeTexts(old *Select) {
	if len(old.fields) == len(s.fields) {
		copy(s.fields, old.fields)
	}
}

// handled is the result of a key that was understood and chose nothing,
// and unhandled that of an event that meant nothing to the question.
func handled() Result   { return Result{Handled: true, Index: -1} }
func unhandled() Result { return Result{Index: -1} }

// Handle takes one event from the terminal.
func (s *Select) Handle(ev term.Event) Result {
	if s.done || len(s.opts) == 0 {
		return unhandled()
	}
	if s.typing {
		return s.handleField(ev)
	}
	k, ok := ev.(term.Key)
	if !ok {
		return unhandled()
	}
	n := len(s.opts)
	switch kindOf(k) {
	case keyUp, keyBackTab:
		s.move(-1)
	case keyDown:
		s.move(1)
	case keyTab:
		// Tab opens the note of an option that takes one, and
		// otherwise moves on like Down.
		if s.opts[s.sel].Text != NoText {
			s.openField()
		} else {
			s.move(1)
		}
	case keyHome:
		s.sel = 0
	case keyEnd:
		s.sel = n - 1
	case keyEnter:
		if k.Repeat {
			// A key that is being held down never answers: the Enter
			// that sent a prompt must not also approve what the prompt
			// led to.
			return handled()
		}
		return s.choose(s.sel)
	case keyCancel, keyBreak:
		return s.finish(Result{Handled: true, Done: true, Cancelled: true, Index: -1})
	case keyPageUp:
		s.bodyTop -= max(s.bodyPage-1, 1)
	case keyPageDown:
		s.bodyTop += max(s.bodyPage-1, 1)
	case keyLineUp:
		s.bodyTop--
	case keyLineDown:
		s.bodyTop++
	case keyRune:
		r := []rune(k.Text)
		if len(r) != 1 {
			return unhandled()
		}
		// The option's own key first, then its number, then j and k.
		for i, o := range s.opts {
			if o.Key != 0 && unicode.ToLower(o.Key) == unicode.ToLower(r[0]) {
				if k.Repeat {
					return handled()
				}
				s.sel = i
				return s.choose(i)
			}
		}
		switch {
		case r[0] >= '1' && r[0] <= '9' && int(r[0]-'1') < n:
			if k.Repeat {
				return handled()
			}
			s.sel = int(r[0] - '1')
			return s.choose(s.sel)
		case r[0] >= '1' && r[0] <= '9' && s.refuseRow > 0:
			// A number past the last: the cursor goes to no, and
			// nothing is chosen.
			s.sel = min(s.refuseRow-1, n-1)
		case r[0] == 'j':
			s.move(1)
		case r[0] == 'k':
			s.move(-1)
		default:
			return unhandled()
		}
	default:
		return unhandled()
	}
	s.bodyTop = min(max(s.bodyTop, 0), max(s.bodyRows-1, 0))
	return handled()
}

// choose answers with option i, or opens its text field when the option
// is its text.
func (s *Select) choose(i int) Result {
	if s.opts[i].Text == TextRequired {
		s.openField()
		return handled()
	}
	res := Result{Handled: true, Done: true, Index: i}
	if f := s.fields[i]; f != nil {
		res.Text = f.Text()
	}
	return s.finish(res)
}

// finish ends the question.
func (s *Select) finish(res Result) Result {
	s.done = true
	s.typing = false
	return res
}

// openField gives the keys to the text field of the option chosen.
func (s *Select) openField() {
	if s.fields[s.sel] == nil {
		s.fields[s.sel] = s.field(s.opts[s.sel].Placeholder)
	}
	s.fields[s.sel].SetLocked(false)
	s.typing = true
}

// closeField takes the keys back from the text field. Its text stays,
// and shows under the option, unless discard is set.
func (s *Select) closeField(discard bool) {
	f := s.fields[s.sel]
	if discard {
		f.Reset()
	}
	f.SetLocked(true)
	s.typing = false
}

// handleField takes an event while a text field has the keys.
func (s *Select) handleField(ev term.Event) Result {
	f := s.fields[s.sel]
	if k, ok := ev.(term.Key); ok {
		switch kindOf(k) {
		case keyCancel, keyTab, keyBackTab:
			// Back to the list; what was typed stays with the option.
			s.closeField(false)
			return handled()
		case keyBreak:
			// Back to the list, and what was typed is thrown away.
			s.closeField(true)
			return handled()
		}
	}
	res := f.Handle(ev)
	switch {
	case res.Action == editor.ActionSubmit:
		// The editor empties itself when it hands text over; the text
		// is the answer now.
		return s.finish(Result{Handled: true, Done: true, Index: s.sel, Text: res.Text})
	case res.Handled:
		// Enter on an empty note chooses the option without one. (The
		// field has just declined to hand over nothing; a key that made
		// a new line changed the text and is not that.)
		if k, ok := ev.(term.Key); ok && kindOf(k) == keyEnter && !res.Changed &&
			s.opts[s.sel].Text == TextOptional && strings.TrimSpace(f.Text()) == "" {
			return s.finish(Result{Handled: true, Done: true, Index: s.sel})
		}
		return handled()
	}
	// The field handed the key back: Up on its first row and Down on
	// its last leave it for the option above or below.
	if k, ok := ev.(term.Key); ok {
		switch kindOf(k) {
		case keyUp:
			s.closeField(false)
			s.move(-1)
			return handled()
		case keyDown:
			s.closeField(false)
			s.move(1)
			return handled()
		}
	}
	return unhandled()
}

// Render draws the question for a terminal width cells wide: the title,
// the body, the options, and a dim line that names the keys. It takes at
// most MaxHeight rows; the body and the options scroll in what room there
// is.
func (s *Select) Render(width int) View {
	v := View{CursorRow: -1}
	if s.done || width < 1 {
		return v
	}
	inner := max(width-s.gutter(), 1)

	title := s.wrap(s.th.Title.Render(s.title), width, "", "")
	if s.title == "" {
		title = nil
	}
	var body block
	for _, l := range s.body {
		for _, row := range s.wrap(l, inner, "", "") {
			body.add(row)
		}
	}
	opts, first, last := s.optionRows(inner)

	// Share out the rows: the title and the line of keys first, then the
	// options, and the body gets what is left, three rows at least when
	// there are that many to give.
	room := max(s.maxHeight-len(title)-1, 1)
	bodyH := max(min(len(body.rows), max(room-len(opts.rows), min(3, room-1))), 0)
	optH := max(min(len(opts.rows), room-bodyH), 1)

	s.bodyRows, s.bodyPage = len(body.rows), bodyH
	s.bodyTop = min(max(s.bodyTop, 0), max(len(body.rows)-bodyH, 0))
	s.optTop = scrollTo(s.optTop, len(opts.rows), optH, first, last)
	keys := s.keysLine(width)

	v.Lines = append(v.Lines, title...)
	lines, _, _ := s.show(&body, s.bodyTop, bodyH)
	v.Lines = append(v.Lines, lines...)
	lines, row, col := s.show(&opts, s.optTop, optH)
	if row >= 0 {
		v.CursorRow, v.CursorCol = len(v.Lines)+row, col
	}
	v.Lines = append(v.Lines, lines...)
	if keys != "" {
		v.Lines = append(v.Lines, keys)
	}
	return s.fit(v, width)
}

// optionRows lays the options out in rows width cells wide, and says
// which rows belong to the option the cursor keys are on.
func (s *Select) optionRows(width int) (b block, first, last int) {
	for i, o := range s.opts {
		if i == s.sel {
			first = len(b.rows)
		}
		// The number that picks the option, for the first nine.
		num := "   "
		if i < 9 {
			num = s.th.Description.Render(strconv.Itoa(i+1)+".") + " "
		}
		st := s.th.Label
		if i == s.sel {
			st = s.th.Selected
		}
		label := st.Render(o.Label)
		if o.Key != 0 {
			label += " " + s.th.Description.Render("("+string(o.Key)+")")
		}
		desc := ""
		if o.Description != "" {
			desc = s.th.Description.Render(o.Description)
		}
		// The description goes beside the label when both fit on one
		// row, and under it otherwise.
		const indent = "   "
		oneRow := label
		if desc != "" {
			oneRow += "  " + desc
		}
		var rows []string
		if s.m.VisibleWidth(oneRow) <= width-3 && !strings.Contains(oneRow, "\n") {
			rows = []string{num + oneRow}
		} else {
			rows = s.wrap(label, width, num, indent)
			if desc != "" {
				rows = append(rows, s.wrap(desc, width, indent, indent)...)
			}
		}
		for j, r := range rows {
			b.rows = append(b.rows, brow{text: r, pointer: i == s.sel && j == 0, cursor: -1})
		}
		// The text field, while it is open or holds something.
		if f := s.fields[i]; f != nil && (s.typing && i == s.sel || !f.IsEmpty()) {
			fv := f.Render(max(width-3, 1))
			for j, l := range fv.Lines {
				row := brow{text: indent + l, cursor: -1}
				if j == fv.CursorRow {
					row.cursor = 3 + fv.CursorCol
				}
				b.rows = append(b.rows, row)
			}
		}
		if i == s.sel {
			last = len(b.rows) - 1
		}
	}
	return b, first, last
}

// keysLine is the dim line that names the keys, most useful first, as
// many as fit.
func (s *Select) keysLine(width int) string {
	if len(s.opts) == 0 {
		return ""
	}
	var parts []string
	if s.typing {
		parts = []string{"Enter choose", "Esc back", "Shift+Enter new line", "Ctrl+C discard"}
	} else {
		parts = []string{"Enter choose", "↑↓ move", "Esc " + s.cancelHint}
		if s.opts[s.sel].Text == TextOptional {
			parts = append(parts, "Tab add a note")
		}
		if n := min(len(s.opts), 9); n > 1 {
			parts = append(parts, "1-"+strconv.Itoa(n)+" pick")
		}
		if s.bodyRows > s.bodyPage {
			parts = append(parts, "PgUp/PgDn scroll")
		}
	}
	return s.hint(parts, width)
}
