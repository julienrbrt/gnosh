package hooks

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// exits is the table of what a hook's exit status means. blocks says
// whether the hook blocks on an event that can block; failed, whether
// that is because it could not evaluate; reason, what the reason must say.
var exits = []struct {
	name    string
	command string
	blocks  bool
	failed  bool
	reason  []string
	exit    int // the status the record must show
}{
	{"exit 0", "exit 0", false, false, nil, 0},
	{"exit 0 with words on stderr", "echo 'just a log line' >&2; exit 0", false, false, nil, 0},
	{"exit 2 with a reason", "echo 'force pushes are not allowed' >&2; exit 2", true, false, []string{"force pushes are not allowed"}, 2},
	{"exit 2 with nothing said", "exit 2", true, false, []string{"blocked this and gave no reason"}, 2},
	{"exit 3 with a reason", "echo 'ffprobe is not installed, so the audio was not checked' >&2; exit 3", true, true, []string{"status 3", "could not evaluate", "ffprobe is not installed, so the audio was not checked"}, 3},
	{"exit 3 with nothing said", "exit 3", true, true, []string{"status 3"}, 3},
	{"exit 126", "exit 126", true, true, []string{"status 126", "could not be run"}, 126},
	{"exit 127", "exit 127", true, true, []string{"status 127", "was not found"}, 127},
	{"a command inside that is missing", "no-such-command-anywhere --check", true, true, []string{"status 127", "no-such-command-anywhere"}, 127},
	{"exit 128 with nothing said", "exit 128", false, false, nil, 128},
	{"exit 129", "exit 129", true, true, []string{"status 129", "killed"}, 129},
	{"exit 137", "exit 137", true, true, []string{"status 137", "signal 9"}, 137},
	{"exit 255", "exit 255", true, true, []string{"status 255"}, 255},
	{"exit 1 with nothing said", "exit 1", false, false, nil, 1},
	{"a test that comes out false", `[ "$GNOSH_HOOK_EVENT_NAME" = NoSuchEvent ] && echo formatted`, false, false, nil, 1},
	{"exit 1 with a reason", "echo 'jq: error: cannot parse the input' >&2; exit 1", true, true, []string{"status 1", "reported trouble", "jq: error: cannot parse the input"}, 1},
	{"exit 1 with only blank lines on stderr", "printf '\\n  \\n' >&2; exit 1", false, false, nil, 1},
	{"exit 42 with nothing said", "exit 42", false, false, nil, 42},
	{"exit 42 with a reason", "echo 'Traceback (most recent call last)' >&2; exit 42", true, true, []string{"status 42", "Traceback (most recent call last)"}, 42},
	{"killed by a signal", "kill -KILL $$", true, true, []string{"killed by signal 9"}, -1},
	{"ended by a signal of its own", "kill -TERM $$", true, true, []string{"killed by signal 15"}, -1},
}

// Every exit status on every event that can block.
func TestExitStatusOnEveryBlockingEvent(t *testing.T) {
	for _, event := range []Event{PreToolUse, UserPromptSubmit, UserPromptExpansion, Stop, SubagentStop, PreModelSwitch, ConfigChange, PostToolBatch, Elicitation, ElicitationResult} {
		for _, tt := range exits {
			t.Run(string(event)+"/"+tt.name, func(t *testing.T) {
				t.Parallel()
				out := one(t, event, tt.command)
				if out.Blocked != tt.blocks || out.CouldNotEvaluate != tt.failed {
					t.Fatalf("Blocked %v, CouldNotEvaluate %v; want %v, %v\nReason: %s", out.Blocked, out.CouldNotEvaluate, tt.blocks, tt.failed, out.Reason)
				}
				contains(t, "the reason", out.Reason, tt.reason...)
				if !tt.blocks && out.Reason != "" {
					t.Errorf("nothing was blocked, yet there is a reason: %q", out.Reason)
				}
				if tt.failed {
					contains(t, "the reason", out.Reason, "The "+string(event)+" hook `"+tt.command+"` could not check this", "A check that could not run does not pass.")
				} else {
					lacks(t, "the reason", out.Reason, "could not check")
				}
				wantDecision := NoDecision
				if (event == PreToolUse || event == PreModelSwitch) && tt.blocks {
					wantDecision = Deny
				}
				if out.PermissionDecision != wantDecision {
					t.Errorf("PermissionDecision = %q, want %q", out.PermissionDecision, wantDecision)
				}
				if out.EndTurn || out.AdditionalContext != "" || len(out.Warnings) != 0 || len(out.SystemMessages) != 0 || out.UpdatedInput != nil {
					t.Errorf("the Outcome says more than the exit status did: %+v", out)
				}

				if len(out.Records) != 1 {
					t.Fatalf("got %d records, want 1", len(out.Records))
				}
				rec := out.Records[0]
				wantResult := ResultNone
				switch {
				case tt.failed:
					wantResult = ResultCouldNotEvaluate
				case tt.blocks:
					wantResult = ResultBlocked
				}
				if rec.Result != wantResult || rec.ExitCode != tt.exit {
					t.Errorf("record: Result %q, ExitCode %d; want %q, %d", rec.Result, rec.ExitCode, wantResult, tt.exit)
				}
				if rec.Event != event || rec.Command != tt.command || rec.Duration <= 0 {
					t.Errorf("record: Event %q, Command %q, Duration %v", rec.Event, rec.Command, rec.Duration)
				}
			})
		}
	}
}

// cannot is every way a hook can fail to evaluate. Each makes the hook
// that fails; says is what the hook's record must say about it. reads
// marks the ways that depend on what the hook printed, which only count
// on events whose hooks' output is read.
var cannot = []struct {
	name  string
	reads bool
	hook  func(t *testing.T, opts *Options) Hook
	says  []string
}{
	{
		name: "it exited 3",
		hook: func(t *testing.T, _ *Options) Hook {
			return Hook{Command: "echo 'the checker is not installed' >&2; exit 3"}
		},
		says: []string{"status 3", "the checker is not installed"},
	},
	{
		name: "it never started",
		hook: func(t *testing.T, opts *Options) Hook {
			opts.Shell = filepath.Join(t.TempDir(), "no-such-shell")
			return Hook{Command: "exit 0"}
		},
		says: []string{"could not be started", "no-such-shell"},
	},
	{
		name: "its script is not there",
		hook: func(t *testing.T, _ *Options) Hook {
			return Hook{Command: shq(filepath.Join(t.TempDir(), "missing-hook.sh"))}
		},
		says: []string{"status 127", "missing-hook.sh"},
	},
	{
		name: "its script is not executable",
		hook: func(t *testing.T, _ *Options) Hook {
			path := filepath.Join(t.TempDir(), "plain-file.sh")
			if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			return Hook{Command: shq(path)}
		},
		says: []string{"status 126", "plain-file.sh"},
	},
	{
		name: "it was killed by a signal",
		hook: func(t *testing.T, _ *Options) Hook { return Hook{Command: "kill -KILL $$"} },
		says: []string{"killed by signal 9"},
	},
	{
		name: "it passed its timeout",
		hook: func(t *testing.T, _ *Options) Hook {
			// (Long enough for a shell to start and write its line on a
			// box busy with every package's tests at once.)
			return Hook{Command: "echo 'still checking' >&2; sleep 600", Timeout: 2 * time.Second}
		},
		says: []string{"did not finish within 2 seconds", "was stopped", "still checking"},
	},
	{
		name: "it exited 1 and said why",
		hook: func(t *testing.T, _ *Options) Hook {
			return Hook{Command: "echo 'cannot open the database' >&2; exit 1"}
		},
		says: []string{"status 1", "cannot open the database"},
	},
	{
		name:  "a decision word gnosh does not know",
		reads: true,
		hook: func(t *testing.T, _ *Options) Hook {
			return Hook{Command: prints(t, `{"decision": "Block", "reason": "not like this"}`)}
		},
		says: []string{"answer could not be read", `"decision" is "Block"`, `exactly "block" or "approve"`},
	},
	{
		name:  "a changed input that is not an object",
		reads: true,
		hook: func(t *testing.T, _ *Options) Hook {
			return Hook{Command: prints(t, `{"hookSpecificOutput": {"updatedInput": "ls -la"}}`)}
		},
		says: []string{"answer could not be read", "updatedInput"},
	},
	{
		name:  "a field of the wrong type",
		reads: true,
		hook: func(t *testing.T, _ *Options) Hook {
			return Hook{Command: prints(t, `{"continue": "false", "systemMessage": "stop here"}`)}
		},
		says: []string{"answer could not be read", `"continue" must be true or false`, "a string"},
	},
	{
		name:  "an answer that is not JSON after all",
		reads: true,
		hook: func(t *testing.T, _ *Options) Hook {
			return Hook{Command: prints(t, `{"decision": "block", }`)}
		},
		says: []string{"answer could not be read", "not one JSON object"},
	},
}

// The table of what happens when a hook could not evaluate, for every
// event and every way of failing: it blocks wherever a block counts, it
// is a warning where nothing can be blocked, and it is never shown as a
// check that passed.
func TestCouldNotEvaluateOnEveryEvent(t *testing.T) {
	for _, event := range Events() {
		info, _ := describe(event)
		for _, c := range cannot {
			t.Run(string(event)+"/"+c.name, func(t *testing.T) {
				t.Parallel()
				opts := options(t)
				h := c.hook(t, &opts)
				h.Event = event
				h.Name = "audio-check"
				out := run(t, newRunner(t, opts, h), input(event))
				if len(out.Records) != 1 {
					t.Fatalf("got %d records, want 1", len(out.Records))
				}
				rec := out.Records[0]

				if c.reads && !info.reads {
					// What this event's hooks print is not read, so
					// there is nothing here that could not be read.
					if out.CouldNotEvaluate || rec.Result != ResultNone || out.Blocked || len(out.Warnings) != 0 {
						t.Errorf("the hook's output was judged on an event that does not read it: %+v", out)
					}
					return
				}

				if !out.CouldNotEvaluate || rec.Result != ResultCouldNotEvaluate {
					t.Fatalf("CouldNotEvaluate %v, record %q; the hook must never read as fine\n%+v", out.CouldNotEvaluate, rec.Result, out)
				}
				contains(t, "the record", rec.Reason, c.says...)
				if out.EndTurn || out.AdditionalContext != "" || out.UpdatedInput != nil {
					t.Errorf("the Outcome says more than a hook that could not evaluate can: %+v", out)
				}

				sentence := []string{"The " + string(event) + ` hook "audio-check" could not check this`, "A check that could not run does not pass."}
				switch event {
				case PreToolUse, PreModelSwitch:
					// The call (or the switch) is blocked, and the model
					// (or the person) reads why.
					if !out.Blocked || out.PermissionDecision != Deny {
						t.Errorf("Blocked %v, PermissionDecision %q; want the call blocked", out.Blocked, out.PermissionDecision)
					}
					contains(t, "the reason", out.Reason, append(sentence, c.says...)...)
				case PostToolUse, PostToolUseFailure, UserPromptSubmit, UserPromptExpansion, Stop, SubagentStop, ConfigChange, PostToolBatch, Elicitation, ElicitationResult:
					// The reason is feedback for the model; the prompt is
					// blocked; the turn is held open; the settings are
					// kept; the MCP server's request is declined.
					if !out.Blocked || out.PermissionDecision != NoDecision {
						t.Errorf("Blocked %v, PermissionDecision %q; want blocked", out.Blocked, out.PermissionDecision)
					}
					contains(t, "the reason", out.Reason, append(sentence, c.says...)...)
				case PermissionRequest:
					// No answer: the question goes to the user, with a
					// note saying why the hook did not answer.
					if out.Blocked || out.PermissionDecision != NoDecision || out.Reason != "" {
						t.Errorf("Blocked %v, PermissionDecision %q, Reason %q; want no answer", out.Blocked, out.PermissionDecision, out.Reason)
					}
					if len(out.Warnings) != 1 {
						t.Fatalf("got %d warnings, want 1: %q", len(out.Warnings), out.Warnings)
					}
					contains(t, "the warning", out.Warnings[0], append([]string{`The PermissionRequest hook "audio-check" did not answer the permission question`, "The question goes to the user instead."}, c.says...)...)
				case SessionStart, PreCompact, PostCompact, SubagentStart, PostModelSwitch, DirectoryAdded, CwdChanged, FileChanged:
					// These cannot block. One warning; the session, the
					// summary, the sub-agent or the switch goes ahead.
					if out.Blocked || out.Reason != "" || out.PermissionDecision != NoDecision {
						t.Errorf("Blocked %v, Reason %q on an event that cannot block", out.Blocked, out.Reason)
					}
					if len(out.Warnings) != 1 {
						t.Fatalf("got %d warnings, want 1: %q", len(out.Warnings), out.Warnings)
					}
					contains(t, "the warning", out.Warnings[0], append([]string{"The " + string(event) + ` hook "audio-check" could not be evaluated`, "cannot stop anything, so gnosh went on without it"}, c.says...)...)
				case SessionEnd, StopFailure, InstructionsLoaded, Notification:
					// Logged: the record has it, and nothing is shown.
					if out.Blocked || out.Reason != "" || len(out.Warnings) != 0 {
						t.Errorf("Blocked %v, Reason %q, Warnings %q on an event where a failure is only recorded", out.Blocked, out.Reason, out.Warnings)
					}
				default:
					t.Fatalf("no row for %s", event)
				}
				if event != PermissionRequest && info.class != warns && len(out.Warnings) != 0 {
					t.Errorf("warnings where the reason already says it: %q", out.Warnings)
				}
			})
		}
	}
}

// A plain block (exit 2) on every event: it blocks where a block counts,
// it is feedback after a tool call, it is no answer to a permission
// question, and elsewhere it is a warning or nothing.
func TestExitTwoOnEveryEvent(t *testing.T) {
	for _, event := range Events() {
		t.Run(string(event), func(t *testing.T) {
			t.Parallel()
			r := newRunner(t, options(t), Hook{Event: event, Name: "guard", Command: "echo 'not like this' >&2; exit 2"})
			out := run(t, r, input(event))
			rec := out.Records[0]
			if out.CouldNotEvaluate || out.EndTurn {
				t.Errorf("CouldNotEvaluate %v, EndTurn %v for a plain block", out.CouldNotEvaluate, out.EndTurn)
			}
			switch event {
			case PreToolUse, PreModelSwitch:
				if !out.Blocked || out.PermissionDecision != Deny || out.Reason != "not like this" || rec.Result != ResultBlocked {
					t.Errorf("got %+v", out)
				}
			case UserPromptSubmit, UserPromptExpansion, Stop, SubagentStop, ConfigChange, PostToolBatch, Elicitation, ElicitationResult:
				if !out.Blocked || out.PermissionDecision != NoDecision || out.Reason != "not like this" || rec.Result != ResultBlocked {
					t.Errorf("got %+v", out)
				}
			case PostToolUse, PostToolUseFailure:
				if !out.Blocked || out.Reason != "not like this" || rec.Result != ResultFeedback {
					t.Errorf("got %+v", out)
				}
			case PermissionRequest:
				// Exit 2 is "no answer" for this event.
				if out.Blocked || out.PermissionDecision != NoDecision || out.Reason != "" || rec.Result != ResultNone {
					t.Errorf("got %+v", out)
				}
				if len(out.Warnings) != 1 {
					t.Fatalf("warnings: %q", out.Warnings)
				}
				contains(t, "the warning", out.Warnings[0], `"guard" did not answer`, "status 2", `"deny" in JSON`)
			case SessionStart, SubagentStart, PreCompact, PostCompact, SessionEnd, PostModelSwitch, DirectoryAdded, CwdChanged, FileChanged:
				// An objection is a warning.
				if out.Blocked || out.Reason != "" || rec.Result != ResultWarning {
					t.Errorf("got %+v", out)
				}
				if len(out.Warnings) != 1 {
					t.Fatalf("warnings: %q", out.Warnings)
				}
				contains(t, "the warning", out.Warnings[0], "The "+string(event)+` hook "guard" objected`, "cannot stop anything", "not like this")
			case StopFailure, InstructionsLoaded, Notification:
				if out.Blocked || out.Reason != "" || len(out.Warnings) != 0 || rec.Result != ResultWarning {
					t.Errorf("got %+v", out)
				}
			default:
				t.Fatalf("no row for %s", event)
			}
			if !strings.Contains(rec.Stderr, "not like this") || rec.ExitCode != 2 {
				t.Errorf("record: Stderr %q, ExitCode %d", rec.Stderr, rec.ExitCode)
			}
		})
	}
}

// On the events where nothing a hook prints is read, what it prints
// changes nothing, whatever it is.
func TestOutputIsNotReadWhereItCannotCount(t *testing.T) {
	for _, event := range []Event{SessionEnd, StopFailure, InstructionsLoaded, Notification} {
		for _, text := range []string{
			`{"decision": "block", "reason": "no"}`,
			`{"continue": false, "stopReason": "halt"}`,
			`{"systemMessage": "look"}`,
			`{"decision": "Deny"}`,
			`{not json at all}`,
			"some plain words",
		} {
			out := one(t, event, prints(t, text))
			if out.Blocked || out.EndTurn || out.CouldNotEvaluate || out.Reason != "" || out.AdditionalContext != "" ||
				len(out.Warnings) != 0 || len(out.SystemMessages) != 0 || out.Records[0].Result != ResultNone {
				t.Errorf("%s hook printing %s changed the Outcome: %+v", event, text, out)
			}
		}
	}
}
