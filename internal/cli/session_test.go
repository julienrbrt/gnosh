package cli

import "testing"

const sampleUUID = "123e4567-e89b-12d3-a456-426614174000"

// TestInvocationForms covers the ways of starting gnosh: a session, a
// session with a prompt, print mode by -p and by a redirected output, and
// where the prompt comes from.
func TestInvocationForms(t *testing.T) {
	runLines(t, allBuilt(t), []lineCase{
		{line: `gnosh`, want: inter(Invocation{})},
		{line: `gnosh "fix the build"`, want: inter(Invocation{Prompt: "fix the build"})},
		{line: `gnosh -p "prompt"`, want: pr(Invocation{Prompt: "prompt"})},
		{line: `gnosh --print "prompt"`, want: pr(Invocation{Prompt: "prompt"})},
		{line: `gnosh "fix the build" -p`, want: pr(Invocation{Prompt: "fix the build"})},

		// Standard input and output decide two things.
		{line: `gnosh -p "prompt"`, env: &piped, want: pr(Invocation{Prompt: "prompt", ReadStdin: true})},
		{line: `gnosh -p`, env: &piped, want: pr(Invocation{ReadStdin: true})},
		{line: `gnosh`, env: &piped, want: inter(Invocation{ReadStdin: true})},
		{line: `gnosh "q"`, env: &captured, want: pr(Invocation{Prompt: "q", PrintImplied: true})},
		{line: `gnosh`, env: &script, want: pr(Invocation{PrintImplied: true, ReadStdin: true})},
		{line: `gnosh -p "q"`, env: &script, want: pr(Invocation{Prompt: "q", ReadStdin: true})},
		{line: `gnosh`, env: &captured, kind: ErrUsage, say: []string{"No prompt was given", "not going to a terminal"}},
		{line: `gnosh -p`, kind: ErrUsage, say: []string{"Print mode answers one prompt, and none was given"}},
		{line: `gnosh -p -y`, kind: ErrUsage, say: []string{"Print mode answers one prompt"}},

		// Several words make one prompt.
		{line: `gnosh fix the tests`, want: inter(Invocation{Prompt: "fix the tests"})},
		{line: `gnosh -p fix the tests`, want: pr(Invocation{Prompt: "fix the tests"})},
		{line: `gnosh -p fix -y the --model q tests`, want: pr(Invocation{Prompt: "fix the tests", PermissionMode: ModeBypassPermissions, Model: "q"})},
		{line: `gnosh "first part" "second part"`, want: inter(Invocation{Prompt: "first part second part"})},

		// After -p no word is a command.
		{line: `gnosh -p trust the tests`, want: pr(Invocation{Prompt: "trust the tests"})},
		{line: `gnosh -p doctor`, want: pr(Invocation{Prompt: "doctor"})},
		{line: `gnosh -yp session list`, want: pr(Invocation{Prompt: "session list", PermissionMode: ModeBypassPermissions})},
		{line: `gnosh "what now" doctor`, want: inter(Invocation{Prompt: "what now doctor"})},

		// A lone word that is a command's name is the command. The error
		// that follows says how to send the word as a prompt.
		{line: `gnosh session`, kind: ErrUsage, say: []string{"gnosh session needs one of its commands after it", "If session is a prompt, put -- before it."}},
		{line: `gnosh -y -C logs`, kind: ErrUnknownFlag, say: []string{"-y (--yolo) was typed before gnosh logs and does not go with it", "If logs is a prompt, put -- before it."}},
		{line: `gnosh -r abc session`, kind: ErrUnknownFlag, say: []string{"-r (--resume) was typed before gnosh session", "If session is a prompt, put -- before it."}},
		{line: `gnosh doctor my code please`, kind: ErrUsage, say: []string{"my was typed after gnosh doctor", "If the whole line is a prompt, put it in quotes or after --."}},
		{line: `gnosh -y -C -- logs`, want: inter(Invocation{PermissionMode: ModeBypassPermissions, Conversation: ConversationContinue, Prompt: "logs"})},
		// help and version may follow anything, as --help and --version may.
		{line: `gnosh -y -C help`, want: Invocation{Command: CmdHelp}},
		{line: `gnosh -m qwen help doctor`, want: Invocation{Command: CmdHelp, HelpFor: []string{"doctor"}}},
		{line: `gnosh -y version`, want: Invocation{Command: CmdVersion}},
		{line: `gnosh --bogus help`, kind: ErrUnknownFlag, say: []string{"--bogus is not a flag of gnosh"}},

		// --bg sends off a run in print mode, so the flags print mode takes
		// go with it at a terminal as they do from a script; -p does not.
		{line: `gnosh --bg --max-turns 5 "fix it"`, want: inter(Invocation{Background: true, MaxTurns: 5, Prompt: "fix it"})},
		{line: `gnosh --bg --max-turns 5 "fix it"`, env: &captured, want: pr(Invocation{Background: true, MaxTurns: 5, Prompt: "fix it", PrintImplied: true})},
		{line: `gnosh --bg -p "fix it"`, kind: ErrConflict, say: []string{"--bg sends a session to the background, and -p does not go with it"}},
		// It may send off a session -c or -r names, but not a copy, and
		// not with nobody there to pick one from a list.
		{line: `gnosh --bg -r 01a1eea5-4a18 "go on"`, want: inter(Invocation{Background: true, Conversation: ConversationResume, Resume: "01a1eea5-4a18", Prompt: "go on"})},
		{line: `gnosh --bg -c "go on"`, want: inter(Invocation{Background: true, Conversation: ConversationContinue, Prompt: "go on"})},
		{line: `gnosh --bg -r`, kind: ErrUsage, say: []string{"-r with --bg needs the session's ID or name after it"}},
		{line: `gnosh --bg --fork-session -c "go on"`, kind: ErrConflict, say: []string{"--bg sends a session to the background, and --fork-session does not go with it"}},
	})
}

// TestPromptText covers what a prompt may hold and how it is kept.
func TestPromptText(t *testing.T) {
	runLines(t, allBuilt(t), []lineCase{
		{args: []string{"-p", "line one\nline two\n"}, want: pr(Invocation{Prompt: "line one\nline two\n"})},
		{args: []string{"  two  spaces\tand a tab  "}, want: inter(Invocation{Prompt: "  two  spaces\tand a tab  "})},
		{args: []string{"-p", "a\n", "b"}, want: pr(Invocation{Prompt: "a\n b"})},
		{args: []string{"héllo wörld — 你好"}, want: inter(Invocation{Prompt: "héllo wörld — 你好"})},
		{args: []string{"/compact"}, want: inter(Invocation{Prompt: "/compact"})},
		{args: []string{"-p", "what does --model do?"}, want: pr(Invocation{Prompt: "what does --model do?"})},
		{args: []string{"a=b"}, want: inter(Invocation{Prompt: "a=b"})},

		// A prompt that begins with a dash goes after "--".
		{line: `gnosh -- -5 is what`, want: inter(Invocation{Prompt: "-5 is what"})},
		{line: `gnosh -p -- "-race fails, why?"`, want: pr(Invocation{Prompt: "-race fails, why?"})},
		{line: `gnosh -p -- --help`, want: pr(Invocation{Prompt: "--help"})},
		{line: `gnosh -- -p`, want: inter(Invocation{Prompt: "-p"})},
		{line: `gnosh -y -- --model x -C`, want: inter(Invocation{Prompt: "--model x -C", PermissionMode: ModeBypassPermissions})},
		{line: `gnosh -- a -- b`, want: inter(Invocation{Prompt: "a -- b"})},
		{line: `gnosh first -- -second`, want: inter(Invocation{Prompt: "first -second"})},
		{line: `gnosh --`, want: inter(Invocation{})},
		{line: `gnosh -p -- -q`, want: pr(Invocation{Prompt: "-q"})},
		{line: `gnosh -- doctor`, want: inter(Invocation{Prompt: "doctor"})},
		{line: `gnosh -- trust x`, want: inter(Invocation{Prompt: "trust x"})},
		{line: `gnosh -- -`, want: inter(Invocation{Prompt: "-"})},

		// Without "--" it is refused, never guessed at.
		{line: `gnosh "-race fails, why?"`, kind: ErrUnknownFlag, say: []string{`"-race fails, why?"`, "has a space in it", "after --"}},
		{line: `gnosh "--model qwen"`, kind: ErrUnknownFlag, say: []string{`"--model qwen"`, "two arguments"}},
		{line: `gnosh -5`, kind: ErrUnknownFlag, say: []string{"-5 is not a flag of gnosh", "after --"}},
		{line: `gnosh -`, kind: ErrUsage, say: []string{"- by itself means nothing"}},
		{line: `gnosh -p -`, kind: ErrUsage, say: []string{"- by itself"}},

		// An empty prompt is a mistake, not a request.
		{line: `gnosh ""`, kind: ErrBadValue, say: []string{"The prompt on the command line is empty"}},
		{line: `gnosh -p "   "`, kind: ErrBadValue, say: []string{"prompt on the command line is empty"}},
		{line: `gnosh -p ""`, env: &piped, kind: ErrBadValue, say: []string{"empty"}},
		{args: []string{"-p", "\n\t"}, kind: ErrBadValue, say: []string{"empty"}},
		{line: `gnosh -- ""`, kind: ErrBadValue, say: []string{"empty"}},
	})
}

// TestConversationFlags covers -c, -r, --session-id, --fork-session and -n
// in every spelling.
func TestConversationFlags(t *testing.T) {
	cont := Invocation{Conversation: ConversationContinue}
	runLines(t, allBuilt(t), []lineCase{
		{line: `gnosh -c`, want: inter(cont)},
		{line: `gnosh -C`, want: inter(cont)},
		{line: `gnosh --continue`, want: inter(cont)},
		{line: `gnosh -c -C --continue`, want: inter(cont)},
		{line: `gnosh -c -p "query"`, want: pr(Invocation{Conversation: ConversationContinue, Prompt: "query"})},
		{line: `gnosh -p -C "query"`, want: pr(Invocation{Conversation: ConversationContinue, Prompt: "query"})},
		{line: `gnosh "query" -c`, want: inter(Invocation{Conversation: ConversationContinue, Prompt: "query"})},
		{line: `gnosh -c "go on"`, want: inter(Invocation{Conversation: ConversationContinue, Prompt: "go on"})},
		{line: `gnosh -C /compact`, want: inter(Invocation{Conversation: ConversationContinue, Prompt: "/compact"})},

		{line: `gnosh -r`, want: inter(Invocation{Conversation: ConversationResume})},
		{line: `gnosh --resume`, want: inter(Invocation{Conversation: ConversationResume})},
		{line: `gnosh -r abc123`, want: inter(Invocation{Conversation: ConversationResume, Resume: "abc123"})},
		{line: `gnosh --resume abc123`, want: inter(Invocation{Conversation: ConversationResume, Resume: "abc123"})},
		{line: `gnosh --resume=abc123`, want: inter(Invocation{Conversation: ConversationResume, Resume: "abc123"})},
		{line: `gnosh -r "My session" "query"`, want: inter(Invocation{Conversation: ConversationResume, Resume: "My session", Prompt: "query"})},
		{line: `gnosh -r "fix the bug"`, want: inter(Invocation{Conversation: ConversationResume, Resume: "fix the bug"})},
		{line: `gnosh -r doctor`, want: inter(Invocation{Conversation: ConversationResume, Resume: "doctor"})},
		{line: `gnosh -r -y`, want: inter(Invocation{Conversation: ConversationResume, PermissionMode: ModeBypassPermissions})},
		{line: `gnosh -r -- "query"`, want: inter(Invocation{Conversation: ConversationResume, Prompt: "query"})},
		{line: `gnosh "query" -r`, want: inter(Invocation{Conversation: ConversationResume, Prompt: "query"})},
		{line: `gnosh -r /home/u/.local/share/gnosh/s/one.jsonl`, want: inter(Invocation{Conversation: ConversationResume, Resume: "/home/u/.local/share/gnosh/s/one.jsonl", ResumeIsFile: true})},
		{line: `gnosh -r notes.jsonl`, want: inter(Invocation{Conversation: ConversationResume, Resume: "notes.jsonl"})},
		{line: `gnosh -p -r abc "query"`, want: pr(Invocation{Conversation: ConversationResume, Resume: "abc", Prompt: "query"})},
		{line: `gnosh "query" -p --resume=abc`, want: pr(Invocation{Conversation: ConversationResume, Resume: "abc", Prompt: "query"})},

		{line: `gnosh --session-id ` + sampleUUID, want: inter(Invocation{SessionID: sampleUUID})},
		{line: `gnosh --session-id=` + sampleUUID, want: inter(Invocation{SessionID: sampleUUID})},
		{line: `gnosh --session-id 123E4567-E89B-12D3-A456-426614174000`, want: inter(Invocation{SessionID: "123E4567-E89B-12D3-A456-426614174000"})},
		{line: `gnosh -c --fork-session`, want: inter(Invocation{Conversation: ConversationContinue, ForkSession: true})},
		{line: `gnosh --fork-session -r abc`, want: inter(Invocation{Conversation: ConversationResume, Resume: "abc", ForkSession: true})},
		{line: `gnosh --fork-session -r`, want: inter(Invocation{Conversation: ConversationResume, ForkSession: true})},
		{line: `gnosh -r abc --fork-session --session-id ` + sampleUUID, want: inter(Invocation{Conversation: ConversationResume, Resume: "abc", ForkSession: true, SessionID: sampleUUID})},
		{line: `gnosh -n "Release notes"`, want: inter(Invocation{Name: "Release notes"})},
		{line: `gnosh --name "Release notes"`, want: inter(Invocation{Name: "Release notes"})},
		{line: `gnosh --name=x -C`, want: inter(Invocation{Name: "x", Conversation: ConversationContinue})},

		// What cannot be combined, or is missing something.
		{line: `gnosh -c -r abc`, kind: ErrConflict, say: []string{"-c and -r each say which conversation"}},
		{line: `gnosh -r a -C`, kind: ErrConflict, say: []string{"-r and -C"}},
		{line: `gnosh --resume --continue`, kind: ErrConflict, say: []string{"--resume and --continue"}},
		{line: `gnosh --fork-session`, kind: ErrUsage, say: []string{"--fork-session goes on in a copy", "Add -c or -r to say which session to copy."}},
		{line: `gnosh -c --session-id ` + sampleUUID, kind: ErrConflict, say: []string{"--session-id gives a new conversation its ID", "-c goes on with an earlier one", "--fork-session"}},
		{line: `gnosh --session-id nope`, kind: ErrBadValue, say: []string{"--session-id nope is not a UUID"}},
		{line: `gnosh --session-id 123e4567e89b12d3a456426614174000`, kind: ErrBadValue, say: []string{"is not a UUID"}},
		{line: `gnosh --session-id 123e4567-e89b-12d3-a456-42661417400g`, kind: ErrBadValue, say: []string{"is not a UUID"}},
		{line: `gnosh -p -r`, kind: ErrUsage, say: []string{"-r needs a session's ID or name after it in print mode"}},
		{line: `gnosh -p --resume -y "query"`, kind: ErrUsage, say: []string{"--resume needs a session's ID or name"}},
		{line: `gnosh -r`, env: &captured, kind: ErrUsage, say: []string{"-r needs a session's ID or name", "not going to a terminal"}},
		{line: `gnosh -p -r "do it"`, kind: ErrUsage, say: []string{"Print mode answers one prompt"}},
		{line: `gnosh -r ""`, kind: ErrBadValue, say: []string{"-r was given an empty value", "type -r by itself"}},
		{line: `gnosh -n ""`, kind: ErrBadValue, say: []string{"-n was given an empty value"}},
		{line: `gnosh -n`, kind: ErrMissingValue, say: []string{"-n needs a value after it (<name>) and the command line ends there"}},
		{line: `gnosh -n -y`, kind: ErrMissingValue, say: []string{"-n needs a value after it (<name>), and what follows is -y", "--name=-y"}},
		{line: `gnosh --fork-session=yes -c`, kind: ErrBadValue, say: []string{"--fork-session=yes", "takes no value", `Leave the =yes off`}},

		// -c used to take the working folder. A single word right after it,
		// the one shape the old use could have, is refused, so that an old
		// script fails loudly instead of continuing some session, in the
		// wrong folder, with a path for a prompt.
		{line: `gnosh -p -y -c /tmp/bench/run1 -m p/m "go"`, kind: ErrUsage,
			say: []string{"-c /tmp/bench/run1: -c means --continue now", "For the folder, write --cwd /tmp/bench/run1.", "For a one-word prompt, write -C /tmp/bench/run1, or put -- before it."}},
		{line: `gnosh -p -y -c DIR -m provider/model`, env: &piped, kind: ErrUsage, say: []string{"-c DIR: -c means --continue now", "--cwd DIR"}},
		{line: `gnosh -c .`, kind: ErrUsage, say: []string{"-c .: -c means --continue now"}},
		{line: `gnosh -c ./work`, kind: ErrUsage, say: []string{"--cwd ./work"}},
		{line: `gnosh -c ../other "the prompt"`, kind: ErrUsage, say: []string{"--cwd ../other"}},
		{line: `gnosh -c ~/src/x`, kind: ErrUsage, say: []string{"--cwd ~/src/x"}},
		{line: `gnosh -c bench/case-1 -p "go"`, kind: ErrUsage, say: []string{"--cwd bench/case-1"}},
		{line: `gnosh -c workdir`, kind: ErrUsage, say: []string{"--cwd workdir", "-C workdir"}},
		{line: `gnosh -c go`, kind: ErrUsage, say: []string{"a single word right after it is refused rather than sent as the prompt", "write -C go"}},
		{line: `gnosh -c /compact`, kind: ErrUsage, say: []string{"write -C /compact"}},
		{line: `gnosh -yc /tmp/a/b`, kind: ErrUsage, say: []string{"-c /tmp/a/b"}},
		{line: `gnosh -Cc /tmp/a/b`, kind: ErrUsage, say: []string{"-c /tmp/a/b"}},
		// Everything else after -c is as before.
		{line: `gnosh -c -- /tmp/a/b`, want: inter(Invocation{Conversation: ConversationContinue, Prompt: "/tmp/a/b"})},
		{line: `gnosh -c -- go`, want: inter(Invocation{Conversation: ConversationContinue, Prompt: "go"})},
		{line: `gnosh -c -p go`, want: pr(Invocation{Conversation: ConversationContinue, Prompt: "go"})},
		{line: `gnosh go -c`, want: inter(Invocation{Conversation: ConversationContinue, Prompt: "go"})},
		{line: `gnosh -c "go on"`, want: inter(Invocation{Conversation: ConversationContinue, Prompt: "go on"})},
		{line: `gnosh -c "/tmp/a/b has a bug"`, want: inter(Invocation{Conversation: ConversationContinue, Prompt: "/tmp/a/b has a bug"})},
		// A word after -c that names a command is the command, and is told so.
		{line: `gnosh -c doctor`, kind: ErrUnknownFlag, say: []string{"-c (--continue) was typed before gnosh doctor and does not go with it"}},
		{line: `gnosh -c -y`, want: inter(Invocation{Conversation: ConversationContinue, PermissionMode: ModeBypassPermissions})},
		// -C and --continue never took a folder, so nothing is refused.
		{line: `gnosh -C /tmp/a/b`, want: inter(Invocation{Conversation: ConversationContinue, Prompt: "/tmp/a/b"})},
		{line: `gnosh -C go`, want: inter(Invocation{Conversation: ConversationContinue, Prompt: "go"})},
		{line: `gnosh -cC go`, want: inter(Invocation{Conversation: ConversationContinue, Prompt: "go"})},
		{line: `gnosh --continue /compact`, want: inter(Invocation{Conversation: ConversationContinue, Prompt: "/compact"})},
	})
}

// TestModelAndPermissionFlags covers the model flags and the permission
// flags that take no list.
func TestModelAndPermissionFlags(t *testing.T) {
	skip := Invocation{PermissionMode: ModeBypassPermissions}
	runLines(t, allBuilt(t), []lineCase{
		{line: `gnosh -m qwen`, want: inter(Invocation{Model: "qwen"})},
		{line: `gnosh --model provider/model`, want: inter(Invocation{Model: "provider/model"})},
		{line: `gnosh --model=provider/model`, want: inter(Invocation{Model: "provider/model"})},
		{line: `gnosh "hi" -m qwen`, want: inter(Invocation{Model: "qwen", Prompt: "hi"})},
		{line: `gnosh -p -m a --small-model b "hi"`, want: pr(Invocation{Model: "a", SmallModel: "b", Prompt: "hi"})},
		{line: `gnosh -p --small-model=b "hi"`, want: pr(Invocation{SmallModel: "b", Prompt: "hi"})},
		{line: `gnosh --reasoning-effort high`, want: inter(Invocation{Effort: "high"})},
		{line: `gnosh --effort off`, want: inter(Invocation{Effort: "off"})},
		{line: `gnosh --effort=low -p x`, want: pr(Invocation{Effort: "low", Prompt: "x"})},

		{line: `gnosh -y`, want: inter(skip)},
		{line: `gnosh --yolo`, want: inter(skip)},
		{line: `gnosh --dangerously-skip-permissions`, want: inter(skip)},
		{line: `gnosh -y --yolo --dangerously-skip-permissions`, want: inter(skip)},
		{line: `gnosh -p -y "go"`, want: pr(Invocation{PermissionMode: ModeBypassPermissions, Prompt: "go"})},
		{line: `gnosh --permission-mode default`, want: inter(Invocation{PermissionMode: ModeDefault})},
		{line: `gnosh --permission-mode manual`, want: inter(Invocation{PermissionMode: ModeDefault})},
		{line: `gnosh --permission-mode plan`, want: inter(Invocation{PermissionMode: ModePlan})},
		{line: `gnosh --permission-mode acceptEdits`, want: inter(Invocation{PermissionMode: ModeAcceptEdits})},
		{line: `gnosh --permission-mode=dontAsk`, want: inter(Invocation{PermissionMode: ModeDontAsk})},
		{line: `gnosh --permission-mode bypassPermissions`, want: inter(skip)},
		{line: `gnosh -y --permission-mode bypassPermissions`, want: inter(skip)},
		{line: `gnosh --trust-folder`, want: inter(Invocation{TrustFolder: true})},
		{line: `gnosh -p --trust-folder x`, want: pr(Invocation{TrustFolder: true, Prompt: "x"})},

		{line: `gnosh -y --permission-mode plan`, kind: ErrConflict, say: []string{"-y skips every permission question and --permission-mode plan starts in another mode, so nothing was started. Keep one of them."}},
		{line: `gnosh --permission-mode default --yolo`, kind: ErrConflict, say: []string{"--yolo skips"}},
		{line: `gnosh --permission-mode auto`, kind: ErrBadValue, say: []string{"--permission-mode auto: gnosh has no auto mode", "It takes default, plan, acceptEdits, dontAsk or bypassPermissions"}},
		{line: `gnosh --permission-mode bogus`, kind: ErrBadValue, say: []string{"--permission-mode takes default, plan, acceptEdits, dontAsk or bypassPermissions, and bogus is none of them"}},
		{line: `gnosh --permission-mode acceptedits`, kind: ErrBadValue, hint: "acceptEdits", say: []string{"Did you mean acceptEdits?"}},
		{line: `gnosh --permission-mode accept-edits`, kind: ErrBadValue, hint: "acceptEdits"},
		{line: `gnosh --permission-mode bypass`, kind: ErrBadValue, hint: "bypassPermissions"},
		{line: `gnosh --permission-mode Manual`, kind: ErrBadValue, hint: "manual"},
		{line: `gnosh --permission-mode ""`, kind: ErrBadValue, say: []string{"empty value"}},
		{line: `gnosh --permission-mode`, kind: ErrMissingValue, say: []string{"--permission-mode needs a value after it (<mode>)"}},
		{line: `gnosh -m ""`, kind: ErrBadValue, say: []string{"-m was given an empty value", "Give it <model>, or leave the flag out"}},
		{line: `gnosh --model=`, kind: ErrBadValue, say: []string{"--model was given an empty value"}},
		{line: `gnosh -m`, kind: ErrMissingValue, say: []string{"-m needs a value after it (<model>)"}},
		{line: `gnosh -m --verbose`, kind: ErrMissingValue, say: []string{"what follows is --verbose", "--model=--verbose"}},
		{line: `gnosh -m -- x`, kind: ErrMissingValue, say: []string{"what follows is --, which ends the flags"}},
		{line: `gnosh --model -fast`, kind: ErrMissingValue, say: []string{"what follows is -fast", "--model=-fast"}},
		{line: `gnosh --model=-fast`, want: inter(Invocation{Model: "-fast"})},
		{line: `gnosh --small-model b`, kind: ErrUsage, say: []string{"--small-model only does something in print mode"}},
	})
}

// TestPromptAndSettingsFlags covers what the model is told and the
// settings for the run.
func TestPromptAndSettingsFlags(t *testing.T) {
	runLines(t, allBuilt(t), []lineCase{
		{line: `gnosh --system-prompt "You are terse."`, want: inter(Invocation{SystemPrompt: "You are terse."})},
		{line: `gnosh --system-prompt="You are terse."`, want: inter(Invocation{SystemPrompt: "You are terse."})},
		{line: `gnosh --system-prompt-file ./sp.md`, want: inter(Invocation{SystemPromptFile: "./sp.md"})},
		{line: `gnosh --append-system-prompt "Answer in French."`, want: inter(Invocation{AppendSystemPrompt: "Answer in French."})},
		{line: `gnosh --append-system-prompt-file=/etc/extra.md`, want: inter(Invocation{AppendSystemPromptFile: "/etc/extra.md"})},
		{line: `gnosh -p q --system-prompt a --system-prompt-file b --append-system-prompt c --append-system-prompt-file d`,
			want: pr(Invocation{Prompt: "q", SystemPrompt: "a", SystemPromptFile: "b", AppendSystemPrompt: "c", AppendSystemPromptFile: "d"})},
		// Free text may begin with a dash, unless it is one of gnosh's flags.
		{line: `gnosh --system-prompt "- be terse"`, want: inter(Invocation{SystemPrompt: "- be terse"})},
		{line: `gnosh --append-system-prompt "--- context ---"`, want: inter(Invocation{AppendSystemPrompt: "--- context ---"})},
		{line: `gnosh --append-system-prompt -x`, want: inter(Invocation{AppendSystemPrompt: "-x"})},
		{line: `gnosh --system-prompt --verbose`, kind: ErrMissingValue, say: []string{"--system-prompt needs a value after it (<text>), and what follows is --verbose"}},
		{line: `gnosh --system-prompt -y`, kind: ErrMissingValue, say: []string{"what follows is -y"}},
		{line: `gnosh --system-prompt=--verbose`, want: inter(Invocation{SystemPrompt: "--verbose"})},
		{line: `gnosh --system-prompt ""`, kind: ErrBadValue, say: []string{"--system-prompt was given an empty value"}},
		{line: `gnosh --system-prompt-file -x`, kind: ErrMissingValue, say: []string{"what follows is -x"}},
		{args: []string{"--system-prompt", "line one\nline two"}, want: inter(Invocation{SystemPrompt: "line one\nline two"})},

		{line: `gnosh --cwd /work/app`, want: inter(Invocation{Cwd: "/work/app"})},
		{line: `gnosh --cwd=/work/app -C`, want: inter(Invocation{Cwd: "/work/app", Conversation: ConversationContinue})},
		{line: `gnosh --cwd "my folder" "the prompt"`, want: inter(Invocation{Cwd: "my folder", Prompt: "the prompt"})},
		{line: `gnosh --settings ./settings.json`, want: inter(Invocation{Settings: "./settings.json"})},
		{line: `gnosh --settings '{"permissions": {"allow": ["Read"]}}'`, want: inter(Invocation{Settings: `{"permissions": {"allow": ["Read"]}}`, SettingsInline: true})},
		{line: `gnosh --settings=' {"env": {}}'`, want: inter(Invocation{Settings: ` {"env": {}}`, SettingsInline: true})},
		{line: `gnosh --setting-sources user`, want: inter(Invocation{SettingSources: []string{"user"}})},
		{line: `gnosh --setting-sources user,project,local`, want: inter(Invocation{SettingSources: []string{"user", "project", "local"}})},
		{line: `gnosh --setting-sources="local, user"`, want: inter(Invocation{SettingSources: []string{"local", "user"}})},
		{line: `gnosh --safe-mode`, want: inter(Invocation{SafeMode: true})},
		{line: `gnosh -p --safe-mode x`, want: pr(Invocation{SafeMode: true, Prompt: "x"})},
		{line: `gnosh --disable-slash-commands`, want: inter(Invocation{DisableSlashCommands: true})},

		// --debug takes its filter only attached.
		{line: `gnosh -d`, want: inter(Invocation{Debug: true})},
		{line: `gnosh --debug`, want: inter(Invocation{Debug: true})},
		{line: `gnosh --debug=hooks,mcp`, want: inter(Invocation{Debug: true, DebugFilter: "hooks,mcp"})},
		{line: `gnosh --debug='!hooks,!mcp'`, want: inter(Invocation{Debug: true, DebugFilter: "!hooks,!mcp"})},
		{line: `gnosh --debug='mcp,startup'`, want: inter(Invocation{Debug: true, DebugFilter: "mcp,startup"})},
		{line: `gnosh --debug hooks`, want: inter(Invocation{Debug: true, Prompt: "hooks"})},
		{line: `gnosh -d "what is wrong"`, want: inter(Invocation{Debug: true, Prompt: "what is wrong"})},
		{line: `gnosh --debug-file /tmp/gnosh-debug.log`, want: inter(Invocation{Debug: true, DebugFile: "/tmp/gnosh-debug.log"})},
		{line: `gnosh --debug=`, kind: ErrBadValue, say: []string{"--debug was given an empty value", "type --debug by itself"}},
		{line: `gnosh -d=hooks`, kind: ErrBadValue, say: []string{"-d=hooks: -d takes a value only in its long form", "--debug=hooks"}},
		// "logs" is a command, and --debug is not one of its flags.
		{line: `gnosh --debug logs`, kind: ErrUnknownFlag, say: []string{"--debug was typed before gnosh logs and does not go with it"}},

		{line: `gnosh --verbose`, want: inter(Invocation{Verbose: true})},
		{line: `gnosh --verbose "hi"`, want: inter(Invocation{Verbose: true, Prompt: "hi"})},

		{line: `gnosh --setting-sources user,team`, kind: ErrBadValue, say: []string{"--setting-sources user,team names team", "user, project and local"}},
		{line: `gnosh --setting-sources user,,local`, kind: ErrBadValue, say: []string{"has an empty item"}},
		{line: `gnosh --setting-sources user,user`, kind: ErrBadValue, say: []string{"names user twice"}},
		{line: `gnosh --setting-sources projects`, kind: ErrBadValue, say: []string{"did you mean project?"}},
		{line: `gnosh --setting-sources ""`, kind: ErrBadValue, say: []string{"empty value"}},
		{line: `gnosh --cwd ""`, kind: ErrBadValue, say: []string{"--cwd was given an empty value"}},
		{line: `gnosh --cwd`, kind: ErrMissingValue, say: []string{"--cwd needs a value after it (<dir>)"}},
		{line: `gnosh --settings`, kind: ErrMissingValue, say: []string{"--settings needs a value after it (<file-or-json>)"}},
		{line: `gnosh --safe-mode=1`, kind: ErrBadValue, say: []string{"takes no value"}},
		{line: `gnosh --verbose=true`, kind: ErrBadValue, say: []string{"--verbose=true: --verbose is on when it is typed"}},
	})
}

// TestPrintFlags covers the flags of print mode, and -v, which is --version
// on a plain gnosh and --verbose in print mode.
func TestPrintFlags(t *testing.T) {
	runLines(t, allBuilt(t), []lineCase{
		{line: `gnosh -p x --output-format text`, want: pr(Invocation{Prompt: "x"})},
		{line: `gnosh -p x --output-format json`, want: pr(Invocation{Prompt: "x", OutputFormat: FormatJSON})},
		{line: `gnosh -p x --output-format=stream-json`, want: pr(Invocation{Prompt: "x", OutputFormat: FormatStreamJSON})},
		{line: `gnosh --output-format json -p x`, want: pr(Invocation{Prompt: "x", OutputFormat: FormatJSON})},
		{line: `gnosh --output-format json "x"`, env: &captured, want: pr(Invocation{Prompt: "x", OutputFormat: FormatJSON, PrintImplied: true})},
		{line: `gnosh -p --input-format text x`, want: pr(Invocation{Prompt: "x"})},
		{line: `gnosh -p --input-format=text`, env: &piped, want: pr(Invocation{ReadStdin: true})},
		{line: `gnosh -p --goal "the tests pass" "fix the build"`, want: pr(Invocation{Goal: "the tests pass", Prompt: "fix the build"})},
		{line: `gnosh -p --goal="the tests pass"`, want: pr(Invocation{Goal: "the tests pass"})},
		{line: `gnosh -p --goal "-race passes"`, want: pr(Invocation{Goal: "-race passes"})},
		{line: `gnosh -p x --max-turns 5`, want: pr(Invocation{MaxTurns: 5, Prompt: "x"})},
		{line: `gnosh -p x --max-turns=1`, want: pr(Invocation{MaxTurns: 1, Prompt: "x"})},
		{line: `gnosh -p x --verbose`, want: pr(Invocation{Verbose: true, Prompt: "x"})},

		// -v.
		{line: `gnosh -v`, want: Invocation{Command: CmdVersion}},
		{line: `gnosh --version`, want: Invocation{Command: CmdVersion}},
		{line: `gnosh version`, want: Invocation{Command: CmdVersion}},
		{line: `gnosh -v "a prompt"`, want: Invocation{Command: CmdVersion}},
		{line: `gnosh -v -y -C`, want: Invocation{Command: CmdVersion}},
		{line: `gnosh -v`, env: &script, want: Invocation{Command: CmdVersion}},
		{line: `gnosh --version`, env: &captured, want: Invocation{Command: CmdVersion}},
		{line: `gnosh -p --version`, want: Invocation{Command: CmdVersion}},
		{line: `gnosh -p -v x`, want: pr(Invocation{Verbose: true, Prompt: "x"})},
		{line: `gnosh -v -p x`, want: pr(Invocation{Verbose: true, Prompt: "x"})},
		{line: `gnosh x -v -p`, want: pr(Invocation{Verbose: true, Prompt: "x"})},
		{line: `gnosh -pv x`, want: pr(Invocation{Verbose: true, Prompt: "x"})},
		{line: `gnosh -vp x`, want: pr(Invocation{Verbose: true, Prompt: "x"})},
		{line: `gnosh -p -v`, kind: ErrUsage, say: []string{"Print mode answers one prompt"}},

		// Flags that only print mode acts on are refused outside it.
		{line: `gnosh --output-format json`, kind: ErrUsage, say: []string{"--output-format only does something in print mode, and this line opens a session", "Add -p"}},
		{line: `gnosh --input-format text`, kind: ErrUsage, say: []string{"--input-format only does something in print mode"}},
		{line: `gnosh --max-turns 3 "x"`, kind: ErrUsage, say: []string{"--max-turns only does something in print mode", "Add -p to answer once and exit."}},
		{line: `gnosh --goal "the tests pass"`, kind: ErrUsage, say: []string{"--goal only does something in print mode", "Inside a session, type /goal."}},

		{line: `gnosh -p x --output-format jsno`, kind: ErrBadValue, hint: "json", say: []string{"--output-format takes text, json or stream-json, and jsno is none of them", "Did you mean json?"}},
		{line: `gnosh -p x --output-format stream_json`, kind: ErrBadValue, hint: "stream-json"},
		{line: `gnosh -p x --output-format JSON`, kind: ErrBadValue, hint: "json"},
		{line: `gnosh -p x --output-format xml`, kind: ErrBadValue, say: []string{"xml is none of them"}},
		{line: `gnosh -p x --input-format json`, kind: ErrBadValue, say: []string{"--input-format takes text or stream-json, and json is none of them"}},
		{line: `gnosh -p x --max-turns 0`, kind: ErrBadValue, say: []string{"--max-turns takes a whole number of 1 or more, and 0 is not one"}},
		{line: `gnosh -p x --max-turns many`, kind: ErrBadValue, say: []string{"many is not one"}},
		{line: `gnosh -p x --max-turns 2.5`, kind: ErrBadValue, say: []string{"2.5 is not one"}},
		{line: `gnosh -p x --max-turns=-1`, kind: ErrBadValue, say: []string{"-1 is not one"}},
		{line: `gnosh -p x --max-turns -1`, kind: ErrMissingValue, say: []string{"what follows is -1"}},
		{line: `gnosh -p x --max-turns 99999999999999999999`, kind: ErrBadValue, say: []string{"is not one"}},
		{line: `gnosh -p x --goal ""`, kind: ErrBadValue, say: []string{"--goal was given an empty value"}},
	})
}

// TestBundles covers one-letter flags that share a dash.
func TestBundles(t *testing.T) {
	runLines(t, allBuilt(t), []lineCase{
		{line: `gnosh -yC`, want: inter(Invocation{PermissionMode: ModeBypassPermissions, Conversation: ConversationContinue})},
		{line: `gnosh -Cy`, want: inter(Invocation{PermissionMode: ModeBypassPermissions, Conversation: ConversationContinue})},
		{line: `gnosh -yd`, want: inter(Invocation{PermissionMode: ModeBypassPermissions, Debug: true})},
		{line: `gnosh -yn abc`, want: inter(Invocation{PermissionMode: ModeBypassPermissions, Name: "abc"})},
		{line: `gnosh -yr`, want: inter(Invocation{PermissionMode: ModeBypassPermissions, Conversation: ConversationResume})},
		{line: `gnosh -yr abc`, want: inter(Invocation{PermissionMode: ModeBypassPermissions, Conversation: ConversationResume, Resume: "abc"})},
		{line: `gnosh -ry abc`, want: inter(Invocation{PermissionMode: ModeBypassPermissions, Conversation: ConversationResume, Prompt: "abc"})},
		{line: `gnosh -ym qwen "hi"`, want: inter(Invocation{PermissionMode: ModeBypassPermissions, Model: "qwen", Prompt: "hi"})},
		{line: `gnosh -py hi`, want: pr(Invocation{PermissionMode: ModeBypassPermissions, Prompt: "hi"})},
		{line: `gnosh -pyvm qwen hi`, want: pr(Invocation{Verbose: true, PermissionMode: ModeBypassPermissions, Model: "qwen", Prompt: "hi"})},
		{line: `gnosh -pyCn draft hi`, want: pr(Invocation{PermissionMode: ModeBypassPermissions, Conversation: ConversationContinue, Name: "draft", Prompt: "hi"})},
		{line: `gnosh -yh`, want: Invocation{Command: CmdHelp}},

		{line: `gnosh -ny abc`, kind: ErrMissingValue, say: []string{"-ny: -n takes a value (<name>), so it has to be the last letter", "then -n <name>"}},
		{line: `gnosh -mqwen`, kind: ErrMissingValue, say: []string{"-mqwen: -m takes a value (<model>)"}},
		{line: `gnosh -ym`, kind: ErrMissingValue, say: []string{"-m needs a value after it (<model>) and the command line ends there"}},
		{line: `gnosh -m=qwen`, kind: ErrBadValue, say: []string{"-m=qwen: a one-letter flag takes its value as the next argument", "Write -m qwen, or --model=qwen"}},
		{line: `gnosh -ym=qwen`, kind: ErrBadValue, say: []string{"-ym=qwen", "Write -m qwen, or --model=qwen"}},
		{line: `gnosh -y=1`, kind: ErrBadValue, say: []string{"-y=1: a one-letter flag cannot be given a value with an equals sign"}},
		{line: `gnosh -=x`, kind: ErrBadValue, say: []string{"-=x"}},
		{line: `gnosh -yz`, kind: ErrUnknownFlag, say: []string{"-yz: -z is not a flag of gnosh"}},
		{line: `gnosh -x`, kind: ErrUnknownFlag, say: []string{"-x is not a flag of gnosh"}},
		{line: `gnosh -Y`, kind: ErrUnknownFlag, hint: "-y", say: []string{"-Y is not a flag of gnosh", "Did you mean -y (--yolo)? Letter case counts."}},
		{line: `gnosh -P x`, kind: ErrUnknownFlag, hint: "-p"},
		{line: `gnosh -é`, kind: ErrUnknownFlag, say: []string{"-é is not a flag of gnosh"}},
		{line: `gnosh -yCx`, kind: ErrUnknownFlag, say: []string{"-yCx: -x is not a flag of gnosh"}},
	})
}

// TestRepeatedFlags covers a flag typed twice: the last one counts, except
// for list flags, which add up.
func TestRepeatedFlags(t *testing.T) {
	runLines(t, allBuilt(t), []lineCase{
		{line: `gnosh -m a -m b`, want: inter(Invocation{Model: "b"})},
		{line: `gnosh --model=a -m b --model c`, want: inter(Invocation{Model: "c"})},
		{line: `gnosh -r a -r b`, want: inter(Invocation{Conversation: ConversationResume, Resume: "b"})},
		{line: `gnosh -r a -r`, want: inter(Invocation{Conversation: ConversationResume})},
		{line: `gnosh -r --resume=a`, want: inter(Invocation{Conversation: ConversationResume, Resume: "a"})},
		{line: `gnosh -n a --name b`, want: inter(Invocation{Name: "b"})},
		{line: `gnosh --cwd a --cwd b`, want: inter(Invocation{Cwd: "b"})},
		{line: `gnosh --permission-mode plan --permission-mode default`, want: inter(Invocation{PermissionMode: ModeDefault})},
		{line: `gnosh --permission-mode plan -y --permission-mode bypassPermissions`, want: inter(Invocation{PermissionMode: ModeBypassPermissions})},
		{line: `gnosh -p x --output-format json --output-format text`, want: pr(Invocation{Prompt: "x"})},
		{line: `gnosh -p x --max-turns 3 --max-turns 9`, want: pr(Invocation{Prompt: "x", MaxTurns: 9})},
		{line: `gnosh --system-prompt a --system-prompt b`, want: inter(Invocation{SystemPrompt: "b"})},
		{line: `gnosh --append-system-prompt a --append-system-prompt b`, want: inter(Invocation{AppendSystemPrompt: "b"})},
		{line: `gnosh --debug=hooks --debug`, want: inter(Invocation{Debug: true})},
		{line: `gnosh --debug --debug=hooks -d --debug=mcp`, want: inter(Invocation{Debug: true, DebugFilter: "mcp"})},
		{line: `gnosh --setting-sources user --setting-sources local`, want: inter(Invocation{SettingSources: []string{"local"}})},
		{line: `gnosh -y -y -C -C`, want: inter(Invocation{PermissionMode: ModeBypassPermissions, Conversation: ConversationContinue})},
		{line: `gnosh -p -p x --print`, want: pr(Invocation{Prompt: "x"})},
		{line: `gnosh -p --goal a --goal b`, want: pr(Invocation{Goal: "b"})},

		// Lists add up, across spellings too.
		{line: `gnosh --allowedTools Read --allowed-tools Edit`, want: inter(Invocation{AllowedTools: []string{"Read", "Edit"}})},
		{line: `gnosh --disallowedTools Bash "hi there" --disallowed-tools "Edit,Write"`, want: inter(Invocation{DisallowedTools: []string{"Bash", "Edit", "Write"}, Prompt: "hi there"})},
		{line: `gnosh --add-dir ../a --add-dir ../b ../c`, want: inter(Invocation{AddDirs: []string{"../a", "../b", "../c"}})},
		{line: `gnosh --tools Read --tools Edit`, want: inter(Invocation{Tools: []string{"Read", "Edit"}, ToolsGiven: true})},
		{line: `gnosh --tools "" --tools Read`, want: inter(Invocation{Tools: []string{"Read"}, ToolsGiven: true})},

		// Every value is checked, even one that a later one replaces.
		{line: `gnosh -p x --output-format nope --output-format text`, kind: ErrBadValue, say: []string{"nope is none of them"}},
	})
}
