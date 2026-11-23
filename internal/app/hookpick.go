package app

import (
	"fmt"

	"github.com/gnosh-ai/gnosh/internal/hooks"
)

// HookChoice is a hook in force as the /hooks browser shows it (/hooks
// with nothing after it, as Claude Code's opens a read-only browser of its
// hooks, each labelled with where it comes from).
type HookChoice struct {
	// Event is the moment the hook runs at, Command what it runs as a
	// person reads it (a shell line, a program and its arguments, POST
	// and a URL, or mcp and a server's tool), and Source the file it is
	// written in.
	Event, Command, Source string
	// Details say the rest, a line each: its name, what it matches, its
	// if filter, its timeout and how it runs.
	Details []string
}

// HookChoices lists the hooks in force, by the moment each runs at, and a
// note on those that are configured and not in force.
func (c *controls) HookChoices() ([]HookChoice, string) { return c.cur().hookChoices() }

// hookChoices is SessionControls.HookChoices.
func (l *live) hookChoices() (out []HookChoice, note string) {
	for _, e := range hooks.Events() {
		for _, h := range l.hooks.r.Hooks(e) {
			out = append(out, HookChoice{Event: string(e), Command: h.CommandLine(), Source: h.Source, Details: hookDetails(h)})
		}
	}
	return out, l.hooksHeld(len(out))
}

// hooksHeld says how many of the hooks configured are not in force, when
// n are and any are not.
func (l *live) hooksHeld(n int) string {
	held := len(l.cfg.AllHooks) - n
	if held <= 0 || l.cfg.HooksOff != "" || l.cfg.Switches.SafeMode.V {
		return ""
	}
	return fmt.Sprintf("%d more are configured and not in force (a folder that is not trusted, or an event this build does not fire); gnosh doctor says which.", held)
}

// hookDetails are the lines that say how a hook runs, beside its command
// and its file.
func hookDetails(h hooks.Hook) []string {
	var out []string
	if h.Name != "" {
		out = append(out, "Name: "+h.Name)
	}
	if h.Matcher != "" && h.Matcher != "*" {
		out = append(out, "Matches: "+h.Matcher)
	}
	if h.If != "" {
		out = append(out, "Only if: "+h.If)
	}
	if h.Timeout > 0 {
		out = append(out, "Timeout: "+h.Timeout.String())
	}
	switch {
	case h.AsyncRewake:
		out = append(out, "Runs in the background, and an exit with 2 wakes the model")
	case h.Async:
		out = append(out, "Runs in the background")
	}
	if h.StatusMessage != "" {
		out = append(out, "Shows while it runs: "+h.StatusMessage)
	}
	if h.Once {
		out = append(out, "Runs once")
	}
	return out
}
