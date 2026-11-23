package app

import (
	"bytes"
	"strings"
	"testing"

	"github.com/gnosh-ai/gnosh/internal/agent"
	"github.com/gnosh-ai/gnosh/internal/cli"
)

// A retry of a sub-agent's request, whose reply had begun to arrive, does
// not throw away what had arrived of the session's own reply: it did, and
// the run printed the rest of its answer alone (the print mode review of
// 2026-11-19). The session's own retry still throws its false start away.
func TestSubAgentRetryKeepsTheMainReply(t *testing.T) {
	var out bytes.Buffer
	f := newPrinter(newProgram(Options{Stdout: &out}), cli.Invocation{OutputFormat: cli.FormatText})
	f.Event(&agent.TextEvent{Text: "The main answer, "})
	f.Event(&agent.RetryEvent{Meta: agent.Meta{Agent: "sub-1"}, Purpose: "main", Discarded: true, Message: "a sub-agent's request failed and is tried again"})
	f.Event(&agent.TextEvent{Text: "whole."})
	f.Event(&agent.ReplyEvent{Purpose: "main"})
	if got := out.String(); got != "The main answer, whole.\n" {
		t.Errorf("printed %q", got)
	}
	out.Reset()
	f.Event(&agent.TextEvent{Text: "A false start"})
	f.Event(&agent.RetryEvent{Purpose: "main", Discarded: true, Message: "the request failed and is tried again"})
	f.Event(&agent.TextEvent{Text: "The answer."})
	f.Event(&agent.ReplyEvent{Purpose: "main"})
	if got := out.String(); !strings.HasSuffix(got, "The answer.\n") || strings.Contains(got, "false start") {
		t.Errorf("printed %q", got)
	}
}

// A failure's tries are said once on standard error, without --verbose:
// a model server that is down said the same paragraph every 15 seconds.
// Another failure is said, and so is the same one after the model
// answered (its count starts again); with --verbose every try is said.
func TestRetriesSaidOnce(t *testing.T) {
	for _, verbose := range []bool{false, true} {
		var errs bytes.Buffer
		f := newPrinter(newProgram(Options{Stdout: &bytes.Buffer{}, Stderr: &errs}), cli.Invocation{OutputFormat: cli.FormatText, Verbose: verbose})
		down := func(n int) *agent.RetryEvent {
			return &agent.RetryEvent{Purpose: "main", Attempt: n, Kind: "network", Message: "the server could not be reached (try " + string(rune('0'+n)) + ")"}
		}
		f.Event(down(1))
		f.Event(down(2))
		f.Event(down(3))
		f.Event(&agent.RetryEvent{Purpose: "main", Attempt: 4, Kind: "overloaded", Status: 503, Message: "the server is busy"})
		f.Event(down(1))
		got := strings.Count(errs.String(), "could not be reached")
		want := 2
		if verbose {
			want = 4
		}
		if got != want || !strings.Contains(errs.String(), "the server is busy") {
			t.Errorf("verbose %v: said %d times, want %d:\n%s", verbose, got, want, errs.String())
		}
	}
}
