package app

import (
	"context"
	"errors"
	"time"

	"github.com/gnosh-ai/gnosh/internal/agent"
	"github.com/gnosh-ai/gnosh/internal/hooks"
)

// promptHookSystem is what the model is told as it judges a prompt hook
// (hooks/prompthook.go): the hook's text is the question.
const promptHookSystem = `You judge one hook for gnosh, a coding agent's harness. The user wrote the hook as a rule about what to block or allow, or as a condition that must hold; the event it runs on comes with it, as JSON. Decide whether the rule allows it, or whether the condition holds, from what you are given.

Answer with one JSON object and nothing else:
{"ok": true} when it is allowed, or the condition holds;
{"ok": false, "reason": "..."} when it is not, the reason in one or two sentences the agent can act on;
add "impossible": true beside "ok": false only when the condition can never be met, however long the agent works.`

// promptHookTokens caps the answer, with room for a model that thinks
// first.
const promptHookTokens = 1024

// promptHookAsk is the hooks' Options.Prompt: the hook's question goes to
// the model the session asks its own side questions, at temperature zero.
// A hook's "model" is not honored: gnosh does not pick a model per hook.
func (l *live) promptHookAsk(ctx context.Context, model, text string) (string, error) {
	if l.agent == nil {
		return "", errors.New("the session has no model to ask yet")
	}
	limit := 2 * time.Minute
	if d, ok := hooks.AnswerLimit(ctx); ok {
		limit = d
	} else if dl, ok := ctx.Deadline(); ok {
		limit = time.Until(dl)
	}
	zero := 0.0
	out, err := l.agent.Side(ctx, agent.SideRequest{Purpose: "hook", System: promptHookSystem, Prompt: text,
		MaxTokens: promptHookTokens, Temperature: &zero, Timeout: limit})
	if err != nil {
		return "", err
	}
	return out.Text, nil
}
