# output-formats.md — what `-p` prints: text, json, stream-json

What this file is: the exact shapes a script or benchmark sees on stdout when
it runs `gnosh -p`, copied from Claude Code so existing scripts and `jq`
one-liners keep working.

In one paragraph: `text` prints the final answer and nothing else. `json`
prints one JSON object when the run is over — the *result object* — with the
answer in `result`, the session ID in `session_id`, and counts, timings and
cost. `stream-json` prints one JSON object per line while the run happens: an
`init` line first, then every assistant and user (tool-result) message, and
the same result object as the last line. Scripts lean on four fields:
`.type`, `.subtype`, `.result`, `.session_id`. Get those right first.

Source: code.claude.com/docs/en — headless, cli-reference, agent-sdk/typescript
("Message Types"), agent-sdk/streaming-output, agent-sdk/agent-loop,
agent-sdk/streaming-vs-single-mode; docs version 2.1.323, read 2026-11-13.
The `message` objects inside `assistant`/`user` lines follow Anthropic's
public Messages API. "(unverified)" = the docs do not say.

---

## 1. `--output-format text` (default)

- stdout: the final response text of the run.
- If the run fails inside the loop (missing credentials, for instance), the
  failure text is printed as the result on stdout and the exit code is
  non-zero.
- When background work starts another turn after the first has finished,
  each turn's result is printed (v2.1.295+; before that, only the last).
- Whether a trailing newline is added is (unverified); gnosh prints the text
  followed by one newline.
- Nothing else goes to stdout. Warnings ("Waiting for background work to
  finish…", skipped MCP servers) go to stderr, and only when stderr is a
  terminal.

## 2. `--output-format json`

stdout carries **one JSON object: the result object** of section 4.5, printed
when the run ends. With background follow-up turns, the last turn's result is
the one printed.

Documented uses that must keep working:

```bash
gnosh -p "Summarize this project" --output-format json | jq -r '.result'
session_id=$(gnosh -p "Start a review" --output-format json | jq -r '.session_id')
gnosh -p "..." --output-format json --json-schema '{...}' | jq '.structured_output'
```

- `total_cost_usd` and the per-model breakdown `modelUsage` are present; both
  are client-side estimates. On `-c`/`-r` the totals cover the whole
  conversation, earlier runs included.
- `structured_output` is present only with `--json-schema` and only when the
  run produced valid structured output; consumers must check for it.
- Whether `--verbose` changes `json` output (for example into an array of all
  messages) is (unverified). gnosh: `json` always prints the single result
  object.

## 3. `--output-format stream-json`

Newline-delimited JSON: one complete JSON object per line, no pretty
printing, flushed as each event happens. The last line is the result object.

- Every line has a string `type`. `system` lines also have `subtype`.
- Almost every line has `uuid` (a unique ID for that line) and `session_id`.
- Consumers are told to ignore types, subtypes and fields they do not know.
  gnosh may therefore add its own, and may omit optional ones.
- A slow reader: the process waits for queued output to drain before exiting,
  up to 30 seconds.

### 3.1 Line types

| `type` | `subtype` | When | gnosh |
| :- | :- | :- | :- |
| `system` | `init` | First line of the stream (startup hook events may precede it). Session metadata. | P0 |
| `assistant` | — | One per completed content block of a model response. | P0 |
| `user` | — | Tool results fed back to the model; with `--replay-user-messages`, also the user's own messages. | P0 |
| `result` | `success`, `error_max_turns`, `error_during_execution`, `error_max_budget_usd`, `error_max_structured_output_retries` | Last line of each turn's run. | P0 |
| `stream_event` | — | Token-level deltas; only with `--include-partial-messages`. | P1 |
| `system` | `compact_boundary` | The conversation was compacted. | P1 |
| `system` | `status` | Status change, e.g. compacting. | P1 |
| `system` | `api_retry` | A model request failed and will be retried. | P1 |
| `system` | `informational` | A plain-text notice or warning from the loop; hook feedback. | P1 |
| `system` | `permission_denied` | A tool call was denied without a prompt. | P1 |
| `system` | `task_started`, `task_progress`, `task_updated`, `task_notification`, `background_tasks_changed` | Background shell commands and sub-agents. | P1 |
| `tool_progress` | — | Heartbeat every 30 s while a tool call runs. | P1 |
| `system` | `hook_started`, `hook_progress`, `hook_response` | Hook lifecycle (always for `SessionStart`/`Setup`; others need `--include-hook-events`). | P2 |
| `system` | `thinking_tokens` | Running estimate while a thinking block is produced. | P2 |
| `system` | `session_state_changed` | `idle` / `running` / `requires_action`; only when `CLAUDE_CODE_EMIT_SESSION_STATE_EVENTS=1`. | P2 |
| `system` | `commands_changed` | The set of slash commands changed mid-session. | P2 |
| `conversation_reset` | — | `/clear` replaced the conversation. | P2 |
| `tool_use_summary` | — | A short summary of preceding tool calls. | P2 |
| `system` | `plugin_install`, `files_persisted`, `worker_shutting_down`, `control_request_progress`, `model_refusal_fallback`, `model_refusal_no_fallback` | Anthropic-side features. | not adopted |
| `auth_status`, `rate_limit_event`, `prompt_suggestion` | — | Anthropic account features. | not adopted |

### 3.2 Order of lines in a simple run

```text
system/init
assistant            (text block: "I'll look at the file.")
assistant            (tool_use block: Read)        same message.id as above
user                 (tool_result for that tool_use)
assistant            (text block: the final answer)
result/success
```

With `--include-partial-messages`, raw API stream events are interleaved, and
each `assistant` line arrives just before that block's `content_block_stop`:

```text
stream_event (message_start)
stream_event (content_block_start)   text block
stream_event (content_block_delta)   text chunks…
assistant                            the complete text block
stream_event (content_block_stop)
stream_event (content_block_start)   tool_use block
stream_event (content_block_delta)   input_json_delta chunks…
assistant                            the complete tool_use block
stream_event (content_block_stop)
stream_event (message_delta)
stream_event (message_stop)
…tool runs…  user (tool_result)  …next response…
result
```

One model response with a text block and a tool call therefore yields **two**
`assistant` lines, each carrying only its own content block, sharing
`message.id`.

## 4. Line shapes

Types below use TypeScript notation as the docs do; `?` = optional.

### 4.1 `system` / `init`

```typescript
{
  type: "system";
  subtype: "init";
  uuid: string;
  session_id: string;
  cwd: string;
  model: string;
  tools: string[];                 // tool names available to the model
  mcp_servers: { name: string; status: string; source?: string }[];
  permissionMode: "default" | "acceptEdits" | "bypassPermissions" | "plan" | "dontAsk" | "auto";
  slash_commands: string[];
  terminal_slash_commands?: string[]; // subset bound to the local terminal, e.g. "exit"
  output_style: string;
  skills: string[];
  agents?: string[];
  plugins: { name: string; path: string }[];
  plugin_errors?: { plugin: string; type: string; message: string; path?: string }[];
  mcp_server_errors?: { name: string; type: string; message: string }[]; // --mcp-config entries skipped
  apiKeySource: string;            // "ANTHROPIC_API_KEY" | "apiKeyHelper" | "/login managed key" | "none"
  claude_code_version: string;
  betas?: string[];
  capabilities?: string[];         // open set, for feature detection
  fast_mode_state?: "on" | "off" | "cooldown";
  fast_mode_disabled_reason?: string;
}
```

Notes:

- `mcp_servers[].status` is one of `connected`, `failed`, `needs-auth`,
  `pending`, `disabled`.
- `tools` includes MCP tools, and lists the sub-agent tool under its old name
  `Task` (the docs say to map names through the legacy-alias table before
  comparing). gnosh: list it as `Agent`? See "decisions for Jae" below.
- `mcp_server_errors` and `plugin_errors` are omitted when empty.
- gnosh values: `apiKeySource: "none"`, `plugins: []`, `output_style:
  "default"`; omit `betas`, `fast_mode_*`.

### 4.2 `assistant`

```typescript
{
  type: "assistant";
  uuid: string;
  session_id: string;
  message: {                       // Anthropic Messages API "message" object
    id: string;
    type: "message";
    role: "assistant";
    model: string;
    content: ContentBlock[];       // exactly one block per line, see 3.2
    stop_reason: string | null;    // "end_turn" | "tool_use" | "max_tokens" | "stop_sequence" | ...
    stop_sequence: string | null;
    usage: Usage;                  // section 4.6; on streamed lines output counts are placeholders
  };
  parent_tool_use_id: string | null; // null = main conversation; otherwise the Agent/Skill tool_use that owns this sub-agent
  error?: "authentication_failed" | "oauth_org_not_allowed" | "account_on_hold" | "billing_error"
        | "rate_limit" | "overloaded" | "invalid_request" | "model_not_found" | "server_error"
        | "max_output_tokens" | "cloud_credential_error" | "unknown";
  aborted?: true;                  // an interrupt cut the message short; no stop_reason
  agent_id?: string;               // set on sub-agent messages; equals that sub-agent's task_id
  timestamp?: string;              // ISO 8601, display only
  user_message_uuid?: string;      // echo of the user message this turn answers (first reply only)
  user_message_uuids?: string[];
}
```

Content blocks (Messages API):

```typescript
type ContentBlock =
  | { type: "text"; text: string }
  | { type: "thinking"; thinking: string; signature?: string }
  | { type: "tool_use"; id: string; name: string; input: object };
```

`error` values gnosh will actually use: `rate_limit`, `overloaded`,
`invalid_request`, `model_not_found`, `server_error`, `max_output_tokens`,
`authentication_failed`, `unknown`.

### 4.3 `user`

```typescript
{
  type: "user";
  uuid?: string;
  session_id?: string;
  message: {                       // Messages API "message param"
    role: "user";
    content: string | UserBlock[];
  };
  parent_tool_use_id: string | null;
  tool_use_result?: unknown;       // structured tool output, see tools.md; not what the model saw
  isSynthetic?: boolean;           // not typed by the user
  isReplay?: true;                 // only on lines produced by --replay-user-messages
  agent_id?: string;
  origin?: Origin;                 // section 4.7
}

type UserBlock =
  | { type: "text"; text: string }
  | { type: "image"; source: { type: "base64"; media_type: string; data: string } }
  | { type: "tool_result"; tool_use_id: string; content: string | (TextBlock | ImageBlock)[]; is_error?: boolean };
```

- A tool result is a `user` line whose content has a `tool_result` block; the
  text in `content` is what the model reads, and `tool_use_result` is the
  tool's structured output object (shape depends on the tool: `tools.md`).
- A denied or failed call is a `tool_result` with `is_error: true`.

### 4.4 `stream_event` (needs `--include-partial-messages`)

```typescript
{
  type: "stream_event";
  event: RawStreamEvent;           // Messages API streaming event, unmodified
  parent_tool_use_id: null;        // always null: main conversation only
  uuid: string;
  session_id: string;
  ttft_ms?: number;                // only on message_start
}
```

`event.type` is one of `message_start`, `content_block_start`,
`content_block_delta`, `content_block_stop`, `message_delta`, `message_stop`
(and `ping`). Delta kinds inside `content_block_delta`: `text_delta`
(`.delta.text`), `input_json_delta` (`.delta.partial_json`), `thinking_delta`
(`.delta.thinking`). The documented `jq` filter:

```bash
jq -rj 'select(.type == "stream_event" and .event.delta.type? == "text_delta") | .event.delta.text'
```

A stream cut mid-message still ends with `message_stop` (v2.1.290+);
`content_block_stop` may be missing for an open tool-call block (treat its
input as incomplete) and `message_delta` may be missing.

gnosh talks to models that may not speak the Anthropic streaming protocol, so
it **synthesizes** these events from whatever its provider streams.

### 4.5 `result` — the result object

Success:

```typescript
{
  type: "result";
  subtype: "success";
  uuid: string;
  session_id: string;
  is_error: boolean;
  result: string;                  // the final answer text
  stop_reason: string | null;
  num_turns: number;
  duration_ms: number;             // wall time of the run
  duration_api_ms: number;         // time spent in model requests
  total_cost_usd: number;          // estimate
  usage: Usage;                    // main loop only (section 4.6)
  modelUsage: { [model: string]: ModelUsage }; // whole tree: main loop + sub-agents + compaction
  permission_denials: { tool_name: string; tool_use_id: string; tool_input: object }[];
  structured_output?: unknown;     // with --json-schema
  terminal_reason?: TerminalReason;
  api_error_status?: number | null;
  ttft_ms?: number;                // time to first complete assistant message
  ttft_stream_ms?: number;         // time to the first message_start
  time_to_request_ms?: number;
  result_index?: number;           // 0-based count of results this process has written
  local_command?: string;          // set when a slash command ended the turn without the model, e.g. "compact"
  deferred_tool_use?: { id: string; name: string; input: object }; // PreToolUse hook answered "defer"
  origin?: Origin;
  user_message_uuid?: string;
  user_message_uuids?: string[];
  queued_turn_count?: number;
}
```

Error (no `result` field; `errors` instead):

```typescript
{
  type: "result";
  subtype: "error_max_turns" | "error_during_execution" | "error_max_budget_usd"
         | "error_max_structured_output_retries";
  uuid: string;
  session_id: string;
  is_error: boolean;
  errors: string[];
  stop_reason: string | null;
  num_turns: number;
  duration_ms: number;
  duration_api_ms: number;
  total_cost_usd: number;
  usage: Usage;
  modelUsage: { [model: string]: ModelUsage };
  permission_denials: { tool_name: string; tool_use_id: string; tool_input: object }[];
  terminal_reason?: TerminalReason;
  startup_failure_reason?: string;
  result_index?: number;
}
```

| `subtype` | Meaning | `result` present |
| :- | :- | :- |
| `success` | The model finished normally. (`is_error` can still be `true` when the turn ended on an API error; then `result` holds the error text and `api_error_status` the HTTP status.) | yes |
| `error_max_turns` | `--max-turns` reached before the model finished. | no |
| `error_max_budget_usd` | `--max-budget-usd` reached. | no |
| `error_during_execution` | An error or interruption broke the loop; also written for known startup refusals, with zeroed totals. | no |
| `error_max_structured_output_retries` | No valid `--json-schema` output within the retry limit. | no |

`TerminalReason` — why the loop ended: `completed`, `max_turns`,
`tool_deferred`, `aborted_streaming`, `aborted_tools`, `hook_stopped`,
`stop_hook_prevented`, `background_requested`, `blocking_limit`,
`rapid_refill_breaker`, `prompt_too_long`, `image_error`, `model_error`,
`api_error`, `malformed_tool_use_exhausted`, `budget_exhausted`,
`structured_output_retry_exhausted`, `tool_deferred_unavailable`,
`turn_setup_failed`.

Special cases worth copying:

- A `UserPromptSubmit` hook that blocks the prompt still produces a result:
  `subtype: "success"`, `is_error: false`, `num_turns: 0`, `result` = the
  block message, no `terminal_reason`.
- A whitespace-only stream-json message ends its turn with result text
  `Blank prompt — the message was only whitespace, so nothing was sent to the model.`
- When several background-task notifications are answered together, each
  still gets a result; all but the last are empty with `num_turns: 0`.
- A `PreToolUse` hook answering `defer`: `stop_reason: "tool_deferred"` and
  `deferred_tool_use` filled in.

### 4.6 `Usage` and `ModelUsage`

```typescript
type Usage = {                     // Messages API usage, non-null on the result
  input_tokens: number;
  output_tokens: number;
  cache_creation_input_tokens: number;
  cache_read_input_tokens: number;
  cache_creation?: { ephemeral_5m_input_tokens: number; ephemeral_1h_input_tokens: number };
  server_tool_use?: object;
  service_tier?: "standard" | "priority" | "batch";
  output_tokens_details?: { thinking_tokens: number };
};

type ModelUsage = {                // note the camelCase
  inputTokens: number;
  outputTokens: number;            // already includes thinking tokens
  thinkingTokens?: number;
  cacheReadInputTokens: number;
  cacheCreationInputTokens: number;
  webSearchRequests: number;
  costUSD: number;
  contextWindow: number;
  maxOutputTokens: number;
  canonicalModel?: string;
  provider?: string;
  costBasis?: "list" | "managed" | "unknown";
};
```

`usage` covers only the main agent loop; `modelUsage` covers every model call
of the run (sub-agents, compaction). gnosh fills what its provider reports and
uses `0` for the rest; `total_cost_usd` and `costUSD` are `0` unless a price
table is configured.

### 4.7 `Origin`

Who caused a turn; appears on `user` and `result` lines.

```typescript
type Origin =
  | { kind: "human" }
  | { kind: "task-notification"; subkind?: string; runId?: string }
  | { kind: "auto-continuation" }
  | { kind: "unclassified" }
  | { kind: "peer"; from: string; name?: string; body?: string }
  | { kind: "channel"; server: string }
  | { kind: "coordinator" };
```

gnosh uses `human`, `task-notification` (a background command or sub-agent
finished and the model is given a turn to react) and `auto-continuation`.

### 4.8 Other `system` lines

All carry `type: "system"`, `uuid`, `session_id` in addition to:

| `subtype` | Fields |
| :- | :- |
| `compact_boundary` | `compact_metadata: { trigger: "manual" \| "auto"; pre_tokens: number }` |
| `status` | `status: "compacting" \| null`, `permissionMode?` |
| `api_retry` | `attempt` (from 1), `max_retries`, `retry_delay_ms`, `error_status` (HTTP status or `null`), `error` (same category strings as `assistant.error`), `no_response?: { waited_ms, retry_wait_ms }` |
| `informational` | `content: string`, `level: "info" \| "notice" \| "suggestion" \| "warning"`, `tool_use_id?`, `prevent_continuation?`, `tag?`. A hook's `systemMessage` arrives this way, each line prefixed with the hook's name, e.g. `PostToolUse:Bash says:`. |
| `permission_denied` | `tool_name`, `tool_use_id`, `message` (the rejection text given to the model), `decision_reason_type?` (`"rule"`, `"mode"`, `"classifier"`, `"asyncAgent"`), `decision_reason?`, `agent_id?`. Best-effort; `permission_denials` on the result is authoritative. Not emitted for denials decided by a `PreToolUse` hook. |
| `task_started` | `task_id`, `description`, `task_type?` (`"local_bash"`, `"local_agent"`, `"mcp_task"`, `"remote_agent"`), `tool_use_id?`, `run_id?`, `is_backgrounded?`, `spawn_depth?`, `parent_task_id?`, `ambient?` |
| `task_progress` | `task_id`, `description`, `usage: { total_tokens, tool_uses, duration_ms }`, `last_tool_name?`, `summary?`, `subagent_type?`, `tool_use_id?`, `run_id?` |
| `task_updated` | `task_id`, `patch: { status?: "pending" \| "running" \| "completed" \| "failed" \| "killed"; description?; end_time? (epoch ms); error?; is_backgrounded?; total_paused_ms? }`, `run_id?` |
| `task_notification` | `task_id`, `status: "completed" \| "failed" \| "stopped"`, `output_file: string`, `summary: string`, `tool_use_id?`, `usage?: { total_tokens, tool_uses, duration_ms }`, `run_id?` |
| `background_tasks_changed` | `tasks: { task_id, task_type, description, subagent_type?, parent_task_id?, run_id?, ambient? }[]` — always the full live set |
| `hook_started` | `hook_id`, `hook_name`, `hook_event` |
| `hook_progress` | `hook_id`, `hook_name`, `hook_event`, `stdout`, `stderr`, `output` |
| `hook_response` | `hook_id`, `hook_name`, `hook_event`, `output`, `stdout`, `stderr`, `exit_code?`, `outcome: "success" \| "error" \| "cancelled"` |
| `thinking_tokens` | `estimated_tokens`, `estimated_tokens_delta`, `user_message_uuid?` |
| `session_state_changed` | `state: "idle" \| "running" \| "requires_action"` |
| `commands_changed` | `commands: { name, description, argumentHint, aliases?, builtin? }[]` |

Other top-level types:

```typescript
{ type: "tool_progress"; tool_use_id: string; tool_name: string; parent_tool_use_id: string | null;
  elapsed_time_seconds: number; heartbeat?: boolean; task_id?: string; subagent_type?: string;
  uuid: string; session_id: string }

{ type: "conversation_reset"; new_conversation_id: string; trigger?: "clear" | "plan_mode_exit" | "fresh_session" | "onboarding";
  user_message_uuid?: string; timestamp?: string; uuid: string; session_id: string }

{ type: "tool_use_summary"; summary: string; preceding_tool_use_ids: string[]; uuid: string; session_id: string }
```

### 4.9 Sub-agent messages in the stream

- Sub-agent `assistant` and `user` lines carry `parent_tool_use_id` = the ID
  of the `Agent` (or forked `Skill`) `tool_use` block that started them; main
  conversation lines carry `null`.
- The first line of a foreground sub-agent is a `user` line with the prompt
  that drives it.
- By default only the sub-agent's `tool_use` and `tool_result` blocks are
  emitted; its text and thinking blocks need `--forward-subagent-text`.
- `stream_event` lines are never emitted for sub-agents.

## 5. `--input-format stream-json` (stdin)

One JSON object per line. The minimal user message:

```json
{"type":"user","message":{"role":"user","content":"Analyze this codebase for security issues"}}
```

`content` may also be an array of blocks (text, base64 image). Optional
fields a sender may set:

| Field | Meaning |
| :- | :- |
| `uuid` | Echoed back as `user_message_uuid` on the first reply and the result. |
| `session_id` | — |
| `parent_tool_use_id` | `null` for ordinary input. |
| `priority` | `"next"` (default: read between tool calls of the running turn, or start the next turn), `"later"` (hold until the turn ends), `"now"` (interrupt, or with `origin: {kind:"human"}` move running work to the background and read it at once). |
| `shouldQuery` | `false` = append to the transcript without starting a turn; merged into the next message that does. |
| `isSynthetic` | The text was not typed by the user. |
| `client_composed` | Deliver verbatim: no `@path` expansion, no `/command`. |
| `origin` | Section 4.7. |

With `--replay-user-messages`, each accepted user message is re-emitted on
stdout as a `user` line with `isReplay: true` and a `uuid`.

The SDK also sends *control requests* on this channel (`initialize`,
`interrupt`, permission prompts answered by the host). Their wire format is
not documented on the CLI pages (unverified) — gnosh does not implement them
in P0/P1; without a host, a call that would prompt is denied (see
`permissions.md`).

## 6. Illustrative gnosh stream (not captured from Claude Code)

```json
{"type":"system","subtype":"init","uuid":"…","session_id":"0b6c…","cwd":"/work/app","model":"qwen3.8-27b","tools":["Agent","Bash","Edit","Glob","Grep","Read","Write"],"mcp_servers":[],"permissionMode":"default","slash_commands":["clear","compact"],"output_style":"default","skills":[],"plugins":[],"apiKeySource":"none","gnosh_version":"0.1.0"}
{"type":"assistant","uuid":"…","session_id":"0b6c…","parent_tool_use_id":null,"message":{"id":"msg_1","type":"message","role":"assistant","model":"qwen3.8-27b","content":[{"type":"tool_use","id":"toolu_1","name":"Read","input":{"file_path":"/work/app/go.mod"}}],"stop_reason":"tool_use","stop_sequence":null,"usage":{"input_tokens":1200,"output_tokens":38,"cache_creation_input_tokens":0,"cache_read_input_tokens":0}}}
{"type":"user","uuid":"…","session_id":"0b6c…","parent_tool_use_id":null,"message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_1","content":"1\tmodule example.com/app\n"}]},"tool_use_result":{"type":"text","file":{"filePath":"/work/app/go.mod","content":"module example.com/app\n","numLines":1,"startLine":1,"totalLines":1}}}
{"type":"assistant","uuid":"…","session_id":"0b6c…","parent_tool_use_id":null,"message":{"id":"msg_2","type":"message","role":"assistant","model":"qwen3.8-27b","content":[{"type":"text","text":"The module is example.com/app."}],"stop_reason":"end_turn","stop_sequence":null,"usage":{"input_tokens":1260,"output_tokens":11,"cache_creation_input_tokens":0,"cache_read_input_tokens":0}}}
{"type":"result","subtype":"success","uuid":"…","session_id":"0b6c…","is_error":false,"result":"The module is example.com/app.","stop_reason":"end_turn","num_turns":2,"duration_ms":4210,"duration_api_ms":3890,"total_cost_usd":0,"usage":{"input_tokens":2460,"output_tokens":49,"cache_creation_input_tokens":0,"cache_read_input_tokens":0},"modelUsage":{"qwen3.8-27b":{"inputTokens":2460,"outputTokens":49,"cacheReadInputTokens":0,"cacheCreationInputTokens":0,"webSearchRequests":0,"costUSD":0,"contextWindow":65536,"maxOutputTokens":8192}},"permission_denials":[],"terminal_reason":"completed"}
```

---

## What gnosh adopts

**P0**

- [ ] `text`: final answer on stdout, diagnostics on stderr.
- [ ] `json`: the single result object, both arms (success with `result`;
      error arms with `errors`), fields `type`, `subtype`, `is_error`,
      `result`, `session_id`, `uuid`, `num_turns`, `duration_ms`,
      `duration_api_ms`, `stop_reason`, `total_cost_usd`, `usage`,
      `modelUsage`, `permission_denials`, `terminal_reason`.
- [ ] `stream-json`: `system/init`, `assistant` (one content block per
      line, shared `message.id`), `user` (tool results with
      `tool_use_result`), final `result`.
- [ ] Result subtypes `success`, `error_max_turns`,
      `error_during_execution`.
- [ ] `parent_tool_use_id` on every `assistant`/`user` line (`null` in the
      main conversation).

**P1**

- [ ] `stream_event` synthesis for `--include-partial-messages`.
- [ ] stdin `stream-json` user messages, `priority`, `--replay-user-messages`.
- [ ] `system`: `compact_boundary`, `status`, `api_retry`, `informational`,
      `permission_denied`; `tool_progress` heartbeats.
- [ ] `system` task lines for background commands and sub-agents;
      sub-agent lines with `parent_tool_use_id`.
- [ ] `origin` on results caused by task notifications.

**P2**

- [ ] `structured_output` (`--json-schema`), `error_max_budget_usd`,
      `error_max_structured_output_retries`.
- [x] `hook_started` / `hook_response` (no `hook_progress`: DECISIONS 368).
- [ ] `hook_progress`, `thinking_tokens`, `session_state_changed`, `commands_changed`,
      `conversation_reset`, `tool_use_summary`, `deferred_tool_use`,
      `user_message_uuid(s)`, `result_index`, timing extras.

## Deliberately not adopted

- `auth_status`, `rate_limit_event` (claude.ai plan limits),
  `prompt_suggestion`, `plugin_install`, `files_persisted`,
  `worker_shutting_down`, `model_refusal_*`, `fast_mode_*`, `betas`,
  `context_usage` / `usage_report` attachments, peer/channel/coordinator
  origins, startup-failure reasons about organizations and gateways.
- The SDK control-request channel (host-side permission callbacks,
  `initialize`, `interrupt`).

## Decisions for Jae

1. **Version field in `init`.** Claude Code emits `claude_code_version`.
   gnosh is not Claude Code, so the example above emits `gnosh_version` and
   omits `claude_code_version`. A script that requires the old key would need
   it; say so if one turns up.
2. **Sub-agent tool name in `init.tools`.** Claude Code lists it as `Task`
   (legacy) even though the tool is called `Agent`. gnosh lists `Agent`.

## Unverified (docs silent)

- Whether `json` + `--verbose` prints an array instead of one object.
- Field order within objects (irrelevant to JSON consumers).
- Trailing newline in `text` mode.
- The wire format of SDK control requests/responses.
- Exact `stop_reason` strings for non-Anthropic models (gnosh maps its
  provider's finish reasons onto `end_turn`, `tool_use`, `max_tokens`).
