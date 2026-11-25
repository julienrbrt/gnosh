package bridge

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"

	"github.com/gnosh-ai/gnosh/internal/core"
	"github.com/gnosh-ai/gnosh/internal/prompt"
	"github.com/gnosh-ai/gnosh/internal/transcript"
)

// Options is what a Server is made from.
type Options struct {
	// Project is the project's directory, an absolute path: the working
	// directory and the one root every call is given.
	Project string
	// Build and Version are what hello reports: the build id the GnoVM
	// worker's handshake compares, and a line naming the build.
	Build   string
	Version string
	// Tools makes the tools the bridge serves. It is called once, when the
	// Server is made, with the way to read a session's stack, for the
	// tools' MemStack.
	Tools func(stack func(session string) []string) []core.Tool
	// Frames reads the frames a call pushed and popped from its result.
	// Nil means calls report none.
	Frames func(res core.ToolResult) []Frame
	// Confirms names the tools some of whose calls need a person's
	// approval, for ToolInfo.Confirm. The calls themselves are judged by
	// core.Describer.
	Confirms map[string]bool
	// Texts are gnosh's words to the model that prompt, room and step
	// hand out. Nil means those three answer with nothing to say.
	Texts *prompt.Set
	// Digest is what a new session is told of the project's mem (the
	// <mem> block), or "" when mem holds nothing to tell.
	Digest func() string
	// UsesMem reports whether a reply of the model calls mem, for the mem
	// reminder's count.
	UsesMem func(core.Message) bool
	// RecordsMem reports whether a tool call kept something in mem (a
	// thought, a fact, a result, a todo), read from the call and its
	// result: a call mem refused or that failed kept nothing. It is for
	// room and step with Covered. Nil means no call does.
	RecordsMem func(call, result core.Block) bool
	// Log, when not nil, is told what the bridge has to say for a person:
	// one line each, for standard error.
	Log func(line string)
	// Turns, when not nil, is gnosh's session work beside the tools: the
	// close checks of a call to mem and the turn-end checks (§11). Nil
	// means calls are run unchecked and end lets every turn end.
	Turns Turns
	// Side, when not nil, answers side requests in place of the host. It
	// is for tests; Serve otherwise asks the host when hello said it
	// answers them.
	Side func(ctx context.Context, sr SideRequest) (SideAnswer, error)
}

// SideAnswer is the host's answer to a side request.
type SideAnswer struct {
	Text string
	Stop string
}

// Server answers one host's requests. It is safe to use from one
// goroutine at a time, which is how Serve uses it.
type Server struct {
	opts   Options
	tools  []core.Tool
	byName map[string]core.Tool

	mu       sync.Mutex
	sessions map[string]*session
	hello    bool
	// side says the host answers side requests, and asker is how Serve
	// writes one and reads its answer.
	side  bool
	asker *asker
}

// session is what the bridge keeps of one host session: its stack (the
// frame ids it stands in, bottom to top) and the store its calls share.
type session struct {
	stack []string
	store *sync.Map
	// used is the host's token count of the session's last request, as
	// step was told it, and pushedAt what it was when each frame on the
	// stack was entered, by the call that entered it: the large-step
	// line's measure (frames.md §8.7).
	used     int
	pushedAt map[string]int
	// large holds the push calls of the frames the large-step line was
	// said of, and remindedAt the number of entries when the mem reminder
	// was last given.
	large      map[string]bool
	remindedAt int
	// slidAt holds, for each frame a covered room slid, the uuid of the
	// last entry the host had sent then: work after it is what the next
	// slide of the frame would take. soon holds the frames the slide-soon
	// line was said of since their last slide.
	slidAt map[string]string
	soon   map[string]bool
	// slides counts, per frame, how many times a covered room has slid it
	// without the frame being popped: a frame that keeps sliding is stuck,
	// and after maxFrameSlides the bridge falls back to the host's own
	// compaction so a run can never slide worse than it would compact
	// (§10.4). popped says a frame the session stood in has been popped
	// since the last push, and verifyTold that the back-at-root verify note
	// has been given since.
	slides     map[string]int
	popped     bool
	verifyTold bool
	// toldStuck holds the frames the stuck-frame "split or pop" note was
	// said of, said once before the frame hits its slide limit.
	toldStuck map[string]bool
}

// maxFrameSlides is how many times one frame may slide before the bridge
// stops sliding it and leaves room-making to the host's compaction. A frame
// that has slid this many times without being popped is not making progress
// a slide can help (one round-2 run slid a single frame 44 times).
const maxFrameSlides = 6

// New makes a Server.
func New(o Options) *Server {
	s := &Server{opts: o, sessions: map[string]*session{}, byName: map[string]core.Tool{}}
	if o.Tools != nil {
		s.tools = o.Tools(s.Stack)
	}
	for _, t := range s.tools {
		s.byName[t.Spec().Name] = t
	}
	return s
}

// Stack is the stack session's mem calls are made from (bridge.md §6.3).
func (s *Server) Stack(id string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if ss := s.sessions[id]; ss != nil {
		return append([]string(nil), ss.stack...)
	}
	return nil
}

func (s *Server) session(id string) *session {
	s.mu.Lock()
	defer s.mu.Unlock()
	ss := s.sessions[id]
	if ss == nil {
		ss = &session{store: &sync.Map{}}
		s.sessions[id] = ss
	}
	return ss
}

// errEnd is what Handle returns, beside its reply, when the bridge must
// end after writing it.
var errEnd = errors.New("the host broke the protocol, so the bridge ends")

// Serve reads requests from r and writes their replies to w, one line
// each, until r ends, ctx is cancelled between requests, or a request
// ends the protocol. It returns nil when r ended. While a request is
// answered the bridge may write side requests of its own and read their
// answers (§11.1); requests the host wrote meanwhile wait their turn.
func (s *Server) Serve(ctx context.Context, r io.Reader, w io.Writer) error {
	out := json.NewEncoder(w)
	out.SetEscapeHTML(false)
	a := newAsker(r, out)
	s.asker = a
	for {
		line, err := a.next()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			if errors.Is(err, bufio.ErrTooLong) {
				_ = a.write(fail(nil, CodeBadRequest, fmt.Sprintf("a request line is longer than %d bytes", MaxLine)))
			}
			return fmt.Errorf("reading the host's requests: %w", err)
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if len(line) == 0 {
			continue
		}
		rep, err := s.Handle(ctx, line)
		if werr := a.write(rep); werr != nil {
			return fmt.Errorf("writing a reply to the host: %w", werr)
		}
		if err != nil {
			return err
		}
	}
}

// Handle answers one request line. A non-nil error means the bridge must
// end once the reply is written.
func (s *Server) Handle(ctx context.Context, line []byte) (Reply, error) {
	var req Request
	if err := json.Unmarshal(line, &req); err != nil {
		var id struct {
			ID json.RawMessage `json:"id"`
		}
		_ = json.Unmarshal(line, &id)
		if !s.hello {
			return fail(id.ID, CodeProtocol, "the first request must be hello, and this line is not a request: "+err.Error()), errEnd
		}
		return fail(id.ID, CodeBadRequest, "the line is not a request: "+err.Error()), nil
	}
	if !s.hello && req.Op != OpHello {
		return fail(req.ID, CodeProtocol, fmt.Sprintf("the first request must be hello, not %q", req.Op)), errEnd
	}
	switch req.Op {
	case OpHello:
		return s.helloReply(req)
	case OpTools:
		return s.toolsReply(req), nil
	case OpCall:
		return s.call(ctx, req), nil
	case OpView:
		return s.view(req), nil
	case OpPrompt:
		return s.prompt(ctx, req), nil
	case OpRoom:
		return s.room(req), nil
	case OpStep:
		return s.step(req), nil
	case OpEnd:
		return s.end(ctx, req), nil
	case OpSideAnswer:
		return fail(req.ID, CodeBadRequest, fmt.Sprintf("side_answer %d answers no side request the bridge is waiting on", req.SideID)), nil
	case "":
		return fail(req.ID, CodeBadRequest, "the request has no op"), nil
	}
	return fail(req.ID, CodeUnknownOp, fmt.Sprintf("this bridge does not answer %q; it answers %v", req.Op, ops)), nil
}

func (s *Server) helloReply(req Request) (Reply, error) {
	if req.Protocol != Protocol {
		rep := fail(req.ID, CodeProtocol, fmt.Sprintf("this bridge speaks protocol %d, and the host asked for %d", Protocol, req.Protocol))
		rep.Error.Protocols = []int{Protocol}
		return rep, errEnd
	}
	s.hello = true
	s.side = req.Side
	if s.opts.Log != nil && req.Client != "" {
		s.opts.Log("gnosh bridge: serving " + req.Client)
	}
	return Reply{OK: true, ID: req.ID, Protocol: Protocol, Build: s.opts.Build, Version: s.opts.Version,
		Project: s.opts.Project, Ops: append([]string(nil), ops...)}, nil
}

func (s *Server) toolsReply(req Request) Reply {
	rep := Reply{OK: true, ID: req.ID, Tools: []ToolInfo{}}
	for _, t := range s.tools {
		spec := t.Spec()
		rep.Tools = append(rep.Tools, ToolInfo{Name: spec.Name, Description: spec.Description,
			InputSchema: spec.InputSchema, Confirm: s.opts.Confirms[spec.Name]})
	}
	return rep
}

func (s *Server) call(ctx context.Context, req Request) Reply {
	switch {
	case req.Session == "":
		return fail(req.ID, CodeBadRequest, "call needs session")
	case req.CallID == "":
		return fail(req.ID, CodeBadRequest, "call needs call_id")
	case req.Name == "":
		return fail(req.ID, CodeBadRequest, "call needs name")
	}
	t := s.byName[req.Name]
	if t == nil {
		return fail(req.ID, CodeUnknownTool, fmt.Sprintf("there is no tool %q; tools lists the ones there are", req.Name))
	}
	input := req.Input
	if len(input) == 0 || string(input) == "null" {
		input = json.RawMessage("{}")
	}
	ss := s.session(req.Session)
	call := core.ToolCall{ID: req.CallID, Name: req.Name, Input: input}
	env := &core.ToolEnv{Cwd: s.opts.Project, Roots: []string{s.opts.Project}, SessionID: req.Session, Store: ss.store}
	if d, ok := t.(core.Describer); ok && !req.Confirmed {
		if a, err := d.Describe(call, env); err == nil && a.Confirm {
			rep := fail(req.ID, CodeConfirm, "a person must approve this call first; send it again with confirmed: true once they have")
			rep.Error.Summary = a.Summary
			return rep
		}
	}
	ctx = s.withSide(ctx)
	if tu := s.opts.Turns; tu != nil {
		if len(req.Entries) > 0 {
			if msg := uniqueUUIDs(req.Entries); msg != "" {
				return fail(req.ID, CodeBadRequest, msg)
			}
			tu.Sync(req.Session, req.Entries)
		}
		tu.Calling(req.Session, call)
	}
	res, err := t.Run(ctx, call, env)
	if err != nil {
		return fail(req.ID, CodeInternal, fmt.Sprintf("running %s: %v", req.Name, err))
	}
	rep := Reply{OK: true, ID: req.ID, Content: res.Content, IsError: res.IsError}
	if tu := s.opts.Turns; tu != nil {
		tu.Called(ctx, req.Session, call, res)
		rep.Notices = tu.Notices(req.Session)
	}
	if s.opts.Frames != nil {
		for _, f := range s.opts.Frames(res) {
			if f.Session != req.Session {
				continue
			}
			rep.Frames = append(rep.Frames, f)
			s.apply(ss, f)
		}
	}
	if rep.Content == nil {
		rep.Content = []core.Block{}
	}
	return rep
}

// apply moves a session's stack by one frame a call kept.
func (s *Server) apply(ss *session, f Frame) {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch f.Kind {
	case "push":
		ss.stack = append(ss.stack, f.ID)
		if ss.pushedAt == nil {
			ss.pushedAt = map[string]int{}
		}
		ss.pushedAt[f.ID] = ss.used
		ss.verifyTold = false
	case "pop":
		ss.popped = true
		delete(ss.slides, f.ID)
		for i := len(ss.stack) - 1; i >= 0; i-- {
			if ss.stack[i] == f.ID {
				ss.stack = ss.stack[:i]
				return
			}
		}
	}
}

func (s *Server) view(req Request) Reply {
	if req.Session == "" {
		return fail(req.ID, CodeBadRequest, "view needs session")
	}
	seen := make(map[string]bool, len(req.Entries))
	for i, e := range req.Entries {
		if e.UUID == "" {
			return fail(req.ID, CodeBadRequest, fmt.Sprintf("entry %d has no uuid", i))
		}
		if seen[e.UUID] {
			return fail(req.ID, CodeBadRequest, fmt.Sprintf("entry %d has the uuid %q of an entry before it", i, e.UUID))
		}
		seen[e.UUID] = true
	}
	msgs, info := transcript.View(req.Entries, transcript.ViewOptions{
		Budget: req.Budget, Room: req.Room, KeepRounds: req.KeepRounds, Prompt: req.Prompt,
		Session: req.Session, Trace: true,
	})
	// The stack the session's next mem calls are made from is the view's,
	// slides included (bridge.md §6.3).
	ss := s.session(req.Session)
	stack := make([]string, len(info.Stack))
	rep := Reply{OK: true, ID: req.ID, Messages: msgs, Sources: info.Sources, Tokens: info.Tokens,
		OverBudget: info.OverBudget, NewSlides: named(info.NewSlides, req.Session), Stack: []StackFrame{}}
	for i, f := range info.Stack {
		stack[i] = f.ID
		rep.Stack = append(rep.Stack, StackFrame{ID: f.ID, Label: f.Label, CallID: f.ToolUseID, Slid: f.Slid})
	}
	s.mu.Lock()
	ss.stack = stack
	s.mu.Unlock()
	if rep.Messages == nil {
		rep.Messages = []core.Message{}
	}
	for i := range rep.Sources {
		if rep.Sources[i] == nil {
			rep.Sources[i] = []string{}
		}
	}
	return rep
}

func fail(id json.RawMessage, code, msg string) Reply {
	return Reply{OK: false, ID: id, Error: &Error{Code: code, Message: msg}}
}
