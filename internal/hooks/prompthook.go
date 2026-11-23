package hooks

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

// This file is prompt hooks, Claude Code's "type": "prompt": in place of a
// command, the hook is a rule or a condition the model judges ("Block any
// Bash command that reads .env files", "All the tests pass"), with the
// event's input put in for $ARGUMENTS (or after the prompt). The model
// answers with {"ok": true} or {"ok": false, "reason": "..."}, and
// "impossible": true beside a false when the condition can never be met.
// The answer is turned into what a command hook would have printed for the
// event, and read as that is:
//
//   - Stop and SubagentStop: "ok": false keeps the turn going, its reason
//     the model's next instruction ("decision": "block"), unless the
//     model said it is impossible, which lets the turn end;
//   - PreToolUse: "ok": false refuses the call and ends the turn, or with
//     continueOnBlock gives the model the reason as the call's error;
//   - PostToolUse: "ok": false ends the turn, or with continueOnBlock
//     gives the model the reason;
//   - PostToolBatch, UserPromptSubmit and UserPromptExpansion: "ok": false
//     blocks, with the reason;
//   - PostToolUseFailure: the reason goes to the model beside the error;
//   - PermissionRequest: an answer decides nothing (a command hook does).
//
// A model that does not answer in time, or not with that JSON, is a hook
// that could not run, as a command that failed is. The model asked is
// the one gnosh asks its own side questions (Options.Prompt).
//
// An agent hook ("type": "agent") is the same question put to a sub-agent
// that may look with its tools first (Options.Agent), whose last message
// is read for the answer. Its "ok": false is read as a prompt hook's with
// continueOnBlock (the reason goes back to the model, and the turn goes
// on), and "impossible" is not read, as in Claude Code.

// promptEvents are the events a prompt hook runs on.
var promptEvents = map[Event]bool{
	PreToolUse: true, PostToolUse: true, PostToolUseFailure: true, PostToolBatch: true,
	Stop: true, SubagentStop: true, UserPromptSubmit: true, UserPromptExpansion: true, PermissionRequest: true,
}

// promptTimeoutDefault and agentTimeoutDefault are how long a prompt
// hook's model and an agent hook's sub-agent are given when the hook sets
// no timeout, as in Claude Code.
const (
	promptTimeoutDefault = 30 * time.Second
	agentTimeoutDefault  = 60 * time.Second
)

// promptWaitMost is how long, past its limit, a prompt hook may wait for a
// model that is busy with another request.
const promptWaitMost = 10 * time.Minute

// answerLimitKey carries, to Options.Prompt, how long the model has to
// answer a prompt hook once it is asked.
type answerLimitKey struct{}

// AnswerLimit returns how long a prompt hook, whose question ctx carries,
// gives the model to answer once it is asked, and whether ctx says.
func AnswerLimit(ctx context.Context) (time.Duration, bool) {
	d, ok := ctx.Value(answerLimitKey{}).(time.Duration)
	return d, ok
}

// judgement is a prompt hook's answer.
type judgement struct {
	OK         *bool  `json:"ok"`
	Reason     string `json:"reason"`
	Impossible bool   `json:"impossible"`
}

// askModel runs a prompt hook: the prompt, with the event in it, goes to
// the model through Options.Prompt, and its answer becomes what a command
// hook would have printed.
func (r *Runner) askModel(ctx context.Context, h Hook, event []byte, limit time.Duration) (p proc) {
	p.exit, p.limit = -1, limit
	began := r.now()
	defer func() { p.took = r.now().Sub(began) }()
	ask := r.opts.Prompt
	if h.Agent {
		ask = r.opts.Agent
	}
	if ask == nil {
		p.startErr = errors.New("this gnosh asks no model for a hook")
		return p
	}
	text := h.Prompt
	if strings.Contains(text, "$ARGUMENTS") {
		text = strings.ReplaceAll(text, "$ARGUMENTS", string(event))
	} else {
		text += "\n\n" + string(event)
	}
	// A prompt hook's limit is the model's to answer in once it is asked
	// (AnswerLimit): its wait for a model busy with another request (a
	// /btw) does not count, or a check failed closed for want of a turn
	// (the side-request review of 2026-11-19). The wait has its own bound,
	// past the limit; an agent hook's sub-agent has the limit as before.
	most := limit
	if !h.Agent {
		most += promptWaitMost
	}
	cctx, cancel := context.WithTimeout(context.WithValue(ctx, answerLimitKey{}, limit), most)
	defer cancel()
	answer, err := ask(cctx, h.Model, text)
	switch {
	case ctx.Err() != nil:
		p.cancelled = true
		return p
	case cctx.Err() != nil || errors.Is(err, context.DeadlineExceeded):
		p.timedOut = true
		return p
	case err != nil:
		p.startErr = err
		return p
	}
	v, err := readVerdict(answer)
	if err != nil {
		p.unknown = "the model did not answer with the JSON a prompt hook asks for (" + err.Error() + ")"
		p.stderr = answer
		return p
	}
	if h.Agent {
		v.Impossible = false
	}
	p.exit, p.stdout = 0, verdictOutput(h.Event, v, h.ContinueOnBlock || h.Agent)
	return p
}

// readVerdict finds the answer in what the model wrote: the whole text, or
// the last JSON object in it that says "ok" (a model may put words or a
// code block around it, and an example or a quoted object before it: the
// answer comes at the end). An object inside another is not one of its
// own.
func readVerdict(text string) (judgement, error) {
	try := func(s string) (judgement, bool) {
		var v judgement
		if json.Unmarshal([]byte(s), &v) == nil && v.OK != nil {
			return v, true
		}
		return judgement{}, false
	}
	t := strings.TrimSpace(text)
	if v, ok := try(t); ok {
		return v, nil
	}
	var last *judgement
	for i := 0; i < len(t); i++ {
		if t[i] != '{' {
			continue
		}
		dec := json.NewDecoder(strings.NewReader(t[i:]))
		var raw json.RawMessage
		if dec.Decode(&raw) != nil {
			continue
		}
		if v, ok := try(string(raw)); ok {
			last = &v
		}
		// (Past the whole object: what is inside it is part of it.)
		i += int(dec.InputOffset()) - 1
	}
	if last != nil {
		return *last, nil
	}
	return judgement{}, errors.New(`no JSON object with "ok" in it`)
}

// verdictOutput is what a command hook would print for the answer, on the
// event.
func verdictOutput(e Event, v judgement, continueOnBlock bool) string {
	if *v.OK {
		return "{}"
	}
	reason := strings.TrimSpace(v.Reason)
	if reason == "" {
		reason = "the hook's model answered \"ok\": false and gave no reason"
	}
	out := map[string]any{}
	switch e {
	case Stop, SubagentStop:
		if v.Impossible {
			return "{}"
		}
		out["decision"], out["reason"] = "block", reason
	case PreToolUse:
		out["hookSpecificOutput"] = map[string]any{"hookEventName": string(e), "permissionDecision": "deny", "permissionDecisionReason": reason}
		if !continueOnBlock {
			out["continue"], out["stopReason"] = false, reason
		}
	case PostToolUse:
		if continueOnBlock {
			out["decision"], out["reason"] = "block", reason
		} else {
			out["continue"], out["stopReason"] = false, reason
		}
	case PostToolBatch, UserPromptSubmit, UserPromptExpansion:
		out["decision"], out["reason"] = "block", reason
	case PostToolUseFailure:
		out["hookSpecificOutput"] = map[string]any{"hookEventName": string(e), "additionalContext": reason}
	default:
		return "{}"
	}
	data, _ := json.Marshal(out)
	return string(data)
}
