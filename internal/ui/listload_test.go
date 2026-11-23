package ui

import (
	"strings"
	"testing"

	"github.com/gnosh-ai/gnosh/internal/agent"
	"github.com/gnosh-ai/gnosh/internal/app"
	"github.com/gnosh-ai/gnosh/internal/text"
)

// rewindControls are a test's controls that list prompts to go back to,
// and count how often the list was read.
type rewindControls struct {
	*controls
	points []app.RewindPoint
	reads  *int
}

func (c rewindControls) RewindPoints() []app.RewindPoint {
	*c.reads++
	return c.points
}

// The list Esc Esc opens is read away from the loop, so that a long
// session's file does not hold the screen still. What is typed while it
// is read is for the list, as had it come at once: Down and Enter pick in
// it, and do not take a prompt back into the box and send it. Ctrl+C lets
// a list that is slow to come go, and so does a question that comes
// first: what is typed next is the question's.
func TestRewindListIsReadAwayFromTheLoop(t *testing.T) {
	g := newRig(t, 100, 24, text.NoColor)
	reads := 0
	g.s.c = rewindControls{g.c, []app.RewindPoint{
		{ID: "u1", Text: "the first prompt", Time: stamp},
		{ID: "u2", Text: "the second prompt", Time: stamp},
	}, &reads}
	g.keys(`"an earlier prompt" <Enter>`)
	g.hold = true

	g.keys(`<Esc> <Esc>`)
	g.liveLacks("Go back to before which prompt?")
	g.keys(`<Down> <Enter>`)
	if g.box() != "" || len(g.c.sent()) != 1 {
		t.Fatalf("keys typed while the list was read went to the box: it holds %q, %d prompts sent", g.box(), len(g.c.sent()))
	}
	g.release()
	g.liveHas("Go back to before: the first prompt", "Go back, the conversation only")
	g.keys(`<Esc>`)
	g.liveLacks("Go back to before")

	g.keys(`<Esc> <Esc> <Ctrl+C>`)
	g.release()
	g.liveLacks("Go back to before which prompt?")
	if g.s.hint != "The list was not opened: Esc Esc opens it again." {
		t.Fatalf("after Ctrl+C the hint is %q", g.s.hint)
	}

	g.keys(`<Esc> <Esc>`)
	g.ev(&agent.QuestionEvent{Question: permission("q1")})
	g.release()
	g.liveHas("Allow Bash?").liveLacks("Go back to before which prompt?")
	if g.s.hint != "A question came first: Esc Esc opens the list again." {
		t.Fatalf("after a question came the hint is %q", g.s.hint)
	}
	if reads != 3 {
		t.Fatalf("the list was read %d times, want 3", reads)
	}
}

// The confirmation of a rewind takes no key typed before it could be
// seen: Esc Esc Enter Enter, typed while the list was read, picks the
// newest prompt and must not put the files back unseen. A long prompt is cut in its title, so
// the files and the choices keep their rows; the files are shown by their
// paths, with marks that turn text around written out; and a prompt whose
// files cannot be put back says why (the rewind review of 2026-11-19).
func TestRewindConfirmation(t *testing.T) {
	g := newRig(t, 100, 24, text.NoColor)
	reads := 0
	long := strings.Repeat("a long pasted prompt ", 150)
	g.s.c = rewindControls{g.c, []app.RewindPoint{
		{ID: "u1", Text: "the first prompt", Time: stamp, FilesWhy: "the copies of what the files held before that prompt were let go"},
		{ID: "u2", Text: long, Time: stamp, Files: []string{"notes.txt", "~/.zshrc", "evil\u202etxt.exe"}},
	}, &reads}
	g.keys(`"an earlier prompt" <Enter>`)
	g.hold = true
	g.keys(`<Esc> <Esc> <Enter> <Enter>`)
	g.release()
	g.liveHas("Go back to before: a long pasted prompt", `notes.txt, ~/.zshrc, evil\u202Etxt.exe`, "Go back, and put the files back")
	commands := func() string {
		g.c.mu.Lock()
		defer g.c.mu.Unlock()
		return strings.Join(g.c.commands, "|")
	}
	if got := commands(); strings.Contains(got, "/rewind") {
		t.Fatalf("a key typed ahead answered the confirmation: %q ran", got)
	}
	// Keys typed once it is there are its own, at once.
	g.keys(`<Enter>`)
	g.release()
	if got := commands(); !strings.Contains(got, "/rewind 2 all") {
		t.Fatalf("the confirmation ran %q", got)
	}

	g = newRig(t, 100, 24, text.NoColor)
	g.s.c = rewindControls{g.c, []app.RewindPoint{
		{ID: "u1", Text: "the first prompt", Time: stamp, FilesWhy: "the copies of what the files held before that prompt were let go", InView: true},
		{ID: "u2", Text: "the second prompt", Time: stamp, InView: true},
	}, &reads}
	g.keys(`"an earlier prompt" <Enter>`)
	g.keys(`<Esc> <Esc>`)
	g.keys(`<Down> <Enter>`)
	g.liveHas("Go back to before: the first prompt", "No file can be put back: the copies of what the files held before that prompt were let go.",
		"Summarize from here")
	// (Nothing came before the first prompt to summarize.)
	g.liveLacks("put the files back", "Summarize up to here")
	g.keys(`<Esc>`)
	g.keys(`<Esc> <Esc>`)
	g.keys(`<Enter>`)
	g.liveHas("Go back to before: the second prompt", "Summarize from here", "Summarize up to here")
	// What the summary is to keep is typed on its row, after Tab.
	g.keys(`<Down> <Tab> "keep the API names" <Enter>`)
	g.c.mu.Lock()
	ran := strings.Join(g.c.commands, "|")
	g.c.mu.Unlock()
	if !strings.Contains(ran, "/rewind 2 summarize keep the API names") {
		t.Fatalf("Summarize from here with a note ran %q", ran)
	}
}
