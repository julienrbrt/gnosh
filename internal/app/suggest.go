package app

import (
	"context"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/gnosh-ai/gnosh/internal/agent"
	"github.com/gnosh-ai/gnosh/internal/permission"
)

// This file is Claude Code's prompt suggestions (promptSuggestionEnabled):
// after a turn of a session a person attends, the session's model is
// asked, beside the session and keeping nothing, what the person would
// most likely send next, and the interface shows it in the empty box,
// where Tab or the right arrow takes it and typing drops it. The option is
// off unless set, as Claude Code has it off for other providers' models:
// each suggestion is a request to the model. It gives way to any request
// of the session's own (agent.SideRequest.Yield), and a prompt sent stops
// it at once.

// SuggestionEvent is the person's likely next prompt, for the interface
// to show in the empty box until they type.
type SuggestionEvent struct {
	Text string
}

// How a suggestion is asked for: a few words, soon, or not at all.
const (
	suggestTokens = 400 // room for a model whose least thinking is still some
	suggestWait   = 60 * time.Second
	suggestChars  = 200
)

// suggester is the suggestion being made, if one is.
type suggester struct {
	mu     sync.Mutex
	cancel context.CancelFunc
}

// start puts a new suggestion in the place of the one being made, if one
// is, and returns the context its request goes by. It is in place before
// the request is sent, so that a prompt sent at once stops it too.
func (s *suggester) start(parent context.Context) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(parent)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cancel != nil {
		s.cancel()
	}
	s.cancel = cancel
	return ctx, cancel
}

// stop stops the suggestion being made, if one is: a prompt was sent.
func (s *suggester) stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cancel != nil {
		s.cancel()
		s.cancel = nil
	}
}

// suggestNext asks for the person's next prompt once a turn has ended,
// when the option is on: in a session a person attends through a front
// end that shows it (Suggester), out of plan mode, after a turn that
// ended as it should and with nothing waiting to go next.
func (l *live) suggestNext(res agent.TurnResult) {
	if !l.suggestOn.Load() || !l.interactive || res.Reason != agent.EndCompleted || l.engine.Mode() == permission.ModePlan {
		return
	}
	if shows, ok := l.front.(Suggester); !ok || !shows.ShowsSuggestions() {
		return
	}
	if !slices.Contains(res.Prompts, l.lastPrompt()) {
		// A turn no prompt of the person's began (a background task's
		// news, a hook, a goal): nobody is at the box for a guess at
		// what they send next, which would cost the model a request, as
		// Claude Code also holds back after such turns.
		return
	}
	a := l.agentReady.Load()
	if a == nil {
		return
	}
	if st := a.State(); st.Queued > 0 || st.Loop {
		// The next turn is already coming (what was queued, or /loop's).
		return
	}
	ctx, cancel := l.suggest.start(l.ctx)
	l.background(func(context.Context) {
		defer cancel()
		// It gives way (Yield) to any request of the session's own, so a
		// turn that begins by any road (a prompt, a !line, a skill, /loop,
		// a hook) never waits behind it.
		out, err := a.Side(ctx, agent.SideRequest{Purpose: "suggestion", Session: true, Yield: true, Prompt: l.texts.NextPrompt(), MaxTokens: suggestTokens, Timeout: suggestWait})
		if err != nil || ctx.Err() != nil || a.State().Busy {
			return
		}
		if text := suggestionText(out.Text); text != "" {
			l.deliver(&SuggestionEvent{Text: text})
		}
	})
}

// blank reports whether a line is a model's way of saying it has nothing
// to suggest, written out where it was asked to write nothing: "(no
// suggestion)", "[none]", "Nothing.", "N/A". Such a line was offered as
// the person's next prompt (Claude Code 2.1.329 fixed the same, a
// placeholder such as "_(no suggestion)_" shown as a suggestion).
func blank(line string) bool {
	if (strings.HasPrefix(line, "(") && strings.HasSuffix(line, ")")) ||
		(strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]")) {
		return true
	}
	w := strings.ToLower(strings.TrimRight(line, ".!:;… "))
	switch w {
	case "none", "nothing", "n/a", "null", "nil", "empty", "blank", "no suggestion", "no suggestions",
		"no prompt", "no next prompt", "no next step", "no clear next step", "nothing to suggest":
		return true
	}
	return false
}

// leaves reports whether a line is a word that leaves the session when
// typed alone (the interface's exit and quit).
func leaves(line string) bool {
	w := strings.ToLower(strings.TrimSpace(line))
	return w == "exit" || w == "quit"
}

// suggestionText is the first line of what the model wrote, without the
// quotes and marks a model puts around such a line, cut short; "" when
// it wrote nothing to offer, or wrote out that it has nothing (blank).
// A line that is not a prompt to the model is not offered: one beginning
// with ! runs as a shell line, with no rule looked at, and one beginning
// with / as a command, when the person takes it with Tab and Enter, and
// exit or quit ends the session (the status line review of 2026-11-19: a
// page the model read could have it suggest "!curl … | sh").
func suggestionText(s string) string {
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		line = strings.Trim(line, "\"'`*_>“”‘’ ")
		if line == "" {
			continue
		}
		if blank(line) || strings.HasPrefix(line, "!") || strings.HasPrefix(line, "/") || leaves(line) {
			return ""
		}
		if utf8.RuneCountInString(line) > suggestChars {
			r := []rune(line)
			line = strings.TrimSpace(string(r[:suggestChars])) + "…"
		}
		return line
	}
	return ""
}
