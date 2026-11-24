package bridge

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/gnosh-ai/gnosh/internal/core"
)

// This file is the bridge's side requests and the end of a turn (bridge.md
// §11): gnosh's checks ask a model, and the bridge has none, so it asks
// its host's, on the same pipe, while it answers the request that needed
// it; and as a host's turn would end, gnosh's turn-end checks are asked.

// ErrNoSide is what Ask returns when nothing answers side requests: the
// host did not say in hello that it answers them.
var ErrNoSide = errors.New("the host answers no side requests, so there is no model to ask")

// sideSlack is how much longer than a side request's own time the bridge
// waits for the host's answer, for the host's own timeout to come first.
const sideSlack = 30 * time.Second

type sideKey struct{}

// Ask asks the model of the host whose request ctx belongs to. The text
// of an answer cut off at its length is no answer, as for a session's own
// side requests.
func Ask(ctx context.Context, sr SideRequest) (SideAnswer, error) {
	f, _ := ctx.Value(sideKey{}).(func(context.Context, SideRequest) (SideAnswer, error))
	if f == nil {
		return SideAnswer{}, ErrNoSide
	}
	ans, err := f(ctx, sr)
	if err != nil {
		return SideAnswer{}, err
	}
	if ans.Stop == string(core.StopMaxTokens) {
		return SideAnswer{}, fmt.Errorf("the host's model was cut off at %d tokens answering the %s request", sr.MaxTokens, sr.Purpose)
	}
	return ans, nil
}

// withSide gives ctx the way to ask the host's model, when there is one.
func (s *Server) withSide(ctx context.Context) context.Context {
	switch {
	case s.opts.Side != nil:
		return context.WithValue(ctx, sideKey{}, s.opts.Side)
	case s.side && s.asker != nil:
		return context.WithValue(ctx, sideKey{}, s.asker.ask)
	}
	return ctx
}

// asker reads the host's lines and writes the bridge's, so that a side
// request can be written, and its answer read, in the middle of a request.
type asker struct {
	lines chan []byte
	err   error // why lines was closed; set before it is
	later [][]byte
	out   *json.Encoder

	mu  sync.Mutex // one write at a time
	ids int
}

func newAsker(r io.Reader, out *json.Encoder) *asker {
	a := &asker{lines: make(chan []byte), out: out}
	go func() {
		in := bufio.NewScanner(r)
		in.Buffer(make([]byte, 0, 64<<10), MaxLine)
		for in.Scan() {
			a.lines <- append([]byte(nil), in.Bytes()...)
		}
		a.err = in.Err()
		if a.err == nil {
			a.err = io.EOF
		}
		close(a.lines)
	}()
	return a
}

// next returns the next request line: one that came while a side request
// waited first.
func (a *asker) next() ([]byte, error) {
	if len(a.later) > 0 {
		line := a.later[0]
		a.later = a.later[1:]
		return line, nil
	}
	line, ok := <-a.lines
	if !ok {
		return nil, a.err
	}
	return line, nil
}

func (a *asker) write(v any) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.out.Encode(v)
}

// ask writes a side request and waits for its answer. Requests the host
// writes meanwhile are kept for next, and an answer to a side request no
// longer waited on is dropped.
func (a *asker) ask(ctx context.Context, sr SideRequest) (SideAnswer, error) {
	a.mu.Lock()
	a.ids++
	sr.ID = a.ids
	a.mu.Unlock()
	if err := a.write(sideLine{Side: &sr}); err != nil {
		return SideAnswer{}, fmt.Errorf("writing a side request to the host: %w", err)
	}
	var timeout <-chan time.Time
	if sr.TimeoutMS > 0 {
		t := time.NewTimer(time.Duration(sr.TimeoutMS)*time.Millisecond + sideSlack)
		defer t.Stop()
		timeout = t.C
	}
	for {
		select {
		case <-ctx.Done():
			return SideAnswer{}, ctx.Err()
		case <-timeout:
			return SideAnswer{}, fmt.Errorf("the host did not answer the %s request within %s", sr.Purpose, time.Duration(sr.TimeoutMS)*time.Millisecond)
		case line, ok := <-a.lines:
			if !ok {
				return SideAnswer{}, fmt.Errorf("the host ended before it answered the %s request", sr.Purpose)
			}
			var req Request
			if json.Unmarshal(line, &req) != nil || req.Op != OpSideAnswer {
				a.later = append(a.later, line)
				continue
			}
			if req.SideID != sr.ID {
				continue
			}
			if req.Failed != "" {
				return SideAnswer{}, fmt.Errorf("the host's model could not answer the %s request: %s", sr.Purpose, req.Failed)
			}
			return SideAnswer{Text: req.Answer, Stop: req.Stop}, nil
		}
	}
}

// end answers end: gnosh's turn-end checks, as a session asks them when
// its model answers with no tool call.
func (s *Server) end(ctx context.Context, req Request) Reply {
	if req.Session == "" {
		return fail(req.ID, CodeBadRequest, "end needs session")
	}
	rep := Reply{OK: true, ID: req.ID, Action: "end"}
	tu := s.opts.Turns
	if tu == nil {
		return rep
	}
	if len(req.Entries) > 0 {
		if msg := uniqueUUIDs(req.Entries); msg != "" {
			return fail(req.ID, CodeBadRequest, msg)
		}
		tu.Sync(req.Session, req.Entries)
	}
	reply := core.Message{Role: core.RoleAssistant}
	if req.Reply != nil {
		reply = *req.Reply
	}
	v := tu.End(s.withSide(ctx), req.Session, reply, req.Finished)
	if v.Continue {
		rep.Action, rep.Send = "continue", v.Send
	}
	rep.Driver, rep.Objection, rep.Stopped = v.Driver, v.Objection, v.Stopped
	rep.Notices = append(v.Notices, tu.Notices(req.Session)...)
	if s.opts.Log != nil {
		for _, n := range rep.Notices {
			s.opts.Log("gnosh bridge: " + n)
		}
	}
	return rep
}
