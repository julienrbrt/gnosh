package app

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gnosh-ai/gnosh/internal/app/apptest"
	"github.com/gnosh-ai/gnosh/internal/sandbox"
)

// TestMain lets the test binary play a language server (fakeLSP) when a
// test starts it as one.
func TestMain(m *testing.M) {
	if os.Getenv("APPTEST_FAKE_LSP") == "1" {
		fakeLSP(os.Stdin, os.Stdout)
		os.Exit(0)
	}
	if len(os.Args) > 1 && os.Args[1] == sandbox.RelayWord {
		// (The sandbox's relay is the program itself, as gnosh's main has
		// it: here the test binary.)
		os.Exit(sandbox.RelayMain(os.Args[2:]))
	}
	if len(os.Args) > 1 && os.Args[1] == sandbox.FilterWord {
		os.Exit(sandbox.FilterMain(os.Args[2:]))
	}
	os.Exit(m.Run())
}

// fakeLSP is a made-up language server: it publishes an error for each
// line that calls undefinedThing, says every symbol is defined at the
// start of the file it is asked about, and answers hover with a type.
func fakeLSP(in io.Reader, out io.Writer) {
	r := bufio.NewReader(in)
	write := func(v any) {
		body, _ := json.Marshal(v)
		fmt.Fprintf(out, "Content-Length: %d\r\n\r\n%s", len(body), body)
	}
	if mark := os.Getenv("APPTEST_FAKE_LSP_MARK"); mark != "" {
		_ = os.WriteFile(mark, []byte("started\n"), 0o600)
	}
	for {
		length := -1
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				return
			}
			line = strings.TrimSpace(line)
			if line == "" {
				break
			}
			if v, ok := strings.CutPrefix(line, "Content-Length:"); ok {
				length, _ = strconv.Atoi(strings.TrimSpace(v))
			}
		}
		body := make([]byte, length)
		if _, err := io.ReadFull(r, body); err != nil {
			return
		}
		var m struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params struct {
				TextDocument struct {
					URI  string `json:"uri"`
					Text string `json:"text"`
				} `json:"textDocument"`
				ContentChanges []struct {
					Text string `json:"text"`
				} `json:"contentChanges"`
			} `json:"params"`
		}
		_ = json.Unmarshal(body, &m)
		reply := func(result any) { write(map[string]any{"jsonrpc": "2.0", "id": m.ID, "result": result}) }
		switch m.Method {
		case "initialize":
			reply(map[string]any{"capabilities": map[string]any{
				"textDocumentSync": map[string]any{"openClose": true, "change": 1}, "definitionProvider": true, "hoverProvider": true}})
		case "shutdown":
			reply(nil)
		case "exit":
			return
		case "textDocument/didOpen", "textDocument/didChange":
			text := m.Params.TextDocument.Text
			if n := len(m.Params.ContentChanges); n > 0 {
				text = m.Params.ContentChanges[n-1].Text
			}
			diags := []map[string]any{}
			for i, line := range strings.Split(text, "\n") {
				if col := strings.Index(line, "undefinedThing"); col >= 0 {
					diags = append(diags, map[string]any{"severity": 1, "source": "compiler", "message": "undefined: undefinedThing",
						"range": map[string]any{"start": map[string]int{"line": i, "character": col}, "end": map[string]int{"line": i, "character": col + 14}}})
				}
			}
			write(map[string]any{"jsonrpc": "2.0", "method": "textDocument/publishDiagnostics",
				"params": map[string]any{"uri": m.Params.TextDocument.URI, "diagnostics": diags}})
		case "textDocument/definition":
			uri := m.Params.TextDocument.URI
			if target := os.Getenv("APPTEST_FAKE_LSP_TARGET"); target != "" {
				uri = "file://" + target
			}
			reply(map[string]any{"uri": uri, "range": map[string]any{"start": map[string]int{"line": 0, "character": 0}, "end": map[string]int{"line": 0, "character": 7}}})
		case "textDocument/hover":
			reply(map[string]any{"contents": map[string]string{"kind": "plaintext", "value": "func main()"}})
		default:
			if len(m.ID) > 0 {
				reply(nil)
			}
		}
	}
}

// With a language server in lspServers: a read file is opened in it, an
// edit gives the model the new problems the server reports (and the person
// a line), and the LSP tool asks it where a symbol is defined and what it
// is.
func TestLanguageServer(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	w := apptest.NewWorld(t)
	w.Write("main.go", "package main\n\nfunc main() {}\n")
	cfg, _ := json.Marshal(map[string]any{"lspServers": map[string]any{"fake": map[string]any{
		"command": exe, "env": map[string]string{"APPTEST_FAKE_LSP": "1"}, "extensionToLanguage": map[string]string{".go": "go"}}}})
	w.Settings(string(cfg))
	w.Model.Respond(func(n int, r apptest.Request) *apptest.Reply {
		switch n {
		case 0:
			return &apptest.Reply{Calls: []apptest.Call{{ID: "c1", Name: "Read", Args: `{"file_path":"main.go"}`}}}
		case 1:
			return &apptest.Reply{Calls: []apptest.Call{{ID: "c2", Name: "Edit", Args: `{"file_path":"main.go","old_string":"func main() {}","new_string":"func main() { undefinedThing() }"}`}}}
		case 2:
			return &apptest.Reply{Calls: []apptest.Call{
				{ID: "c3", Name: "LSP", Args: `{"operation":"goToDefinition","filePath":"main.go","line":3,"character":16}`},
				{ID: "c4", Name: "LSP", Args: `{"operation":"hover","filePath":"main.go","line":3,"character":6}`},
			}}
		}
		return &apptest.Reply{Text: "Done."}
	})
	r := run(t, w, "--trust-folder", "-y", "--verbose", "-p", "fix it")
	r.wantCode(t, 0)
	body := string(w.Model.Requests()[2].Body)
	// The person is told how many new problems the edit brought, under
	// the edit's own line; the model is not (it has the problems).
	edit, note := strings.Index(r.err, "Edit: main.go"), strings.Index(r.err, "Found 1 new diagnostic issue in 1 file (fake).")
	if edit < 0 || note < edit {
		t.Errorf("the note is not under the edit's line:\n%s", r.err)
	}
	if strings.Contains(body, "new diagnostic issue in") {
		t.Error("the person's note went to the model")
	}
	has(t, "the hover's line", r.err, "LSP: hover main.go:3:6")
	if !strings.Contains(body, "main.go:3:15: error: undefined: undefinedThing (compiler)") {
		i := max(strings.Index(body, `"role":"tool"`), 0)
		t.Errorf("the request after the edit does not carry the new problem; gnosh said:\n%s\nthe request from its first tool result on:\n%s", r.err, clipText(body[i:], 4000))
	}
	has(t, "the definition", toolResult(t, w, 3, "c3"), "main.go:1:1: package main")
	has(t, "the hover", toolResult(t, w, 3, "c4"), "func main()")
	if !strings.Contains(string(w.Model.Requests()[0].Body), `"name":"LSP"`) {
		t.Error("the LSP tool is not offered")
	}
}

// Without lspServers there is no LSP tool, and nothing is started.
func TestNoLanguageServerNoTool(t *testing.T) {
	w := apptest.NewWorld(t, apptest.Reply{Text: "Fine."})
	run(t, w, "-p", "hello").wantCode(t, 0)
	if strings.Contains(string(w.Model.Requests()[0].Body), `"name":"LSP"`) {
		t.Error("the LSP tool is offered with no language server")
	}
}

// A server may name any file, and the line of one the Read deny rules keep
// from the model is not shown: the place is, by the server's count.
func TestLanguageServerKeepsToReadRules(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	w := apptest.NewWorld(t)
	w.Write("main.go", "package main\n\nfunc main() {}\n")
	secret := w.Write("secret.go", "const key = \"hunter2\"\n")
	cfg, _ := json.Marshal(map[string]any{"lspServers": map[string]any{"fake": map[string]any{
		"command": exe, "env": map[string]string{"APPTEST_FAKE_LSP": "1", "APPTEST_FAKE_LSP_TARGET": secret},
		"extensionToLanguage": map[string]string{".go": "go"}}}})
	w.Settings(string(cfg))
	w.Deny("Read(./secret.go)")
	w.Model.Respond(func(n int, r apptest.Request) *apptest.Reply {
		if n == 0 {
			return &apptest.Reply{Calls: []apptest.Call{{ID: "c1", Name: "LSP", Args: `{"operation":"goToDefinition","filePath":"main.go","line":3,"character":6}`},
				{ID: "c2", Name: "LSP", Args: `{"operation":"hover","filePath":"main.go","line":3,"character":6}`}}}
		}
		return &apptest.Reply{Text: "Done."}
	})
	run(t, w, "--trust-folder", "-y", "-p", "where").wantCode(t, 0)
	got := toolResult(t, w, 1, "c1")
	has(t, "the definition", got, "secret.go:1:1")
	if strings.Contains(got, "hunter2") {
		t.Errorf("a line the deny rule keeps from the model was shown: %s", got)
	}
	// A hover describes what is defined, here in the file the model may
	// not read (a constant's value, say): it is not shown.
	hover := toolResult(t, w, 1, "c2")
	has(t, "the hover", hover, "defined in secret.go", "is not shown")
	if strings.Contains(hover, "func main()") {
		t.Errorf("the hover of what a denied file defines was shown: %s", hover)
	}
}

// In a folder that is not trusted no language server starts, the person's
// own neither: a server reads the folder's code and may run some of it.
// The trust question shows a project's server's whole command line, as a
// program it would run (the language servers review of 2026-11-20).
func TestLanguageServersWaitForTrust(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	mark := filepath.Join(t.TempDir(), "started")
	w := apptest.NewWorld(t,
		apptest.Reply{Calls: []apptest.Call{{ID: "c1", Name: "Read", Args: `{"file_path":"main.go"}`}}},
		apptest.Reply{Text: "Read it."})
	w.Write("main.go", "package main\n")
	entry := map[string]any{"command": exe, "env": map[string]string{"APPTEST_FAKE_LSP": "1", "APPTEST_FAKE_LSP_MARK": mark},
		"extensionToLanguage": map[string]string{".go": "go"}}
	mine, _ := json.Marshal(map[string]any{"lspServers": map[string]any{"mine": entry}})
	w.Settings(string(mine))
	theirs, _ := json.Marshal(map[string]any{"lspServers": map[string]any{"theirs": entry}})
	w.Write("gnosh.json", string(theirs)+"\n")
	s := sit(t, w)
	s.see("? Trust this folder?")
	s.seeAnywhere("language server theirs for .go, run as ")
	s.seeAnywhere("your language server mine (")
	s.seeAnywhere("Some of these run commands")
	s.typeLine("1")
	s.see("Type a prompt and Enter.")
	s.typeLine("look")
	s.see("Read it.")
	s.seeAnywhere("The language servers wait for the folder to be trusted")
	s.typeLine("/exit")
	s.exit()
	if _, err := os.Stat(mark); err == nil {
		t.Error("a language server started in a folder that is not trusted")
	}
}

// A folder with nothing of its own is asked about all the same when the
// person has language servers, which would read its code: a No keeps them
// off, a Yes starts them.
func TestLanguageServersAskForANewFolder(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	mark := filepath.Join(t.TempDir(), "started")
	w := apptest.NewWorld(t,
		apptest.Reply{Calls: []apptest.Call{{ID: "c1", Name: "Read", Args: `{"file_path":"main.go"}`}}},
		apptest.Reply{Text: "Read it."})
	w.Write("main.go", "package main\n")
	mine, _ := json.Marshal(map[string]any{"lspServers": map[string]any{"mine": map[string]any{"command": exe,
		"env": map[string]string{"APPTEST_FAKE_LSP": "1", "APPTEST_FAKE_LSP_MARK": mark}, "extensionToLanguage": map[string]string{".go": "go"}}}})
	w.Settings(string(mine))
	s := sit(t, w)
	s.see("? Trust this folder?")
	s.seeAnywhere("your language server mine (")
	s.typeLine("2")
	s.see("Type a prompt and Enter.")
	s.typeLine("look")
	s.see("Read it.")
	s.typeLine("/exit")
	s.exit()
	for range 200 {
		if _, err := os.Stat(mark); err == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Error("the language server did not start after a Yes")
}
