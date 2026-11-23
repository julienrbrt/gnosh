package ui

import (
	"strings"
	"testing"

	"github.com/gnosh-ai/gnosh/internal/agent"
	"github.com/gnosh-ai/gnosh/internal/app"
	"github.com/gnosh-ai/gnosh/internal/text"
)

// Under a prompt, the interface says what each path it mentioned with @
// brought, from the Source of the mention's part.
func TestMentionsSaid(t *testing.T) {
	parts := []agent.Part{
		{Source: "environment"},
		{Source: "mention:file:notes.txt"},
		{Source: "instructions"},
		{Source: "mention:dir:docs"},
		{Source: "mention:denied:.env"},
		{Source: "mention:failed:big.log"},
		{Source: "mention:agent:reviewer"},
		{Source: "mention:odd:x"},
	}
	want := "@notes.txt read · @docs listed · @.env denied by a rule · @big.log could not be read · @agent-reviewer asked for"
	if got := mentionsSaid(parts); got != want {
		t.Errorf("mentionsSaid = %q, want %q", got, want)
	}
	if got := mentionsSaid(nil); got != "" {
		t.Errorf("with no mentions: %q", got)
	}
}

// kindsOnly is a session's controls that can only name kinds of sub-agent.
type kindsOnly struct{ app.Controls }

func (kindsOnly) AgentKinds() []string { return []string{"general-purpose", "Explore", "reviewer"} }

// After an @, the kinds of sub-agent are offered as @agent-<name>, for
// what was typed that "agent-" and a name begin with.
func TestCompleteAgent(t *testing.T) {
	s := &session{c: kindsOnly{}}
	labels := func(typed string) string {
		var out []string
		for _, c := range s.completeAgent(typed, 4) {
			if c.Start != 4 || c.Text != c.Label+" " {
				t.Errorf("%q: candidate %+v", typed, c)
			}
			out = append(out, c.Label)
		}
		return strings.Join(out, " ")
	}
	for typed, want := range map[string]string{
		"a":         "@agent-general-purpose @agent-Explore @agent-reviewer",
		"agent-":    "@agent-general-purpose @agent-Explore @agent-reviewer",
		"agent-rev": "@agent-reviewer",
		"agent-z":   "",
		"src/":      "",
		"":          "",
	} {
		if got := labels(typed); got != want {
			t.Errorf("after @%s: %q, want %q", typed, got, want)
		}
	}
	if got := (&session{}).completeAgent("a", 0); got != nil {
		t.Errorf("controls that cannot name kinds: %+v", got)
	}
}

// A command named partway through a prompt, as Claude Code completes it:
// the commands and skills whose names begin with what follows the slash,
// a bare slash for every one, and a path when no name begins that way or
// the word goes on past a second slash. A line that is itself a command
// keeps its words for the command.
func TestCompleteCommandMidPrompt(t *testing.T) {
	s := &session{cwd: "/proj", info: app.SessionInfo{
		Commands:    []string{"help", "review", "recap", "status"},
		CommandList: []app.CommandInfo{{Name: "exit", Aliases: []string{"quit"}}},
		Skills:      []app.CommandInfo{{Name: "release-notes"}},
	}, listDir: func(dir string) []fileEntry {
		if dir == "/" {
			return []fileEntry{{name: "tmp", dir: true}, {name: "srv", dir: true}}
		}
		return nil
	}}
	offered := func(text string) string {
		var out []string
		for _, c := range s.complete(text, len(text)) {
			if !strings.HasPrefix(c.Text, text[c.Start:]) {
				t.Errorf("%q: candidate %+v", text, c)
			}
			out = append(out, c.Text)
		}
		return strings.Join(out, " ")
	}
	for text, want := range map[string]string{
		"run the tests, then /re": "/review /recap /release-notes",
		"then /st":                "/status",
		"then /":                  "/help /review /recap /status /release-notes",
		"then /qu":                "/quit",
		"look in /tm":             "/tmp/",
		"look in /srv/x":          "",
		"/re":                     "/review /recap /release-notes",
		"/loop 5m /re":            "",
	} {
		if got := offered(text); got != want {
			t.Errorf("%q: offered %q, want %q", text, got, want)
		}
	}
}

// A slash in the middle of a prompt opens no list by itself, so Enter
// sends what was typed (a route such as /st stays /st); Tab opens it.
func TestMidPromptSlashWaitsForTab(t *testing.T) {
	g := newRig(t, 80, 24, text.NoColor)
	g.keys(`"show GET /st"`)
	g.liveLacks("/status")
	g.keys(`<Enter>`)
	if got := g.c.sent(); len(got) != 1 || got[0] != "show GET /st" {
		t.Fatalf("sent %q", got)
	}
	g.keys(`"then /st" <Tab>`)
	if g.box() != "then /status" {
		t.Fatalf("Tab gave %q", g.box())
	}
	// At the start of the box the slash still opens the list of commands.
	g.keys(`<Ctrl+U> "/st"`)
	g.liveHas("/status")
}

// What is typed after /cd and /add-dir is a folder, slash or no slash:
// only folders are offered for it, from the folder the session is in. A
// folder typed in full is offered as typed first, so that Enter runs the
// command with it rather than taking the name with a slash.
func TestCompleteFolder(t *testing.T) {
	s := &session{cwd: "/proj", home: "/home/u", listDir: func(dir string) []fileEntry {
		switch dir {
		case "/proj":
			return []fileEntry{{name: "docs", dir: true}, {name: "doctor.go"}, {name: "src", dir: true}}
		case "/home/u":
			return []fileEntry{{name: "code", dir: true}, {name: "notes.txt"}}
		}
		return nil
	}}
	labels := func(text string) string {
		var out []string
		for _, c := range s.complete(text, len(text)) {
			out = append(out, c.Text)
		}
		return strings.Join(out, " ")
	}
	for text, want := range map[string]string{
		"/cd do":       "docs/",
		"/cd docs":     "docs docs/",
		"/cd ":         "docs/ src/",
		"/add-dir s":   "src/",
		"/cd ~/":       "~/code/",
		"/cd docs x":   "",
		"/cdx do":      "",
		"tell me do":   "",
		"look at src/": "",
	} {
		if got := labels(text); got != want {
			t.Errorf("%q: offered %q, want %q", text, got, want)
		}
	}
}
