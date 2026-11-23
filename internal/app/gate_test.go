package app

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/gnosh-ai/gnosh/internal/app/apptest"
	"github.com/gnosh-ai/gnosh/internal/core"
)

// preToolUse writes a PreToolUse hook that answers with the given JSON,
// and puts it in the user's config for the Bash tool.
func preToolUse(w *apptest.World, name, answer string) {
	hook := w.Script(w.Home+"/hooks/"+name+".sh", "cat > /dev/null\ncat <<'JSON'\n"+answer+"\nJSON")
	w.Hook("PreToolUse", "Bash", hook)
}

// toolResult returns what the model read as the result of a call, from
// the request that followed it.
func toolResult(t *testing.T, w *apptest.World, request int, id string) string {
	t.Helper()
	reqs := w.Model.Requests()
	if request >= len(reqs) {
		t.Fatalf("the model was sent %d requests, and request %d was asked for", len(reqs), request)
	}
	want := callID(t, reqs[request], id)
	for _, m := range reqs[request].Messages() {
		if m["role"] == "tool" && m["tool_call_id"] == want {
			return apptest.Text(m)
		}
	}
	t.Fatalf("request %d holds no result for the call %s", request, id)
	return ""
}

const hookAllows = `{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"allow","permissionDecisionReason":"the hook says yes"}}`

// The gate's order, first step against second: a hook's approval does not
// lift a deny rule.
func TestGateDenyRuleBeatsAHooksApproval(t *testing.T) {
	w := apptest.NewWorld(t,
		apptest.Reply{Calls: []apptest.Call{{ID: "c1", Name: "Bash", Args: `{"command":"rm -f a.txt"}`}}},
		apptest.Reply{Text: "Refused."})
	w.Write("a.txt", "keep me\n")
	preToolUse(w, "allow", hookAllows)
	w.Deny("Bash(rm *)")
	run(t, w, "-y", "-p", "remove it").wantCode(t, 0)
	if !w.Exists("a.txt") {
		t.Errorf("the file was removed: a hook's approval lifted a deny rule")
	}
	has(t, "what the model read", toolResult(t, w, 1, "c1"), "Bash(rm *)")
}

// A hook's approval answers the question the mode would have asked: in a
// run that nobody attends, the call runs.
func TestGateHookApprovalStandsInForTheQuestion(t *testing.T) {
	w := apptest.NewWorld(t,
		apptest.Reply{Calls: []apptest.Call{{ID: "c1", Name: "Bash", Args: `{"command":"touch made.txt"}`}}},
		apptest.Reply{Text: "Made."})
	preToolUse(w, "allow", hookAllows)
	run(t, w, "-p", "make it").wantCode(t, 0).wantNoErr(t)
	if !w.Exists("made.txt") {
		t.Errorf("the command did not run although a hook approved it: %s", toolResult(t, w, 1, "c1"))
	}
}

// A hook's "ask" asks even when the call would have run without a
// question: with permissions skipped, in a run nobody attends, the call
// is refused for want of somebody to ask.
func TestGateHookAskAsksEvenWhenPermissionsAreSkipped(t *testing.T) {
	w := apptest.NewWorld(t,
		apptest.Reply{Calls: []apptest.Call{{ID: "c1", Name: "Bash", Args: `{"command":"touch made.txt"}`}}},
		apptest.Reply{Text: "Not made."})
	preToolUse(w, "ask", `{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"ask","permissionDecisionReason":"a person must look at this one"}}`)
	r := run(t, w, "-y", "-p", "make it")
	r.wantCode(t, 0)
	if w.Exists("made.txt") {
		t.Errorf("the command ran although a hook wanted a person asked")
	}
	has(t, "what the model read", toolResult(t, w, 1, "c1"), "nobody is here to give it", "a person must look at this one")
	// The person who started the run with -y is not told to give it -y.
	has(t, "stderr", r.err, "Bash was not run: it is one of the calls gnosh puts to a person even with permissions skipped", "No flag lets a run approve such a call")
	hasNot(t, "stderr", r.err, "give it -y")
}

// Without -y the person is told how a run is let act.
func TestUnattendedRunSaysHowToLetItAct(t *testing.T) {
	w := apptest.NewWorld(t,
		apptest.Reply{Calls: []apptest.Call{{ID: "c1", Name: "Bash", Args: `{"command":"touch made.txt"}`}}},
		apptest.Reply{Text: "Not made."})
	r := run(t, w, "-p", "make it")
	r.wantCode(t, 0)
	has(t, "stderr", r.err, "Bash was not run: it needs a person's approval, and a run has nobody to ask. To let a run act, give it -y")
}

// A hook may change a call's arguments. The rules then judge the call as
// it will run, not as the model wrote it, and the tool runs the changed
// one.
func TestGateRulesJudgeWhatAHookMadeOfTheCall(t *testing.T) {
	t.Run("the changed call is the one that runs", func(t *testing.T) {
		w := apptest.NewWorld(t,
			apptest.Reply{Calls: []apptest.Call{{ID: "c1", Name: "Bash", Args: `{"command":"touch one.txt"}`}}},
			apptest.Reply{Text: "Made."})
		preToolUse(w, "rewrite", `{"hookSpecificOutput":{"hookEventName":"PreToolUse","updatedInput":{"command":"touch two.txt"}}}`)
		run(t, w, "-y", "-p", "make it").wantCode(t, 0)
		if w.Exists("one.txt") || !w.Exists("two.txt") {
			t.Errorf("one.txt exists: %v, two.txt exists: %v; want only two.txt", w.Exists("one.txt"), w.Exists("two.txt"))
		}
	})
	t.Run("a deny rule sees the changed call", func(t *testing.T) {
		w := apptest.NewWorld(t,
			apptest.Reply{Calls: []apptest.Call{{ID: "c1", Name: "Bash", Args: `{"command":"echo harmless"}`}}},
			apptest.Reply{Text: "Refused."})
		w.Write("a.txt", "keep me\n")
		preToolUse(w, "rewrite", `{"hookSpecificOutput":{"hookEventName":"PreToolUse","updatedInput":{"command":"rm -f a.txt"}}}`)
		w.Deny("Bash(rm *)")
		run(t, w, "-y", "-p", "say something").wantCode(t, 0)
		if !w.Exists("a.txt") {
			t.Errorf("the file was removed: the deny rule was checked against the call as the model wrote it")
		}
		has(t, "what the model read", toolResult(t, w, 1, "c1"), "Bash(rm *)")
	})
}

// When the rules come to "ask", a PermissionRequest hook may answer in
// the person's place, either way.
func TestGatePermissionRequestHookAnswers(t *testing.T) {
	script := func() []apptest.Reply {
		return []apptest.Reply{
			{Calls: []apptest.Call{{ID: "c1", Name: "Bash", Args: `{"command":"touch made.txt"}`}}},
			{Text: "Over."},
		}
	}
	answer := func(w *apptest.World, behavior string) {
		hook := w.Script(w.Home+"/hooks/permission.sh", "cat > /dev/null\ncat <<'JSON'\n"+
			`{"hookSpecificOutput":{"hookEventName":"PermissionRequest","decision":{"behavior":"`+behavior+`","message":"the hook has spoken"}}}`+"\nJSON")
		w.Hook("PermissionRequest", "", hook)
	}
	t.Run("allow", func(t *testing.T) {
		w := apptest.NewWorld(t, script()...)
		answer(w, "allow")
		run(t, w, "-p", "make it").wantCode(t, 0)
		if !w.Exists("made.txt") {
			t.Errorf("the command did not run although the hook allowed it: %s", toolResult(t, w, 1, "c1"))
		}
	})
	t.Run("deny", func(t *testing.T) {
		w := apptest.NewWorld(t, script()...)
		answer(w, "deny")
		run(t, w, "-p", "make it").wantCode(t, 0)
		if w.Exists("made.txt") {
			t.Errorf("the command ran although the hook refused it")
		}
		has(t, "what the model read", toolResult(t, w, 1, "c1"), "PermissionRequest hook", "the hook has spoken")
	})
}

// A call that its tool already knows cannot run (an edit to a file that
// was never read) is still judged first: with nobody to ask, the model
// reads no more than it would of any call that needs approval, and with
// permissions skipped it reads the tool's own reason.
func TestGateJudgesACallThatCannotRun(t *testing.T) {
	script := func() []apptest.Reply {
		return []apptest.Reply{
			{Calls: []apptest.Call{{ID: "c1", Name: "Edit", Args: `{"file_path":"a.txt","old_string":"two","new_string":"three"}`}}},
			{Text: "Over."},
		}
	}
	w := apptest.NewWorld(t, script()...)
	w.Write("a.txt", "two\n")
	run(t, w, "-p", "change it").wantCode(t, 0)
	got := toolResult(t, w, 1, "c1")
	has(t, "what the model read, with nobody to ask", got, "nobody is here to give it")

	w = apptest.NewWorld(t, script()...)
	w.Write("a.txt", "two\n")
	run(t, w, "-y", "-p", "change it").wantCode(t, 0)
	got = toolResult(t, w, 1, "c1")
	has(t, "what the model read, with permissions skipped", got, "This call did not run", "Read")
	if w.Read("a.txt") != "two\n" {
		t.Errorf("a file that was never read was changed")
	}
}

// At the terminal: "don't ask again" writes the rule where the config
// says approvals are kept, and the same call is not asked about again; a
// bare no stops the turn, and the model is not sent the refusal.
func TestGateAnswersOfThePerson(t *testing.T) {
	w := apptest.NewWorld(t,
		apptest.Reply{Calls: []apptest.Call{{ID: "c1", Name: "Bash", Args: `{"command":"touch made.txt"}`}}},
		apptest.Reply{Calls: []apptest.Call{{ID: "c2", Name: "Bash", Args: `{"command":"touch made.txt"}`}}},
		apptest.Reply{Text: "Made twice."},
		apptest.Reply{Calls: []apptest.Call{{ID: "c3", Name: "Bash", Args: `{"command":"mkdir other"}`}}})
	s := sit(t, w)
	s.see("Type a prompt and Enter.")
	s.typeLine("make it twice")
	s.see("? Allow Bash?")
	s.see("  2. Yes, and don't ask again for Bash(touch made.txt)")
	s.typeLine("2")
	// The second, identical call runs without a question.
	s.see("Made twice.")
	s.see("-- done in ")
	if strings.Count(s.out.String(), "? Allow Bash?") != 1 {
		t.Errorf("the same call was asked about twice after \"don't ask again\":\n%s", clip(s.out.String()))
	}
	has(t, "the settings file approvals are kept in", w.Read(".gnosh/settings.local.json"), "Bash(touch made.txt)")

	// A bare no.
	s.typeLine("now something else")
	s.see("? Allow Bash?")
	s.see("$ mkdir other")
	s.typeLine("no")
	s.see("-- ended (the person refused permission for Bash)")
	if w.Exists("other") {
		t.Errorf("the command ran although the person said no")
	}
	if n := len(w.Model.Requests()); n != 4 {
		t.Errorf("the model was sent %d requests, want 4: a bare no stops the turn", n)
	}
	s.typeLine("/exit")
	s.exit()
	checkSession(t, w, w.Sessions()[0])
}

// The settings file approvals are kept in is read as every config file is,
// so what a person wrote in it by hand, with comments and a comma after a
// last item, does not stop an approval from being saved. The file is then
// written again whole, as plain JSON: the rules that were in it are kept,
// and the comments are not.
func TestSavedApprovalReadsWhatAPersonWrote(t *testing.T) {
	w := apptest.NewWorld(t,
		apptest.Reply{Calls: []apptest.Call{{ID: "c1", Name: "Bash", Args: `{"command":"touch made.txt"}`}}},
		apptest.Reply{Text: "Made."})
	const local = ".gnosh/settings.local.json"
	w.Write(local, "{\n  // what I never want run here\n  \"permissions\": {\n    \"deny\": [\"Bash(rm *)\",],  /* and no more */\n  },\n}\n")
	s := sit(t, w)
	s.see("Type a prompt and Enter.")
	s.typeLine("make it")
	s.see("? Allow Bash?")
	s.see("  2. Yes, and don't ask again for Bash(touch made.txt)")
	s.typeLine("2")
	s.see("Made.")
	s.see("-- done in ")
	s.typeLine("/exit")
	s.exit()
	hasNot(t, "what the session said", s.out.String(), "could not be written down")

	got := w.Read(local)
	has(t, "the settings file", got, "Bash(touch made.txt)", "Bash(rm *)")
	hasNot(t, "the settings file", got, "//", "/*", "never want")
	var doc struct {
		Permissions struct{ Allow, Deny []string }
	}
	if err := json.Unmarshal([]byte(got), &doc); err != nil {
		t.Fatalf("the settings file gnosh wrote is not plain JSON: %v\n%s", err, got)
	}
	if !slices.Equal(doc.Permissions.Allow, []string{"Bash(touch made.txt)"}) || !slices.Equal(doc.Permissions.Deny, []string{"Bash(rm *)"}) {
		t.Errorf("the rules after the save: %+v", doc.Permissions)
	}
	// And gnosh loads what it wrote: the deny rule is still in force.
	out := run(t, w, "-p", "/permissions").wantCode(t, 0).out
	has(t, "/permissions after the save", out, "Bash(rm *)")
}

// /permissions allow, ask and deny put a rule in force and in the
// project's local settings file; /permissions remove takes it out of
// both; a rule from another file is named and left, and a rule gnosh would
// not load is not written.
func TestPermissionsEditsRules(t *testing.T) {
	w := apptest.NewWorld(t)
	w.Deny("Bash(curl *)")
	local := filepath.Join(w.Dir, ".gnosh", "settings.local.json")
	perm := func(args string) string {
		t.Helper()
		return run(t, w, "--trust-folder", "-p", "/permissions "+args).wantCode(t, 0).out
	}
	has(t, "deny", perm("deny Bash(rm *)"), "The deny rule Bash(rm *) is in force, and kept in "+local)
	has(t, "the local file", w.Read(local), `"deny"`, "Bash(rm *)")
	has(t, "the next session", perm(""), "Bash(rm *)", "from "+local)
	has(t, "allow, quoted", perm(`allow "Bash(go test *)"`), "The allow rule Bash(go test *) is in force")
	has(t, "remove", perm("remove Bash(rm *)"), "The deny rule Bash(rm *) is taken out.")
	if got := w.Read(local); strings.Contains(got, "Bash(rm *)") || !strings.Contains(got, "Bash(go test *)") {
		t.Errorf("the local file after the removal:\n%s", got)
	}
	hasNot(t, "the next session", perm(""), "Bash(rm *)")
	has(t, "a rule in the user's file", perm("remove Bash(curl *)"), "the deny rule Bash(curl *) comes from", "which gnosh does not write")
	has(t, "a rule gnosh would not load", perm("allow NoSuchTool"), "NoSuchTool was not added")
	hasNot(t, "the local file", w.Read(local), "NoSuchTool")
	has(t, "a rule that is not there", perm("remove Bash(ls *)"), "No rule Bash(ls *) is in force")
	// The same words on two lists: removing one, named, leaves the other.
	perm("allow Bash(git push *)")
	perm("ask Bash(git push *)")
	has(t, "remove from one list", perm("remove allow Bash(git push *)"), "The allow rule Bash(git push *) is taken out.")
	if got := w.Read(local); !strings.Contains(got, `"ask": [`) || !strings.Contains(got, "Bash(git push *)") {
		t.Errorf("the ask rule went with the allow rule:\n%s", got)
	}
	has(t, "no rule", perm("deny"), "/permissions takes allow, ask, deny or remove")
}

// dropTool is a tool whose every call throws something away for good: it
// says so in what it reports of a call (core.Action.Confirm).
type dropTool struct{ dropped *[]string }

func (dropTool) Spec() core.ToolSpec {
	return core.ToolSpec{Name: "Drop", Description: "Deletes a machine's saved state.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"name":{"type":"string"}},"required":["name"]}`)}
}

func (dropTool) Describe(call core.ToolCall, env *core.ToolEnv) (core.Action, error) {
	var in struct {
		Name string `json:"name"`
	}
	_ = json.Unmarshal(call.Input, &in)
	return core.Action{Kind: core.ActionOther, Summary: "Delete the saved state of the machine " + in.Name + ". It cannot be got back.", Confirm: true}, nil
}

func (d dropTool) Run(ctx context.Context, call core.ToolCall, env *core.ToolEnv) (core.ToolResult, error) {
	*d.dropped = append(*d.dropped, string(call.Input))
	return core.TextResult("dropped"), nil
}

// dropFeature brings the Drop tool, as a tool that otherwise never asks.
func dropFeature(dropped *[]string) Feature {
	return Feature{
		Name:  "drop",
		Tools: []ToolName{{Name: "Drop", Quiet: true}},
		Session: func(ctx context.Context, s *Session) (*Contribution, error) {
			return &Contribution{Tools: []core.Tool{dropTool{dropped: dropped}}}, nil
		},
	}
}

// A call that throws away what cannot be got back is put to a person
// whatever would otherwise let it run: skipped permissions, an allow rule,
// a hook's approval, a PermissionRequest hook. Where nobody can be asked
// it does not run, and the model reads why. A deny rule refuses it
// outright.
func TestGateConfirmAlwaysAsksAPerson(t *testing.T) {
	script := func() []apptest.Reply {
		return []apptest.Reply{
			{Calls: []apptest.Call{{ID: "c1", Name: "Drop", Args: `{"name":"scratch"}`}}},
			{Text: "Over."},
		}
	}
	unattended := func(t *testing.T, prep func(w *apptest.World), args ...string) (*apptest.World, result, []string) {
		var dropped []string
		w := apptest.NewWorld(t, script()...)
		if prep != nil {
			prep(w)
		}
		o := testOptions(w, append(append([]string{"-p", "--output-format", "json"}, args...), "drop the scratch machine")...)
		o.Features = []Feature{dropFeature(&dropped)}
		return w, runWith(t, t.Context(), o).wantCode(t, 0), dropped
	}
	for _, tc := range []struct {
		name string
		prep func(w *apptest.World)
		args []string
	}{
		{"with permissions skipped", nil, []string{"-y"}},
		{"with an allow rule for the tool", nil, []string{"--allowedTools", "Drop", "--"}},
		{"with a hook that approves", func(w *apptest.World) {
			hook := w.Script(w.Home+"/hooks/allow.sh", "cat > /dev/null\ncat <<'JSON'\n"+hookAllows+"\nJSON")
			w.Hook("PreToolUse", "", hook)
		}, nil},
		{"with a PermissionRequest hook that allows", func(w *apptest.World) {
			hook := w.Script(w.Home+"/hooks/permission.sh", "cat > /dev/null\ncat <<'JSON'\n"+
				`{"hookSpecificOutput":{"hookEventName":"PermissionRequest","decision":{"behavior":"allow"}}}`+"\nJSON")
			w.Hook("PermissionRequest", "", hook)
		}, nil},
	} {
		t.Run("nobody to ask, "+tc.name, func(t *testing.T) {
			w, r, dropped := unattended(t, tc.prep, tc.args...)
			if len(dropped) != 0 {
				t.Fatalf("the call ran with nobody to confirm it: %v", dropped)
			}
			has(t, "what the model read", toolResult(t, w, 1, "c1"), "nobody is here to give it", "cannot be got back")
			has(t, "the result", r.out, `"tool_name":"Drop"`)
		})
	}
	t.Run("a deny rule refuses it outright", func(t *testing.T) {
		w, _, dropped := unattended(t, nil, "-y", "--disallowedTools", "Drop(*)", "--")
		if len(dropped) != 0 {
			t.Fatalf("the call ran although a deny rule covers it")
		}
		// The whole tool is denied, so the model is not offered it at all.
		hasNot(t, "the request", requestText(t, w, 0), `"name":"Drop"`)
	})
	t.Run("a person is asked, and only this once", func(t *testing.T) {
		var dropped []string
		w := apptest.NewWorld(t, script()...)
		pr, pw := io.Pipe()
		s := &seat{t: t, in: pw, out: &safeBuffer{}, errs: &safeBuffer{}, signals: make(chan os.Signal, 4), done: make(chan int, 1)}
		o := testOptions(w, "-y")
		o.Features = []Feature{dropFeature(&dropped)}
		o.Stdin, o.Stdout, o.Stderr = pr, s.out, s.errs
		o.StdinIsTerminal, o.StdoutIsTerminal = true, true
		o.Signals = s.signals
		go func() { s.done <- Main(context.Background(), o) }()
		defer func() { pw.Close(); <-s.done }()
		s.see("permission mode bypassPermissions")
		s.typeLine("drop the scratch machine")
		s.see("? Allow Drop?")
		s.see("Delete the saved state of the machine scratch.")
		s.see("cannot be got back, so gnosh asks before it runs")
		s.see("  1. Yes, this once")
		s.see("  2. No")
		if strings.Contains(s.out.String(), "don't ask again") {
			t.Errorf("a call that must always be confirmed offers \"don't ask again\":\n%s", clip(s.out.String()))
		}
		if len(dropped) != 0 {
			t.Fatalf("the call ran before the person answered")
		}
		s.typeLine("1")
		s.see("Over.")
		if len(dropped) != 1 {
			t.Errorf("the call ran %d times after the yes", len(dropped))
		}
	})
}

// A hook's "if" filter: the hook runs for the calls its rule takes and
// for no others, before a call and after it, and a filter on an event
// that is not about a tool call keeps its hook from running at all, of
// which the person is told.
func TestHookIfFilter(t *testing.T) {
	w := apptest.NewWorld(t,
		apptest.Reply{Calls: []apptest.Call{
			{ID: "c1", Name: "Bash", Args: `{"command":"rm -f a.txt"}`},
			{ID: "c2", Name: "Bash", Args: `{"command":"touch b.txt"}`},
		}},
		apptest.Reply{Text: "Tidied."})
	w.Write("a.txt", "keep me\n")
	log := w.Home + "/hook-log"
	pre := w.Script(w.Home+"/hooks/pre.sh", "printf 'pre %s\\n' \"$(cat)\" >> "+log+"\necho 'no removing here' >&2\nexit 2")
	post := w.Script(w.Home+"/hooks/post.sh", "printf 'post %s\\n' \"$(cat)\" >> "+log)
	stop := w.Script(w.Home+"/hooks/stop.sh", "cat > /dev/null\necho stop >> "+log)
	w.Settings(`{"hooks": {
		"PreToolUse": [{"matcher": "Bash", "hooks": [{"type": "command", "command": "` + pre + `", "if": "Bash(rm *)"}]}],
		"PostToolUse": [{"hooks": [{"type": "command", "command": "` + post + `", "if": "Bash(touch *)"}]}],
		"Stop": [{"hooks": [{"type": "command", "command": "` + stop + `", "if": "Bash(rm *)"}]}]
	}}`)
	r := run(t, w, "-y", "-p", "tidy up").wantCode(t, 0)
	if !w.Exists("a.txt") {
		t.Errorf("rm ran: the PreToolUse hook filtered to it did not run")
	}
	has(t, "what the model read of rm", toolResult(t, w, 1, "c1"), "no removing here")
	if !w.Exists("b.txt") {
		t.Errorf("touch did not run: %s", toolResult(t, w, 1, "c2"))
	}
	data, _ := os.ReadFile(log)
	var pres, posts []string
	for _, line := range strings.Split(string(data), "\n") {
		switch {
		case strings.HasPrefix(line, "pre "):
			pres = append(pres, line)
		case strings.HasPrefix(line, "post "):
			posts = append(posts, line)
		case line == "stop":
			t.Errorf("the Stop hook with an \"if\" filter ran")
		}
	}
	if len(pres) != 1 || !strings.Contains(pres[0], "rm -f a.txt") {
		t.Errorf("the PreToolUse hook ran for %q, want once, for rm", pres)
	}
	if len(posts) != 1 || !strings.Contains(posts[0], "touch b.txt") {
		t.Errorf("the PostToolUse hook ran for %q, want once, for touch", posts)
	}
	has(t, "the notices", r.err, `Stop hook from`, `has an "if" filter, which only the hooks of tool events have, so it never runs`)
}

// A filter whose rule cannot be applied lets its hook run for every call
// its matcher takes, and the person is told when the session starts.
func TestHookIfFilterThatCannotBeApplied(t *testing.T) {
	w := apptest.NewWorld(t,
		apptest.Reply{Calls: []apptest.Call{{ID: "c1", Name: "Bash", Args: `{"command":"touch b.txt"}`}}},
		apptest.Reply{Text: "Made."})
	log := w.Home + "/hook-log"
	pre := w.Script(w.Home+"/hooks/pre.sh", "cat > /dev/null\necho pre >> "+log)
	w.Settings(`{"hooks": {"PreToolUse": [{"matcher": "Bash", "hooks": [{"type": "command", "command": "` + pre + `", "if": "Nonesuch(x)"}]}]}}`)
	r := run(t, w, "-y", "-p", "make it").wantCode(t, 0)
	if data, _ := os.ReadFile(log); strings.Count(string(data), "pre") != 1 {
		t.Errorf("the hook ran %d times, want once", strings.Count(string(data), "pre"))
	}
	has(t, "the notices", r.err, `has the "if" filter Nonesuch(x), which cannot be applied`, "so it runs for every call its matcher takes")
}

// /hooks shows a hook's "if" filter beside its matcher.
func TestSlashHooksShowsTheFilter(t *testing.T) {
	w := apptest.NewWorld(t)
	w.Settings(`{"hooks": {"PreToolUse": [{"matcher": "Bash", "hooks": [{"type": "command", "command": "true", "if": "Bash(git push *)"}]}]}}`)
	r := run(t, w, "-p", "/hooks").wantCode(t, 0)
	has(t, "/hooks", r.out, "PreToolUse:", "[matches Bash]", "[if Bash(git push *)]")
}

// --permission-prompts none: nobody answers, so a call that needs a yes is
// refused with word not to send it again. host (the default) refuses as a
// run always has. The tool that asks a person is offered in neither.
func TestPermissionPromptsNone(t *testing.T) {
	w := apptest.NewWorld(t,
		apptest.Reply{Calls: []apptest.Call{{ID: "c1", Name: "Bash", Args: `{"command":"touch made.txt"}`}}},
		apptest.Reply{Text: "Could not."})
	run(t, w, "-p", "make it", "--permission-prompts", "none").wantCode(t, 0)
	body := string(w.Model.Requests()[0].Body)
	hasNot(t, "the tools offered", body, `"name":"AskUserQuestion"`)
	has(t, "what the model read", toolResult(t, w, 1, "c1"), "nobody is here to give it", "do not send this call again")
	if w.Exists("made.txt") {
		t.Errorf("the call ran")
	}
	w.Model.Add(apptest.Reply{Calls: []apptest.Call{{ID: "c1", Name: "Bash", Args: `{"command":"touch made.txt"}`}}}, apptest.Reply{Text: "Could not."})
	run(t, w, "-p", "make it", "--permission-prompts", "host").wantCode(t, 0)
	reqs := w.Model.Requests()
	// (Nor with host: nobody answers in a run, a PreToolUse hook aside.)
	hasNot(t, "the tools offered with host", string(reqs[2].Body), `"name":"AskUserQuestion"`)
	hasNot(t, "what the model read with host", toolResult(t, w, 3, "c1"), "do not send this call again")
}

// permissions.denyInsteadOfPrompting, as Claude Code's: in a session a
// person attends, a call that would ask is refused instead and the turn
// goes on; the tools that need a person (AskUserQuestion and the plan
// tools) are not offered. A project cannot set it.
func TestDenyInsteadOfPrompting(t *testing.T) {
	w := apptest.NewWorld(t,
		apptest.Reply{Calls: []apptest.Call{{ID: "c1", Name: "Bash", Args: `{"command":"touch made.txt"}`}}},
		apptest.Reply{Text: "Could not."})
	w.Settings(`{"permissions": {"denyInsteadOfPrompting": true}}`)
	s := sit(t, w)
	s.see("Type a prompt and Enter.")
	s.typeLine("make it")
	s.see("Could not.")
	if strings.Contains(s.out.String(), "Allow Bash?") {
		t.Error("a permission question was put")
	}
	body := string(w.Model.Requests()[0].Body)
	hasNot(t, "the tools offered", body, `"name":"AskUserQuestion"`, `"name":"ExitPlanMode"`, `"name":"EnterPlanMode"`)
	has(t, "what the model read", toolResult(t, w, 1, "c1"), "permissions.denyInsteadOfPrompting", "Do not send this call again")
	if w.Exists("made.txt") {
		t.Errorf("the call ran")
	}
	s.typeLine("/exit")
	s.exit()
}

// When nobody answers the question gnosh puts even with permissions
// skipped, the model is told why it was asked, and for a program whose
// name the line puts together, to write it out.
func TestCriticalTimeoutSaysWhy(t *testing.T) {
	got := criticalTimeout("A dangerous removal cannot be ruled out: the line gives the program it runs ($S) more values than gnosh follows.")
	has(t, "a name put together", got, "nobody answered within two minutes", "Why it asks: A dangerous removal cannot be ruled out: the line gives the program it runs ($S)", "Write the program's name out as a plain word")
	got = criticalTimeout("This removal looks dangerous (rm -rf ~).")
	has(t, "a removal", got, "Why it asks: This removal looks dangerous (rm -rf ~).", "say what you were about to do and why")
	hasNot(t, "a removal", got, "plain word")
}
