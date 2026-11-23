package config

import (
	"strconv"
	"strings"
	"time"
)

// Limits are the request and tool limits that Claude Code's environment
// variables set. gnosh honours each with Claude Code's meaning. A variable
// may come from the shell or from the env block of a settings file; the
// env block wins. A value that cannot be used is passed over, with a
// warning that doctor shows.
type Limits struct {
	// APITimeout is API_TIMEOUT_MS: how long a model API that is not on
	// the user's own machine may take to start its answer. Set and zero
	// means no limit. It is already worked into each Endpoint's Timeouts.
	APITimeout Value[time.Duration]
	// BashDefaultTimeout is BASH_DEFAULT_TIMEOUT_MS: how long a command
	// runs before it moves to the background. Default: two minutes.
	BashDefaultTimeout Value[time.Duration]
	// BashMaxTimeout is BASH_MAX_TIMEOUT_MS: the most a call's own timeout
	// may ask for, never less than BashDefaultTimeout. Default: ten
	// minutes.
	BashMaxTimeout Value[time.Duration]
	// BashMaxOutput is BASH_MAX_OUTPUT_LENGTH: the characters of a
	// command's output the model reads, at most 150,000. Default: 30,000.
	BashMaxOutput Value[int]
	// MaxOutputTokens is CLAUDE_CODE_MAX_OUTPUT_TOKENS: how long one reply
	// may run. Not set means no limit is sent.
	MaxOutputTokens Value[int]
	// MaxThinkingTokens is MAX_THINKING_TOKENS: the most tokens a model
	// may think before a reply, over each model's own thinkingBudget; 0
	// turns thinking off. Not set means each model's own.
	MaxThinkingTokens Value[int]
	// FileReadMaxTokens is CLAUDE_CODE_FILE_READ_MAX_OUTPUT_TOKENS: the
	// most one read returns. Default: 25,000.
	FileReadMaxTokens Value[int]
	// GlobTimeout is CLAUDE_CODE_GLOB_TIMEOUT_SECONDS: how long a file
	// search may run. Not set means the search tool's own limit.
	GlobTimeout Value[time.Duration]
	// WebFetchCacheTTL is CLAUDE_CODE_WEBFETCH_CACHE_TTL_MS: how long a
	// fetched page is kept for another call. Default: 15 minutes, which 0
	// keeps too, as in Claude Code (its reference: "0, a decimal, or any
	// other spelling keeps the default").
	WebFetchCacheTTL Value[time.Duration]
	// WebFetchDeadline is CLAUDE_CODE_WEBFETCH_DEADLINE_MS: how long a page
	// may take to download, its redirects with it; 0 is no limit. Default:
	// 5 minutes.
	WebFetchDeadline Value[time.Duration]
	// GlobNoIgnore is CLAUDE_CODE_GLOB_NO_IGNORE: true lists, in a Glob,
	// the files .gitignore leaves out, as Claude Code's Glob does by
	// default. Not set, or false, leaves them out, as gnosh's Glob does.
	GlobNoIgnore Value[bool]
	// AutoCompactPct is CLAUDE_AUTOCOMPACT_PCT_OVERRIDE: a percentage of
	// the auto-compact window, which can make the backstop run earlier and
	// never later. Not set means none.
	AutoCompactPct Value[int]
	// MCPTimeout is MCP_TIMEOUT: how long an MCP server may take to start
	// and answer the handshake. Default: 30 seconds.
	MCPTimeout Value[time.Duration]
	// MCPToolTimeout is MCP_TOOL_TIMEOUT: how long one call of an MCP
	// server's tool may run when the server's entry gives no timeout of
	// its own. Not set means no limit.
	MCPToolTimeout Value[time.Duration]
	// MCPMaxOutputTokens is MAX_MCP_OUTPUT_TOKENS: the most of one MCP
	// tool's result the model is handed; a longer one goes to a file.
	// Default: 25,000.
	MCPMaxOutputTokens Value[int]
}

// limitVars are the variables of Claude Code's, and the two provider keys,
// that gnosh reads.
var limitVars = []struct{ name, what, from string }{
	{"API_TIMEOUT_MS", "milliseconds a model API that is not local may take to start answering; 0 is no limit", fromClaudeCode},
	{"BASH_DEFAULT_TIMEOUT_MS", "milliseconds a command runs before it moves to the background", fromClaudeCode},
	{"BASH_MAX_TIMEOUT_MS", "the most a command's own timeout may ask for", fromClaudeCode},
	{"BASH_MAX_OUTPUT_LENGTH", "characters of a command's output the model reads", fromClaudeCode},
	{"CLAUDE_CODE_MAX_OUTPUT_TOKENS", "how long one reply may run", fromClaudeCode},
	{"MAX_THINKING_TOKENS", "tokens a model may think before a reply; 0 turns thinking off", fromClaudeCode},
	{"CLAUDE_CODE_FILE_READ_MAX_OUTPUT_TOKENS", "the most one read returns", fromClaudeCode},
	{"CLAUDE_CODE_GLOB_TIMEOUT_SECONDS", "seconds a file search may run", fromClaudeCode},
	{"CLAUDE_CODE_WEBFETCH_CACHE_TTL_MS", "milliseconds a fetched page is kept for another call", fromClaudeCode},
	{"CLAUDE_CODE_WEBFETCH_DEADLINE_MS", "milliseconds a page may take to download; 0 is no limit", fromClaudeCode},
	{"CLAUDE_CODE_GLOB_NO_IGNORE", "true lists in a Glob the files .gitignore leaves out, as Claude Code's Glob does by default", fromClaudeCode},
	{"CLAUDE_CODE_AUTO_COMPACT_WINDOW", "tokens of context before room is made; outranks the autoCompactWindow option", fromClaudeCode},
	{"CLAUDE_AUTOCOMPACT_PCT_OVERRIDE", "a percentage of that window; earlier only", fromClaudeCode},
	{"CLAUDE_CODE_SAFE_MODE", "the same as --safe-mode", fromClaudeCode},
	{"MCP_TIMEOUT", "milliseconds an MCP server may take to start", fromClaudeCode},
	{"MCP_TOOL_TIMEOUT", "milliseconds one call of an MCP server's tool may run, where its entry gives no timeout", fromClaudeCode},
	{"MAX_MCP_OUTPUT_TOKENS", "tokens of one MCP tool's result the model is handed; a longer one goes to a file", fromClaudeCode},
	{"ANTHROPIC_API_KEY", "the key of an anthropic provider that gives none", fromAPIs + ": Anthropic's own variable, which Claude Code reads too"},
	{"OPENAI_API_KEY", "the key of an openai provider that gives none", fromAPIs + ": OpenAI's own variable"},
}

// limitVar looks up one of Claude Code's variables: in the env block of
// the settings files first, then in the shell's environment. An empty
// value counts as not set.
func (ld *loader) limitVar(name string) (string, Origin, bool) {
	if v, ok := ld.b.env[name]; ok {
		o := Origin{Layer: LayerEnv, File: v.Origin.File, Line: v.Origin.Line, Name: "$" + name}
		return v.Value.Reveal(), o, v.Value.Reveal() != ""
	}
	v := ld.env[name]
	return v, envOrigin(name), v != ""
}

// bad records a variable whose value could not be used. It is a warning,
// never a mistake that stops startup.
func (ld *loader) bad(o Origin, name, format string, args ...any) {
	before := len(ld.probs.list)
	ld.probs.at(Warning, o, "$"+name+" "+format, args...)
	ld.badEnv = append(ld.badEnv, ld.probs.list[before:]...)
}

// whole reads a variable as a whole number of at least least.
func (ld *loader) whole(name string, least int) (int, Origin, bool) {
	text, o, ok := ld.limitVar(name)
	if !ok {
		return 0, o, false
	}
	n, err := strconv.Atoi(strings.TrimSpace(text))
	if err != nil || n < least {
		ld.bad(o, name, "holds %s, which is not a whole number of at least %d, so it was not used.", show(text), least)
		return 0, o, false
	}
	return n, o, true
}

// onOff reads an on/off variable. Absent, empty and unreadable all mean
// "not set".
func (ld *loader) onOff(name string) (on, set bool) {
	text, ok := ld.env[name]
	if !ok || text == "" {
		return false, false
	}
	switch strings.ToLower(strings.TrimSpace(text)) {
	case "1", "true", "yes", "on":
		return true, true
	case "0", "false", "no", "off":
		return false, true
	}
	ld.bad(envOrigin(name), name, "holds %s, which is neither 1 nor 0, so it was read as not set.", show(text))
	return false, false
}

// limits reads Claude Code's variables.
func (ld *loader) limits() Limits {
	ms := func(n int) time.Duration { return time.Duration(n) * time.Millisecond }
	l := Limits{
		BashDefaultTimeout: Value[time.Duration]{V: 2 * time.Minute},
		BashMaxTimeout:     Value[time.Duration]{V: 10 * time.Minute},
		BashMaxOutput:      Value[int]{V: 30000},
		FileReadMaxTokens:  Value[int]{V: 25000},
		WebFetchCacheTTL:   Value[time.Duration]{V: 15 * time.Minute},
		WebFetchDeadline:   Value[time.Duration]{V: 5 * time.Minute},
		MCPTimeout:         Value[time.Duration]{V: 30 * time.Second},
		MCPMaxOutputTokens: Value[int]{V: 25000},
	}
	if n, o, ok := ld.whole("MAX_MCP_OUTPUT_TOKENS", 1); ok {
		l.MCPMaxOutputTokens.put(n, o)
	}
	if n, o, ok := ld.whole("MCP_TIMEOUT", 1); ok {
		l.MCPTimeout.put(ms(n), o)
	}
	if n, o, ok := ld.whole("MCP_TOOL_TIMEOUT", 1); ok {
		l.MCPToolTimeout.put(ms(n), o)
	}
	if n, o, ok := ld.whole("API_TIMEOUT_MS", 0); ok {
		l.APITimeout.put(ms(n), o)
	}
	if n, o, ok := ld.whole("BASH_DEFAULT_TIMEOUT_MS", 1); ok {
		l.BashDefaultTimeout.put(ms(n), o)
	}
	if n, o, ok := ld.whole("BASH_MAX_TIMEOUT_MS", 1); ok {
		l.BashMaxTimeout.put(ms(n), o)
	}
	if l.BashMaxTimeout.V < l.BashDefaultTimeout.V {
		if l.BashMaxTimeout.Set {
			ld.bad(l.BashMaxTimeout.Origin, "BASH_MAX_TIMEOUT_MS", "is less than the time a command runs by default (%v), and the most is never less than that, so %v is used.", l.BashDefaultTimeout.V, l.BashDefaultTimeout.V)
		}
		l.BashMaxTimeout.V = l.BashDefaultTimeout.V
	}
	if n, o, ok := ld.whole("BASH_MAX_OUTPUT_LENGTH", 1); ok {
		const most = 150000
		if n > most {
			ld.bad(o, "BASH_MAX_OUTPUT_LENGTH", "is %d, and the most gnosh shows the model of one command's output is %d, so %d is used.", n, most, most)
			n = most
		}
		l.BashMaxOutput.put(n, o)
	}
	if n, o, ok := ld.whole("CLAUDE_CODE_MAX_OUTPUT_TOKENS", 1); ok {
		l.MaxOutputTokens.put(n, o)
	}
	if n, o, ok := ld.whole("MAX_THINKING_TOKENS", 0); ok {
		l.MaxThinkingTokens.put(n, o)
	}
	if n, o, ok := ld.whole("CLAUDE_CODE_FILE_READ_MAX_OUTPUT_TOKENS", 1); ok {
		l.FileReadMaxTokens.put(n, o)
	}
	if n, o, ok := ld.whole("CLAUDE_CODE_GLOB_TIMEOUT_SECONDS", 1); ok {
		l.GlobTimeout.put(time.Duration(n)*time.Second, o)
	}
	if n, o, ok := ld.whole("CLAUDE_CODE_WEBFETCH_CACHE_TTL_MS", 1); ok {
		l.WebFetchCacheTTL.put(ms(n), o)
	}
	if n, o, ok := ld.whole("CLAUDE_CODE_WEBFETCH_DEADLINE_MS", 0); ok {
		l.WebFetchDeadline.put(ms(n), o)
	}
	if text, o, ok := ld.limitVar("CLAUDE_CODE_GLOB_NO_IGNORE"); ok {
		switch strings.ToLower(strings.TrimSpace(text)) {
		case "1", "true", "yes", "on":
			l.GlobNoIgnore.put(true, o)
		case "0", "false", "no", "off":
			l.GlobNoIgnore.put(false, o)
		default:
			ld.bad(o, "CLAUDE_CODE_GLOB_NO_IGNORE", "holds %s, which is neither true nor false, so it was not used.", show(text))
		}
	}
	if n, o, ok := ld.whole("CLAUDE_CODE_AUTO_COMPACT_WINDOW", 1); ok {
		if n < 100000 || n > 1000000 {
			ld.bad(o, "CLAUDE_CODE_AUTO_COMPACT_WINDOW", "is %d, which is not between 100000 and 1000000, so it was not used.", n)
		} else {
			ld.b.opts.AutoCompactWindow.put(n, o)
		}
	}
	if n, o, ok := ld.whole("CLAUDE_AUTOCOMPACT_PCT_OVERRIDE", 1); ok {
		if n > 100 {
			ld.bad(o, "CLAUDE_AUTOCOMPACT_PCT_OVERRIDE", "is %d, which is not a percentage from 1 to 100, so it was not used.", n)
		} else {
			l.AutoCompactPct.put(n, o)
		}
	}
	return l
}
