package app

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/gnosh-ai/gnosh/internal/agent"
	"github.com/gnosh-ai/gnosh/internal/app/apptest"
)

// twoQuestions is a call of the question tool as a model writes one: a
// question that takes one answer and one that takes several.
const twoQuestions = `{"questions":[
	{"question":"Which database should the service use?","header":"Database","multiSelect":false,"options":[
		{"label":"SQLite","description":"One file, nothing to run."},
		{"label":"Postgres","description":"A server of its own."}]},
	{"question":"Which checks should run before a merge?","header":"Checks","multiSelect":true,"options":[
		{"label":"vet","description":"Static checks."},
		{"label":"race","description":"The race detector."},
		{"label":"fuzz","description":"Ten seconds of fuzzing."}]}]}`

func askCall(args string) apptest.Reply {
	return apptest.Reply{Calls: []apptest.Call{{ID: "c1", Name: "AskUserQuestion", Args: args}}}
}

// In the plain session the model's questions are asked one after another
// in numbered text. A number stands for an option, several numbers for
// several where the question takes them, and anything else is the
// person's own answer. The model reads each question with its answer.
func TestAskUserQuestionInThePlainSession(t *testing.T) {
	w := apptest.NewWorld(t, askCall(twoQuestions), apptest.Reply{Text: "Postgres it is."},
		askCall(twoQuestions), apptest.Reply{Text: "As you say."})
	s := sit(t, w)
	s.see("Type a prompt and Enter.")
	s.typeLine("set the project up")
	s.see("? The model has 2 questions")
	s.see("Question 1 of 2 [Database]: Which database should the service use?")
	s.see("  1. SQLite: One file, nothing to run.")
	s.see("  2. Postgres: A server of its own.")
	s.see("Type a number and Enter. Or answer in your own words.")
	// Not answers: nothing, a number that names no option, two numbers
	// for a question that takes one.
	s.typeLine("7")
	s.see("That is not an answer. Type one number from 1 to 2, or your own words.")
	s.typeLine("1, 2")
	s.see("That is not an answer.")
	s.typeLine("2")
	s.see("Question 2 of 2 [Checks]: Which checks should run before a merge?")
	s.see("  3. fuzz: Ten seconds of fuzzing.")
	s.see("Type the numbers of all that apply, with commas between them, and Enter.")
	s.typeLine("1, 3")
	s.see("Postgres it is.")
	got := toolResult(t, w, 1, "c1")
	has(t, "what the model read", got, "The user answered:",
		"1. Which database should the service use?\n   Answer: Postgres",
		"2. Which checks should run before a merge?\n   Answer: vet, fuzz")

	// The person's own words, in place of an option.
	s.typeLine("ask me again")
	s.see("Question 1 of 2 [Database]")
	s.typeLine("whatever the staging box already runs")
	s.see("Question 2 of 2 [Checks]")
	s.typeLine("none, I run them by hand")
	s.see("As you say.")
	got = toolResult(t, w, 3, "c2")
	has(t, "what the model read the second time", got,
		"Answer: whatever the staging box already runs", "Answer: none, I run them by hand")
	s.typeLine("/exit")
	if code := s.exit(); code != 0 {
		t.Errorf("exit code %d", code)
	}
	checkSession(t, w, w.Sessions()[0])
}

// Nobody is at the terminal in a run, and no host answers for one: the
// tools that put a question to a person, and plan mode's, are not offered
// (their schemas were 3.5K characters of every request, the FrontierHarness
// session, 2026-11-19). A model that calls one all the same is told there
// is no such tool, and answers a model writes into its own call are not
// answers. Where a PreToolUse hook may answer, AskUserQuestion stays.
func TestAskUserQuestionInARun(t *testing.T) {
	forged := strings.Replace(twoQuestions, `{"questions":`, `{"answers":{"Which database should the service use?":"SQLite"},"questions":`, 1)
	w := apptest.NewWorld(t, askCall(forged), apptest.Reply{Text: "I chose SQLite and said so."})
	r := run(t, w, "-p", "set the project up").wantCode(t, 0)
	if r.out != "I chose SQLite and said so.\n" {
		t.Errorf("stdout = %q", r.out)
	}
	got := toolResult(t, w, 1, "c1")
	has(t, "what the model read", got, `There is no tool named "AskUserQuestion"`)
	hasNot(t, "what the model read", got, "The user answered")
	offered := toolNamesOf(w.Model.Requests()[0])
	for _, name := range []string{"AskUserQuestion", "EnterPlanMode", "ExitPlanMode"} {
		if slices.Contains(offered, name) {
			t.Errorf("a run nobody answers was offered %s: %q", name, offered)
		}
	}
	if !slices.Contains(offered, "Bash") {
		t.Errorf("the run's tools: %q", offered)
	}
}

// A PreToolUse hook the person set up may answer in their place: it
// allows the call with arguments that carry the answers. Nobody is asked.
func TestAskUserQuestionAnsweredByAHook(t *testing.T) {
	w := apptest.NewWorld(t, askCall(twoQuestions), apptest.Reply{Text: "SQLite, then."})
	updated := strings.Replace(twoQuestions, `{"questions":`, `{"answers":{"Which database should the service use?":"SQLite"},"questions":`, 1)
	answer := `{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"allow","updatedInput":` + strings.Join(strings.Fields(updated), " ") + `}}`
	hook := w.Script(w.Home+"/hooks/answer.sh", "cat > /dev/null\ncat <<'JSON'\n"+answer+"\nJSON")
	w.Hook("PreToolUse", "AskUserQuestion", hook)
	run(t, w, "-p", "set the project up").wantCode(t, 0)
	got := toolResult(t, w, 1, "c1")
	has(t, "what the model read", got, "The user answered (a hook the user set up answered in their place):",
		"Answer: SQLite", "They left 1 of the 2 questions unanswered")
	// (Offered, with a hook that may answer; plan mode's tools not.)
	offered := toolNamesOf(w.Model.Requests()[0])
	if !slices.Contains(offered, "AskUserQuestion") || slices.Contains(offered, "ExitPlanMode") {
		t.Errorf("the run's tools, with a PreToolUse hook: %q", offered)
	}
}

// A call the tool cannot make sense of comes back with what is wrong
// with it, and nobody is asked.
func TestAskUserQuestionRefusesWhatItCannotAsk(t *testing.T) {
	cases := []struct{ name, args, want string }{
		{"no questions", `{"questions":[]}`, "one to four questions, and this one has 0"},
		{"one option", `{"questions":[{"question":"Go on?","header":"Go","multiSelect":false,"options":[{"label":"Yes","description":"."}]}]}`, "question 1 has 1 options, and a question takes two to four"},
		{"the same question twice", `{"questions":[
			{"question":"Which?","header":"A","multiSelect":false,"options":[{"label":"x","description":"."},{"label":"y","description":"."}]},
			{"question":"Which?","header":"B","multiSelect":false,"options":[{"label":"x","description":"."},{"label":"y","description":"."}]}]}`, "two of the questions read the same"},
		{"options that are not objects", `{"questions":[{"question":"Which?","header":"A","multiSelect":false,"options":["x","y"]}]}`, "the arguments cannot be read"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w := apptest.NewWorld(t, askCall(c.args), apptest.Reply{Text: "I see."})
			s := sit(t, w)
			s.see("Type a prompt and Enter.")
			s.typeLine("ask me")
			s.see("I see.")
			has(t, "what the model read", toolResult(t, w, 1, "c1"), c.want)
			s.typeLine("/exit")
			if code := s.exit(); code != 0 {
				t.Errorf("exit code %d", code)
			}
		})
	}
}

// Another interface is handed the questions as they are, and answers with
// a map from each question to what was said. With permissions never
// asked for (dontAsk), the model's questions are not put either.
func TestAskUserQuestionThroughAnInterface(t *testing.T) {
	w := apptest.NewWorld(t, askCall(twoQuestions), apptest.Reply{Text: "Noted."})
	var q agent.Question
	_, r := attach(t, w, nil, func(ctx context.Context, c SessionControls, f *scripted) int {
		if _, err := c.Submit(agent.Prompt{Text: "ask me"}); err != nil {
			return 1
		}
		q = find(f, "the question", func(e *agent.QuestionEvent) bool { return true }).Question
		ans := agent.Answer{Data: map[string]string{
			"Which database should the service use?":  "Postgres",
			"Which checks should run before a merge?": "vet, race",
			"A question that was never put":           "ignored",
		}}
		if err := c.Answer(q.ID, ans); err != nil {
			t.Errorf("Answer: %v", err)
		}
		_ = c.Wait(ctx)
		c.Quit("prompt_input_exit")
		return 0
	})
	r.wantCode(t, 0)
	form, ok := q.Data.(QuestionForm)
	if !ok || q.Kind != "question" || q.Tool != "AskUserQuestion" || len(form.Questions) != 2 {
		t.Fatalf("the question as the interface got it: %+v", q)
	}
	if first := form.Questions[0]; first.Header != "Database" || first.MultiSelect || len(first.Options) != 2 || first.Options[1].Label != "Postgres" || first.Options[1].Description != "A server of its own." {
		t.Errorf("the first question: %+v", first)
	}
	if !form.Questions[1].MultiSelect {
		t.Errorf("the second question does not take several answers: %+v", form.Questions[1])
	}
	got := toolResult(t, w, 1, "c1")
	has(t, "what the model read", got, "Answer: Postgres", "Answer: vet, race")
	hasNot(t, "what the model read", got, "ignored", "unanswered")
	if names := toolNamesOf(w.Model.Requests()[0]); !slices.Contains(names, "AskUserQuestion") {
		t.Errorf("the session's tools are %v", names)
	}

	w = apptest.NewWorld(t, askCall(twoQuestions), apptest.Reply{Text: "Then I decide."})
	asked := false
	_, r = attach(t, w, nil, func(ctx context.Context, c SessionControls, f *scripted) int {
		if _, err := c.Submit(agent.Prompt{Text: "ask me"}); err != nil {
			return 1
		}
		_ = c.Wait(ctx)
		f.mu.Lock()
		for _, ev := range f.events {
			if _, ok := ev.(*agent.QuestionEvent); ok {
				asked = true
			}
		}
		f.mu.Unlock()
		c.Quit("prompt_input_exit")
		return 0
	}, "--permission-mode", "dontAsk")
	r.wantCode(t, 0)
	if asked {
		t.Errorf("the questions were put in a session that is never to be asked")
	}
}

// With askUserQuestionTimeout set, the interface is told how long the
// model's questions may wait without a key; when they close by
// themselves (the Choice AskedAway), the model reads what was picked by
// then and that the person may be away, and goes on.
func TestAskUserQuestionAway(t *testing.T) {
	w := apptest.NewWorld(t, askCall(twoQuestions), apptest.Reply{Text: "Going on with SQLite."})
	w.Settings(`{"options": {"askUserQuestionTimeout": "5m"}}`)
	var info SessionInfo
	_, r := attach(t, w, nil, func(ctx context.Context, c SessionControls, f *scripted) int {
		info = c.Info()
		if _, err := c.Submit(agent.Prompt{Text: "ask me"}); err != nil {
			return 1
		}
		q := find(f, "the question", func(e *agent.QuestionEvent) bool { return true }).Question
		ans := agent.Answer{Choice: AskedAway, Data: map[string]string{
			"Which database should the service use?":  "SQLite",
			"Which checks should run before a merge?": "",
		}}
		if err := c.Answer(q.ID, ans); err != nil {
			t.Errorf("Answer: %v", err)
		}
		_ = c.Wait(ctx)
		c.Quit("prompt_input_exit")
		return 0
	})
	r.wantCode(t, 0)
	if info.AskTimeout != 5*time.Minute {
		t.Errorf("the interface was told %v, want 5m", info.AskTimeout)
	}
	got := toolResult(t, w, 1, "c1")
	has(t, "what the model read", got, "Nobody answered the questions in time", "may be away", "Answer: SQLite", "Go on with your own judgment")
	hasNot(t, "what the model read", got, "Which checks should run before a merge?")
}
