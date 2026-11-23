package ui

import (
	"strings"
	"testing"

	"github.com/gnosh-ai/gnosh/internal/app"
	"github.com/gnosh-ai/gnosh/internal/text"
)

// A suggested next prompt (promptSuggestionEnabled) shows in the empty box;
// Tab or the right arrow takes it, and typing anything else drops it.
func TestSuggestedPrompt(t *testing.T) {
	g := newRig(t, 100, 24, text.NoColor)
	g.ev(&app.SuggestionEvent{Text: "run the tests again"})
	if !strings.Contains(g.region(), "run the tests again  (Tab takes it)") {
		t.Fatalf("the suggestion is not in the box:\n%s", g.region())
	}
	g.keys(`<Tab>`)
	if got := g.box(); got != "run the tests again" {
		t.Fatalf("Tab took %q, want the suggestion", got)
	}

	g.keys(`<Ctrl+U>`)
	g.ev(&app.SuggestionEvent{Text: "commit it"})
	g.keys(`<Right>`)
	if got := g.box(); got != "commit it" {
		t.Fatalf("the right arrow took %q", got)
	}

	g.keys(`<Ctrl+U>`)
	g.ev(&app.SuggestionEvent{Text: "push the branch"})
	g.keys(`"x"`)
	if got := g.box(); got != "x" {
		t.Fatalf("typing over the suggestion left %q", got)
	}
	g.keys(`<Ctrl+U> <Tab>`)
	if strings.Contains(g.box(), "push the branch") {
		t.Errorf("a suggestion typed over came back with Tab")
	}

	// One that comes while the box holds text is not shown.
	g.keys(`"draft"`)
	g.ev(&app.SuggestionEvent{Text: "something else"})
	if strings.Contains(g.region(), "something else") {
		t.Errorf("a suggestion was shown over what the person is typing")
	}
}
