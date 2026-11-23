package ui

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/gnosh-ai/gnosh/internal/term"
	"github.com/gnosh-ai/gnosh/internal/text"
)

// Keystrokes are read as Claude Code writes them, in any case, and come
// out as the keys the terminal reports.
func TestParseStroke(t *testing.T) {
	for in, want := range map[string]term.Key{
		"ctrl+e":       ctrlKey('e'),
		"CTRL+E":       ctrlKey('e'),
		"control+k":    ctrlKey('k'),
		"meta+p":       altKey('p'),
		"opt+t":        altKey('t'),
		"shift+tab":    namedKey(term.KeyTab, term.ModShift),
		"escape":       namedKey(term.KeyEscape, 0),
		"esc":          namedKey(term.KeyEscape, 0),
		"return":       namedKey(term.KeyEnter, 0),
		"ctrl+enter":   namedKey(term.KeyEnter, term.ModCtrl),
		"pageup":       namedKey(term.KeyPageUp, 0),
		"f5":           namedKey(term.KeyF1+4, 0),
		"space":        {Code: term.KeyRune, Rune: ' '},
		"x":            {Code: term.KeyRune, Rune: 'x'},
		"K":            {Code: term.KeyRune, Rune: 'k'},
		"shift+k":      {Code: term.KeyRune, Rune: 'K'},
		"ctrl+shift+c": {Code: term.KeyRune, Rune: 'C', Mod: term.ModCtrl},
		"ctrl+_":       ctrlKey('_'),
		"cmd+k":        {Code: term.KeyRune, Rune: 'k', Mod: term.ModSuper},
	} {
		got, err := parseStroke(in)
		if err != nil || got != want {
			t.Errorf("parseStroke(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	for _, bad := range []string{"ctl+k", "ctrl+", "", "f99", "hyperdrive", "ctrl+ k"} {
		if k, err := parseStroke(bad); err == nil {
			t.Errorf("parseStroke(%q) = %v, want an error", bad, k)
		}
	}
	// A key pressed is looked up as a binding names it.
	if got := canonKey(term.Key{Code: term.KeyRune, Rune: 'k', Mod: term.ModShift | term.ModCapsLock, Text: "K"}); got != (term.Key{Code: term.KeyRune, Rune: 'K'}) {
		t.Errorf("Shift+k is looked up as %v", got)
	}
}

// What in the file cannot be used is said and left out; the rest is in
// force.
func TestParseKeyBindings(t *testing.T) {
	b, problems := parseKeyBindings([]byte(`{
		"bindings": [
			{"context": "Chat", "bindings": {"ctrl+e": "chat:externalEditor", "ctrl+s": null, "ctrl+y": "command:compact",
				"ctrl+q": "chat:nope", "ctrl+c": "chat:stash", "ctl+k": "chat:stash", "ctrl+k ctrl+s": "chat:stash", "a b c": "chat:stash", "ctrl+w": 3}},
			{"context": "Task", "bindings": {"ctrl+f": "task:background"}},
			{"context": "Settings", "bindings": {"x": "select:accept"}}
		]}`))
	chat := b.byCtx["Chat"]
	if chat[ctrlKey('e')] != "chat:externalEditor" || chat[ctrlKey('y')] != "command:compact" || chat[ctrlKey('f')] != "task:background" {
		t.Errorf("the bindings in force: %v", chat)
	}
	if action, ok := chat[ctrlKey('s')]; !ok || action != "" {
		t.Errorf("ctrl+s is not taken away: %q %v", action, ok)
	}
	if len(chat) != 4 {
		t.Errorf("%d bindings in force in Chat, want 4: %v", len(chat), chat)
	}
	if b.chords["Chat"][[2]term.Key{ctrlKey('k'), ctrlKey('s')}] != "chat:stash" {
		t.Errorf("the chords in force: %v", b.chords["Chat"])
	}
	all := strings.Join(problems, "\n")
	for _, want := range []string{`"chat:nope" is not an action gnosh carries out`, "ctrl+c cannot be bound", `"ctl" is no modifier`, "a binding is one key, or a chord of two", "ctrl+w is neither an action's name nor null", `the context "Settings" is not one gnosh takes keys for`} {
		if !strings.Contains(all, want) {
			t.Errorf("the problems do not say %q:\n%s", want, all)
		}
	}
	if _, problems := parseKeyBindings([]byte("{not json")); len(problems) != 1 || !strings.Contains(problems[0], "no key is changed") {
		t.Errorf("a file that is not JSON: %q", problems)
	}
}

// In the box a bound key does its action, null takes a key away, and
// command: runs a command; the file is read again when it changes, and a
// problem in it is said.
func TestKeyBindingsInTheBox(t *testing.T) {
	path := filepath.Join(t.TempDir(), "keybindings.json")
	write := func(text string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(`{"bindings": [{"context": "Chat", "bindings": {"ctrl+k": "chat:stash", "ctrl+s": null, "ctrl+y": "command:compact"}}]}`)
	g := newRig(t, 100, 24, text.NoColor, func(c *controls) {
		c.info.Keybindings = path
		c.info.Commands = append(c.info.Commands, "compact")
	})
	g.keys(`"abc" <Ctrl+K>`)
	if got := g.box(); got != "" {
		t.Fatalf("Ctrl+K bound to chat:stash left %q in the box", got)
	}
	g.keys(`<Ctrl+S>`)
	if got := g.box(); got != "" {
		t.Fatalf("Ctrl+S, taken away, put %q in the box", got)
	}
	g.keys(`<Ctrl+K>`)
	if got := g.box(); got != "abc" {
		t.Fatalf("Ctrl+K again brought back %q, want abc", got)
	}
	g.keys(`<Ctrl+U> <Ctrl+Y>`)
	if !slices.Contains(g.c.commands, "/compact") {
		t.Fatalf("Ctrl+Y bound to command:compact ran %q", g.c.commands)
	}
	// Changed, the file is read again: Ctrl+K takes back nothing, and a
	// context gnosh has no keys for is said.
	write(`{"bindings": [{"context": "Chat", "bindings": {"ctrl+k": null}}, {"context": "Nope", "bindings": {}}]}`)
	g.wait(2 * time.Second).keys(`"xyz" <Ctrl+A> <Ctrl+K>`)
	if got := g.box(); got != "xyz" {
		t.Fatalf("Ctrl+K taken away still cut, leaving %q", got)
	}
	g.committedHas(`the context "Nope" is not one gnosh takes keys for`)
	// With the file gone every key is its own again.
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	g.wait(2 * time.Second).keys(`<Ctrl+A> <Ctrl+K>`)
	if got := g.box(); got != "" {
		t.Fatalf("without the file Ctrl+K left %q", got)
	}
}

// A chord of the person's waits for its second key: the pair does the
// action, another key lets the first one through, and a chord left
// unfinished does nothing.
func TestKeyBindingChords(t *testing.T) {
	path := filepath.Join(t.TempDir(), "keybindings.json")
	if err := os.WriteFile(path, []byte(`{"bindings": [{"context": "Chat", "bindings": {"ctrl+k ctrl+s": "command:compact"}}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	g := newRig(t, 100, 24, text.NoColor, func(c *controls) {
		c.info.Keybindings = path
		c.info.Commands = append(c.info.Commands, "compact")
	})
	g.keys(`<Ctrl+K> <Ctrl+S>`)
	if !slices.Contains(g.c.commands, "/compact") {
		t.Fatalf("Ctrl+K Ctrl+S ran %q", g.c.commands)
	}
	g.keys(`"abc" <Ctrl+A> <Ctrl+K> "z"`)
	if got := g.box(); got != "z" {
		t.Fatalf("Ctrl+K then z left %q in the box, want z (Ctrl+K cut the line, then z was typed)", got)
	}
	g.keys(`<Ctrl+K>`).wait(4 * time.Second).keys(`<Ctrl+S>`)
	if got := g.box(); got != "" {
		t.Fatalf("Ctrl+S after a chord left unfinished did not stash: %q", got)
	}
	if n := strings.Count(strings.Join(g.c.commands, " "), "/compact"); n != 1 {
		t.Fatalf("/compact ran %d times", n)
	}
}

// The first key of a chord keeps its own binding when no chord follows:
// taken away, it does nothing; bound, it does its action. A key that does
// not end a chord may start one. chat:clearInput clears the box.
func TestKeyBindingChordFirstKeyAlone(t *testing.T) {
	path := filepath.Join(t.TempDir(), "keybindings.json")
	if err := os.WriteFile(path, []byte(`{"bindings": [{"context": "Chat", "bindings": {
		"ctrl+k": null, "ctrl+k ctrl+s": "command:compact",
		"ctrl+y": "chat:clearInput", "ctrl+y ctrl+y": "command:cost"}}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	g := newRig(t, 100, 24, text.NoColor, func(c *controls) {
		c.info.Keybindings = path
		c.info.Commands = append(c.info.Commands, "compact", "cost")
	})
	g.keys(`"abc" <Ctrl+A> <Ctrl+K> "z"`)
	if got := g.box(); got != "zabc" {
		t.Fatalf("Ctrl+K, taken away, then z left %q in the box, want zabc", got)
	}
	g.keys(`<Ctrl+Y> "q"`)
	if got := g.box(); got != "q" {
		t.Fatalf("Ctrl+Y (chat:clearInput) then q left %q in the box, want q", got)
	}
	g.keys(`<Ctrl+K> <Ctrl+K> <Ctrl+S>`)
	if !slices.Contains(g.c.commands, "/compact") {
		t.Fatalf("Ctrl+K Ctrl+K Ctrl+S did not end in the chord: ran %q", g.c.commands)
	}
	if got := g.box(); got != "q" {
		t.Fatalf("the Ctrl+K that did not end a chord changed the box to %q", got)
	}
}

// Whatever the file holds, reading it never panics, and a key it binds is
// already in the form keys are looked up by.
func FuzzKeyBindings(f *testing.F) {
	for _, seed := range []string{
		`{"bindings": [{"context": "Chat", "bindings": {"ctrl+e": "chat:externalEditor", "ctrl+s": null}}]}`,
		`{"bindings": [{"context": "Global", "bindings": {"ctrl+k ctrl+s": "command:compact", "shift+K": "app:redraw"}}]}`,
		`{"bindings": [{"context": "Transcript", "bindings": {"q": "transcript:exit", "f12": null, "ctrl++": "chat:undo"}}]}`,
		`{"bindings": null}`, `[]`, `{`, ``,
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, data string) {
		b, _ := parseKeyBindings([]byte(data))
		for ctx, keys := range b.byCtx {
			for k := range keys {
				if canonKey(k) != k {
					t.Fatalf("%s: %v is kept as it is not looked up", ctx, k)
				}
			}
		}
		for _, stroke := range strings.Fields(data) {
			if k, err := parseStroke(stroke); err == nil && canonKey(k) != k {
				t.Fatalf("parseStroke(%q) = %v, which is not looked up as it is", stroke, k)
			}
		}
	})
}

// A problem in the file is said as the session starts, before any key.
func TestKeyBindingsProblemsAtStart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "keybindings.json")
	if err := os.WriteFile(path, []byte(`{"bindings": [{"context": "Chat", "bindings": {"ctl+k": "chat:stash"}}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	g := newRig(t, 100, 24, text.NoColor, func(c *controls) { c.info.Keybindings = path })
	g.committedHas(`"ctl" is no modifier`)
}

// A named pipe where the keybindings file should be is not read: the keys
// were stopped waiting on it (the status line review of 2026-11-19).
func TestKeyBindingsFileThatIsAPipe(t *testing.T) {
	path := filepath.Join(t.TempDir(), "keybindings.json")
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		g := newRig(t, 100, 24, text.NoColor, func(c *controls) { c.info.Keybindings = path })
		g.keys(`"hi"`)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("the session waited on a named pipe for its keybindings")
	}
}

// The list of keys (? in an empty box) shows the person's own keys after
// gnosh's: what each does here, a key taken away, a command, a chord.
func TestKeyBindingsInTheListOfKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), "keybindings.json")
	if err := os.WriteFile(path, []byte(`{"bindings": [
		{"context": "Chat", "bindings": {"ctrl+k": "chat:stash", "ctrl+s": null, "ctrl+y": "command:compact", "ctrl+x ctrl+o": "app:toggleTranscript"}},
		{"context": "Transcript", "bindings": {"ctrl+q": "transcript:exit"}}
	]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	g := newRig(t, 100, 40, text.NoColor, func(c *controls) {
		c.info.Keybindings = path
		c.info.Commands = append(c.info.Commands, "compact")
	})
	g.keys(`"?"`)
	g.committedHas("Keys", "Yours, from "+path)
	// Under that line, each key with what it does, on its own row.
	var yours []string
	for i, l := range g.committed {
		if strings.Contains(l, "Yours, from") {
			yours = g.committed[i+1:]
		}
	}
	for _, want := range [][2]string{
		{"Ctrl+K", "put the prompt aside, and bring it back"},
		{"Ctrl+S", "taken away: it does nothing"},
		{"Ctrl+Y", "runs /compact"},
		{"Ctrl+X Ctrl+O", "the whole transcript: every thought, call and result"},
		{"Ctrl+Q", "leave the transcript (in the transcript)"},
	} {
		found := false
		for _, l := range yours {
			if strings.HasPrefix(strings.TrimSpace(l), want[0]+" ") && strings.Contains(l, want[1]) {
				found = true
			}
		}
		if !found {
			t.Errorf("no row %q %q under the person's keys:\n%s", want[0], want[1], strings.Join(yours, "\n"))
		}
	}
}

func TestKeyLabel(t *testing.T) {
	for _, c := range []struct {
		key  term.Key
		want string
	}{
		{term.Key{Code: term.KeyRune, Rune: 'y', Mod: term.ModCtrl}, "Ctrl+Y"},
		{term.Key{Code: term.KeyRune, Rune: 'p', Mod: term.ModAlt}, "Alt+P"},
		{term.Key{Code: term.KeyTab, Mod: term.ModShift}, "Shift+Tab"},
		{term.Key{Code: term.KeyEnter, Mod: term.ModCtrl}, "Ctrl+Enter"},
		{term.Key{Code: term.KeyEscape}, "Esc"},
		{term.Key{Code: term.KeyF5}, "F5"},
		{term.Key{Code: term.KeyRune, Rune: ' ', Mod: term.ModCtrl}, "Ctrl+Space"},
		{term.Key{Code: term.KeyRune, Rune: 'q', Text: "q"}, "q"},
	} {
		if got := keyLabel(c.key); got != c.want {
			t.Errorf("keyLabel(%+v) = %q, want %q", c.key, got, c.want)
		}
	}
}

// The hints name the key the person bound to what they tell of: the
// transcript view's, and in it the key that shows all; with gnosh's own
// key given to something else and none bound in its place, no hint names
// one.
func TestKeyHintsFollowTheKeybindings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "keybindings.json")
	write := func(text string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(`{"bindings": [{"context": "Chat", "bindings": {"ctrl+y": "app:toggleTranscript"}}, {"context": "Transcript", "bindings": {"ctrl+a": "transcript:toggleShowAll"}}]}`)
	g := newRig(t, 100, 24, text.NoColor, func(c *controls) { c.info.Keybindings = path })
	if got := g.s.keyHint("app:toggleTranscript", "Ctrl+O"); got != "Ctrl+Y" {
		t.Errorf("the transcript's key is named %q, want Ctrl+Y", got)
	}
	if got := g.s.keyHint("transcript:toggleShowAll", "Ctrl+E"); got != "Ctrl+A" {
		t.Errorf("the key that shows all is named %q, want Ctrl+A", got)
	}
	if got := g.s.keyHint("transcript:exit", "q"); got != "q" {
		t.Errorf("the key that goes back is named %q, want q", got)
	}
	if got := g.s.transcriptHint(" · ", "shows it"); got != " · Ctrl+Y shows it" {
		t.Errorf("the hint is %q", got)
	}
	// Ctrl+O given to something else, and no key in its place.
	write(`{"bindings": [{"context": "Chat", "bindings": {"ctrl+o": "chat:stash"}}]}`)
	g.wait(2 * time.Second)
	if got := g.s.transcriptHint(" · ", "shows it"); got != "" {
		t.Errorf("with Ctrl+O given away the hint is %q, want none", got)
	}
	// Taken away in a chord of gnosh's: Ctrl+X Ctrl+K.
	write(`{"bindings": [{"context": "Chat", "bindings": {"ctrl+x ctrl+k": null}}]}`)
	g.wait(2 * time.Second)
	if got := g.s.keyHint("chat:killAgents", "Ctrl+X Ctrl+K"); got != "" {
		t.Errorf("with Ctrl+X Ctrl+K taken away its hint is %q, want none", got)
	}
	if got := g.s.keyHint("app:toggleTranscript", "Ctrl+O"); got != "Ctrl+O" {
		t.Errorf("with no binding of it the transcript's key is %q, want Ctrl+O", got)
	}
}

// The welcome line and the working line name the key the person bound to
// stop a turn.
func TestStopHintFollowsTheKeybindings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "keybindings.json")
	if err := os.WriteFile(path, []byte(`{"bindings": [{"context": "Chat", "bindings": {"ctrl+q": "chat:cancel"}}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	g := newRig(t, 100, 24, text.NoColor, func(c *controls) { c.info.Keybindings = path })
	if got := g.s.keyHint("chat:cancel", "Esc"); got != "Ctrl+Q" {
		t.Fatalf("the stop key is named %q, want Ctrl+Q", got)
	}
	if got := g.s.keyHint("chat:cycleMode", "Shift+Tab"); got != "Shift+Tab" {
		t.Errorf("the mode key is named %q, want Shift+Tab", got)
	}
}
