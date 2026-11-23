package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gnosh-ai/gnosh/internal/core"
)

// step is one answer of the scripted model.
type step struct {
	evs    []core.Event
	err    error         // yielded after evs
	stall  bool          // after evs, wait until the request is cancelled
	hang   chan struct{} // after evs, wait until closed, whatever the context says
	wait   chan struct{} // before evs, wait until closed or cancelled
	at     func()        // called when the request arrives
	mid    func()        // called after evs have been yielded
	noStop bool          // end without a stop event and without an error
}

// fakeModel is a core.Provider that replays a script. When the script is
// used up it asks respond, and when there is none it refuses the request,
// so that a test never passes on an answer it did not write.
type fakeModel struct {
	name    string
	mu      sync.Mutex
	script  []step
	respond func(req *core.Request, n int) step
	reqs    []*core.Request
	calls   int

	// honest makes the model count tokens the way a server does: every
	// usage report says how long the request really was, by the loop's
	// own default estimate times dense, and how long the answer was.
	honest bool
	dense  float64 // how many real tokens an estimated one is; zero means one
	// window, when above zero, is the server's real context window: a
	// request longer than that is refused, and refused counts them.
	window  int
	refused int
	// says makes a refusal carry the server's own numbers, as llama.cpp's
	// does (n_prompt_tokens, n_ctx).
	says    bool
	longest int // the longest request that was accepted, in real tokens
}

// realTokens counts a request the way the honest model does.
func (f *fakeModel) realTokens(req *core.Request) int {
	counter := &Agent{est: defaultEstimate}
	n := float64(counter.estimate(req, Model{}))
	if f.dense > 0 {
		n *= f.dense
	}
	return int(n + 0.5)
}

func (f *fakeModel) Name() string { return f.name }

func (f *fakeModel) push(steps ...step) {
	f.mu.Lock()
	f.script = append(f.script, steps...)
	f.mu.Unlock()
}

// answer sets what the model does once its script is used up.
func (f *fakeModel) answer(respond func(req *core.Request, n int) step) {
	f.mu.Lock()
	f.respond = respond
	f.mu.Unlock()
}

func (f *fakeModel) requests() []*core.Request {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.reqs)
}

// stats returns how many requests the model refused as too long, and the
// longest it took.
func (f *fakeModel) stats() (refused, longest int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.refused, f.longest
}

func (f *fakeModel) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func (f *fakeModel) Stream(ctx context.Context, req *core.Request) iter.Seq2[core.Event, error] {
	f.mu.Lock()
	n := f.calls
	f.calls++
	f.reqs = append(f.reqs, cloneRequest(req))
	honest, window := f.honest, f.window
	real := 0
	if honest || window > 0 {
		real = f.realTokens(req)
	}
	var st step
	var respond func(*core.Request, int) step
	switch {
	case window > 0 && real > window:
		// A request the server cannot hold is refused before anything is
		// answered.
		f.refused++
		pe := &core.ProviderError{
			Provider: f.name, Kind: core.ErrContextTooLong, Status: 400,
			Message: fmt.Sprintf("the request (%d tokens) exceeds the available context size (%d tokens)", real, window),
		}
		if f.says {
			pe.Tokens, pe.Window = real, window
		}
		st = step{err: pe}
	case len(f.script) > 0:
		st = f.script[0]
		f.script = f.script[1:]
	case f.respond != nil:
		respond = f.respond
	default:
		st = step{err: &core.ProviderError{Provider: f.name, Kind: core.ErrInvalidRequest, Message: "the test's script has run out"}}
	}
	if st.err == nil || respond != nil {
		f.longest = max(f.longest, real)
	}
	f.mu.Unlock()
	if respond != nil {
		st = respond(req, n)
	}
	if honest || window > 0 {
		// Say how long the request and the answer really were.
		out := 0
		evs := slices.Clone(st.evs)
		for _, ev := range evs {
			if ev.Type == core.EventTextDelta || ev.Type == core.EventToolUseDelta {
				out += defaultEstimate(ev.Text)
			}
		}
		for i := range evs {
			if evs[i].Type == core.EventUsage {
				evs[i].Usage = core.Usage{InputTokens: real, OutputTokens: out}
			}
		}
		st.evs = evs
	}
	return func(yield func(core.Event, error) bool) {
		if st.at != nil {
			st.at()
		}
		if st.wait != nil {
			select {
			case <-st.wait:
			case <-ctx.Done():
				yield(core.Event{}, ctx.Err())
				return
			}
		}
		for _, ev := range st.evs {
			if ctx.Err() != nil {
				yield(core.Event{}, ctx.Err())
				return
			}
			if !yield(ev, nil) {
				return
			}
		}
		if st.mid != nil {
			st.mid()
		}
		switch {
		case st.hang != nil:
			<-st.hang
			yield(core.Event{}, errors.New("the hung request was let go"))
		case st.stall:
			<-ctx.Done()
			yield(core.Event{}, ctx.Err())
		case st.err != nil:
			yield(core.Event{}, st.err)
		}
	}
}

func usageEv(in, out int) core.Event {
	return core.Event{Type: core.EventUsage, Usage: core.Usage{InputTokens: in, OutputTokens: out}}
}

func stopEv(r core.StopReason) core.Event { return core.Event{Type: core.EventStop, StopReason: r} }

// say is a reply of plain text.
func say(text string) step {
	return step{evs: []core.Event{{Type: core.EventTextDelta, Text: text}, usageEv(100, 10), stopEv(core.StopEndTurn)}}
}

// sayIn is a reply of text that arrives in pieces.
func sayIn(pieces ...string) step {
	var evs []core.Event
	for _, p := range pieces {
		evs = append(evs, core.Event{Type: core.EventTextDelta, Text: p})
	}
	return step{evs: append(evs, usageEv(100, 10), stopEv(core.StopEndTurn))}
}

// tc is a tool call.
func tc(id, name, input string) core.ToolCall {
	return core.ToolCall{ID: id, Name: name, Input: json.RawMessage(input)}
}

// callEvents are the events that spell out tool calls.
func callEvents(calls ...core.ToolCall) []core.Event {
	var evs []core.Event
	for _, c := range calls {
		evs = append(evs,
			core.Event{Type: core.EventToolUseStart, ID: c.ID, Name: c.Name},
			core.Event{Type: core.EventToolUseDelta, ID: c.ID, Text: string(c.Input)},
			core.Event{Type: core.EventToolUseStop, ID: c.ID},
		)
	}
	return evs
}

// use is a reply that asks for tools.
func use(calls ...core.ToolCall) step {
	return step{evs: append(callEvents(calls...), usageEv(100, 10), stopEv(core.StopToolUse))}
}

// silence is a reply with nothing in it.
func silence() step {
	return step{evs: []core.Event{usageEv(100, 0), stopEv(core.StopEndTurn)}}
}

// thought is a reply that only reasons.
func thought(text string) step {
	return step{evs: []core.Event{{Type: core.EventThinkingDelta, Text: text}, usageEv(100, 5), stopEv(core.StopEndTurn)}}
}

// broken is a request that fails.
func broken(kind core.ErrorKind, status int) step {
	return step{err: &core.ProviderError{Provider: "fake", Kind: kind, Status: status, Message: "scripted " + string(kind)}}
}

// fakeTool is a tool whose behaviour a test writes.
type fakeTool struct {
	name string
	run  func(ctx context.Context, call core.ToolCall, env *core.ToolEnv) (core.ToolResult, error)
}

func (f *fakeTool) Spec() core.ToolSpec {
	return core.ToolSpec{Name: f.name, Description: "a test tool", InputSchema: json.RawMessage(`{"type":"object"}`)}
}

func (f *fakeTool) Run(ctx context.Context, call core.ToolCall, env *core.ToolEnv) (core.ToolResult, error) {
	if f.run == nil {
		return core.TextResult(f.name + " ran " + string(call.Input)), nil
	}
	return f.run(ctx, call, env)
}

// lookTool is a fakeTool that says its calls only look.
type lookTool struct{ fakeTool }

func (l *lookTool) Parallel(core.ToolCall) bool { return true }

// descTool is a fakeTool that describes its calls.
type descTool struct {
	fakeTool
	describe func(call core.ToolCall, env *core.ToolEnv) (core.Action, error)
}

func (d *descTool) Describe(call core.ToolCall, env *core.ToolEnv) (core.Action, error) {
	return d.describe(call, env)
}

func tool(name string, run func(ctx context.Context, call core.ToolCall, env *core.ToolEnv) (core.ToolResult, error)) *fakeTool {
	return &fakeTool{name: name, run: run}
}

func look(name string, run func(ctx context.Context, call core.ToolCall, env *core.ToolEnv) (core.ToolResult, error)) *lookTool {
	return &lookTool{fakeTool{name: name, run: run}}
}

// memLog is a Log kept in memory.
type memLog struct {
	mu      sync.Mutex
	entries []Entry
	cut     map[string]bool // entries slid out of the view
	fail    error           // returned by Append when set
	failOn  func(Entry) bool
	panicOn func(Entry) bool // makes Append crash
}

func newMemLog() *memLog { return &memLog{cut: map[string]bool{}} }

func (l *memLog) Append(_ context.Context, entries ...Entry) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.fail != nil {
		return l.fail
	}
	if l.failOn != nil {
		for _, e := range entries {
			if l.failOn(e) {
				return errors.New("the disk is full")
			}
		}
	}
	if l.panicOn != nil {
		for _, e := range entries {
			if l.panicOn(e) {
				panic("the log crashed")
			}
		}
	}
	l.entries = append(l.entries, entries...)
	return nil
}

func (l *memLog) all() []Entry {
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.Clone(l.entries)
}

// of returns the entries of one kind, in order.
func (l *memLog) of(kind EntryKind) []Entry {
	var out []Entry
	for _, e := range l.all() {
		if e.Kind == kind {
			out = append(out, e)
		}
	}
	return out
}

// talk returns the kinds of the entries that make up the conversation and
// its turns, leaving out notices and queue marks.
func (l *memLog) talk() []EntryKind {
	var out []EntryKind
	for _, e := range l.all() {
		switch e.Kind {
		case EntryNotice, EntryQueued, EntryUnqueued:
		default:
			out = append(out, e.Kind)
		}
	}
	return out
}

func (l *memLog) kinds() []EntryKind {
	var out []EntryKind
	for _, e := range l.all() {
		out = append(out, e.Kind)
	}
	return out
}

// live returns the entries the model still has: those after the last
// summary that were not slid out. The caller holds the lock.
func (l *memLog) live() []Entry {
	start := 0
	for i, e := range l.entries {
		if e.Kind == EntrySummary {
			start = i
		}
	}
	var out []Entry
	for _, e := range l.entries[start:] {
		if !l.cut[e.ID] {
			out = append(out, e)
		}
	}
	return out
}

func (l *memLog) View(context.Context) (View, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	var msgs []core.Message
	user := func(blocks ...core.Block) {
		if n := len(msgs); n > 0 && msgs[n-1].Role == core.RoleUser {
			msgs[n-1].Blocks = append(msgs[n-1].Blocks, blocks...)
			return
		}
		msgs = append(msgs, core.Message{Role: core.RoleUser, Blocks: slices.Clone(blocks)})
	}
	for _, e := range l.live() {
		switch e.Kind {
		case EntryPrompt, EntryNote, EntrySummary:
			user(e.Blocks...)
		case EntryResult:
			user(core.ToolResultBlock(e.ToolUseID, e.Result))
		case EntryAssistant:
			if !e.Silent {
				msgs = append(msgs, e.Message)
			}
		}
	}
	return View{Messages: msgs}, nil
}

func (l *memLog) Recent(context.Context) iter.Seq2[Entry, error] {
	entries := l.all()
	return func(yield func(Entry, error) bool) {
		for i := len(entries) - 1; i >= 0; i-- {
			if !yield(entries[i], nil) {
				return
			}
		}
	}
}

// slideLog is a memLog whose owner can make room: it takes the oldest
// rounds after the first prompt out of the view, as a session in frames
// does.
type slideLog struct {
	*memLog
	asked []RoomRequest
	off   bool // refuse to make room
}

func (l *slideLog) MakeRoom(_ context.Context, req RoomRequest) (Room, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.asked = append(l.asked, req)
	if l.off {
		return Room{}, nil
	}
	live := l.live()
	size := func(e Entry) int {
		n := 0
		for _, b := range e.Blocks {
			n += req.Estimate(b.Text)
		}
		for _, b := range e.Message.Blocks {
			n += req.Estimate(b.Text) + req.Estimate(string(b.Input))
		}
		n += req.Estimate(core.TextOf(e.Result.Content))
		return n
	}
	// The first prompt anchors the cut. The cut ends before an assistant
	// entry that asks for tools, and never reaches the protected prompt.
	first := -1
	for i, e := range live {
		if e.Kind == EntryPrompt {
			first = i
			break
		}
	}
	if first < 0 {
		return Room{}, nil
	}
	end, freed := -1, 0
	sum := 0
	for i := first + 1; i < len(live); i++ {
		e := live[i]
		if e.ID != "" && e.ID == req.Protect {
			break
		}
		if e.Kind == EntryAssistant && len(e.Message.ToolUses()) > 0 && sum > 0 {
			end, freed = i, sum
			if sum >= req.Need {
				break
			}
		}
		sum += size(e)
	}
	if end < 0 {
		return Room{}, nil
	}
	for _, e := range live[first+1 : end] {
		l.cut[e.ID] = true
	}
	room := Room{Freed: freed, Notice: fmt.Sprintf("slid: about %d tokens out of the model's context", freed)}
	if req.Moment != RoomBeforePrompt {
		room.Reminder = "<slid>The start of the conversation left your context to make room.</slid>"
	}
	return room, nil
}

// recorder collects a session's events.
type recorder struct {
	mu    sync.Mutex
	evs   []Event
	delay time.Duration // makes the consumer slow
}

func (r *recorder) on(ev Event) {
	if r.delay > 0 {
		time.Sleep(r.delay)
	}
	r.mu.Lock()
	r.evs = append(r.evs, ev)
	r.mu.Unlock()
}

func (r *recorder) all() []Event {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.evs)
}

// eventsOf returns the recorded events of one type.
func eventsOf[T Event](r *recorder) []T {
	var out []T
	for _, ev := range r.all() {
		if e, ok := ev.(T); ok {
			out = append(out, e)
		}
	}
	return out
}

// waitEvent waits until an event of type T that pred accepts has arrived.
func waitEvent[T Event](t *testing.T, r *recorder, pred func(T) bool) T {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		for _, e := range eventsOf[T](r) {
			if pred == nil || pred(e) {
				return e
			}
		}
		if time.Now().After(deadline) {
			var zero T
			t.Fatalf("no %T arrived in time; events so far: %s", zero, typesOf(r.all()))
			return zero
		}
		time.Sleep(time.Millisecond)
	}
}

// typesOf names the types of events, for failure messages.
func typesOf(evs []Event) string {
	var names []string
	for _, ev := range evs {
		names = append(names, strings.TrimPrefix(fmt.Sprintf("%T", ev), "*agent."))
	}
	return strings.Join(names, " ")
}

// notices returns the text of every notice at or above a level.
func notices(r *recorder, level Level) []string {
	var out []string
	for _, n := range eventsOf[*NoticeEvent](r) {
		if level == "" || n.Level == level {
			out = append(out, n.Text)
		}
	}
	return out
}

// rig is a session wired to fakes.
type rig struct {
	t     *testing.T
	a     *Agent
	log   *memLog
	model *fakeModel
	rec   *recorder
}

// testRetry is a retry policy with waits short enough for a test.
var testRetry = RetryPolicy{
	Pauses:          []time.Duration{time.Millisecond, 2 * time.Millisecond, 3 * time.Millisecond},
	GoneAfter:       60 * time.Millisecond,
	NotRunningPause: 2 * time.Millisecond,
	NotRunningAfter: 20 * time.Millisecond,
	BusyMax:         5 * time.Millisecond,
}

var idCounter atomic.Int64

func testID() string { return fmt.Sprintf("id%d", idCounter.Add(1)) }

// newRig makes a session whose model, log and consumer are fakes. mod may
// change the options before the session starts. The session is closed,
// and the goroutines it started are checked to be gone, when the test
// ends.
func newRig(t *testing.T, mod func(o *Options)) *rig {
	t.Helper()
	before := runtime.NumGoroutine()
	r := &rig{t: t, log: newMemLog(), model: &fakeModel{name: "fake"}, rec: &recorder{}}
	opts := Options{
		SessionID:   "session-1",
		Cwd:         "/work",
		Interactive: true,
		Model:       Model{Provider: r.model, Info: core.ModelInfo{ID: "big", ContextWindow: 100000}, Address: "http://203.0.113.7:8080"},
		Log:         r.log,
		Gate:        AllowAll,
		OnEvent:     r.rec.on,
		NewID:       testID,
		Retry:       testRetry,
		StopGrace:   3 * time.Second,
	}
	if mod != nil {
		mod(&opts)
	}
	if l, ok := opts.Log.(*memLog); ok {
		r.log = l
	}
	if l, ok := opts.Log.(*slideLog); ok {
		r.log = l.memLog
	}
	a, err := New(opts)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	r.a = a
	t.Cleanup(func() {
		a.Close()
		waitGoroutines(t, before)
	})
	return r
}

// waitGoroutines fails the test if more goroutines are left than there
// were before it.
func waitGoroutines(t *testing.T, before int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for runtime.NumGoroutine() > before {
		if time.Now().After(deadline) {
			buf := make([]byte, 1<<20)
			buf = buf[:runtime.Stack(buf, true)]
			t.Errorf("%d goroutines are left, %d before the test:\n%s", runtime.NumGoroutine(), before, buf)
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// bound is how long a test waits for a turn before it calls it hung.
const bound = 20 * time.Second

// run sends a prompt and waits for the turn that answers it.
func (r *rig) run(text string) TurnResult {
	r.t.Helper()
	return r.runPrompt(Prompt{Text: text})
}

func (r *rig) runPrompt(p Prompt) TurnResult {
	r.t.Helper()
	type out struct {
		res TurnResult
		err error
	}
	ch := make(chan out, 1)
	go func() {
		res, err := r.a.Run(context.Background(), p)
		ch <- out{res, err}
	}()
	select {
	case o := <-ch:
		if o.err != nil {
			r.t.Fatalf("Run: %v", o.err)
		}
		r.drain()
		return o.res
	case <-time.After(bound):
		r.t.Fatalf("the turn did not end within %s; the session is %+v; events: %s", bound, r.a.State(), typesOf(r.rec.all()))
		return TurnResult{}
	}
}

// idle waits until the session has nothing left to do.
func (r *rig) idle() {
	r.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), bound)
	defer cancel()
	if err := r.a.Wait(ctx); err != nil {
		r.t.Fatalf("the session did not go idle within %s: %+v; events: %s", bound, r.a.State(), typesOf(r.rec.all()))
	}
	r.drain()
}

// drained reports whether every event put in so far has been handed to
// the consumer.
func (s *sink) drained() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.q) == 0 && !s.busy
}

// drain waits until the consumer has been handed every event made so far.
func (r *rig) drain() {
	r.t.Helper()
	deadline := time.Now().Add(bound)
	for !r.a.sink.drained() {
		if time.Now().After(deadline) {
			r.t.Fatalf("the events were not all delivered within %s", bound)
		}
		time.Sleep(200 * time.Microsecond)
	}
}

// checkLog fails the test unless the log is a conversation a model can be
// sent and every turn in it has an end.
func (r *rig) checkLog() {
	r.t.Helper()
	checkLogValid(r.t, r.log)
}

func checkLogValid(t *testing.T, l *memLog) {
	t.Helper()
	v, _ := l.View(context.Background())
	if len(v.Messages) > 0 {
		if err := checkView(v.Messages); err != nil {
			t.Errorf("the log is not a valid conversation: %v\n%s", err, dumpLog(l))
		}
	}
	// Every tool call has exactly one result, and nothing is open after
	// the last turn's end.
	results := map[string]int{}
	var calls []string
	open := false
	for _, e := range l.all() {
		switch e.Kind {
		case EntryAssistant:
			open = true
			if !e.Silent {
				for _, u := range e.Message.ToolUses() {
					calls = append(calls, u.ID)
				}
			}
		case EntryResult:
			open = true
			results[e.ToolUseID]++
		case EntryPrompt, EntryNote, EntrySummary:
			// What is stored between turns names no turn.
			open = open || e.Turn != ""
		case EntryTurn:
			open = false
		}
	}
	for _, id := range calls {
		if results[id] != 1 {
			t.Errorf("tool call %s has %d results, want 1\n%s", id, results[id], dumpLog(l))
		}
	}
	if open {
		t.Errorf("the log ends in the middle of a turn\n%s", dumpLog(l))
	}
	for _, e := range l.all() {
		for _, s := range entryTexts(e) {
			if strings.Contains(strings.ToLower(s), "context canceled") {
				t.Errorf("the log says %q; a stopped turn must say so in plain words", s)
			}
		}
	}
}

// entryTexts returns every text an entry holds.
func entryTexts(e Entry) []string {
	out := []string{e.Text, core.TextOf(e.Blocks), e.Message.Text(), core.TextOf(e.Result.Content)}
	return out
}

// dumpLog writes a log out for a failure message.
func dumpLog(l *memLog) string {
	var b strings.Builder
	for i, e := range l.all() {
		fmt.Fprintf(&b, "%3d %-9s", i, e.Kind)
		switch e.Kind {
		case EntryPrompt, EntryNote, EntrySummary, EntryQueued:
			fmt.Fprintf(&b, " hidden=%v src=%q %q", e.Hidden, e.Source, clip(core.TextOf(e.Blocks), 80))
		case EntryAssistant:
			fmt.Fprintf(&b, " stop=%s silent=%v %q", e.StopReason, e.Silent, clip(e.Message.Text(), 60))
			for _, u := range e.Message.ToolUses() {
				fmt.Fprintf(&b, " call(%s %s)", u.ID, u.Name)
			}
		case EntryResult:
			fmt.Fprintf(&b, " for=%s err=%v %q", e.ToolUseID, e.Result.IsError, clip(core.TextOf(e.Result.Content), 60))
		case EntryTurn:
			fmt.Fprintf(&b, " %s %q", e.Reason, e.Text)
		case EntryNotice:
			fmt.Fprintf(&b, " %s %q", e.Level, clip(e.Text, 80))
		case EntryUnqueued:
			fmt.Fprintf(&b, " prompt=%s", e.Prompt)
		}
		b.WriteString("\n")
	}
	return b.String()
}

// lastUserText returns the text of the last user message of a request.
func lastUserText(req *core.Request) string {
	for i := len(req.Messages) - 1; i >= 0; i-- {
		if req.Messages[i].Role == core.RoleUser {
			var b strings.Builder
			for _, blk := range req.Messages[i].Blocks {
				switch blk.Type {
				case core.BlockText:
					b.WriteString(blk.Text)
				case core.BlockToolResult:
					b.WriteString(core.TextOf(blk.Content))
				}
				b.WriteString("\n")
			}
			return b.String()
		}
	}
	return ""
}

// toolResults returns, by call id, the text of the tool results in a
// request's last user message, and their order.
func toolResults(req *core.Request) (map[string]string, []string) {
	out := map[string]string{}
	var order []string
	if len(req.Messages) == 0 {
		return out, nil
	}
	last := req.Messages[len(req.Messages)-1]
	for _, b := range last.Blocks {
		if b.Type == core.BlockToolResult {
			out[b.ToolUseID] = core.TextOf(b.Content)
			order = append(order, b.ToolUseID)
		}
	}
	return out, order
}
