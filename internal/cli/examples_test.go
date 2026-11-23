package cli

import "testing"

// TestHarnessSpecExamples parses the command lines that appear in
// spec/gnosh/harness-cli.md, sections 1 to 5, and checks that each means
// what the spec says. Placeholders in the spec (<id>, DIR) are given plain
// values.
//
// Where spec/DECISIONS.md item 26 overrules the spec, the line is written
// as it is typed now: print mode is -p where the spec writes `gnosh run`, a
// session is named with -r where it writes -s, and -q and -D are left out.
// The spec's lines for commands and flags whose parts are not built are
// not here: TestWhatIsNotInTheTable says what becomes of each.
func TestHarnessSpecExamples(t *testing.T) {
	skip := ModeBypassPermissions
	runLines(t, allBuilt(t), []lineCase{
		// The opening paragraph.
		{line: `gnosh`, want: inter(Invocation{})},
		{line: `gnosh "fix the build"`, want: inter(Invocation{Prompt: "fix the build"})},
		// (The spec's "prompt" stands for any prompt; the one word prompt
		// alone is gnosh prompt now, and goes after -- or -p as a prompt.)
		{line: `gnosh "the prompt"`, want: inter(Invocation{Prompt: "the prompt"})},
		{line: `gnosh -p "prompt"`, want: pr(Invocation{Prompt: "prompt"})},
		{line: `gnosh -- "prompt"`, want: inter(Invocation{Prompt: "prompt"})},
		{line: `gnosh -y`, want: inter(Invocation{PermissionMode: skip})},
		{line: `gnosh -y -C`, want: inter(Invocation{PermissionMode: skip, Conversation: ConversationContinue})},
		{line: `gnosh -y -r 5f3c`, want: inter(Invocation{PermissionMode: skip, Conversation: ConversationResume, Resume: "5f3c"})},
		// The line a script runs gnosh with: CLI-30 and spec/DECISIONS.md
		// item 10 changed what -c means, so the folder is --cwd.
		{line: `gnosh -p -y --cwd DIR -m provider/model "do the task"`,
			want: pr(Invocation{PermissionMode: skip, Cwd: "DIR", Model: "provider/model", Prompt: "do the task"})},
		{line: `gnosh -p -y --cwd DIR -m provider/model`, env: &piped,
			want: pr(Invocation{PermissionMode: skip, Cwd: "DIR", Model: "provider/model", ReadStdin: true})},
		// The old spelling is refused, with the fix, rather than read as
		// "continue, and send DIR as the prompt".
		{line: `gnosh -p -y -c DIR -m provider/model "do the task"`, kind: ErrUsage,
			say: []string{"-c DIR: -c means --continue now", "For the folder, write --cwd DIR."}},
		{line: `gnosh -p -y -c /srv/bench/case-1 -m provider/model "do the task"`, kind: ErrUsage,
			say: []string{"-c /srv/bench/case-1: -c means --continue now", "write --cwd /srv/bench/case-1"}},

		// CLI-1.
		{line: `gnosh -r 5f3c "now add the tests"`, want: inter(Invocation{Conversation: ConversationResume, Resume: "5f3c", Prompt: "now add the tests"})},
		{line: `gnosh -r 5f3c -- "prompt"`, want: inter(Invocation{Conversation: ConversationResume, Resume: "5f3c", Prompt: "prompt"})},
		{line: `gnosh -p "prompt"`, env: &piped, want: pr(Invocation{Prompt: "prompt", ReadStdin: true})},
		{line: `gnosh -c`, want: inter(Invocation{Conversation: ConversationContinue})},
		{line: `gnosh -C`, want: inter(Invocation{Conversation: ConversationContinue})},
		{line: `gnosh -r`, want: inter(Invocation{Conversation: ConversationResume})},
		{line: `gnosh -r "Release notes"`, want: inter(Invocation{Conversation: ConversationResume, Resume: "Release notes"})},

		// CLI-2: every flag of a session works after -p.
		{line: `gnosh -p fix the tests`, want: pr(Invocation{Prompt: "fix the tests"})},
		{line: `gnosh -p -y --cwd DIR -m provider/model "prompt"`,
			want: pr(Invocation{PermissionMode: skip, Cwd: "DIR", Model: "provider/model", Prompt: "prompt"})},

		// CLI-3.
		{line: `gnosh -p "..."`, env: &piped, want: pr(Invocation{Prompt: "...", ReadStdin: true})},
		{line: `gnosh -p "   "`, kind: ErrBadValue, say: []string{"prompt on the command line is empty"}},

		// CLI-4.
		{line: `gnosh --continue`, want: inter(Invocation{Conversation: ConversationContinue})},
		{line: `gnosh --resume 5f3c`, want: inter(Invocation{Conversation: ConversationResume, Resume: "5f3c"})},
		{line: `gnosh -p -r`, kind: ErrUsage, say: []string{"-r needs a session's ID or name after it in print mode"}},
		{line: `gnosh -C --fork-session`, want: inter(Invocation{Conversation: ConversationContinue, ForkSession: true})},
		{line: `gnosh -r 5f3c --fork-session`, want: inter(Invocation{Conversation: ConversationResume, Resume: "5f3c", ForkSession: true})},
		{line: `gnosh -p -r 5f3c --fork-session "go on"`, want: pr(Invocation{Conversation: ConversationResume, Resume: "5f3c", ForkSession: true, Prompt: "go on"})},
		{line: `gnosh -n "Release notes"`, want: inter(Invocation{Name: "Release notes"})},
		{line: `gnosh --name "Release notes" -C --fork-session`, want: inter(Invocation{Name: "Release notes", Conversation: ConversationContinue, ForkSession: true})},

		// CLI-5.
		{line: `gnosh -m qwen3.8-27b`, want: inter(Invocation{Model: "qwen3.8-27b"})},
		{line: `gnosh --model local/qwen3.8-27b`, want: inter(Invocation{Model: "local/qwen3.8-27b"})},
		{line: `gnosh -p -m local/qwen --small-model local/qwen "task"`, want: pr(Invocation{Model: "local/qwen", SmallModel: "local/qwen", Prompt: "task"})},
		{line: `gnosh -p --reasoning-effort low "task"`, want: pr(Invocation{Effort: "low", Prompt: "task"})},
		{line: `gnosh -p --effort low "task"`, want: pr(Invocation{Effort: "low", Prompt: "task"})},

		// CLI-6.
		{line: `gnosh --yolo`, want: inter(Invocation{PermissionMode: skip})},
		// Skipping within reach, without starting that way: the mode is
		// whatever else the line says.
		{line: `gnosh --allow-dangerously-skip-permissions`, want: inter(Invocation{AllowSkipPermissions: true})},
		{line: `gnosh --allow-dangerously-skip-permissions --permission-mode plan`, want: inter(Invocation{AllowSkipPermissions: true, PermissionMode: ModePlan})},
		{line: `gnosh --dangerously-skip-permissions`, want: inter(Invocation{PermissionMode: skip})},
		{line: `gnosh -p --yolo "task"`, want: pr(Invocation{PermissionMode: skip, Prompt: "task"})},
		{line: `gnosh --permission-mode default`, want: inter(Invocation{PermissionMode: ModeDefault})},
		{line: `gnosh --permission-mode manual`, want: inter(Invocation{PermissionMode: ModeDefault})},
		{line: `gnosh --permission-mode plan`, want: inter(Invocation{PermissionMode: ModePlan})},
		{line: `gnosh --permission-mode acceptEdits`, want: inter(Invocation{PermissionMode: ModeAcceptEdits})},
		{line: `gnosh --permission-mode dontAsk`, want: inter(Invocation{PermissionMode: ModeDontAsk})},
		{line: `gnosh --permission-mode bypassPermissions`, want: inter(Invocation{PermissionMode: ModeBypassPermissions})},
		{line: `gnosh --permission-mode auto`, kind: ErrBadValue, say: []string{"gnosh has no auto mode"}},
		{line: `gnosh --allowedTools "Read,Bash(git diff *)"`, want: inter(Invocation{AllowedTools: []string{"Read", "Bash(git diff *)"}})},
		{line: `gnosh --allowed-tools Read`, want: inter(Invocation{AllowedTools: []string{"Read"}})},
		{line: `gnosh --disallowedTools Bash`, want: inter(Invocation{DisallowedTools: []string{"Bash"}})},
		{line: `gnosh --disallowed-tools "Bash(rm *)"`, want: inter(Invocation{DisallowedTools: []string{"Bash(rm *)"}})},
		{line: `gnosh -p --tools "" "review the change"`, want: pr(Invocation{ToolsGiven: true, Prompt: "review the change"})},
		{line: `gnosh --tools default`, want: inter(Invocation{Tools: []string{"default"}, ToolsGiven: true})},
		{line: `gnosh --add-dir ../shared --add-dir /srv/docs`, want: inter(Invocation{AddDirs: []string{"../shared", "/srv/docs"}})},
		{line: `gnosh --trust-folder`, want: inter(Invocation{TrustFolder: true})},
		{line: `gnosh -p --trust-folder "task"`, want: pr(Invocation{TrustFolder: true, Prompt: "task"})},

		// CLI-7.
		{line: `gnosh --system-prompt "You review Go code."`, want: inter(Invocation{SystemPrompt: "You review Go code."})},
		{line: `gnosh --system-prompt-file ./review.md`, want: inter(Invocation{SystemPromptFile: "./review.md"})},
		{line: `gnosh --append-system-prompt "Answer briefly."`, want: inter(Invocation{AppendSystemPrompt: "Answer briefly."})},
		{line: `gnosh --append-system-prompt-file ./extra.md`, want: inter(Invocation{AppendSystemPromptFile: "./extra.md"})},

		// CLI-8.
		{line: `gnosh --cwd DIR`, want: inter(Invocation{Cwd: "DIR"})},
		{line: `gnosh --safe-mode`, want: inter(Invocation{SafeMode: true})},
		{line: `gnosh -d`, want: inter(Invocation{Debug: true})},
		{line: `gnosh --debug`, want: inter(Invocation{Debug: true})},

		// CLI-9.
		{line: `gnosh -p --output-format text "q"`, want: pr(Invocation{Prompt: "q"})},
		{line: `gnosh -p --output-format json "q"`, want: pr(Invocation{OutputFormat: FormatJSON, Prompt: "q"})},
		{line: `gnosh -p --output-format stream-json "q"`, want: pr(Invocation{OutputFormat: FormatStreamJSON, Prompt: "q"})},
		{line: `gnosh -p -v "q"`, want: pr(Invocation{Verbose: true, Prompt: "q"})},
		{line: `gnosh -p --verbose "q"`, want: pr(Invocation{Verbose: true, Prompt: "q"})},
		{line: `gnosh -p --goal "go test ./... passes" "fix the build"`, want: pr(Invocation{Goal: "go test ./... passes", Prompt: "fix the build"})},

		// CLI-10 and CLI-11.
		{line: `gnosh -p`, env: &piped, want: pr(Invocation{ReadStdin: true})},
		{line: `gnosh doctor`, want: Invocation{Command: CmdDoctor}},
		{line: `gnosh trust`, want: Invocation{Command: CmdTrust}},
		{line: `gnosh trust ../other`, want: Invocation{Command: CmdTrust, Args: []string{"../other"}}},
		{line: `gnosh trust --list`, want: Invocation{Command: CmdTrust, List: true}},
		{line: `gnosh trust --forget`, want: Invocation{Command: CmdTrust, Forget: true}},
		{line: `gnosh trust --forget ../other`, want: Invocation{Command: CmdTrust, Forget: true, Args: []string{"../other"}}},
		{line: `gnosh models`, want: Invocation{Command: CmdModels}},
		{line: `gnosh session list`, want: Invocation{Command: CmdSessionList}},
		{line: `gnosh session show 5f3c`, want: Invocation{Command: CmdSessionShow, Args: []string{"5f3c"}}},
		{line: `gnosh session last`, want: Invocation{Command: CmdSessionLast}},
		{line: `gnosh session rename 5f3c "A better title"`, want: Invocation{Command: CmdSessionRename, Args: []string{"5f3c", "A better title"}}},
		{line: `gnosh sessions list --json`, want: Invocation{Command: CmdSessionList, JSON: true}},
		{line: `gnosh s ls`, want: Invocation{Command: CmdSessionList}},
		{line: `gnosh s show 5f3c --json`, want: Invocation{Command: CmdSessionShow, Args: []string{"5f3c"}, JSON: true}},
		{line: `gnosh s last --json`, want: Invocation{Command: CmdSessionLast, JSON: true}},
		{line: `gnosh s rename 5f3c title --json`, want: Invocation{Command: CmdSessionRename, Args: []string{"5f3c", "title"}, JSON: true}},
		{line: `gnosh mem`, want: Invocation{Command: CmdMem}},
		{line: `gnosh mem show`, want: Invocation{Command: CmdMem, Args: []string{"show"}}},
		{line: `gnosh mem show plan risks`, want: Invocation{Command: CmdMem, Args: []string{"show", "plan", "risks"}}},
		{line: `gnosh mem find flaky tests`, want: Invocation{Command: CmdMem, Args: []string{"find", "flaky", "tests"}}},
		{line: `gnosh mem log`, want: Invocation{Command: CmdMem, Args: []string{"log"}}},
		{line: `gnosh mem log 20`, want: Invocation{Command: CmdMem, Args: []string{"log", "20"}}},
		{line: `gnosh logs`, want: Invocation{Command: CmdLogs}},
		{line: `gnosh logs -f`, want: Invocation{Command: CmdLogs, Follow: true}},
		{line: `gnosh logs -t 200`, want: Invocation{Command: CmdLogs, Tail: 200}},
		{line: `gnosh logs -f -t 200`, want: Invocation{Command: CmdLogs, Follow: true, Tail: 200}},
		{line: `gnosh dirs`, want: Invocation{Command: CmdDirs}},
		{line: `gnosh --version`, want: Invocation{Command: CmdVersion}},

		// CLI-13: not carried over.
		{line: `gnosh -H box.example.com`, kind: ErrUnknownFlag},
		{line: `gnosh --host box.example.com`, kind: ErrUnknownFlag},
		{line: `gnosh --channels server:chat --dangerously-load-development-channels server:hook`, want: inter(Invocation{Channels: []string{"server:chat", "server:hook"}})},

		// CLI-30.
		{line: `gnosh --cwd /work/app -c`, want: inter(Invocation{Cwd: "/work/app", Conversation: ConversationContinue})},

		// CLI-31, as spec/DECISIONS.md item 11 leaves it.
		{line: `gnosh -p --permission-mode dontAsk --allowedTools "Bash(go test *)" "run the tests and report"`,
			want: pr(Invocation{PermissionMode: ModeDontAsk, AllowedTools: []string{"Bash(go test *)"}, Prompt: "run the tests and report"})},

		// CLI-32: the one-argument forms keep working.
		{line: `gnosh --disallowedTools "Bash Edit"`, want: inter(Invocation{DisallowedTools: []string{"Bash", "Edit"}})},
		{line: `gnosh --disallowedTools Bash,Edit`, want: inter(Invocation{DisallowedTools: []string{"Bash", "Edit"}})},
		{line: `gnosh -p -y --disallowedTools "Bash Edit" "look, do not touch"`, want: pr(Invocation{PermissionMode: skip, DisallowedTools: []string{"Bash", "Edit"}, Prompt: "look, do not touch"})},
	})
}

// TestClaudeCodeSpecExamples parses the command lines of spec/cc/cli.md and
// the ones spec/cc/output-formats.md says must keep working, for the flags
// and commands gnosh has built. The others are not in the table
// (TestWhatIsNotInTheTable).
func TestClaudeCodeSpecExamples(t *testing.T) {
	runLines(t, allBuilt(t), []lineCase{
		// Section 1: invocation forms.
		{line: `gnosh`, want: inter(Invocation{})},
		{line: `gnosh "query"`, want: inter(Invocation{Prompt: "query"})},
		{line: `gnosh -p "query"`, want: pr(Invocation{Prompt: "query"})},
		{line: `gnosh -p "query"`, env: &piped, want: pr(Invocation{Prompt: "query", ReadStdin: true})},
		{line: `gnosh -p`, env: &piped, want: pr(Invocation{ReadStdin: true})},
		{line: `gnosh -c`, want: inter(Invocation{Conversation: ConversationContinue})},
		{line: `gnosh -c -p "query"`, want: pr(Invocation{Conversation: ConversationContinue, Prompt: "query"})},
		{line: `gnosh -r "auth-refactor" "query"`, want: inter(Invocation{Conversation: ConversationResume, Resume: "auth-refactor", Prompt: "query"})},
		{line: `gnosh -r`, want: inter(Invocation{Conversation: ConversationResume})},
		// Output that is not a terminal means print mode, which needs a prompt.
		{line: `gnosh "query"`, env: &script, want: pr(Invocation{Prompt: "query", PrintImplied: true, ReadStdin: true})},
		{line: `gnosh`, env: &captured, kind: ErrUsage, say: []string{"No prompt was given"}},

		// Section 2: the parsing rules.
		{line: `gnosh --allowedTools Read`, want: inter(Invocation{AllowedTools: []string{"Read"}})},
		{line: `gnosh --allowed-tools Read`, want: inter(Invocation{AllowedTools: []string{"Read"}})},
		{line: `gnosh --disallowedTools Edit`, want: inter(Invocation{DisallowedTools: []string{"Edit"}})},
		{line: `gnosh --disallowed-tools Edit`, want: inter(Invocation{DisallowedTools: []string{"Edit"}})},
		{line: `gnosh --model qwen`, want: inter(Invocation{Model: "qwen"})},
		{line: `gnosh --model=qwen`, want: inter(Invocation{Model: "qwen"})},
		{line: `gnosh --debug='mcp,startup'`, want: inter(Invocation{Debug: true, DebugFilter: "mcp,startup"})},
		{line: `gnosh --debug startup`, want: inter(Invocation{Debug: true, Prompt: "startup"})},
		{line: `gnosh --add-dir ../apps ../lib`, want: inter(Invocation{AddDirs: []string{"../apps", "../lib"}})},
		{line: `gnosh -p "query" --allowedTools "Read,Edit,Bash"`, want: pr(Invocation{Prompt: "query", AllowedTools: []string{"Read", "Edit", "Bash"}})},
		{line: `gnosh --allowedTools "Bash(git log *)" "Bash(git diff *)" "Read"`, want: inter(Invocation{AllowedTools: []string{"Bash(git log *)", "Bash(git diff *)", "Read"}})},
		{line: `gnosh --allowedTools "Bash,Read,Edit"`, want: inter(Invocation{AllowedTools: []string{"Bash", "Read", "Edit"}})},
		{line: `gnosh --allowedTools "Bash(git *) Edit"`, want: inter(Invocation{AllowedTools: []string{"Bash(git *)", "Edit"}})},
		{line: `gnosh -r /home/u/t/0b6c.jsonl`, want: inter(Invocation{Conversation: ConversationResume, Resume: "/home/u/t/0b6c.jsonl", ResumeIsFile: true})},
		{line: `gnosh --debug=hooks,mcp`, want: inter(Invocation{Debug: true, DebugFilter: "hooks,mcp"})},
		{line: `gnosh --debug='!hooks,!mcp'`, want: inter(Invocation{Debug: true, DebugFilter: "!hooks,!mcp"})},
		// "claude udpate" prints "Did you mean claude update?": the same
		// check, with a command gnosh has.
		{line: `gnosh docter`, kind: ErrUnknownCommand, hint: "doctor", say: []string{"Did you mean doctor?"}},
		{line: `gnosh --no-such-flag`, kind: ErrUnknownFlag},

		// Section 3: the flags.
		{line: `gnosh -p "q" --output-format json --verbose`, want: pr(Invocation{Prompt: "q", OutputFormat: FormatJSON, Verbose: true})},
		{line: `gnosh -p "q" --output-format stream-json --verbose`, want: pr(Invocation{Prompt: "q", OutputFormat: FormatStreamJSON, Verbose: true})},
		{line: `gnosh -p "q" --input-format text`, want: pr(Invocation{Prompt: "q"})},
		{line: `gnosh -p "q" --max-turns 3`, want: pr(Invocation{Prompt: "q", MaxTurns: 3})},
		{line: `gnosh --session-id ` + sampleUUID, want: inter(Invocation{SessionID: sampleUUID})},
		{line: `gnosh -c --fork-session`, want: inter(Invocation{Conversation: ConversationContinue, ForkSession: true})},
		{line: `gnosh -n "auth-refactor"`, want: inter(Invocation{Name: "auth-refactor"})},
		{line: `gnosh --permission-mode plan`, want: inter(Invocation{PermissionMode: ModePlan})},
		{line: `gnosh --dangerously-skip-permissions`, want: inter(Invocation{PermissionMode: ModeBypassPermissions})},
		{line: `gnosh --disallowedTools "Edit"`, want: inter(Invocation{DisallowedTools: []string{"Edit"}})},
		{line: `gnosh --disallowedTools "*"`, want: inter(Invocation{DisallowedTools: []string{"*"}})},
		{line: `gnosh --disallowedTools "mcp__*" "Bash(rm *)"`, want: inter(Invocation{DisallowedTools: []string{"mcp__*", "Bash(rm *)"}})},
		{line: `gnosh --tools ""`, want: inter(Invocation{ToolsGiven: true})},
		{line: `gnosh --tools "default"`, want: inter(Invocation{Tools: []string{"default"}, ToolsGiven: true})},
		{line: `gnosh --tools "Bash,Edit,Read"`, want: inter(Invocation{Tools: []string{"Bash", "Edit", "Read"}, ToolsGiven: true})},
		{line: `gnosh --system-prompt "a" --append-system-prompt "b"`, want: inter(Invocation{SystemPrompt: "a", AppendSystemPrompt: "b"})},
		{line: `gnosh --system-prompt "inline" --system-prompt-file ./sp.md`, want: inter(Invocation{SystemPrompt: "inline", SystemPromptFile: "./sp.md"})},
		{line: `gnosh --settings ./ci-settings.json`, want: inter(Invocation{Settings: "./ci-settings.json"})},
		{line: `gnosh --settings '{"permissions": {"defaultMode": "plan"}}'`, want: inter(Invocation{Settings: `{"permissions": {"defaultMode": "plan"}}`, SettingsInline: true})},
		{line: `gnosh --setting-sources user,project,local`, want: inter(Invocation{SettingSources: []string{"user", "project", "local"}})},
		{line: `gnosh -p "q" --append-system-prompt-file ./ctx.md --settings ./s.json --add-dir ../lib`,
			want: pr(Invocation{Prompt: "q", AppendSystemPromptFile: "./ctx.md", Settings: "./s.json", AddDirs: []string{"../lib"}})},
		{line: `gnosh --safe-mode`, want: inter(Invocation{SafeMode: true})},
		{line: `gnosh --disable-slash-commands`, want: inter(Invocation{DisableSlashCommands: true})},
		{line: `gnosh -v`, want: Invocation{Command: CmdVersion}},
		{line: `gnosh --version`, want: Invocation{Command: CmdVersion}},
		{line: `gnosh -h`, want: helpFor()},
		{line: `gnosh --help`, want: helpFor()},
		{line: `gnosh -d`, want: inter(Invocation{Debug: true})},
		{line: `gnosh --debug-file /tmp/debug.log`, want: inter(Invocation{Debug: true, DebugFile: "/tmp/debug.log"})},

		// Section 4.1: a prompt that names a slash command is a prompt.
		{line: `gnosh -p "/compact"`, want: pr(Invocation{Prompt: "/compact"})},
		{line: `gnosh -p "/model qwen"`, want: pr(Invocation{Prompt: "/model qwen"})},

		// Section 5: subcommands.
		{line: `gnosh doctor`, want: Invocation{Command: CmdDoctor}},

		// spec/cc/output-formats.md section 2.
		{line: `gnosh -p "Summarize this project" --output-format json`, env: &captured, want: pr(Invocation{Prompt: "Summarize this project", OutputFormat: FormatJSON})},
		{line: `gnosh -p "Start a review" --output-format json`, env: &script, want: pr(Invocation{Prompt: "Start a review", OutputFormat: FormatJSON, ReadStdin: true})},
	})
}
