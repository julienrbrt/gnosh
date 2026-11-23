package agent

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/gnosh-ai/gnosh/internal/core"
)

// RepeatPolicy tunes the guard that notices a model repeating itself. The
// guard never ends a turn: it tells the model what it keeps doing, and the
// turn goes on. The zero value gives the defaults.
type RepeatPolicy struct {
	// Window is how many of the latest rounds are looked at (ten), and
	// Over how many of them may be the same before the model is told
	// (five: the sixth tells it).
	Window int
	Over   int

	// Key returns what is compared for one call and its result. Rounds
	// are the same when the keys of their calls are. Nil compares the
	// tool, the arguments and the result's text as they are; the wiring
	// gives a function that blanks the numbers in a result for tools
	// whose every answer ends in a duration or a counter.
	Key func(call core.ToolCall, res core.ToolResult) string

	// Nudge may return its own words for a repetition, for a case that
	// deserves them: a close that mem keeps refusing is told that the
	// work is still to do. Returning "" leaves the words to the loop.
	// What it returns is stored as given.
	Nudge func(rep Repetition) string
}

// Repetition is what the guard found.
type Repetition struct {
	// Rounds is how many of the last Window rounds were the same as the
	// newest one, and Window how many rounds were looked at.
	Rounds int
	Window int
	// Calls are the calls of the newest round with their results.
	Calls []RepeatedCall
	// Other holds the calls of the other round when two rounds took
	// turns, and is nil when one round was repeated.
	Other []RepeatedCall
	// Cycle holds the rounds of a cycle of three or four that came round
	// three times in a row, in order, and is nil otherwise.
	Cycle [][]RepeatedCall
}

// cycleTurns is how many times in a row a cycle of three or four rounds
// must come round, the same calls with the same results, to be a loop:
// the same three commands made a hundred calls over, under the bar of the
// rules for one round and for two (the FrontierHarness session,
// 2026-11-20).
const cycleTurns = 3

// RepeatedCall is one call of a repeated round, with its result.
type RepeatedCall struct {
	// Call is the call as it ran and Result what it returned.
	Call   core.ToolCall
	Result core.ToolResult
}

// repeatGuard remembers the latest rounds of a turn.
type repeatGuard struct {
	sigs   []string
	rounds [][]RepeatedCall
}

// reset forgets everything, so that counting starts afresh.
func (g *repeatGuard) reset() {
	g.sigs, g.rounds = nil, nil
}

// defaultRepeatKey compares a call as it is, and its result with what
// changes from run to run blanked (sameButFor).
func defaultRepeatKey(call core.ToolCall, res core.ToolResult) string {
	return RepeatKey(call, res)
}

// RepeatKey is what the guard compares of a call and its result unless the
// wiring says otherwise: the tool and its arguments as they are, whether
// it failed, and its result's text with what changes from run to run
// blanked (sameButFor). A model ran "python3 /app/eval.py | tail -60" 58
// times and restarted postfix 35, in a loop of the same commands, and was
// never told: each result held a fresh random id, or a new PID (the
// FrontierHarness session, 2026-11-20).
func RepeatKey(call core.ToolCall, res core.ToolResult) string {
	failed := "ok"
	if res.IsError {
		failed = "error"
	}
	return call.Name + "\x00" + string(call.Input) + "\x00" + failed + "\x00" + sameButFor(core.TextOf(res.Content))
}

// hexID is a run of hex digits as long as a short commit or longer, a
// random id, a hash, a part of a UUID.
var hexID = regexp.MustCompile(`[0-9A-Fa-f]{4,}`)

// digits is a run of digits: a PID, a port, a time, a count.
var digits = regexp.MustCompile(`[0-9]+`)

// sameButFor is a result's text with what changes from one run of the same
// command to the next blanked: every run of hex digits of four or more
// that holds a digit (an id, a hash), then every run of digits.
func sameButFor(text string) string {
	text = hexID.ReplaceAllStringFunc(text, func(s string) string {
		if strings.ContainsAny(s, "0123456789") {
			return "#"
		}
		return s
	})
	return digits.ReplaceAllString(text, "#")
}

// checkRepeat looks at the round that just ended. It is only ever called
// after a round the model will be asked again after, so a final answer is
// never replaced by a nudge and a turn that was ended is never started
// again by one. When the model is found to repeat itself it is told so,
// with the call quoted, and the counting starts afresh; there is no limit
// on how often that may happen.
func (a *Agent) checkRepeat(t *turn, ro roundOutcome) error {
	window, over := a.opts.Repeat.Window, a.opts.Repeat.Over
	if window <= 0 {
		window = 10
	}
	if over <= 0 {
		over = 5
	}
	key := a.opts.Repeat.Key
	if key == nil {
		key = defaultRepeatKey
	}

	var sig strings.Builder
	calls := make([]RepeatedCall, 0, len(ro.calls))
	for _, c := range ro.calls {
		call := core.ToolCall{ID: c.call.ID, Name: c.call.Name, Input: c.input}
		calls = append(calls, RepeatedCall{Call: call, Result: c.res})
		sig.WriteString(key(call, c.res))
		sig.WriteString("\x01")
	}
	g := &t.guard
	g.sigs = append(g.sigs, sig.String())
	g.rounds = append(g.rounds, calls)
	// (Enough is kept for the window, and for a cycle of four come round
	// cycleTurns times.)
	if keep := max(window, 4*cycleTurns); len(g.sigs) > keep {
		g.sigs = g.sigs[len(g.sigs)-keep:]
		g.rounds = g.rounds[len(g.rounds)-keep:]
	}
	sigs, rounds := g.sigs, g.rounds
	if len(sigs) > window {
		sigs, rounds = sigs[len(sigs)-window:], rounds[len(rounds)-window:]
	}

	newest := sigs[len(sigs)-1]
	same := 0
	otherAt := -1
	distinct := map[string]bool{}
	for i, s := range sigs {
		distinct[s] = true
		if s == newest {
			same++
		} else {
			otherAt = i
		}
	}

	var rep Repetition
	switch {
	case same > over:
		rep = Repetition{Rounds: same, Window: len(sigs), Calls: calls}
	case len(sigs) == window && len(distinct) == 2:
		// Two rounds taking turns each fill half the window, under the
		// bar; a full window with only two kinds of round in it is a
		// loop all the same.
		rep = Repetition{Rounds: same, Window: len(sigs), Calls: calls, Other: rounds[otherAt]}
	default:
		cyc, looked := g.cycle()
		if cyc == nil {
			return nil
		}
		rep = Repetition{Rounds: looked, Window: looked, Calls: calls, Cycle: cyc}
	}
	g.reset()

	text := ""
	if f := a.opts.Repeat.Nudge; f != nil {
		text = f(rep)
	}
	if text == "" {
		switch {
		case rep.Cycle != nil:
			quoted := make([]string, len(rep.Cycle))
			for i, round := range rep.Cycle {
				quoted[i] = quoteCalls(round)
			}
			text = a.mark(fmt.Sprintf(a.texts.RepeatCycle, rep.Window, len(rep.Cycle), strings.Join(quoted, "\n\nthen\n\n")))
		case rep.Other == nil:
			text = a.mark(fmt.Sprintf(a.texts.RepeatNudge, rep.Rounds, rep.Window, quoteCalls(rep.Calls)))
		default:
			text = a.mark(fmt.Sprintf(a.texts.RepeatPair, rep.Window, quoteCalls(rep.Calls)+"\n\nand\n\n"+quoteCalls(rep.Other)))
		}
	}
	return a.ownPrompt(t, "nudge:repeat", text)
}

// cycle finds the rounds the latest rounds keep going round, and how many
// rounds were looked at: a cycle of three or four rounds come round
// cycleTurns times in a row, the same calls with the same results; or, in
// any order, the last 4*cycleTurns rounds made of no more than three kinds
// of round (a test run and a restart of the server, one 58 times and the
// other 35, in no fixed order: the FrontierHarness session, 2026-11-20).
// It returns nil when there is no such loop.
func (g *repeatGuard) cycle() ([][]RepeatedCall, int) {
	for p := 3; p <= 4; p++ {
		n := p * cycleTurns
		if len(g.sigs) < n {
			continue
		}
		s := g.sigs[len(g.sigs)-n:]
		loop := true
		for i := p; i < n && loop; i++ {
			loop = s[i] == s[i-p]
		}
		if loop {
			return g.rounds[len(g.rounds)-p:], n
		}
	}
	n := 4 * cycleTurns
	if len(g.sigs) < n {
		return nil, 0
	}
	seen := map[string]bool{}
	var kinds [][]RepeatedCall
	for i := len(g.sigs) - n; i < len(g.sigs); i++ {
		if !seen[g.sigs[i]] {
			seen[g.sigs[i]] = true
			kinds = append(kinds, g.rounds[i])
		}
	}
	if len(kinds) > 3 {
		return nil, 0
	}
	return kinds, n
}

// quoteCalls writes calls and their results so that a model can see what
// is meant.
func quoteCalls(calls []RepeatedCall) string {
	var b strings.Builder
	for i, c := range calls {
		if i > 0 {
			b.WriteString("\n")
		}
		fmt.Fprintf(&b, "%s %s\n", c.Call.Name, clip(string(c.Call.Input), 300))
		result := strings.TrimSpace(core.TextOf(c.Result.Content))
		if result == "" {
			result = "(no text)"
		}
		if c.Result.IsError {
			b.WriteString("failed: ")
		} else {
			b.WriteString("returned: ")
		}
		b.WriteString(clip(result, 600))
	}
	return b.String()
}
