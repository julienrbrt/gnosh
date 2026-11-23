# interactive.md — the interactive session: input, keys, prompts, status

What this file is: how an interactive session *behaves* — what the keys do,
how the input box works, what a permission prompt offers, what the status
line is told — so Jae's hands work in gnosh as they do in Claude Code.

It is about behaviour only. gnosh has its own look: its own colours,
wording, working indicator, logo and layout. Nothing here asks gnosh to
resemble Claude Code on screen, and its text, spinner words, glyphs and art
must not be copied. Where this file quotes a Claude Code label it is to
identify the behaviour, not to prescribe the wording.

In one paragraph: the conversation is printed into the terminal's own
scrollback, like any command-line program, with an input box at the bottom.
Enter sends; a backslash before Enter (or Ctrl+J) makes a new line. A line
starting with `/` is a command, with `!` a shell command, and `@` completes
a file path. Esc stops the model; Esc twice opens rewind; Shift+Tab changes
the permission mode; Ctrl+C clears the input and, pressed again, quits.
While the model works you can keep typing — what you send is queued. When a
tool needs approval a prompt appears with yes, yes-and-remember, and no.

Source: code.claude.com/docs/en — interactive-mode, keybindings,
terminal-config, fullscreen, statusline, permissions, permission-modes,
checkpointing, model-config, common-workflows, settings-reference; docs
version 2.1.323, read 2026-11-13. Own words. "(unverified)" = docs silent.

---

## 1. Rendering

Claude Code has two renderers. gnosh copies the behaviour of the first.

| Renderer | Behaviour |
| :- | :- |
| **Classic (main screen)** | Output is written into the terminal's normal scrollback. The terminal's own search (Cmd+F), selection, copy, and tmux copy mode all work on the conversation. The input box follows the output down the screen. |
| Fullscreen (alternate screen) | Draws like `vim`: input fixed at the bottom, only visible messages rendered, in-app scrolling, mouse support. A research preview in Claude Code. Not adopted. |

Requirements that follow from "classic":

- Finished output is never redrawn above the live region; only the bottom
  area (in-progress message, working indicator, input box, status line) is
  repainted in place.
- **Ctrl+L** forces a full redraw without losing input or history.
- Nothing depends on the mouse.
- With `--ax-screen-reader` (P2): flat text, no borders, no animation.
- Tool calls are shown collapsed — a one-line summary such as the command
  and a line count of its output — unless verbose is on.
- Thinking is shown collapsed by default.
- After each turn a line reports how long it took (`showTurnDuration`).
- Prose may be wrapped at a column limit in wide terminals
  (`maxProseWidth`).
- Stdout not a terminal → there is no interactive session; the run behaves
  as `-p` (`cli.md`).

## 2. The input box

### 2.1 Sending and new lines

| Key | Effect |
| :- | :- |
| Enter | Send. |
| `\` then Enter | New line. Works in every terminal. |
| Ctrl+J | New line. Works in every terminal. |
| Shift+Enter | New line, in terminals that report it (iTerm2, WezTerm, Ghostty, Kitty, Warp, Windows Terminal natively). |
| Option+Enter | New line on macOS when Option is set as Meta. |
| Pasting | Multi-line text pastes as multi-line input; it does not send. |

### 2.2 First-character modes

| First character | Mode |
| :- | :- |
| `/` | Command or skill. A menu of matches opens and filters as letters are typed. |
| `!` | **Shell mode**: the rest is run directly as a shell command, without the model interpreting or approving it. Section 2.6. |
| `@` (anywhere) | File path mention with completion. Section 2.5. |
| `?` on an empty input | Toggle the shortcut help panel. |

(`:` for emoji shortcodes exists in Claude Code; not adopted. The older `#`
memory shortcut is no longer documented; not adopted.)

### 2.3 Editing keys

| Key | Effect |
| :- | :- |
| Ctrl+A / Ctrl+E | Start / end of the current line. |
| Alt+B / Alt+F | Back / forward one word. |
| Ctrl+K | Delete to end of line (kept for Ctrl+Y). |
| Ctrl+U | Delete from the cursor to the start of the line (kept); repeat to clear across lines. |
| Ctrl+W | Delete back to the previous whitespace (kept) — removes a whole path or `--flag=value` in one press. |
| Alt+D | Delete to end of word (kept). |
| Ctrl+Y | Paste the last deleted text; Alt+Y afterwards cycles older deletions. |
| Ctrl+_ | Undo the last input edit. |
| Ctrl+D with text in the input | Delete the character after the cursor. |
| Ctrl+G or Ctrl+X Ctrl+E | Open the input in the user's editor (`$VISUAL` / `$EDITOR`). |
| Ctrl+S | Stash the current input and clear it; on an empty input, restore the stash (text, cursor, pastes, mode). |

For the word keys a word is a run of letters and digits, so `_`, `.` and
`/` separate words; Ctrl+W ignores punctuation and stops only at
whitespace. These are fixed readline conventions and are not rebindable.

Vim editing (`editorMode: "vim"`: NORMAL / INSERT / VISUAL modes) is P2.

### 2.4 History

- **Up / Down** (or Ctrl+P / Ctrl+N): in a multi-row input they first move
  the cursor; from the first or last row they step through prompt history.
- History is kept **per working directory** and reaches back into earlier
  sessions of the same project. After `/clear` the new session's prompts
  come first.
- Sending the same prompt twice in a row records one entry.
- **Ctrl+R**: reverse search. Type to search; Ctrl+R again for older
  matches; Tab or Esc accepts the match for editing; Enter accepts and
  sends; Ctrl+C cancels and restores the original input; Backspace on an
  empty search cancels. The inline search covers prompts from all projects.
- A draft cleared with Ctrl+C or double Esc can be brought back with Up
  (the ten most recent cleared drafts are kept for the session).
- `!` history expansion is off.
- Stored in `history.jsonl` in the config directory (`sessions.md`).

### 2.5 `@` mentions

- Typing `@` opens path completion; Tab or Enter accepts.
- `@path/to/file` in a sent prompt **includes that file's content** in the
  conversation, when it fits Read's limits (25,000 tokens; a text file over
  256 KB is not included). No tool call happens, so `PreToolUse` hooks do
  not fire for it; `Read` deny rules are honoured.
- `@path/to/dir` includes a **listing** of the directory, not contents.
- Paths may be relative or absolute; several mentions may appear in one
  message.
- Mentioning a file also loads the memory files of its directory chain.
- The picker leaves out gitignored files (`respectGitignore`); a custom
  command may supply suggestions (`fileSuggestion`).
- `CLAUDE_CODE_DISABLE_FILE_MENTIONS=1`: the model gets the paths with a
  note to read them itself.
- `@server:resource` (MCP resources) and `@agent-name` are P2.

### 2.6 Shell mode (`!`)

- The command and its output are **added to the conversation**.
- Output streams as it is produced; Ctrl+B backgrounds a long command.
- By default the model then **responds to the output** without a further
  prompt; `respondToBashCommands: false` only adds it to context.
- Tab completes from earlier `!` commands of the project; a token
  containing `/` completes file paths.
- Leave shell mode with Esc, Backspace, or Ctrl+U on an empty input.
- Pasting text that starts with `!` into an empty input enters shell mode.
- These commands are the user's own: no permission prompt, no sandbox.

### 2.7 Pasting

- A paste of more than **800 characters or more than three lines** is
  collapsed in the input to a placeholder (`[Pasted text #1 +120 lines]`);
  the full text is still sent, and is kept so that recalling the prompt
  from history later sends it again.
- Deleting into a placeholder with a word or line key removes it whole;
  Ctrl+Y restores it.
- Pasted text is marked for the model as pasted, not typed: the model is
  told a paste may contain instructions the user did not write and to follow
  them only where the typed message asks.
- Invisible Unicode characters in a prompt are removed on Enter and the
  cleaned prompt is put back for a second Enter.
- **Images** (P2): Ctrl+V pastes an image from the clipboard as an
  `[Image #N]` chip at the cursor; dragging or pasting an image path also
  attaches it.

### 2.8 Typing while the model works

- Enter while a turn is running **queues** the message; queued entries are
  listed until sent.
- A queued *message* is given to the model as soon as the tool calls it is
  running finish, inside the same turn; if the turn ends first, queued
  messages go out in order with no further key press.
- Queued *commands* and `!` commands wait until the turn ends and then run
  one at a time, in order.
- **Up** from the first input row takes queued entries back into the input.
- **Ctrl+Enter** (or Ctrl+X Ctrl+S) sends the queue now: work that can move
  to the background does, and the model reads the messages in the same
  turn; otherwise the turn is interrupted.
- A few commands act at once instead of queueing (`/status`, `/tasks`,
  `/model`, `/permissions`).

## 3. Keys outside the input

| Key | Effect |
| :- | :- |
| **Esc** | Interrupt the model: stop the current response or tool call so the user can redirect. Work done so far is kept. Queued messages are sent next. If a dialog is open, Esc closes it; on a permission prompt it declines (the same as "no" without a comment). |
| **Esc Esc** | With text in the input: clear it (saved to history). With an empty input: open the rewind menu (`sessions.md`). |
| **Ctrl+C** | While something runs: interrupt it. Otherwise the first press clears the input and a second press exits. |
| **Ctrl+D** | Exit: the first press shows a hint, a second within 800 ms exits. (With text in the input it deletes a character.) |
| **Shift+Tab** | Cycle the permission mode: `default` → `acceptEdits` → `plan` → (`bypassPermissions` if available) → back. On a file-edit prompt it picks "allow for the rest of the session"; on the plan approval prompt it approves the plan. |
| **Ctrl+O** | Toggle the detailed transcript view: full tool input and output, thinking, a timestamp and the model on each assistant message, and anything shown collapsed. |
| **Ctrl+B** | Move the running foreground work — a shell command or a sub-agent — to the background. (Under tmux, press twice.) |
| **Ctrl+R** | History search (2.4). |
| **Ctrl+L** | Redraw the screen. |
| **Ctrl+T** | Show or hide the model's task checklist. (gnosh: show or hide the current `mem` plan.) |
| **Ctrl+Z** | Suspend to the shell; `fg` resumes. |
| Ctrl+X Ctrl+K | Stop all running background sub-agents; press twice within 3 seconds. |
| Alt+P | Switch model without clearing the input. (P2) |
| Alt+T | Toggle thinking. (P2) |
| Left / Right | Switch tabs in dialogs. |
| Tab | Accept the highlighted completion; on a permission prompt, open or close the comment field. |

In the transcript view (Ctrl+O): `q`, Ctrl+C or Esc leave it; Ctrl+E toggles
"show all".

**Key bindings file** (P2): `~/.claude/keybindings.json` — an object with a
`bindings` array; each block has a `context` (`Global`, `Chat`,
`Autocomplete`, `Confirmation`, `Transcript`, `HistorySearch`, `Task`, ...)
and a map from keystroke (`"ctrl+e"`) to an action name
(`"chat:externalEditor"`) or `null` to unbind. Changes apply without a
restart. `/keybindings` opens the file.

## 4. The permission prompt

Detailed rules are in `permissions.md`; this is the interaction.

- It names the tool, shows what is about to happen (for a shell command its
  description and the command; for an edit the diff; for a fetch the URL),
  says why approval is needed, and offers choices.
- **Shell command**: *yes* · *yes, and don't ask again for `<rule>`* (the
  rule is shown, e.g. `npm test *`; it is saved to local settings) · *no*.
- **File edit**: *yes* · *yes, for the rest of the session* · *no*. The
  proposed change can be edited before accepting.
- **Web fetch**: *yes, this fetch* · *yes, and don't ask again for this
  domain* · *no*.
- **Writes under the project's `.claude/` or `~/.claude/`**: may offer a
  session-long grant for that folder.
- **Esc** = no. **Tab** on *yes* or *no* opens a comment field; Enter sends
  the answer with the comment (after the result for yes; as the reason for
  no). Not offered on fetch prompts, nor on the options that remember.
- **No with no comment, in the main conversation, ends the turn.** No with
  a comment lets the model continue with the reason.
- The remembering options are offered only when the prompt can show
  everything the saved rule would allow; otherwise only a one-time yes.
- A prompt raised by a hook's `ask` shows where the hook came from
  (`[settings]`, `[skill]`).
- A background sub-agent's prompt appears here too, naming the sub-agent.
- While a prompt waits, the status line is hidden.
- After about six seconds without a keystroke the `Notification` hook fires
  with `permission_prompt`.
- Prompts do not time out (exceptions in `permissions.md`).

`AskUserQuestion` is a sibling dialog: one to four questions, each with two
to four options plus a free-text "other"; multi-select when the question
says so.

**Plan approval**: the plan is shown with *approve and auto-accept edits* ·
*approve, approve each edit* · *keep planning*; Ctrl+G opens the plan in the
editor first.

## 5. The status line

A user-supplied command, set as `statusLine` in settings:

```json
{ "statusLine": { "type": "command", "command": "~/.claude/statusline.sh", "padding": 2, "refreshInterval": 5 } }
```

- The command receives a JSON object on **stdin** and whatever it prints on
  stdout is shown below the input: several lines allowed, ANSI colours and
  OSC 8 links allowed.
- The output is captured, so the script cannot query the terminal size; the
  harness sets `COLUMNS` and `LINES` in its environment.
- It runs once at session start (and on resume) and again when: a new
  assistant message arrives, a compaction finishes, the permission mode
  changes, vim mode toggles, the `command` setting changes, or the
  `refreshInterval` (seconds, minimum 1) elapses.
- Updates are **debounced at 300 ms**; a run still in flight when a new
  update is due is cancelled.
- It is hidden during some interactions (help, permission prompts).
- It runs under the same workspace-trust rule as hooks, and is switched off
  by `disableAllHooks`.

JSON on stdin (fields gnosh provides in **bold**):

```json
{
  "cwd": "/current/working/directory",
  "session_id": "abc123...",
  "session_name": "my-session",
  "prompt_id": "550e8400-...",
  "transcript_path": "/path/to/transcript.jsonl",
  "model": { "id": "model-id", "display_name": "Name" },
  "workspace": {
    "current_dir": "/current/working/directory",
    "project_dir": "/original/project/directory",
    "added_dirs": [],
    "git_worktree": "feature-xyz",
    "repo": { "host": "github.com", "owner": "org", "name": "repo" }
  },
  "version": "x.y.z",
  "output_style": { "name": "default" },
  "cost": {
    "total_cost_usd": 0.01234,
    "total_duration_ms": 45000,
    "total_api_duration_ms": 2300,
    "total_lines_added": 156,
    "total_lines_removed": 23
  },
  "context_window": {
    "total_input_tokens": 15500,
    "total_output_tokens": 1200,
    "context_window_size": 200000,
    "used_percentage": 8,
    "remaining_percentage": 92,
    "current_usage": {
      "input_tokens": 8500, "output_tokens": 1200,
      "cache_creation_input_tokens": 5000, "cache_read_input_tokens": 2000
    }
  },
  "exceeds_200k_tokens": false,
  "effort": { "level": "high" },
  "thinking": { "enabled": true },
  "vim": { "mode": "NORMAL" },
  "agent": { "name": "security-reviewer" },
  "worktree": { "name": "…", "path": "…", "branch": "…", "original_cwd": "…", "original_branch": "…" }
}
```

| Field | Notes |
| :- | :- |
| **`cwd`**, **`workspace.current_dir`** | Same value; the second is preferred. |
| **`workspace.project_dir`** | Where the session was started. |
| **`workspace.added_dirs`** | Empty array when none. |
| **`session_id`**, **`transcript_path`**, **`version`** | `version` is the harness version (gnosh's own). |
| **`session_name`** | Absent until a name or generated title exists. |
| **`model.id`**, **`model.display_name`** | |
| **`cost.*`** | `total_cost_usd` is an estimate (0 without a price table) and resets on `/clear`; `total_duration_ms` accumulates across resumes. |
| **`context_window.*`** | From the most recent model response. `used_percentage` counts input tokens only (input + cache write + cache read) against `context_window_size`. `current_usage` is `null` before the first response and right after a compaction; the percentages may be `null` early. |
| **`output_style.name`** | |
| `prompt_id`, `effort`, `thinking`, `vim`, `agent`, `worktree`, `workspace.git_worktree`, `workspace.repo` | Present only when applicable. |
| `prompt_cache` | Adopted 2026-11-17 in the fields gnosh can tell from the providers' cache counts (llama.cpp's cache_n, cached_tokens): `warm`, `caching_observed`, `requests`, `misses`, `expected_rebuilds`, `hit_ratio`, `cache_write_tokens`, `miss_recache_tokens`, `last_miss_at`. A model server of the person's own has no cache lifetime, so `ttl`, `expires_at` and the miss causes are left out. `/cost` says the same in words. |
| `rate_limits`, `fast_mode`, `pr` | Anthropic account and PR-badge data: not adopted. |

With or without a custom status line, the footer always shows the current
permission mode.

## 6. Working indicator, thinking, notifications

- **Working indicator**: while a turn runs, an animated indicator with a
  rotating word. The words are configurable (`spinnerVerbs`: `append` or
  `replace`); a one-line tip under it can be turned off
  (`spinnerTipsEnabled`); a running hook can set the text
  (`statusMessage`). The docs do not specify what else it shows; gnosh's
  own indicator also shows elapsed time and how to interrupt. gnosh
  supplies its own words and style.
- **Thinking**: collapsed by default; Ctrl+O shows it, set apart from the
  answer (Claude Code uses grey italics). With Anthropic's API the text is a
  summary and may be redacted unless `showThinkingSummaries` is set; gnosh
  shows whatever its model actually returns. `alwaysThinkingEnabled: false`
  and `MAX_THINKING_TOKENS=0` turn thinking off. The word `ultrathink` in a
  prompt asks for deeper reasoning for that turn (P2).
- **Terminal notifications**: when the model finishes or waits on a prompt
  and the user appears to be away, a notification is raised — a desktop
  notification in terminals that support the escape sequence (iTerm2, Kitty,
  Ghostty), the bell with `preferredNotifChannel: "terminal_bell"`,
  otherwise nothing — and the `Notification` hook fires (`idle_prompt`
  after 60 s, `permission_prompt` after 6 s).
- **Terminal title**: set from the session name; a progress indication may
  be sent to terminals that support it (`terminalProgressBarEnabled`);
  `CLAUDE_CODE_DISABLE_TERMINAL_TITLE=1` turns both off.
- **Background work**: running background shells and sub-agents are shown
  below the input; `/tasks` lists and stops them.
- **Session recap** (P2): on return after three idle minutes, a one-line
  recap of the session (at most 400 characters).

## 7. Startup and exit

- First start in a folder: the workspace trust dialog (`permissions.md`).
- A broken settings file: a dialog offering to fix it, exit, or continue
  without it (`settings.md`).
- Starting in `bypassPermissions` for the first time: a warning that must be
  accepted.
- `SessionStart` hooks run in the background at launch; typing is possible
  at once, and the first response waits for them.
- On exit: `SessionEnd` hooks (1.5 s budget), background tasks stopped, and
  a hint showing how to resume the session.

---

## What gnosh adopts

**P0** — none; the first usable binary is print mode. Everything here is P1
unless marked.

**P1**

- [ ] Inline rendering into native scrollback; live region repainted in
      place; Ctrl+L redraw; collapsed tool calls; Ctrl+O detail view.
- [ ] Input: Enter, `\`+Enter, Ctrl+J, Shift+Enter where reported;
      multi-line paste; the editing keys of 2.3; stash (Ctrl+S); external
      editor (Ctrl+G).
- [ ] `/` menu, `!` shell mode with `respondToBashCommands`, `@` file and
      directory mentions with completion.
- [ ] History per directory, Up/Down rules, Ctrl+R search, cleared-draft
      recall.
- [ ] Paste collapsing at 800 characters / three lines, with the paste
      marked as pasted for the model; invisible characters taken out on
      Enter (`spec/DECISIONS.md` 72).
- [ ] Queueing while the model works; Up to take back.
- [ ] Esc, Esc-Esc, Ctrl+C (clear, then exit), Ctrl+D (double press),
      Shift+Tab mode cycle, Ctrl+B, Ctrl+Z.
- [ ] Permission prompts per section 4, including the Tab comment field and
      "no without a comment ends the turn".
- [ ] `statusLine` command with the bold JSON fields, 300 ms debounce,
      `COLUMNS`/`LINES`, `refreshInterval`, `padding`.
- [ ] Working indicator with elapsed time and interrupt hint; turn-duration
      line; thinking collapsed with Ctrl+O to expand.
- [ ] Bell / desktop notification when waiting; terminal title.
- [ ] Trust dialog, settings-error dialog, bypass warning, resume hint on
      exit.

**P2**

- [ ] Image paste; `@server:resource`; `@agent`; vim mode;
      `keybindings.json`; Ctrl+Enter send-now; Alt+P / Alt+T; session
      recap; `--ax-screen-reader`; `maxProseWidth`; `spinnerVerbs`;
      `fileSuggestion`; Ctrl+X Ctrl+K.

## Deliberately not adopted

- Claude Code's visual identity: its colours, logo and mascot art, spinner
  words and glyphs, tip texts, the wording of its labels and prompts.
- The fullscreen (alternate-screen) renderer and everything specific to it:
  mouse selection, in-app scrolling, `/tui`, `/focus`, `/scroll-speed`, the
  diff panel.
- Emoji shortcodes, spell checking, voice dictation, the git-history
  example in a new session's box (the next-prompt suggestions are adopted,
  off unless set: `spec/DECISIONS.md` 71), "you should know" notes, `/btw` panel, PR status badge, issue links,
  usage-limit waiting, fast mode toggle (Alt+O).
- The task checklist as a todo-tool view (Ctrl+T shows the `mem` plan
  instead).
- The `#` memory shortcut.
- `rate_limits`, `fast_mode`, `pr` in the status-line JSON (`prompt_cache` is adopted).

## Unverified (docs silent)

- The exact contents and layout of the default footer and of the working
  indicator (and they are not to be copied anyway).
- Whether Esc during a tool call kills a running shell command or leaves it
  (gnosh: kills the foreground command; background ones keep running).
- The precise rule for when Up moves the cursor versus recalling history in
  a single-line input (gnosh: history when the cursor is on the first row).
- How `@` completion ranks matches.
- What the default exit hint says.
