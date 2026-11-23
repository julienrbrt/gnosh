package llm

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"iter"
	"net/http"
	"strings"

	"github.com/gnosh-ai/gnosh/internal/core"
)

// openAI speaks the chat-completions format. With the provider's kind set
// to llamacpp it also writes and reads what llama.cpp's server adds.
type openAI struct{ *provider }

var (
	_ core.Provider    = (*openAI)(nil)
	_ core.ModelLister = (*openAI)(nil)
)

// Stream sends req to the service's chat/completions endpoint and yields
// the answer's events.
func (o *openAI) Stream(ctx context.Context, req *core.Request) iter.Seq2[core.Event, error] {
	return o.stream(ctx, o, req)
}

func (o *openAI) streamURL() string { return o.endpoint("/chat/completions", nil) }

func (o *openAI) encode(req *core.Request) ([]byte, error) {
	body, err := o.chatBody(req, true)
	if err != nil {
		return nil, err
	}
	return o.encodeBody(body, req)
}

// encodeBody merges the configured and the request's own extra fields into
// body and writes it out.
func (p *provider) encodeBody(body obj, req *core.Request) ([]byte, error) {
	merge(body, p.extra)
	merge(body, req.Extra)
	out, err := encodeJSON(body)
	if err != nil {
		return nil, p.fail(core.ErrInvalidRequest, "the request cannot be written as JSON: %v", err)
	}
	return out, nil
}

// chatBody builds a chat-completions request body, before any extra
// fields are merged in.
func (o *openAI) chatBody(req *core.Request, stream bool) (obj, error) {
	body := obj{"messages": o.messages(req)}
	if req.Model != "" {
		body["model"] = req.Model
	}
	if stream {
		body["stream"] = true
		body["stream_options"] = obj{"include_usage": true}
	}
	if len(req.Tools) > 0 {
		tools := make([]obj, 0, len(req.Tools))
		for _, t := range req.Tools {
			fn := obj{"name": t.Name}
			if t.Description != "" {
				fn["description"] = t.Description
			}
			schema, err := o.schema(t)
			if err != nil {
				return nil, err
			}
			fn["parameters"] = schema
			tools = append(tools, obj{"type": "function", "function": fn})
		}
		body["tools"] = tools
	}
	if req.MaxTokens > 0 {
		body[o.maxTokensField] = req.MaxTokens
	}
	if req.Temperature != nil {
		body["temperature"] = *req.Temperature
	}
	if req.TopP != nil {
		body["top_p"] = *req.TopP
	}
	if len(req.Stop) > 0 {
		body["stop"] = req.Stop
	}
	if req.Thinking.Effort != "" {
		body["reasoning_effort"] = req.Thinking.Effort
	}
	if o.kind == TypeLlamaCpp {
		o.llamaFields(body, req)
		breakControlIn(body)
	}
	return body, nil
}

// schema returns a tool's argument schema as it goes into a request.
func (p *provider) schema(t core.ToolSpec) (json.RawMessage, error) {
	raw := bytes.TrimSpace(t.InputSchema)
	if len(raw) == 0 {
		return json.RawMessage(`{"type":"object","properties":{}}`), nil
	}
	if !json.Valid(raw) {
		return nil, p.fail(core.ErrInvalidRequest, "the input schema of the tool %q is not valid JSON", t.Name)
	}
	return json.RawMessage(raw), nil
}

// messages turns a request's system prompt and conversation into the
// format's message list.
func (o *openAI) messages(req *core.Request) []obj {
	var out []obj
	if sys := joinParts(req.System); sys != "" {
		out = append(out, obj{"role": "system", "content": sys})
	}
	names := make(map[string]string) // tool call id -> tool name
	for _, m := range req.Messages {
		if m.Role == core.RoleAssistant {
			out = append(out, o.assistantMessage(m, names))
		} else {
			out = append(out, o.userMessages(m, names)...)
		}
	}
	return out
}

// joinParts joins the parts of a system prompt that hold anything, with a
// blank line between them.
func joinParts(parts []string) string {
	var keep []string
	for _, s := range parts {
		if strings.TrimSpace(s) != "" {
			keep = append(keep, s)
		}
	}
	return strings.Join(keep, "\n\n")
}

// assistantMessage turns one assistant turn into one message: its text in
// content, its tool calls in tool_calls and, if thinking is kept, its
// reasoning in reasoning_content.
func (o *openAI) assistantMessage(m core.Message, names map[string]string) obj {
	var text, thinking strings.Builder
	var calls []obj
	for _, b := range m.Blocks {
		switch b.Type {
		case core.BlockText:
			text.WriteString(b.Text)
		case core.BlockThinking:
			if o.keepThinking && b.Text != "" {
				if thinking.Len() > 0 {
					thinking.WriteString("\n\n")
				}
				thinking.WriteString(b.Text)
			}
		case core.BlockToolUse:
			names[b.ID] = b.Name
			calls = append(calls, obj{
				"id":   b.ID,
				"type": "function",
				"function": obj{
					"name":      b.Name,
					"arguments": string(objectJSON(b.Input)),
				},
			})
		}
	}
	msg := obj{"role": "assistant", "content": text.String()}
	if thinking.Len() > 0 {
		msg["reasoning_content"] = thinking.String()
	}
	if len(calls) > 0 {
		msg["tool_calls"] = calls
	}
	return msg
}

// userMessages turns one user turn into the format's messages: a "tool"
// message for each tool result, first, because the format wants them right
// after the assistant message that made the calls; then one "user" message
// with everything else. The format's tool messages hold text only, so the
// images of a tool result travel in that user message, and the tool
// message says so. The format has no mark for a result that is a failure
// either: the result's own words have to say it.
func (o *openAI) userMessages(m core.Message, names map[string]string) []obj {
	var out []obj
	var fromTools, own []obj // content parts of the user message
	for _, b := range m.Blocks {
		switch b.Type {
		case core.BlockToolResult:
			text := core.TextOf(b.Content)
			if b.IsError && strings.TrimSpace(text) == "" {
				// The format has no field for a failure. A result
				// with words says it in its words; one without must
				// not read as a quiet success.
				text = "[The tool call failed and gave no details.]"
			}
			var images []obj
			for _, c := range b.Content {
				if c.Type == core.BlockImage {
					images = append(images, imagePart(c))
				}
			}
			if n := len(images); n > 0 {
				note := "[This result came with 1 image. It is in the next message.]"
				if n > 1 {
					note = fmt.Sprintf("[This result came with %d images. They are in the next message.]", n)
				}
				if text != "" {
					text += "\n\n"
				}
				text += note
				label := "Images from the result of tool call " + b.ToolUseID
				if n == 1 {
					label = "Image from the result of tool call " + b.ToolUseID
				}
				if name := names[b.ToolUseID]; name != "" {
					label += " (" + name + ")"
				}
				fromTools = append(fromTools, obj{"type": "text", "text": label + ":"})
				fromTools = append(fromTools, images...)
			}
			out = append(out, obj{"role": "tool", "tool_call_id": b.ToolUseID, "content": text})
		case core.BlockText:
			if b.Text != "" {
				own = append(own, obj{"type": "text", "text": b.Text})
			}
		case core.BlockImage:
			own = append(own, imagePart(b))
		}
	}
	parts := append(fromTools, own...)
	switch {
	case len(parts) > 0:
		out = append(out, obj{"role": "user", "content": userContent(parts)})
	case len(out) == 0:
		// A turn that says nothing is still a turn.
		out = append(out, obj{"role": "user", "content": ""})
	}
	return out
}

// userContent returns the content of a user message. Text alone is sent
// as one string, which every server and chat template takes; only a
// message with an image is sent as a list of parts.
func userContent(parts []obj) any {
	texts := make([]string, 0, len(parts))
	for _, part := range parts {
		if part["type"] != "text" {
			return parts
		}
		texts = append(texts, part["text"].(string))
	}
	return strings.Join(texts, "\n\n")
}

// imagePart makes the content part for an image block: a data URL.
func imagePart(b core.Block) obj {
	media := b.MediaType
	if media == "" {
		media = http.DetectContentType(b.Data)
	}
	return obj{
		"type":      "image_url",
		"image_url": obj{"url": "data:" + media + ";base64," + base64.StdEncoding.EncodeToString(b.Data)},
	}
}

// A chunk of a chat-completions stream. Fields that servers disagree about
// are kept raw and read by hand, so that one odd field does not make the
// whole chunk unreadable.
type chatChunk struct {
	Choices []chatChoice    `json:"choices"`
	Usage   *chatUsage      `json:"usage"`
	Timings *chatTimings    `json:"timings"`
	Error   json.RawMessage `json:"error"`
	// Object and Message are only looked at to tell an error that was
	// sent bare, without the "error" wrapper, from a chunk.
	Object  str             `json:"object"`
	Message json.RawMessage `json:"message"`
}

type chatChoice struct {
	Index *num       `json:"index"`
	Delta *chatDelta `json:"delta"`
	// Message is where a server that does not really stream puts the
	// whole answer. It is read like one big delta.
	Message      *chatDelta      `json:"message"`
	FinishReason json.RawMessage `json:"finish_reason"`
}

type chatDelta struct {
	Content          json.RawMessage `json:"content"`
	ReasoningContent json.RawMessage `json:"reasoning_content"` // llama.cpp, DeepSeek, vLLM, Qwen
	Reasoning        json.RawMessage `json:"reasoning"`         // OpenRouter, Ollama, Groq, newer vLLM
	Refusal          json.RawMessage `json:"refusal"`
	ToolCalls        []chatToolCall  `json:"tool_calls"`
	FunctionCall     *chatFunction   `json:"function_call"` // the format's older, single call
}

type chatToolCall struct {
	Index    *num          `json:"index"`
	ID       str           `json:"id"`
	Function *chatFunction `json:"function"`
	// Some servers leave the "function" wrapper out.
	Name      str             `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

type chatFunction struct {
	Name      str             `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

type chatUsage struct {
	PromptTokens     *num `json:"prompt_tokens"`
	CompletionTokens *num `json:"completion_tokens"`
	PromptDetails    *struct {
		CachedTokens     *num `json:"cached_tokens"`
		CacheWriteTokens *num `json:"cache_write_tokens"`
	} `json:"prompt_tokens_details"`
	// DeepSeek counts its cache hits at the top level.
	PromptCacheHitTokens *num `json:"prompt_cache_hit_tokens"`
}

type chatTimings struct {
	CacheN             *num `json:"cache_n"`
	PromptN            *num `json:"prompt_n"`
	PromptMS           num  `json:"prompt_ms"`
	PromptPerSecond    num  `json:"prompt_per_second"`
	PredictedN         *num `json:"predicted_n"`
	PredictedMS        num  `json:"predicted_ms"`
	PredictedPerSecond num  `json:"predicted_per_second"`
}

// chatCall is one tool call of an answer, as its pieces come in.
type chatCall struct {
	id      string // the id its events carry
	wireID  string // the id the server gave it, if any
	name    string
	args    []byte   // its arguments so far
	waiting []string // argument pieces that came before the name did
	started bool
}

// whole reports whether the call's arguments are complete: nothing can be
// added to a finished JSON value, so a piece that follows one must belong
// to another call.
func (c *chatCall) whole() bool {
	return len(bytes.TrimSpace(c.args)) > 0 && json.Valid(c.args)
}

// chatDecoder reads the stream of one chat-completions answer.
type chatDecoder struct {
	p     *provider
	split *thinkSplitter // nil when <think> tags are left alone

	calls   []*chatCall
	byIndex map[int]*chatCall
	byID    map[string]*chatCall // by the id the server gave
	ids     map[string]bool      // ids handed out in this answer

	finish     string // the finish reason, once a chunk gave one
	usage      *chatUsage
	timings    *chatTimings
	answered   bool   // text, thinking or a tool call arrived
	fieldThink bool   // reasoning arrived in a field of its own
	last       []byte // the last chunk, for error reports
}

func (o *openAI) newDecoder(func()) decoder {
	d := &chatDecoder{p: o.provider}
	if o.splitThinkTags {
		d.split = &thinkSplitter{}
	}
	return d
}

func (d *chatDecoder) stopped() bool { return d.finish != "" }

func (d *chatDecoder) leftover(out []core.Event) []core.Event { return d.flushText(out) }

func (d *chatDecoder) event(out []core.Event, ev sseEvent) ([]core.Event, bool, error) {
	data := bytes.TrimSpace(ev.Data)
	if len(data) == 0 {
		return out, false, nil
	}
	if bytes.Equal(data, []byte("[DONE]")) {
		out, err := d.finishUp(out, true)
		return out, true, err
	}
	if ev.Name == "error" {
		return out, false, d.p.apiError(0, nil, data)
	}
	if data[0] != '{' {
		// Not a chunk: a keep-alive some server sends as data.
		return out, false, nil
	}
	d.last = append(d.last[:0], data...)

	var chunk chatChunk
	if err := json.Unmarshal(data, &chunk); err != nil {
		return out, false, d.p.badEvent(data, err)
	}
	if !isJSONNull(chunk.Error) {
		return out, false, d.p.apiError(0, nil, data)
	}
	if len(chunk.Choices) == 0 && chunk.Usage == nil && chunk.Timings == nil &&
		(chunk.Object == "error" || !isJSONNull(chunk.Message)) {
		return out, false, d.p.apiError(0, nil, data)
	}
	if u := chunk.Usage; u != nil && (u.PromptTokens != nil || u.CompletionTokens != nil) {
		d.usage = u // An empty usage object, which some servers send along, replaces nothing.
	}
	if chunk.Timings != nil {
		d.timings = chunk.Timings
	}
	for i := range chunk.Choices {
		choice := &chunk.Choices[i]
		if choice.Index != nil && choice.Index.count() != 0 {
			continue // Only one answer is ever asked for.
		}
		delta := choice.Delta
		if delta == nil {
			delta = choice.Message
		}
		if delta != nil {
			out = d.delta(out, delta)
		}
		if reason, ok := jsonString(choice.FinishReason); ok && reason != "" {
			reason = strings.ToLower(reason)
			if reason == "error" {
				return out, false, d.p.apiError(0, nil, data)
			}
			if d.finish == "" {
				d.finish = reason
			}
		}
	}
	return out, false, nil
}

// delta takes what one chunk adds to the answer.
func (d *chatDecoder) delta(out []core.Event, delta *chatDelta) []core.Event {
	// Reasoning has two names. A server that sends both sends the same
	// words twice, so only the first is read.
	thinking, _ := jsonString(delta.ReasoningContent)
	if thinking == "" {
		thinking, _ = jsonString(delta.Reasoning)
	}
	if thinking != "" {
		d.fieldThink = true
		d.answered = true
		out = append(out, core.Event{Type: core.EventThinkingDelta, Text: thinking})
	}

	if text := contentText(delta.Content); text != "" {
		out = d.text(out, text)
	}
	if refusal, _ := jsonString(delta.Refusal); refusal != "" {
		out = d.text(out, refusal)
	}

	for i := range delta.ToolCalls {
		tc := &delta.ToolCalls[i]
		name, args := tc.Name, tc.Arguments
		if tc.Function != nil {
			name, args = tc.Function.Name, tc.Function.Arguments
		}
		out = d.toolPiece(out, tc.Index, string(tc.ID), string(name), args)
	}
	if fc := delta.FunctionCall; fc != nil {
		zero := num(0)
		out = d.toolPiece(out, &zero, "", string(fc.Name), fc.Arguments)
	}
	return out
}

// contentText reads a delta's content: a string from nearly every server,
// a list of parts from a few.
func contentText(raw json.RawMessage) string {
	if s, ok := jsonString(raw); ok {
		return s
	}
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || raw[0] != '[' {
		return ""
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &parts) != nil {
		return ""
	}
	var b strings.Builder
	for _, part := range parts {
		if part.Type == "text" || part.Type == "" {
			b.WriteString(part.Text)
		}
	}
	return b.String()
}

// text takes a piece of the answer's text, splitting inline thinking off
// if the provider is set to.
func (d *chatDecoder) text(out []core.Event, s string) []core.Event {
	if d.split == nil {
		d.answered = true
		return append(out, core.Event{Type: core.EventTextDelta, Text: s})
	}
	d.split.feed(s, func(thinking bool, s string) { out = d.splitPiece(out, thinking, s) })
	return out
}

// splitPiece hands on one stretch of text the tag splitter has sorted, as
// thinking or as text.
func (d *chatDecoder) splitPiece(out []core.Event, thinking bool, s string) []core.Event {
	if !thinking {
		d.answered = true
		return append(out, core.Event{Type: core.EventTextDelta, Text: s})
	}
	if d.fieldThink {
		// The server sends reasoning in a field of its own and leaves
		// the tags in the text as well. Once is enough.
		return out
	}
	d.answered = true
	return append(out, core.Event{Type: core.EventThinkingDelta, Text: s})
}

// flushText hands on whatever text the tag splitter still holds.
func (d *chatDecoder) flushText(out []core.Event) []core.Event {
	if d.split != nil {
		d.split.flush(func(thinking bool, s string) { out = d.splitPiece(out, thinking, s) })
	}
	return out
}

// toolPiece takes one piece of a tool call. Servers agree on little here.
// The format keys pieces by index, gives id and name with the first piece
// and the arguments in fragments; but some servers send each call whole,
// some reuse an index for every call, some leave the index or the id out,
// and some send the arguments as an object. The call a piece belongs to is
// worked out from whatever it does carry.
func (d *chatDecoder) toolPiece(out []core.Event, index *num, id, name string, rawArgs json.RawMessage) []core.Event {
	var c *chatCall
	switch {
	case index != nil:
		i := index.count()
		c = d.byIndex[i]
		if c != nil {
			// A server may use an index again for another call. Two
			// ids that differ show it; so does a name for a call that
			// has its name and whose arguments are already whole.
			switch {
			case id != "" && c.wireID != "":
				if id != c.wireID {
					c = nil
				}
			case name != "" && c.name != "" && c.whole():
				c = nil
			}
		}
		if c == nil {
			c = d.newCall(id)
			if d.byIndex == nil {
				d.byIndex = make(map[int]*chatCall)
			}
			d.byIndex[i] = c
		}
	case id != "":
		if c = d.byID[id]; c == nil {
			c = d.newCall(id)
		}
	case name != "" || len(d.calls) == 0:
		c = d.newCall("")
	default:
		c = d.calls[len(d.calls)-1]
	}
	if c.wireID == "" && id != "" {
		c.wireID = id
		if d.byID == nil {
			d.byID = make(map[string]*chatCall)
		}
		if d.byID[id] == nil {
			d.byID[id] = c
		}
	}
	if c.name == "" {
		c.name = name
	}

	args, ok := jsonString(rawArgs)
	if !ok && !isJSONNull(rawArgs) {
		args = string(bytes.TrimSpace(rawArgs)) // an object, sent as it is
	}
	c.args = append(c.args, args...)

	if !c.started && c.name != "" {
		out = d.startCall(out, c)
	}
	if args != "" {
		if c.started {
			out = append(out, core.Event{Type: core.EventToolUseDelta, ID: c.id, Text: args})
		} else {
			c.waiting = append(c.waiting, args)
		}
	}
	return out
}

// newCall begins a tool call. Its events carry the server's id for it,
// unless there is none or the answer already has a call by that id; then
// one is made up.
func (d *chatDecoder) newCall(wireID string) *chatCall {
	if d.ids == nil {
		d.ids = make(map[string]bool)
	}
	c := &chatCall{id: d.p.callID(d.ids, wireID)}
	d.calls = append(d.calls, c)
	return c
}

// startCall announces a tool call, after any text still held back, and
// hands on the argument pieces that came before its name.
func (d *chatDecoder) startCall(out []core.Event, c *chatCall) []core.Event {
	out = d.flushText(out)
	d.answered = true
	c.started = true
	out = append(out, core.Event{Type: core.EventToolUseStart, ID: c.id, Name: c.name})
	for _, piece := range c.waiting {
		out = append(out, core.Event{Type: core.EventToolUseDelta, ID: c.id, Text: piece})
	}
	c.waiting = nil
	return out
}

func (d *chatDecoder) end(out []core.Event) ([]core.Event, error) {
	return d.finishUp(out, false)
}

// finishUp closes the answer. said reports whether the server ended the
// stream with [DONE]; otherwise the connection just closed.
//
// A stream that gave a finish reason is whole whether or not [DONE]
// followed. A stream that gave none is taken as whole only if it both
// ended with [DONE] and brought some answer: the server plainly meant to
// stop there, and the reason is then read off the answer. Anything else is
// a stream that was cut off.
func (d *chatDecoder) finishUp(out []core.Event, said bool) ([]core.Event, error) {
	out = d.flushText(out)
	if d.finish == "" && !(said && d.answered) {
		return out, d.p.endedEarly(d.last)
	}
	for _, c := range d.calls {
		if !c.started {
			out = d.startCall(out, c) // A call that never got a name is still handed on.
		}
	}
	for _, c := range d.calls {
		out = append(out, core.Event{Type: core.EventToolUseStop, ID: c.id})
	}
	if u, ok := d.usageNow(); ok {
		out = append(out, core.Event{Type: core.EventUsage, Usage: u})
	}
	if t := d.timings; t != nil && d.p.tracing() {
		d.p.traceFn(TraceRecord{Kind: TraceTimings, Timings: &Timings{
			CacheN:             countOf(t.CacheN),
			PromptN:            countOf(t.PromptN),
			PromptMS:           float64(t.PromptMS),
			PromptPerSecond:    float64(t.PromptPerSecond),
			PredictedN:         countOf(t.PredictedN),
			PredictedMS:        float64(t.PredictedMS),
			PredictedPerSecond: float64(t.PredictedPerSecond),
		}})
	}
	return append(out, core.Event{Type: core.EventStop, StopReason: d.stopReason()}), nil
}

// usageNow puts the token counts together from the usage chunk and from
// llama.cpp's timings.
//
// The format's prompt_tokens is the whole prompt, cached part included;
// core's InputTokens is only what was read fresh, so the cached part is
// taken out. llama.cpp's timings give the split directly (prompt_n fresh,
// cache_n from the cache), and are preferred for it when they are there.
// A llama.cpp too old to report cache_n still reports prompt_n; what it
// did not read fresh, out of the whole prompt, came from its cache.
func (d *chatDecoder) usageNow() (core.Usage, bool) {
	var u core.Usage
	have := false
	whole := -1 // the size of the whole prompt, if the usage chunk gave it
	if us := d.usage; us != nil {
		have = true
		cached, written := countOf(us.PromptCacheHitTokens), 0
		if det := us.PromptDetails; det != nil {
			if det.CachedTokens != nil {
				cached = det.CachedTokens.count()
			}
			written = countOf(det.CacheWriteTokens)
		}
		if us.PromptTokens != nil {
			whole = us.PromptTokens.count()
		}
		u.CacheReadTokens = cached
		u.CacheWriteTokens = written
		u.InputTokens = max(0, countOf(us.PromptTokens)-cached-written)
		u.OutputTokens = countOf(us.CompletionTokens)
	}
	if t := d.timings; t != nil && (t.PromptN != nil || t.CacheN != nil || t.PredictedN != nil) {
		switch {
		case t.CacheN != nil:
			u.InputTokens, u.CacheReadTokens, u.CacheWriteTokens = countOf(t.PromptN), t.CacheN.count(), 0
		case t.PromptN != nil && whole >= 0:
			fresh := min(t.PromptN.count(), whole)
			u.InputTokens, u.CacheReadTokens, u.CacheWriteTokens = fresh, whole-fresh, 0
		case t.PromptN != nil:
			u.InputTokens, u.CacheReadTokens, u.CacheWriteTokens = t.PromptN.count(), 0, 0
		}
		if !have || d.usage.CompletionTokens == nil {
			u.OutputTokens = countOf(t.PredictedN)
		}
		have = true
	}
	return u, have
}

// stopReason turns the finish reason into core's. Whether the model wants
// tools run is read off the answer rather than off the reason, because
// servers get it wrong both ways: "stop" with tool calls, and "tool_calls"
// with none. A caller told to run tools must have some to run, and tool
// calls that are there must be answered.
func (d *chatDecoder) stopReason() core.StopReason {
	switch d.finish {
	case "length", "max_tokens":
		return core.StopMaxTokens
	case "content_filter":
		return core.StopRefusal
	case "stop_sequence":
		return core.StopSequence
	}
	// "stop", "tool_calls", "function_call", llama.cpp's older "tool",
	// no reason at all, and whatever else a server may say.
	if len(d.calls) > 0 {
		return core.StopToolUse
	}
	return core.StopEndTurn
}

// modelList is the answer of the format's models endpoint, with the
// fields different services add to say how big a model's context is.
type modelList struct {
	Data []struct {
		ID string `json:"id"`

		ContextLength *num `json:"context_length"` // OpenRouter
		ContextWindow *num `json:"context_window"` // Groq
		MaxModelLen   *num `json:"max_model_len"`  // vLLM

		MaxCompletionTokens *num `json:"max_completion_tokens"` // Groq
		TopProvider         *struct {
			MaxCompletionTokens *num `json:"max_completion_tokens"`
		} `json:"top_provider"` // OpenRouter

		// llama.cpp's router says whether a model is loaded.
		Status *struct {
			Value string `json:"value"`
		} `json:"status"`
	} `json:"data"`
}

// Models lists the models the service says it serves. OpenAI's own list
// names models and nothing more; a model's sizes are filled in for the
// services that report them.
func (o *openAI) Models(ctx context.Context) ([]core.ModelInfo, error) {
	list, err := o.modelList(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]core.ModelInfo, 0, len(list.Data))
	for _, m := range list.Data {
		info := core.ModelInfo{ID: m.ID}
		for _, n := range []*num{m.ContextLength, m.ContextWindow, m.MaxModelLen} {
			if n != nil && info.ContextWindow == 0 {
				info.ContextWindow = n.count()
			}
		}
		info.MaxOutput = countOf(m.MaxCompletionTokens)
		if info.MaxOutput == 0 && m.TopProvider != nil {
			info.MaxOutput = countOf(m.TopProvider.MaxCompletionTokens)
		}
		out = append(out, info)
	}
	return out, nil
}

// modelList fetches and reads the service's model list.
func (o *openAI) modelList(ctx context.Context) (*modelList, error) {
	body, err := o.call(ctx, http.MethodGet, o.endpoint("/models", nil), nil)
	if err != nil {
		return nil, err
	}
	var list modelList
	if err := json.Unmarshal(body, &list); err != nil {
		e := o.fail(core.ErrBadResponse, "the model list cannot be read: %v", err)
		e.Body = string(clip(body, bodyStart))
		return nil, e
	}
	return &list, nil
}
