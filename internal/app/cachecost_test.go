package app

import (
	"testing"

	"github.com/gnosh-ai/gnosh/internal/app/apptest"
)

// /cost says how the model server's prompt cache served the session's
// requests (Claude Code's "Prompt cache (main)"): the share of what was
// read that came from it, and a request that read again what the one
// before it had sent, as a server that let the conversation go does.
func TestCostSaysTheCache(t *testing.T) {
	w := apptest.NewWorld(t,
		apptest.Reply{Text: "One.", In: 10000, Out: 10},
		apptest.Reply{Text: "Two.", In: 10300, Out: 10, Cached: 10000},
		apptest.Reply{Text: "Three.", In: 10600, Out: 10},
	)
	s := sit(t, w)
	s.see("Type a prompt and Enter.")
	s.typeLine("the first prompt")
	s.see("One.")
	s.typeLine("the second prompt")
	s.see("Two.")
	s.typeLine("the third prompt")
	s.see("Three.")
	// (A request's counts are kept as its turn ends, after its words are
	// on the screen: /cost typed between the two, on a loaded machine,
	// told of two requests, 16 November's CI of a7a1c6a.)
	s.see("-- done in ")
	s.typeLine("/cost")
	s.see("  cache    32% of what was read came from the model server's cache; 1 request read again about")
	s.typeLine("/exit")
	s.exit()
}
