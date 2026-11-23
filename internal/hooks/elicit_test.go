package hooks

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// An Elicitation hook answers an MCP server's request in the person's
// place: accept with the values, decline in any of its forms, cancel, or
// nothing, which leaves the form to the person. What cannot be read, or
// a hook that could not evaluate, declines.
func TestElicitationHookAnswers(t *testing.T) {
	answer := func(action, rest string) string {
		return `{"hookSpecificOutput": {"hookEventName": "Elicitation", "action": "` + action + `"` + rest + `}}`
	}
	out := one(t, Elicitation, prints(t, answer("accept", `, "content": {"project_key": "DOCS"}`)))
	if out.ElicitAction != "accept" || string(out.ElicitContent) != `{"project_key":"DOCS"}` || out.Blocked || out.Records[0].Result != ResultAnswered {
		t.Errorf("accept: %+v", out)
	}
	if out := one(t, Elicitation, prints(t, answer("accept", ""))); out.ElicitAction != "accept" || out.ElicitContent != nil {
		t.Errorf("accept with no content: %+v", out)
	}
	if out := one(t, Elicitation, prints(t, answer("cancel", ""))); out.ElicitAction != "cancel" || out.Blocked {
		t.Errorf("cancel: %+v", out)
	}
	if out := one(t, Elicitation, "true"); out.ElicitAction != "" || out.Blocked {
		t.Errorf("a hook that says nothing: %+v", out)
	}
	for name, command := range map[string]string{
		"decline":                  prints(t, answer("decline", "")),
		"exit 2 beside an accept":  script(t, "echo '"+answer("accept", `, "content": {"project_key": "DOCS"}`)+"'\necho no >&2\nexit 2"),
		"a block beside an accept": prints(t, `{"decision": "block", "hookSpecificOutput": {"hookEventName": "Elicitation", "action": "accept", "content": {"project_key": "DOCS"}}}`),
		"an action not known":      prints(t, answer("maybe", "")),
		"content that is a list":   prints(t, answer("accept", `, "content": ["DOCS"]`)),
		"a hook that crashed":      script(t, "echo broken >&2\nexit 1"),
	} {
		out := one(t, Elicitation, command)
		if out.ElicitAction != "decline" || !out.Blocked || out.ElicitContent != nil {
			t.Errorf("%s: %+v", name, out)
		}
	}
	// An answer counts only from a hook that exits 0.
	out = one(t, Elicitation, script(t, "echo '"+answer("accept", `, "content": {"project_key": "DOCS"}`)+"'\nexit 1"))
	if out.ElicitAction != "" || out.Blocked || !strings.Contains(strings.Join(out.Records[0].Notes, " "), "an answer counts only from a hook that exits 0") {
		t.Errorf("an accept with exit 1: %+v", out)
	}
	// Values without accept are not used.
	out = one(t, Elicitation, prints(t, `{"hookSpecificOutput": {"hookEventName": "Elicitation", "content": {"project_key": "DOCS"}}}`))
	if out.ElicitAction != "" || !strings.Contains(strings.Join(out.Records[0].Notes, " "), "without \"action\": \"accept\"") {
		t.Errorf("content without an action: %+v", out)
	}
}

// Several hooks: a decline wins over the others' answers, a cancel over an
// accept, two accepts must agree on the values, and the same values
// written differently agree.
func TestElicitationHooksTogether(t *testing.T) {
	answer := func(action, content string) string {
		s := `{"hookSpecificOutput": {"hookEventName": "Elicitation", "action": "` + action + `"`
		if content != "" {
			s += `, "content": ` + content
		}
		return s + `}}`
	}
	both := func(a, b string) Outcome {
		r := newRunner(t, options(t), Hook{Event: Elicitation, Command: prints(t, a)}, Hook{Event: Elicitation, Command: prints(t, b)})
		return run(t, r, input(Elicitation))
	}
	if out := both(answer("accept", `{"project_key": "DOCS"}`), answer("decline", "")); out.ElicitAction != "decline" {
		t.Errorf("accept and decline: %+v", out)
	}
	if out := both(answer("accept", `{"project_key": "DOCS"}`), answer("cancel", "")); out.ElicitAction != "cancel" {
		t.Errorf("accept and cancel: %+v", out)
	}
	if out := both(answer("cancel", ""), answer("accept", `{"project_key": "DOCS"}`)); out.ElicitAction != "cancel" {
		t.Errorf("cancel and accept: %+v", out)
	}
	out := both(answer("accept", `{"project_key": "DOCS"}`), answer("accept", `{"project_key": "WEB"}`))
	if out.ElicitAction != "decline" || !out.Blocked || !strings.Contains(out.Reason, "different values") {
		t.Errorf("two accepts that differ: %+v", out)
	}
	if out := both(answer("accept", `{"project_key": "DOCS"}`), answer("accept", `{ "project_key" : "DOCS" }`)); out.ElicitAction != "accept" || out.Blocked {
		t.Errorf("two accepts that agree: %+v", out)
	}
	// Two accepts that differ decline, whatever a third hook says.
	r := newRunner(t, options(t), Hook{Event: Elicitation, Command: prints(t, answer("cancel", ""))},
		Hook{Event: Elicitation, Command: prints(t, answer("accept", `{"project_key": "DOCS"}`))},
		Hook{Event: Elicitation, Command: prints(t, answer("accept", `{"project_key": "WEB"}`))})
	if out := run(t, r, input(Elicitation)); out.ElicitAction != "decline" || !out.Blocked {
		t.Errorf("a cancel beside two different accepts: %+v", out)
	}
}

// The hooks are told the server, its message and its schema (Elicitation),
// or the person's answer (ElicitationResult), and a matcher is held
// against the server's name.
func TestElicitationHookInput(t *testing.T) {
	dir := t.TempDir()
	saved := filepath.Join(dir, "input.json")
	keep := script(t, "cat > "+shq(saved))
	r := newRunner(t, options(t),
		Hook{Event: Elicitation, Matcher: "issue-tracker", Command: keep},
		Hook{Event: Elicitation, Matcher: "other-server", Command: prints(t, `{"hookSpecificOutput": {"hookEventName": "Elicitation", "action": "decline"}}`)})
	out := run(t, r, input(Elicitation))
	if out.ElicitAction != "" || len(out.Records) != 1 {
		t.Fatalf("the matchers: %+v", out)
	}
	var got map[string]any
	data, _ := os.ReadFile(saved)
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("the input: %v (%s)", err, data)
	}
	if got["hook_event_name"] != "Elicitation" || got["mcp_server_name"] != "issue-tracker" || got["message"] != "Which project?" || got["mode"] != "form" {
		t.Errorf("the input: %s", data)
	}
	if schema, _ := got["requested_schema"].(map[string]any); schema == nil || schema["type"] != "object" {
		t.Errorf("the schema in the input: %s", data)
	}

	r = newRunner(t, options(t), Hook{Event: ElicitationResult, Command: script(t, "cat > "+shq(saved)+"\necho '{\"hookSpecificOutput\": {\"hookEventName\": \"ElicitationResult\", \"action\": \"accept\", \"content\": {\"project_key\": \"WEB-2\"}}}'")})
	out = run(t, r, input(ElicitationResult))
	if out.ElicitAction != "accept" || string(out.ElicitContent) != `{"project_key":"WEB-2"}` {
		t.Errorf("an ElicitationResult hook's change: %+v", out)
	}
	data, _ = os.ReadFile(saved)
	contains(t, "the ElicitationResult input", string(data), `"hook_event_name":"ElicitationResult"`, `"action":"accept"`, `"content":{"project_key":"WEB"}`, `"mcp_server_name":"issue-tracker"`)
}
