package permission

import (
	"path"
	"slices"
	"strings"
)

// This file is Claude Code's two blocks, the permissions keys
// blockReadsOutsideWorkingDirectories and
// blockWritesOutsideWorkingDirectories (Options.BlockReads and
// BlockWrites). With one on, the file tools refuse a path outside the
// working directories in every mode, bypassPermissions too, and no allow
// rule lets them through. The read block also asks, in every mode, about
// a shell command that reads such a path or whose files cannot be told;
// the write block leaves shell commands alone, as Claude Code's does (its
// sandbox is what holds them there, and gnosh has none).

// withLinks returns the absolute paths of ps, cleaned, each followed by
// where its links lead when that differs.
func withLinks(ps []string, resolve func(string) (string, error)) []string {
	var out []string
	for _, p := range ps {
		if !strings.HasPrefix(p, "/") {
			continue
		}
		p = path.Clean(p)
		out = append(out, p)
		if resolve != nil {
			if r, err := resolve(p); err == nil && r != p {
				out = append(out, r)
			}
		}
	}
	return out
}

// blockDirs are the working directories the blocks count: the primary one,
// and those a layer adds that the project did not supply (--add-dir,
// /add-dir, the person's own settings), each also where its links lead (a
// folder given as /tmp/x on macOS is /private/tmp/x). A repository can
// turn a block on for itself, and cannot widen the person's.
func (e *Engine) blockDirs() []string {
	out := []string{e.cwd}
	for _, ly := range e.all() {
		if ly.held || ly.FromProject || ly.Scope == ScopeProject {
			continue
		}
		for _, d := range ly.Dirs {
			if strings.HasPrefix(d, "/") {
				out = append(out, e.linked(path.Clean(d))...)
			}
		}
	}
	return out
}

// linked is a folder with where its links lead (withLinks), looked up once:
// the folders the blocks count are compared with every path a search
// passes.
func (e *Engine) linked(dir string) []string {
	if v, ok := e.links.Load(dir); ok {
		return v.([]string)
	}
	v := withLinks([]string{dir}, e.resolve)
	e.links.Store(dir, v)
	return v
}

// outside reports whether p lies outside every one of dirs.
func outside(dirs []string, p string) bool {
	for _, d := range dirs {
		if inside(d, p) {
			return false
		}
	}
	return true
}

// ownRead reports whether p is one of gnosh's own that the read block lets
// through (Options.OwnReads, AddOwnRead).
func (e *Engine) ownRead(p string) bool {
	for _, own := range e.ownReads {
		if inside(own, p) {
			return true
		}
	}
	return false
}

// AddOwnRead adds a folder of gnosh's own to those the read block lets the
// file tools reach (Options.OwnReads): the session's own folder, where a
// command's saved output, a fetched page and a pasted picture are kept,
// once the session's id is known.
func (e *Engine) AddOwnRead(dir string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, p := range withLinks([]string{dir}, e.resolve) {
		if !slices.Contains(e.ownReads, p) {
			e.ownReads = append(e.ownReads, p)
		}
	}
}

// settingsFile reports whether p is one of the settings files the write
// block refuses wherever they are: one Options.SettingsFiles names, or a
// file of one of the SettingsNames anywhere (a subfolder's gnosh.json is
// the settings of a session started there). Names are compared as the
// disk compares them (guardPath: macOS folds case).
func (e *Engine) settingsFile(p string) bool {
	key := guardPath(p)
	for _, s := range e.settingsFiles {
		if key == guardPath(s) {
			return true
		}
	}
	for _, name := range e.settingsNames {
		if strings.HasSuffix(key, "/"+guardPath(name)) {
			return true
		}
	}
	return false
}

// blocked applies the file tools' blocks to a call: a Deny, and false when
// neither block speaks. (The read block's question about a shell command
// is shellBlocked, put only where the command would have run.)
func (e *Engine) blocked(req *request) (Decision, bool) {
	switch req.info.class {
	case classFileRead:
		if !e.blockReads {
			break
		}
		dirs := e.blockDirs()
		for _, p := range req.reads {
			if outside(dirs, p) && !e.ownRead(p) {
				return Decision{Behavior: Deny, Stage: StageBlock,
					Reason: p + " is outside the working directories, and the setting permissions.blockReadsOutsideWorkingDirectories refuses reads there in every mode. If it is needed, ask the user to add its folder with /add-dir."}, true
			}
		}
	case classFileEdit:
		if !e.blockWrites {
			break
		}
		dirs := e.blockDirs()
		for _, p := range req.writes {
			if e.settingsFile(p) {
				return Decision{Behavior: Deny, Stage: StageBlock,
					Reason: p + " is a settings file, and while the setting permissions.blockWritesOutsideWorkingDirectories is on no tool changes one, wherever it is: a settings file could lift the block. The user edits settings files themselves."}, true
			}
			if outside(dirs, p) {
				return Decision{Behavior: Deny, Stage: StageBlock,
					Reason: p + " is outside the working directories, and the setting permissions.blockWritesOutsideWorkingDirectories refuses changes there in every mode. If it must change, ask the user to add its folder with /add-dir."}, true
			}
		}
	}
	return Decision{}, false
}

// shellBlocked is the read block's question about a shell command on this
// machine: one that reads a file outside the working directories, runs in
// a folder outside them (the shell's own, or one the line moves to with
// cd, pushd, env -C, git -C and their kin, where a grep -r or a find with
// no path reads), or whose files cannot be told. It asks in every mode,
// bypassPermissions too; dontAsk refuses. A command reads only what gnosh
// can trace: a program that opens files of its own (python -c, curl
// file://) is not held, as in Claude Code, whose sandbox is what holds
// those.
func (e *Engine) shellBlocked(req *request) (Decision, bool) {
	if !e.blockReads || req.info.class != classShell || req.remote != "" {
		// (A command on another machine reads that machine's files.)
		return Decision{}, false
	}
	ask := func(reason string) (Decision, bool) {
		return Decision{Behavior: Ask, Stage: StageBlock, PersonOnly: e.mode == ModePlan, Reason: reason}, true
	}
	why := req.pathOpaque
	if why == "" {
		why = req.pathCapped
	}
	if why == "" && req.unresolved {
		why = "the links of a path it names could not be followed"
	}
	if why != "" {
		return ask("gnosh cannot tell which files this command reads (" + why + "), and the setting permissions.blockReadsOutsideWorkingDirectories asks about such a command in every mode.")
	}
	dirs := e.blockDirs()
	places := append([]string{e.cur}, req.view.pwds...)
	for _, dir := range places {
		for _, d := range e.linked(path.Clean(dir)) {
			if outside(dirs, d) && !e.ownRead(d) {
				return ask("This command runs in " + d + ", outside the working directories, where what it reads cannot be held to them, and the setting permissions.blockReadsOutsideWorkingDirectories asks about that in every mode.")
			}
		}
	}
	for _, p := range req.reads {
		if outside(dirs, p) && !e.ownRead(p) {
			return ask("This command reads " + p + ", outside the working directories, and the setting permissions.blockReadsOutsideWorkingDirectories asks about that in every mode.")
		}
	}
	return Decision{}, false
}
