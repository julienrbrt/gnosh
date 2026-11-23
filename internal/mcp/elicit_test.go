package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// An elicitation is read with its fields in the order the schema writes
// them; a field of a kind MCP does not allow is refused.
func TestReadElicitation(t *testing.T) {
	e, err := readElicitation(json.RawMessage(`{"message": "Which branch?", "requestedSchema": {"type": "object",
	  "properties": {"zeta": {"type": "string", "enum": ["main", "dev"], "enumNames": ["Main", "Development"]},
	                 "alpha": {"type": "integer", "minimum": 1, "maximum": 9},
	                 "ok": {"type": "boolean", "title": "Proceed"}},
	  "required": ["zeta"]}}`))
	if err != nil {
		t.Fatal(err)
	}
	if e.Message != "Which branch?" || len(e.Fields) != 3 || e.Fields[0].Name != "zeta" || e.Fields[1].Name != "alpha" || e.Fields[2].Name != "ok" {
		t.Fatalf("read: %+v", e)
	}
	if f := e.Fields[0]; !f.Required || len(f.Enum) != 2 || f.EnumNames[1] != "Development" {
		t.Errorf("the enum field: %+v", f)
	}
	if f := e.Fields[1]; f.Required || f.Minimum == nil || *f.Maximum != 9 {
		t.Errorf("the integer field: %+v", f)
	}
	if _, err := readElicitation(json.RawMessage(`{"message": "x", "requestedSchema": {"type": "object", "properties": {"list": {"type": "array"}}}}`)); err == nil || !strings.Contains(err.Error(), `"array"`) {
		t.Errorf("an array field: %v", err)
	}
}

// A server asks for something in the middle of a call: the client, given
// someone to ask, declares the capability, answers the request with what
// they said, and the call goes on; without anyone to ask the capability
// is not declared and the request is refused.
func TestElicitationInACall(t *testing.T) {
	f := &fakeHTTP{}
	answered := make(chan string, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var m message
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &m)
		switch {
		case m.Method == "tools/call":
			w.Header().Set("Content-Type", "text/event-stream")
			fl := w.(http.Flusher)
			fmt.Fprint(w, `data: {"jsonrpc":"2.0","id":"el-1","method":"elicitation/create","params":{"message":"Your name?","requestedSchema":{"type":"object","properties":{"name":{"type":"string"}},"required":["name"]}}}`+"\n\n")
			fl.Flush()
			var got string
			select {
			case got = <-answered:
			case <-time.After(10 * time.Second):
			}
			fmt.Fprintf(w, `data: {"jsonrpc":"2.0","id":%s,"result":{"content":[{"type":"text","text":%q}]}}`+"\n\n", m.ID, got)
			fl.Flush()
		case m.Method == "" && string(m.ID) == `"el-1"`:
			answered <- string(m.Result) + string(errorText(m))
			w.WriteHeader(http.StatusAccepted)
		case m.Method == "initialize":
			f.mu.Lock()
			f.seen = append(f.seen, r.Header.Clone())
			f.methods = append(f.methods, string(body))
			f.mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%s,"result":{"protocolVersion":"2025-06-18","serverInfo":{"name":"asker","version":"1"},"capabilities":{"tools":{}}}}`, m.ID)
		default:
			w.WriteHeader(http.StatusAccepted)
		}
	}))
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	var asked Elicitation
	c, err := StartHTTP(ctx, "asker", srv.URL, nil, Options{OnElicit: func(ctx context.Context, e Elicitation) (ElicitResult, error) {
		asked = e
		return ElicitResult{Action: "accept", Content: map[string]any{"name": "Jae"}}, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	res, err := c.CallTool(ctx, "who", nil)
	c.Close()
	if err != nil || len(res.Content) != 1 || !strings.Contains(res.Content[0].Text, `"action":"accept"`) || !strings.Contains(res.Content[0].Text, `"name":"Jae"`) {
		t.Fatalf("the call: %+v, %v", res, err)
	}
	if asked.Message != "Your name?" || len(asked.Fields) != 1 || !asked.Fields[0].Required {
		t.Errorf("what was asked: %+v", asked)
	}
	f.mu.Lock()
	hello := f.methods[0]
	f.mu.Unlock()
	if !strings.Contains(hello, `"elicitation":{}`) {
		t.Errorf("the handshake did not declare elicitation: %s", hello)
	}

	// Nobody to ask: no capability, and the request is refused.
	f.mu.Lock()
	f.methods = nil
	f.mu.Unlock()
	c, err = StartHTTP(ctx, "asker", srv.URL, nil, Options{})
	if err != nil {
		t.Fatal(err)
	}
	res, err = c.CallTool(ctx, "who", nil)
	c.Close()
	if err != nil || !strings.Contains(res.Content[0].Text, "nobody is there to ask") {
		t.Errorf("a request with nobody to ask: %+v, %v", res, err)
	}
	f.mu.Lock()
	hello = f.methods[0]
	f.mu.Unlock()
	if strings.Contains(hello, "elicitation") {
		t.Errorf("the handshake declared elicitation with nobody to ask: %s", hello)
	}
}

// errorText is a message's error as JSON, or nothing.
func errorText(m message) []byte {
	if m.Error == nil {
		return nil
	}
	data, _ := json.Marshal(m.Error)
	return data
}

// A server that takes its request back (notifications/cancelled) has the
// question withdrawn from the person, and is sent no answer to it, though
// it took it back at once.
func TestElicitationTakenBack(t *testing.T) {
	var mu sync.Mutex
	var replies []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var m message
		_ = json.Unmarshal(body, &m)
		switch {
		case m.Method == "initialize":
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%s,"result":{"protocolVersion":"2025-06-18","serverInfo":{"name":"asker","version":"1"},"capabilities":{"tools":{}}}}`, m.ID)
		case m.Method == "tools/call":
			w.Header().Set("Content-Type", "text/event-stream")
			fl := w.(http.Flusher)
			fmt.Fprint(w, `data: {"jsonrpc":"2.0","id":7,"method":"elicitation/create","params":{"message":"Name?","requestedSchema":{"type":"object","properties":{"name":{"type":"string"}}}}}`+"\n\n")
			fmt.Fprint(w, `data: {"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId": 7,"reason":"done without it"}}`+"\n\n")
			fl.Flush()
			time.Sleep(100 * time.Millisecond)
			fmt.Fprintf(w, `data: {"jsonrpc":"2.0","id":%s,"result":{"content":[{"type":"text","text":"went on"}]}}`+"\n\n", m.ID)
			fl.Flush()
		case m.Method == "" && len(m.ID) > 0:
			mu.Lock()
			replies = append(replies, string(body))
			mu.Unlock()
			w.WriteHeader(http.StatusAccepted)
		default:
			w.WriteHeader(http.StatusAccepted)
		}
	}))
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	withdrawn := make(chan struct{})
	c, err := StartHTTP(ctx, "asker", srv.URL, nil, Options{OnElicit: func(ctx context.Context, e Elicitation) (ElicitResult, error) {
		<-ctx.Done()
		close(withdrawn)
		return ElicitResult{Action: "cancel"}, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	res, err := c.CallTool(ctx, "who", nil)
	if err != nil || len(res.Content) != 1 || res.Content[0].Text != "went on" {
		t.Fatalf("the call: %+v, %v", res, err)
	}
	select {
	case <-withdrawn:
	case <-ctx.Done():
		t.Fatal("the question was not withdrawn")
	}
	time.Sleep(200 * time.Millisecond) // (an answer sent after it would be here by now)
	c.Close()
	mu.Lock()
	defer mu.Unlock()
	if len(replies) != 0 {
		t.Errorf("the server was answered after it took its request back: %q", replies)
	}
}

// Closing the connection withdraws a question that waits.
func TestElicitationEndsWithTheConnection(t *testing.T) {
	asked := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var m message
		_ = json.Unmarshal(body, &m)
		switch m.Method {
		case "initialize":
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%s,"result":{"protocolVersion":"2025-06-18","serverInfo":{"name":"asker","version":"1"},"capabilities":{"tools":{}}}}`, m.ID)
		case "tools/call":
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, `data: {"jsonrpc":"2.0","id":"q","method":"elicitation/create","params":{"message":"Go?","requestedSchema":{"type":"object","properties":{}}}}`+"\n\n")
			w.(http.Flusher).Flush()
			<-r.Context().Done()
		default:
			w.WriteHeader(http.StatusAccepted)
		}
	}))
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	withdrawn := make(chan struct{})
	c, err := StartHTTP(ctx, "asker", srv.URL, nil, Options{OnElicit: func(ctx context.Context, e Elicitation) (ElicitResult, error) {
		close(asked)
		<-ctx.Done()
		close(withdrawn)
		return ElicitResult{Action: "cancel"}, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	go c.CallTool(ctx, "who", nil)
	select {
	case <-asked:
	case <-ctx.Done():
		t.Fatal("the question was not put")
	}
	c.Close()
	select {
	case <-withdrawn:
	case <-ctx.Done():
		t.Fatal("closing did not withdraw the question")
	}
}

// A field named twice is asked once, and a request for too many values is
// refused.
func TestReadElicitationLimits(t *testing.T) {
	e, err := readElicitation(json.RawMessage(`{"message": "x", "requestedSchema": {"type": "object", "properties": {"a": {"type": "string"}, "a": {"type": "string"}, "b": {"type": "boolean"}}}}`))
	if err != nil || len(e.Fields) != 2 || e.Fields[0].Name != "a" || e.Fields[1].Name != "b" {
		t.Errorf("a field named twice: %+v, %v", e, err)
	}
	var props []string
	for i := 0; i <= maxElicitFields; i++ {
		props = append(props, fmt.Sprintf(`"f%d": {"type": "string"}`, i))
	}
	_, err = readElicitation(json.RawMessage(`{"message": "x", "requestedSchema": {"type": "object", "properties": {` + strings.Join(props, ",") + `}}}`))
	if err == nil || !strings.Contains(err.Error(), "at most 20") {
		t.Errorf("too many fields: %v", err)
	}
}
