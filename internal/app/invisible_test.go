package app

import (
	"testing"

	"github.com/gnosh-ai/gnosh/internal/app/apptest"
)

// The line front end takes invisible characters out of a typed line as
// the full interface does: a plain one goes without them, and one that
// could have hidden text, or that would turn into a command, is shown
// and not sent.
func TestPlainTakesOutInvisibleCharacters(t *testing.T) {
	w := apptest.NewWorld(t, apptest.Reply{Text: "Cleaned."})
	s := sit(t, w)
	s.see("Type a prompt and Enter.")
	s.typeLine("say\u200B it")
	s.see("Took out 1 invisible character before sending the line.")
	s.see("Cleaned.")
	if reqs := w.Model.Requests(); len(reqs) != 1 {
		t.Fatalf("%d requests", len(reqs))
	} else {
		has(t, "the request", string(reqs[0].Body), "say it")
	}
	s.typeLine("hi\U000E0069\U000E0067")
	s.see("Took out 2 invisible characters that could hide text or reorder it; nothing was sent. What is left: hi")
	s.typeLine("\u200B/exit")
	s.see("what is left reads as a command, so it was not run: /exit")
	if n := len(w.Model.Requests()); n != 1 {
		t.Errorf("%d requests: a line that was held back went out", n)
	}
	// A shell line that a blank of another kind begins is still one.
	s.typeLine("\u00A0!rm report\u200B.txt")
	s.see("from the shell line, which changes what it runs, so it was not run")
	s.typeLine("/exit")
	s.exit()
}
