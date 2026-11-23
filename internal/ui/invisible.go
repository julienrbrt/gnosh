package ui

import (
	"strings"

	"github.com/gnosh-ai/gnosh/internal/agent"
	"github.com/gnosh-ai/gnosh/internal/text"
	"github.com/gnosh-ai/gnosh/internal/ui/editor"
)

// This file is Claude Code's removal of invisible characters from a
// prompt: as Enter sends it, the characters a terminal draws as nothing
// (text.CleanInvisible) are taken out of what was typed and of every
// paste in it, so that a prompt cannot carry words the person never saw.
// When what was taken out could hide text or change its order, or when
// what is left begins as a command or a shell line would and what was
// typed did not, nothing is sent: the prompt is back in the box to be
// read, and Enter sends it as it now stands.

// cleanDraft returns the draft with the invisible characters taken out,
// the whole text at once, so that a character beside a paste is judged
// with its neighbours in the paste; the pastes keep their bounds.
func cleanDraft(d editor.Draft) (editor.Draft, text.Invisible) {
	at := make([]int, 0, 2*len(d.Pastes))
	for _, sp := range d.Pastes {
		at = append(at, sp.Start, sp.End)
	}
	clean, moved, found := text.CleanInvisibleAt(d.Text, at)
	if found.Removed == 0 {
		return d, found
	}
	out := editor.Draft{Text: clean}
	for i := 0; i+1 < len(moved); i += 2 {
		if moved[i+1] > moved[i] {
			out.Pastes = append(out.Pastes, editor.Span{Start: moved[i], End: moved[i+1]})
		}
	}
	return out, found
}

// cleanToSend takes the invisible characters out of a draft Enter is
// sending as mode, and reports whether it goes. When it does not, the
// person was told why and the cleaned draft is back in the box.
func (s *session) cleanToSend(d *editor.Draft, mode editor.Mode) bool {
	clean, found := cleanDraft(*d)
	if found.Removed == 0 {
		return true
	}
	what := count(found.Removed, "invisible character", "invisible characters")
	lead := strings.TrimLeft(clean.Text, " \t\n")
	switch {
	case strings.TrimSpace(clean.Text) == "":
		s.flash("The prompt held only invisible characters, so nothing was sent.")
		return false
	case found.Hiding:
		s.restore(clean)
		s.notice(agent.LevelWarning, "Took out "+what+" that could hide text or reorder it; nothing was sent.\nRead the prompt in the box; Enter sends it.")
		return false
	case mode == editor.ModeShell:
		// What is taken out of a command changes what it does ("rm
		// report<ZWSP>.txt" names another file), so it is never run
		// before it is read.
		s.restore(clean)
		s.notice(agent.LevelWarning, "Took out "+what+" from the shell line, which changes what it runs; nothing was run.\nRead it in the box; Enter runs it.")
		return false
	case mode == editor.ModePrompt && (strings.HasPrefix(lead, "/") || strings.HasPrefix(lead, "!") || leaveWord(clean.Text)):
		s.restore(clean)
		s.notice(agent.LevelWarning, "Took out "+what+", and what is left reads as a command; nothing was sent.\nRead it in the box; Enter runs it.")
		return false
	}
	*d = clean
	s.notice(agent.LevelInfo, "Took out "+what+" before sending the prompt.")
	return true
}

// leaveWord reports whether a prompt is the one word that leaves the
// session (exit, quit).
func leaveWord(s string) bool {
	w := strings.ToLower(strings.TrimSpace(s))
	return w == "exit" || w == "quit"
}
