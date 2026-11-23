package app

import (
	"testing"

	"github.com/gnosh-ai/gnosh/internal/app/apptest"
)

// A model on a llama.cpp server that thinks is given a budget for it: 4096
// tokens unless its thinkingBudget or MAX_THINKING_TOKENS says otherwise,
// with the note for the end of its thinking. MAX_THINKING_TOKENS=0 turns
// thinking off, and with it off no budget is sent.
func TestThinkingBudget(t *testing.T) {
	body := func(env []string, budget string) string {
		w := apptest.NewWorld(t, apptest.Reply{Text: "ok"})
		if budget != "" {
			w.Settings(`{"providers": {"local": {"type": "llamacpp", "baseUrl": "` + w.Model.URL + `"}}, "models": {"local/test-model": {"thinkingBudget": ` + budget + `}}, "model": "local/test-model"}`)
		}
		o := testOptions(w, "-p", "think about it")
		o.Environ = append(o.Environ, env...)
		runWith(t, t.Context(), o).wantCode(t, 0)
		return string(w.Model.Requests()[0].Body)
	}
	got := body(nil, "")
	has(t, "the default", got, `"enable_thinking":true`, `"reasoning_budget_tokens":4096`, `"reasoning_budget_message":"\n\n<gnosh>Thinking time is up.`)

	got = body([]string{"MAX_THINKING_TOKENS=1000"}, "")
	has(t, "MAX_THINKING_TOKENS", got, `"reasoning_budget_tokens":1000`)

	got = body(nil, "0")
	hasNot(t, "a model whose budget is 0", got, "reasoning_budget_tokens")

	got = body([]string{"MAX_THINKING_TOKENS=0"}, "")
	has(t, "MAX_THINKING_TOKENS=0", got, `"enable_thinking":false`)
	hasNot(t, "MAX_THINKING_TOKENS=0", got, "reasoning_budget_tokens")
}

// /status says how long the model may think.
func TestStatusSaysTheThinkingBudget(t *testing.T) {
	w := apptest.NewWorld(t)
	r := run(t, w, "-p", "/status").wantCode(t, 0)
	has(t, "/status", r.out, "thinking     on, at most 4096 tokens before a reply")
	o := testOptions(w, "-p", "/status")
	o.Environ = append(o.Environ, "MAX_THINKING_TOKENS=0")
	r = runWith(t, t.Context(), o).wantCode(t, 0)
	has(t, "/status with thinking off", r.out, "thinking     off")
	hasNot(t, "/status with thinking off", r.out, "at most")
}
