# harness-hooks.md — hooks: events, protocol, fail-closed, the Stop hook

*Since 14 November 2026 gnosh is configured by one JSON file, `gnosh.json`, and no longer by `gnoshrc` (`spec/DECISIONS.md`, items 17 and 23 to 28). Where this file speaks of `gnoshrc`, of its directives or of its dashed option names, it records what the build was first given.*

What this file is: what the earlier gnosh's hooks did by the end, as
requirements for the new one. Claude Code's hook protocol itself (the config
shape, the JSON a hook reads and writes, the exit codes) is in
`spec/cc/hooks.md`. This file says which of Claude Code's events gnosh fires
and when, and where gnosh differs on purpose: a hook that could not check
**blocks**, a config entry gnosh cannot honour **stops startup**, a guard
covers gnosh's extra tools, and a Stop hook can keep a turn going **with no
cap**, with checkpoints where the model judges whether to stop.

In one paragraph: a hook is a command of the user's that gnosh runs at a named
moment (before a tool call, when a prompt is sent, when a turn is about to
end...). It reads a JSON description of the moment on stdin and answers with
its exit code and, optionally, JSON on stdout. Jae uses hooks as **gates**:
"the tests must pass before you stop", "the video you say you rendered must
exist". So the two things that matter most are that a gate which did not run
never counts as passed, and that a gate can hold a turn open for as long as
the work takes.

Read first: `harness-00-principles.md` (PRIN-4, PRIN-6), `spec/cc/hooks.md`.

History, for dates: only PreToolUse existed on 2026-10-21. Stop and strict
event names came 2026-10-22 at Jae's words "make the hook system not
normalize, make it error or panic on hooks it doesn't recognize, and implement
the post hook we need". Fail-closed came 2026-10-23. The other events were
added between 2026-10-22 and 2026-10-25, each "as in Claude Code".

---

## 1. Events

### HOOK-1 The fifteen events gnosh fires (P1 unless marked)

"Top" = only for the main agent. "All" = for sub-agents' work too.

| Event | Fires | Matcher is tested against | Can it block? | Scope | Pri |
| :- | :- | :- | :- | :- | :- |
| `PreToolUse` | Before every tool call | The tool's name | Yes: the call does not run. It can also approve the call, put it to the user, change its input, or add context. | All, and the call that starts a sub-agent | P1 |
| `PostToolUse` | After a tool call succeeds | The tool's name | The call has run. A block hands the reason to the model as feedback after the tool's output. | All | P1 |
| `PostToolUseFailure` | After a tool call returns an error the model reads | The tool's name | As PostToolUse, after the error. | All | P1 |
| `UserPromptSubmit` | When the user sends a prompt, before the model sees it | Nothing (a matcher is a config error) | Yes: the prompt is not sent. It can add context to the prompt. | Top | P1 |
| `SessionStart` | Before the first prompt a process sends to a session, and again after a summary | The source: `startup`, `resume`, `clear`, `compact`, `fork` | No: an objection is a warning. It can add context and set the environment of the session's commands. | Top | P1 |
| `Stop` | When the main agent is about to end its turn | Nothing | Yes: the turn goes on, with the reason as the model's next instruction. | Top | P1 |
| `SubagentStart` | When a sub-agent starts, before its first prompt | The sub-agent's type | No. It can add context to the sub-agent's first prompt. | Sub-agents | P2 |
| `SubagentStop` | When a sub-agent is about to finish | Nothing | Yes, as Stop, for the sub-agent. | Sub-agents | P2 |
| `StopFailure` | Instead of Stop, when a turn ends on an error from the model's API | The error's kind | No. | Top | P2 |
| `PreCompact` | Just before a conversation is summarized | The trigger: `manual` or `auto` | No: an objection is a warning and the summary goes ahead. | Top | P2 |
| `PostCompact` | Once it has been summarized, with the summary | The trigger | No. | Top | P2 |
| `SessionEnd` | When a session ends | The reason: `clear`, `other`, `prompt_input_exit` | No. | Top | P2 |
| `InstructionsLoaded` | When a context file is loaded | The load reason | No; it is not waited for. | Top | P2 |
| `PermissionRequest` | When a permission question is about to be shown | The tool's name | It answers the question in the user's place: allow or deny. | All (unconfirmed for sub-agents) | P2 |
| `PreModelSwitch` | Before a switch the person asked for (`/model`, its picker) is made | The model switched to, as provider/model or by its own name | Yes: the model stays. `"ask"` puts the switch to the person; in a run, nobody can answer, so it is a refusal. | Top | P2 |
| `ConfigChange` | When a settings file (the person's gnosh.json, the project's, its local file) changes while the session runs, before the change is taken in | The source: `user_settings`, `project_settings`, `local_settings` | Yes: none of the new settings is taken in, and the person is told. | Top | P2 |
| `PostToolBatch` | Once after every tool call of a reply has finished, before the model is asked again; not when the round itself ends the turn | Nothing | Yes: the model is not asked again, and the reason is kept in the conversation. It can add context next to the results. | All | P2 |
| `FileChanged` | When a watched file is made, changed or removed, whatever did it: the files the matchers name (split on `\|`, in the folder the session started in) and the `watchPaths` a SessionStart, CwdChanged or FileChanged hook gave | The file's name | No. A systemMessage is shown; it may write to the environment file and give watchPaths. | Top | P2 |
| `Notification` | On a notification gnosh raises: a permission question that has waited, a finished turn with no reply | The type: `permission_prompt`, `idle_prompt` | No; it is not waited for. | Top | P2 |

### HOOK-2 Event names are exact, and an unknown one stops startup (P1)

`PreToolUse` is an event. `pretooluse`, `pre_tool_use` and `PRE_TOOL_USE`
are startup errors that list the fifteen names. So is any CC event gnosh
does not fire (HOOK-3). The reason, from the bug that started it: a `Stop`
hook written into the config of the 2026-10-21 program loaded without a
word and never ran. A gate that looks configured and is dead is worse than
no gate.

### HOOK-3 Claude Code events gnosh does not fire yet (P2)

Claude Code 2.1.310 had 33 events. On 16 November 2026 gnosh has 23 of
them; the 10 it lacks are WorktreeCreate, WorktreeRemove, MessageDisplay,
Setup, PermissionDenied, TaskCreated, TaskCompleted, TeammateIdle,
Elicitation and ElicitationResult. The list as it stood when this was
written, best fit first:

1. `DirectoryAdded` (gnosh has `/add-dir`; work on it had begun and was not
   landed). CC runs it in the background after the directory is added, with
   `directory` and `source` (`slash_command`); it cannot block or undo.
2. `PreModelSwitch`, `PostModelSwitch` (gnosh has `/model`). Both are
   built (PostModelSwitch on 2026-11-13, PreModelSwitch on 2026-11-16).
3. `PostToolBatch` (once after every call of a batch has finished). Built
   on 2026-11-16 (the agent loop's AfterRound moment).
4. `PermissionDenied`.
5. `CwdChanged`, `FileChanged`. Both built (FileChanged on 2026-11-16,
   by looking at the watched files twice a second).

Not gnosh features: `Setup`, `TeammateIdle`, `TaskCreated`,
`TaskCompleted`, `Elicitation`, `ElicitationResult`, `WorktreeCreate`,
`WorktreeRemove`, `MessageDisplay`. (`UserPromptExpansion` and
`ConfigChange` were on this list and are built now: gnosh has typed
commands that expand, and watches its settings files.)

Also missing from every payload: CC's `transcript_path`. The new gnosh keeps
sessions as files, so it should be given (P1).

---

## 2. Configuration

### HOOK-4 Two ways to write a hook, and what is refused (P1)

In `gnoshrc`:

```sh
hook add Stop --name tests --command "./hooks/tests-must-pass.sh" --timeout 1800
hook add PreToolUse --matcher "Bash" --command "./hooks/no-force-push.sh"
```

In JSON settings, Claude Code's own block, pasted as it is:

```json
{
  "hooks": {
    "PreToolUse": [
      { "matcher": "Bash",
        "hooks": [ { "type": "command", "command": "./hooks/no-force-push.sh", "timeout": 10 } ] }
    ]
  }
}
```

Rules:

- gnosh runs `command` hooks and `http` hooks. An `http` hook POSTs the
  event as JSON to its `url`, with its `headers` (in whose values `$NAME`
  is a variable `allowedEnvVars` names, and nothing for any other), and
  reads the body of the answer as a command's stdout; an answer with
  another status than 2xx, or none in time, is a hook that could not
  check, which blocks where a hook can block (`spec/DECISIONS.md`, item
  37).
- `statusMessage` is shown in place of the working line's own words while
  the hook runs, as CC's spinner text is.
- Fields of a CC hook that are **accepted and change nothing**: `once`,
  and the default values of `shell` (`bash`) and `onFailure` (`continue`).
- `args` is Claude Code's exec form, run as written: `command` is the
  program, found on the hook's PATH unless it holds a slash, and each
  element of `args` is one argument that no shell reads;
  `${CLAUDE_PROJECT_DIR}` (and `${GNOSH_PROJECT_DIR}`) in either is the
  project's directory (15 November 2026).
- `async` and `asyncRewake` run the hook in the background, as CC does: it
  decides nothing, what its answer adds for the model goes out with the
  next request, and with `asyncRewake` an exit with 2 wakes the model with
  its stderr. One that cannot be evaluated is a warning. The session's end
  stops those still running (`spec/DECISIONS.md`, item 35).
- `mcp_tool` hooks call a tool of a connected MCP server: `server`, `tool`,
  and `input` (its arguments, in whose strings `${field.path}` is that
  field of the event); the tool's text is read as a command's stdout, and
  a tool that fails or cannot be called is a hook that could not check.
- Anything else a CC hook can ask for is a **startup error that names it**:
  another `type` (`prompt`, `agent`), a matcher on an
  event that takes none (CC ignores it), a matcher gnosh's regular
  expressions cannot read. (The `if` filter is built:
  `spec/DECISIONS.md`, item 32.) The
  reasoning: a guard run some other way than written may fail open. To
  support `if`, gnosh would have to match commands exactly as CC does.
- A relative `command` is found from the **working directory**, not from the
  config file. In the user's global config it must be an absolute path or an
  inline command.
- Identical commands configured twice for one event run once.

### HOOK-5 Matchers read as Claude Code's (P1)

- Empty, or `*`: everything.
- Only letters, digits, `_`, `-`, spaces, `,` and `|`: exact names separated
  by `|` or `,` (`Bash`, `Edit|Write`). `Bash` does not match `BashOutput`.
- Anything else: a regular expression, unanchored (`mcp__github__.*`).

A matcher that can match no tool gnosh has is logged as a warning.

### HOOK-6 Tool names in matchers and payloads (P1; OQ-10)

The earlier gnosh's tools had lower-case names of their own (`bash`, `view`,
`edit`...). Matchers knew both those and CC's names, today's and older ones
(`Read`, `Bash`, `Edit`, `MultiEdit`, `Write`, `Glob`, `Grep`, `LS`,
`WebFetch`, `WebSearch`, `Agent`, `Task`, `AskUserQuestion`, `BashOutput`,
`TaskOutput`, `KillShell`, `TaskStop`, and the MCP forms
`mcp__server__tool`), so a CC hook worked unchanged. The payload's
`tool_name` was gnosh's own name.

The new gnosh's tools carry CC's names (ARCHITECTURE.md), so `tool_name` is
CC's name and CC hooks that test it work. The older CC names above still
match the tool that does that job now.

### HOOK-7 Timeouts (P1)

Claude Code's defaults: 600 seconds for a command hook; 30 for
`UserPromptSubmit`, which holds up the prompt just sent; 1.5 for
`SessionEnd`, so that quitting is not held up (a hook's own `timeout` is
honoured there up to 60). When a hook passes its timeout it is stopped, with
its whole process group, and counts as a hook that could not evaluate
(HOOK-10).

---

## 3. How a hook runs

### HOOK-8 Order (P1)

For one event:

1. Pick the hooks whose matcher fits.
2. Run them **at the same time**.
3. Wait for all of them (or their timeouts).
4. Combine their answers **in the order they are configured**, so the
   outcome never depends on which finished first:
   - a block from any hook wins over an "ask", which wins over an approval,
     which wins over no opinion;
   - a hook that ends the turn (`"continue": false`) ends it whatever the
     others said;
   - reasons and added context are joined in config order.
5. For `PreToolUse`, the outcome is applied **before** the permission check:
   a block means no question is shown; an approval means no question is
   needed; no opinion goes on to the normal check.

### HOOK-9 What a hook's answer means (P1)

The exit code and JSON are CC's (`spec/cc/hooks.md`), plus one code of
gnosh's own:

| Exit | Meaning |
| :- | :- |
| 0 | Ran. stdout is read: JSON, or for `UserPromptSubmit` and `SessionStart` plain text that becomes context. |
| 2 | **Block**, with stderr as the reason. |
| 3 | **Could not evaluate** (gnosh's own; 3 is "unknown" in the monitoring-plugin convention, beside 0 "OK" and 2 "critical"). Blocks as 2 does, and says the check could not run. |
| 126, 127, above 128 | The hook could not start, a command in it was not found, or it was killed. As 3. |
| Any other non-zero | **With text on stderr**: the hook ran into trouble and did not check. As 3, with the text as the reason. **Silent**: a test that came out false; no opinion, and nothing is shown. |

Why "other" is split by what the hook said (decided 2026-10-23, when Jae
answered "whatever makes for a better harness, use best judgement and think
of alternative solutions to find the best one"): blocking on every non-zero
exit would break common one-line hooks such as
`[[ $file == *.go ]] && gofmt -w "$file"`, which exit 1 on every file that is
not Go. Text on stderr (a traceback, "No such file", a tool's error) is what
tells a failure from a false test. The cost, accepted: a hook that logs to
stderr and then ends on a false test blocks, loudly, with its log line as the
reason. The guide tells authors to end such a hook with its verdict.

A hook that only wants to **warn** exits 0 with CC's
`{"systemMessage": "..."}`. gnosh shows the message to the user and blocks
nothing.

Decision words are matched exactly: `allow`, `deny`, `ask`, and CC's
top-level `approve` and `block`. `"Deny"` or `"reject"` is an answer gnosh
could not read (HOOK-10). A hook that wrote `"Deny"` meant to deny.

### HOOK-10 Could not evaluate: gnosh fails closed (P1)

This is the main difference from Claude Code, which lets all of these pass.

A hook **could not evaluate** when any of these is true:

- it exited 3;
- it never started (the script path is wrong, the file is not executable);
- it exited 126 or 127 (also when a command inside the script is missing);
- it was killed by a signal;
- it passed its timeout;
- it exited with another non-zero code and wrote a reason to stderr;
- it exited 0 with JSON gnosh cannot read: a decision word it does not know,
  a changed input that is not a JSON object, a field of the wrong type.

A turn cancelled by the user is not a hook failure.

What happens then, per event:

| Event | A hook that could not evaluate |
| :- | :- |
| `PreToolUse` | The call is blocked. The model reads that the hook *could not check this*, with the hook's own words, and that a check which could not run does not pass. |
| `PostToolUse`, `PostToolUseFailure` | The reason goes to the model as feedback. |
| `UserPromptSubmit` | The prompt is blocked and comes back into the editor with the reason. |
| `Stop`, `SubagentStop` | The agent keeps working. The user is told the turn goes on because a check that could not run does not pass. The reflection checkpoint (HOOK-21) is the way out: the model may judge that the check cannot run here, say why, and stop. So failing closed can never trap a turn for ever. |
| `PermissionRequest` | No answer. The question is shown to the user, with a note saying why the hook did not answer. |
| `SessionStart`, `PreCompact`, `PostCompact`, `SubagentStart` | These cannot block. One warning; the prompt, summary or sub-agent goes ahead without the hook. |
| `SessionEnd`, `StopFailure`, `InstructionsLoaded`, `Notification` | Logged. |

When a hook that could not evaluate also gave a verdict of its own (a deny
alongside an unreadable field), **the deny holds**.

On screen the hook's line says that it could not evaluate. It must never read
as OK. `gnosh doctor` reports a hook that cannot run as an error wherever it
would block (CFG-60).

Worked example. A gate that checks a rendered video for sound:

```sh
#!/bin/sh
# The check needs ffprobe. Without it, say so: do not pass the work unchecked.
command -v ffprobe >/dev/null || { echo "ffprobe is not installed, so the audio was not checked" >&2; exit 3; }
ffprobe -v error -select_streams a -show_entries stream=codec_type -of csv=p=0 "$1" | grep -q audio \
  || { echo "$1 has no audio stream" >&2; exit 2; }
```

With `ffprobe` missing, the model reads that the hook could not check, not
that the video is wrong, and the work is held either way.

---

## 4. Per-event behaviour beyond Claude Code's documentation

Each item here was a bug or a decision in the earlier gnosh.

### HOOK-11 PreToolUse (P1)

- An **approval** from a hook skips the permission question. No opinion
  never does.
- **`ask`** puts the call to the user with the hook's reason, **even when
  the call would otherwise run without asking** (permissions skipped, an
  allowed tool, `acceptEdits`). Once the user allows it, it runs with no
  second question. Where nobody can answer (`gnosh run`), the call is
  refused and the model reads the reason, unless a `PermissionRequest` hook
  allows it. (The earlier gnosh first read `ask` as no opinion, so the call
  ran unasked.)
- A changed input that is not a JSON object voids the approval given with
  it: the call goes to the normal permission check rather than running
  approved without the rewrite it was approved with.
- The sub-agent tool's own call is hooked, so "never start a sub-agent" is a
  policy a hook can enforce.

### HOOK-12 PostToolUse and formatters (P1)

A hook that reformats the file the agent just wrote would make the agent's
next edit of it fail as "changed since it was read". So after a PostToolUse
hook runs on a file-changing tool, gnosh records a fresh read of that file
and tells the model the hook changed it, so that it views the file again
before editing near what changed. CC does the same.

The payload carries what the tool returned (`tool_response`) and how long the
call took (`duration_ms`); PostToolUseFailure carries the error text the model
reads (`error`) and whether the call failed because the turn was cancelled
(`is_interrupt`).

### HOOK-13 Hooks see sub-agents' tool calls (P1)

Tool hooks run on every tool call, a sub-agent's included. The earlier gnosh
did not at first, so a hook that refused to read a key file could be walked
around by handing the read to a sub-agent. For a sub-agent's call the
payload's `session_id` is the session the user is in, and `agent_id` and
`agent_type` name the sub-agent. A hook that halts inside a sub-agent ends
the **parent's** turn too, with a line saying the sub-agent was stopped and
why.

### HOOK-14 A guard also covers gnosh's own tools, for blocking only (P1)

gnosh has tools CC has nothing like. A guard written for CC must not be
walked around through them, and an approval written for CC must not be
widened by them.

| A matcher naming | Also matches gnosh tools that | Because |
| :- | :- | :- |
| `Edit` | change files in other ways (several edits at once; a rename or symbol replacement through a language server) | They edit. |
| `WebFetch` | save a URL to a file; fetch a URL and pay for it | They fetch. |
| `Write` | save a URL to a file | It writes one. |
| `WebSearch` | search code on the web | They search. |

A hook that matched **only** through this table is a guard: it can block the
call, add context or change the input, but its approval counts as no
opinion, and the call asks as usual. What it checked before approving was
another tool's input. To approve one of these tools, the hook names it.

The rule behind the table: **a name added this way can only make gnosh
stricter than before, never looser.**

### HOOK-15 UserPromptSubmit (P1)

- What the hook prints (plain text, or CC's `additionalContext`) reaches the
  model after the prompt, set apart in a tag that marks it as a prompt
  hook's output (the earlier gnosh used `<user-prompt-submit-hook>`). The
  user sees only what they typed.
- **The added context is stored with the prompt** and sent byte-for-byte the
  same on every later round, so a local model server's cache still matches
  (LM-10).
- It runs **once, when the prompt is sent**. A prompt sent while the agent
  works runs its hook then, waits in the queue with what the hook added, and
  does not run the hook again when its turn comes. Prompts keep the order
  they were sent in, however long each one's hooks take.
- While the hook runs the session counts as busy, Esc stops the hook, and a
  prompt cancelled in its hooks is a cancelled prompt.
- A blocked prompt stores nothing and calls no model. Interactively it comes
  **back into the editor** with its attachments and the reason. In a run it
  is exit 1 with the reason. A prompt refused while another turn is running
  leaves that turn alone.
- The hook's `prompt` field is the prompt **as the model will read it**: the
  typed text followed by the text of any pasted or attached text file. (A
  long paste becomes an attachment; a hook looking for secrets must see it.)
  `attachments` lists the file names.
- It does not run for prompts gnosh sends itself (a Stop hook's reason,
  `/loop`, a checkpoint, a background job's end notice) or for a sub-agent's
  prompt.

### HOOK-16 SessionStart (P1)

- Sources: `startup` (a new session, the first this process has started),
  `resume` (the session already has messages), `clear` (a new session in a
  process that has already had one), `compact` (the first prompt after a
  summary), `fork` (a copy made by `/branch` or `--fork-session`).
- It runs **before** that prompt's `UserPromptSubmit` hooks, and what it
  prints comes first.
- It runs once per start however many prompts are sent at once.
- If a `UserPromptSubmit` hook blocks the first prompt, what SessionStart
  printed is kept and sent with the next prompt: neither lost nor sent
  twice.
- The payload names the model.
- A resumed session must not be told that every file it read has changed.

### HOOK-17 PreCompact, PostCompact (P2)

`PreCompact` carries `trigger` and `custom_instructions` (the text typed
after `/compact`; empty for an automatic summary). `PostCompact` carries
`trigger` and `compact_summary`, and runs after the summary is saved and
before any prompt queued meanwhile. Neither can stop the summary. A summary
is never refused because a prompt is waiting in its hooks.

### HOOK-18 SessionEnd (P2)

Reasons: `clear` (the user started a new session), `other` (switched to
another session; a run ended; the process was stopped by a signal),
`prompt_input_exit` (the user quit). A session ends once: switching away and
then quitting does not end it twice, and a session that never had a message
does not end at all. It runs on SIGINT and SIGTERM too (CLI-43).

### HOOK-19 PermissionRequest (P2)

It fires only when a question is really about to be shown: not for a call
that needs no permission. `PreToolUse` hooks have already run. It answers
with CC's `hookSpecificOutput.decision`: `{"behavior": "allow"}`, or
`{"behavior": "deny", "message": "..."}` with `"interrupt": true` to end the
turn. A hook's deny lets the model read the message and go on; a deny the
user gives in person ends the turn. Exit 2 is "no answer" for this event.
`updatedInput` and `updatedPermissions` are not applied: the question is
shown instead, as the call stands.

### HOOK-20 Notification (P2)

`permission_prompt`: a permission question has waited about six seconds with
no key pressed (each key starts the wait again). `idle_prompt`: a turn
finished about a minute ago and nothing has been typed since. They fire
whether or not desktop notifications are on. The timers exist only when a
Notification hook is configured. The idle notice names the session whose
turn finished, not whichever is on screen.

---

## 5. The Stop hook

This is the hook Jae asked for by name, and the one his own files use.

### HOOK-21 Refusing to let a turn end, with no cap (P1)

When the main agent is about to end its turn, every Stop hook runs.

- **Block** (exit 2, or CC's `{"decision": "block", "reason": "..."}`): the
  turn stays alive. The hook's reason becomes the model's next instruction.
  It is sent as a prompt of gnosh's own: the model sees it, the conversation
  does not show it as something the user typed, and Up-arrow history does
  not offer it.
- **Could not evaluate**: as a block (HOOK-10), and the user is told why the
  turn goes on.
- **End the turn** (`"continue": false`): the turn ends now, whatever
  another Stop hook said.
- **Otherwise** the turn ends.

There is **no limit** on how many times in a row a Stop hook may block
(PRIN-4; Jae, 2026-10-22: "if it's not done, it's not done"). The earlier
gnosh briefly ended the turn after ten blocks; that was removed the same
day.

**The reflection checkpoint.** At every 10th block in a row, the instruction
sent to the model is different: it is asked to judge honestly whether there
is a genuine reason to stop, namely

- the task cannot be done as asked;
- it is blocked on something only the user can provide or decide;
- the check cannot pass in this environment;
- it is repeating itself without progress.

If it finds one, it explains why and ends its reply with a marker on a line
of its own:

```
<!-- GNOSH_STOP -->
```

Then gnosh ends the turn and shows the user a warning that quotes **the
model's reason and the hook's last objection**. If it finds none, it keeps
working, and the count goes on to the next checkpoint.

Rules for the marker:

- It counts **only in the reply to a checkpoint**. At any other time the
  hook stays in charge, and the marker is just text.
- It is not shown to the user.
- It is judged on a **finished reply**, never on text still streaming. (A
  line that only begins with the marker must not latch.)
- The checkpoint instruction must ask for the decision first, so that the
  model answers the question rather than carrying on. Its wording was
  tested against the real model with a tenth block whose check could not
  pass, and by replaying real requests rather than short made-up ones;
  re-test any new wording the same way.

A hook that wants to object once and then stand down reads
`stop_hook_active` in its input (true when the turn is only still running
because a Stop hook blocked it). The input also carries
`last_assistant_message`, the text the turn is about to end on, so a hook can
judge the answer without reading the session.

### HOOK-22 What does not go to a Stop hook (P1)

- Sub-agents (they have `SubagentStop`, which works the same way, with the
  same checkpoints, and hands the parent the answer the sub-agent ended on
  *after* its hooks let it end).
- A turn that ended on a provider error (`StopFailure` instead).
- A turn that was **halted**: the user denied a permission, a hook ended the
  turn, a tool said to stop. A halt is final. (A Stop hook that objected
  after a deny once restarted the turn.)
- A turn the user cancelled.
- The request that writes a summary.

And every way a Stop hook can end or prolong a turn is visible to the user
(PRIN-8): it let the turn end; it blocked; it ended the turn; it could not
evaluate; the model chose to stop at a checkpoint, with its reason.

Worked example, the gate in Jae's working project. The model once reported a
rendered video of "1 091 784 KB" after three `ls` calls and no render. The
Stop hook now pulls every video file name out of the reply, lists the folder
where renders land, and blocks with the names that are not there. A false
claim blocks; a true claim passes; a reply that claims nothing passes.

### HOOK-23 SessionStart can set the environment of the session's commands (P2)

As CC: a SessionStart hook is given a file path in `CLAUDE_ENV_FILE` (and
`GNOSH_ENV_FILE`). `export` lines appended to it are run before each shell
command of the session and of its sub-agents. The file is the session's own,
kept in gnosh's state directory (never in the project), and lasts as long as
the session. Permission questions and PreToolUse hooks see the command
alone, not what the file adds.

---

## 6. What a hook is given

### HOOK-30 Environment (P1)

| Variable | Value |
| :- | :- |
| `GNOSH` | `1` |
| `AGENT`, `AI_AGENT` | `gnosh` (so a script can tell it is being run by an AI agent; the shell tool sets the same three) |
| `GNOSH_EVENT` | The event name |
| `GNOSH_SESSION_ID`, `GNOSH_CWD` | The session, the working directory |
| `GNOSH_PROJECT_DIR`, `CLAUDE_PROJECT_DIR` | The project root, under both names, so CC hook commands such as `"${CLAUDE_PROJECT_DIR}/.claude/hooks/check.sh"` work unchanged |
| `GNOSH_TOOL_NAME`, `GNOSH_TOOL_USE_ID` | Tool events: the tool, and the call's ID (the same for a call's Pre and Post hooks) |
| `GNOSH_TOOL_INPUT_COMMAND`, `GNOSH_TOOL_INPUT_FILE_PATH` | For a shell call, the command; for a file tool, the path |
| `GNOSH_STOP_HOOK_ACTIVE` | `Stop`, `SubagentStop`: `1` or `0` |
| `GNOSH_AGENT_ID`, `GNOSH_AGENT_TYPE` | A sub-agent's events and calls |
| `GNOSH_SESSION_START_SOURCE`, `GNOSH_SESSION_END_REASON`, `GNOSH_PRE_COMPACT_TRIGGER`, `GNOSH_POST_COMPACT_TRIGGER`, `GNOSH_STOP_FAILURE_ERROR`, `GNOSH_INSTRUCTIONS_LOAD_REASON`, `GNOSH_NOTIFICATION_TYPE` | The event's own value |
| `GNOSH_ENV_FILE`, `CLAUDE_ENV_FILE` | `SessionStart` only (HOOK-23) |
| `GNOSH_MEM_STATUS` | The path of a JSON file with what the project's `mem` holds, rewritten after every change (the other spec set, `integration-contract.md`). It is set before `mem` is first used, so test that the file exists. |

The earlier gnosh also set every one of these under two older prefixes.
Those are not carried over (PRIN-5), unless one of Jae's own hook scripts
turns out to read one (unconfirmed; OQ-21).

### HOOK-31 Input on stdin (P1)

Claude Code's fields, as in `spec/cc/hooks.md`: `hook_event_name`,
`session_id`, `cwd`, `transcript_path`, and `permission_mode` on the events
CC gives it to (`PreToolUse`, `PostToolUse`, `PostToolUseFailure`,
`UserPromptSubmit`, `Stop`, `SubagentStop`, `PermissionRequest`).

`permission_mode` is `plan` for the plan agent, `bypassPermissions` when
every request is granted (`-y`, or a `gnosh run`), `acceptEdits`, `dontAsk`,
else `default`. A sub-agent's hooks get the mode of the agent that started
it.

Per event, beyond CC's documentation:

| Event | Fields |
| :- | :- |
| Tool events | `tool_name`, `tool_use_id`, `tool_input`; for a sub-agent's call also `agent_id`, `agent_type` |
| `PostToolUse` | `tool_response` (at least the text the model reads; for a shell call the exit code), `duration_ms` |
| `PostToolUseFailure` | `error`, `is_interrupt`, `duration_ms` |
| `UserPromptSubmit` | `prompt`, `attachments` |
| `SessionStart` | `source`, `model` |
| `SessionEnd` | `reason` |
| `Stop`, `SubagentStop` | `stop_hook_active`, `last_assistant_message`; for `SubagentStop` also `agent_id`, `agent_type` |
| `StopFailure` | `error` (the kind: `rate_limit`, `overloaded`, `authentication_failed`, `billing_error`, `model_not_found`, `invalid_request`, `server_error`, `unknown`), `error_details`, `last_assistant_message`. A model server that refused the connection, never answered or timed out is `server_error`, and the hook fires only once gnosh has given up waiting for it (LM-4). |
| `PreCompact` | `trigger`, `custom_instructions` |
| `PostCompact` | `trigger`, `compact_summary` |
| `InstructionsLoaded` | `file_path`, `memory_type` (`User`, `Project`, `Local`), `load_reason` (`session_start`, `nested_traversal`, `include`, `compact`), `trigger_file_path`, `parent_file_path` |
| `PermissionRequest` | `tool_name`, `tool_input` |
| `Notification` | `message`, `title`, `notification_type` |

### HOOK-32 Output (P1)

Claude Code's JSON, read in full: the top-level `decision` (`approve`,
`block`), `reason`, `continue` and `stopReason`, `systemMessage`, and
`hookSpecificOutput` with `permissionDecision` (`allow`, `deny`, `ask`),
`permissionDecisionReason`, `additionalContext`, `updatedInput` and, for
`PermissionRequest`, `decision`. Top-level fields count beside
`hookSpecificOutput`: a hook that blocks at the top level and adds context
inside it gets both. Where both give a decision, `permissionDecision` wins.
`additionalContext` may be a string or a list of strings.

A field of the wrong type is named in the error, and a deny given beside it
still holds.

---

## 7. What the user sees

### HOOK-40 Hooks are never invisible (P1)

- Each tool call shows which hooks ran on it and what each said: nothing,
  approved, blocked (with the reason), feedback, a warning, or could not
  evaluate.
- For events with no tool line (Stop, SessionStart, UserPromptSubmit,
  PreCompact...), a block, a warning or a could-not-evaluate is shown as a
  warning in the session, and on stderr in a run (CLI-42).
- A warning is not the end of a turn. It must not make the interface think
  the agent has gone idle.
- `/hooks` lists the hooks in force, per event (read-only, unlike CC's).

---

## 8. Inherited extras that are not carried over (OQ-24)

The earlier gnosh's hook protocol came from its upstream and had things of
its own beside Claude Code's. Jae never asked for them and none of his hooks
is known to use them (unconfirmed):

- an exit code that ended the turn (CC: `"continue": false`);
- a second JSON envelope (a truth-valued halt field, a `context` field, a
  changed-input field under another name, a version number, top-level
  `allow` and `deny`);
- changed input **merged into** the call's input rather than replacing it;
- the event name repeated under a second key.

Recommended reading: follow Claude Code for all four, and keep only what is
gnosh's own on purpose, which is exit 3 and everything in sections 3 to 5.

---

## 9. Priorities

- **P1 (before Jae switches):** `Stop` with checkpoints; `PreToolUse`;
  `PostToolUse` and `PostToolUseFailure`; `UserPromptSubmit`; `SessionStart`
  (context only); fail-closed; strict config; CC's block format and
  matchers; `CLAUDE_PROJECT_DIR`; hooks on sub-agents' calls; the guard
  table; warnings in a run; `doctor`'s hook checks.
- **P2:** the other nine events, `CLAUDE_ENV_FILE`, `/hooks`, the missing CC
  events in the order of HOOK-3.
- **P0:** none. The first binary may ship without hooks, **provided it
  refuses to start when the config names one** (PRIN-6): a configured gate
  that silently does not run is the bug this whole file exists to prevent.
