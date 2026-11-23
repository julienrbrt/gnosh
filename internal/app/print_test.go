package app

import (
	"strings"
	"testing"

	"github.com/gnosh-ai/gnosh/internal/app/apptest"
)

// What a reply ends with in gnosh's own marker, after the answer, is a
// model repeating a line gnosh put after the prompt: it is not part of a
// run's result. Anything else is left as the model wrote it.
func TestDropEchoedLine(t *testing.T) {
	cases := map[string]string{
		"ready\n<gnosh>Use mem and Gno whenever reasonable: work things out": "ready",
		"ready\n\n<gnosh>Use mem and Gno whenever reasonable.</gnosh>\n":     "ready",
		"<gnosh>Use mem and Gno whenever reasonable.</gnosh>":                "<gnosh>Use mem and Gno whenever reasonable.</gnosh>",
		"It is written <gnosh>.":                     "It is written <gnosh>.",
		"first\n<gnosh>a note\nthat runs on</gnosh>": "first",
		"ready</gnosh>":                              "ready",
		"first\n<gnosh>a note\nthat runs on":         "first\n<gnosh>a note\nthat runs on",
		"</gnosh>":                                   "</gnosh>",
		"plain answer\n":                             "plain answer\n",
		"two lines\nof answer":                       "two lines\nof answer",
	}
	for in, want := range cases {
		if got := dropEchoedLine(in); got != want {
			t.Errorf("dropEchoedLine(%q) = %q, want %q", in, got, want)
		}
	}
}

// In a run, the echo is left out of what is printed and of the result.
func TestRunLeavesOutAnEchoedLine(t *testing.T) {
	w := apptest.NewWorld(t, apptest.Reply{Text: "ready\n<gnosh>Use mem whenever reasonable.</gnosh>"})
	r := run(t, w, "-p", "Reply with the one word: ready").wantCode(t, 0)
	if r.out != "ready\n" {
		t.Errorf("text output = %q", r.out)
	}
	w.Model.Add(apptest.Reply{Text: "ready\n<gnosh>Use mem whenever reasonable.</gnosh>"})
	r = run(t, w, "-p", "--output-format", "json", "again").wantCode(t, 0)
	if obj := jsonLines(t, r.out)[0]; obj["result"] != "ready" {
		t.Errorf("json result = %v", obj["result"])
	}
}

// stream-json carries a hook's start and end: the SessionStart hooks'
// always, the others' with --include-hook-events, as system lines
// hook_started and hook_response (Claude Code's), one ID for both.
func TestStreamJSONHookEvents(t *testing.T) {
	w := apptest.NewWorld(t)
	w.Hook("SessionStart", "", "echo starting")
	w.Hook("PostToolUse", "Bash", "echo after; exit 0")
	w.Model.Respond(func(n int, r apptest.Request) *apptest.Reply {
		if !strings.Contains(string(r.Body), `"role":"tool"`) {
			return &apptest.Reply{Calls: []apptest.Call{{ID: "c1", Name: "Bash", Args: `{"command":"true"}`}}}
		}
		return &apptest.Reply{Text: "Done."}
	})
	hookLines := func(out string) []map[string]any {
		var got []map[string]any
		for _, l := range jsonLines(t, out) {
			if l["type"] == "system" && (l["subtype"] == "hook_started" || l["subtype"] == "hook_response") {
				got = append(got, l)
			}
		}
		return got
	}
	r := run(t, w, "-y", "-p", "--output-format", "stream-json", "--verbose", "go")
	r.wantCode(t, 0)
	got := hookLines(r.out)
	if len(got) != 2 || got[0]["hook_name"] != "SessionStart:startup" || got[1]["subtype"] != "hook_response" ||
		got[1]["stdout"] != "starting\n" || got[1]["outcome"] != "success" || got[0]["hook_id"] != got[1]["hook_id"] {
		t.Errorf("without the flag, the SessionStart hook's alone: %v", got)
	}
	r = run(t, w, "-y", "-p", "--output-format", "stream-json", "--verbose", "--include-hook-events", "go")
	r.wantCode(t, 0)
	got = hookLines(r.out)
	names := []string{}
	for _, l := range got {
		names = append(names, l["subtype"].(string)+" "+l["hook_name"].(string))
	}
	if strings.Join(names, ", ") != "hook_started SessionStart:startup, hook_response SessionStart:startup, hook_started PostToolUse:Bash, hook_response PostToolUse:Bash" {
		t.Errorf("with the flag: %v", names)
	}
	r = run(t, w, "-p", "--include-hook-events", "go")
	has(t, "the flag without stream-json", r.err, "only does something with --output-format stream-json")
}
