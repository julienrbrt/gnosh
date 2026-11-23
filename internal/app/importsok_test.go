package app

import (
	"strings"
	"testing"

	"github.com/gnosh-ai/gnosh/internal/app/apptest"
)

// A project's instruction file that brings in a file from outside the
// project has the person asked, once for the project, as Claude Code asks:
// a yes loads it, in this session and the next ones, which are not asked
// again; the places keys are kept stay out all the same.
func TestOutsideImportsAreAskedOnce(t *testing.T) {
	w := apptest.NewWorld(t, apptest.Reply{Text: "One."}, apptest.Reply{Text: "Two."})
	w.Write(w.Home+"/shared/notes.md", "Prefer tabs, from the shared notes.\n")
	w.Write(w.Home+"/.ssh/id_test", "KEY-SECRET-9\n")
	w.Write("AGENTS.md", "Read @~/shared/notes.md and @~/.ssh/id_test.\n")
	s := sit(t, w)
	s.see("Load files from outside the project?")
	s.see("~/shared/notes.md")
	s.typeLine("1")
	s.see("Type a prompt and Enter.")
	s.typeLine("hi")
	s.see("One.")
	s.typeLine("/exit")
	s.exit()
	first := string(w.Model.Requests()[0].Body)
	has(t, "the first request", first, "Prefer tabs, from the shared notes.")
	hasNot(t, "the first request", first, "KEY-SECRET-9")
	// (The key is not offered in the question either.)
	out := s.out.String()
	if i, j := strings.Index(out, "Load files"), strings.Index(out, "1. Yes"); i < 0 || j < i || strings.Contains(out[i:j], "id_test") {
		t.Errorf("the question:\n%s", out)
	}

	s = sit(t, w)
	s.see("Type a prompt and Enter.")
	s.typeLine("hi again")
	s.see("Two.")
	s.typeLine("/exit")
	s.exit()
	if strings.Count(s.out.String(), "Load files from outside the project?") != 0 {
		t.Error("the question was put again")
	}
	has(t, "the next session's request", string(w.Model.Requests()[1].Body), "Prefer tabs, from the shared notes.")
}

// A no leaves the files out, and is not asked again or warned of at each
// start; a run nobody attends loads them only after a yes.
func TestOutsideImportsLeftOut(t *testing.T) {
	w := apptest.NewWorld(t, apptest.Reply{Text: "One."}, apptest.Reply{Text: "Two."})
	w.Write(w.Home+"/shared/notes.md", "Prefer tabs, from the shared notes.\n")
	w.Write("AGENTS.md", "Read @~/shared/notes.md.\n")
	r := run(t, w, "-p", "hi").wantCode(t, 0)
	has(t, "a run's warning", r.err, "outside the project")
	hasNot(t, "a run's request", string(w.Model.Requests()[0].Body), "from the shared notes")

	s := sit(t, w)
	s.see("Load files from outside the project?")
	s.typeLine("2")
	s.see("Type a prompt and Enter.")
	s.typeLine("hi")
	s.see("Two.")
	s.typeLine("/exit")
	s.exit()
	hasNot(t, "the request after a no", string(w.Model.Requests()[1].Body), "from the shared notes")

	w.Model.Add(apptest.Reply{Text: "Three."})
	r = run(t, w, "-p", "hi").wantCode(t, 0)
	hasNot(t, "a run's warnings after a no", r.err, "outside the project")
}
