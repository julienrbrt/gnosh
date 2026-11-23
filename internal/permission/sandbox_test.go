package permission

import (
	"strings"
	"testing"

	"github.com/gnosh-ai/gnosh/internal/core"
)

// With the sandbox on and autoAllowBashIfSandboxed, a command that runs in
// the sandbox runs without asking, as if an allow rule covered it; the
// deny and ask rules and the checks no allow rule passes hold as for every
// command; a command that runs outside (excluded, or asked out of it), one
// on another machine, a Monitor's and any in plan mode are judged as
// without a sandbox.
func TestSandboxAllows(t *testing.T) {
	outside := func(command string, asked bool) bool { return asked || command == "docker ps" }
	make := func(mode Mode, r rules, sb Sandbox) *Engine {
		o := testOptions(mode, r)
		o.Sandbox = sb
		return mustEngine(t, o)
	}
	on := Sandbox{On: true, AutoAllow: true, Outside: outside}
	call := func(e *Engine, tool, cmd string, kv ...any) Decision {
		in := input(append([]any{"command", cmd}, kv...)...)
		return e.Decide(tool, core.ToolCall{Name: tool, Input: in}, core.Action{Kind: core.ActionExecute, Command: cmd})
	}
	for _, c := range []struct {
		name  string
		e     *Engine
		tool  string
		cmd   string
		kv    []any
		want  Behavior
		stage Stage
	}{
		{"a command in the sandbox", make(ModeDefault, rules{}, on), "Bash", "make build", nil, Allow, StageSandbox},
		{"one that writes in the project", make(ModeDefault, rules{}, on), "Bash", "echo x > out.txt", nil, Allow, StageSandbox},
		{"in dontAsk mode too", make(ModeDontAsk, rules{}, on), "Bash", "make build", nil, Allow, StageSandbox},
		{"asked out of the sandbox", make(ModeDefault, rules{}, on), "Bash", "make build", []any{"dangerouslyDisableSandbox", true}, Ask, StageMode},
		{"an excluded command", make(ModeDefault, rules{}, on), "Bash", "docker ps", nil, Ask, StageMode},
		{"a deny rule", make(ModeDefault, rules{deny: []string{"Bash(rm *)"}}, on), "Bash", "rm x", nil, Deny, StageDeny},
		{"an ask rule", make(ModeDefault, rules{ask: []string{"Bash(git push *)"}}, on), "Bash", "git push origin", nil, Ask, StageAsk},
		{"a protected file", make(ModeDefault, rules{}, on), "Bash", "echo x > .bashrc", nil, Ask, StageSafety},
		{"plan mode", make(ModePlan, rules{}, on), "Bash", "make build", nil, Ask, StageMode},
		{"another machine", make(ModeDefault, rules{}, on), "Bash", "make build", []any{"host", "far"}, Ask, StageSafety},
		{"a Monitor", make(ModeDefault, rules{}, on), "Monitor", "make watch", nil, Ask, StageMode},
		{"without auto-allow", make(ModeDefault, rules{}, Sandbox{On: true, Outside: outside}), "Bash", "make build", nil, Ask, StageMode},
		{"without a sandbox", make(ModeDefault, rules{}, Sandbox{}), "Bash", "make build", nil, Ask, StageMode},
	} {
		d := call(c.e, c.tool, c.cmd, c.kv...)
		if d.Behavior != c.want || d.Stage != c.stage {
			t.Errorf("%s: %s at %s (%s), want %s at %s", c.name, d.Behavior, d.Stage, d.Reason, c.want, c.stage)
		}
	}
}

// The Read and Edit rules' paths, for the sandbox's lists: those written
// out in full or with a last /**, as written (a deny rule's are folded for
// matching), and a Read deny rule's pattern as the pattern, for the
// sandbox to find what it matches; no other pattern, no "!" rule.
func TestSandboxPaths(t *testing.T) {
	e := engine(t, ModeDefault, rules{
		deny:  []string{"Read(./.env)", "Read(~/.ssh/**)", "Read(*.pem)", "Edit(./Build/**)", "Bash(rm *)"},
		allow: []string{"Edit(//srv/out/**)", "Read(./docs/**)"},
	})
	read, write, allow := e.SandboxPaths()
	if strings.Join(read, " ") != "/proj/.env /home/u/.ssh /proj/**/*.pem" || strings.Join(write, " ") != "/proj/Build" || strings.Join(allow, " ") != "/srv/out" {
		t.Errorf("read %q, write %q, allow %q", read, write, allow)
	}
}

// The model's asking out of the sandbox counts however it is written, as
// the shell tool reads it: "true" in quotes is out too.
func TestSandboxAskedOutInAnySpelling(t *testing.T) {
	o := testOptions(ModeDefault, rules{})
	o.Sandbox = Sandbox{On: true, AutoAllow: true, Outside: func(command string, asked bool) bool { return asked }}
	e := mustEngine(t, o)
	for _, v := range []any{true, "true", "TRUE", " true ", 1, "yes"} {
		in := input("command", "make build", "dangerouslyDisableSandbox", v)
		if d := e.Decide("Bash", core.ToolCall{Name: "Bash", Input: in}, core.Action{Kind: core.ActionExecute, Command: "make build"}); d.Stage == StageSandbox {
			t.Errorf("asked out with %#v, and still let run as sandboxed", v)
		}
	}
	for _, v := range []any{false, "false", ""} {
		in := input("command", "make build", "dangerouslyDisableSandbox", v)
		if d := e.Decide("Bash", core.ToolCall{Name: "Bash", Input: in}, core.Action{Kind: core.ActionExecute, Command: "make build"}); d.Stage != StageSandbox {
			t.Errorf("not asked out with %#v, and not let run as sandboxed: %s", v, d.Stage)
		}
	}
}

// What the WebFetch(domain:...) rules say of a host, for the sandbox's
// proxy: a deny rule refuses it, an ask rule asks first though an allow
// rule names it too, an allow rule lets it through, but one with a * in
// a name or alone (which Claude Code's sandbox does not take); with
// ownOnly, a repository's allow rules count for nothing (the sandbox
// review).
func TestFetchRule(t *testing.T) {
	o := testOptions(ModeDefault, rules{
		deny:  []string{"WebFetch(domain:pastebin.com)"},
		ask:   []string{"WebFetch(domain:ask.example)"},
		allow: []string{"WebFetch(domain:*.npmjs.org)", "WebFetch(domain:ask.example)", "WebFetch(domain:*evil.org)", "WebFetch(domain:*)", "WebFetch(domain:pastebin.com)"},
	})
	o.Trusted = true
	o.Layers = append(o.Layers, Layer{Scope: ScopeProject, Name: "repo", Allow: []string{"WebFetch(domain:repo.example)"}})
	e := mustEngine(t, o)
	for host, want := range map[string]HostRule{
		"pastebin.com": HostDenied, "ask.example": HostAsked, "registry.npmjs.org": HostAllowed, "npmjs.org": HostUnruled,
		"notevil.org": HostUnruled, "other.example": HostUnruled, "repo.example": HostAllowed,
	} {
		if got := e.FetchRule(host, false); got != want {
			t.Errorf("FetchRule(%q) = %v, want %v", host, got, want)
		}
	}
	if got := e.FetchRule("repo.example", true); got != HostUnruled {
		t.Errorf("a repository's allow rule under strictAllowlist: %v", got)
	}
	if got := e.FetchRule("registry.npmjs.org", true); got != HostAllowed {
		t.Errorf("the person's own allow rule under strictAllowlist: %v", got)
	}
}
