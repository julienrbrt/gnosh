package agentdef

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func write(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

// gnosh's own two kinds read like any definition, and say what they are
// for.
func TestBuiltin(t *testing.T) {
	defs := Builtin()
	if len(defs) != 3 {
		t.Fatalf("%d built-in definitions, want 3", len(defs))
	}
	byName := map[string]Def{}
	for _, d := range defs {
		byName[d.Name] = d
		if d.Scope != "built-in" || d.Description == "" || len(d.Prompt) < 200 {
			t.Errorf("the built-in %q: %+v", d.Name, d)
		}
	}
	gp, ex := byName["general-purpose"], byName["Explore"]
	if gp.ToolsSet || gp.OmitInstructions {
		t.Errorf("general-purpose is to have every tool and the instruction files: %+v", gp)
	}
	if strings.Join(ex.Tools, " ") != "Read Glob Grep LSP" || !ex.ToolsSet || !ex.OmitInstructions {
		t.Errorf("Explore is to read and search only, without the instruction files: %+v", ex)
	}
	if pl := byName["Plan"]; strings.Join(pl.Tools, " ") != "Read Glob Grep LSP" || !pl.ToolsSet || !pl.OmitInstructions {
		t.Errorf("Plan is to read and search only, without the instruction files: %+v", pl)
	}
}

// A definition file in the forms people write them.
func TestParse(t *testing.T) {
	d, reason := Parse("---\nname: code-reviewer\ndescription: \"Reviews code: quality, security\"  \ntools: Read, Glob, Grep, Bash(git diff *)\ndisallowedTools:\n  - Write\n  - 'Edit'\nmodel: inherit\npermissionMode: plan\nmaxTurns: 12\nomitClaudeMd: true\ncolor: blue\nhooks:\n  PreToolUse:\n    - matcher: Bash\nmade-up-field: 3\n---\n\nYou are a code reviewer.\n\nBe specific.\n")
	if reason != "" {
		t.Fatalf("skipped: %s", reason)
	}
	want := Def{
		Name: "code-reviewer", Description: "Reviews code: quality, security",
		Prompt: "You are a code reviewer.\n\nBe specific.",
		Tools:  []string{"Read", "Glob", "Grep", "Bash"}, ToolsSet: true, Disallowed: []string{"Write", "Edit"},
		PermissionMode: "plan", MaxTurns: 12, OmitInstructions: true,
	}
	if d.Name != want.Name || d.Description != want.Description || d.Prompt != want.Prompt ||
		strings.Join(d.Tools, ",") != strings.Join(want.Tools, ",") || !d.ToolsSet ||
		strings.Join(d.Disallowed, ",") != strings.Join(want.Disallowed, ",") ||
		d.Model != "" || d.PermissionMode != "plan" || d.MaxTurns != 12 || !d.OmitInstructions ||
		string(d.Hooks) != `{"PreToolUse":[{"matcher":"Bash"}]}` {
		t.Errorf("read as %+v", d)
	}

	// Agent(kind, ...) among its tools: one entry, whatever its commas;
	// and a model written as inherit.
	d, reason = Parse("---\nname: lead\ndescription: Leads.\ntools: Read, Agent(worker, researcher), Grep\nmodel: inherit\n---\nLead.")
	if reason != "" || strings.Join(d.Tools, ",") != "Read,Agent,Grep" || strings.Join(d.AgentTypes, ",") != "worker,researcher" || !d.InheritModel || d.Model != "" {
		t.Errorf("read as %+v (%s)", d, reason)
	}
	if d, _ = Parse("---\nname: lead\ndescription: Leads.\ntools: [Agent(worker, x), Read]\n---\nLead."); strings.Join(d.Tools, ",") != "Agent,Read" || strings.Join(d.AgentTypes, ",") != "worker,x" {
		t.Errorf("a list in brackets read as %q, %q", d.Tools, d.AgentTypes)
	}
	if d, _ = Parse("---\nname: lead\ndescription: Leads.\ntools: Agent, Read\n---\nLead."); d.AgentTypes != nil || d.InheritModel {
		t.Errorf("Agent bare read as %q, inherit %v", d.AgentTypes, d.InheritModel)
	}

	// The prompt a session that runs as it starts with.
	if d, _ = Parse("---\nname: s\ndescription: d\ninitialPrompt: List the files first.\n---\nx"); d.InitialPrompt != "List the files first." {
		t.Errorf("initialPrompt read as %q", d.InitialPrompt)
	}

	// The skills it preloads, as a list or with commas, and its effort.
	d, reason = Parse("---\nname: careful\ndescription: Checks.\nskills:\n  - review-list\n  - ' style '\neffort: high\n---\nCheck.")
	if reason != "" || strings.Join(d.Skills, ",") != "review-list,style" || d.Effort != "high" {
		t.Errorf("read as %+v (%s)", d, reason)
	}
	if d, _ = Parse("---\nname: c\ndescription: d\nskills: a, b\n---\nx"); strings.Join(d.Skills, ",") != "a,b" {
		t.Errorf("skills with commas read as %q", d.Skills)
	}

	// A list in brackets, a description that runs over lines, and a model.
	d, reason = Parse("---\nname: tester\ndescription: >\n  Runs the tests\n  and reports.\ntools: [Bash, Read]\nmodel: local/qwen\n---\nRun them.")
	if reason != "" || d.Description != "Runs the tests and reports." || strings.Join(d.Tools, ",") != "Bash,Read" || d.Model != "local/qwen" {
		t.Errorf("read as %+v (%s)", d, reason)
	}

	for text, why := range map[string]string{
		"name: x\ndescription: y\n":                              `first line is not "---"`,
		"---\nname: x\ndescription: y\n":                         "not closed",
		"---\ndescription: y\n---\nbody":                         "no name",
		"---\nname: x\n---\nbody":                                "no description",
		"---\nname: -x\ndescription: y\n---\nbody":               "cannot be an agent's",
		"---\nname: a:b\ndescription: y\n---\nbody":              "cannot be an agent's",
		"---\nname: x\n  stray\n: y\ndescription: y\n---\nbody":  "cannot be read",
		"---\nname: x\ndescription: y\nmaxTurns: lots\n---\nbod": "not a number of turns",
		"---\nname: x\ndescription: y\nhooks: Stop\n---\nbody":   "something other than a block of events",
	} {
		if _, reason := Parse(text); !strings.Contains(reason, why) {
			t.Errorf("%q: skipped for %q, want a reason holding %q", text, reason, why)
		}
	}
}

// The project's definitions come before the person's, and the person's
// before gnosh's own; the directory closest to the work wins; folders
// below a directory are searched; what cannot be used is listed.
func TestLoad(t *testing.T) {
	root := t.TempDir()
	user := filepath.Join(root, "home", ".claude", "agents")
	near := filepath.Join(root, "proj", "sub", ".claude", "agents")
	far := filepath.Join(root, "proj", ".claude", "agents")
	def := func(name, body string) string {
		return "---\nname: " + name + "\ndescription: what " + name + " is for\n---\n" + body + "\n"
	}
	write(t, filepath.Join(user, "reviewer.md"), def("reviewer", "the person's reviewer"))
	write(t, filepath.Join(user, "deep", "er", "explore.md"), def("Explore", "the person's own Explore"))
	write(t, filepath.Join(far, "reviewer.md"), def("reviewer", "the project's reviewer"))
	write(t, filepath.Join(far, "tester.md"), def("tester", "the project's tester, far"))
	write(t, filepath.Join(near, "tester.md"), def("tester", "the project's tester, near"))
	write(t, filepath.Join(near, "z-again.md"), def("tester", "a second tester in the same tree"))
	write(t, filepath.Join(near, "broken.md"), "no settings here\n")
	write(t, filepath.Join(near, "notes.txt"), "not a definition\n")

	s := Load(user, []string{near, far, filepath.Join(root, "is-not-there")})
	got := map[string]string{}
	for _, d := range s.All() {
		got[d.Name] = d.Scope + ": " + d.Prompt
	}
	want := map[string]string{
		"general-purpose": "built-in: ",
		"Explore":         "user: the person's own Explore",
		"Plan":            "built-in: ",
		"reviewer":        "project: the project's reviewer",
		"tester":          "project: the project's tester, near",
	}
	for name, w := range want {
		if !strings.HasPrefix(got[name], w) {
			t.Errorf("%s is %q, want it to begin %q", name, got[name], w)
		}
	}
	if len(got) != len(want) {
		t.Errorf("the definitions are %v", s.Names())
	}
	if names := strings.Join(s.Names(), " "); names != "general-purpose Plan Explore reviewer tester" {
		t.Errorf("in the order %q", names)
	}
	var reasons []string
	for _, sk := range s.Skipped() {
		reasons = append(reasons, filepath.Base(sk.Path)+": "+sk.Reason)
	}
	all := strings.Join(reasons, "\n")
	if len(reasons) != 2 || !strings.Contains(all, "broken.md: its first line") || !strings.Contains(all, "z-again.md: ") {
		t.Errorf("skipped:\n%s", all)
	}
	if d, ok := s.Get("tester"); !ok || d.Source != filepath.Join(near, "tester.md") {
		t.Errorf("Get(tester) = %+v, %v", d, ok)
	}
	if _, ok := s.Get("nobody"); ok {
		t.Error("Get found an agent that is not defined")
	}
}

// A project's agent file that is a link out of the project is not read;
// one that leads to another place in the project is (the skills review of
// 2026-11-19).
func TestProjectAgentLinkedOutIsNotRead(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	def := "---\nname: %s\ndescription: what %s is for\n---\nDo it.\n"
	write(t, filepath.Join(root, "elsewhere", "spy.md"), fmt.Sprintf(def, "spy", "spy"))
	write(t, filepath.Join(root, "proj", "docs", "helper.md"), fmt.Sprintf(def, "helper", "helper"))
	agents := filepath.Join(root, "proj", ".claude", "agents")
	if err := os.MkdirAll(agents, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "elsewhere", "spy.md"), filepath.Join(agents, "spy.md")); err != nil {
		t.Skip("no links here:", err)
	}
	if err := os.Symlink("../../docs/helper.md", filepath.Join(agents, "helper.md")); err != nil {
		t.Fatal(err)
	}
	s := LoadFrom(nil, []string{agents}, []string{filepath.Join(root, "proj")})
	if _, ok := s.Get("spy"); ok {
		t.Error("an agent file linked out of the project was read")
	}
	if _, ok := s.Get("helper"); !ok {
		t.Error("an agent file linked to another place in the project was not read")
	}
	if sk := s.Skipped(); len(sk) != 1 || !strings.Contains(sk[0].Reason, "it leads out of the project, to "+filepath.Join(root, "elsewhere", "spy.md")) {
		t.Errorf("skipped: %+v", sk)
	}
	// Without the project's folders given, the loader reads what it finds.
	if _, ok := LoadFrom(nil, []string{agents}, nil).Get("spy"); !ok {
		t.Error("with no folders given, the linked file was not read")
	}
}

// An agent's mcpServers: names of the session's servers and servers of its
// own, in a block, as a flow list, and in --agents JSON; mcp__ entries in
// its tools are kept whole.
func TestParseMCPServers(t *testing.T) {
	d, reason := Parse("---\nname: browser\ndescription: Tests in a browser.\ntools: Read, mcp__playwright__*\nmcpServers:\n  # its own, and the session's\n  - playwright:\n      type: stdio\n      command: npx\n      args: [\"-y\", \"@playwright/mcp@latest\"]\n  - github\n---\nTest it.")
	if reason != "" {
		t.Fatalf("skipped: %s", reason)
	}
	if len(d.MCPServers) != 2 || d.MCPServers[0].Name != "playwright" || d.MCPServers[1].Name != "github" || d.MCPServers[1].Inline != nil {
		t.Fatalf("read as %+v", d.MCPServers)
	}
	var own struct {
		Type, Command string
		Args          []string
	}
	if err := json.Unmarshal(d.MCPServers[0].Inline, &own); err != nil || own.Type != "stdio" || own.Command != "npx" || strings.Join(own.Args, " ") != "-y @playwright/mcp@latest" {
		t.Errorf("its own server: %s", d.MCPServers[0].Inline)
	}
	if strings.Join(d.Tools, ",") != "Read,mcp__playwright__*" {
		t.Errorf("tools: %q", d.Tools)
	}
	d, reason = Parse("---\nname: notes\ndescription: Notes.\nmcpServers: [github, slack]\n---\nGo.")
	if reason != "" || len(d.MCPServers) != 2 || d.MCPServers[1].Name != "slack" {
		t.Errorf("a flow list: %+v (%s)", d.MCPServers, reason)
	}
	for text, want := range map[string]string{
		"---\nname: a\ndescription: A.\nmcpServers:\n  - github\n  - github\n---\nGo.":                  `names the server "github" twice`,
		"---\nname: a\ndescription: A.\nmcpServers:\n  - one:\n      type: stdio\n    two: x\n---\nGo.": "neither a server's name nor one server",
		"---\nname: a\ndescription: A.\nmcpServers:\n  - one: stdio\n---\nGo.":                          `"one: stdio", which is not a server's name`,
	} {
		if _, reason := Parse(text); !strings.Contains(reason, want) {
			t.Errorf("%q: skipped for %q, want %q", text, reason, want)
		}
	}
	defs, err := FromJSON(`{"tester": {"description": "Tests.", "prompt": "Test.", "mcpServers": ["github", {"local": {"command": "./srv"}}]}}`)
	if err != nil || len(defs) != 1 || len(defs[0].MCPServers) != 2 || string(defs[0].MCPServers[1].Inline) != `{"command": "./srv"}` {
		t.Errorf("--agents JSON: %+v, %v", defs, err)
	}
	if _, err := FromJSON(`{"tester": {"description": "Tests.", "prompt": "Test.", "mcpServers": "github"}}`); err == nil || !strings.Contains(err.Error(), "mcpServers is not a list") {
		t.Errorf("mcpServers as a string: %v", err)
	}
}

// A definition file is read only when it is a plain file: a link a
// repository holds may lead to /dev/zero or a terminal, which would never
// end (review of 2026-11-19).
func TestDefinitionThatIsNotAPlainFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.Symlink("/dev/zero", filepath.Join(dir, "zero.md")); err != nil {
		t.Skip(err)
	}
	done := make(chan string, 1)
	go func() {
		_, err := readSmall(filepath.Join(dir, "zero.md"))
		done <- fmt.Sprint(err)
	}()
	select {
	case msg := <-done:
		if !strings.Contains(msg, "not a plain file") {
			t.Errorf("reading a link to /dev/zero said %q", msg)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("reading a link to /dev/zero did not end")
	}
}

// A setting gnosh knows written in another letter case would be passed
// over, and the agent run without what it takes away: the definition is
// not used. A key gnosh does not know at all is passed over.
func TestSettingInAnotherCase(t *testing.T) {
	if _, reason := Parse("---\nname: reader\ndescription: Reads.\ndisallowedtools: Write, Edit\n---\nRead.\n"); !strings.Contains(reason, `"disallowedTools"`) {
		t.Errorf("a definition with disallowedtools: %q", reason)
	}
	if d, reason := Parse("---\nname: reader\ndescription: Reads.\ncolor: blue\n---\nRead.\n"); reason != "" || d.Name != "reader" {
		t.Errorf("a definition with an unknown key: %q", reason)
	}
}

// memory is user, project or local, in a file and in --agents' JSON; any
// other word, or a name that names no folder, keeps the definition from
// being used (DECISIONS 374).
func TestParseMemory(t *testing.T) {
	file := func(name, memory string) string {
		return "---\nname: " + name + "\ndescription: Reviews.\nmemory: " + memory + "\n---\nReview.\n"
	}
	for _, scope := range []string{"user", "project", "local"} {
		if d, reason := Parse(file("reviewer", scope)); reason != "" || d.Memory != scope {
			t.Errorf("memory: %s: %q, %+v", scope, reason, d)
		}
	}
	if _, reason := Parse(file("reviewer", "forever")); !strings.Contains(reason, "user, project or local") {
		t.Errorf("memory: forever: %q", reason)
	}
	if _, reason := Parse(file("team/reviewer", "project")); !strings.Contains(reason, "names no folder") {
		t.Errorf("a name with a slash, with a memory: %q", reason)
	}
	if _, reason := Parse(file("team/reviewer", "")); reason != "" {
		t.Errorf("a name with a slash, without a memory: %q", reason)
	}
	defs, err := FromJSON(`{"reviewer": {"description": "Reviews.", "prompt": "Review.", "memory": "local"}}`)
	if err != nil || len(defs) != 1 || defs[0].Memory != "local" {
		t.Errorf("memory in JSON: %v %+v", err, defs)
	}
	if _, err := FromJSON(`{"reviewer": {"description": "Reviews.", "prompt": "Review.", "memory": "always"}}`); err == nil || !strings.Contains(err.Error(), "user, project or local") {
		t.Errorf("memory: always, in JSON: %v", err)
	}
}

// autoCompactWindow is a whole number of tokens, 100000 or more, in a file
// and in JSON; another value is noted and not used, and the definition is
// used without it, as Claude Code runs it ("200k" is not 200,000).
func TestParseCompactWindow(t *testing.T) {
	file := func(window string) Def {
		t.Helper()
		d, reason := Parse("---\nname: reader\ndescription: Reads logs.\nautoCompactWindow: " + window + "\n---\nRead.\n")
		if reason != "" {
			t.Fatalf("autoCompactWindow: %s: %s", window, reason)
		}
		return d
	}
	if d := file("200000"); d.CompactWindow != 200000 || d.CompactWindowUnused != "" {
		t.Errorf("200000: %+v", d)
	}
	for _, bad := range []string{"200k", "99999", "-5"} {
		if d := file(bad); d.CompactWindow != 0 || d.CompactWindowUnused != bad {
			t.Errorf("%s: %+v", bad, d)
		}
	}
	defs, err := FromJSON(`{"a": {"description": "A.", "prompt": "A.", "autoCompactWindow": 150000}, "b": {"description": "B.", "prompt": "B.", "autoCompactWindow": 40000}}`)
	if err != nil || len(defs) != 2 {
		t.Fatalf("JSON: %v %+v", err, defs)
	}
	for _, d := range defs {
		if d.Name == "a" && d.CompactWindow != 150000 || d.Name == "b" && (d.CompactWindow != 0 || d.CompactWindowUnused != "40000") {
			t.Errorf("JSON %s: %+v", d.Name, d)
		}
	}
}
