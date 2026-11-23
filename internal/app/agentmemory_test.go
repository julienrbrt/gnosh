package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/gnosh-ai/gnosh/internal/config"
	"github.com/gnosh-ai/gnosh/internal/core"
	"github.com/gnosh-ai/gnosh/internal/permission"
)

// A memory's index is given as far as its first 200 lines or 25 KB go,
// whichever ends first, and said to be cut when it is; a local memory's
// folder is made with a .gitignore that keeps it out of git.
func TestAgentMemoryIndex(t *testing.T) {
	base := t.TempDir()
	dir := filepath.Join(base, ".gnosh", permission.MemoryLocalFolder, "rev")
	open := func(content string) (string, bool) {
		t.Helper()
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, memoryIndex), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		index, cut, err := openAgentMemory(dir, base, "local")
		if err != nil {
			t.Fatal(err)
		}
		return index, cut
	}
	lines := func(n int) string { return strings.Repeat("- a note\n", n) }
	if index, cut := open(lines(200)); cut || strings.Count(index, "\n") != 200 {
		t.Errorf("200 lines: %d lines, cut %v", strings.Count(index, "\n"), cut)
	}
	if index, cut := open(lines(250)); !cut || strings.Count(index, "\n") != 200 {
		t.Errorf("250 lines: %d lines, cut %v", strings.Count(index, "\n"), cut)
	}
	long := strings.Repeat("é", 20_000) // 40,000 bytes on one line
	if index, cut := open(long); !cut || len(index) > memoryIndexBytes || !utf8.ValidString(index) || index == "" {
		t.Errorf("one long line: %d bytes, cut %v, valid %v", len(index), cut, utf8.ValidString(index))
	}
	if index, cut := open(lines(10) + long); !cut || index != lines(10) {
		t.Errorf("a long line after ten: %q, cut %v", index, cut)
	}
	if got, err := os.ReadFile(filepath.Join(dir, ".gitignore")); err != nil || !strings.Contains(string(got), "\n*\n") {
		t.Errorf("the local memory's .gitignore: %q %v", got, err)
	}
	if err := os.Remove(filepath.Join(dir, memoryIndex)); err != nil {
		t.Fatal(err)
	}
	if index, cut, err := openAgentMemory(dir, base, "local"); index != "" || cut || err != nil {
		t.Errorf("no index: %q %v %v", index, cut, err)
	}
}

// A kind's autoCompactWindow holds where it is the lower; the session's,
// set lower, wins (Claude Code takes the lowest).
func TestLowerWindow(t *testing.T) {
	for _, c := range []struct {
		kind    int
		session config.Value[int]
		want    int
	}{
		{200000, config.Value[int]{}, 200000},
		{200000, config.Value[int]{V: 150000, Set: true}, 150000},
		{200000, config.Value[int]{V: 500000, Set: true}, 200000},
	} {
		if got := lowerWindow(c.kind, c.session); got != c.want {
			t.Errorf("lowerWindow(%d, %+v) = %d, want %d", c.kind, c.session, got, c.want)
		}
	}
}

// Started in the home folder, its .claude folder's definitions are the
// person's own: not the folder's (held until it is trusted), nor read
// twice; its .gnosh folder is still the folder's.
func TestHomeFolderDefinitionsAreThePersons(t *testing.T) {
	cfg := &config.Config{Paths: config.Paths{Home: "/h", Cwd: "/h", ProjectRoot: "/h", ConfigDir: "/h/.config/gnosh"}}
	if got := strings.Join(projectDefDirs(cfg, "agents", nil), " "); got != "/h/.gnosh/agents" {
		t.Errorf("in the home folder: %s", got)
	}
	cfg.Paths.Cwd, cfg.Paths.ProjectRoot = "/h/proj", "/h/proj"
	if got := strings.Join(projectDefDirs(cfg, "skills", nil), " "); got != "/h/proj/.gnosh/skills /h/proj/.claude/skills" {
		t.Errorf("in a project: %s", got)
	}
}

// A file tool given for the memory alone refuses a file outside the
// memory folder, a relative one or one through a link, before anybody is
// asked, and lets one inside it through to the tool.
func TestMemoryOnlyTool(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(base, ".gnosh", "agent-memory", "rev")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(base, filepath.Join(dir, "out")); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "a/b/c/d"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("a/b/c/d", filepath.Join(dir, "l")); err != nil {
		t.Fatal(err)
	}
	tool := memoryOnly{Tool: describedTool{}, dir: dir}
	env := &core.ToolEnv{Cwd: base}
	for path, inside := range map[string]bool{
		filepath.Join(dir, "MEMORY.md"):   true,
		".gnosh/agent-memory/rev/a.md":    true,
		"main.go":                         false,
		filepath.Join(base, "main.go"):    false,
		filepath.Join(dir, "out/main.go"): false,
		filepath.Join(dir, "../x.md"):     false,
		// A link in the folder, then climbing: the tool tidies the text
		// first, and writes the project's main.go.
		filepath.Join(dir, "l/../../../../main.go"): false,
	} {
		call := core.ToolCall{Name: "Write", Input: json.RawMessage(`{"file_path":` + strconv.Quote(path) + `}`)}
		_, err := tool.Describe(call, env)
		if inside != (err == nil) {
			t.Errorf("%s: Describe said %v", path, err)
		}
		res, _ := tool.Run(context.Background(), call, env)
		if inside == res.IsError {
			t.Errorf("%s: Run said %+v", path, res)
		}
	}
}

// describedTool is a file tool that says yes to everything, and reads
// its file_path as the file tools do: from the folder the call is in,
// the text tidied.
type describedTool struct{}

func (describedTool) Spec() core.ToolSpec { return core.ToolSpec{Name: "Write"} }
func (describedTool) Describe(call core.ToolCall, env *core.ToolEnv) (core.Action, error) {
	var in struct {
		FilePath string `json:"file_path"`
	}
	_ = json.Unmarshal(call.Input, &in)
	p := in.FilePath
	if !filepath.IsAbs(p) {
		p = filepath.Join(env.Cwd, p)
	}
	return core.Action{Kind: core.ActionEdit, Paths: []string{filepath.Clean(p)}}, nil
}
func (describedTool) Run(context.Context, core.ToolCall, *core.ToolEnv) (core.ToolResult, error) {
	return core.TextResult("written"), nil
}
