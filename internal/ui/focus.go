package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/gnosh-ai/gnosh/internal/agent"
	"github.com/gnosh-ai/gnosh/internal/app"
	"github.com/gnosh-ai/gnosh/internal/diff"
)

// Focus view, as Claude Code's /focus: a quieter screen, in which each
// run of tool calls is written as one line that sums it up (the files read,
// the files edited with the lines added and taken out, the commands run,
// how many failed, how long the model thought) in place of the calls
// themselves. /focus turns it on and off and keeps the choice for every
// project (the viewMode option, "focus"); the transcript view, Ctrl+O,
// shows every call whole either way. A call in flight still shows in the
// live region, under a line that sums up the run so far, and so do the
// questions; a deliverable still stands on its own.
//
// Where gnosh differs: Claude Code draws its focus view over the whole
// screen, and keeps only a turn's last answer. gnosh writes into the
// terminal's scrollback, where nothing written can be taken back, so the
// model's words between calls stay where they were written, and only the
// calls and the thoughts fold.

// focusWords are the lines that turn focus view over: a bare /focus is
// sent to the session as /focus on or /focus off, by what the screen
// shows.
var focusWords = map[string]bool{"/focus": true}

// fold is what focus view holds of the run of calls under way, until
// something else is written into the scrollback.
type fold struct {
	calls, failed int
	// kinds counts the calls by kind (foldKind), and order holds the kinds
	// as they first came.
	kinds map[string]int
	order []string
	// edited are the files the edits changed, with the lines added and
	// taken out in all.
	edited         map[string]bool
	added, removed int
	// thought is how long the model thought, in all.
	thought time.Duration
}

// empty reports whether the fold holds nothing to write.
func (f *fold) empty() bool { return f.calls == 0 && f.thought == 0 }

// foldKind is the kind of call a tool's call counts as in a fold line.
func foldKind(name string) string {
	switch name {
	case "Read":
		return "read"
	case "Edit", "MultiEdit", "Write", "NotebookEdit":
		return "edit"
	case "Bash", "Monitor":
		return "command"
	case "Grep", "Glob":
		return "search"
	case "WebFetch":
		return "fetch"
	case "WebSearch":
		return "websearch"
	case "Agent", "Task", "SendMessage":
		return "agent"
	case "gnovm_run":
		return "gno"
	}
	return "other"
}

// add takes in a call that ended, for the fold line.
func (f *fold) add(ev *agent.ToolEndEvent) {
	f.calls++
	res := ev.Result
	if ev.Denied || ev.Interrupted || res.IsError {
		f.failed++
		return
	}
	kind := foldKind(ev.Name)
	if f.kinds == nil {
		f.kinds = map[string]int{}
	}
	if f.kinds[kind] == 0 {
		f.order = append(f.order, kind)
	}
	f.kinds[kind]++
	if d := res.Display; kind == "edit" && d != nil && d.Kind == "diff" && d.Path != "" {
		if f.edited == nil {
			f.edited = map[string]bool{}
		}
		f.edited[d.Path] = true
		st := diff.Compare(d.Before, d.After, diff.Options{}).Stats
		f.added += st.Added
		f.removed += st.Removed
	}
}

// words says what the fold holds, in a line without its mark: "Read 3
// files, edited 2 files +40 -12, ran 4 commands".
func (f *fold) words() string {
	var parts []string
	if f.thought > 0 {
		parts = append(parts, "thought for "+span(f.thought))
	}
	for _, kind := range f.order {
		n := f.kinds[kind]
		switch kind {
		case "read":
			parts = append(parts, "read "+count(n, "file", "files"))
		case "edit":
			if len(f.edited) == 0 {
				parts = append(parts, "made "+count(n, "edit", "edits"))
				break
			}
			parts = append(parts, fmt.Sprintf("edited %s +%d -%d", count(len(f.edited), "file", "files"), f.added, f.removed))
		case "command":
			parts = append(parts, "ran "+count(n, "command", "commands"))
		case "search":
			parts = append(parts, "searched for "+count(n, "pattern", "patterns"))
		case "fetch":
			parts = append(parts, "fetched "+count(n, "page", "pages"))
		case "websearch":
			parts = append(parts, "searched the web "+count(n, "time", "times"))
		case "agent":
			parts = append(parts, "ran "+count(n, "sub-agent", "sub-agents"))
		case "gno":
			parts = append(parts, "made "+count(n, "Gno call", "Gno calls"))
		default:
			parts = append(parts, "made "+count(n, "other call", "other calls"))
		}
	}
	if f.failed > 0 && len(f.order) == 0 {
		parts = append(parts, count(f.failed, "call", "calls"))
	}
	line := strings.Join(parts, ", ")
	if line != "" {
		line = strings.ToUpper(line[:1]) + line[1:]
	}
	return line
}

// foldLine draws the fold as a line: so far says the run is still under
// way, for the live region.
func (s *session) foldLine(so bool) string {
	th := &s.th
	f := &s.fold
	mark := th.Tool.Render(th.ToolMark)
	if f.calls == 0 {
		mark = th.Thinking.Render(th.ThinkMark)
	}
	line := mark + " "
	if so {
		line += th.Quiet.Render("so far: ")
	}
	line += f.words()
	if f.failed > 0 {
		failed := fmt.Sprintf("%d failed", f.failed)
		switch {
		case f.failed == f.calls && f.calls == 1:
			failed = "failed"
		case f.failed == f.calls:
			failed = "all failed"
		}
		line += th.Quiet.Render(th.Sep) + th.Fail.Render(failed)
	}
	return line
}

// writeFold writes the fold into the scrollback as its line, and empties
// it. It is called as anything else is written (begin). The line says how
// to see what it folds when that fits on it.
func (s *session) writeFold() {
	if s.fold.empty() {
		return
	}
	line := s.foldLine(false)
	hint := s.transcriptHint(s.th.Sep, "shows them")
	if s.fold.calls == 0 {
		hint = s.transcriptHint(s.th.Sep, "shows it")
	}
	if with := line + s.th.Quiet.Render(hint); hint != "" && s.m.VisibleWidth(with) <= s.cols {
		line = with
	}
	s.fold = fold{}
	s.begin(blkToolLine)
	s.put(wrap(s.m, line, s.cols, "", "  ")...)
}

// foldsCall reports whether focus view takes the call that ended into the
// fold: every call but a deliverable, which is the thing the person asked
// for and stands on its own, and one a hook said something about or one
// with notes for the person (the new problems an edit brought), which is
// written whole so that what was said is seen (the sandbox review).
func (s *session) foldsCall(c *call, ev *agent.ToolEndEvent) bool {
	if !s.focus || len(c.hooks) > 0 || len(ev.Result.Notes) > 0 {
		return false
	}
	_, _, ok := app.DeliverableOf(ev.Result.Meta)
	return !ok || ev.Denied || ev.Interrupted || ev.Result.IsError
}

// setView puts a viewMode in force: "focus" folds the calls and the
// thoughts, "verbose" shows them whole as they come (detail), and anything
// else is neither. A fold under way is written first.
func (s *session) setView(mode string) {
	if s.focus && mode != "focus" {
		s.writeFold()
	}
	s.focus = mode == "focus"
	s.detail = mode == "verbose"
}

// unfocused is the view focus view is left for: verbose when the verbose
// option is on, else the default.
func (s *session) unfocused() string {
	if s.info.Verbose {
		return "verbose"
	}
	return "default"
}

// viewOf is the viewMode the interface starts with: the option when it was
// set, else the verbose option's word.
func viewOf(info app.SessionInfo) string {
	if info.ViewMode != "" {
		return info.ViewMode
	}
	if info.Verbose {
		return "verbose"
	}
	return "default"
}

// focusSet reads a /focus line for the view it asks for: on is focus view.
func focusSet(line string) (on, ok bool) {
	words := strings.Fields(line)
	if len(words) != 2 || !strings.EqualFold(words[0], "/focus") {
		return false, false
	}
	switch strings.ToLower(words[1]) {
	case "on":
		return true, true
	case "off":
		return false, true
	}
	return false, false
}
