package cli

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

// describe writes what Parse returned as one string, for comparing two
// answers.
func describe(inv Invocation, err error) string {
	if err != nil {
		return "error: " + err.Error()
	}
	return fmt.Sprintf("%+v", inv)
}

// flagUse is one flag as one command takes it, with what a line needs
// around it for the flag to be allowed there.
type flagUse struct {
	c *command
	f *flag
	// print says the line is given -p (only for gnosh itself), which is
	// what decides the meaning of -v.
	print bool
}

// uses lists every flag of every command, and for gnosh itself each flag
// both without and with -p.
func uses(tab *Table) []flagUse {
	var out []flagUse
	for _, c := range tab.commands {
		if c.needSub {
			continue
		}
		for _, f := range c.flags {
			switch {
			case c == tab.root:
				if !f.printOnly {
					out = append(out, flagUse{c, f, false})
				}
				// (--bg starts a session in the background, never with -p.)
				if f.id != idPrint && f.id != idBack {
					out = append(out, flagUse{c, f, true})
				}
			default:
				out = append(out, flagUse{c, f, false})
			}
		}
	}
	return out
}

// printLetterOf reports whether r is a letter that some flag of c takes over
// in print mode.
func printLetterOf(c *command, r rune) bool {
	for _, f := range c.flags {
		if f.printLetter == r {
			return true
		}
	}
	return false
}

// spellings returns the ways to type the flag in this use: all of them,
// but -v only where it means this flag.
func (u flagUse) spellings() []string {
	var out []string
	if u.print && u.f.printLetter != 0 {
		out = append(out, "-"+string(u.f.printLetter))
	}
	for _, s := range u.f.spellings() {
		if u.print && len(s) == 2 && printLetterOf(u.c, rune(s[1])) {
			continue
		}
		out = append(out, s)
	}
	return out
}

// sample returns a value the flag accepts.
func sample(f *flag) string {
	switch {
	case len(f.choices) > 0:
		// The last one, which is not the default where the flag has more
		// than one: the flag then shows.
		names := f.choiceNames()
		return names[len(names)-1]
	case f.number:
		return "7"
	case f.id == idSessionID:
		return sampleUUID
	case f.id == "--setting-sources":
		return "user,local"
	case f.list == listRules:
		return "Read,Bash(git diff *)"
	case f.list == listPairs:
		return "KEY=value"
	case f.list == listHeaders:
		return "X-Key: value"
	}
	return "value"
}

// around returns what a line needs besides the flag: the words and flags
// before it, and the command's arguments.
func (u flagUse) around() (before, args []string) {
	before = append(before, u.c.path()...)
	if u.c.session && u.print {
		before = append(before, "-p")
	}
	if u.f.id == idFork {
		before = append(before, "-C")
	}
	if u.f.id == "--include-partial-messages" || u.f.id == "--include-hook-events" {
		before = append(before, "--output-format", "stream-json")
	}
	switch u.f.id {
	case "--replay-user-messages":
		// The prompts come on standard input, one JSON line each.
		return append(before, "--input-format", "stream-json", "--output-format", "stream-json"), nil
	case "--input-format":
		// Its sample, stream-json, wants stream-json output and no prompt.
		return append(before, "--output-format", "stream-json"), nil
	}
	if u.c.session {
		return before, []string{"the prompt"}
	}
	for i := 0; i < u.c.minArgs; i++ {
		args = append(args, fmt.Sprintf("arg%d", i))
	}
	return before, args
}

// forms returns the ways to type one spelling of the flag with a value, and
// the ways to type it bare. Lines in the same group must mean the same.
func forms(f *flag, spelling, value string) (withValue, bare [][]string) {
	long := strings.HasPrefix(spelling, "--")
	switch f.kind {
	case kindBool:
		bare = append(bare, []string{spelling})
	case kindText, kindList:
		withValue = append(withValue, []string{spelling, value})
		if long {
			withValue = append(withValue, []string{spelling + "=" + value})
		}
	case kindOptional:
		bare = append(bare, []string{spelling})
		withValue = append(withValue, []string{spelling, value})
		if long {
			withValue = append(withValue, []string{spelling + "=" + value})
		}
	case kindAttached:
		bare = append(bare, []string{spelling})
		if long {
			withValue = append(withValue, []string{spelling + "=" + value})
		}
	}
	return withValue, bare
}

// lines returns the two places a flag can stand: before the command's
// arguments (which then go after "--", so that no list or optional value
// can reach them) and after them.
func (u flagUse) lines(form []string) [][]string {
	before, args := u.around()
	first := append(append(append([]string{}, before...), form...), "--")
	first = append(first, args...)
	second := append(append(append([]string{}, before...), args...), form...)
	return [][]string{first, second}
}

// TestEverySpelling types every flag of every command in every spelling,
// with its value given every way and the flag before and after the
// command's arguments, and checks that all of them parse, and to the same
// Invocation.
func TestEverySpelling(t *testing.T) {
	tab := allBuilt(t)
	count := 0
	for _, u := range uses(tab) {
		value := sample(u.f)
		var groups [2][][]string
		for _, s := range u.spellings() {
			withValue, bare := forms(u.f, s, value)
			for _, form := range withValue {
				groups[0] = append(groups[0], u.lines(form)...)
			}
			for _, form := range bare {
				groups[1] = append(groups[1], u.lines(form)...)
			}
		}
		if u.f.id == idResume && u.print {
			// A bare -r is refused in print mode, by rule.
			groups[1] = nil
		}
		for _, lines := range groups {
			var first Invocation
			for i, line := range lines {
				count++
				inv, err := tab.Parse(line, tty)
				if err != nil {
					t.Errorf("%s in %s: Parse(%q) failed: %v", u.f.id, u.c.title(), line, err)
					continue
				}
				if i == 0 {
					first = inv
					continue
				}
				if d := diff(inv, first); d != "" {
					t.Errorf("%s in %s: Parse(%q) differs from Parse(%q):\n%s", u.f.id, u.c.title(), line, lines[0], d)
				}
			}
		}
		// The flag did something: the line with it is not the line without.
		if u.f.id == idHelp || u.f.id == idVersion {
			continue
		}
		before, args := u.around()
		without, err := tab.Parse(append(before, args...), tty)
		for _, lines := range groups {
			if len(lines) == 0 || err != nil {
				continue
			}
			with, err := tab.Parse(lines[0], tty)
			if err == nil && diff(with, without) == "" && !redundant(u) {
				t.Errorf("%s in %s: Parse(%q) is the same as without the flag", u.f.id, u.c.title(), lines[0])
			}
		}
	}
	if count < 300 {
		t.Errorf("only %d lines were tried; the table should give many more", count)
	}
}

// redundant reports whether the flag adds nothing to the line it is tried
// on: a flag with one value, which is what gnosh does without it too
// (--input-format text).
func redundant(u flagUse) bool {
	return len(u.f.choiceNames()) == 1
}

// FuzzSpellings gives every flag an arbitrary value and checks that its
// spellings still agree with each other: typed the same way, two spellings
// of one flag either both parse, to the same Invocation, or both fail in
// the same way.
func FuzzSpellings(f *testing.F) {
	tab := allBuilt(f)
	all := uses(tab)
	for i := range all {
		f.Add(uint16(i), sample(all[i].f), uint8(3))
	}
	for _, v := range []string{"", " ", "-x", "--", "--help", "a b", "a,b", "Bash(", "x\ny", "=", "-", "\xff"} {
		f.Add(uint16(len(v)*7), v, uint8(len(v)))
	}
	f.Fuzz(func(t *testing.T, which uint16, value string, envBits uint8) {
		u := all[int(which)%len(all)]
		env := Env{StdinIsTerminal: envBits&1 != 0, StdoutIsTerminal: envBits&2 != 0}
		// Lines typed the same way, one per spelling, by kind of spelling.
		type shape struct {
			long bool
			form int
			bare bool
			spot int
		}
		answers := map[shape]string{}
		typed := map[shape][]string{}
		for _, s := range u.spellings() {
			withValue, bare := forms(u.f, s, value)
			for group, fs := range [][][]string{withValue, bare} {
				for form, fm := range fs {
					for spot, line := range u.lines(fm) {
						inv, err := tab.Parse(line, env)
						got := describe(inv, nil)
						if err != nil {
							var e *Error
							if !errors.As(err, &e) {
								t.Fatalf("Parse(%q) returned an error that is not a *Error: %v", line, err)
							}
							got = "error of kind " + string(e.Kind)
						}
						k := shape{strings.HasPrefix(s, "--"), form, group == 1, spot}
						if prev, ok := answers[k]; ok && prev != got {
							t.Errorf("two spellings of %s disagree:\n  Parse(%q) = %s\n  Parse(%q) = %s", u.f.id, typed[k], prev, line, got)
						}
						answers[k], typed[k] = got, line
					}
				}
			}
		}
	})
}
