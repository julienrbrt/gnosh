package hooks

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

// A prompt hook asks the model, with the event put in for $ARGUMENTS, and
// its "ok": false becomes what a command hook would have said on the
// event: a Stop that keeps the turn going with the reason, a PreToolUse
// that refuses the call and ends the turn (or, with continueOnBlock, only
// refuses it), a UserPromptSubmit that blocks the prompt. "ok": true lets
// everything through, and an impossible Stop lets the turn end.
func TestPromptHook(t *testing.T) {
	var mu sync.Mutex
	var asked []string
	answer := `{"ok": false, "reason": "the tests were not run"}`
	opts := options(t)
	opts.Prompt = func(ctx context.Context, model, text string) (string, error) {
		mu.Lock()
		defer mu.Unlock()
		asked = append(asked, text)
		return answer, nil
	}
	stop := Hook{Event: Stop, Prompt: "Did the work end well? $ARGUMENTS Check the tests ran."}
	out := run(t, newRunner(t, opts, stop), input(Stop))
	if !out.Blocked || !strings.Contains(out.Reason, "the tests were not run") || out.EndTurn {
		t.Errorf("a Stop hook's ok: false: %+v", out)
	}
	if len(asked) != 1 || !strings.Contains(asked[0], `"hook_event_name":"Stop"`) || strings.Contains(asked[0], "$ARGUMENTS") || !strings.HasSuffix(asked[0], "Check the tests ran.") {
		t.Errorf("what the model was asked: %q", asked)
	}
	if len(out.Records) != 1 || !strings.HasPrefix(out.Records[0].Command, "prompt: Did the work end well?") {
		t.Errorf("the record: %+v", out.Records)
	}

	answer = "Thinking it over.\n```json\n{\"ok\": false, \"reason\": \"no tests exist\", \"impossible\": true}\n```"
	if out := run(t, newRunner(t, opts, stop), input(Stop)); out.Blocked {
		t.Errorf("a Stop hook's impossible ok: false let the turn go on: %+v", out)
	}

	answer = `{"ok": false, "reason": "it reads .env"}`
	pre := Hook{Event: PreToolUse, Matcher: "Bash", Prompt: "Block any Bash command that reads .env files."}
	out = run(t, newRunner(t, opts, pre), input(PreToolUse))
	if out.PermissionDecision != Deny || !strings.Contains(out.Reason, "it reads .env") || !out.EndTurn {
		t.Errorf("a PreToolUse hook's ok: false: %+v", out)
	}
	if !strings.Contains(asked[len(asked)-1], "Block any Bash command that reads .env files.\n\n{") {
		t.Errorf("without $ARGUMENTS the event does not follow the prompt: %q", asked[len(asked)-1])
	}
	pre.ContinueOnBlock = true
	if out := run(t, newRunner(t, opts, pre), input(PreToolUse)); out.PermissionDecision != Deny || out.EndTurn {
		t.Errorf("continueOnBlock: %+v", out)
	}

	prompt := Hook{Event: UserPromptSubmit, Prompt: "Refuse prompts that ask to deploy."}
	if out := run(t, newRunner(t, opts, prompt), input(UserPromptSubmit)); !out.Blocked || !strings.Contains(out.Reason, "it reads .env") {
		t.Errorf("a UserPromptSubmit hook's ok: false: %+v", out)
	}

	answer = `{"ok": true}`
	for _, h := range []Hook{stop, pre, prompt} {
		if out := run(t, newRunner(t, opts, h), input(h.Event)); out.Blocked || out.PermissionDecision == Deny || out.EndTurn {
			t.Errorf("%s with ok: true: %+v", h.Event, out)
		}
	}
}

// A model that does not answer with the JSON, does not answer in time, or
// is not there to ask, is a hook that could not run: on an event that can
// block, it blocks, as a command that fails does.
func TestPromptHookFailures(t *testing.T) {
	opts := options(t)
	reply := "Sure, that looks fine to me."
	opts.Prompt = func(ctx context.Context, model, text string) (string, error) {
		if reply == "" {
			// (As the session's model does: the limit is the answer's,
			// AnswerLimit, and is past.)
			limit, ok := AnswerLimit(ctx)
			if !ok {
				t.Error("a prompt hook's question does not say its limit")
			}
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-time.After(limit):
				return "", fmt.Errorf("no answer within %s: %w", limit, context.DeadlineExceeded)
			}
		}
		return reply, nil
	}
	pre := Hook{Event: PreToolUse, Prompt: "Block nothing."}
	if out := run(t, newRunner(t, opts, pre), input(PreToolUse)); !out.Blocked || !strings.Contains(out.Reason, "did not answer with the JSON a prompt hook asks for") {
		t.Errorf("an answer that is not the JSON: %+v", out)
	}
	reply = ""
	pre.Timeout = 50 * time.Millisecond
	if out := run(t, newRunner(t, opts, pre), input(PreToolUse)); !out.Blocked {
		t.Errorf("a model that did not answer in time: %+v", out)
	}
	opts.Prompt = nil
	if out := run(t, newRunner(t, opts, pre), input(PreToolUse)); !out.Blocked {
		t.Errorf("no model to ask: %+v", out)
	}
}

// A prompt hook is read from a settings file with its own fields, on the
// events that take one; a prompt hook on another event, one without its
// prompt, and fields of another type are said.
func TestPromptHookConfig(t *testing.T) {
	cfg, err := ParseConfig(json.RawMessage(`{"Stop": [{"hooks": [{"type": "prompt", "prompt": "All tests pass. $ARGUMENTS", "timeout": 45, "model": "small", "continueOnBlock": true}]}]}`), "settings.json")
	if err != nil || len(cfg.Hooks) != 1 {
		t.Fatalf("a prompt hook: %v", err)
	}
	if h := cfg.Hooks[0]; h.Prompt != "All tests pass. $ARGUMENTS" || h.Model != "small" || !h.ContinueOnBlock || h.Timeout != 45*time.Second {
		t.Errorf("the hook read: %+v", h)
	}
	for text, want := range map[string]string{
		`{"SessionStart": [{"hooks": [{"type": "prompt", "prompt": "x"}]}]}`:                       "a prompt hook runs on PreToolUse",
		`{"Stop": [{"hooks": [{"type": "prompt"}]}]}`:                                              "the prompt hook has no",
		`{"Stop": [{"hooks": [{"type": "prompt", "prompt": "x", "command": "true"}]}]}`:            `"type": "prompt" does not take`,
		`{"Stop": [{"hooks": [{"type": "command", "command": "true", "continueOnBlock": true}]}]}`: `"type": "command" does not take`,
		`{"Stop": [{"hooks": [{"type": "script", "prompt": "x"}]}]}`:                               "prompt hooks and agent hooks only",
	} {
		_, err := ParseConfig(json.RawMessage(text), "settings.json")
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: the error says %v, want %q", text, err, want)
		}
	}
}

// The answer is the last object with "ok" in the reply: an example or a
// quoted object before it is not the answer, and an object inside another
// is part of it.
func TestReadVerdictTakesTheLast(t *testing.T) {
	for text, want := range map[string]bool{
		`health said {"ok": true, "db": "down"}, so: {"ok": false, "reason": "db down"}`:      false,
		`I'll answer {"ok": true} if they pass. They do not. {"ok": false, "reason": "fail"}`: false,
		`{"ok": false, "reason": "x", "detail": {"ok": true}}`:                                false,
		"Checked.\n```json\n{\"ok\": true}\n```":                                              true,
	} {
		v, err := readVerdict(text)
		if err != nil || *v.OK != want {
			t.Errorf("%q: %+v, %v; want ok %v", text, v, err, want)
		}
	}
}
