package permission

import (
	"fmt"
	"strings"
)

// Mode says what runs without asking when no rule has spoken.
type Mode string

const (
	// ModeDefault asks before anything that changes files, runs a command
	// that is not on the read-only list, or reaches the network.
	ModeDefault Mode = "default"
	// ModeAcceptEdits also lets file edits, and a few file commands, run
	// inside the working directories.
	ModeAcceptEdits Mode = "acceptEdits"
	// ModePlan lets the model look and refuses file edits; commands that
	// are not read-only still ask.
	ModePlan Mode = "plan"
	// ModeDontAsk refuses whatever would have asked.
	ModeDontAsk Mode = "dontAsk"
	// ModeBypass runs everything that no deny rule, ask rule or
	// dangerous-removal check stops.
	ModeBypass Mode = "bypassPermissions"
)

// Modes lists the modes in the order a person meets them.
var Modes = []Mode{ModeDefault, ModeAcceptEdits, ModePlan, ModeDontAsk, ModeBypass}

// ParseMode reads a mode as it is typed in a flag or a settings file.
// "manual" is another name for "default". "auto" is Claude Code's
// classifier mode, which gnosh does not have: it reads as ModeDefault with
// fellBack set, so the caller can say so in one line.
func ParseMode(s string) (m Mode, fellBack bool, err error) {
	switch s {
	case "default", "manual":
		return ModeDefault, false, nil
	case "acceptEdits":
		return ModeAcceptEdits, false, nil
	case "plan":
		return ModePlan, false, nil
	case "dontAsk":
		return ModeDontAsk, false, nil
	case "bypassPermissions":
		return ModeBypass, false, nil
	case "auto":
		return ModeDefault, true, nil
	}
	names := make([]string, len(Modes))
	for i, m := range Modes {
		names[i] = string(m)
	}
	return "", false, fmt.Errorf("permission mode %q is not one gnosh knows; the modes are %s (and \"manual\", another name for \"default\")", s, strings.Join(names, ", "))
}

// valid reports whether m is one of the five modes.
func (m Mode) valid() bool {
	for _, k := range Modes {
		if m == k {
			return true
		}
	}
	return false
}

// Behavior is what the engine decided about a tool call.
type Behavior string

const (
	// Allow: the call runs.
	Allow Behavior = "allow"
	// Ask: a person is asked first. Where nobody can answer, the caller
	// refuses the call.
	Ask Behavior = "ask"
	// Deny: the call is refused and the model is told why.
	Deny Behavior = "deny"
)

// Stage names the step of the decision order that settled a call.
type Stage string

const (
	// StageTool: the tool itself is switched off (a bare deny rule, or
	// --tools left it out).
	StageTool Stage = "tool"
	// StageDeny: a deny rule matched.
	StageDeny Stage = "deny-rule"
	// StageBlock: blockReadsOutsideWorkingDirectories or
	// blockWritesOutsideWorkingDirectories (Options.BlockReads and
	// BlockWrites) refused the call, or asks about a command whose reads
	// it cannot rule out, in every mode.
	StageBlock Stage = "block"
	// StageAsk: an ask rule matched.
	StageAsk Stage = "ask-rule"
	// StageSafety: one of the checks no allow rule passes: a protected
	// path, a dangerous removal, a command that could not be checked
	// against the deny rules.
	StageSafety Stage = "safety"
	// StageAllow: an allow rule matched.
	StageAllow Stage = "allow-rule"
	// StageMode: no rule spoke; the mode decided.
	StageMode Stage = "mode"
	// StageSandbox: a shell command that runs in the operating system's
	// sandbox was let run, as Options.Sandbox says (sandbox.go).
	StageSandbox Stage = "sandbox"
	// StageHook: a PreToolUse hook decided (see Decision.WithHook).
	StageHook Stage = "hook"
)

// List says which of the three rule lists a rule is in.
type List string

const (
	// ListDeny rules refuse.
	ListDeny List = "deny"
	// ListAsk rules force a question.
	ListAsk List = "ask"
	// ListAllow rules let a call run.
	ListAllow List = "allow"
)

// Scope says what kind of place a layer of rules came from. It decides
// where a rule's /path anchors and whether an untrusted folder holds the
// layer back.
type Scope string

const (
	// ScopeUser: the user's own settings, which apply in every project.
	ScopeUser Scope = "user"
	// ScopeProject: settings that came with the project folder.
	ScopeProject Scope = "project"
	// ScopeLocal: the user's private settings for one project. A local
	// file that is tracked in git, or reached through a link, belongs to
	// the project: its Layer must then set FromProject.
	ScopeLocal Scope = "local"
	// ScopeSettingsFlag: a file or JSON given to --settings.
	ScopeSettingsFlag Scope = "flag-settings"
	// ScopeCLI: --allowedTools, --disallowedTools and --add-dir.
	ScopeCLI Scope = "cli"
	// ScopeSkill: a skill's allowed-tools, for the turn that runs it.
	ScopeSkill Scope = "skill"
	// ScopeSession: rules approved at a prompt during the session.
	ScopeSession Scope = "session"
)

// GrantScope says how long an approval given at a prompt lasts.
type GrantScope string

const (
	// GrantSession: until the session ends; never written to a file.
	GrantSession GrantScope = "session"
	// GrantLocal: kept. The caller writes the rule to the project's local
	// settings file; the engine applies it from now on.
	GrantLocal GrantScope = "local"
)
