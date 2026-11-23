package ui

import (
	"testing"
	"time"

	"github.com/gnosh-ai/gnosh/internal/agent"
	"github.com/gnosh-ai/gnosh/internal/app"
	"github.com/gnosh-ai/gnosh/internal/term"
	"github.com/gnosh-ai/gnosh/internal/text"
)

// With askUserQuestionTimeout set, the model's questions close by
// themselves when no key has been pressed for that long, counting from
// the last key: the screen counts down for the last twenty seconds, and
// what was picked by then goes as the answer, with the word that the
// person seems away. Permission questions and a server's form wait.
func TestModelQuestionsCloseWhenNobodyAnswers(t *testing.T) {
	g := newRig(t, 80, 30, text.NoColor, func(c *controls) { c.info.AskTimeout = time.Minute })
	g.keys(`"ask me" <Enter>`).start()
	data := app.QuestionForm{Questions: []app.FormQuestion{
		{Question: "Which database?", Header: "Database", Options: []app.FormOption{{Label: "SQLite"}, {Label: "Postgres"}}},
		{Question: "Which checks?", Header: "Checks", Options: []app.FormOption{{Label: "vet"}, {Label: "race"}}, MultiSelect: true},
	}}
	g.ev(&agent.QuestionEvent{Question: agent.Question{ID: "q1", Kind: "question", Tool: "AskUserQuestion", Title: "Questions", Data: data}})
	g.wait(30 * time.Second)
	g.liveLacks("goes on without you")
	g.keys(`<Down> <Enter>`) // Postgres; the timer starts again
	g.wait(45 * time.Second).liveHas("goes on without you in 15s")
	if len(g.c.answers) != 0 {
		t.Fatalf("answered too soon: %+v", g.c.answers)
	}
	g.wait(16 * time.Second)
	if len(g.c.answers) != 1 || g.c.answers[0].ans.Choice != app.AskedAway {
		t.Fatalf("the questions did not close by themselves: %+v", g.c.answers)
	}
	got, _ := g.c.answers[0].ans.Data.(map[string]string)
	if got["Which database?"] != "Postgres" || got["Which checks?"] != "" {
		t.Errorf("what was picked by then: %v", got)
	}
	g.liveLacks("Which database?").committedHas("no answer in 1m 00s: the model goes on without you", "Database: Postgres")

	// A permission question, and a server's form, wait for their answer.
	g.ev(&agent.AnswerEvent{ID: "q1"})
	g.ev(&agent.QuestionEvent{Question: permission("p1")})
	g.wait(10 * time.Minute).liveHas("Allow Bash?").liveLacks("goes on without you")
	g.keys(`<Enter>`)
	g.ev(&agent.AnswerEvent{ID: "p1"})
	form := app.QuestionForm{Lead: "The MCP server deploy asks: Where?", EscCancels: true, Questions: data.Questions[:1]}
	g.ev(&agent.QuestionEvent{Question: agent.Question{ID: "q2", Kind: "question", Title: "deploy", Data: form}})
	g.wait(10 * time.Minute)
	if len(g.c.answers) != 2 {
		t.Fatalf("a question that waits was closed: %+v", g.c.answers)
	}
}

// The person is there when they read the transcript view or come back from
// Ctrl+Z, though no key reaches the question: the time it may go without
// one starts again.
func TestModelQuestionWaitsWhileThePersonReads(t *testing.T) {
	g := newRig(t, 80, 30, text.NoColor, func(c *controls) { c.info.AskTimeout = time.Minute })
	g.free = true // (the transcript view takes the whole screen)
	g.keys(`"ask me" <Enter>`).start()
	data := app.QuestionForm{Questions: []app.FormQuestion{
		{Question: "Which database?", Header: "Database", Options: []app.FormOption{{Label: "SQLite"}, {Label: "Postgres"}}},
	}}
	g.ev(&agent.QuestionEvent{Question: agent.Question{ID: "q1", Kind: "question", Tool: "AskUserQuestion", Title: "Questions", Data: data}})
	g.wait(50 * time.Second)
	g.s.input(term.Resume{})
	g.wait(50 * time.Second)
	if len(g.c.answers) != 0 {
		t.Fatalf("closed while the person was back from Ctrl+Z: %+v", g.c.answers)
	}
	g.keys(`<Ctrl+O>`)
	g.wait(50 * time.Second)
	g.keys(`"j"`)
	g.wait(50 * time.Second)
	if len(g.c.answers) != 0 {
		t.Fatalf("closed while the person read the transcript: %+v", g.c.answers)
	}
	g.wait(11 * time.Second)
	if len(g.c.answers) != 1 || g.c.answers[0].ans.Choice != app.AskedAway {
		t.Fatalf("the question did not close after a minute without the person: %+v", g.c.answers)
	}
}
