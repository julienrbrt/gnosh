// Package apptest is what the tests of the program are run against: a
// model server that answers from a script and remembers what it was sent,
// and a folder tree with a home directory of its own, so that a test never
// touches the real one and never reaches the network.
//
// It is used by the tests of internal/app, which run the program through
// app.Main, and by the tests at the root of the repository, which run the
// real binary as a subprocess.
package apptest

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// Call is a tool call in a scripted reply.
type Call struct {
	// ID is the call's id, Name the tool and Args its arguments, as the
	// JSON text a model would write.
	ID, Name, Args string
}

// Reply is one scripted answer of the model server: the reply to the chat
// request with the same number.
type Reply struct {
	// Text is the answer, Thinking the reasoning before it, and Calls the
	// tool calls after it.
	Text     string
	Thinking string
	Calls    []Call

	// Status, when not zero, makes the server answer with that HTTP
	// status and Body instead of a reply. Header is added to the answer.
	Status int
	Body   string
	Header map[string]string

	// In and Out are the token counts the server reports. With both zero
	// it reports a quarter of the bytes it was sent and of what it wrote.
	In, Out int
	// Cached is the part of In the server says came from its prompt cache
	// (usage.prompt_tokens_details.cached_tokens).
	Cached int

	// Arrived is called when the request has arrived, before anything is
	// answered.
	Arrived func()
	// Hold, when not nil, makes the server stop after the first piece of
	// the reply and wait until Hold is closed or the client goes away.
	Hold <-chan struct{}
	// Cut makes the server drop the connection after the first piece.
	Cut bool
	// Finish is the finish reason: "stop" by default, or "tool_calls"
	// when the reply holds calls.
	Finish string
}

// Request is one chat request as the server received it.
type Request struct {
	// Body is the request body, byte for byte.
	Body []byte
	// JSON is the body, decoded.
	JSON map[string]any
}

// Messages returns the request's messages.
func (r Request) Messages() []map[string]any {
	var out []map[string]any
	list, _ := r.JSON["messages"].([]any)
	for _, m := range list {
		if mm, ok := m.(map[string]any); ok {
			out = append(out, mm)
		}
	}
	return out
}

// Text returns the text of a message of the request: its content when
// that is a string, and the text parts of it joined otherwise.
func Text(msg map[string]any) string {
	switch c := msg["content"].(type) {
	case string:
		return c
	case []any:
		var parts []string
		for _, p := range c {
			if pm, ok := p.(map[string]any); ok {
				if s, ok := pm["text"].(string); ok {
					parts = append(parts, s)
				}
			}
		}
		return strings.Join(parts, "\n")
	}
	return ""
}

// Model is a chat-completions server that answers from a script.
type Model struct {
	// URL is the server's address, without a path.
	URL string

	t   testing.TB
	srv *httptest.Server

	mu      sync.Mutex
	respond func(n int, r Request) *Reply
	script  []Reply
	next    int
	reqs    []Request
	paths   []string
	overrun int
	window  int
	models  []string
}

// NewModel starts a model server that answers the chat requests it is
// sent with replies, in order. It lists one model, "test-model", and
// reports a context window of 100000 tokens. It is closed when the test
// ends.
func NewModel(t testing.TB, replies ...Reply) *Model {
	m := StartModel(replies...)
	m.t = t
	t.Cleanup(m.Close)
	return m
}

// StartModel starts a model server outside a test, for trying the program
// by hand (tools/dev/scriptmodel). The caller closes it.
func StartModel(replies ...Reply) *Model {
	m := &Model{script: replies, window: 100000, models: []string{"test-model"}}
	m.srv = httptest.NewServer(http.HandlerFunc(m.serve))
	m.URL = m.srv.URL
	return m
}

// Close stops the server.
func (m *Model) Close() { m.srv.Close() }

// Respond makes f answer the requests it returns a reply for, in place
// of the script: n counts the chat requests from zero. A request f
// returns nil for is answered by the next reply of the script.
func (m *Model) Respond(f func(n int, r Request) *Reply) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.respond = f
}

// Add appends replies to the script.
func (m *Model) Add(replies ...Reply) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.script = append(m.script, replies...)
}

// SetWindow sets the context window the server reports. Zero makes it
// report none.
func (m *Model) SetWindow(tokens int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.window = tokens
}

// SetModels sets the models the server lists.
func (m *Model) SetModels(ids ...string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.models = ids
}

// Requests returns the chat requests received so far, in order.
func (m *Model) Requests() []Request {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]Request(nil), m.reqs...)
}

// Paths returns every path the server was asked for, in order, each with
// its method: "GET /v1/models".
func (m *Model) Paths() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.paths...)
}

// Done fails the test unless the script was used to its end and no
// request came after it.
func (m *Model) Done() {
	m.t.Helper()
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.next < len(m.script) {
		m.t.Errorf("the model gave %d of the %d replies of its script (it was sent %d requests)", m.next, len(m.script), len(m.reqs))
	}
	if m.overrun > 0 {
		m.t.Errorf("the model was sent %d requests more than the script has replies for", m.overrun)
	}
}

func (m *Model) serve(w http.ResponseWriter, r *http.Request) {
	m.mu.Lock()
	m.paths = append(m.paths, r.Method+" "+r.URL.Path)
	window, models := m.window, append([]string(nil), m.models...)
	m.mu.Unlock()
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/v1/models":
		var data []map[string]any
		for _, id := range models {
			data = append(data, map[string]any{"id": id, "object": "model"})
		}
		writeJSON(w, http.StatusOK, map[string]any{"object": "list", "data": data})
	case r.Method == http.MethodGet && r.URL.Path == "/props":
		if window == 0 {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": map[string]any{"message": "not found", "code": 404}})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"default_generation_settings": map[string]any{"n_ctx": window}})
	case r.Method == http.MethodPost && r.URL.Path == "/v1/chat/completions":
		m.chat(w, r)
	default:
		writeJSON(w, http.StatusNotFound, map[string]any{"error": map[string]any{"message": "not found", "code": 404}})
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func (m *Model) chat(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	req := Request{Body: body}
	_ = json.Unmarshal(body, &req.JSON)

	m.mu.Lock()
	n := len(m.reqs)
	m.reqs = append(m.reqs, req)
	var rep Reply
	var own *Reply
	if m.respond != nil {
		own = m.respond(n, req)
	}
	switch {
	case own != nil:
		rep = *own
	case m.next < len(m.script):
		rep = m.script[m.next]
		m.next++
	default:
		m.overrun++
		rep = Reply{Text: "(the scripted model has no more replies)"}
	}
	m.mu.Unlock()

	if rep.Arrived != nil {
		rep.Arrived()
	}
	for k, v := range rep.Header {
		w.Header().Set(k, v)
	}
	if rep.Status != 0 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(rep.Status)
		text := rep.Body
		if text == "" {
			text = fmt.Sprintf(`{"error":{"message":"scripted failure","code":%d}}`, rep.Status)
		}
		_, _ = io.WriteString(w, text)
		return
	}

	model, _ := req.JSON["model"].(string)
	w.Header().Set("Content-Type", "text/event-stream")
	w.WriteHeader(http.StatusOK)
	flusher, _ := w.(http.Flusher)
	send := func(v any) {
		b, _ := json.Marshal(v)
		fmt.Fprintf(w, "data: %s\n\n", b)
		if flusher != nil {
			flusher.Flush()
		}
	}
	chunk := func(delta map[string]any, finish any) map[string]any {
		return map[string]any{
			"id": "chatcmpl-test", "object": "chat.completion.chunk", "created": 1, "model": model,
			"choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": finish}},
		}
	}
	held := false
	hold := func() bool {
		if held {
			return true
		}
		held = true
		if rep.Cut {
			panic(http.ErrAbortHandler)
		}
		if rep.Hold == nil {
			return true
		}
		select {
		case <-rep.Hold:
			return true
		case <-r.Context().Done():
			return false
		}
	}

	send(chunk(map[string]any{"role": "assistant", "content": ""}, nil))
	if rep.Thinking != "" {
		send(chunk(map[string]any{"reasoning_content": rep.Thinking}, nil))
	}
	// The answer goes out in two pieces, as a stream would bring it.
	text := rep.Text
	if cut := len(text) / 2; cut > 0 {
		for cut < len(text) && text[cut]&0xC0 == 0x80 {
			cut++
		}
		send(chunk(map[string]any{"content": text[:cut]}, nil))
		if !hold() {
			return
		}
		text = text[cut:]
	}
	if text != "" {
		send(chunk(map[string]any{"content": text}, nil))
	}
	if !hold() {
		return
	}
	size := len(rep.Text) + len(rep.Thinking)
	for i, c := range rep.Calls {
		size += len(c.Args) + len(c.Name)
		send(chunk(map[string]any{"tool_calls": []any{map[string]any{
			"index": i, "id": c.ID, "type": "function",
			"function": map[string]any{"name": c.Name, "arguments": c.Args},
		}}}, nil))
	}
	finish := rep.Finish
	if finish == "" {
		finish = "stop"
		if len(rep.Calls) > 0 {
			finish = "tool_calls"
		}
	}
	send(chunk(map[string]any{}, finish))
	in, out := rep.In, rep.Out
	if in == 0 && out == 0 {
		in, out = len(body)/4, size/4+1
	}
	usage := map[string]any{"prompt_tokens": in, "completion_tokens": out, "total_tokens": in + out}
	if rep.Cached > 0 {
		usage["prompt_tokens_details"] = map[string]any{"cached_tokens": rep.Cached}
	}
	send(map[string]any{
		"id": "chatcmpl-test", "object": "chat.completion.chunk", "created": 1, "model": model,
		"choices": []any{},
		"usage":   usage,
	})
	fmt.Fprint(w, "data: [DONE]\n\n")
	if flusher != nil {
		flusher.Flush()
	}
}
