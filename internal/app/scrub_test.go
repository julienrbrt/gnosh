package app

import (
	"os"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/gnosh-ai/gnosh/internal/app/apptest"
	"github.com/gnosh-ai/gnosh/internal/sandbox"
)

// With CLAUDE_CODE_SUBPROCESS_ENV_SCRUB=1 the model's commands and the
// person's hooks start without the credentials gnosh was started with,
// their own settings' among them; a package index keeps its URL without
// the password, and what is no credential stays. Without it, nothing is
// taken.
func TestSubprocessEnvScrub(t *testing.T) {
	probe := `{"command": "echo \"[$NPM_TOKEN|$SET_TOKEN|$PLAIN|$PIP_INDEX_URL|$GITHUB_TOKEN]\""}`
	w := apptest.NewWorld(t,
		apptest.Reply{Calls: []apptest.Call{{ID: "c1", Name: "Bash", Args: probe}}}, apptest.Reply{Text: "ok"},
		apptest.Reply{Calls: []apptest.Call{{ID: "c1", Name: "Bash", Args: probe}}}, apptest.Reply{Text: "ok"},
	)
	seen := w.Home + "/hook-env.txt"
	w.Settings(`{"env": {"SET_TOKEN": "from-settings"}, "hooks": {"PreToolUse": [{"matcher": "Bash", "hooks": [{"type": "command", "command": "env > ` + seen + `"}]}]}}`)
	env := []string{"NPM_TOKEN=abc123", "PLAIN=hello", "PIP_INDEX_URL=https://u:pw@pypi.example/simple", "GITHUB_TOKEN=ghp_keptkeptkeptkept1234"}

	o := testOptions(w, "-y", "-p", "look")
	o.Environ = append(append(o.Environ, env...), "CLAUDE_CODE_SUBPROCESS_ENV_SCRUB=1")
	runWith(t, t.Context(), o).wantCode(t, 0)
	has(t, "a command's environment, scrubbed", toolResult(t, w, 1, "c1"), "[||hello|https://pypi.example/simple|ghp_keptkeptkeptkept1234]")
	hook := w.Read(seen)
	hasNot(t, "a hook's environment, scrubbed", hook, "NPM_TOKEN=", "SET_TOKEN=", "u:pw@")
	has(t, "a hook's environment, scrubbed", hook, "PLAIN=hello", "GITHUB_TOKEN=")

	o = testOptions(w, "-y", "-p", "look")
	o.Environ = append(o.Environ, env...)
	runWith(t, t.Context(), o).wantCode(t, 0)
	has(t, "a command's environment, as it was", toolResult(t, w, 3, "c1"), "[abc123|from-settings|hello|https://u:pw@pypi.example/simple|")
	has(t, "a hook's environment, as it was", w.Read(seen), "NPM_TOKEN=abc123", "SET_TOKEN=from-settings")

	// /status says the scrub is on, and only then.
	o = testOptions(w, "-p", "/status")
	o.Environ = append(o.Environ, "CLAUDE_CODE_SUBPROCESS_ENV_SCRUB=1")
	has(t, "/status with the scrub", runWith(t, t.Context(), o).wantCode(t, 0).out, "scrub        CLAUDE_CODE_SUBPROCESS_ENV_SCRUB")
	hasNot(t, "/status without it", run(t, w, "-p", "/status").wantCode(t, 0).out, "CLAUDE_CODE_SUBPROCESS_ENV_SCRUB")
}

// With the scrub on, the sandbox's settings that let a command out are
// taken back: filesystem.disabled anywhere, and on Linux the commands it
// excludes and the model's asking out of it; /sandbox says so.
func TestScrubTakesBackSandboxLoosenings(t *testing.T) {
	if st := sandbox.Check(); !st.OK {
		t.Skip(st.Why)
	}
	w := apptest.NewWorld(t)
	w.Settings(`{"sandbox": {"enabled": true, "excludedCommands": ["docker *"], "filesystem": {"disabled": true}}}`)
	o := testOptions(w, "-p", "/sandbox")
	o.Environ = append(o.Environ, "CLAUDE_CODE_SUBPROCESS_ENV_SCRUB=1")
	r := runWith(t, t.Context(), o).wantCode(t, 0)
	hasNot(t, "/sandbox with the scrub on", r.out, "Files: left open")
	if runtime.GOOS == "linux" {
		hasNot(t, "/sandbox with the scrub on, on Linux", r.out, "docker *", "dangerouslyDisableSandbox")
		has(t, "/sandbox with the scrub on, on Linux", r.out, "CLAUDE_CODE_SUBPROCESS_ENV_SCRUB")
	}
	r = run(t, w, "-p", "/sandbox").wantCode(t, 0)
	if !strings.Contains(r.out, "Files: left open") {
		t.Errorf("without the scrub, filesystem.disabled is not in force:\n%s", r.out)
	}
}

// A stdio MCP server starts from the scrubbed environment with the scrub
// on, and from the baseline alone with CLAUDE_CODE_MCP_ALLOWLIST_ENV on;
// its own env from its settings is laid over either.
func TestMCPServerEnvironment(t *testing.T) {
	w := apptest.NewWorld(t)
	seen := w.Home + "/server-env.txt"
	server := w.Script(w.Home+"/bin/env-server.sh", "env > "+seen+"\n"+fakeMCPServer)
	w.Write(w.ConfigDir()+"/mcp.json", `{"mcpServers": {"own": {"command": "`+server+`", "env": {"SERVER_KEY": "given"}}}}`)
	run(t, w, "trust").wantCode(t, 0) // (the person's own MCP servers wait for trust)
	for _, c := range []struct {
		env       []string
		has, lack []string
	}{
		{[]string{"CLAUDE_CODE_SUBPROCESS_ENV_SCRUB=1"}, []string{"PLAIN=hello", "SERVER_KEY=given"}, []string{"NPM_TOKEN="}},
		{[]string{"CLAUDE_CODE_MCP_ALLOWLIST_ENV=1"}, []string{"SERVER_KEY=given", "HOME="}, []string{"PLAIN=hello", "NPM_TOKEN="}},
		{nil, []string{"PLAIN=hello", "NPM_TOKEN=abc123", "SERVER_KEY=given"}, nil},
	} {
		o := testOptions(w, "-p", "/mcp")
		o.Environ = append(append(o.Environ, "PLAIN=hello", "NPM_TOKEN=abc123"), c.env...)
		r := runWith(t, t.Context(), o).wantCode(t, 0)
		has(t, "/mcp", r.out, "own: connected")
		got := w.Read(seen)
		has(t, "the server's environment with "+strings.Join(c.env, " "), got, c.has...)
		hasNot(t, "the server's environment with "+strings.Join(c.env, " "), got, c.lack...)
	}
}

// On Linux, with the scrub on and the sandbox off, a command runs in a PID
// namespace of its own: gnosh's process is not in its /proc.
func TestScrubGivesCommandsTheirOwnPIDs(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux's alone")
	}
	if st := sandbox.Check(); !st.OK {
		t.Skip(st.Why)
	}
	probe := `{"command": "test -e /proc/` + strconv.Itoa(os.Getpid()) + ` && echo [seen] || echo [unseen]"}`
	w := apptest.NewWorld(t,
		apptest.Reply{Calls: []apptest.Call{{ID: "c1", Name: "Bash", Args: probe}}}, apptest.Reply{Text: "ok"},
		apptest.Reply{Calls: []apptest.Call{{ID: "c1", Name: "Bash", Args: probe}}}, apptest.Reply{Text: "ok"},
	)
	o := testOptions(w, "-y", "-p", "look")
	o.Environ = append(o.Environ, "CLAUDE_CODE_SUBPROCESS_ENV_SCRUB=1")
	runWith(t, t.Context(), o).wantCode(t, 0)
	has(t, "with the scrub", toolResult(t, w, 1, "c1"), "[unseen]")
	run(t, w, "-y", "-p", "look").wantCode(t, 0)
	has(t, "without it", toolResult(t, w, 3, "c1"), "[seen]")
}
