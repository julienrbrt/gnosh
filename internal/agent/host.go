package agent

import (
	"context"
	"fmt"

	"github.com/gnosh-ai/gnosh/internal/core"
)

// HostDrivers asks a session's Drivers as its turns would end, for a loop
// that is not an Agent's: `gnosh bridge`'s host runs the model, and asks
// the bridge whether its turn may end. The rules are askDrivers' and the
// checkpoint's (harness-agent-loop.md LOOP-20), with the same texts: the
// first driver that sends the model back does so, every Every times in a
// row the model is asked first whether there is a genuine reason to stop,
// the marker counts only in the finished reply to that question, and a
// driver that cannot decide sends the model back. It is not safe for use
// by several goroutines at once.
type HostDrivers struct {
	// Drivers are asked in order, as an Agent asks Options.Drivers.
	Drivers []Driver

	streak   []int
	sentBack bool
	cp       *checkpoint
	turn     string
}

// HostVerdict is what HostDrivers.End says.
type HostVerdict struct {
	// Continue says the model is sent back, with Prompt as a hidden prompt
	// of the harness's own; otherwise the turn ends.
	Continue bool
	Prompt   string
	// Driver names the driver that sent the model back, or the one whose
	// checkpoint the model chose to stop at; Objection is what it holds
	// against the turn ending.
	Driver    string
	Objection string
	// Stopped is the model's reason when it stopped at a checkpoint.
	Stopped string
	// Notices are lines for the person.
	Notices []string
}

// Begin starts a new turn: the streaks and the checkpoint are the turn's.
func (h *HostDrivers) Begin(turn string) {
	h.streak = make([]int, len(h.Drivers))
	h.sentBack = false
	h.cp = nil
	h.turn = turn
}

// End is asked as the turn would end on reply, which asks for no tool.
// finished says the model ended it itself (not cut off at its length or
// refused), which the marker of a checkpoint needs.
func (h *HostDrivers) End(ctx context.Context, reply Reply, finished bool) HostVerdict {
	if len(h.streak) != len(h.Drivers) {
		h.Begin(h.turn)
	}
	if cp := h.cp; cp != nil {
		h.cp = nil
		if finished {
			if reason, ok := cutMarker(reply.Text, cp.marker); ok {
				return HostVerdict{Driver: cp.driver, Objection: cp.objection, Stopped: reason,
					Notices: []string{fmt.Sprintf("The agent chose to stop (%s): %s", cp.objection, reason)}}
			}
		}
	}
	texts := defaultTexts
	var out HostVerdict
	for i, d := range h.Drivers {
		every := d.Every
		if every <= 0 {
			every = 10
		}
		marker := d.Marker
		if marker == "" {
			marker = StopMarker
		}
		end := TurnEnd{
			Turn: h.turn, Reply: reply.Message, Text: reply.Text,
			Streak: h.streak[i], SentBack: h.sentBack,
			Checkpoint: (h.streak[i]+1)%every == 0, Marker: marker,
		}
		v, err := d.Decide(ctx, end)
		if ctx.Err() != nil {
			return out
		}
		if err != nil {
			out.Notices = append(out.Notices, fmt.Sprintf(
				"The turn-end check %q could not run (%v). The turn goes on, because a check that could not run has not passed.", d.Name, err))
			v = Verdict{
				Action:    Continue,
				Prompt:    defaultMark(fmt.Sprintf(texts.DriverFailed, d.Name, err)),
				Objection: fmt.Sprintf("the check %q could not run: %v", d.Name, err),
			}
		}
		if v.Notice != "" {
			out.Notices = append(out.Notices, v.Notice)
		}
		switch v.Action {
		case End:
			h.streak[i] = 0
		case Halt:
			out.Driver = d.Name
			out.Objection = firstNonEmpty(v.Objection, v.Prompt, d.Name+" ended the turn")
			return out
		case Continue:
			h.streak[i]++
			h.sentBack = true
			if v.Prompt == "" {
				v.Prompt = defaultMark(firstNonEmpty(v.Objection, "The turn may not end yet: the check "+d.Name+" objects."))
			}
			objection := firstNonEmpty(v.Objection, clip(oneLine(v.Prompt), 400))
			prompt := v.Prompt
			if end.Checkpoint {
				if v.Checkpoint != "" {
					prompt = v.Checkpoint
				} else {
					prompt = defaultMark(fmt.Sprintf(texts.Checkpoint, marker, h.streak[i])) + "\n\n" + v.Prompt
				}
				h.cp = &checkpoint{driver: d.Name, marker: marker, objection: objection}
			}
			out.Continue, out.Prompt, out.Driver, out.Objection = true, prompt, d.Name, objection
			return out
		}
	}
	return out
}

// Reply is the reply a host's turn would end on.
type Reply struct {
	Message core.Message
	Text    string
}
