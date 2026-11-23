package hooks

import (
	"crypto/rand"
	"fmt"
)

// Life is one moment of a hook's run, as Options.Lifecycle is told it: its
// start, then its end, for Claude Code's stream-json lines hook_started and
// hook_response (with --include-hook-events; always for SessionStart).
type Life struct {
	// ID is the same at a hook's start and at its end, and no other run's.
	ID string
	// Event is the moment the hook ran at, and Name the hook as Claude
	// Code names it: the moment and what its matcher was tested against
	// ("PostToolUse:Bash", "SessionStart:startup"), or the moment alone.
	Event Event
	Name  string
	// Ended is false at the start. At the end, Stdout and Stderr are what
	// the hook wrote (as far as gnosh keeps them), ExitCode its exit status
	// (-1 for none), and Outcome "success", "error" or "cancelled".
	Ended          bool
	Stdout, Stderr string
	ExitCode       int
	Outcome        string
}

// lifeStart tells Options.Lifecycle a hook of the moment starts, and
// returns the ID its end is told with.
func (r *Runner) lifeStart(in *Input) string {
	if r.opts.Lifecycle == nil {
		return ""
	}
	id := lifeID()
	r.opts.Lifecycle(Life{ID: id, Event: in.Event, Name: lifeName(in)})
	return id
}

// lifeEnd tells Options.Lifecycle how the hook ended.
func (r *Runner) lifeEnd(id string, in *Input, p proc) {
	if r.opts.Lifecycle == nil {
		return
	}
	outcome := "success"
	switch {
	case p.cancelled:
		outcome = "cancelled"
	case p.startErr != nil || p.timedOut || p.exit != 0 || p.unknown != "":
		outcome = "error"
	}
	r.opts.Lifecycle(Life{ID: id, Event: in.Event, Name: lifeName(in), Ended: true,
		Stdout: p.stdout, Stderr: p.stderr, ExitCode: p.exit, Outcome: outcome})
}

// lifeName is a hook's name as Claude Code's stream-json gives it.
func lifeName(in *Input) string {
	if s := in.subject(); s != "" {
		return string(in.Event) + ":" + s
	}
	return string(in.Event)
}

// lifeID is a random ID in the shape of a UUID (version 4).
func lifeID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
