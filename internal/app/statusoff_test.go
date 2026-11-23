package app

import (
	"testing"

	"github.com/gnosh-ai/gnosh/internal/app/apptest"
)

// /status names the features that are off in the session, each with what
// turns it on (Claude Code 2.1.326 has the like), and not those that are on.
func TestStatusSaysWhatIsOff(t *testing.T) {
	w := apptest.NewWorld(t)
	r := run(t, w, "-p", "/status").wantCode(t, 0)
	has(t, "/status", r.out,
		"off          WebSearch, until webSearchUrl names a SearXNG server",
		"             mem's search by meaning, until memEmbeddingsUrl names an embedding server",
		"             prompt suggestions, until promptSuggestionEnabled is on")
	hasNot(t, "/status", r.out, "automatic summaries, until", "images: the model's server")

	w.Settings(`{"options": {"webSearchUrl": "http://203.0.113.9:8888", "promptSuggestionEnabled": true, "autoCompactEnabled": false}}`)
	r = run(t, w, "-p", "/status").wantCode(t, 0)
	has(t, "/status with options set", r.out, "off          mem's search by meaning", "automatic summaries, until autoCompactEnabled is on")
	hasNot(t, "/status with options set", r.out, "WebSearch, until", "prompt suggestions, until")
}
