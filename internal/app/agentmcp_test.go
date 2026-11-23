package app

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"syscall"
	"testing"

	"github.com/gnosh-ai/gnosh/internal/app/agentdef"
	"github.com/gnosh-ai/gnosh/internal/app/apptest"
)

// A definition's tools and disallowedTools hold MCP tools by name, by
// their server (mcp__github__*), and when taking away also as mcp__github
// and mcp__*; a server whose name holds a double underscore is named
// whole.
func TestToolListed(t *testing.T) {
	cases := []struct {
		list []string
		name string
		away bool
		want bool
	}{
		{[]string{"Read"}, "Read", false, true},
		{[]string{"mcp__github__*"}, "mcp__github__create_issue", false, true},
		{[]string{"mcp__github__*"}, "mcp__gitlab__create_issue", false, false},
		{[]string{"mcp__github"}, "mcp__github__create_issue", false, false},
		{[]string{"mcp__github"}, "mcp__github__create_issue", true, true},
		{[]string{"mcp__*"}, "mcp__github__create_issue", true, true},
		{[]string{"mcp__*"}, "Read", true, false},
		{[]string{"mcp__*"}, "mcp__github__create_issue", false, true},
		{[]string{"mcp__reports__v2__*"}, "mcp__reports__v2__run", false, true},
		{[]string{"mcp__reports"}, "mcp__reports__v2__run", true, true},
		{[]string{"mcp__reports__v2"}, "mcp__reports__v2__run", true, false},
		{[]string{"Read*"}, "ReadMore", false, false},
	}
	for _, c := range cases {
		if got := toolListed(c.list, c.name, c.away); got != c.want {
			t.Errorf("toolListed(%q, %q, away %v) = %v, want %v", c.list, c.name, c.away, got, c.want)
		}
	}
}

// A sub-agent whose definition writes an MCP server of its own has that
// server's tools, which the session's model is never offered; the server
// is started when the sub-agent starts and stopped when it ends. A name of
// a server the session lacks is said, and the sub-agent goes without it.
func TestSubAgentOwnMCPServer(t *testing.T) {
	w := apptest.NewWorld(t,
		apptest.Reply{Calls: []apptest.Call{{ID: "c1", Name: "Agent", Args: `{"description":"greet","prompt":"Greet Jae.","subagent_type":"greeter"}`}}},
		apptest.Reply{Calls: []apptest.Call{{ID: "c2", Name: "mcp__own__greet", Args: `{"name":"Jae"}`}}},
		apptest.Reply{Text: "Greeted."},
		apptest.Reply{Text: "Done."},
	)
	pids := w.Home + "/server-pids"
	server := w.Script(w.Home+"/bin/greeter.sh", "echo $$ >> "+pids+"\n"+fakeMCPServer)
	w.Write(w.ConfigDir()+"/agents/greeter.md", `---
name: greeter
description: Greets people.
mcpServers:
  - own:
      command: "`+server+`"
  - nosuch
---
You greet people with the greet tool.
`)
	run(t, w, "trust").wantCode(t, 0) // (the person's own MCP servers wait for trust)
	r := run(t, w, "-y", "-p", "greet Jae")
	r.wantCode(t, 0)
	reqs := w.Model.Requests()
	if len(reqs) != 4 {
		t.Fatalf("the model was asked %d times, want 4", len(reqs))
	}
	if body := string(reqs[0].Body); strings.Contains(body, "mcp__own__greet") {
		t.Errorf("the session's model was offered the sub-agent's own server's tool")
	}
	has(t, "the sub-agent's tools", string(reqs[1].Body), "mcp__own__greet", "Greets someone by name.")
	// (The sub-agent's conversation has its own calls: its first is c1.)
	has(t, "what the sub-agent read", toolResult(t, w, 2, "c1"), "hello, Jae")
	has(t, "what was said of the server the session lacks", r.out+r.err, `the server "nosuch"`, "goes without it")
	data, _ := os.ReadFile(pids)
	for _, pid := range strings.Fields(string(data)) {
		n := 0
		fmt.Sscan(pid, &n)
		if n > 0 && syscall.Kill(n, 0) == nil {
			t.Errorf("the sub-agent's server (pid %d) still runs after it ended", n)
		}
	}
	if len(strings.Fields(string(data))) != 1 {
		t.Errorf("the server was started %d times, want once", len(strings.Fields(string(data))))
	}
}

// The session that runs as an agent (--agent) has the agent's own servers
// from the start, beside the files', and /mcp shows them.
func TestAgentSessionOwnMCPServer(t *testing.T) {
	w := apptest.NewWorld(t,
		apptest.Reply{Calls: []apptest.Call{{ID: "c1", Name: "mcp__own__greet", Args: `{"name":"Jae"}`}}},
		apptest.Reply{Text: "Greeted."},
	)
	server := w.Script(w.Home+"/bin/greeter.sh", fakeMCPServer)
	w.Write(w.ConfigDir()+"/agents/greeter.md", `---
name: greeter
description: Greets people.
mcpServers:
  - own:
      command: "`+server+`"
---
You greet people with the greet tool.
`)
	run(t, w, "trust").wantCode(t, 0) // (the person's own MCP servers wait for trust)
	run(t, w, "--agent", "greeter", "-y", "-p", "greet Jae").wantCode(t, 0)
	has(t, "the tools offered", string(w.Model.Requests()[0].Body), "mcp__own__greet")
	has(t, "what the model read", toolResult(t, w, 1, "c1"), "hello, Jae")
	r := run(t, w, "--agent", "greeter", "-p", "/mcp").wantCode(t, 0)
	has(t, "/mcp", r.out, "own: connected", "agent, ")
}

// /status names the agent and skill files it found and does not use, and
// why.
func TestStatusNamesUnusedFiles(t *testing.T) {
	w := apptest.NewWorld(t)
	w.Write(w.ConfigDir()+"/agents/broken.md", "---\nname: broken\n---\nNo description.\n")
	r := run(t, w, "-p", "/status").wantCode(t, 0)
	has(t, "/status", r.out, "not used", "agents/broken.md: no kind of sub-agent")
}

// With --strict-mcp-config the servers an agent's file writes are left
// out, and that is said; the session that runs as an agent whose tools
// name one server's has that server's tools and no other's.
func TestAgentServersUnderStrictAndToolLists(t *testing.T) {
	w := apptest.NewWorld(t, apptest.Reply{Text: "ok"})
	server := w.Script(w.Home+"/bin/greeter.sh", fakeMCPServer)
	w.Write(w.ConfigDir()+"/agents/greeter.md", `---
name: greeter
description: Greets people.
tools: Read, mcp__own__*
mcpServers:
  - own:
      command: "`+server+`"
---
You greet people.
`)
	w.Write(w.ConfigDir()+"/mcp.json", `{"mcpServers": {"other": {"command": "`+server+`"}}}`)
	run(t, w, "trust").wantCode(t, 0) // (the person's own MCP servers wait for trust)
	r := run(t, w, "--agent", "greeter", "--strict-mcp-config", "-p", "/mcp").wantCode(t, 0)
	has(t, "/mcp under --strict-mcp-config", r.out+r.err, "leaves out")
	hasNot(t, "/mcp under --strict-mcp-config", r.out, "own: connected")

	run(t, w, "--agent", "greeter", "-y", "-p", "hi").wantCode(t, 0)
	body := string(w.Model.Requests()[0].Body)
	has(t, "the tools of the agent's own server", body, "mcp__own__greet")
	hasNot(t, "the tools of a server its tools do not name", body, "mcp__other__greet")
}

// A sub-agent's own server named like one of the session's is not started:
// the sub-agent has the session's.
func TestSubAgentOwnServerNamedLikeTheSessions(t *testing.T) {
	w := apptest.NewWorld(t,
		apptest.Reply{Calls: []apptest.Call{{ID: "c1", Name: "Agent", Args: `{"description":"greet","prompt":"Greet Jae.","subagent_type":"greeter"}`}}},
		apptest.Reply{Text: "Greeted."},
		apptest.Reply{Text: "Done."},
	)
	server := w.Script(w.Home+"/bin/greeter.sh", fakeMCPServer)
	started := w.Home + "/own-started"
	own := w.Script(w.Home+"/bin/own.sh", "touch "+started+"\n"+fakeMCPServer)
	w.Write(w.ConfigDir()+"/mcp.json", `{"mcpServers": {"own": {"command": "`+server+`"}}}`)
	w.Write(w.ConfigDir()+"/agents/greeter.md", `---
name: greeter
description: Greets people.
mcpServers:
  - own:
      command: "`+own+`"
---
You greet people.
`)
	r := run(t, w, "-y", "-p", "greet Jae").wantCode(t, 0)
	has(t, "what was said", r.out+r.err, "the name of one of this session's, so it is not started")
	if w.Exists(started) {
		t.Errorf("the sub-agent's own server of a session server's name was started")
	}
}

// The /agents browser says what each kind is given and how it runs.
func TestAgentDetails(t *testing.T) {
	d := agentdef.Def{Name: "x", Tools: []string{"Read", "mcp__greet__*"}, ToolsSet: true, Disallowed: []string{"Bash"}, Model: "small", MaxTurns: 5,
		MCPServers: []agentdef.MCPServer{{Name: "github"}, {Name: "greet", Inline: json.RawMessage(`{"command":"x"}`)}}, Isolation: "worktree", Background: true,
		Memory: "project", CompactWindow: 200000}
	got := strings.Join(agentDetails(d), "\n")
	for _, want := range []string{"Tools: Read, mcp__greet__*", "Not: Bash", "Model: small", "At most 5 replies", "MCP servers: github (the session's), greet (its own)",
		"Works in a git work tree of its own", "Runs in the background", "Keeps a memory of its own: kept in this project and shared with whoever works on it through version control (project)",
		"Makes room at 200.0K tokens of context"} {
		if !strings.Contains(got, want) {
			t.Errorf("the details lack %q:\n%s", want, got)
		}
	}
	if got := agentDetails(agentdef.Def{Name: "y"}); got[0] != "Tools: every tool a sub-agent may have" || got[1] != "Model: the session's" {
		t.Errorf("a kind that sets nothing: %q", got)
	}
}

// /status says what the configuration was warned of: here a hook whose
// matcher fits no tool, which never runs.
func TestStatusNamesConfigWarnings(t *testing.T) {
	w := apptest.NewWorld(t)
	w.Settings(`{"hooks": {"PreToolUse": [{"matcher": "NoSuchTool", "hooks": [{"type": "command", "command": "true"}]}]}}`)
	r := run(t, w, "-p", "/status").wantCode(t, 0)
	has(t, "/status", r.out, "warnings", `the matcher "NoSuchTool"`, "fits no tool gnosh has")
}
