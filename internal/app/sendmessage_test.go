package app

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/gnosh-ai/gnosh/internal/app/agentdef"
	"github.com/gnosh-ai/gnosh/internal/app/apptest"
)

// SendMessage gives a sub-agent that ended more to do: it runs again in
// the background under the same id, with its first task and its answer to
// it in front of it, and its report comes to the session as a notice.
func TestSendMessageResumesASubAgent(t *testing.T) {
	w := apptest.NewWorld(t)
	idRe := regexp.MustCompile(`\[agent id: ([0-9a-f-]+)\]`)
	sent := false
	w.Model.Respond(func(n int, r apptest.Request) *apptest.Reply {
		body := string(r.Body)
		if strings.Contains(body, "You are a sub-agent inside gnosh") {
			switch {
			case !strings.Contains(body, "now count the oranges"):
				return &apptest.Reply{Text: "There are 3 apples."}
			case strings.Contains(body, "count the apples") && strings.Contains(body, "There are 3 apples."):
				return &apptest.Reply{Text: "There are 5 oranges."}
			default:
				return &apptest.Reply{Text: "There are 5 oranges, and I know of no apples."}
			}
		}
		switch m := idRe.FindStringSubmatch(body); {
		case strings.Contains(body, "There are 5 oranges"):
			return &apptest.Reply{Text: "Counted."}
		case m != nil && !sent:
			sent = true
			return &apptest.Reply{Calls: []apptest.Call{{ID: "c2", Name: "SendMessage", Args: fmt.Sprintf(`{"to":%q,"message":"now count the oranges","summary":"count the oranges"}`, m[1])}}}
		case strings.Contains(body, `"role":"tool"`):
			return &apptest.Reply{Text: "Waiting for it."}
		}
		return &apptest.Reply{Calls: []apptest.Call{{ID: "c1", Name: "Agent", Args: `{"description":"count apples","prompt":"count the apples"}`}}}
	})
	r := run(t, w, "-p", "count").wantCode(t, 0)
	if !sent {
		t.Fatalf("the report gave no agent id:\n%s", r.out)
	}
	if !anyRequestHas(w, "is working in the background on: count the oranges") {
		t.Errorf("SendMessage did not say the sub-agent works in the background")
	}
	reqs := w.Model.Requests()
	last := string(reqs[len(reqs)-1].Body)
	if !strings.Contains(last, "There are 5 oranges.") || strings.Contains(last, "know of no apples") {
		t.Errorf("the resumed sub-agent's report, as the session was told it: %s", last)
	}
}

// What SendMessage does not take up: an id no sub-agent of the session has,
// and a one-shot kind.
func TestSendMessageRefusals(t *testing.T) {
	w := apptest.NewWorld(t)
	idRe := regexp.MustCompile(`\[agent id: ([0-9a-f-]+)\]`)
	w.Model.Respond(func(n int, r apptest.Request) *apptest.Reply {
		body := string(r.Body)
		if strings.Contains(body, "look around") && !strings.Contains(body, "0000-nope") {
			// The Explore sub-agent (its own prompt is not gnosh's).
			return &apptest.Reply{Text: "Looked."}
		}
		switch {
		case strings.Contains(body, "Looked."):
			if idRe.MatchString(body) {
				return &apptest.Reply{Text: "An Explore report gave an id."}
			}
			return &apptest.Reply{Text: "Done."}
		case strings.Contains(body, "0000-nope"):
			return &apptest.Reply{Calls: []apptest.Call{{ID: "c2", Name: "Agent", Args: `{"description":"look","prompt":"look around","subagent_type":"Explore"}`}}}
		}
		return &apptest.Reply{Calls: []apptest.Call{{ID: "c1", Name: "SendMessage", Args: `{"to":"0000-nope","message":"x"}`}}}
	})
	run(t, w, "-p", "go").wantCode(t, 0)
	if !anyRequestHas(w, `no sub-agent of this session has the id \"0000-nope\"`) {
		t.Errorf("an unknown id was not refused")
	}
	if !anyRequestHas(w, "Looked.") || anyRequestHas(w, "[agent id:") {
		t.Errorf("an Explore sub-agent's report gave an id to resume it by, or none came")
	}
}

// SendMessage is judged as starting a sub-agent of the kind it takes up
// again: a deny rule about that kind, made after the sub-agent ended,
// refuses the call, and nothing runs.
func TestSendMessageObeysAgentRules(t *testing.T) {
	w := apptest.NewWorld(t)
	idRe := regexp.MustCompile(`\[agent id: ([0-9a-f-]+)\]`)
	w.Model.Respond(func(n int, r apptest.Request) *apptest.Reply {
		body := string(r.Body)
		m := idRe.FindStringSubmatch(body)
		switch {
		case strings.Contains(body, "You are a sub-agent inside gnosh"):
			return &apptest.Reply{Text: "There are 3 apples."}
		case strings.Contains(body, "judged as starting one is"):
			return &apptest.Reply{Text: "Refused."}
		case m != nil && strings.Contains(body, "more please"):
			return &apptest.Reply{Calls: []apptest.Call{{ID: "c2", Name: "SendMessage", Args: fmt.Sprintf(`{"to":%q,"message":"now count the oranges"}`, m[1])}}}
		case m != nil:
			return &apptest.Reply{Text: "Counted."}
		}
		return &apptest.Reply{Calls: []apptest.Call{{ID: "c1", Name: "Agent", Args: `{"description":"count apples","prompt":"count the apples"}`}}}
	})
	run(t, w, "-p", "count").wantCode(t, 0)
	if !anyRequestHas(w, "[agent id:") {
		t.Fatal("the sub-agent's report gave no id")
	}
	w.Deny("Agent(general-purpose)")
	run(t, w, "-c", "-p", "more please").wantCode(t, 0)
	if !anyRequestHas(w, "The deny rule Agent(general-purpose)") {
		t.Errorf("the deny rule about general-purpose sub-agents did not refuse SendMessage")
	}
	for _, r := range w.Model.Requests() {
		body := string(r.Body)
		if strings.Contains(body, "You are a sub-agent inside gnosh") && strings.Contains(body, "now count the oranges") {
			t.Errorf("the refused sub-agent ran again")
		}
	}
}

// A session that runs as an agent whose tools let it start only some kinds
// of sub-agent does not take up one of another kind with SendMessage, as
// it would not start one with Agent.
func TestSendMessageKeepsToTheAgentsKinds(t *testing.T) {
	w := apptest.NewWorld(t)
	w.Write(w.ConfigDir()+"/agents/lead.md", "---\nname: lead\ndescription: Leads.\ntools: Read, SendMessage, Agent(helper)\n---\nYou lead.\n")
	w.Write(w.ConfigDir()+"/agents/helper.md", "---\nname: helper\ndescription: Helps.\n---\nYou help.\n")
	idRe := regexp.MustCompile(`\[agent id: ([0-9a-f-]+)\]`)
	w.Model.Respond(func(n int, r apptest.Request) *apptest.Reply {
		body := string(r.Body)
		m := idRe.FindStringSubmatch(body)
		switch {
		case strings.Contains(body, "You are a sub-agent inside gnosh"):
			return &apptest.Reply{Text: "There are 3 apples."}
		case strings.Contains(body, "whose tools let it start only these kinds"):
			return &apptest.Reply{Text: "Refused."}
		case m != nil && strings.Contains(body, "more please"):
			return &apptest.Reply{Calls: []apptest.Call{{ID: "c2", Name: "SendMessage", Args: fmt.Sprintf(`{"to":%q,"message":"now count the oranges"}`, m[1])}}}
		case m != nil:
			return &apptest.Reply{Text: "Counted."}
		}
		return &apptest.Reply{Calls: []apptest.Call{{ID: "c1", Name: "Agent", Args: `{"description":"count apples","prompt":"count the apples"}`}}}
	})
	run(t, w, "-p", "count").wantCode(t, 0)
	run(t, w, "-c", "--agent", "lead", "-p", "more please").wantCode(t, 0)
	if !anyRequestHas(w, "whose tools let it start only these kinds of sub-agent: helper; the sub-agent") {
		t.Errorf("SendMessage took up a general-purpose sub-agent under an agent that may start only helpers")
	}
}

// With Agent switched off there is no SendMessage either: it takes up only
// what Agent started.
func TestNoSendMessageWithoutAgent(t *testing.T) {
	w := apptest.NewWorld(t)
	w.Deny("Agent")
	w.Model.Respond(func(n int, r apptest.Request) *apptest.Reply { return &apptest.Reply{Text: "Hi."} })
	run(t, w, "-p", "hi").wantCode(t, 0)
	names := toolNamesOf(w.Model.Requests()[0])
	if slices.Contains(names, "Agent") || slices.Contains(names, "SendMessage") {
		t.Errorf("tools offered with Agent switched off: %v", names)
	}
}

// Which ends of a run leave a sub-agent to be given more to do.
func TestTakesMore(t *testing.T) {
	general := agentdef.Def{Name: "general-purpose"}
	for _, c := range []struct {
		def   agentdef.Def
		ended string
		want  bool
	}{
		{general, "completed", true},
		{general, "partial", true},
		{general, "failed", true},
		{general, "halted", false},
		{general, "stopped", false},
		{general, "", false},
		{agentdef.Def{Name: "Explore"}, "completed", false},
		{agentdef.Def{Name: "worker", Isolation: "worktree"}, "completed", false},
	} {
		if got := takesMore(c.def, c.ended); got != c.want {
			t.Errorf("takesMore(%s, %q) = %v, want %v", c.def.Name, c.ended, got, c.want)
		}
	}
}

// anyRequestHas reports whether a request the model was sent holds text.
func anyRequestHas(w *apptest.World, text string) bool {
	for _, r := range w.Model.Requests() {
		if strings.Contains(string(r.Body), text) {
			return true
		}
	}
	return false
}

// /tasks lists the sub-agents of the session that have ended, as Claude
// Code's does under "Earlier in this session".
func TestTasksListsEndedSubAgents(t *testing.T) {
	w := apptest.NewWorld(t)
	w.Model.Respond(func(n int, r apptest.Request) *apptest.Reply {
		body := string(r.Body)
		switch {
		case strings.Contains(body, "You are a sub-agent inside gnosh"):
			return &apptest.Reply{Text: "There are 3 apples."}
		case strings.Contains(body, `"role":"tool"`):
			return &apptest.Reply{Text: "Counted."}
		}
		return &apptest.Reply{Calls: []apptest.Call{{ID: "c1", Name: "Agent", Args: `{"description":"count apples","prompt":"count the apples"}`}}}
	})
	s := sit(t, w)
	s.see("Type a prompt and Enter.")
	s.typeLine("count")
	s.see("Counted.")
	s.typeLine("/tasks")
	s.see("Earlier in this session:")
	s.see("sub-agent (general-purpose), done: ")
	s.see("more to do (SendMessage to its id)")
	s.typeLine("/exit")
	s.exit()
}

// The sub-agents in the background stopped (Ctrl+X Ctrl+K) while one is
// being taken up again: it is not started, as Claude Code 2.1.329 fixed
// for its own, and SendMessage says so; it was not yet among those that
// were stopped.
func TestSendMessageStoppedWhileItIsMade(t *testing.T) {
	old := whileSubIsMade
	// (Through the control the interface's Ctrl+X Ctrl+K calls, which
	// with no sub-agent listed yet still stops the one being made.)
	whileSubIsMade = func(l *live) { (&controls{l: l}).StopBackgroundAgents() }
	defer func() { whileSubIsMade = old }()
	w := apptest.NewWorld(t)
	idRe := regexp.MustCompile(`\[agent id: ([0-9a-f-]+)\]`)
	sent, resumed := false, false
	w.Model.Respond(func(n int, r apptest.Request) *apptest.Reply {
		body := string(r.Body)
		if strings.Contains(body, "You are a sub-agent inside gnosh") {
			if strings.Contains(body, "now count the oranges") {
				resumed = true
			}
			return &apptest.Reply{Text: "There are 3 apples."}
		}
		switch m := idRe.FindStringSubmatch(body); {
		case m != nil && !sent:
			sent = true
			return &apptest.Reply{Calls: []apptest.Call{{ID: "c2", Name: "SendMessage", Args: fmt.Sprintf(`{"to":%q,"message":"now count the oranges","summary":"count the oranges"}`, m[1])}}}
		case strings.Contains(body, `"role":"tool"`):
			return &apptest.Reply{Text: "Stopped, then."}
		}
		return &apptest.Reply{Calls: []apptest.Call{{ID: "c1", Name: "Agent", Args: `{"description":"count apples","prompt":"count the apples"}`}}}
	})
	run(t, w, "-p", "count").wantCode(t, 0)
	if !sent {
		t.Fatal("no SendMessage was sent")
	}
	if !anyRequestHas(w, "The sub-agent was not started: the sub-agents in the background were stopped while it was being made ready.") {
		t.Errorf("SendMessage did not say the sub-agent was not started")
	}
	if resumed {
		t.Errorf("the sub-agent ran on after the background sub-agents were stopped")
	}
}
