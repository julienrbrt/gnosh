package cli

import (
	"errors"
	"strings"
	"testing"
)

// TestUnknownFlags covers flags gnosh does not have: the error names what
// was typed and the nearest real flag.
func TestUnknownFlags(t *testing.T) {
	runLines(t, allBuilt(t), []lineCase{
		{line: `gnosh --alowedTools Read`, kind: ErrUnknownFlag, hint: "--allowedTools", say: []string{"--alowedTools is not a flag of gnosh, so nothing was started. Did you mean --allowedTools?"}},
		{line: `gnosh --allowedtools Read`, kind: ErrUnknownFlag, hint: "--allowedTools"},
		{line: `gnosh --allowed_tools Read`, kind: ErrUnknownFlag, hint: "--allowedTools"},
		{line: `gnosh --AllowedTools Read`, kind: ErrUnknownFlag, hint: "--allowedTools"},
		{line: `gnosh --dissallowed-tools Bash`, kind: ErrUnknownFlag, hint: "--disallowed-tools"},
		{line: `gnosh --Print x`, kind: ErrUnknownFlag, hint: "--print"},
		{line: `gnosh --contnue`, kind: ErrUnknownFlag, hint: "--continue"},
		{line: `gnosh --cont`, kind: ErrUnknownFlag, hint: "--continue"},
		{line: `gnosh --outputformat json`, kind: ErrUnknownFlag, hint: "--output-format"},
		{line: `gnosh --output_format json`, kind: ErrUnknownFlag, hint: "--output-format"},
		{line: `gnosh --permision-mode plan`, kind: ErrUnknownFlag, hint: "--permission-mode"},
		{line: `gnosh --modle qwen`, kind: ErrUnknownFlag, hint: "--model"},
		{line: `gnosh --mode plan`, kind: ErrUnknownFlag, hint: "--model"},
		{line: `gnosh --skip-permissions`, kind: ErrUnknownFlag, say: []string{"--skip-permissions is not a flag of gnosh"}},
		{line: `gnosh --system-promt x`, kind: ErrUnknownFlag, hint: "--system-prompt"},
		{line: `gnosh --verbos`, kind: ErrUnknownFlag, hint: "--verbose"},
		{line: `gnosh -p --max-turn 3 x`, kind: ErrUnknownFlag, hint: "--max-turns", say: []string{"--max-turn is not a flag of gnosh"}},
		{line: `gnosh --model=x --bogus=y`, kind: ErrUnknownFlag, say: []string{"--bogus is not a flag of gnosh"}},
		{line: `gnosh --=x`, kind: ErrUnknownFlag, say: []string{"-- is not a flag of gnosh"}},
		{line: `gnosh ---x`, kind: ErrUnknownFlag, say: []string{"---x is not a flag of gnosh"}},
		{line: `gnosh --frobnicate`, kind: ErrUnknownFlag, say: []string{`--frobnicate is not a flag of gnosh, so nothing was started. "gnosh --help" lists the flags; a prompt that begins with a dash goes after --.`}},

		// A word with one dash, as some programs take them.
		{line: `gnosh -print x`, kind: ErrUnknownFlag, hint: "--print", say: []string{"-print is not a flag of gnosh", "Flags spelled as words take two dashes: did you mean --print?"}},
		{line: `gnosh -model qwen`, kind: ErrUnknownFlag, hint: "--model"},
		{line: `gnosh -model=qwen`, kind: ErrUnknownFlag, hint: "--model"},
		{line: `gnosh -version`, kind: ErrUnknownFlag, hint: "--version"},
		{line: `gnosh -help`, kind: ErrUnknownFlag, hint: "--help"},

		// A flag of another command says where it belongs.
		{line: `gnosh --json`, kind: ErrUnknownFlag, say: []string{"--json does not go with gnosh, so nothing was started. It is a flag of gnosh session list, gnosh session show, gnosh session last and others."}},
		{line: `gnosh --cwd x version --cwd y`, kind: ErrUnknownFlag, say: []string{"--cwd does not go with gnosh version, so nothing was done. It is a flag of gnosh, gnosh doctor, gnosh trust and others."}},
		{line: `gnosh --list`, kind: ErrUnknownFlag, say: []string{"--list does not go with gnosh", "It is a flag of gnosh trust."}},
		{line: `gnosh -f`, kind: ErrUnknownFlag, say: []string{"-f does not go with gnosh", "It is a flag of gnosh logs."}},

		// What the specs list as not carried over is simply unknown.
		{line: `gnosh --host box`, kind: ErrUnknownFlag, say: []string{"--host is not a flag of gnosh"}},
		{line: `gnosh -H box`, kind: ErrUnknownFlag, say: []string{"-H does not go with gnosh", "It is a flag of gnosh mcp add."}},
		{line: `gnosh --from-pr 12`, kind: ErrUnknownFlag, say: []string{"--from-pr is not a flag of gnosh"}},
		{line: `gnosh --betas x`, kind: ErrUnknownFlag, say: []string{"--betas is not a flag of gnosh"}},
		{line: `gnosh --plugin-dir x`, kind: ErrUnknownFlag, say: []string{"--plugin-dir is not a flag of gnosh"}},
		{line: `gnosh --prompt-suggestions`, kind: ErrUnknownFlag, say: []string{"--prompt-suggestions is not a flag of gnosh"}},
		{line: `gnosh --chrome`, kind: ErrUnknownFlag, say: []string{"--chrome is not a flag of gnosh"}},
		{line: `gnosh --ide`, kind: ErrUnknownFlag, say: []string{"--ide is not a flag of gnosh"}},
		{line: `gnosh --teleport`, kind: ErrUnknownFlag, say: []string{"--teleport is not a flag of gnosh"}},
		{line: `gnosh --remote`, kind: ErrUnknownFlag, say: []string{"--remote is not a flag of gnosh"}},
		{line: `gnosh --file a:b`, kind: ErrUnknownFlag, say: []string{"--file is not a flag of gnosh"}},
		{line: `gnosh --enable-auto-mode`, kind: ErrUnknownFlag, say: []string{"--enable-auto-mode is not a flag of gnosh"}},
		// So are the ones Claude Code has and the specs put off till later.
		// -w is Claude Code's letter for --worktree, which gnosh spells out.
		{line: `gnosh -w feature`, kind: ErrUnknownFlag, hint: "--worktree", say: []string{"-w is not a flag of gnosh", "Did you mean --worktree?", "-cwd"}},

		// Only whole names count: no flag is guessed from its start, though
		// the start is enough for a suggestion.
		{line: `gnosh --verb`, kind: ErrUnknownFlag, hint: "--verbose"},
		{line: `gnosh --dangerously-skip`, kind: ErrUnknownFlag, hint: "--dangerously-skip-permissions"},
		{line: `gnosh --permission-mo`, kind: ErrUnknownFlag, hint: "--permission-mode"},
		{line: `gnosh --permission-pr`, kind: ErrUnknownFlag, hint: "--permission-prompts"},
		// A start that two flags share suggests neither.
		{line: `gnosh --permission`, kind: ErrUnknownFlag, say: []string{"--permission is not a flag of gnosh"}},
		{line: `gnosh --system`, kind: ErrUnknownFlag, say: []string{"--system is not a flag of gnosh", `"gnosh --help" lists the flags`}},
	})
}

// TestTypedExactly covers CLI-33: an error repeats what was typed letter
// for letter, never re-capitalized or re-spelled.
func TestTypedExactly(t *testing.T) {
	tab := allBuilt(t)
	for _, c := range []struct {
		args  []string
		start string
	}{
		{[]string{"--Disallowed-Tools", "Bash"}, "--Disallowed-Tools is not a flag"},
		{[]string{"--disallowed-TOOLS", "Bash"}, "--disallowed-TOOLS is not a flag"},
		{[]string{"--allowedTools", "Bash(Git:* (x),Edit(y)"}, `--allowedTools "Bash(Git:* (x),Edit(y)": its parentheses`},
		{[]string{"--disallowed-tools", "Bash(Git:*", "Read(a (b) c"}, `--disallowed-tools "Read(a (b) c": its parentheses`},
		{[]string{"--permission-mode", "ACCEPTedits"}, "--permission-mode takes default, plan, acceptEdits, dontAsk or bypassPermissions, and ACCEPTedits is none of them"},
		{[]string{"--session-id", "Bash(git:*)"}, "--session-id Bash(git:*) is not a UUID"},
		{[]string{"DOCTOR"}, "DOCTOR is not a gnosh command"},
		{[]string{"session", "LIST"}, "LIST is not a command of gnosh session"},
		{[]string{"-ZZ"}, "-ZZ: -Z is not a flag"},
		{[]string{"--allowed-tools=x", "--Model", "q"}, "--Model is not a flag"},
		{[]string{"--MODEL=q"}, "--MODEL is not a flag"},
		{[]string{"--output-format", "Stream-JSON", "-p", "x"}, "--output-format takes text, json or stream-json, and Stream-JSON is none of them"},
	} {
		_, err := tab.Parse(c.args, tty)
		if err == nil {
			t.Errorf("Parse(%q) succeeded; want an error", c.args)
			continue
		}
		if !strings.HasPrefix(err.Error(), c.start) {
			t.Errorf("Parse(%q):\n  got  %s\n  want it to begin %s", c.args, err, c.start)
		}
	}
}

// TestMistypedCommands covers the rule that tells a mistyped command from a
// prompt: one slip away from a command's name, on a line that would make
// sense for that command.
func TestMistypedCommands(t *testing.T) {
	runLines(t, allBuilt(t), []lineCase{
		{line: `gnosh docter`, kind: ErrUnknownCommand, hint: "doctor", say: []string{"docter is not a gnosh command, so nothing was started. Did you mean doctor? If it is your prompt, put -- before it: gnosh -- docter"}},
		{line: `gnosh doctr`, kind: ErrUnknownCommand, hint: "doctor"},
		{line: `gnosh doctors`, kind: ErrUnknownCommand, hint: "doctor"},
		{line: `gnosh dcotor`, kind: ErrUnknownCommand, hint: "doctor"},
		{line: `gnosh Doctor`, kind: ErrUnknownCommand, hint: "doctor"},
		{line: `gnosh -y docter`, kind: ErrUnknownCommand, hint: "doctor"},
		{line: `gnosh model`, kind: ErrUnknownCommand, hint: "models"},
		{line: `gnosh log`, kind: ErrUnknownCommand, hint: "logs"},
		{line: `gnosh dir`, kind: ErrUnknownCommand, hint: "dirs"},
		{line: `gnosh trst`, kind: ErrUnknownCommand, hint: "trust"},
		{line: `gnosh hlep`, kind: ErrUnknownCommand, hint: "help"},
		{line: `gnosh verison`, kind: ErrUnknownCommand, hint: "version"},
		{line: `gnosh sesions`, kind: ErrUnknownCommand, hint: "sessions"},
		// A three-letter command: only swapped letters, or the wrong case.
		{line: `gnosh mme`, kind: ErrUnknownCommand, hint: "mem"},
		{line: `gnosh emm`, kind: ErrUnknownCommand, hint: "mem"},
		{line: `gnosh MEM`, kind: ErrUnknownCommand, hint: "mem"},
		{line: `gnosh men`, want: inter(Invocation{Prompt: "men"})},
		{line: `gnosh mum`, want: inter(Invocation{Prompt: "mum"})},
		{line: `gnosh mems`, want: inter(Invocation{Prompt: "mems"})},
		{line: `gnosh me`, want: inter(Invocation{Prompt: "me"})},
		// One-letter aliases are never matched.
		{line: `gnosh a`, want: inter(Invocation{Prompt: "a"})},
		{line: `gnosh S`, want: inter(Invocation{Prompt: "S"})},
		{line: `gnosh x`, want: inter(Invocation{Prompt: "x"})},

		// With more words, only when the next word is one of the near
		// command's own commands.
		{line: `gnosh sesion list`, kind: ErrUnknownCommand, hint: "session"},
		{line: `gnosh sessoin lst`, kind: ErrUnknownCommand, hint: "session"},
		{line: `gnosh sesion of the day`, want: inter(Invocation{Prompt: "sesion of the day"})},
		{line: `gnosh doctors hate this`, want: inter(Invocation{Prompt: "doctors hate this"})},
		{line: `gnosh model the data as a graph`, want: inter(Invocation{Prompt: "model the data as a graph"})},
		{line: `gnosh log the output`, want: inter(Invocation{Prompt: "log the output"})},
		{line: `gnosh trst ../folder`, want: inter(Invocation{Prompt: "trst ../folder"})},
		{line: `gnosh mme the files`, want: inter(Invocation{Prompt: "mme the files"})},

		// Anything else is a prompt: a word far from every command, a
		// quoted sentence, a word after "--" or after -p.
		{line: `gnosh hello`, want: inter(Invocation{Prompt: "hello"})},
		{line: `gnosh hi`, want: inter(Invocation{Prompt: "hi"})},
		{line: `gnosh continue`, want: inter(Invocation{Prompt: "continue"})},
		{line: `gnosh "docter who"`, want: inter(Invocation{Prompt: "docter who"})},
		{line: `gnosh "doctor my code"`, want: inter(Invocation{Prompt: "doctor my code"})},
		{line: `gnosh -- docter`, want: inter(Invocation{Prompt: "docter"})},
		{line: `gnosh -p docter`, want: pr(Invocation{Prompt: "docter"})},
		{line: `gnosh docter -p`, want: pr(Invocation{Prompt: "docter"})},
		{line: `gnosh fix docter`, want: inter(Invocation{Prompt: "fix docter"})},

		// Commands the earlier gnosh had and this one does not are words
		// like any other.
		{line: `gnosh server`, want: inter(Invocation{Prompt: "server"})},
		{line: `gnosh login`, want: inter(Invocation{Prompt: "login"})},
		{line: `gnosh logout`, want: inter(Invocation{Prompt: "logout"})},
		{line: `gnosh update-providers`, want: inter(Invocation{Prompt: "update-providers"})},
		{line: `gnosh schema`, want: inter(Invocation{Prompt: "schema"})},
	})
}

func TestShown(t *testing.T) {
	long := strings.Repeat("x", 100)
	for in, want := range map[string]string{
		"--Disallowed-Tools": "--Disallowed-Tools",
		"Bash(git:*)":        "Bash(git:*)",
		"héllo":              "héllo",
		"":                   `""`,
		"two words":          `"two words"`,
		"tab\there":          `"tab\there"`,
		"line\nbreak":        `"line\nbreak"`,
		"esc\x1b[31m":        `"esc\x1b[31m"`,
		"bad\xffbyte":        `"bad\xffbyte"`,
		`say "hi"`:           `"say \"hi\""`,
		`back\slash`:         `"back\\slash"`,
		"nbsp\u00a0here":     `"nbsp\u00a0here"`,
		long:                 `"` + strings.Repeat("x", shownMax) + `"...`,
	} {
		if got := shown(in); got != want {
			t.Errorf("shown(%q) = %s, want %s", in, got, want)
		}
	}
}

// TestMessagesArePlain makes sure nothing typed can smuggle control
// characters into a message, and that long text is cut.
func TestMessagesArePlain(t *testing.T) {
	tab := allBuilt(t)
	for _, args := range [][]string{
		{"--bad\x1b[31mflag"},
		{"-\x07"},
		{"--model", "\x1b]0;title\x07", "--output-format", "x\x1by"},
		{"session", "li\x00st"},
		{"--permission-mode", "pl\nan"},
		{"doc\x1btor"},
		{"--allowedTools", "A(b (c),D(\x1b[0m)"},
		{"--" + strings.Repeat("y", 5000)},
		{"trust", "a", strings.Repeat("z", 5000)},
		{"-p", "x", "--max-turns", strings.Repeat("9", 400)},
		{"--session-id", "\xff\xfe"},
	} {
		_, err := tab.Parse(args, tty)
		var e *Error
		if !errors.As(err, &e) {
			t.Errorf("Parse(%q) = %v; want an error", args, err)
			continue
		}
		checkMessage(t, e)
		if len(e.Message) > 700 {
			t.Errorf("Parse(%.40q...): the message is %d bytes long:\n  %.200s...", args, len(e.Message), e.Message)
		}
	}
}

func TestDistance(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want int
	}{
		{"", "", 0}, {"a", "", 1}, {"", "abc", 3},
		{"doctor", "doctor", 0}, {"Doctor", "doctor", 0},
		{"docter", "doctor", 1}, {"doctr", "doctor", 1}, {"doctorr", "doctor", 1},
		{"udpate", "update", 1}, {"rnu", "run", 1}, {"ran", "run", 1},
		{"hello", "help", 2}, {"kitten", "sitting", 3},
		{"ab", "ba", 1}, {"abc", "cab", 2},
		{strings.Repeat("a", 100), "a", longestName},
	} {
		if got := distance(c.a, c.b); got != c.want {
			t.Errorf("distance(%q, %q) = %d, want %d", c.a, c.b, got, c.want)
		}
		if got := distance(c.b, c.a); got != c.want {
			t.Errorf("distance(%q, %q) = %d, want %d", c.b, c.a, got, c.want)
		}
	}
}

func TestNearest(t *testing.T) {
	names := []string{"continue", "resume", "settings", "setting-sources", "model", "allowedTools", "allowed-tools", "system-prompt", "system-prompt-file"}
	for _, c := range []struct {
		typed string
		limit int
		want  string
	}{
		{"contnue", 2, "continue"},
		{"CONTINUE", 1, "continue"},
		{"allowed_tools", 1, "allowedTools"},
		{"allowedtools", 1, "allowedTools"},
		{"sett", 1, ""}, // the start of two names
		{"mod", 1, "model"},
		{"modle", 1, "model"},
		{"system", 3, ""},
		{"zzz", 3, ""},
		{"", 3, ""},
		{"x", 3, ""},
		{strings.Repeat("m", 200), 3, ""},
	} {
		if got := nearest(c.typed, names, c.limit); got != c.want {
			t.Errorf("nearest(%q, limit %d) = %q, want %q", c.typed, c.limit, got, c.want)
		}
	}
}
