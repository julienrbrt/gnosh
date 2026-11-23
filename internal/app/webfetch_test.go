package app

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/gnosh-ai/gnosh/internal/app/apptest"
	"github.com/gnosh-ai/gnosh/internal/tools/webtool"
)

// WebFetch: the page is read by a side request with the model's request,
// and the model reads the answer; a page is kept for the next call; a long
// page says how to read on; what is not on the open web is refused before
// anybody is asked.
func TestWebFetch(t *testing.T) {
	var fetched atomic.Int32
	long := strings.Repeat("filler text. ", 9000) + "THE END"
	old := fetchPage
	fetchPage = func(ctx context.Context, raw string, o webtool.Options) (webtool.Page, error) {
		fetched.Add(1)
		if !strings.HasPrefix(o.UserAgent, "gnosh/") {
			t.Errorf("the user agent is %q", o.UserAgent)
		}
		text := "# The docs\n\nThe answer is 42, and the command is `make it`.\n"
		if strings.Contains(raw, "long") {
			text = long
		}
		return webtool.Page{URL: raw, Code: 200, Status: "OK", ContentType: "text/html", Bytes: len(text), Text: text}, nil
	}
	defer func() { fetchPage = old }()

	var sidePrompts []string
	main := 0
	w := apptest.NewWorld(t)
	w.Model.Respond(func(n int, r apptest.Request) *apptest.Reply {
		body := string(r.Body)
		if strings.Contains(body, "<page>") {
			sidePrompts = append(sidePrompts, body)
			if strings.Contains(body, "THE END") || strings.Contains(body, "filler") {
				return &apptest.Reply{Text: "It is filler."}
			}
			return &apptest.Reply{Text: "The answer is 42; the command is `make it`."}
		}
		main++
		switch main {
		case 1:
			return &apptest.Reply{Calls: []apptest.Call{{ID: "c1", Name: "WebFetch", Args: `{"url":"http://docs.example.org/page","prompt":"what is the answer?"}`}}}
		case 2:
			return &apptest.Reply{Calls: []apptest.Call{{ID: "c2", Name: "WebFetch", Args: `{"url":"https://docs.example.org/page","prompt":"and the command?"}`}}}
		case 3:
			return &apptest.Reply{Calls: []apptest.Call{{ID: "c3", Name: "WebFetch", Args: `{"url":"https://docs.example.org/long","prompt":"summarize"}`}}}
		case 4:
			return &apptest.Reply{Calls: []apptest.Call{{ID: "c4", Name: "WebFetch", Args: `{"url":"http://localhost:8080/admin","prompt":"look"}`}}}
		}
		return &apptest.Reply{Text: "Done."}
	})
	run(t, w, "-y", "-p", "look up the docs").wantCode(t, 0)

	first := toolResult(t, w, 2, "c1")
	has(t, "the first fetch's result", first, "The answer is 42")
	if len(sidePrompts) < 1 || !strings.Contains(sidePrompts[0], "what is the answer?") || !strings.Contains(sidePrompts[0], "The docs") || !strings.Contains(sidePrompts[0], "https://docs.example.org/page") {
		t.Fatalf("the side request: %v", sidePrompts)
	}
	// The same page again, from the cache: fetched once for both calls,
	// and http was asked for as https.
	has(t, "the second fetch's result", toolResult(t, w, 4, "c2"), "make it")
	// The long page: what is left, and the offset that reads on.
	has(t, "the long page's result", toolResult(t, w, 6, "c3"), "It is filler.", "characters were read", "WebFetch with offset 100000 reads on")
	if n := fetched.Load(); n != 2 {
		t.Errorf("the pages were fetched %d times, want 2 (the first page once, from the cache the second time, and the long one)", n)
	}
	// localhost: refused before anything is fetched or anybody is asked.
	has(t, "the fetch from localhost", toolResult(t, w, 7, "c4"), "use curl through the shell")
}

// What is not text is kept in a file of the session's, named for what it
// is, and the model is told where, without the smaller model: a picture to
// Read, a PDF to turn into text. Asked again within the 15 minutes, it is
// the same file, not fetched again; one too long to keep whole is not
// kept at all.
func TestWebFetchKeepsWhatIsNotText(t *testing.T) {
	pdf := []byte("%PDF-1.7\n%%EOF\n")
	png := []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n', 0, 0}
	var fetched atomic.Int32
	old := fetchPage
	fetchPage = func(ctx context.Context, raw string, o webtool.Options) (webtool.Page, error) {
		fetched.Add(1)
		switch {
		case strings.HasSuffix(raw, "/paper"):
			return webtool.Page{URL: raw, Code: 200, Status: "OK", ContentType: "application/pdf", Bytes: len(pdf), Body: pdf}, nil
		case strings.HasSuffix(raw, "/huge"):
			return webtool.Page{URL: raw, Code: 200, Status: "OK", ContentType: "video/mp4", Bytes: 10 << 20, Cut: true, Body: make([]byte, 16)}, nil
		}
		return webtool.Page{URL: raw, Code: 200, Status: "OK", ContentType: "image/png", Bytes: len(png), Body: png}, nil
	}
	defer func() { fetchPage = old }()

	w := apptest.NewWorld(t)
	main := 0
	w.Model.Respond(func(n int, r apptest.Request) *apptest.Reply {
		if strings.Contains(string(r.Body), "<page>") {
			t.Errorf("a file that is not text went to the smaller model")
			return &apptest.Reply{Text: "?"}
		}
		main++
		switch main {
		case 1:
			return &apptest.Reply{Calls: []apptest.Call{{ID: "c1", Name: "WebFetch", Args: `{"url":"https://files.example.org/paper","prompt":"what does it say?"}`}}}
		case 2:
			return &apptest.Reply{Calls: []apptest.Call{{ID: "c2", Name: "WebFetch", Args: `{"url":"https://files.example.org/paper","prompt":"and the title?"}`}}}
		case 3:
			return &apptest.Reply{Calls: []apptest.Call{{ID: "c3", Name: "WebFetch", Args: `{"url":"https://files.example.org/logo","prompt":"describe it"}`}}}
		case 4:
			return &apptest.Reply{Calls: []apptest.Call{{ID: "c4", Name: "WebFetch", Args: `{"url":"https://files.example.org/huge","prompt":"watch it"}`}}}
		}
		return &apptest.Reply{Text: "Done."}
	})
	run(t, w, "-y", "-p", "fetch the files").wantCode(t, 0)

	first := toolResult(t, w, 4, "c1")
	has(t, "the PDF's result", first, "is a PDF (application/pdf", "Read that file for its text")
	saved := regexp.MustCompile(`saved to (\S+\.pdf)[.:]`).FindStringSubmatch(first)
	if saved == nil {
		t.Fatalf("no file is named in %q", first)
	}
	if got, err := os.ReadFile(saved[1]); err != nil || !bytes.Equal(got, pdf) {
		t.Errorf("the saved file holds %q, %v", got, err)
	}
	if info, err := os.Stat(saved[1]); err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("the saved file: %v, %v", info, err)
	}
	has(t, "the PDF asked for again", toolResult(t, w, 4, "c2"), "saved to "+saved[1]+":")
	has(t, "the picture's result", toolResult(t, w, 4, "c3"), "is a picture (image/png", ".png: Read that file to see it.")
	has(t, "the long video's result", toolResult(t, w, 4, "c4"), "longer than the 10.0 MB WebFetch keeps", "curl through Bash")
	if n := fetched.Load(); n != 3 {
		t.Errorf("the files were fetched %d times, want 3 (the PDF once)", n)
	}
}

// Claude Code's WebFetch variables: CLAUDE_CODE_DISABLE_WEB_FETCH=1 leaves
// the tool out; CLAUDE_CODE_WEBFETCH_CACHE_TTL_MS=0 keeps the default, as
// Claude Code's reference says, so the same page asked for twice is
// fetched once; CLAUDE_CODE_WEBFETCH_DEADLINE_MS
// is the download's bound, 0 none.
func TestWebFetchVariables(t *testing.T) {
	off := apptest.NewWorld(t, apptest.Reply{Text: "Fine."})
	o := testOptions(off, "-p", "hello")
	o.Environ = append(o.Environ, "CLAUDE_CODE_DISABLE_WEB_FETCH=1")
	runWith(t, t.Context(), o).wantCode(t, 0)
	if body := string(off.Model.Requests()[0].Body); strings.Contains(body, `"name":"WebFetch"`) {
		t.Error("WebFetch is offered with CLAUDE_CODE_DISABLE_WEB_FETCH=1")
	}
	// As Claude Code reads it: "true" as well as 1, and from the env block
	// of a settings file as well as from the shell.
	blocked := apptest.NewWorld(t, apptest.Reply{Text: "Fine."})
	blocked.Settings(`{"env": {"CLAUDE_CODE_DISABLE_WEB_FETCH": "True"}}`)
	runWith(t, t.Context(), testOptions(blocked, "-p", "hello")).wantCode(t, 0)
	if body := string(blocked.Model.Requests()[0].Body); strings.Contains(body, `"name":"WebFetch"`) {
		t.Error("WebFetch is offered with CLAUDE_CODE_DISABLE_WEB_FETCH=True in the settings' env block")
	}

	var fetched atomic.Int32
	var timeouts []string
	old := fetchPage
	fetchPage = func(ctx context.Context, raw string, o webtool.Options) (webtool.Page, error) {
		fetched.Add(1)
		timeouts = append(timeouts, o.Timeout.String())
		return webtool.Page{URL: raw, Code: 200, Status: "OK", ContentType: "text/plain", Bytes: 5, Text: "hello"}, nil
	}
	defer func() { fetchPage = old }()
	w := apptest.NewWorld(t)
	main := 0
	w.Model.Respond(func(n int, r apptest.Request) *apptest.Reply {
		if strings.Contains(string(r.Body), "<page>") {
			return &apptest.Reply{Text: "It says hello."}
		}
		main++
		if main <= 2 {
			return &apptest.Reply{Calls: []apptest.Call{{ID: fmt.Sprintf("c%d", main), Name: "WebFetch", Args: `{"url":"https://docs.example.org/page","prompt":"what?"}`}}}
		}
		return &apptest.Reply{Text: "Done."}
	})
	o = testOptions(w, "-y", "-p", "fetch it twice")
	o.Environ = append(o.Environ, "CLAUDE_CODE_WEBFETCH_CACHE_TTL_MS=0", "CLAUDE_CODE_WEBFETCH_DEADLINE_MS=0")
	runWith(t, t.Context(), o).wantCode(t, 0)
	if n := fetched.Load(); n != 1 {
		t.Errorf("with a cache time of 0 (the default kept) the page was fetched %d times, want 1", n)
	}
	if len(timeouts) == 0 || timeouts[0] != "-1ns" {
		t.Errorf("a deadline of 0 gave the fetch the timeouts %v, want no bound (-1ns)", timeouts)
	}
}
