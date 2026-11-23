package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gnosh-ai/gnosh/internal/agent"
	"github.com/gnosh-ai/gnosh/internal/config"
	"github.com/gnosh-ai/gnosh/internal/lsp"
	"github.com/gnosh-ai/gnosh/internal/prompt"
)

// This file is the session's language servers (the lspServers key, Claude
// Code's .lsp.json entries): each is started the first time a file of one
// of its extensions is read or changed, in a trusted folder only, is told
// of the files the model reads and changes, and after each change gives
// the model the errors and warnings the change brought, as Claude Code's
// code intelligence does ("Found N new diagnostic issues in M files"). The
// LSP tool asks them where symbols are (lsptool.go). They stop with the
// session.

// lspSettle is how long a change waits for its diagnostics, and lspQuiet
// the quiet after them that says the server has said all it will.
const (
	lspSettle = 3 * time.Second
	lspQuiet  = 300 * time.Millisecond
	// lspEditWait bounds what one change may wait for its server, a start
	// included: one still starting goes on starting, and the change gets
	// no diagnostics this time.
	lspEditWait = 10 * time.Second
	// lspRetry is how long a server that could not start is let be before
	// it is tried again (after the person installed it, say).
	lspRetry = time.Minute
	// lspFileMost is the largest file a server is told of.
	lspFileMost = 4 << 20
	// lspIssuesShown is the most new issues the model is told of at once,
	// and lspMessageMost the longest message of one.
	lspIssuesShown = 20
	lspMessageMost = 400
	// lspRefreshMost is the most files told of again before a change, when
	// they changed on disk meanwhile.
	lspRefreshMost = 64
)

// errLSPUntrusted is why no server starts in a folder that is not trusted:
// a language server reads the folder's code and may run some of it (build
// scripts, macros), and a relative command would be the folder's own file
// (the language servers review of 2026-11-20).
var errLSPUntrusted = errors.New("the folder is not trusted, and a language server reads, and may run, its code")

// lspServers is the session's language servers.
type lspServers struct {
	l    *live
	list []*lspServer
	mu   sync.Mutex
	// closed says the session has stopped its servers: none starts again.
	// mu also guards each server's base.
	closed bool
	// toldUntrusted says the person was told the servers wait for trust.
	toldUntrusted bool
}

// lspServer is one configured server, started or not.
type lspServer struct {
	cfg config.LSPServer
	// mu guards what follows; it is never held across a start or a close.
	mu       sync.Mutex
	client   *lsp.Client
	starting chan struct{} // closed when the start under way ends; nil when none is
	failed   error         // the last start's error, until lspRetry has passed
	failedAt time.Time
	gaveUp   error // it stopped, and restartOnCrash or maxRestarts says to leave it
	told     bool  // the person was told it could not start
	restarts int   // starts after the first, after it stopped on its own
	// editing is one slot: one change is synced and its diagnostics awaited
	// at a time, so that what was before it and what came after are told
	// apart.
	editing chan struct{}
	// base is each file's diagnostics from this server as the model last
	// knew them, or as they were when the file was read: a change's new
	// problems are what is not in it, and a problem the server published
	// late, after the change before, is told with the next (the language
	// servers review). Each server has its own: one server's change once
	// took another's files out of a shared one (the second review).
	// lspServers.mu guards it.
	base map[string][]lsp.Diagnostic
}

// newLSPServers makes the session's servers, none started.
func newLSPServers(l *live, cfgs []config.LSPServer) *lspServers {
	s := &lspServers{l: l}
	for _, c := range cfgs {
		s.list = append(s.list, &lspServer{cfg: c, editing: make(chan struct{}, 1), base: map[string][]lsp.Diagnostic{}})
	}
	return s
}

// serverFor is the server for the file at path and the file's language,
// or nil: the first in the list that names its extension.
func (s *lspServers) serverFor(path string) (*lspServer, string) {
	if s == nil {
		return nil, ""
	}
	ext := strings.ToLower(filepath.Ext(path))
	for _, srv := range s.list {
		if lang, ok := srv.cfg.Extensions[ext]; ok {
			return srv, lang
		}
	}
	return nil, ""
}

// trusted reports whether servers may start in this folder, and tells the
// person once when they may not.
func (s *lspServers) trusted() bool {
	if s.l.st == nil || s.l.st.trusted {
		return true
	}
	s.mu.Lock()
	told := s.toldUntrusted
	s.toldUntrusted = true
	s.mu.Unlock()
	if !told {
		s.l.notice(agent.LevelInfo, "lsp", "The language servers wait for the folder to be trusted: a server reads, and may run, the folder's own code.")
	}
	return false
}

// clientOf is srv's client, started if it is not: the start runs in the
// background with its own time limit (startupTimeout), and this waits for
// it only as long as ctx lets; a caller that stops waiting leaves the
// start going, and is no failed start. A server that stopped on its own is
// started again as restartOnCrash and maxRestarts allow; one that could
// not start is tried again after lspRetry.
func (s *lspServers) clientOf(ctx context.Context, srv *lspServer) (*lsp.Client, error) {
	if !s.trusted() {
		return nil, errLSPUntrusted
	}
	for {
		s.mu.Lock()
		closed := s.closed
		s.mu.Unlock()
		if closed {
			return nil, errors.New("the session's language servers have stopped")
		}
		srv.mu.Lock()
		if c := srv.client; c != nil && c.Err() == nil {
			srv.mu.Unlock()
			return c, nil
		}
		if old := srv.client; old != nil {
			srv.client = nil
			go old.Close()
			switch {
			case !srv.cfg.RestartOnCrash:
				srv.gaveUp = fmt.Errorf("it stopped (%v), and its restartOnCrash is false", old.Err())
			case srv.cfg.MaxRestarts >= 0 && srv.restarts >= srv.cfg.MaxRestarts:
				srv.gaveUp = fmt.Errorf("it stopped (%v) after %s, its maxRestarts", old.Err(), countWords(srv.restarts, "restart", "restarts"))
			default:
				srv.restarts++
				s.l.flog.logf("lsp", "%s stopped (%v); starting it again", srv.cfg.Name, old.Err())
			}
		}
		if srv.gaveUp != nil {
			err := srv.gaveUp
			srv.mu.Unlock()
			return nil, err
		}
		if srv.starting == nil {
			if srv.failed != nil && s.l.p.now().Sub(srv.failedAt) < lspRetry {
				err := srv.failed
				srv.mu.Unlock()
				return nil, err
			}
			srv.starting = make(chan struct{})
			s.l.background(func(ctx context.Context) { s.start(ctx, srv) })
		}
		wait := srv.starting
		srv.mu.Unlock()
		select {
		case <-wait:
		case <-ctx.Done():
			return nil, fmt.Errorf("%s is still starting", srv.cfg.Name)
		}
	}
}

// start starts srv, in the session's background: its result is for the
// callers that wait, and for the next.
func (s *lspServers) start(ctx context.Context, srv *lspServer) {
	root := srv.cfg.WorkspaceFolder
	if root == "" {
		root = s.root()
	}
	// The program's environment with the settings' env block over it, as
	// the commands gnosh starts have it; the project's folder, as Claude
	// Code exports it to a language server; then the entry's own.
	more := []string{"CLAUDE_PROJECT_DIR=" + root, "GNOSH_PROJECT_DIR=" + root}
	for k, v := range srv.cfg.Env {
		more = append(more, k+"="+v)
	}
	c, err := lsp.Start(ctx, lsp.Config{Name: srv.cfg.Name, Command: srv.cfg.Command, Args: srv.cfg.Args,
		Env: overlayEnv(s.l.ownCommandEnv(), more), Dir: root, Root: root, InitOptions: srv.cfg.InitOptions,
		Settings: srv.cfg.Settings, StartWait: srv.cfg.StartupTimeout, StopWait: srv.cfg.ShutdownTimeout})
	s.mu.Lock()
	closed := s.closed
	s.mu.Unlock()
	srv.mu.Lock()
	tell := false
	switch {
	case closed:
		if c != nil {
			go c.Close()
		}
	case err != nil:
		srv.failed, srv.failedAt = err, s.l.p.now()
		tell, srv.told = !srv.told, true
	default:
		srv.client, srv.failed = c, nil
		s.l.flog.logf("lsp", "%s started for %s", srv.cfg.Name, root)
	}
	close(srv.starting)
	srv.starting = nil
	srv.mu.Unlock()
	if tell {
		s.l.notice(agent.LevelWarning, "lsp", fmt.Sprintf("The language server %s could not start: %v. Files it is for get no diagnostics until it can.", srv.cfg.Name, err))
	}
}

// edit takes srv's one slot for a change, as long as ctx lets.
func (srv *lspServer) edit(ctx context.Context) (release func(), ok bool) {
	select {
	case srv.editing <- struct{}{}:
		return func() { <-srv.editing }, true
	case <-ctx.Done():
		return nil, false
	}
}

// readFileFor is the text of the file at path for a server, or false for
// one too large or not there.
func readFileFor(path string) (string, bool) {
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > lspFileMost {
		return "", false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	return string(data), true
}

// refresh tells the server again of the files it was told of that changed
// on disk since (a command, an editor, git), which a server reads from
// what it was told and not from the disk: but for skip, which the caller
// sends itself.
func (s *lspServers) refresh(ctx context.Context, c *lsp.Client, skip string) {
	for i, p := range c.Open() {
		if i == lspRefreshMost || ctx.Err() != nil {
			return
		}
		if p == skip {
			continue
		}
		_, lang := s.serverFor(p)
		text, ok := readFileFor(p)
		if old, told := c.Text(p); ok && told && old != text {
			_, _ = c.Sync(ctx, p, lang, text)
		}
	}
}

// opened tells the file's server of a file the model read, away from the
// call, so that what the server says of it before a change is known when
// the change comes: that is the file's base. The text is taken now, as
// the model read it: one taken later could already be a change's; and
// nothing is sent once a change has told the server of the file.
func (s *lspServers) opened(path string) {
	srv, lang := s.serverFor(path)
	if srv == nil {
		return
	}
	text, ok := readFileFor(path)
	if !ok {
		return
	}
	s.l.background(func(ctx context.Context) {
		ctx, cancel := context.WithTimeout(ctx, lspEditWait+lspSettle)
		defer cancel()
		c, err := s.clientOf(ctx, srv)
		if err != nil {
			return
		}
		release, ok := srv.edit(ctx)
		if !ok {
			return
		}
		defer release()
		if _, told := c.Text(path); told {
			return
		}
		synced, err := c.Sync(ctx, path, lang, text)
		if err != nil || !synced.Sent {
			return
		}
		settle, done := context.WithTimeout(ctx, lspSettle)
		defer done()
		if c.Settle(settle, path, synced, lspQuiet) {
			s.mu.Lock()
			srv.base[path] = c.Diagnostics()[path]
			s.mu.Unlock()
		}
	})
}

// changed tells the file's server of a file a call changed, waits for
// what it says, and returns the new errors and warnings since the model
// last knew (the base), in this file and others, for the model; the person
// is told how many.
func (s *lspServers) changed(ctx context.Context, path string) ([]agent.Part, string) {
	srv, lang := s.serverFor(path)
	if srv == nil {
		return nil, ""
	}
	ctx, cancel := context.WithTimeout(ctx, lspEditWait)
	defer cancel()
	c, err := s.clientOf(ctx, srv)
	if err != nil {
		return nil, ""
	}
	release, ok := srv.edit(ctx)
	if !ok {
		return nil, ""
	}
	defer release()
	// The text is read once the slot is held: read before, it could be
	// older than another change's that took the slot first, and be sent
	// as the newer (the second review).
	text, ok := readFileFor(path)
	if !ok {
		return nil, ""
	}
	s.mu.Lock()
	if _, known := srv.base[path]; !known {
		// What the server said of the file before this change, when it
		// said it too late for opened (a first load can take a while):
		// otherwise the file's old problems would count as new.
		if d, said := c.Diagnostics()[path]; said {
			srv.base[path] = d
		}
	}
	s.mu.Unlock()
	s.refresh(ctx, c, path)
	synced, err := c.Sync(ctx, path, lang, text)
	if err != nil || !synced.Sent || !srv.cfg.Diagnostics {
		// (With diagnostics off the server is still told of the change,
		// for the LSP tool's questions.)
		return nil, ""
	}
	settle, done := context.WithTimeout(ctx, lspSettle)
	c.Settle(settle, path, synced, lspQuiet)
	done()
	after := c.Diagnostics()
	s.mu.Lock()
	issues := newIssues(srv.base, after)
	for p := range srv.base {
		if _, still := after[p]; !still {
			delete(srv.base, p)
		}
	}
	for p, d := range after {
		srv.base[p] = d
	}
	s.mu.Unlock()
	// What a file the Read deny rules or the read block keep from the
	// model holds is not told, its problems' words among it: they may
	// quote it.
	issues = slices.DeleteFunc(issues, func(is issue) bool { return s.l.engine != nil && s.l.engine.ReadDenied(is.path) })
	if len(issues) == 0 {
		return nil, ""
	}
	files := map[string]bool{}
	for _, is := range issues {
		files[is.path] = true
	}
	// (The person's line goes under the call, as Claude Code puts it.)
	note := fmt.Sprintf("Found %s in %s (%s).",
		countWords(len(issues), "new diagnostic issue", "new diagnostic issues"), countWords(len(files), "file", "files"), srv.cfg.Name)
	lead, list := s.issuesText(srv.cfg.Name, issues)
	// (gnosh's line in its own words; the server's, which may quote the
	// repository's text, outside them.)
	return []agent.Part{{Source: "lsp", Text: prompt.Wrap(lead) + "\n" + prompt.Untrusted(list)}}, note
}

// issue is one diagnostic a change brought.
type issue struct {
	path string
	d    lsp.Diagnostic
}

// newIssues is the errors and warnings of after that were not in before:
// the same severity, message, source and code in the same file count as
// the same issue however its place moved, as many times as before had it.
func newIssues(before, after map[string][]lsp.Diagnostic) []issue {
	key := func(d lsp.Diagnostic) string {
		return fmt.Sprintf("%d\x00%s\x00%s\x00%s", d.Severity, d.Message, d.Source, d.Code)
	}
	var out []issue
	paths := make([]string, 0, len(after))
	for p := range after {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		had := map[string]int{}
		for _, d := range before[p] {
			had[key(d)]++
		}
		for _, d := range after[p] {
			if d.Severity != lsp.SeverityError && d.Severity != lsp.SeverityWarning && d.Severity != 0 {
				continue
			}
			if k := key(d); had[k] > 0 {
				had[k]--
				continue
			}
			out = append(out, issue{path: p, d: d})
		}
	}
	return out
}

// issuesText words the new issues for the model: a line of gnosh's own,
// and the list, errors first, at most lspIssuesShown of them, each at its
// 1-based line and character.
func (s *lspServers) issuesText(server string, issues []issue) (lead, list string) {
	sort.SliceStable(issues, func(i, j int) bool {
		return issues[i].d.Severity == lsp.SeverityError && issues[j].d.Severity != lsp.SeverityError
	})
	lead = fmt.Sprintf("The language server %s reports %s after this change:", server, countWords(len(issues), "new problem", "new problems"))
	var b strings.Builder
	lines := map[string][]string{}
	for i, is := range issues {
		if i == lspIssuesShown {
			fmt.Fprintf(&b, "... and %d more.\n", len(issues)-i)
			break
		}
		if _, ok := lines[is.path]; !ok {
			lines[is.path] = s.l.lspLines(is.path)
		}
		at := is.d.Range.Start
		lineText := ""
		if ls := lines[is.path]; at.Line >= 0 && at.Line < len(ls) {
			lineText = ls[at.Line]
		}
		line, char := lsp.ToRunes(at, lineText)
		sev := is.d.Severity
		if sev == 0 {
			sev = lsp.SeverityError
		}
		msg := clipText(strings.Join(strings.Fields(is.d.Message), " "), lspMessageMost)
		if src := clipText(strings.Join(strings.Fields(is.d.Source), " "), 40); src != "" {
			msg += " (" + src + ")"
		}
		fmt.Fprintf(&b, "%s:%d:%d: %s: %s\n", s.l.shownPath(is.path), line, char, sev, msg)
	}
	return lead, b.String()
}

// lspLines is the lines of a file a server named, to place what it says
// at a line and character and show that line: none of a file the Read
// deny rules or the read block keep from the model (a server may name any
// file), which is then placed by the protocol's own count.
func (l *live) lspLines(path string) []string {
	if l.engine != nil && l.engine.ReadDenied(path) {
		return nil
	}
	text, _ := readFileFor(path)
	return strings.Split(text, "\n")
}

// shownPath is a path as the model is shown it: relative to the working
// directory when inside it.
func (l *live) shownPath(path string) string {
	if rel, err := filepath.Rel(l.cwd(), path); err == nil && !strings.HasPrefix(rel, "..") {
		return rel
	}
	return path
}

// close stops every server that was started, and keeps any from starting
// again.
func (s *lspServers) close() {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
	var wg sync.WaitGroup
	for _, srv := range s.list {
		srv.mu.Lock()
		c := srv.client
		srv.client = nil
		srv.mu.Unlock()
		if c != nil {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_ = c.Close()
			}()
		}
	}
	wg.Wait()
}

// lspPath is the file a finished call read or changed, from what the tool
// said of it (the result's filePath, Claude Code's), and whether it
// changed it.
func lspPath(done agent.ToolDone) (path string, change bool) {
	if done.Result.IsError || done.Interrupted {
		return "", false
	}
	switch done.Call.Name {
	case "Write", "Edit", "NotebookEdit":
		change = true
	case "Read":
	default:
		return "", false
	}
	meta := done.Result.Meta
	if p, ok := meta["filePath"].(string); ok {
		return p, change
	}
	if f, ok := meta["file"].(map[string]any); ok {
		if p, ok := f["filePath"].(string); ok {
			return p, change
		}
	}
	var in struct {
		FilePath string `json:"file_path"`
	}
	if json.Unmarshal(done.Call.Input, &in) == nil && filepath.IsAbs(in.FilePath) {
		return in.FilePath, change
	}
	return "", false
}
