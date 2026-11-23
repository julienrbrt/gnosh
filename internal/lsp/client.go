package lsp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gnosh-ai/gnosh/internal/execpath"
)

// Config says how to start a server and what to tell it.
type Config struct {
	// Name is how the server is named in messages ("gopls").
	Name string
	// Command and Args start it, in Dir, with the environment Env (nil is
	// this process's own).
	Command string
	Args    []string
	Env     []string
	Dir     string
	// Root is the workspace folder, an absolute path.
	Root string
	// InitOptions is sent as initializationOptions, when not empty.
	InitOptions json.RawMessage
	// Settings, when not empty, is sent by workspace/didChangeConfiguration
	// once the server has started, and is what workspace/configuration is
	// answered from: each item's section looked up in it by its dots.
	Settings json.RawMessage
	// StartWait is how long the server may take to answer initialize.
	// Default 30 seconds: gopls loads a large module that long.
	StartWait time.Duration
	// StopWait is how long Close waits for the server to answer shutdown
	// and end before it is stopped. Default 2 seconds.
	StopWait time.Duration
}

// ErrClosed is the error of a call on a connection that has ended.
var ErrClosed = errors.New("the language server's connection has ended")

// Client is one running server.
type Client struct {
	name     string
	root     string
	settings json.RawMessage
	stopWait time.Duration
	cmd      *exec.Cmd // nil when the server is not a process of the client's
	in       *os.File  // the server's standard input
	errs     *tail     // what it wrote on its standard error

	// writing is the one slot for a writer: a channel and not a mutex, so
	// that a writer waiting behind one held up by a server that reads
	// nothing gives up when its own context ends (the reader, answering a
	// request itself, among them; the second review).
	writing chan struct{}
	broken  error // a write gave up partway: nothing more is sent

	mu      sync.Mutex
	next    int64
	pending map[int64]chan reply
	err     error // why the connection ended
	caps    map[string]json.RawMessage
	docs    map[string]*document
	diags   map[string]published
	seq     uint64        // publishes so far, of every file
	changed chan struct{} // closed at each publish, then made again

	done      chan struct{} // closed when the reader stops
	exited    chan struct{} // closed when the server's process has exited
	closeOnce sync.Once
	answering chan struct{} // a slot for each request of the server's being answered
}

// answersAtOnce is how many of the server's requests are answered at
// once, each by a goroutine of its own; more are answered in turn, by the
// reader (the language servers review of 2026-11-20: one goroutine a
// request, with no limit, let a server make as many as it liked).
const answersAtOnce = 16

// document is a file the server has been told of.
type document struct {
	version int
	text    string
}

// published is what the server last said of one file.
type published struct {
	seq     uint64 // the publish it came in, counted over every file
	version int    // the version of the file it is of, or -1 when the server did not say
	diags   []Diagnostic
}

// reply is a response to one of the client's requests.
type reply struct {
	result json.RawMessage
	err    error
}

// rpcError is the error of a response.
type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *rpcError) Error() string { return fmt.Sprintf("%s (code %d)", e.Message, e.Code) }

// message is any message, either way.
type message struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

// writeWait is the longest one message may wait to be taken by a server
// that does not read its input; the connection ends then.
const writeWait = time.Minute

// stderrWait is how long the reader waits, once the server has exited,
// for the last of its output: a process it left running outside its group
// may hold the output open for ever.
const stderrWait = 2 * time.Second

// Start starts the server and makes the protocol's opening exchange with
// it (initialize, then initialized).
func Start(ctx context.Context, cfg Config) (*Client, error) {
	// The program on the PATH it is given (the settings' env block or the
	// entry's own env may have changed it), not on gnosh's.
	cmd, ok := execpath.Command(cfg.Command, cfg.Args, cfg.Env, cfg.Dir)
	if !ok {
		return nil, fmt.Errorf("%s could not be started: it is not a program on the PATH it is given", cfg.Command)
	}
	ownGroup(cmd)
	errs := &tail{max: 16 << 10}
	cmd.Stderr = errs
	cmd.WaitDelay = time.Second
	// Pipes of the client's own, not exec's: a write to the server's
	// input can then be given up (a deadline), and Wait does not close the
	// output before the last of it is read.
	inR, in, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	out, outW, err := os.Pipe()
	if err != nil {
		inR.Close()
		in.Close()
		return nil, err
	}
	cmd.Stdin, cmd.Stdout = inR, outW
	err = cmd.Start()
	inR.Close()
	outW.Close()
	if err != nil {
		in.Close()
		out.Close()
		return nil, fmt.Errorf("%s could not be started: %w", cfg.Command, err)
	}
	c := newClient(cfg, in)
	c.cmd, c.errs = cmd, errs
	go func() {
		_ = cmd.Wait()
		close(c.exited)
	}()
	// (The reader starts once the client knows its process: output that
	// is not the protocol closes the client at once, which stops it.)
	go c.read(out)
	if err := c.initialize(ctx, cfg); err != nil {
		_ = c.Close()
		if said := strings.TrimSpace(errs.String()); said != "" {
			err = fmt.Errorf("%w; it wrote: %s", err, clip(said, 500))
		}
		return nil, err
	}
	return c, nil
}

// connect makes a client of a server already running at the other end of
// in and out, and starts reading.
func connect(cfg Config, in, out *os.File) *Client {
	c := newClient(cfg, in)
	go c.read(out)
	return c
}

// newClient makes a client that writes to in, not yet reading.
func newClient(cfg Config, in *os.File) *Client {
	name := cfg.Name
	if name == "" {
		name = filepath.Base(cfg.Command)
	}
	return &Client{name: name, root: cfg.Root, settings: cfg.Settings, stopWait: cfg.StopWait, in: in, errs: &tail{},
		pending: map[int64]chan reply{}, docs: map[string]*document{}, diags: map[string]published{},
		changed: make(chan struct{}), done: make(chan struct{}), exited: make(chan struct{}),
		answering: make(chan struct{}, answersAtOnce), writing: make(chan struct{}, 1)}
}

// Name is the server's name.
func (c *Client) Name() string { return c.name }

// initialize makes the opening exchange.
func (c *Client) initialize(ctx context.Context, cfg Config) error {
	wait := cfg.StartWait
	if wait <= 0 {
		wait = 30 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, wait)
	defer cancel()
	root := FileURI(cfg.Root)
	params := map[string]any{
		"processId":        os.Getpid(),
		"clientInfo":       map[string]string{"name": "gnosh"},
		"rootUri":          root,
		"workspaceFolders": []map[string]string{{"uri": root, "name": filepath.Base(cfg.Root)}},
		"capabilities": map[string]any{
			"general": map[string]any{"positionEncodings": []string{"utf-16"}},
			"textDocument": map[string]any{
				"synchronization":    map[string]any{"didSave": true},
				"publishDiagnostics": map[string]any{"versionSupport": true},
				"definition":         map[string]any{"linkSupport": true},
				"references":         map[string]any{},
				"hover":              map[string]any{"contentFormat": []string{"plaintext", "markdown"}},
				"documentSymbol":     map[string]any{"hierarchicalDocumentSymbolSupport": true},
				"implementation":     map[string]any{"linkSupport": true},
				"callHierarchy":      map[string]any{},
			},
			"workspace": map[string]any{"symbol": map[string]any{}, "workspaceFolders": true, "configuration": true},
			"window":    map[string]any{"workDoneProgress": true},
		},
	}
	if len(cfg.InitOptions) > 0 {
		params["initializationOptions"] = cfg.InitOptions
	}
	var res struct {
		Capabilities map[string]json.RawMessage `json:"capabilities"`
	}
	if err := c.call(ctx, "initialize", params, &res); err != nil {
		return fmt.Errorf("%s did not start: %w", c.name, err)
	}
	c.mu.Lock()
	c.caps = res.Capabilities
	c.mu.Unlock()
	if err := c.notify(ctx, "initialized", struct{}{}); err != nil {
		return err
	}
	if len(c.settings) > 0 {
		return c.notify(ctx, "workspace/didChangeConfiguration", map[string]json.RawMessage{"settings": c.settings})
	}
	return nil
}

// section is what the settings hold under a dotted section name ("gopls",
// "go.buildFlags"), or null; "" is the whole of them.
func (c *Client) section(name string) json.RawMessage {
	if len(c.settings) == 0 {
		return json.RawMessage("null")
	}
	cur := c.settings
	for _, part := range strings.Split(name, ".") {
		if part == "" {
			continue
		}
		var obj map[string]json.RawMessage
		if json.Unmarshal(cur, &obj) != nil {
			return json.RawMessage("null")
		}
		next, ok := obj[part]
		if !ok {
			return json.RawMessage("null")
		}
		cur = next
	}
	return cur
}

// Has reports whether the server said it does what capability names
// ("definitionProvider", "callHierarchyProvider"): present, and neither
// false nor null.
func (c *Client) Has(capability string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	raw, ok := c.caps[capability]
	s := strings.TrimSpace(string(raw))
	return ok && s != "false" && s != "null"
}

// read takes the server's messages until its output ends, or is not
// messages, or a little after the server has exited.
func (c *Client) read(out *os.File) {
	defer close(c.done)
	defer out.Close()
	go func() {
		select {
		case <-c.exited:
			select {
			case <-c.done:
			case <-time.After(stderrWait):
				_ = out.Close()
			}
		case <-c.done:
		}
	}()
	r := bufio.NewReaderSize(out, maxHeader)
	for {
		body, err := readMessage(r)
		if err != nil {
			c.lost(err)
			return
		}
		var m message
		if json.Unmarshal(body, &m) != nil {
			c.lost(fmt.Errorf("%w: a message that is not JSON", errNotProtocol))
			return
		}
		c.dispatch(m)
	}
}

// lost ends the connection: every call that waits is told why.
func (c *Client) lost(err error) {
	if errors.Is(err, io.EOF) || errors.Is(err, os.ErrClosed) {
		err = nil
	}
	c.mu.Lock()
	if c.err == nil {
		if err == nil {
			c.err = ErrClosed
		} else {
			c.err = fmt.Errorf("%w: %v", ErrClosed, err)
		}
	}
	for id, ch := range c.pending {
		ch <- reply{err: c.err}
		delete(c.pending, id)
	}
	c.mu.Unlock()
	if err != nil && errors.Is(err, errNotProtocol) {
		// (Claude Code disconnects such a server, and so does gnosh.)
		go c.Close()
	}
}

// Err says why the connection ended, or nil while it lasts.
func (c *Client) Err() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.err
}

// dispatch hands a message on: a response to the call that waits for it,
// a request of the server's to be answered, a notification to be kept.
func (c *Client) dispatch(m message) {
	switch {
	case m.Method != "" && len(m.ID) > 0:
		select {
		case c.answering <- struct{}{}:
			go func() {
				defer func() { <-c.answering }()
				c.answer(m)
			}()
		default:
			c.answer(m)
		}
	case m.Method == "textDocument/publishDiagnostics":
		c.publish(m.Params)
	case m.Method != "":
		// (Logs, progress, and whatever else a server says.)
	case len(m.ID) > 0:
		id, err := strconv.ParseInt(string(m.ID), 10, 64)
		if err != nil {
			return
		}
		c.mu.Lock()
		ch := c.pending[id]
		delete(c.pending, id)
		c.mu.Unlock()
		if ch == nil {
			return
		}
		if m.Error != nil {
			ch <- reply{err: m.Error}
			return
		}
		ch <- reply{result: m.Result}
	}
}

// answer answers a request of the server's, as a client that keeps no
// settings and changes no file.
func (c *Client) answer(m message) {
	var result any
	var fail *rpcError
	switch m.Method {
	case "workspace/configuration":
		var p struct {
			Items []struct {
				Section string `json:"section"`
			} `json:"items"`
		}
		_ = json.Unmarshal(m.Params, &p)
		items := make([]json.RawMessage, len(p.Items))
		for i, item := range p.Items {
			items[i] = c.section(item.Section)
		}
		result = items
	case "workspace/workspaceFolders":
		result = []map[string]string{{"uri": FileURI(c.root), "name": filepath.Base(c.root)}}
	case "client/registerCapability", "client/unregisterCapability", "window/workDoneProgress/create",
		"window/showMessageRequest", "workspace/diagnostic/refresh", "workspace/semanticTokens/refresh",
		"workspace/inlayHint/refresh", "workspace/codeLens/refresh":
		result = nil
	case "window/showDocument":
		result = map[string]bool{"success": false}
	case "workspace/applyEdit":
		result = map[string]any{"applied": false, "failureReason": "gnosh's client of language servers changes no file"}
	default:
		fail = &rpcError{Code: -32601, Message: "method not found: " + m.Method}
	}
	out := message{ID: m.ID}
	if fail != nil {
		out.Error = fail
	} else {
		raw, err := json.Marshal(result)
		if err != nil {
			return
		}
		out.Result = raw
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = c.send(ctx, out)
}

// publish keeps the diagnostics the server published for a file.
func (c *Client) publish(params json.RawMessage) {
	var p struct {
		URI         string       `json:"uri"`
		Version     *int         `json:"version"`
		Diagnostics []Diagnostic `json:"diagnostics"`
	}
	if json.Unmarshal(params, &p) != nil || p.URI == "" {
		return
	}
	version := -1
	if p.Version != nil {
		version = *p.Version
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.seq++
	c.diags[p.URI] = published{seq: c.seq, version: version, diags: p.Diagnostics}
	close(c.changed)
	c.changed = make(chan struct{})
}

// send writes one message. A server that has stopped reading its input
// does not hold the caller for ever: the write gives up when ctx ends or
// after writeWait, and one that gave up partway ends the connection.
func (c *Client) send(ctx context.Context, m message) error {
	m.JSONRPC = "2.0"
	body, err := json.Marshal(m)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case c.writing <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	defer func() { <-c.writing }()
	if c.broken != nil {
		return c.broken
	}
	deadline := time.Now().Add(writeWait)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	_ = c.in.SetWriteDeadline(deadline)
	fired := make(chan struct{})
	stop := context.AfterFunc(ctx, func() {
		_ = c.in.SetWriteDeadline(time.Now())
		close(fired)
	})
	n, err := c.in.Write(frame(body))
	if !stop() {
		// (The deadline was moved to now: it is put back before the
		// lock goes, so that it cannot cut the next writer's message.)
		<-fired
	}
	switch {
	case err == nil:
		return nil
	case n == 0 && ctx.Err() != nil:
		return ctx.Err()
	}
	c.broken = fmt.Errorf("%w: it stopped reading its input (%v)", ErrClosed, err)
	go c.Close()
	return c.broken
}

// notify sends a notification. params nil sends none.
func (c *Client) notify(ctx context.Context, method string, params any) error {
	m := message{Method: method}
	if params != nil {
		raw, err := json.Marshal(params)
		if err != nil {
			return err
		}
		m.Params = raw
	}
	return c.send(ctx, m)
}

// call makes a request and waits for its response, which it decodes into
// out (when not nil). params nil sends none. A call given up asks the
// server to cancel it.
func (c *Client) call(ctx context.Context, method string, params, out any) error {
	m := message{Method: method}
	if params != nil {
		raw, err := json.Marshal(params)
		if err != nil {
			return err
		}
		m.Params = raw
	}
	c.mu.Lock()
	if c.err != nil {
		err := c.err
		c.mu.Unlock()
		return err
	}
	c.next++
	id := c.next
	ch := make(chan reply, 1)
	c.pending[id] = ch
	c.mu.Unlock()
	m.ID = json.RawMessage(strconv.FormatInt(id, 10))
	forget := func() {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
	}
	if err := c.send(ctx, m); err != nil {
		forget()
		return err
	}
	select {
	case r := <-ch:
		if r.err != nil {
			return fmt.Errorf("%s: %w", method, r.err)
		}
		if out != nil && len(r.result) > 0 && string(r.result) != "null" {
			if err := json.Unmarshal(r.result, out); err != nil {
				return fmt.Errorf("%s: the answer could not be read: %w", method, err)
			}
		}
		return nil
	case <-ctx.Done():
		forget()
		cctx, cancel := context.WithTimeout(context.Background(), time.Second)
		_ = c.notify(cctx, "$/cancelRequest", map[string]int64{"id": id})
		cancel()
		return fmt.Errorf("%s: %w", method, ctx.Err())
	}
}

// Close ends the connection as the protocol asks (shutdown, then exit),
// and stops the server: SIGINT, SIGTERM and SIGKILL to its process group
// while it runs.
func (c *Client) Close() error {
	c.closeOnce.Do(func() {
		if c.Err() == nil {
			wait := c.stopWait
			if wait <= 0 {
				wait = 2 * time.Second
			}
			ctx, cancel := context.WithTimeout(context.Background(), wait)
			if c.call(ctx, "shutdown", nil, nil) == nil {
				_ = c.notify(ctx, "exit", nil)
			}
			cancel()
		}
		_ = c.in.Close()
		if c.cmd != nil {
			gone := func() bool {
				select {
				case <-c.exited:
					return true
				default:
					return false
				}
			}
			for step, wait := range []time.Duration{500 * time.Millisecond, 500 * time.Millisecond, time.Second, 2 * time.Second} {
				if gone() {
					stop(c.cmd, 2, true)
					break
				}
				if step > 0 {
					stop(c.cmd, step-1, false)
				}
				select {
				case <-c.exited:
				case <-time.After(wait):
				}
			}
		}
		select {
		case <-c.done:
		case <-time.After(stderrWait + time.Second):
		}
		c.lost(nil)
	})
	return nil
}

// tail keeps the last max bytes written to it.
type tail struct {
	mu  sync.Mutex
	max int
	b   []byte
}

func (t *tail) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.max <= 0 {
		return len(p), nil
	}
	t.b = append(t.b, p...)
	if over := len(t.b) - t.max; over > 0 {
		t.b = append(t.b[:0], t.b[over:]...)
	}
	return len(p), nil
}

func (t *tail) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return string(t.b)
}
