package app

import (
	"time"

	"github.com/gnosh-ai/gnosh/internal/agent"
	"github.com/gnosh-ai/gnosh/internal/core"
	"github.com/gnosh-ai/gnosh/internal/transcript"
)

// TranscriptEntry is one entry of the session's conversation as the
// interface's transcript view shows it (Ctrl+O, as Claude Code's): whole,
// with the model's reasoning and every call's input and result, and with
// the work of the steps that were popped, which the model is no longer
// sent.
type TranscriptEntry struct {
	// Time is when the entry was written.
	Time time.Time
	// Kind is what the entry is: "prompt" (what the person sent, with
	// what came with it), "results" (the results of the calls of the
	// reply before), "hook" (what a hook added), "notice" (what gnosh
	// said: as a message to the model, or to the person), "reply" (the
	// model's), "summary" (what stands for the conversation before it,
	// once that was compacted) or "frame" (a step begun or ended).
	Kind string
	// Message is the message of a prompt, of results, of a hook, of a
	// reply, or of a notice that went to the model.
	Message core.Message
	// Hidden marks a prompt that gnosh sent and the person did not type
	// (a /goal directive).
	Hidden bool
	// Model is the model that wrote a reply, where the session knows it.
	Model string
	// Level is how much a notice to the person matters.
	Level agent.Level
	// Text is a summary's text, a notice's to the person, or for a frame
	// what was begun ("push"), ended ("pop") or slid, by Op.
	Text string
	// Op, Frame and Label are a frame entry's: what happened, the step's
	// id, and its title.
	Op, Frame, Label string
}

// Transcript returns the session's conversation, the oldest entry first:
// what the transcript view shows. A rewind's cut-off entries are not in
// it.
func (c *controls) Transcript() []TranscriptEntry { return c.cur().transcriptEntries() }

// transcriptEntries is SessionControls.Transcript.
func (l *live) transcriptEntries() []TranscriptEntry {
	if l.ts == nil {
		return nil
	}
	return transcriptEntriesOf(l.ts.Entries())
}

// transcriptEntriesOf is the conversation a session file's entries hold,
// as the transcript view shows it.
func transcriptEntriesOf(all []transcript.Entry) []TranscriptEntry {
	entries := transcript.Conversation(all)
	out := make([]TranscriptEntry, 0, len(entries))
	for i := range entries {
		e := &entries[i]
		t := TranscriptEntry{Time: e.Timestamp}
		f, _ := loopOf(e)
		switch e.Type {
		case transcript.TypeUser:
			switch e.Source {
			case transcript.SourceToolResults:
				t.Kind = "results"
			case transcript.SourceHook:
				t.Kind = "hook"
			case transcript.SourceNotice:
				t.Kind = "notice"
			default:
				t.Kind, t.Hidden = "prompt", f.Hidden
			}
			t.Message = e.Message
		case transcript.TypeAssistant:
			t.Kind, t.Message, t.Model = "reply", e.Message, f.Model
			// (The usage entry written after a reply names its model too.)
			if t.Model == "" && i+1 < len(entries) && entries[i+1].Type == transcript.TypeUsage {
				t.Model = entries[i+1].Model
			}
		case transcript.TypeSummary:
			t.Kind, t.Text = "summary", e.Summary
		case transcript.TypeFrame:
			t.Kind, t.Op, t.Frame, t.Label, t.Text = "frame", string(e.Op), e.Frame, e.Label, e.Result
		case typeNotice:
			if f.Text == "" || f.Silent {
				continue
			}
			t.Kind, t.Level, t.Text = "notice", f.Level, f.Text
		default:
			continue
		}
		out = append(out, t)
	}
	return out
}
