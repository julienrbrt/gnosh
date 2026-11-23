package hooks

import (
	"strings"
	"time"
)

// Event names a moment at which gnosh runs hooks. The names are Claude
// Code's, and they are written exactly: "PreToolUse" is an event,
// "pretooluse" and "pre_tool_use" are mistakes.
type Event string

// The events gnosh has. The comment on each says when the agent loop fires
// it and what a hook can do there.
const (
	// PreToolUse fires before every tool call, a sub-agent's included. A
	// hook can stop the call, approve it, put it to the user, change its
	// input, or add text for the model.
	PreToolUse Event = "PreToolUse"
	// PostToolUse fires after a tool call succeeds. The call has run, so a
	// block is feedback: its reason goes to the model after the tool's
	// output.
	PostToolUse Event = "PostToolUse"
	// PostToolUseFailure fires after a tool call returns an error the
	// model reads. A block is feedback, as for PostToolUse.
	PostToolUseFailure Event = "PostToolUseFailure"
	// UserPromptSubmit fires when the user sends a prompt, before the
	// model sees it. A hook can stop the prompt or add text beside it.
	UserPromptSubmit Event = "UserPromptSubmit"
	// SessionStart fires before the first prompt a process sends to a
	// session, and again after a summary. It cannot stop anything; it can
	// add text for the model.
	SessionStart Event = "SessionStart"
	// Stop fires when the main agent is about to end its turn. A block
	// keeps the turn going, with the reason as the model's next
	// instruction.
	Stop Event = "Stop"
	// SubagentStart fires when a sub-agent starts, before its first
	// prompt. It cannot stop anything; it can add text to that prompt.
	SubagentStart Event = "SubagentStart"
	// SubagentStop fires when a sub-agent is about to finish. It works as
	// Stop does, for the sub-agent.
	SubagentStop Event = "SubagentStop"
	// StopFailure fires instead of Stop when a turn ends on an error from
	// the model's service. Nothing a hook says there is read.
	StopFailure Event = "StopFailure"
	// PreCompact fires just before a conversation is summarized. It
	// cannot stop the summary; what a hook prints becomes extra
	// instructions for it.
	PreCompact Event = "PreCompact"
	// PostCompact fires once a conversation has been summarized. It
	// cannot stop anything.
	PostCompact Event = "PostCompact"
	// SessionEnd fires when a session ends. Nothing a hook says there is
	// read, and the hooks get little time, so that quitting is quick.
	SessionEnd Event = "SessionEnd"
	// InstructionsLoaded fires when a context file is loaded. Nothing a
	// hook says there is read, and the loop does not wait for it.
	InstructionsLoaded Event = "InstructionsLoaded"
	// PermissionRequest fires when a permission question is about to be
	// shown. A hook can answer it in the user's place: allow or deny.
	PermissionRequest Event = "PermissionRequest"
	// Notification fires when gnosh raises a notification. Nothing a hook
	// says there is read, and the loop does not wait for it.
	Notification Event = "Notification"
	// PreModelSwitch fires before a switch of the session's model that the
	// person asked for (/model, or its picker) is made. A hook can refuse
	// the switch, or put it to the person; a refusal leaves the model as it
	// was.
	PreModelSwitch Event = "PreModelSwitch"
	// PostModelSwitch fires after the session's model is switched (/model).
	// What a hook adds (additionalContext, or plain text) goes to the model
	// with the next request. It cannot undo the switch.
	PostModelSwitch Event = "PostModelSwitch"
	// DirectoryAdded fires when a working directory is added to a running
	// session (/add-dir). A hook's systemMessage goes to the model with the
	// next turn.
	DirectoryAdded Event = "DirectoryAdded"
	// CwdChanged fires when a command leaves the shell in another
	// directory, which later commands start in. A hook's systemMessage is
	// shown, and it may write export lines to the session's environment
	// file (GNOSH_ENV_FILE, CLAUDE_ENV_FILE), which every later command
	// runs first.
	CwdChanged Event = "CwdChanged"
	// UserPromptExpansion fires when a typed /name expands into a prompt
	// (a skill or a command of the person's), before the model sees it. A
	// hook can stop the expansion or add text beside it.
	UserPromptExpansion Event = "UserPromptExpansion"
	// ConfigChange fires when a settings file changes while the session
	// runs, before the change is taken in. A block keeps the settings the
	// session had.
	ConfigChange Event = "ConfigChange"
	// PostToolBatch fires once after every tool call of a reply has
	// finished, before the model is asked again. A hook can add text for
	// the model, and a block ends the turn there, with the reason kept in
	// the conversation.
	PostToolBatch Event = "PostToolBatch"
	// FileChanged fires when a watched file is made, changed or removed,
	// whatever did it. Nothing can be stopped; a hook's systemMessage is
	// shown, it may write export lines to the session's environment file,
	// and it may say which files are watched from then on (watchPaths).
	FileChanged Event = "FileChanged"
	// Elicitation fires when an MCP server asks the person for values in
	// the middle of a call, before the form is shown. A hook can answer in
	// the person's place ("action": "accept", with the values in
	// "content"), decline or cancel; one that says nothing leaves the form
	// to the person. A block, or a hook that could not evaluate, declines.
	Elicitation Event = "Elicitation"
	// ElicitationResult fires when the person has answered an MCP server's
	// form, before the answer goes to the server. A hook can put other
	// values in its place, or decline or cancel instead; one that says
	// nothing lets the answer go as it is. A block, or a hook that could
	// not evaluate, declines.
	ElicitationResult Event = "ElicitationResult"
)

// Events returns the events gnosh has, in the order the specification
// lists them.
func Events() []Event {
	return []Event{
		PreToolUse, PostToolUse, PostToolUseFailure, UserPromptSubmit,
		SessionStart, Stop, SubagentStart, SubagentStop, StopFailure,
		PreCompact, PostCompact, SessionEnd, InstructionsLoaded,
		PermissionRequest, Notification, PreModelSwitch, PostModelSwitch,
		DirectoryAdded, CwdChanged, UserPromptExpansion, ConfigChange,
		PostToolBatch, FileChanged, Elicitation, ElicitationResult,
	}
}

// CanBlock reports whether a hook on this event can stop the thing the
// event announces: a tool call, a prompt, the end of a turn. These are the
// events on which a hook that could not run blocks, and so the ones on
// which a broken hook holds the work up.
func (e Event) CanBlock() bool {
	i, ok := describe(e)
	return ok && i.class == gate
}

// OfTool reports whether the event is about one tool call: the events on
// which a matcher is held against the tool's name and an "if" filter
// against the call.
func (e Event) OfTool() bool {
	i, ok := describe(e)
	return ok && i.subject == toolName
}

// class sorts the events by what an objection, or a hook that could not
// evaluate, does to the thing the event announces.
type class int

const (
	// gate: the thing does not happen. The call does not run, the prompt
	// is not sent, the turn does not end.
	gate class = iota
	// feedback: the thing has already happened, and the reason goes to
	// the model.
	feedback
	// answers: the hooks answer a permission question. A hook that does
	// not answer leaves the question to the user.
	answers
	// warns: nothing can be stopped. The user is shown one warning.
	warns
	// records: nothing can be stopped and what the hook prints is not
	// read. What happened is kept in the hook's record.
	records
)

// subject says what an event's matcher is tested against.
type subject int

const (
	// nothing: the event takes no matcher.
	nothing subject = iota
	// toolName: the name of the tool being called.
	toolName
	// ownValue: a value of the event's own, such as a session's source.
	ownValue
)

// The time a hook may take, as Claude Code sets it.
const (
	// defaultTimeout is what a hook gets on most events.
	defaultTimeout = 600 * time.Second
	// promptTimeout is what a hook on UserPromptSubmit, PreModelSwitch or
	// PostModelSwitch gets: each holds up what the person has just asked
	// for.
	promptTimeout = 30 * time.Second
	// sessionEndTimeout is what a SessionEnd hook gets, so that quitting
	// is not held up.
	sessionEndTimeout = 1500 * time.Millisecond
	// sessionEndLimit is the most a SessionEnd hook's own timeout can ask
	// for.
	sessionEndLimit = 60 * time.Second
)

// eventInfo is what the package knows about one event.
type eventInfo struct {
	class class

	// subject is what a matcher is tested against, and what is the word
	// for it in messages ("the tool's name", "the source").
	subject subject
	what    string
	// values lists every value the subject can take, when they are a
	// fixed list.
	values []string
	// narrow marks the events whose list matchers are made of letters,
	// digits, "_" and "|" only (StopFailure and FileChanged, as in Claude
	// Code).
	narrow bool

	// mode says that the event's input carries permission_mode.
	mode bool
	// reads says that what a hook prints is read. Where it is false only
	// the exit status counts.
	reads bool
	// plain says that plain text printed by a hook that exited 0 is text
	// for the model.
	plain bool
	// halts says that "continue": false ends the turn.
	halts bool
	// fields lists what a hook may say inside hookSpecificOutput, beside
	// hookEventName.
	fields []string
}

// describe returns what the package knows about e, and whether e is one
// of the events gnosh has.
func describe(e Event) (eventInfo, bool) {
	tool := eventInfo{subject: toolName, what: "the tool's name", mode: true, reads: true, halts: true}
	switch e {
	case PreToolUse:
		tool.class = gate
		tool.fields = []string{"permissionDecision", "permissionDecisionReason", "updatedInput", "additionalContext"}
		return tool, true
	case PostToolUse:
		tool.class = feedback
		tool.fields = []string{"additionalContext", "updatedToolOutput", "updatedMCPToolOutput"}
		return tool, true
	case PostToolUseFailure:
		tool.class = feedback
		tool.fields = []string{"additionalContext"}
		return tool, true
	case PermissionRequest:
		tool.class = answers
		tool.fields = []string{"decision"}
		return tool, true
	case UserPromptSubmit:
		return eventInfo{
			class: gate, mode: true, reads: true, plain: true, halts: true,
			fields: []string{"additionalContext", "sessionTitle", "suppressOriginalPrompt"},
		}, true
	case Stop, SubagentStop:
		return eventInfo{
			class: gate, mode: true, reads: true, halts: true,
			fields: []string{"additionalContext"},
		}, true
	case SessionStart:
		return eventInfo{
			class: warns, subject: ownValue, what: "the source",
			values: []string{"startup", "resume", "clear", "compact", "fork"},
			reads:  true, plain: true, halts: true,
			fields: []string{"additionalContext", "initialUserMessage", "sessionTitle", "watchPaths", "reloadSkills"},
		}, true
	case SubagentStart:
		return eventInfo{
			class: warns, subject: ownValue, what: "the sub-agent's type",
			reads: true, halts: true,
			fields: []string{"additionalContext"},
		}, true
	case PreCompact:
		return eventInfo{
			class: warns, subject: ownValue, what: "the trigger",
			values: []string{"manual", "auto"},
			reads:  true, plain: true,
		}, true
	case PostCompact:
		return eventInfo{
			class: warns, subject: ownValue, what: "the trigger",
			values: []string{"manual", "auto"},
			reads:  true, halts: true,
		}, true
	case StopFailure:
		return eventInfo{
			class: records, subject: ownValue, what: "the kind of error", narrow: true,
			values: []string{
				"rate_limit", "overloaded", "authentication_failed", "billing_error",
				"model_not_found", "invalid_request", "server_error", "max_output_tokens", "unknown",
			},
		}, true
	case SessionEnd:
		return eventInfo{
			class: records, subject: ownValue, what: "the reason",
			values: []string{"clear", "other", "prompt_input_exit", "resume", "logout"},
		}, true
	case InstructionsLoaded:
		return eventInfo{
			class: records, subject: ownValue, what: "the reason the file was loaded",
			values: []string{"session_start", "nested_traversal", "include", "compact", "path_glob_match"},
		}, true
	case Notification:
		return eventInfo{
			class: records, subject: ownValue, what: "the type of notification",
			values: []string{"permission_prompt", "idle_prompt", "push_notification"},
		}, true
	case PreModelSwitch:
		return eventInfo{
			class: gate, subject: ownValue, what: "the model switched to",
			reads:  true,
			fields: []string{"permissionDecision", "permissionDecisionReason"},
		}, true
	case PostModelSwitch:
		return eventInfo{
			class: warns, subject: ownValue, what: "the model switched to",
			reads: true, plain: true,
			fields: []string{"additionalContext"},
		}, true
	case DirectoryAdded:
		return eventInfo{
			class: warns, subject: ownValue, what: "the source",
			values: []string{"slash_command", "register_repo_root"},
			reads:  true,
		}, true
	case CwdChanged:
		return eventInfo{class: warns, reads: true, fields: []string{"watchPaths"}}, true
	case FileChanged:
		return eventInfo{
			class: warns, subject: ownValue, what: "the file's name", narrow: true,
			reads: true, fields: []string{"watchPaths"},
		}, true
	case PostToolBatch:
		return eventInfo{
			class: gate, mode: true, reads: true, halts: true,
			fields: []string{"additionalContext"},
		}, true
	case ConfigChange:
		// (Claude Code's policy_settings and skills are not here: gnosh
		// has no managed settings and does not watch skill files, so a
		// matcher on them fits nothing, which is said.)
		return eventInfo{
			class: gate, subject: ownValue, what: "the source",
			values: []string{"user_settings", "project_settings", "local_settings"},
			reads:  true,
		}, true
	case UserPromptExpansion:
		return eventInfo{
			class: gate, subject: ownValue, what: "the command's name",
			mode: true, reads: true, plain: true, halts: true,
			fields: []string{"additionalContext"},
		}, true
	case Elicitation, ElicitationResult:
		return eventInfo{
			class: gate, subject: ownValue, what: "the MCP server's name",
			reads: true, fields: []string{"action", "content"},
		}, true
	}
	return eventInfo{}, false
}

// timeoutFor returns how long a hook on event e may run when its own
// configured timeout is own (zero: none configured).
func timeoutFor(e Event, own time.Duration) time.Duration {
	switch {
	case e == SessionEnd && own > 0:
		return min(own, sessionEndLimit)
	case e == SessionEnd:
		return sessionEndTimeout
	case own > 0:
		return own
	case e == UserPromptSubmit, e == PreModelSwitch, e == PostModelSwitch:
		return promptTimeout
	}
	return defaultTimeout
}

// claudeOnly lists the Claude Code hook events gnosh does not have, and
// says for each whether a hook on it can block or decide something in
// Claude Code. A hook configured on one of them would never run.
func claudeOnly(name string) (canBlock, ok bool) {
	switch name {
	case "WorktreeCreate", "WorktreeRemove", "TaskCreated", "TaskCompleted",
		"TeammateIdle":
		return true, true
	case "PermissionDenied", "Setup", "MessageDisplay":
		return false, true
	}
	return false, false
}

// eventList is the events' names in a sentence.
func eventList() string {
	var names []string
	for _, e := range Events() {
		names = append(names, string(e))
	}
	return strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1]
}

// nearEvent returns the event that name is a misspelling of by letter case,
// underscores, hyphens or spaces alone, if there is one.
func nearEvent(name string) (Event, bool) {
	flat := func(s string) string {
		return strings.ToLower(strings.NewReplacer("_", "", "-", "", " ", "").Replace(s))
	}
	for _, e := range Events() {
		if flat(name) == flat(string(e)) {
			return e, true
		}
	}
	return "", false
}

// eventProblem says what is wrong with name as an event, or returns "" if
// it is one of gnosh's.
func eventProblem(name string) string {
	if _, ok := describe(Event(name)); ok {
		return ""
	}
	if e, ok := nearEvent(name); ok {
		return quote(name) + " is not a hook event. Event names are written exactly, and this one is " + string(e) + ". (gnosh's hook events are " + eventList() + ".)"
	}
	if canBlock, ok := claudeOnly(name); ok {
		msg := quote(name) + " is a Claude Code hook event that gnosh does not have, so this hook would never run."
		if canBlock {
			msg += " In Claude Code it can block, so a guard written on it would be silently absent here."
		}
		return msg + " Remove it, or move the check to an event gnosh has: " + eventList() + "."
	}
	if name == "" {
		return "the hook names no event. gnosh's hook events are " + eventList() + "."
	}
	return quote(name) + " is not a hook event. gnosh's hook events are " + eventList() + ", written exactly so."
}
