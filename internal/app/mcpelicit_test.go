package app

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/gnosh-ai/gnosh/internal/app/apptest"
	"github.com/gnosh-ai/gnosh/internal/mcp"
)

// An elicitation's fields become the form's questions, and what comes back
// is held to each field's kind, bounds and format.
func TestElicitFormAndContent(t *testing.T) {
	one, nine := 1.0, 9.0
	fields := []mcp.ElicitField{
		{Name: "branch", Type: "string", Enum: []string{"main", "dev"}, EnumNames: []string{"Main", "Development"}, Required: true},
		{Name: "count", Title: "How many", Type: "integer", Minimum: &one, Maximum: &nine},
		{Name: "go", Type: "boolean", Required: true},
		{Name: "mail", Type: "string", Format: "email"},
	}
	form := elicitForm(mcp.Elicitation{Message: "Set it up", Fields: fields})
	if len(form.Questions) != 4 || len(form.Questions[0].Options) != 2 || form.Questions[0].Options[1].Label != "Development" ||
		len(form.Questions[2].Options) != 2 || len(form.Questions[1].Options) != 0 {
		t.Fatalf("the form: %+v", form)
	}
	if q := form.Questions[1].Question; !strings.Contains(q, "How many") || !strings.Contains(q, "a whole number") || !strings.Contains(q, "at most 9") || !strings.Contains(q, "may be left empty") {
		t.Errorf("the integer's question: %q", q)
	}
	answer := func(vals ...string) map[string]string {
		m := map[string]string{}
		for i, v := range vals {
			m[form.Questions[i].Question] = v
		}
		return m
	}
	got, why := elicitContent(fields, form, answer("Development", "3", "Yes", "jae@example.com"))
	if why != "" || !reflect.DeepEqual(got, map[string]any{"branch": "dev", "count": int64(3), "go": true, "mail": "jae@example.com"}) {
		t.Errorf("good answers: %v, %q", got, why)
	}
	for _, c := range []struct {
		vals []string
		want string
	}{
		{[]string{"", "3", "Yes", ""}, "branch is needed"},
		{[]string{"main", "3.5", "Yes", ""}, "a whole number"},
		{[]string{"main", "12", "Yes", ""}, "out of its bounds"},
		{[]string{"staging", "", "Yes", ""}, "one of main, dev"},
		{[]string{"main", "", "maybe", ""}, "Yes or No"},
		{[]string{"main", "", "No", "not-an-address"}, "is an email address"},
		{[]string{"main", "NaN", "Yes", ""}, "a whole number"},
	} {
		if _, why := elicitContent(fields, form, answer(c.vals...)); !strings.Contains(why, c.want) {
			t.Errorf("%q: %q, want %q", c.vals, why, c.want)
		}
	}
	// Two fields that read alike are told apart.
	twins := elicitForm(mcp.Elicitation{Fields: []mcp.ElicitField{{Name: "first", Title: "Name", Type: "string", Required: true}, {Name: "last", Title: "Name", Type: "string", Required: true}}})
	if a, b := twins.Questions[0].Question, twins.Questions[1].Question; a == b || !strings.HasSuffix(b, "[last]") {
		t.Errorf("fields that read alike: %q and %q", a, b)
	}
	// A request with no fields asks whether to go on.
	empty := elicitForm(mcp.Elicitation{Message: "Go?"})
	if _, why := elicitContent(nil, empty, map[string]string{"Go on?": "Yes"}); why != "" {
		t.Errorf("a confirmation: %q", why)
	}
}

// askingMCP is an MCP server at a URL whose one tool, deploy, asks the
// person where to deploy (elicitation/create) in the middle of the call,
// and says what it was told. hellos gets the handshake of each connection.
func askingMCP(t *testing.T, hellos chan<- string) *httptest.Server {
	answers := make(chan string, 4)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var m struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Result json.RawMessage `json:"result"`
			Error  json.RawMessage `json:"error"`
		}
		_ = json.Unmarshal(body, &m)
		switch m.Method {
		case "initialize":
			select {
			case hellos <- string(body):
			default:
			}
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%s,"result":{"protocolVersion":"2025-06-18","serverInfo":{"name":"asker","version":"1"},"capabilities":{"tools":{}}}}`, m.ID)
		case "tools/list":
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%s,"result":{"tools":[{"name":"deploy","description":"Deploys, after asking where."}]}}`, m.ID)
		case "tools/call":
			w.Header().Set("Content-Type", "text/event-stream")
			fl := w.(http.Flusher)
			fmt.Fprint(w, `data: {"jsonrpc":"2.0","id":"ask-1","method":"elicitation/create","params":{"message":"Where should it go?","requestedSchema":{"type":"object","properties":{`+
				`"region":{"type":"string","title":"Region","description":"the data center","enum":["us-east","eu-west"],"enumNames":["US East","EU West"]},`+
				`"sure":{"type":"boolean","title":"Sure"}},"required":["region","sure"]}}}`+"\n\n")
			fl.Flush()
			var got string
			select {
			case got = <-answers:
			case <-time.After(20 * time.Second):
				got = "no answer"
			case <-r.Context().Done():
				return
			}
			text, _ := json.Marshal("told: " + got)
			fmt.Fprintf(w, `data: {"jsonrpc":"2.0","id":%s,"result":{"content":[{"type":"text","text":%s}]}}`+"\n\n", m.ID, text)
			fl.Flush()
		case "":
			if string(m.ID) == `"ask-1"` {
				answers <- string(m.Result) + string(m.Error)
			}
			w.WriteHeader(http.StatusAccepted)
		default:
			w.WriteHeader(http.StatusAccepted)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// An MCP server that asks for something in the middle of a call puts its
// form to the person, under its message, and gets what they answered; in
// a run, with nobody to ask, the client declares no elicitation and the
// request is refused.
func TestMCPElicitationInThePlainSession(t *testing.T) {
	call := apptest.Reply{Calls: []apptest.Call{{ID: "c1", Name: "mcp__asker__deploy", Args: `{}`}}}
	w := apptest.NewWorld(t, call, apptest.Reply{Text: "Deployed."}, call, apptest.Reply{Text: "Could not ask."})
	hellos := make(chan string, 4)
	srv := askingMCP(t, hellos)
	w.Write(w.ConfigDir()+"/mcp.json", `{"mcpServers": {"asker": {"type": "http", "url": "`+srv.URL+`"}}}`)

	s := sit(t, w, "--dangerously-skip-permissions")
	s.see("Type a prompt and Enter.")
	s.mcpConnected("asker")
	s.typeLine("deploy it")
	s.see("? The MCP server asker asks: Where should it go?")
	s.see("Question 1 of 2 [region]: Region: the data center")
	s.see("  2. EU West")
	s.typeLine("2")
	s.see("Question 2 of 2 [sure]: Sure")
	s.typeLine("1")
	s.see("Deployed.")
	has(t, "what the model read", toolResult(t, w, 1, "c1"), `told: {"action":"accept","content":{"region":"eu-west","sure":true}}`)
	s.typeLine("/exit")
	if code := s.exit(); code != 0 {
		t.Errorf("exit code %d", code)
	}
	if hello := <-hellos; !strings.Contains(hello, `"elicitation":{}`) {
		t.Errorf("the plain session did not declare elicitation: %s", hello)
	}

	run(t, w, "-y", "-p", "deploy it").wantCode(t, 0)
	has(t, "what the model read in a run", toolResult(t, w, 3, "c1"), "nobody is there to ask")
	if hello := <-hellos; strings.Contains(hello, "elicitation") {
		t.Errorf("a run declared elicitation: %s", hello)
	}
}

// An Elicitation hook may answer for the person, in a run too, which then
// declares the capability; its values are held to the fields; and an
// ElicitationResult hook may put other values in place of the person's.
func TestMCPElicitationHooks(t *testing.T) {
	call := apptest.Reply{Calls: []apptest.Call{{ID: "c1", Name: "mcp__asker__deploy", Args: `{}`}}}
	w := apptest.NewWorld(t, call, apptest.Reply{Text: "Deployed."}, call, apptest.Reply{Text: "Refused."}, call, apptest.Reply{Text: "Deployed again."},
		call, apptest.Reply{Text: "Deployed a third time."})
	hellos := make(chan string, 8)
	srv := askingMCP(t, hellos)
	w.Write(w.ConfigDir()+"/mcp.json", `{"mcpServers": {"asker": {"type": "http", "url": "`+srv.URL+`"}}}`)
	// (One hook, whose script each part writes anew.)
	hook := w.Home + "/hooks/elicit.sh"
	w.Script(hook, `cat > /dev/null
echo '{"hookSpecificOutput": {"hookEventName": "Elicitation", "action": "accept", "content": {"region": "us-east", "sure": true}}}'`)
	w.Settings(`{"hooks": {"Elicitation": [{"matcher": "asker", "hooks": [{"type": "command", "command": "` + hook + `"}]}]}}`)
	run(t, w, "-y", "-p", "deploy it").wantCode(t, 0)
	has(t, "what the model read", toolResult(t, w, 1, "c1"), `told: {"action":"accept","content":{"region":"us-east","sure":true}}`)
	if hello := <-hellos; !strings.Contains(hello, `"elicitation":{}`) {
		t.Errorf("a run with an Elicitation hook did not declare elicitation: %s", hello)
	}

	// Values that do not fit the fields decline.
	w.Script(hook, `cat > /dev/null
echo '{"hookSpecificOutput": {"hookEventName": "Elicitation", "action": "accept", "content": {"region": "mars", "sure": true}}}'`)
	run(t, w, "-y", "-p", "deploy it").wantCode(t, 0)
	has(t, "what the model read of a hook's wrong values", toolResult(t, w, 3, "c1"), `told: {"action":"decline"}`)
	<-hellos

	// The Elicitation hook leaves it to the person, who answers; an
	// ElicitationResult hook changes the region.
	w.Script(hook, `cat > /dev/null`)
	change := w.Script(w.Home+"/hooks/change.sh", `cat > /dev/null
echo '{"hookSpecificOutput": {"hookEventName": "ElicitationResult", "action": "accept", "content": {"region": "us-east", "sure": true}}}'`)
	w.Settings(`{"hooks": {"ElicitationResult": [{"hooks": [{"type": "command", "command": "` + change + `"}]}]}}`)
	s := sit(t, w, "--dangerously-skip-permissions")
	s.see("Type a prompt and Enter.")
	s.mcpConnected("asker")
	s.typeLine("deploy it")
	s.see("? The MCP server asker asks: Where should it go?")
	s.typeLine("2")
	s.see("Question 2 of 2 [sure]")
	s.typeLine("1")
	s.see("Deployed again.")
	has(t, "what the model read after the change", toolResult(t, w, 5, "c1"), `told: {"action":"accept","content":{"region":"us-east","sure":true}}`)

	// An ElicitationResult hook's accept with no values keeps the person's.
	w.Script(change, `cat > /dev/null
echo '{"hookSpecificOutput": {"hookEventName": "ElicitationResult", "action": "accept"}}'`)
	s.typeLine("deploy it")
	s.see("Question 1 of 2 [region]")
	s.typeLine("2")
	s.see("Question 2 of 2 [sure]")
	s.typeLine("1")
	s.see("Deployed a third time.")
	has(t, "what the model read after an accept with no values", toolResult(t, w, 7, "c2"), `told: {"action":"accept","content":{"region":"eu-west","sure":true}}`)
	s.typeLine("/exit")
	s.exit()
}

// A question that takes only its options (a server's list of values, a
// Yes or No) takes a number or an option's label in the plain session,
// and nothing else.
func TestFormAnswerWithoutOwnWords(t *testing.T) {
	q := FormQuestion{Question: "Sure", Options: []FormOption{{Label: "Yes"}, {Label: "No"}}, NoOther: true}
	for line, want := range map[string]string{"1": "Yes", "2": "No", "no": "No", " YES ": "Yes"} {
		if got, ok := formAnswer(q, line); !ok || got != want {
			t.Errorf("%q: %q, %v; want %q", line, got, ok, want)
		}
	}
	for _, line := range []string{"maybe", "3", "1, 2"} {
		if got, ok := formAnswer(q, line); ok {
			t.Errorf("%q was taken as %q", line, got)
		}
	}
	if form := elicitForm(mcp.Elicitation{Fields: []mcp.ElicitField{{Name: "n", Type: "string"}, {Name: "b", Type: "boolean"}, {Name: "e", Type: "string", Enum: []string{"a"}}}}); form.Questions[0].NoOther || !form.Questions[1].NoOther || !form.Questions[2].NoOther {
		t.Errorf("which questions take own words: %+v", form.Questions)
	}
	// A question answered by typing takes the line as it is, a number
	// too, and an empty line leaves it unanswered.
	typed := FormQuestion{Question: "replicas (a whole number)"}
	for line, want := range map[string]string{"3": "3", "  42 ": "42", "": "", "3 or 4": "3 or 4"} {
		if got, ok := formAnswer(typed, line); !ok || got != want {
			t.Errorf("typed %q: %q, %v; want %q", line, got, ok, want)
		}
	}
}
