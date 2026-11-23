package hooks

import (
	"math"
	"strings"
	"time"
)

// This file is the hooks that run in the background (spec/cc/hooks.md 7).
// "async": true starts the hook and goes on at once. Such a hook decides
// nothing; what its answer adds for the model (its additionalContext and
// its systemMessage) goes out with the model's next request. With
// "asyncRewake": true it is the same, but an exit with 2 wakes the model
// at once, even in an idle session, with what the hook wrote to stderr, or
// to stdout when stderr is empty. An "async" hook has no time limit; an
// "asyncRewake" one keeps its timeout. Close stops those still running.

// Background is what a hook that ran in the background left when it
// ended. It goes to Options.Background, from a goroutine of its own.
type Background struct {
	// Event is the moment the hook ran at, and Record what it did.
	Event  Event
	Record Record
	// Context is what its answer added for the model: its
	// additionalContext, then its systemMessage.
	Context []string
	// Wake is the text that wakes the model: what an "asyncRewake" hook
	// that exited with 2 wrote to stderr, or to stdout when stderr was
	// empty. It is empty otherwise.
	Wake string
	// Warning says, for the person, why the hook could not be evaluated.
	// Nothing of such a hook goes to the model.
	Warning string
}

// background starts a hook in the background and returns its record for
// now. When the hook ends, what it left goes to Options.Background, unless
// the hook was stopped by Close.
func (r *Runner) background(in *Input, info eventInfo, h selected, stdin []byte, env []string, dir string) Record {
	limit := time.Duration(math.MaxInt64)
	if h.AsyncRewake {
		limit = timeoutFor(h.Event, h.Timeout)
	}
	event := in.Event
	life := *in
	id := r.lifeStart(&life)
	r.bg.Add(1)
	go func() {
		defer r.bg.Done()
		p := r.exec(r.bgCtx, h.Hook, stdin, env, dir, limit)
		r.lifeEnd(id, &life, p)
		if p.cancelled || r.onBackground == nil {
			return
		}
		v := r.judge(event, info, h, p)
		b := Background{Event: event, Record: Record{
			Event: h.Event, Matcher: h.Matcher, Command: h.CommandLine(), Name: h.Name, Source: h.Source,
			Duration: p.took, ExitCode: p.exit, Result: ResultNone, Background: true,
			SystemMessage: v.warning, AdditionalContext: v.context, Notes: v.notes,
			Stderr: start(strings.TrimSpace(p.stderr), stderrStart),
		}}
		switch {
		case v.failure != "":
			b.Record.Result, b.Record.Reason = ResultCouldNotEvaluate, v.failure+"."
			b.Warning = "The " + string(event) + " hook " + h.label() + ", which ran in the background, could not be evaluated: " + v.failure + "."
		default:
			for _, text := range []string{v.context, v.warning} {
				if strings.TrimSpace(text) != "" {
					b.Context = append(b.Context, text)
				}
			}
			if h.AsyncRewake && p.exit == 2 {
				b.Wake = clip(strings.TrimSpace(p.stderr), r.maxText)
				if b.Wake == "" {
					b.Wake = clip(strings.TrimSpace(p.stdout), r.maxText)
				}
				if b.Wake == "" {
					b.Wake = "The hook exited with 2 and said nothing."
				}
				b.Record.Result = ResultFeedback
			}
		}
		r.onBackground(b)
	}()
	return Record{
		Event: h.Event, Matcher: h.Matcher, Command: h.CommandLine(), Name: h.Name, Source: h.Source,
		ExitCode: -1, Result: ResultBackground, Background: true,
	}
}

// Close stops the hooks still running in the background, each with its
// whole process group, and waits for them, though not for longer than
// wait. Nothing of a hook it stopped goes to Options.Background. A Runner
// runs no hook in the background after Close.
func (r *Runner) Close(wait time.Duration) {
	r.bgStop()
	done := make(chan struct{})
	go func() {
		r.bg.Wait()
		close(done)
	}()
	t := time.NewTimer(wait)
	defer t.Stop()
	select {
	case <-done:
	case <-t.C:
	}
}
