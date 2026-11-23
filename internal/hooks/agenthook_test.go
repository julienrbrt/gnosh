package hooks

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// An agent hook is read with its own fields, on the events that take
// one; continueOnBlock, PermissionRequest and the events of command hooks
// are refused.
func TestAgentHookConfig(t *testing.T) {
	cfg, err := ParseConfig(json.RawMessage(`{"Stop": [{"hooks": [{"type": "agent", "prompt": "Verify the tests pass. $ARGUMENTS", "timeout": 120, "model": "small"}]}]}`), "settings.json")
	if err != nil || len(cfg.Hooks) != 1 {
		t.Fatalf("an agent hook: %v", err)
	}
	if h := cfg.Hooks[0]; !h.Agent || h.Prompt != "Verify the tests pass. $ARGUMENTS" || h.Model != "small" || h.Timeout != 120*time.Second || h.CommandLine() != "agent: Verify the tests pass. $ARGUMENTS" {
		t.Errorf("the hook read: %+v", h)
	}
	for text, want := range map[string]string{
		`{"Stop": [{"hooks": [{"type": "agent", "prompt": "x", "continueOnBlock": true}]}]}`: `"continueOnBlock", which a hook of "type": "agent" does not take`,
		`{"Stop": [{"hooks": [{"type": "agent"}]}]}`:                                         `the agent hook has no "prompt"`,
		`{"PermissionRequest": [{"hooks": [{"type": "agent", "prompt": "x"}]}]}`:             "PermissionRequest runs command, http, mcp_tool and prompt hooks",
		`{"Elicitation": [{"hooks": [{"type": "agent", "prompt": "x"}]}]}`:                   "Elicitation runs command, http and mcp_tool hooks",
		`{"Stop": [{"hooks": [{"type": "agent", "prompt": "x", "command": "true"}]}]}`:       `"type": "agent" does not take`,
	} {
		_, err := ParseConfig(json.RawMessage(text), "settings.json")
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: the error says %v, want %q", text, err, want)
		}
	}
	if _, err := NewHook(Spec{Event: "Stop", Agent: true}); err == nil || !strings.Contains(err.Error(), `the agent hook has no "prompt"`) {
		t.Errorf("NewHook of an agent hook with no prompt: %v", err)
	}
}

// An agent hook hands its task, with the event put in, to Options.Agent,
// and reads the last message as a prompt hook's answer: "ok": false
// refuses a call without ending the turn (as continueOnBlock does), an
// impossible Stop still keeps the turn going, it gets 60 seconds unless it
// says otherwise, and it does not run on an event of its own sub-agent.
func TestAgentHook(t *testing.T) {
	var tasks []string
	var left time.Duration
	answer := `{"ok": false, "reason": "the tests were not run"}`
	opts := options(t)
	opts.Prompt = func(ctx context.Context, model, text string) (string, error) {
		t.Errorf("an agent hook asked the model directly: %q", text)
		return `{"ok": true}`, nil
	}
	opts.Agent = func(ctx context.Context, model, task string) (string, error) {
		tasks = append(tasks, task)
		if dl, ok := ctx.Deadline(); ok {
			left = time.Until(dl)
		}
		return "I looked at the test log.\n" + answer, nil
	}
	pre := Hook{Event: PreToolUse, Matcher: "Bash", Agent: true, Prompt: "Check that $ARGUMENTS runs no deploy."}
	out := run(t, newRunner(t, opts, pre), input(PreToolUse))
	if out.PermissionDecision != Deny || !strings.Contains(out.Reason, "the tests were not run") || out.EndTurn {
		t.Errorf("a PreToolUse agent hook's ok: false: %+v", out)
	}
	if len(tasks) != 1 || !strings.Contains(tasks[0], `"tool_name":"Bash"`) || strings.Contains(tasks[0], "$ARGUMENTS") {
		t.Errorf("the task: %q", tasks)
	}
	if left < 50*time.Second || left > 61*time.Second {
		t.Errorf("the agent hook was given %v, want 60 seconds", left)
	}
	if len(out.Records) != 1 || !strings.HasPrefix(out.Records[0].Command, "agent: Check that") {
		t.Errorf("the record: %+v", out.Records)
	}

	answer = `{"ok": false, "reason": "no tests exist", "impossible": true}`
	stop := Hook{Event: Stop, Agent: true, Prompt: "Verify the tests pass."}
	if out := run(t, newRunner(t, opts, stop), input(Stop)); !out.Blocked || !strings.Contains(out.Reason, "no tests exist") {
		t.Errorf("an agent hook's impossible ok: false let the turn end: %+v", out)
	}

	in := input(Stop)
	in.NoAgentHooks = true
	before := len(tasks)
	if out := run(t, newRunner(t, opts, stop), in); out.Blocked || len(out.Records) != 0 || len(tasks) != before {
		t.Errorf("an agent hook ran on its own sub-agent's event: %+v", out)
	}

	opts.Agent = nil
	if out := run(t, newRunner(t, opts, stop), input(Stop)); !out.Blocked || !out.CouldNotEvaluate {
		t.Errorf("an agent hook with no sub-agent to run: %+v", out)
	}
}
