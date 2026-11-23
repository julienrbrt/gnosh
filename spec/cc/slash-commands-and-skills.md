# slash-commands-and-skills.md — built-in commands, custom commands, skills

What this file is: the `/commands` a user types inside a session, and the
"skill" mechanism for adding new ones — named as in Claude Code, so Jae's
fingers and his existing `~/.claude/skills/` and `.claude/commands/` folders
both keep working.

In one paragraph: a line that starts with `/name` is a command. Some are
built into the program (`/clear`, `/compact`, `/model`). The rest are
*skills*: a folder with a `SKILL.md` file — a few lines of settings at the
top, then instructions in plain Markdown. Typing `/name args` puts those
instructions into the conversation, with `$ARGUMENTS` replaced by what was
typed. The model can also start a skill on its own, because it is shown a
short list of every skill's name and description; the full text is loaded
only when the skill is actually used. A skill can run shell commands while
it is being prepared (to paste in a diff, say) and can pre-approve tools for
the turn it runs in.

Source: code.claude.com/docs/en — commands, skills, goal, scheduled-tasks,
headless, context-window, settings-reference, common-workflows; docs version
2.1.323, read 2026-11-13. Own words; names and field names are literal.
"(unverified)" = the docs do not say.

---

## 1. How a command line is read

- A command is recognised **only at the start of the message**. Text after
  the name is its arguments.
- Typing `/` opens a menu of available commands; more letters filter it.
- A command sent while the model is responding is queued and runs when the
  turn ends. A few run immediately without interrupting: `/status`,
  `/tasks`, `/usage`, `/model`, `/effort`, `/permissions`.
- An unknown `/name` is reported; nothing runs.
- At the terminal prompt several skills may be stacked at the start of one
  message: `/skill-a /skill-b do XYZ` loads both and gives the trailing text
  to each. Up to six. In `-p` only the first is expanded.
- A `/name` that appears *after* other text does not run the skill; it
  counts as the user's permission for the model to run it in that turn.
- In `-p`, a prompt beginning with `/name` runs that skill or custom
  command. Terminal-only built-ins are unavailable there; `/model`,
  `/effort`, `/rename` take their value as an argument; `/config key=value`
  sets one setting; `/mcp` prints server status as text; `/compact`,
  `/clear` and `/context` work.

## 2. Built-in commands

One line each. **Bold** = named in the brief as mattering; the last column
is gnosh's priority.

### Session and context

| Command | Aliases | Meaning | Pri |
| :- | :- | :- | :- |
| **`/clear [name]`** | `/reset`, `/new` | Start a new conversation with empty context. A name labels the old conversation in the resume picker. Fires `SessionEnd` (`clear`) then `SessionStart` (`clear`). Also removes an active goal. | P1 (first) |
| **`/compact [instructions]`** | — | Summarise the conversation so far to free context; optional instructions say what the summary should keep. See `sessions.md`. | P1 (first) |
| **`/resume [session]`** | `/continue` | Resume a conversation by ID or name, or open the session picker. | P1 |
| **`/rename [name]`** | — | Rename the session and show the name by the prompt. With no name, generate one from the conversation. Control and invisible characters become spaces; names are capped at 200 characters; a name that is empty after cleaning is refused with `That name is empty once invisible characters are removed. Usage: /rename <name>`. Works in `-p`. | P1 |
| **`/export [filename]`** | — | Export the conversation as plain text: to the given file, or with no argument through a dialog offering the clipboard or a file. | P1 |
| **`/rewind`** | `/checkpoint`, `/undo` | Go back to an earlier point in the conversation and/or restore files, or summarise from a chosen message. See `sessions.md`. | P1 |
| `/branch [name]` | — | Copy the conversation at this point into a new session and switch to it; the original stays resumable. | P1 |
| `/context [all]` | — | Show what is filling the context window, by category; `all` expands the per-item list (including the memory files loaded). | P1 |
| `/copy [N]` | — | Copy the last (or Nth-latest) response to the clipboard; with code blocks present, a picker chooses a block; `w` in the picker writes to a file instead. | P1 |
| `/btw [question]` | — | Ask a side question without adding it to the conversation. | P2 |
| `/recap` | — | One-line summary of the session. | P2 |
| `/exit` | `/quit` | Exit. | P1 (first) |

### Model and behaviour

| Command | Aliases | Meaning | Pri |
| :- | :- | :- | :- |
| **`/model [model]`** | — | Switch model and save it as the default for new sessions. No argument: a picker (`s` on a row = this session only). In `-p` an argument is required and is not saved. Takes effect from the next request of the running turn. | P1 (first) |
| `/effort [level\|auto\|status]` | — | Set reasoning effort (`low` … `max`). | P2 |
| **`/permissions`** | `/allowed-tools` | View rules by scope, add and remove rules, manage working directories. Changes apply from the model's next tool call. | P1 |
| `/plan [description]` | — | Enter plan mode, optionally starting on a task at once. | P1 |
| `/output-style [style]` | — | List or switch output styles. | P2 |
| `/config [key=value ...]` | — | Open settings; `key=value` sets one option without the menu. | P1 |
| `/add-dir <path>` | — | Add a working directory for this session (with path completion). | P1 |
| `/cd <path>` | — | Move the session to another working directory, keeping the conversation; applies the new directory's project settings, skills and memory files. | P2 (built: gnosh starts again in the folder, DECISIONS 77) |

### Extension points

| Command | Aliases | Meaning | Pri |
| :- | :- | :- | :- |
| **`/hooks`** | — | Read-only browser of configured hooks and where each comes from. | P1 |
| **`/memory`** | — | List and open the memory files (`memory-files.md`). | P1 |
| **`/agents`** | — | In current Claude Code this only prints a reminder to ask the model to create or edit sub-agents, or to edit `.claude/agents/` directly (it was an interactive manager through v2.1.197). gnosh: list the agents found and where they are defined. | P1 |
| `/skills` | — | List available skills, filterable; Space cycles a skill's visibility. | P1 |
| `/reload-skills` | — | Re-scan skill and command directories. | P1 |
| `/mcp [reconnect <server>\|all \| enable\|disable <server>\|all]` | — | Manage MCP server connections. | P1 |
| `/init` | — | Have the model write a starting `CLAUDE.md` for the project. | P1 |
| `/statusline` | — | Set up the status line from a description. | P2 |
| `/keybindings` | — | Open the key-bindings file. | P2 |

### Work that keeps going

| Command | Aliases | Meaning | Pri |
| :- | :- | :- | :- |
| **`/loop [interval] [prompt]`** | — | Re-run a prompt while the session stays open. Section 3. | P1 (gnosh already has its own `/loop`) |
| **`/goal [condition\|clear]`** | — | Keep working, turn after turn, until a condition holds. Section 4. | P1 (gnosh already has its own `/goal`) |
| `/tasks` | `/bashes` | List and stop background work in this session: shell commands and sub-agents, including finished ones. | P1 |
| `/background [prompt]`, `/fork [prompt]`, `/subtask <task>`, `/stop` | — | Detach the session, copy it into a background session, spawn a forked sub-agent, stop a background session. | P2 |

### Information and diagnosis

| Command | Aliases | Meaning | Pri |
| :- | :- | :- | :- |
| **`/status`** | — | Version, model, connectivity, which settings files loaded (`Setting sources`), session kind. Works while the model is responding. | P1 |
| **`/cost`** | alias of `/usage`; also `/stats` | Session cost and activity. In Claude Code also plan-limit information. gnosh: token counts and, if a price table is set, cost. | P1 |
| **`/doctor`** | — | A check-up of installation and configuration that proposes fixes and applies them after confirmation (in Claude Code it is a bundled skill). `claude doctor` in the shell is the read-only version. | P1 |
| **`/help`** | — | Help and the list of commands. | P1 (first) |
| `/diff` | — | Review the changes in the working tree, including the model's edits. | P1 |
| `/debug [description]` | — | Turn on debug logging for the session and help diagnose a problem. | P2 |
| `/release-notes` | — | Changelog viewer. | P2 |
| `/theme`, `/color`, `/tui`, `/focus`, `/scroll-speed`, `/terminal-setup` | — | Look and rendering. | P2 / own design |
| `/security-review`, `/code-review` (`/review`), `/simplify`, `/verify`, `/run`, `/batch`, `/update-config`, `/fewer-permission-prompts` | — | Bundled skills: prompts shipped with Claude Code. gnosh ships none of Anthropic's text; its equivalents are its own. Built: `/security-review`, `/code-review` (`/review`), `/simplify`, and `/fewer-permission-prompts`, which gnosh's engine works out itself rather than a model (DECISIONS 376); also `/skill-doctor` (DECISIONS 377). | the rest not adopted |

Removed in Claude Code, do not add: `/vim` (v2.1.92; use the editor-mode
setting), `/pr-comments` (v2.1.91), `/ultraplan`.

## 3. `/loop`

| Typed | Behaviour |
| :- | :- |
| `/loop 5m check the deploy` | The prompt runs on a fixed interval. |
| `/loop check the deploy` | The prompt runs at an interval the model picks after each iteration, between one minute and one hour; the chosen delay and the reason are printed. |
| `/loop` | A built-in maintenance prompt runs (continue unfinished work, tend the branch's pull request, then clean-up passes), or the user's `loop.md` if one exists. |

- Interval: a leading token such as `30m`, or a trailing clause such as
  `every 2 hours`. Units `s`, `m`, `h`, `d`. Minimum one minute; seconds are
  rounded up; odd values (`7m`, `90m`) are rounded to a schedulable interval
  and the user is told.
- The prompt may itself be a skill (`/loop 20m /review-pr 1234`), but only
  one the model is allowed to invoke; built-in commands and
  `disable-model-invocation` skills arrive as plain text instead.
- Loops are session-scoped: they stop when the session closes, are restored
  on `--resume`/`--continue` unless expired (seven days), and are listed and
  cancelled like other scheduled tasks. `CLAUDE_CODE_DISABLE_CRON=1` turns
  the feature off.
- In `-p` with a text prompt the run stays alive for pending self-paced
  wake-ups until the loop ends.
- Each iteration fires `UserPromptSubmit`.

## 4. `/goal`

- `/goal <condition>` sets a completion condition (up to 4,000 characters)
  and immediately starts a turn with the condition as the directive. One
  goal per session; a new one replaces the old.
- After **each turn** a small, fresh model is given the condition and the
  conversation and returns one of three verdicts with a short reason:
  *not yet met* (the model takes the reason as guidance and starts another
  turn), *met* (goal cleared, recorded as achieved), *impossible* (goal
  cleared, recorded as failed with the reason).
- The judge does not run commands or read files; it judges only what the
  conversation shows, so conditions should be things the model's own output
  can demonstrate ("`npm test` exits 0").
- `/goal` with no argument shows status: the condition, running time, turns
  evaluated, tokens spent, the latest reason. `/goal clear` (also `stop`,
  `off`, `reset`, `none`, `cancel`) removes it, printing `Goal cleared:` and
  the condition, or `No goal set`.
- An indicator shows that a goal is active and for how long.
- It does not change the permission mode.
- If the model keeps answering the judge without using any tool for several
  turns in a row, the loop stops with a warning and the goal stays set (this
  is the stop-hook continuation cap of `hooks.md`).
- A turn that fails on an error the user must fix (bad credentials, model
  unavailable, context overflow that compaction could not clear) clears the
  goal with a warning; other errors retry or pause it.
- A goal still active when a session ends is restored on resume, with the
  counters reset.
- Works in `-p`: `claude -p "/goal <condition>"` runs the loop to completion
  in one invocation.
- Mechanically it is a session-scoped, prompt-type `Stop` hook, and it
  cannot run while `disableAllHooks` is set.

gnosh already has `/goal` with every goal judged; the points above are the
behaviours to keep aligned.

## 5. Skills

### 5.1 Where they live

| Scope | Path | Loaded in |
| :- | :- | :- |
| Personal | `~/.claude/skills/<name>/SKILL.md` | Every project |
| Project | `.claude/skills/<name>/SKILL.md` | That repository: from the start directory and every parent up to the repository root |
| Nested | `<subdir>/.claude/skills/<name>/SKILL.md` | Loaded the first time the model reads or edits a file in that subdirectory; until then not in the menu |
| Added directory | `.claude/skills/` inside an `--add-dir` or `/add-dir` directory | That session |
| Old format | `.claude/commands/<name>.md`, `~/.claude/commands/<name>.md` | Same mechanism; one file, no supporting files |

- Name clashes: personal beats project; a skill beats a command file of the
  same name; a user's skill replaces a built-in or bundled command of the
  same name (but not that command's aliases); an added directory's skill
  runs under a prefixed name (`/cli:verify`).
- A file in a subdirectory of `commands/` is named with colons:
  `.claude/commands/frontend/component.md` → `/frontend:component`.
- A `<name>` entry may be a symlink to a directory elsewhere.
- Skill directories are **watched**: adding, editing or removing a
  `SKILL.md` takes effect in the running session. A skills directory that
  did not exist at session start needs `/reload-skills`.
- A name containing control characters, zero-width or direction-changing
  characters, odd blanks, or `<` / `>` is refused and the skill does not
  load.
- `--disable-slash-commands` turns all skills and commands off; `--bare`
  skips discovery (a skill can still be run by name).

A skill folder may hold more than `SKILL.md` — reference files the model
reads when the skill tells it to, and scripts it runs. Only `SKILL.md` is
loaded when the skill is invoked.

### 5.2 `SKILL.md` frontmatter

YAML between `---` lines, which must start on the file's first line;
otherwise the whole file is content. All fields are optional. Unknown fields
are ignored silently. Booleans also accept `yes`/`no`, `on`/`off`, `1`/`0`.
If the YAML does not parse, the skill loads with no fields set.

| Field | Meaning |
| :- | :- |
| `name` | Command name shown in the menu. Default: the directory name (which also still works). Not available in command files. |
| `description` | What the skill does and when to use it — this is what the model matches against. Default: the first non-empty line of the body. |
| `when_to_use` | Extra trigger hints, appended to the description in the listing. The two together are cut at 1,536 characters. |
| `argument-hint` | Shown during completion, e.g. `[issue-number]`. |
| `arguments` | Names for positional arguments (`arguments: [issue, branch]`), used as `$issue`, `$branch`. A space-separated string or a YAML list. |
| `disable-model-invocation` | `true`: only the user can start it. Its description is then not shown to the model at all. Default `false`. |
| `user-invocable` | `false`: only the model can start it; hidden from the menu and not run when typed. Default `true`. |
| `allowed-tools` | Permission rules granted **for the turn that invokes the skill** (cleared when the user sends the next message). Does not restrict other tools. Space- or comma-separated string, or a YAML list: `Bash(git add *) Bash(git commit *)`. |
| `disallowed-tools` | Tools removed from the model while the skill is active; cleared at the next user message. |
| `model` | Model for the rest of the current turn (`inherit` = keep). With `context: fork`, the sub-agent's model. |
| `effort` | Effort level for the rest of the turn. |
| `context` | `fork`: run the skill in a sub-agent instead of inline. |
| `agent` | Which agent type runs a forked skill. Default `general-purpose`. |
| `background` | With `context: fork`: `false` = wait for the result in the invoking turn. Default `true` (runs in the background). |
| `hooks` | Hooks registered when the skill is invoked and kept for the rest of the session (`hooks.md`; `once: true` removes one after its first successful run). |
| `paths` | Glob patterns; the model auto-loads the skill only when working with matching files. Same glob format as path-scoped rules. Not available in command files. |
| `shell` | Shell for injected commands: `bash` (default) or `powershell`. |
| `metadata`, `license`, `compatibility` | Accepted, not acted on (they belong to the open Agent Skills format). |

### 5.3 Substitutions in the body

| Placeholder | Becomes |
| :- | :- |
| `$ARGUMENTS` | The whole argument string as typed. |
| `$ARGUMENTS[N]`, `$N` | The Nth argument, 0-based (`$0` is the first). Shell-style quoting: `/x "hello world" two` → `$0` = `hello world`. |
| `$name` | A named argument from `arguments`. |
| `${CLAUDE_SESSION_ID}` | The session ID. |
| `${CLAUDE_SKILL_DIR}` | The directory holding this `SKILL.md`. |
| `${CLAUDE_PROJECT_DIR}` | The project root. |
| `${CLAUDE_EFFORT}` | The current effort level. |

Rules:

- If arguments were given and **no** placeholder received one, they are
  appended to the end as `ARGUMENTS: <value>`.
- An indexed placeholder with no argument at that position stays as literal
  text; a named one becomes an empty string.
- Argument values are inserted literally and never re-expanded.
- `\$1.00` keeps a literal `$` before a digit, `ARGUMENTS`, or a declared
  name. A doubled backslash does not escape.
- `${CLAUDE_SKILL_DIR}` and `${CLAUDE_PROJECT_DIR}` are also substituted in
  the Bash rules of `allowed-tools`, so a skill can pre-approve its own
  bundled script:
  `allowed-tools: Bash(${CLAUDE_SKILL_DIR}/scripts/render.sh *)`.

### 5.4 Shell injection

- Inline: `` !`command` `` — the command runs **before** the model sees the
  skill, and its output replaces the placeholder. Recognised only when `!`
  is at the start of a line or right after whitespace.
- Block: a fenced block opened with ` ```! ` runs several lines.
- One pass over the original file; command output is not scanned again.
- Each command runs like a Bash tool call: the session shell's current
  directory, the 2-minute default timeout, stderr merged into stdout, long
  output replaced by a file path plus preview.
- **A failing command aborts the whole invocation** (the model never sees
  the skill); the error reads `Shell command failed for pattern "..."` and
  includes the output. Non-zero exit = failure, except exit 1 from the
  search and comparison commands that the Bash tool treats as benign.
  Append `|| true` to tolerate a failure.
- **Permission**: injected commands never prompt. Each is checked against
  the permission rules; anything other than "allowed" — a deny, an ask, or
  no matching rule — aborts with
  `Shell command permission check failed for pattern "..."`. Pre-approve
  them with `allowed-tools`. Deny and ask rules still win.
- `disableSkillShellExecution: true` replaces every injected command with
  `[shell command execution disabled by policy]`.

`@path` file inclusion inside a skill body is not described on the current
skills page (unverified). In a *prompt*, `@path` includes a file's content
(when it fits Read's limits) or a directory's listing — see
`interactive.md`. gnosh applies prompt `@` expansion to typed text only, not
to skill bodies.

### 5.5 How a skill reaches the model

1. **The listing.** Every session gets a list of skill names with their
   descriptions (name only for a skill set to `name-only`; nothing for one
   with `disable-model-invocation`). Its budget is 1% of the model's context
   window (`skillListingBudgetFraction`, or a fixed character count in
   `SLASH_COMMAND_TOOL_CHAR_BUDGET`). Every name always appears; when the
   budget overflows, descriptions are dropped starting with the least-used
   skills. Each entry's text is capped at 1,536 characters
   (`skillListingMaxDescChars`).
2. **Invocation.** The user types `/name args`, or the model calls the
   `Skill` tool. Either way the rendered `SKILL.md` (substitutions done,
   injected commands run) enters the conversation **as one message and stays
   there** for later turns. The file is not re-read on later turns.
3. **Repeat.** Re-invoking a skill whose rendered text is identical adds a
   short "already loaded" note instead of a second copy; different text
   (new arguments, new command output) is appended in full.
4. **Compaction.** After a compaction the most recent invocation of each
   skill is re-attached, the first 5,000 tokens of each, within a combined
   25,000 tokens, most recent first.
5. **Blocked call.** If the model calls a skill marked
   `disable-model-invocation`, the call is refused and the model is told
   not to reproduce the steps another way.
6. Only skills can be reached through the `Skill` tool, plus a few built-in
   commands such as `/init`; `/compact` and other built-ins cannot.

Settings-level visibility, without editing the skill: `skillOverrides` maps
a skill name to `"on"`, `"name-only"`, `"user-invocable-only"` or `"off"`.
Permission rules `Skill(name)` / `Skill(name *)` allow or deny the model's
use; a bare `Skill` deny disables all model-invoked skills.

### 5.6 Forked skills (`context: fork`)

The skill body becomes the *task* of a new sub-agent of type `agent`. The
sub-agent does not see the conversation (despite the word "fork"), so the
body must stand alone. It runs in the background by default and its result
arrives in the conversation when done; the harness waits instead when
`background: false`, in `-p`, when background tasks are disabled, when the
same skill is already running, or when a scheduled task fired it. Edits made
by a backgrounded forked skill are outside the session's checkpoints.

### 5.7 Workspace trust

A project skill's `allowed-tools` and `hooks` are honoured even in a folder
that was never trusted and in `-p`. The docs flag this themselves: review
the `allowed-tools` of skills in a repository before running there. (See the
decision note in `permissions.md`; gnosh can hold project skills' grants
until the folder is trusted.)

---

## What gnosh adopts

**P0** — none. The first usable binary is print mode, where the only thing
needed from this file is that automatic compaction exists (`sessions.md`).

**P1**

- [ ] First, with the interactive session: command recognition at the start
      of a message, unknown-command report, `/clear` (`/reset`, `/new`),
      `/compact [instructions]`, `/model`, `/help`, `/exit` (`/quit`).
- [ ] In `-p`: a prompt starting with `/name` runs that skill or command.

- [ ] `/resume` (`/continue`), `/rename`, `/export`, `/rewind`
      (`/checkpoint`, `/undo`), `/branch`, `/context`, `/copy`.
- [ ] `/permissions` (`/allowed-tools`), `/plan`, `/config key=value`,
      `/add-dir`, `/hooks`, `/memory`, `/agents`, `/skills`,
      `/reload-skills`, `/mcp`, `/init`, `/tasks` (`/bashes`), `/status`,
      `/cost` (`/usage`, `/stats`), `/doctor`, `/diff`.
- [ ] `/loop` and `/goal` behaviours of sections 3 and 4.
- [ ] Skills: the five locations, precedence, watching; all frontmatter
      fields of 5.2 except `shell` and the three inert ones; substitutions;
      shell injection with its abort and permission rules; the listing with
      its budget; the `Skill` tool; re-attachment after compaction.
- [ ] `.claude/commands/*.md` with colon-named subdirectories.
- [ ] Command queueing while the model works; the `/` menu.

**P2**

- [ ] `context: fork`, `paths`, `skillOverrides`, stacking several skills,
      nested skills, added-directory prefixes.
- [ ] `/cd`, `/btw`, `/recap`, `/effort`, `/output-style`, `/statusline`,
      `/keybindings`, `/debug`, `/background`, `/fork`, `/subtask`.

## Deliberately not adopted

- Account, billing and Anthropic-service commands: `/login`, `/logout`,
  `/upgrade`, `/usage-credits`, `/privacy-settings`, `/passes`,
  `/rate-limit-options`, `/fast`, `/advisor`, `/feedback`, `/bug`,
  `/insights`, `/team-onboarding`, `/stickers`, `/radio`, `/mobile`,
  `/powerup`.
- Cloud, desktop, browser, IDE: `/teleport`, `/remote-control`,
  `/remote-env`, `/schedule`, `/autofix-pr`, `/ultrareview`, `/desktop`,
  `/chrome`, `/ide`, `/web-setup`, `/install-github-app`,
  `/install-slack-app`, `/voice`.
- Plugins, mods, workflows, artifacts, design: `/plugin`,
  `/reload-plugins`, `/plugin-authoring`, `/workflows`,
  `/workflow-authoring`, `/artifacts`, `/artifact-*`, `/design*`,
  `/slides`, `/dataviz`, `/skill-doctor`.
- `/auto-mode-setup`, `/sandbox`, `/setup-bedrock`, `/setup-vertex`,
  `/claude-api`, `/import`, `/heapdump`, `/restart`.
- Anthropic's bundled skill texts; skills synced from claude.ai; plugin
  skills and the `plugin:skill` namespace; the reserved names `synced` and
  `anthropic-skills`.
- `shell: powershell`.

## Unverified (docs silent)

- The name of the `Skill` tool's argument field (gnosh: `args`).
- `@path` expansion inside skill and command bodies.
- The exact text of the skill listing and of the "already loaded" note
  (gnosh writes its own).
- What `/export` writes for tool calls and thinking (see `sessions.md`).
- The full set of commands that run immediately instead of queueing.
