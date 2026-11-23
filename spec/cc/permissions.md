# permissions.md — modes, rules, prompts, trust

*Since 14 November 2026 gnosh is configured by one JSON file, `gnosh.json`, and no longer by `gnoshrc` (`spec/DECISIONS.md`, items 17 and 23 to 28). Where this file speaks of `gnoshrc`, of its directives or of its dashed option names, it records what the build was first given.*

What this file is: how gnosh decides whether a tool call runs, asks, or is
refused, using Claude Code's modes and rule syntax so that Jae's existing
`permissions` blocks and `--allowedTools` habits carry over unchanged.

In one paragraph: every tool call is checked against three lists of rules —
deny, ask, allow, in that order, first match wins — and then against the
session's *mode*, which says what runs without asking when no rule spoke.
`default` asks before anything that changes files, runs commands or touches
the network. `acceptEdits` stops asking about file edits inside the project.
`plan` lets the model look but not change. `dontAsk` refuses whatever would
have asked. `bypassPermissions` asks about almost nothing. A rule is a tool
name, optionally with a pattern in parentheses: `Bash(git diff *)`,
`Edit(/src/**)`, `WebFetch(domain:example.com)`. A few dangerous things
(writes to `.git`, `rm -rf ~`) are checked before any allow rule and cannot
be pre-approved.

Source: code.claude.com/docs/en — permissions, permission-modes,
settings-reference (permission settings), tools-reference, headless,
security; docs version 2.1.323, read 2026-11-13. Own words; rule strings,
key names and quoted messages are literal. "(unverified)" = docs silent.

---

## 1. How one tool call is decided

The docs give the pieces below but no single flowchart; this order is the
one consistent with all of them, and it is the order gnosh implements.

1. **`PreToolUse` hooks** run first. A hook that blocks (exit code 2 or a
   `deny` decision) stops the call before any rule is looked at — so a
   blocking hook beats an allow rule. A hook may also answer `allow`, `ask`
   or `defer`, and may rewrite the input (`hooks.md`).
2. **Deny rules.** Any match refuses the call. A hook's `allow` does not
   override a deny rule.
3. **Ask rules.** Any match forces a prompt — even when a hook said `allow`,
   and even in `acceptEdits`, `auto` and `bypassPermissions`. In `dontAsk`
   the call is denied instead.
4. **Safety checks that no allow rule can pass**: writes to protected paths
   (section 9), recognised critical-path removals (section 10), the tools
   that need a person (`AskUserQuestion`). These follow their own per-mode
   table.
5. **Allow rules** (settings, `--allowedTools`, session grants from earlier
   prompts, a skill's `allowed-tools`) and a hook's `allow`: the call runs.
   Allow rules have no effect in `bypassPermissions` (everything already
   runs).
6. **The mode's baseline** (section 2): runs, or needs approval.
7. **Needs approval**: `PermissionRequest` hooks may answer. Otherwise an
   interactive session shows the prompt (section 12); a session that cannot
   prompt denies the call (section 13).

Permission rules are enforced by the harness, never by the model: text in a
prompt or a memory file changes what the model tries, not what is allowed.

## 2. Modes

| Mode | Runs without asking | Notes |
| :- | :- | :- |
| `default` | Reads inside the working directories; the built-in read-only shell commands. | Shown to the user as **Manual**; `manual` is accepted as an alias anywhere the value is typed. |
| `acceptEdits` | The above, plus file edits and a few filesystem commands inside the working directories. | Exact scope in 2.2. |
| `plan` | Reads and read-only commands; nothing that edits. | The model researches and proposes; edits stay blocked until the plan is approved. 2.3. |
| `dontAsk` | What `default` runs, plus whatever allow rules or a `PreToolUse` hook approve. Everything else is **denied**, never prompted. | For CI. Never appears in the Shift+Tab cycle. |
| `bypassPermissions` | Everything, including protected-path writes. | Deny rules still block; ask rules still prompt; critical-path removals still prompt. 2.4. |
| `auto` | Everything, after review by a second model (Anthropic's classifier). | Not adopted; see 2.5. |

Deny rules block in every mode. Ask rules prompt in every mode (deny in
`dontAsk`).

### 2.1 Which mode a session starts in

First that applies:

1. `--permission-mode <mode>` or `--dangerously-skip-permissions`.
2. `permissions.defaultMode` from settings, by settings precedence.
   `bypassPermissions` (and `auto`) set in **project or local** settings do
   not take effect — a repository must not be able to switch prompts off; the
   session then starts in `default`. The other values apply from any file.
3. The built-in default: `default` for `-p` runs and for gnosh interactive
   sessions. (Claude Code's own interactive default became `auto` in
   v2.1.283; gnosh has no classifier, so its interactive default is
   `default`.)

Switching during a session: **Shift+Tab** cycles `default` → `acceptEdits` →
`plan` → back. `bypassPermissions` joins the cycle (after `plan`) only if the
session was started with it available: `--permission-mode bypassPermissions`,
`--dangerously-skip-permissions`, `--allow-dangerously-skip-permissions`
(adds it without starting in it), or `defaultMode: "bypassPermissions"` in
user settings. It cannot be entered from a session started without it.
`dontAsk` is never in the cycle.

The status area names the mode. Claude Code's wording, for reference only
(gnosh words its own): `manual mode on`, `accept edits on`, `plan mode on`,
`don't ask on`, `bypass permissions on`.

### 2.2 What `acceptEdits` covers, exactly

Runs without asking:

- Creating and editing files with the file tools.
- These shell commands: `mkdir`, `touch`, `rm`, `rmdir`, `mv`, `cp`, `sed`.
  All but `touch` also when prefixed by a safe environment assignment
  (`LANG=C`, `NO_COLOR=1`) or a process wrapper (`timeout`, `nice`, `nohup`).

Only for paths **inside the working directory or an additional directory**,
and only if the path still resolves inside after following symlinks.

Still prompts:

- Paths outside that scope.
- Writes to protected paths.
- Recognised `rm`/`rmdir` critical-path removals.
- `touch` with anything beyond plain file paths (an option, a glob, a prefix
  such as `timeout 5` or `LANG=C`).
- Every other shell command outside the built-in read-only set.
- Web fetches and searches.

### 2.3 Plan mode

- Entered with Shift+Tab, with `--permission-mode plan`, by prefixing one
  prompt with `/plan`, or by the model calling `EnterPlanMode`.
- The model reads files and runs exploring commands, and writes a plan. File
  edits are blocked. Shell commands outside the read-only set prompt.
- A `PreToolUse` hook's `allow` has no effect on an edit or other
  non-read-only call while planning (it still applies to `ExitPlanMode`).
- When the plan is ready (`ExitPlanMode`) the user chooses:
  *approve and auto-accept edits* (session moves to `acceptEdits`),
  *approve and approve each edit by hand* (moves to `default`), or
  *keep planning* (stays, with the user's feedback). If the session was
  started with bypass available, the first choice instead switches to
  `bypassPermissions` and says so loudly. Ctrl+G opens the plan in the
  user's editor before deciding. Shift+Tab at the approval prompt approves
  the plan; Esc first, then Shift+Tab, leaves plan mode.
- Approving also gives an unnamed session a title derived from the plan.
- With `showClearContextOnPlanAccept`, an extra first choice approves the
  plan and clears the planning context.
- Exception copied from Claude Code: in an interactive terminal session with
  bypass available, plan mode's blocks are not enforced (the model is still
  told to plan). In `-p` they always are.

gnosh keeps the plan text in `mem` rather than in a plan file; the mode
semantics above are unchanged.

### 2.4 `bypassPermissions`

- Skips prompts, including for protected paths.
- Still applies: deny rules; ask rules; `AskUserQuestion`; critical-path
  removals (a prompt with a two-minute countdown in the terminal; denied
  where nobody can answer).
- **Refuses to start as root or under sudo** on Linux and macOS, with
  `--dangerously-skip-permissions cannot be used with root/sudo privileges for security reasons`
  (skipped inside a recognised sandbox).
- First interactive use shows a warning dialog asking the user to accept
  responsibility. Accepting writes `skipDangerousModePermissionPrompt: true`
  to user settings; declining exits. No dialog in `-p`.
- `permissions.disableBypassPermissionsMode: "disable"` in any settings file
  makes the flag an error and the mode unavailable.

### 2.5 `auto` — not adopted

Claude Code's `auto` sends each non-trivial action to a separate classifier
model that blocks what goes beyond the request. gnosh has no classifier.
Claude Code's documented fallback when `auto` is selected but unavailable is
to start in `default`; gnosh does the same and prints one line saying so.
The value is accepted everywhere so existing settings files load.

## 3. Rule syntax

A rule is `Tool` or `Tool(specifier)`. Parentheses inside the specifier are
literal and need no escaping.

| Form | Meaning |
| :- | :- |
| `Bash` | Every use of the tool. `Bash(*)` is the same. |
| `Bash(npm run build)` | Tool-specific specifier (sections 4–7). |
| `Tool(param:value)` | **Deny and ask only.** Matches a top-level scalar input parameter: `Agent(model:opus)`, `Bash(run_in_background:true)`. `*` in the value is a wildcard; an omitted parameter never matches; one parameter per rule; whitespace around the colon is ignored. Cannot be used on a tool's primary field (`command`, `file_path`, `path`, `notebook_path`, `url`) — such a rule is ignored with a startup warning. |
| `*`, `mcp__*` | **Deny and ask only.** Glob in the tool-name position; must match the whole name. |
| `mcp__server__get_*` | Allow rules may glob only after a literal `mcp__<server>__` prefix. An unanchored allow glob (`*`, `B*`, `mcp__*`) is skipped. |

More rules about rules:

- **A bare-name deny removes the tool from the model's view** — the model
  never sees it. A scoped deny (`Bash(rm *)`) leaves the tool and refuses
  matching calls.
- Names are the canonical tool names (`tools.md`), not display labels. A
  deny or ask rule naming no known tool gets a startup warning (names with
  `_` or `*`, and removed tools such as `TaskOutput`, are exempt).
- Specificity never matters: deny, then ask, then allow; an allow rule
  cannot carve an exception out of a deny, nor out of an ask.

## 4. Bash rules

A Bash rule is matched against the command text. `*` matches any text,
spaces included. A rule with no `*` matches one exact command.

| Rule | Matches | Does not match |
| :- | :- | :- |
| `Bash(npm run build)` | `npm run build` | `npm run build --watch` |
| `Bash(npm run *)` | `npm run build`, `npm run test --watch`, `npm run` | `npm install` |
| `Bash(git log * main)` | `git log --oneline main`, `git log -5 main` | `git log main`, `git push origin main` |
| `Bash(git * main)` | `git merge main`, `git push origin main` | `git log` |
| `Bash(* --version)` | `node --version` | `node -v` |
| `Bash(ls *)` | `ls -la`, `ls` | `lsof` |
| `Bash(ls*)` | `ls -la`, `lsof` | |
| `Bash(* --help *)` | `npm --help x` | `npm --help` |

1. `*` stands for whatever text is in its place, anywhere in the rule.
2. A trailing ` *` (space, star) also matches the bare command — but only
   when it is the rule's **only** wildcard.
3. The space before a trailing `*` is part of the rule (`ls *` ≠ `ls*`).
4. `:*` at the very end is another spelling of a trailing ` *`
   (`Bash(ls:*)` = `Bash(ls *)`). Anywhere else the colon is literal.
5. Startup warnings: an allow rule with a `*` before the subcommand
   (`Bash(git * main)`); an allow rule with a `*` inside a variable's value
   (`Bash(PORT=* npm run dev)`, v2.1.323).

### 4.1 Compound commands

- Separators: `&&`, `||`, `;`, `|`, `|&`, `&`, and newlines.
- **Allow**: every sub-command must be allowed on its own (by a rule or by
  being read-only). `Bash(safe-cmd *)` does not permit
  `safe-cmd && other-cmd`.
- **Deny and ask**: apply when **any** sub-command matches, including one
  nested in a subshell, a command substitution, or a loop body.
- A dangling operator (`npm test &&`) makes the command unparseable: it is
  not split and no allow rule approves it.
- "Yes, and don't ask again" on a compound command saves one rule per
  sub-command that needed approval, up to 5; a `cd` to a directory outside
  the working directories yields a `Read` rule for that path.

### 4.2 What is stripped before matching

- Wrappers: `timeout`, `time`, `nice`, `nohup`, `stdbuf`, the builtins
  `command` and `builtin`, zsh's `noglob`. Not stripped: `command -v`,
  `nocorrect`.
- A leading assignment of certain known-safe environment variables
  (`NODE_ENV=test npm test` matches `Bash(npm test *)`). An **allow** rule
  does not match past any other assignment; **deny and ask** rules match
  past any (`FOO=bar rm -rf tmp/` still hits a `Bash(rm *)` deny). The list
  of "known-safe" variables is not published (unverified); gnosh starts with
  `LANG`, `LC_*`, `NO_COLOR`, `NODE_ENV`, `TERM`, `TZ`, `CI`.
- Bare `xargs` (no flags). `xargs -n1 grep` is matched as `xargs`.
- The list is fixed. Runners such as `npx`, `docker exec`, `direnv exec`,
  `devbox run`, `mise exec` are not stripped, so `Bash(devbox run *)`
  matches whatever follows `run`.

### 4.3 What a prefix rule cannot approve

A rule containing `*` never auto-approves:

- exec wrappers: `watch`, `setsid`, `ionice`, `flock`;
- `find` with an action that runs, deletes or writes: `-exec`, `-delete`,
  `-rm`, `-fprint`, or with `-files0-from`.

Only an exact rule (no `*`) for the full command line approves those.

### 4.4 Redirections

- Output targets (`> f`, `>> f`, `2> f`) are checked as if the model wrote
  the file: `Edit` rules, protected paths, working directories. A rule such
  as `Bash(git commit *)` allows the command, not the target. A target that
  starts with `~` or contains a glob character needs approval.
- Input targets (`< f`) are checked against `Read` rules and the working
  directories; outside them needs approval unless an allow rule covers it; a
  glob, or a relative path after a `cd` in the same command, always needs
  approval.
- `tee` targets are checked like output redirects, pipelines included.
- Not checked: `/dev/null`, descriptor forms (`2>&1`, `<&3`), here-docs and
  here-strings.

### 4.5 Read-only commands

A built-in, non-configurable set runs without a prompt in every mode. The
docs name: `ls`, `cat`, `echo`, `pwd`, `head`, `tail`, `grep`, `find`, `wc`,
`which`, `diff`, `stat`, `du`, `cd`, and read-only forms of `git`. The full
set is not published (unverified); gnosh starts with that list plus the
obviously read-only `git` subcommands (`status`, `diff`, `log`, `show`,
`branch` without arguments, `rev-parse`, `ls-files`, `blame`).

- A compound command runs unprompted when each part qualifies.
- Unquoted globs are fine for commands whose every flag is read-only
  (`ls *.ts`, `wc -l src/*.py`).
- An ask or deny rule overrides the set.

Still prompts in `default` mode:

- an unquoted glob with a command that has write- or exec-capable flags
  (`find`, `sort`, `sed`, `git`);
- `ps` forms that could print environment variables (`ps auxe`, `ps aux -e`);
- `which` with flags beyond lookup; `file` with `-m`/`-f`;
- a command that sets, unsets or loops over special variables such as
  `PATH` or `IFS`;
- a command the parser cannot fully parse, or longer than 10,000 characters;
- `cd` into a *different* directory combined with `git` (the new directory's
  git hooks could run);
- `cd` combined with a redirect whose target depends on the directory
  (`/dev/null` is fine).

`cd` into a path inside the working directories is itself read-only.

### 4.6 Limits worth stating in gnosh's docs

A Bash rule matches the text the model wrote. It does not match the same
program reached another way: `/usr/bin/curl`, `sh -c 'curl …'`,
`git -C . push`. Deny and ask rules are a guard against the usual form, not
a security boundary. Rules that try to constrain arguments
(`Bash(curl http://github.com/ *)`) are fragile.

## 5. Read and Edit rules (file paths)

- `Edit(path)` covers **every built-in tool that edits files**: `Edit`,
  `Write`, `NotebookEdit`.
- `Read(path)` covers `Read`, and best-effort `Grep`, `Glob`, `@file`
  mentions, pasted image paths and directory listings.
- An `Edit` **allow** also grants reading the same path.
- A `Read` **deny** also blocks `Edit` and `Write` on that path, including
  creating a new file (not `NotebookEdit`).
- `Write(path)`, `NotebookEdit(path)`, `MultiEdit(path)`, `Glob(path)`,
  `Grep(path)` are accepted and **never match**; write the `Edit` or `Read`
  rule instead. (Bare `Write`, `Glob`, ... still match the whole tool.)
- Read/Edit deny rules also apply to file commands the harness recognises in
  Bash (`cat`, `head`, `tail`, `sed`, `tee`) and to redirect targets. They
  do not reach commands that read without naming the file (`grep -r x .`) or
  arbitrary subprocesses.
- `.claudeignore` has no effect.

### 5.1 The four anchors

Patterns use gitignore syntax: `*` stays within one path segment, `**`
crosses directories.

| Pattern | Anchored at | Example |
| :- | :- | :- |
| `//path` | Filesystem root (a true absolute path) | `Read(//Users/alice/secrets/**)` |
| `~/path` | Home directory | `Read(~/Documents/*.pdf)` |
| `/path` | **The settings source**, not the filesystem root | `Edit(/src/**/*.ts)` |
| `path` or `./path` | Current directory | `Read(*.env)` |

Where a single-slash `/path` anchors:

| Rule defined in | `/path` means |
| :- | :- |
| Project or local settings | `<primary working directory>/path` |
| User settings | `~/.claude/path` |
| A file given to `--settings` | `<that file's directory>/path` |
| CLI flags, session rules | `<primary working directory>/path` |

So `Read(/secrets/**)` in *user* settings guards `~/.claude/secrets`, not a
project's `secrets/`. To write a user-level rule that applies in every
project use `//` or `~/`.

### 5.2 Depth

- A bare filename matches at any depth below the anchor: `Read(.env)` =
  `Read(**/.env)`.
- A relative pattern with one directory segment, such as `src/**`:
  as an **allow** rule it matches only `<cwd>/src/...`; as a **deny or ask**
  rule it matches a `src` directory at any depth below the current
  directory.
- Every other shape matches the same in all rule types: `Edit(/src/**)` and
  `Edit(src/components/**)` only at their anchored place, `Edit(**/src/**)`
  at any depth.

### 5.3 Details

- A deny or ask pattern starting with `!` is a gitignore negation: it carves
  its matches out of the `path`/`./path` rules listed **before** it in the
  same source. It is always read relative to the current directory, cannot
  reopen a file inside a directory blocked as a whole, and never affects
  rules from another source.
- A rule saved from a prompt escapes gitignore characters in the approved
  path so it matches only that literal path.
- A deny or ask rule whose pattern is unusable still guards that exact path;
  an unusable allow rule approves nothing.
- **Symlinks**: an allow rule applies only when both the requested path and
  its resolved target match; a deny rule applies when either does. `Edit`
  and `Write` refuse a path that is itself a symlink. A path whose links
  cannot be resolved (a loop) is refused.

## 6. WebFetch rules

`WebFetch(domain:<host>)` matches the URL's hostname, case-insensitively,
ignoring a trailing dot.

| Rule | Matches |
| :- | :- |
| `WebFetch(domain:example.com)` | `example.com` only. |
| `WebFetch(domain:*.example.com)` | Any subdomain at any depth; not `example.com` itself. |
| `WebFetch(domain:*)` | Every host. |
| `WebFetch(domain:example.*)` | `example.org`; not `example.evil.com` — outside a leading `*.` or a bare `*`, the wildcard cannot cross a dot. |
| `WebFetch` (bare) | allow: fetch anything without asking. deny: remove the tool. |

`WebSearch` takes no specifier.

## 7. Other rule families

- **MCP**: `mcp__<server>` or `mcp__<server>__*` = every tool of that
  server; `mcp__<server>__<tool>` = one tool. Characters such as `.` or
  spaces in a server name become `_`. Allow rules for MCP tools take no
  parenthesised specifier (one that has it matches nothing; a settings file
  skips any `mcp__` rule with parentheses and reports it).
- **Agent**: `Agent(Explore)`, `Agent(my-agent)` — used in deny to switch a
  sub-agent type off.
- **Skill**: `Skill(name)` exact; `Skill(name *)` the name with any
  arguments; `Skill(skill:name)` (deny/ask) matches the skill under any of
  its names.
- **Cd**: governs the user's `/cd` command only (the model cannot call it).
  Bare `Cd` deny disables `/cd`; any `Cd` allow rule turns `/cd` into an
  allowlist. Paths use the same anchors but match whole directory paths
  (`*` = one segment, `**` = any depth). (P2; built with /cd, DECISIONS
  77: deny rules are checked against the folder as typed, by way of each
  link and as resolved; allow rules against it as resolved; a Cd ask rule
  is a mistake, as there is nobody else to ask.)

## 8. Where rules live, and which win

Settings keys (all under `permissions`; see `settings.md` for the files):

| Key | Type | Meaning |
| :- | :- | :- |
| `allow` | string[] | Run without asking. |
| `ask` | string[] | Always prompt (deny in `dontAsk`). |
| `deny` | string[] | Always refuse. Matching files are also left out of file discovery and search results. |
| `defaultMode` | string | Starting mode (2.1). |
| `additionalDirectories` | string[] | Extra working directories (file access only). |
| `disableBypassPermissionsMode` | `"disable"` | Forbid `bypassPermissions`. |
| `disableAutoMode` | `"disable"` | Forbid `auto`. |
| `denyInsteadOfPrompting` | boolean | Deny what would prompt, and keep going. User/managed settings and `--settings` only; a repository cannot set it. Also removes `AskUserQuestion`, `EnterPlanMode`, `ExitPlanMode`. v2.1.319+. |
| `blockReadsOutsideWorkingDirectories` | boolean | File tools refuse reads outside the working directories in every mode. (P2) |
| `blockWritesOutsideWorkingDirectories` | boolean | `Write`/`Edit`/`NotebookEdit` refuse writes outside them in every mode. (P2) |

Top-level: `skipDangerousModePermissionPrompt` (boolean).

Per-session: `--allowedTools` adds allow rules, `--disallowedTools` adds deny
rules, `--add-dir` and `/add-dir` add directories, `/permissions` edits
rules live (a change applies from the model's next tool call).

**Merging**: rule arrays from all sources are combined. Because deny is
checked first, a deny from *any* source wins over an allow from any other —
user over project, project over user, managed over `--allowedTools`.

**"Don't ask again"** from a prompt:

| What was approved | Lasts | Saved where |
| :- | :- | :- |
| A Bash command | Permanently, per repository and command | `.claude/settings.local.json` at the git repository root (the main checkout for a worktree) |
| A file edit | Until the session ends | Not saved to a file |
| A WebFetch domain | Permanently, per repository and domain | Same file, as `WebFetch(domain:…)` |
| WebSearch | Permanently, per repository | Same file |

A prompt offers the remembering options only when it can show everything the
saved rule would allow; otherwise only a one-time yes is offered.

## 9. Working directories and protected paths

**Working directories** = the directory the session started in (the primary
working directory) plus additional directories from `--add-dir`, `/add-dir`
and `permissions.additionalDirectories`. Inside them reads need no prompt
and edits follow the mode. Additional directories give file access, not
configuration: settings, hooks and memory files are not loaded from them
(skills, commands and sub-agents are, for directories added by flag or
`/add-dir`; memory files only with
`CLAUDE_CODE_ADDITIONAL_DIRECTORIES_CLAUDE_MD=1`).

**Protected paths** hold repository state and the harness's own
configuration. Writes to them:

| Mode | Outcome |
| :- | :- |
| `default`, `acceptEdits` | Prompt |
| `plan` | Prompt (allowed in an interactive session with bypass available) |
| `dontAsk` | Denied |
| `bypassPermissions` | Allowed |

Allow rules in settings do **not** pre-approve them — the check runs before
allow rules, so `Edit(.claude/**)` changes nothing. The prompt may offer a
session-long grant for the project's `.claude/` folder or for `~/.claude/`.

Protected directories: `.git`, `.config/git`, `.vscode`, `.idea`, `.husky`,
`.cargo`, `.devcontainer`, `.yarn`, `.mvn`, `.claude` (except its
`worktrees/`, the session's own plan files, and Markdown files in the
auto-memory and agent-memory directories).

Protected files: `.gitconfig`, `.gitmodules`; `.bashrc`, `.bash_profile`,
`.bash_login`, `.bash_aliases`, `.bash_logout`, `.zshrc`, `.zprofile`,
`.zshenv`, `.zlogin`, `.zlogout`, `.profile`, `.envrc`; `.npmrc`, `.yarnrc`,
`.yarnrc.yml`, `.pnp.cjs`, `.pnp.loader.mjs`, `.pnpmfile.cjs`,
`bunfig.toml`, `.bunfig.toml`; `.bazelrc`, `.bazelversion`, `.bazeliskrc`;
`.pre-commit-config.yaml`, `lefthook.yml`, `lefthook.yaml`,
`.lefthook.yml`, `.lefthook.yaml`; `gradle-wrapper.properties`,
`maven-wrapper.properties`; `.devcontainer.json`; `.ripgreprc`,
`pyrightconfig.json`; `.mcp.json`, `.claude.json`; any file passed to
`--settings`, `--mcp-config` or `--agents`; and the real file behind a
settings file that is a symlink.

gnosh adds its own: `gnoshrc` (wherever it lives), `.gnosh/`, and the `mem`
store.

## 10. Critical-path removals

`rm` and `rmdir` commands whose target is recognised as critical are checked
before they run. No allow rule and no hook `allow` approves one.

| Mode | Outcome |
| :- | :- |
| `default`, `acceptEdits`, `plan` | Prompt |
| `dontAsk` | Denied |
| `bypassPermissions` | In the terminal: a prompt with a two-minute countdown; if it runs out the command is denied and the model is told what to do instead (any key stops the countdown; after three unanswered prompts in a session further ones are denied at once until the user sends a message). In `-p`: denied unless a `PermissionRequest` hook allows it. `CLAUDE_CODE_DISABLE_DANGEROUS_RM_TIMEOUT=1` removes the time limit; `CLAUDE_CODE_EVEN_MORE_DANGEROUSLY_SKIP_PERMISSIONS=1` (launch environment only, v2.1.311+) removes the prompt. |

Critical targets:

- the filesystem root and every direct child of it (`/usr`, `/etc`, …);
- the home directory;
- the working directory and its parents;
- an additional working directory or its parents, when the removal is a glob
  under it (`rm -rf <dir>/*`);
- a glob or trailing slash directly under a shell variable
  (`rm -rf "$DIR"/*`), because an empty variable makes it the root — unless
  every expansion is guarded as `"${DIR:?}"`;
- a variable followed by one common top-level name (`"$TMPDIR/mnt"`);
- a variable assigned in the same command from a directory-printing
  substitution (`D=$(pwd); rm -rf "$D"`);
- a recursive `rm` whose target is only a command substitution
  (`rm -rf "$(pwd)"`);
- targets ending in `/*` or `/*/` that cannot be resolved beforehand.

The check also looks inside subshells, brace groups, command and process
substitutions, and scripts passed to `sh -c` / `bash -c`. When the shell
might read the command differently from the parser, the reason shown is
`Dangerous rm operation not ruled out — the shell may read this command differently than it was parsed`.
The docs say plainly that the check can miss commands; it is a seat belt,
not a wall.

## 11. Workspace trust

- An interactive session started in a folder not yet trusted shows a trust
  dialog. `-p` runs never show it.
- Trust is keyed on the **git repository root** (the main checkout for a
  worktree; a nested repository such as a submodule needs its own), or
  outside a repository on the start directory, covering its subdirectories
  except nested repositories.
- Started in the **home directory**, trust is held for that session only and
  never written.
- Claude Code stores it as `projects["<path>"].hasTrustDialogAccepted: true`
  in `~/.claude.json`; gnosh stores it in its own state file.
- A background session checks trust before starting; where no dialog can
  appear it exits with a `Workspace not trusted` error.

What trust gates. Three situations differ:

- **A — interactive, dialog not yet accepted.** Hooks from *every* settings
  file, the user's own included, are held back until the dialog is accepted
  (hooks page, "Workspace trust"). Project allow rules and additional
  directories are held and listed in the dialog.
- **B — interactive, only a parent folder was trusted** (the folder is
  covered by a parent's trust, not trusted itself).
- **C — `-p` or an embedding program**: no dialog ever; the folder counts as
  trusted for hooks.

| Supplied by the repository | B: only a parent trusted | C: `-p`, never trusted |
| :- | :- | :- |
| `permissions.allow` and `additionalDirectories` in `.claude/settings.json` | Not used until the dialog is accepted (it appears again, listing them) | Not used; stderr warning `this workspace has not been trusted` |
| `deny` and `ask` rules | Applied (they only restrict) | Applied |
| Hooks in settings files, the `env` block, helper commands, a project skill's hooks and `allowed-tools` | **Used** | **Used** |
| Hooks in a project sub-agent's frontmatter; inline `mcpServers` in a sub-agent | Not used, no dialog offered | Not used |
| `.mcp.json` servers | Asked about before connecting; the repository's own approvals do not count | Connected without asking |

To trust one exact folder by hand in Claude Code: set
`projects["<path>"].hasTrustDialogAccepted` to `true` in `~/.claude.json`.

`.claude/settings.local.json` is treated as the user's own file — unless it
is tracked in git or `.claude` is a symlink, in which case it is treated as
repository-supplied.

The docs' own advice before running `-p` in a repository one did not write:
`--setting-sources user`, or `--bare`, or
`--settings '{"disableAllHooks": true}'`.

**Decision for Jae.** In `-p` (situation C), and when only a parent folder
was trusted (B), Claude Code runs a repository's hooks and applies its `env`
block without the folder itself ever being trusted. That is a real hole for
a tool that will be pointed at other people's repositories. gnosh can match
it exactly (maximum compatibility) or be stricter: in `-p`, run
project-supplied hooks and apply project `env` only if the folder is already
trusted or an explicit trust flag is passed, and say on stderr when they were
skipped. Recommendation: stricter. It costs nothing in Jae's own,
already-trusted repositories.

## 12. The permission prompt

Behaviour to match (wording and look are gnosh's own):

- It shows what is about to happen: for Bash the model's `description`, the
  command, and why it needs approval; for an edit, the diff.
- Choices for a shell command: **yes** (this once); **yes, and don't ask
  again for `<rule>`** (saves the rule; the rule text is shown, e.g.
  `npm test *`); **no**.
- Choices for a file edit: yes; yes for the rest of the session; no. The
  user can change the proposed edit before accepting (the result records
  `userModified`).
- Choices for WebFetch: yes (this fetch only); yes and don't ask again for
  this domain; no.
- **Esc** cancels. **Tab** on *yes* or *no* opens a comment field: on yes
  the comment is sent to the model after the result; on no it is sent as the
  reason for the denial and the model carries on.
- **No without a comment, from the main conversation, stops the turn.**
- A background sub-agent's prompt appears in the main session and names the
  sub-agent; Esc denies that one call without stopping the sub-agent.
- Prompts never time out on their own. Two exceptions: `AskUserQuestion`
  can, by setting; and the critical-path removal prompt in
  `bypassPermissions` has a two-minute countdown (section 10).
- `/permissions` lists every rule with the file it came from and lets the
  user add or remove rules while the model works.

## 13. When nobody can answer

- `-p` run in `default` mode: a call needing approval is **denied** unless an
  allow rule, a `PreToolUse` or `PermissionRequest` hook, or a prompt
  handler approves it. The model receives the rejection as the tool result
  and usually tries another way. Denials are listed in
  `permission_denials` on the result and, in stream-json, as
  `system/permission_denied` lines.
- `--permission-prompts none` (or `permissions.denyInsteadOfPrompting`):
  same, plus the model is told nobody can approve and not to retry, and
  tools that need a person are removed. The denial text the model reads
  begins
  `Permission for this tool use was denied. It requires approval, and this session has no approval surface`.
- `dontAsk` mode: as above, and ask rules deny instead of prompting.
- `PermissionRequest` hooks still run where no prompt can be shown; with no
  decision from them the call is denied.

---

## What gnosh adopts

**P0**

- [ ] Decision order of section 1.
- [ ] Modes `default`, `acceptEdits`, `plan`, `dontAsk`,
      `bypassPermissions`; `manual` alias; `auto` accepted and mapped to
      `default` with a notice.
- [ ] Start-mode resolution (2.1), including "no `bypassPermissions` from
      project or local settings".
- [ ] Rule syntax: bare, specifier, tool-name globs for deny/ask, MCP forms;
      bare-name deny removes the tool from the model.
- [ ] Bash matching: wildcards incl. `:*`, compound splitting, wrapper and
      safe-assignment stripping, exec-wrapper/`find` exclusions,
      redirection and `tee` target checks, the read-only set and its
      exceptions.
- [ ] Read/Edit path rules: four anchors, depth rules, `Edit` covers all
      edit tools, `Read` deny blocks edits, symlink rule.
- [ ] `permissions.allow/ask/deny/defaultMode/additionalDirectories`,
      `--allowedTools`, `--disallowedTools`, `--add-dir`.
- [ ] `acceptEdits` scope exactly as 2.2.
- [ ] Protected paths and critical-path removals.
- [ ] `-p` denial behaviour and `permission_denials`.
- [ ] Root/sudo refusal for `bypassPermissions`.

**P1**

- [ ] The interactive prompt (section 12) with "don't ask again" saving to
      `.claude/settings.local.json`.
- [ ] Shift+Tab cycle, `--allow-dangerously-skip-permissions`, the bypass
      warning dialog.
- [ ] Plan approval flow; `/permissions`; `/add-dir`.
- [ ] Workspace trust dialog and store.
- [ ] `WebFetch(domain:…)`, `Skill(…)`, `Agent(…)` rules;
      `Tool(param:value)` rules; `!` negation.
- [ ] `--permission-prompts none`, `denyInsteadOfPrompting`,
      `disableBypassPermissionsMode`.

**P2**

- [ ] `Cd` rules, `blockReadsOutsideWorkingDirectories`,
      `blockWritesOutsideWorkingDirectories`, startup warnings for suspect
      rules, mounted-drive prompt, network-path checks.

## Deliberately not adopted

- `auto` mode and everything about its classifier (`autoMode`,
  `useAutoModeDuringPlan`, "Yes, and switch to auto mode",
  `skipAutoPermissionPrompt`).
- Managed (organisation) settings and `allowManagedPermissionRulesOnly`.
- OS sandboxing and its interaction with rules (a possible later project of
  its own).
- Mods (`tool.check`), connector-tool organisation policies, PowerShell
  rules, Windows path forms.

## Unverified (docs silent)

- A single authoritative flowchart of the decision order (section 1 is a
  reconstruction).
- The full read-only command set and the full "known-safe" environment
  variable list.
- The exact labels of the file-edit prompt's choices.
- Whether a hook's `allow` passes protected-path writes outside plan mode
  (gnosh: no — the safety check comes first).
- What exactly declining the trust dialog does (gnosh: exit).
