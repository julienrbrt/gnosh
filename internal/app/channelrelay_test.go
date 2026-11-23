package app

import (
	"encoding/json"
	"regexp"
	"strings"
	"testing"

	"github.com/gnosh-ai/gnosh/internal/app/apptest"
)

// relayChannel is a channel in sh that relays permission questions: it
// declares claude/channel/permission besides claude/channel, writes each
// question it is sent to the file record, and answers it at once with the
// verdict behavior.
func relayChannel(record, behavior string) string {
	return `while IFS= read -r line; do
  id=$(printf '%s' "$line" | sed -n 's/.*"id":\([0-9][0-9]*\).*/\1/p')
  case "$line" in
    *'"method":"notifications/claude/channel/permission_request"'*)
      printf '%s\n' "$line" >> '` + record + `'
      rid=$(printf '%s' "$line" | sed -n 's/.*"request_id":"\([a-z]*\)".*/\1/p')
      printf '{"jsonrpc":"2.0","method":"notifications/claude/channel/permission","params":{"request_id":"%s","behavior":"` + behavior + `"}}\n' "$rid" ;;
    *'"method":"initialize"'*)
      printf '{"jsonrpc":"2.0","id":%s,"result":{"protocolVersion":"2025-06-18","serverInfo":{"name":"phone","version":"0.1"},"capabilities":{"tools":{},"experimental":{"claude/channel":{},"claude/channel/permission":{}}}}}\n' "$id" ;;
    *'"method":"tools/list"'*)
      printf '{"jsonrpc":"2.0","id":%s,"result":{"tools":[]}}\n' "$id" ;;
  esac
done`
}

// relayedRequest reads the one question a relaying channel was sent.
func relayedRequest(t *testing.T, w *apptest.World, record string) map[string]string {
	t.Helper()
	lines := strings.Split(strings.TrimSpace(w.Read(record)), "\n")
	if len(lines) != 1 {
		t.Fatalf("the channel was sent %d questions, want 1:\n%s", len(lines), strings.Join(lines, "\n"))
	}
	var m struct {
		Params map[string]string `json:"params"`
	}
	if err := json.Unmarshal([]byte(lines[0]), &m); err != nil {
		t.Fatalf("the question the channel was sent cannot be read: %v\n%s", err, lines[0])
	}
	return m.Params
}

// A channel that relays permission questions is sent the question about a
// call as it opens, with an id of five letters, the tool, what the call
// does and its input (a credential in it masked), and its "allow" answers
// the question that waits at the terminal: the call runs.
func TestChannelRelayAllows(t *testing.T) {
	w := apptest.NewWorld(t,
		apptest.Reply{Calls: []apptest.Call{{ID: "c1", Name: "Write", Args: `{"file_path":"out.txt","content":"key sk-live9abc12345678\n"}`}}},
		apptest.Reply{Text: "Done."},
	)
	record := w.Home + "/relayed.jsonl"
	server := w.Script(w.Home+"/bin/phone.sh", relayChannel(record, "allow"))
	w.Write(w.ConfigDir()+"/mcp.json", `{"mcpServers": {"phone": {"command": "`+server+`"}}}`)
	run(t, w, "trust").wantCode(t, 0) // (the person's own MCP servers wait for trust)
	s := sitWith(t, w, nil, "--channels", "server:phone")
	s.see("The channel phone takes permission questions too")
	s.typeLine("write it")
	s.see("(the channel phone answered the question: yes.)")
	s.see("Done.")
	s.typeLine("/exit")
	s.exit()
	if got := w.Read("out.txt"); !strings.Contains(got, "sk-live9abc12345678") {
		t.Errorf("the file the call was allowed to write holds %q", got)
	}
	p := relayedRequest(t, w, record)
	if !regexp.MustCompile(`^[a-km-z]{5}$`).MatchString(p["request_id"]) || p["tool_name"] != "Write" || p["description"] == "" {
		t.Errorf("the question the channel was sent: %q", p)
	}
	preview := p["input_preview"]
	has(t, "the input it was shown", preview, `"file_path":"out.txt"`, `"content":"key [REDACTED]\n"`)
	if strings.Contains(preview, "sk-live") {
		t.Errorf("a credential went to the channel: %s", preview)
	}
}

// A "deny" from the channel refuses the call, as a no at the terminal
// does: it does not run, and the turn stops.
func TestChannelRelayDenies(t *testing.T) {
	w := apptest.NewWorld(t,
		apptest.Reply{Calls: []apptest.Call{{ID: "c1", Name: "Write", Args: `{"file_path":"out.txt","content":"hi\n"}`}}},
	)
	record := w.Home + "/relayed.jsonl"
	server := w.Script(w.Home+"/bin/phone.sh", relayChannel(record, "deny"))
	w.Write(w.ConfigDir()+"/mcp.json", `{"mcpServers": {"phone": {"command": "`+server+`"}}}`)
	run(t, w, "trust").wantCode(t, 0) // (the person's own MCP servers wait for trust)
	s := sitWith(t, w, nil, "--channels", "server:phone")
	s.see("The channel phone takes permission questions too")
	s.typeLine("write it")
	s.see("(the channel phone answered the question: no.)")
	s.typeLine("/exit")
	s.exit()
	if w.Exists("out.txt") {
		t.Errorf("a call the channel refused ran")
	}
	_ = relayedRequest(t, w, record)
}

// What a channel is sent is made fit for another screen: no characters
// that turn text around or cannot be seen, lookalikes written plainly,
// blanks folded, the middle of a long value left out with a count.
func TestRelayText(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"rm -rf  build\n\tnow", "rm -rf build now"},
		{"pay \u202Etxt.exe\u202C", "pay txt.exe"},
		{"a\u200Bb\uFEFFc\u2060d", "abcd"},
		{"\u201Cquoted\u201D \u2039x\u203A \uFF1Cy\uFF1E", `"quoted" <x> <y>`},
		{"bell\x07 esc\x1b[2J", "bell esc [2J"},
	} {
		if got := relayText(c.in); got != c.want {
			t.Errorf("relayText(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	long := strings.Repeat("a", 2000) + strings.Repeat("b", 2000) + "END"
	got := relayText(long)
	if !strings.HasPrefix(got, strings.Repeat("a", 1750)) || !strings.HasSuffix(got, "bEND") || !strings.Contains(got, " ⋯ 503 code points elided ⋯ ") {
		t.Errorf("a long value: %q…%q", got[:40], got[len(got)-40:])
	}
}

// Tokens services hand out as credentials are masked; a span with a
// path's, a URL's or a shell's characters in it never is, nor a word that
// only begins the same.
func TestMaskCredentials(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"export GH=ghp_abcdefgh1234 && go", "export GH=[REDACTED] && go"},
		{"curl -H 'Authorization: Bearer sk-proj9abcdefgh'", "curl -H 'Authorization: Bearer [REDACTED]'"},
		{"AKIAIOSFODNN7EXAMPLE", "[REDACTED]"},
		{"ghp_short1", "ghp_short1"},
		{"sk-learn and task-list-12345678", "sk-learn and task-list-12345678"},
		{"cat ~/keys/ghp_abcdefgh1234/x", "cat ~/keys/ghp_abcdefgh1234/x"},
		{"https://example.com/ghp_abcdefgh1234.txt", "https://example.com/ghp_abcdefgh1234.txt"},
		{"git clone https://x:ghp_abcdefgh1234@github.com/o/r", "git clone https://x:[REDACTED]@github.com/o/r"},
		{"curl 'https://example.com/v1?key=AIzaSyA1234567890abcdef&alt=json'", "curl 'https://example.com/v1?key=[REDACTED]&alt=json'"},
	} {
		if got := maskCredentials(c.in); got != c.want {
			t.Errorf("maskCredentials(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	// A character that cannot be seen, put inside a credential, does not
	// hide it.
	if got := relayText("export T=ghp_abcd\u200befgh1234"); got != "export T=[REDACTED]" {
		t.Errorf("a credential with a zero-width space in it: %q", got)
	}
}

// A value's line ends stay in the preview, written as JSON writes them,
// so a command of two lines does not read as one.
func TestRelayPreviewKeepsLines(t *testing.T) {
	got := relayPreview(json.RawMessage(`{"command":"echo ok\nrm -rf ~/src","description":"say ok"}`))
	if want := `{"command":"echo ok\nrm -rf ~/src","description":"say ok"}`; got != want {
		t.Errorf("relayPreview = %s, want %s", got, want)
	}
}

// A request id is five letters without l, never issued twice; a verdict
// is taken only for an open question, and only from a channel it went to.
func TestRelayBook(t *testing.T) {
	var b relayBook
	id := regexp.MustCompile(`^[a-km-z]{5}$`)
	seen := map[string]bool{}
	for i := range 3000 {
		got := b.issue(&relayed{question: "q" + string(rune('a'+i%26)) + strings.Repeat("x", i/26), to: map[string]bool{"phone": true}})
		if !id.MatchString(got) || seen[got] {
			t.Fatalf("id %d: %q (seen before: %v)", i, got, seen[got])
		}
		seen[got] = true
	}
	r := &relayed{question: "q-last", to: map[string]bool{"phone": true}}
	last := b.issue(r)
	if _, ok := b.take(last, "other"); ok {
		t.Errorf("a verdict from a channel the question did not go to was taken")
	}
	if got, ok := b.take(last, "phone"); !ok || got != r {
		t.Errorf("the verdict for an open question was not taken")
	}
	if _, ok := b.take(last, "phone"); ok {
		t.Errorf("a second verdict for the same question was taken")
	}
	r2 := &relayed{question: "q-closed", to: map[string]bool{"phone": true}}
	closed := b.issue(r2)
	b.close("q-closed")
	if _, ok := b.take(closed, "phone"); ok {
		t.Errorf("a verdict for a question answered at the terminal was taken")
	}
}

// A question only the person may answer, about a call that could destroy
// what has to be kept, is never sent to a channel: it waits at the
// terminal alone.
func TestChannelRelayLeavesPersonOnlyQuestions(t *testing.T) {
	w := apptest.NewWorld(t,
		apptest.Reply{Calls: []apptest.Call{{ID: "c1", Name: "Bash", Args: `{"command":"rm -rf ~"}`}}},
	)
	record := w.Home + "/relayed.jsonl"
	server := w.Script(w.Home+"/bin/phone.sh", relayChannel(record, "allow"))
	w.Write(w.ConfigDir()+"/mcp.json", `{"mcpServers": {"phone": {"command": "`+server+`"}}}`)
	run(t, w, "trust").wantCode(t, 0) // (the person's own MCP servers wait for trust)
	s := sitWith(t, w, nil, "--channels", "server:phone")
	s.see("The channel phone takes permission questions too")
	s.typeLine("clean up")
	s.see("? Allow Bash?")
	s.typeLine("no")
	s.see("-- ended")
	s.typeLine("/exit")
	s.exit()
	if w.Exists(record) {
		t.Errorf("the channel was sent a question only the person may answer:\n%s", w.Read(record))
	}
	if strings.Contains(s.out.String(), "answered the question") {
		t.Errorf("a channel answered a question only the person may answer:\n%s", s.out.String())
	}
}
