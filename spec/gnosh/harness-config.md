# harness-config.md — gnoshrc, settings, environment variables, doctor

*Since 14 November 2026 gnosh is configured by one JSON file, `gnosh.json`, and no longer by `gnoshrc` (`spec/DECISIONS.md`, items 17 and 23 to 28). Where this file speaks of `gnoshrc`, of its directives or of its dashed option names, it records what the build was first given.*

What this file is: how gnosh is configured. It covers the `gnoshrc` line format
and every directive and option the earlier gnosh had by the end, where config
and state live and which wins, the environment variables, the old names that
still have to be read, the rule that a config mistake stops startup, and what
`gnosh doctor` checks.

In one paragraph: the user's settings are a short text file,
`~/.config/gnosh/gnoshrc`, one setting per line, written as commands
(`provider add local --type llamacpp --base-url ...`, `option request-timeout
900`). A project may carry its own, which counts only once the folder is
trusted. gnosh also keeps a small state file of choices made inside the program
(the model picked, how hard it thinks). Anything in any of these that gnosh
does not understand stops it at startup with a message that names the line.

Read first: `harness-00-principles.md` (PRIN-5 no compat bloat, PRIN-6 config
mistakes stop startup). Claude Code's own settings files are in `spec/cc/`.

---

## 1. What Jae has today (the files that must keep working)

These are the only existing files that matter (PRIN-5). The Mac's were read on
2026-11-13; the AI box's are from the session notes and were not re-read
(unconfirmed in detail).

**On the Mac**

`~/.config/crush/crushrc`, in substance (the LAN address here is a stand-in):

```sh
# two shell variables, then a probe: if the box does not answer on the home
# LAN but a local ssh forward does, use the forward
box=http://192.0.2.91 qwen_port=8080 gptoss_port=8081
if ! nc -z -G 1 192.0.2.91 8080 2>/dev/null && nc -z -G 1 127.0.0.1 18080 2>/dev/null; then
  box=http://127.0.0.1 qwen_port=18080 gptoss_port=18081
fi
provider add qwen --name "Qwen3.8-27B (ai box)" --type llamacpp --base-url "$box:$qwen_port"
provider add gptoss --name "gpt-oss-120b (ai box)" --type llamacpp --base-url "$box:$gptoss_port"
option notifications disabled
option request-timeout 900
```

His own comment on the last line: "Re-reading a long conversation the single
slot has dropped takes minutes; the default 2-minute wait cancelled it."

`~/.local/share/crush/crush.json` (state the program wrote): thinking off, and
the large model set to `qwen3.8-27b` of provider `qwen`.

**On the AI box, in the account gnosh runs as** (from notes)

- `~/.config/crush/crushrc`: the same two providers at `127.0.0.1:8080`
  (both models are behind one router port since 2026-10-25), a `model add`
  line per model giving its context window (262144 for Qwen, 131072 for
  gpt-oss), `option request-timeout 900`, two `host add` lines, and since
  2026-10-30 `option mem-embeddings-url ...`.
- `~/.config/crush/CRUSH.md`: about 5 KB of standing notes about the box,
  read into every session as a global context file.
- `~/.local/share/crush/`: the state file (model choice, thinking level per
  model), the trusted-folders record, the analytics answer, the list of
  projects opened, and the per-session environment files written by
  SessionStart hooks.
- A wrapper script that exports `GNOSH_DISABLE_METRICS=1`,
  `GNOSH_DISABLE_PROVIDER_AUTO_UPDATE=1`, `GNOSH_DISABLE_DEFAULT_PROVIDERS=1`,
  `GNOSH_DISABLE_UPDATE_CHECK=1` and `GNOSH_PROFILE=1`, then runs the binary.
- In the project he works in: `klash.json` with one Stop hook, an
  `AGENTS.md`, and a `.crush/` data folder with his sessions.
- Scratch projects written by test and review scripts: a small JSON config
  naming one provider, sometimes with a request-body extra that pins a
  llama.cpp slot (`"extra_body": {"id_slot": 1}`).

The page at gnosh.ai tells a new user to write exactly this:

```sh
provider add local --type llamacpp --base-url "http://127.0.0.1:8080"
# a local model can take minutes to read a long prompt
option request-timeout 900
```

**CFG-1 (P0).** The three lines above, and the AI box's file as described,
load and mean the same thing in the new gnosh.

---

## 2. Where things live, and which wins

### CFG-2 Locations (P0)

| What | Where | Notes |
| :- | :- | :- |
| The user's config | `$XDG_CONFIG_HOME/gnosh/gnoshrc`, default `~/.config/gnosh/gnoshrc` | `GNOSH_GLOBAL_CONFIG` names another directory. |
| A project's config | `gnoshrc` or `.gnoshrc` in the working directory | Counts only in a trusted folder (SAFE-1). The dotted name wins over the plain one. |
| The user's global context files | `~/.config/gnosh/GNOSH.md` and `~/.config/AGENTS.md` | See CFG-20. |
| The user's state | `$XDG_DATA_HOME/gnosh/`, default `~/.local/share/gnosh/` | Written by gnosh, never by hand; `GNOSH_GLOBAL_DATA` names another directory. gnosh never runs a config file found here. |
| A project's data | `.gnosh/` in the project | Sessions, logs, `mem`, the artifact register. The closest `.gnosh/` on the way up to the project root is used, so a subfolder shares its project's. |

The state directory holds, each in its own file: the settings chosen inside
the program (model, thinking level per model, the auto-compact window), the
trusted-folders record (SAFE-6), the answer to the analytics question
(SAFE-32), the list of projects opened, and one environment file per session
for SessionStart hooks (HOOK-23).

### CFG-3 The project data folder is never deleted (P0)

A project generator that wanted an empty folder once had the model delete the
data folder while gnosh ran; the session went on writing to files nobody could
reach and was gone at exit. Three requirements came of it:

1. The model is told that `.gnosh/` is gnosh's own, is never deleted or
   moved, and that a tool wanting an empty folder is run in a new subfolder.
2. gnosh notices when its open data folder has been removed, puts it back
   with what it holds in memory, and says so once (P1).
3. A new `.gnosh/` gets a `.gitignore` of its own so it is never committed.

### CFG-4 Precedence (P0)

From weakest to strongest:

1. gnosh's built-in defaults.
2. The user's `gnoshrc`.
3. The user's state file (what he chose inside gnosh: `/model`, `/think`,
   `/autocompact`).
4. The project's `gnoshrc`, in a trusted folder.
5. Environment variables (section 6).
6. Command-line flags, which hold for the one run and are never saved
   (`-m`, `--permission-mode`, `--add-dir`, the tool flags).

Within one file, later lines win. `doctor` and `/status` say which of these
each effective value came from.

Whether 3 outranks 2 in the earlier gnosh is unconfirmed; the order above is
what its behaviour implied (a scratch config that copied only the rc file lost
the model choice, which lived in the state file). OQ-4.

A choice made inside gnosh is written to the **user's** state, never the
project's, and a command-line override is never written anywhere. A session
resumed after `-m` does not switch back to the model it last used.

### CFG-5 Old names: only Jae's own files (P0)

Jae, 2026-10-23: "just remove this kind of stuff. nobody has downloaded except
me. this is bloatware." The standing rule from that day: support for an old
name exists only because **his own files still use it**; drop it once his
files have moved, and ask first.

The earlier gnosh read every name from three renames (`gnosh`, `klash`,
`crush`; newest wins). The new gnosh reads `gnosh` names, plus exactly this
fallback list:

| Old name still read | Because |
| :- | :- |
| `~/.config/crush/crushrc` when no `~/.config/gnosh/gnoshrc` exists | His config on both machines. |
| `~/.config/crush/CRUSH.md` when no `~/.config/gnosh/GNOSH.md` exists | The box's standing notes. |
| A project's `klash.json`, for its `hooks` block only (P1) | The Stop hook in his working project. |

Not read: `klashrc`, `crashrc`, `crush.json` as config, `.klash/`, `.crash/`,
`KLASH_*`, `CRUSH_*`, `CRASH_*`, old tool names, old skill addresses, and the
old sessions database. The old state file is not imported: he picks the model
and the thinking level once (OQ-6). No warning is printed about any old file
(PRIN-5).

The recommended end state is a one-time move of those three files, done with
him, after which this table is deleted (OQ-1).

---

## 3. The gnoshrc line format

*13 November 2026: this line format was replaced by one JSON file, `gnosh.json` (`spec/DECISIONS.md`, item 17); this section records what was asked before that ruling.*

### CFG-10 Grammar (P0)

```
file       = { line }
line       = [ directive ] [ "#" comment ] newline
directive  = word { word }
word       = bare | 'single-quoted' | "double-quoted"
```

- One directive per line. A backslash at the end of a line joins the next
  line to it.
- `#` starts a comment when it begins a word. Blank lines are skipped.
- Single quotes keep everything literally. Double quotes keep spaces and
  allow `\"` and `\\`.
- In a bare or double-quoted word, `$NAME` and `${NAME}` are replaced by the
  environment variable, or by nothing when it is unset. `${NAME:?message}`
  stops startup with the message when it is unset.
- A leading `~` or `~/` in a value that is a path is the home directory.
- The first word is the directive: `provider`, `model`, `mcp`, `lsp`, `hook`,
  `host`, `permissions`, `option`.
- Things named by an ID (`provider`, `model`, `mcp`, `lsp`, `host`, a named
  `hook`) take `add` to create **or update** and `remove` (or `rm`) to delete.
  `remove` acts on whatever earlier lines set.
- A flag that takes a truth value accepts `true`, `false`, `1`, `0`, `yes`,
  `no`, in any letter case. For `option <key>` with a truth value, the value
  may be left out and means true.
- A flag marked *repeatable* may be given more than once. A flag marked
  *key value* takes two words.

### CFG-11 It is a list of settings, not a script (P0; see OQ-1)

In the earlier gnosh this file was a shell script run by a shell built into
the program, so it could hold variables, `if`, `source`, pipes and
`$(command)`. The new gnosh has no shell of its own (ARCHITECTURE.md decision
1), and a config file that is code is the reason a cloned repository could
run a stranger's program (SAFE-1).

So: any shell construct (`if`, `then`, `fi`, `for`, `case`, `;`, `&&`, `|`,
a redirection, `$(...)`, backticks, an assignment line, `source`) is a startup
error that says, in words, that gnoshrc is a list of settings and names the
replacement:

| The shell did | gnoshrc does |
| :- | :- |
| Pick between two addresses by probing them | `--base-url` twice: the first that answers is used (CFG-12) |
| `source other-file` | `include other-file` (P2) |
| `$(password-manager read key)` for a secret | `--api-key-command "password-manager read key"`, run without a shell when the key is first needed (P2) |
| `if [[ $HOSTNAME == x ]]` | A separate file per machine, included by name (P2) |

Jae's Mac file uses the first of these, so it needs a three-line edit, which
is his to approve (OQ-1). The AI box's file is plain directives and loads as
it is.

### CFG-12 `provider` (P0)

`provider add <id> [flags]` adds a model provider or updates the one with
that ID. `provider remove <id>` removes it and the models registered on it.

| Flag | Value | Meaning | Pri |
| :- | :- | :- | :- |
| `--type` | `llamacpp`, `openai-compat`, `openai`, `anthropic`, `ollama`, `lmstudio`, `litellm`, `omlx` | Which wire format and which extras (thinking switch, model discovery). Hosted aggregators the earlier gnosh also knew (`openrouter`, `vercel`, `google`, `google-vertex`, `azure`, `bedrock`) are P2. An unknown type is a startup error listing the known ones. | P0 `llamacpp`, `openai-compat`; P1 `anthropic`, `openai`, `ollama`, `lmstudio`; P2 the rest |
| `--base-url` | URL, repeatable | Where the provider's API is. Given more than once, the first that accepts a connection at startup is used and `doctor` says which. A URL ending in `/v1` and one without are both accepted for the local types (two bugs came from doubling `/v1`). | P0 once; P1 repeatable |
| `--name` | text | The name shown to the user. | P0 |
| `--api-key` | text | The key. Empty for a local server. | P0 |
| `--discover-models` | truth | Ask the server which models it has. Default: true when no model is listed for the provider. A listed model keeps every field the user set; discovery fills the rest. See LM-20. | P0 |
| `--extra-body` | JSON object | Merged into every request body. Used to pin a llama.cpp slot (`{"id_slot": 1}`). | P1 |
| `--extra-header` | key value, repeatable | An HTTP header. A header whose value comes out empty is not sent. | P1 |
| `--disable` | truth | Keep the entry but do not use it. | P1 |
| `--system-prompt-prefix` | text | Put before the system prompt for this provider. | P2 |
| `--provider-options` | JSON object | Provider-specific options. | P2 |
| `--flat-rate` | truth | Do not count cost for this provider. | P2 |

A provider that cannot be used (its server does not answer, its key is unset)
is **left out with its reason kept**, and gnosh still starts. Every place
that lists providers says which were left out and why (PRIN-7). Only an
unknown word is a startup error.

### CFG-13 `model` (P0)

A model is named `<provider>/<id>`, the form `gnosh models` prints.

`model add <provider>/<id> [flags]` registers a model by hand on an existing
provider. `model remove <provider>/<id>` removes it.

| Flag | Value | Meaning | Pri |
| :- | :- | :- | :- |
| `--context-window` | tokens | The model's window. When left out, it is asked of the server (LM-20); when that fails it is taken as 32,768 so the session can never outgrow an unknown window (LOOP-51). | P0 |
| `--name` | text | Display name. | P0 |
| `--default-max-tokens` | tokens | The reply limit sent with a request. Unset: none is sent to a local server. | P1 |
| `--reasoning-levels` | list | The levels this model's thinking has, weakest first (`off on` for a Qwen; `low medium high` for gpt-oss). When left out the model's ID decides. See LOOP-41. | P1 |
| `--can-reason`, `--supports-images` | truth | What the model can do. A model that cannot see images is told what an image is instead of being sent it (TOOL-12). | P1 |
| `--reasoning-effort` | level | The level to start at. | P2 |
| `--price-input`, `--price-output`, `--price-cache-create`, `--price-cache-hit` | per million tokens | For the cost line. | P2 |

`model large [<provider>/<id>] [flags]` and `model small [...]` choose the two
slots. The **large** model does the work. The **small** model does the short
side jobs: naming a session, naming todos, checking a goal or a close. With
no small model set, the large one does them. Any slot name other than `large`
and `small` is a startup error.

Flags on `model large` / `model small`: `--think`, `--reasoning-effort`,
`--max-tokens`, `--temperature`, `--top-p`, `--top-k`, `--frequency-penalty`,
`--presence-penalty`, `--provider-options` (P1 the first two and
`--temperature`; P2 the rest).

On the AI box both slots must name the same Qwen (LM-30). With nothing
chosen and several models discovered, the earlier gnosh took the first one
discovered, which was the wrong one and cost Jae his loaded model twice. New
rule: **with more than one model available and none chosen, gnosh does not
guess.** Interactively it asks; non-interactively it stops and says to pass
`-m` (LM-31).

### CFG-14 `host` (P1)

`host add <name> --ssh <destination> [--description "..."]` declares another
machine the model may run commands on (TOOL-20). `host remove <name>`.

- `<destination>` is `user@address` or a `Host` alias from `~/.ssh/config`,
  so keys and jump hosts are ssh's business.
- Refused at load: the name `local`; an empty destination; a destination that
  starts with `-` (it would be read as an ssh option); one holding shell
  syntax.

### CFG-15 `hook` (P1)

`hook add <event> --command <command> [--name <name>] [--matcher <matcher>]
[--timeout <seconds>]` and `hook remove <event> [--name <name>]` (without a
name, every hook of that event). Events, matchers and timeouts: `harness-hooks.md`.
An unknown event, or a matcher on an event that takes none, is refused on the
line that is wrong.

### CFG-16 `permissions` (P1)

- `permissions allow <rule>...` lets tools or commands run without asking.
- `permissions deny <rule>...` takes a tool away from the model entirely, or
  stops the commands a `Bash(pattern)` rule matches.
- A rule is a tool name or CC's rule syntax (`spec/cc/`), for example
  `Read`, `Bash(git diff *)`, `'Bash(git push *)'`. Quote a rule that holds
  spaces or parentheses.
- A name gnosh does not know is refused on its line with the known names.
  Names of tools from MCP servers are accepted in `allow` (they are only
  discovered when the server connects) and refused in `deny`, where they
  would do nothing.

How rules match: TOOL-30 to TOOL-36.

### CFG-17 `mcp` and `lsp` (P2)

`mcp add <name>` with `--type stdio|sse|http` (default `stdio`), `--command`,
`--args` (repeatable), `--env` (key value, repeatable), `--url`, `--header`
(key value, repeatable), `--timeout`, `--disabled`, `--enabled-tools`,
`--disabled-tools`, and the OAuth flags; `mcp remove <name>`. A type other
than the three is refused; a missing type means `stdio`. A disabled server is
not checked until it is enabled.

`lsp add <name> --command <command>` with `--args`, `--env`, `--filetypes`,
`--root-markers`, `--timeout`, `--disabled`, `--init-options`, `--options`;
`lsp remove <name>`. gnosh knows the Gno language server (`gnopls`: file type
`gno`, root markers `gnomod.toml`, `gnowork.toml`, `gno.mod`) the way it knows
Go's.

Neither is used in Jae's own files today (unconfirmed).

### CFG-18 `option` (P0)

`option <key> [value]`. For a list option each line adds one value, and
`option reset <key>` clears what earlier lines added.

**Options Jae uses**

| Key | Value | Default | Meaning | Pri |
| :- | :- | :- | :- | :- |
| `request-timeout` | seconds | 60 | How long a model request may go **without sending anything** before it is given up. It is an idle limit, reset by every piece of the reply, not a limit on the whole request. Before the first piece arrives the limit is five times this (LM-2). `0` waits for ever. | P0 |
| `notifications` | `auto`, `native`, `osc`, `bell`, `disabled` | `auto` | How gnosh tells the user it needs him or has finished when the terminal is not in front. | P0 accepted; P1 built |
| `mem-embeddings-url` | URL | unset | The other spec set. Unset, nothing is sent anywhere (PRIN-2). | P1 |

**Options about the turn**

| Key | Value | Default | Meaning | Pri |
| :- | :- | :- | :- | :- |
| `auto-summarize` | truth | true | The compaction backstop (LOOP-60). | P1 |
| `auto-compact-window` | tokens, 100000 to 1000000 | unset (automatic) | How full the context may get before the backstop runs. Never past the model's own window. Also set by `/autocompact`. | P1 |
| `idle-compaction` | truth | true | Summarize a long conversation left idle 54 minutes (LOOP-64). | P2 |
| `loop-max-turns` | number | unset (no cap) | An optional cap on `/loop` for someone who wants one. Unset, zero or negative means none (PRIN-4). | P1 |
| `max-background-jobs` | number | 50 | Background commands that may run at once; finished ones do not count. | P1 |
| `mem-review-closes` | truth | true | The other spec set. | P1 |

**Options about what the model reads**

| Key | Value | Default | Meaning | Pri |
| :- | :- | :- | :- | :- |
| `global-context-path` | path, list | the two files in CFG-2 | More global context: a file, or a folder whose Markdown files are all read. | P1 |
| `context-path` | path, list | see CFG-20 | More project context files. | P1 |
| `skill-path` | directory, list | none | More places to find skills. | P2 |
| `disable-skill` | name, list | none | Hide a skill from the model. | P2 |
| `initialize-as` | file name | `AGENTS.md` | The file `/init` writes. | P2 |

**Options about the network** (all off unless set; SAFE-30)

| Key | Value | Default | Meaning | Pri |
| :- | :- | :- | :- | :- |
| `metrics` | truth | unset = off | Send anonymous usage events. | P2 |
| `metrics-url`, `metrics-key` | URL, text | gnosh's own collector | Where they go. | P2 |
| `update-repo` | `owner/repo` | unset = no check | Which repository's releases the update check reads. | P2 |
| `artifact-server` | address, or `off` | `127.0.0.1:7777` | Where recorded deliverables are served (TOOL-52). | P2 |

**Options about gnosh itself**

| Key | Value | Default | Meaning | Pri |
| :- | :- | :- | :- | :- |
| `debug` | truth | false | Write more to the log. | P1 |
| `data-directory` | path | `.gnosh` | The project data folder's name. | P2 |
| `attribution-trailer-style`, `attribution-generated-with` | see OQ-12 | off | Lines added to commits the agent makes. | P2 |

**Options of the earlier gnosh that are not carried over**, each a startup
error that says it is gone and why: `provider-auto-update`,
`default-providers` (no vendor catalog; OQ-3), `auto-lsp`, `debug-lsp`,
`progress`, and all of `option ui ...` (`compact`, `diff`, `transparent`,
`mouse`, `scrollbar`, `exit-banner`, `completions-max-depth`,
`completions-max-items`), which set up the full-screen interface.

Tool limits the earlier gnosh took only in JSON (`tools.ls.max_depth`,
`tools.ls.max_items`, `tools.grep.timeout`, `tools.glob.timeout`) are covered
by CC's environment variables (section 6) or by fixed limits in
`harness-tools.md`.

### CFG-19 JSON settings (P1; see OQ-8)

The earlier gnosh also read a JSON file with the same content (`gnosh.json`),
and that is where pasted CC settings went: a `hooks` block in either gnosh's
flat shape or CC's grouped shape, `permissions.allow` and `permissions.deny`
with CC's rule syntax, `env`, and `hosts`.

New rule: gnosh reads **CC-shaped settings** (`hooks`, `permissions`, `env`;
`spec/cc/`) from `gnosh.json` beside a `gnoshrc`, so that a block copied from
`~/.claude/settings.json` works. Everything gnosh-specific is set in
`gnoshrc`. An unknown key in the JSON is a startup error. (In the earlier
gnosh a misspelt JSON key was the one config mistake that still went
unnoticed.)

---

## 4. Context files (what the model reads about the project)

### CFG-20 Which files, in what order (P0)

As CC does (`spec/cc/`), with gnosh's own file added:

- In the working directory **and every directory above it**, outermost first
  and closest last: `AGENTS.md`, `GNOSH.md`, `CLAUDE.md`, each also in the
  directory's `.gnosh/` and `.claude/` (`.claude/CLAUDE.md`), then the rules
  in `.gnosh/rules/` and `.claude/rules/` (a rule with `paths:` waits for a
  file it names, as Claude Code's does), then `CLAUDE.local.md`
  (DECISIONS 245).
- The user's global files: `~/.config/gnosh/GNOSH.md`, the rules in
  `~/.config/gnosh/rules/`, and `~/.config/AGENTS.md`, and anything added with
  `option global-context-path`. (`~/.claude/CLAUDE.md` is not read.)
- At the start, files that come to more than 40,000 characters are said to be
  long (DECISIONS 261), as Claude Code warns.
- A subdirectory's own files, **the first time the agent reads, writes or
  edits a file there** (P1). They arrive with that tool's result. One longer
  than 40,000 characters comes cut, with a note to read the rest.
- Each file is given once however many ways it is reached.

Measured on 2026-11-06: a rule written in a repository's `AGENTS.md` reaches a
project inside that repository and never a new project elsewhere. Only the
global files and what gnosh itself says reach every project. That is why the
Gno audit rule lives in gnosh's own instructions (TOOL-60).

### CFG-21 Imports (P1)

A line `@path` in a context file brings that file in, as in CC, up to four
levels deep.

### CFG-22 Limits that keep context files safe (P1)

- A project's files import only files inside the project. A stranger's
  repository cannot put `@~/.ssh/id_ed25519` in the prompt.
- A directory above the project gives only files owned by the user or by
  root, so a shared directory cannot inject instructions.
- HTML comments are removed before the model reads a file.
- A file over 4 MiB is skipped, and the user is told.
- Context files are **text for the model and never run anything**, so they
  are read in an untrusted folder too (SAFE-3).

### CFG-23 After a summary, after a rewind (P1)

Context files given on demand are given again when the conversation that
held them is cut away (a rewind, a summary), so the agent never works in a
directory without its instructions.

---

## 5. A config mistake stops startup

### CFG-40 The rule, and every case found (P0)

PRIN-6. Each row below was a real bug in the earlier gnosh: the setting was
accepted, looked configured, and did nothing. Each is now an error at
startup that names the file, the line, the bad word and the words gnosh
knows.

| # | The mistake | Used to happen | Must happen |
| :- | :- | :- | :- |
| 1 | A hook event gnosh does not fire (`pre_tool_use`, `PRETOOLUSE`, `Stopp`) | Loaded; the hook never ran | Error listing the events. Names match exactly: no case folding, no underscore forms. |
| 2 | A matcher on an event with nothing to match (`Stop`, `UserPromptSubmit`, `SubagentStop`) | Loaded; matched nothing | Error. |
| 3 | A model slot other than `large` or `small` | Loaded; the default model was used | Error. |
| 4 | An MCP server type other than `stdio`, `sse`, `http` | Failed later with a message naming nothing | Error at load. A missing type is `stdio`. |
| 5 | A tool name gnosh does not have, in allow, deny or disabled lists (`websearch`, `Bash` where `bash` was meant) | The tool stayed on, or kept asking | Error listing the names. CC's names and gnosh's are both known. |
| 6 | An option key gnosh does not know | (Already an error in `gnoshrc`) | Error. |
| 7 | A settings file of the wrong shape (`"hooks": "x"`, a bare list) | The whole file was dropped without a word | Error naming the file. |
| 8 | A project settings file owned by someone other than the folder's owner (written with sudo, say) | Skipped without a word, tools it turned off still on | Error: whose file, whose folder, and the fix (`chown`), or remove it. |
| 9 | A hook answer in words gnosh does not read (`"Deny"`, `"reject"`) | Read as no opinion | The hook could not be evaluated, which blocks (HOOK-10). |
| 10 | A CC hook entry gnosh cannot run as written | Config error with a confusing message, or ignored | Error that names the part (HOOK-4). |
| 11 | A permission rule gnosh cannot apply | Refused at startup, or ignored | Error naming the rule. |
| 12 | A host with a bad name or destination | n/a | Error (CFG-14). |
| 13 | A key gnosh does not know in a JSON settings file | Ignored | Error (CFG-19). |
| 14 | A gnosh command that writes config the loader refuses (`hook add pre_tool_use`) | A gnosh that would not start, from its own command | The command refuses on the spot. |
| 15 | A tool that exists in the program but is missing from the list of names the config knows | The tool was never offered to any model, and every test passed | One list of tool names, and a test that every tool built is offered. |

Not an error, on purpose:

- **State gnosh wrote itself** (recent models, the projects list). A stale
  entry there must never block startup.
- **A provider that cannot be reached.** Left out, with the reason (CFG-12).
- **A disabled entry.** Not checked until it is enabled.
- **A bad value in a CC environment variable.** Handled as CC documents
  (usually ignored); gnosh logs it once and `doctor` shows it.

### CFG-41 One check, used everywhere (P0)

The same validation runs when config is first loaded, when it is loaded
again, and before any gnosh command writes a setting. A config that is
refused while gnosh runs leaves the running settings exactly as they were.

### CFG-42 When settings take effect (P0)

- Settings are read at startup.
- A choice made inside gnosh (`/model`, `/think`, `/autocompact`,
  `/add-dir`) applies at once.
- A file edited by hand, a new hook, or a new binary applies at the next
  start. A running session keeps what it started with; this surprised Jae
  more than once, so `/status` says when a config file is newer than the
  session's start (P2).

### CFG-43 Warnings from loading are kept (P1)

Whatever is noticed while config loads (a provider left out, both a JSON and
an rc file found in one folder, a variable that could not be resolved) is
written to the log once logging is set up, and to stderr in a
non-interactive run. The earlier gnosh threw these away because the log's
location was not yet known.

---

## 6. Environment variables

### CFG-50 gnosh's own (P0 for the first two; P2 for the rest)

| Variable | Meaning |
| :- | :- |
| `GNOSH_GLOBAL_CONFIG`, `GNOSH_GLOBAL_DATA` | Use these directories instead of the XDG ones. Tests and checks set both so they can never touch the user's real files (PRIN-15). |
| `GNOSH_SAFE_MODE` | The same as `--safe-mode` (CLI-20). CC's `CLAUDE_CODE_SAFE_MODE` is read too. |
| `GNOSH_PROFILE` | Serve a debugging page on loopback so a stuck session can be examined without killing it (LOOP-70). If the usual port is taken, a free one is used and logged. |
| `GNOSH_DISABLE_METRICS` | `1` forces analytics off; `0` turns them on; unset leaves it to the other settings. `DO_NOT_TRACK=1` beats everything. |
| `GNOSH_DISABLE_UPDATE_CHECK` | `0` turns the update check on. Unset or `1`: off. |
| `GNOSH_METRICS_URL`, `GNOSH_UPDATE_REPO` | As the options of the same names. |

`GNOSH_DISABLE_PROVIDER_AUTO_UPDATE` and `GNOSH_DISABLE_DEFAULT_PROVIDERS`,
which his wrapper sets, are accepted and do nothing, since there is no
catalog (OQ-3). They are the two exceptions to "unknown is an error", and go
when his wrapper is edited.

One rule for every on/off variable: **absent, empty and unreadable all mean
"not set".** `=0` on a "disable" switch turns the thing on; `=1` turns it off.

Variables gnosh sets for the commands and hooks it runs are in TOOL-8 and
HOOK-30.

### CFG-51 Claude Code's, honoured with CC's meaning (P1)

| Variable | Meaning in gnosh | Default |
| :- | :- | :- |
| `API_TIMEOUT_MS` | How long to wait for a response to start, for a model API that is not local. `0` = no limit. | 600000 (remote); no limit (local) (LM-1) |
| `BASH_DEFAULT_TIMEOUT_MS` | How long a command runs before it moves to the background. | 120000 |
| `BASH_MAX_TIMEOUT_MS` | The most a call's own `timeout` may ask for (never less than the default). | 600000 |
| `BASH_MAX_OUTPUT_LENGTH` | Characters of a command's output the model reads, at most 150000. | 30000 |
| `CLAUDE_CODE_MAX_OUTPUT_TOKENS` | How long one reply may run. | unset |
| `CLAUDE_CODE_FILE_READ_MAX_OUTPUT_TOKENS` | The most one read returns. | 25000 |
| `CLAUDE_CODE_GLOB_TIMEOUT_SECONDS` | How long a file search may run. | CC's |
| `CLAUDE_CODE_AUTO_COMPACT_WINDOW` | How full the context may get before the backstop, 100k to 1M, never past the model's window. Outranks `auto-compact-window`. | unset |
| `CLAUDE_AUTOCOMPACT_PCT_OVERRIDE` | A percentage of that window; earlier only, never later. | unset |
| `CLAUDE_CODE_IDLE_COMPACT_MIN_TOKENS` | The size from which idle compaction applies (at least 100k). | 200000 |
| `CLAUDE_PROJECT_DIR`, `CLAUDE_ENV_FILE` | Set **by** gnosh for hooks, so CC hook commands work unchanged (HOOK-30, HOOK-23). | |

### CFG-52 Provider keys (P1)

`ANTHROPIC_API_KEY` and `OPENAI_API_KEY` are read for the providers of those
types when the config gives no key. The earlier gnosh knew some thirty such
variables through its vendor catalog; those are not carried over (OQ-3).

---

## 7. `gnosh doctor`

### CFG-60 What it checks (P1)

As CC's `claude doctor`, for gnosh's own setup. It loads the config for the
working directory and prints one line per check, each marked fine, worth
knowing, or "a turn cannot run". It exits 1 when a turn could not run. A
config that fails to load is printed with its error.

1. **Config files:** which were read, in order, and which were held because
   the folder is not trusted (SAFE-5).
2. **Providers:** each usable provider with its address and how many models
   it has; each configured provider that was left out, with the reason; when
   several addresses were given, which answered.
3. **Models:** the large and small choices, and whether a provider offers
   each. With several models available and none chosen, that is an error
   (CFG-13).
4. **Hooks, per event:** each hook, whether the program its command starts
   with is installed, and whether a script it names exists (with
   `${CLAUDE_PROJECT_DIR}` and gnosh's own project variable filled in). A
   hook that cannot run is an **error** where it would block (PreToolUse:
   every call it matches; UserPromptSubmit: every prompt; Stop and
   SubagentStop: every turn held at its end) and a warning elsewhere. A
   missing PermissionRequest hook means every question it matches is put to
   the user, with a note.
5. **Tools:** the search program the search tools use, if any is needed, and
   each configured language server's program, looked up the way it will be
   run (with `~` and variables expanded).
6. **Trust:** whether the folder is trusted and what is being held.
7. **Environment:** any CC variable with a value gnosh could not use.

### CFG-61 It must not disturb a running model server (P0)

`gnosh doctor` and `gnosh models` only **list** models. On a server that
holds one model at a time, asking it about a model that is not loaded loads
that model and throws out the one in use. So these commands ask only for the
list of models and their state, never for a model's slots or properties
(LM-32).

### CFG-62 The same report inside a session (P1)

`/doctor` shows the same checks, and `/status`, `/hooks`, `/permissions` and
`/context` show the live values (UI-20).

---

## 8. Commands that need no model

**CFG-70 (P1).** Commands that only read gnosh's own data (`gnosh mem ...`,
`gnosh session ...`, `gnosh trust ...`, `gnosh dirs`, `gnosh doctor`) start
no provider and make no request. They work with the model server off.
