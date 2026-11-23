package menu

import (
	"strconv"
	"strings"

	"github.com/gnosh-ai/gnosh/internal/term"
	"github.com/gnosh-ai/gnosh/internal/text"
	"github.com/gnosh-ai/gnosh/internal/ui/editor"
)

// Question is one question of a Form.
type Question struct {
	// Header is a very short name for the question, shown on its tab and
	// in the review.
	Header string
	// Text is the question.
	Text string
	// Description is a line or two more about it, shown dimmed.
	Description string
	// Options are the answers offered. Only Label and Description are
	// used. With no options the question is answered by typing.
	Options []Option
	// Multi lets any number of the options be chosen: Space ticks them.
	Multi bool
	// NoOther leaves out the last row, on which the person can answer in
	// their own words instead.
	NoOther bool
	// Placeholder is shown, dimmed, in the empty field for the person's
	// own words.
	Placeholder string
}

// Answer is the answer to one question of a Form.
type Answer struct {
	// Picked are the options chosen, by their place in the question's
	// Options, in order. A question that takes one answer has at most
	// one.
	Picked []int
	// Other is what the person typed in their own words, when that is
	// their answer or part of it.
	Other string
}

// Empty reports whether the question was left without an answer.
func (a Answer) Empty() bool { return len(a.Picked) == 0 && a.Other == "" }

// FormOptions are what a Form is made from.
type FormOptions struct {
	// Questions are the questions, in order.
	Questions []Question
	// Lead stands at the top of every tab: who asks and why, when that is
	// not the model (an MCP server's message). At most leadRows rows of it
	// are drawn.
	Lead string
	// ReviewTitle and ReviewNote stand at the top of the last tab, where
	// the answers are looked over before they are sent: "Review your
	// answers" and nothing, when empty.
	ReviewTitle, ReviewNote string
	// Theme, MaxHeight and Metrics are as in a Select.
	Theme     Theme
	MaxHeight int
	Metrics   text.Metrics
}

// leadRows is the most rows a form's lead takes.
const leadRows = 4

// Form asks several questions at once, one tab each, with a last tab on
// which the answers are looked over and sent. It is what the model's
// question tool shows. A form of one question has no tabs and no review:
// answering the question ends it.
type Form struct {
	frame
	qs                      []Question
	reviewTitle, reviewNote string
	lead                    string
	st                      []qstate
	tab                     int  // the question in front; len(qs) is the review
	typing                  bool // the keys go to the text field of the question in front
	top                     int  // the first row of the options that is shown
}

// qstate is where one question of a form stands.
type qstate struct {
	row     int            // the row the cursor keys are on; len(Options) is the row for the person's own words
	picked  []bool         // which options are chosen
	other   *editor.Editor // the field for the person's own words
	otherOn bool           // those words are the answer, or part of it
}

// NewForm makes a form with the questions of o.
func NewForm(o FormOptions) *Form {
	f := &Form{
		frame:       newFrame(o.Theme, o.Metrics, o.MaxHeight),
		qs:          make([]Question, len(o.Questions)),
		reviewTitle: lines(o.ReviewTitle),
		reviewNote:  lines(o.ReviewNote),
		lead:        lines(o.Lead),
		st:          make([]qstate, len(o.Questions)),
	}
	if f.reviewTitle == "" {
		f.reviewTitle = "Review your answers"
	}
	// The questions are the model's words: made safe to draw, once.
	for i, q := range o.Questions {
		q.Header, q.Text, q.Description = oneLine(q.Header), lines(q.Text), lines(q.Description)
		q.Options = safeOptions(q.Options)
		f.qs[i] = q
	}
	for i, q := range f.qs {
		f.st[i].picked = make([]bool, len(q.Options))
		f.st[i].other = f.field(q.Placeholder)
		f.st[i].other.SetLocked(true)
	}
	f.enter(0)
	return f
}

// Tab returns the question in front, counted from 0; the number of
// questions means the review.
func (f *Form) Tab() int { return f.tab }

// Typing reports whether a text field is open and taking the keys.
func (f *Form) Typing() bool { return f.typing }

// Answers returns the answers as they stand.
func (f *Form) Answers() []Answer {
	out := make([]Answer, len(f.qs))
	for i := range f.qs {
		for j, on := range f.st[i].picked {
			if on {
				out[i].Picked = append(out[i].Picked, j)
			}
		}
		if f.st[i].otherOn {
			out[i].Other = f.st[i].other.Text()
		}
	}
	return out
}

// hasOther reports whether question i has the row for the person's own
// words. A question with no options always does: it is the question.
func (f *Form) hasOther(i int) bool {
	return !f.qs[i].NoOther || len(f.qs[i].Options) == 0
}

// rows is how many rows the cursor keys move over in question i.
func (f *Form) rows(i int) int {
	n := len(f.qs[i].Options)
	if f.hasOther(i) {
		n++
	}
	return n
}

// enter brings a tab to the front. A question that is answered by typing
// has its field open at once.
func (f *Form) enter(tab int) {
	f.typing = false
	f.tab = tab
	f.top = 0
	if tab < len(f.qs) && len(f.qs[tab].Options) == 0 {
		f.openField()
	}
}

// openField gives the keys to the field of the question in front.
func (f *Form) openField() {
	st := &f.st[f.tab]
	st.row = len(f.qs[f.tab].Options)
	st.other.SetLocked(false)
	f.typing = true
}

// closeField takes the keys back from the field. What was typed stays.
// It becomes the answer, or part of it, when it is the only answer there
// is or the question takes several; an option already chosen in a
// question that takes one is not pushed out by words that were never
// confirmed.
func (f *Form) closeField() {
	st := &f.st[f.tab]
	st.other.SetLocked(true)
	f.typing = false
	typed := strings.TrimSpace(st.other.Text()) != ""
	switch {
	case !typed:
		st.otherOn = false
	case f.qs[f.tab].Multi || !f.anyPicked(f.tab):
		st.otherOn = true
	}
}

// anyPicked reports whether an option of question i is chosen.
func (f *Form) anyPicked(i int) bool {
	for _, on := range f.st[i].picked {
		if on {
			return true
		}
	}
	return false
}

// pick makes row the answer of the question in front (or, in a question
// that takes several, ticks or unticks it).
func (f *Form) pick(row int) {
	q, st := f.qs[f.tab], &f.st[f.tab]
	other := row == len(q.Options)
	if q.Multi {
		if other {
			st.otherOn = !st.otherOn
		} else {
			st.picked[row] = !st.picked[row]
		}
		return
	}
	for j := range st.picked {
		st.picked[j] = !other && j == row
	}
	st.otherOn = other
}

// advance goes on to the next tab, or ends a form of one question.
func (f *Form) advance() Result {
	if len(f.qs) == 1 {
		return f.finish(false)
	}
	f.enter(f.tab + 1)
	return handled()
}

// finish ends the form.
func (f *Form) finish(cancelled bool) Result {
	res := Result{Handled: true, Done: true, Cancelled: cancelled, Index: -1}
	if !cancelled {
		res.Answers = f.Answers()
	}
	f.done, f.typing = true, false
	return res
}

// turn brings the tab n places on to the front, going round.
func (f *Form) turn(n int) Result {
	if len(f.qs) < 2 {
		return unhandled()
	}
	if f.typing {
		f.closeField()
	}
	tabs := len(f.qs) + 1
	f.enter(((f.tab+n)%tabs + tabs) % tabs)
	return handled()
}

// Handle takes one event from the terminal.
func (f *Form) Handle(ev term.Event) Result {
	if f.done || len(f.qs) == 0 {
		return unhandled()
	}
	if f.typing {
		return f.handleField(ev)
	}
	k, ok := ev.(term.Key)
	if !ok {
		return unhandled()
	}
	kind := kindOf(k)
	if k.Repeat && (kind == keyEnter || kind == keySpace || kind == keyRune && k.Text >= "1" && k.Text <= "9") {
		// A key that is being held down never answers.
		return handled()
	}
	switch kind {
	case keyLeft, keyBackTab:
		return f.turn(-1)
	case keyRight, keyTab:
		return f.turn(1)
	case keyCancel, keyBreak:
		return f.finish(true)
	}
	if f.tab == len(f.qs) {
		// The review: Enter sends.
		if kind == keyEnter {
			return f.finish(false)
		}
		return unhandled()
	}
	q, st := f.qs[f.tab], &f.st[f.tab]
	n := f.rows(f.tab)
	onOther := st.row == len(q.Options)
	switch kind {
	case keyUp:
		st.row = (st.row + n - 1) % n
	case keyDown:
		st.row = (st.row + 1) % n
	case keyHome:
		st.row = 0
	case keyEnd:
		st.row = n - 1
	case keySpace:
		// Space marks without moving on. The row for the person's own
		// words is marked once there are words on it.
		if onOther && strings.TrimSpace(st.other.Text()) == "" {
			f.openField()
		} else {
			f.pick(st.row)
		}
	case keyEnter:
		switch {
		case onOther:
			f.openField()
		case !q.Multi:
			f.pick(st.row)
			return f.advance()
		default:
			// In a question that takes several, Enter goes on. If
			// nothing is ticked, it ticks the row it is on first, so
			// that Enter alone is never an empty answer by accident.
			if !f.anyPicked(f.tab) && !st.otherOn {
				f.pick(st.row)
			}
			return f.advance()
		}
	case keyRune:
		r := []rune(k.Text)
		switch {
		case len(r) != 1:
			return unhandled()
		case r[0] == 'j':
			st.row = (st.row + 1) % n
		case r[0] == 'k':
			st.row = (st.row + n - 1) % n
		case r[0] >= '1' && r[0] <= '9' && int(r[0]-'1') < n:
			// A number is the row and the key that answers on it.
			st.row = int(r[0] - '1')
			switch {
			case st.row == len(q.Options):
				f.openField()
			case q.Multi:
				f.pick(st.row)
			default:
				f.pick(st.row)
				return f.advance()
			}
		default:
			return unhandled()
		}
	default:
		return unhandled()
	}
	return handled()
}

// handleField takes an event while the text field has the keys.
func (f *Form) handleField(ev term.Event) Result {
	q, st := f.qs[f.tab], &f.st[f.tab]
	k, isKey := ev.(term.Key)
	if isKey {
		switch kindOf(k) {
		case keyCancel:
			// Out of the field. In a question that is only a field,
			// there is nowhere else to be: Esc cancels the form.
			if len(q.Options) == 0 {
				return f.finish(true)
			}
			f.closeField()
			return handled()
		case keyBreak:
			// What was typed is thrown away.
			if len(q.Options) == 0 && st.other.IsEmpty() {
				return f.finish(true)
			}
			st.other.Reset()
			if len(q.Options) > 0 {
				f.closeField()
			} else {
				st.otherOn = false
			}
			return handled()
		case keyTab:
			return f.turn(1)
		case keyBackTab:
			return f.turn(-1)
		}
	}
	res := st.other.Handle(ev)
	switch {
	case res.Action == editor.ActionSubmit:
		// The editor empties itself when it hands text over; here the
		// text stays in the field, to be seen and changed.
		st.other.SetDraft(editor.Draft{Text: res.Text, Pastes: res.Pastes})
		f.closeField()
		if !q.Multi {
			f.pick(len(q.Options))
		}
		st.otherOn = true
		return f.advance()
	case res.Handled:
		if isKey && kindOf(k) == keyEnter && !res.Changed {
			// Enter with nothing typed: nothing to confirm.
			if len(q.Options) > 0 {
				f.closeField()
			}
		}
		return handled()
	}
	// The field handed the key back: Up on its first row and Down on
	// its last leave it for the options.
	if isKey && len(q.Options) > 0 {
		n := f.rows(f.tab)
		switch kindOf(k) {
		case keyUp:
			f.closeField()
			st.row = (st.row + n - 1) % n
			return handled()
		case keyDown:
			f.closeField()
			st.row = (st.row + 1) % n
			return handled()
		}
	}
	return unhandled()
}

// Render draws the form: the tabs, the question in front with its
// options, or the review, and a dim line that names the keys.
func (f *Form) Render(width int) View {
	v := View{CursorRow: -1}
	if f.done || width < 1 || len(f.qs) == 0 {
		return v
	}
	inner := max(width-f.gutter(), 1)
	if f.lead != "" {
		lead := f.wrap(f.th.Description.Render(f.lead), width, "", "")
		if len(lead) > leadRows {
			lead = append(lead[:leadRows-1:leadRows-1], f.th.Description.Render("…"))
		}
		v.Lines = append(v.Lines, lead...)
	}
	if len(f.qs) > 1 {
		v.Lines = append(v.Lines, f.tabsLine(width))
	}
	if f.tab == len(f.qs) {
		v.Lines = append(v.Lines, f.review(width)...)
		if keys := f.hint([]string{"Enter send", "←/→ change an answer", "Esc cancel"}, width); keys != "" {
			v.Lines = append(v.Lines, keys)
		}
		return f.fit(v, width)
	}
	q := f.qs[f.tab]
	if q.Text != "" {
		v.Lines = append(v.Lines, f.wrap(f.th.Title.Render(q.Text), width, "", "")...)
	}
	if q.Description != "" {
		v.Lines = append(v.Lines, f.wrap(f.th.Description.Render(q.Description), width, "", "")...)
	}
	b, first, last := f.optionRows(inner)
	h := max(min(len(b.rows), f.maxHeight-len(v.Lines)-1), 1)
	f.top = scrollTo(f.top, len(b.rows), h, first, last)
	lines, row, col := f.show(&b, f.top, h)
	if row >= 0 {
		v.CursorRow, v.CursorCol = len(v.Lines)+row, col
	}
	v.Lines = append(v.Lines, lines...)
	if keys := f.hint(f.keys(), width); keys != "" {
		v.Lines = append(v.Lines, keys)
	}
	return f.fit(v, width)
}

// keys names the keys of the question in front, most useful first.
func (f *Form) keys() []string {
	q := f.qs[f.tab]
	var parts []string
	switch {
	case f.typing:
		parts = []string{"Enter confirm"}
		if len(q.Options) > 0 {
			parts = append(parts, "Esc back")
		} else {
			parts = append(parts, "Esc cancel")
		}
	case q.Multi:
		parts = []string{"Space tick", "Enter next", "↑↓ move", "Esc cancel"}
	default:
		parts = []string{"Enter choose", "↑↓ move", "Esc cancel"}
	}
	if len(f.qs) > 1 {
		if f.typing {
			parts = append(parts, "Tab next question")
		} else {
			parts = append(parts, "←/→ questions")
		}
	}
	return parts
}

// header is the short name of question i.
func (f *Form) header(i int) string {
	if h := f.qs[i].Header; h != "" {
		return h
	}
	return "Question " + strconv.Itoa(i+1)
}

// tabsLine draws the row of tabs: the one in front in brackets, each
// answered question with a mark. When the tabs do not fit the terminal,
// it says only which question is in front.
func (f *Form) tabsLine(width int) string {
	answers := f.Answers()
	name := func(i int) string {
		if i == len(f.qs) {
			return "Review"
		}
		s := f.header(i)
		if !answers[i].Empty() {
			s += " " + f.th.Answered
		}
		return s
	}
	var b strings.Builder
	for i := 0; i <= len(f.qs); i++ {
		if i == f.tab {
			b.WriteString(f.th.TabCurrent.Render("[" + name(i) + "]"))
		} else {
			b.WriteString(f.th.Tab.Render(" " + name(i) + " "))
		}
	}
	if line := b.String(); f.m.VisibleWidth(line) <= width {
		return line
	}
	short := "[" + name(f.tab) + "]"
	if f.tab < len(f.qs) {
		short = "[" + strconv.Itoa(f.tab+1) + " of " + strconv.Itoa(len(f.qs)) + ": " + name(f.tab) + "]"
	}
	return f.th.TabCurrent.Render(short)
}

// summary is the answer to question i in a few words.
func (f *Form) summary(i int, a Answer) string {
	var parts []string
	for _, j := range a.Picked {
		parts = append(parts, f.qs[i].Options[j].Label)
	}
	if a.Other != "" {
		// What the person typed, on one line, with anything a terminal
		// would act on made visible.
		parts = append(parts, oneLine(strings.Join(strings.Fields(a.Other), " ")))
	}
	return strings.Join(parts, ", ")
}

// review draws the last tab: every question's answer, and which were
// left without one.
func (f *Form) review(width int) []string {
	lines := f.wrap(f.th.Title.Render(f.reviewTitle), width, "", "")
	if f.reviewNote != "" {
		lines = append(lines, f.wrap(f.th.Description.Render(f.reviewNote), width, "", "")...)
	}
	for i, a := range f.Answers() {
		answer := f.th.Missing.Render("(no answer)")
		if !a.Empty() {
			answer = f.th.Label.Render(f.summary(i, a))
		}
		lines = append(lines, f.wrap(f.header(i)+": "+answer, width, "  ", "    ")...)
	}
	// The review must fit with its tabs and its line of keys: the
	// answers at its end are what matter.
	if room := max(f.maxHeight-2, 1); len(lines) > room {
		lines = lines[len(lines)-room:]
	}
	return lines
}

// optionRows lays out the rows of the question in front, and says which
// of them belong to the row the cursor keys are on.
func (f *Form) optionRows(width int) (b block, first, last int) {
	q, st := f.qs[f.tab], f.st[f.tab]
	for i := 0; i < f.rows(f.tab); i++ {
		if i == st.row {
			first = len(b.rows)
		}
		other := i == len(q.Options)
		// What stands before the label: the number that picks the row,
		// and a box that says whether it is chosen.
		on := other && st.otherOn || !other && st.picked[i]
		box := f.th.Unpicked
		switch {
		case q.Multi && on:
			box = f.th.Checked
		case q.Multi:
			box = f.th.Unchecked
		case on:
			box = f.th.Picked
		}
		num := "   "
		if i < 9 {
			num = f.th.Description.Render(strconv.Itoa(i+1)+".") + " "
		}
		head := num + box + " "
		pad := strings.Repeat(" ", f.m.VisibleWidth(head))
		if len(q.Options) == 0 {
			// A question that is only a field has no row above it.
			pad = ""
		} else {
			label, desc := "Other, in your own words", ""
			if !other {
				label, desc = q.Options[i].Label, q.Options[i].Description
			}
			look := f.th.Label
			if i == st.row {
				look = f.th.Selected
			}
			label = look.Render(label)
			one := label
			if desc != "" {
				desc = f.th.Description.Render(desc)
				one += "  " + desc
			}
			// The description goes beside the label when both fit on
			// one row, and under it otherwise.
			var rows []string
			if f.m.VisibleWidth(head+one) <= width && !strings.Contains(one, "\n") {
				rows = []string{head + one}
			} else {
				rows = f.wrap(label, width, head, pad)
				if desc != "" {
					rows = append(rows, f.wrap(desc, width, pad, pad)...)
				}
			}
			for j, r := range rows {
				b.rows = append(b.rows, brow{text: r, pointer: i == st.row && j == 0, cursor: -1})
			}
		}
		if other && (f.typing || !st.other.IsEmpty()) {
			// The field, while it is open or holds something.
			fv := st.other.Render(max(width-len(pad), 1))
			for j, l := range fv.Lines {
				row := brow{text: pad + l, cursor: -1}
				if j == fv.CursorRow {
					row.cursor = len(pad) + fv.CursorCol
				}
				b.rows = append(b.rows, row)
			}
		}
		if i == st.row {
			last = len(b.rows) - 1
		}
	}
	return b, first, max(last, first)
}
