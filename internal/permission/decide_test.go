package permission

import (
	"strings"
	"sync"
	"testing"

	"github.com/gnosh-ai/gnosh/internal/core"
)

// TestModes is the table of what each mode lets run when no rule speaks.
func TestModes(t *testing.T) {
	type call func(e *Engine) Decision
	calls := []struct {
		name string
		do   call
		// want is the behaviour in default, acceptEdits, plan, dontAsk and
		// bypassPermissions, in that order.
		want [5]Behavior
	}{
		{"read inside", func(e *Engine) Decision { return file(e, "Read", "/proj/a.go") }, [5]Behavior{Allow, Allow, Allow, Allow, Allow}},
		{"grep inside", func(e *Engine) Decision { return file(e, "Grep", "/proj/src") }, [5]Behavior{Allow, Allow, Allow, Allow, Allow}},
		{"read in an added directory", func(e *Engine) Decision { return file(e, "Read", "/extra/a") }, [5]Behavior{Allow, Allow, Allow, Allow, Allow}},
		{"read outside", func(e *Engine) Decision { return file(e, "Read", "/etc/passwd") }, [5]Behavior{Ask, Ask, Ask, Deny, Allow}},
		{"edit inside", func(e *Engine) Decision { return file(e, "Edit", "/proj/a.go") }, [5]Behavior{Ask, Allow, Deny, Deny, Allow}},
		{"write inside", func(e *Engine) Decision { return file(e, "Write", "/proj/new.go") }, [5]Behavior{Ask, Allow, Deny, Deny, Allow}},
		{"notebook edit inside", func(e *Engine) Decision { return file(e, "NotebookEdit", "/proj/a.ipynb") }, [5]Behavior{Ask, Allow, Deny, Deny, Allow}},
		{"edit in an added directory", func(e *Engine) Decision { return file(e, "Edit", "/extra/a") }, [5]Behavior{Ask, Allow, Deny, Deny, Allow}},
		{"edit outside", func(e *Engine) Decision { return file(e, "Edit", "/etc/hosts") }, [5]Behavior{Ask, Ask, Deny, Deny, Allow}},
		{"edit a protected path", func(e *Engine) Decision { return file(e, "Edit", "/proj/.git/config") }, [5]Behavior{Ask, Ask, Ask, Deny, Allow}},
		{"read-only command", func(e *Engine) Decision { return bash(e, "git status") }, [5]Behavior{Allow, Allow, Allow, Allow, Allow}},
		{"other command", func(e *Engine) Decision { return bash(e, "make test") }, [5]Behavior{Ask, Ask, Ask, Deny, Allow}},
		{"file command inside", func(e *Engine) Decision { return bash(e, "rm -rf build") }, [5]Behavior{Ask, Allow, Ask, Deny, Allow}},
		{"dangerous removal", func(e *Engine) Decision { return bash(e, "rm -rf ~") }, [5]Behavior{Ask, Ask, Ask, Deny, Ask}},
		{"web fetch", func(e *Engine) Decision { return fetch(e, "https://example.com/") }, [5]Behavior{Ask, Ask, Ask, Deny, Allow}},
		{"web search", func(e *Engine) Decision { return other(e, "WebSearch", "query", "x") }, [5]Behavior{Ask, Ask, Ask, Deny, Allow}},
		{"MCP tool", func(e *Engine) Decision { return other(e, "mcp__srv__do") }, [5]Behavior{Ask, Ask, Ask, Deny, Allow}},
		{"skill", func(e *Engine) Decision { return other(e, "Skill", "skill", "deploy") }, [5]Behavior{Ask, Ask, Ask, Deny, Allow}},
		{"sub-agent", func(e *Engine) Decision { return other(e, "Agent", "prompt", "x") }, [5]Behavior{Allow, Allow, Allow, Allow, Allow}},
		{"stop a task", func(e *Engine) Decision { return other(e, "TaskStop", "task_id", "1") }, [5]Behavior{Allow, Allow, Allow, Allow, Allow}},
		{"question to the person", func(e *Engine) Decision { return other(e, "AskUserQuestion") }, [5]Behavior{Allow, Allow, Allow, Deny, Allow}},
		{"enter plan mode", func(e *Engine) Decision { return other(e, "EnterPlanMode") }, [5]Behavior{Allow, Allow, Allow, Allow, Allow}},
		// (Outside plan mode there is no plan to present: the plan review
		// of 2026-11-19.)
		{"present the plan", func(e *Engine) Decision { return other(e, "ExitPlanMode") }, [5]Behavior{Deny, Deny, Ask, Deny, Allow}},
	}
	for i, mode := range Modes {
		o := testOptions(mode, rules{})
		o.Layers = append(o.Layers, Layer{Scope: ScopeCLI, Name: "--add-dir", Dirs: []string{"/extra"}})
		e := mustEngine(t, o)
		for _, c := range calls {
			d := c.do(e)
			if d.Behavior != c.want[i] {
				t.Errorf("%s in %s: got %s (%s), want %s", c.name, mode, d.Behavior, d.Reason, c.want[i])
			}
			if d.Mode != mode {
				t.Errorf("%s in %s: decision records mode %q", c.name, mode, d.Mode)
			}
			if d.Reason == "" {
				t.Errorf("%s in %s: no reason", c.name, mode)
			}
			if d.Behavior != Ask && len(d.Suggestions) != 0 {
				t.Errorf("%s in %s: suggestions on a call that does not ask", c.name, mode)
			}
		}
	}
}

func TestAllowRulesInEachMode(t *testing.T) {
	r := rules{allow: []string{"Bash(make *)", "Edit(//proj/**)", "WebFetch(domain:example.com)", "mcp__srv__do", "WebSearch", "Skill(deploy)"}}
	type want struct{ make, edit, fetch, mcp, search, skill Behavior }
	for mode, w := range map[Mode]want{
		ModeDefault:     {Allow, Allow, Allow, Allow, Allow, Allow},
		ModeAcceptEdits: {Allow, Allow, Allow, Allow, Allow, Allow},
		ModeDontAsk:     {Allow, Allow, Allow, Allow, Allow, Allow},
		// While planning nothing that may change things runs, whatever
		// rule would allow it; looking at the web still does.
		ModePlan:   {Ask, Deny, Allow, Ask, Allow, Ask},
		ModeBypass: {Allow, Allow, Allow, Allow, Allow, Allow},
	} {
		e := engine(t, mode, r)
		got := want{
			bash(e, "make test").Behavior,
			file(e, "Edit", "/proj/a.go").Behavior,
			fetch(e, "https://example.com/x").Behavior,
			other(e, "mcp__srv__do").Behavior,
			other(e, "WebSearch", "query", "x").Behavior,
			other(e, "Skill", "skill", "deploy").Behavior,
		}
		if got != w {
			t.Errorf("%s: got %+v, want %+v", mode, got, w)
		}
	}
	// What an allow rule approved is said to come from that rule.
	e := engine(t, ModeDefault, r)
	if d := bash(e, "make test"); d.Stage != StageAllow || d.Rule != "Bash(make *)" || d.Source != "user settings" {
		t.Errorf("allowed by rule: %+v", d)
	}
	if d := bash(e, "ls"); d.Stage != StageMode || d.Rule != "" {
		t.Errorf("allowed by the mode: %+v", d)
	}
}

func TestPrecedenceAcrossLayers(t *testing.T) {
	layers := func(user, project, local, cli rules) []Layer {
		return []Layer{
			{Scope: ScopeUser, Name: "user", Base: "/home/u/.config/gnosh", Allow: user.allow, Ask: user.ask, Deny: user.deny},
			{Scope: ScopeProject, Name: "project", Allow: project.allow, Ask: project.ask, Deny: project.deny},
			{Scope: ScopeLocal, Name: "local", Allow: local.allow, Ask: local.ask, Deny: local.deny},
			{Scope: ScopeCLI, Name: "command line", Allow: cli.allow, Ask: cli.ask, Deny: cli.deny},
		}
	}
	tests := []struct {
		name                      string
		user, project, local, cli rules
		cmd                       string
		want                      Behavior
		source                    string
	}{
		{"deny in user beats allow in project", rules{deny: []string{"Bash(make *)"}}, rules{allow: []string{"Bash(make *)"}}, rules{}, rules{}, "make x", Deny, "user"},
		{"deny in project beats allow in user", rules{allow: []string{"Bash(make *)"}}, rules{deny: []string{"Bash(make *)"}}, rules{}, rules{}, "make x", Deny, "project"},
		{"deny in settings beats --allowedTools", rules{deny: []string{"Bash(make *)"}}, rules{}, rules{}, rules{allow: []string{"Bash"}}, "make x", Deny, "user"},
		{"--disallowedTools beats allow in settings", rules{allow: []string{"Bash"}}, rules{}, rules{}, rules{deny: []string{"Bash(make *)"}}, "make x", Deny, "command line"},
		{"ask in local beats allow in user", rules{allow: []string{"Bash(make *)"}}, rules{}, rules{ask: []string{"Bash(make deploy*)"}}, rules{}, "make deploy", Ask, "local"},
		{"deny beats ask", rules{ask: []string{"Bash(make *)"}}, rules{deny: []string{"Bash(make *)"}}, rules{}, rules{}, "make x", Deny, "project"},
		{"a narrower allow carves nothing out of a deny", rules{deny: []string{"Bash(git *)"}}, rules{}, rules{allow: []string{"Bash(git status)"}}, rules{}, "git status", Deny, "user"},
		{"a narrower allow carves nothing out of an ask", rules{ask: []string{"Bash(git *)"}}, rules{}, rules{allow: []string{"Bash(git push origin main)"}}, rules{}, "git push origin main", Ask, "user"},
		{"allow from any layer", rules{}, rules{}, rules{}, rules{allow: []string{"Bash(make *)"}}, "make x", Allow, "command line"},
		{"allow lists add up", rules{allow: []string{"Bash(make *)"}}, rules{allow: []string{"Bash(npm *)"}}, rules{}, rules{}, "make x && npm ci", Allow, "user"},
	}
	for _, tt := range tests {
		o := testOptions(ModeDefault, rules{})
		o.Layers = layers(tt.user, tt.project, tt.local, tt.cli)
		d := bash(mustEngine(t, o), tt.cmd)
		if d.Behavior != tt.want || d.Source != tt.source {
			t.Errorf("%s: got %s from %q (%s), want %s from %q", tt.name, d.Behavior, d.Source, d.Reason, tt.want, tt.source)
		}
	}

	// A /path rule is read from its own settings directory.
	o := testOptions(ModeDefault, rules{})
	o.Layers = []Layer{
		{Scope: ScopeUser, Name: "user", Base: "/home/u/.config/gnosh", Deny: []string{"Read(/secrets/**)"}},
		{Scope: ScopeProject, Name: "project", Deny: []string{"Read(/keys/**)"}},
		{Scope: ScopeSettingsFlag, Name: "--settings", Base: "/tmp/run", Deny: []string{"Read(/held/**)"}},
		{Scope: ScopeCLI, Name: "--disallowedTools", Deny: []string{"Read(/cli/**)"}},
	}
	e := mustEngine(t, o)
	for p, want := range map[string]Behavior{
		"/home/u/.config/gnosh/secrets/a": Deny,
		"/proj/secrets/a":                 Allow,
		"/proj/keys/a":                    Deny,
		"/tmp/run/held/a":                 Deny,
		"/proj/held/a":                    Allow,
		"/proj/cli/a":                     Deny,
	} {
		if d := file(e, "Read", p); d.Behavior != want && !(want == Allow && d.Behavior == Ask) {
			t.Errorf("%s: got %s, want %s", p, d.Behavior, want)
		}
	}
}

func TestUntrustedFolder(t *testing.T) {
	o := testOptions("", rules{allow: []string{"Bash(go test *)"}})
	o.Trusted = false
	o.Layers = append(o.Layers,
		Layer{Scope: ScopeProject, Name: "project settings", DefaultMode: "acceptEdits",
			Allow: []string{"Bash(make *)", "Edit(**)"}, Deny: []string{"Bash(curl *)"}, Ask: []string{"Bash(git push *)"},
			Dirs: []string{"/elsewhere"}},
		Layer{Scope: ScopeLocal, Name: "local settings tracked in git", FromProject: true, Allow: []string{"Bash(npm *)"}},
		Layer{Scope: ScopeLocal, Name: "local settings", Allow: []string{"Bash(cargo *)"}},
	)
	e := mustEngine(t, o)
	check := func(when string, want map[string]Behavior) {
		t.Helper()
		for cmd, w := range want {
			if d := bash(e, cmd); d.Behavior != w {
				t.Errorf("%s: %q: got %s (%s), want %s", when, cmd, d.Behavior, d.Reason, w)
			}
		}
	}
	// Until the folder is trusted, what the project supplied can only
	// take away.
	check("untrusted", map[string]Behavior{
		"make all":         Ask,   // the project's allow rule waits
		"npm ci":           Ask,   // so does a local file the repository carries
		"cargo build":      Allow, // the user's own local file applies
		"go test ./...":    Allow, // so do the user's settings
		"curl x":           Deny,  // the project's deny rule applies
		"git push":         Ask,   // and its ask rule
		"cat /elsewhere/a": Ask,   // its additional directory waits
	})
	if e.Mode() != ModeDefault {
		t.Errorf("an untrusted project's defaultMode applied: %s", e.Mode())
	}
	if d := file(e, "Edit", "/proj/a.go"); d.Behavior != Ask {
		t.Errorf("an untrusted project's Edit allow applied: %+v", d)
	}
	x := e.Explain()
	held := 0
	for _, r := range x.Rules {
		if r.Held {
			held++
			if r.List != ListAllow || (r.Source != "project settings" && r.Source != "local settings tracked in git") {
				t.Errorf("held rule: %+v", r)
			}
		}
	}
	if held != 3 || x.Trusted || len(x.HeldDirs) != 1 || x.HeldDirs[0] != "/elsewhere" {
		t.Errorf("explain: %d held rules, trusted=%v, held dirs %q", held, x.Trusted, x.HeldDirs)
	}

	e.SetTrusted(true)
	check("trusted", map[string]Behavior{
		"make all":         Allow,
		"npm ci":           Allow,
		"curl x":           Deny,
		"git push":         Ask,
		"cat /elsewhere/a": Allow,
	})

	// Started trusted, the project's mode applies.
	o.Trusted = true
	if e := mustEngine(t, o); e.Mode() != ModeAcceptEdits {
		t.Errorf("a trusted project's defaultMode: %s", e.Mode())
	}
}

func TestStartMode(t *testing.T) {
	type src struct {
		scope Scope
		mode  string
	}
	tests := []struct {
		name    string
		flag    Mode
		layers  []src
		disable bool
		want    Mode
		wantErr bool
	}{
		{"nothing set", "", nil, false, ModeDefault, false},
		{"the flag wins", ModePlan, []src{{ScopeUser, "acceptEdits"}}, false, ModePlan, false},
		{"manual is default", "manual", nil, false, ModeDefault, false},
		{"auto falls back to default", "auto", nil, false, ModeDefault, false},
		{"user settings", "", []src{{ScopeUser, "acceptEdits"}}, false, ModeAcceptEdits, false},
		{"local beats project beats user", "", []src{{ScopeUser, "acceptEdits"}, {ScopeProject, "plan"}, {ScopeLocal, "dontAsk"}}, false, ModeDontAsk, false},
		{"--settings beats the files", "", []src{{ScopeLocal, "plan"}, {ScopeSettingsFlag, "acceptEdits"}}, false, ModeAcceptEdits, false},
		{"bypass from the user's settings", "", []src{{ScopeUser, "bypassPermissions"}}, false, ModeBypass, false},
		{"bypass from a project is ignored", "", []src{{ScopeProject, "bypassPermissions"}}, false, ModeDefault, false},
		{"bypass from local settings is ignored", "", []src{{ScopeUser, "plan"}, {ScopeLocal, "bypassPermissions"}}, false, ModePlan, false},
		{"auto in settings is default", "", []src{{ScopeUser, "auto"}}, false, ModeDefault, false},
		{"bypass switched off, flag", ModeBypass, nil, true, "", true},
		{"bypass switched off, settings", "", []src{{ScopeUser, "bypassPermissions"}}, true, ModeDefault, false},
		{"an unknown mode in the flag", "yolo", nil, false, "", true},
		{"an unknown mode in settings", "", []src{{ScopeUser, "AcceptEdits"}}, false, "", true},
	}
	for _, tt := range tests {
		o := Options{Mode: tt.flag, Cwd: "/proj", Home: "/home/u", Trusted: true, DisableBypass: tt.disable}
		for _, l := range tt.layers {
			o.Layers = append(o.Layers, Layer{Scope: l.scope, Name: string(l.scope), Base: "/x", DefaultMode: l.mode})
		}
		e, err := New(o)
		if (err != nil) != tt.wantErr {
			t.Errorf("%s: error %v", tt.name, err)
			continue
		}
		if err == nil && e.Mode() != tt.want {
			t.Errorf("%s: got %s, want %s", tt.name, e.Mode(), tt.want)
		}
	}
	e := mustEngine(t, Options{Mode: "auto", Cwd: "/proj"})
	if x := e.Explain(); len(x.Notes) != 1 || !strings.Contains(x.Notes[0], "auto") {
		t.Errorf("the fallback from auto should be noted once: %q", x.Notes)
	}

	e = mustEngine(t, Options{Cwd: "/proj", DisableBypass: true})
	if err := e.SetMode(ModeBypass); err == nil {
		t.Errorf("SetMode let bypass through although it is switched off")
	}
	if err := e.SetMode("auto"); err == nil {
		t.Errorf("SetMode took a mode gnosh does not have")
	}
	if err := e.SetMode(ModePlan); err != nil || e.Mode() != ModePlan {
		t.Errorf("SetMode(plan): %v, mode %s", err, e.Mode())
	}
	if _, err := New(Options{Cwd: "relative"}); err == nil {
		t.Errorf("a relative working directory should be refused")
	}
}

func TestToolEnabled(t *testing.T) {
	e := engine(t, ModeDefault, rules{deny: []string{"WebSearch", "Bash(rm *)", "Edit(.env)", "Agent(Explore)"}})
	for tool, want := range map[string]bool{
		"WebSearch": false, "Bash": true, "Edit": true, "Agent": true, "Read": true, "Task": true,
	} {
		if got := e.ToolEnabled(tool); got != want {
			t.Errorf("%s enabled = %v, want %v", tool, got, want)
		}
	}
	e = engine(t, ModeDefault, rules{deny: []string{"Bash"}})
	if e.ToolEnabled("Bash") || e.ToolEnabled("Monitor") {
		t.Errorf("a bare Bash deny removes Bash and the tool that uses its rules")
	}

	// --tools restricts the built-in tools and leaves MCP tools alone.
	o := testOptions(ModeDefault, rules{})
	o.RestrictTools, o.OnlyTools = true, []string{"Read", "Bash", "Task"}
	e = mustEngine(t, o)
	for tool, want := range map[string]bool{
		"Read": true, "Bash": true, "Agent": true, "Edit": false, "Write": false, "WebFetch": false, "mcp__a__b": true,
	} {
		if got := e.ToolEnabled(tool); got != want {
			t.Errorf("--tools: %s enabled = %v, want %v", tool, got, want)
		}
	}
	if d := file(e, "Edit", "/proj/a"); d.Behavior != Deny || d.Stage != StageTool {
		t.Errorf("a tool --tools left out is refused: %+v", d)
	}
	o.OnlyTools = nil // --tools ""
	e = mustEngine(t, o)
	if e.ToolEnabled("Read") || e.ToolEnabled("Bash") || !e.ToolEnabled("mcp__a__b") {
		t.Errorf("--tools \"\" should leave no built-in tool")
	}
}

func TestGrantAndExplain(t *testing.T) {
	o := testOptions(ModeDefault, rules{allow: []string{"Bash(git commit *)"}, deny: []string{"Bash(rm *)"}, ask: []string{"Bash(git push *)"}})
	o.Layers = append(o.Layers, Layer{Scope: ScopeCLI, Name: "--add-dir", Dirs: []string{"/extra"}})
	e := mustEngine(t, o)

	if d := bash(e, "make test"); d.Behavior != Ask {
		t.Fatalf("before the grant: %+v", d)
	}
	if err := e.Grant("Bash(make *)", GrantLocal); err != nil {
		t.Fatal(err)
	}
	if err := e.Grant("Bash(make *)", GrantLocal); err != nil { // twice is once
		t.Fatal(err)
	}
	if d := bash(e, "make test"); d.Behavior != Allow || d.Rule != "Bash(make *)" {
		t.Errorf("after the grant: %+v", d)
	}
	if err := e.Grant("Edit(//proj/a.go)", GrantSession); err != nil {
		t.Fatal(err)
	}
	if d := file(e, "Edit", "/proj/a.go"); d.Behavior != Allow {
		t.Errorf("after a session grant: %+v", d)
	}
	if d := file(e, "Edit", "/proj/b.go"); d.Behavior != Ask {
		t.Errorf("a grant for one file covered another: %+v", d)
	}
	// A grant never reaches past a deny rule.
	if err := e.Grant("Bash(rm *)", GrantSession); err != nil {
		t.Fatal(err)
	}
	if d := bash(e, "rm x"); d.Behavior != Deny {
		t.Errorf("a grant beat a deny rule: %+v", d)
	}
	if err := e.AddDir("/more"); err != nil {
		t.Fatal(err)
	}
	if err := e.AddDir("relative"); err == nil {
		t.Errorf("AddDir took a relative path")
	}
	if d := file(e, "Read", "/more/x"); d.Behavior != Allow {
		t.Errorf("after /add-dir: %+v", d)
	}

	x := e.Explain()
	var got []string
	for _, r := range x.Rules {
		got = append(got, string(r.List)+" "+r.Rule+" <"+r.Source+">")
	}
	want := []string{
		"deny Bash(rm *) <user settings>",
		"ask Bash(git push *) <user settings>",
		"allow Bash(git commit *) <user settings>",
		"allow Bash(make *) <approved at a prompt and kept>",
		"allow Edit(//proj/a.go) <approved for this session>",
		"allow Bash(rm *) <approved for this session>",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("explain rules:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if strings.Join(x.Dirs, " ") != "/proj /extra /more" || x.Mode != ModeDefault || !x.Trusted {
		t.Errorf("explain: dirs %q mode %s trusted %v", x.Dirs, x.Mode, x.Trusted)
	}
}

func TestSuggestions(t *testing.T) {
	e := engine(t, ModeDefault, rules{allow: []string{"Bash(git add *)"}})
	tests := []struct {
		cmd  string
		want []string
	}{
		{"npm test --watch", []string{"Bash(npm test --watch)", "Bash(npm test *)"}},
		{"npm test", []string{"Bash(npm test)"}},
		{"make", []string{"Bash(make)"}},
		{"(make)", []string{"Bash(make)"}}, // a subshell is the commands in it
		{"pytest -x tests", []string{"Bash(pytest -x tests)", "Bash(pytest *)"}},
		{"git push origin main", []string{"Bash(git push origin main)", "Bash(git push *)"}},
		{"git -C /x push", []string{"Bash(git -C /x push)"}}, // never a rule for all of git
		{"rm -rf build", []string{"Bash(rm -rf build)"}},     // never a prefix rule for rm
		{"curl -s https://example.com", []string{"Bash(curl -s https://example.com)"}},
		{"python3 -c 'print(1)'", []string{"Bash(python3 -c 'print(1)')"}},
		{"./run.sh --fast", []string{"Bash(./run.sh --fast)"}},
		{"echo 'a*b' >/dev/null && make x", []string{`Bash(make x)`}},
		{"mytool a*b", []string{`Bash(mytool a\*b)`, "Bash(mytool *)"}}, // a star in the command is escaped in the rule
		// One rule for each part that needed approval; parts that already
		// run are left out.
		{"git add . && git commit -m wip && ls", []string{"Bash(git commit -m wip)", "Bash(git commit *)"}},
		{"make a && make b", []string{"Bash(make a) + Bash(make b)"}},
		{"npm ci && npm test -- x", []string{"Bash(npm ci) + Bash(npm test -- x)", "Bash(npm ci) + Bash(npm test *)"}},
		// A cd out of the working directories yields a Read rule.
		{"cd /opt/data", []string{"Read(//opt/data/**)"}},
		// But not for the root, a top-level folder, the home or a folder
		// that holds the project: "cd .." from the project's root kept
		// read access to every project beside it (the approvals review of
		// 2026-11-19).
		{"cd .. && ls", nil},
		{"cd / && ls", nil},
		{"cd /opt && ls", nil},
		{"cd ~ && ls", nil},
		// A program written in capitals is the program a Mac runs: no
		// prefix rule for it where there would be none in lower case.
		{"PYTHON3 -c 'print(1)'", []string{"Bash(PYTHON3 -c 'print(1)')"}},
		{"GIT -c x=y log", []string{"Bash(GIT -c x=y log)"}},
		{"FIND . -name x", []string{"Bash(FIND . -name x)"}},
		// More than five parts, or a part no rule could cover: a one-time
		// yes only.
		{"a1; a2; a3; a4; a5; a6", nil},
		{"make > out.txt", nil},
		{"FOO=1 make", nil},
		{"make $(echo x)", nil},
		{"make &", nil},
		{"make &&", nil},
		{`"make" x`, nil},
	}
	for _, tt := range tests {
		d := bash(e, tt.cmd)
		if d.Behavior != Ask {
			t.Errorf("%q: got %s, want ask", tt.cmd, d.Behavior)
			continue
		}
		got := suggested(d)
		if strings.Join(got, " | ") != strings.Join(tt.want, " | ") {
			t.Errorf("%q: suggestions %q, want %q", tt.cmd, got, tt.want)
		}
		// Taking the narrowest suggestion makes the same command run, and
		// only widens what was asked about.
		if len(d.Suggestions) == 0 {
			continue
		}
		e2 := engine(t, ModeDefault, rules{allow: []string{"Bash(git add *)"}})
		for _, r := range d.Suggestions[0].Rules {
			if err := e2.Grant(r, d.Suggestions[0].Scope); err != nil {
				t.Errorf("%q: suggested rule %s cannot be granted: %v", tt.cmd, r, err)
			}
		}
		if d2 := bash(e2, tt.cmd); d2.Behavior != Allow {
			t.Errorf("%q: still %s after granting %q (%s)", tt.cmd, d2.Behavior, d.Suggestions[0].Rules, d2.Reason)
		}
		if d2 := bash(e2, tt.cmd+" && touch /tmp/pwned"); d2.Behavior != Ask {
			t.Errorf("%q: the granted rule let more through: %+v", tt.cmd, d2)
		}
	}

	// A * in a host would be a wildcard in the rule: no rule is offered.
	if d := fetch(e, "https://*.com/x"); len(d.Suggestions) != 0 {
		t.Errorf("a host with a *: %q", suggested(d))
	}
	if d := fetch(e, "https://docs.example.org/x"); strings.Join(suggested(d), " | ") != "WebFetch(domain:docs.example.org)" {
		t.Errorf("a plain host: %q", suggested(d))
	}

	// File tools.
	d := file(e, "Edit", "/proj/src/a*.go")
	if got := suggested(d); strings.Join(got, " | ") != `Edit(//proj/src/a\*.go) | mode:acceptEdits` || d.Suggestions[0].Scope != GrantSession {
		t.Errorf("edit inside: %q", got)
	}
	d = file(e, "Write", "/etc/x")
	if got := suggested(d); strings.Join(got, " | ") != "Edit(//etc/x)" {
		t.Errorf("edit outside: %q", got)
	}
	d = file(e, "Read", "/opt/data/a.txt")
	if got := suggested(d); strings.Join(got, " | ") != "Read(//opt/data/a.txt) | Read(//opt/data/**)" {
		t.Errorf("read outside: %q", got)
	}
	d = file(e, "Read", "/etc/passwd")
	if got := suggested(d); strings.Join(got, " | ") != "Read(//etc/passwd)" {
		t.Errorf("read of a file in a top-level directory: %q", got)
	}
	// Other tools.
	if got := suggested(other(e, "mcp__srv__do")); strings.Join(got, "|") != "mcp__srv__do" {
		t.Errorf("mcp: %q", got)
	}
	if got := suggested(other(e, "Skill", "skill", "deploy", "args", "prod")); strings.Join(got, "|") != "Skill(deploy *)" {
		t.Errorf("skill: %q", got)
	}
	// Approving a plan may switch to acceptEdits; it keeps no rule, so
	// each plan is asked about.
	if got := suggested(other(engine(t, ModePlan, rules{}), "ExitPlanMode")); strings.Join(got, "|") != "mode:acceptEdits" {
		t.Errorf("the plan is approved each time: %q", got)
	}
	// Nothing is suggested for what an ask rule or a safety check asks
	// about every time.
	e = engine(t, ModeDefault, rules{ask: []string{"Bash(make *)"}})
	for _, d := range []Decision{bash(e, "make x"), bash(e, "rm -rf /"), file(e, "Edit", "/proj/.git/config")} {
		if len(d.Suggestions) != 0 {
			t.Errorf("suggestions on %q: %q", d.Reason, suggested(d))
		}
	}
}

func TestHooks(t *testing.T) {
	type tc struct {
		name string
		mode Mode
		r    rules
		do   func(e *Engine) Decision
		// want is the behaviour with no hook, a hook's allow, a hook's
		// ask and a hook's deny.
		want [4]Behavior
	}
	mk := func(cmd string) func(e *Engine) Decision { return func(e *Engine) Decision { return bash(e, cmd) } }
	ed := func(p string) func(e *Engine) Decision { return func(e *Engine) Decision { return file(e, "Edit", p) } }
	for _, tt := range []tc{
		{"the mode would ask", ModeDefault, rules{}, mk("make"), [4]Behavior{Ask, Allow, Ask, Deny}},
		{"the mode lets it run", ModeDefault, rules{}, mk("ls"), [4]Behavior{Allow, Allow, Ask, Deny}},
		{"an allow rule lets it run", ModeDefault, rules{allow: []string{"Bash"}}, mk("make"), [4]Behavior{Allow, Allow, Ask, Deny}},
		{"a deny rule", ModeDefault, rules{deny: []string{"Bash(make*)"}}, mk("make"), [4]Behavior{Deny, Deny, Deny, Deny}},
		{"an ask rule", ModeDefault, rules{ask: []string{"Bash(make*)"}}, mk("make"), [4]Behavior{Ask, Ask, Ask, Deny}},
		{"a protected path", ModeAcceptEdits, rules{}, ed("/proj/.git/config"), [4]Behavior{Ask, Ask, Ask, Deny}},
		{"a dangerous removal", ModeBypass, rules{}, mk("rm -rf /"), [4]Behavior{Ask, Ask, Ask, Deny}},
		{"bypass", ModeBypass, rules{}, mk("make"), [4]Behavior{Allow, Allow, Ask, Deny}},
		{"acceptEdits", ModeAcceptEdits, rules{}, ed("/proj/a.go"), [4]Behavior{Allow, Allow, Ask, Deny}},
		{"dontAsk, would have asked", ModeDontAsk, rules{}, mk("make"), [4]Behavior{Deny, Allow, Deny, Deny}},
		{"dontAsk, an ask rule", ModeDontAsk, rules{ask: []string{"Bash(make*)"}}, mk("make"), [4]Behavior{Deny, Deny, Deny, Deny}},
		{"dontAsk, lets it run", ModeDontAsk, rules{}, mk("ls"), [4]Behavior{Allow, Allow, Deny, Deny}},
		{"plan, an edit", ModePlan, rules{}, ed("/proj/a.go"), [4]Behavior{Deny, Deny, Deny, Deny}},
		{"plan, a command that changes things", ModePlan, rules{}, mk("make"), [4]Behavior{Ask, Ask, Ask, Deny}},
		// (A hook's allow does not approve a plan: the person does. The
		// plan review of 2026-11-19.)
		{"plan, presenting the plan", ModePlan, rules{}, func(e *Engine) Decision { return other(e, "ExitPlanMode") }, [4]Behavior{Ask, Ask, Ask, Deny}},
		{"plan, a web fetch", ModePlan, rules{}, func(e *Engine) Decision { return fetch(e, "https://example.com") }, [4]Behavior{Ask, Allow, Ask, Deny}},
	} {
		d := tt.do(engine(t, tt.mode, tt.r))
		got := [4]Behavior{
			d.WithHook(HookNone, "").Behavior,
			d.WithHook(HookAllow, "").Behavior,
			d.WithHook(HookAsk, "the hook wants a look").Behavior,
			d.WithHook(HookDeny, "the hook says no").Behavior,
		}
		if got != tt.want {
			t.Errorf("%s: got %v, want %v", tt.name, got, tt.want)
		}
	}
	d := bash(engine(t, ModeDefault, rules{}), "make")
	if h := d.WithHook(HookAsk, "check the target"); h.Reason != "check the target" || h.Stage != StageHook || len(h.Suggestions) != 0 {
		t.Errorf("a hook's ask carries its reason and no way to stop being asked: %+v", h)
	}
	if h := d.WithHook(HookDeny, ""); h.Reason == "" {
		t.Errorf("a hook's block with no words still gets a reason")
	}
}

func TestOtherToolsAndPaths(t *testing.T) {
	// A tool this package has no table entry for is held to the Read and
	// Edit deny rules by what it says it touches, and is never let run
	// because of where that is: --add-dir approves file reads by the
	// file tools, and nothing else.
	o := testOptions(ModeAcceptEdits, rules{deny: []string{"Read(.env)", "Edit(//proj/gen/**)"}})
	o.Layers = append(o.Layers, Layer{Scope: ScopeCLI, Name: "--add-dir", Dirs: []string{"/extra"}})
	e := mustEngine(t, o)
	act := func(kind core.ActionKind, p string) Decision {
		return e.Decide("mcp__fs__op", coreCall("mcp__fs__op", "path", p), core.Action{Kind: kind, Paths: []string{p}})
	}
	if d := act(core.ActionRead, "/proj/.env"); d.Behavior != Deny {
		t.Errorf("a Read deny reaches another tool's read: %+v", d)
	}
	if d := act(core.ActionEdit, "/proj/gen/a"); d.Behavior != Deny {
		t.Errorf("an Edit deny reaches another tool's edit: %+v", d)
	}
	if d := act(core.ActionRead, "/extra/a"); d.Behavior != Ask {
		t.Errorf("an added directory approved another tool's read: %+v", d)
	}
	if d := act(core.ActionEdit, "/proj/a"); d.Behavior != Ask {
		t.Errorf("acceptEdits approved another tool's edit: %+v", d)
	}
	if d := act(core.ActionEdit, "/proj/.git/config"); d.Behavior != Ask || d.Stage != StageSafety {
		t.Errorf("another tool's change to a protected path: %+v", d)
	}

	// A file tool with nothing to judge it by asks.
	if d := e.Decide("Read", coreCall("Read"), core.Action{Kind: core.ActionRead}); d.Behavior != Ask {
		t.Errorf("Read with no path: %+v", d)
	}
	if d := e.Decide("Read", coreCall("Read", "file_path", "relative/x"), core.Action{Kind: core.ActionRead}); d.Behavior != Ask {
		t.Errorf("Read with a relative path: %+v", d)
	}
	if d := e.Decide("Grep", coreCall("Grep", "pattern", "x"), core.Action{Kind: core.ActionRead}); d.Behavior != Allow {
		t.Errorf("Grep with no path searches the working directory: %+v", d)
	}
	if d := e.Decide("Read", coreCall("Read", "file_path", "/proj/a.go"), core.Action{}); d.Behavior != Allow {
		t.Errorf("the path is taken from the input when the tool describes none: %+v", d)
	}
	// The command the tool reports must be the command that was sent.
	if d := e.Decide("Bash", coreCall("Bash", "command", "rm -rf x"), coreAction("ls")); d.Behavior != Ask {
		t.Errorf("mismatched commands: %+v", d)
	}
	if d := e.Decide("Bash", coreCall("Bash", "command", "ls"), core.Action{}); d.Behavior != Allow {
		t.Errorf("the command is taken from the input when the tool describes none: %+v", d)
	}
}

func TestGitSettingsThatRunPrograms(t *testing.T) {
	o := testOptions(ModeDefault, rules{allow: []string{"Bash(git *)"}})
	o.Trusted, o.GitRunsPrograms = false, true
	e := mustEngine(t, o)
	for _, cmd := range []string{"git status", "git log", "ls && git diff", "timeout 5 git status", "/usr/bin/git status"} {
		if d := bash(e, cmd); d.Behavior != Ask || d.Stage != StageSafety || !strings.Contains(d.Reason, "git settings") {
			t.Errorf("%q: %+v", cmd, d)
		}
	}
	if d := bash(e, "ls"); d.Behavior != Allow {
		t.Errorf("other commands are not held: %+v", d)
	}
}

func TestConcurrentUse(t *testing.T) {
	e := engine(t, ModeDefault, rules{allow: []string{"Bash(make *)"}, deny: []string{"Bash(rm *)"}})
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				bash(e, "make x && ls | head")
				file(e, "Read", "/proj/a")
				e.ToolEnabled("Bash")
				e.Explain()
				switch (i + j) % 4 {
				case 0:
					_ = e.SetMode(ModeAcceptEdits)
				case 1:
					_ = e.Grant("Bash(npm test *)", GrantSession)
				case 2:
					e.SetCwd("/proj/src")
				case 3:
					_ = e.AddDir("/extra")
				}
			}
		}(i)
	}
	wg.Wait()
}

// TestDecideAt: the shell's directory goes with the question. Agents that
// share an engine and stand in different directories are each judged
// where they stand, whatever another asked last, and the engine's own
// directory stays where it was.
func TestDecideAt(t *testing.T) {
	e := mustEngine(t, testOptions(ModeBypass, rules{deny: []string{"Read(./.env)"}}))
	at := func(dir, cmd string) Behavior {
		return e.DecideAt(dir, "Bash", core.ToolCall{Name: "Bash", Input: input("command", cmd)},
			core.Action{Kind: core.ActionExecute, Command: cmd}).Behavior
	}
	// In the project "cat .env" reads the file the rule names. In a folder
	// under it the same words read another file.
	const cmd = "cat .env"
	if got := at("/proj", cmd); got != Deny {
		t.Fatalf("in the project: %s, want deny", got)
	}
	if got := at("/proj/build/", cmd); got != Allow {
		t.Fatalf("in a folder of the project: %s, want allow", got)
	}
	// The engine itself is where it was, and a directory that is no
	// absolute path changes nothing.
	for _, dir := range []string{"", "build", "./build"} {
		if got := at(dir, cmd); got != Deny {
			t.Errorf("with the directory %q: %s, want the engine's own directory, where it is denied", dir, got)
		}
	}
	if d := bash(e, cmd); d.Behavior != Deny {
		t.Errorf("Decide after a DecideAt elsewhere: %s (%s): the engine's own directory moved", d.Behavior, d.Reason)
	}
	// Told where the shell is, the engine is asked from there, and a
	// question that brings its own directory is still judged in that one.
	e.SetCwd("/proj/build")
	if d := bash(e, cmd); d.Behavior != Allow {
		t.Errorf("Decide after SetCwd: %s (%s), want allow", d.Behavior, d.Reason)
	}
	if got := at("/proj", cmd); got != Deny {
		t.Errorf("DecideAt the project after SetCwd elsewhere: %s, want deny", got)
	}
	// Many agents at once, half of them in each directory.
	var wg sync.WaitGroup
	for i := range 32 {
		dir, want := "/proj", Deny
		if i%2 == 1 {
			dir, want = "/proj/build", Allow
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 100 {
				if got := at(dir, cmd); got != want {
					t.Errorf("in %s among others: %s, want %s", dir, got, want)
					return
				}
			}
		}()
	}
	wg.Wait()
}

// The approvals kept for the project are what its local file holds: one the
// file no longer holds, taken out by hand, is no longer in force once the
// file is read again (the approvals review of 2026-11-19).
func TestKeepOnlyDropsWhatTheFileNoLongerHolds(t *testing.T) {
	e := engine(t, ModeDefault, rules{})
	for _, r := range []string{"Bash(make deploy)", "Bash(make test)"} {
		if err := e.Grant(r, GrantLocal); err != nil {
			t.Fatal(err)
		}
	}
	if d := bash(e, "make deploy"); d.Behavior != Allow {
		t.Fatalf("a kept approval: %s", d.Behavior)
	}
	e.KeepOnly(map[List][]string{ListAllow: {"Bash(make test)"}})
	if d := bash(e, "make deploy"); d.Behavior != Ask {
		t.Errorf("an approval the file no longer holds is still in force: %s", d.Behavior)
	}
	if d := bash(e, "make test"); d.Behavior != Allow {
		t.Errorf("an approval the file still holds was dropped: %s", d.Behavior)
	}
}

// A layer kept to one sub-agent (a forked skill's grant) is in force for
// that sub-agent's calls alone; one kept to the session's own agent is in
// force for its sub-agents too, which work for it; and RemoveAgentLayers
// ends one agent's without touching another's.
func TestLayerKeptToOneAgent(t *testing.T) {
	e := engine(t, ModeDefault, rules{})
	for _, ly := range []Layer{
		{Scope: ScopeSkill, Name: "the sub-agent's skill", Agent: "sub-1", Allow: []string{"Bash(touch sub.txt)"}},
		{Scope: ScopeSkill, Name: "the session's skill", Agent: MainAgent, Allow: []string{"Bash(touch main.txt)"}},
		{Scope: ScopeSkill, Name: "everyone's", Allow: []string{"Bash(touch all.txt)"}},
	} {
		if err := e.AddLayer(ly); err != nil {
			t.Fatal(err)
		}
	}
	decide := func(agent, cmd string) Behavior {
		return e.DecideFor(agent, "/proj", "Bash", core.ToolCall{Name: "Bash", Input: input("command", cmd)},
			core.Action{Kind: core.ActionExecute, Command: cmd}).Behavior
	}
	want := func(agent, cmd string, b Behavior) {
		t.Helper()
		if got := decide(agent, cmd); got != b {
			t.Errorf("%q for agent %q: %s, want %s", cmd, agent, got, b)
		}
	}
	want("sub-1", "touch sub.txt", Allow)
	want("sub-2", "touch sub.txt", Ask)
	want("", "touch sub.txt", Ask)
	want("", "touch main.txt", Allow)
	want("sub-1", "touch main.txt", Allow)
	want("sub-1", "touch all.txt", Allow)
	want("", "touch all.txt", Allow)
	if d := bash(e, "touch sub.txt"); d.Behavior != Ask {
		t.Errorf("Decide, the session's own agent, under a sub-agent's grant: %s", d.Behavior)
	}
	if d := e.DecideAt("/proj", "Bash", core.ToolCall{Name: "Bash", Input: input("command", "touch main.txt")},
		core.Action{Kind: core.ActionExecute, Command: "touch main.txt"}); d.Behavior != Allow {
		t.Errorf("DecideAt is the session's own agent's: %s", d.Behavior)
	}

	// The person's next prompt ends the session's own agent's grants; the
	// sub-agent's hold until it ends.
	e.RemoveAgentLayers("", ScopeSkill)
	want("", "touch main.txt", Ask)
	want("sub-1", "touch main.txt", Ask)
	want("sub-1", "touch sub.txt", Allow)
	want("", "touch all.txt", Allow)
	e.RemoveAgentLayers("sub-1", ScopeSkill)
	want("sub-1", "touch sub.txt", Ask)
	want("sub-1", "touch all.txt", Allow)
}
