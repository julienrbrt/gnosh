package hooks

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestParseConfigReadsClaudeCodesBlock(t *testing.T) {
	block := `{
	  "PreToolUse": [
	    { "matcher": "Bash",
	      "hooks": [ { "type": "command", "command": "./hooks/no-force-push.sh", "timeout": 10 } ] },
	    { "matcher": "Edit|Write",
	      "hooks": [
	        { "type": "command", "command": "\"${CLAUDE_PROJECT_DIR}/.claude/hooks/check.sh\"" },
	        { "type": "command", "command": "gofmt -l .", "timeout": 1.5 }
	      ] }
	  ],
	  "Stop": [ { "hooks": [ { "type": "command", "command": "./hooks/tests-must-pass.sh", "timeout": 1800 } ] } ]
	}`
	cfg, err := ParseConfig(json.RawMessage(block), "gnosh.json")
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	want := []Hook{
		{Event: PreToolUse, Matcher: "Bash", Command: "./hooks/no-force-push.sh", Timeout: 10 * time.Second, Source: "gnosh.json: PreToolUse group 1, hook 1"},
		{Event: PreToolUse, Matcher: "Edit|Write", Command: `"${CLAUDE_PROJECT_DIR}/.claude/hooks/check.sh"`, Source: "gnosh.json: PreToolUse group 2, hook 1"},
		{Event: PreToolUse, Matcher: "Edit|Write", Command: "gofmt -l .", Timeout: 1500 * time.Millisecond, Source: "gnosh.json: PreToolUse group 2, hook 2"},
		{Event: Stop, Command: "./hooks/tests-must-pass.sh", Timeout: 1800 * time.Second, Source: "gnosh.json: Stop group 1, hook 1"},
	}
	if !reflect.DeepEqual(cfg.Hooks, want) {
		t.Errorf("hooks:\n got %+v\nwant %+v", cfg.Hooks, want)
	}
	if problems := Check(cfg, Options{Fires: Events()}); len(problems) != 0 {
		t.Errorf("Check found problems in a good block:\n%s", problemsText(problems))
	}
}

// With no file named, a problem still says where in the block it is.
func TestParseConfigWithNoSourceNamed(t *testing.T) {
	cfg, err := ParseConfig(json.RawMessage(`{"Stop": [{"hooks": [{"type": "command", "command": "true"}, {"type": "script"}]}], "stopp": []}`), "")
	if err == nil || len(cfg.Hooks) != 1 || cfg.Hooks[0].Source != "Stop group 1, hook 1" {
		t.Fatalf("ParseConfig = %+v, %v", cfg.Hooks, err)
	}
	contains(t, "the error", err.Error(), `Stop group 1, hook 2: the hook has "type": "script"`, "\n\"stopp\" is not a hook event")
}

func TestParseConfigTakesAnAbsentBlock(t *testing.T) {
	for _, raw := range []string{"", "null", "  null\n", "{}"} {
		cfg, err := ParseConfig(json.RawMessage(raw), "gnosh.json")
		if err != nil || len(cfg.Hooks) != 0 {
			t.Errorf("ParseConfig(%q) = %v hooks, error %v; want none and nil", raw, len(cfg.Hooks), err)
		}
	}
}

// What Claude Code accepts and gnosh has no use for is accepted when it
// changes nothing.
func TestParseConfigAcceptsWhatChangesNothing(t *testing.T) {
	block := `{
	  "Stop": [ { "matcher": "", "hooks": [ {
	    "type": "command", "command": "true",
	    "statusMessage": "Checking", "once": true, "shell": "bash",
	    "async": false, "asyncRewake": false, "onFailure": "continue", "timeout": null
	  }, { "type": "command", "command": "true", "onFailure": "block" } ] } ],
	  "UserPromptSubmit": [ { "matcher": "*", "hooks": [ { "type": "command", "command": "true" } ] } ],
	  "SubagentStop": [ { "matcher": null, "hooks": [ { "type": "command", "command": "true" } ] } ],
	  "PostToolUse": [ { "hooks": [] } ]
	}`
	cfg, err := ParseConfig(json.RawMessage(block), "gnosh.json")
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	if len(cfg.Hooks) != 4 {
		t.Errorf("got %d hooks, want 4: %+v", len(cfg.Hooks), cfg.Hooks)
	}
}

// One test per thing ParseConfig refuses. Each refusal names what is
// wrong and where.
func TestParseConfigRefuses(t *testing.T) {
	hook := func(fields string) string {
		return `{"PreToolUse": [{"matcher": "Bash", "hooks": [{` + fields + `}]}]}`
	}
	tests := []struct {
		name  string
		block string
		want  []string // each must be in the one problem
	}{
		{"a string for a block", `"x"`, []string{"must be a JSON object", "a string"}},
		{"a bare list for a block", `[{"hooks": []}]`, []string{"must be a JSON object", "a list"}},
		{"a number for a block", `42`, []string{"must be a JSON object", "a number"}},
		{"a block that is not JSON", `{"Stop": [`, []string{"cannot be read", "ends before it is complete"}},
		{"text after the block", `{} {}`, []string{"cannot be read", "more text after the object"}},

		{"an event nobody has", `{"Stopp": []}`, []string{`"Stopp" is not a hook event`, "PreToolUse", "Notification"}},
		{"an event in lower case", `{"pretooluse": []}`, []string{`"pretooluse" is not a hook event`, "written exactly", "this one is PreToolUse.", "SubagentStop", "Notification"}},
		{"an event with underscores", `{"pre_tool_use": []}`, []string{`"pre_tool_use" is not a hook event`, "this one is PreToolUse.", "Notification"}},
		{"an event in capitals", `{"PRE_TOOL_USE": []}`, []string{`"PRE_TOOL_USE" is not a hook event`, "this one is PreToolUse.", "Notification"}},
		{"an empty event name", `{"": []}`, []string{"names no event"}},
		{"a Claude Code event that can block", `{"WorktreeCreate": []}`, []string{`"WorktreeCreate" is a Claude Code hook event that gnosh does not have`, "would never run", "can block"}},
		{"a Claude Code event that only watches", `{"MessageDisplay": []}`, []string{`"MessageDisplay" is a Claude Code hook event that gnosh does not have`, "would never run"}},
		{"an event written twice", `{"Stop": [], "Stop": []}`, []string{`"Stop" is written twice`, "silently dropped"}},

		{"an event that is not a list", `{"Stop": {"hooks": []}}`, []string{"must be a list of matcher groups", "an object"}},
		{"a group that is not an object", `{"Stop": ["tests.sh"]}`, []string{"a matcher group must be a JSON object", "a string"}},
		{"a group with a field gnosh does not know", `{"Stop": [{"hooks": [], "enabled": true}]}`, []string{`"enabled"`, "does not know"}},
		{"a matcher that is a list", `{"PreToolUse": [{"matcher": ["Edit", "Write"], "hooks": []}]}`, []string{`"matcher" must be one string`, "a list"}},
		{"hooks that are not a list", `{"Stop": [{"hooks": {"type": "command"}}]}`, []string{`"hooks" must be a list`, "an object"}},
		{"a group with no hooks", `{"PreToolUse": [{"matcher": "Bash"}]}`, []string{`no "hooks" list`, "configures nothing"}},
		{"a matcher written twice", `{"PreToolUse": [{"matcher": "Bash", "matcher": "Edit", "hooks": []}]}`, []string{`"matcher" is written twice`}},
		{"a hook where a group belongs", `{"Stop": [{"type": "command", "command": "./tests.sh", "timeout": 1800}]}`, []string{"written where a matcher group belongs", `{"hooks": [{"type": "command", "command": "..."}]}`}},
		{"a bare command where a group belongs", `{"Stop": [{"command": "./tests.sh"}]}`, []string{"written where a matcher group belongs"}},
		{"a hook that is not an object", `{"Stop": [{"hooks": ["./tests.sh"]}]}`, []string{"a hook must be a JSON object", "a string"}},

		{"no type", hook(`"command": "true"`), []string{`no "type"`, `"type": "command"`}},
		{"a type that is not a string", hook(`"type": 1, "command": "true"`), []string{`"type" must be a string`}},
		{"an http hook with no url", hook(`"type": "http"`), []string{`the http hook has no "url"`}},
		{"an http hook with a command", hook(`"type": "http", "url": "https://example.com/hook", "command": "x"`), []string{`the hook has "command", which a hook of "type": "http" does not take`}},
		{"an http hook in the background", hook(`"type": "http", "url": "https://example.com/hook", "async": true`), []string{`"async", which a hook of "type": "http" does not take`}},
		{"a command hook with a url", hook(`"type": "command", "command": "x", "url": "https://example.com/hook"`), []string{`"url", which a hook of "type": "command" does not take`}},
		{"an http hook whose url is not http", hook(`"type": "http", "url": "ftp://example.com/hook"`), []string{"the hook's url cannot be used: it must start with http:// or https://"}},
		{"an mcp_tool hook with no tool", hook(`"type": "mcp_tool", "server": "s"`), []string{`an mcp_tool hook names both the "server" and the "tool" it calls`}},
		{"an mcp_tool hook with neither", hook(`"type": "mcp_tool"`), []string{`the mcp_tool hook names no "server" and no "tool"`}},
		{"an mcp_tool hook whose input is not an object", hook(`"type": "mcp_tool", "server": "s", "tool": "t", "input": [1]`), []string{`"input" is a JSON object`}},
		{"a command hook with a server", hook(`"type": "command", "command": "x", "server": "s"`), []string{`"server", which a hook of "type": "command" does not take`}},
		{"a hook of another type", hook(`"type": "script", "prompt": "is it done?"`), []string{`"type": "script"`, "command hooks, http hooks, mcp_tool hooks, prompt hooks and agent hooks only"}},

		{"an if filter that is not a string", hook(`"type": "command", "command": "true", "if": ["Bash(rm *)"]`), []string{`"if" is one permission rule`, "this one is a list"}},
		{"an empty if filter", hook(`"type": "command", "command": "true", "if": " "`), []string{`"if" is empty`, `leave "if" out`}},
		{"args that are not a list", hook(`"type": "command", "command": "check", "args": "--strict"`), []string{`"args" is a list of strings`, "this one is a string"}},
		{"args that are not strings", hook(`"type": "command", "command": "check", "args": ["-n", 3]`), []string{`"args" is a list of strings`, "this one is a list"}},
		{"an argument with a NUL in it", hook(`"type": "command", "command": "check", "args": ["a\u0000b"]`), []string{"an argument of the hook holds a NUL character"}},
		{"async that is not true or false", hook(`"type": "command", "command": "true", "async": "no"`), []string{`"async": "no"`}},
		{"another shell", hook(`"type": "command", "command": "true", "shell": "powershell"`), []string{`"shell": "powershell"`, "POSIX shell"}},
		{"an onFailure gnosh does not know", hook(`"type": "command", "command": "true", "onFailure": "retry"`), []string{`"onFailure": "retry"`, "does not know"}},
		{"a field gnosh does not know", hook(`"type": "command", "command": "true", "retries": 3`), []string{`"retries"`, "does not know"}},
		{"a field written twice", hook(`"type": "command", "command": "true", "command": "false"`), []string{`"command" is written twice`}},
		{"a status message that is not a string", hook(`"type": "command", "command": "true", "statusMessage": 1`), []string{`"statusMessage" must be a string`}},
		{"once that is not true or false", hook(`"type": "command", "command": "true", "once": "yes"`), []string{`"once" must be true or false`}},

		{"no command", hook(`"type": "command"`), []string{"no command to run"}},
		{"an empty command", hook(`"type": "command", "command": "  "`), []string{"no command to run"}},
		{"a command that is not a string", hook(`"type": "command", "command": ["sh", "-c", "true"]`), []string{`"command" must be a string`, "a list"}},
		{"a timeout of zero", hook(`"type": "command", "command": "true", "timeout": 0`), []string{`the timeout "0" cannot be used`, "greater than zero"}},
		{"a timeout below zero", hook(`"type": "command", "command": "true", "timeout": -5`), []string{`the timeout "-5" cannot be used`}},
		{"a timeout in words", hook(`"type": "command", "command": "true", "timeout": "10"`), []string{`the timeout "10" cannot be used`, "number of seconds", "without quotes"}},
		{"a timeout beyond any time", hook(`"type": "command", "command": "true", "timeout": 1e300`), []string{"cannot be used"}},

		{"a matcher on Stop", `{"Stop": [{"matcher": "Bash", "hooks": [{"type": "command", "command": "true"}]}]}`, []string{"a Stop hook takes no matcher", `"Bash"`, "Remove the matcher"}},
		{"a matcher on UserPromptSubmit", `{"UserPromptSubmit": [{"matcher": "deploy", "hooks": [{"type": "command", "command": "true"}]}]}`, []string{"a UserPromptSubmit hook takes no matcher"}},
		{"a matcher on SubagentStop", `{"SubagentStop": [{"matcher": "Explore", "hooks": [{"type": "command", "command": "true"}]}]}`, []string{"a SubagentStop hook takes no matcher"}},
		{"a matcher with look-ahead", `{"PreToolUse": [{"matcher": "^(?!Read)", "hooks": [{"type": "command", "command": "true"}]}]}`, []string{`"^(?!Read)"`, "cannot be read as a regular expression", "look-ahead"}},
		{"a matcher with a backreference", `{"PreToolUse": [{"matcher": "(a)\\1", "hooks": [{"type": "command", "command": "true"}]}]}`, []string{"cannot be read as a regular expression"}},
		{"a matcher that names nothing", `{"PreToolUse": [{"matcher": " | ", "hooks": [{"type": "command", "command": "true"}]}]}`, []string{"names nothing", `"*"`}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := ParseConfig(json.RawMessage(tt.block), "/home/user/.config/gnosh/gnosh.json")
			var ce *ConfigError
			if !errors.As(err, &ce) {
				t.Fatalf("ParseConfig returned %v, want a *ConfigError", err)
			}
			if len(ce.Problems) != 1 {
				t.Fatalf("got %d problems, want 1:\n%s", len(ce.Problems), err)
			}
			p := ce.Problems[0]
			if p.Warning {
				t.Errorf("the problem is only a warning: %s", p)
			}
			contains(t, "the problem", p.Message, tt.want...)
			if !strings.HasPrefix(p.Source, "/home/user/.config/gnosh/gnosh.json") {
				t.Errorf("the problem does not say which file: Source = %q", p.Source)
			}
			if len(cfg.Hooks) != 0 {
				t.Errorf("a refused hook was kept: %+v", cfg.Hooks)
			}
			// The same problem stops startup by both roads.
			if got := Check(cfg, Options{Fires: Events()}); !reflect.DeepEqual(got, ce.Problems) {
				t.Errorf("Check does not repeat the problem:\n%s", problemsText(got))
			}
			if _, err := NewRunner(cfg, Options{Fires: Events(), ProjectDir: "/project"}); err == nil {
				t.Errorf("NewRunner made a Runner from a configuration with a problem")
			}
		})
	}
}

// Every problem of a block is reported in one go, each with its place,
// and the hooks that are fine are still there to be checked too.
func TestParseConfigReportsEverythingAtOnce(t *testing.T) {
	block := `{
	  "PreToolUse": [
	    { "matcher": "Bash", "hooks": [
	        { "type": "command", "command": "./ok.sh" },
	        { "type": "script", "prompt": "is it safe?" },
	        { "type": "command", "command": "./filtered.sh", "if": 7, "timeout": -1 } ] } ],
	  "stop": [ { "hooks": [ { "type": "command", "command": "./tests.sh" } ] } ],
	  "PermissionRequest": [ { "hooks": [ { "type": "command", "command": "./answer.sh" } ] } ]
	}`
	cfg, err := ParseConfig(json.RawMessage(block), "gnosh.json")
	if err == nil {
		t.Fatal("ParseConfig accepted the block")
	}
	text := err.Error()
	contains(t, "the error", text,
		`gnosh.json: PreToolUse group 1, hook 2: the hook has "type": "script"`,
		`gnosh.json: PreToolUse group 1, hook 3: "if" is one permission rule`,
		`gnosh.json: PreToolUse group 1, hook 3: the timeout "-1" cannot be used`,
		`gnosh.json: "stop" is not a hook event`,
	)
	if n := len(err.(*ConfigError).Problems); n != 4 {
		t.Errorf("got %d problems, want 4:\n%s", n, text)
	}
	if len(cfg.Hooks) != 2 {
		t.Fatalf("got %d readable hooks, want 2: %+v", len(cfg.Hooks), cfg.Hooks)
	}
	// Check adds what only it can know: this build fires no
	// PermissionRequest hooks.
	fires := []Event{PreToolUse, Stop}
	all := problemsText(Check(cfg, Options{Fires: fires}))
	contains(t, "Check", all, `"type": "script"`, `"if" is one permission rule`, `"stop" is not a hook event`, "does not run PermissionRequest hooks yet")
}

func TestNewHook(t *testing.T) {
	h, err := NewHook(Spec{Event: "Stop", Command: "./hooks/tests-must-pass.sh", Name: "tests", Timeout: 1800 * time.Second, Source: "gnosh.json:7", Global: true})
	if err != nil {
		t.Fatalf("NewHook: %v", err)
	}
	want := Hook{Event: Stop, Command: "./hooks/tests-must-pass.sh", Name: "tests", Timeout: 1800 * time.Second, Source: "gnosh.json:7", Global: true}
	if !reflect.DeepEqual(h, want) {
		t.Errorf("got %+v\nwant %+v", h, want)
	}
	if h, err := NewHook(Spec{Event: "PreToolUse", Matcher: "Bash", Command: "./no-force-push.sh", Timeout: 1500 * time.Millisecond}); err != nil || h.Timeout != 1500*time.Millisecond || h.Matcher != "Bash" {
		t.Errorf("NewHook = %+v, %v", h, err)
	}
}

// A settings line is refused for the same reasons as a JSON hook, so a
// command that writes one can check it first.
func TestNewHookRefuses(t *testing.T) {
	tests := []struct {
		name string
		spec Spec
		want []string
	}{
		{"an event nobody has", Spec{Event: "Stopp", Command: "true"}, []string{`"Stopp" is not a hook event`}},
		{"an event with underscores", Spec{Event: "pre_tool_use", Command: "true"}, []string{`"pre_tool_use" is not a hook event`, "PreToolUse"}},
		{"a Claude Code event gnosh does not have", Spec{Event: "WorktreeCreate", Command: "true"}, []string{"Claude Code hook event that gnosh does not have", "can block"}},
		{"no event", Spec{Command: "true"}, []string{"names no event"}},
		{"no command", Spec{Event: "Stop"}, []string{"no command to run"}},
		{"a matcher on Stop", Spec{Event: "Stop", Matcher: "Bash", Command: "true"}, []string{"a Stop hook takes no matcher"}},
		{"a matcher that cannot be read", Spec{Event: "PreToolUse", Matcher: "Bash(", Command: "true"}, []string{"cannot be read as a regular expression"}},
		{"a timeout below zero", Spec{Event: "Stop", Command: "true", Timeout: -3 * time.Second}, []string{"timeout is less than zero"}},
		{"a command with a NUL in it", Spec{Event: "Stop", Command: "true\x00"}, []string{"NUL"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.spec.Source = "gnosh.json:3"
			h, err := NewHook(tt.spec)
			var ce *ConfigError
			if !errors.As(err, &ce) {
				t.Fatalf("NewHook returned %+v, %v; want a *ConfigError", h, err)
			}
			contains(t, "the error", err.Error(), append(tt.want, "gnosh.json:3: ")...)
			if !reflect.DeepEqual(h, Hook{}) {
				t.Errorf("a refused line still gave a hook: %+v", h)
			}
		})
	}
	// Two things wrong with one line are both said.
	_, err := NewHook(Spec{Event: "stop", Timeout: -1})
	contains(t, "the error", err.Error(), "is not a hook event", "less than zero", "no command to run")
}

// A hook built by hand goes through the same check as one that was
// parsed.
func TestCheckFindsProblemsInHooksBuiltByHand(t *testing.T) {
	cfg := Config{Hooks: []Hook{
		{Event: "PreTool", Command: "true", Source: "a"},
		{Event: Stop, Matcher: "Bash", Command: "true", Source: "b"},
		{Event: PreToolUse, Matcher: "[", Command: "true", Source: "c"},
		{Event: PreToolUse, Source: "d"},
		{Event: PreToolUse, Command: "true", Timeout: -time.Second, Source: "e"},
		{Event: PreToolUse, Command: "true", Source: "fine"},
	}}
	problems := Check(cfg, Options{Fires: Events()})
	if len(problems) != 5 {
		t.Fatalf("got %d problems, want 5:\n%s", len(problems), problemsText(problems))
	}
	contains(t, "the problems", problemsText(problems),
		`a: "PreTool" is not a hook event`,
		"b: a Stop hook takes no matcher",
		"c: the matcher",
		"d: the hook has no command to run",
		"e: the hook's timeout is less than zero",
	)
	if _, err := NewRunner(cfg, Options{Fires: Events(), ProjectDir: "/project"}); err == nil {
		t.Error("NewRunner made a Runner from hooks with problems")
	}
}

// A hook on an event the program does not fire would never run, so it is
// refused: every one of them, and a gate with the reason spelt out.
func TestCheckRefusesEventsThatAreNotFired(t *testing.T) {
	for _, event := range Events() {
		t.Run(string(event), func(t *testing.T) {
			cfg := Config{Hooks: []Hook{{Event: event, Command: "./check.sh", Source: "gnosh.json"}}}
			var others []Event
			for _, e := range Events() {
				if e != event {
					others = append(others, e)
				}
			}
			for _, fires := range [][]Event{nil, others} {
				problems := Check(cfg, Options{Fires: fires})
				if len(problems) != 1 || problems[0].Warning {
					t.Fatalf("with %d events fired: got %s, want one problem that stops startup", len(fires), problemsText(problems))
				}
				contains(t, "the problem", problems[0].String(), "gnosh.json: ", "does not run "+string(event)+" hooks yet", "./check.sh", "would never run")
				canDecide := event.CanBlock() || event == PermissionRequest
				if got := strings.Contains(problems[0].Message, "can stop or decide things"); got != canDecide {
					t.Errorf("says the event can stop or decide things: %v, want %v", got, canDecide)
				}
				if _, err := NewRunner(cfg, Options{Fires: fires, ProjectDir: "/project"}); err == nil {
					t.Error("NewRunner made a Runner with a hook that would never run")
				}
			}
			if problems := Check(cfg, Options{Fires: []Event{event}}); len(problems) != 0 {
				t.Errorf("refused though the event is fired:\n%s", problemsText(problems))
			}
		})
	}
}

func TestCanBlock(t *testing.T) {
	blocking := map[Event]bool{PreToolUse: true, UserPromptSubmit: true, UserPromptExpansion: true, Stop: true, SubagentStop: true, PreModelSwitch: true, ConfigChange: true, PostToolBatch: true, Elicitation: true, ElicitationResult: true}
	for _, e := range Events() {
		if e.CanBlock() != blocking[e] {
			t.Errorf("%s.CanBlock() = %v, want %v", e, e.CanBlock(), blocking[e])
		}
	}
	if Event("stop").CanBlock() {
		t.Error(`Event("stop").CanBlock() is true for a name that is not an event`)
	}
	if n := len(Events()); n != 25 {
		t.Errorf("Events() has %d events, want 25", n)
	}
}

// In the user's own configuration there is no folder a relative path
// could be relative to.
func TestCheckRefusesARelativeCommandInTheUsersOwnConfig(t *testing.T) {
	relative := []string{"./hooks/check.sh", "hooks/check.sh --strict", "../check.sh", "  ./check.sh"}
	fine := []string{
		"/usr/local/bin/check", "~/hooks/check.sh", "jq -e .ok", "gofmt -l .",
		`"$CLAUDE_PROJECT_DIR"/.claude/hooks/check.sh`, "$HOME/hooks/check.sh",
		"[ -f go.mod ] && go vet ./...", "LOG=logs/hook.log check", "echo a/b",
	}
	for _, command := range relative {
		global := Config{Hooks: []Hook{{Event: Stop, Command: command, Global: true, Source: "gnosh.json:2"}}}
		problems := Check(global, Options{Fires: Events()})
		if len(problems) != 1 || problems[0].Warning {
			t.Errorf("%q in the user's own config: got %s, want one problem", command, problemsText(problems))
			continue
		}
		contains(t, "the problem", problems[0].String(), "gnosh.json:2: ", "starts with a relative path", "every folder", "full path")
		project := Config{Hooks: []Hook{{Event: Stop, Command: command}}}
		if problems := Check(project, Options{Fires: Events()}); len(problems) != 0 {
			t.Errorf("%q in a project's config was refused: %s", command, problemsText(problems))
		}
	}
	for _, command := range fine {
		global := Config{Hooks: []Hook{{Event: Stop, Command: command, Global: true}}}
		if problems := Check(global, Options{Fires: Events()}); len(problems) != 0 {
			t.Errorf("%q was refused: %s", command, problemsText(problems))
		}
	}
	// "echo a/b" is the one a first word alone cannot tell apart.
	if !startsWithRelativePath("a/b") || startsWithRelativePath("echo a/b") {
		t.Error("startsWithRelativePath looks past the first word")
	}

	cfg, err := ParseConfig(json.RawMessage(`{"Stop": [{"hooks": [{"type": "command", "command": "./tests.sh"}]}]}`), "gnosh.json")
	if err != nil {
		t.Fatal(err)
	}
	if len(Check(cfg, Options{Fires: Events()})) != 0 || len(Check(cfg.AsGlobal(), Options{Fires: Events()})) != 1 {
		t.Error("AsGlobal does not mark a parsed block as the user's own")
	}
	if cfg.Hooks[0].Global {
		t.Error("AsGlobal changed the configuration it was called on")
	}
}

// A matcher that can never fit is worth a warning, and never more.
func TestCheckWarnsAboutMatchersThatFitNothing(t *testing.T) {
	tools := []string{"Read", "Write", "Edit", "Bash", "Grep", "Glob", "Agent", "TaskStop", "Download"}
	opts := Options{Fires: Events(), ToolNames: tools, Guards: map[string][]string{"Download": {"WebFetch", "Write"}}}
	tests := []struct {
		hook Hook
		warn string // "" for no warning
	}{
		{Hook{Event: PreToolUse, Matcher: "Bash"}, ""},
		{Hook{Event: PreToolUse, Matcher: "TodoWrite"}, `the matcher "TodoWrite"`},
		{Hook{Event: PreToolUse, Matcher: "bash"}, `the matcher "bash"`},
		{Hook{Event: PreToolUse, Matcher: "Edit|MultiEdit|Write"}, ""},
		{Hook{Event: PostToolUse, Matcher: "LS"}, `the matcher "LS"`},
		{Hook{Event: PreToolUse, Matcher: "Task"}, ""},      // an older name of Agent
		{Hook{Event: PreToolUse, Matcher: "KillShell"}, ""}, // an older name of TaskStop
		{Hook{Event: PreToolUse, Matcher: "WebFetch"}, ""},  // guards Download
		{Hook{Event: PreToolUse, Matcher: "MultiEdit"}, ""}, // guards Edit
		{Hook{Event: PreToolUse, Matcher: "WebSearch"}, `the matcher "WebSearch"`},
		{Hook{Event: PreToolUse, Matcher: "^Notebook"}, `the matcher "^Notebook"`},
		{Hook{Event: PreToolUse, Matcher: "mcp__github__.*"}, ""}, // its tools come and go with their server
		{Hook{Event: PermissionRequest, Matcher: "Bash.*"}, ""},
		{Hook{Event: PreToolUse}, ""},
		{Hook{Event: SessionStart, Matcher: "startup|resume"}, ""},
		{Hook{Event: SessionStart, Matcher: "start"}, `the matcher "start"`},
		{Hook{Event: SessionStart, Matcher: "^start"}, ""},
		{Hook{Event: SessionEnd, Matcher: "logout|prompt_input_exit"}, ""},
		{Hook{Event: SessionEnd, Matcher: "quit"}, `the matcher "quit"`},
		{Hook{Event: PreCompact, Matcher: "automatic"}, `the matcher "automatic"`},
		{Hook{Event: PostCompact, Matcher: "auto"}, ""},
		{Hook{Event: StopFailure, Matcher: "rate_limit|overloaded"}, ""},
		{Hook{Event: StopFailure, Matcher: "server"}, `the matcher "server"`},
		{Hook{Event: InstructionsLoaded, Matcher: "startup"}, `the matcher "startup"`},
		{Hook{Event: Notification, Matcher: "idle_prompt"}, ""},
		{Hook{Event: Notification, Matcher: "idle"}, `the matcher "idle"`},
		{Hook{Event: SubagentStart, Matcher: "any-custom-agent"}, ""}, // agent types are not a fixed list
	}
	for _, tt := range tests {
		tt.hook.Command = "./check.sh"
		tt.hook.Source = "gnosh.json"
		problems := Check(Config{Hooks: []Hook{tt.hook}}, opts)
		switch {
		case tt.warn == "" && len(problems) != 0:
			t.Errorf("%s %q: got %s, want nothing", tt.hook.Event, tt.hook.Matcher, problemsText(problems))
		case tt.warn != "" && (len(problems) != 1 || !problems[0].Warning):
			t.Errorf("%s %q: got %q, want one warning", tt.hook.Event, tt.hook.Matcher, problemsText(problems))
		case tt.warn != "":
			contains(t, "the warning", problems[0].String(), "gnosh.json: ", tt.warn, "never runs")
			if _, err := NewRunner(Config{Hooks: []Hook{tt.hook}}, Options{Fires: Events(), ToolNames: tools, ProjectDir: "/project"}); err != nil {
				t.Errorf("a warning stopped NewRunner: %v", err)
			}
		}
	}
	// Without a list of tools there is nothing to judge a tool matcher by.
	if problems := Check(Config{Hooks: []Hook{{Event: PreToolUse, Matcher: "TodoWrite", Command: "x"}}}, Options{Fires: Events()}); len(problems) != 0 {
		t.Errorf("warned without a list of tools: %s", problemsText(problems))
	}
}

func TestMergeKeepsEveryLayerInOrder(t *testing.T) {
	userOwn := Config{}
	userOwn.Add(Hook{Event: Stop, Name: "tests", Command: "/opt/hooks/tests.sh", Source: "user file:1"})
	userOwn.Add(Hook{Event: PreToolUse, Matcher: "Bash", Command: "/opt/hooks/guard.sh", Source: "user file:2"})
	userJSON, err := ParseConfig(json.RawMessage(`{"PreToolUse": [{"matcher": "Edit", "hooks": [{"type": "command", "command": "/opt/hooks/fmt.sh"}]}]}`), "user gnosh.json")
	if err != nil {
		t.Fatal(err)
	}
	projectOwn := Config{}
	projectOwn.Add(Hook{Event: Stop, Name: "tests", Command: "./hooks/tests.sh", Timeout: time.Hour, Source: "project file:1"})
	projectOwn.Add(Hook{Event: Stop, Name: "lint", Command: "./hooks/lint.sh", Source: "project file:2"})
	projectOwn.Add(Hook{Event: PreToolUse, Matcher: "Bash", Command: "/opt/hooks/guard.sh", Source: "project file:3"})
	projectJSON, err := ParseConfig(json.RawMessage(`{"Stop": [{"hooks": [{"type": "command", "command": "./hooks/video.sh"}]}]}`), "project gnosh.json")
	if err != nil {
		t.Fatal(err)
	}

	all := Merge(userOwn, userJSON, projectOwn, projectJSON)
	var got []string
	for _, h := range all.Hooks {
		got = append(got, string(h.Event)+" "+h.Command+" <"+h.Source+">")
	}
	want := []string{
		// The named hook of a later layer took the place of the earlier
		// one, where that one stood.
		"Stop ./hooks/tests.sh <project file:1>",
		"PreToolUse /opt/hooks/guard.sh <user file:2>",
		"PreToolUse /opt/hooks/fmt.sh <user gnosh.json: PreToolUse group 1, hook 1>",
		"Stop ./hooks/lint.sh <project file:2>",
		// Hooks without a name never replace each other, even when they
		// are the same.
		"PreToolUse /opt/hooks/guard.sh <project file:3>",
		"Stop ./hooks/video.sh <project gnosh.json: Stop group 1, hook 1>",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("merged hooks:\n got %s\nwant %s", strings.Join(got, "\n     "), strings.Join(want, "\n     "))
	}
	if len(userOwn.Hooks) != 2 || userOwn.Hooks[0].Command != "/opt/hooks/tests.sh" {
		t.Errorf("Merge changed a layer it was given: %+v", userOwn.Hooks)
	}

	r, err := NewRunner(all, Options{Fires: Events(), ProjectDir: "/project"})
	if err != nil {
		t.Fatal(err)
	}
	if !r.Has(Stop) || !r.Has(PreToolUse) || r.Has(UserPromptSubmit) {
		t.Errorf("Has: Stop %v, PreToolUse %v, UserPromptSubmit %v", r.Has(Stop), r.Has(PreToolUse), r.Has(UserPromptSubmit))
	}
	var stops []string
	for _, h := range r.Hooks(Stop) {
		stops = append(stops, h.Command)
	}
	if want := []string{"./hooks/tests.sh", "./hooks/lint.sh", "./hooks/video.sh"}; !reflect.DeepEqual(stops, want) {
		t.Errorf("Hooks(Stop) = %v, want %v", stops, want)
	}
	if len(r.Hooks(SessionEnd)) != 0 {
		t.Error("Hooks(SessionEnd) is not empty")
	}
}

// What was wrong with one layer is still known after the layers are
// merged, so that one call reports everything.
func TestMergeCarriesProblemsAlong(t *testing.T) {
	good, err := ParseConfig(json.RawMessage(`{"Stop": [{"hooks": [{"type": "command", "command": "./tests.sh"}]}]}`), "a.json")
	if err != nil {
		t.Fatal(err)
	}
	bad, err := ParseConfig(json.RawMessage(`{"Stop": [{"hooks": [{"type": "command", "command": "./ok.sh"}, {"type": "script", "prompt": "done?"}]}], "WorktreeCreate": []}`), "b.json")
	if err == nil {
		t.Fatal("the second block was accepted")
	}
	all := Merge(good, bad)
	if len(all.Hooks) != 2 {
		t.Errorf("got %d hooks, want 2", len(all.Hooks))
	}
	problems := Check(all, Options{Fires: Events()})
	if len(problems) != 2 {
		t.Fatalf("got %d problems, want 2:\n%s", len(problems), problemsText(problems))
	}
	contains(t, "the problems", problemsText(problems), `b.json: Stop group 1, hook 2: the hook has "type": "script"`, `b.json: "WorktreeCreate"`)
	_, err = NewRunner(all, Options{Fires: Events(), ProjectDir: "/project"})
	var ce *ConfigError
	if !errors.As(err, &ce) || len(ce.Problems) != 2 {
		t.Errorf("NewRunner returned %v, want a *ConfigError with both problems", err)
	}
}

func TestAddAndRemove(t *testing.T) {
	var cfg Config
	cfg.Add(Hook{Event: Stop, Name: "tests", Command: "one"})
	cfg.Add(Hook{Event: Stop, Command: "two"})
	cfg.Add(Hook{Event: SubagentStop, Name: "tests", Command: "three"}) // same name, another event
	cfg.Add(Hook{Event: Stop, Name: "tests", Command: "four"})          // replaces "one"
	cfg.Add(Hook{Event: Stop, Command: "two"})                          // no name: added again
	commands := func() string {
		var out []string
		for _, h := range cfg.Hooks {
			out = append(out, h.Command)
		}
		return strings.Join(out, " ")
	}
	if got := commands(); got != "four two three two" {
		t.Fatalf("after adding: %q", got)
	}

	if n, err := cfg.Remove("Stop", "nothing-by-this-name"); n != 0 || err != nil {
		t.Errorf("Remove of a name nobody has = %d, %v", n, err)
	}
	if n, err := cfg.Remove("Stop", "tests"); n != 1 || err != nil || commands() != "two three two" {
		t.Errorf("Remove(Stop, tests) = %d, %v; left %q", n, err, commands())
	}
	if n, err := cfg.Remove("Stop", ""); n != 2 || err != nil || commands() != "three" {
		t.Errorf("Remove(Stop) = %d, %v; left %q", n, err, commands())
	}
	// A misspelt event is a mistake, not "nothing to remove".
	n, err := cfg.Remove("subagent_stop", "")
	if n != 0 || err == nil || commands() != "three" {
		t.Fatalf("Remove(subagent_stop) = %d, %v; left %q", n, err, commands())
	}
	contains(t, "the error", err.Error(), `"subagent_stop" is not a hook event`, "SubagentStop")
}

func TestNewRunnerNeedsTheProjectDirectory(t *testing.T) {
	cfg := Config{Hooks: []Hook{{Event: Stop, Command: "true"}}}
	_, err := NewRunner(cfg, Options{Fires: Events()})
	if err == nil {
		t.Fatal("NewRunner made a Runner with hooks and no project directory")
	}
	contains(t, "the error", err.Error(), "project's directory", "CLAUDE_PROJECT_DIR")
	// With no hooks there is nothing that needs it.
	if _, err := NewRunner(Config{}, Options{Fires: Events()}); err != nil {
		t.Errorf("NewRunner with no hooks: %v", err)
	}
}

func TestTimeoutFor(t *testing.T) {
	tests := []struct {
		event Event
		own   time.Duration
		want  time.Duration
	}{
		{PreToolUse, 0, 600 * time.Second},
		{Stop, 0, 600 * time.Second},
		{Stop, 1800 * time.Second, 1800 * time.Second},
		{PreToolUse, 10 * time.Second, 10 * time.Second},
		{UserPromptSubmit, 0, 30 * time.Second},
		{UserPromptSubmit, 120 * time.Second, 120 * time.Second},
		{SessionEnd, 0, 1500 * time.Millisecond},
		{SessionEnd, 500 * time.Millisecond, 500 * time.Millisecond},
		{SessionEnd, 30 * time.Second, 30 * time.Second},
		{SessionEnd, 600 * time.Second, 60 * time.Second},
		{SessionStart, 0, 600 * time.Second},
		{Notification, 0, 600 * time.Second},
	}
	for _, tt := range tests {
		if got := timeoutFor(tt.event, tt.own); got != tt.want {
			t.Errorf("timeoutFor(%s, %v) = %v, want %v", tt.event, tt.own, got, tt.want)
		}
	}
}
