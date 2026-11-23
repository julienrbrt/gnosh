package hooks

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"
)

// stdinOf runs the event with one hook that saves its stdin, and returns
// what the hook read: as it was written, and decoded.
func stdinOf(t *testing.T, in Input) (string, map[string]any) {
	t.Helper()
	file := filepath.Join(t.TempDir(), "stdin.json")
	r := newRunner(t, options(t), Hook{Event: in.Event, Command: "cat > " + shq(file)})
	out := run(t, r, in)
	if len(out.Records) != 1 || out.Records[0].ExitCode != 0 {
		t.Fatalf("the hook that saves its stdin did not run cleanly: %+v", out.Records)
	}
	raw, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("stdin is not one JSON object: %v\n%s", err, raw)
	}
	return string(raw), got
}

// What each event's hooks read on stdin, field by field. The names are
// Claude Code's; the fields are the ones the two specifications list.
func TestStdinOfEveryEvent(t *testing.T) {
	cwd := t.TempDir()
	common := func(event Event) map[string]any {
		return map[string]any{
			"hook_event_name": string(event),
			"session_id":      "session-1",
			"transcript_path": "/transcripts/session-1.jsonl",
			"cwd":             cwd,
		}
	}
	with := func(base map[string]any, more map[string]any) map[string]any {
		for k, v := range more {
			base[k] = v
		}
		return base
	}
	bash := map[string]any{"command": "npm test 2>&1 | tee <log> && echo ok", "description": "Run the tests", "timeout": float64(120000), "run_in_background": false}
	bashRaw := json.RawMessage(`{"command": "npm test 2>&1 | tee <log> && echo ok", "description": "Run the tests", "timeout": 120000, "run_in_background": false}`)

	tests := []struct {
		name string
		in   Input
		want map[string]any
	}{
		{
			name: "PreToolUse",
			in:   Input{Event: PreToolUse, PermissionMode: "acceptEdits", ToolName: "Bash", ToolUseID: "toolu_01", ToolInput: bashRaw},
			want: with(common(PreToolUse), map[string]any{
				"permission_mode": "acceptEdits", "tool_name": "Bash", "tool_use_id": "toolu_01", "tool_input": bash,
			}),
		},
		{
			name: "PreToolUse of a sub-agent's call, with no input and no mode given",
			in:   Input{Event: PreToolUse, AgentID: "agent-7", AgentType: "Explore", ToolName: "Glob", ToolUseID: "toolu_02"},
			want: with(common(PreToolUse), map[string]any{
				"permission_mode": "default", "tool_name": "Glob", "tool_use_id": "toolu_02", "tool_input": map[string]any{},
				"agent_id": "agent-7", "agent_type": "Explore",
			}),
		},
		{
			name: "PostToolUse",
			in: Input{Event: PostToolUse, PermissionMode: "bypassPermissions", ToolName: "Write", ToolUseID: "toolu_03",
				ToolInput:    json.RawMessage(`{"file_path":"/project/a.go","content":"package a\n"}`),
				ToolResponse: json.RawMessage(`{"type":"create","filePath":"/project/a.go"}`), Duration: 1234 * time.Millisecond},
			want: with(common(PostToolUse), map[string]any{
				"permission_mode": "bypassPermissions", "tool_name": "Write", "tool_use_id": "toolu_03",
				"tool_input":    map[string]any{"file_path": "/project/a.go", "content": "package a\n"},
				"tool_response": map[string]any{"type": "create", "filePath": "/project/a.go"},
				"duration_ms":   float64(1234),
			}),
		},
		{
			name: "PostToolUseFailure",
			in: Input{Event: PostToolUseFailure, PermissionMode: "plan", ToolName: "Bash", ToolUseID: "toolu_04", ToolInput: bashRaw,
				Error: "Exit code 1\nnpm ERR! missing script: test", IsInterrupt: true, Duration: 15 * time.Millisecond},
			want: with(common(PostToolUseFailure), map[string]any{
				"permission_mode": "plan", "tool_name": "Bash", "tool_use_id": "toolu_04", "tool_input": bash,
				"error": "Exit code 1\nnpm ERR! missing script: test", "is_interrupt": true, "duration_ms": float64(15),
			}),
		},
		{
			name: "PostToolUseFailure with the call's length unknown",
			in:   Input{Event: PostToolUseFailure, ToolName: "Bash", ToolUseID: "toolu_05", ToolInput: bashRaw, Error: "Exit code 2"},
			want: with(common(PostToolUseFailure), map[string]any{
				"permission_mode": "default", "tool_name": "Bash", "tool_use_id": "toolu_05", "tool_input": bash,
				"error": "Exit code 2", "is_interrupt": false,
			}),
		},
		{
			name: "PermissionRequest",
			in:   Input{Event: PermissionRequest, PermissionMode: "dontAsk", ToolName: "Bash", ToolUseID: "toolu_06", ToolInput: bashRaw},
			want: with(common(PermissionRequest), map[string]any{
				// As in Claude Code, a permission request carries no
				// tool_use_id.
				"permission_mode": "dontAsk", "tool_name": "Bash", "tool_input": bash,
			}),
		},
		{
			name: "UserPromptSubmit",
			in:   Input{Event: UserPromptSubmit, Prompt: "fix the build\n\n<pasted text>", Attachments: []string{"paste-1.txt", "notes.md"}},
			want: with(common(UserPromptSubmit), map[string]any{
				"permission_mode": "default", "prompt": "fix the build\n\n<pasted text>", "attachments": []any{"paste-1.txt", "notes.md"},
			}),
		},
		{
			name: "UserPromptSubmit with nothing attached",
			in:   Input{Event: UserPromptSubmit, Prompt: "hello"},
			want: with(common(UserPromptSubmit), map[string]any{"permission_mode": "default", "prompt": "hello", "attachments": []any{}}),
		},
		{
			name: "SessionStart",
			in:   Input{Event: SessionStart, Source: "resume", Model: "qwen3.8-27b", PermissionMode: "plan"},
			want: with(common(SessionStart), map[string]any{"source": "resume", "model": "qwen3.8-27b"}),
		},
		{
			name: "SessionStart with no model known",
			in:   Input{Event: SessionStart, Source: "clear"},
			want: with(common(SessionStart), map[string]any{"source": "clear"}),
		},
		{
			name: "SessionEnd",
			in:   Input{Event: SessionEnd, Reason: "prompt_input_exit"},
			want: with(common(SessionEnd), map[string]any{"reason": "prompt_input_exit"}),
		},
		{
			name: "Stop",
			in:   Input{Event: Stop, PermissionMode: "bypassPermissions", StopHookActive: true, LastAssistantMessage: "The render is at out/final.mp4."},
			want: with(common(Stop), map[string]any{
				"permission_mode": "bypassPermissions", "stop_hook_active": true, "last_assistant_message": "The render is at out/final.mp4.",
			}),
		},
		{
			name: "Stop, the first time",
			in:   Input{Event: Stop},
			want: with(common(Stop), map[string]any{"permission_mode": "default", "stop_hook_active": false, "last_assistant_message": ""}),
		},
		{
			name: "SubagentStop",
			in:   Input{Event: SubagentStop, AgentID: "agent-7", AgentType: "Explore", StopHookActive: true, LastAssistantMessage: "Found it in a.go."},
			want: with(common(SubagentStop), map[string]any{
				"permission_mode": "default", "stop_hook_active": true, "last_assistant_message": "Found it in a.go.",
				"agent_id": "agent-7", "agent_type": "Explore",
			}),
		},
		{
			name: "SubagentStop of a helper with no type",
			in:   Input{Event: SubagentStop, AgentID: "agent-8"},
			want: with(common(SubagentStop), map[string]any{
				"permission_mode": "default", "stop_hook_active": false, "last_assistant_message": "", "agent_id": "agent-8", "agent_type": "",
			}),
		},
		{
			name: "SubagentStart",
			in:   Input{Event: SubagentStart, AgentID: "agent-7", AgentType: "general-purpose"},
			want: with(common(SubagentStart), map[string]any{"agent_id": "agent-7", "agent_type": "general-purpose"}),
		},
		{
			name: "StopFailure",
			in:   Input{Event: StopFailure, Error: "server_error", ErrorDetails: "the model server at 203.0.113.7 did not answer", LastAssistantMessage: "local: server_error"},
			want: with(common(StopFailure), map[string]any{
				"error": "server_error", "error_details": "the model server at 203.0.113.7 did not answer", "last_assistant_message": "local: server_error",
			}),
		},
		{
			name: "StopFailure with nothing more known",
			in:   Input{Event: StopFailure, Error: "unknown"},
			want: with(common(StopFailure), map[string]any{"error": "unknown"}),
		},
		{
			name: "PreCompact",
			in:   Input{Event: PreCompact, Trigger: "manual", CustomInstructions: "keep the test names"},
			want: with(common(PreCompact), map[string]any{"trigger": "manual", "custom_instructions": "keep the test names"}),
		},
		{
			name: "PreCompact, automatic",
			in:   Input{Event: PreCompact, Trigger: "auto"},
			want: with(common(PreCompact), map[string]any{"trigger": "auto", "custom_instructions": ""}),
		},
		{
			name: "PostCompact",
			in:   Input{Event: PostCompact, Trigger: "auto", CompactSummary: "We were fixing the build."},
			want: with(common(PostCompact), map[string]any{"trigger": "auto", "compact_summary": "We were fixing the build."}),
		},
		{
			name: "InstructionsLoaded",
			in: Input{Event: InstructionsLoaded, FilePath: "/project/docs/AGENTS.md", MemoryType: "Project", LoadReason: "include",
				TriggerFilePath: "/project/docs/a.md", ParentFilePath: "/project/AGENTS.md"},
			want: with(common(InstructionsLoaded), map[string]any{
				"file_path": "/project/docs/AGENTS.md", "memory_type": "Project", "load_reason": "include",
				"trigger_file_path": "/project/docs/a.md", "parent_file_path": "/project/AGENTS.md",
			}),
		},
		{
			name: "InstructionsLoaded at the start of a session",
			in:   Input{Event: InstructionsLoaded, FilePath: "/home/user/.config/gnosh/GNOSH.md", MemoryType: "User", LoadReason: "session_start"},
			want: with(common(InstructionsLoaded), map[string]any{
				"file_path": "/home/user/.config/gnosh/GNOSH.md", "memory_type": "User", "load_reason": "session_start",
			}),
		},
		{
			name: "Notification",
			in:   Input{Event: Notification, Message: "gnosh is waiting for your answer", Title: "Permission needed", NotificationType: "permission_prompt"},
			want: with(common(Notification), map[string]any{
				"message": "gnosh is waiting for your answer", "title": "Permission needed", "notification_type": "permission_prompt",
			}),
		},
		{
			name: "Notification with no title",
			in:   Input{Event: Notification, Message: "the turn finished a minute ago", NotificationType: "idle_prompt"},
			want: with(common(Notification), map[string]any{"message": "the turn finished a minute ago", "notification_type": "idle_prompt"}),
		},
		{
			name: "FileChanged",
			in:   Input{Event: FileChanged, FilePath: "/project/.envrc", FileEvent: "unlink", EnvFile: "/state/env.sh"},
			want: with(common(FileChanged), map[string]any{"file_path": "/project/.envrc", "event": "unlink"}),
		},
		{
			name: "PostToolBatch",
			in: Input{Event: PostToolBatch, ToolCalls: []BatchCall{
				{ToolName: "Read", ToolUseID: "toolu_01", ToolInput: json.RawMessage(`{"file_path":"/p/a.go"}`), ToolResponse: "1\tpackage a"},
				{ToolName: "Bash", ToolUseID: "toolu_02", ToolResponse: "Exit code 1"},
			}},
			want: with(common(PostToolBatch), map[string]any{"permission_mode": "default", "tool_calls": []any{
				map[string]any{"tool_name": "Read", "tool_use_id": "toolu_01", "tool_input": map[string]any{"file_path": "/p/a.go"}, "tool_response": "1\tpackage a"},
				map[string]any{"tool_name": "Bash", "tool_use_id": "toolu_02", "tool_input": map[string]any{}, "tool_response": "Exit code 1"},
			}}),
		},
		{
			name: "ConfigChange",
			in:   Input{Event: ConfigChange, Source: "project_settings", FilePath: "/project/gnosh.json"},
			want: with(common(ConfigChange), map[string]any{"source": "project_settings", "file_path": "/project/gnosh.json"}),
		},
		{
			name: "PreModelSwitch",
			in:   Input{Event: PreModelSwitch, FromModel: "local/a", ToModel: "local/b", RequestedModel: "b", Source: "command", ContextTokens: 182340},
			want: with(common(PreModelSwitch), map[string]any{"from_model": "local/a", "to_model": "local/b", "requested_model": "b", "source": "command", "context_tokens": 182340.0}),
		},
		{
			name: "PostModelSwitch",
			in:   Input{Event: PostModelSwitch, FromModel: "local/a", ToModel: "local/b", RequestedModel: "b", Source: "command"},
			want: with(common(PostModelSwitch), map[string]any{"from_model": "local/a", "to_model": "local/b", "requested_model": "b", "source": "command", "context_tokens": 0.0}),
		},
		{
			name: "DirectoryAdded",
			in:   Input{Event: DirectoryAdded, Directory: "/project/docs", Source: "slash_command"},
			want: with(common(DirectoryAdded), map[string]any{"directory": "/project/docs", "source": "slash_command"}),
		},
		{
			name: "CwdChanged",
			in:   Input{Event: CwdChanged, OldCwd: "/project", NewCwd: "/project/sub"},
			want: with(common(CwdChanged), map[string]any{"old_cwd": "/project", "new_cwd": "/project/sub"}),
		},
		{
			name: "UserPromptExpansion",
			in:   Input{Event: UserPromptExpansion, ExpansionType: "slash_command", CommandName: "deploy", CommandArgs: "staging", CommandSource: "project", Prompt: "Deploy to staging."},
			want: with(common(UserPromptExpansion), map[string]any{"permission_mode": "default", "expansion_type": "slash_command", "command_name": "deploy", "command_args": "staging", "command_source": "project", "prompt": "Deploy to staging."}),
		},
		{
			name: "Elicitation",
			in: Input{Event: Elicitation, MCPServerName: "issue-tracker", Message: "Which project?", Mode: "form",
				RequestedSchema: json.RawMessage(`{"type": "object", "properties": {"project_key": {"type": "string"}}}`)},
			want: with(common(Elicitation), map[string]any{"mcp_server_name": "issue-tracker", "message": "Which project?", "mode": "form",
				"requested_schema": map[string]any{"type": "object", "properties": map[string]any{"project_key": map[string]any{"type": "string"}}}}),
		},
		{
			name: "ElicitationResult",
			in:   Input{Event: ElicitationResult, MCPServerName: "issue-tracker", Action: "accept", Mode: "form", Content: json.RawMessage(`{"project_key":"WEB"}`)},
			want: with(common(ElicitationResult), map[string]any{"mcp_server_name": "issue-tracker", "action": "accept", "mode": "form", "content": map[string]any{"project_key": "WEB"}}),
		},
	}
	seen := map[Event]bool{}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.in.SessionID = "session-1"
			tt.in.TranscriptPath = "/transcripts/session-1.jsonl"
			tt.in.Cwd = cwd
			_, got := stdinOf(t, tt.in)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("stdin:\n got %s\nwant %s", pretty(got), pretty(tt.want))
			}
		})
		seen[tt.in.Event] = true
	}
	for _, e := range Events() {
		if !seen[e] {
			t.Errorf("no test of what %s hooks read on stdin", e)
		}
	}
}

func pretty(v any) string {
	b, _ := json.MarshalIndent(v, "", "  ")
	return string(b)
}

// The JSON is written as Claude Code writes it: compact, and with <, >
// and & as themselves, so that a hook that searches its raw input finds
// them.
func TestStdinIsWrittenPlainly(t *testing.T) {
	in := input(PreToolUse)
	in.ToolInput = json.RawMessage(`{ "command" : "make > build.log && echo <done>" }`)
	raw, _ := stdinOf(t, in)
	contains(t, "stdin", raw, `"tool_name":"Bash"`, `"tool_input":{"command":"make > build.log && echo <done>"}`)
	// A JSON writer that escaped them would write backslash-u-003e and the like.
	lacks(t, "stdin", raw, `\`+"u003e", `\`+"u0026", `\`+"u003c")
	if strings.Count(strings.TrimSpace(raw), "\n") != 0 {
		t.Errorf("stdin is spread over several lines:\n%s", raw)
	}
}

// Extra adds fields this package has none for, and cannot replace or
// forge one of its own.
func TestStdinExtraFields(t *testing.T) {
	in := input(Stop)
	in.Extra = map[string]any{
		"background_tasks": []map[string]any{{"id": "bash_1", "type": "shell", "status": "running"}},
		"prompt_id":        "550e8400-e29b-41d4-a716-446655440000",
		"session_id":       "forged",
		"hook_event_name":  "PreToolUse",
		"agent_id":         "forged",
		"cwd":              "/forged",
		"transcript_path":  "/forged",
	}
	in.TranscriptPath = ""
	_, got := stdinOf(t, in)
	if got["session_id"] != "session-1" || got["hook_event_name"] != "Stop" || got["cwd"] == "/forged" {
		t.Errorf("Extra replaced a field of the package's own: %s", pretty(got))
	}
	for _, name := range []string{"agent_id", "transcript_path"} {
		if _, ok := got[name]; ok {
			t.Errorf("Extra put in %q, which this event's input does not carry here: %s", name, pretty(got))
		}
	}
	if got["prompt_id"] != "550e8400-e29b-41d4-a716-446655440000" {
		t.Errorf("prompt_id is %v", got["prompt_id"])
	}
	tasks, _ := got["background_tasks"].([]any)
	if len(tasks) != 1 {
		t.Errorf("background_tasks is %v", got["background_tasks"])
	}

	// A value that cannot be written as JSON is the caller's mistake, and
	// no hook runs on half an event.
	in.Extra = map[string]any{"bad": func() {}}
	r := newRunner(t, options(t), Hook{Event: Stop, Command: "true"})
	out, err := r.Run(context.Background(), in)
	if err == nil || len(out.Records) != 0 {
		t.Errorf("Run = %+v, %v; want an error and no hook run", out, err)
	}
}

// envOf runs the event with one hook that saves its environment, and
// returns the variables this package is answerable for: everything named
// GNOSH*, CLAUDE*, AGENT, AI_AGENT, PWD and OLDPWD.
func envOf(t *testing.T, opts Options, in Input) map[string]string {
	t.Helper()
	file := filepath.Join(t.TempDir(), "env")
	r := newRunner(t, opts, Hook{Event: in.Event, Command: "env > " + shq(file)})
	out := run(t, r, in)
	if len(out.Records) != 1 || out.Records[0].ExitCode != 0 {
		t.Fatalf("the hook that saves its environment did not run cleanly: %+v", out.Records)
	}
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		name, value, _ := strings.Cut(line, "=")
		if strings.HasPrefix(name, "GNOSH") || strings.HasPrefix(name, "CLAUDE") || name == "AGENT" || name == "AI_AGENT" || name == "PWD" || name == "OLDPWD" {
			got[name] = value
		}
	}
	delete(got, strings.SplitN(marker, "=", 2)[0])
	return got
}

// The environment each event's hooks are given: exactly the documented
// variables, and none left over from another event or from gnosh's own
// environment.
func TestEnvironmentOfEveryEvent(t *testing.T) {
	opts := options(t)
	project := opts.ProjectDir
	cwd := t.TempDir()
	opts.MemStatus = "/project/.gnosh/mem/status.json"
	base := opts.Environ()
	opts.Environ = func() []string {
		return append(append([]string{}, base...),
			// What a gnosh that is itself running inside a hook, or
			// inside a command of another harness, would hand down.
			"GNOSH=0", "AGENT=another-harness", "AI_AGENT=another-harness",
			"GNOSH_HOOK_EVENT_NAME=Stop", "GNOSH_TOOL_NAME=stale", "GNOSH_TOOL_USE_ID=stale",
			"GNOSH_TOOL_INPUT_COMMAND=stale", "GNOSH_TOOL_INPUT_FILE_PATH=stale",
			"GNOSH_STOP_HOOK_ACTIVE=1", "GNOSH_AGENT_ID=stale", "GNOSH_AGENT_TYPE=stale",
			"GNOSH_SESSION_START_SOURCE=stale", "GNOSH_SESSION_END_REASON=stale",
			"GNOSH_PRE_COMPACT_TRIGGER=stale", "GNOSH_POST_COMPACT_TRIGGER=stale",
			"GNOSH_STOP_FAILURE_ERROR=stale", "GNOSH_INSTRUCTIONS_LOAD_REASON=stale",
			"GNOSH_NOTIFICATION_TYPE=stale", "GNOSH_ENV_FILE=/stale", "CLAUDE_ENV_FILE=/stale",
			"CLAUDE_PROJECT_DIR=/stale", "GNOSH_PROJECT_DIR=/stale", "GNOSH_MEM_STATUS=/stale",
			"PWD=/stale", "OLDPWD=/stale", "CLAUDECODE=1",
			// A setting of the user's that is not this package's to touch.
			"GNOSH_DEBUG_PAGE=1",
		)
	}
	always := func(event Event) map[string]string {
		return map[string]string{
			"GNOSH": "1", "AGENT": "gnosh", "AI_AGENT": "gnosh",
			"GNOSH_HOOK_EVENT_NAME": string(event), "GNOSH_SESSION_ID": "session-1", "GNOSH_CWD": cwd,
			"GNOSH_PROJECT_DIR": project, "CLAUDE_PROJECT_DIR": project,
			"GNOSH_MEM_STATUS": "/project/.gnosh/mem/status.json",
			"GNOSH_DEBUG_PAGE": "1",
			"PWD":              cwd,
		}
	}
	with := func(base map[string]string, more ...string) map[string]string {
		for i := 0; i < len(more); i += 2 {
			base[more[i]] = more[i+1]
		}
		return base
	}
	shell := json.RawMessage(`{"command":"git push --force origin main","description":"Push"}`)
	file := json.RawMessage(`{"file_path":"/project/src/main.go","old_string":"a","new_string":"b"}`)

	tests := []struct {
		name string
		in   Input
		want map[string]string
	}{
		{"PreToolUse of a shell call", Input{Event: PreToolUse, ToolName: "Bash", ToolUseID: "toolu_1", ToolInput: shell},
			with(always(PreToolUse), "GNOSH_TOOL_NAME", "Bash", "GNOSH_TOOL_USE_ID", "toolu_1", "GNOSH_TOOL_INPUT_COMMAND", "git push --force origin main")},
		{"PreToolUse of a file tool, in a sub-agent", Input{Event: PreToolUse, ToolName: "Edit", ToolUseID: "toolu_2", ToolInput: file, AgentID: "agent-7", AgentType: "Explore"},
			with(always(PreToolUse), "GNOSH_TOOL_NAME", "Edit", "GNOSH_TOOL_USE_ID", "toolu_2", "GNOSH_TOOL_INPUT_FILE_PATH", "/project/src/main.go", "GNOSH_AGENT_ID", "agent-7", "GNOSH_AGENT_TYPE", "Explore")},
		{"PreToolUse of a tool with neither", Input{Event: PreToolUse, ToolName: "Grep", ToolUseID: "toolu_3", ToolInput: json.RawMessage(`{"pattern":"x"}`)},
			with(always(PreToolUse), "GNOSH_TOOL_NAME", "Grep", "GNOSH_TOOL_USE_ID", "toolu_3")},
		{"PostToolUse", Input{Event: PostToolUse, ToolName: "Bash", ToolUseID: "toolu_1", ToolInput: shell},
			with(always(PostToolUse), "GNOSH_TOOL_NAME", "Bash", "GNOSH_TOOL_USE_ID", "toolu_1", "GNOSH_TOOL_INPUT_COMMAND", "git push --force origin main")},
		{"PostToolUseFailure", Input{Event: PostToolUseFailure, ToolName: "Edit", ToolUseID: "toolu_2", ToolInput: file},
			with(always(PostToolUseFailure), "GNOSH_TOOL_NAME", "Edit", "GNOSH_TOOL_USE_ID", "toolu_2", "GNOSH_TOOL_INPUT_FILE_PATH", "/project/src/main.go")},
		{"PermissionRequest", Input{Event: PermissionRequest, ToolName: "Bash", ToolInput: shell},
			with(always(PermissionRequest), "GNOSH_TOOL_NAME", "Bash", "GNOSH_TOOL_INPUT_COMMAND", "git push --force origin main")},
		{"UserPromptSubmit", Input{Event: UserPromptSubmit, Prompt: "hi"}, always(UserPromptSubmit)},
		{"SessionStart", Input{Event: SessionStart, Source: "startup", EnvFile: "/state/gnosh/env/session-1.sh"},
			with(always(SessionStart), "GNOSH_SESSION_START_SOURCE", "startup", "GNOSH_ENV_FILE", "/state/gnosh/env/session-1.sh", "CLAUDE_ENV_FILE", "/state/gnosh/env/session-1.sh")},
		{"SessionStart with no environment file", Input{Event: SessionStart, Source: "compact"},
			with(always(SessionStart), "GNOSH_SESSION_START_SOURCE", "compact")},
		{"SessionEnd", Input{Event: SessionEnd, Reason: "clear"}, with(always(SessionEnd), "GNOSH_SESSION_END_REASON", "clear")},
		{"Stop, the first time", Input{Event: Stop}, with(always(Stop), "GNOSH_STOP_HOOK_ACTIVE", "0")},
		{"Stop, held open by a hook", Input{Event: Stop, StopHookActive: true}, with(always(Stop), "GNOSH_STOP_HOOK_ACTIVE", "1")},
		{"SubagentStop", Input{Event: SubagentStop, AgentID: "agent-7", AgentType: "Explore", StopHookActive: true},
			with(always(SubagentStop), "GNOSH_STOP_HOOK_ACTIVE", "1", "GNOSH_AGENT_ID", "agent-7", "GNOSH_AGENT_TYPE", "Explore")},
		{"SubagentStart", Input{Event: SubagentStart, AgentID: "agent-7", AgentType: "Explore"},
			with(always(SubagentStart), "GNOSH_AGENT_ID", "agent-7", "GNOSH_AGENT_TYPE", "Explore")},
		{"StopFailure", Input{Event: StopFailure, Error: "overloaded"}, with(always(StopFailure), "GNOSH_STOP_FAILURE_ERROR", "overloaded")},
		{"PreCompact", Input{Event: PreCompact, Trigger: "auto"}, with(always(PreCompact), "GNOSH_PRE_COMPACT_TRIGGER", "auto")},
		{"PostCompact", Input{Event: PostCompact, Trigger: "manual"}, with(always(PostCompact), "GNOSH_POST_COMPACT_TRIGGER", "manual")},
		{"InstructionsLoaded", Input{Event: InstructionsLoaded, LoadReason: "nested_traversal", FilePath: "/project/sub/AGENTS.md"},
			with(always(InstructionsLoaded), "GNOSH_INSTRUCTIONS_LOAD_REASON", "nested_traversal")},
		{"Notification", Input{Event: Notification, NotificationType: "idle_prompt", Message: "idle"},
			with(always(Notification), "GNOSH_NOTIFICATION_TYPE", "idle_prompt")},
		{"PreModelSwitch", Input{Event: PreModelSwitch, FromModel: "local/a", ToModel: "local/b", Source: "command"}, always(PreModelSwitch)},
		{"ConfigChange", Input{Event: ConfigChange, Source: "user_settings", FilePath: "/home/gnosh.json"}, always(ConfigChange)},
		{"PostToolBatch", Input{Event: PostToolBatch}, always(PostToolBatch)},
		{"FileChanged", Input{Event: FileChanged, FilePath: "/project/.envrc", FileEvent: "change", EnvFile: "/state/gnosh/env/session-1.sh"},
			with(always(FileChanged), "GNOSH_ENV_FILE", "/state/gnosh/env/session-1.sh", "CLAUDE_ENV_FILE", "/state/gnosh/env/session-1.sh")},
		{"PostModelSwitch", Input{Event: PostModelSwitch, FromModel: "local/a", ToModel: "local/b", Source: "command"}, always(PostModelSwitch)},
		{"DirectoryAdded", Input{Event: DirectoryAdded, Directory: "/project/docs", Source: "slash_command"}, always(DirectoryAdded)},
		{"UserPromptExpansion", Input{Event: UserPromptExpansion, ExpansionType: "slash_command", CommandName: "deploy", Prompt: "x"}, always(UserPromptExpansion)},
		{"Elicitation", Input{Event: Elicitation, MCPServerName: "issue-tracker", Message: "Which project?"}, always(Elicitation)},
		{"ElicitationResult", Input{Event: ElicitationResult, MCPServerName: "issue-tracker", Action: "decline"}, always(ElicitationResult)},
		{"CwdChanged", Input{Event: CwdChanged, OldCwd: "/project", NewCwd: "/project/sub", EnvFile: "/state/gnosh/env/session-1.sh"},
			with(always(CwdChanged), "GNOSH_ENV_FILE", "/state/gnosh/env/session-1.sh", "CLAUDE_ENV_FILE", "/state/gnosh/env/session-1.sh")},
	}
	seen := map[Event]bool{}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.in.SessionID = "session-1"
			tt.in.Cwd = cwd
			got := envOf(t, opts, tt.in)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("environment:\n got %s\nwant %s", sortedEnv(got), sortedEnv(tt.want))
			}
		})
		seen[tt.in.Event] = true
	}
	for _, e := range Events() {
		if !seen[e] {
			t.Errorf("no test of the environment %s hooks are given", e)
		}
	}
}

func sortedEnv(m map[string]string) string {
	var lines []string
	for k, v := range m {
		lines = append(lines, k+"="+v)
	}
	sort.Strings(lines)
	return strings.Join(lines, "\n     ")
}

// A hook written for Claude Code names its script through
// CLAUDE_PROJECT_DIR. The shell fills it in, so a directory with spaces
// or shell characters in its name is a name and nothing more.
func TestCommandsFindTheProjectThroughItsVariable(t *testing.T) {
	opts := options(t)
	opts.ProjectDir = filepath.Join(t.TempDir(), "my project; echo pwned >&2; exit 2 #")
	hookDir := filepath.Join(opts.ProjectDir, ".claude", "hooks")
	if err := os.MkdirAll(hookDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(hookDir, "check.sh"), []byte("echo checked by the script of the project >&2\nexit 2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, command := range []string{
		`sh "$CLAUDE_PROJECT_DIR"/.claude/hooks/check.sh`,
		`sh "${CLAUDE_PROJECT_DIR}/.claude/hooks/check.sh"`,
		`sh "$GNOSH_PROJECT_DIR/.claude/hooks/check.sh"`,
	} {
		r := newRunner(t, opts, Hook{Event: Stop, Command: command})
		out := run(t, r, input(Stop))
		if !out.Blocked || out.Reason != "checked by the script of the project" {
			t.Errorf("%s: Blocked %v, Reason %q", command, out.Blocked, out.Reason)
		}
	}
}

// The hooks start in the session's working directory, so a relative
// command is found from there, and in the next best place when that
// directory is gone.
func TestHooksStartInTheWorkingDirectory(t *testing.T) {
	opts := options(t)
	real := func(dir string) string {
		t.Helper()
		resolved, err := filepath.EvalSymlinks(dir)
		if err != nil {
			t.Fatal(err)
		}
		return resolved
	}
	cwd := real(t.TempDir())
	opts.StartDir = real(t.TempDir())
	opts.ProjectDir = real(t.TempDir())
	if err := os.WriteFile(filepath.Join(cwd, "where.sh"), []byte("pwd -P >&2; exit 2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r := newRunner(t, opts, Hook{Event: Stop, Command: "sh ./where.sh"})
	in := input(Stop)
	in.Cwd = cwd
	if out := run(t, r, in); out.Reason != cwd {
		t.Errorf("a relative command ran in %q, want the working directory %q", out.Reason, cwd)
	}

	where := newRunner(t, opts, Hook{Event: Stop, Command: `pwd -P >&2; echo "$PWD" >&2; exit 2`})
	gone := filepath.Join(cwd, "removed")
	tests := []struct {
		name     string
		cwd      string
		startDir string
		want     string
	}{
		{"the working directory", cwd, opts.StartDir, cwd},
		{"a working directory that is gone", gone, opts.StartDir, opts.StartDir},
		{"no working directory given", "", opts.StartDir, opts.StartDir},
	}
	for _, tt := range tests {
		in := input(Stop)
		in.Cwd = tt.cwd
		out := run(t, where, in)
		if out.Reason != tt.want+"\n"+tt.want {
			t.Errorf("%s: the hook started in %q, want %q", tt.name, out.Reason, tt.want)
		}
	}
	// With the starting directory gone too, the project's.
	opts.StartDir = gone
	r = newRunner(t, opts, Hook{Event: Stop, Command: "pwd -P >&2; exit 2"})
	in = input(Stop)
	in.Cwd = gone
	if out := run(t, r, in); out.Reason != opts.ProjectDir {
		t.Errorf("the hook started in %q, want the project's directory %q", out.Reason, opts.ProjectDir)
	}
	// The hook is still told the directory the session is in.
	_, got := stdinOf(t, in)
	if got["cwd"] != gone {
		t.Errorf("cwd on stdin is %v, want %q", got["cwd"], gone)
	}
}

// An Input the agent loop built wrongly is an error, not a quiet run of
// the wrong hooks. Nothing runs, and a tool call or a prompt is not let
// through.
func TestRunRefusesAWrongInput(t *testing.T) {
	dir := t.TempDir()
	ranFile := filepath.Join(dir, "ran")
	var hooks []Hook
	opts := options(t)
	opts.Fires = nil
	for _, e := range Events() {
		if e != Notification {
			// (The Notification hook has to go for NewRunner to take the
			// rest.)
			hooks = append(hooks, Hook{Event: e, Command: "echo ran >> " + shq(ranFile)})
			opts.Fires = append(opts.Fires, e)
		}
	}
	r := newRunner(t, opts, hooks...)

	tests := []struct {
		name string
		in   Input
		want []string
	}{
		{"an event that is not one", Input{Event: "PreTool"}, []string{`"PreTool" is not a hook event`}},
		{"an event in lower case", Input{Event: "stop"}, []string{`"stop" is not a hook event`, "Stop"}},
		{"no event", Input{}, []string{"names no event"}},
		{"an event the program said it does not fire", Input{Event: Notification, NotificationType: "idle_prompt"}, []string{"does not fire Notification", "Options.Fires"}},
		{"a tool event with no tool", Input{Event: PreToolUse}, []string{"PreToolUse", "no tool name"}},
		{"a tool input that is a list", Input{Event: PreToolUse, ToolName: "Bash", ToolInput: json.RawMessage(`["ls"]`)}, []string{"tool input of Bash is not one JSON object"}},
		{"a tool input that is not JSON", Input{Event: PostToolUse, ToolName: "Bash", ToolInput: json.RawMessage(`{"command": `)}, []string{"tool input of Bash is not one JSON object"}},
		{"a tool response that is not JSON", Input{Event: PostToolUse, ToolName: "Bash", ToolResponse: json.RawMessage(`{oops`)}, []string{"tool response of Bash is not JSON"}},
		{"a permission mode nobody has", Input{Event: PreToolUse, ToolName: "Bash", PermissionMode: "auto"}, []string{`"auto"`, "bypassPermissions"}},
		{"a session source nobody has", Input{Event: SessionStart, Source: "restart"}, []string{"the source", `"restart"`, "startup, resume, clear, compact, fork"}},
		{"no session source", Input{Event: SessionStart}, []string{"the source", `""`}},
		{"an end reason nobody has", Input{Event: SessionEnd, Reason: "quit"}, []string{"the reason", `"quit"`, "prompt_input_exit"}},
		{"a trigger nobody has", Input{Event: PreCompact, Trigger: "automatic"}, []string{"the trigger", `"automatic"`, "manual, auto"}},
		{"an error kind as the provider names it", Input{Event: StopFailure, Error: "server"}, []string{"the kind of error", `"server"`, "server_error"}},
		{"a load reason nobody has", Input{Event: InstructionsLoaded, LoadReason: "startup"}, []string{`"startup"`, "session_start"}},
	}
	for _, tt := range tests {
		out, err := r.Run(context.Background(), tt.in)
		if err == nil {
			t.Errorf("%s: Run returned no error", tt.name)
			continue
		}
		contains(t, tt.name, err.Error(), tt.want...)
		if len(out.Records) != 0 {
			t.Errorf("%s: hooks were run: %+v", tt.name, out.Records)
		}
		if tt.in.Event == PreToolUse && (!out.Blocked || out.PermissionDecision != Deny || out.Reason == "") {
			t.Errorf("%s: the Outcome lets the call through: %+v", tt.name, out)
		}
	}
	// And for a prompt.
	notFired := newRunner(t, Options{Fires: []Event{Stop}, ProjectDir: dir})
	out, err := notFired.Run(context.Background(), Input{Event: UserPromptSubmit, Prompt: "hi"})
	if err == nil || !out.Blocked || out.Reason == "" {
		t.Errorf("a prompt on a Runner that does not fire it: %+v, %v", out, err)
	}
	if _, err := os.Stat(ranFile); err == nil {
		t.Error("a hook ran on a wrong Input")
	}
}

// With no hook for the event, or none that fits, Run does nothing and
// says nothing.
func TestRunWithNothingToRun(t *testing.T) {
	r := newRunner(t, options(t), Hook{Event: PreToolUse, Matcher: "Edit", Command: "exit 2"})
	for _, event := range Events() {
		out, err := r.Run(context.Background(), input(event))
		if err != nil || !reflect.DeepEqual(out, Outcome{}) {
			t.Errorf("%s: Run = %+v, %v; want the zero Outcome", event, out, err)
		}
	}
	empty, err := NewRunner(Config{}, Options{Fires: Events()})
	if err != nil {
		t.Fatal(err)
	}
	if out, err := empty.Run(context.Background(), input(Stop)); err != nil || !reflect.DeepEqual(out, Outcome{}) {
		t.Errorf("an empty configuration: Run = %+v, %v", out, err)
	}
}
