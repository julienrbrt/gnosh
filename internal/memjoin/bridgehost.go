package memjoin

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/gnosh-ai/gnosh/internal/agent"
	"github.com/gnosh-ai/gnosh/internal/app"
	"github.com/gnosh-ai/gnosh/internal/bridge"
	"github.com/gnosh-ai/gnosh/internal/config"
	"github.com/gnosh-ai/gnosh/internal/core"
	"github.com/gnosh-ai/gnosh/internal/gnovm"
	gnovmtools "github.com/gnosh-ai/gnosh/internal/gnovm/tools"
	"github.com/gnosh-ai/gnosh/internal/prompt"
	"github.com/gnosh-ai/gnosh/internal/transcript"
)

// This file is a session's join for `gnosh bridge` (bridge.md §11): each
// session of the host gets a join of its own, as a gnosh session does, so
// that a call to mem goes through the same checks (the fresh guard, the
// namer, the close check with its review, the pop receipt) and the turn
// ends through the same drivers (the refused-close send-back and the goal
// judge). What differs is where the join reads the conversation from (the
// entries the host sends, hostLog) and who answers its side requests (the
// host's model, bridge.Ask).

// bridgeHost holds the joins of the host's sessions. It is bridge.Turns,
// and fills the tools' slots.
type bridgeHost struct {
	p       *process
	vms     *gnovm.Lazy
	dir     string
	project string
	dirs    app.Dirs
	config  *config.Config
	texts   *prompt.Set

	mu    sync.Mutex
	joins map[string]*bridgeJoin
}

// bridgeJoin is one host session's join, its log and its drivers.
type bridgeJoin struct {
	j       *join
	log     *hostLog
	drivers agent.HostDrivers

	mu      sync.Mutex
	notices []string
}

func newBridgeHost(p *process, vms *gnovm.Lazy, dir, project string, dirs app.Dirs, cfg *config.Config) *bridgeHost {
	return &bridgeHost{p: p, vms: vms, dir: dir, project: project, dirs: dirs, config: cfg, texts: prompt.Builtin(), joins: map[string]*bridgeJoin{}}
}

// of returns the join of session, made the first time it is asked for.
func (h *bridgeHost) of(session string) *bridgeJoin {
	h.mu.Lock()
	defer h.mu.Unlock()
	if bj := h.joins[session]; bj != nil {
		return bj
	}
	bj := &bridgeJoin{log: &hostLog{id: session}}
	dirs := h.dirs
	if dirs.Cwd == "" {
		dirs.Cwd = h.project
	}
	s := &app.Session{
		ID: session, Config: h.config, Dirs: dirs, Texts: h.texts,
		Notice: func(_ agent.Level, _ string, text string) {
			bj.mu.Lock()
			bj.notices = append(bj.notices, text)
			bj.mu.Unlock()
		},
	}
	j := &join{
		p: h.p, s: s, vms: h.vms, dir: h.dir, project: h.project, id: session,
		ts: bj.log, texts: h.texts, main: true, run: true,
		kept: map[string]*keptCall{}, inflight: map[string]bool{},
		side: hostSide,
	}
	bj.j = j
	// The order is a session's: a refused close sends the model back
	// first, then the goal driver judges what was closed as done.
	bj.drivers.Drivers = []agent.Driver{j.closeDriver(), j.goalDriver()}
	h.joins[session] = bj
	return bj
}

// hostSide asks the host's model a side request, as agent.Side asks the
// small model: the answer's thinking is taken off, and an empty answer is
// none.
func hostSide(ctx context.Context, sr agent.SideRequest) (agent.SideResult, error) {
	timeout := sr.Timeout
	if timeout <= 0 {
		timeout = 2 * time.Minute
	}
	ans, err := bridge.Ask(ctx, bridge.SideRequest{
		Purpose: sr.Purpose, System: sr.System, Prompt: sr.Prompt,
		MaxTokens: sr.MaxTokens, Temperature: sr.Temperature, TimeoutMS: timeout.Milliseconds(),
	})
	if err != nil {
		return agent.SideResult{}, err
	}
	text := agent.AnswerText(ans.Text)
	if text == "" {
		return agent.SideResult{}, fmt.Errorf("the host's model answered the %s request with nothing", sr.Purpose)
	}
	return agent.SideResult{Text: text}, nil
}

// options fills the tools' slots with the joins of the host's sessions.
func (h *bridgeHost) options(o gnovmtools.Options) gnovmtools.Options {
	o.MemFresh = func(ctx context.Context, session, callID string) string {
		return h.of(session).j.memFresh(ctx, session, callID)
	}
	o.Namer = func(ctx context.Context, session string, texts []string) map[string]string {
		return h.of(session).j.namer(ctx, session, texts)
	}
	o.CheckCloses = func(ctx context.Context, session, callID string, closes []gnovm.MemClose) string {
		return h.of(session).j.checkCloses(ctx, session, callID, closes)
	}
	o.ClosesKept = func(ctx context.Context, session, callID string, kept bool) {
		h.of(session).j.closesKept(ctx, session, callID, kept)
	}
	o.OnMemFrames = func(ctx context.Context, session, callID string, frames []gnovm.MemFrame) string {
		return h.of(session).j.onMemFrames(ctx, session, callID, frames)
	}
	o.OnMemGoals = func(ctx context.Context, session string, goals []gnovm.MemGoal) {
		h.of(session).j.onMemGoals(ctx, session, goals)
	}
	o.OnMemRules = func(ctx context.Context, session string, rules []gnovm.MemRule) {
		h.of(session).j.onMemRules(ctx, session, rules)
	}
	return o
}

// Prompt is bridge.Turns.Prompt: a prompt the person typed starts a turn.
func (h *bridgeHost) Prompt(_ context.Context, session, text string) {
	bj := h.of(session)
	turn := newTurnID()
	bj.drivers.Begin(turn)
	var blocks []core.Block
	if text != "" {
		blocks = []core.Block{core.Text(text)}
	}
	bj.j.count(agent.Entry{Kind: agent.EntryPrompt, Turn: turn, Time: time.Now(), Blocks: blocks})
}

// Sync is bridge.Turns.Sync.
func (h *bridgeHost) Sync(session string, entries []transcript.Entry) {
	h.of(session).log.set(entries)
}

// Calling is bridge.Turns.Calling: a gnovm_run call is out, so that the
// frames mem keeps for it go with its result.
func (h *bridgeHost) Calling(session string, call core.ToolCall) {
	if call.Name != gnovmtools.RunToolName {
		return
	}
	j := h.of(session).j
	j.mu.Lock()
	j.inflight[call.ID] = true
	j.mu.Unlock()
}

// Called is bridge.Turns.Called: a call's result is in, and counts as a
// session's stored result does.
func (h *bridgeHost) Called(ctx context.Context, session string, call core.ToolCall, res core.ToolResult) {
	if call.Name != gnovmtools.RunToolName {
		return
	}
	j := h.of(session).j
	e := agent.Entry{Kind: agent.EntryResult, ToolUseID: call.ID, ToolName: call.Name, Input: call.Input, Result: res}
	j.count(e)
	j.afterStored(ctx, e, nil)
}

// End is bridge.Turns.End: the drivers are asked, as a session's loop asks
// them; a turn they let end is over.
func (h *bridgeHost) End(ctx context.Context, session string, reply core.Message, finished bool) bridge.EndVerdict {
	bj := h.of(session)
	v := bj.drivers.End(ctx, agent.Reply{Message: reply, Text: reply.Text()}, finished)
	if !v.Continue {
		bj.j.turnEnded(ctx, agent.TurnResult{})
	}
	return bridge.EndVerdict{Continue: v.Continue, Send: v.Prompt, Driver: v.Driver, Objection: v.Objection, Stopped: v.Stopped, Notices: v.Notices}
}

// Notices is bridge.Turns.Notices.
func (h *bridgeHost) Notices(session string) []string {
	bj := h.of(session)
	bj.mu.Lock()
	defer bj.mu.Unlock()
	out := bj.notices
	bj.notices = nil
	return out
}

func newTurnID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return "turn-" + hex.EncodeToString(b[:])
}

// hostLog is a host session's conversation as the join reads it: the
// entries the host sent last, with what the join wrote itself since.
type hostLog struct {
	id string

	mu      sync.Mutex
	entries []transcript.Entry
}

var errNoRedact = errors.New("a bridge session's conversation is the host's, and is not redacted here")

func (l *hostLog) set(entries []transcript.Entry) {
	l.mu.Lock()
	l.entries = slices.Clone(entries)
	l.mu.Unlock()
}

func (l *hostLog) ID() string { return l.id }

func (l *hostLog) Entries() []transcript.Entry {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.entries[:len(l.entries):len(l.entries)]
}

func (l *hostLog) View(opts transcript.ViewOptions) ([]core.Message, transcript.ViewInfo) {
	if opts.Session == "" {
		opts.Session = l.id
	}
	return transcript.View(l.Entries(), opts)
}

func (l *hostLog) Stack() []transcript.Frame { return transcript.Stack(l.Entries(), l.id) }

func (l *hostLog) Preview(next []transcript.Entry, opts transcript.ViewOptions) ([]transcript.Cut, []transcript.Frame) {
	opts.Session = l.id
	return transcript.Preview(l.Entries(), next, opts)
}

func (l *hostLog) Append(e transcript.Entry) (transcript.Entry, error) {
	out, err := l.AppendAll(e)
	if err != nil {
		return transcript.Entry{}, err
	}
	return out[0], nil
}

// AppendAll keeps what the join writes (a goal's binding, a task list,
// frames that came late) until the host sends the conversation again.
func (l *hostLog) AppendAll(entries ...transcript.Entry) ([]transcript.Entry, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]transcript.Entry, len(entries))
	for i, e := range entries {
		if e.SessionID == "" {
			e.SessionID = l.id
		}
		if e.Timestamp.IsZero() {
			e.Timestamp = time.Now()
		}
		out[i] = e
	}
	l.entries = append(l.entries, out...)
	return out, nil
}

func (l *hostLog) Redact(func(e *transcript.Entry) bool) (int, error) { return 0, errNoRedact }
