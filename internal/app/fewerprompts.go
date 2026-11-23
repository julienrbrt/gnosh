package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/gnosh-ai/gnosh/internal/agent"
	"github.com/gnosh-ai/gnosh/internal/core"
	"github.com/gnosh-ai/gnosh/internal/permission"
	"github.com/gnosh-ai/gnosh/internal/tools/shelltool"
	"github.com/gnosh-ai/gnosh/internal/transcript"
)

// /fewer-permission-prompts (DECISIONS 376), as Claude Code's: what in
// this folder's sessions would be asked about now, and the allow rules
// that would let it run without a question, the most used first, for the
// person to keep the ones that only read. Claude Code's has the model scan
// the transcripts and judge what reads; gnosh's own engine judges each
// call the sessions made, as manual mode would today, and offers the rule
// its question would have offered: the person, not a model, says which
// rules only read.

const (
	fewerFiles = 200  // the newest session files looked through
	fewerCalls = 5000 // the calls judged, at most
	fewerShown = 20   // the rules listed
)

// askedRule is a set of allow rules, kept together, that would let calls
// the folder's sessions made run without a question, with how many.
type askedRule struct {
	rules   []string
	calls   int
	example string // one such call, as it was written
}

// slashFewerPrompts is /fewer-permission-prompts: the list, or with add
// and its numbers (or all), the rules kept, as /permissions allow keeps
// one.
func (l *live) slashFewerPrompts(ctx context.Context, c *SlashContext) error {
	args := strings.Fields(c.Args)
	if len(args) > 0 {
		if args[0] != "add" || len(args) < 2 {
			return errors.New("/fewer-permission-prompts takes nothing, to list the rules, or add and the numbers of the ones to keep (or all)")
		}
		l.fewerMu.Lock()
		last := l.fewerLast
		l.fewerMu.Unlock()
		if len(last) == 0 {
			// The numbers are those of a list the person has seen: worked
			// out again here, newer sessions could have moved them, and a
			// rule never shown would be kept (the review of 2026-11-20).
			return errors.New("the numbers are those of the list /fewer-permission-prompts gives, and it has given none in this session yet: list first, or keep a rule by its words with /permissions allow <rule>")
		}
		var pick []askedRule
		if len(args) == 2 && args[1] == "all" {
			pick = last
		} else {
			for _, a := range args[1:] {
				n, err := strconv.Atoi(strings.TrimSuffix(a, ","))
				if err != nil || n < 1 || n > len(last) {
					return fmt.Errorf("%q is not one of the list's numbers (1 to %d)", oneLine(a, 20), len(last))
				}
				pick = append(pick, last[n-1])
			}
		}
		for _, r := range pick {
			for _, rule := range r.rules {
				l.permissionsAct(c, "allow", rule)
			}
		}
		return nil
	}
	found, sessions := l.askedRules(ctx)
	l.fewerMu.Lock()
	l.fewerLast = found
	l.fewerMu.Unlock()
	if len(found) == 0 {
		c.Say("Of the commands, MCP tools and web pages in this folder's last %s, none would be asked about now in manual mode.", lastSessions(sessions))
		return nil
	}
	c.Say("These calls in this folder's last %s would be asked about now in manual mode; an allow rule lets every call it matches run without a question:", lastSessions(sessions))
	for i, r := range found {
		line := fmt.Sprintf("%2d. %s · %s", i+1, visible(strings.Join(r.rules, ", ")), countWords(r.calls, "call", "calls"))
		if r.example != "" {
			line += ", as " + visible(oneLine(r.example, 80))
		}
		c.Say("%s", line)
	}
	c.Say("Keep only what reads and changes nothing: /fewer-permission-prompts add 1 3, or add all. Each is kept in %s, as /permissions allow keeps one; /permissions remove takes one out.", l.tilde(l.cfg.Paths.LocalSettings))
	return nil
}

// askedRules judges the Bash, MCP and WebFetch calls of the folder's
// newest sessions and their sub-agents' as manual mode would now, and
// groups those that would be asked about by the rules their question would
// offer, the most calls first; it says how many sessions it looked
// through.
func (l *live) askedRules(ctx context.Context) ([]askedRule, int) {
	// Only the tools whose calls are judged here: commands, web pages and
	// MCP tools.
	tools := map[string]core.Tool{}
	for _, t := range l.offeredTools() {
		if name := t.Spec().Name; name == shelltool.BashName || name == webFetchName || strings.HasPrefix(name, "mcp__") {
			tools[name] = t
		}
	}
	files, sessions := recentSessionFiles([]string{l.store.ProjectDir(l.cfg.Paths.Cwd), l.store.ProjectDir(l.cfg.Paths.ProjectRoot)})
	byRules := map[string]*askedRule{}
	// The same call, made again (as a loop's are), is judged once: by its
	// tool, where it ran and its arguments.
	judgedAs := map[string]*askedRule{}
	judged := 0
	for _, f := range files {
		if judged >= fewerCalls {
			break // (nothing after can change the list)
		}
		// The calls of the file that would ask now, by their ids, until
		// their results say they ran: one the person refused, or that was
		// cut off, counts for nothing (the review of 2026-11-20: refused
		// calls, made again and again, would have topped the list).
		asking := map[string]*askedRule{}
		for te, err := range transcript.ReadFile(ctx, f, nil) {
			if err != nil || ctx.Err() != nil {
				break
			}
			if te.Type == transcript.TypeAssistant {
				l.judgeCalls(te, tools, byRules, judgedAs, asking, &judged)
				continue
			}
			if len(asking) == 0 || !resultFor(te, asking) {
				continue
			}
			for _, e := range decode(&te) {
				if e.Kind == agent.EntryResult {
					if r := asking[e.ToolUseID]; r != nil && !e.Denied && !e.Interrupted {
						r.calls++
					}
					delete(asking, e.ToolUseID)
				}
			}
		}
	}
	var out []askedRule
	for _, r := range byRules {
		if r.calls > 0 {
			out = append(out, *r)
		}
	}
	slices.SortFunc(out, func(a, b askedRule) int {
		if a.calls != b.calls {
			return b.calls - a.calls
		}
		return slices.Compare(a.rules, b.rules)
	})
	if len(out) > fewerShown {
		out = out[:fewerShown]
	}
	return out, sessions
}

// judgeCalls judges the calls of an assistant entry that would be asked
// about now, as manual mode would, and notes each under the rules its
// question would offer, to be counted once its result says it ran.
func (l *live) judgeCalls(te transcript.Entry, tools map[string]core.Tool, byRules, judgedAs, asking map[string]*askedRule, judged *int) {
	for _, b := range te.Message.ToolUses() {
		t := tools[b.Name]
		if t == nil || *judged >= fewerCalls {
			continue
		}
		*judged++
		cwd := firstOf(te.Cwd, l.cwd())
		key := b.Name + "\x00" + cwd + "\x00" + string(b.Input)
		r, seen := judgedAs[key]
		if !seen {
			r = l.judgeCall(t, b, cwd, byRules)
			judgedAs[key] = r
		}
		if r != nil {
			asking[b.ID] = r
		}
	}
}

// judgeCall is the rules a past call's question would offer, as the call
// would be judged now in manual mode, or nil where it would not ask.
// (Described with no id: a past call is not to be taken for one of this
// session's, which the shell keeps account of by id.)
func (l *live) judgeCall(t core.Tool, b core.Block, cwd string, byRules map[string]*askedRule) *askedRule {
	call := core.ToolCall{Name: b.Name, Input: b.Input}
	act, err := describe(t, call, &core.ToolEnv{Cwd: cwd, Roots: l.roots})
	if err != nil {
		return nil
	}
	d := l.engine.DecideAs(permission.ModeDefault, b.Name, call, act)
	if d.Behavior != permission.Ask {
		return nil
	}
	// The widest rules the question offers: the prefix rule ("kubectl get
	// *") where the engine finds one sensible, so that the calls it covers
	// count together.
	var rules []string
	for _, s := range d.Suggestions {
		if s.Mode == "" && len(s.Rules) > 0 {
			rules = s.Rules
		}
	}
	if len(rules) == 0 {
		return nil
	}
	key := strings.Join(rules, "\x00")
	r := byRules[key]
	if r == nil {
		r = &askedRule{rules: rules, example: firstOf(act.Command, act.URL)}
		byRules[key] = r
	}
	return r
}

// resultFor reports whether a user entry holds the result of a call that
// is waited on, before its gnosh fields are read at all.
func resultFor(te transcript.Entry, asking map[string]*askedRule) bool {
	for _, b := range te.Message.Blocks {
		if b.Type == core.BlockToolResult && asking[b.ToolUseID] != nil {
			return true
		}
	}
	return false
}

// recentSessionFiles are the session files in dirs (a session store's
// folders of projects), with their sub-agents', the newest first, at most
// fewerFiles; and how many of them are sessions' own. A session's
// sub-agents keep theirs as a session store does, under
// <session>/subagents/projects/<folder>/ (subagentsDir). Each folder is
// read as a folder, not a pattern: a [ or * in the store's path is a name.
func recentSessionFiles(dirs []string) ([]string, int) {
	type file struct {
		path string
		at   time.Time
		own  bool
	}
	var found []file
	add := func(path string, own bool) {
		if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() {
			found = append(found, file{path, info.ModTime(), own})
		}
	}
	seen := map[string]bool{}
	for _, dir := range dirs {
		if seen[dir] {
			continue
		}
		seen[dir] = true
		entries, _ := os.ReadDir(dir)
		for _, d := range entries {
			path := filepath.Join(dir, d.Name())
			if !d.IsDir() {
				if strings.HasSuffix(d.Name(), ".jsonl") {
					add(path, true)
				}
				continue
			}
			subs := filepath.Join(path, "subagents", "projects")
			folders, _ := os.ReadDir(subs)
			for _, f := range folders {
				if f.IsDir() {
					for _, p := range jsonlIn(filepath.Join(subs, f.Name())) {
						add(p, false)
					}
				}
			}
		}
	}
	slices.SortFunc(found, func(a, b file) int { return b.at.Compare(a.at) })
	if len(found) > fewerFiles {
		found = found[:fewerFiles]
	}
	paths, sessions := make([]string, len(found)), 0
	for i, f := range found {
		paths[i] = f.path
		if f.own {
			sessions++
		}
	}
	return paths, sessions
}

// jsonlIn are the session files in a folder.
func jsonlIn(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, d := range entries {
		if !d.IsDir() && strings.HasSuffix(d.Name(), ".jsonl") {
			out = append(out, filepath.Join(dir, d.Name()))
		}
	}
	return out
}

// lastSessions says how many sessions were looked through, after "last":
// "session" for one, "12 sessions".
func lastSessions(n int) string {
	if n == 1 {
		return "session"
	}
	return countWords(n, "session", "sessions")
}
