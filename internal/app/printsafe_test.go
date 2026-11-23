package app

import (
	"strings"
	"testing"

	"github.com/gnosh-ai/gnosh/internal/app/apptest"
)

// A reply printed to a terminal may colour itself and do nothing else: an
// escape sequence that would write the clipboard or set the title is left
// out, as Claude Code does since 2.1.325. A pipe or a file gets the reply
// as it is.
func TestPrintToATerminalIsSafe(t *testing.T) {
	reply := "Done: \x1b[1mbold\x1b[0m\x1b]52;c;cm0gLXJmIH4=\x07 and\x1b]0;pwned\x07 more."
	w := apptest.NewWorld(t, apptest.Reply{Text: reply}, apptest.Reply{Text: reply})
	o := testOptions(w, "-p", "go")
	o.StdoutIsTerminal = true
	r := runWith(t, t.Context(), o).wantCode(t, 0)
	if want := "Done: \x1b[1mbold\x1b[0m and more."; strings.TrimSpace(r.out) != want {
		t.Errorf("printed to a terminal: %q, want %q", r.out, want)
	}
	r = run(t, w, "-p", "go").wantCode(t, 0)
	if strings.TrimSpace(r.out) != reply {
		t.Errorf("printed to a pipe: %q, want the reply as it is", r.out)
	}
}

// What a command says, printed to a terminal, is held to the same: /btw
// prints the model's words.
func TestCommandOutputToATerminalIsSafe(t *testing.T) {
	w := apptest.NewWorld(t, apptest.Reply{Text: "Noted."})
	run(t, w, "-p", "remember seven").wantCode(t, 0)
	w.Model.Add(apptest.Reply{Text: "It was seven.\x1b]52;c;cm0gLXJmIH4=\x07"})
	o := testOptions(w, "-c", "-p", "/btw which number?")
	o.StdoutIsTerminal = true
	r := runWith(t, t.Context(), o).wantCode(t, 0)
	if strings.Contains(r.out, "\x1b]52") || !strings.Contains(r.out, "It was seven.") {
		t.Errorf("/btw printed to a terminal: %q", r.out)
	}
}

// A reply printed to a terminal hides nothing ("conceal" is taken out of
// its styles) and leaves no style on after it: what comes next, gnosh's
// own lines and the person's shell, is not drawn in it (the terminal
// review of 2026-11-19).
func TestPrintToATerminalHidesNothingAndClosesItsStyle(t *testing.T) {
	w := apptest.NewWorld(t, apptest.Reply{Text: "Run: \x1b[8mrm -rf ~\x1b[31m red"})
	o := testOptions(w, "-p", "go")
	o.StdoutIsTerminal = true
	r := runWith(t, t.Context(), o).wantCode(t, 0)
	if strings.Contains(r.out, "\x1b[8m") || !strings.Contains(r.out, "Run: rm -rf ~\x1b[31m red") {
		t.Errorf("printed to a terminal: %q", r.out)
	}
	if i, j := strings.LastIndex(r.out, "\x1b[31m"), strings.LastIndex(r.out, "\x1b[0m"); j < i {
		t.Errorf("the reply's style was left on: %q", r.out)
	}
}
