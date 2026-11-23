# hooks.md — hook events, their JSON, and how a hook answers

*Since 14 November 2026 gnosh is configured by one JSON file, `gnosh.json`, and no longer by `gnoshrc` (`spec/DECISIONS.md`, items 17 and 23 to 28). Where this file speaks of `gnoshrc`, of its directives or of its dashed option names, it records what the build was first given.*

What this file is: the hook system gnosh copies from Claude Code, exact
enough that a `hooks` block from one of Jae's existing `settings.json` files
runs unchanged, and that a hook script written for Claude Code reads the same
JSON on stdin and is understood when it answers.

In one paragraph: a hook is a command the harness runs when something happens
— a session starts, the user sends a prompt, the model is about to call a
tool, a tool finished, the model wants to stop. The command gets a JSON
object on stdin describing the event. It answers with its exit code (0 =
fine, 2 = block, anything else = a warning that does not block) and,
optionally, a JSON object on stdout for finer control: allow or deny a tool
call, rewrite its input, add text to the model's context, refuse to let the
turn end. Hooks are configured per event, filtered by a matcher (usually the
tool name), and all matching hooks run in parallel.

Source: code.claude.com/docs/en — hooks (reference), hooks-guide,
settings-reference (hooks and automation), agent-sdk/typescript (hook input
and output types); docs version 2.1.323, read 2026-11-13. Own words; event
names, field names and quoted messages are literal. "(unverified)" = the docs
do not say.

---

## 1. Events at a glance

33 events in the docs. Priority is gnosh's.

| Event | Fires | Matcher is tested against | Can block | Pri |
| :- | :- | :- | :- | :- |
| `SessionStart` | A session begins or resumes | `source`: `startup`, `resume`, `clear`, `compact`, `fork` | no | P0 |
| `UserPromptSubmit` | A prompt is submitted, before the model sees it | — | yes | P0 |
| `PreToolUse` | Before a tool call runs | tool name | yes | P0 |
| `PostToolUse` | After a tool call succeeds | tool name | feedback only | P0 |
| `PostToolUseFailure` | After a tool call fails | tool name | feedback only | P0 |
| `Stop` | The model finishes responding | — | yes (keeps it working) | P0 |
| `SessionEnd` | The session terminates | `reason`: `clear`, `resume`, `logout`, `prompt_input_exit`, `other` | no | P0 |
| `PermissionRequest` | A tool call needs a permission decision | tool name | decides | P1 |
| `Notification` | The harness sends a notification | `notification_type` | no | P1 |
| `SubagentStart` | A sub-agent is spawned | agent type | no | P1 |
| `SubagentStop` | A sub-agent finishes | agent type | yes | P1 |
| `PreCompact` | Before context compaction | `trigger`: `manual`, `auto` | yes | P1 |
| `PostCompact` | After compaction | `trigger` | no | P1 |
| `StopFailure` | The turn ends on an API error | error type | no | P1 |
| `InstructionsLoaded` | A memory or rules file is loaded | `load_reason` | no | P1 |
| `PostToolBatch` | A whole batch of parallel tool calls resolved | — | yes | P2 |
| `UserPromptExpansion` | A typed `/command` expands into a prompt | command name | yes | P2 |
| `PermissionDenied` | Auto mode denied a call | tool name | no | not adopted (auto mode) |
| `CwdChanged` | The working directory changes | — | no | P2 |
| `DirectoryAdded` | A working directory is added mid-session | `source`: `slash_command`, `register_repo_root` | no | P2 |
| `FileChanged` | A watched file changes on disk | literal file names | no | P2 |
| `ConfigChange` | A settings file changes during a session | source: `user_settings`, `project_settings`, `local_settings`, `policy_settings`, `skills` | yes | P2 |
| `Setup` | `--init-only`, or `--init` / `--maintenance` with `-p` | `init`, `maintenance` | no | P2 |
| `PreModelSwitch` / `PostModelSwitch` | Before / after the session's model changes | canonical name of the target model | yes / no | P2 |
| `WorktreeCreate` / `WorktreeRemove` | A worktree is created / removed | — | yes | P2 |
| `MessageDisplay` | While assistant text is displayed | — | display only | P2 |
| `TaskCreated` / `TaskCompleted` | A task-list task is created / completed | — | yes | not adopted (`mem`) |
| `TeammateIdle` | An agent-team teammate is about to go idle | — | yes | not adopted |
| `Elicitation` / `ElicitationResult` | An MCP server asks the user for input / the user answered | MCP server name | yes | P2 (with MCP) |

Cadence: per session — `SessionStart`, `SessionEnd`; per turn —
`UserPromptSubmit`, `Stop`, `StopFailure`; per tool call inside the loop —
`PreToolUse`, then (`PermissionRequest` if a prompt is due), then
`PostToolUse` or `PostToolUseFailure`, then `PostToolBatch` once per batch.

**Events gnosh does not run (yet).** Claude Code treats an unknown hook
event name as a settings *warning*: that entry is skipped and the rest of
the file applies. gnosh does the same for names it has never heard of and
for observation-only events it has not built yet (`Notification`,
`InstructionsLoaded`, `CwdChanged`, ...), and lists them in `gnosh doctor`.

One exception, because a silently skipped guard is a guard that failed
open: if a settings file configures a hook on an event that can **block or
decide** — `PreToolUse`, `PermissionRequest`, `UserPromptSubmit`,
`UserPromptExpansion`, `Stop`, `SubagentStop`, `PostToolBatch`,
`PreCompact`, `ConfigChange` — and this gnosh build does not run that event,
or cannot run that handler `type`, gnosh refuses to start and names the
hook. (Jae's standing rule: hooks fail closed.) A handler field gnosh cannot
evaluate, such as `if`, makes the hook run unconditionally rather than not
at all — which is also what Claude Code does when it cannot analyse a
command.

## 2. Configuration shape

Hooks live under the `hooks` key of a settings file. Three levels: event →
matcher group → handlers.

```json
{
  "hooks": {
    "PreToolUse": [
      {
        "matcher": "Bash",
        "hooks": [
          {
            "type": "command",
            "if": "Bash(rm *)",
            "command": "${CLAUDE_PROJECT_DIR}/.claude/hooks/block-rm.sh",
            "args": [],
            "timeout": 30
          }
        ]
      }
    ]
  }
}
```

- `matcher` is a single string. An array is an invalid setting; under
  `PreToolUse` or `PermissionRequest` it also stops that file's other hooks
  from loading.
- Where hooks may be defined: user settings, project settings, local
  settings, managed settings, a plugin's `hooks/hooks.json`, a skill's
  frontmatter (registered when the skill is invoked, for the rest of the
  session), a sub-agent's frontmatter (only while that sub-agent runs; a
  `Stop` hook there becomes `SubagentStop`).
- **Hook entries merge across settings files**; they do not replace each
  other. The same handler defined in more than one settings file runs once.
- Hooks from settings also run inside sub-agents; the input then carries
  `agent_id` and `agent_type`.
- `disableAllHooks: true` switches every hook off. It is read after normal
  settings precedence, so a project's `false` overrides a user's `true`;
  `--settings '{"disableAllHooks": true}'` wins over both. There is no way
  to disable one hook while leaving it configured.
- Edits to hook settings are picked up during a session by a file watcher.
- `/hooks` opens a read-only browser of configured hooks, each labelled with
  its source.

### 2.1 Matcher

| Matcher value | Evaluated as |
| :- | :- |
| `"*"`, `""`, or omitted | Matches everything. |
| Only letters, digits, `_`, `-`, spaces, `,` and `\|` | An exact string, or a list of exact strings separated by `\|` or `,` (surrounding whitespace allowed): `Bash`, `Edit\|Write`, `Edit, Write`. |
| Contains any other character | A regular expression, **unanchored** (a match anywhere succeeds): `^Notebook`, `mcp__memory__.*`. |

Consequences:

- `Edit.*` matches both `Edit` and `NotebookEdit`; write `^Edit$` for a
  whole-string match.
- `mcp__memory` contains only exact-match characters, so it is an exact
  string and matches no tool; write `mcp__memory__.*`.
- For `FileChanged` and `StopFailure` the exact-match character set is
  narrower (letters, digits, `_`, `|`), and only `|` separates alternatives.
- A `matcher` on an event with no matcher support is silently ignored.
- Matchers use bare tool names, never the `Tool(specifier)` rule form (that
  goes in `if`). Old tool names (`Task`, `KillShell`) are accepted.

The regular-expression dialect is JavaScript's. gnosh uses Go's `regexp`
(RE2): ordinary patterns behave the same; look-around and backreferences
are not available and are reported as an invalid matcher.

### 2.2 Handler fields

Common to all handler types:

| Field | Req | Meaning |
| :- | :- | :- |
| `type` | yes | `command`, `http`, `mcp_tool`, `prompt`, or `agent`. |
| `if` | no | One permission rule (`Bash(git *)`, `Edit(*.ts)`); the handler runs only if the tool call matches. Evaluated only on tool events (`PreToolUse`, `PostToolUse`, `PostToolUseFailure`, `PermissionRequest`, `PermissionDenied`); **on any other event a handler with `if` never runs.** Exactly one rule; no `&&`, `\|\|` or lists. |
| `timeout` | no | Seconds. Defaults: 600 for `command`, `http`, `mcp_tool`; 30 for `prompt`; 60 for `agent`. The 600 default drops to 30 on `UserPromptSubmit`, `PreModelSwitch`, `PostModelSwitch`, and to 10 on `MessageDisplay`. `SessionEnd` hooks share a 1.5-second budget, raised to the longest configured `timeout`, at most 60 seconds. Not enforced on `async` command hooks. |
| `statusMessage` | no | Spinner text shown while the hook runs. |
| `once` | no | Remove the hook after its first successful run. Honoured only in skill frontmatter. |

`if` on Bash commands: leading `VAR=value` assignments are stripped; each
sub-command of a compound command is checked; commands inside `$()` and
backticks are checked; a pattern that specifies more than the command name
(`Bash(git push *)`) runs the hook anyway when the command contains `$()`,
backticks or `$VAR`; and when the command cannot be analysed the hook runs
regardless. The filter is best-effort — it saves process spawns, it is not a
security gate. In `if` for a file tool, `Edit(src/**)` matches only `src`
under the working directory; `Edit(**/src/**)` matches at any depth.

Command handlers (`type: "command"`):

| Field | Req | Meaning |
| :- | :- | :- |
| `command` | yes | Shell command line; or, with `args`, the executable to spawn. |
| `args` | no | Argument list. Present → **exec form**: `command` is resolved on `PATH` and spawned directly, no shell, each element one argument. Absent → **shell form**: the string goes to `sh -c`. |
| `async` | no | Run in the background without blocking (section 7). |
| `asyncRewake` | no | Run in the background; on exit code 2 wake the model with the hook's stderr (or stdout if stderr is empty) as a system reminder. |
| `shell` | no | `bash` (default) or `powershell`. Ignored when `args` is set. |
| `onFailure` | no | `continue` or `block`: what happens to the triggering action when the hook itself fails (section 4.4). v2.1.295+. |

HTTP handlers (`type: "http"`): `url` (required), `headers` (values may
interpolate `$VAR` / `${VAR}`, but only variables named in `allowedEnvVars`;
others become empty), `allowedEnvVars`, `onFailure`. The event JSON is the
POST body with `Content-Type: application/json`; the response body uses the
same JSON output format. Settings `allowedHttpHookUrls` and
`httpHookAllowedEnvVars` restrict them.

MCP-tool handlers (`type: "mcp_tool"`): `server`, `tool`, `input` (string
values may substitute `${tool_input.file_path}`-style paths from the event
JSON). The tool's text output is read like command stdout.

Prompt and agent handlers (`type: "prompt"` / `"agent"`): `prompt`
(`$ARGUMENTS` is replaced by the event JSON), `model`. A model call returns
`{"ok": true}` or `{"ok": false, "reason": "..."}`. At most 12 prompt/agent
hooks run at once.

### 2.3 Where a handler runs

- In the session's current directory, with the harness's environment
  (credential scrubbing aside). If that directory no longer exists: the
  directory the session started in, else the project root, else home, else
  the system temp directory.
- On macOS and Linux a command hook runs in its own session **without a
  controlling terminal**: it cannot open `/dev/tty` or write escape
  sequences to the screen. To show something it returns `systemMessage`; to
  ring the bell or raise a desktop notification it returns
  `terminalSequence`.
- All hooks matching one event run **in parallel**.

### 2.4 Placeholders and environment

| Name | Meaning |
| :- | :- |
| `${CLAUDE_PROJECT_DIR}` | The project root where the session started. Substituted in `command` and each `args` element, and exported as an environment variable to the hook. Stays put when the session enters a worktree or runs `cd` (the event's `cwd` field follows those). |
| `${CLAUDE_PLUGIN_ROOT}`, `${CLAUDE_PLUGIN_DATA}` | Plugin directories. (not adopted) |
| `CLAUDE_ENV_FILE` | Path of a file a hook may append `export` lines to; they are applied to every later Bash command. Provided to `SessionStart`, `Setup`, `CwdChanged` and `FileChanged` hooks. |
| `CLAUDE_EFFORT` | The effort level, same as `effort.level` in the input. |
| `CLAUDECODE=1` | Set in every subprocess the harness spawns. |
| `CLAUDE_CODE_REMOTE` | `"true"` only in Anthropic's cloud. (not adopted) |

There is no model environment variable; the model name arrives only in
`SessionStart` input (and the model-switch events).

gnosh exports `CLAUDE_PROJECT_DIR` under that exact name (existing hook
commands depend on it) and additionally `GNOSH_PROJECT_DIR`.

## 3. Input: fields every event gets

A command hook reads one JSON object from stdin.

| Field | Meaning |
| :- | :- |
| `session_id` | Current session ID. |
| `transcript_path` | Path of the session's transcript file (JSONL). It is written asynchronously and may lag the conversation. |
| `cwd` | Working directory when the hook fires. |
| `hook_event_name` | The event name. |
| `permission_mode` | `default`, `plan`, `acceptEdits`, `auto`, `dontAsk` or `bypassPermissions` — never `manual`. Not on every event. |
| `prompt_id` | UUID of the user prompt being processed; absent before the first input. |
| `scratchpad_dir` | The session's scratch directory, when it has one. v2.1.257+. |
| `effort` | `{ "level": "low" \| "medium" \| "high" \| "xhigh" \| "max" }`, on events with a tool-use context, for models that have effort. |
| `agent_id` | Present when the hook fires inside a sub-agent: that sub-agent's ID. |
| `agent_type` | The agent's name (`Explore`, a custom name), inside a sub-agent or when the session runs with `--agent`. |

Example (`PreToolUse` on Bash):

```json
{
  "session_id": "abc123",
  "prompt_id": "550e8400-e29b-41d4-a716-446655440000",
  "transcript_path": "/home/user/.claude/projects/.../transcript.jsonl",
  "cwd": "/home/user/my-project",
  "permission_mode": "default",
  "hook_event_name": "PreToolUse",
  "tool_name": "Bash",
  "tool_input": { "command": "npm test", "description": "Run test suite", "timeout": 120000, "run_in_background": false },
  "tool_use_id": "toolu_01ABC123..."
}
```

## 4. Output: exit codes

| Exit | Meaning |
| :- | :- |
| `0` | Success. JSON on stdout, if any, is applied. The action goes ahead unless that JSON blocks it. |
| `2` | **Blocking error.** On events that can block, the action is stopped and the hook's **stderr** is the reason (shown to the model or the user, per event). |
| any other | **Non-blocking error.** The action goes ahead. The user sees a `<hook name> hook error` notice carrying the first line of stderr (`Failed with non-blocking status code: ...`). |

Exit code 1 does **not** block. A hook that enforces a policy must exit 2
(or set `onFailure: "block"`).

How stdout and the exit code combine:

| Stdout | Exit 0 | Exit 2 | Other exit |
| :- | :- | :- | :- |
| One valid JSON object | Success; fields apply | Blocks; fields are read but cannot undo the block (even `permissionDecision: "allow"`) | Success; exit code ignored, fields decide |
| JSON that does not parse or fails validation | Non-blocking error | Blocks; stderr is the reason | Non-blocking error |
| Plain text or nothing | Success | Blocks; stderr is the reason | Non-blocking error |

What counts as JSON output: stdout holding exactly one JSON object (on one
line or several) with only whitespace around it. Output that does not start
with `{`, or starts with `{` and does not end with `}`, is plain text — so a
JSON array or a quoted string is plain text. Anything a shell profile echoes
before the object spoils it.

What happens to plain stdout on exit 0: it goes to the debug log, not the
transcript — except on `UserPromptSubmit`, `UserPromptExpansion`,
`SessionStart` and `PostModelSwitch`, where it is **added to the model's
context**, and on `PreCompact`, where it becomes extra instructions for the
compaction summary. The model never sees stderr from a hook that exited 0.

### 4.1 What exit code 2 does, per event

| Event | Exit 2 |
| :- | :- |
| `PreToolUse` | Blocks the tool call; stderr goes to the model as the tool's error. |
| `UserPromptSubmit` | Blocks the prompt; it never reaches the model. |
| `UserPromptExpansion` | Blocks the expansion. |
| `Stop` | Prevents stopping; the conversation continues with stderr as feedback. |
| `SubagentStop` | Prevents the sub-agent from stopping. |
| `PostToolBatch` | Stops the loop before the next model call. |
| `PreCompact` | Blocks compaction. |
| `ConfigChange` | Blocks the change (except managed settings). |
| `PreModelSwitch` | Blocks the switch; stderr shown to the user. |
| `PostToolUse`, `PostToolUseFailure` | Cannot block (the tool already ran); stderr is shown to the model. |
| `PermissionRequest` | Not honoured; the permission flow continues unchanged. Deny through JSON instead. |
| `SessionStart`, `SubagentStart`, `SessionEnd`, `CwdChanged`, `FileChanged`, `PostCompact`, `PostModelSwitch` | Stderr shown to the user only. |
| `Notification`, `Setup`, `StopFailure`, `InstructionsLoaded`, `PermissionDenied` | Ignored. |
| `DirectoryAdded` | Stderr to the debug log. |
| `WorktreeCreate`, `WorktreeRemove` | Any non-zero exit fails the operation. |

The text the model receives for a blocked tool call is prefixed with the
event, the tool and the hook's command, for example
`PreToolUse:Bash hook error: [${CLAUDE_PROJECT_DIR}/.claude/hooks/no-rm.sh]: Blocked: rm commands are not allowed`.

One special case: on `Stop`, `SubagentStop` and `TaskCompleted`, exit 2 with
empty stdout and a stderr saying a file is missing (`No such file or
directory`) is treated as a non-blocking error — a broken hook path must not
trap the model in a loop.

### 4.2 Timeouts

A `command`, `http` or `mcp_tool` hook that reaches its timeout is cancelled
and its output discarded: on most events that means **no decision**. On
`PreToolUse` a timed-out hook does not block the call (it continues through
the normal permission flow) unless `onFailure: "block"` is set. On
`PreModelSwitch` a timeout blocks the switch.

### 4.3 Hooks that leave a process running

A hook may start a long-running program in the background, but must redirect
that program's stdout and stderr; otherwise a non-async hook takes up to 10
seconds to finish after its own process exits, and a decision printed just
before starting an un-redirected background process is treated as suspect
and the tool call is blocked.

### 4.4 `onFailure`

By default a hook that fails (cannot start, exits other than 0 or 2, times
out, HTTP error, invalid JSON) lets the action through. With
`"onFailure": "block"` on a `command` or `http` hook a failure does what exit
code 2 does on that event (on `PermissionRequest`: denies the request). The
error then includes `failed; blocking because onFailure is "block"` (or
`timed out`). It has no effect on `Stop`, `SubagentStop`, `TaskCompleted`,
`TeammateIdle`, or on `async` hooks.

## 5. Output: JSON

Exit 0 and print one JSON object.

### 5.1 Fields every event accepts

| Field | Default | Meaning |
| :- | :- | :- |
| `continue` | `true` | `false` stops the model entirely after the hook runs. Takes precedence over any event-specific decision. |
| `stopReason` | — | Shown to the user when `continue` is `false`; it stays in the conversation. |
| `systemMessage` | — | A warning shown to the user. In stream-json it arrives as a `system/informational` line, prefixed with the hook's name. |
| `suppressOutput` | `false` | Accepted, does nothing. |
| `terminalSequence` | — | An escape sequence the harness emits for the hook: only OSC `0`/`1`/`2` (titles), OSC `9`, `99`, `777` (notifications) and BEL. Anything else makes the field ignored. Interactive sessions only; not from `async` hooks. |

Some events discard `continue` and `systemMessage` (noted per event).

`additionalContext`, `systemMessage`, `initialUserMessage` and plain stdout
are each capped at **10,000 characters**. Over the cap, the text is saved to
a file in the session directory and replaced with the file path plus a
preview of the first 2,000 characters; the model is not told to read the
file.

### 5.2 Decision patterns

| Events | How a hook decides |
| :- | :- |
| `UserPromptSubmit`, `UserPromptExpansion`, `PostToolUse`, `PostToolUseFailure`, `PostToolBatch`, `Stop`, `SubagentStop`, `ConfigChange`, `PreCompact` | Top-level `"decision": "block"` with `"reason"`. `block` is the only value; to allow, omit `decision`. |
| `PreToolUse` | `hookSpecificOutput.permissionDecision`: `allow`, `deny`, `ask`, `defer`; with `permissionDecisionReason`. |
| `PermissionRequest` | `hookSpecificOutput.decision.behavior`: `allow` or `deny`. |
| `SessionStart`, `SubagentStart`, `PostModelSwitch` | No decision; `hookSpecificOutput.additionalContext` only. |
| `Setup`, `Notification`, `SessionEnd`, `PostCompact`, `InstructionsLoaded`, `StopFailure`, `CwdChanged`, `DirectoryAdded`, `FileChanged` | None; side effects only. |

`hookSpecificOutput` always carries `hookEventName` set to the event's name.

### 5.3 Several hooks on one event

- Every matching hook runs to completion before results are combined. One
  hook's `deny` does not stop its siblings, so a side effect in another hook
  still happens.
- `PreToolUse` decisions: the most restrictive wins — `deny`, then `defer`,
  then `ask`, then `allow`.
- `additionalContext` from every hook is kept and passed on together.
- Two hooks that both return `updatedInput` for the same call are not
  merged, and which rewrite wins is not guaranteed: have only one hook
  rewrite a given tool's input.
- A command hook can only talk through stdout, stderr and its exit code; it
  cannot trigger a `/command` or a tool call.
- `PreToolUse` hooks run before any permission-mode check, in every mode,
  `dontAsk` and `bypassPermissions` included: a hook's `deny` holds
  everywhere.

### 5.4 Adding context for the model

`hookSpecificOutput.additionalContext` (a string) is wrapped as a system
reminder and inserted into the conversation where the hook fired; the model
reads it on its next request; it is not shown as a chat message.

| Event | Where the text lands |
| :- | :- |
| `SessionStart`, `SubagentStart` | At the start of the conversation, before the first prompt. |
| `UserPromptSubmit`, `UserPromptExpansion` | Alongside the submitted prompt. |
| `PreToolUse`, `PostToolUse`, `PostToolUseFailure`, `PostToolBatch` | Next to the tool result. |
| `Stop`, `SubagentStop` | At the end of the turn; the conversation continues so the model can act on it. |

- Several hooks on one event: the model receives all values.
- A literal `<system-reminder>` tag inside the string has its `<` escaped.
- The injected text is saved in the transcript. On resume it is **replayed,
  not re-run** (so timestamps go stale) — except `SessionStart`, which runs
  again with `source: "resume"`.

Rewriting, not just allowing or blocking: `PreToolUse` and
`PermissionRequest` may replace the tool's input (`updatedInput`);
`PostToolUse` may replace the tool's result (`updatedToolOutput`).
`UserPromptSubmit` cannot replace the prompt, only add context beside it.

## 6. Events in detail

Each entry: when it fires, input fields beyond the common ones, what the
hook can return. Field names are literal.

### 6.1 SessionStart

- **When**: a session starts (`source: "startup"`), is resumed by `-c`, `-r`
  or `/resume` (`"resume"`), after `/clear` (`"clear"`), after any
  compaction (`"compact"`), or is created as a copy of another by
  `--fork-session` or `/branch` (`"fork"`).
- Only `command` and `mcp_tool` handlers are supported. It runs on every
  session, so it must be fast.
- In an interactive session these hooks run in the background at launch: the
  user can type at once, but the model's first response waits for them so
  their context arrives. A prompt sent meanwhile waits; Esc takes it back.
- **Input**: `source`; optionally `model` (may be absent, e.g. after
  `/clear`), `agent_type` (with `--agent`), `session_title` (only a
  user-set title). On `resume` and `fork` with at least one earlier
  response: `seconds_since_last_response`, `context_tokens`,
  `prompt_cache_likely_expired`, `estimated_cache_write_usd`.
- **Output**: no decision. Plain stdout is added to the model's context.
  `hookSpecificOutput` may carry:

  | Field | Meaning |
  | :- | :- |
  | `additionalContext` | Context placed before the first prompt. |
  | `initialUserMessage` | In `-p` runs: becomes the first user message, even with no prompt given; a prompt that was given follows as the next turn. |
  | `sessionTitle` | Names the session, like `/rename`. For `startup`, `resume`, `fork`. |
  | `watchPaths` | Absolute paths to watch for `FileChanged`. |
  | `reloadSkills` | `true` = re-scan skill and command directories after the hooks finish. |

- `CLAUDE_ENV_FILE` is available (section 2.4): each later Bash command runs
  the file's contents as shell code first.

### 6.2 UserPromptSubmit

- **When**: a prompt is submitted, before the model processes it. Also on
  turns the harness starts itself: a scheduled task or `/loop` iteration, a
  background sub-agent reporting back, a message from another session.
- **Input**: `prompt` (the submitted text, with collapsed pastes expanded);
  `session_title` when set. For a typed `/skill args` line the hook receives
  the line as typed, not the expanded skill.
- **Output**:

  | Field | Meaning |
  | :- | :- |
  | `decision: "block"` | The prompt never reaches the model. |
  | `reason` | Shown to the user; not added to context. |
  | `hookSpecificOutput.additionalContext` | Added beside the prompt. Plain stdout does the same. Both arrive as a system reminder that begins with the hook's name; neither is shown in the transcript. |
  | `hookSpecificOutput.sessionTitle` | Names the session. |
  | `hookSpecificOutput.suppressOriginalPrompt` | When blocking: leave the prompt text out of the block message. |

- A blocked prompt's block message ends with `Original prompt:` and the
  text, and is written to the transcript, unless suppressed.
- Default timeout 30 s. A timed-out hook's output is discarded and the
  prompt goes through (unless `onFailure: "block"`).
- Esc while hooks are checking: the check stops and the text returns to the
  input box.
- In `-p`, a blocked prompt still yields a result line: `subtype: "success"`,
  `num_turns: 0`, `result` = the block message.

### 6.3 PreToolUse

- **When**: after the model produced a tool call, before it runs, for every
  tool including MCP tools. Not for calls rejected earlier (unknown tool,
  input failing validation). Not for files pulled in by `@` mentions, which
  involve no tool call.
- **Input**: `tool_name`, `tool_input` (shapes in `tools.md`; file paths
  already absolute), `tool_use_id`; for an MCP tool also `mcp_server`:
  `{ "name": ..., "source": ... }`.
- **Output** in `hookSpecificOutput`:

  | Field | Meaning |
  | :- | :- |
  | `permissionDecision` | `"allow"`: skip the permission prompt. `"deny"`: refuse the call. `"ask"`: show the prompt. `"defer"`: end the turn so the call can be resumed later (`-p` only, single tool call). |
  | `permissionDecisionReason` | For `deny`: given to the model. For `ask`: shown in the prompt (and given to the model if nobody can answer). For `allow`/`defer`: debug log only. |
  | `updatedInput` | Replaces the **whole** input object (include unchanged fields). Permission rules are then evaluated against the new input. With `allow` = rewrite and approve; with `ask` = show the rewritten call. |
  | `additionalContext` | Added next to the tool result. |

- Several hooks, different answers: `deny` > `defer` > `ask` > `allow`.
- `allow` does not beat deny or ask rules, protected-path and critical-path
  checks, or plan mode's block on edits.
- An `ask` prompt is labelled with the hook's origin: `[settings]`,
  `[plugin:<name>]` or `[skill]`.
- Exit 2 = `deny` with stderr as the reason.
- `AskUserQuestion` and `ExitPlanMode` are approved only by `allow`
  **together with** `updatedInput` carrying the answer (for
  `AskUserQuestion`: the original `questions` plus an `answers` object
  mapping question text to the chosen label).
- Deprecated but accepted: top-level `decision` with `"approve"` (= allow)
  or `"block"` (= deny), and `reason`.
- `continue: false` stops the model even if the tool call fails or finishes
  while a response is still streaming.

### 6.4 PermissionRequest

- **When**: the harness is about to ask the user to approve a tool call —
  or would auto-deny one because nobody can be asked. `PreToolUse` runs for
  every call; this one only when approval is actually needed.
- **Input**: `tool_name`, `tool_input` (no `tool_use_id`), optional
  `permission_suggestions` (the updates the dialog would offer), `mcp_server`
  for MCP tools.
- **Output**: `hookSpecificOutput.decision`:

  | Field | Meaning |
  | :- | :- |
  | `behavior` | `"allow"` or `"deny"`. Deny and ask rules are still evaluated; an allow here does not beat a deny rule. |
  | `updatedInput` | With allow: replace the whole input; it is re-checked against deny and ask rules. |
  | `updatedPermissions` | With allow: permission updates to apply (table below). |
  | `message` | With deny: tells the model why. |
  | `interrupt` | With deny: `true` stops the model. |

- Exit 2 without a decision object changes nothing, and stderr is dropped.
- No decision from any hook: the prompt is shown; where it cannot be, the
  call is denied.
- In `-p` these hooks run in every mode except `dontAsk`, where a call that
  would prompt is denied outright.

Permission update entries (also the shape of `permission_suggestions`):

| `type` | Other fields | Effect |
| :- | :- | :- |
| `addRules` | `rules`, `behavior`, `destination` | Add rules. `rules` = `[{ "toolName": "Bash", "ruleContent": "npm test" }]`; omit `ruleContent` for the whole tool. `behavior` = `allow` / `deny` / `ask`. |
| `replaceRules` | same | Replace all rules of that behaviour at the destination. |
| `removeRules` | same | Remove matching rules. |
| `setMode` | `mode`, `destination` | Change the permission mode. `bypassPermissions` only if it was made available at launch; never persisted as a default. |
| `addDirectories` / `removeDirectories` | `directories`, `destination` | Add / remove working directories. |

`destination`: `session` (memory only), `localSettings`
(`.claude/settings.local.json`), `projectSettings`
(`.claude/settings.json`), `userSettings` (`~/.claude/settings.json`).

### 6.5 PostToolUse

- **When**: right after a tool call succeeds. Does not fire when a shell
  command or an outside process rewrites a file (that is `FileChanged`).
- **Input**: `tool_name`, `tool_input`, `tool_response` (the tool's
  **structured** result object, e.g. `{ "filePath": ..., "type": "create" }`
  for Write), `tool_use_id`, optional `duration_ms` (execution time only),
  `mcp_server` for MCP tools.
- **Output**:

  | Field | Meaning |
  | :- | :- |
  | `decision: "block"` + `reason` | Puts `reason` next to the tool result for the model. The model still sees the original output. |
  | `hookSpecificOutput.additionalContext` | Added next to the tool result. |
  | `hookSpecificOutput.updatedToolOutput` | Replaces what the model sees. Must match the tool's structured output shape (for Bash: `stdout`, `stderr`, `interrupted`, `isImage`); a mismatching value is ignored. The tool has already run. |
  | `hookSpecificOutput.updatedMCPToolOutput` | Same, MCP tools only; older spelling. |

- Exit 2: stderr is shown to the model (the tool already ran).
- If the user ends the turn while a tool runs, `PostToolUse` hooks get 10
  seconds on its result; Esc cancels them at once.

### 6.6 PostToolUseFailure

- **When**: a tool that started running failed. Not for calls rejected
  before running (unknown tool, invalid input, permission denial).
- **Input**: `tool_name`, `tool_input`, `tool_use_id`, `error` (generally
  the text the model receives), optional `is_interrupt`, `duration_ms`.
  For Bash the `error` starts with a line `Exit code N` followed by the
  interleaved output; long text is cut in the middle around
  `... [N characters truncated] ...`; the harness may add lines such as
  `Command timed out after 2m 0s`. Only `tool_name`, `is_interrupt` and the
  first line are stable to match on.
- **Output**: `hookSpecificOutput.additionalContext` (added beside the
  error). Exit 2: stderr shown to the model.

### 6.7 PostToolBatch

- **When**: once after every call in a batch of parallel tool calls has
  resolved, before the next model request. (`PostToolUse` fires per tool and
  may run concurrently.)
- **Input**: `tool_calls`: array of `{ tool_name, tool_input, tool_use_id,
  tool_response }` where `tool_response` is the **serialized** content the
  model sees (for Read: numbered text), not the structured object.
- **Output**: `additionalContext` (injected once before the next model
  call). `decision: "block"` or `continue: false` stops the loop before the
  next model call.

### 6.8 Stop

- **When**: the main agent finished responding. Not after a user interrupt.
  API errors fire `StopFailure` instead.
- **Input**:

  | Field | Meaning |
  | :- | :- |
  | `stop_hook_active` | `true` when the model is already continuing because a stop hook blocked. **The loop guard**: a hook that always blocks must check this (or inspect the transcript) and let the stop through. |
  | `last_assistant_message` | Text of the final response. Use this, not the transcript file, which may lag. |
  | `background_tasks` | In-flight tasks: `{ id, type, status, description, command?, agent_type? }` with `type` such as `shell`, `subagent`, `monitor`. Empty when nothing runs. |
  | `session_crons` | Scheduled wake-ups: `{ id, schedule, recurring, prompt }`. |

- **Output**:

  | Field | Meaning |
  | :- | :- |
  | `decision: "block"` + `reason` | The model does not stop; `reason` (required) tells it why to continue. Shown as a hook error. |
  | `hookSpecificOutput.additionalContext` | Also continues the conversation, but as guidance: labelled as stop-hook feedback, no error notice. |

- Exit 2 = block with stderr as the reason.
- **Continuation cap**: after stop hooks have continued the turn 8 times in
  a row, the next block is overridden and the turn ends. The count resets
  whenever the model calls a tool. `CLAUDE_CODE_STOP_HOOK_BLOCK_CAP` raises
  it.
- `/goal` is the built-in shortcut for a session-scoped, prompt-type Stop
  hook (see `slash-commands-and-skills.md`).

### 6.9 SubagentStop / SubagentStart

- `SubagentStart` — a sub-agent is spawned or resumed. Matcher: agent type
  (`general-purpose`, `Explore`, `Plan`, or a custom agent's `name`).
  Input: `agent_id`, `agent_type`. Cannot block. Output:
  `additionalContext`, placed at the start of the sub-agent's conversation.
- `SubagentStop` — a sub-agent finished responding. Input:
  `stop_hook_active`, `agent_id`, `agent_type`, `agent_transcript_path` (the
  sub-agent's own transcript, in a `subagents/` folder beside the main one),
  `last_assistant_message`, `background_tasks`, `session_crons`. Output: as
  `Stop`; `decision: "block"` keeps the sub-agent running with `reason` as
  its next instruction.
- `SubagentStop` also fires for the harness's own internal helper agents,
  with `agent_type` equal to the session's agent name or an empty string. A
  matcher naming agent types does not match the empty string; an omitted,
  empty or `*` matcher does.
- To put context into the *parent* after a sub-agent returns, use
  `PostToolUse` on `Agent`.

### 6.10 StopFailure

- **When**: the turn ended on a model/API error, in place of `Stop`.
- Matcher and `error` values: `rate_limit`, `overloaded`,
  `authentication_failed`, `invalid_request`, `model_not_found`,
  `server_error`, `max_output_tokens`, `unknown` (plus Anthropic account
  values gnosh never produces).
- **Input**: `error`, optional `error_details`, optional
  `last_assistant_message` (here: the rendered error text).
- Output and exit code are ignored, except `terminalSequence`.

### 6.11 Notification

- **When**: the harness raises a notification; it fires even when desktop
  notifications are switched off.
- Types gnosh needs: `permission_prompt` — a permission prompt has waited
  about **six seconds** with no keystroke (each keystroke defers it; for an
  immediate signal use `PermissionRequest`); `idle_prompt` — the model
  finished about **60 seconds** ago, nothing typed since, no background
  agent running. With MCP elicitation: `elicitation_dialog`,
  `elicitation_url_dialog`, `elicitation_complete`, `elicitation_response`.
  With background sessions: `agent_needs_input`, `agent_completed`.
- **Input**: `message`, optional `title`, `notification_type`.
- Cannot block. `systemMessage` and `continue` are discarded;
  `terminalSequence` is still emitted (that is how a hook raises a desktop
  notification).

### 6.12 PreCompact / PostCompact

- `PreCompact` — before compaction. Matcher/`trigger`: `manual` (`/compact`)
  or `auto`. Input: `trigger`, `custom_instructions` (the text given to
  `/compact`, else `null`). Exit 0 without blocking: **stdout becomes extra
  instructions for the summary** (cut at 10,000 characters). Exit 2 or
  `decision: "block"` blocks compaction: a proactive auto-compaction is
  skipped; one triggered by a context-limit error lets that error surface.
  `systemMessage` and `continue` are discarded.
- `PostCompact` — after compaction. Input: `trigger`, `compact_summary`.
  No decision control.
- After a compaction `SessionStart` fires with `source: "compact"`.

### 6.13 SessionEnd

- **When**: the session ends. Matcher/`reason`: `clear`, `resume` (switched
  away by `/resume`), `logout`, `prompt_input_exit` (the user exited at the
  prompt), `other`.
- **Input**: `reason`. No decision control; JSON output discarded.
- Budget: 1.5 seconds for all `SessionEnd` hooks, raised to the highest
  per-hook `timeout` (up to 60 s), or set by
  `CLAUDE_CODE_SESSIONEND_HOOKS_TIMEOUT_MS`.
- On SIGTERM in `-p`, `SessionEnd` is the only hook that still runs.

### 6.14 InstructionsLoaded

- **When**: a memory file (`CLAUDE.md`) or a rules file
  (`.claude/rules/*.md`) is loaded into context — at session start for
  eagerly loaded files, later for lazily loaded ones. Not when `AGENTS.md`
  is read directly as the project instructions; yes when a `CLAUDE.md`
  imports it.
- Matcher/`load_reason`: `session_start`, `nested_traversal`,
  `path_glob_match`, `include`, `compact`.
- **Input**: `file_path`, `memory_type` (`User`, `Project`, `Local`,
  `Managed`), `load_reason`, `globs` (for `path_glob_match`),
  `trigger_file_path` (lazy loads), `parent_file_path` (`include` loads).
- Runs asynchronously; no decision control; exit code and JSON discarded.

### 6.15 The rest, briefly

| Event | Input beyond common | Output |
| :- | :- | :- |
| `UserPromptExpansion` | `expansion_type` (`slash_command` / `mcp_prompt`), `command_name`, `command_args`, `command_source`, `prompt` | `decision: "block"` + `reason` (shown to the user); `additionalContext`. Fires for a typed `/name` — the path `PreToolUse` on `Skill` does not see. |
| `CwdChanged` | `old_cwd`, `new_cwd` | `watchPaths`; `systemMessage` shown briefly. Has `CLAUDE_ENV_FILE`; variables written there last until the next `CwdChanged`. |
| `FileChanged` | `file_path`, `event` (`change` / `add` / `unlink`) | `watchPaths`. The `matcher` is split on `\|` into literal file names to watch in the working directory. Has `CLAUDE_ENV_FILE`. |
| `DirectoryAdded` | `directory`, `source` | None; `systemMessage` is delivered to the model on the next turn. |
| `ConfigChange` | `source`, `file_path?` | `decision: "block"` keeps the new settings from applying. |
| `Setup` | `trigger` (`init` / `maintenance`) | None. Has `CLAUDE_ENV_FILE`. |
| `PreModelSwitch` | `from_model`, `to_model`, `requested_model`, `source` (`command` / `picker` / `sdk`), plus Anthropic cache-cost fields | `permissionDecision` allow / deny / ask, or `decision: "block"`; a timeout blocks the switch. |
| `PostModelSwitch` | The same, `source` also `auto` or `resume` | `additionalContext` (or plain stdout), delivered with the next request. Cannot block. |
| `MessageDisplay` | `turn_id`, `message_id`, `index`, `final`, `delta` (newly completed lines of assistant text) | `hookSpecificOutput.displayContent` replaces what is drawn; the transcript and the model keep the original. In `-p`: one call per message with the whole text. Timeout 10 s; on failure the original is shown. |
| `WorktreeCreate` | `name` (a slug for the new worktree) | Replaces the default git behaviour: a command hook prints the created directory's path as the last non-empty stdout line; an HTTP hook returns `hookSpecificOutput.worktreePath`. Failure or no path fails the creation. |
| `WorktreeRemove` | `worktree_path` | Exit code only: non-zero with the directory still present fails the removal. |
| `PermissionDenied` | `tool_name`, `tool_input`, `tool_use_id`, `reason` | `hookSpecificOutput.retry: true` tells the model it may try again. Fires only for auto-mode denials. |
| `TaskCreated` / `TaskCompleted` | `task_id`, `task_subject`, optional `task_description`, `teammate_name`, `team_name` | `TaskCreated`: exit 2 or `decision: "block"` cancels the creation. `TaskCompleted`: exit 2 keeps the task open. Either way the message goes back to the model. |
| `TeammateIdle` | `teammate_name`, `team_name` | Exit 2 keeps the teammate working; `continue: false` stops it. |
| `Elicitation` | `mcp_server_name`, `message`, optional `mode`, `url`, `elicitation_id`, `requested_schema` | `hookSpecificOutput.action`: `accept` (with `content` = the form values), `decline` or `cancel`; no output = show the dialog. Matcher: MCP server name. |
| `ElicitationResult` | `mcp_server_name`, `action`, optional `mode`, `elicitation_id`, `content` | May replace or block the user's answer before it goes back to the server. |

## 7. Background (async) hooks

- `"async": true` on a `command` hook: the process starts and the harness
  continues at once. Such a hook **cannot decide anything** — `decision`,
  `permissionDecision` and `continue` have no effect.
- When it exits, `additionalContext` and `systemMessage` from its JSON are
  delivered to the model on the **next** turn (not shown to the user). If
  the session is idle they wait for the next user input.
- `asyncRewake: true`: same, but exit code 2 wakes the model immediately,
  even from idle, with stderr (or stdout) as a system reminder. `timeout`
  is still enforced for these.
- The JSON must be alone on stdout, or alone on one line of it.
- In `-p`, an async hook still running at exit is killed and recorded as
  `cancelled`.
- `timeout` is not enforced on plain `async` hooks.

## 8. Prompt and agent hooks (P2)

- `type: "prompt"`: the event JSON and the configured `prompt` go to a small
  model, which must answer
  `{ "ok": true | false, "reason": "...", "impossible": true | false }`.
  If `$ARGUMENTS` is absent from the prompt the JSON is appended.
- Supported on `PreToolUse`, `PostToolUse`, `PostToolUseFailure`,
  `PostToolBatch`, `Stop`, `SubagentStop`, `UserPromptSubmit`,
  `UserPromptExpansion` among others.
- `ok: false` on `Stop`/`SubagentStop`: `reason` is fed back and the turn
  continues — unless `impossible: true`, which lets the turn end.
- `ok: false` on `PreToolUse`: the call is denied and by default the turn
  ends; `continueOnBlock: true` returns the reason to the model instead.
- `type: "agent"`: the same contract, judged by a sub-agent that may use
  read-only tools; experimental in Claude Code.

gnosh already has its own goal-judging; adopt these only if Jae wants a
second model consulted from settings.

## 9. Hooks and workspace trust

- Interactive session: hooks from every settings file — the user's own
  included — are held until the workspace trust dialog is accepted for the
  folder (or a parent whose trust covers it).
- `-p`: no dialog; the folder counts as trusted and a repository's hooks
  run. See the decision note in `permissions.md`, section 11.
- Hooks in a project *sub-agent's* frontmatter need the folder itself
  trusted and never run in an untrusted `-p` session.

## 10. Observability

- Debug log: `--debug` writes to `~/.claude/debug/<session-id>.txt`;
  `--debug-file <path>` chooses the file. Each command hook leaves a line
  with event, matched value, command, outcome and duration, e.g.
  `Hooks: PostToolUse:Write [.claude/hooks/log-write.sh] finished with status 0 (31ms)`.
  Outcomes also include `timed out after <N>ms`, `cancelled`,
  `moved to the background`, `failed to start`.
- stream-json: `system/hook_started`, `hook_progress` (only for hooks
  running over a second and producing output), `hook_response` — always for
  `SessionStart` and `Setup`, for others with `--include-hook-events`.
  A hook's `systemMessage` arrives as `system/informational`.
- `-p` and long hooks: a progress heartbeat every 30 seconds (v2.1.323).

---

## What gnosh adopts

Checked against the code on 16 November 2026. `[x]` is built, `[~]` is
built in part (what is missing is said), `[ ]` is not built.

**P0**

- [x] The `hooks` settings shape (event → matcher group → handlers), merged
      across settings files, duplicates run once.
- [x] `type: "command"`, shell form and exec form (`args`), `timeout`,
      `${CLAUDE_PROJECT_DIR}` substitution and export.
- [x] Matcher rules of 2.1 (exact / list / unanchored regex / match-all).
- [x] Common input fields: `session_id`, `transcript_path`, `cwd`,
      `hook_event_name`, `permission_mode`, `agent_id`, `agent_type`.
- [x] Exit-code contract (0 / 2 / other), the stdout-JSON recognition rule,
      the stdout × exit table. (gnosh is stricter: a hook that could not
      evaluate blocks, `spec/gnosh/harness-hooks.md`.)
- [x] Universal JSON fields `continue`, `stopReason`, `systemMessage`.
- [x] Events `SessionStart`, `UserPromptSubmit`, `PreToolUse`,
      `PostToolUse`, `PostToolUseFailure`, `Stop`, `SessionEnd` with the
      inputs and outputs above, including `updatedInput`,
      `additionalContext`, `stop_hook_active`, `last_assistant_message`.
- [x] Parallel execution; no controlling terminal for hook processes.
- [x] `disableAllHooks`.
- [x] Unknown names and unbuilt observation-only events are skipped with a
      notice; an unbuilt event that can block, or an unrunnable handler on
      one, stops startup (fail closed); an unevaluable `if` runs the hook.
      (gnosh: every unknown or unbuilt event stops startup, HOOK-2.)

**P1**

- [x] `if` filter with permission-rule matching.
- [~] `PermissionRequest` (with permission update entries), `Notification`
      (`permission_prompt` at 6 s, `idle_prompt` at 60 s), `SubagentStart`,
      `SubagentStop`, `PreCompact`, `PostCompact`, `StopFailure`,
      `InstructionsLoaded`. (All built; a PermissionRequest allow that
      changes the input or the permissions is not applied, and the
      question is shown instead.)
- [~] `async`, `asyncRewake`, `onFailure`, `statusMessage`. (`onFailure`:
      only "continue" is taken; a failing hook already blocks.)
- [~] `terminalSequence` with its allowlist; `CLAUDE_ENV_FILE`.
      (`CLAUDE_ENV_FILE` is built; a terminal sequence is noted and not
      written.)
- [~] `updatedToolOutput`; the 10,000-character cap with file overflow.
      (`updatedToolOutput` and `updatedMCPToolOutput` are built, read as
      text; one gnosh cannot read withholds the output, DECISIONS 70. The
      cap with file overflow is not.)
- [~] `/hooks` browser; hooks in skill and sub-agent frontmatter; `once`.
      (`/hooks` is a read-only browser, DECISIONS 78; skills' hooks and
      `once` are built.)
- [~] Debug-log lines; `hook_*` stream-json events. (Each hook's record
      is in the log; no stream-json events.)
- [x] Hooks held until workspace trust in interactive sessions.

**P2**

- [~] `PostToolBatch`, `UserPromptExpansion`, `CwdChanged`, `FileChanged`,
      `DirectoryAdded`, `ConfigChange`, `Setup`, `PreModelSwitch`,
      `PostModelSwitch`, `MessageDisplay`, `WorktreeCreate`,
      `WorktreeRemove`, `Elicitation`, `ElicitationResult`. (Built: all but
      `Setup`, `MessageDisplay`, `WorktreeCreate`, `WorktreeRemove`,
      `Elicitation` and `ElicitationResult`.)
- [~] `type: "http"`, `"mcp_tool"`, `"prompt"`, `"agent"`. (`http` and
      `mcp_tool` are built; `prompt` and `agent` need a second model.)
- [ ] `permissionDecision: "defer"`.

## Deliberately not adopted

- `PermissionDenied` and `classifierContext` (auto-mode classifier).
- `TaskCreated`, `TaskCompleted` (task-list tools; gnosh uses `mem`),
  `TeammateIdle` (agent teams).
- Plugin hooks (`hooks/hooks.json`, `${CLAUDE_PLUGIN_ROOT}`,
  `${CLAUDE_PLUGIN_DATA}`, `${user_config.*}`), mods, managed-settings locks
  (`allowManagedHooksOnly`, `strictPluginOnlyCustomization`).
- `shell: "powershell"` and the Windows-without-Git-Bash failure rules.
- Anthropic-account notification types (`auth_success`,
  `quota_auto_resume_*`, `plugin_notification`) and `CLAUDE_CODE_REMOTE`.
- The SessionStart cost fields that assume Anthropic prompt caching
  (`prompt_cache_likely_expired`, `estimated_cache_write_usd`).

## Decision for Jae

**The stop-hook continuation cap.** Claude Code ends the turn after stop
hooks have forced 8 continuations in a row with no tool call in between.
Jae's standing rule is "no give-up caps". This one only trips when the model
is spinning without doing anything, so the recommendation is to keep it as a
loop guard, default 8, with `CLAUDE_CODE_STOP_HOOK_BLOCK_CAP` (and a
`gnoshrc` key) to change it and `0` meaning no cap.

## Unverified (docs silent)

- The exact text wrapped around `additionalContext` when it is given to the
  model (gnosh writes its own wrapper).
- Whether `SessionStart` hooks in `-p` block the first request (gnosh: yes,
  they are awaited).
- Whether `Notification` hooks are awaited (gnosh: not awaited).
- The JavaScript-regex features existing matchers rely on beyond RE2.
