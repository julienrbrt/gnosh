package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/gnosh-ai/gnosh/internal/agent"
	"github.com/gnosh-ai/gnosh/internal/app/agentdef"
	"github.com/gnosh-ai/gnosh/internal/config"
	"github.com/gnosh-ai/gnosh/internal/core"
	"github.com/gnosh-ai/gnosh/internal/permission"
)

// An agent's memory (DECISIONS 374), as Claude Code's subagents have one:
// a kind of agent whose definition says memory: user, project or local
// keeps a folder of its own that outlasts its sessions. Its sub-agents are
// told where it is and what to keep there, start with the beginning of
// its MEMORY.md, and have Read, Write and Edit to keep it with. The notes
// in such a folder (its Markdown files) are not among the protected paths,
// as in Claude Code: a change to one asks as any edit does.
// CLAUDE_CODE_DISABLE_AUTO_MEMORY=1 turns it off, as in Claude Code.

const (
	memoryIndex = "MEMORY.md"
	// What of the index a sub-agent starts with: its first lines, as far
	// as both limits allow.
	memoryIndexLines = 200
	memoryIndexBytes = 25 * 1024
)

// lowerWindow is the auto-compact window of a sub-agent whose kind sets
// one, kind: the session's where that is lower, as Claude Code takes the
// lowest of the two (with the model's window, which bounds every agent).
func lowerWindow(kind int, session config.Value[int]) int {
	if session.Set && session.V > 0 && session.V < kind {
		return session.V
	}
	return kind
}

// memoryTools are the tools a sub-agent with a memory has for it.
var memoryTools = map[string]bool{"Read": true, "Write": true, "Edit": true}

// agentMemoryDir is the folder a kind of agent keeps its memory in, and
// below where the person's or the project's folder ends, the part of it
// gnosh makes (and that may hold no link), or "" when it keeps none. A
// definition of Claude Code's, from a .claude folder, keeps Claude Code's
// places, so that both keep one memory of the same agent; gnosh's own
// (from its folders, or --agents) keep gnosh's: the user's in gnosh's
// config folder, the project's in .gnosh.
func (l *live) agentMemoryDir(def agentdef.Def) (dir, base string) {
	theirs := strings.Contains(filepath.ToSlash(def.Source), "/"+theirDir+"/")
	switch def.Memory {
	case "user":
		base = l.cfg.Paths.ConfigDir
		if theirs {
			base = filepath.Join(l.cfg.Paths.Home, theirDir)
		}
		return filepath.Join(base, permission.MemoryFolder, def.Name), base
	case "project", "local":
		base = l.cfg.Paths.ProjectRoot
		own := ownDir
		if theirs {
			own = theirDir
		}
		folder := permission.MemoryFolder
		if def.Memory == "local" {
			folder = permission.MemoryLocalFolder
		}
		return filepath.Join(base, own, folder, def.Name), base
	}
	return "", ""
}

// memoryOff says whether agents' memories are turned off.
func (l *live) memoryOff() bool { return l.envOn("CLAUDE_CODE_DISABLE_AUTO_MEMORY") }

// memoryScopeWords says where a memory is kept, for the agent.
func memoryScopeWords(scope string) string {
	switch scope {
	case "user":
		return "kept for you across every project"
	case "local":
		return "kept in this project and out of its version control"
	}
	return "kept in this project and shared with whoever works on it through version control"
}

// openAgentMemory makes an agent's memory folder where it is not there
// yet (a local one with a .gitignore that keeps it out of git), and reads
// the beginning of its index. Nothing below base may be a link, nor the
// index: a project could lead an agent's memory, and what it writes in it
// with no question, anywhere, or have a file of the person's read into its
// prompt.
func openAgentMemory(dir, base, scope string) (index string, cut bool, err error) {
	rel, err := filepath.Rel(base, dir)
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") {
		return "", false, fmt.Errorf("%s is not inside %s", dir, base)
	}
	perm := os.FileMode(0o755)
	if scope == "user" {
		// (The config folder itself, on a machine gnosh has not kept one
		// on yet.)
		if err := os.MkdirAll(base, 0o700); err != nil {
			return "", false, err
		}
		perm = 0o700
	}
	at := base
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		at = filepath.Join(at, part)
		info, err := os.Lstat(at)
		switch {
		case errors.Is(err, os.ErrNotExist):
			if err := os.Mkdir(at, perm); err != nil && !errors.Is(err, os.ErrExist) {
				return "", false, err
			}
			if info, err = os.Lstat(at); err != nil {
				return "", false, err
			}
		case err != nil:
			return "", false, err
		}
		if !info.IsDir() {
			return "", false, fmt.Errorf("%s is a link or a file, not a folder, and gnosh keeps a memory only in a folder of its own making", at)
		}
	}
	if scope == "local" {
		ignore := filepath.Join(dir, ".gitignore")
		if _, err := os.Lstat(ignore); errors.Is(err, os.ErrNotExist) {
			_ = os.WriteFile(ignore, []byte("# An agent's local memory, which gnosh keeps out of version control.\n*\n"), 0o644)
		}
	}
	f, err := openNoLink(filepath.Join(dir, memoryIndex))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("its %s could not be read (%v): a link is not followed", memoryIndex, err)
	}
	defer f.Close()
	if info, err := f.Stat(); err != nil || !info.Mode().IsRegular() {
		return "", false, fmt.Errorf("its %s is not a plain file", memoryIndex)
	}
	b, err := io.ReadAll(io.LimitReader(f, memoryIndexBytes+1))
	if err != nil {
		return "", false, err
	}
	if len(b) > memoryIndexBytes {
		// (At the last whole line within the limit.)
		b, cut = b[:memoryIndexBytes], true
		if i := bytes.LastIndexByte(b, '\n'); i >= 0 {
			b = b[:i+1]
		}
		// (A line longer than the limit, cut mid-letter: at most the
		// three bytes of the letter cut are taken off the end, whatever is
		// before them.)
		for i := 0; i < utf8.UTFMax-1 && len(b) > 0; i++ {
			if r, size := utf8.DecodeLastRune(b); r != utf8.RuneError || size > 1 {
				break
			}
			b = b[:len(b)-1]
		}
	}
	lines := strings.SplitAfterN(string(b), "\n", memoryIndexLines+1)
	if len(lines) > memoryIndexLines && lines[memoryIndexLines] != "" {
		lines, cut = lines[:memoryIndexLines], true
	}
	return strings.Join(lines[:min(len(lines), memoryIndexLines)], ""), cut, nil
}

// memoryOnly is a file tool a kind of agent is given for its memory alone,
// its tools list leaving the tool out: a call on a file outside the memory
// folder, links followed, is refused (the review of 2026-11-20: a kind
// listed as reading only, given Write for its notes, could otherwise
// change the project, unasked in acceptEdits).
type memoryOnly struct {
	core.Tool
	dir string
}

// Describe is the tool's own, but for a file outside the memory folder,
// which it refuses: the call cannot run, and the gate is asked about
// what it was aimed at, as for any call that cannot.
func (t memoryOnly) Describe(call core.ToolCall, env *core.ToolEnv) (core.Action, error) {
	act, err := describe(t.Tool, call, env)
	if why := t.outside(act); why != "" {
		return act, errors.New(why)
	}
	return act, err
}

// Parallel is the tool's own: a Read of the memory still runs beside the
// round's other calls that only look.
func (t memoryOnly) Parallel(call core.ToolCall) bool {
	c, ok := t.Tool.(agent.Concurrent)
	return ok && c.Parallel(call)
}

// Run refuses a file outside the memory folder, and is the tool's own
// otherwise.
func (t memoryOnly) Run(ctx context.Context, call core.ToolCall, env *core.ToolEnv) (core.ToolResult, error) {
	act, _ := describe(t.Tool, call, env)
	if why := t.outside(act); why != "" {
		return core.ErrorResult(why), nil
	}
	return t.Tool.Run(ctx, call, env)
}

// outside says why a call is not on files in the memory folder, or "": it
// judges the paths the tool itself says the call touches (Action.Paths,
// from the same reading of file_path its Run makes), links followed, not
// a reading of its own (the cleanup review of 2026-11-20: "l/../../x"
// with l a link read one way here and another in the tool).
func (t memoryOnly) outside(act core.Action) string {
	name := t.Tool.Spec().Name
	why := fmt.Sprintf("This kind of agent has %s for its memory only, the folder %s: its tools list leaves %s out for any other file.", name, t.dir, name)
	dir, err := resolvePath(t.dir)
	if err != nil || len(act.Paths) == 0 {
		return why
	}
	for _, p := range act.Paths {
		if real, err := resolvePath(p); err != nil || !within(real, dir) {
			return why
		}
	}
	return ""
}
