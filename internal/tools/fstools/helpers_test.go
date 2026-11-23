package fstools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gnosh-ai/gnosh/internal/core"
)

// in is the arguments of a tool call, as the JSON object a model sends.
type in map[string]any

// fixture is a temp directory, a session, and the six tools.
type fixture struct {
	t    *testing.T
	dir  string
	env  *core.ToolEnv
	opts Options

	read, write, edit, glob, grep, nbedit core.Tool
}

// newFixture makes a fixture whose tools are set up with opts. Searches
// get no time limit unless opts asks for one, so that a slow machine does
// not fail a test.
func newFixture(t *testing.T, opts Options) *fixture {
	t.Helper()
	if opts.SearchTimeout == 0 {
		opts.SearchTimeout = -1
	}
	// On macOS the temp directory is reached through a link (/var is
	// /private/var); the tools report real paths, so the tests use them.
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	tools := New(opts)
	return &fixture{
		t:      t,
		dir:    dir,
		env:    &core.ToolEnv{Cwd: dir, Roots: []string{dir}, SessionID: "test", Store: &sync.Map{}},
		opts:   opts.withDefaults(),
		read:   tools[0],
		write:  tools[1],
		edit:   tools[2],
		glob:   tools[3],
		grep:   tools[4],
		nbedit: tools[5],
	}
}

// path returns the full path of a name in the fixture's directory.
func (f *fixture) path(rel string) string {
	return filepath.Join(f.dir, filepath.FromSlash(rel))
}

// file writes a file under the fixture's directory, making the
// directories above it, and returns its path.
func (f *fixture) file(rel, content string) string {
	f.t.Helper()
	p := f.path(rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		f.t.Fatal(err)
	}
	return p
}

// mkdir makes a directory under the fixture's directory.
func (f *fixture) mkdir(rel string) string {
	f.t.Helper()
	p := f.path(rel)
	if err := os.MkdirAll(p, 0o755); err != nil {
		f.t.Fatal(err)
	}
	return p
}

// symlink makes a symbolic link at rel that points at target.
func (f *fixture) symlink(target, rel string) string {
	f.t.Helper()
	p := f.path(rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		f.t.Fatal(err)
	}
	if err := os.Symlink(target, p); err != nil {
		f.t.Skipf("cannot make a symbolic link here: %v", err)
	}
	return p
}

// content returns what a file holds.
func (f *fixture) content(path string) string {
	f.t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		f.t.Fatal(err)
	}
	return string(b)
}

// setTime sets a file's modification time to a given number of seconds
// after a fixed moment, so that tests can order files exactly.
func (f *fixture) setTime(path string, seconds int) {
	f.t.Helper()
	when := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC).Add(time.Duration(seconds) * time.Second)
	if err := os.Chtimes(path, when, when); err != nil {
		f.t.Fatal(err)
	}
}

func call(input in) core.ToolCall {
	raw, err := json.Marshal(input)
	if err != nil {
		panic(err)
	}
	return core.ToolCall{ID: "call-1", Name: "test", Input: raw}
}

// run runs a tool and fails the test if the harness-level error is set.
func (f *fixture) run(tool core.Tool, input in) core.ToolResult {
	f.t.Helper()
	res, err := tool.Run(context.Background(), call(input), f.env)
	if err != nil {
		f.t.Fatalf("%s(%v): unexpected error: %v", tool.Spec().Name, input, err)
	}
	return res
}

// ok runs a tool, requires that it did not fail, and returns its text.
func (f *fixture) ok(tool core.Tool, input in) string {
	f.t.Helper()
	res := f.run(tool, input)
	if res.IsError {
		f.t.Fatalf("%s(%v) failed: %s", tool.Spec().Name, input, core.TextOf(res.Content))
	}
	return core.TextOf(res.Content)
}

// fails runs a tool, requires that it failed with a message holding every
// one of wants, and returns the message.
func (f *fixture) fails(tool core.Tool, input in, wants ...string) string {
	f.t.Helper()
	res := f.run(tool, input)
	text := core.TextOf(res.Content)
	if !res.IsError {
		f.t.Fatalf("%s(%v) succeeded, want a failure about %q. It said: %s", tool.Spec().Name, input, wants, text)
	}
	for _, want := range wants {
		if !strings.Contains(text, want) {
			f.t.Errorf("%s(%v) failed with\n  %s\nwhich does not mention %q", tool.Spec().Name, input, text, want)
		}
	}
	return text
}

// describe asks a tool what a call would do.
func (f *fixture) describe(tool core.Tool, input in) (core.Action, error) {
	f.t.Helper()
	d, ok := tool.(core.Describer)
	if !ok {
		f.t.Fatalf("%s is not a core.Describer", tool.Spec().Name)
	}
	return d.Describe(call(input), f.env)
}

// mustContain fails the test unless text holds every one of wants.
func mustContain(t *testing.T, text string, wants ...string) {
	t.Helper()
	for _, want := range wants {
		if !strings.Contains(text, want) {
			t.Errorf("text does not hold %q:\n%s", want, text)
		}
	}
}

// mustNotContain fails the test if text holds any of the unwanted.
func mustNotContain(t *testing.T, text string, unwanted ...string) {
	t.Helper()
	for _, u := range unwanted {
		if strings.Contains(text, u) {
			t.Errorf("text holds %q and should not:\n%s", u, text)
		}
	}
}

// numberedText builds what Read shows for the given lines, numbered from
// first.
func numberedText(first int, lines ...string) string {
	var b strings.Builder
	for i, l := range lines {
		b.WriteString(itoa(first + i))
		b.WriteByte('\t')
		b.WriteString(l)
		b.WriteByte('\n')
	}
	return b.String()
}

func itoa(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}

// manyLines returns n lines "line 1", "line 2", ... each ended with a
// line break.
func manyLines(n int) string {
	var b strings.Builder
	for i := 1; i <= n; i++ {
		b.WriteString("line ")
		b.WriteString(itoa(i))
		b.WriteByte('\n')
	}
	return b.String()
}

// dirNames lists the names in a directory.
func dirNames(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, len(entries))
	for i, e := range entries {
		names[i] = e.Name()
	}
	return names
}

// metaOf digs a value out of a result's Meta by a path of keys.
func metaOf(t *testing.T, res core.ToolResult, keys ...string) any {
	t.Helper()
	var v any = res.Meta
	for _, k := range keys {
		m, ok := v.(map[string]any)
		if !ok {
			t.Fatalf("Meta has no %q on the way to %v: %#v", k, keys, res.Meta)
		}
		v = m[k]
	}
	return v
}
