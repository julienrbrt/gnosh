package permission

import (
	"strings"
	"testing"
	"time"

	"github.com/gnosh-ai/gnosh/internal/core"
)

func TestCouldMatch(t *testing.T) {
	toks := func(s string) []tok {
		var out []tok
		for i := 0; i < len(s); i++ {
			if s[i] == '*' {
				out = appendHole(out)
			} else {
				out = append(out, tok{b: s[i]})
			}
		}
		return out
	}
	for _, tt := range []struct {
		a, b string
		want bool
	}{
		{"rm *", "* -rf x", true},
		{"rm *", "rm*-rf*x", true},
		{"rm *", "echo *", false},
		{"git push *", "git * origin", true},
		{"git push *", "git status *", false},
		{"git push *", "git *", true},
		{"git push *", "*", true},
		{"abc", "abc", true},
		{"abc", "abd", false},
		{"a*c", "abc", true},
		{"a*c", "ab", false},
		{"", "", true},
		{"", "*", true},
		{"*", "", true},
		{"a", "", false},
		{"*a*", "*b*", true},
		{"a*", "*b", true},
		{"a*b", "b*a", false},
		{"a*a", "a", false},
	} {
		if got := couldMatch(toks(tt.a), toks(tt.b)); got != tt.want {
			t.Errorf("couldMatch(%q, %q) = %v, want %v", tt.a, tt.b, got, tt.want)
		}
		if got := couldMatch(toks(tt.b), toks(tt.a)); got != tt.want {
			t.Errorf("couldMatch(%q, %q) = %v, want %v", tt.b, tt.a, got, tt.want)
		}
	}
}

// TestGuardsThatCannotBeChecked: when part of a command is only known
// once the shell runs it, a deny or ask rule that could apply makes the
// engine ask, in every mode; a rule that could not apply changes nothing.
func TestGuardsThatCannotBeChecked(t *testing.T) {
	r := rules{deny: []string{"Bash(rm *)", "Bash(git push *)"}, ask: []string{"Bash(npm publish*)"}}
	e := engine(t, ModeBypass, r)
	for _, cmd := range []string{
		"$CMD -rf x", "${CMD} -rf x", "$(echo rm) -rf x", "`echo rm` -rf x", "r$X -rf x", "$X$Y",
		"/bin/r? -rf x", "r[m] -rf x", "~user/rm x", "=rm x",
		"git ${X:-push} origin", "git $SUB origin main", "git pus? origin", "git $(echo push)",
		"npm $X", "npm pub$X", "env $X y", "sudo $FLAGS y", "xargs $X",
	} {
		d := bash(e, cmd)
		if d.Behavior != Ask || d.Stage != StageSafety {
			t.Errorf("%q: got %s at %s (%s), want ask at the safety stage", cmd, d.Behavior, d.Stage, d.Reason)
		}
		if h := d.WithHook(HookAllow, ""); h.Behavior != Ask {
			t.Errorf("%q: a hook's approval passed an unchecked guard", cmd)
		}
	}
	// Braces can be worked out, and then the rule is checked: these are
	// refused outright.
	// ($IFS is a blank, a tab and a line break.)
	for _, cmd := range []string{"{rm,-rf,x}", "git {push,origin}", "{git,push} origin", "bash {-c,rm}", "rm${IFS}-rf${IFS}x", "git${IFS}push${IFS}origin",
		// (What follows a wrapper's unknown word may be the command.)
		"env $X rm y", "sudo $FLAGS rm y"} {
		if d := bash(e, cmd); d.Behavior != Deny {
			t.Errorf("%q: got %s at %s (%s), want it refused", cmd, d.Behavior, d.Stage, d.Reason)
		}
	}
	for _, cmd := range []string{
		"echo $HOME", "go test $PKG", "git status $X", "git log ${REV:-HEAD}", "ls *.go", "cat {a,b}",
		"make $(nproc)", "npm run $X", "$HOME/bin/tool x", "git commit -m \"$MSG\"", "cd $DIR && ls",
	} {
		if d := bash(e, cmd); d.Behavior != Allow {
			t.Errorf("%q: got %s (%s), want allow: no guard could apply", cmd, d.Behavior, d.Reason)
		}
	}
	// dontAsk refuses instead.
	if d := bash(engine(t, ModeDontAsk, r), "$CMD -rf x"); d.Behavior != Deny {
		t.Errorf("dontAsk: %+v", d)
	}
}

func TestSmallGuards(t *testing.T) {
	// A Skill deny is not dodged by adding an argument.
	e := engine(t, ModeBypass, rules{deny: []string{"Skill(danger)"}, ask: []string{"Skill(careful)"}})
	if d := other(e, "Skill", "skill", "danger", "args", "now"); d.Behavior != Deny {
		t.Errorf("Skill deny with arguments: %+v", d)
	}
	if d := other(e, "Skill", "skill", "careful", "args", "now"); d.Behavior != Ask {
		t.Errorf("Skill ask with arguments: %+v", d)
	}
	if d := other(e, "Skill", "skill", "/danger"); d.Behavior != Deny {
		t.Errorf("a leading slash names the same skill: %+v", d)
	}

	// A shell tool call with no command has nothing to be judged by.
	e = engine(t, ModeDefault, rules{})
	if d := bash(e, ""); d.Behavior != Ask {
		t.Errorf("empty command: %+v", d)
	}
	if d := bash(e, "  \n "); d.Behavior != Ask {
		t.Errorf("blank command: %+v", d)
	}
	ws := e.Decide("Monitor", coreCall("Monitor", "ws", map[string]any{"url": "wss://example.com"}), core.Action{Kind: core.ActionNetwork, URL: "wss://example.com"})
	if ws.Behavior != Ask {
		t.Errorf("Monitor on a socket: %+v", ws)
	}

	// A URL whose host cannot be read is not waved past rules about
	// hosts.
	e = engine(t, ModeBypass, rules{deny: []string{"WebFetch(domain:evil.example.com)"}})
	if d := fetch(e, "evil.example.com/x"); d.Behavior != Ask {
		t.Errorf("a URL with no readable host: %+v", d)
	}
	if d := fetch(e, "https://fine.example.com/x"); d.Behavior != Allow {
		t.Errorf("a readable host: %+v", d)
	}

	// ${name:=value} assigns.
	e = engine(t, ModeDefault, rules{allow: []string{"Bash(echo *)"}})
	if d := bash(e, "echo ${PATH:=/tmp}"); d.Behavior != Ask {
		t.Errorf("an assigning expansion: %+v", d)
	}
	if d := bash(e, "echo ${X:-fallback}"); d.Behavior != Allow {
		t.Errorf("a default-value expansion under an allow rule: %+v", d)
	}
}

func TestLayersComeAndGo(t *testing.T) {
	e := engine(t, ModeDefault, rules{deny: []string{"Read(*.pem)"}})
	if d := bash(e, "git add ."); d.Behavior != Ask {
		t.Fatalf("before the skill: %+v", d)
	}
	skill := Layer{Scope: ScopeSkill, Name: "skill commit", Allow: []string{"Bash(git add *)", "Bash(git commit *)"}}
	if err := e.AddLayer(skill); err != nil {
		t.Fatal(err)
	}
	if d := bash(e, "git add . && git commit -m x"); d.Behavior != Allow || d.Source != "skill commit" {
		t.Errorf("during the skill's turn: %+v", d)
	}
	e.RemoveLayers(ScopeSkill)
	if d := bash(e, "git add ."); d.Behavior != Ask {
		t.Errorf("after the skill's turn: %+v", d)
	}
	if d := file(e, "Read", "/proj/key.pem"); d.Behavior != Deny {
		t.Errorf("removing skill layers dropped another layer: %+v", d)
	}
	if err := e.AddLayer(Layer{Scope: ScopeSkill, Name: "bad skill", Allow: []string{"Nope(x)"}}); err == nil {
		t.Errorf("AddLayer took a rule that cannot be applied")
	}
	// A project skill's grants wait for trust like any project layer.
	e.SetTrusted(false)
	if err := e.AddLayer(Layer{Scope: ScopeSkill, Name: "project skill", FromProject: true, Allow: []string{"Bash(make *)"}}); err != nil {
		t.Fatal(err)
	}
	if d := bash(e, "make x"); d.Behavior != Ask {
		t.Errorf("an untrusted project's skill granted something: %+v", d)
	}

	if !e.ReadDenied("/proj/a/key.pem") || e.ReadDenied("/proj/a/key.txt") {
		t.Errorf("ReadDenied disagrees with the Read deny rule")
	}
}

func TestDecideAs(t *testing.T) {
	// A sub-agent with a mode of its own shares the session's rules and
	// approvals; only the mode differs.
	e := engine(t, ModeDefault, rules{allow: []string{"Bash(make *)"}, deny: []string{"Bash(rm *)"}})
	edit := func(mode Mode) Behavior {
		return e.DecideAs(mode, "Edit", coreCall("Edit", "file_path", "/proj/a.go"), core.Action{Kind: core.ActionEdit, Paths: []string{"/proj/a.go"}}).Behavior
	}
	for mode, want := range map[Mode]Behavior{
		ModeDefault: Ask, ModeAcceptEdits: Allow, ModePlan: Deny, ModeDontAsk: Deny, ModeBypass: Allow,
	} {
		if got := edit(mode); got != want {
			t.Errorf("an edit judged as %s: got %s, want %s", mode, got, want)
		}
	}
	if e.Mode() != ModeDefault {
		t.Errorf("DecideAs changed the session's mode to %s", e.Mode())
	}
	if d := e.DecideAs(ModeBypass, "Bash", coreCall("Bash", "command", "rm x"), coreAction("rm x")); d.Behavior != Deny {
		t.Errorf("the session's deny rules apply in any mode: %+v", d)
	}
	if d := e.DecideAs(ModePlan, "Bash", coreCall("Bash", "command", "make x"), coreAction("make x")); d.Behavior != Ask || d.Mode != ModePlan {
		t.Errorf("judged as plan: %+v", d)
	}
	if d := e.DecideAs("auto", "Read", coreCall("Read"), core.Action{}); d.Behavior != Deny {
		t.Errorf("a mode gnosh does not have: %+v", d)
	}
	o := testOptions(ModeDefault, rules{})
	o.DisableBypass = true
	if d := mustEngine(t, o).DecideAs(ModeBypass, "Bash", coreCall("Bash", "command", "make"), coreAction("make")); d.Behavior != Deny {
		t.Errorf("bypass where the settings forbid it: %+v", d)
	}
}

func TestBlankCommands(t *testing.T) {
	e := engine(t, ModeDefault, rules{})
	for _, cmd := range []string{"", " ", "\n", " \\\n ", "\\\n", "# only a comment", "\t\n\n"} {
		if d := bash(e, cmd); d.Behavior != Ask {
			t.Errorf("%q: got %s (%s), want ask", cmd, d.Behavior, d.Reason)
		}
	}
	if d := bash(e, "cat < /dev/null"); d.Behavior != Allow {
		t.Errorf("input from the null device: %+v", d)
	}
}

func TestFileRulesThatCannotBeChecked(t *testing.T) {
	r := rules{allow: []string{"Bash"}, deny: []string{"Read(.env)"}}
	// Under an allow rule the unchecked guard asks; with permissions
	// skipped it is let go (see safety).
	if d := bash(engine(t, ModeDefault, r), "cat $F"); d.Behavior != Ask || d.Stage != StageSafety {
		t.Errorf("default: %+v", d)
	}
	if d := bash(engine(t, ModeBypass, r), "cat $F"); d.Behavior != Allow {
		t.Errorf("bypass: %+v", d)
	}
	if d := bash(engine(t, ModeBypass, r), "cat .env"); d.Behavior != Deny {
		t.Errorf("a file named outright is still refused: %+v", d)
	}
}

// TestPathologicalLinesComeBack: lines built to make the engine work
// hard are answered promptly, and never with a yes.
func TestPathologicalLinesComeBack(t *testing.T) {
	rep := func(s string, n int) string { return strings.Repeat(s, n) }
	lines := []string{
		rep("sudo ", 1900) + "rm x",
		rep("nice -x ", 1200) + "rm x",
		rep("watch ", 1600) + "rm x",
		rep("env -S ", 1400) + "rm",
		rep("$X ", 3300),
		rep("a$X", 3300) + " b",
		rep("a;", 4900) + "rm x",
		rep("$(", 60) + "rm x" + rep(")", 60),
		rep("(", 70) + "rm x" + rep(")", 70),
		rep("{ ", 100) + "rm x" + rep("; }", 100),
		rep("eval ", 1900) + "rm x",
		"sh -c '" + rep("sh -c \"", 1) + "rm x" + rep("\"", 1) + "'",
		rep("cat <<E\n", 500) + rep("E\n", 500),
		rep("a|", 4900) + "rm x",
		rep("! ", 3000) + "rm x",
		"ls " + rep("*", 9000),
		"echo " + rep("{a,b}", 1900),
	}
	e := engine(t, ModeBypass, rules{
		allow: []string{"Bash(echo *)"},
		deny:  []string{"Bash(rm *)", "Bash(*a*a*a*a*a*a*a*b)", "Bash(git * push * main *)"},
		ask:   []string{"Bash(* --force *)"},
	})
	for _, line := range lines {
		start := time.Now()
		d := bash(e, line)
		if took := time.Since(start); took > 10*time.Second {
			t.Errorf("%.40q... (%d bytes) took %s", line, len(line), took)
		}
		if strings.Contains(line, "rm x") && d.Behavior == Allow {
			t.Errorf("%.40q...: allowed (%s)", line, d.Reason)
		}
	}
	// The path matcher has the same promise.
	e = engine(t, ModeDefault, rules{deny: []string{"Read(" + rep("*a", 40) + "b)", "Read(" + rep("**/", 40) + "x)"}})
	start := time.Now()
	file(e, "Read", "/proj/"+rep("a", 4000))
	file(e, "Read", "/proj/"+rep("a/", 2000)+"y")
	if took := time.Since(start); took > 10*time.Second {
		t.Errorf("path matching took %s", took)
	}
}

// EditDenied answers for an Edit deny rule, as written or as resolved, and
// for no other list or tool: a rewind leaves such a file as it is (the
// rewind review of 2026-11-19).
func TestEditDenied(t *testing.T) {
	e := engine(t, ModeDefault, rules{deny: []string{"Edit(//proj/secret/**)", "Read(//proj/hidden.txt)"}, allow: []string{"Edit(//proj/open/**)"}})
	for p, want := range map[string]bool{
		"/proj/secret/a.txt":     true,
		"/proj/secret/deep/b.go": true,
		"/proj/hidden.txt":       false,
		"/proj/open/c.txt":       false,
		"/proj/other.txt":        false,
	} {
		if got := e.EditDenied(p); got != want {
			t.Errorf("EditDenied(%s) = %v, want %v", p, got, want)
		}
	}
}
