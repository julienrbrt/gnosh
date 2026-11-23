package app

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime/debug"
	"runtime/pprof"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gnosh-ai/gnosh/internal/agent"
	"github.com/gnosh-ai/gnosh/internal/core"
	"github.com/gnosh-ai/gnosh/internal/hooks"
	"github.com/gnosh-ai/gnosh/internal/tools/shelltool"
	"github.com/gnosh-ai/gnosh/internal/transcript"
)

// crashDir is the folder a session's crash files and log go to: the
// folder its project's sessions are kept in.
func (l *live) crashDir() string {
	if l == nil || l.store == nil || l.cfg == nil {
		return ""
	}
	return l.store.ProjectDir(l.cfg.Paths.Cwd)
}

// fileLog is the project's log: one line for each thing worth finding
// later. With --debug it holds more, and with a filter only the kinds the
// filter names. A nil fileLog writes nothing.
type fileLog struct {
	mu      sync.Mutex
	f       *os.File
	now     func() time.Time
	session string
	// debug says the log holds more; /config debug=true can turn it on
	// while the session runs.
	debug atomic.Bool
	only  []string
	not   []string
	// noArgs are the tools whose arguments and results are never written
	// (ToolName.NoLogArgs).
	noArgs map[string]bool
	// fromFeature, when not nil, reports whether notices of that source
	// are a feature's. Their words are not written: a feature may quote
	// in a notice what the person later has it erase, and it can erase
	// that from the session's file, which it knows, but not from here.
	fromFeature func(source string) bool
}

// openFileLog opens the log a session writes to: the file --debug-file
// names, or gnosh.log in the folder the project's sessions are kept in. A
// log that cannot be opened is done without.
func openFileLog(p *program, l *live) *fileLog {
	path := filepath.Join(l.crashDir(), "gnosh.log")
	if l.inv.DebugFile != "" {
		path = p.absFrom(l.inv.DebugFile)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		p.warnf("the log %s could not be opened (%v), so this session writes none.", path, err)
		return nil
	}
	g := &fileLog{f: f, now: p.now, session: l.ts.ID(), noArgs: p.noLogTools()}
	g.debug.Store(l.cfg.Options.Debug.V)
	for _, word := range strings.Split(l.inv.DebugFilter, ",") {
		word = strings.TrimSpace(word)
		switch {
		case word == "":
		case strings.HasPrefix(word, "!"):
			g.not = append(g.not, word[1:])
		default:
			g.only = append(g.only, word)
		}
	}
	return g
}

// wants reports whether lines of a kind are kept.
func (g *fileLog) wants(kind string) bool {
	for _, n := range g.not {
		if n == kind {
			return false
		}
	}
	if len(g.only) == 0 {
		return true
	}
	for _, o := range g.only {
		if o == kind {
			return true
		}
	}
	return false
}

// logf writes one line of a kind.
func (g *fileLog) logf(kind, format string, args ...any) {
	if g == nil {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.f == nil || !g.wants(kind) {
		return
	}
	text := strings.Join(strings.Fields(fmt.Sprintf(format, args...)), " ")
	fmt.Fprintf(g.f, "%s %s %-9s %s\n", g.now().UTC().Format("2006-01-02T15:04:05.000Z"), shortID(g.session), kind, text)
}

// debugf writes one line of a kind when the log was asked for more.
func (g *fileLog) debugf(kind, format string, args ...any) {
	if g == nil || !g.debug.Load() {
		return
	}
	g.logf(kind, format, args...)
}

// close closes the log.
func (g *fileLog) close() {
	if g == nil {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.f != nil {
		_ = g.f.Close()
		g.f = nil
	}
}

// shortID is the start of an id, enough to tell sessions apart in a log.
func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

// event keeps a line of what happened in the session. The pieces of a
// reply and of a command's output as they arrive are not kept: the reply
// and the result that follow hold them whole, in the session's file.
func (g *fileLog) event(ev any) {
	if g == nil {
		return
	}
	switch ev := ev.(type) {
	case *agent.TurnStartEvent:
		g.logf("turn", "turn %s started for %d prompt(s)", shortID(ev.Turn), len(ev.Prompts))
	case *agent.TurnEndEvent:
		r := ev.Result
		g.logf("turn", "turn %s ended: %s %s (%d replies, %s, %s waiting on the model)", shortID(r.Turn), r.Reason, r.Detail, r.Rounds, r.Duration.Round(time.Millisecond), r.APIDuration.Round(time.Millisecond))
	case *agent.RequestEvent:
		g.debugf("model", "request (%s) round %d try %d to %s/%s: %d messages, about %d tokens", ev.Purpose, ev.Round, ev.Attempt, ev.Provider, ev.Model, ev.Messages, ev.Estimate)
	case *agent.ReplyEvent:
		g.logf("model", "reply (%s) round %d from %s/%s: stop %s, prompt %d (cached %d), written %d, first piece after %s, %s in all", ev.Purpose, ev.Round, ev.Provider, ev.Model,
			ev.StopReason, ev.Usage.PromptTokens(), ev.Usage.CacheReadTokens, ev.Usage.OutputTokens, ev.FirstPiece.Round(time.Millisecond), ev.Duration.Round(time.Millisecond))
	case *agent.RetryEvent:
		g.logf("model", "%s", ev.Message)
	case *agent.ToolStartEvent:
		if g.noArgs[ev.Name] {
			g.debugf("tools", "%s %s starts", ev.Name, ev.CallID)
			break
		}
		g.debugf("tools", "%s %s starts: %s", ev.Name, ev.CallID, firstOf(ev.Action.Summary, string(ev.Input)))
	case *agent.ToolEndEvent:
		how := "ok"
		switch {
		case ev.Denied:
			how = "refused"
		case ev.Interrupted:
			how = "stopped"
		case ev.Result.IsError:
			how = "failed"
		}
		if g.noArgs[ev.Name] {
			// What such a tool was given and what it answered may be
			// something the person later asks to be forgotten: the log
			// says that the call was made, and no more.
			g.logf("tools", "%s %s %s after %s", ev.Name, ev.CallID, how, ev.Duration.Round(time.Millisecond))
			break
		}
		g.logf("tools", "%s %s %s after %s: %s", ev.Name, ev.CallID, how, ev.Duration.Round(time.Millisecond), toolLine(ev.Name, ev.Input))
	case *agent.NoticeEvent:
		if g.fromFeature != nil && g.fromFeature(ev.Source) {
			g.logf("notice", "%s (%s): a line of %d characters, shown to the person and kept in the session's file", ev.Level, ev.Source, len(ev.Text))
			break
		}
		g.logf("notice", "%s (%s): %s", ev.Level, ev.Source, ev.Text)
	case *agent.QuestionEvent:
		// A question's heading says what was asked; its full text can
		// quote a call's arguments, and is not kept.
		g.logf("question", "asked (%s): %s", firstOf(ev.Question.Kind, "question"), firstOf(ev.Question.Title, "(no heading)"))
	case *agent.AnswerEvent:
		// Which choice was taken, and never the words that were typed:
		// they may be a secret gnosh asked for.
		g.logf("question", "answered: %s (taken back: %v)", firstOf(ev.Answer.Choice, "(typed)"), ev.Withdrawn)
	case *TasksEvent:
		done := 0
		for _, t := range ev.Tasks {
			if t.Status == TaskCompleted {
				done++
			}
		}
		// How long the list is, and not what it says.
		g.logf("turn", "the task list has %d lines, %d of them done", len(ev.Tasks), done)
	case *agent.DriverEvent:
		g.logf("turn", "the check %s holds the turn open (%d in a row, checkpoint %v): %s", ev.Name, ev.Streak, ev.Checkpoint, ev.Objection)
	case *agent.SummaryEvent:
		g.logf("context", "summary %s (%s): %d tokens before, %d after%s", ev.Phase, ev.Trigger, ev.PreTokens, ev.PostTokens, failedWith(ev.Err))
	case *agent.RoomEvent:
		g.logf("context", "about %d tokens slid out of the view: %s", ev.Freed, ev.Notice)
	case *agent.ContextEvent:
		g.debugf("context", "about %d of %d tokens (room is made at %d)", ev.Context.Used, ev.Context.Window, ev.Context.Point)
	case *agent.SideEvent:
		g.logf("model", "side request (%s) to %s/%s took %s: prompt %d (cached %d), written %d%s", ev.Purpose, ev.Provider, ev.Model, ev.Duration.Round(time.Millisecond),
			ev.Usage.PromptTokens(), ev.Usage.CacheReadTokens, ev.Usage.OutputTokens, failedWith(ev.Err))
	case *agent.LoopEvent:
		g.logf("turn", "/loop on %v after %d turns: %s", ev.On, ev.Turns, ev.Why)
	case *agent.PromptReturnedEvent:
		g.logf("turn", "a prompt was not sent (%s): %s", ev.Why, ev.Reason)
	case *agent.LostEvent:
		g.logf("notice", "%d pieces of live output (%d bytes) were not shown: the front end fell behind", ev.Events, ev.Bytes)
	case *HookEvent:
		for _, r := range ev.Records {
			if r.Result == hooks.ResultNone {
				g.debugf("hooks", "%s", r.String())
			} else {
				g.logf("hooks", "%s", r.String())
			}
		}
	case *CapEvent:
		g.logf("turn", "the run reached its %d turns (--max-turns) and is stopped there", ev.Turns)
	}
}

// failedWith is ", failed: <err>" for a log line, or nothing when err is
// nil: a line that says "error <nil>" each time a thing went right is one
// more to pass over when looking for what went wrong.
func failedWith(err error) string {
	if err == nil {
		return ""
	}
	return ", failed: " + err.Error()
}

// crashFile is where a fatal error of the Go runtime, and a panic gnosh
// caught, are written, so that what happened is still there when the
// screen has scrolled on. It lives beside the project's sessions. A nil
// crashFile records nothing.
type crashFile struct {
	mu   sync.Mutex
	path string
	f    *os.File
	own  bool // the runtime was pointed at the file
}

// openCrashFile makes the session's crash file, and when this run is the
// whole process, points the Go runtime at it and holds the file's lock
// while it runs. An earlier crash that nobody has been told of is told
// now. An empty crash file nobody holds is a run's that ended without
// closing it (killed, or the machine went down): it is removed, so that a
// folder does not fill up with files that look like crashes.
func openCrashFile(p *program, l *live) *crashFile {
	dir := l.crashDir()
	if dir == "" || os.MkdirAll(dir, 0o700) != nil {
		return nil
	}
	if old, _ := filepath.Glob(filepath.Join(dir, "crash-*.log")); len(old) > 0 {
		for _, path := range old {
			info, err := os.Stat(path)
			if err != nil {
				continue
			}
			if info.Size() == 0 {
				if !transcript.InUse(path) {
					_ = os.Remove(path)
				}
				continue
			}
			l.notice(agent.LevelWarning, "crash", fmt.Sprintf("Something in gnosh crashed in this folder on %s. What was written down about it is in %s.",
				info.ModTime().Local().Format("2006-01-02 at 15:04"), strings.TrimSuffix(path, ".log")+".seen"))
			_ = os.Rename(path, strings.TrimSuffix(path, ".log")+".seen")
		}
	}
	c := &crashFile{path: filepath.Join(dir, "crash-"+p.now().UTC().Format("20060102T150405")+"-"+shortID(l.ts.ID())+".log")}
	if p.o.Exit == nil {
		// Not the whole process (a test, an embedding program): the file is
		// made when something is written to it.
		return c
	}
	f, err := os.OpenFile(c.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return c
	}
	c.f = f
	transcript.Hold(f)
	if debug.SetCrashOutput(f, debug.CrashOptions{}) == nil {
		c.own = true
	}
	return c
}

// record writes a panic gnosh caught to the crash file and returns the
// file's path, or "" when nothing could be written.
func (c *crashFile) record(what string, v any, stack []byte) string {
	if c == nil {
		return ""
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.f == nil {
		f, err := os.OpenFile(c.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		if err != nil {
			return ""
		}
		c.f = f
	}
	fmt.Fprintf(c.f, "%s %s panicked: %v\n%s\n", time.Now().UTC().Format(time.RFC3339), firstOf(what, "gnosh"), v, stack)
	_ = c.f.Sync()
	return c.path
}

// close lets go of the crash file. A file nothing was written to is
// removed.
func (c *crashFile) close() {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.own {
		_ = debug.SetCrashOutput(nil, debug.CrashOptions{})
		c.own = false
	}
	if c.f == nil {
		return
	}
	info, err := c.f.Stat()
	_ = c.f.Close()
	c.f = nil
	if err == nil && info.Size() == 0 {
		_ = os.Remove(c.path)
	}
}

// crashed is what happens when gnosh itself panics: what it can say is
// written to a crash file, the person is told where that is and how to get
// the session back, and the program ends with a code of its own. dir is
// the folder to write to, or "" when no session was open yet.
func (p *program) crashed(dir string, v any, stack []byte) int {
	if dir == "" {
		dir = os.TempDir()
	}
	path := filepath.Join(dir, "crash-"+p.now().UTC().Format("20060102T150405")+"-"+strconv.Itoa(os.Getpid())+".log")
	text := fmt.Sprintf("%s gnosh %s panicked: %v\n%s\n", p.now().UTC().Format(time.RFC3339), p.versionLine(), v, stack)
	where := "It could not write down what happened (" + path + " could not be written)."
	if err := os.MkdirAll(dir, 0o700); err == nil {
		if err := os.WriteFile(path, []byte(text), 0o600); err == nil {
			where = "What happened is written in " + path + "."
		}
	}
	p.errf("gnosh hit a fault of its own and stopped (%v). %s The session's file is kept as it was: gnosh -c continues it.", v, where)
	return 2
}

// debugPage is the debugging page of GNOSH_DEBUG_PAGE: with it a session
// that seems stuck can be looked at without being killed.
type debugPage struct {
	ln  net.Listener
	srv *http.Server
}

// debugPagePort is the port the debugging page is served on when it is
// free.
const debugPagePort = 6060

// startDebugPage serves, on loopback only, what is needed to see where a
// session is stuck: what it says it is doing, every goroutine's stack, and
// a short CPU profile. When the usual port is taken a free one is used,
// and the port is in the log and told to the person.
func startDebugPage(l *live) *debugPage {
	ln, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(debugPagePort))
	if err != nil {
		if ln, err = net.Listen("tcp", "127.0.0.1:0"); err != nil {
			l.notice(agent.LevelWarning, "debug-page", fmt.Sprintf("GNOSH_DEBUG_PAGE is set, but the debugging page could not be served (%v).", err))
			return nil
		}
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		st, ctx := l.agent.State(), l.agent.Context()
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		fmt.Fprintf(w, "gnosh %s\nsession %s\nstarted %s\n\n", l.p.versionLine(), l.ts.ID(), l.p.start.Format(time.RFC3339))
		fmt.Fprintf(w, "busy: %v\nphase: %s\nwaiting for: %s\nsince: %s\nturn: %s, round %d\nqueued prompts: %d\n", st.Busy, st.Phase, st.Waiting, st.Since.Format(time.RFC3339), st.Turn, st.Round, st.Queued)
		for _, c := range st.Running {
			fmt.Fprintf(w, "running: %s %s since %s\n", c.Name, c.CallID, c.Since.Format(time.RFC3339))
		}
		fmt.Fprintf(w, "context: about %d of %d tokens\n\n/goroutines  every goroutine's stack\n/cpu?seconds=5  a CPU profile, for go tool pprof\n", ctx.Used, ctx.Window)
	})
	mux.HandleFunc("/goroutines", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_ = pprof.Lookup("goroutine").WriteTo(w, 2)
	})
	mux.HandleFunc("/cpu", func(w http.ResponseWriter, r *http.Request) {
		secs, err := strconv.Atoi(r.URL.Query().Get("seconds"))
		if err != nil || secs < 1 || secs > 120 {
			secs = 5
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		if err := pprof.StartCPUProfile(w); err != nil {
			http.Error(w, "a CPU profile is already being taken", http.StatusConflict)
			return
		}
		select {
		case <-time.After(time.Duration(secs) * time.Second):
		case <-r.Context().Done():
		}
		pprof.StopCPUProfile()
	})
	guarded := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Only this machine, asked directly: a page on another site whose
		// name points here is refused.
		host, _, err := net.SplitHostPort(r.Host)
		if err != nil {
			host = r.Host
		}
		if (host != "127.0.0.1" && host != "localhost" && host != "::1") || r.Header.Get("Origin") != "" {
			http.Error(w, "this page is for this machine only", http.StatusForbidden)
			return
		}
		mux.ServeHTTP(w, r)
	})
	dp := &debugPage{ln: ln, srv: &http.Server{Handler: guarded, ReadHeaderTimeout: 10 * time.Second}}
	go func() { _ = dp.srv.Serve(ln) }()
	l.flog.logf("startup", "GNOSH_DEBUG_PAGE: the debugging page is at http://%s/", ln.Addr())
	l.notice(agent.LevelInfo, "debug-page", fmt.Sprintf("GNOSH_DEBUG_PAGE is set: the debugging page is at http://%s/ (this machine only).", ln.Addr()))
	return dp
}

// close stops the debugging page.
func (dp *debugPage) close() {
	if dp == nil {
		return
	}
	_ = dp.srv.Close()
}

// toolLine is a tool call on one line, for a log and for --verbose: the
// tool's name and the argument that says most about the call. A command
// for another machine names the machine.
func toolLine(name string, input []byte) string {
	var args map[string]any
	if json.Unmarshal(input, &args) != nil {
		return name
	}
	if host, _ := args["host"].(string); name == shelltool.BashName {
		if host = strings.TrimSpace(host); host != "" && host != shelltool.LocalName {
			name += " on " + oneLine(host, 60)
		}
	}
	if label, _ := args["label"].(string); name == DeliverToolName && strings.TrimSpace(label) != "" {
		// What is claimed or delivered, and the file or the address when
		// the call gives one.
		line := name + ": " + oneLine(label, 80)
		if target, _ := args["target"].(string); strings.TrimSpace(target) != "" {
			line += " (" + oneLine(target, 200)
			if host, _ := args["host"].(string); strings.TrimSpace(host) != "" && strings.TrimSpace(host) != shelltool.LocalName {
				line += " on " + oneLine(host, 60)
			}
			line += ")"
		}
		return line
	}
	if op, _ := args["operation"].(string); name == lspToolName && op != "" {
		// What is asked, and where: the file, at line:character.
		line := name + ": " + oneLine(op, 40)
		if q, _ := args["query"].(string); q != "" {
			line += " " + oneLine(q, 80)
		}
		if p, _ := args["filePath"].(string); p != "" {
			line += " " + oneLine(p, 200)
			if n, ok := args["line"].(float64); ok && n >= 1 {
				line += fmt.Sprintf(":%d", int(n))
				if c, ok := args["character"].(float64); ok && c >= 1 {
					line += fmt.Sprintf(":%d", int(c))
				}
			}
		}
		return line
	}
	for _, key := range []string{"command", "file_path", "pattern", "path", "url", "task_id", "code", "description", "name"} {
		if v, ok := args[key].(string); ok && v != "" {
			return name + ": " + oneLine(v, 200)
		}
	}
	return name + ": " + oneLine(string(input), 200)
}

// resultLine is a tool call's result on one line: how it ended, with the
// fact a person looks for first.
func resultLine(res core.ToolResult, denied, interrupted bool) string {
	switch {
	case denied:
		return "refused: " + oneLine(core.TextOf(res.Content), 200)
	case interrupted:
		return "stopped"
	}
	var parts []string
	if code, ok := metaInt(res.Meta, "exitCode"); ok {
		parts = append(parts, fmt.Sprintf("exit code %d", code))
	}
	if d := res.Display; d != nil && d.Kind == "diff" {
		added, removed := diffCounts(d.Before, d.After)
		parts = append(parts, fmt.Sprintf("+%d -%d %s", added, removed, firstOf(d.Path, d.Title)))
	}
	if res.IsError {
		text := core.TextOf(res.Content)
		if _, shell := res.Meta["host"]; shell {
			// A command's result begins with the line that tells the
			// model where it ran; a person has been told by the call's
			// own line.
			_, text = shelltool.SplitStamp(text)
		}
		parts = append(parts, "failed: "+oneLine(text, 200))
	}
	if len(parts) == 0 {
		return "ok"
	}
	return strings.Join(parts, ", ")
}

// metaInt reads a whole number out of a result's metadata, as a tool put
// it there or as JSON gave it back.
func metaInt(meta map[string]any, key string) (int, bool) {
	switch v := meta[key].(type) {
	case int:
		return v, true
	case int64:
		return int(v), true
	case float64:
		return int(v), true
	}
	return 0, false
}

// diffCounts counts the lines a change added and took away, the plain way:
// what is in one side and not in the other, line by line.
func diffCounts(before, after string) (added, removed int) {
	count := func(s string) map[string]int {
		m := map[string]int{}
		if s == "" {
			return m
		}
		for _, line := range strings.Split(strings.TrimSuffix(s, "\n"), "\n") {
			m[line]++
		}
		return m
	}
	b, a := count(before), count(after)
	for line, n := range a {
		if d := n - b[line]; d > 0 {
			added += d
		}
	}
	for line, n := range b {
		if d := n - a[line]; d > 0 {
			removed += d
		}
	}
	return added, removed
}
