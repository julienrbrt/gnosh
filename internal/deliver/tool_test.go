package deliver

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gnosh-ai/gnosh/internal/agent"
	"github.com/gnosh-ai/gnosh/internal/app"
	"github.com/gnosh-ai/gnosh/internal/app/apptest"
	"github.com/gnosh-ai/gnosh/internal/config"
	"github.com/gnosh-ai/gnosh/internal/core"
	"github.com/gnosh-ai/gnosh/internal/tools/shelltool"
)

// bench is the tool on a workbench: one session's join with real folders
// under it, a made-up machine behind a stand-in for ssh, and everything
// the join hands the session kept for the test to read.
type bench struct {
	t    *testing.T
	w    *apptest.World
	far  *apptest.FarMachine
	j    *join
	tool *tool
	env  *core.ToolEnv

	mu      sync.Mutex
	lists   [][]app.Deliverable
	notices []string
	served  []string
}

// The hosts a bench declares. The addresses are from the range kept for
// documentation, and nothing ever connects to them.
var benchHosts = []config.Host{
	{Name: "render", SSH: "root@203.0.113.7", Description: "the render box"},
	{Name: "web", SSH: "deploy@203.0.113.8"},
}

// newBench makes a bench. listen is where files are served, or
// config.ServerOff; most is the largest file copied back from a host, or
// zero for the default.
func newBench(t *testing.T, listen string, most int64) *bench {
	t.Helper()
	b := &bench{t: t, w: apptest.NewWorld(t)}
	b.far = b.w.FarMachine()
	shell := shelltool.New(shelltool.Options{
		Environ:  func() []string { return b.w.Env() },
		SpoolDir: t.TempDir(),
		Local:    "dev@box",
		Hosts: []shelltool.Host{
			{Name: "render", Target: "root@203.0.113.7"},
			{Name: "web", Target: "deploy@203.0.113.8"},
		},
	})
	t.Cleanup(func() {
		if err := shell.Close(); err != nil {
			t.Errorf("closing the shell manager: %v", err)
		}
	})
	cfg := &config.Config{Hosts: benchHosts}
	cfg.Options.DeliverablesServer.V = listen
	s := &app.Session{
		ID:          "session-1",
		Interactive: true,
		Config:      cfg,
		Dirs: app.Dirs{
			Home: b.w.Home, Cwd: b.w.Dir, ProjectRoot: b.w.Dir,
			State: b.w.DataDir(), Project: filepath.Join(b.w.Dir, ".gnosh"),
		},
		Deliverables: func(list []app.Deliverable) {
			b.mu.Lock()
			b.lists = append(b.lists, list)
			b.mu.Unlock()
		},
		Notice: func(level agent.Level, source, text string) {
			b.mu.Lock()
			b.notices = append(b.notices, string(level)+": "+text)
			b.mu.Unlock()
		},
		Shell: func() *shelltool.Manager { return shell },
		Now:   time.Now,
	}
	p := &process{servers: map[string]*server{}, opts: Options{MaxCopyBytes: DefaultMaxCopyBytes, served: func(base string) {
		b.mu.Lock()
		b.served = append(b.served, base)
		b.mu.Unlock()
	}}}
	if most > 0 {
		p.opts.MaxCopyBytes = most
	}
	t.Cleanup(func() { p.shutdown(context.Background()) })
	c, err := p.session(t.Context(), s)
	if err != nil || len(c.Tools) != 1 {
		t.Fatalf("the session was given %d tools (%v)", len(c.Tools), err)
	}
	b.tool = c.Tools[0].(*tool)
	b.j = b.tool.j
	b.env = &core.ToolEnv{Cwd: b.w.Dir, Roots: []string{b.w.Dir}, SessionID: s.ID, Store: new(sync.Map)}
	c.Ready(nil)
	return b
}

// call calls the tool as the loop does: what the call would do is asked
// first, and a result that went well is told to the join as an entry of
// the session's file.
func (b *bench) call(args map[string]any) core.ToolResult {
	b.t.Helper()
	input, err := json.Marshal(args)
	if err != nil {
		b.t.Fatal(err)
	}
	c := core.ToolCall{ID: "call-1", Name: app.DeliverToolName, Input: input}
	_, wrong := b.tool.Describe(c, b.env)
	res, err := b.tool.Run(b.t.Context(), c, b.env)
	if err != nil {
		b.t.Fatalf("Deliver %s: the tool failed to run: %v", input, err)
	}
	if (wrong != nil) && !res.IsError {
		b.t.Errorf("Deliver %s: Describe said the call cannot run (%v), and it ran", input, wrong)
	}
	if _, err := json.Marshal(res.Meta); err != nil {
		b.t.Errorf("Deliver %s: the result's Meta cannot be written as JSON: %v", input, err)
	}
	b.j.afterStored(b.t.Context(), agent.Entry{Kind: agent.EntryResult, ToolName: app.DeliverToolName, Result: res}, nil)
	return res
}

// list is the list the session was last handed, each entry on a line.
func (b *bench) list() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.lists) == 0 {
		return ""
	}
	return listLine(b.lists[len(b.lists)-1])
}

// addressIn finds the address a result gives the model.
func addressIn(res core.ToolResult) string {
	for line := range strings.Lines(core.TextOf(res.Content)) {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(line), "URL: "); ok {
			return rest
		}
	}
	return ""
}

// A label alone claims a deliverable; the label with a file delivers it;
// the same label again takes the earlier entry's place. What the model
// reads says which it was, the file, and the address; and the address
// serves the file.
func TestClaimThenDeliver(t *testing.T) {
	b := newBench(t, "127.0.0.1:0", 0)
	video := b.w.Write("out/ad.mp4", "the bytes of a video")

	res := b.call(map[string]any{"label": "Hebrew ad video"})
	if res.IsError || core.TextOf(res.Content) != `Claimed "Hebrew ad video": the user sees it as pending. When it exists, call Deliver again with the same label and its target.` {
		t.Fatalf("a claim: %v %q", res.IsError, core.TextOf(res.Content))
	}
	if d, replaced, ok := app.DeliverableOf(res.Meta); !ok || replaced || d != (app.Deliverable{Label: "Hebrew ad video", Pending: true}) {
		t.Errorf("what the claim recorded: %+v, %v, %v", d, replaced, ok)
	}
	if got := b.list(); got != "deliverable pending: Hebrew ad video" {
		t.Errorf("the list after the claim: %q", got)
	}
	if len(b.served) != 0 {
		t.Errorf("a server was started for a claim")
	}
	res = b.call(map[string]any{"label": "Hebrew ad video"})
	has(t, "a claim made twice", core.TextOf(res.Content), `"Hebrew ad video" is claimed already`)

	// Delivered: a relative path is read from the working directory, and
	// the label is the same one whatever its letter case and blanks.
	res = b.call(map[string]any{"label": "hebrew  ad video", "target": "out/ad.mp4"})
	if res.IsError {
		t.Fatalf("delivering the file: %s", core.TextOf(res.Content))
	}
	address := addressIn(res)
	has(t, "what the model reads", core.TextOf(res.Content),
		`Recorded "hebrew ad video".`, "File: "+video+" (20 bytes)", "URL: http://127.0.0.1:",
		"Give them the URL", "to this machine only", "ssh -L ")
	hasNot(t, "what the model reads", core.TextOf(res.Content), "in the place of")
	if d, replaced, _ := app.DeliverableOf(res.Meta); replaced || d != (app.Deliverable{Label: "hebrew ad video", Path: video, URL: address}) {
		t.Errorf("what the delivery recorded: %+v, replaced %v", d, replaced)
	}
	if got, want := b.list(), "deliverable: hebrew ad video | http://127.0.0.1:PORT/TOKEN/ad.mp4 | "+video; got != want {
		t.Errorf("the list after the delivery:\n%s\nwant\n%s", got, want)
	}
	status, header, body := fetch(t, http.MethodGet, address)
	if status != http.StatusOK || body != "the bytes of a video" || header.Get("Content-Type") != "video/mp4" {
		t.Errorf("the address serves %d %q as %q", status, body, header.Get("Content-Type"))
	}
	if len(b.served) != 1 || !strings.HasPrefix(address, b.served[0]+"/") {
		t.Errorf("servers started: %q, and the address is %s", b.served, address)
	}

	// The same file again keeps its address; a newer file under the same
	// label takes the entry's place and says so.
	res = b.call(map[string]any{"label": "hebrew ad video", "target": video})
	if addressIn(res) != address {
		t.Errorf("the same file delivered again is at %s, and was at %s", addressIn(res), address)
	}
	v2 := b.w.Write("out/ad-v2.mp4", "a second cut")
	res = b.call(map[string]any{"label": "HEBREW AD VIDEO", "target": "~/../proj/out/ad-v2.mp4"})
	has(t, "a newer version", core.TextOf(res.Content), `Recorded "HEBREW AD VIDEO", in the place of the version recorded before.`, "File: "+v2)
	if d, replaced, _ := app.DeliverableOf(res.Meta); !replaced || d.Path != v2 {
		t.Errorf("a newer version recorded %+v, replaced %v", d, replaced)
	}
	if got, want := b.list(), "deliverable: HEBREW AD VIDEO | http://127.0.0.1:PORT/TOKEN/ad-v2.mp4 | "+v2; got != want {
		t.Errorf("the list holds\n%s\nwant the latest version alone:\n%s", got, want)
	}
	if addressIn(res) == address {
		t.Errorf("another file was given the first one's address")
	}

	// Claimed again: the version there is stays on the entry.
	res = b.call(map[string]any{"label": "hebrew ad video"})
	has(t, "a second claim", core.TextOf(res.Content), `Claimed "hebrew ad video" again`, "a new version is pending", "stays listed")
	if got, want := b.list(), "deliverable, a new version is pending: hebrew ad video | http://127.0.0.1:PORT/TOKEN/ad-v2.mp4 | "+v2; got != want {
		t.Errorf("the list after the second claim:\n%s\nwant\n%s", got, want)
	}

	// A second thing has an entry of its own.
	b.call(map[string]any{"label": "notes"})
	if got := b.list(); !strings.HasSuffix(got, " ; deliverable pending: notes") || strings.Count(got, " ; ") != 1 {
		t.Errorf("the list with a second label: %q", got)
	}
}

// An address that begins http:// or https:// is taken as given: nothing
// is fetched, and a host says nothing about it.
func TestDeliverAnAddress(t *testing.T) {
	b := newBench(t, "127.0.0.1:0", 0)
	for given, address := range map[string]string{
		"https://example.com/landing?x=1#top": "https://example.com/landing?x=1#top",
		" http://203.0.113.9:8080/report ":    "http://203.0.113.9:8080/report",
		"HTTPS://Example.com/Page":            "https://Example.com/Page",
	} {
		res := b.call(map[string]any{"label": given, "target": given, "host": "render"})
		if res.IsError || core.TextOf(res.Content) != "Recorded \""+strings.TrimSpace(given)+"\".\nLink: "+address+"\nThe user is shown it." {
			t.Errorf("delivering %s: %v %q", given, res.IsError, core.TextOf(res.Content))
		}
		if d, replaced, _ := app.DeliverableOf(res.Meta); replaced || d != (app.Deliverable{Label: strings.TrimSpace(given), URL: address}) {
			t.Errorf("delivering %s recorded %+v", given, d)
		}
	}
	// The same label again, with another address: the entry's place is
	// taken.
	res := b.call(map[string]any{"label": "https://example.com/landing?x=1#top", "target": "https://example.com/v2"})
	has(t, "a newer address", core.TextOf(res.Content), "in the place of the version recorded before", "Link: https://example.com/v2")
	if got := b.list(); strings.Count(got, " ; ") != 2 || !strings.HasSuffix(got, "deliverable: https://example.com/landing?x=1#top | https://example.com/v2") {
		t.Errorf("the list: %q", got)
	}
	if calls := b.far.Calls(); len(calls) != 0 || len(b.served) != 0 {
		t.Errorf("an address made gnosh reach a machine (%q) or start a server (%q)", calls, b.served)
	}
}

// What is refused, and what the model is told to do about it. A refusal
// records nothing.
func TestDeliverRefusals(t *testing.T) {
	b := newBench(t, "127.0.0.1:0", 0)
	b.w.Write("notes.md", "notes")
	if err := os.MkdirAll(filepath.Join(b.w.Dir, "site"), 0o755); err != nil {
		t.Fatal(err)
	}
	long := strings.Repeat("a very long label ", 6)
	for _, tc := range []struct {
		name string
		args map[string]any
		want []string
	}{
		{"no label", map[string]any{"target": "notes.md"}, []string{"label is missing", "a short, stable name"}},
		{"a blank label", map[string]any{"label": " \n "}, []string{"label is missing"}},
		{"a label that is a sentence", map[string]any{"label": long}, []string{"the label is 107 characters long", "at most 80"}},
		{"a label that is no string", map[string]any{"label": 7}, []string{"label must be a string"}},
		{"a target that is no string", map[string]any{"label": "x", "target": []string{"a"}}, []string{"target must be a string"}},
		{"an argument it does not have", map[string]any{"label": "x", "path": "notes.md"}, []string{`"path" is not an argument of Deliver, which takes label, target and host`, "goes in target"}},
		{"another argument it does not have", map[string]any{"label": "x", "description": "the notes"}, []string{`"description" is not an argument of Deliver`}},
		{"a file that is not there", map[string]any{"label": "x", "target": "out/missing.mp4"},
			[]string{"there is no file at " + filepath.Join(b.w.Dir, "out/missing.mp4") + " on this machine", "the working directory is " + b.w.Dir, "create it first", "name that machine as host"}},
		{"a file that is elsewhere", map[string]any{"label": "x", "target": "/nonexistent/notes.md"},
			[]string{"there is no file at /nonexistent/notes.md", "There is a file of that name at " + filepath.Join(b.w.Dir, "notes.md")}},
		{"a directory", map[string]any{"label": "x", "target": "site"}, []string{filepath.Join(b.w.Dir, "site") + " is a directory", "name the file the user would open"}},
		{"a device", map[string]any{"label": "x", "target": "/dev/null"}, []string{"/dev/null is not a regular file"}},
		{"an address of another kind", map[string]any{"label": "x", "target": "s3://bucket/ad.mp4"}, []string{"s3://bucket/ad.mp4 is neither", "http:// or https://"}},
		{"an address with no host", map[string]any{"label": "x", "target": "https:///landing"}, []string{"cannot be read as an address"}},
		{"a target with a line break", map[string]any{"label": "x", "target": "a\nb"}, []string{"holds a line break"}},
		{"a link that turns its text around", map[string]any{"label": "x", "target": "https://example.com/\u202egpj.exe"}, []string{"holds a line break or another character that cannot be seen"}},
		{"a path with a zero-width space", map[string]any{"label": "x", "target": "notes\u200b.md"}, []string{"cannot be seen"}},
		{"a host with a hidden character", map[string]any{"label": "x", "target": "a.mp4", "host": "ren\u200bder"}, []string{"the host holds a character that cannot be seen"}},
		{"a machine that is not declared", map[string]any{"label": "x", "target": "a.mp4", "host": "elsewhere"},
			[]string{`the host "elsewhere" is not a machine declared for this session`, "render (root@203.0.113.7), web (deploy@203.0.113.8)", "Leave host out"}},
		{"a file that is not there on a host", map[string]any{"label": "x", "target": "renders/missing.mp4", "host": "render"},
			[]string{"there is no file at renders/missing.mp4 on render", "is read from " + b.far.Home}},
	} {
		res := b.call(tc.args)
		got := core.TextOf(res.Content)
		if !res.IsError || !strings.HasPrefix(got, "Nothing was recorded: ") || len(res.Meta) != 0 {
			t.Errorf("%s: the result is %v %q with Meta %v, want a refusal that records nothing", tc.name, res.IsError, got, res.Meta)
		}
		has(t, tc.name, got, tc.want...)
	}
	if got := b.list(); got != "" {
		t.Errorf("refused calls left %q on the list", got)
	}
	if _, err := os.Stat(filepath.Join(b.w.Dir, ".gnosh", folderName, registerName)); err == nil {
		t.Errorf("refused calls wrote a register")
	}

	// A file's own address is read as its path.
	res := b.call(map[string]any{"label": "notes", "target": "file://" + filepath.Join(b.w.Dir, "notes.md")})
	if d, _, ok := app.DeliverableOf(res.Meta); !ok || d.Path != filepath.Join(b.w.Dir, "notes.md") {
		t.Errorf("a file:// address: %q, %+v", core.TextOf(res.Content), d)
	}
}

// With a host, the file is looked for on that machine and copied back by
// the road a command for it takes; the copy is in the project's data
// folder, and is what the address serves.
func TestDeliverFromAHost(t *testing.T) {
	b := newBench(t, "127.0.0.1:0", 0)
	if err := os.MkdirAll(filepath.Join(b.far.Home, "renders"), 0o755); err != nil {
		t.Fatal(err)
	}
	content := "the bytes of a video\x00\xff on the render box"
	if err := os.WriteFile(filepath.Join(b.far.Home, "renders", "ad.mp4"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	res := b.call(map[string]any{"label": "hebrew ad video", "target": "renders/ad.mp4", "host": "render"})
	if res.IsError {
		t.Fatalf("delivering from the host: %s", core.TextOf(res.Content))
	}
	address := addressIn(res)
	has(t, "what the model reads", core.TextOf(res.Content),
		`Recorded "hebrew ad video".`, "File: renders/ad.mp4 on render (40 bytes), copied to this machine.", "URL: http://127.0.0.1:")
	if d, _, _ := app.DeliverableOf(res.Meta); d != (app.Deliverable{Label: "hebrew ad video", Path: "renders/ad.mp4", Host: "render", URL: address}) {
		t.Errorf("what was recorded: %+v", d)
	}
	if got, want := b.list(), "deliverable: hebrew ad video | http://127.0.0.1:PORT/TOKEN/ad.mp4 | renders/ad.mp4 on render"; got != want {
		t.Errorf("the list:\n%s\nwant\n%s", got, want)
	}
	if status, _, body := fetch(t, http.MethodGet, address); status != http.StatusOK || body != content {
		t.Errorf("the address serves %d %q", status, body)
	}
	if calls := b.far.Calls(); !slices.Equal(calls, []string{"root@203.0.113.7"}) {
		t.Errorf("ssh was called for %q, want once for root@203.0.113.7", calls)
	}

	// The copy: in the project's data folder, the owner's alone, and no
	// part of a copy left beside it.
	copies, _ := filepath.Glob(filepath.Join(b.w.Dir, ".gnosh", folderName, copiesName, "*", "*"))
	if len(copies) != 1 || filepath.Base(copies[0]) != "ad.mp4" {
		t.Fatalf("the copies in the data folder: %q", copies)
	}
	if data, err := os.ReadFile(copies[0]); err != nil || string(data) != content {
		t.Errorf("the copy holds %q (%v)", data, err)
	}
	if st, _ := os.Stat(copies[0]); st.Mode().Perm() != 0o600 {
		t.Errorf("the copy's mode is %v", st.Mode())
	}
	hidden, _ := filepath.Glob(filepath.Join(filepath.Dir(copies[0]), ".*"))
	if len(hidden) != 0 {
		t.Errorf("left beside the copy: %q", hidden)
	}

	// A newer version at the same path keeps the address and takes the
	// copy's place.
	if err := os.WriteFile(filepath.Join(b.far.Home, "renders", "ad.mp4"), []byte("a second cut"), 0o644); err != nil {
		t.Fatal(err)
	}
	res = b.call(map[string]any{"label": "hebrew ad video", "target": "renders/ad.mp4", "host": "render"})
	if addressIn(res) != address {
		t.Errorf("the newer version is at %s, and the first was at %s", addressIn(res), address)
	}
	if _, _, body := fetch(t, http.MethodGet, address); body != "a second cut" {
		t.Errorf("the address serves %q after the newer version was delivered", body)
	}
	if copies, _ := filepath.Glob(filepath.Join(b.w.Dir, ".gnosh", folderName, copiesName, "*", "*")); len(copies) != 1 {
		t.Errorf("the copies after the newer version: %q", copies)
	}

	// The machine named "local" is this one.
	here := b.w.Write("here.txt", "on this machine")
	res = b.call(map[string]any{"label": "a local file", "target": "here.txt", "host": "local"})
	if d, _, _ := app.DeliverableOf(res.Meta); d.Path != here || d.Host != "" {
		t.Errorf("a file on the machine named local: %+v", d)
	}

	// A machine that cannot be reached: said in the words a command for
	// it gets, and nothing is recorded.
	b.far.Fail("ssh: connect to host 203.0.113.7 port 22: Connection refused")
	res = b.call(map[string]any{"label": "another", "target": "renders/ad.mp4", "host": "render"})
	if !res.IsError {
		t.Fatalf("a machine that cannot be reached: %q", core.TextOf(res.Content))
	}
	has(t, "a machine that cannot be reached", core.TextOf(res.Content), "Nothing was recorded: ", "refused the connection", "Nothing was run on render")
	if strings.Contains(b.list(), "another") {
		t.Errorf("a file that could not be fetched is on the list: %q", b.list())
	}
}

// A file on a host that is larger than gnosh copies back is recorded with
// its path there and is not served; the result says what the limit is.
func TestDeliverFromAHostOverTheLimit(t *testing.T) {
	b := newBench(t, "127.0.0.1:0", 1000)
	if err := os.WriteFile(filepath.Join(b.far.Home, "raw.mov"), make([]byte, 5000), 0o644); err != nil {
		t.Fatal(err)
	}
	res := b.call(map[string]any{"label": "raw footage", "target": "raw.mov", "host": "render"})
	if res.IsError {
		t.Fatalf("a file over the limit: %s", core.TextOf(res.Content))
	}
	has(t, "what the model reads", core.TextOf(res.Content),
		`Recorded "raw footage".`, "File: raw.mov on render (4.9 KiB)",
		"No URL: the file is larger than the 1000 bytes gnosh copies back from another machine", "deliver a smaller version")
	if d, _, _ := app.DeliverableOf(res.Meta); d != (app.Deliverable{Label: "raw footage", Path: "raw.mov", Host: "render"}) {
		t.Errorf("what was recorded: %+v", d)
	}
	if got := b.list(); got != "deliverable: raw footage | raw.mov on render" {
		t.Errorf("the list: %q", got)
	}
	if copies, _ := filepath.Glob(filepath.Join(b.w.Dir, ".gnosh", folderName, copiesName, "*", "*")); len(copies) != 0 {
		t.Errorf("something of the file was kept: %q", copies)
	}
	// A file at the limit is copied.
	if err := os.WriteFile(filepath.Join(b.far.Home, "ok.mov"), make([]byte, 1000), 0o644); err != nil {
		t.Fatal(err)
	}
	res = b.call(map[string]any{"label": "a cut", "target": "ok.mov", "host": "render"})
	address := addressIn(res)
	if address == "" {
		t.Fatalf("a file at the limit was not served: %s", core.TextOf(res.Content))
	}
	if status, _, _ := fetch(t, http.MethodGet, address); status != http.StatusOK {
		t.Errorf("the address of the file at the limit answers %d", status)
	}

	// When that file has grown past the limit and is delivered again, the
	// copy made before is no longer the file: its address stops serving
	// it, and the entry has none.
	if err := os.WriteFile(filepath.Join(b.far.Home, "ok.mov"), make([]byte, 1001), 0o644); err != nil {
		t.Fatal(err)
	}
	res = b.call(map[string]any{"label": "a cut", "target": "ok.mov", "host": "render"})
	has(t, "what the model reads", core.TextOf(res.Content), `Recorded "a cut", in the place of the version recorded before.`, "File: ok.mov on render (1001 bytes)", "No URL: the file is larger than")
	if got := b.list(); !strings.HasSuffix(got, " ; deliverable: a cut | ok.mov on render") {
		t.Errorf("the list after the file grew: %q", got)
	}
	if status, _, _ := fetch(t, http.MethodGet, address); status != http.StatusNotFound {
		t.Errorf("the address of the copy made before answers %d", status)
	}
	if copies, _ := filepath.Glob(filepath.Join(b.w.Dir, ".gnosh", folderName, copiesName, "*", "*")); len(copies) != 0 {
		t.Errorf("the copy made before was kept: %q", copies)
	}
}

// With the server switched off nothing is served and no port is opened: a
// file is recorded with its path, and a file on a host is looked for and
// not copied.
func TestDeliverWithTheServerOff(t *testing.T) {
	b := newBench(t, config.ServerOff, 0)
	video := b.w.Write("ad.mp4", "the bytes of a video")
	res := b.call(map[string]any{"label": "ad", "target": "ad.mp4"})
	if res.IsError || addressIn(res) != "" {
		t.Fatalf("with the server off: %v %q", res.IsError, core.TextOf(res.Content))
	}
	has(t, "what the model reads", core.TextOf(res.Content), `Recorded "ad".`, "File: "+video,
		"No URL: gnosh is not serving delivered files in this session (the option deliverablesServer is off). The user is shown the path.")
	if got := b.list(); got != "deliverable: ad | "+video {
		t.Errorf("the list: %q", got)
	}

	if err := os.WriteFile(filepath.Join(b.far.Home, "far.mp4"), []byte("on the render box"), 0o644); err != nil {
		t.Fatal(err)
	}
	res = b.call(map[string]any{"label": "far", "target": "far.mp4", "host": "render"})
	if res.IsError {
		t.Fatalf("a file on a host with the server off: %s", core.TextOf(res.Content))
	}
	has(t, "what the model reads", core.TextOf(res.Content), "File: far.mp4 on render (17 bytes)", "No URL:", "was not copied to this machine")
	res = b.call(map[string]any{"label": "gone", "target": "gone.mp4", "host": "render"})
	if !res.IsError || !strings.Contains(core.TextOf(res.Content), "there is no file at gone.mp4 on render") {
		t.Errorf("a missing file on a host with the server off: %q", core.TextOf(res.Content))
	}
	if len(b.served) != 0 || len(b.notices) != 0 {
		t.Errorf("with the server off a server was started (%q) or the person was told something (%q)", b.served, b.notices)
	}
	if _, err := os.Stat(filepath.Join(b.w.Dir, ".gnosh")); err == nil {
		t.Errorf("with the server off the project's data folder was made")
	}
	if _, err := os.Stat(filepath.Join(b.w.DataDir(), stateName)); err == nil {
		t.Errorf("with the server off the account's key was made")
	}
}

// What a call would do, as the person's rules and the person are told: a
// claim and an address touch nothing; a file on this machine is named as
// the file that is handed over; a file on a host is the very command that
// runs there.
func TestDescribe(t *testing.T) {
	b := newBench(t, "127.0.0.1:0", 0)
	video := b.w.Write("out/ad.mp4", "the bytes of a video")
	describe := func(args map[string]any) (core.Action, error) {
		input, _ := json.Marshal(args)
		return b.tool.Describe(core.ToolCall{ID: "c", Name: app.DeliverToolName, Input: input}, b.env)
	}

	a, err := describe(map[string]any{"label": "ad"})
	if err != nil || a.Kind != core.ActionOther || len(a.Paths) != 0 || a.Command != "" || a.Summary != `Claim the deliverable "ad", which is then shown as pending` {
		t.Errorf("a claim: %+v, %v", a, err)
	}
	a, err = describe(map[string]any{"label": "page", "target": "https://example.com/landing"})
	if err != nil || a.Kind != core.ActionOther || a.URL != "" || !strings.Contains(a.Summary, "the address https://example.com/landing") {
		t.Errorf("an address: %+v, %v", a, err)
	}
	a, err = describe(map[string]any{"label": "ad", "target": "out/ad.mp4"})
	if err != nil || a.Kind != core.ActionRead || !slices.Equal(a.Paths, []string{video}) || a.Summary != `Record the deliverable "ad": the file `+video {
		t.Errorf("a file: %+v, %v", a, err)
	}
	// A file that is not there: the call cannot run, and what it was
	// aimed at is still named, so that the rules are held against it.
	a, err = describe(map[string]any{"label": "ad", "target": "out/missing.mp4"})
	if err == nil || !strings.Contains(err.Error(), "there is no file at") || a.Kind != core.ActionRead || !slices.Equal(a.Paths, []string{filepath.Join(b.w.Dir, "out/missing.mp4")}) {
		t.Errorf("a missing file: %+v, %v", a, err)
	}
	a, err = describe(map[string]any{"label": "ad", "target": "renders/ad.mp4", "host": "render"})
	if err != nil || a.Kind != core.ActionExecute || a.Command != shelltool.CopyCommand("renders/ad.mp4", DefaultMaxCopyBytes) {
		t.Errorf("a file on a host: %+v, %v", a, err)
	}
	has(t, "the line a person is shown", a.Summary, `Record the deliverable "ad": look for renders/ad.mp4 on render (root@203.0.113.7)`, "copy it back to this machine, if it is at most 1 GiB")
	a, err = describe(map[string]any{"label": "ad", "target": "renders/ad.mp4", "host": "elsewhere"})
	if err == nil || a.Kind != core.ActionOther || a.Command != "" {
		t.Errorf("a file on an undeclared machine: %+v, %v", a, err)
	}
	a, err = describe(map[string]any{"target": "x"})
	if err == nil || a.Kind != core.ActionOther {
		t.Errorf("a call with no label: %+v, %v", a, err)
	}
	// Asking what a call would do does nothing.
	if calls := b.far.Calls(); len(calls) != 0 || len(b.served) != 0 || b.list() != "" {
		t.Errorf("describing calls reached a machine (%q), started a server (%q) or recorded something (%q)", calls, b.served, b.list())
	}
}

// What the model is told about the tool is the same in every session,
// whatever machines are declared and wherever files are served.
func TestSpec(t *testing.T) {
	a, b := newBench(t, "127.0.0.1:0", 0), newBench(t, config.ServerOff, 0)
	b.j.hosts, b.j.hostNames = map[string]string{}, nil
	sa, sb := a.tool.Spec(), b.tool.Spec()
	if sa.Name != "Deliver" || sa.Description != sb.Description || string(sa.InputSchema) != string(sb.InputSchema) {
		t.Errorf("the tool's description differs between two sessions")
	}
	var schema struct {
		Type       string                       `json:"type"`
		Properties map[string]map[string]string `json:"properties"`
		Required   []string                     `json:"required"`
		More       bool                         `json:"additionalProperties"`
	}
	if err := json.Unmarshal(sa.InputSchema, &schema); err != nil {
		t.Fatalf("the schema is not JSON: %v", err)
	}
	if schema.Type != "object" || len(schema.Properties) != 3 || !slices.Equal(schema.Required, []string{"label"}) || schema.More {
		t.Errorf("the schema: %+v", schema)
	}
	for _, name := range []string{"label", "target", "host"} {
		if p := schema.Properties[name]; p["type"] != "string" || p["description"] == "" {
			t.Errorf("the argument %s: %v", name, p)
		}
	}
	has(t, "the description", sa.Description, "label alone", "pending", "http:// or https://", "must exist", "at most 1 GiB", "the same label for a newer version")
	hasNot(t, "the description", sa.Description, "render box", "root@", "203.0.113", "7777")
}

// Two labels that differ only in letter case or in their blanks are one,
// and what cannot be seen is taken out of a label.
func TestLabels(t *testing.T) {
	for label, want := range map[string]string{
		"  Demo   video ":                "Demo video",
		"demo\tvideo\n":                  "demo video",
		"a\x1b[31mb":                     "a[31mb",
		"right\u202eto\u200bleft":        "righttoleft",
		"plain":                          "plain",
		"\u05e1\u05e8\u05d8\u05d5\u05df": "\u05e1\u05e8\u05d8\u05d5\u05df",
	} {
		if got := cleanLabel(label); got != want {
			t.Errorf("cleanLabel(%q) = %q, want %q", label, got, want)
		}
	}
	if labelKey("Demo  Video") != labelKey(" demo video") || labelKey("demo video") == labelKey("demo videos") {
		t.Errorf("labels are not compared by their words alone")
	}
	var l list
	l.put(app.Deliverable{Label: "Demo video", Pending: true})
	l.put(app.Deliverable{Label: "notes", Pending: true})
	l.put(app.Deliverable{Label: "demo  VIDEO", Path: "/out/demo.mp4"})
	l.put(app.Deliverable{Label: " ", Path: "/nothing"})
	if got := listLine(l.all()); got != "deliverable pending: notes ; deliverable: demo VIDEO | /out/demo.mp4" {
		t.Errorf("the list: %q", got)
	}
	if d, ok := l.get("DEMO VIDEO"); !ok || d.Path != "/out/demo.mp4" {
		t.Errorf("an entry by its label: %+v, %v", d, ok)
	}
}

// A call is held to the file it was judged for: a link changed between
// the question and the run makes its target name another file, which is
// not delivered (the deliverables review of 2026-11-19).
func TestDeliverIsHeldToTheFileItWasJudgedFor(t *testing.T) {
	b := newBench(t, "127.0.0.1:0", 0)
	b.w.Write("one.md", "one")
	b.w.Write("two.md", "two")
	if err := os.Symlink("one.md", b.w.Path("report.md")); err != nil {
		t.Skip("no links here:", err)
	}
	input, err := json.Marshal(map[string]any{"label": "report", "target": "report.md"})
	if err != nil {
		t.Fatal(err)
	}
	c := core.ToolCall{ID: "call-judged", Name: app.DeliverToolName, Input: input}
	if _, err := b.tool.Describe(c, b.env); err != nil {
		t.Fatal(err)
	}
	os.Remove(b.w.Path("report.md"))
	if err := os.Symlink("two.md", b.w.Path("report.md")); err != nil {
		t.Fatal(err)
	}
	res, err := b.tool.Run(t.Context(), c, b.env)
	if err != nil {
		t.Fatal(err)
	}
	if got := core.TextOf(res.Content); !res.IsError || !strings.Contains(got, "the call was judged for the file "+b.w.Path("one.md")) {
		t.Errorf("a call whose link changed after the question: %v %q", res.IsError, got)
	}
	if got := b.list(); got != "" {
		t.Errorf("the list holds %q", got)
	}
}
