package llm

import (
	"bytes"
	"context"
	"errors"
	"io"
	"iter"
	"net/http"

	"github.com/gnosh-ai/gnosh/internal/core"
)

// wire is one wire format: how a request is written and how the stream
// that answers it is read.
type wire interface {
	// streamURL is where a request for a streamed answer is sent.
	streamURL() string
	// encode writes the JSON body that asks for req's answer as a stream.
	encode(req *core.Request) ([]byte, error)
	// newDecoder makes the reader of one answer's stream. begun is to be
	// called when the stream shows that the answer has begun without an
	// event that says so reaching the caller.
	newDecoder(begun func()) decoder
}

// decoder turns one answer's server-sent events into core's events. It
// appends what an event stands for to out and returns it.
type decoder interface {
	// event takes the next server-sent event. done says the stream is
	// over and the last of the events returned is the EventStop.
	event(out []core.Event, ev sseEvent) (events []core.Event, done bool, err error)
	// end is called when the stream stopped without the format's own
	// last word: the connection closed, or broke after the reason for
	// stopping was in. It returns the events that close the answer, or
	// the error if the answer cannot be taken as whole.
	end(out []core.Event) ([]core.Event, error)
	// stopped reports whether the stream has said why the model stopped.
	stopped() bool
	// leftover returns whatever of the answer the decoder was still
	// holding back. It is called when the stream fails, so that the
	// caller gets every piece that arrived before the error.
	leftover(out []core.Event) []core.Event
}

// stream sends req and returns the sequence of its answer's events.
//
// Everything happens on the goroutine that ranges over the sequence: the
// request is sent when the range starts, each event is read when the
// consumer asks for it, and the connection is closed before the range
// statement returns, however it ends. There is no goroutine to leak. Each
// new range over the sequence sends the request again.
func (p *provider) stream(ctx context.Context, w wire, req *core.Request) iter.Seq2[core.Event, error] {
	return func(yield func(core.Event, error) bool) {
		if req == nil {
			yield(core.Event{}, p.fail(core.ErrInvalidRequest, "there is no request to send"))
			return
		}
		x, err := p.open(ctx, w, req)
		if err != nil {
			yield(core.Event{}, err)
			return
		}
		defer x.close()
		x.traceResponse(nil)

		rd := newSSEReader(x)
		if p.tracing() {
			rd.skipped = func(line []byte) {
				p.traceFn(TraceRecord{Kind: TraceIgnored, Data: string(line)})
			}
		}
		dec := w.newDecoder(x.lim.begun)

		var out []core.Event
		begun := false
		for {
			ev, err := rd.next()
			done := false
			switch {
			case err == nil:
				if p.tracing() {
					p.traceFn(TraceRecord{Kind: TraceEvent, Event: ev.Name, Data: string(ev.Data)})
				}
				out, done, err = dec.event(out[:0], ev)
			case err == io.EOF:
				if rd.events == 0 {
					err = p.notAStream(x, rd.head)
					out = out[:0]
				} else {
					out, err = dec.end(out[:0])
				}
				done = true
			case errors.Is(err, errEventTooBig):
				out = out[:0]
				err = p.fail(core.ErrBadResponse, "one event of the stream is larger than %d bytes", rd.max)
			default:
				// The connection broke or a time limit ran out. If the
				// model had already said why it stopped, the answer is
				// whole and only its trailer is lost, so it stands;
				// unless the caller is the one who gave up.
				if dec.stopped() && ctx.Err() == nil {
					out, err = dec.end(out[:0])
					done = true
				} else {
					out, err = out[:0], x.failure("reading the answer", err)
				}
			}
			if err != nil {
				out = dec.leftover(out)
			}
			for _, e := range out {
				if !begun && carriesAnswer(e) {
					begun = true
					x.lim.begun()
				}
				if !yield(e, nil) {
					return
				}
			}
			if err != nil {
				yield(core.Event{}, err)
				return
			}
			if done {
				return
			}
		}
	}
}

// carriesAnswer reports whether an event holds some of the answer itself:
// the first such event is what the first-token limit waits for.
func carriesAnswer(e core.Event) bool {
	switch e.Type {
	case core.EventTextDelta, core.EventThinkingDelta, core.EventThinkingSignature,
		core.EventToolUseStart, core.EventToolUseDelta:
		return true
	}
	return false
}

// notAStream makes the error for a response that said it worked and then
// held no events at all. Some servers answer a request they cannot serve
// with status 200 and a JSON error; some ignore "stream": true.
func (p *provider) notAStream(x *exchange, head []byte) error {
	trimmed := bytes.TrimSpace(head)
	if len(trimmed) == 0 {
		return p.fail(core.ErrBadResponse, "the server answered with an empty stream")
	}
	if trimmed[0] == '{' || trimmed[0] == '[' {
		info := readErrorBody(trimmed)
		if info.message != "" && (len(info.codes) > 0 || info.status != 0 || bytes.Contains(trimmed, []byte(`"error"`))) {
			return p.apiError(x.resp.StatusCode, x.resp.Header, trimmed)
		}
		e := p.fail(core.ErrBadResponse, "the server answered with one JSON document instead of a stream of events")
		e.Body = string(clip(trimmed, bodyStart))
		return e
	}
	e := p.fail(core.ErrBadResponse, "the server's answer is not a stream of events")
	e.Body = string(clip(trimmed, bodyStart))
	return e
}

// badEvent makes the error for an event that cannot be read.
func (p *provider) badEvent(data []byte, err error) *core.ProviderError {
	e := p.fail(core.ErrBadResponse, "an event of the stream cannot be read: %v", err)
	e.Body = string(clip(data, bodyStart))
	e.Err = err
	return e
}

// endedEarly makes the error for a stream that stopped without saying why
// the model did.
func (p *provider) endedEarly(last []byte) *core.ProviderError {
	e := p.fail(core.ErrBadResponse, "the stream ended before the model said why it stopped")
	e.Body = string(clip(last, bodyStart))
	return e
}

// open sends req and returns the exchange of an answer that has begun.
// A server that refuses the images in a request (imagesRefused) is sent
// the request again at once without them, and from then on is never sent
// one: each image is a line of text that says it was left out. One that
// could not read an image (imageUnreadable) is sent it again without the
// images it may not have read (images.go). Without that, a picture a tool
// read would fail every later request of the session in the same way,
// since the conversation keeps it.
func (p *provider) open(ctx context.Context, w wire, req *core.Request) (*exchange, error) {
	send := p.prepared(req)
	for {
		body, err := w.encode(send)
		if err != nil {
			return nil, err
		}
		x, err := p.send(ctx, http.MethodPost, w.streamURL(), body, "text/event-stream")
		if err != nil {
			return nil, err
		}
		if x.ok() {
			p.took(send)
			return x, nil
		}
		err = x.statusError()
		x.close()
		next, again := p.afterImageError(req, send, err)
		if !again {
			return nil, err
		}
		send = next
	}
}
