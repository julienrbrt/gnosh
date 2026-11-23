package app

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/gnosh-ai/gnosh/internal/core"
	"github.com/gnosh-ai/gnosh/internal/permission"
)

// This file is EnterPlanMode and ExitPlanMode (spec/cc/tools.md section
// 14, permissions.md 2.3): the model's own switch into plan mode, where it
// looks and plans and changes nothing, and its way out, which is to
// present the plan. ExitPlanMode always asks (the engine's decideOther):
// the person carries the plan out, asking about each edit (the session
// goes back to the default mode) or accepting them (acceptEdits, the
// question's second choice), or keeps the session planning, and what they
// say comes back to the model. Neither is a sub-agent's.

const (
	enterPlanName = "EnterPlanMode"
	exitPlanName  = "ExitPlanMode"
)

// enterPlanTool is EnterPlanMode.
type enterPlanTool struct{ l *live }

func (t *enterPlanTool) Spec() core.ToolSpec {
	return core.ToolSpec{
		Name:        enterPlanName,
		Description: "Switches to plan mode, for a task that needs a plan first: you look and change nothing, then present the plan with ExitPlanMode. Not for a small, clear change.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
	}
}

func (t *enterPlanTool) Describe(call core.ToolCall, env *core.ToolEnv) (core.Action, error) {
	return core.Action{Kind: core.ActionOther, Summary: "Switch to plan mode"}, nil
}

func (t *enterPlanTool) Run(ctx context.Context, call core.ToolCall, env *core.ToolEnv) (core.ToolResult, error) {
	if t.l.engine.Mode() == permission.ModePlan {
		return core.TextResult("Plan mode is on already. Look and plan; present the plan with ExitPlanMode."), nil
	}
	if err := t.l.setMode(permission.ModePlan); err != nil {
		return core.ErrorResult("Plan mode could not be switched on: " + strings.TrimRight(err.Error(), ".") + "."), nil
	}
	res := core.TextResult("Plan mode is on: read, search and think, and change nothing. When the plan is ready, present it with ExitPlanMode; the user approves it or asks for changes.")
	// (For the screen, which says it in the person's words.)
	res.Meta = map[string]any{"planMode": true}
	return res, nil
}

// planApproved says a plan can be approved in this session, so that plan
// mode's tools are offered: ExitPlanMode is a person's alone to approve,
// who is there in a session somebody attends, and is reached through the
// agent view from a run in the background (agent.Options.Relay). In any
// other run it is not, and permissions.denyInsteadOfPrompting refuses
// what would ask (and takes the plan tools away, as in Claude Code).
func (l *live) planApproved() bool {
	return !l.cfg.Permissions.DenyInsteadOfPrompting.V && (l.interactive || l.p.runAsks != "")
}

// planInAnswer is what plan mode's refusal of an edit says to do in a
// session with no plan tools, in place of presenting the plan with
// ExitPlanMode.
const planInAnswer = "Nobody approves a plan in this session: give the plan in your answer."

// exitPlanTool is ExitPlanMode. planned holds the calls that were made
// in plan mode, as Describe saw them: the approval may have switched the
// mode before Run (acceptEdits), so Run cannot look at the mode itself.
type exitPlanTool struct {
	l       *live
	planned sync.Map
}

func (t *exitPlanTool) Spec() core.ToolSpec {
	return core.ToolSpec{
		Name:        exitPlanName,
		Description: "In plan mode, presents the finished plan for the user to approve; approved, the session leaves plan mode and you carry it out. The plan is Markdown: what changes where, in what order, and how you will check it.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"plan":{"type":"string"}},"required":["plan"],"additionalProperties":false}`),
	}
}

// notPlanning is ExitPlanMode's refusal outside plan mode: it asked, and
// its yes switched the mode, whatever the session was in (a "yes, asking
// about each edit" left acceptEdits on, and a plan never planned could
// switch the session to acceptEdits; the plan review of 2026-11-19).
func (t *exitPlanTool) notPlanning() error {
	return fmt.Errorf("ExitPlanMode is for plan mode, and the session is in the %s mode: there is no plan to present. Carry the task out, or switch to plan mode with EnterPlanMode first", t.l.engine.Mode())
}

func (t *exitPlanTool) Describe(call core.ToolCall, env *core.ToolEnv) (core.Action, error) {
	action := core.Action{Kind: core.ActionOther, Summary: "Present the plan for approval"}
	if t.l.engine.Mode() != permission.ModePlan {
		return action, t.notPlanning()
	}
	if call.ID != "" {
		t.planned.Store(call.ID, true)
	}
	return action, nil
}

func (t *exitPlanTool) Run(ctx context.Context, call core.ToolCall, env *core.ToolEnv) (core.ToolResult, error) {
	l := t.l
	if _, ok := t.planned.LoadAndDelete(call.ID); !ok && l.engine.Mode() != permission.ModePlan {
		return core.ErrorResult(t.notPlanning().Error() + "."), nil
	}
	// The person said yes. Accepting the edits has switched the mode
	// already; a plain yes goes back to the default mode.
	if l.engine.Mode() == permission.ModePlan {
		if err := l.setMode(permission.ModeDefault); err != nil {
			return core.ErrorResult("The plan was approved, and plan mode could not be left: " + strings.TrimRight(err.Error(), ".") + "."), nil
		}
	}
	text := fmt.Sprintf("The user approved the plan. The session is in the %s mode now: carry the plan out.", l.engine.Mode())
	// (For the screen, which says it in the person's words: the model's
	// text, under the call, had told the person to carry the plan out.)
	meta := map[string]any{"approved": true, "mode": string(l.engine.Mode())}
	if path := l.keepPlan(call); path != "" {
		text += " The plan as approved is kept in " + path + ", for when it is no longer in front of you."
		meta["planFile"] = path
	}
	res := core.TextResult(text)
	res.Meta = meta
	return res, nil
}

// keepPlan writes an approved plan to a file of the session's own, the
// person's alone (0600), and returns its path, or "" when it could not be
// kept: it was kept nowhere, so that after a summary neither the person
// nor the model could look at it again (the plan review of 2026-11-19).
func (l *live) keepPlan(call core.ToolCall) string {
	var in struct {
		Plan string `json:"plan"`
	}
	if json.Unmarshal(call.Input, &in) != nil || strings.TrimSpace(in.Plan) == "" || l.ts == nil {
		return ""
	}
	dir := filepath.Join(l.st.dirs(l.ts.ID()).Session, "plans")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return ""
	}
	path := filepath.Join(dir, l.p.now().UTC().Format("20060102-150405")+".md")
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return ""
	}
	_, werr := f.WriteString(strings.TrimSpace(in.Plan) + "\n")
	if cerr := f.Close(); werr != nil || cerr != nil {
		_ = os.Remove(path)
		return ""
	}
	return path
}
