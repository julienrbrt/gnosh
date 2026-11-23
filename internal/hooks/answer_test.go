package hooks

import (
	"strings"
	"testing"
)

// expect is what an Outcome must be. A reason, a stop reason or a context
// that starts with "~" is a part the text must contain; anything else is
// the whole text.
type expect struct {
	blocked  bool
	failed   bool
	decision Decision
	reason   string
	context  string
	endTurn  bool
	stop     string
	system   []string
	input    string
	output   string   // the replacement for the tool's output, compacted
	warnings []string // one part for each warning expected, in order
	result   Result   // the hook's record; ResultNone when left empty
	notes    []string // parts the record's notes must contain
	action   string   // the answer to an MCP server's request
	content  string   // its values, compacted
}

// check compares an Outcome of one hook with what is expected.
func check(t *testing.T, out Outcome, want expect) {
	t.Helper()
	text := func(what, got, want string) {
		t.Helper()
		if part, ok := strings.CutPrefix(want, "~"); ok {
			if !strings.Contains(got, part) {
				t.Errorf("%s does not mention %q: %q", what, part, got)
			}
		} else if got != want {
			t.Errorf("%s = %q, want %q", what, got, want)
		}
	}
	if out.Blocked != want.blocked {
		t.Errorf("Blocked = %v, want %v", out.Blocked, want.blocked)
	}
	if out.CouldNotEvaluate != want.failed {
		t.Errorf("CouldNotEvaluate = %v, want %v", out.CouldNotEvaluate, want.failed)
	}
	if out.PermissionDecision != want.decision {
		t.Errorf("PermissionDecision = %q, want %q", out.PermissionDecision, want.decision)
	}
	text("Reason", out.Reason, want.reason)
	text("AdditionalContext", out.AdditionalContext, want.context)
	if out.EndTurn != want.endTurn {
		t.Errorf("EndTurn = %v, want %v", out.EndTurn, want.endTurn)
	}
	text("StopReason", out.StopReason, want.stop)
	if strings.Join(out.SystemMessages, "|") != strings.Join(want.system, "|") {
		t.Errorf("SystemMessages = %q, want %q", out.SystemMessages, want.system)
	}
	if string(out.UpdatedInput) != want.input {
		t.Errorf("UpdatedInput = %s, want %s", out.UpdatedInput, want.input)
	}
	if string(out.UpdatedOutput) != want.output {
		t.Errorf("UpdatedOutput = %s, want %s", out.UpdatedOutput, want.output)
	}
	if out.ElicitAction != want.action || string(out.ElicitContent) != want.content {
		t.Errorf("ElicitAction %q with %s, want %q with %s", out.ElicitAction, out.ElicitContent, want.action, want.content)
	}
	if len(out.Warnings) != len(want.warnings) {
		t.Errorf("got %d warnings, want %d: %q", len(out.Warnings), len(want.warnings), out.Warnings)
	} else {
		for i, part := range want.warnings {
			contains(t, "the warning", out.Warnings[i], part)
		}
	}
	if len(out.Records) != 1 {
		t.Fatalf("got %d records, want 1", len(out.Records))
	}
	rec := out.Records[0]
	wantResult := want.result
	if wantResult == "" {
		wantResult = ResultNone
	}
	if rec.Result != wantResult {
		t.Errorf("the record's Result = %q, want %q", rec.Result, wantResult)
	}
	contains(t, "the record's notes", strings.Join(rec.Notes, "\n"), want.notes...)
	if len(want.notes) == 0 && len(rec.Notes) != 0 {
		t.Errorf("the record has notes nobody expected: %q", rec.Notes)
	}
}

// answerForms is every form of answer a hook can print, event by event, with
// what it comes to. after is shell text run after the answer is printed,
// such as "exit 2".
var answerForms = []struct {
	name   string
	event  Event
	prints string
	after  string
	want   expect
}{
	// ---- PreToolUse: allow, deny, ask, the older top-level words, a
	// changed input, added text.
	{name: "allow", event: PreToolUse,
		prints: `{"hookSpecificOutput": {"hookEventName": "PreToolUse", "permissionDecision": "allow", "permissionDecisionReason": "a read-only command"}}`,
		want:   expect{decision: Allow, reason: "a read-only command", result: ResultApproved}},
	{name: "deny", event: PreToolUse,
		prints: `{"hookSpecificOutput": {"hookEventName": "PreToolUse", "permissionDecision": "deny", "permissionDecisionReason": "rm is not allowed here"}}`,
		want:   expect{blocked: true, decision: Deny, reason: "rm is not allowed here", result: ResultBlocked}},
	{name: "deny with no reason", event: PreToolUse,
		prints: `{"hookSpecificOutput": {"permissionDecision": "deny"}}`,
		want:   expect{blocked: true, decision: Deny, reason: "~blocked this and gave no reason", result: ResultBlocked}},
	{name: "ask", event: PreToolUse,
		prints: `{"hookSpecificOutput": {"hookEventName": "PreToolUse", "permissionDecision": "ask", "permissionDecisionReason": "a push needs your say"}}`,
		want:   expect{decision: Ask, reason: "a push needs your say", result: ResultAsked}},
	{name: "the older approve", event: PreToolUse,
		prints: `{"decision": "approve", "reason": "fine by the old form"}`,
		want:   expect{decision: Allow, reason: "fine by the old form", result: ResultApproved}},
	{name: "the older block", event: PreToolUse,
		prints: `{"decision": "block", "reason": "no, by the old form"}`,
		want:   expect{blocked: true, decision: Deny, reason: "no, by the old form", result: ResultBlocked}},
	{name: "both forms: permissionDecision wins", event: PreToolUse,
		prints: `{"decision": "block", "reason": "the old form says no", "hookSpecificOutput": {"permissionDecision": "allow"}}`,
		want:   expect{decision: Allow, reason: "the old form says no", result: ResultApproved}},
	{name: "both forms: permissionDecision wins the other way", event: PreToolUse,
		prints: `{"decision": "approve", "hookSpecificOutput": {"permissionDecision": "deny", "permissionDecisionReason": "the new form says no"}}`,
		want:   expect{blocked: true, decision: Deny, reason: "the new form says no", result: ResultBlocked}},
	{name: "allow with a changed input", event: PreToolUse,
		prints: `{"hookSpecificOutput": {"permissionDecision": "allow", "updatedInput": { "command": "ls -la",  "description": "List" }}}`,
		want:   expect{decision: Allow, input: `{"command":"ls -la","description":"List"}`, result: ResultApproved}},
	{name: "a changed input and no opinion", event: PreToolUse,
		prints: `{"hookSpecificOutput": {"updatedInput": {"command": "ls -la"}}}`,
		want:   expect{input: `{"command":"ls -la"}`}},
	{name: "ask with a changed input", event: PreToolUse,
		prints: `{"hookSpecificOutput": {"permissionDecision": "ask", "permissionDecisionReason": "like this?", "updatedInput": {"command": "ls"}}}`,
		want:   expect{decision: Ask, reason: "like this?", input: `{"command":"ls"}`, result: ResultAsked}},
	{name: "deny with a changed input", event: PreToolUse,
		prints: `{"hookSpecificOutput": {"permissionDecision": "deny", "permissionDecisionReason": "no", "updatedInput": {"command": "ls"}}}`,
		want:   expect{blocked: true, decision: Deny, reason: "no", result: ResultBlocked}},
	{name: "added text", event: PreToolUse,
		prints: `{"hookSpecificOutput": {"hookEventName": "PreToolUse", "additionalContext": "this repo is linted on commit"}}`,
		want:   expect{context: "this repo is linted on commit"}},
	{name: "added text as a list", event: PreToolUse,
		prints: `{"hookSpecificOutput": {"additionalContext": ["first line", "second line"]}}`,
		want:   expect{context: "first line\nsecond line"}},
	{name: "a block at the top and text inside", event: PreToolUse,
		prints: `{"decision": "block", "reason": "not on main", "hookSpecificOutput": {"additionalContext": "the work branch is feature/x"}}`,
		want:   expect{blocked: true, decision: Deny, reason: "not on main", context: "the work branch is feature/x", result: ResultBlocked}},
	{name: "a warning for the user", event: PreToolUse,
		prints: `{"systemMessage": "this repository has a pre-push check"}`,
		want:   expect{system: []string{"this repository has a pre-push check"}, result: ResultWarning}},
	{name: "the turn ended", event: PreToolUse,
		prints: `{"continue": false, "stopReason": "the budget is spent"}`,
		want:   expect{endTurn: true, stop: "the budget is spent", result: ResultEndedTurn}},
	{name: "the turn ended with no reason", event: PreToolUse,
		prints: `{"continue": false}`,
		want:   expect{endTurn: true, stop: "~ended the turn and gave no reason", result: ResultEndedTurn}},
	{name: "the turn ended, and an approval beside it", event: PreToolUse,
		prints: `{"continue": false, "stopReason": "enough", "hookSpecificOutput": {"permissionDecision": "allow"}}`,
		want:   expect{endTurn: true, stop: "enough", decision: Allow, result: ResultEndedTurn}},
	{name: "continue and suppressOutput change nothing", event: PreToolUse,
		prints: `{"continue": true, "suppressOutput": true}`,
		want:   expect{}},
	{name: "an empty object", event: PreToolUse, prints: `{}`, want: expect{}},
	{name: "fields that are null", event: PreToolUse,
		prints: `{"decision": null, "reason": null, "continue": null, "systemMessage": null, "hookSpecificOutput": null}`,
		want:   expect{}},
	{name: "null inside hookSpecificOutput", event: PreToolUse,
		prints: `{"hookSpecificOutput": {"permissionDecision": null, "updatedInput": null, "additionalContext": null}}`,
		want:   expect{}},
	{name: "an answer over several lines", event: PreToolUse,
		prints: "\n\n  {\n    \"decision\": \"block\",\n    \"reason\": \"spread over lines\"\n  }\n\n",
		want:   expect{blocked: true, decision: Deny, reason: "spread over lines", result: ResultBlocked}},
	{name: "a terminal sequence is noted and not written", event: PreToolUse,
		prints: `{"terminalSequence": "(bell)"}`,
		want:   expect{notes: []string{"terminal sequence"}}},
	{name: "no output", event: PreToolUse, prints: ``, want: expect{}},
	{name: "plain words", event: PreToolUse, prints: "the linter is happy\n", want: expect{}},
	{name: "a JSON list is plain text", event: PreToolUse, prints: `[{"decision": "block"}]`, want: expect{}},
	{name: "a quoted string is plain text", event: PreToolUse, prints: `"block"`, want: expect{}},
	{name: "words on stderr beside an approval", event: PreToolUse,
		prints: `{"hookSpecificOutput": {"permissionDecision": "allow"}}`, after: "echo 'checked 3 rules' >&2",
		want: expect{decision: Allow, result: ResultApproved}},

	// ---- PreToolUse: answers that cannot be read. The call is blocked.
	{name: "Deny with a capital", event: PreToolUse,
		prints: `{"hookSpecificOutput": {"permissionDecision": "Deny", "permissionDecisionReason": "no"}}`,
		want:   expect{blocked: true, failed: true, decision: Deny, reason: `~"permissionDecision" is "Deny"; it must be exactly "allow", "deny" or "ask"`, result: ResultCouldNotEvaluate}},
	{name: "reject", event: PreToolUse,
		prints: `{"hookSpecificOutput": {"permissionDecision": "reject"}}`,
		want:   expect{blocked: true, failed: true, decision: Deny, reason: `~"permissionDecision" is "reject"`, result: ResultCouldNotEvaluate}},
	{name: "defer, which gnosh does not have", event: PreToolUse,
		prints: `{"hookSpecificOutput": {"permissionDecision": "defer"}}`,
		want:   expect{blocked: true, failed: true, decision: Deny, reason: `~"defer", which gnosh does not support`, result: ResultCouldNotEvaluate}},
	{name: "deny at the top level", event: PreToolUse,
		prints: `{"decision": "deny", "reason": "no"}`,
		want:   expect{blocked: true, failed: true, decision: Deny, reason: `~"decision" is "deny"; it must be exactly "block" or "approve"`, result: ResultCouldNotEvaluate}},
	{name: "allow at the top level", event: PreToolUse,
		prints: `{"decision": "allow"}`,
		want:   expect{blocked: true, failed: true, decision: Deny, reason: `~"decision" is "allow"`, result: ResultCouldNotEvaluate}},
	{name: "an empty decision", event: PreToolUse,
		prints: `{"decision": ""}`,
		want:   expect{blocked: true, failed: true, decision: Deny, reason: `~"decision" is ""`, result: ResultCouldNotEvaluate}},
	{name: "an approval with a changed input that is a string", event: PreToolUse,
		prints: `{"hookSpecificOutput": {"permissionDecision": "allow", "updatedInput": "ls -la"}}`,
		want:   expect{blocked: true, failed: true, decision: Deny, reason: `~"updatedInput" must be a JSON object holding the whole new input; this one is a string`, result: ResultCouldNotEvaluate}},
	{name: "a changed input that is a list", event: PreToolUse,
		prints: `{"hookSpecificOutput": {"updatedInput": ["ls", "-la"]}}`,
		want:   expect{blocked: true, failed: true, decision: Deny, reason: `~this one is a list`, result: ResultCouldNotEvaluate}},
	{name: "continue as a string", event: PreToolUse,
		prints: `{"continue": "false"}`,
		want:   expect{blocked: true, failed: true, decision: Deny, reason: `~"continue" must be true or false; this one is a string`, result: ResultCouldNotEvaluate}},
	{name: "a reason that is a number", event: PreToolUse,
		prints: `{"decision": "approve", "reason": 5}`,
		want:   expect{blocked: true, failed: true, decision: Deny, reason: `~"reason" must be a string; this one is a number`, result: ResultCouldNotEvaluate}},
	{name: "a system message that is an object", event: PreToolUse,
		prints: `{"systemMessage": {"text": "hi"}}`,
		want:   expect{blocked: true, failed: true, decision: Deny, reason: `~"systemMessage" must be a string; this one is an object`, result: ResultCouldNotEvaluate}},
	{name: "added text that is a number", event: PreToolUse,
		prints: `{"hookSpecificOutput": {"additionalContext": 5}}`,
		want:   expect{blocked: true, failed: true, decision: Deny, reason: `~"additionalContext" must be a string or a list of strings`, result: ResultCouldNotEvaluate}},
	{name: "added text that is a list of numbers", event: PreToolUse,
		prints: `{"hookSpecificOutput": {"additionalContext": ["a", 5]}}`,
		want:   expect{blocked: true, failed: true, decision: Deny, reason: `~"additionalContext" must be a string or a list of strings`, result: ResultCouldNotEvaluate}},
	{name: "a misspelt field at the top", event: PreToolUse,
		prints: `{"decison": "block", "reason": "typo"}`,
		want:   expect{blocked: true, failed: true, decision: Deny, reason: `~it has "decison", which is not a field of a hook's answer`, result: ResultCouldNotEvaluate}},
	{name: "a misspelt field inside", event: PreToolUse,
		prints: `{"hookSpecificOutput": {"permissionDecison": "deny"}}`,
		want:   expect{blocked: true, failed: true, decision: Deny, reason: `~"hookSpecificOutput" has "permissionDecison", which is not something a PreToolUse hook can say`, result: ResultCouldNotEvaluate}},
	{name: "a field of another event", event: PreToolUse,
		prints: `{"hookSpecificOutput": {"updatedToolOutput": {"stdout": ""}}}`,
		want:   expect{blocked: true, failed: true, decision: Deny, reason: `~"updatedToolOutput", which is not something a PreToolUse hook can say`, result: ResultCouldNotEvaluate}},
	{name: "an answer for another event", event: PreToolUse,
		prints: `{"hookSpecificOutput": {"hookEventName": "PostToolUse", "additionalContext": "x"}}`,
		want:   expect{blocked: true, failed: true, decision: Deny, context: "x", reason: `~is for the event "PostToolUse", but the hook was run for PreToolUse`, result: ResultCouldNotEvaluate}},
	{name: "a field written twice", event: PreToolUse,
		prints: `{"decision": "approve", "decision": "block"}`,
		want:   expect{blocked: true, failed: true, decision: Deny, reason: `~it has "decision" twice`, result: ResultCouldNotEvaluate}},
	{name: "a field written twice inside", event: PreToolUse,
		prints: `{"hookSpecificOutput": {"permissionDecision": "allow", "permissionDecision": "deny"}}`,
		want:   expect{blocked: true, failed: true, decision: Deny, reason: `~"hookSpecificOutput" must be a JSON object; this one has "permissionDecision" twice`, result: ResultCouldNotEvaluate}},
	{name: "braces around what is not JSON", event: PreToolUse,
		prints: `{the linter is happy}`,
		want:   expect{blocked: true, failed: true, decision: Deny, reason: `~it is not one JSON object`, result: ResultCouldNotEvaluate}},
	{name: "hookSpecificOutput that is a string", event: PreToolUse,
		prints: `{"hookSpecificOutput": "allow"}`,
		want:   expect{blocked: true, failed: true, decision: Deny, reason: `~"hookSpecificOutput" must be a JSON object; this one is a string`, result: ResultCouldNotEvaluate}},
	{name: "a deny beside a field that cannot be read: the deny holds", event: PreToolUse,
		prints: `{"hookSpecificOutput": {"permissionDecision": "deny", "permissionDecisionReason": "this file holds secrets", "updatedInput": "x"}}`,
		want:   expect{blocked: true, failed: true, decision: Deny, reason: "~The hook said: this file holds secrets", result: ResultCouldNotEvaluate}},
	{name: "a line of text before the answer", event: PreToolUse,
		prints: "Loading the profile...\n" + `{"hookSpecificOutput": {"permissionDecision": "deny", "permissionDecisionReason": "no"}}`,
		want:   expect{blocked: true, failed: true, decision: Deny, reason: "~it printed other text along with its JSON answer", result: ResultCouldNotEvaluate}},
	{name: "a line of text after the answer", event: PreToolUse,
		prints: `{"decision": "block", "reason": "no"}` + "\ndone\n",
		want:   expect{blocked: true, failed: true, decision: Deny, reason: "~it printed other text along with its JSON answer", result: ResultCouldNotEvaluate}},
	{name: "text and a JSON object that is no answer", event: PreToolUse,
		prints: "the event was:\n" + `{"tool_name": "Bash", "cwd": "/project"}`,
		want:   expect{}},

	// ---- PreToolUse: an exit status other than 0 can only make the
	// answer stricter.
	{name: "exit 1 and a deny: the deny holds", event: PreToolUse,
		prints: `{"hookSpecificOutput": {"permissionDecision": "deny", "permissionDecisionReason": "no, and I mean it"}}`, after: "exit 1",
		want: expect{blocked: true, decision: Deny, reason: "no, and I mean it", result: ResultBlocked}},
	{name: "exit 1 and an approval: the approval does not count", event: PreToolUse,
		prints: `{"hookSpecificOutput": {"permissionDecision": "allow"}}`, after: "exit 1",
		want: expect{notes: []string{"its approval was not used, because it exited with status 1"}}},
	{name: "exit 1 and an ask", event: PreToolUse,
		prints: `{"hookSpecificOutput": {"permissionDecision": "ask", "permissionDecisionReason": "sure?"}}`, after: "exit 1",
		want: expect{decision: Ask, reason: "sure?", result: ResultAsked}},
	{name: "exit 1 and a changed input", event: PreToolUse,
		prints: `{"hookSpecificOutput": {"updatedInput": {"command": "ls"}, "additionalContext": "narrowed"}}`, after: "exit 1",
		want: expect{input: `{"command":"ls"}`, context: "narrowed"}},
	{name: "exit 2 and an approval: the block stands", event: PreToolUse,
		prints: `{"hookSpecificOutput": {"permissionDecision": "allow"}}`, after: "echo 'blocked all the same' >&2; exit 2",
		want: expect{blocked: true, decision: Deny, reason: "blocked all the same", result: ResultBlocked}},
	{name: "exit 2 with its reason in the JSON", event: PreToolUse,
		prints: `{"decision": "block", "reason": "the reason is in here"}`, after: "exit 2",
		want: expect{blocked: true, decision: Deny, reason: "the reason is in here", result: ResultBlocked}},
	{name: "exit 2 with text and a warning beside it", event: PreToolUse,
		prints: `{"systemMessage": "look at the log", "hookSpecificOutput": {"additionalContext": "the log is in out/"}}`, after: "echo 'no' >&2; exit 2",
		want: expect{blocked: true, decision: Deny, reason: "no", context: "the log is in out/", system: []string{"look at the log"}, result: ResultBlocked}},
	{name: "exit 3 and a deny", event: PreToolUse,
		prints: `{"hookSpecificOutput": {"permissionDecision": "deny", "permissionDecisionReason": "and it is denied anyway"}}`, after: "exit 3",
		want: expect{blocked: true, failed: true, decision: Deny, reason: "~The hook said: and it is denied anyway", result: ResultCouldNotEvaluate}},
	{name: "exit 3 and an approval", event: PreToolUse,
		prints: `{"hookSpecificOutput": {"permissionDecision": "allow"}}`, after: "exit 3",
		want: expect{blocked: true, failed: true, decision: Deny, reason: "~status 3", result: ResultCouldNotEvaluate}},
	{name: "exit 1 with trouble on stderr and an approval", event: PreToolUse,
		prints: `{"hookSpecificOutput": {"permissionDecision": "allow"}}`, after: "echo 'KeyError: tool_input' >&2; exit 1",
		want: expect{blocked: true, failed: true, decision: Deny, reason: "~KeyError: tool_input", result: ResultCouldNotEvaluate}},
	{name: "exit 1 and an answer that cannot be read", event: PreToolUse,
		prints: `{"hookSpecificOutput": {"permissionDecision": "Allow"}}`, after: "exit 1",
		want: expect{blocked: true, failed: true, decision: Deny, reason: `~"permissionDecision" is "Allow"`, result: ResultCouldNotEvaluate}},

	// ---- UserPromptSubmit: plain text is text for the model.
	{name: "plain text", event: UserPromptSubmit,
		prints: "Today is Friday.\nThe build is red.\n",
		want:   expect{context: "Today is Friday.\nThe build is red."}},
	{name: "added text", event: UserPromptSubmit,
		prints: `{"hookSpecificOutput": {"hookEventName": "UserPromptSubmit", "additionalContext": "the build is red"}}`,
		want:   expect{context: "the build is red"}},
	{name: "block", event: UserPromptSubmit,
		prints: `{"decision": "block", "reason": "that looks like an API key"}`,
		want:   expect{blocked: true, reason: "that looks like an API key", result: ResultBlocked}},
	{name: "block with the prompt left out of the message", event: UserPromptSubmit,
		prints: `{"decision": "block", "reason": "a key", "hookSpecificOutput": {"suppressOriginalPrompt": true}}`,
		want:   expect{blocked: true, reason: "a key", result: ResultBlocked}},
	{name: "approve means nothing here", event: UserPromptSubmit, prints: `{"decision": "approve"}`, want: expect{}},
	{name: "a session title is noted and not set", event: UserPromptSubmit,
		prints: `{"hookSpecificOutput": {"sessionTitle": "Fixing the build", "additionalContext": "x"}}`,
		want:   expect{context: "x", notes: []string{"name the session"}}},
	{name: "plain text from a hook that blocks", event: UserPromptSubmit,
		prints: "half a thought", after: "echo 'no prompts on Sunday' >&2; exit 2",
		want: expect{blocked: true, reason: "no prompts on Sunday", result: ResultBlocked}},
	{name: "plain text from a test that came out false", event: UserPromptSubmit,
		prints: "half a thought", after: "exit 1", want: expect{}},
	{name: "a permission decision, which only a tool hook can give", event: UserPromptSubmit,
		prints: `{"hookSpecificOutput": {"permissionDecision": "deny", "permissionDecisionReason": "no"}}`,
		want:   expect{blocked: true, failed: true, reason: `~it gives "permissionDecision", which only a PreToolUse or PreModelSwitch hook can`, result: ResultCouldNotEvaluate}},
	{name: "text along with an answer", event: UserPromptSubmit,
		prints: "Context follows.\n" + `{"hookSpecificOutput": {"additionalContext": "x"}}`,
		want:   expect{blocked: true, failed: true, reason: "~other text along with its JSON answer", result: ResultCouldNotEvaluate}},
	{name: "ending the turn", event: UserPromptSubmit,
		prints: `{"continue": false, "stopReason": "not now"}`,
		want:   expect{endTurn: true, stop: "not now", result: ResultEndedTurn}},

	// ---- SessionStart: text for the model; it cannot block.
	{name: "plain text", event: SessionStart,
		prints: "The staging server is down today.",
		want:   expect{context: "The staging server is down today."}},
	{name: "added text", event: SessionStart,
		prints: `{"hookSpecificOutput": {"hookEventName": "SessionStart", "additionalContext": "the branch is main"}}`,
		want:   expect{context: "the branch is main"}},
	{name: "a block is only an objection", event: SessionStart,
		prints: `{"decision": "block", "reason": "not on a Sunday"}`,
		want:   expect{warnings: []string{"objected"}, result: ResultWarning}},
	{name: "a first message of its own, which gnosh does not send", event: SessionStart,
		prints: `{"hookSpecificOutput": {"initialUserMessage": "run the tests"}}`,
		want:   expect{failed: true, warnings: []string{"initialUserMessage"}, result: ResultCouldNotEvaluate}},
	{name: "what is noted and not done", event: SessionStart,
		prints: `{"hookSpecificOutput": {"sessionTitle": "Build", "watchPaths": ["/project/.env"], "reloadSkills": true, "additionalContext": "x"}}`,
		want:   expect{context: "x", notes: []string{"name the session", "skills to be loaded again"}}},
	{name: "ending the turn", event: SessionStart,
		prints: `{"continue": false, "stopReason": "the license has run out"}`,
		want:   expect{endTurn: true, stop: "the license has run out", result: ResultEndedTurn}},
	{name: "a warning for the user", event: SessionStart,
		prints: `{"systemMessage": "three todos are overdue"}`,
		want:   expect{system: []string{"three todos are overdue"}, result: ResultWarning}},

	// ---- PostModelSwitch: text for the model; it cannot undo the switch.
	{name: "plain text", event: PostModelSwitch,
		prints: "This model answers slowly: keep replies short.",
		want:   expect{context: "This model answers slowly: keep replies short."}},
	{name: "added text", event: PostModelSwitch,
		prints: `{"hookSpecificOutput": {"hookEventName": "PostModelSwitch", "additionalContext": "the new model knows Gno"}}`,
		want:   expect{context: "the new model knows Gno"}},
	{name: "a block is only an objection", event: PostModelSwitch,
		prints: `{"decision": "block", "reason": "too slow"}`,
		want:   expect{warnings: []string{"objected"}, result: ResultWarning}},

	// ---- PreModelSwitch: allow, deny or ask, as PreToolUse does; nothing
	// for the model.
	{name: "deny", event: PreModelSwitch,
		prints: `{"hookSpecificOutput": {"hookEventName": "PreModelSwitch", "permissionDecision": "deny", "permissionDecisionReason": "not approved here"}}`,
		want:   expect{blocked: true, decision: Deny, reason: "not approved here", result: ResultBlocked}},
	{name: "ask", event: PreModelSwitch,
		prints: `{"hookSpecificOutput": {"hookEventName": "PreModelSwitch", "permissionDecision": "ask", "permissionDecisionReason": "it sends 180k tokens again"}}`,
		want:   expect{decision: Ask, reason: "it sends 180k tokens again", result: ResultAsked}},
	{name: "allow", event: PreModelSwitch,
		prints: `{"hookSpecificOutput": {"hookEventName": "PreModelSwitch", "permissionDecision": "allow"}}`,
		want:   expect{decision: Allow, result: ResultApproved}},
	{name: "a top-level block", event: PreModelSwitch,
		prints: `{"decision": "block", "reason": "pinned to the small model"}`,
		want:   expect{blocked: true, decision: Deny, reason: "pinned to the small model", result: ResultBlocked}},
	{name: "a cost report", event: PreModelSwitch,
		prints: `{"systemMessage": "about $1.14 to fill the new cache"}`,
		want:   expect{system: []string{"about $1.14 to fill the new cache"}, result: ResultWarning}},
	{name: "no text for the model", event: PreModelSwitch,
		prints: `{"hookSpecificOutput": {"hookEventName": "PreModelSwitch", "additionalContext": "x"}}`,
		want:   expect{blocked: true, failed: true, decision: Deny, reason: "~not something a PreModelSwitch hook can say", result: ResultCouldNotEvaluate}},
	{name: "plain words are not text for the model", event: PreModelSwitch, prints: "fine by me", want: expect{}},

	// ---- PostToolBatch: text for the model after the batch; a block or
	// "continue": false ends the turn before the next request.
	{name: "added text", event: PostToolBatch,
		prints: `{"hookSpecificOutput": {"hookEventName": "PostToolBatch", "additionalContext": "the suite ran: 2 failures"}}`,
		want:   expect{context: "the suite ran: 2 failures"}},
	{name: "block", event: PostToolBatch,
		prints: `{"decision": "block", "reason": "stop and fix the build"}`,
		want:   expect{blocked: true, reason: "stop and fix the build", result: ResultBlocked}},
	{name: "continue false", event: PostToolBatch,
		prints: `{"continue": false, "stopReason": "enough for today"}`,
		want:   expect{endTurn: true, stop: "enough for today", result: ResultEndedTurn}},
	{name: "plain words are not text for the model", event: PostToolBatch, prints: "ran the suite", want: expect{}},

	// ---- FileChanged: a message is shown; nothing can be stopped.
	{name: "a message", event: FileChanged,
		prints: `{"systemMessage": ".envrc changed: direnv allow ran"}`,
		want:   expect{system: []string{".envrc changed: direnv allow ran"}, result: ResultWarning}},
	{name: "a block is only an objection", event: FileChanged,
		prints: `{"decision": "block", "reason": "no"}`,
		want:   expect{warnings: []string{"objected"}, result: ResultWarning}},
	{name: "a relative path to watch", event: FileChanged,
		prints: `{"hookSpecificOutput": {"hookEventName": "FileChanged", "watchPaths": ["rel/.env"]}}`,
		want:   expect{failed: true, warnings: []string{"not an absolute path"}, result: ResultCouldNotEvaluate}},

	// ---- Elicitation: an answer in the person's place; a decline in any
	// form wins.
	{name: "accept", event: Elicitation,
		prints: `{"hookSpecificOutput": {"hookEventName": "Elicitation", "action": "accept", "content": {"project_key": "DOCS"}}}`,
		want:   expect{action: "accept", content: `{"project_key":"DOCS"}`, result: ResultAnswered}},
	{name: "cancel", event: Elicitation,
		prints: `{"hookSpecificOutput": {"hookEventName": "Elicitation", "action": "cancel"}}`,
		want:   expect{action: "cancel", result: ResultAnswered}},
	{name: "decline", event: Elicitation,
		prints: `{"hookSpecificOutput": {"hookEventName": "Elicitation", "action": "decline"}}`,
		want:   expect{blocked: true, action: "decline", reason: "~blocked this and gave no reason", result: ResultBlocked}},
	{name: "a block wins over an accept", event: Elicitation,
		prints: `{"decision": "block", "reason": "not from this server", "hookSpecificOutput": {"hookEventName": "Elicitation", "action": "accept", "content": {}}}`,
		want:   expect{blocked: true, action: "decline", reason: "not from this server", result: ResultBlocked}},
	{name: "an action not known", event: Elicitation,
		prints: `{"hookSpecificOutput": {"hookEventName": "Elicitation", "action": "approve"}}`,
		want:   expect{blocked: true, failed: true, action: "decline", reason: "~must be exactly", result: ResultCouldNotEvaluate}},
	{name: "values without an accept", event: Elicitation,
		prints: `{"hookSpecificOutput": {"hookEventName": "Elicitation", "content": {"project_key": "DOCS"}}}`,
		want:   expect{notes: []string{"without \"action\": \"accept\""}}},
	{name: "plain words are not read", event: Elicitation, prints: "fine by me", want: expect{}},

	// ---- ElicitationResult: another answer in place of the person's.
	{name: "other values", event: ElicitationResult,
		prints: `{"hookSpecificOutput": {"hookEventName": "ElicitationResult", "action": "accept", "content": {"project_key": "WEB-2"}}}`,
		want:   expect{action: "accept", content: `{"project_key":"WEB-2"}`, result: ResultAnswered}},
	{name: "block", event: ElicitationResult,
		prints: `{"decision": "block", "reason": "no keys to this server"}`,
		want:   expect{blocked: true, action: "decline", reason: "no keys to this server", result: ResultBlocked}},
	{name: "nothing for the model", event: ElicitationResult,
		prints: `{"hookSpecificOutput": {"hookEventName": "ElicitationResult", "additionalContext": "x"}}`,
		want:   expect{blocked: true, failed: true, action: "decline", reason: "~not something a ElicitationResult hook can say", result: ResultCouldNotEvaluate}},

	// ---- ConfigChange: a block keeps the settings the session had.
	{name: "block", event: ConfigChange,
		prints: `{"decision": "block", "reason": "project settings change only with review"}`,
		want:   expect{blocked: true, reason: "project settings change only with review", result: ResultBlocked}},
	{name: "plain words are not read", event: ConfigChange, prints: "logged", want: expect{}},
	{name: "nothing for the model", event: ConfigChange,
		prints: `{"hookSpecificOutput": {"hookEventName": "ConfigChange", "additionalContext": "x"}}`,
		want:   expect{blocked: true, failed: true, reason: "~not something a ConfigChange hook can say", result: ResultCouldNotEvaluate}},

	// ---- DirectoryAdded: a message, which goes to the model.
	{name: "a message", event: DirectoryAdded,
		prints: `{"systemMessage": "the docs are in docs/"}`,
		want:   expect{system: []string{"the docs are in docs/"}, result: ResultWarning}},
	{name: "plain words are not read", event: DirectoryAdded, prints: "the docs are in docs/", want: expect{}},

	// ---- UserPromptExpansion: like UserPromptSubmit, for the prompt a
	// typed /name expands into.
	{name: "plain text", event: UserPromptExpansion,
		prints: "Staging is frozen until noon.\n",
		want:   expect{context: "Staging is frozen until noon."}},
	{name: "added text", event: UserPromptExpansion,
		prints: `{"hookSpecificOutput": {"hookEventName": "UserPromptExpansion", "additionalContext": "use the blue cluster"}}`,
		want:   expect{context: "use the blue cluster"}},
	{name: "block", event: UserPromptExpansion,
		prints: `{"decision": "block", "reason": "no deploys on Friday"}`,
		want:   expect{blocked: true, reason: "no deploys on Friday", result: ResultBlocked}},

	// ---- CwdChanged: a message is shown; nothing else is read.
	{name: "a message", event: CwdChanged,
		prints: `{"systemMessage": "the sub folder has its own .env"}`,
		want:   expect{system: []string{"the sub folder has its own .env"}, result: ResultWarning}},
	{name: "plain words are not read", event: CwdChanged, prints: "nothing", want: expect{}},

	// ---- Stop and SubagentStop: a block keeps the turn going.
	{name: "block", event: Stop,
		prints: `{"decision": "block", "reason": "out/final.mp4 is not there"}`,
		want:   expect{blocked: true, reason: "out/final.mp4 is not there", result: ResultBlocked}},
	{name: "block with no reason", event: Stop,
		prints: `{"decision": "block"}`,
		want:   expect{blocked: true, reason: "~blocked this and gave no reason", result: ResultBlocked}},
	{name: "approve", event: Stop, prints: `{"decision": "approve", "reason": "the tests pass"}`, want: expect{}},
	{name: "added text keeps the turn going without a block", event: Stop,
		prints: `{"hookSpecificOutput": {"hookEventName": "Stop", "additionalContext": "the changelog is not updated"}}`,
		want:   expect{context: "the changelog is not updated"}},
	{name: "plain words are not read", event: Stop, prints: "the tests pass", want: expect{}},
	{name: "a tool hook's reason with nothing to go with it", event: Stop,
		prints: `{"hookSpecificOutput": {"permissionDecisionReason": "because"}}`,
		want:   expect{blocked: true, failed: true, reason: `~it gives "permissionDecisionReason", which only a PreToolUse or PreModelSwitch hook can`, result: ResultCouldNotEvaluate}},
	{name: "ending the turn beats its own block", event: Stop,
		prints: `{"continue": false, "stopReason": "stop for today", "decision": "block", "reason": "not done"}`,
		want:   expect{blocked: true, reason: "not done", endTurn: true, stop: "stop for today", result: ResultEndedTurn}},
	{name: "block", event: SubagentStop,
		prints: `{"decision": "block", "reason": "name the file you found it in"}`,
		want:   expect{blocked: true, reason: "name the file you found it in", result: ResultBlocked}},
	{name: "added text", event: SubagentStop,
		prints: `{"hookSpecificOutput": {"hookEventName": "SubagentStop", "additionalContext": "cite line numbers"}}`,
		want:   expect{context: "cite line numbers"}},
	{name: "an answer written for Stop", event: SubagentStop,
		prints: `{"hookSpecificOutput": {"hookEventName": "Stop", "additionalContext": "x"}}`,
		want:   expect{blocked: true, failed: true, context: "x", reason: `~is for the event "Stop", but the hook was run for SubagentStop`, result: ResultCouldNotEvaluate}},

	// ---- PostToolUse and PostToolUseFailure: a block is feedback.
	{name: "block", event: PostToolUse,
		prints: `{"decision": "block", "reason": "the file no longer compiles"}`,
		want:   expect{blocked: true, reason: "the file no longer compiles", result: ResultFeedback}},
	{name: "added text", event: PostToolUse,
		prints: `{"hookSpecificOutput": {"hookEventName": "PostToolUse", "additionalContext": "gofmt changed the file"}}`,
		want:   expect{context: "gofmt changed the file"}},
	{name: "a replaced tool result", event: PostToolUse,
		prints: `{"hookSpecificOutput": {"updatedToolOutput": {"stdout": "[redacted]", "stderr": "", "interrupted": false, "isImage": false}}}`,
		want:   expect{output: `{"stdout":"[redacted]","stderr":"","interrupted":false,"isImage":false}`}},
	{name: "a replaced MCP tool result", event: PostToolUse,
		prints: `{"hookSpecificOutput": {"updatedMCPToolOutput": "x"}}`,
		want:   expect{output: `"x"`}},
	{name: "two different replacements in one answer", event: PostToolUse,
		prints: `{"hookSpecificOutput": {"updatedToolOutput": "a", "updatedMCPToolOutput": "b"}}`,
		want:   expect{blocked: true, failed: true, reason: `~only one can stand in for the tool's output`, output: `"a"`, result: ResultCouldNotEvaluate}},
	{name: "added text", event: PostToolUseFailure,
		prints: `{"hookSpecificOutput": {"hookEventName": "PostToolUseFailure", "additionalContext": "the test server is on port 8081"}}`,
		want:   expect{context: "the test server is on port 8081"}},
	{name: "block", event: PostToolUseFailure,
		prints: `{"decision": "block", "reason": "do not retry this"}`,
		want:   expect{blocked: true, reason: "do not retry this", result: ResultFeedback}},
	{name: "a permission decision after the call has run", event: PostToolUse,
		prints: `{"hookSpecificOutput": {"permissionDecision": "allow"}}`,
		want:   expect{blocked: true, failed: true, reason: "~only a PreToolUse or PreModelSwitch hook can", result: ResultCouldNotEvaluate}},

	// ---- PermissionRequest: allow or deny in the user's place.
	{name: "allow", event: PermissionRequest,
		prints: `{"hookSpecificOutput": {"hookEventName": "PermissionRequest", "decision": {"behavior": "allow"}}}`,
		want:   expect{decision: Allow, result: ResultApproved}},
	{name: "deny", event: PermissionRequest,
		prints: `{"hookSpecificOutput": {"hookEventName": "PermissionRequest", "decision": {"behavior": "deny", "message": "not in this repository"}}}`,
		want:   expect{decision: Deny, reason: "not in this repository", result: ResultBlocked}},
	{name: "deny with no message", event: PermissionRequest,
		prints: `{"hookSpecificOutput": {"decision": {"behavior": "deny"}}}`,
		want:   expect{decision: Deny, reason: "~blocked this and gave no reason", result: ResultBlocked}},
	{name: "deny and end the turn", event: PermissionRequest,
		prints: `{"hookSpecificOutput": {"decision": {"behavior": "deny", "message": "stop asking", "interrupt": true}}}`,
		want:   expect{decision: Deny, reason: "stop asking", endTurn: true, stop: "stop asking", result: ResultEndedTurn}},
	{name: "deny, and interrupt false", event: PermissionRequest,
		prints: `{"hookSpecificOutput": {"decision": {"behavior": "deny", "message": "no", "interrupt": false}}}`,
		want:   expect{decision: Deny, reason: "no", result: ResultBlocked}},
	{name: "allow with a changed input: the question is shown", event: PermissionRequest,
		prints: `{"hookSpecificOutput": {"decision": {"behavior": "allow", "updatedInput": {"command": "ls"}}}}`,
		want:   expect{warnings: []string{"changed input or changed permissions, which gnosh does not apply"}, notes: []string{"the question is shown instead"}}},
	{name: "allow with changed permissions: the question is shown", event: PermissionRequest,
		prints: `{"hookSpecificOutput": {"decision": {"behavior": "allow", "updatedPermissions": [{"type": "setMode", "mode": "acceptEdits", "destination": "session"}]}}}`,
		want:   expect{warnings: []string{"did not answer the permission question"}, notes: []string{"changed permissions"}}},
	{name: "deny with a changed input: the deny holds", event: PermissionRequest,
		prints: `{"hookSpecificOutput": {"decision": {"behavior": "deny", "message": "no", "updatedInput": {"command": "ls"}}}}`,
		want:   expect{decision: Deny, reason: "no", result: ResultBlocked}},
	{name: "Deny with a capital is no answer", event: PermissionRequest,
		prints: `{"hookSpecificOutput": {"decision": {"behavior": "Deny"}}}`,
		want:   expect{failed: true, warnings: []string{`"behavior" is "Deny"; it must be exactly "allow" or "deny"`}, result: ResultCouldNotEvaluate}},
	{name: "ask is not an answer to a question", event: PermissionRequest,
		prints: `{"hookSpecificOutput": {"decision": {"behavior": "ask"}}}`,
		want:   expect{failed: true, warnings: []string{`"behavior" is "ask"`}, result: ResultCouldNotEvaluate}},
	{name: "a decision with no behavior", event: PermissionRequest,
		prints: `{"hookSpecificOutput": {"decision": {"message": "hm"}}}`,
		want:   expect{failed: true, warnings: []string{`"decision" has no "behavior"`}, result: ResultCouldNotEvaluate}},
	{name: "a decision that is a word", event: PermissionRequest,
		prints: `{"hookSpecificOutput": {"decision": "allow"}}`,
		want:   expect{failed: true, warnings: []string{`"decision" must be a JSON object such as {"behavior": "allow"}; this one is a string`}, result: ResultCouldNotEvaluate}},
	{name: "a changed input that is not an object", event: PermissionRequest,
		prints: `{"hookSpecificOutput": {"decision": {"behavior": "allow", "updatedInput": "ls"}}}`,
		want:   expect{failed: true, warnings: []string{`"updatedInput" must be a JSON object holding the whole new input; this one is a string`}, result: ResultCouldNotEvaluate}},
	{name: "changed permissions that are not a list", event: PermissionRequest,
		prints: `{"hookSpecificOutput": {"decision": {"behavior": "allow", "updatedPermissions": {"type": "setMode"}}}}`,
		want:   expect{failed: true, warnings: []string{`"updatedPermissions" must be a list; this one is an object`}, result: ResultCouldNotEvaluate}},
	{name: "null inside the decision", event: PermissionRequest,
		prints: `{"hookSpecificOutput": {"decision": {"behavior": "deny", "message": null, "interrupt": null, "updatedInput": null}}}`,
		want:   expect{decision: Deny, reason: "~blocked this and gave no reason", result: ResultBlocked}},
	{name: "a field the decision does not have", event: PermissionRequest,
		prints: `{"hookSpecificOutput": {"decision": {"behavior": "allow", "remember": true}}}`,
		want:   expect{failed: true, warnings: []string{`"decision" has "remember"`}, result: ResultCouldNotEvaluate}},
	{name: "a block at the top level: the refusal holds", event: PermissionRequest,
		prints: `{"decision": "block", "reason": "never this command"}`,
		want:   expect{failed: true, decision: Deny, reason: "never this command", warnings: []string{"Its refusal holds."}, result: ResultCouldNotEvaluate}},
	{name: "a deny in a tool hook's form: the refusal holds", event: PermissionRequest,
		prints: `{"hookSpecificOutput": {"permissionDecision": "deny", "permissionDecisionReason": "never this command"}}`,
		want:   expect{failed: true, decision: Deny, reason: "never this command", warnings: []string{"only a PreToolUse or PreModelSwitch hook can"}, result: ResultCouldNotEvaluate}},
	{name: "an allow in a tool hook's form is no answer", event: PermissionRequest,
		prints: `{"hookSpecificOutput": {"permissionDecision": "allow"}}`,
		want:   expect{failed: true, warnings: []string{"did not answer the permission question"}, result: ResultCouldNotEvaluate}},
	{name: "nothing to say", event: PermissionRequest, prints: `{}`, want: expect{}},
	{name: "no output", event: PermissionRequest, prints: ``, want: expect{}},
	{name: "allow, then exit 1", event: PermissionRequest,
		prints: `{"hookSpecificOutput": {"decision": {"behavior": "allow"}}}`, after: "exit 1",
		want: expect{warnings: []string{"its approval was not used, because it exited with status 1"}, notes: []string{"approval was not used"}}},
	{name: "deny, then exit 2", event: PermissionRequest,
		prints: `{"hookSpecificOutput": {"decision": {"behavior": "deny", "message": "no"}}}`, after: "exit 2",
		want: expect{decision: Deny, reason: "no", result: ResultBlocked, notes: []string{"status 2"}}},

	// ---- PreCompact, PostCompact, SubagentStart: nothing can be blocked.
	{name: "plain text is extra instructions for the summary", event: PreCompact,
		prints: "Keep every file name that was edited.",
		want:   expect{context: "Keep every file name that was edited."}},
	{name: "a block is only an objection", event: PreCompact,
		prints: `{"decision": "block", "reason": "not in the middle of a rebase"}`,
		want:   expect{warnings: []string{"not in the middle of a rebase"}, result: ResultWarning}},
	{name: "it cannot end the turn", event: PreCompact,
		prints: `{"continue": false, "stopReason": "no"}`,
		want:   expect{notes: []string{`"continue": false, which a PreCompact hook cannot use to end the turn`}}},
	{name: "a warning for the user", event: PreCompact,
		prints: `{"systemMessage": "the summary will take a while"}`,
		want:   expect{system: []string{"the summary will take a while"}, result: ResultWarning}},
	{name: "added text is not something it can say", event: PreCompact,
		prints: `{"hookSpecificOutput": {"hookEventName": "PreCompact", "additionalContext": "x"}}`,
		want:   expect{failed: true, warnings: []string{`"hookSpecificOutput" has "additionalContext", which is not something a PreCompact hook can say`}, result: ResultCouldNotEvaluate}},
	{name: "plain words are not read", event: PostCompact, prints: "saved", want: expect{}},
	{name: "a block is only an objection", event: PostCompact,
		prints: `{"decision": "block", "reason": "the summary lost the file names"}`,
		want:   expect{warnings: []string{"the summary lost the file names"}, result: ResultWarning}},
	{name: "ending the turn", event: PostCompact,
		prints: `{"continue": false, "stopReason": "review the summary first"}`,
		want:   expect{endTurn: true, stop: "review the summary first", result: ResultEndedTurn}},
	{name: "added text", event: SubagentStart,
		prints: `{"hookSpecificOutput": {"hookEventName": "SubagentStart", "additionalContext": "search only under src/"}}`,
		want:   expect{context: "search only under src/"}},
	{name: "plain words are not read", event: SubagentStart, prints: "search only under src/", want: expect{}},
}

// Every form of JSON answer, on every event that reads one.
func TestAnswers(t *testing.T) {
	for _, tt := range answerForms {
		t.Run(string(tt.event)+"/"+tt.name, func(t *testing.T) {
			t.Parallel()
			command := prints(t, tt.prints)
			if tt.after != "" {
				command += "; " + tt.after
			}
			check(t, one(t, tt.event, command), tt.want)
		})
	}
	// Every event whose hooks' output is read has its forms above.
	covered := map[Event]bool{}
	for _, tt := range answerForms {
		covered[tt.event] = true
	}
	for _, e := range Events() {
		if info, _ := describe(e); info.reads && !covered[e] {
			t.Errorf("no answers tested for %s", e)
		}
	}
}

// What a hook says is passed on at a bounded length, with its start and
// its end kept.
func TestLongAnswersAreClipped(t *testing.T) {
	opts := options(t)
	opts.MaxText = 200
	long := strings.Repeat("0123456789", 100) // 1,000 characters
	r := newRunner(t, opts,
		Hook{Event: UserPromptSubmit, Command: prints(t, "START "+long+" END")},
		Hook{Event: Stop, Command: prints(t, `{"decision": "block", "reason": "FIRST `+long+` LAST", "systemMessage": "WARN `+long+` DONE"}`)},
	)
	for _, tt := range []struct {
		event Event
		text  func(Outcome) string
		parts []string
	}{
		{UserPromptSubmit, func(o Outcome) string { return o.AdditionalContext }, []string{"START ", " END"}},
		{Stop, func(o Outcome) string { return o.Reason }, []string{"FIRST ", " LAST"}},
		{Stop, func(o Outcome) string { return o.SystemMessages[0] }, []string{"WARN ", " DONE"}},
	} {
		got := tt.text(run(t, r, input(tt.event)))
		contains(t, "the clipped text", got, append(tt.parts, "characters left out by gnosh")...)
		if n := len([]rune(got)); n > 260 {
			t.Errorf("%s: the text is %d characters long, want about 200:\n%s", tt.event, n, got)
		}
	}
}

// watchPaths, from SessionStart, CwdChanged and FileChanged hooks, are the
// files to watch from then on: every hook's, once each; an empty list
// clears them, and a hook that says nothing of them leaves them.
func TestWatchPaths(t *testing.T) {
	for _, event := range []Event{SessionStart, CwdChanged, FileChanged} {
		r := newRunner(t, options(t),
			Hook{Event: event, Command: `echo '{"hookSpecificOutput": {"hookEventName": "` + string(event) + `", "watchPaths": ["/p/.env", "/p/.envrc"]}}'`},
			Hook{Event: event, Command: `echo '{"hookSpecificOutput": {"hookEventName": "` + string(event) + `", "watchPaths": ["/p/.env", "/q/x"]}}'`},
		)
		out := run(t, r, input(event))
		if !out.WatchSet || strings.Join(out.WatchPaths, " ") != "/p/.env /p/.envrc /q/x" {
			t.Errorf("%s: WatchSet %v, WatchPaths %q", event, out.WatchSet, out.WatchPaths)
		}
	}
	r := newRunner(t, options(t), Hook{Event: CwdChanged, Command: `echo '{"hookSpecificOutput": {"hookEventName": "CwdChanged", "watchPaths": []}}'`})
	if out := run(t, r, input(CwdChanged)); !out.WatchSet || len(out.WatchPaths) != 0 {
		t.Errorf("an empty list: WatchSet %v, WatchPaths %q", out.WatchSet, out.WatchPaths)
	}
	r = newRunner(t, options(t), Hook{Event: CwdChanged, Command: `echo '{"systemMessage": "moved"}'`})
	if out := run(t, r, input(CwdChanged)); out.WatchSet {
		t.Errorf("a hook that named no files set the list: %q", out.WatchPaths)
	}
}
