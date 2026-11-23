package deliver

import (
	"context"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/gnosh-ai/gnosh/internal/agent"
	"github.com/gnosh-ai/gnosh/internal/app"
	"github.com/gnosh-ai/gnosh/internal/app/apptest"
)

// The tool is offered to the session a person is in and to no other: not
// to a run, which has nobody to show anything to, and not to a sub-agent.
// The part of the system prompt about deliverables goes with the tool,
// and so does the command.
func TestOfferedToTheSessionAPersonIsIn(t *testing.T) {
	// A run.
	w := newWorld(t, says("Done."))
	w.run("-p", "hello").wantCode(t, 0)
	req := w.Model.Requests()[0]
	if slices.Contains(toolNamesOf(req), app.DeliverToolName) {
		t.Errorf("a run was offered %v", toolNamesOf(req))
	}
	hasNot(t, "a run's system prompt", systemOf(req), "<deliver>", "`Deliver` tool")
	if len(w.servers()) != 0 {
		t.Errorf("a run started a server: %q", w.servers())
	}

	// A session a person attends, and the sub-agent it starts.
	w = newWorld(t,
		apptest.Reply{Calls: []apptest.Call{call("c1", "Agent", map[string]any{"description": "look around", "prompt": "List the files.", "subagent_type": "Explore"})}},
		says("There is one file."),
		says("Looked."))
	var info app.SessionInfo
	var help string
	f, r := w.attach(yes, func(ctx context.Context, c app.SessionControls, f *front) int {
		info = c.Info()
		res, err := c.Command(ctx, "/help")
		if err != nil {
			t.Errorf("/help: %v", err)
		}
		help = res.Text
		f.say(ctx, c, "look around")
		c.Quit("prompt_input_exit")
		return 0
	})
	r.wantCode(t, 0)
	reqs := w.Model.Requests()
	if len(reqs) != 3 {
		t.Fatalf("the model was sent %d requests, want 3", len(reqs))
	}
	if names := toolNamesOf(reqs[0]); len(names) == 0 || names[len(names)-1] != app.DeliverToolName {
		t.Errorf("the session's tools are %v, want Deliver after gnosh's own", names)
	}
	has(t, "the session's system prompt", systemOf(reqs[0]), "<deliver>", "`Deliver` tool", "Before you start on it")
	if slices.Contains(toolNamesOf(reqs[1]), app.DeliverToolName) {
		t.Errorf("the sub-agent was offered %v", toolNamesOf(reqs[1]))
	}
	hasNot(t, "the sub-agent's system prompt", systemOf(reqs[1]), "<deliver>")
	if !slices.Contains(info.Tools, app.DeliverToolName) || !slices.Contains(info.Commands, "deliverables") {
		t.Errorf("the session's tools %v and commands %v", info.Tools, info.Commands)
	}
	has(t, "/help", help, "/deliverables", "What the agent has produced for you: the latest version of each thing, with its address and its file. (also /artifacts)")
	if len(f.asked()) != 0 || len(w.servers()) != 0 {
		t.Errorf("a session that delivered nothing asked %d questions and started %q", len(f.asked()), w.servers())
	}
	for _, name := range []string{filepath.Join(w.Dir, ".gnosh"), filepath.Join(w.DataDir(), stateName)} {
		if _, err := os.Stat(name); err == nil {
			t.Errorf("a session that delivered nothing made %s", name)
		}
	}
}

// tokenOf is the token of an address gnosh serves a file at.
func tokenOf(address string) string {
	if m := regexp.MustCompile(`^http://127\.0\.0\.1:\d+/([A-Za-z0-9_-]{43})/`).FindStringSubmatch(address); m != nil {
		return m[1]
	}
	return ""
}

// The list is built from the calls the session's file records, and from
// nothing else: it is there again when the session is continued or
// resumed in another process, with the address of each file, which still
// serves it. Recording asks nothing of the person.
func TestListIsBuiltFromTheSessionsFile(t *testing.T) {
	w := newWorld(t,
		deliver("c1", map[string]any{"label": "demo reel"}),
		deliver("c2", map[string]any{"label": "hebrew ad video"}),
		deliver("c3", map[string]any{"label": "hebrew ad video", "target": "out/ad.mp4"}),
		deliver("c4", map[string]any{"label": "landing page", "target": "https://example.com/landing"}),
		deliver("c5", map[string]any{"label": "notes", "target": "out/missing.md"}),
		says("The video and the page are there; the reel is still to come."))
	video := w.Write("out/ad.mp4", "the bytes of a video")
	const (
		reel  = "deliverable pending: demo reel"
		claim = "deliverable pending: hebrew ad video"
		page  = "deliverable: landing page | https://example.com/landing"
	)
	ad := "deliverable: hebrew ad video | http://127.0.0.1:PORT/TOKEN/ad.mp4 | " + video
	wantList := strings.Join([]string{reel, ad, page}, " ; ")

	var first []app.Deliverable
	var said, id string
	f, r := w.attach(nil, func(ctx context.Context, c app.SessionControls, f *front) int {
		id = c.Info().ID
		if got := c.Deliverables(); len(got) != 0 {
			t.Errorf("a new session has the deliverables %+v", got)
		}
		f.say(ctx, c, "make the video, the page and the reel")
		first = c.Deliverables()
		res, err := c.Command(ctx, "/deliverables")
		if err != nil {
			t.Errorf("/deliverables: %v", err)
		}
		said = res.Text
		// The address serves the file while the session runs.
		if status, header, body := fetch(t, http.MethodGet, first[1].URL); status != http.StatusOK || body != "the bytes of a video" || header.Get("Content-Type") != "video/mp4" {
			t.Errorf("the address serves %d %q as %q", status, body, header.Get("Content-Type"))
		}
		c.Quit("prompt_input_exit")
		return 0
	})
	r.wantCode(t, 0)
	w.Model.Done()

	// The front end was handed the list after each call that recorded
	// something, and after no other.
	if got, want := f.lists(), []string{
		reel,
		reel + " ; " + claim,
		reel + " ; " + ad,
		wantList,
	}; !slices.Equal(got, want) {
		t.Errorf("the lists the front end was handed:\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if got := listLine(first); got != wantList {
		t.Errorf("the session's list:\n%s\nwant\n%s", got, wantList)
	}
	ends := f.ends()
	if len(ends) != 5 {
		t.Fatalf("%d calls of Deliver ended, want 5", len(ends))
	}
	has(t, "the claim's result", text(ends[1]), `Claimed "hebrew ad video"`)
	has(t, "the delivery's result", text(ends[2]), `Recorded "hebrew ad video".`, "File: "+video+" (20 bytes)", "URL: "+first[1].URL)
	has(t, "the page's result", text(ends[3]), `Recorded "landing page".`, "Link: https://example.com/landing")
	// A file that is not there: the call does not run, and the model
	// reads why and what to do.
	if !ends[4].Result.IsError || !strings.Contains(text(ends[4]), "This call did not run: there is no file at "+filepath.Join(w.Dir, "out/missing.md")+" on this machine") ||
		!strings.Contains(text(ends[4]), "create it first") {
		t.Errorf("the missing file's result: %q", text(ends[4]))
	}
	// Each end carries the deliverable as it then stood, for the line a
	// front end writes.
	if d, _, ok := app.DeliverableOf(ends[2].Result.Meta); !ok || d.Label != "hebrew ad video" || d.Path != video || d.URL != first[1].URL || d.Pending {
		t.Errorf("what the delivery's end carries: %+v, %v", d, ok)
	}
	if _, _, ok := app.DeliverableOf(ends[4].Result.Meta); ok {
		t.Errorf("a call that failed carries a deliverable")
	}
	// Recording asked nothing of the person, though the session does not
	// skip permissions.
	if qs := f.asked(); len(qs) != 0 {
		t.Errorf("the person was asked %d questions, the first %q", len(qs), qs[0].Title)
	}
	// The model read what was recorded.
	last := w.Model.Requests()[5]
	has(t, "what the model read", string(last.Body), `Recorded \"hebrew ad video\".`, `This call did not run: there is no file at`)
	// The command says the same list in plain lines.
	has(t, "/deliverables", said,
		"Deliverables, the latest version of each (2 delivered, 1 pending):",
		"  demo reel (pending)",
		"  hebrew ad video | "+first[1].URL+" | "+video,
		"  landing page | https://example.com/landing",
		"answers on this machine only", "ssh -L ")

	// Another process continues the session: the list is there as it
	// starts, and the address of the file, on whatever port this run got,
	// holds the same token and serves the file before anything is
	// delivered anew.
	token := tokenOf(first[1].URL)
	if token == "" {
		t.Fatalf("the address %q holds no token", first[1].URL)
	}
	for _, args := range [][]string{{"-c"}, {"-r", id}} {
		var again []app.Deliverable
		var also string
		f, r := w.attach(nil, func(ctx context.Context, c app.SessionControls, f *front) int {
			again = c.Deliverables()
			if len(again) == 3 {
				if status, _, body := fetch(t, http.MethodGet, again[1].URL); status != http.StatusOK || body != "the bytes of a video" {
					t.Errorf("gnosh %s: the address serves %d %q", strings.Join(args, " "), status, body)
				}
			}
			res, _ := c.Command(ctx, "/artifacts")
			also = res.Text
			c.Quit("prompt_input_exit")
			return 0
		}, args...)
		r.wantCode(t, 0)
		if got := listLine(again); got != wantList {
			t.Errorf("gnosh %s: the list is\n%s\nwant\n%s", strings.Join(args, " "), got, wantList)
		}
		if len(again) == 3 && tokenOf(again[1].URL) != token {
			t.Errorf("gnosh %s: the file's address is %s, and its token was %s", strings.Join(args, " "), again[1].URL, token)
		}
		if got := f.lists(); !slices.Equal(got, []string{wantList}) {
			t.Errorf("gnosh %s: the front end was handed %q as the session opened", strings.Join(args, " "), got)
		}
		has(t, "/artifacts in the later session", also, "  demo reel (pending)", "  landing page | https://example.com/landing")
	}
	if n := len(w.servers()); n != 3 {
		t.Errorf("%d servers were started by the three sessions, want one each", n)
	}

	// Nothing but the calls is in the session's file, and one entry in
	// the register.
	if files := w.registerOf().Files; len(files) != 1 || files[0].Token != token || files[0].Path != video || files[0].Label != "hebrew ad video" {
		t.Errorf("the register: %+v", files)
	}
	data, err := os.ReadFile(w.Sessions()[0])
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(data), `"tool":"Deliver"`); n != 5 {
		t.Errorf("the session's file records %d results of Deliver, want 5", n)
	}
}

// In the session that draws nothing, the same two lines are printed as
// plain text: one when a deliverable is claimed, one when it is
// delivered, with the address and the file whole.
func TestPlainSessionPrintsTheLines(t *testing.T) {
	w := newWorld(t,
		deliver("c1", map[string]any{"label": "hebrew ad video"}),
		deliver("c2", map[string]any{"label": "hebrew ad video", "target": "out/ad.mp4"}),
		deliver("c3", map[string]any{"label": "notes", "target": "/nonexistent/notes.md"}),
		says("The video is there."))
	video := w.Write("out/ad.mp4", "the bytes of a video")
	s := w.sit()
	s.see("Type a prompt and Enter.")
	s.typeLine("make the video")
	s.see("deliverable pending: hebrew ad video\n")
	s.see("deliverable: hebrew ad video | http://127.0.0.1:")
	s.see("/ad.mp4 | " + video + "\n")
	s.see("* Deliver: notes (/nonexistent/notes.md)\n")
	s.see("  = failed: This call did not run: there is no file at /nonexistent/notes.md")
	s.see("The video is there.")
	s.see("deliverables: 1 delivered")
	s.typeLine("/deliverables")
	s.see("Deliverables, the latest version of each (1 delivered):")
	s.see("  hebrew ad video | http://127.0.0.1:")
	s.typeLine("/exit")
	screen := s.out.String()
	// A call that went well is its line and nothing else: not the call's
	// own line before it, and not a line that says ok after it.
	hasNot(t, "the screen", screen, "* Deliver: hebrew ad video", "  = ok")
	if n := strings.Count(screen, "deliverable pending: hebrew ad video"); n != 1 {
		t.Errorf("the claim was printed %d times", n)
	}
}

// hostWorld is a world whose config declares two hosts, with the made-up
// machine behind them and a file on it.
func hostWorld(t *testing.T, replies ...apptest.Reply) (*world, *apptest.FarMachine) {
	t.Helper()
	w := newWorld(t, replies...)
	far := w.FarMachine()
	w.Settings(`{"hosts": {"render": {"ssh": "root@203.0.113.7", "description": "the render box"}, "web": {"ssh": "deploy@203.0.113.8"}}}`)
	if err := os.MkdirAll(filepath.Join(far.Home, "renders"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(far.Home, "renders", "ad.mp4"), []byte("a video on the render box"), 0o644); err != nil {
		t.Fatal(err)
	}
	return w, far
}

// once runs one prompt in an interactive session and returns the front
// end, with the session's deliverables as they stood at the end.
func once(t *testing.T, w *world, answer func(agent.Question) agent.Answer, args ...string) (*front, []app.Deliverable) {
	t.Helper()
	var list []app.Deliverable
	f, r := w.attach(answer, func(ctx context.Context, c app.SessionControls, f *front) int {
		f.say(ctx, c, "deliver it")
		list = c.Deliverables()
		for _, d := range list {
			if d.Path != "" && d.URL != "" {
				if status, _, _ := fetch(t, http.MethodGet, d.URL); status != http.StatusOK {
					t.Errorf("the address %s answers %d", d.URL, status)
				}
			}
		}
		c.Quit("prompt_input_exit")
		return 0
	}, args...)
	r.wantCode(t, 0)
	return f, list
}

// A call with a host runs a command on that machine, and is judged as one:
// it asks, when anything asks at all, and the question names the machine
// and shows the very command; the person's deny rules are held against
// the command before it leaves; and with permissions skipped the
// declaration of the host is the whole gate.
func TestHostCallIsJudgedAsOne(t *testing.T) {
	fromRender := func() []apptest.Reply {
		return []apptest.Reply{
			deliver("c1", map[string]any{"label": "hebrew ad video", "target": "renders/ad.mp4", "host": "render"}),
			says("So it went."),
		}
	}
	const wantLine = "deliverable: hebrew ad video | http://127.0.0.1:PORT/TOKEN/ad.mp4 | renders/ad.mp4 on render"

	t.Run("it asks, and a yes runs it", func(t *testing.T) {
		w, far := hostWorld(t, fromRender()...)
		f, list := once(t, w, yes)
		qs := f.asked()
		if len(qs) != 1 {
			t.Fatalf("the person was asked %d questions, want one", len(qs))
		}
		if qs[0].Title != "Allow Deliver?" || qs[0].Kind != "permission" {
			t.Errorf("the question: %q, of kind %q", qs[0].Title, qs[0].Kind)
		}
		has(t, "the question", qs[0].Text,
			`Record the deliverable "hebrew ad video": look for renders/ad.mp4 on render (root@203.0.113.7) and copy it back to this machine`,
			"  $ ls -ld 'renders/ad.mp4' && test -f 'renders/ad.mp4'", "&& cat 'renders/ad.mp4'",
			"Why gnosh asks: Deliver runs a command on another machine for this call, which is judged as that command is.",
			"The command runs on the host render.")
		if got := listLine(list); got != wantLine {
			t.Errorf("the list:\n%s\nwant\n%s", got, wantLine)
		}
		if calls := far.Calls(); !slices.Equal(calls, []string{"root@203.0.113.7"}) {
			t.Errorf("ssh was called for %q", calls)
		}
	})

	t.Run("a no runs nothing and records nothing", func(t *testing.T) {
		w, far := hostWorld(t, fromRender()...)
		f, list := once(t, w, func(agent.Question) agent.Answer { return agent.Answer{Choice: "no", Text: "not from that box"} })
		ends := f.ends()
		if len(ends) != 1 || !ends[0].Denied || !strings.Contains(text(ends[0]), "the user refused it, and said: not from that box") {
			t.Fatalf("the call's end: %+v", ends)
		}
		if len(list) != 0 || len(far.Calls()) != 0 || len(w.servers()) != 0 {
			t.Errorf("a refused call left the list %+v, reached %q and started %q", list, far.Calls(), w.servers())
		}
	})

	t.Run("with permissions skipped nothing asks", func(t *testing.T) {
		w, far := hostWorld(t, fromRender()...)
		// Rules about commands are in force: the copy command is one the
		// rules can read whole, so they are checked against it and
		// nobody is asked.
		w.Deny("Bash(rm *)", "Bash(sudo *)")
		f, list := once(t, w, nil, "-y")
		if qs := f.asked(); len(qs) != 0 {
			t.Fatalf("the person was asked %q: %s", qs[0].Title, qs[0].Text)
		}
		if got := listLine(list); got != wantLine || len(far.Calls()) != 1 {
			t.Errorf("the list is %q after %d calls of ssh", got, len(far.Calls()))
		}
	})

	t.Run("a deny rule for the command refuses it before it leaves", func(t *testing.T) {
		for _, mode := range [][]string{nil, {"-y"}} {
			w, far := hostWorld(t, fromRender()...)
			w.Deny("Bash(cat *)")
			f, list := once(t, w, yes, mode...)
			ends := f.ends()
			if len(ends) != 1 || !ends[0].Denied {
				t.Fatalf("%v: the call's end: %+v", mode, ends)
			}
			has(t, "what the model reads", text(ends[0]), "This call did not run.", "is judged as that command is", "The deny rule Bash(cat *)")
			if len(f.asked()) != 0 || len(list) != 0 || len(far.Calls()) != 0 {
				t.Errorf("%v: %d questions, the list %+v, ssh called for %q", mode, len(f.asked()), list, far.Calls())
			}
		}
	})

	t.Run("a file outside the working directories asks, as reading it does", func(t *testing.T) {
		w := newWorld(t)
		outside := filepath.Join(w.Home, "Desktop", "report.pdf")
		w.Write(outside, "a report")
		w.Model.Add(deliver("c1", map[string]any{"label": "the report", "target": outside}), says("So it went."))
		f, list := once(t, w, yes)
		qs := f.asked()
		if len(qs) != 1 || qs[0].Title != "Allow Deliver?" {
			t.Fatalf("the person was asked %d questions", len(qs))
		}
		has(t, "the question", qs[0].Text, `Record the deliverable "the report": the file `+outside,
			"Why gnosh asks: Deliver hands over a file for this call, which is judged as reading that file is.",
			outside+" is outside the working directories, and reading there asks first.")
		if got := listLine(list); got != "deliverable: the report | http://127.0.0.1:PORT/TOKEN/report.pdf | "+outside {
			t.Errorf("the list: %q", got)
		}

		// Refused: nothing is recorded and nothing is served.
		w = newWorld(t)
		outside = filepath.Join(w.Home, "Desktop", "report.pdf")
		w.Write(outside, "a report")
		w.Model.Add(deliver("c1", map[string]any{"label": "the report", "target": outside}), says("So it went."))
		f, list = once(t, w, nil)
		if len(f.asked()) != 1 || len(list) != 0 || len(w.servers()) != 0 {
			t.Errorf("refused: %d questions, the list %+v, servers %q", len(f.asked()), list, w.servers())
		}
	})

	t.Run("a file on this machine is held to the rules about files", func(t *testing.T) {
		w := newWorld(t,
			deliver("c1", map[string]any{"label": "the key", "target": "secret/key.txt"}),
			deliver("c2", map[string]any{"label": "the notes", "target": "notes.md"}),
			says("So it went."))
		w.Write("secret/key.txt", "a secret")
		notes := w.Write("notes.md", "notes")
		w.Deny("Read(./secret/**)")
		f, list := once(t, w, yes)
		ends := f.ends()
		if len(ends) != 2 || !ends[0].Denied || ends[1].Denied {
			t.Fatalf("the calls' ends: %+v", ends)
		}
		has(t, "what the model reads", text(ends[0]), "This call did not run.", "The deny rule Read(./secret/**)")
		if got := listLine(list); got != "deliverable: the notes | http://127.0.0.1:PORT/TOKEN/notes.md | "+notes {
			t.Errorf("the list: %q", got)
		}
		if len(f.asked()) != 0 {
			t.Errorf("the person was asked %d questions", len(f.asked()))
		}
		if files := w.registerOf().Files; len(files) != 1 || files[0].Path != notes {
			t.Errorf("the register: %+v", files)
		}
	})
}

// A run never opens the port, also in a project whose register already
// holds delivered files.
func TestARunOpensNoPort(t *testing.T) {
	w := newWorld(t,
		deliver("c1", map[string]any{"label": "notes", "target": "notes.md"}),
		says("Delivered."))
	w.Write("notes.md", "notes")
	// A port that is free now: the runs below are told to use it.
	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := probe.Addr().String()
	probe.Close()
	w.Settings(`{"options": {"deliverablesServer": "` + addr + `"}}`)

	_, list := once(t, w, nil)
	if len(list) != 1 || !strings.HasPrefix(list[0].URL, "http://"+addr+"/") {
		t.Fatalf("the session's list: %+v, want the file served at %s", list, addr)
	}
	if got := w.servers(); !slices.Equal(got, []string{"http://" + addr}) {
		t.Fatalf("the session started %q", got)
	}

	// The same project, as a run: while the model is asked, nothing
	// listens where the session's server was.
	listening := func() bool {
		conn, err := net.DialTimeout("tcp", addr, time.Second)
		if err != nil {
			return false
		}
		conn.Close()
		return true
	}
	open := false
	w.Model.Add(apptest.Reply{Text: "Nothing to do.", Arrived: func() { open = listening() }})
	w.run("-p", "-c", "anything more?").wantCode(t, 0)
	if open {
		t.Errorf("while a run was under way something listened on %s", addr)
	}
	if got := w.servers(); len(got) != 1 {
		t.Errorf("the run started a server: %q", got)
	}
}

// When the port is taken, a free one is used, the person is told which,
// and the address says so too.
func TestATakenPortIsSaid(t *testing.T) {
	w := newWorld(t,
		deliver("c1", map[string]any{"label": "notes", "target": "notes.md"}),
		says("Delivered."))
	w.Write("notes.md", "notes")
	taken, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer taken.Close()
	addr := taken.Addr().String()
	w.Settings(`{"options": {"deliverablesServer": "` + addr + `"}}`)

	f, list := once(t, w, nil)
	if len(list) != 1 || list[0].URL == "" || strings.HasPrefix(list[0].URL, "http://"+addr+"/") {
		t.Fatalf("the session's list: %+v, want the file served on another port than %s", list, addr)
	}
	started := w.servers()
	if len(started) != 1 || !strings.HasPrefix(list[0].URL, started[0]+"/") {
		t.Fatalf("the session started %q, and the address is %s", started, list[0].URL)
	}
	want := "The port of " + addr + " is taken on this machine, so the files gnosh delivers are served at " + started[0] + "/ in this session, to this machine only. An address handed out earlier at " + addr + " now reaches whatever holds that port: another gnosh session, or a program that is not gnosh, which would see the address."
	if !slices.Contains(f.noticed(), want) {
		t.Errorf("the person was told %q, want among it:\n%s", f.noticed(), want)
	}
}

// With the option off nothing is served: a delivered file is listed with
// its path alone, and no port is opened.
func TestTheOptionSwitchesTheServerOff(t *testing.T) {
	w := newWorld(t,
		deliver("c1", map[string]any{"label": "notes", "target": "notes.md"}),
		says("Delivered."))
	notes := w.Write("notes.md", "notes")
	w.Settings(`{"options": {"deliverablesServer": "off"}}`)
	f, list := once(t, w, nil)
	if got := listLine(list); got != "deliverable: notes | "+notes {
		t.Errorf("the list: %q", got)
	}
	has(t, "what the model reads", text(f.ends()[0]), "No URL: gnosh is not serving delivered files in this session (the option deliverablesServer is off).")
	if len(w.servers()) != 0 {
		t.Errorf("a server was started: %q", w.servers())
	}
}

// A register that this account's gnosh did not write, such as one a
// cloned repository brings with it, is not served from, and the person is
// told that it is there.
func TestARegisterOfSomeoneElsesIsNotServed(t *testing.T) {
	w := newWorld(t, says("Hello."))
	secret := filepath.Join(w.Home, "secret.txt")
	if err := os.WriteFile(secret, []byte("a secret of the person's"), 0o600); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(w.Dir, ".gnosh", folderName)
	stranger := newRegister(dir, []byte(strings.Repeat("s", 32)), nil, nil)
	planted, err := stranger.add(t.Context(), "", secret, secret, "a present")
	if err != nil {
		t.Fatal(err)
	}

	f, list := once(t, w, nil)
	if len(list) != 0 || len(w.servers()) != 0 {
		t.Errorf("the session lists %+v and started %q", list, w.servers())
	}
	want := filepath.Join(dir, registerName) + " holds 1 entry that this account's gnosh did not write: the seal does not fit this account's key. They are not served."
	if !slices.Contains(f.noticed(), want) {
		t.Errorf("the person was told %q, want among it:\n%s", f.noticed(), want)
	}

	// And once the session serves a file of its own, the stranger's entry
	// is still not found on its server.
	w.Model.Add(deliver("c1", map[string]any{"label": "notes", "target": "notes.md"}), says("Delivered."))
	w.Write("notes.md", "notes")
	_, list = once(t, w, nil, "-c")
	if len(list) != 1 || list[0].URL == "" {
		t.Fatalf("the session's list: %+v", list)
	}
	// The server is gone with the session; the register says what it
	// would have served.
	ours := newRegister(dir, mustKey(t, w), nil, nil)
	if _, ok := ours.byToken(planted.Token); ok {
		t.Errorf("the stranger's entry is served")
	}
	if served, foreign, err := ours.count(); served != 1 || foreign != 1 || err != nil {
		t.Errorf("the register holds %d entries of this account's and %d of someone else's (%v)", served, foreign, err)
	}
}

// mustKey reads the account's key of a world.
func mustKey(t *testing.T, w *world) []byte {
	t.Helper()
	key, err := loadKey(w.DataDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	return key
}
