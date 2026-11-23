package memjoin

import (
	"strings"
	"testing"

	"github.com/gnosh-ai/gnosh/internal/app/apptest"
	"github.com/gnosh-ai/gnosh/internal/prompt"
)

// plan is the reply that plans a task with two todos and enters it.
func plan() apptest.Reply {
	return reply(memCall("p1", `mem.Todo("tidy the docs", "fix the typo in README.md", "add a changelog line")`+".Push()"))
}

const (
	docs      = "tidy-docs"
	docsTypo  = "tidy-docs/fix-typo-readme-md"
	docsEntry = "tidy-docs/add-changelog-line"
)

// The fresh guard, end to end: mem refuses to pop a todo when nothing came
// between entering it and the pop. A read of mem is no work, a failed call
// is no work, and a call that did something is. A refused close is also
// what the turn's end sends the model back for, once.
func TestFreshGuardAndTheSendBack(t *testing.T) {
	w := newWorld(t,
		plan(),
		reply(memCall("c2", `mem.Next()`)),
		// Straight after entering: refused.
		reply(memCall("c3", `mem.Pop("README.md is fine as it is")`)),
		// A look at mem and a call that fails are no work: refused again.
		reply(memCall("c4", `mem.Show()`)),
		reply(call("c5", "Read", map[string]any{"file_path": "no-such-file.md"})),
		reply(memCall("c6", `mem.Pop("README.md is fine as it is")`)),
		// The model gives up and tells the person it is done.
		says("The typo is fixed."),
		// Sent back, it does the work, and the pop is kept.
		reply(call("c7", "Read", map[string]any{"file_path": "README.md"})),
		reply(memCall("c8", `mem.Pop("nothing to do: README.md holds no typo, as a read of it shows")`)),
		says("README.md needed no change."),
	)
	w.Write("README.md", "# The docs\n\nAll is well.\n")
	r := w.run("-y", "-p", "tidy the docs")
	r.wantCode(t, 0)
	w.script.done()
	reqs := w.script.requests()

	const refusal = "not popped: nothing came between your entering `" + docsTypo + "` \"fix the typo in README.md\" and this pop, so it is not done. Do it, or check that it is done, then pop it."
	first := lastResult(t, reqs[3])
	has(t, "the first pop's result", first, "ERROR — nothing from this call was kept: "+refusal)
	hasNot(t, "the first pop's result", first, "removed from your context")
	has(t, "the second pop's result", lastResult(t, reqs[6]), refusal)

	// What mem was told: the stack on every call, and the fresh todo only
	// where the session stood in it with nothing done.
	var told []string
	for _, req := range w.memRequests() {
		told = append(told, strings.Join(req.Frames, ">")+"|"+req.Fresh)
	}
	want := []string{
		"|",                                    // the plan, from the root
		docs + "|",                             // mem.Next(): no Pop( in the code, so not asked
		docs + ">" + docsTypo + "|" + docsTypo, // the pop straight after entering
		docs + ">" + docsTypo + "|",            // mem.Show()
		docs + ">" + docsTypo + "|" + docsTypo, // the pop after a look and a failed call
		docs + ">" + docsTypo + "|",            // the pop after a read: work was done
		docs + ">" + docsTypo + "|",            // the same request again, with the close approved
	}
	if strings.Join(told, "\n") != strings.Join(want, "\n") {
		t.Errorf("what mem was told of the stack and the fresh todo:\n%s\nwant:\n%s", strings.Join(told, "\n"), strings.Join(want, "\n"))
	}

	// The turn would have ended on "The typo is fixed." in a todo whose
	// close was refused with nothing done since: the model is sent back,
	// and the person is told.
	sent := lastUser(t, reqs[7])
	if want := prompt.Builtin().CloseSendBack(docsTypo, ""); sent != want {
		t.Errorf("the send-back is:\n%s\nwant:\n%s", sent, want)
	}
	has(t, "the send-back", sent, "mem refused to close `"+docsTypo+"`: nothing was done in it, and it is still open.", "Do not tell the user it is done.")
	has(t, "stderr", r.err, "mem refused to close `"+docsTypo+"`, since nothing was done in it; the agent goes back to it")

	// After real work the pop is kept, and the turn ends: the model is
	// sent back once a turn, and not again.
	kept := lastResult(t, reqs[9])
	has(t, "the kept pop's result", kept, "done `"+docsTypo+"`", "Everything since you entered `"+docsTypo+"` was removed from your context")
	if strings.Count(r.err, "the agent goes back to it") != 1 {
		t.Errorf("the model was sent back more than once:\n%s", r.err)
	}
	if got := strings.TrimSpace(r.out); !strings.HasSuffix(got, "README.md needed no change.") {
		t.Errorf("stdout = %q", got)
	}
}

// The close check, end to end: a todo entered and closed in one call with
// nothing done; a result that says a file changed which nothing in the
// todo changed or showed; and the review, asked of a small model, with its
// verdict remembered.
func TestCloseCheck(t *testing.T) {
	w := newWorld(t,
		plan(),
		// Entered and closed in one go, with nothing done since the plan.
		reply(memCall("c2", "mem.Next()\nmem.Pop(\"the typo is fixed\")")),
		// Entered, a file read, and then a claim about another file.
		reply(memCall("c3", `mem.Next()`)),
		reply(call("c4", "Read", map[string]any{"file_path": "NOTES.md"})),
		reply(memCall("c5", `mem.Pop("README.md now says colour, not color")`)),
		// The file the claim names is read: the claim check passes, and
		// the review refuses.
		reply(call("c6", "Read", map[string]any{"file_path": "README.md"})),
		reply(memCall("c7", `mem.Pop("README.md now says colour, not color")`)),
		// The same close again, unchanged: refused from memory, with no
		// second request to the model.
		reply(memCall("c8", `mem.Pop("README.md now says colour, not color")`)),
		// The edit is made, and the close holds.
		reply(call("c9", "Edit", map[string]any{"file_path": "README.md", "old_string": "color", "new_string": "colour"})),
		reply(memCall("c10", `mem.Pop("README.md now says colour, not color")`)),
		says("The typo is fixed."),
	)
	w.Write("README.md", "# The docs\n\nPick a color.\n")
	w.Write("NOTES.md", "notes\n")
	reviews := 0
	w.script.onSide = func(kind string, r apptest.Request) *apptest.Reply {
		if kind != sideReview {
			return nil
		}
		reviews++
		if reviews == 1 {
			return &apptest.Reply{Text: "Here is my verdict.\n```json\n{\"ok\": false, \"reason\": \"README.md was read and still says color.\"}\n```"}
		}
		return &apptest.Reply{Text: `{"ok": true, "reason": "the edit of README.md shows it"}`}
	}
	r := w.run("-y", "-p", "tidy the docs")
	r.wantCode(t, 0)
	w.script.done()
	reqs := w.script.requests()
	texts := prompt.Builtin()

	// Entered here.
	entered := lastResult(t, reqs[2])
	if want := "ERROR — nothing from this call was kept: " + texts.CloseEnteredHere(docsTypo); !strings.HasPrefix(entered, want) {
		t.Errorf("the result of entering and closing in one call:\n%s\nwant it to begin:\n%s", entered, want)
	}
	has(t, "the entered-here refusal", entered,
		"not popped: this call entered `"+docsTypo+"` and closed it with nothing done in it, so nothing from the call was kept and you are not in `"+docsTypo+"`.",
		"mem.Done(\""+docsTypo+"\", \"what came of it\").", "failed · mem · ")
	hasNot(t, "the entered-here refusal", entered, "done `"+docsTypo+"`")

	// The claim check.
	claim := lastResult(t, reqs[5])
	has(t, "the claim check's refusal", claim,
		"ERROR — nothing from this call was kept: not popped: nothing in `"+docsTypo+"` changed or showed `README.md`, which your result says changed. If the change is still to make, make it; if it was made before, read it here so it shows; then close `"+docsTypo+"` with what came of it. If you did it before you entered `"+docsTypo+"`, step out with mem.Leave(\"done before\") and close it by its name from where you did it: mem.Done(\""+docsTypo+"\", \"what came of it\").")

	// The review's refusal, in the model's own reason.
	refused := lastResult(t, reqs[7])
	has(t, "the review's refusal", refused,
		"ERROR — nothing from this call was kept: not popped: the check of `"+docsTypo+"` found: README.md was read and still says color. Do what is missing; if it is done already, show it here (read it, or run what checks it); then close `"+docsTypo+"` with what came of it.",
		"If it cannot be done now, mem.Leave(\"why\") leaves it open; either way, tell the user plainly how it stands.")
	again := lastResult(t, reqs[8])
	firstLine := func(s string) string { line, _, _ := strings.Cut(s, "\n"); return line }
	if firstLine(again) != firstLine(refused) {
		t.Errorf("the same close sent again was not refused with the same words:\n%s\n\n%s", refused, again)
	}

	// What the reviewer was shown, and how it was asked.
	sides := w.script.sides(sideReview)
	if len(sides) != 2 {
		t.Fatalf("the review was asked %d times, want twice: a close sent again unchanged is not reviewed again", len(sides))
	}
	asked := lastUser(t, sides[0])
	has(t, "what the reviewer was shown", asked,
		"The todo: fix the typo in README.md",
		"What the user asked in this turn:\ntidy the docs",
		"Files the agent's edits and writes changed in the todo: none",
		"The result the agent is closing the todo with, which is its claim:\n  README.md now says colour, not color",
		"the agent called Read: {\"file_path\":\"README.md\"}",
		"the tool returned: 1\t# The docs",
		"Is that result what came of the todo \"fix the typo in README.md\", and not of other work?")
	body := sides[0].JSON
	if body["temperature"] != float64(0) || body["max_tokens"] != float64(1500) {
		t.Errorf("the review was asked with temperature %v and room for %v tokens, want 0 and 1500", body["temperature"], body["max_tokens"])
	}
	if tools, _ := body["tools"].([]any); len(tools) != 0 {
		t.Errorf("the review was sent tools")
	}
	has(t, "what the second review was shown", lastUser(t, sides[1]),
		"Files the agent's edits and writes changed in the todo: README.md",
		// The reviewer reads what happened, the refusals among it.
		"the tool failed: ERROR — nothing from this call was kept: not popped: the check of `"+docsTypo+"` found: README.md was read and still says color.",
		"the agent called Edit: {\"file_path\":\"README.md\"")

	// The close that holds is kept, and the context is cut.
	kept := lastResult(t, reqs[10])
	has(t, "the kept pop's result", kept, "done `"+docsTypo+"`", "In it you edited README.md.")
}

// When the review cannot be read the close is kept unchecked, and the
// person is warned; with the option off no model is asked at all, and the
// free checks still run.
func TestCloseReviewFailsOpenAndCanBeTurnedOff(t *testing.T) {
	script := []apptest.Reply{
		plan(),
		reply(memCall("c2", `mem.Next()`)),
		reply(call("c3", "Read", map[string]any{"file_path": "README.md"})),
		reply(memCall("c4", `mem.Pop("looked it over: no typo")`)),
		says("No typo."),
	}
	t.Run("an answer that cannot be read", func(t *testing.T) {
		w := newWorld(t, script...)
		w.Write("README.md", "# The docs\n")
		w.script.onSide = func(kind string, r apptest.Request) *apptest.Reply {
			if kind == sideReview {
				return &apptest.Reply{Text: "I think it is probably fine."}
			}
			return nil
		}
		r := w.run("-y", "-p", "tidy the docs")
		r.wantCode(t, 0)
		has(t, "the pop's result", lastResult(t, w.script.requests()[4]), "done `"+docsTypo+"`")
		has(t, "stderr", r.err, "The close of `"+docsTypo+"` could not be checked, so it was kept unchecked: the review's answer could not be read: I think it is probably fine.")
	})
	t.Run("the option turned off", func(t *testing.T) {
		w := newWorld(t, script...)
		w.Write("README.md", "# The docs\n")
		w.Settings(`{"options": {"memReviewCloses": false}}`)
		r := w.run("-y", "-p", "tidy the docs")
		r.wantCode(t, 0)
		has(t, "the pop's result", lastResult(t, w.script.requests()[4]), "done `"+docsTypo+"`")
		if n := len(w.script.sides(sideReview)); n != 0 {
			t.Errorf("the review was asked %d times with the option off", n)
		}
		hasNot(t, "stderr", r.err, "could not be checked")
	})
}

// What a result claims of files (close-check.md section 3.3).
func TestClaims(t *testing.T) {
	for _, c := range []struct{ result, want string }{
		{"Median added in stats.go:25-36, tests pass", "stats.go made"},
		{"README.md:3 now says Get", "README.md"},
		{"README.md now says colour, not color", "README.md"},
		{"stats.go updated", "stats.go"},
		{"renamed klash to gnosh in README.md and docs/usage.md; grep finds nothing", "README.md docs/usage.md"},
		{"README.md is unchanged; fixed the port in internal/net/listen.go", "internal/net/listen.go"},
		{"README.md still references Fetch (not done yet)", ""},
		{"will update stats.go next", ""},
		{"go test ./... passes", ""},
		{"bumped the version to v1.4", ""},
		{"wrote the notes. See https://example.com/a.md for more", ""},
		{"created stats_test.go - it covers Median", "stats_test.go made"},
		{"nothing to do: README.md holds no typo", ""},
		{"looked at client.Get and server.go", ""},
		{"edited `cmd/root.go`, documented the flag in (README.md).", "cmd/root.go README.md"},
	} {
		var got []string
		for _, cl := range claims(c.result) {
			s := cl.file
			if cl.made {
				s += " made"
			}
			got = append(got, s)
		}
		if strings.Join(got, " ") != c.want {
			t.Errorf("claims(%q) = %q, want %q", c.result, strings.Join(got, " "), c.want)
		}
	}
}

// What counts as a change to mem, and so as work in a todo.
func TestChangesMem(t *testing.T) {
	for code, want := range map[string]bool{
		`mem.Show()`:                                  false,
		`mem.Find("port")`:                            false,
		`mem.Next()`:                                  false,
		`mem.Pop("done")`:                             false,
		`mem.Leave("later")`:                          false,
		`mem.Frame("x").Push()`:                       false,
		`mem.Fact("cmd/test")`:                        false,
		`mem.Thought("port")`:                         false,
		`mem.Fact("cmd/test", "go test ./...")`:       true,
		`mem.Fact("the tests need the race flag")`:    true,
		`mem.Think("the port is 8080")`:               true,
		`mem.Frame("x").Think("a note")`:              true,
		`mem.Todo("one more step")`:                   true,
		`mem.Done("x/y", "it holds")`:                 true,
		`mem.Frame("x").Parallel()`:                   true,
		`mem.Rule("gofumpt", "always")`:               true,
		`mem.Show(); mem.Later("fix the flaky test")`: true,
	} {
		if got := changesMem(code); got != want {
			t.Errorf("changesMem(%q) = %v, want %v", code, got, want)
		}
	}
}
