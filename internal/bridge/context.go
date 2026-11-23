package bridge

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"

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
func (s *Server) prompt(req Request) Reply {
	if req.Session == "" {
		return fail(req.ID, CodeBadRequest, "prompt needs session")
	}
	rep := Reply{OK: true, ID: req.ID, Parts: []string{}}
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
