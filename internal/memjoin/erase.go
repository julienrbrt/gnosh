package memjoin

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/gnosh-ai/gnosh/internal/agent"
	"github.com/gnosh-ai/gnosh/internal/core"
	"github.com/gnosh-ai/gnosh/internal/gnovm"
	"github.com/gnosh-ai/gnosh/internal/transcript"
)

// This file follows an erasure in mem (mem.md section 16). When mem has
// forgotten something or redacted a secret, by the agent's call or by the
// person's /mem, gnosh takes the erased text out of its own copies of
// mem's text, in every session of the project: the hidden parts stored
// after a prompt (the digest, what was recalled, the lines), a summary, a
// goal's binding, a task list, and the frames' own titles and results.
//
// What the person saw stays as it was: what they typed, the model's
// replies, tool calls and their results. Those still reach the model in
// their session, so the sessions that still show a redacted secret are
// named.
//
// The erasure holds the very text erased. It is handed from function to
// function here and to transcript.Redact, and to nothing else: it is
// never logged, never put in an error, and never written anywhere.

// scrubbed is what following an erasure came to.
type scrubbed struct {
	// files is how many session files were written again.
	files int
	// shown names the sessions whose visible messages still hold a
	// redacted secret.
	shown []string
	// left names the sessions that are open in another gnosh, whose
	// copies could not be touched.
	left []string
}

// onMemErased is the tools' OnMemErased slot: a kept call of the agent
// erased something.
func (j *join) onMemErased(ctx context.Context, _ string, e gnovm.MemErasure) {
	j.tell(agent.LevelWarning, "mem", j.scrub(ctx, e).told())
}

// told is what the person is told after an erasure, or "" when there is
// nothing to tell.
func (s scrubbed) told() string {
	var parts []string
	if n := len(s.shown); n > 0 {
		parts = append(parts, fmt.Sprintf("what you saw of it is still in %d session(s), %s, and reaches the model again in them: start a new session, or delete those", n, quoted(s.shown)))
	}
	if n := len(s.left); n > 0 {
		// What follows an erasure may have been a forget, or the agent's
		// own call, and neither can be typed again: mem no longer knows
		// the name. /mem redact with the text reaches those copies after
		// any of them.
		parts = append(parts, fmt.Sprintf("%d session(s) are open in another gnosh, %s, so gnosh's own copies in them were left as they are: to take a text out of them too, close them and run /mem redact with that text", n, quoted(s.left)))
	}
	return strings.Join(parts, "; ")
}

// quoted writes names in quotes, joined by commas.
func quoted(names []string) string {
	out := make([]string, len(names))
	for i, n := range names {
		out[i] = fmt.Sprintf("%q", n)
	}
	return strings.Join(out, ", ")
}

// scrub takes what e erased out of gnosh's own copies of mem's text in
// every session of the project.
func (j *join) scrub(ctx context.Context, e gnovm.MemErasure) scrubbed {
	var out scrubbed
	if len(e.Texts) == 0 && len(e.Names) == 0 && len(e.Renamed) == 0 {
		return out
	}
	store := j.s.Sessions
	if store == nil {
		return out
	}
	files, _ := filepath.Glob(filepath.Join(store.Dir, "projects", "*", "*.jsonl"))
	sort.Strings(files)
	mine := j.vms.MemStatusPath(j.project)
	same := map[string]bool{}
	for _, path := range files {
		id := strings.TrimSuffix(filepath.Base(path), ".jsonl")
		cwd := startCwd(path)
		if cwd == "" {
			continue
		}
		// A session is this project's when its folder's mem is this mem:
		// the sessions of a repository's subfolders and worktrees share
		// one.
		belongs, known := same[cwd]
		if !known {
			belongs = j.vms.MemStatusPath(cwd) == mine
			same[cwd] = belongs
		}
		if !belongs {
			continue
		}
		sc := &scrubber{e: e}
		var changed int
		var err error
		switch open := j.p.open(id); {
		case id == j.id:
			changed, err = j.ts.Redact(sc.edit)
			j.reload()
		case open != nil:
			changed, err = open.ts.Redact(sc.edit)
			open.reload()
		default:
			changed, err = store.Redact(ctx, path, sc.edit)
		}
		var locked *transcript.LockedError
		switch {
		case errors.As(err, &locked):
			// Its writer is another gnosh. The file is read, to say
			// whether anything of what was erased is in it.
			if held := sc.look(ctx, store, path); held {
				out.left = append(out.left, sc.name(id))
			}
		case err == nil && changed > 0:
			out.files++
		}
		if sc.shown {
			out.shown = append(out.shown, sc.name(id))
		}
	}
	return out
}

// startCwd reads the working directory a session was started in from the
// first line of its file, without reading the rest.
func startCwd(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	r := bufio.NewReaderSize(f, 64<<10)
	var line []byte
	for {
		part, more, err := r.ReadLine()
		line = append(line, part...)
		if err != nil || !more {
			break
		}
	}
	var first struct {
		Cwd string `json:"cwd"`
	}
	if json.Unmarshal(line, &first) != nil {
		return ""
	}
	return first.Cwd
}

// reload reads the goal's binding and the task list from the session's
// log again, after a redaction changed them there, and hands the front
// end what they now are.
func (j *join) reload() {
	entries := transcript.Conversation(j.ts.Entries())
	j.mu.Lock()
	j.goal.raw = ""
	j.goal.load(j.id, entries)
	j.tasks.list = nil
	j.tasks.load(entries)
	j.mu.Unlock()
	j.show()
}

// scrubber is the edit a redaction is given: it takes what was erased out
// of the entries that are gnosh's own copies of mem's text, and notes
// what it sees of the session on the way.
type scrubber struct {
	e gnovm.MemErasure
	// shown says a visible message holds a redacted secret, and held that
	// one of gnosh's own copies holds something that was erased.
	shown bool
	held  bool
	// title is the session's name and prompt the first thing the person
	// typed in it.
	title  string
	user   bool
	prompt string
}

// name is what a session is called for a person: its title, else the
// start of its first prompt, else the start of its id. A title or a prompt
// may itself hold what was erased, so it is named without it: the person
// is told which session it is, and not the text again.
func (sc *scrubber) name(id string) string {
	if title := strings.TrimSpace(sc.e.Scrub(sc.title)); title != "" {
		return title
	}
	// The whole prompt first, and then its start: a text cut short by the
	// clipping would no longer be found.
	if start := clipText(firstLine(sc.e.Scrub(sc.prompt)), 60); start != "" {
		return start
	}
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// text takes what was erased out of s and reports whether that changed
// it.
func (sc *scrubber) text(s *string) bool {
	if *s == "" {
		return false
	}
	out := sc.e.Scrub(*s)
	if out == *s {
		return false
	}
	*s = out
	return true
}

// secret reports whether text holds a secret that was redacted.
func (sc *scrubber) secret(text string) bool {
	for _, s := range sc.e.Secrets() {
		if strings.Contains(text, s) {
			return true
		}
	}
	return false
}

// visible looks at a message the person saw: it is left as it is, and a
// redacted secret in it is noted.
func (sc *scrubber) visible(blocks []core.Block) {
	for i := range blocks {
		b := &blocks[i]
		if sc.secret(b.Text) || sc.secret(string(b.Input)) {
			sc.shown = true
		}
		sc.visible(b.Content)
	}
}

// edit is the function handed to transcript.Redact.
func (sc *scrubber) edit(en *transcript.Entry) bool {
	changed := false
	switch en.Type {
	case transcript.TypeTitle:
		if en.Title != "" && (en.Source == transcript.SourceUser || !sc.user) {
			sc.title, sc.user = en.Title, en.Source == transcript.SourceUser
		}
		return false
	case transcript.TypeAssistant:
		sc.visible(en.Message.Blocks)
		return false
	case transcript.TypeUser:
		if en.Source == transcript.SourcePrompt || en.Source == transcript.SourceToolResults {
			if en.Source == transcript.SourcePrompt && sc.prompt == "" {
				sc.prompt = en.Message.Text()
			}
			sc.visible(en.Message.Blocks)
			return false
		}
		// A hidden part: the digest, what was recalled, a line, a
		// reminder, a goal's directive, what a hook added.
		for i := range en.Message.Blocks {
			if sc.text(&en.Message.Blocks[i].Text) {
				changed = true
			}
		}
	case transcript.TypeSummary:
		changed = sc.text(&en.Summary)
	case transcript.TypeFrame:
		if sc.text(&en.Label) {
			changed = true
		}
		if sc.text(&en.Result) {
			changed = true
		}
		if name := renamed(sc.e, en.Frame); name != en.Frame {
			en.Frame, changed = name, true
		}
	case transcript.TypeMeta:
		switch en.Key {
		case metaGoal:
			changed = sc.binding(&en.Value)
		case metaTasks:
			changed = sc.tasks(&en.Value)
		default:
			return false
		}
	default:
		// A line gnosh said for itself about mem is a copy of mem's text
		// too, and the one copy that is neither sent to a model nor
		// something the person typed.
		if sc.said(en) {
			sc.held = true
			return true
		}
		return false
	}
	// What the loop keeps beside such an entry (a summary's own blocks)
	// is a copy of the same text.
	for name, raw := range en.Extra {
		if out, ok := sc.raw(raw); ok {
			en.Extra[name], changed = out, true
		}
	}
	if changed {
		sc.held = true
	}
	return changed
}

// How a line gnosh said for itself is kept in a session's file, as the
// wiring writes it: an entry of this type, whose Extra holds the loop's own
// fields under this key. Who said the line is one of those fields, and its
// text another.
const (
	saidEntry transcript.EntryType = "notice"
	loopKey   string               = "loop"
)

// said takes what was erased out of a line gnosh said for itself about
// mem or about a goal (a todo that is due, a rule that was set, a pop, a
// goal's verdict), and reports whether that changed it. Only the line's
// text is gone through: the loop's other fields are words of gnosh's own,
// which an erased text must not be able to change. A line that came from
// anywhere else is not mem's text, and is left as it is.
func (sc *scrubber) said(en *transcript.Entry) bool {
	if en.Type != saidEntry {
		return false
	}
	var loop map[string]json.RawMessage
	if json.Unmarshal(en.Extra[loopKey], &loop) != nil {
		return false
	}
	var kind, src, text string
	if json.Unmarshal(loop["kind"], &kind) != nil || kind != string(saidEntry) {
		return false
	}
	if json.Unmarshal(loop["src"], &src) != nil || (src != "mem" && src != "goal") {
		return false
	}
	if json.Unmarshal(loop["text"], &text) != nil || !sc.text(&text) {
		return false
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if enc.Encode(text) != nil {
		return false
	}
	loop["text"] = bytes.TrimRight(buf.Bytes(), "\n")
	out, err := json.Marshal(loop)
	if err != nil {
		return false
	}
	en.Extra[loopKey] = out
	return true
}

// renamed returns the name a todo goes by after the erasure.
func renamed(e gnovm.MemErasure, name string) string {
	for old, now := range e.Renamed {
		switch {
		case name == old:
			return now
		case strings.HasPrefix(name, old+"/"):
			return now + name[len(old):]
		}
	}
	return name
}

// binding takes what was erased out of a goal's binding as it is stored:
// a renamed todo is bound under its new name, and the condition, the
// title, the last verdict and what was last asked lose the erased text.
func (sc *scrubber) binding(value *string) bool {
	var b gnovm.GoalBinding
	if *value == "" || json.Unmarshal([]byte(*value), &b) != nil {
		return false
	}
	changed := false
	if name := renamed(sc.e, b.Name); name != b.Name {
		b.Name, changed = name, true
	}
	for _, s := range []*string{&b.Condition, &b.Title, &b.Verdict, &b.Asked} {
		if sc.text(s) {
			changed = true
		}
	}
	if changed {
		*value = encodeGoal(b)
	}
	return changed
}

// tasks takes what was erased out of a task list as it is kept. A list
// kept in a shape this build does not read loses the erased text all the
// same, string by string: an erasure must not depend on which build wrote
// the file.
func (sc *scrubber) tasks(value *string) bool {
	if *value == "" {
		return false
	}
	list, ok := decodeTasks(*value)
	if !ok {
		out, changed := sc.raw(json.RawMessage(*value))
		if changed {
			*value = string(out)
		}
		return changed
	}
	out, changed := scrubTasks(list, sc.e)
	if !changed {
		return false
	}
	*value = encodeTasks(out)
	return true
}

// raw takes what was erased out of every string of a JSON value, and
// reports whether that changed it.
func (sc *scrubber) raw(raw json.RawMessage) (json.RawMessage, bool) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if dec.Decode(&v) != nil {
		return nil, false
	}
	changed := false
	var walk func(v any) any
	walk = func(v any) any {
		switch x := v.(type) {
		case string:
			if sc.text(&x) {
				changed = true
			}
			return x
		case []any:
			for i := range x {
				x[i] = walk(x[i])
			}
		case map[string]any:
			for k := range x {
				x[k] = walk(x[k])
			}
		}
		return v
	}
	v = walk(v)
	if !changed {
		return nil, false
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if enc.Encode(v) != nil {
		return nil, false
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), true
}

// look reads a session file that cannot be written, and reports whether
// one of gnosh's own copies in it holds something that was erased. What
// it sees of the session's visible messages is noted as edit notes it.
func (sc *scrubber) look(ctx context.Context, store *transcript.Store, path string) bool {
	s, err := store.OpenPath(ctx, path, true)
	if err != nil {
		return false
	}
	defer s.Close()
	for _, en := range s.Entries() {
		// edit is given a copy, as Redact gives it one: what it changes
		// here is thrown away.
		c := en
		c.Message.Blocks = cloneBlocks(en.Message.Blocks)
		c.Extra = cloneExtra(en.Extra)
		sc.edit(&c)
	}
	return sc.held
}

func cloneBlocks(blocks []core.Block) []core.Block {
	if blocks == nil {
		return nil
	}
	out := append([]core.Block(nil), blocks...)
	for i := range out {
		out[i].Content = cloneBlocks(out[i].Content)
	}
	return out
}

func cloneExtra(extra map[string]json.RawMessage) map[string]json.RawMessage {
	if extra == nil {
		return nil
	}
	out := make(map[string]json.RawMessage, len(extra))
	for k, v := range extra {
		out[k] = v
	}
	return out
}
