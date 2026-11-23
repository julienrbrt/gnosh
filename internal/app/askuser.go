package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/gnosh-ai/gnosh/internal/agent"
	"github.com/gnosh-ai/gnosh/internal/core"
)

// This file is the AskUserQuestion tool: how a model puts questions to the
// person. Its name, its arguments and its structured result are Claude
// Code's (spec/cc/tools.md, section 12); its words are gnosh's own.
//
// The tool puts one question to the person through the loop (agent.Ask),
// of the kind "question", with the model's questions in Question.Data as a
// QuestionForm. What a front end makes of that is its own business: the
// full interface shows a tab for each question and a last one to look the
// answers over, the plain session asks them one after another in numbered
// text. The answer comes back in Answer.Data as a map from each question's
// text to what was answered: the label of the option picked, the labels
// joined with ", " when several were, or the person's own words.
//
// Nothing approves the tool on the person's behalf: no mode, no rule. A
// PreToolUse hook can answer for them, by allowing the call with arguments
// that carry the answers (spec/cc/hooks.md); answers a model writes into
// its own call are not read. In a run nobody attends the questions are not
// put, and the result says so in words a model can act on. A sub-agent has
// no such tool.

// askToolName is the name of the tool, which is Claude Code's.
const askToolName = "AskUserQuestion"

// questionKind is the kind of the question the tool puts.
const questionKind = "question"

// AskedAway is the Choice of the answer an interface gives for the model's
// questions when they went without a key for the askUserQuestionTimeout
// option's time (SessionInfo.AskTimeout): its Data holds what was picked
// by then, and the model goes on without the person.
const AskedAway = "away"

// askIdleOf reads the askUserQuestionTimeout option: 60s, 5m or 10m, and
// otherwise none.
func askIdleOf(v string) time.Duration {
	switch v {
	case "60s":
		return time.Minute
	case "5m":
		return 5 * time.Minute
	case "10m":
		return 10 * time.Minute
	}
	return 0
}

// QuestionForm is the Data of a question of the kind "question" that a
// model put with AskUserQuestion: its questions, as the model wrote them.
// There are one to four, each with two to four options, and each can also
// be answered in the person's own words.
type QuestionForm struct {
	Questions []FormQuestion
	// Lead stands above the questions: who asks and why, when that is not
	// the model (an MCP server's message).
	Lead string
	// EscCancels says Esc answers the form as cancelled, with the Choice
	// "cancel", and the turn goes on: the asker is told and decides what
	// follows, as an MCP server is. Esc on the model's own questions stops
	// the turn.
	EscCancels bool
}

// FormQuestion is one question of a QuestionForm.
type FormQuestion struct {
	// Question is the question in full, and Header a very short name for
	// it: a tab's label.
	Question string
	Header   string
	// Options are the answers offered.
	Options []FormOption
	// MultiSelect says any number of the options may be chosen.
	MultiSelect bool
	// NoOther says the question takes only its options, and no answer in
	// the person's own words (an MCP server's list of values, or a Yes or
	// No).
	NoOther bool
}

// FormOption is one answer a FormQuestion offers.
type FormOption struct {
	// Label is the answer, in a few words, and Description what choosing
	// it means.
	Label       string
	Description string
	// Preview is something to look at while the option is considered: a
	// sketch of the code or the layout it stands for. It may be empty.
	Preview string
}

const askDescription = `Puts one to four questions to the person at the terminal and waits for their answers. It is for a choice that is theirs to make and that the code, the files and what they have already said do not settle: which of several designs they want, what an unclear request means, which of two costly paths to take. It is not for asking leave to run a tool (gnosh asks them itself when leave is needed), and not for asking whether to go on with what they asked for.

Ask everything you need in one call: the questions are shown together, each with its options. The person picks an option, or several when multiSelect is true, or answers in their own words instead.

- question: the question in full. Every question in a call must read differently, because the answers come back by question.
- header: a very short name for the question, twelve characters at most. It is the question's tab.
- options: two to four answers. label is one to five words; description says what choosing it means or leads to. Do not add an option for "other" or "none of these": the person can always answer in their own words.
- multiSelect: true when more than one option may be chosen.

The result gives each question with what the person answered. Their answer is an instruction: follow it. When nobody is at the terminal (a run started with -p), the questions are not put and the result says so; decide with what you know and say what you assumed.`

var askSchema = json.RawMessage(`{"type":"object","properties":{"questions":{"type":"array","minItems":1,"maxItems":4,"description":"One to four questions, shown together.","items":{"type":"object","properties":{"question":{"type":"string","description":"The question in full."},"header":{"type":"string","description":"A very short name for the question, twelve characters at most."},"options":{"type":"array","minItems":2,"maxItems":4,"description":"Two to four answers. The person can always answer in their own words as well.","items":{"type":"object","properties":{"label":{"type":"string","description":"The answer, in one to five words."},"description":{"type":"string","description":"What choosing it means or leads to."},"preview":{"type":"string","description":"Optional: something to look at beside the option, such as a sketch of the code it stands for."}},"required":["label","description"],"additionalProperties":false}},"multiSelect":{"type":"boolean","description":"True when more than one option may be chosen."}},"required":["question","header","options","multiSelect"],"additionalProperties":false}}},"required":["questions"],"additionalProperties":false}`)

// askInput is the tool's arguments. The model writes the questions; the
// answers and the annotations are written by a hook that answers in the
// person's place, and by gnosh into the result.
type askInput struct {
	Questions   []askQuestion      `json:"questions"`
	Answers     map[string]string  `json:"answers,omitempty"`
	Annotations map[string]askNote `json:"annotations,omitempty"`
}

type askQuestion struct {
	Question    string      `json:"question"`
	Header      string      `json:"header"`
	Options     []askOption `json:"options"`
	MultiSelect bool        `json:"multiSelect"`
}

type askOption struct {
	Label       string `json:"label"`
	Description string `json:"description"`
	Preview     string `json:"preview,omitempty"`
}

// askNote is what the person added to one question.
type askNote struct {
	Preview string `json:"preview,omitempty"`
	Notes   string `json:"notes,omitempty"`
}

// askTool is the AskUserQuestion tool of one session.
type askTool struct {
	l *live
}

// Spec is what a model is told about the tool.
func (t *askTool) Spec() core.ToolSpec {
	return core.ToolSpec{Name: askToolName, Description: askDescription, InputSchema: askSchema}
}

// read reads a call's arguments and says, in words for the model, what is
// wrong with them when something is.
func (t *askTool) read(input json.RawMessage) (askInput, string) {
	var in askInput
	if err := json.Unmarshal(input, &in); err != nil {
		return in, fmt.Sprintf("the arguments cannot be read (%v). questions is a list of one to four objects, each with question, header, options (two to four objects with label and description) and multiSelect", err)
	}
	if n := len(in.Questions); n < 1 || n > 4 {
		return in, fmt.Sprintf("a call takes one to four questions, and this one has %d", n)
	}
	seen := map[string]bool{}
	for i := range in.Questions {
		q := &in.Questions[i]
		q.Question, q.Header = strings.TrimSpace(q.Question), strings.TrimSpace(q.Header)
		switch {
		case q.Question == "":
			return in, fmt.Sprintf("question %d has no text", i+1)
		case seen[q.Question]:
			return in, fmt.Sprintf("two of the questions read the same (%q), and the answers come back by question: word them differently", oneLine(q.Question, 80))
		case len(q.Options) < 2 || len(q.Options) > 4:
			return in, fmt.Sprintf("question %d has %d options, and a question takes two to four. The person can always answer in their own words, so leave out any option that only says \"other\"", i+1, len(q.Options))
		}
		seen[q.Question] = true
		labels := map[string]bool{}
		for j := range q.Options {
			o := &q.Options[j]
			o.Label = strings.TrimSpace(o.Label)
			switch {
			case o.Label == "":
				return in, fmt.Sprintf("option %d of question %d has no label", j+1, i+1)
			case labels[o.Label]:
				return in, fmt.Sprintf("question %d offers %q twice", i+1, oneLine(o.Label, 60))
			}
			labels[o.Label] = true
		}
	}
	return in, ""
}

// Describe says what a call would do. The call is itself a question to the
// person, so nothing is asked about it first.
func (t *askTool) Describe(call core.ToolCall, env *core.ToolEnv) (core.Action, error) {
	in, wrong := t.read(call.Input)
	act := core.Action{Kind: core.ActionOther, Summary: "Ask the person a question"}
	if wrong != "" {
		return act, errors.New(wrong)
	}
	if n := len(in.Questions); n > 1 {
		act.Summary = fmt.Sprintf("Ask the person %d questions", n)
	}
	return act, nil
}

// form is the questions as a front end is handed them.
func (in askInput) form() QuestionForm {
	var f QuestionForm
	for _, q := range in.Questions {
		fq := FormQuestion{Question: q.Question, Header: q.Header, MultiSelect: q.MultiSelect}
		for _, o := range q.Options {
			fq.Options = append(fq.Options, FormOption(o))
		}
		f.Questions = append(f.Questions, fq)
	}
	return f
}

// Run puts the questions and returns the answers.
func (t *askTool) Run(ctx context.Context, call core.ToolCall, env *core.ToolEnv) (core.ToolResult, error) {
	in, wrong := t.read(call.Input)
	if wrong != "" {
		return core.ErrorResult("The questions were not put: " + wrong + "."), nil
	}
	// Answers in the arguments are the person's only when a hook they set
	// up put them there. What a model writes into its own call is not an
	// answer.
	_, byHook := t.l.hookChanged.LoadAndDelete(call.ID)
	if byHook && len(in.Answers) > 0 {
		return t.result(in, in.Answers, "", "a hook the user set up answered in their place"), nil
	}
	in.Answers, in.Annotations = nil, nil

	a := agent.FromContext(ctx)
	if a == nil {
		return core.ToolResult{}, errors.New("the question tool was run outside a session")
	}
	title := "The model has a question"
	if n := len(in.Questions); n > 1 {
		title = fmt.Sprintf("The model has %d questions", n)
	}
	ans, err := a.Ask(ctx, agent.Question{Kind: questionKind, Title: title, CallID: call.ID, Tool: askToolName, Data: in.form()})
	switch {
	case errors.Is(err, agent.ErrNobodyToAsk):
		return core.ErrorResult("The questions were not put: nobody is at the terminal in this run, so nobody could answer. Do not ask again. Decide with what you know and say in your answer what you assumed; or, if the choice is too large to make for them, stop and say what you need to be told."), nil
	case err != nil:
		return core.ToolResult{}, err
	}
	answers, _ := ans.Data.(map[string]string)
	if ans.Choice == AskedAway {
		return t.away(in, answers), nil
	}
	return t.result(in, answers, strings.TrimSpace(ans.Text), ""), nil
}

// away is the result of questions that closed by themselves, nobody having
// touched a key for the askUserQuestionTimeout option's time: what was
// picked by then, and that the model goes on without the person.
func (t *askTool) away(in askInput, answers map[string]string) core.ToolResult {
	kept := map[string]string{}
	var b strings.Builder
	b.WriteString("Nobody answered the questions in time: the user may be away from the keyboard.")
	for i, q := range in.Questions {
		got := strings.TrimSpace(answers[q.Question])
		if got == "" {
			continue
		}
		if len(kept) == 0 {
			b.WriteString(" What they had picked by then:\n")
		}
		kept[q.Question] = got
		fmt.Fprintf(&b, "%d. %s\n   Answer: %s\n", i+1, q.Question, got)
	}
	b.WriteString("\nGo on with your own judgment for what is not answered, say in your answer what you assumed, and ask again later if it still matters.")
	meta := map[string]any{"questions": in.Questions, "answers": kept, "timedOut": true}
	return core.ToolResult{Content: []core.Block{core.Text(b.String())}, Meta: meta}
}

// result writes the tool's result: what the model reads, and Claude
// Code's structured form of it for hooks and the transcript. said is what
// the person typed when they answered none of the questions as put.
func (t *askTool) result(in askInput, answers map[string]string, said, who string) core.ToolResult {
	kept := map[string]string{}
	var b strings.Builder
	for i, q := range in.Questions {
		got := strings.TrimSpace(answers[q.Question])
		if got == "" {
			continue
		}
		kept[q.Question] = got
		if b.Len() == 0 {
			b.WriteString("The user answered")
			if who != "" {
				b.WriteString(" (" + who + ")")
			}
			b.WriteString(":\n")
		}
		fmt.Fprintf(&b, "%d. %s\n   Answer: %s\n", i+1, q.Question, got)
	}
	meta := map[string]any{"questions": in.Questions, "answers": kept}
	if len(in.Annotations) > 0 {
		meta["annotations"] = in.Annotations
	}
	switch {
	case len(kept) == 0 && said != "":
		// They answered in their own way, without taking the questions
		// one by one.
		meta["response"] = said
		return core.ToolResult{Content: []core.Block{core.Text("The user did not answer the questions as put. They replied:\n" + said)}, Meta: meta}
	case len(kept) == 0:
		return core.ToolResult{Content: []core.Block{core.Text("The user closed the questions without answering any of them. Do not put the same questions again: go on with what you know and say what you assumed, or say in plain words what you need to be told.")}, Meta: meta}
	}
	if left := len(in.Questions) - len(kept); left > 0 {
		fmt.Fprintf(&b, "They left %d of the %d questions unanswered: decide those yourself, and say what you chose.\n", left, len(in.Questions))
	}
	return core.ToolResult{Content: []core.Block{core.Text(strings.TrimRight(b.String(), "\n"))}, Meta: meta}
}
