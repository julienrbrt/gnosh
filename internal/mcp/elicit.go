package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
)

// This file is elicitation (MCP's elicitation/create, as Claude Code
// answers it): a server asks the person for a few values in the middle of
// a tool call: a message and a flat schema of the fields it wants, each a
// string, a number, an integer, a boolean or one of a list (an enum). The
// client declares the capability only when it is given someone to ask
// (Options.OnElicit), and answers accept with the values, decline, or
// cancel.

// Elicitation is what a server asks for.
type Elicitation struct {
	// Message says what it asks and why.
	Message string
	// Fields are what it asks for, in the order its schema lists them.
	Fields []ElicitField
	// Schema is the schema of the fields as the server sent it (for the
	// Elicitation hooks).
	Schema json.RawMessage
}

// ElicitField is one value an Elicitation asks for.
type ElicitField struct {
	// Name is the field's key, Title and Description what the server says
	// of it.
	Name, Title, Description string
	// Type is "string", "number", "integer" or "boolean"; Enum, when not
	// empty, lists the strings it may be (EnumNames shows them).
	Type      string
	Enum      []string
	EnumNames []string
	// Required says it must be given.
	Required bool
	// Minimum and Maximum bound a number; MinLength and MaxLength a
	// string. Nil leaves it unbounded.
	Minimum, Maximum     *float64
	MinLength, MaxLength *int
	// Format is a string's format: email, uri, date or date-time.
	Format string
	// Default is the value the server suggests, as JSON.
	Default json.RawMessage
}

// ElicitResult is the answer: Action is "accept", "decline" or "cancel",
// and Content the values, by field name, when it is accept.
type ElicitResult struct {
	Action  string         `json:"action"`
	Content map[string]any `json:"content,omitempty"`
}

// maxElicitFields is the most values a server may ask for at once.
const maxElicitFields = 20

// readElicitation reads the params of an elicitation/create request. A
// schema that is not a flat object of the kinds of field MCP allows, or
// that asks for more than maxElicitFields values, is refused, with why.
func readElicitation(raw json.RawMessage) (Elicitation, error) {
	var p struct {
		Message string `json:"message"`
		Schema  struct {
			Type       string                     `json:"type"`
			Properties map[string]json.RawMessage `json:"properties"`
			Required   []string                   `json:"required"`
		} `json:"requestedSchema"`
	}
	var whole struct {
		Schema json.RawMessage `json:"requestedSchema"`
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return Elicitation{}, fmt.Errorf("the request cannot be read: %w", err)
	}
	_ = json.Unmarshal(raw, &whole)
	if p.Schema.Type != "" && p.Schema.Type != "object" {
		return Elicitation{}, fmt.Errorf("its schema is a %s, not an object of fields", p.Schema.Type)
	}
	// The fields in the order the schema writes them.
	order, err := keysInOrder(raw)
	if err != nil {
		return Elicitation{}, err
	}
	if len(order) > maxElicitFields {
		return Elicitation{}, fmt.Errorf("it asks for %d values, and gnosh asks for at most %d at once", len(order), maxElicitFields)
	}
	required := map[string]bool{}
	for _, r := range p.Schema.Required {
		required[r] = true
	}
	e := Elicitation{Message: p.Message, Schema: whole.Schema}
	for _, name := range order {
		var f struct {
			Type        string          `json:"type"`
			Title       string          `json:"title"`
			Description string          `json:"description"`
			Enum        []string        `json:"enum"`
			EnumNames   []string        `json:"enumNames"`
			Minimum     *float64        `json:"minimum"`
			Maximum     *float64        `json:"maximum"`
			MinLength   *int            `json:"minLength"`
			MaxLength   *int            `json:"maxLength"`
			Format      string          `json:"format"`
			Default     json.RawMessage `json:"default"`
		}
		if err := json.Unmarshal(p.Schema.Properties[name], &f); err != nil {
			return Elicitation{}, fmt.Errorf("the field %q cannot be read: %w", name, err)
		}
		switch f.Type {
		case "string", "number", "integer", "boolean":
		default:
			return Elicitation{}, fmt.Errorf("the field %q is of the type %q, and a server may ask only for a string, a number, an integer or a boolean", name, f.Type)
		}
		e.Fields = append(e.Fields, ElicitField{Name: name, Title: f.Title, Description: f.Description, Type: f.Type, Enum: f.Enum, EnumNames: f.EnumNames,
			Required: required[name], Minimum: f.Minimum, Maximum: f.Maximum, MinLength: f.MinLength, MaxLength: f.MaxLength, Format: f.Format, Default: f.Default})
	}
	return e, nil
}

// keysInOrder returns the keys of requestedSchema.properties in the
// order the request writes them, each once.
func keysInOrder(raw json.RawMessage) ([]string, error) {
	var p struct {
		Schema struct {
			Properties json.RawMessage `json:"properties"`
		} `json:"requestedSchema"`
	}
	if err := json.Unmarshal(raw, &p); err != nil || len(p.Schema.Properties) == 0 {
		return nil, nil
	}
	dec := json.NewDecoder(bytes.NewReader(p.Schema.Properties))
	if t, err := dec.Token(); err != nil || t != json.Delim('{') {
		return nil, fmt.Errorf("the schema's properties are not an object")
	}
	var out []string
	seen := map[string]bool{}
	for dec.More() {
		t, err := dec.Token()
		if err != nil {
			return nil, err
		}
		key, _ := t.(string)
		if !seen[key] {
			seen[key] = true
			out = append(out, key)
		}
		var skip json.RawMessage
		if err := dec.Decode(&skip); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// elicited answers an elicitation/create request: with OnElicit's answer,
// or with an error when the request cannot be asked. ctx ends when the
// server takes the request back or the connection ends, which withdraws
// the question.
func (c *Client) elicited(ctx context.Context, m message) message {
	reply := message{JSONRPC: "2.0", ID: m.ID}
	e, err := readElicitation(m.Params)
	if err != nil {
		reply.Error = &RPCError{Code: -32602, Message: "gnosh cannot ask this: " + err.Error()}
		return reply
	}
	res, err := c.opt.OnElicit(ctx, e)
	if err != nil {
		reply.Error = &RPCError{Code: -32603, Message: err.Error()}
		return reply
	}
	switch res.Action {
	case "accept", "decline", "cancel":
	default:
		res = ElicitResult{Action: "cancel"}
	}
	if res.Action != "accept" {
		res.Content = nil
	}
	data, err := json.Marshal(res)
	if err != nil {
		// (A value JSON cannot hold, such as NaN: no answer at all would
		// leave the server waiting.)
		reply.Error = &RPCError{Code: -32603, Message: "gnosh could not write the answer: " + err.Error()}
		return reply
	}
	reply.Result = data
	return reply
}

// idKey is a JSON-RPC id as a key: its JSON, without spaces.
func idKey(raw json.RawMessage) string {
	var b bytes.Buffer
	if json.Compact(&b, raw) != nil {
		return string(raw)
	}
	return b.String()
}
