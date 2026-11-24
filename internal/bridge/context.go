package bridge

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/gnosh-ai/gnosh/internal/core"
	"github.com/gnosh-ai/gnosh/internal/prompt"
	"github.com/gnosh-ai/gnosh/internal/transcript"
)

// This file is what a gnosh session tells its model of mem, and how it
// makes room, for a host that is not gnosh (bridge.md §10): the part of
// the system prompt about mem and the lines that go with a prompt
// (prompt), the slide in place of a summary (room), and the lines a
// session adds between two rounds of a turn (step). The words are
// internal/prompt's and the cut is transcript.Slide's, as in a session.

// remindAfter is how many replies of the model may go by with no call on
// mem before it is reminded of mem (memjoin's own count).
const remindAfter = 10

// editTools are gnosh's own file-editing tools, whose paths a slide's line
// names; a host names its own in edit_tools.
var editTools = []string{"Edit", "Write", "MultiEdit"}

// prompt answers prompt: the system prompt's part about mem, and the
// lines that go after a prompt a person typed. With first, the digest of
// mem comes first among them.
func (s *Server) prompt(ctx context.Context, req Request) Reply {
	if req.Session == "" {
		return fail(req.ID, CodeBadRequest, "prompt needs session")
	}
	rep := Reply{OK: true, ID: req.ID, Parts: []string{}}
	if tu := s.opts.Turns; tu != nil {
		// A prompt the person typed starts a turn (§11).
		if len(req.Entries) > 0 && uniqueUUIDs(req.Entries) == "" {
			tu.Sync(req.Session, req.Entries)
		}
		tu.Prompt(ctx, req.Session, req.Text)
	}
	t := s.opts.Texts
	if t == nil {
		return rep
	}
	rep.System = t.Memory(prompt.MemoryLean)
	if req.First && s.opts.Digest != nil {
		if block := strings.TrimSpace(s.opts.Digest()); block != "" {
			rep.Parts = append(rep.Parts, block)
		}
	}
	rep.Parts = append(rep.Parts, t.MemLine())
	if line := t.FrameLine(s.framesOf(req.Session, req.Entries), nil); line != "" {
		rep.Parts = append(rep.Parts, line)
	}
	return rep
}

// framesOf is the stack a session stands in, as the prompt texts take it:
// from entries when the host sent them, else as the bridge keeps it.
func (s *Server) framesOf(session string, entries []transcript.Entry) []prompt.Frame {
	if len(entries) > 0 {
		_, info := transcript.View(entries, transcript.ViewOptions{Session: session})
		out := make([]prompt.Frame, len(info.Stack))
		for i, f := range info.Stack {
			out[i] = prompt.Frame{ID: f.ID, Purpose: f.Label}
		}
		return out
	}
	ids := s.Stack(session)
	out := make([]prompt.Frame, len(ids))
	for i, id := range ids {
		out[i] = prompt.Frame{ID: id}
	}
	return out
}

// room answers room: the slide (frames.md §8). It plans a cut that frees
// about need tokens, as a session does at the point where it would be
// summarized, and returns the slide entries the host keeps with the
// conversation and sends in every later view. Freed 0 means nothing could
// slide (the session stands in no frame, or too little is left), and the
// host makes room its own way, by a summary.
func (s *Server) room(req Request) Reply {
	switch {
	case req.Session == "":
		return fail(req.ID, CodeBadRequest, "room needs session")
	case req.Need <= 0:
		return fail(req.ID, CodeBadRequest, "room needs need, the tokens to free")
	}
	if err := uniqueUUIDs(req.Entries); err != "" {
		return fail(req.ID, CodeBadRequest, err)
	}
	slides, cuts := transcript.Slide(req.Entries, req.Need, transcript.ViewOptions{
		Session: req.Session, Prompt: req.Prompt, KeepRounds: req.KeepRounds, Trace: true,
	})
	rep := Reply{OK: true, ID: req.ID, NewSlides: named(slides, req.Session)}
	if len(slides) == 0 {
		rep.NewSlides = []transcript.Entry{}
		return rep
	}
	if req.Covered {
		for _, c := range cuts {
			if !s.covered(req.Entries, c.Removed) {
				what := "what came before `" + c.Before + "`"
				if c.Frame != "" {
					what = "the start of `" + c.Frame + "`"
				}
				rep.NewSlides = []transcript.Entry{}
				rep.Notice = fmt.Sprintf("nothing slid: %s holds work mem has no record of, so it is left to a summary", what)
				if s.opts.Log != nil {
					s.opts.Log("gnosh bridge: " + rep.Notice)
				}
				return rep
			}
		}
		ss := s.session(req.Session)
		s.mu.Lock()
		if ss.slidAt == nil {
			ss.slidAt = map[string]string{}
		}
		last := lastMessage(req.Entries)
		for _, c := range cuts {
			f := c.Frame
			if f == "" {
				f = c.Before
			}
			ss.slidAt[f] = last
			delete(ss.soon, f)
		}
		s.mu.Unlock()
	}
	names := req.EditTools
	if len(names) == 0 {
		names = editTools
	}
	var told []prompt.Slide
	var notices []string
	for _, c := range cuts {
		rep.Freed += c.Tokens
		files := c.EditedPaths(names...)
		switch {
		case c.Frame != "":
			told = append(told, prompt.Slide{Frame: c.Frame, Files: files})
			notices = append(notices, fmt.Sprintf("slid `%s`: about %d tokens of its start out of the model's context, instead of compacting", c.Frame, c.Tokens))
		case c.Before != "":
			told = append(told, prompt.Slide{Frame: c.Before, Before: true, Files: files})
			notices = append(notices, fmt.Sprintf("slid what came before `%s`: about %d tokens out of the model's context, instead of compacting", c.Before, c.Tokens))
		}
	}
	rep.Notice = strings.Join(notices, "; ")
	if s.opts.Texts != nil {
		if line := s.opts.Texts.SlideLine(told); line != "" {
			rep.Parts = []string{line}
		}
	}
	if s.opts.Log != nil && rep.Notice != "" {
		s.opts.Log("gnosh bridge: " + rep.Notice)
	}
	return rep
}

// step answers step: what a session adds before the model's next reply
// within a turn. used is the host's token count of the request just
// answered and point the count at which it would summarize. The lines are
// the large-step line, once per frame (frames.md §8.7), and the mem
// reminder after ten replies with no call on mem.
func (s *Server) step(req Request) Reply {
	if req.Session == "" {
		return fail(req.ID, CodeBadRequest, "step needs session")
	}
	ss := s.session(req.Session)
	rep := Reply{OK: true, ID: req.ID, Parts: []string{}}
	s.mu.Lock()
	if req.Used > 0 {
		ss.used = req.Used
	}
	s.mu.Unlock()
	t := s.opts.Texts
	if t == nil || len(req.Entries) == 0 {
		return rep
	}
	_, info := transcript.View(req.Entries, transcript.ViewOptions{Session: req.Session})
	frames := make([]prompt.Frame, len(info.Stack))
	for i, f := range info.Stack {
		frames[i] = prompt.Frame{ID: f.ID, Purpose: f.Label}
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if n := len(info.Stack); n > 0 && req.Point > 0 && req.Used > 0 {
		top := info.Stack[n-1]
		at, ok := ss.pushedAt[top.ID]
		grown, room := req.Used-at, req.Point-at
		key := top.ID + "\x00" + top.ToolUseID
		if ok && at > 0 && room > 0 && grown > room/2 && grown > req.Point/10 && !ss.large[key] {
			if ss.large == nil {
				ss.large = map[string]bool{}
			}
			ss.large[key] = true
			rep.Parts = append(rep.Parts, t.LargeStep(top.ID, grown))
		}
	}
	// Near the point, with nothing kept in mem since the top todo was
	// entered or last slid: the slide-soon line, for a host that slides
	// only what mem covers (§10.4).
	if n := len(info.Stack); req.Covered && n > 0 && req.Point > 0 && req.Used >= req.Point-req.Point/8 {
		top := info.Stack[n-1]
		if !ss.soon[top.ID] && !s.recordedSince(req.Entries, s.since(req.Entries, ss, top)) {
			if ss.soon == nil {
				ss.soon = map[string]bool{}
			}
			ss.soon[top.ID] = true
			rep.Parts = append(rep.Parts, t.SlideSoon(top.ID))
		}
	}
	replies := 0
	for i := len(req.Entries) - 1; i >= 0 && i >= ss.remindedAt; i-- {
		e := &req.Entries[i]
		if e.Type != transcript.TypeAssistant {
			continue
		}
		if s.opts.UsesMem != nil && s.opts.UsesMem(e.Message) {
			break
		}
		replies++
	}
	if replies >= remindAfter {
		ss.remindedAt = len(req.Entries)
		rep.Parts = append(rep.Parts, t.MemReminder(frames, nil))
	}
	return rep
}

// covered reports whether mem got a record at or after the first of the
// removed entries that the host sent: a reply that keeps something in mem
// (Options.RecordsMem) came during or after the work the cut would take.
func (s *Server) covered(entries []transcript.Entry, removed []string) bool {
	in := make(map[string]bool, len(removed))
	for _, u := range removed {
		in[u] = true
	}
	for i, e := range entries {
		if in[e.UUID] {
			return s.recordedSince(entries, i)
		}
	}
	return true // nothing of the host's is cut
}

// recordedSince reports whether a call at or after entries[from] kept
// something in mem: a call whose result, among the entries, says so.
func (s *Server) recordedSince(entries []transcript.Entry, from int) bool {
	if s.opts.RecordsMem == nil {
		return false
	}
	results := map[string]core.Block{}
	for _, e := range entries {
		if e.Type != transcript.TypeUser {
			continue
		}
		for _, b := range e.Message.Blocks {
			if b.Type == core.BlockToolResult {
				results[b.ToolUseID] = b
			}
		}
	}
	for i := max(from, 0); i < len(entries); i++ {
		if entries[i].Type != transcript.TypeAssistant {
			continue
		}
		for _, b := range entries[i].Message.Blocks {
			if b.Type != core.BlockToolUse {
				continue
			}
			if r, ok := results[b.ID]; ok && s.opts.RecordsMem(b, r) {
				return true
			}
		}
	}
	return false
}

// since is where the work of the frame top that a slide would take
// begins in entries: after the reply that entered it, or after what the
// host had sent when the frame last slid. The caller holds s.mu.
func (s *Server) since(entries []transcript.Entry, ss *session, top transcript.Frame) int {
	from := 0
	for i, e := range entries {
		switch {
		case e.UUID != "" && e.UUID == top.UUID:
			from = max(from, i+1)
		case e.Type == transcript.TypeAssistant && top.ToolUseID != "" && callsID(e.Message, top.ToolUseID):
			from = max(from, i+1)
		case ss.slidAt[top.ID] != "" && e.UUID == ss.slidAt[top.ID]:
			from = max(from, i+1)
		}
	}
	return from
}

// callsID reports whether m holds the tool call id.
func callsID(m core.Message, id string) bool {
	for _, b := range m.Blocks {
		if b.Type == core.BlockToolUse && b.ID == id {
			return true
		}
	}
	return false
}

// lastMessage is the uuid of the last user or assistant entry.
func lastMessage(entries []transcript.Entry) string {
	for i := len(entries) - 1; i >= 0; i-- {
		if entries[i].IsMessage() {
			return entries[i].UUID
		}
	}
	return ""
}

func uniqueUUIDs(entries []transcript.Entry) string {
	seen := make(map[string]bool, len(entries))
	for i, e := range entries {
		if e.UUID == "" {
			return fmt.Sprintf("entry %d has no uuid", i)
		}
		if seen[e.UUID] {
			return fmt.Sprintf("entry %d has the uuid %q of an entry before it", i, e.UUID)
		}
		seen[e.UUID] = true
	}
	return ""
}

// named gives the slide entries a view or a room planned what a session's
// file would: a uuid of their own and the session's id, so that a host can
// send them back as they are.
func named(slides []transcript.Entry, session string) []transcript.Entry {
	for i := range slides {
		if slides[i].UUID == "" {
			var b [8]byte
			_, _ = rand.Read(b[:])
			slides[i].UUID = "slide-" + hex.EncodeToString(b[:])
		}
		if slides[i].SessionID == "" {
			slides[i].SessionID = session
		}
	}
	return slides
}
