package memjoin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gnosh-ai/gnosh/internal/agent"
	"github.com/gnosh-ai/gnosh/internal/app"
	"github.com/gnosh-ai/gnosh/internal/app/apptest"
	"github.com/gnosh-ai/gnosh/internal/cli"
	"github.com/gnosh-ai/gnosh/internal/core"
	"github.com/gnosh-ai/gnosh/internal/gnovm"
	"github.com/gnosh-ai/gnosh/internal/prompt"
	"github.com/gnosh-ai/gnosh/internal/transcript"
)

// safeBuffer is a buffer several goroutines may write to and read.
type safeBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *safeBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *safeBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// result is how one run of the program ended.
type result struct {
	code     int
	out, err string
}

// world is a place to run gnosh in with the join: a home, a project
// folder, and a model server that answers from a script. Nothing in it is
// the real machine's: HOME and the XDG directories lie in a temporary
// directory, and the GnoVM runs in the test's own process.
type world struct {
	*apptest.World
	t      *testing.T
	script *script

	mu sync.Mutex
	// sent holds every request the tools sent to the GnoVM, in order.
	sent []gnovm.RunRequest
}

// feature is the join as the tests run it: its machines in this process,
// and every request to them noted.
func (w *world) feature() app.Feature {
	return New(Options{InProcess: true, watch: func(req gnovm.RunRequest) {
		w.mu.Lock()
		w.sent = append(w.sent, req)
		w.mu.Unlock()
	}})
}

// memRequests returns the requests on mem that the tools sent, in order.
func (w *world) memRequests() []gnovm.RunRequest {
	w.mu.Lock()
	defer w.mu.Unlock()
	var out []gnovm.RunRequest
	for _, r := range w.sent {
		if r.Name == gnovm.MemVM {
			out = append(out, r)
		}
	}
	return out
}

// newWorld makes a world whose model answers the session's own requests
// with replies, in order, and the join's side requests by itself.
func newWorld(t *testing.T, replies ...apptest.Reply) *world {
	t.Helper()
	w := &world{World: apptest.NewWorld(t), t: t}
	w.script = &script{t: t, main: replies, side: map[string][]apptest.Request{}}
	w.Model.Respond(w.script.respond)
	return w
}

// options are the Options a test runs the program with: the world's
// environment and folder, the join with its machines in this process, and
// waits short enough for a test.
func (w *world) options(args ...string) app.Options {
	return app.Options{
		Args:      args,
		Stdin:     strings.NewReader(""),
		Environ:   w.Env(),
		Cwd:       w.Dir,
		Features:  []app.Feature{w.feature()},
		Build:     cli.Build{Commit: "0123456789ab", Time: time.Date(2026, 11, 13, 0, 0, 0, 0, time.UTC)},
		BuildID:   "test-build",
		StdinWait: 50 * time.Millisecond,
		StopGrace: 3 * time.Second,
		ModelWait: 5 * time.Second,
		Retry: agent.RetryPolicy{
			Pauses:          []time.Duration{10 * time.Millisecond},
			GoneAfter:       400 * time.Millisecond,
			NotRunningPause: 10 * time.Millisecond,
			NotRunningAfter: 400 * time.Millisecond,
			BusyMax:         20 * time.Millisecond,
		},
	}
}

// run runs the program with args in the world's folder and waits for it.
func (w *world) run(args ...string) result {
	w.t.Helper()
	return w.runWith(w.options(args...))
}

// runWith runs the program with o and waits for it, failing the test when
// it does not end.
func (w *world) runWith(o app.Options) result {
	w.t.Helper()
	var out, errb safeBuffer
	if o.Stdout == nil {
		o.Stdout = &out
	}
	if o.Stderr == nil {
		o.Stderr = &errb
	}
	done := make(chan int, 1)
	go func() { done <- app.Main(context.Background(), o) }()
	select {
	case code := <-done:
		return result{code: code, out: out.String(), err: errb.String()}
	case <-time.After(120 * time.Second):
		buf := make([]byte, 1<<20)
		n := runtime.Stack(buf, true)
		w.t.Fatalf("the program did not end within two minutes.\nstdout:\n%s\nstderr:\n%s\ngoroutines:\n%s", out.String(), errb.String(), buf[:n])
	}
	return result{}
}

// wantCode fails the test unless the run ended with the code.
func (r result) wantCode(t *testing.T, code int) result {
	t.Helper()
	if r.code != code {
		t.Fatalf("exit code %d, want %d\nstdout:\n%s\nstderr:\n%s", r.code, code, r.out, r.err)
	}
	return r
}

// gnoDir is the gnovm data directory of the world.
func (w *world) gnoDir() string { return DataDir(w.DataDir()) }

// session opens a session file of the world as gnosh itself would,
// read-only.
func (w *world) session(path string) *transcript.Session {
	w.t.Helper()
	s, err := transcript.New(w.DataDir()).OpenPath(context.Background(), path, true)
	if err != nil {
		w.t.Fatalf("the session %s cannot be opened: %v", path, err)
	}
	w.t.Cleanup(func() { s.Close() })
	return s
}

// script answers the model server's requests. The session's own requests,
// which carry tools, are answered from main, in order. The join's side
// requests carry none and are answered by what they ask: a name for each
// text, an approval for a close, a verdict for a goal; a test may put its
// own answer in place with onSide.
type script struct {
	t *testing.T

	mu     sync.Mutex
	main   []apptest.Reply
	next   int
	reqs   []apptest.Request // the session's own requests, in order
	side   map[string][]apptest.Request
	onSide func(kind string, r apptest.Request) *apptest.Reply
	extra  int
}

// The kinds of side request, told by their instructions.
const (
	sideNamer  = "namer"
	sideReview = "close-review"
	sideJudge  = "goal-judge"
	sideTitle  = "title"
	sideOther  = "other"
)

func sideKind(r apptest.Request) string {
	set := prompt.Builtin()
	system := ""
	for _, m := range r.Messages() {
		if m["role"] == "system" {
			system = apptest.Text(m)
			break
		}
	}
	switch {
	case system == set.Namer([]string{"x"}).System:
		return sideNamer
	case system == set.CloseReview(prompt.CloseReviewInput{}).System:
		return sideReview
	case system == set.GoalJudge(prompt.GoalJudgeInput{}).System:
		return sideJudge
	case system == set.Title("x").System:
		return sideTitle
	}
	return sideOther
}

func (s *script) respond(n int, r apptest.Request) *apptest.Reply {
	s.mu.Lock()
	defer s.mu.Unlock()
	if tools, _ := r.JSON["tools"].([]any); len(tools) > 0 {
		s.reqs = append(s.reqs, r)
		if s.next >= len(s.main) {
			s.extra++
			return &apptest.Reply{Text: "(the scripted model has no more replies)"}
		}
		rep := s.main[s.next]
		s.next++
		return &rep
	}
	kind := sideKind(r)
	s.side[kind] = append(s.side[kind], r)
	if s.onSide != nil {
		if rep := s.onSide(kind, r); rep != nil {
			return rep
		}
	}
	switch kind {
	case sideReview:
		return &apptest.Reply{Text: `{"ok": true, "reason": "what happened in the todo shows it"}`}
	case sideJudge:
		return &apptest.Reply{Text: `{"ok": true, "basis": "conversation", "reason": "the conversation shows it"}`}
	case sideTitle:
		return &apptest.Reply{Text: "A test session"}
	}
	// A namer that answers with nothing a name can be made of: mem then
	// names from the words, which is what the tests' names are written
	// for.
	return &apptest.Reply{Text: "!!!"}
}

// add appends replies for the session's own requests.
func (s *script) add(replies ...apptest.Reply) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.main = append(s.main, replies...)
}

// requests returns the session's own requests so far, in order.
func (s *script) requests() []apptest.Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]apptest.Request(nil), s.reqs...)
}

// sides returns the side requests of a kind so far, in order.
func (s *script) sides(kind string) []apptest.Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]apptest.Request(nil), s.side[kind]...)
}

// done fails the test unless every scripted reply was given and no request
// came after the last.
func (s *script) done() {
	s.t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.next < len(s.main) {
		s.t.Errorf("the model gave %d of the %d replies of its script", s.next, len(s.main))
	}
	if s.extra > 0 {
		s.t.Errorf("the model was sent %d requests more than the script has replies for", s.extra)
	}
}

// memCall is a scripted call on mem.
func memCall(id, code string) apptest.Call {
	args, _ := json.Marshal(map[string]string{"name": "mem", "code": code})
	return apptest.Call{ID: id, Name: "gnovm_run", Args: string(args)}
}

// call is a scripted call of a tool, with its arguments as a map.
func call(id, name string, args map[string]any) apptest.Call {
	raw, _ := json.Marshal(args)
	return apptest.Call{ID: id, Name: name, Args: string(raw)}
}

// reply is a scripted reply that makes calls.
func reply(calls ...apptest.Call) apptest.Reply { return apptest.Reply{Calls: calls} }

// says is a scripted reply of words alone.
func says(text string) apptest.Reply { return apptest.Reply{Text: text} }

// message is one message of a request, as a test reads it.
type message struct {
	role  string
	text  string   // its text, parts joined by line breaks
	calls []string // the ids of its tool calls
	names []string // their tools
	args  []string // their arguments
	id    string   // for a tool message, the call it answers
	raw   string   // the message as it was sent
}

// messagesOf lays out the messages of a request.
func messagesOf(r apptest.Request) []message {
	var out []message
	for _, m := range r.Messages() {
		raw, _ := json.Marshal(m)
		msg := message{raw: string(raw), text: apptest.Text(m)}
		msg.role, _ = m["role"].(string)
		msg.id, _ = m["tool_call_id"].(string)
		calls, _ := m["tool_calls"].([]any)
		for _, c := range calls {
			cm, _ := c.(map[string]any)
			id, _ := cm["id"].(string)
			fn, _ := cm["function"].(map[string]any)
			name, _ := fn["name"].(string)
			args, _ := fn["arguments"].(string)
			msg.calls = append(msg.calls, id)
			msg.names = append(msg.names, name)
			msg.args = append(msg.args, args)
		}
		out = append(out, msg)
	}
	return out
}

// conversation is a request's messages after the system prompt, each on a
// line, for a failure message.
func conversation(r apptest.Request) string {
	var b strings.Builder
	for i, m := range messagesOf(r) {
		text := m.text
		if len(text) > 300 {
			text = text[:300] + "…"
		}
		fmt.Fprintf(&b, "%2d %-9s %s", i, m.role, strings.ReplaceAll(text, "\n", "⏎"))
		for k := range m.calls {
			fmt.Fprintf(&b, " [calls %s %s]", m.names[k], m.args[k])
		}
		if m.id != "" {
			fmt.Fprintf(&b, " [answers %s]", m.id)
		}
		b.WriteString("\n")
	}
	return b.String()
}

// has fails the test unless text holds every one of the pieces.
func has(t *testing.T, what, text string, pieces ...string) {
	t.Helper()
	for _, p := range pieces {
		if !strings.Contains(text, p) {
			t.Errorf("%s does not hold %q:\n%s", what, p, clip(text))
		}
	}
}

// hasNot fails the test if text holds any of the pieces.
func hasNot(t *testing.T, what, text string, pieces ...string) {
	t.Helper()
	for _, p := range pieces {
		if i := strings.Index(text, p); i >= 0 {
			t.Errorf("%s holds %q and must not: ...%s...", what, p, text[max(0, i-200):min(len(text), i+len(p)+200)])
		}
	}
}

// inOrder fails the test unless text holds every one of the pieces, each
// after the one before it.
func inOrder(t *testing.T, what, text string, pieces ...string) {
	t.Helper()
	at := 0
	for _, p := range pieces {
		i := strings.Index(text[at:], p)
		if i < 0 {
			t.Errorf("%s does not hold %q after what comes before it:\n%s", what, p, clip(text))
			return
		}
		at += i + len(p)
	}
}

// clip shortens a long text for a failure message: its start and its end.
func clip(s string) string {
	if len(s) <= 3000 {
		return s
	}
	return s[:1200] + fmt.Sprintf("\n   [... %d bytes left out ...]\n", len(s)-3000) + s[len(s)-1800:]
}

// seat is a person at a terminal, for a test: what they type goes down a
// pipe that the program is told is a terminal, and what the program prints
// is read as it comes.
type seat struct {
	t       *testing.T
	in      *io.PipeWriter
	out     *safeBuffer
	errs    *safeBuffer
	signals chan os.Signal
	done    chan int
	pos     int
}

// sit starts an interactive session with a person at its terminal.
func (w *world) sit(args ...string) *seat {
	w.t.Helper()
	pr, pw := io.Pipe()
	s := &seat{t: w.t, in: pw, out: &safeBuffer{}, errs: &safeBuffer{}, signals: make(chan os.Signal, 4), done: make(chan int, 1)}
	o := w.options(args...)
	o.Stdin, o.Stdout, o.Stderr = pr, s.out, s.errs
	o.StdinIsTerminal, o.StdoutIsTerminal = true, true
	o.Signals = s.signals
	go func() { s.done <- app.Main(context.Background(), o) }()
	w.t.Cleanup(func() {
		pw.Close()
		select {
		case code := <-s.done:
			s.done <- code
		case <-time.After(30 * time.Second):
			w.t.Errorf("the session did not end when its input was closed.\nscreen:\n%s", s.out.String())
		}
	})
	return s
}

// see waits until the screen shows piece after everything seen so far.
func (s *seat) see(piece string) {
	s.t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	for {
		screen := s.out.String()
		if i := strings.Index(screen[s.pos:], piece); i >= 0 {
			s.pos += i + len(piece)
			return
		}
		if time.Now().After(deadline) {
			s.t.Fatalf("the screen never showed %q after what was seen before.\nseen before:\n%s\nsince then:\n%s\nstderr:\n%s", piece, clip(screen[:s.pos]), clip(screen[s.pos:]), s.errs.String())
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// typeLine types a line and Enter.
func (s *seat) typeLine(line string) {
	s.t.Helper()
	if _, err := io.WriteString(s.in, line+"\n"); err != nil {
		s.t.Fatalf("typing %q: %v", line, err)
	}
}

// exit waits for the program to end and returns its exit code.
func (s *seat) exit() int {
	s.t.Helper()
	select {
	case code := <-s.done:
		s.done <- code
		return code
	case <-time.After(60 * time.Second):
		s.t.Fatalf("the program did not end.\nscreen:\n%s", clip(s.out.String()))
	}
	return -1
}

// front is a front end as another interface would be attached to a
// session (app.Options.Interactive): it keeps the events it is told, and
// what it does with the session's controls is the test's to say.
type front struct {
	t   *testing.T
	run func(ctx context.Context, c app.SessionControls, f *front) int

	mu     sync.Mutex
	events []any
}

// Event keeps an event.
func (f *front) Event(ev any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, ev)
}

// Run hands the test the session's controls.
func (f *front) Run(ctx context.Context, c app.Controls) int {
	more, ok := c.(app.SessionControls)
	if !ok {
		f.t.Errorf("the front end was handed controls of type %T, which are not SessionControls", c)
		return 1
	}
	return f.run(ctx, more, f)
}

// attach runs an interactive session of the world with a scripted front
// end attached, and waits for it to end.
func (w *world) attach(run func(ctx context.Context, c app.SessionControls, f *front) int, args ...string) (*front, result) {
	w.t.Helper()
	f := &front{t: w.t, run: run}
	o := w.options(args...)
	o.StdinIsTerminal, o.StdoutIsTerminal = true, true
	o.Signals = make(chan os.Signal, 4)
	o.Interactive = func(app.FrontendInfo) app.Frontend { return f }
	return f, w.runWith(o)
}

// say sends a prompt as the person would type it and waits until the
// session has nothing in hand and the front end has been told everything.
func (f *front) say(ctx context.Context, c app.SessionControls, text string) {
	f.t.Helper()
	if _, err := c.Submit(agent.Prompt{Text: text}); err != nil {
		f.t.Errorf("the prompt %q could not be sent: %v", text, err)
		return
	}
	if err := c.Wait(ctx); err != nil {
		f.t.Errorf("waiting for the turn of %q: %v", text, err)
	}
}

// mark returns how many events the front end has been told so far, so
// that a test can read what came after.
func (f *front) mark() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.events)
}

// facts returns the status facts the front end was told from the event
// numbered from on, each as "name=value", in order.
func (f *front) facts(from int) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, ev := range f.events[from:] {
		if se, ok := ev.(*app.StatusEvent); ok {
			for _, name := range []string{"frame", "goal"} {
				if v, has := se.Facts[name]; has {
					out = append(out, name+"="+v)
				}
			}
		}
	}
	return out
}

// taskLists returns the task lists the front end was told from the event
// numbered from on, each as one line.
func (f *front) taskLists(from int) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, ev := range f.events[from:] {
		if te, ok := ev.(*app.TasksEvent); ok {
			out = append(out, taskLine(te.Tasks))
		}
	}
	return out
}

// noticed returns the lines the person was told from the event numbered
// from on, in order.
func (f *front) noticed(from int) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, ev := range f.events[from:] {
		if ne, ok := ev.(*agent.NoticeEvent); ok {
			out = append(out, ne.Text)
		}
	}
	return out
}

// taskLine writes a task list as a front end shows it, on one line: a
// line that is done, the one in progress as it reads while it is being
// done, and the ones still to come.
func taskLine(tasks []app.Task) string {
	var parts []string
	for _, t := range tasks {
		switch t.Status {
		case app.TaskCompleted:
			parts = append(parts, "[x] "+t.Content)
		case app.TaskInProgress:
			text := t.ActiveForm
			if text == "" {
				text = t.Content
			}
			parts = append(parts, "[>] "+text)
		default:
			parts = append(parts, "[ ] "+t.Content)
		}
	}
	return strings.Join(parts, " | ")
}

// results returns the text of every tool result of a request, by the code
// or arguments of the call it answers, in order.
func results(r apptest.Request) []string {
	var out []string
	for _, m := range messagesOf(r) {
		if m.role == "tool" {
			out = append(out, m.text)
		}
	}
	return out
}

// lastResult returns the text of the last tool result of a request.
func lastResult(t *testing.T, r apptest.Request) string {
	t.Helper()
	all := results(r)
	if len(all) == 0 {
		t.Fatalf("the request holds no tool result:\n%s", conversation(r))
	}
	return all[len(all)-1]
}

// lastUser returns the text of the last user message of a request.
func lastUser(t *testing.T, r apptest.Request) string {
	t.Helper()
	msgs := messagesOf(r)
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].role == "user" {
			return msgs[i].text
		}
	}
	t.Fatalf("the request holds no user message:\n%s", conversation(r))
	return ""
}

// coreText is a message of one text block.
func coreText(s string) core.Message { return core.UserText(s) }
