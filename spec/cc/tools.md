# tools.md — built-in tools: names, inputs, results, limits

What this file is: the tools the model can call, with the exact names and
argument names Claude Code uses, so that permission rules, hook matchers and
`--allowedTools` lists written for Claude Code mean the same thing in gnosh,
and so that a model which already knows these tools finds them where it
expects.

In one paragraph: there are six tools that matter on day one — `Read`,
`Write`, `Edit`, `Bash`, `Grep` and `Glob` — and a seventh, `Agent`, right
after. The file tools take an absolute `file_path`. `Edit` swaps one exact
string for another and refuses when the string is missing or appears more
than once. A file must have been read before it is changed. `Bash` runs one
command with a two-minute default timeout, can run it in the background, and
trims long output. Each tool gives the model text, and gives programs a
structured object alongside.

Source: code.claude.com/docs/en — tools-reference, hooks ("PreToolUse input"),
agent-sdk/typescript and agent-sdk/python ("Tool Input/Output Types"),
interactive-mode, env-vars, permissions; docs version 2.1.323, read
2026-11-13. This describes contracts in our own words. The descriptions shown
to the model are gnosh's own text: nothing here is, or should be replaced by,
Claude Code's tool-description prompts. "(unverified)" = the docs do not say.

---

## 1. Rules that apply to every tool

1. **Names are exact, case-sensitive strings** (`Read`, not `read`). The same
   string is used in permission rules, `--tools` / `--allowedTools` /
   `--disallowedTools`, sub-agent `tools:` lists, skill `allowed-tools`, hook
   matchers, and the `name` of a `tool_use` block.
2. **A tool call has two results.** The model receives text (or image blocks)
   in a `tool_result` block. Programs receive a structured object: the
   `tool_use_result` field of the stream-json `user` line and the
   `tool_response` field of `PostToolUse` hook input. Shapes are given per
   tool below; key names are spelled exactly as listed (note that they are
   camelCase while inputs are snake_case).
3. **A failed or refused call** is a `tool_result` with `is_error: true` and
   a message the model can act on. A denied call returns the rejection
   message as the result; the model usually tries another way.
4. **File paths are absolute** by the time hooks and permission checks see
   them: `~` and relative paths are expanded first, so a rule or hook cannot
   be dodged by respelling a path.
5. **Parallel calls.** When one model response contains several tool calls,
   read-only tools (`Read`, `Glob`, `Grep`, read-only MCP tools) may run
   concurrently; tools that change state (`Edit`, `Write`, `Bash`) run one
   after another.
6. **Permission column** below says whether the tool asks in `default` mode
   for a target inside the working directories. File-reading tools still ask
   for paths outside them. Details: `permissions.md`.

## 2. The tool list

| Tool | What it does | Asks in `default` mode | gnosh |
| :- | :- | :- | :- |
| `Read` | Read a file (text, image, PDF, notebook). | No | P0 |
| `Write` | Create or overwrite a file. | Yes | P0 |
| `Edit` | Replace an exact string in a file. | Yes | P0 |
| `Bash` | Run a shell command. | Yes, except built-in read-only commands | P0 |
| `Grep` | Search file contents by regular expression. | No | P0 |
| `Glob` | Find files by name pattern. | No | P0 |
| `Agent` | Run a sub-agent in its own context. | No (its own calls are checked) | P1 (foreground first, then background) |
| `TaskStop` | Stop a background command or sub-agent. | No | P1 (with background Bash) |
| `WebFetch` | Fetch a URL and extract what a prompt asks for. | Yes | P1 |
| `AskUserQuestion` | Ask the user multiple-choice questions. | No (it is itself a prompt) | P1 |
| `Skill` | Run a skill in the main conversation. | Yes | P1 |
| `EnterPlanMode` / `ExitPlanMode` | Switch into plan mode / present the plan for approval. | No / Yes | P1 |
| `WebSearch` | Search the web. | Yes | P2 (needs a search provider) |
| `NotebookEdit` | Change one cell of a Jupyter notebook. | Yes | P2 |
| `Monitor` | Watch a background command and feed each output line to the model. | Yes | P2 |
| `ToolSearch` | Find and load deferred tools. | No | P2 (with MCP) |
| `ListMcpResourcesTool`, `ReadMcpResourceTool`, `WaitForMcpServers` | MCP resources and server readiness. | No | P2 |
| `EnterWorktree` / `ExitWorktree` | Move the session into / out of a git worktree. | Yes / No | P2 |
| `CronCreate`, `CronDelete`, `CronList`, `ScheduleWakeup` | Session-scoped scheduled prompts (behind `/loop`). | No | P2 (gnosh's own `/loop`) |
| `LSP` | Language-server lookups; inactive without a plugin. | No | P2 |
| `TodoWrite`, `TaskCreate`, `TaskGet`, `TaskList`, `TaskUpdate` | The session's task checklist. | No | **replaced by `mem`** |
| `PowerShell` | Windows shell. | Yes | not adopted |
| `Artifact`, `EndConversation`, `ListAgents`, `SendMessage`, `SendUserFile`, `SendFeedback`, `PushNotification`, `RemoteTrigger`, `ReportFindings`, `ShareOnboardingGuide`, `SubagentHandback`, `Workflow` | Anthropic services, agent teams, classifier plumbing. | — | not adopted |

### Old names

| Old name | Now | Note |
| :- | :- | :- |
| `Task` | `Agent` | Still accepted as an alias in rules, matchers and tool lists. Claude Code's `system/init` line still lists the tool as `Task`. |
| `KillShell`, `KillBash` | `TaskStop` | Still accepted as aliases. |
| `BashOutput`, `TaskOutput` | — | Removed in v2.1.277. A background task's output is a file the model reads with `Read`. A deny rule or `--disallowedTools` entry naming them is ignored without a warning. |
| `MultiEdit` | — | The permissions page calls it "legacy"; it is not in the tools table. A `MultiEdit(path)` rule is accepted and never matches. |
| `LS` | — | Not mentioned anywhere in the current docs; Jae's earlier notes record it as a tool name older Claude Code versions had. Directories are listed with a shell command. (unverified: when it was removed) |

gnosh accepts `Task`, `KillShell` and `KillBash` as aliases, and treats
`BashOutput`, `TaskOutput`, `MultiEdit`, `LS` as known-but-absent names (no
warning, never match).

### Default tool set

In Claude Code on macOS and Linux, `Glob` and `Grep` are **left out by
default**: the model searches with `find` and `grep` through `Bash`, where
those two names run built-in fast search programs. They come back when named
in `--tools` or `--allowedTools`, when `Bash` is removed, or when a sub-agent
lists them and omits `Bash`.

gnosh has no embedded search programs behind `find`/`grep`, so gnosh keeps
**`Glob` and `Grep` in the default set**. (Decision recorded here; say if you
want Claude Code's behaviour instead.)

---

## 3. Read

Reads a file and returns its content with line numbers.

| Field | Type | Req | Meaning |
| :- | :- | :- | :- |
| `file_path` | string | yes | Absolute path of the file. |
| `offset` | number | no | Line number to start from. |
| `limit` | number | no | Number of lines to read. |
| `pages` | string | no | PDF page range such as `"1-5"`; at most 20 pages per call; needed for PDFs longer than 10 pages. |
| `allow_large` | boolean | no | Read past the usual size limits when the context window has room (v2.1.296+). |

Behaviour:

- Text is returned with line numbers: **the line number, a tab, the line**.
  The hooks page shows the text the model receives for a Read as
  `1\tfrom __future__ import annotations\n2\t...`. Whether the number is
  padded is not stated (the example is not); gnosh does not pad.
- Empty file: a notice that the file exists but is empty (not an error).
- `offset` beyond the last line: a notice giving the file's line count.
- Directories are not readable; the model lists them with a shell command.
- A file whose extension marks it as binary (`.xlsx`, `.parquet`, `.heic`,
  `.zip`, ...) is refused, with a hint to use a shell command or a skill.
- **Images** (PNG, JPEG, GIF, WebP) are returned as image content, resized
  and recompressed to fit the model's limits; over 128 MB is refused. (P2)
- **PDFs**: short ones whole; longer than 10 pages only by `pages`; page
  rendering needs `pdftoppm` (error text `pdftoppm is not installed`). (P2)
  gnosh gives a PDF's text instead of pictures of its pages, by poppler's
  `pdfinfo` and `pdftotext`, under the same limits (whole up to 10 pages,
  at most 20 pages a call), each page headed `[page N]`; pages past what
  one call shows are named for the next call, a page with no text (a scan)
  is said to be one, and without poppler Read says to install it.
- **Notebooks** (`.ipynb`): all cells with outputs; more than 256 KB of cells
  or over the token limit is an error; a file over 100 MB is refused. (P2)

Size limits for text:

| Case | What the model gets |
| :- | :- |
| One call | At most 25,000 tokens (`CLAUDE_CODE_FILE_READ_MAX_OUTPUT_TOKENS`). |
| Whole-file read over the token limit | The first page plus a `PARTIAL view` notice saying how much was returned and how to read more with `offset`/`limit`. A partial read does **not** count as "read" for Edit/Write. |
| Whole-file read of a file over 256 KB | An error: read a portion with `offset`/`limit`, or search with Grep. |
| `offset`/`limit` read over the token limit | The same error. |
| `allow_large: true` | Sized against the room left in the context window instead. |

Not documented (unverified): a default line count when `limit` is omitted,
and any cutting of very long single lines. The documented bound is the token
cap, so gnosh needs no line default — it returns lines until the cap is
reached — and sets its own rule for over-long lines in gnosh's tool spec.

Structured result — a union on `type`:

```typescript
| { type: "text";  file: { filePath: string; content: string; numLines: number; startLine: number;
                           totalLines: number; truncatedByTokenCap?: boolean } }
| { type: "image"; file: { base64: string; type: "image/jpeg" | "image/png" | "image/gif" | "image/webp";
                           originalSize: number; dimensions?: { originalWidth?, originalHeight?, displayWidth?, displayHeight? } } }
| { type: "notebook"; file: { filePath: string; cells: unknown[] } }
| { type: "pdf";   file: { filePath: string; base64: string; originalSize: number } }
| { type: "parts"; file: { filePath: string; originalSize: number; count: number; outputDir: string }; firstPage?: number }
| { type: "file_unchanged"; file: { filePath: string }; source?: "seeded" }
```

`file_unchanged` is what a repeat read returns when the file has not changed
since the model last read it (the content is already in context); `source:
"seeded"` means the earlier copy came from a memory file loaded at startup.

Side effects of a Read: nested memory files (`CLAUDE.md` in the file's
directory chain) and path-scoped rules that apply to the file are loaded; see
`memory-files.md`.

## 4. Write

Creates a file, or replaces the whole content of an existing one. No append,
no merge.

| Field | Type | Req | Meaning |
| :- | :- | :- | :- |
| `file_path` | string | yes | Absolute path. |
| `content` | string | yes | The full new content. |

Rules:

- **Read before overwrite.** An existing file must have been read in this
  conversation, otherwise the call fails with an error. New files are
  exempt. Notebooks and files read only partially always need the read.
  (Claude Code lets its newest models skip the read when reading would not
  need a prompt; gnosh always requires it.)
- Viewing the file with a recognised Bash command counts as reading it (list
  under Edit).
- A path matched by a `Read` deny rule is refused, even for a new file.
- If the path is itself a symlink the write is refused and the model is
  pointed at the link's target.
- A file that is not valid UTF-8 may be replaced by Write, saved as UTF-8,
  unless the new content contains U+FFFD (the replacement character Read
  shows for undecodable bytes) — that guard stops garbled text being written
  back.

Structured result:

```typescript
{ type: "create" | "update"; filePath: string; content: string;
  structuredPatch: Hunk[];          // empty for a new file, an unchanged write, or a skipped diff
  originalFile: string | null;      // null for a new file or when the old content exceeds ~10 MB
  gitDiff?: GitDiff; userModified?: boolean }

type Hunk = { oldStart: number; oldLines: number; newStart: number; newLines: number; lines: string[] };
type GitDiff = { filename: string; status: "modified" | "added"; additions: number; deletions: number;
                 changes: number; patch: string; repository?: string | null };
```

## 5. Edit

Exact string replacement. No regular expressions, no fuzzy matching.

| Field | Type | Req | Meaning |
| :- | :- | :- | :- |
| `file_path` | string | yes | Absolute path. |
| `old_string` | string | yes | Text to find, exactly as in the file. |
| `new_string` | string | yes | Replacement text. |
| `replace_all` | boolean | no | Replace every occurrence. Default `false`. |

Checks, in order:

1. A path matched by a `Read` deny rule is refused.
2. **Read before edit.** The file must have been read in this conversation;
   a read cut short with a `PARTIAL view` notice does not count.
3. **Match.** `old_string` must occur in the file exactly; one character of
   whitespace difference is a miss.
4. **Uniqueness.** `old_string` must occur exactly once, unless
   `replace_all` is `true`. Otherwise the model must supply more surrounding
   context or set `replace_all`.

A file that changed on disk after the model read it can still be edited when
`old_string` matches the current content exactly and unambiguously; the
result then notes that the file carries other changes so the model re-reads
before edits that depend on surroundings. In any other stale case the model
must read again.

**Bash views that count as a read**: `cat`, `nl`, `bat`, `batcat`, `head`,
`tail`, `sed -n 'X,Yp'`, `grep`, `egrep`, `fgrep`, `rg` — each run on one
file with no pipes or redirects, and only if the command printed some of the
file's text.

**Non-UTF-8 files** are refused and left untouched (exception: a file
starting with a little-endian UTF-16 byte-order mark). The error tells the
model to change the file with a shell command that keeps its encoding, or to
ask about converting it.

Structured result:

```typescript
{ filePath: string; oldString: string; newString: string; originalFile: string | null;
  structuredPatch: Hunk[]; userModified: boolean; replaceAll: boolean; gitDiff?: GitDiff }
```

`userModified` is `true` when the user changed the proposed edit in the
permission prompt before accepting it.

Unverified: whether `old_string == new_string` is an error (gnosh: error),
whether an empty `old_string` may create a file (gnosh: no, use Write), and
the error message texts (gnosh writes its own).

## 6. Bash

Runs one shell command.

| Field | Type | Req | Meaning |
| :- | :- | :- | :- |
| `command` | string | yes | The command line. |
| `timeout` | number | no | Milliseconds. Foreground default 120,000; values above the ceiling (600,000 by default) are lowered to it, not rejected. With `run_in_background`, it is the background time limit instead. |
| `description` | string | no | A short (5–10 word) statement of what the command does; shown in the permission prompt. |
| `run_in_background` | boolean | no | Start the command as a background task and return at once. |
| `dangerouslyDisableSandbox` | boolean | no | Run outside the OS sandbox. (not adopted until gnosh has a sandbox) |

### 6.1 Process and state

- Each command runs in its own process. The shell is `bash` or `zsh`: the
  user's `$SHELL` when it is one of those, else the first working `zsh` then
  `bash` found (`CLAUDE_CODE_SHELL` overrides; other shells unsupported).
- **Working directory persists** between commands in the main session, as
  long as it stays inside the project directory or an additional working
  directory. If a `cd` lands outside, the directory is reset to the project
  directory and `Shell cwd was reset to <dir>` is appended to the result.
  `CLAUDE_BASH_MAINTAIN_PROJECT_WORKING_DIR=1` makes every command start in
  the project directory. Sub-agents never carry a directory change over.
- **Environment variables do not persist**: an `export` is gone in the next
  command.
- Aliases, functions and shell options from the user's startup file
  (`~/.zshrc`, `~/.bashrc` or `~/.profile`) are captured once at session
  start and applied to every command.
- `CLAUDE_ENV_FILE`: a shell script sourced before each command (how hooks
  persist environment changes; see `hooks.md`).
- Spawned processes get `CLAUDECODE=1` in their environment. (gnosh sets
  `GNOSH=1`; whether it also sets `CLAUDECODE=1` is Jae's call.)
- `CLAUDE_CODE_SHELL_PREFIX`: a wrapper command that receives each command
  line as one quoted argument. (P2)

### 6.2 Timeouts

| Setting | Default | Meaning |
| :- | :- | :- |
| `BASH_DEFAULT_TIMEOUT_MS` | 120000 | Timeout when the model passes none. |
| `BASH_MAX_TIMEOUT_MS` | 600000 | Ceiling on what the model may ask for. The effective ceiling is the larger of the two variables. |

**At the timeout the command is moved to the background, not killed** —
unless it starts with `sleep`, or background tasks are disabled
(`CLAUDE_CODE_DISABLE_BACKGROUND_TASKS=1` or `--bare`), in which case it is
stopped. The result then says so — Claude Code's text is
`Command did not complete within its 120s timeout and was moved to the background`
(seconds = the timeout that applied) — followed by the task ID and the path
of the file receiving the output. A `cd` inside a command that was moved does
not carry over; the result says
`Session cwd remains <dir>; directory changes made by the backgrounded command do not apply to subsequent commands.`

### 6.3 Output limits

stdout and stderr are **merged into one interleaved stream**. Output is
streamed to a working file while the command runs and read back when it
ends.

| Result | What the model gets |
| :- | :- |
| Valid | Inline up to about 30,000 characters. Beyond that: the path of a file holding the output (itself cut past 64 MiB) plus a preview of the first 2,000 characters; the model reads or searches the file for the rest. |
| Failure | Inline up to about 10,000 characters. Beyond that: a head-and-tail excerpt of that size, no file path. |

- A non-zero exit is a failure. **Exit code 1 is a valid result** only for
  `grep`, `rg`, `egrep`, `fgrep`, `find`, `diff`, `test`, `[`, `git diff`
  and `git grep`.
- `BASH_MAX_OUTPUT_LENGTH`: size of the read-back window, default 30,000,
  hard ceiling 150,000.
- Setting `bashOutputMaxChars` (up to 128,000) sizes the inline ceiling and
  the read-back window together and makes the variable ignored (v2.1.261+).
- A command whose output passes 5 GB is killed.

### 6.4 Background commands

- `run_in_background: true` starts the command, returns a **background task
  ID** immediately, and writes output to a file. The model reads that file
  with `Read` (there is no output-fetching tool since v2.1.277) and stops the
  task with `TaskStop`.
- The user can also push a running foreground command to the background with
  Ctrl+B, and list or stop tasks with `/tasks`.
- When a background command ends, the model is told through a **task
  notification** (task ID, status `completed` / `failed` / `stopped`, output
  file path, a one-line summary). When the session is idle the notification
  starts a new turn of its own (the docs call it a synthetic follow-up
  turn); several that are queued together may be answered in one turn.
  Whether one can be slipped into a running turn is not stated (unverified);
  gnosh delivers it between tool calls when a turn is running. The
  notification carries a notice that no human typed it, so the model does
  not read it as an instruction or an approval.
- Lifetime: a command started by the main conversation or a background
  sub-agent keeps running after the final response until it exits, is
  stopped, or hits its time limit. A command started by a *foreground*
  sub-agent stops when that sub-agent's run ends. All are stopped when the
  program exits.
- **Time limit**, only in unattended sessions (`-p`, SDK, CI): 30 minutes
  from the moment the command enters the background, or the `timeout` given
  with `run_in_background`, at most 2 hours. In a `-p` run given a text
  prompt the default is 10 minutes. Interactive sessions have no limit. At
  the limit the command is stopped and the model is told:
  `Background command "<description>" was stopped after reaching its background time limit`.
  `BASH_DEFAULT_TIMEOUT_MS` above 1,800,000 raises the default;
  `BASH_MAX_TIMEOUT_MS` above 7,200,000 raises the maximum.
- Memory pressure (macOS/Linux): running background tasks are stopped when
  the OS reports critical memory pressure and the session has been idle for
  30 minutes. (P2)

Structured result:

```typescript
{ stdout: string;                 // the merged command output
  stderr: string;                 // notices added by the tool itself (e.g. the cwd reset), not the command's stderr
  interrupted: boolean;
  isImage?: boolean;
  backgroundTaskId?: string;      // present for background commands
  backgroundedByUser?: boolean;   // Ctrl+B
  timedOutAfterMs?: number;       // set when the timeout moved it to the background
  backgroundCwdHint?: string;
  backgroundEndsWithFinalResponse?: true; // owned by a foreground sub-agent
  persistedOutputPath?: string; persistedOutputSize?: number; // when output was saved to a file
  returnCodeInterpretation?: string; noOutputExpected?: boolean;
  gitOperation?: { commit?: { sha: string; kind: "committed" | "amended" | "cherry-picked"; branch?: string };
                   push?: { branch: string }; branch?: { ref: string; action: "merged" | "rebased" };
                   pr?: { number: number; url?: string; action: string } } }
```

### 6.5 TaskStop

| Field | Type | Req | Meaning |
| :- | :- | :- | :- |
| `task_id` | string | no | ID of the background task (command or sub-agent) to stop. |
| `shell_id` | string | no | Deprecated spelling of `task_id`. |

Result: `{ message: string; task_id: string; task_type: string; command?: string }`.
When no task has that ID the error lists the running background agents by ID
and description.

## 7. Grep

Searches file contents. Claude Code builds it on ripgrep and uses ripgrep's
regular-expression syntax (not POSIX grep): metacharacters must be escaped,
so `interface{}` is searched as `interface\{\}`.

| Field | Type | Req | Meaning |
| :- | :- | :- | :- |
| `pattern` | string | yes | Regular expression. |
| `path` | string | no | File or directory to search. Default: the working directory. |
| `glob` | string | no | Only files matching this glob, e.g. `**/*.tsx`. |
| `type` | string | no | Only files of this ripgrep file type, e.g. `py`, `rust`, `go`. |
| `output_mode` | string | no | `files_with_matches` (default), `content`, or `count`. |
| `-i` | boolean | no | Case-insensitive. |
| `-n` | boolean | no | Show line numbers (content mode). |
| `-o` | boolean | no | Print only the matched part of each line (content mode). |
| `-A`, `-B` | number | no | Lines of context after / before each match. |
| `-C`, `context` | number | no | Lines of context on both sides (`-C` is the alias). |
| `head_limit` | number | no | Keep only the first N lines/entries. |
| `offset` | number | no | Skip the first N lines/entries before applying `head_limit`. |
| `multiline` | boolean | no | Let a pattern match across line boundaries. Default: single-line. |

Behaviour:

- `files_with_matches`: paths only. `content`: matching lines with file and
  line number. `count`: matches per file, then a total across all matching
  files (the total is not reduced by `head_limit`/`offset`).
- In `content` mode, an `offset` past the last match of a pattern that does
  match returns `No entries at this offset` (so the model does not conclude
  "no match").
- A pattern, glob or type the engine rejects is an **error** carrying the
  engine's diagnostic (it is not reported as "no files found").
- **Respects `.gitignore`**; a gitignored file is searched only when its path
  is passed directly.
- Timeout 20 s (`CLAUDE_CODE_GLOB_TIMEOUT_SECONDS`, shared with Glob).
- The permission check runs before the path's existence check.

Structured result:

```typescript
{ mode?: "content" | "files_with_matches" | "count";
  numFiles: number;               // 0 in content mode
  filenames: string[];            // filled in files_with_matches mode only
  content?: string;               // matching lines (content) or per-file counts (count)
  numLines?: number;              // content mode
  numMatches?: number;            // count mode: total over the full result set
  totalFiles?: number;            // before head_limit/offset, files_with_matches mode
  totalLines?: number;            // before head_limit/offset, content mode
  appliedLimit?: number; appliedOffset?: number }
```

gnosh implements this in Go with the standard `regexp` package (RE2 syntax).
ripgrep's default syntax is the same family — no backreferences, no
look-around — but the two are not identical in every corner (unverified).
`type` names follow ripgrep's list; gnosh ships a table for the common ones.
Unverified: the default `head_limit` and the order of files in
`files_with_matches` (gnosh: most recently modified first, like Glob).

## 8. Glob

Finds files by name pattern.

| Field | Type | Req | Meaning |
| :- | :- | :- | :- |
| `pattern` | string | yes | Glob, with `**` for any depth and `{a,b}` alternatives: `**/*.js`, `src/**/*.ts`, `*.{json,yaml}`. |
| `path` | string | no | Directory to search. Default: the working directory. |

- Results are **sorted by modification time** and **capped at 100 files**;
  `truncated` tells the model to narrow the pattern.
- **Does not respect `.gitignore`** by default (`CLAUDE_CODE_GLOB_NO_IGNORE=false`
  makes it), and **includes dotfiles** (`CLAUDE_CODE_GLOB_HIDDEN=false`
  excludes them).
- A `pattern` or `path` containing a null byte is an error.
- Timeout 20 s.

Structured result:
`{ durationMs: number; numFiles: number; filenames: string[]; truncated: boolean; totalMatches?: number; countIsComplete?: boolean }`.

Unverified: newest-first or oldest-first within "sorted by modification
time" (gnosh: newest first).

## 9. Agent (old name `Task`)

Starts a sub-agent with its own context window; the parent sees only the
sub-agent's final result, not its intermediate tool calls. Full behaviour in
`subagents.md`.

| Field | Type | Req | Meaning |
| :- | :- | :- | :- |
| `description` | string | yes | Short (3–5 word) label for the task. |
| `prompt` | string | yes | The task. It is all the sub-agent knows. |
| `subagent_type` | string | no | Which agent definition to use (e.g. `Explore`, `general-purpose`, a custom name). |
| `model` | string | no | Model override for this run. In Claude Code: `sonnet` / `opus` / `haiku` / `fable`. gnosh: its own model names. |
| `effort` | string | no | `low` / `medium` / `high` / `xhigh` / `max`. |
| `run_in_background` | boolean | no | Run without making the caller wait. |
| `name` | string | no | A name for the spawned agent, usable to message or stop it. |
| `isolation` | string | no | `worktree` (run in its own git worktree) or `remote` (Anthropic cloud; not adopted). |
| `mode`, `team_name` | — | — | Deprecated and ignored. |

Launching a sub-agent does not itself ask for permission; the sub-agent's own
tool calls are checked as they happen.

Structured result — a union on `status`:

```typescript
| { status: "completed"; agentId: string; agentType?: string;
    content: { type: "text"; text: string }[];     // the sub-agent's final text
    totalToolUseCount: number; totalDurationMs: number;
    totalTokens: number; usage: Usage;             // both from the sub-agent's LAST model request, not the whole run
    resolvedModel?: string; modelsUsed?: string[];
    toolStats?: { readCount, searchCount, bashCount, editFileCount, linesAdded, linesRemoved, otherToolCount };
    prompt: string; worktreePath?: string; worktreeBranch?: string }
| { status: "async_launched"; agentId: string; description: string; prompt: string;
    outputFile: string; canReadOutputFile?: boolean; resolvedModel?: string }
```

## 10. WebFetch

Fetches a URL and answers a prompt about its content.

| Field | Type | Req | Meaning |
| :- | :- | :- | :- |
| `url` | string | yes | The URL. |
| `prompt` | string | yes | What to extract from the page. |
| `offset` | number | no | Characters to skip from the start of the page, to continue a long one (v2.1.290+). |

Behaviour:

- HTML is converted to Markdown, then the prompt is run against the content
  in a **separate, small model call**; the model receives that answer, not
  the raw page. Lossy by design.
- `localhost` and any hostname without a dot are refused before any request;
  the error says to use `curl` through Bash for local servers.
- `http://` is upgraded to `https://`.
- At most 100,000 characters of page content per call; the result for a
  longer page says how much went unread.
- Responses are cached for 15 minutes (`CLAUDE_CODE_WEBFETCH_CACHE_TTL_MS`).
- A download not finished within 5 minutes, redirects included, fails
  (`CLAUDE_CODE_WEBFETCH_DEADLINE_MS`; `0` = no limit).
- A redirect to a **different host** is not followed: the result names the
  original URL and the target, and the model fetches the target itself.
- `CLAUDE_CODE_DISABLE_WEB_FETCH=1` removes the tool.
- Content that is not text (a PDF, a picture) is saved to a local file, and
  the result says where; one sent as `application/octet-stream` is named by
  what it is (2.1.329 fixed PDFs and pictures sent that way being saved as
  `.bin`). gnosh saves it into the session's folder for tool output, which
  Read may read without a question, reads the type from the first bytes,
  and does not put the prompt to the small model.
- Request headers: Claude Code sends a `User-Agent` starting `Claude-User`
  and an `Accept` header preferring Markdown. gnosh sends its own
  `User-Agent` and the same `Accept` preference.

Permission: in `default` and `acceptEdits` it prompts per fetch, with the
choices *yes (this fetch only)*, *yes and don't ask again for this domain*
(saves `WebFetch(domain:<host>)` to local settings), and *no*. Claude Code
also has a built-in list of pre-approved documentation domains; the list is
not published (unverified), so gnosh starts with none.

Structured result:
`{ bytes: number; code: number; codeText: string; result: string; durationMs: number; url: string }`.

## 11. WebSearch

| Field | Type | Req | Meaning |
| :- | :- | :- | :- |
| `query` | string | yes | The search query. |
| `allowed_domains` | string[] | no | Only results from these hosts. |
| `blocked_domains` | string[] | no | Never results from these hosts. Cannot be combined with `allowed_domains`. |
| `mode` | string | no | `standard` (lighter) or `extended` (full). Absent = full. v2.1.282+. |

Returns titles and URLs only; pages are read with WebFetch. In Claude Code
the search runs on Anthropic's backend (up to eight backend searches per
call) and is not configurable; an interactive session is limited to 200
calls, refilling at about 100 per hour
(`CLAUDE_CODE_MAX_WEB_SEARCHES_PER_SESSION`,
`CLAUDE_CODE_WEB_SEARCH_REFILLS_PER_HOUR`); in `-p` the limit is per turn.

Structured result:
`{ query: string; results: (string | { tool_use_id: string; content: { title: string; url: string }[] })[]; durationSeconds: number; searchCount?: number }`.

gnosh: P2. Keep the name and inputs; the backend is a provider gnosh
configures (or an MCP search tool).

## 12. AskUserQuestion

Asks one to four multiple-choice questions and waits.

| Field | Type | Req | Meaning |
| :- | :- | :- | :- |
| `questions` | array | yes | 1–4 items: `{ question: string; header: string; options: { label: string; description: string; preview?: string }[]; multiSelect: boolean }`. `header` is a very short chip label (at most 12 characters); 2–4 options each; `label` is 1–5 words. |
| `answers` | object | no | Filled by the harness (or by a hook's `updatedInput`): question text → chosen label; multi-select answers are the labels joined with commas. The model does not set it. |
| `annotations` | object | no | Per-question `preview` / `notes` from the user. |

- The user may pick an option or type free text through an "Other" row.
- Questions stay open until answered. Setting `askUserQuestionTimeout`
  (`60s`, `5m`, `10m`) closes an unanswered question after that idle time,
  submitting whatever was selected and telling the model the user may be
  away; a countdown shows for the last 20 seconds.
- Never auto-approved by any mode. In `dontAsk` mode, and in `-p` runs with
  `--permission-prompts none`, the tool is denied or removed.

Structured result:
`{ questions: [...]; answers: Record<string,string>; response?: string; annotations?: {...}; afkTimeoutMs?: number }`
— `response` is set when the user typed a free-form reply instead of
answering; the model is then told what the user responded.

## 13. Skill

Runs a skill inside the main conversation (see
`slash-commands-and-skills.md`). Input: the skill's name in a field called
`skill` (documented through the `Skill(skill:<name>)` rule form). An
argument string is also passed; its field name is not in the docs
(unverified) — gnosh calls it `args`. Permission rules: `Skill(name)` exact,
`Skill(name *)` name with any arguments.

## 14. EnterPlanMode / ExitPlanMode

- `EnterPlanMode`: no input. Switches the session to plan mode.
- `ExitPlanMode`: the model writes the plan to a plan file first; the
  harness injects `plan` (Markdown) and `planFilePath` into the input before
  hooks see it, shows the plan, and asks for approval (see
  `permissions.md`, plan mode). `allowedPrompts` is deprecated and ignored.
  Result: `{ plan: string | null; isAgent: boolean; filePath?: string; planWasEdited?: boolean }`.

gnosh keeps plans in `mem`; these two tools remain the model-facing switch
for plan *mode* (a permission state), with the plan text stored by `mem`
rather than under `~/.claude/plans/`.

## 15. NotebookEdit (P2)

| Field | Type | Req | Meaning |
| :- | :- | :- | :- |
| `notebook_path` | string | yes | Absolute path of the `.ipynb` file. |
| `cell_id` | string | no | Target cell. |
| `new_source` | string | yes | New cell source. |
| `cell_type` | string | no | `code` or `markdown`; required for `insert`. |
| `edit_mode` | string | no | `replace` (default), `insert` (after the target; at the start when no `cell_id`), `delete`. |

Covered by `Edit(path)` permission rules. Refuses a file that is not UTF-8.

gnosh: built (DECISIONS 325). Cells are named by their `id`, or `cell-N`
where a notebook older than nbformat 4.5 has none; Read shows the name of
each cell.

## 16. Monitor (P2)

Input `{ description: string; timeout_ms: number; command?: string; ws?: { url: string; protocols?: string[] } }`
— exactly one of `command` or `ws`. Each stdout line (or WebSocket text
frame) becomes an event delivered to the model. Deadline default 300,000 ms,
effective maximum 1,800,000 ms (10 minutes in a single-prompt `-p` run).
Uses Bash's permission rules. Result `{ taskId: string; timeoutMs: number }`;
stopped with `TaskStop`.

## 17. Todo and task tools — replaced by `mem`

For the record, so their names are recognised and not re-invented:

- `TodoWrite` — input `{ todos: { content: string; status: "pending" | "in_progress" | "completed"; activeForm: string }[] }`;
  result `{ oldTodos, newTodos }`. Disabled by default in Claude Code.
- `TaskCreate` `{ subject, description, activeForm?, metadata? }` →
  `{ task: { id, subject } }`; `TaskUpdate` `{ taskId, status?: "pending" | "in_progress" | "completed" | "deleted", subject?, description?, activeForm?, addBlocks?, addBlockedBy?, owner?, metadata? }`;
  `TaskGet` `{ taskId }`; `TaskList` `{}`.
- Claude Code itself now leaves all five out by default on its newer models
  and on any model ID it does not recognise, because the definitions cost
  context; they return with `CLAUDE_CODE_ENABLE_TODO_TOOLS=1` or by naming
  one in `--allowedTools`/`--tools`.

gnosh does not provide them. Plans and progress live in `mem` (Gno). A
permission rule, hook matcher or tool list that names one of them is
accepted as a known name and never matches.

---

## What gnosh adopts

**P0**

- [ ] Tool names exactly as in section 2, with aliases `Task`, `KillShell`,
      `KillBash` and the known-but-absent names.
- [ ] `Read`: `file_path`, `offset`, `limit`; numbered output; empty-file and
      past-the-end notices; 25,000-token cap, 256 KB whole-file refusal,
      `PARTIAL view` first page; binary-extension refusal; structured
      `text` / `file_unchanged` result.
- [ ] `Write`: read-before-overwrite, symlink refusal, structured result with
      `structuredPatch`.
- [ ] `Edit`: read-before-edit, exact match, uniqueness, `replace_all`,
      changed-on-disk rule, the Bash-view list, non-UTF-8 refusal.
- [ ] `Bash`: `command`, `timeout`, `description`; cwd persistence and reset
      notice; env not persisted; 120 s / 600 s timeouts with the two
      variables; merged output; 30,000 / 10,000 character limits with
      file-plus-preview overflow; benign exit-1 list.
- [ ] `Grep` and `Glob` with all fields above, in the default tool set.
- [ ] Parallel read-only calls, sequential state-changing calls.
- [ ] Absolute `file_path` normalisation before hooks and permission checks.

**P1**

- [ ] `Agent`, foreground, with `description`, `prompt`, `subagent_type`,
      `model` (first), then `run_in_background`, `name` and the
      `async_launched` result.
- [ ] `Bash` `run_in_background`, timeout-moves-to-background, output file,
      task notification, `TaskStop`, Ctrl+B, time limits in `-p`.
- [ ] `WebFetch`, `AskUserQuestion`, `Skill`, `EnterPlanMode`/`ExitPlanMode`.
- [ ] `CLAUDE_ENV_FILE`, startup-file aliases and functions.
- [ ] `Read` for images.

**P2**

- [ ] `WebSearch`, `NotebookEdit`, `Monitor`, `ToolSearch`, MCP resource
      tools, worktree tools, `Read` for PDFs and notebooks, `allow_large`,
      `Agent` `isolation: "worktree"`, `effort`, `gitOperation` and
      `gitDiff` result extras.

## Deliberately not adopted

- `TodoWrite` and the `Task*` list tools: gnosh keeps plans in `mem`.
- `PowerShell` (Windows), `dangerouslyDisableSandbox` (no sandbox yet).
- `Artifact`, `SendUserFile`, `PushNotification`, `RemoteTrigger`,
  `ShareOnboardingGuide`, `SendFeedback`: Anthropic-hosted services.
- `EndConversation`, `SubagentHandback`, `ReportFindings`, `Workflow`,
  `ListAgents`, `SendMessage`: Anthropic model-policy, classifier and
  agent-team features.
- `Agent` `isolation: "remote"`, model aliases `sonnet`/`opus`/`haiku`/`fable`.
- WebFetch's `Claude-User` user agent, its unpublished pre-approved domain
  list, and its Anthropic-side domain safety check.
- WebSearch's Anthropic backend.

## Unverified (docs silent)

- Read: padding of the line number, whether there is a default line count
  without `limit`, long-line truncation, exact conditions for
  `file_unchanged`.
- Edit: behaviour for identical or empty `old_string`; all error texts.
- Grep: default `head_limit`, file ordering, exact regex-dialect differences
  from Go's `regexp`.
- Glob: direction of the modification-time sort.
- Skill: the name of the arguments field.
- WebFetch: the pre-approved documentation domains.
- The text of the task notification the model reads (gnosh writes its own).
- When `LS` and `MultiEdit` were removed.
- Whether a task notification can be delivered inside a running turn.
