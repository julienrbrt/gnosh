package memjoin

import (
	"encoding/json"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gnosh-ai/gnosh/internal/core"
	gnovmtools "github.com/gnosh-ai/gnosh/internal/gnovm/tools"
	"github.com/gnosh-ai/gnosh/internal/prompt"
	"github.com/gnosh-ai/gnosh/internal/tools/fstools"
	"github.com/gnosh-ai/gnosh/internal/tools/shelltool"
	"github.com/gnosh-ai/gnosh/internal/transcript"
)

// This file lays a session's log out as the things that happened in it,
// in order, the way the checks read it: what the person typed, what the
// model said and called, what each call returned, and where the session
// entered and left a todo. It reads the whole conversation, the work of
// popped todos included: the checks judge what was done, not what the
// model is still sent.

// eventKind sorts the things that happen in a session.
type eventKind uint8

const (
	// evPrompt is a prompt the person typed.
	evPrompt eventKind = iota
	// evHidden is text the harness or a hook put on the user's side.
	evHidden
	// evSaid is the words of a reply of the model.
	evSaid
	// evCall is one tool call of a reply, with its result.
	evCall
	// evPush and evPop are the session entering and leaving a todo.
	evPush
	evPop
	// evSummary is a summary that stands for what came before it.
	evSummary
)

// event is one thing that happened in a session.
type event struct {
	kind eventKind
	// uuid is the entry it came from and time when that was written.
	uuid string
	time time.Time
	// reply numbers the model's replies from one; the words and the calls
	// of one reply share it. alone says the reply made no tool call, and
	// ended says it was cancelled or failed.
	reply int
	alone bool
	ended bool
	// text is the words of a prompt or a reply, a summary's text, or what
	// a call returned.
	text string

	// The rest is a call's: its id, its tool, its arguments, the path it
	// names, whether it has a result, whether that result is a failure,
	// and for a command its exit code.
	id       string
	tool     string
	input    json.RawMessage
	path     string
	answered bool
	failed   bool
	exit     int
	hasExit  bool

	// frame is the todo of a push or a pop, and id then the call that made
	// it.
	frame string
}

// mem reports whether a call event is a call on mem, and returns its code.
func (e *event) mem() (string, bool) {
	if e.kind != evCall || e.tool != gnovmtools.RunToolName {
		return "", false
	}
	return memCode(e.input)
}

// layout lays the conversation of a session out as events. session is the
// session whose pushes and pops count: a copy of another session's rows
// carries that session's.
func layout(entries []transcript.Entry, session string) []event {
	entries = transcript.Conversation(entries)
	// The result of every call, by the call's id.
	type answer struct {
		block *core.Block
		exit  int
		has   bool
	}
	answers := map[string]answer{}
	for i := range entries {
		e := &entries[i]
		if e.Type != transcript.TypeUser {
			continue
		}
		exit, has := exitCode(e)
		for k := range e.Message.Blocks {
			if b := &e.Message.Blocks[k]; b.Type == core.BlockToolResult {
				answers[b.ToolUseID] = answer{b, exit, has}
			}
		}
	}

	var out []event
	reply := 0
	for i := range entries {
		e := &entries[i]
		switch e.Type {
		case transcript.TypeUser:
			var words []string
			for _, b := range e.Message.Blocks {
				if b.Type == core.BlockText && strings.TrimSpace(b.Text) != "" {
					words = append(words, b.Text)
				}
			}
			if len(words) == 0 {
				continue
			}
			kind := evHidden
			if e.Source == transcript.SourcePrompt {
				kind = evPrompt
			}
			out = append(out, event{kind: kind, uuid: e.UUID, time: e.Timestamp, text: strings.Join(words, "\n")})
		case transcript.TypeAssistant:
			ended := e.Stop == core.StopCancelled || e.Stop == core.StopError
			if len(e.Message.Blocks) == 0 {
				if ended && len(out) > 0 {
					out[len(out)-1].ended = true
				}
				continue
			}
			reply++
			uses := e.Message.ToolUses()
			if text := strings.TrimSpace(e.Message.Text()); text != "" {
				out = append(out, event{kind: evSaid, uuid: e.UUID, time: e.Timestamp, reply: reply, alone: len(uses) == 0, ended: ended, text: text})
			}
			for _, u := range uses {
				ev := event{kind: evCall, uuid: e.UUID, time: e.Timestamp, reply: reply, ended: ended, id: u.ID, tool: u.Name, input: u.Input, path: callPath(u.Input)}
				if a, ok := answers[u.ID]; ok {
					ev.answered, ev.failed = true, a.block.IsError
					ev.text = core.TextOf(a.block.Content)
					ev.exit, ev.hasExit = a.exit, a.has
				}
				out = append(out, ev)
			}
		case transcript.TypeFrame:
			if e.SessionID != session {
				continue
			}
			switch e.Op {
			case transcript.OpPush:
				out = append(out, event{kind: evPush, uuid: e.UUID, time: e.Timestamp, frame: e.Frame, id: e.ToolUseID})
			case transcript.OpPop:
				out = append(out, event{kind: evPop, uuid: e.UUID, time: e.Timestamp, frame: e.Frame, id: e.ToolUseID, text: e.Result})
			}
		case transcript.TypeSummary:
			out = append(out, event{kind: evSummary, uuid: e.UUID, time: e.Timestamp, text: e.Summary})
		}
	}
	return out
}

// callPath is the file a call names: its file_path argument, or its path.
func callPath(input json.RawMessage) string {
	var args struct {
		FilePath string `json:"file_path"`
		Path     string `json:"path"`
	}
	if json.Unmarshal(input, &args) != nil {
		return ""
	}
	if args.FilePath != "" {
		return args.FilePath
	}
	return args.Path
}

// exitCode reads a command's exit code out of what the loop keeps beside
// a result. The loop keeps a result's facts under the entry's "loop" key;
// when they are not there, the caller goes by whether the result is a
// failure.
func exitCode(e *transcript.Entry) (int, bool) {
	raw, ok := e.Extra[loopKey]
	if !ok {
		return 0, false
	}
	var loop struct {
		Meta struct {
			ExitCode *float64 `json:"exitCode"`
		} `json:"meta"`
	}
	if json.Unmarshal(raw, &loop) != nil || loop.Meta.ExitCode == nil {
		return 0, false
	}
	return int(*loop.Meta.ExitCode), true
}

// turnStart returns the index of the event at which the turn of the event
// at index at began: the prompt the person typed that started it, with
// the hidden text that stands right before it. A typed prompt starts a
// turn when what comes before it is nothing, a summary, a reply that made
// no tool call, or anything at all after a reply that was cancelled or
// failed; any other typed prompt was folded into a turn already running.
func turnStart(events []event, at int) int {
	promptish := func(e *event) bool { return e.kind == evPrompt || e.kind == evHidden }
	for i := min(at, len(events)-1); i >= 0; i-- {
		if events[i].kind != evPrompt {
			continue
		}
		first := i
		for first > 0 && promptish(&events[first-1]) && !events[first-1].ended {
			first--
		}
		if first == 0 || startsTurn(events, first) {
			return first
		}
		i = first
	}
	return 0
}

// startsTurn reports whether the run of prompts that begins at first
// starts a turn, by what stands before it.
func startsTurn(events []event, first int) bool {
	for k := first - 1; k >= 0; k-- {
		switch e := &events[k]; e.kind {
		case evPush, evPop:
			// A push or a pop follows the results of the round that made
			// it; the reply itself is further back.
		case evSummary:
			return true
		case evSaid:
			return e.alone || e.ended
		default:
			return e.ended
		}
	}
	return true
}

// The marks of a change to mem in a call's code (frames.md section 9).
var changeMarks = []string{
	"mem.Think(", "mem.Note(", "mem.Rule(", "mem.Todo(", "mem.Later(", "mem.Wait(", "mem.Ask(", "mem.Done(",
	"mem.Drop(", "mem.Reopen(", "mem.Forget(", "mem.Redact(", "mem.Start(",
	".Todo(", ".Wait(", ".Ask(", ".Parallel(", ".Done(", ".Drop(", ".Reopen(", ".Note(", ".Think(",
}

var (
	// keepCallRe is a call that keeps or reads a fact or a thought.
	keepCallRe = regexp.MustCompile(`mem\.(?:Fact|Thought)\(`)
	// keepReadRe is such a call given one string and nothing else.
	keepReadRe = regexp.MustCompile(`mem\.(?:Fact|Thought)\(\s*("(?:[^"\\]|\\.)*"|` + "`[^`]*`" + `)\s*\)`)
)

// changesMem reports whether a mem call's code changes mem: it holds one
// of the calls that do, or a mem.Fact or mem.Thought given anything but
// one name alone, which only reads.
func changesMem(code string) bool {
	for _, mark := range changeMarks {
		if strings.Contains(code, mark) {
			return true
		}
	}
	reads := 0
	for _, m := range keepReadRe.FindAllStringSubmatch(code, -1) {
		if nameLike(unquote(m[1])) {
			reads++
		}
	}
	return len(keepCallRe.FindAllStringIndex(code, -1)) > reads
}

// refusalOf returns the line of a mem call's answer that refuses the
// close of a todo, and the todo; ok is false when the answer refuses no
// close.
func refusalOf(text string) (line, todo string, ok bool) {
	todo, ok = prompt.RefusedClose(text)
	if !ok {
		return "", "", false
	}
	at := strings.Index(text, "not popped: ")
	if by := strings.Index(text, "not closed: "); by >= 0 && (at < 0 || by < at) {
		at = by
	}
	line = text[at:]
	if end := strings.IndexByte(line, '\n'); end >= 0 {
		line = line[:end]
	}
	return strings.TrimSpace(line), todo, true
}

// worked reports whether events hold work in the todo called frame
// (frames.md section 9): a call of a tool other than mem that did not
// fail; a call on mem that changed it and was not refused; or an answer
// the model gave the person, unless a close of the todo was refused
// before it, in which case the answer is the claim and not the work. A
// prompt, a read of mem, stepping in or out, and a refused close are no
// work.
func worked(events []event, frame string) bool {
	return lastWork(events, frame) >= 0
}

// lastWork returns the index of the last of events that is work in the
// todo called frame, or -1 when none is.
func lastWork(events []event, frame string) int {
	last := -1
	refused := false
	for i := range events {
		e := &events[i]
		switch e.kind {
		case evSaid:
			if e.alone && !refused {
				last = i
			}
		case evCall:
			if !e.answered {
				continue
			}
			code, onMem := e.mem()
			switch {
			case !onMem:
				if !e.failed {
					last = i
				}
			case e.failed:
				if _, todo, ok := refusalOf(e.text); ok && todo == frame {
					refused = true
				}
			case failedText(e.text):
			case changesMem(code):
				last = i
			}
		}
	}
	return last
}

// failedText reports whether a mem call's answer says, in its status
// line, that the call failed.
func failedText(text string) bool {
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(line, "failed · ") {
			return true
		}
	}
	return false
}

// repliesAsEvents turns the replies a cut holds into events, for the rule
// of what work is.
func repliesAsEvents(replies []transcript.Reply) []event {
	var out []event
	for n, r := range replies {
		if r.Said {
			out = append(out, event{kind: evSaid, uuid: r.UUID, reply: n + 1, alone: len(r.Calls) == 0})
		}
		for _, c := range r.Calls {
			out = append(out, event{
				kind: evCall, uuid: r.UUID, reply: n + 1, id: c.ID, tool: c.Name, input: c.Input, path: c.Path,
				answered: c.Answered, failed: c.IsError, text: core.TextOf(c.Content),
			})
		}
	}
	return out
}

// The tools that change files, read them, and run commands, by the names
// the model calls them by.
func editTool(name string) bool {
	d := fstools.Defaults()
	return name == d.EditName || name == d.WriteName
}

func readTool(name string) bool { return name == fstools.Defaults().ReadName }

func shellTool(name string) bool { return name == shelltool.BashName }

// command returns the command line of a shell call.
func command(input json.RawMessage) string {
	var args struct {
		Command string `json:"command"`
	}
	_ = json.Unmarshal(input, &args)
	return args.Command
}

// clipText cuts s to at most max characters, keeping its start, and marks
// the cut.
func clipText(s string, max int) string {
	s = strings.TrimSpace(s)
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	n := 0
	for i := range s {
		if n == max {
			return s[:i] + "…"
		}
		n++
	}
	return s
}

// clips are the most characters each part of a laid-out conversation may
// have: what the person typed, what the model said, a call's arguments,
// and a call's result.
type clips struct{ user, said, input, result int }

// The three levels a long conversation is clipped at, the least first.
var clipLevels = []clips{
	{2000, 4000, 2000, 2000},
	{1000, 2000, 1200, 800},
	{500, 800, 600, 300},
}

// maxShown is the most characters of a laid-out conversation a judge is
// shown.
const maxShown = 24000

// written lays events out as text for a judge to read: the prompts typed,
// what the agent said and called, and what its tools returned, a failed
// call's result marked so. Hidden text of the harness and the frame
// events are left out. Each event begins a line of its own with who it
// is, and every further line of it is indented (under): a "the user:" in
// a tool's output, a file or the agent's own words never stands where the
// user's word does (the side-request review of 2026-11-19).
func written(events []event, c clips) string {
	var b strings.Builder
	for i := range events {
		e := &events[i]
		switch e.kind {
		case evPrompt:
			b.WriteString("the user: " + under(clipText(e.text, c.user)) + "\n")
		case evSaid:
			b.WriteString("the agent: " + under(clipText(e.text, c.said)) + "\n")
		case evCall:
			b.WriteString("the agent called " + oneLineName(e.tool) + ": " + under(clipText(callText(e), c.input)) + "\n")
			switch {
			case !e.answered:
				b.WriteString("the call has no result\n")
			case e.failed:
				b.WriteString("the tool failed: " + under(clipText(e.text, c.result)) + "\n")
			default:
				b.WriteString("the tool returned: " + under(clipText(e.text, c.result)) + "\n")
			}
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

// under indents every line of s but the first by two spaces, once the
// line breaks a model may read as one (carriage return, next line, line
// and paragraph separators) are made line feeds.
func under(s string) string {
	return strings.ReplaceAll(prompt.Lines(s), "\n", "\n  ")
}

// oneLineName is a tool's name on one line.
func oneLineName(s string) string {
	return strings.Join(strings.Fields(prompt.Lines(s)), " ")
}

// callText is a call as a judge reads it: a command's line, a mem call's
// code, and any other call's arguments as they were written.
func callText(e *event) string {
	if shellTool(e.tool) {
		if line := command(e.input); line != "" {
			return line
		}
	}
	if e.tool == gnovmtools.RunToolName {
		var p gnovmtools.RunParams
		if json.Unmarshal(e.input, &p) == nil && p.Code != "" {
			if name := strings.TrimSpace(p.Name); name != "" {
				return name + ": " + p.Code
			}
			return p.Code
		}
	}
	return string(e.input)
}

// shownTo lays events out within the room a judge has: clipped harder
// while the whole is over the limit, and then cut, keeping the end.
func shownTo(events []event) string {
	text := ""
	for _, level := range clipLevels {
		text = written(events, level)
		if utf8.RuneCountInString(text) <= maxShown {
			return text
		}
	}
	return tail(text, maxShown)
}

// tail keeps the last max characters of s.
func tail(s string, max int) string {
	n := utf8.RuneCountInString(s)
	if n <= max {
		return s
	}
	skip := n - max
	for i := range s {
		if skip == 0 {
			return "…" + s[i:]
		}
		skip--
	}
	return s
}

// endsWith reports whether path names the file name: it is name, or ends
// in a path separator and name.
func endsWith(path, name string) bool {
	path, name = filepath.ToSlash(path), filepath.ToSlash(name)
	return path == name || strings.HasSuffix(path, "/"+strings.TrimPrefix(name, "./"))
}
