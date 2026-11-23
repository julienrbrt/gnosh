package app

import (
	"fmt"
	"strings"
	"testing"

	"github.com/gnosh-ai/gnosh/internal/app/apptest"
)

// bigFiles writes n files of about 12,000 bytes each into the world's
// folder.
func bigFiles(w *apptest.World, n int) {
	var b strings.Builder
	for i := 0; b.Len() < 12000; i++ {
		fmt.Fprintf(&b, "line %04d of the big file holds plain words and nothing else at all\n", i)
	}
	for i := 0; i < n; i++ {
		w.Write(fmt.Sprintf("big%d.txt", i), b.String())
	}
}

// isSummaryRequest reports whether a request asks the model for a summary
// of the conversation.
func isSummaryRequest(r apptest.Request) bool {
	msgs := r.Messages()
	return len(msgs) > 0 && strings.Contains(apptest.Text(msgs[len(msgs)-1]), "replaced by a summary")
}

// With a small context window, a conversation that outgrows it is made to
// fit before each request goes out: no request is ever larger than the
// window the server reported, and the work carries on.
func TestContextWindowIsNeverExceeded(t *testing.T) {
	const window = 16000
	w := apptest.NewWorld(t)
	w.Model.SetWindow(window)
	bigFiles(w, 8)
	reads := 0
	w.Model.Respond(func(n int, r apptest.Request) *apptest.Reply {
		if isSummaryRequest(r) {
			return &apptest.Reply{Text: "SUMMARY: the user asked to read all the big files; some are read, the rest are still to read."}
		}
		reads++
		if reads <= 8 {
			return &apptest.Reply{Calls: []apptest.Call{{ID: fmt.Sprintf("c%d", reads), Name: "Read", Args: fmt.Sprintf(`{"file_path":"big%d.txt"}`, reads-1)}}}
		}
		return &apptest.Reply{Text: "All read."}
	})
	r := run(t, w, "-p", "read all the big files").wantCode(t, 0)
	if r.out != "All read.\n" {
		t.Errorf("stdout = %q", r.out)
	}
	summaries := 0
	for i, req := range w.Model.Requests() {
		// A token is at least three bytes of this plain text, so a request
		// of no more than three bytes a token of the window is inside it.
		if tokens := len(req.Body) / 4; tokens > window {
			t.Errorf("request %d is about %d tokens, more than the window of %d", i, tokens, window)
		}
		if isSummaryRequest(req) {
			summaries++
		}
	}
	if summaries == 0 {
		t.Errorf("the conversation outgrew the window and no room was made")
	}
	has(t, "stderr", r.err, "summarized")
	checkSession(t, w, w.Sessions()[0])
}

// A server that cannot say how large its window is: gnosh says what it
// assumes, on standard error, and the run goes on.
func TestUnknownContextWindow(t *testing.T) {
	w := apptest.NewWorld(t, apptest.Reply{Text: "ok"})
	w.Model.SetWindow(0)
	r := run(t, w, "-p", "hello").wantCode(t, 0)
	if r.out != "ok\n" {
		t.Errorf("stdout = %q", r.out)
	}
	has(t, "stderr", r.err, "could not learn the context window", "32,768")
}

// A server that refuses a request as too long, although gnosh believed it
// would fit: gnosh learns the real window, makes room and asks again.
func TestRequestRefusedAsTooLong(t *testing.T) {
	w := apptest.NewWorld(t)
	bigFiles(w, 3)
	reads, refused := 0, false
	w.Model.Respond(func(n int, r apptest.Request) *apptest.Reply {
		if isSummaryRequest(r) {
			return &apptest.Reply{Text: "SUMMARY: the user asked to read the big files; three are read."}
		}
		reads++
		switch {
		case reads <= 3:
			return &apptest.Reply{Calls: []apptest.Call{{ID: fmt.Sprintf("c%d", reads), Name: "Read", Args: fmt.Sprintf(`{"file_path":"big%d.txt"}`, reads-1)}}}
		case !refused:
			refused = true
			reads--
			sent := len(r.Body) / 4
			return &apptest.Reply{Status: 400, Body: fmt.Sprintf(
				`{"error":{"code":400,"message":"the request exceeds the available context size, try increasing it","type":"exceed_context_size_error","n_prompt_tokens":%d,"n_ctx":%d}}`, sent, sent-1500)}
		}
		return &apptest.Reply{Text: "Done after making room."}
	})
	r := run(t, w, "-p", "read the big files").wantCode(t, 0)
	if r.out != "Done after making room.\n" {
		t.Errorf("stdout = %q", r.out)
	}
	has(t, "stderr", r.err, "too long", "makes room and asks again")
}

// Each way a provider can fail is told on standard error in plain words
// that name the server, and ends the run as the kind of failure asks: a
// refusal at once, a server that is busy or restarting after waiting for
// it.
func TestProviderErrors(t *testing.T) {
	fail := func(status int, body string) apptest.Reply { return apptest.Reply{Status: status, Body: body} }
	many := func(r apptest.Reply) []apptest.Reply {
		out := make([]apptest.Reply, 400)
		for i := range out {
			out[i] = r
		}
		return out
	}
	cases := []struct {
		name     string
		replies  []apptest.Reply
		code     int
		out      string
		stderr   []string
		requests int // 0: any number
		reason   string
	}{
		{"the key is refused", []apptest.Reply{fail(401, `{"error":{"message":"Invalid API key","code":"invalid_api_key"}}`)},
			1, "", []string{"refused the key", "HTTP 401", "Invalid API key", "http://127.0.0.1:"}, 1, "api_error"},
		{"the request is refused", []apptest.Reply{fail(400, `{"error":{"message":"messages: bad field","type":"invalid_request_error"}}`)},
			1, "", []string{"refused the request", "HTTP 400", "messages: bad field"}, 1, "api_error"},
		{"too many requests, then an answer", []apptest.Reply{fail(429, `{"error":{"message":"Rate limit exceeded"}}`), {Text: "ok after a wait"}},
			0, "ok after a wait\n", []string{"asked for fewer requests", "HTTP 429", "Trying again"}, 2, ""},
		{"loading its model, then an answer", []apptest.Reply{fail(503, `{"error":{"message":"Loading model"}}`), {Text: "ok after loading"}},
			0, "ok after loading\n", []string{"is loading its model", "HTTP 503", "Loading model", "is answering again"}, 2, ""},
		{"busy, then an answer", []apptest.Reply{fail(503, `{"error":{"message":"Server busy"}}`), {Text: "ok after a wait"}},
			0, "ok after a wait\n", []string{"too busy", "HTTP 503", "Server busy", "is answering again"}, 2, ""},
		{"a failure on the server's side, then an answer", []apptest.Reply{fail(500, ""), {Text: "ok after all"}},
			0, "ok after all\n", []string{"failed on its own side", "HTTP 500", "Trying again"}, 2, ""},
		{"a failure on the server's side that does not pass", many(fail(500, "")),
			1, "", []string{"kept failing", "HTTP 500", "The turn failed"}, 0, "api_error"},
		{"the answer breaks off, then comes whole", []apptest.Reply{{Text: "half an answer", Cut: true}, {Text: "the whole answer"}},
			0, "the whole answer\n", []string{"could not be reached", "Trying again"}, 2, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := apptest.NewWorld(t, tc.replies...)
			r := run(t, w, "-p", "hello").wantCode(t, tc.code)
			if r.out != tc.out {
				t.Errorf("stdout = %q, want %q", r.out, tc.out)
			}
			has(t, "stderr", r.err, tc.stderr...)
			if n := len(w.Model.Requests()); tc.requests != 0 && n != tc.requests {
				t.Errorf("the model was sent %d requests, want %d", n, tc.requests)
			}
			if tc.reason != "" {
				// The same failure in the json format: a result object that
				// says so, and the same exit code.
				w := apptest.NewWorld(t, tc.replies...)
				r := run(t, w, "-p", "--output-format", "json", "hello").wantCode(t, tc.code)
				obj := jsonLines(t, r.out)[0]
				if obj["subtype"] != "error_during_execution" || obj["is_error"] != true || obj["terminal_reason"] != tc.reason {
					t.Errorf("result: %s", clip(r.out))
				}
				if errs, _ := obj["errors"].([]any); len(errs) == 0 {
					t.Errorf("the result names no error: %s", clip(r.out))
				}
			}
		})
	}
	t.Run("the server is not running", func(t *testing.T) {
		w := apptest.NewWorld(t)
		w.Settings(`{"providers": {"gone": {"type": "llamacpp", "baseUrl": "http://127.0.0.1:1"}}, "models": {"gone/m": {"contextWindow": 50000}}, "model": "gone/m"}`)
		r := run(t, w, "-p", "hello").wantCode(t, 1)
		if r.out != "" {
			t.Errorf("stdout = %q", r.out)
		}
		has(t, "stderr", r.err, "gone (m) at http://127.0.0.1:1", "is not running", "Start it")
	})
}
