package lsp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// fake is a made-up language server at the other end of two pipes. It
// answers what handle says, and keeps what the client sent it.
type fake struct {
	t      *testing.T
	out    *os.File // what the server writes, the client's input
	mu     sync.Mutex
	got    []message
	handle func(f *fake, m message)
}

// write sends a message to the client.
func (f *fake) write(m message) {
	m.JSONRPC = "2.0"
	body, _ := json.Marshal(m)
	_, _ = f.out.Write(frame(body))
}

// reply answers a request with result.
func (f *fake) reply(m message, result any) {
	raw, _ := json.Marshal(result)
	f.write(message{ID: m.ID, Result: raw})
}

// received returns what the client sent of one method.
func (f *fake) received(method string) []message {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []message
	for _, m := range f.got {
		if m.Method == method {
			out = append(out, m)
		}
	}
	return out
}

// newFake connects a client to a made-up server, and makes the opening
// exchange.
func newFake(t *testing.T, handle func(f *fake, m message)) (*Client, *fake) {
	t.Helper()
	return newFakeWith(t, nil, handle)
}

// newFakeWith is newFake with settings for the server.
func newFakeWith(t *testing.T, settings json.RawMessage, handle func(f *fake, m message)) (*Client, *fake) {
	t.Helper()
	inR, inW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	outR, outW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	f := &fake{t: t, out: outW, handle: handle}
	go func() {
		defer outW.Close()
		r := bufio.NewReaderSize(inR, maxHeader)
		for {
			body, err := readMessage(r)
			if err != nil {
				return
			}
			var m message
			if json.Unmarshal(body, &m) != nil {
				return
			}
			f.mu.Lock()
			f.got = append(f.got, m)
			f.mu.Unlock()
			if m.Method == "exit" {
				return
			}
			f.handle(f, m)
		}
	}()
	root := t.TempDir()
	c := connect(Config{Name: "fake", Root: root, Settings: settings}, inW, outR)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.initialize(ctx, Config{Root: root}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c, f
}

// standard answers the opening exchange, shutdown, and publishes one
// error for a file whose text holds "bad", and another for a file beside
// it, as a change in one file shows in another.
func standard(f *fake, m message) {
	switch m.Method {
	case "initialize":
		f.reply(m, map[string]any{"capabilities": map[string]any{
			"textDocumentSync":   map[string]any{"openClose": true, "change": 1, "save": map[string]bool{"includeText": true}},
			"definitionProvider": true, "hoverProvider": map[string]any{}, "referencesProvider": false,
		}})
	case "initialized":
		// A request of the server's own, which the client answers.
		f.write(message{ID: json.RawMessage(`"cfg-1"`), Method: "workspace/configuration",
			Params: json.RawMessage(`{"items":[{"section":"gopls"},{"section":"go"}]}`)})
	case "shutdown":
		f.reply(m, nil)
	case "textDocument/didOpen", "textDocument/didChange":
		var p struct {
			TextDocument struct {
				URI  string `json:"uri"`
				Text string `json:"text"`
			} `json:"textDocument"`
			ContentChanges []struct {
				Text string `json:"text"`
			} `json:"contentChanges"`
		}
		_ = json.Unmarshal(m.Params, &p)
		text := p.TextDocument.Text
		if len(p.ContentChanges) > 0 {
			text = p.ContentChanges[0].Text
		}
		var diags []Diagnostic
		if strings.Contains(text, "bad") {
			diags = append(diags, Diagnostic{Range: Range{Start: Position{Line: 0, Character: 4}}, Severity: SeverityError, Message: "undefined: bad", Source: "compiler"})
		}
		raw, _ := json.Marshal(map[string]any{"uri": p.TextDocument.URI, "diagnostics": diags})
		f.write(message{Method: "textDocument/publishDiagnostics", Params: raw})
		other := strings.Replace(p.TextDocument.URI, "a.go", "b.go", 1)
		raw, _ = json.Marshal(map[string]any{"uri": other, "diagnostics": diags})
		f.write(message{Method: "textDocument/publishDiagnostics", Params: raw})
	default:
		if len(m.ID) > 0 && m.Method != "" {
			f.write(message{ID: m.ID, Error: &rpcError{Code: -32601, Message: "no"}})
		}
	}
}

// The opening exchange tells the server who the client is and what it
// can do, and the server's requests are answered; the capabilities the
// server gave are what Has reports. Close says shutdown, then exit.
func TestOpeningAndClosing(t *testing.T) {
	c, f := newFake(t, standard)
	inits := f.received("initialize")
	if len(inits) != 1 || !strings.Contains(string(inits[0].Params), `"hierarchicalDocumentSymbolSupport":true`) {
		t.Fatalf("initialize: %+v", inits)
	}
	if !c.Has("definitionProvider") || !c.Has("hoverProvider") || c.Has("referencesProvider") || c.Has("callHierarchyProvider") {
		t.Error("Has does not follow the server's capabilities")
	}
	waitFor(t, func() bool {
		f.mu.Lock()
		defer f.mu.Unlock()
		for _, m := range f.got {
			if string(m.ID) == `"cfg-1"` && string(m.Result) == "[null,null]" {
				return true
			}
		}
		return false
	}, "the answer to workspace/configuration")
	_ = c.Close()
	if len(f.received("shutdown")) != 1 || len(f.received("exit")) != 1 {
		t.Error("Close did not say shutdown and exit")
	}
	if !errors.Is(c.Err(), ErrClosed) {
		t.Errorf("Err after Close = %v", c.Err())
	}
}

// waitFor waits up to two seconds for ok.
func waitFor(t *testing.T, ok func() bool, what string) {
	t.Helper()
	for range 200 {
		if ok() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("no %s", what)
}

// A file is opened the first time and changed after, its whole text each
// time, with a save to a server that asked for them; the same text again
// sends nothing. Settle waits for the file's diagnostics, and Diagnostics
// has every file's.
func TestSyncAndDiagnostics(t *testing.T) {
	c, f := newFake(t, standard)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	path := filepath.Join(c.root, "a.go")
	s, err := c.Sync(ctx, path, "go", "package a\n")
	if err != nil || !s.Sent {
		t.Fatalf("open: %+v, %v", s, err)
	}
	if !c.Settle(ctx, path, s, 50*time.Millisecond) {
		t.Fatal("the opened file's diagnostics did not come")
	}
	if d := c.Diagnostics(); len(d) != 0 {
		t.Errorf("a clean file: %v", d)
	}
	s, err = c.Sync(ctx, path, "go", "var bad = x\n")
	if err != nil || !s.Sent || s.Version != 2 || !c.Settle(ctx, path, s, 50*time.Millisecond) {
		t.Fatalf("change: %+v, %v", s, err)
	}
	d := c.Diagnostics()
	if len(d[path]) != 1 || d[path][0].Message != "undefined: bad" || len(d[filepath.Join(c.root, "b.go")]) != 1 {
		t.Errorf("diagnostics after the change: %+v", d)
	}
	if s, _ := c.Sync(ctx, path, "go", "var bad = x\n"); s.Sent {
		t.Error("the same text was sent again")
	}
	changes, saves := f.received("textDocument/didChange"), f.received("textDocument/didSave")
	if len(f.received("textDocument/didOpen")) != 1 || len(changes) != 1 || len(saves) != 1 ||
		!strings.Contains(string(changes[0].Params), `"version":2`) || !strings.Contains(string(saves[0].Params), `"text":"var bad = x\n"`) {
		t.Errorf("sync messages: opens %d, changes %+v, saves %+v", len(f.received("textDocument/didOpen")), changes, saves)
	}
	// Settle gives up with its context when nothing comes.
	short, stop := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer stop()
	c.mu.Lock()
	now := c.seq
	c.mu.Unlock()
	if c.Settle(short, filepath.Join(c.root, "none.go"), Synced{Since: now}, 10*time.Millisecond) {
		t.Error("Settle said diagnostics came for a file nothing was published of")
	}
}

// The questions: a definition given as location links, a hover as markup,
// a file's symbols with their children, the workspace's symbols, and
// incoming calls; an error answer is an error.
func TestQuestions(t *testing.T) {
	c, _ := newFake(t, func(f *fake, m message) {
		uri := FileURI("/w/a.go")
		item := map[string]any{"name": "F", "kind": 12, "uri": uri, "range": Range{}, "selectionRange": Range{Start: Position{Line: 3, Character: 5}}}
		switch m.Method {
		case "textDocument/definition":
			f.reply(m, []map[string]any{{"targetUri": uri, "targetRange": Range{}, "targetSelectionRange": Range{Start: Position{Line: 9, Character: 5}}}})
		case "textDocument/hover":
			f.reply(m, map[string]any{"contents": map[string]string{"kind": "markdown", "value": "func F() error"}})
		case "textDocument/documentSymbol":
			f.reply(m, []map[string]any{{"name": "T", "kind": 23, "range": Range{}, "selectionRange": Range{Start: Position{Line: 1}},
				"children": []map[string]any{{"name": "M", "kind": 6, "range": Range{}, "selectionRange": Range{Start: Position{Line: 2}}}}}})
		case "workspace/symbol":
			f.reply(m, []map[string]any{{"name": "F", "kind": 12, "location": map[string]any{"uri": uri, "range": Range{}}, "containerName": "a"}})
		case "textDocument/prepareCallHierarchy":
			f.reply(m, []any{item})
		case "callHierarchy/incomingCalls":
			f.reply(m, []map[string]any{{"from": map[string]any{"name": "main", "kind": 12, "uri": uri, "range": Range{}, "selectionRange": Range{}},
				"fromRanges": []Range{{Start: Position{Line: 20, Character: 1}}}}})
		case "textDocument/references":
			f.write(message{ID: m.ID, Error: &rpcError{Code: -32803, Message: "no package for file"}})
		default:
			standard(f, m)
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	locs, err := c.Definition(ctx, "/w/a.go", Position{Line: 3, Character: 5})
	if err != nil || len(locs) != 1 || locs[0].Range.Start.Line != 9 || URIPath(locs[0].URI) != "/w/a.go" {
		t.Errorf("definition: %+v, %v", locs, err)
	}
	if text, err := c.Hover(ctx, "/w/a.go", Position{}); err != nil || text != "func F() error" {
		t.Errorf("hover: %q, %v", text, err)
	}
	syms, err := c.DocumentSymbols(ctx, "/w/a.go")
	if err != nil || len(syms) != 1 || syms[0].Name != "T" || len(syms[0].Children) != 1 || syms[0].Children[0].Location.Range.Start.Line != 2 {
		t.Errorf("document symbols: %+v, %v", syms, err)
	}
	if ws, err := c.WorkspaceSymbols(ctx, "F"); err != nil || len(ws) != 1 || ws[0].Container != "a" {
		t.Errorf("workspace symbols: %+v, %v", ws, err)
	}
	calls, err := c.IncomingCalls(ctx, "/w/a.go", Position{Line: 3, Character: 5})
	if err != nil || len(calls) != 1 || calls[0].Item.Name != "main" || calls[0].Ranges[0].Start.Line != 20 {
		t.Errorf("incoming calls: %+v, %v", calls, err)
	}
	if _, err := c.References(ctx, "/w/a.go", Position{}); err == nil || !strings.Contains(err.Error(), "no package for file") {
		t.Errorf("an error answer: %v", err)
	}
}

// A server that writes anything but messages on its output is
// disconnected, as is one whose message is over the size taken.
func TestServerThatIsNotProtocolIsDisconnected(t *testing.T) {
	for name, junk := range map[string]string{
		"a log line":     "starting up...\r\n\r\n",
		"a huge message": "Content-Length: 99999999999\r\n\r\n",
		"a long header":  "X-Junk: " + strings.Repeat("x", maxHeader) + "\r\n\r\n",
	} {
		c, f := newFake(t, standard)
		_, _ = f.out.WriteString(junk)
		waitFor(t, func() bool { return c.Err() != nil }, name+" disconnecting")
		if !strings.Contains(c.Err().Error(), "not Language Server Protocol") {
			t.Errorf("%s: %v", name, c.Err())
		}
	}
}

// Positions: a model's 1-based line and character, counted in code
// points, against the protocol's UTF-16 units from zero.
func TestPositions(t *testing.T) {
	line := "é😀x" // é is one unit, the face two
	if p := FromRunes(4, 3, line); p.Line != 3 || p.Character != 3 {
		t.Errorf("FromRunes = %+v", p)
	}
	if l, ch := ToRunes(Position{Line: 3, Character: 3}, line); l != 4 || ch != 3 {
		t.Errorf("ToRunes = %d:%d", l, ch)
	}
	if p := FromRunes(1, 99, "ab"); p.Character != 2 {
		t.Errorf("past the end = %+v", p)
	}
	if URIPath(FileURI("/a b/c.go")) != "/a b/c.go" || URIPath("https://x/y") != "" {
		t.Error("URIs")
	}
}

// Start runs a real process: this test binary, as a server that answers
// the opening exchange and shutdown (TestMain), and stops it on Close.
func TestStartAndStopAProcess(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	c, err := Start(ctx, Config{Name: "self", Command: exe, Env: append(os.Environ(), "GNOSH_FAKE_LSP=1"), Root: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if !c.Has("hoverProvider") {
		t.Error("the process's capabilities")
	}
	_ = c.Close()
	select {
	case <-c.exited:
	case <-time.After(5 * time.Second):
		t.Fatal("the server process was not stopped")
	}
	if _, err := Start(ctx, Config{Command: filepath.Join(t.TempDir(), "no-such-server"), Root: t.TempDir()}); err == nil || !strings.Contains(err.Error(), "could not be started") {
		t.Errorf("a missing server: %v", err)
	}

	// A bare name is looked for on the PATH the server is given, not on
	// this process's own, and not in a relative folder of it.
	bin := t.TempDir()
	if err := os.Symlink(exe, filepath.Join(bin, "fake-lsp-server")); err != nil {
		t.Fatal(err)
	}
	c, err = Start(ctx, Config{Name: "self", Command: "fake-lsp-server", Env: []string{"PATH=.:" + bin, "GNOSH_FAKE_LSP=1"}, Root: t.TempDir()})
	if err != nil {
		t.Fatalf("a server on the PATH it is given: %v", err)
	}
	_ = c.Close()
	if _, err := Start(ctx, Config{Command: "fake-lsp-server", Env: []string{"PATH=.", "GNOSH_FAKE_LSP=1"}, Dir: bin, Root: t.TempDir()}); err == nil || !strings.Contains(err.Error(), "not a program on the PATH it is given") {
		t.Errorf("a server in a relative PATH folder: %v", err)
	}
}

// TestMain runs the test binary as a made-up server when asked to.
func TestMain(m *testing.M) {
	if os.Getenv("GNOSH_FAKE_LSP") == "1" {
		r := bufio.NewReaderSize(os.Stdin, maxHeader)
		for {
			body, err := readMessage(r)
			if err != nil {
				os.Exit(0)
			}
			var msg message
			_ = json.Unmarshal(body, &msg)
			var result any
			switch msg.Method {
			case "initialize":
				result = map[string]any{"capabilities": map[string]any{"hoverProvider": true}}
			case "exit":
				os.Exit(0)
			}
			if len(msg.ID) > 0 {
				raw, _ := json.Marshal(result)
				out, _ := json.Marshal(message{JSONRPC: "2.0", ID: msg.ID, Result: raw})
				_, _ = os.Stdout.Write(frame(out))
			}
		}
	}
	os.Exit(m.Run())
}

// Settings are sent once the server has started, and the server's
// questions about its configuration are answered from them, section by
// section.
func TestSettings(t *testing.T) {
	c, f := newFakeWith(t, json.RawMessage(`{"gopls":{"buildFlags":["-tags=x"],"a":{"b":2}}}`), func(f *fake, m message) {
		if m.Method == "initialized" {
			f.write(message{ID: json.RawMessage(`7`), Method: "workspace/configuration",
				Params: json.RawMessage(`{"items":[{"section":"gopls"},{"section":"gopls.a.b"},{"section":"other"}]}`)})
			return
		}
		standard(f, m)
	})
	waitFor(t, func() bool {
		f.mu.Lock()
		defer f.mu.Unlock()
		for _, m := range f.got {
			if string(m.ID) == "7" && string(m.Result) == `[{"buildFlags":["-tags=x"],"a":{"b":2}},2,null]` {
				return true
			}
		}
		return false
	}, "the configuration answered from the settings")
	sent := f.received("workspace/didChangeConfiguration")
	if len(sent) != 1 || !strings.Contains(string(sent[0].Params), `"buildFlags":["-tags=x"]`) {
		t.Errorf("didChangeConfiguration: %+v", sent)
	}
	_ = c
}

// A late publish of an older version of a file is not taken for the
// diagnostics of the version just sent: Settle waits for its own.
func TestSettleWaitsForItsVersion(t *testing.T) {
	c, f := newFake(t, func(f *fake, m message) {
		if m.Method == "textDocument/didChange" {
			// The answer to the change before, late, then this one's.
			old, _ := json.Marshal(map[string]any{"uri": FileURI("/w/a.go"), "version": 1, "diagnostics": []Diagnostic{{Message: "old"}}})
			f.write(message{Method: "textDocument/publishDiagnostics", Params: old})
			time.Sleep(50 * time.Millisecond)
			cur, _ := json.Marshal(map[string]any{"uri": FileURI("/w/a.go"), "version": 2, "diagnostics": []Diagnostic{{Message: "new"}}})
			f.write(message{Method: "textDocument/publishDiagnostics", Params: cur})
			return
		}
		standard(f, m)
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	first, err := c.Sync(ctx, "/w/a.go", "go", "one")
	if err != nil || !c.Settle(ctx, "/w/a.go", first, 10*time.Millisecond) {
		t.Fatalf("open: %+v, %v", first, err)
	}
	s, err := c.Sync(ctx, "/w/a.go", "go", "two")
	if err != nil || !c.Settle(ctx, "/w/a.go", s, 10*time.Millisecond) {
		t.Fatalf("settle: %+v, %v", s, err)
	}
	if d := c.Diagnostics()["/w/a.go"]; len(d) != 1 || d[0].Message != "new" {
		t.Errorf("settled on %+v, want the new version's", d)
	}
	_ = f
}

// A writer waiting for another, which a server that reads nothing holds up,
// gives up when its own context ends: the reader, answering a request
// itself, among them (the second review).
func TestSendGivesUpBehindAnotherWriter(t *testing.T) {
	c, _ := newFake(t, standard)
	c.writing <- struct{}{} // another writer, stuck
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	began := time.Now()
	err := c.send(ctx, message{Method: "x"})
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(began) > 5*time.Second {
		t.Errorf("send behind a stuck writer: %v after %v", err, time.Since(began))
	}
	<-c.writing
}
