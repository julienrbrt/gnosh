package app

import (
	"encoding/json"
	"slices"
	"strings"

	"github.com/gnosh-ai/gnosh/internal/agent"
	"github.com/gnosh-ai/gnosh/internal/envscrub"
	"github.com/gnosh-ai/gnosh/internal/sandbox"
)

// With CLAUDE_CODE_SUBPROCESS_ENV_SCRUB set to 1, the programs gnosh starts
// that are not gnosh itself (the shell's commands, a skill's !`commands`,
// hooks, stdio MCP servers, LSP servers, the status line and the file
// suggestion command, gnosh's own git) start without the credentials
// gnosh was started with; gnosh keeps them for its own requests, and a
// gnosh it starts again (/restart, a work tree, the background) has them
// (envscrub; spec/DECISIONS.md 384).

// scrubbing reports whether the scrub is on.
func (p *program) scrubbing() bool { return envscrub.On(p.o.Environ) }

// childEnv is env as a program gnosh starts is given it: without its
// credentials when the scrub is on.
func (p *program) childEnv(env []string) []string {
	if p.scrubbing() {
		return envscrub.Env(env)
	}
	return env
}

// childEnviron is the environment gnosh was started with, as a program it
// starts is given it.
func (p *program) childEnviron() []string { return p.childEnv(slices.Clone(p.o.Environ)) }

// scriptCaps are CLAUDE_CODE_SCRIPT_CAPS, with the scrub on: how many of
// the model's commands holding each text may run in the session (a JSON
// object of numbers, {"deploy.sh": 2}). One that cannot be read caps
// nothing, and the person is told.
func (l *live) scriptCaps() map[string]int {
	raw := strings.TrimSpace(l.envVar("CLAUDE_CODE_SCRIPT_CAPS"))
	if raw == "" || !l.p.scrubbing() {
		return nil
	}
	var caps map[string]int
	if err := json.Unmarshal([]byte(raw), &caps); err != nil {
		l.notice(agent.LevelWarning, "scrub", "CLAUDE_CODE_SCRIPT_CAPS is not a JSON object of whole numbers ({\"deploy.sh\": 2}), so no script is capped: "+err.Error())
		return nil
	}
	return caps
}

// pidIsolation is how a command that runs outside the sandbox is given a
// PID namespace of its own with the scrub on, on Linux, as Claude Code
// gives one: bubblewrap with the files as they are and a /proc of its
// own, so that it reads neither gnosh's environment nor the person's
// other processes' through /proc (whose credentials the scrub took out of
// its own). nil, and the person told, where bubblewrap cannot make one.
func (l *live) pidIsolation() func([]string) []string {
	st := sandbox.Check()
	if !st.OK || st.Tool == "" {
		l.notice(agent.LevelWarning, "scrub", "CLAUDE_CODE_SUBPROCESS_ENV_SCRUB is set, and the commands that run outside the sandbox cannot be given a PID namespace of their own here ("+firstOf(st.Why, "bubblewrap cannot run")+"): such a command can read the environment of your other processes, gnosh's among them, through /proc.")
		return nil
	}
	tool := st.Tool
	return func(argv []string) []string {
		return append([]string{tool, "--dev-bind", "/", "/", "--unshare-pid", "--proc", "/proc", "--die-with-parent", "--"}, argv...)
	}
}

// mcpEnviron is the environment a stdio MCP server starts from, its own
// env from its settings laid over it: with CLAUDE_CODE_MCP_ALLOWLIST_ENV
// on, only what a program needs to run as the person (envscrub.Baseline),
// as Claude Code starts one; else childEnviron.
func (p *program) mcpEnviron() []string {
	if envscrub.Switch(p.o.Environ, envscrub.AllowlistVar) {
		return envscrub.Baseline(p.o.Environ)
	}
	return p.childEnviron()
}
