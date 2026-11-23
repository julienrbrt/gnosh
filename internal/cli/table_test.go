package cli

import (
	"strings"
	"sync"
	"testing"
)

// TestTableIsSound checks the table against the rules the parser leans on.
// A row that breaks one would make some line read wrongly, so each is
// checked here once rather than hoped for.
func TestTableIsSound(t *testing.T) {
	tab := Default()

	ids := map[string]bool{}
	for _, f := range tab.flags {
		if ids[f.id] {
			t.Errorf("two flags have the id %q", f.id)
		}
		ids[f.id] = true
		if len(f.long) == 0 {
			t.Errorf("%s has no spelling as a word", f.id)
		}
		if f.help == "" || !strings.HasSuffix(strings.ReplaceAll(f.help, nbsp, " "), ".") && !strings.HasSuffix(f.help, ")") {
			t.Errorf("%s: the help is empty or does not end like a sentence: %q", f.id, f.help)
		}
		for _, l := range f.long {
			if l == "" || strings.ContainsAny(l, "= \t") || strings.HasPrefix(l, "-") {
				t.Errorf("%s: %q cannot be typed as --%s", f.id, l, l)
			}
		}
		for _, r := range f.short {
			if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z') {
				t.Errorf("%s: -%c is not a letter", f.id, r)
			}
		}
		switch f.kind {
		case kindBool:
			if f.arg != "" || len(f.choices) > 0 || f.number {
				t.Errorf("%s takes no value but describes one", f.id)
			}
		case kindList:
			if f.list == 0 || f.arg == "" || !strings.HasSuffix(f.arg, "...") {
				t.Errorf("%s is a list flag without a kind of list, or its value is not written with ...", f.id)
			}
		default:
			if f.arg == "" {
				t.Errorf("%s takes a value and help has no name for it", f.id)
			}
			if f.set == nil {
				t.Errorf("%s takes a value and records it nowhere", f.id)
			}
		}
		if f.kind != kindList && f.list != 0 {
			t.Errorf("%s has a kind of list but is not a list flag", f.id)
		}
		if f.printLetter != 0 && f.kind != kindBool {
			t.Errorf("%s has a print-mode letter and takes a value", f.id)
		}
		owned := false
		for _, c := range tab.commands {
			owned = owned || hasFlag(c.flags, f)
		}
		if !owned {
			t.Errorf("no command takes %s", f.id)
		}
	}

	names := map[string]bool{}
	for _, c := range tab.commands {
		title := c.title()
		if names[title] {
			t.Errorf("two commands are %q", title)
		}
		names[title] = true
		if c.summary == "" {
			t.Errorf("%s has no summary", title)
		}
		if c.needSub != (len(c.subs) > 0 && c.parent != nil) {
			t.Errorf("%s: needSub is %v with %d subcommands", title, c.needSub, len(c.subs))
		}
		if c.needSub && c.cmd != "" {
			t.Errorf("%s needs a subcommand and yet is a command of its own", title)
		}
		if !c.needSub && c.cmd == "" {
			t.Errorf("%s leads to no Command", title)
		}
		if c.maxArgs >= 0 && c.minArgs > c.maxArgs {
			t.Errorf("%s needs %d arguments and takes %d at most", title, c.minArgs, c.maxArgs)
		}
		if (c.minArgs > 0 || c.maxArgs != 0) && !c.needSub && c.argsUsage == "" {
			t.Errorf("%s takes arguments and help does not show them", title)
		}
		if !hasFlag(c.flags, tab.flags[indexOf(tab, idHelp)]) {
			t.Errorf("%s does not take --help", title)
		}

		// Within a command, and within everything that can be typed before
		// a group's subcommand, a spelling names one flag only.
		seen := map[string]*flag{}
		for _, f := range c.reach() {
			for _, s := range f.spellings() {
				if other, dup := seen[s]; dup && other != f {
					t.Errorf("%s: %s is both %s and %s", title, s, other.id, f.id)
				}
				seen[s] = f
			}
		}
		// A print-mode letter takes over a letter of a flag that takes no
		// value either, so the line is cut the same way whichever it means.
		for _, f := range c.flags {
			if f.printLetter == 0 {
				continue
			}
			other := c.letter(f.printLetter)
			if other == nil || other.kind != kindBool {
				t.Errorf("%s: -%c means %s in print mode, and outside it is not a flag without a value", title, f.printLetter, f.id)
			}
		}
		// No flag's word is made only of the command's one-letter flags:
		// "-print" can then be told from letters sharing a dash.
		for _, f := range c.reach() {
			for _, l := range f.long {
				all := len(l) > 1
				for _, r := range l {
					all = all && c.letter(r) != nil
				}
				if all {
					t.Errorf("%s: --%s is made only of one-letter flags, so -%s cannot be told from them", title, l, l)
				}
			}
		}
		// A subcommand's name or alias names one subcommand only.
		subs := map[string]bool{}
		for _, s := range c.subs {
			for _, w := range append([]string{s.name}, s.aliases...) {
				if subs[w] {
					t.Errorf("%s has two commands called %s", title, w)
				}
				subs[w] = true
				if w == "" || strings.HasPrefix(w, "-") || hasSpace(w) {
					t.Errorf("%s: %q cannot be typed as a command", title, w)
				}
			}
		}
	}

	// Only gnosh itself opens a session or answers a prompt: print mode is
	// a flag of it, -p, and not a command of its own.
	for _, c := range tab.commands {
		if c.session != (c == tab.root) {
			t.Errorf("%s: session is %v", c.title(), c.session)
		}
	}
}

func indexOf(tab *Table, id string) int {
	for i, f := range tab.flags {
		if f.id == id {
			return i
		}
	}
	return 0
}

// TestTableHoldsWhatIsBuilt lists every flag and command of the table by
// name, so that dropping one fails here and not in Jae's terminal. It also
// lists what the two specs name and the table leaves out: what
// spec/DECISIONS.md item 26 took off the command line, and the commands
// and flags whose parts are not written. One of those gets its row with
// the code that builds it, and not before.
func TestTableHoldsWhatIsBuilt(t *testing.T) {
	tab := Default()
	have := map[string]bool{}
	for _, e := range tab.Entries() {
		if e.Kind == EntryValue {
			continue
		}
		for _, s := range e.Spellings {
			if e.Kind == EntryFlag {
				for _, c := range e.Commands {
					have[c+" "+s] = true
				}
			} else {
				have["gnosh "+s] = true
			}
		}
	}
	for _, want := range []string{
		// The flags of a session.
		"-C", "--continue", "-c", "-r", "--resume", "--session-id", "--fork-session", "-n", "--name",
		"-m", "--model", "--small-model", "--reasoning-effort", "--effort",
		"-y", "--yolo", "--dangerously-skip-permissions", "--permission-mode",
		"--allowedTools", "--allowed-tools", "--disallowedTools", "--disallowed-tools", "--tools", "--add-dir", "--trust-folder",
		"--system-prompt", "--system-prompt-file", "--append-system-prompt", "--append-system-prompt-file",
		"--cwd", "--settings", "--setting-sources", "--safe-mode", "--disable-slash-commands",
		"-d", "--debug", "--debug-file", "-v", "--verbose",
		"-p", "--print", "--output-format", "--input-format", "--max-turns", "--goal",
		"--version", "-h", "--help",
	} {
		if !have["gnosh "+want] {
			t.Errorf("gnosh does not take %s", want)
		}
	}
	for _, want := range []string{
		"gnosh doctor", "gnosh doctor --hosts", "gnosh trust", "gnosh trust --list", "gnosh trust --forget", "gnosh models",
		"gnosh session list", "gnosh session show", "gnosh session last", "gnosh session rename",
		"gnosh sessions list", "gnosh s list", "gnosh session ls",
		"gnosh session list --json", "gnosh session show --json", "gnosh session last --json", "gnosh session rename --json",
		"gnosh mem", "gnosh logs", "gnosh logs -f", "gnosh logs --follow", "gnosh logs -t", "gnosh logs --tail", "gnosh dirs",
		"gnosh mcp add", "gnosh mcp add-json", "gnosh mcp get", "gnosh mcp list", "gnosh mcp remove", "gnosh mcp ls", "gnosh mcp rm",
		"gnosh mcp login", "gnosh mcp login --no-browser", "gnosh mcp logout",
		"gnosh mcp add -s", "gnosh mcp add --scope", "gnosh mcp add -t", "gnosh mcp add --transport",
		"gnosh mcp add -e", "gnosh mcp add --env", "gnosh mcp add -H", "gnosh mcp add --header", "gnosh mcp remove -s",
		"gnosh --mcp-config", "gnosh --strict-mcp-config",
		"gnosh help", "gnosh version", "gnosh --allow-dangerously-skip-permissions",
		"gnosh prompt show", "gnosh prompt path", "gnosh prompt export",
	} {
		if !have[want] {
			t.Errorf("the table has no %s", want)
		}
	}
	for _, gone := range []string{
		// What the specs list as not carried over, or not adopted.
		"gnosh server", "gnosh login", "gnosh logout", "gnosh update-providers", "gnosh schema",
		"gnosh -H", "gnosh --host", "gnosh --from-pr", "gnosh --betas",
		"gnosh --plugin-dir", "gnosh --plugin-url", "gnosh --prompt-suggestions", "gnosh --file",
		"gnosh auth", "gnosh install", "gnosh plugin", "gnosh mcp serve",
		// What spec/DECISIONS.md item 26 took off: print mode is -p, a
		// session is named with --resume, and there is no other data folder.
		"gnosh run", "gnosh r", "gnosh -q", "gnosh --quiet", "gnosh -s", "gnosh --session", "gnosh -D", "gnosh --data-dir",
		// What the specs name and nothing builds yet.

		"gnosh session delete", "gnosh session rm", "gnosh session web",
		"gnosh prompt tools", "gnosh prompt context",
		"gnosh projects", "gnosh stats",
	} {
		if have[gone] {
			t.Errorf("the table has %s, which is not to be on the command line", gone)
		}
	}
}

// TestSharedTable parses from many goroutines with one table. Parse and
// Help change nothing in it, which the race detector checks.
func TestSharedTable(t *testing.T) {
	tab := allBuilt(t)
	lines := [][]string{
		{"-y", "-C"},
		{"-p", "--allowedTools", "Bash(git diff *),Edit", "do it"},
		{"session", "list", "--json"},
		{"--alowedTools", "x"},
		{"docter"},
		{"--help"},
		{"mem", "find", "-x", "y"},
	}
	want := make([]string, len(lines))
	for i, l := range lines {
		inv, err := tab.Parse(l, tty)
		want[i] = describe(inv, err)
	}
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for n := 0; n < 200; n++ {
				i := n % len(lines)
				inv, err := tab.Parse(lines[i], tty)
				if got := describe(inv, err); got != want[i] {
					t.Errorf("Parse(%q) gave a different answer on another goroutine:\n  %s\n  %s", lines[i], got, want[i])
					return
				}
				if _, err := tab.Help("session", "list"); err != nil {
					t.Error(err)
					return
				}
			}
		}()
	}
	wg.Wait()
}
