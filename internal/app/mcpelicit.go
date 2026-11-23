package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/mail"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gnosh-ai/gnosh/internal/agent"
	"github.com/gnosh-ai/gnosh/internal/hooks"
	"github.com/gnosh-ai/gnosh/internal/mcp"
)

// This file puts an MCP server's elicitation to the person (mcp/elicit.go),
// as Claude Code does: the server's message and one question for each field
// it asks for, in the form the model's own questions use (a boolean is Yes
// or No, a field with a list of values offers them, anything else is typed
// in). What comes back is held to the field's kind and bounds; an answer
// that does not fit is said and asked again, twice at most, and then the
// request is declined. Esc on the form cancels it and the turn goes on (the
// server decides what follows), stopping the turn takes the form back (and
// cancels it), and a run with nobody to ask declines it. The server's
// message stands above the questions, under the server's name.
//
// The Elicitation hooks run first, and may answer in the person's place
// (in a run too, which then declares the capability); the
// ElicitationResult hooks run on the person's answer, and may put another
// in its place. Values a hook gives are held to the fields as the
// person's are.

// elicitTries is how many times the form is put before a request whose
// answers do not fit is declined.
const elicitTries = 3

// mcpElicit is the elicitation handler of the server s.
func (l *live) mcpElicit(s *mcpServer) func(context.Context, mcp.Elicitation) (mcp.ElicitResult, error) {
	name := s.cfg.Name
	return func(ctx context.Context, e mcp.Elicitation) (mcp.ElicitResult, error) {
		if l.agent == nil {
			return mcp.ElicitResult{Action: "decline"}, nil
		}
		if calls := s.inFlight(); len(calls) > 0 {
			// What a server asks while calls of its tools run is about
			// them: once every one of them has ended (finished, or
			// stopped with the turn or the sub-agent that made it), the
			// question is taken back.
			var cancel context.CancelFunc
			ctx, cancel = context.WithCancel(ctx)
			defer cancel()
			go func() {
				for _, c := range calls {
					select {
					case <-c.Done():
					case <-ctx.Done():
						return
					}
				}
				cancel()
			}()
		}
		if res, done := l.elicitHooks(ctx, name, e); done {
			return res, nil
		}
		if !l.interactive {
			return mcp.ElicitResult{Action: "decline"}, nil
		}
		res := l.elicitPerson(ctx, name, e)
		if ctx.Err() != nil {
			// Taken back before it was answered: no answer to look over.
			return res, nil
		}
		return l.elicitResultHooks(ctx, name, e, res), nil
	}
}

// elicitPerson puts what the server name asks for to the person, until
// ctx ends.
func (l *live) elicitPerson(ctx context.Context, name string, e mcp.Elicitation) mcp.ElicitResult {
	form := elicitForm(e)
	form.Lead = "The MCP server " + name + " asks: " + oneLine(e.Message, 600)
	form.EscCancels = true
	title := "The MCP server " + name + " asks for something"
	if len(e.Fields) == 0 {
		title = "The MCP server " + name + " asks you to confirm"
	}
	for try := 1; ; try++ {
		ans, err := l.agent.Ask(ctx, agent.Question{Kind: questionKind, Title: title, Text: oneLine(e.Message, 600), Data: form})
		switch {
		case errors.Is(err, agent.ErrNobodyToAsk):
			return mcp.ElicitResult{Action: "decline"}
		case err != nil:
			return mcp.ElicitResult{Action: "cancel"}
		}
		if ans.Choice == "cancel" {
			return mcp.ElicitResult{Action: "cancel"}
		}
		values, _ := ans.Data.(map[string]string)
		if len(values) == 0 && strings.TrimSpace(ans.Text) == "" && len(e.Fields) > 0 {
			return mcp.ElicitResult{Action: "cancel"}
		}
		if len(e.Fields) == 0 && strings.TrimSpace(values[form.Questions[0].Question]) != "Yes" {
			// No to a confirmation is an answer: the request is declined.
			return mcp.ElicitResult{Action: "decline"}
		}
		content, why := elicitContent(e.Fields, form, values)
		if why == "" {
			l.flog.logf("mcp", "the MCP server %s was given what it asked for (%d fields)", name, len(content))
			return mcp.ElicitResult{Action: "accept", Content: content}
		}
		if try >= elicitTries {
			l.notice(agent.LevelWarning, "mcp", fmt.Sprintf("What the MCP server %s asked for was declined: %s.", name, why))
			return mcp.ElicitResult{Action: "decline"}
		}
		l.notice(agent.LevelWarning, "mcp", fmt.Sprintf("That does not fit what the MCP server %s asked for: %s. It is asked again.", name, why))
	}
}

// elicitHooks runs the Elicitation hooks on what the server name asks
// for. done says they settled it in the person's place: answered,
// declined or cancelled.
func (l *live) elicitHooks(ctx context.Context, name string, e mcp.Elicitation) (res mcp.ElicitResult, done bool) {
	if !l.hooks.has(hooks.Elicitation) {
		return mcp.ElicitResult{}, false
	}
	out, err := l.hooks.run(ctx, hooks.Input{Event: hooks.Elicitation, MCPServerName: name, Message: e.Message, Mode: "form", RequestedSchema: e.Schema})
	switch {
	case ctx.Err() != nil:
		// Taken back while the hooks ran (the call ended, or the server
		// took it back): nobody is told anything.
		return mcp.ElicitResult{Action: "cancel"}, true
	case err != nil:
		l.notice(agent.LevelWarning, "hooks", fmt.Sprintf("The Elicitation hooks could not be run (%v), so what the MCP server %s asked for was declined.", err, name))
		return mcp.ElicitResult{Action: "decline"}, true
	}
	if out.ElicitAction == "" {
		return mcp.ElicitResult{}, false
	}
	if out.ElicitContent == nil {
		out.ElicitContent = json.RawMessage("{}")
	}
	return l.elicitByHook(hooks.Elicitation, name, e, out), true
}

// elicitResultHooks runs the ElicitationResult hooks on the person's
// answer res, and gives the answer that goes to the server name.
func (l *live) elicitResultHooks(ctx context.Context, name string, e mcp.Elicitation, res mcp.ElicitResult) mcp.ElicitResult {
	if !l.hooks.has(hooks.ElicitationResult) {
		return res
	}
	var content json.RawMessage
	if res.Action == "accept" {
		content, _ = json.Marshal(res.Content)
	}
	out, err := l.hooks.run(ctx, hooks.Input{Event: hooks.ElicitationResult, MCPServerName: name, Action: res.Action, Mode: "form", Content: content})
	switch {
	case ctx.Err() != nil:
		return mcp.ElicitResult{Action: "cancel"}
	case err != nil:
		l.notice(agent.LevelWarning, "hooks", fmt.Sprintf("The ElicitationResult hooks could not be run (%v), so the answer to the MCP server %s was declined.", err, name))
		return mcp.ElicitResult{Action: "decline"}
	case out.ElicitAction == "":
		return res
	}
	if out.ElicitContent == nil {
		// An accept that gives no values keeps the person's.
		out.ElicitContent = content
		if out.ElicitContent == nil {
			out.ElicitContent = json.RawMessage("{}")
		}
	}
	return l.elicitByHook(hooks.ElicitationResult, name, e, out)
}

// elicitByHook is the answer the hooks of the event gave to what the
// server name asked for: their values are held to the fields, and one
// that does not fit declines.
func (l *live) elicitByHook(event hooks.Event, name string, e mcp.Elicitation, out hooks.Outcome) mcp.ElicitResult {
	switch out.ElicitAction {
	case "decline":
		why := ""
		if r := strings.TrimSpace(out.Reason); r != "" {
			why = " " + oneLine(r, 300)
		}
		l.notice(agent.LevelWarning, "hooks", fmt.Sprintf("The %s hooks declined what the MCP server %s asked for.%s", event, name, why))
		return mcp.ElicitResult{Action: "decline"}
	case "cancel":
		l.flog.logf("hooks", "the %s hooks cancelled what the MCP server %s asked for", event, name)
		return mcp.ElicitResult{Action: "cancel"}
	}
	content, why := elicitHookValues(e, out.ElicitContent)
	if why != "" {
		l.notice(agent.LevelWarning, "hooks", fmt.Sprintf("The %s hooks answered the MCP server %s with values that do not fit what it asked for (%s), so the request was declined.", event, name, why))
		return mcp.ElicitResult{Action: "decline"}
	}
	l.flog.logf("hooks", "the %s hooks answered the MCP server %s (%d fields)", event, name, len(content))
	return mcp.ElicitResult{Action: "accept", Content: content}
}

// elicitHookValues holds the values a hook gave (a JSON object, by field
// name) to the fields, as the person's answers are held, and gives them
// in each field's kind; values for fields that were not asked for are
// left out.
func elicitHookValues(e mcp.Elicitation, raw json.RawMessage) (map[string]any, string) {
	var given map[string]any
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&given); err != nil {
		return nil, "they are not a JSON object"
	}
	if len(e.Fields) == 0 {
		return map[string]any{}, ""
	}
	form := elicitForm(e)
	values := map[string]string{}
	for i, f := range e.Fields {
		var s string
		switch v := given[f.Name].(type) {
		case nil:
		case string:
			s = v
		case bool:
			s = strconv.FormatBool(v)
		case json.Number:
			s = v.String()
		default:
			return nil, "the value of " + firstOf(f.Title, f.Name) + " is not one a form takes"
		}
		if s == "" && given[f.Name] != nil && f.Required {
			return nil, firstOf(f.Title, f.Name) + " is needed"
		}
		values[form.Questions[i].Question] = s
	}
	return elicitContent(e.Fields, form, values)
}

// elicitForm is the form an elicitation is put as: one question for each
// field, the field's name as its tab.
func elicitForm(e mcp.Elicitation) QuestionForm {
	var form QuestionForm
	asked := map[string]bool{}
	for _, f := range e.Fields {
		q := FormQuestion{Question: elicitQuestion(f), Header: elicitHeader(f.Name)}
		if asked[q.Question] {
			// (The answers are keyed by the question: two fields that read
			// alike are told apart by their names.)
			q.Question += " [" + oneLine(f.Name, 40) + "]"
		}
		asked[q.Question] = true
		switch {
		case f.Type == "boolean":
			q.Options, q.NoOther = []FormOption{{Label: "Yes"}, {Label: "No"}}, true
		case len(f.Enum) > 0:
			q.NoOther = true
			for i, v := range f.Enum {
				label := v
				if i < len(f.EnumNames) && strings.TrimSpace(f.EnumNames[i]) != "" {
					label = f.EnumNames[i]
				}
				q.Options = append(q.Options, FormOption{Label: oneLine(label, 80)})
			}
		}
		form.Questions = append(form.Questions, q)
	}
	if len(form.Questions) == 0 {
		// Nothing to fill in: the question is whether to go on.
		form.Questions = []FormQuestion{{Question: "Go on?", Header: "Confirm", Options: []FormOption{{Label: "Yes"}, {Label: "No"}}, NoOther: true}}
	}
	return form
}

// elicitQuestion is a field's question: its title or name, what the server
// says of it, and what kind of answer it takes.
func elicitQuestion(f mcp.ElicitField) string {
	q := oneLine(firstOf(f.Title, f.Name), 120)
	if d := oneLine(f.Description, 300); d != "" {
		q += ": " + d
	}
	var kind []string
	switch f.Type {
	case "number":
		kind = append(kind, "a number")
	case "integer":
		kind = append(kind, "a whole number")
	}
	if f.Format != "" && f.Type == "string" {
		kind = append(kind, "a "+f.Format)
	}
	if f.Minimum != nil {
		kind = append(kind, "at least "+strconv.FormatFloat(*f.Minimum, 'g', -1, 64))
	}
	if f.Maximum != nil {
		kind = append(kind, "at most "+strconv.FormatFloat(*f.Maximum, 'g', -1, 64))
	}
	if !f.Required {
		kind = append(kind, "may be left empty")
	}
	if len(kind) > 0 {
		q += " (" + strings.Join(kind, ", ") + ")"
	}
	return q
}

// elicitHeader is a field's tab: its name, cut to twelve characters.
func elicitHeader(name string) string {
	if utf8.RuneCountInString(name) <= 12 {
		return name
	}
	return string([]rune(name)[:11]) + "…"
}

// elicitContent turns the answers into the values the server asked for,
// each of its field's kind, or says why one does not fit.
func elicitContent(fields []mcp.ElicitField, form QuestionForm, values map[string]string) (map[string]any, string) {
	content := map[string]any{}
	if len(fields) == 0 {
		// A confirmation: Yes accepts.
		if strings.TrimSpace(values[form.Questions[0].Question]) != "Yes" {
			return nil, "it was not confirmed"
		}
		return content, ""
	}
	for i, f := range fields {
		q := form.Questions[i]
		got := strings.TrimSpace(values[q.Question])
		name := firstOf(f.Title, f.Name)
		if got == "" {
			if f.Required {
				return nil, name + " is needed"
			}
			continue
		}
		switch f.Type {
		case "boolean":
			switch strings.ToLower(got) {
			case "yes", "true":
				content[f.Name] = true
			case "no", "false":
				content[f.Name] = false
			default:
				return nil, name + " is Yes or No"
			}
		case "number", "integer":
			n, err := strconv.ParseFloat(got, 64)
			if err != nil || math.IsNaN(n) || math.IsInf(n, 0) || f.Type == "integer" && n != float64(int64(n)) {
				return nil, name + " is " + map[string]string{"number": "a number", "integer": "a whole number"}[f.Type]
			}
			if f.Minimum != nil && n < *f.Minimum || f.Maximum != nil && n > *f.Maximum {
				return nil, name + " is out of its bounds"
			}
			if f.Type == "integer" {
				content[f.Name] = int64(n)
			} else {
				content[f.Name] = n
			}
		default:
			value := got
			if len(f.Enum) > 0 {
				value = ""
				for j, v := range f.Enum {
					label := v
					if j < len(f.EnumNames) && strings.TrimSpace(f.EnumNames[j]) != "" {
						label = f.EnumNames[j]
					}
					if got == oneLine(label, 80) || got == v {
						value = v
					}
				}
				if value == "" {
					return nil, name + " is one of " + strings.Join(f.Enum, ", ")
				}
			}
			n := utf8.RuneCountInString(value)
			if f.MinLength != nil && n < *f.MinLength || f.MaxLength != nil && n > *f.MaxLength {
				return nil, name + " is not of the length it takes"
			}
			if why := elicitFormat(f.Format, value); why != "" {
				return nil, name + " " + why
			}
			content[f.Name] = value
		}
	}
	return content, ""
}

// elicitFormat says why a string is not of its format, or "".
func elicitFormat(format, v string) string {
	switch format {
	case "email":
		if a, err := mail.ParseAddress(v); err != nil || a.Address != v {
			return "is an email address"
		}
	case "uri":
		if u, err := url.Parse(v); err != nil || u.Scheme == "" {
			return "is an address with a scheme (https://...)"
		}
	case "date":
		if _, err := time.Parse("2006-01-02", v); err != nil {
			return "is a date, as 2026-11-18"
		}
	case "date-time":
		if _, err := time.Parse(time.RFC3339, v); err != nil {
			return "is a date and time, as 2026-11-18T09:30:00Z"
		}
	}
	return ""
}
