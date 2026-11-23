package hooks

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// Input describes one occurrence of an event: what the hooks are told on
// stdin and in their environment, and what their matchers are tested
// against. Set Event, the fields every event has, and the fields of that
// event; the rest are ignored.
//
// The agent loop should build every Input through one helper of its own,
// so that the session's facts are the same in all of them.
type Input struct {
	// Event is the event that is happening.
	Event Event

	// IfMatch, on a tool event, says whether the call takes a hook's "if"
	// filter (Hook.If). Nil means every filter takes it. It is never sent
	// to a hook.
	IfMatch func(rule string) bool
	// NoAgentHooks says the event is one of an agent hook's own sub-agent:
	// no agent hook runs on it, so that a hook cannot set itself off. It
	// is never sent to a hook.
	NoAgentHooks bool

	// SessionID is the session the user is in ("session_id"). For a
	// sub-agent's event it is still the user's session; AgentID names the
	// sub-agent.
	SessionID string
	// TranscriptPath is the file the session is kept in
	// ("transcript_path"). Empty leaves the field out.
	TranscriptPath string
	// Cwd is the session's working directory at this moment ("cwd"). The
	// hooks start in it; when it is gone they start in the directory the
	// session started in, then the project's, then the user's home.
	Cwd string
	// PermissionMode is "default", "plan", "acceptEdits", "dontAsk" or
	// "bypassPermissions" ("permission_mode"); empty means "default". It
	// is given to hooks on PreToolUse, PostToolUse, PostToolUseFailure,
	// UserPromptSubmit, Stop, SubagentStop and PermissionRequest. A
	// sub-agent's events carry the mode of the agent that started it.
	PermissionMode string
	// AgentID and AgentType name the sub-agent an event belongs to
	// ("agent_id", "agent_type"). Both are empty for the main agent's
	// events.
	AgentID   string
	AgentType string

	// ToolName, ToolUseID and ToolInput describe a tool call, for
	// PreToolUse, PostToolUse, PostToolUseFailure and PermissionRequest
	// ("tool_name", "tool_use_id", "tool_input"). ToolName is what tool
	// matchers are tested against. ToolInput is one JSON object; empty
	// means {}. A call's Pre and Post hooks get the same ToolUseID.
	// PermissionRequest hooks are not given it on stdin, as in Claude
	// Code.
	ToolName  string
	ToolUseID string
	ToolInput json.RawMessage

	// ToolResponse is what the tool returned, for PostToolUse
	// ("tool_response"): any JSON value, in Claude Code's shape for the
	// tool where it has one, and always holding the text the model reads.
	ToolResponse json.RawMessage
	// Duration is how long the call took, for PostToolUse and
	// PostToolUseFailure ("duration_ms"). Zero leaves the field out.
	Duration time.Duration

	// Error is, for PostToolUseFailure, the error text the model reads
	// and, for StopFailure, the kind of error that ended the turn:
	// rate_limit, overloaded, authentication_failed, billing_error,
	// model_not_found, invalid_request, server_error or unknown ("error").
	// StopFailure matchers are tested against it.
	Error string
	// IsInterrupt says, for PostToolUseFailure, that the call failed
	// because the turn was cancelled ("is_interrupt").
	IsInterrupt bool
	// ErrorDetails says more about a StopFailure ("error_details").
	ErrorDetails string

	// Prompt is, for UserPromptSubmit, the prompt as the model will read
	// it: what was typed, then the text of anything pasted or attached as
	// text ("prompt"). Attachments lists the attached files' names
	// ("attachments").
	Prompt      string
	Attachments []string

	// Source says, for SessionStart, how the session comes to start:
	// startup, resume, clear, compact or fork ("source"); SessionStart
	// matchers are tested against it. For PreModelSwitch and
	// PostModelSwitch it is what asked for the switch (command), for
	// ConfigChange which settings file changed (user_settings,
	// project_settings, local_settings), and for DirectoryAdded what added the
	// directory (slash_command), which DirectoryAdded matchers are tested
	// against.
	Source string
	// Model names the model, for SessionStart ("model").
	Model string
	// EnvFile is, for SessionStart, CwdChanged and FileChanged, the path of the session's own
	// environment file. Hooks get it as GNOSH_ENV_FILE and
	// CLAUDE_ENV_FILE and may append export lines to it.
	EnvFile string

	// Reason says, for SessionEnd, why the session ends: clear, other or
	// prompt_input_exit ("reason"). SessionEnd matchers are tested
	// against it.
	Reason string

	// StopHookActive says, for Stop and SubagentStop, that the turn is
	// only still running because a Stop hook blocked its end
	// ("stop_hook_active"). A hook that wants to object once reads it.
	StopHookActive bool
	// LastAssistantMessage is the text the turn is about to end on, for
	// Stop and SubagentStop, or the error as shown, for StopFailure
	// ("last_assistant_message").
	LastAssistantMessage string

	// Trigger says, for PreCompact and PostCompact, what started the
	// summary: manual or auto ("trigger"). Their matchers are tested
	// against it.
	Trigger string
	// CustomInstructions is, for PreCompact, the text typed after
	// /compact; empty for an automatic summary ("custom_instructions").
	CustomInstructions string
	// CompactSummary is the summary, for PostCompact ("compact_summary").
	CompactSummary string

	// FilePath is also, for ConfigChange, the settings file that changed,
	// and for FileChanged the watched file (its name is what the matchers
	// are tested against); FileEvent is then what happened to it: change,
	// add or unlink ("event").
	FileEvent string
	//
	// FilePath, MemoryType, LoadReason, TriggerFilePath and
	// ParentFilePath describe a context file, for InstructionsLoaded
	// ("file_path", "memory_type", "load_reason", "trigger_file_path",
	// "parent_file_path"). MemoryType is User, Project or Local.
	// LoadReason is session_start, nested_traversal, include or compact,
	// and is what the matchers are tested against.
	FilePath        string
	MemoryType      string
	LoadReason      string
	TriggerFilePath string
	ParentFilePath  string

	// Message, Title and NotificationType describe a notification
	// ("message", "title", "notification_type"). NotificationType is
	// permission_prompt, idle_prompt, or push_notification (the model's
	// own, PushNotification), and is what the matchers are tested against.
	Message          string
	Title            string
	NotificationType string

	// FromModel, ToModel and RequestedModel are, for PreModelSwitch and
	// PostModelSwitch, the model before, the model after (their matchers
	// are tested against it), and the name it was asked for by
	// ("from_model", "to_model", "requested_model").
	FromModel, ToModel, RequestedModel string
	// ContextTokens is, for PreModelSwitch and PostModelSwitch, how many
	// tokens the conversation came to at the last count: what the next
	// request sends the new model ("context_tokens").
	ContextTokens int
	// Directory is, for DirectoryAdded, the directory added ("directory").
	Directory string
	// OldCwd and NewCwd are, for CwdChanged, the directory the shell was
	// in and the one it is in now ("old_cwd", "new_cwd").
	OldCwd, NewCwd string
	// ExpansionType, CommandName, CommandArgs and CommandSource are, for
	// UserPromptExpansion, how the line expanded (slash_command), the
	// command's name (what its matchers are held against), what was
	// typed after it, and where the command comes from (user, project);
	// Prompt is then the text it expands into.
	ExpansionType, CommandName, CommandArgs, CommandSource string

	// ToolCalls are, for PostToolBatch, every tool call of the batch, in
	// the order the model wrote them ("tool_calls").
	ToolCalls []BatchCall

	// MCPServerName, Mode, RequestedSchema, Action and Content are, for
	// Elicitation and ElicitationResult: the MCP server that asks (what
	// the matchers are tested against, "mcp_server_name"); the kind of
	// request, "form" ("mode"); the fields it asks for, as the JSON schema
	// it sent ("requested_schema", Elicitation); and the person's answer,
	// accept, decline or cancel ("action"), with the values when they
	// accepted ("content", ElicitationResult). Message is the server's
	// message (Elicitation).
	MCPServerName   string
	Mode            string
	RequestedSchema json.RawMessage
	Action          string
	Content         json.RawMessage

	// Extra holds further fields for stdin, by their JSON names, for
	// things Claude Code documents that this package has no field for
	// (such as "background_tasks" on Stop or "mcp_server" on a tool
	// event). Values must be JSON-encodable. A name this package writes
	// itself is not replaced.
	Extra map[string]any
}

// BatchCall is one tool call of a batch, for PostToolBatch: the call, and
// what the model reads of its result, as text ("tool_response"; Claude
// Code's is the tool_result content the model is sent).
type BatchCall struct {
	ToolName     string
	ToolUseID    string
	ToolInput    json.RawMessage
	ToolResponse string
}

// permissionModes are the values permission_mode can have.
var permissionModes = []string{"default", "plan", "acceptEdits", "dontAsk", "bypassPermissions"}

// subject returns what the event's matchers are tested against.
func (in *Input) subject() string {
	switch in.Event {
	case PreToolUse, PostToolUse, PostToolUseFailure, PermissionRequest:
		return in.ToolName
	case SessionStart:
		return in.Source
	case SubagentStart:
		return in.AgentType
	case StopFailure:
		return in.Error
	case PreCompact, PostCompact:
		return in.Trigger
	case SessionEnd:
		return in.Reason
	case InstructionsLoaded:
		return in.LoadReason
	case Notification:
		return in.NotificationType
	case PreModelSwitch, PostModelSwitch:
		return in.ToModel
	case DirectoryAdded:
		return in.Source
	case UserPromptExpansion:
		return in.CommandName
	case ConfigChange:
		return in.Source
	case FileChanged:
		return filepath.Base(in.FilePath)
	case Elicitation, ElicitationResult:
		return in.MCPServerName
	}
	return ""
}

// subjects returns every name the event's matchers may fit: the subject,
// and for a model switch also the model's own name without its provider,
// so that "qwen3.8-27b" fits qwen/qwen3.8-27b, as in Claude Code
// "claude-opus-5" fits every spelling of that model.
func (in *Input) subjects() []string {
	s := in.subject()
	if in.Event == PreModelSwitch || in.Event == PostModelSwitch {
		if _, id, ok := strings.Cut(s, "/"); ok && id != "" {
			return []string{s, id}
		}
	}
	return []string{s}
}

// check says what is wrong with the Input as the agent loop built it.
// These are mistakes in the program, not in a hook: an event given a
// value its matchers could never be written against would run the wrong
// hooks, or none, without a word.
func (in *Input) check(info eventInfo) error {
	if in.PermissionMode != "" && !slices.Contains(permissionModes, in.PermissionMode) {
		return fmt.Errorf("the permission mode %s is not one hooks are told: %s", quote(in.PermissionMode), strings.Join(permissionModes, ", "))
	}
	switch info.subject {
	case toolName:
		if in.ToolName == "" {
			return fmt.Errorf("the event has no tool name, which is what its hooks are matched against")
		}
		if raw := bytes.TrimSpace(in.ToolInput); len(raw) > 0 && (kindOf(raw) != "an object" || !json.Valid(raw)) {
			return fmt.Errorf("the tool input of %s is not one JSON object", in.ToolName)
		}
	case ownValue:
		if len(info.values) > 0 && !slices.Contains(info.values, in.subject()) {
			return fmt.Errorf("%s is %s, which is not one of the values its hooks are matched against: %s", info.what, quote(in.subject()), strings.Join(info.values, ", "))
		}
	}
	if raw := bytes.TrimSpace(in.ToolResponse); len(raw) > 0 && !json.Valid(raw) {
		return fmt.Errorf("the tool response of %s is not JSON", in.ToolName)
	}
	return nil
}

// payload builds the JSON object the event's hooks read on stdin. cwd is
// the working directory to report. Field names are Claude Code's.
func (in *Input) payload(info eventInfo, cwd string) ([]byte, error) {
	m := make(map[string]any, len(in.Extra)+12)
	maps.Copy(m, in.Extra)

	m["hook_event_name"] = string(in.Event)
	m["session_id"] = in.SessionID
	m["cwd"] = cwd
	set := func(name, value string) {
		if value != "" {
			m[name] = value
		} else {
			delete(m, name) // never Extra's word for a field of ours
		}
	}
	set("transcript_path", in.TranscriptPath)
	if info.mode {
		m["permission_mode"] = in.mode()
	} else {
		delete(m, "permission_mode")
	}
	set("agent_id", in.AgentID)
	set("agent_type", in.AgentType)

	if info.subject == toolName {
		input := json.RawMessage(bytes.TrimSpace(in.ToolInput))
		if len(input) == 0 {
			input = json.RawMessage("{}")
		}
		m["tool_name"] = in.ToolName
		m["tool_input"] = input
	}
	switch in.Event {
	case PreToolUse:
		m["tool_use_id"] = in.ToolUseID
	case PostToolUse:
		m["tool_use_id"] = in.ToolUseID
		if raw := bytes.TrimSpace(in.ToolResponse); len(raw) > 0 {
			m["tool_response"] = json.RawMessage(raw)
		}
		in.duration(m)
	case PostToolUseFailure:
		m["tool_use_id"] = in.ToolUseID
		m["error"] = in.Error
		m["is_interrupt"] = in.IsInterrupt
		in.duration(m)
	case UserPromptSubmit:
		m["prompt"] = in.Prompt
		m["attachments"] = append([]string{}, in.Attachments...)
	case SessionStart:
		m["source"] = in.Source
		set("model", in.Model)
	case SessionEnd:
		m["reason"] = in.Reason
	case Stop:
		m["stop_hook_active"] = in.StopHookActive
		m["last_assistant_message"] = in.LastAssistantMessage
	case SubagentStop:
		m["stop_hook_active"] = in.StopHookActive
		m["last_assistant_message"] = in.LastAssistantMessage
		m["agent_id"] = in.AgentID
		m["agent_type"] = in.AgentType
	case SubagentStart:
		m["agent_id"] = in.AgentID
		m["agent_type"] = in.AgentType
	case StopFailure:
		m["error"] = in.Error
		set("error_details", in.ErrorDetails)
		set("last_assistant_message", in.LastAssistantMessage)
	case PreCompact:
		m["trigger"] = in.Trigger
		m["custom_instructions"] = in.CustomInstructions
	case PostCompact:
		m["trigger"] = in.Trigger
		m["compact_summary"] = in.CompactSummary
	case InstructionsLoaded:
		m["file_path"] = in.FilePath
		m["memory_type"] = in.MemoryType
		m["load_reason"] = in.LoadReason
		set("trigger_file_path", in.TriggerFilePath)
		set("parent_file_path", in.ParentFilePath)
	case Notification:
		m["message"] = in.Message
		set("title", in.Title)
		m["notification_type"] = in.NotificationType
	case PreModelSwitch, PostModelSwitch:
		m["from_model"] = in.FromModel
		m["to_model"] = in.ToModel
		m["requested_model"] = in.RequestedModel
		m["source"] = in.Source
		m["context_tokens"] = in.ContextTokens
	case DirectoryAdded:
		m["directory"] = in.Directory
		m["source"] = in.Source
	case CwdChanged:
		m["old_cwd"] = in.OldCwd
		m["new_cwd"] = in.NewCwd
	case ConfigChange:
		m["source"] = in.Source
		set("file_path", in.FilePath)
	case FileChanged:
		m["file_path"] = in.FilePath
		m["event"] = in.FileEvent
	case PostToolBatch:
		calls := make([]map[string]any, 0, len(in.ToolCalls))
		for _, c := range in.ToolCalls {
			input := json.RawMessage(bytes.TrimSpace(c.ToolInput))
			if len(input) == 0 || !json.Valid(input) {
				input = json.RawMessage("{}")
			}
			calls = append(calls, map[string]any{"tool_name": c.ToolName, "tool_use_id": c.ToolUseID, "tool_input": input, "tool_response": c.ToolResponse})
		}
		m["tool_calls"] = calls
	case UserPromptExpansion:
		m["expansion_type"] = in.ExpansionType
		m["command_name"] = in.CommandName
		m["command_args"] = in.CommandArgs
		m["command_source"] = in.CommandSource
		m["prompt"] = in.Prompt
	case Elicitation:
		m["mcp_server_name"] = in.MCPServerName
		m["message"] = in.Message
		set("mode", in.Mode)
		if raw := bytes.TrimSpace(in.RequestedSchema); len(raw) > 0 && json.Valid(raw) {
			m["requested_schema"] = json.RawMessage(raw)
		}
	case ElicitationResult:
		m["mcp_server_name"] = in.MCPServerName
		m["action"] = in.Action
		set("mode", in.Mode)
		if raw := bytes.TrimSpace(in.Content); len(raw) > 0 && json.Valid(raw) {
			m["content"] = json.RawMessage(raw)
		}
	}

	// Claude Code writes this JSON without escaping <, > and &, and a hook
	// that searches its raw input for a redirect or an && must find it.
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(m); err != nil {
		return nil, fmt.Errorf("the event's details could not be written as JSON: %w", err)
	}
	return buf.Bytes(), nil
}

// mode returns the permission mode hooks are told.
func (in *Input) mode() string {
	if in.PermissionMode == "" {
		return "default"
	}
	return in.PermissionMode
}

// duration writes duration_ms when the call's length is known.
func (in *Input) duration(m map[string]any) {
	if in.Duration > 0 {
		m["duration_ms"] = in.Duration.Milliseconds()
	} else {
		delete(m, "duration_ms")
	}
}

// ownedEnv lists every variable this package sets for a hook. Each is
// taken out of the environment a hook inherits before the ones that apply
// are set, so that a hook never reads another event's value left over
// from a gnosh that is itself running inside a hook or a command.
// CLAUDECODE is taken out and never set: a gnosh started from inside
// Claude Code must not hand its hooks the mark of another program.
var ownedEnv = []string{
	"GNOSH", "AGENT", "AI_AGENT", "CLAUDECODE",
	"GNOSH_HOOK_EVENT_NAME", "GNOSH_SESSION_ID", "GNOSH_CWD",
	"GNOSH_PROJECT_DIR", "CLAUDE_PROJECT_DIR",
	"GNOSH_TOOL_NAME", "GNOSH_TOOL_USE_ID",
	"GNOSH_TOOL_INPUT_COMMAND", "GNOSH_TOOL_INPUT_FILE_PATH",
	"GNOSH_STOP_HOOK_ACTIVE",
	"GNOSH_AGENT_ID", "GNOSH_AGENT_TYPE",
	"GNOSH_SESSION_START_SOURCE", "GNOSH_SESSION_END_REASON",
	"GNOSH_PRE_COMPACT_TRIGGER", "GNOSH_POST_COMPACT_TRIGGER",
	"GNOSH_STOP_FAILURE_ERROR", "GNOSH_INSTRUCTIONS_LOAD_REASON",
	"GNOSH_NOTIFICATION_TYPE",
	"GNOSH_ENV_FILE", "CLAUDE_ENV_FILE",
	"GNOSH_MEM_STATUS",
	"PWD", "OLDPWD",
}

// environ builds the environment of the event's hooks: base, with this
// package's variables laid over it. dir is the directory the hooks start
// in. The result is sorted, so that it is the same every time.
func (in *Input) environ(info eventInfo, base []string, dir, projectDir, memStatus string) []string {
	vars := make(map[string]string, len(base)+16)
	for _, kv := range base {
		if name, value, ok := strings.Cut(kv, "="); ok && name != "" {
			vars[name] = value
		}
	}
	for _, name := range ownedEnv {
		delete(vars, name)
	}
	set := func(name, value string) {
		if value != "" {
			vars[name] = value
		}
	}
	vars["GNOSH"] = "1"
	vars["AGENT"] = "gnosh"
	vars["AI_AGENT"] = "gnosh"
	vars["GNOSH_HOOK_EVENT_NAME"] = string(in.Event)
	vars["GNOSH_SESSION_ID"] = in.SessionID
	vars["GNOSH_CWD"] = in.Cwd
	vars["GNOSH_PROJECT_DIR"] = projectDir
	vars["CLAUDE_PROJECT_DIR"] = projectDir
	vars["PWD"] = dir
	set("GNOSH_MEM_STATUS", memStatus)
	set("GNOSH_AGENT_ID", in.AgentID)
	set("GNOSH_AGENT_TYPE", in.AgentType)

	if info.subject == toolName {
		vars["GNOSH_TOOL_NAME"] = in.ToolName
		set("GNOSH_TOOL_USE_ID", in.ToolUseID)
		// For a shell call, the command; for a file tool, the path.
		var input struct {
			Command  *string `json:"command"`
			FilePath *string `json:"file_path"`
		}
		if json.Unmarshal(in.ToolInput, &input) == nil {
			if input.Command != nil {
				vars["GNOSH_TOOL_INPUT_COMMAND"] = *input.Command
			}
			if input.FilePath != nil {
				vars["GNOSH_TOOL_INPUT_FILE_PATH"] = *input.FilePath
			}
		}
	}
	switch in.Event {
	case Stop, SubagentStop:
		vars["GNOSH_STOP_HOOK_ACTIVE"] = "0"
		if in.StopHookActive {
			vars["GNOSH_STOP_HOOK_ACTIVE"] = "1"
		}
	case SessionStart:
		vars["GNOSH_SESSION_START_SOURCE"] = in.Source
		set("GNOSH_ENV_FILE", in.EnvFile)
		set("CLAUDE_ENV_FILE", in.EnvFile)
	case CwdChanged, FileChanged:
		set("GNOSH_ENV_FILE", in.EnvFile)
		set("CLAUDE_ENV_FILE", in.EnvFile)
	case SessionEnd:
		vars["GNOSH_SESSION_END_REASON"] = in.Reason
	case PreCompact:
		vars["GNOSH_PRE_COMPACT_TRIGGER"] = in.Trigger
	case PostCompact:
		vars["GNOSH_POST_COMPACT_TRIGGER"] = in.Trigger
	case StopFailure:
		vars["GNOSH_STOP_FAILURE_ERROR"] = in.Error
	case InstructionsLoaded:
		vars["GNOSH_INSTRUCTIONS_LOAD_REASON"] = in.LoadReason
	case Notification:
		vars["GNOSH_NOTIFICATION_TYPE"] = in.NotificationType
	}

	out := make([]string, 0, len(vars))
	for _, name := range slices.Sorted(maps.Keys(vars)) {
		out = append(out, name+"="+vars[name])
	}
	return out
}
