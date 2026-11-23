package menu

import (
	"fmt"
	"strings"
	"testing"

	"github.com/gnosh-ai/gnosh/internal/term"
	"github.com/gnosh-ai/gnosh/internal/text"
)

// permission is a question of the kind gnosh asks before a command runs.
func permission() *Select {
	return NewSelect(SelectOptions{
		Title: "Run this command?",
		Body:  []string{"npm test -- --watch"},
		Options: []Option{
			{Label: "Allow once", Text: TextOptional, Placeholder: "a note for the model"},
			{Label: "Allow, and do not ask again for", Description: "Bash(npm test *)"},
			{Label: "Deny", Key: 'n', Text: TextOptional},
			{Label: "Deny, and tell the model why", Text: TextRequired, Placeholder: "what it should do instead"},
		},
		CancelHint: "deny",
	})
}

// numbered is a question with n plain options.
func numbered(n int, o SelectOptions) *Select {
	o.Title = "Pick one"
	for i := 1; i <= n; i++ {
		o.Options = append(o.Options, Option{Label: fmt.Sprintf("Option %d", i)})
	}
	return NewSelect(o)
}

func TestSelectView(t *testing.T) {
	g := newRig(t, 70, permission())
	g.screen(-1, 0,
		"Run this command?",
		"  npm test -- --watch",
		"> 1. Allow once",
		"  2. Allow, and do not ask again for  Bash(npm test *)",
		"  3. Deny (n)",
		"  4. Deny, and tell the model why",
		"Enter choose · ↑↓ move · Esc deny · Tab add a note · 1-4 pick")
	g.do(`<Down>`).open().screen(-1, 0,
		"Run this command?",
		"  npm test -- --watch",
		"  1. Allow once",
		"> 2. Allow, and do not ask again for  Bash(npm test *)",
		"  3. Deny (n)",
		"  4. Deny, and tell the model why",
		"Enter choose · ↑↓ move · Esc deny · 1-4 pick")

	// Narrow: long labels wrap under themselves, the line about an
	// option goes under it, and the line of keys keeps what fits.
	g = newRig(t, 30, permission())
	g.screen(-1, 0,
		"Run this command?",
		"  npm test -- --watch",
		"> 1. Allow once",
		"  2. Allow, and do not ask",
		"     again for",
		"     Bash(npm test *)",
		"  3. Deny (n)",
		"  4. Deny, and tell the model",
		"     why",
		"Enter choose · ↑↓ move")

	// A question with no title and no body is its options.
	g = newRig(t, 40, NewSelect(SelectOptions{Options: []Option{{Label: "One"}, {Label: "Two"}}, Selected: 1}))
	g.screen(-1, 0, "  1. One", "> 2. Two", "Enter choose · ↑↓ move · Esc cancel")
	// And one with no options is nothing to answer.
	g = newRig(t, 40, NewSelect(SelectOptions{Title: "Nothing to choose"}))
	g.screen(-1, 0, "Nothing to choose")
	if r := g.p.Handle(key("Enter")); r.Handled || r.Done {
		t.Errorf("Enter on a question with no options: %+v", r)
	}
}

// The option the cursor keys are on is marked without colour, and with
// colour has the look of the theme.
func TestSelectLooks(t *testing.T) {
	th := DefaultTheme(text.TrueColor)
	s := NewSelect(SelectOptions{Title: "Sure?", Options: []Option{{Label: "Yes", Key: 'y'}, {Label: "No", Description: "leave it"}}, Theme: th})
	v := s.Render(40)
	want := []string{
		th.Title.Render("Sure?"),
		th.PointerStyle.Render(">") + " " + th.Description.Render("1.") + " " + th.Selected.Render("Yes") + " " + th.Description.Render("(y)"),
		"  " + th.Description.Render("2.") + " " + th.Label.Render("No") + "  " + th.Description.Render("leave it"),
		th.Hint.Render("Enter choose · ↑↓ move · Esc cancel"),
	}
	if strings.Join(v.Lines, "\n") != strings.Join(want, "\n") {
		t.Errorf("view:\n%q\nwant:\n%q", v.Lines, want)
	}
}

// Every key that moves, going round at the ends.
func TestSelectMoves(t *testing.T) {
	s := numbered(4, SelectOptions{})
	g := newRig(t, 40, s)
	for _, c := range []struct {
		key  string
		want int
	}{
		{`<Down>`, 1}, {`"j"`, 2}, {`<Tab>`, 3}, {`<Down>`, 0}, {`<Up>`, 3}, {`"k"`, 2}, {`<Shift+Tab>`, 1},
		{`<Ctrl+P>`, 0}, {`<Ctrl+P>`, 3}, {`<Ctrl+N>`, 0}, {`<End>`, 3}, {`<Tab>`, 0}, {`<End>`, 3}, {`<Home>`, 0},
		{`<Ctrl+I>`, 1}, {`<Shift+Tab>`, 0}, {`<Shift+Tab>`, 3},
	} {
		g.do(c.key).open()
		if s.Selected() != c.want {
			t.Fatalf("after %s the cursor is on option %d, want %d", c.key, s.Selected(), c.want)
		}
	}
	g.screen(-1, 0, "Pick one", "  1. Option 1", "  2. Option 2", "  3. Option 3", "> 4. Option 4", "Enter choose · ↑↓ move · Esc cancel")
}

// On a question about letting something happen the cursor keys stop at
// the ends, and a number past the last option goes to the no without
// choosing it.
func TestSelectNoWrap(t *testing.T) {
	s := numbered(3, SelectOptions{NoWrap: true, RefuseRow: 3})
	g := newRig(t, 40, s)
	for _, c := range []struct {
		key  string
		want int
	}{
		{`<Up>`, 0}, {`"k"`, 0}, {`<End>`, 2}, {`<Down>`, 2}, {`"j"`, 2}, {`<Tab>`, 2}, {`<Home>`, 0}, {`"7"`, 2}, {`<Up>`, 1},
	} {
		if res := g.do(c.key).last; res.Done {
			t.Fatalf("%s chose option %d", c.key, res.Index)
		}
		if s.Selected() != c.want {
			t.Fatalf("after %s the cursor is on option %d, want %d", c.key, s.Selected(), c.want)
		}
	}
}

// Enter, the number and the option's own key all choose.
func TestSelectChooses(t *testing.T) {
	for _, k := range []string{`<Down> <Down> <Enter>`, `"3"`, `<End> <Up> <Ctrl+M>`, `<Down>*2 <Ctrl+J>`} {
		newRig(t, 40, numbered(4, SelectOptions{})).do(k).chose(2, "")
	}
	keyed := func() *Select {
		return NewSelect(SelectOptions{Title: "Which?", Options: []Option{
			{Label: "Apple", Key: 'a'}, {Label: "Durian", Key: 'D'}, {Label: "Jackfruit", Key: 'j'}, {Label: "Kiwi", Key: '2'},
		}})
	}
	newRig(t, 40, keyed()).do(`"a"`).chose(0, "")
	newRig(t, 40, keyed()).do(`"A"`).chose(0, "")
	newRig(t, 40, keyed()).do(`"d"`).chose(1, "")
	// An option's own key comes before j and k and before the numbers.
	newRig(t, 40, keyed()).do(`"j"`).chose(2, "")
	newRig(t, 40, keyed()).do(`"2"`).chose(3, "")
	newRig(t, 40, keyed()).do(`"k"`).open()

	// Only the first nine have numbers.
	newRig(t, 40, numbered(12, SelectOptions{})).do(`"9"`).chose(8, "")
	g := newRig(t, 40, numbered(12, SelectOptions{})).do(`<End>`)
	g.screen(-1, 0,
		"Pick one",
		"  1. Option 1", "  2. Option 2", "  3. Option 3", "  4. Option 4", "  5. Option 5", "  6. Option 6",
		"  7. Option 7", "  8. Option 8", "  9. Option 9", "     Option 10", "     Option 11", ">    Option 12",
		"Enter choose · ↑↓ move · Esc cancel")
	g.do(`<Enter>`).chose(11, "")
}

// Esc and Ctrl+C cancel, and that is never a choice.
func TestSelectCancels(t *testing.T) {
	for _, k := range []string{`<Esc>`, `<Ctrl+C>`, `<Down> <Esc>`, `<Down> <Down> <Ctrl+C>`} {
		newRig(t, 70, permission()).do(k).cancelled()
	}
}

// What means nothing to a list is handed back and changes nothing.
func TestSelectUnhandled(t *testing.T) {
	s := numbered(3, SelectOptions{})
	g := newRig(t, 40, s)
	g.do(`<Down>`)
	for _, k := range []string{`<F5>`, `<Ctrl+L>`, `<Left>`, `<Right>`, `<Space>`, `<Backspace>`, `"x"`, `"7"`, `"0"`, `<Paste "2">`, `<Focus>`, `<Resize 10x10>`, `<Ctrl+U>`, `<Alt+Enter>`, `<Shift+Enter>`} {
		g.do(k)
		if g.last.Handled || g.last.Done || g.last.Index != -1 || s.Selected() != 1 {
			t.Errorf("%s: %+v, cursor on option %d", k, g.last, s.Selected())
		}
	}
}

// A long list scrolls to follow the cursor and marks where options are
// hidden. The mark goes where the pointer would be, so the option with
// the pointer is kept off the edge while there is more beyond it.
func TestSelectLongList(t *testing.T) {
	s := numbered(15, SelectOptions{MaxHeight: 8})
	g := newRig(t, 40, s)
	const keys = "Enter choose · ↑↓ move · Esc cancel"
	g.screen(-1, 0, "Pick one",
		"> 1. Option 1", "  2. Option 2", "  3. Option 3", "  4. Option 4", "  5. Option 5", "↓ 6. Option 6", keys)
	g.do(`<Down>*4`).screen(-1, 0, "Pick one",
		"  1. Option 1", "  2. Option 2", "  3. Option 3", "  4. Option 4", "> 5. Option 5", "↓ 6. Option 6", keys)
	g.do(`<Down>`).screen(-1, 0, "Pick one",
		"↑ 2. Option 2", "  3. Option 3", "  4. Option 4", "  5. Option 5", "> 6. Option 6", "↓ 7. Option 7", keys)
	g.do(`<Up>*3`).screen(-1, 0, "Pick one",
		"↑ 2. Option 2", "> 3. Option 3", "  4. Option 4", "  5. Option 5", "  6. Option 6", "↓ 7. Option 7", keys)
	g.do(`<Up>`).screen(-1, 0, "Pick one",
		"  1. Option 1", "> 2. Option 2", "  3. Option 3", "  4. Option 4", "  5. Option 5", "↓ 6. Option 6", keys)
	g.do(`<Up>`).screen(-1, 0, "Pick one",
		"> 1. Option 1", "  2. Option 2", "  3. Option 3", "  4. Option 4", "  5. Option 5", "↓ 6. Option 6", keys)
	g.do(`<Up>`).screen(-1, 0, "Pick one",
		"↑    Option 10", "     Option 11", "     Option 12", "     Option 13", "     Option 14", ">    Option 15", keys)
	g.do(`<Enter>`).chose(14, "")

	// The room can change while the question is up.
	s = numbered(15, SelectOptions{})
	g = newRig(t, 40, s)
	g.do(`<Down>*7`)
	s.SetMaxHeight(5)
	g.screen(-1, 0, "Pick one", "↑ 7. Option 7", "> 8. Option 8", "↓ 9. Option 9", keys)
	s.SetMaxHeight(0)
	if n := len(g.p.Render(40).Lines); n != 17 {
		t.Errorf("%d rows at the usual height, want 17", n)
	}
}

// A body too tall for the room scrolls, and the options stay in view.
func TestSelectBody(t *testing.T) {
	var body []string
	for i := 1; i <= 30; i++ {
		body = append(body, fmt.Sprintf("line %d", i))
	}
	s := NewConfirm(ConfirmOptions{Title: "Apply this change?", Body: body, MaxHeight: 12})
	g := newRig(t, 70, s)
	const keys = "Enter choose · ↑↓ move · Esc cancel · 1-2 pick · PgUp/PgDn scroll"
	g.screen(-1, 0, "Apply this change?",
		"  line 1", "  line 2", "  line 3", "  line 4", "  line 5", "  line 6", "  line 7", "↓ line 8",
		"  1. Yes (y)", "> 2. No (n)", keys)
	g.do(`<PgDn>`).open().screen(-1, 0, "Apply this change?",
		"↑ line 8", "  line 9", "  line 10", "  line 11", "  line 12", "  line 13", "  line 14", "↓ line 15",
		"  1. Yes (y)", "> 2. No (n)", keys)
	g.do(`<Shift+Down>`).screen(-1, 0, "Apply this change?",
		"↑ line 9", "  line 10", "  line 11", "  line 12", "  line 13", "  line 14", "  line 15", "↓ line 16",
		"  1. Yes (y)", "> 2. No (n)", keys)
	g.do(`<PgDn>*9`).screen(-1, 0, "Apply this change?",
		"↑ line 23", "  line 24", "  line 25", "  line 26", "  line 27", "  line 28", "  line 29", "  line 30",
		"  1. Yes (y)", "> 2. No (n)", keys)
	g.do(`<Shift+Up>`).screen(-1, 0, "Apply this change?",
		"↑ line 22", "  line 23", "  line 24", "  line 25", "  line 26", "  line 27", "  line 28", "↓ line 29",
		"  1. Yes (y)", "> 2. No (n)", keys)
	g.do(`<PgUp>*9`).screen(-1, 0, "Apply this change?",
		"  line 1", "  line 2", "  line 3", "  line 4", "  line 5", "  line 6", "  line 7", "↓ line 8",
		"  1. Yes (y)", "> 2. No (n)", keys)
	g.do(`"y"`).chose(0, "")

	// Body lines are styled by the caller; long ones wrap, tabs are
	// expanded, and their styles end with each row.
	red := text.Style{}.Fg(text.Red).In(text.ANSI16)
	s = NewSelect(SelectOptions{
		Title:   "Run?",
		Body:    []string{red.Render("rm -rf build dist node_modules") + " && make", "\tindented"},
		Options: []Option{{Label: "Yes"}, {Label: "No"}},
	})
	g = newRig(t, 24, s)
	g.screen(-1, 0, "Run?",
		"  rm -rf build dist",
		"  node_modules && make",
		"          indented",
		"> 1. Yes", "  2. No", "Enter choose · ↑↓ move")
	v := g.p.Render(24)
	if v.Lines[1] != "  "+red.Render("rm -rf build dist") || !strings.HasPrefix(v.Lines[2], "  "+red.Render("node_modules")) {
		t.Errorf("styled body rows: %q, %q", v.Lines[1], v.Lines[2])
	}
}

// An option that is its text: choosing it opens a field, and Enter in
// the field is the choice.
func TestSelectTextRequired(t *testing.T) {
	const typing = "Enter choose · Esc back · Shift+Enter new line · Ctrl+C discard"
	head := []string{"Run this command?", "  npm test -- --watch", "  1. Allow once", "  2. Allow, and do not ask again for  Bash(npm test *)", "  3. Deny (n)"}
	with := func(rows ...string) []string { return append(append([]string{}, head...), rows...) }

	s := permission()
	g := newRig(t, 70, s)
	g.do(`<End> <Enter>`).open().screen(6, 7, with(
		"> 4. Deny, and tell the model why",
		"     | what it should do instead",
		typing)...)
	if !s.Typing() {
		t.Fatal("the field is not open")
	}
	// In the field, letters and digits are text and choose nothing.
	g.do(`"use yarn 1"`).open().screen(6, 17, with("> 4. Deny, and tell the model why", "     | use yarn 1", typing)...)
	g.do(`<Enter>`).chose(3, "use yarn 1")

	// The number and Tab open it too. Enter with nothing typed stays.
	g = newRig(t, 70, permission())
	g.do(`"4" <Enter>`).open()
	g.do(`"x" <Backspace> <Enter>`).open()
	g.do(`"  " <Enter>`).open()
	g.do(`"go" <Enter>`).chose(3, "  go")
	g = newRig(t, 70, permission())
	g.do(`<Up> <Tab> "tabbed" <Enter>`).chose(3, "tabbed")

	// Esc goes back to the list; what was typed stays with the option
	// and is there when the field is opened again.
	s = permission()
	g = newRig(t, 70, s)
	g.do(`"4" "half" <Esc>`).open().screen(-1, 0, with(
		"> 4. Deny, and tell the model why",
		"     | half",
		"Enter choose · ↑↓ move · Esc deny · 1-4 pick")...)
	if s.Typing() {
		t.Fatal("the field is still open")
	}
	g.do(`<Enter> " done" <Enter>`).chose(3, "half done")
	// Tab goes back too, and Esc on the list is still the way out.
	g = newRig(t, 70, permission())
	g.do(`"4" "half" <Tab>`).open().do(`<Esc>`).cancelled()
	// Ctrl+C goes back and throws away what was typed.
	g = newRig(t, 70, permission())
	g.do(`"4" "half" <Ctrl+C>`).open().screen(-1, 0, with(
		"> 4. Deny, and tell the model why",
		"Enter choose · ↑↓ move · Esc deny · 1-4 pick")...)
	g.do(`<Ctrl+C>`).cancelled()

	// Up and Down leave the field for the options beside it.
	s = permission()
	g = newRig(t, 70, s)
	g.do(`"4" "note" <Up>`).open()
	if s.Typing() || s.Selected() != 2 {
		t.Fatalf("after Up in the field: typing %v, cursor on option %d", s.Typing(), s.Selected())
	}
	g.do(`<Down> <Enter> <Down>`).open()
	if s.Typing() || s.Selected() != 0 {
		t.Fatalf("after Down in the field: typing %v, cursor on option %d", s.Typing(), s.Selected())
	}

	// The field grows: a new line, and a line too long for the terminal.
	g = newRig(t, 40, permission())
	g.do(`"4" "one" <Shift+Enter> "two two two two two two two two two two"`).open()
	v := g.p.Render(40)
	got := plainLines(v)
	want := []string{"     | one", "       two two two two two two two two", "       two two"}
	if strings.Join(got[len(got)-4:len(got)-1], "\n") != strings.Join(want, "\n") || v.CursorRow != len(got)-2 || v.CursorCol != 14 {
		t.Errorf("field rows:\n%s\ncursor row %d col %d", box(got), v.CursorRow, v.CursorCol)
	}
	g.do(`<Enter>`).chose(3, "one\ntwo two two two two two two two two two")
	// A paste goes into the field.
	g = newRig(t, 70, permission())
	g.do(`"4" <Paste "pasted\ntext"> <Enter>`).chose(3, "pasted\ntext")
}

// An option that may be given a note: Tab opens the field, and choosing
// the option sends the note along.
func TestSelectTextOptional(t *testing.T) {
	g := newRig(t, 70, permission())
	g.do(`<Tab>`).open().screen(3, 7,
		"Run this command?",
		"  npm test -- --watch",
		"> 1. Allow once",
		"     | a note for the model",
		"  2. Allow, and do not ask again for  Bash(npm test *)",
		"  3. Deny (n)",
		"  4. Deny, and tell the model why",
		"Enter choose · Esc back · Shift+Enter new line · Ctrl+C discard")
	g.do(`"then lint" <Enter>`).chose(0, "then lint")

	// Enter on an empty note chooses the option without one.
	newRig(t, 70, permission()).do(`<Tab> <Enter>`).chose(0, "")
	newRig(t, 70, permission()).do(`<Tab> "  " <Enter>`).chose(0, "")
	// A new line in the note is a new line, not the choice.
	g = newRig(t, 70, permission())
	g.do(`<Tab> <Ctrl+J>`).open().do(`<Shift+Enter>`).open().do(`"x" <Enter>`).chose(0, "\n\nx")
	// Tab closes the note and keeps it; Enter, the number and the key
	// then send it along.
	newRig(t, 70, permission()).do(`<Tab> "kept" <Tab> <Enter>`).chose(0, "kept")
	newRig(t, 70, permission()).do(`<Tab> "kept" <Esc> "1"`).chose(0, "kept")
	newRig(t, 70, permission()).do(`<Down>*2 <Tab> "because" <Shift+Tab> "n"`).chose(2, "because")
	// A note thrown away is not sent.
	newRig(t, 70, permission()).do(`<Tab> "oops" <Ctrl+C> <Enter>`).chose(0, "")
	// Without Tab, the option is chosen at once.
	newRig(t, 70, permission()).do(`<Enter>`).chose(0, "")
	newRig(t, 70, permission()).do(`"n"`).chose(2, "")
	// A note on one option is not sent with another.
	newRig(t, 70, permission()).do(`<Tab> "mine" <Tab> "2"`).chose(1, "")
}

func TestConfirm(t *testing.T) {
	// No is where the cursor starts, unless the caller says otherwise.
	g := newRig(t, 40, NewConfirm(ConfirmOptions{Title: "Delete the branch?"}))
	g.screen(-1, 0, "Delete the branch?", "  1. Yes (y)", "> 2. No (n)", "Enter choose · ↑↓ move · Esc cancel")
	g.do(`<Enter>`).chose(1, "")
	if g.last.Yes() {
		t.Error("Enter on the default was a yes")
	}
	for _, c := range []struct {
		keys string
		yes  bool
	}{{`"y"`, true}, {`"Y"`, true}, {`"n"`, false}, {`"1"`, true}, {`"2"`, false}, {`<Up> <Enter>`, true}} {
		g = newRig(t, 40, NewConfirm(ConfirmOptions{Title: "Delete the branch?"})).do(c.keys)
		if !g.last.Done || g.last.Cancelled || g.last.Yes() != c.yes {
			t.Errorf("%s: %+v, want yes %v", c.keys, g.last, c.yes)
		}
	}
	g = newRig(t, 40, NewConfirm(ConfirmOptions{Title: "Delete the branch?"})).do(`<Esc>`).cancelled()
	if g.last.Yes() {
		t.Error("a cancelled question was a yes")
	}
	g = newRig(t, 40, NewConfirm(ConfirmOptions{Title: "Go on?", DefaultYes: true, Yes: "Go on", No: "Stop here"}))
	g.screen(-1, 0, "Go on?", "> 1. Go on (y)", "  2. Stop here (n)", "Enter choose · ↑↓ move · Esc cancel")
	if g.do(`<Enter>`); !g.last.Yes() {
		t.Errorf("Enter with yes as the default: %+v", g.last)
	}
	// A result that is not an answer is not a yes.
	if (Result{}).Yes() || (Result{Handled: true}).Yes() {
		t.Error("an empty result was a yes")
	}
}

// Nothing a question is given can reach the terminal as a control
// sequence. Styles and links in the body are kept; everything else that
// a terminal would act on is drawn as a stand-in.
func TestQuestionsAreSafeToDraw(t *testing.T) {
	link := text.Hyperlink("https://example.com/docs", "the docs")
	red := text.Style{}.Fg(text.Red).In(text.ANSI16).Render("red")
	s := NewSelect(SelectOptions{
		Title: "Run\x1b[2J this?\nReally\x07?",
		Body:  []string{red + " and " + link + " and \x1b[H\x1b]0;title\x07 and \u009b31m", "a\rb\x00"},
		Options: []Option{
			{Label: "Yes\x1b[8m, and more\nbesides", Description: "does \x1b[31mthis\x1b[0m", Key: 'y'},
			{Label: "No"},
		},
		CancelHint: "de\x1bny",
	})
	g := newRig(t, 78, s)
	g.screen(-1, 0,
		"Run^[[2J this?",
		"Really^G?",
		"  red and the docs and ^[[H^[]0;title^G and <U+009B>31m",
		"  a^Mb^@",
		"> 1. Yes^[[8m, and more^Jbesides (y)  does ^[[31mthis^[[0m",
		"  2. No",
		"Enter choose · ↑↓ move · Esc de^[ny · 1-2 pick")
	v := g.p.Render(78)
	if !strings.Contains(v.Lines[2], red) || !strings.Contains(v.Lines[2], link) {
		t.Errorf("the body's style and link were not kept: %q", v.Lines[2])
	}

	// The model's own words in a form, and what the person typed.
	f := NewForm(FormOptions{
		ReviewTitle: "Send\x1b[2J?",
		Questions: []Question{
			{Header: "A\x1bB", Text: "Which\x1b[1m one?\nSecond line", Description: "about\x07 it", Options: []Option{{Label: "On\x1be", Description: "t\x1bwo"}}},
			{Header: "Typed", Text: "Type"},
		},
	})
	g = newRig(t, 78, f)
	g.screen(-1, 0,
		"[A^[B] Typed  Review",
		"Which^[[1m one?",
		"Second line",
		"about^G it",
		"> 1. ( ) On^[e  t^[wo",
		"  2. ( ) Other, in your own words",
		"Enter choose · ↑↓ move · Esc cancel · ←/→ questions")
	g.do(`"1" <Paste "typed \x1b[31m\ttext\nmore"> <Enter>`).screen(-1, 0,
		" A^[B ✓  Typed ✓ [Review]",
		"Send^[[2J?",
		"  A^[B: On^[e",
		"  Typed: typed ^[[31m text more",
		"Enter send · ←/→ change an answer · Esc cancel")

	in := NewInput(InputOptions{Title: "Na\x1bme", Body: []string{"\x1b[2Jbody"}, Placeholder: "pl\x1bace"})
	newRig(t, 60, in).screen(2, 4, "Na^[me", "  ^[[2Jbody", "  | pl^[ace", "Enter answer · Esc cancel · Shift+Enter new line")
	sec := NewSecret(SecretOptions{Title: "Un\x1block", Body: []string{"\x07body"}, Label: "Pass\x1b"})
	newRig(t, 60, sec).screen(2, 10, "Un^[lock", "  ^Gbody", "> Pass^[:", "Enter confirm · Esc cancel · what you type is not shown")
}

// A key that is being held down moves the cursor but never chooses.
func TestHeldKeysDoNotChoose(t *testing.T) {
	held := func(name string) term.Key {
		k := key(name)
		k.Repeat = true
		return k
	}
	s := permission()
	for _, name := range []string{"Enter", "Ctrl+M", "1", "2", "n", "N"} {
		if r := s.Handle(held(name)); r.Done || !r.Handled || r.Index != -1 {
			t.Errorf("held %s on a list: %+v", name, r)
		}
	}
	if r := s.Handle(held("Down")); !r.Handled || s.Selected() != 1 {
		t.Errorf("held Down: %+v, cursor on option %d", r, s.Selected())
	}
	if r := s.Handle(held("j")); !r.Handled || s.Selected() != 2 {
		t.Errorf("held j: %+v, cursor on option %d", r, s.Selected())
	}
	if r := s.Handle(key("Enter")); !r.Done || r.Index != 2 {
		t.Errorf("Enter pressed afresh: %+v", r)
	}

	f := survey(FormOptions{})
	for _, name := range []string{"Enter", "Space", "1", "2"} {
		if r := f.Handle(held(name)); r.Done || !r.Handled || f.Tab() != 0 || !f.Answers()[0].Empty() {
			t.Errorf("held %s on a form: %+v, tab %d, answer %+v", name, r, f.Tab(), f.Answers()[0])
		}
	}
	f.Handle(held("Left"))
	if r := f.Handle(held("Enter")); r.Done || f.Tab() != 3 {
		t.Errorf("held Enter on the review: %+v", r)
	}
	// In a text field a held key types, as it should.
	f = survey(FormOptions{})
	f.Handle(key("Right"))
	f.Handle(key("Right"))
	f.Handle(held("x"))
	f.Handle(held("x"))
	f.Handle(key("Enter"))
	if got := f.Answers()[2].Other; got != "xx" {
		t.Errorf("held x in a field typed %q", got)
	}
}
