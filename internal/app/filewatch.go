package app

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/gnosh-ai/gnosh/internal/hooks"
)

// This file is the FileChanged hook event, as Claude Code has it: files are
// watched while the session runs, and when one is made, changed or
// removed, whatever did it (a tool, a command, another program), the
// FileChanged hooks run, told which file and what happened. The files
// watched are the ones the FileChanged matchers name (split on "|", each a
// file name in the folder the session started in) and the ones the last
// hook to say so named (watchPaths, from a SessionStart, CwdChanged or
// FileChanged hook). gnosh looks at them twice a second, which the
// standard library can do on every system, and only while a FileChanged
// hook is configured.

// fileWatch is how the watched files stood at the last look.
type fileWatch struct {
	// stop ends the watcher, and done is closed once it has ended: the
	// session stops it before its SessionEnd hooks run.
	stop func()
	done chan struct{}

	mu sync.Mutex
	// dynamic are the files the hooks named (watchPaths), in force until
	// a hook names others.
	dynamic []string
	stamps  map[string]fileStamp
}

// watchEvery is how often the watched files are looked at.
const watchEvery = 500 * time.Millisecond

// watchFiles starts looking at the watched files, when a FileChanged hook
// is configured, until ctx ends.
func (l *live) watchFiles(ctx context.Context) {
	if !l.hooks.has(hooks.FileChanged) {
		return
	}
	ctx, cancel := context.WithCancel(ctx)
	l.watch.stop, l.watch.done = cancel, make(chan struct{})
	l.lookAtWatched(ctx, true)
	go func() {
		defer close(l.watch.done)
		t := time.NewTicker(watchEvery)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				l.lookAtWatched(ctx, false)
			}
		}
	}()
}

// stopWatching ends the watcher and waits, a moment at most, for a
// FileChanged hook it runs to end, so that none runs after SessionEnd.
func (l *live) stopWatching() {
	if l.watch.stop == nil {
		return
	}
	l.watch.stop()
	select {
	case <-l.watch.done:
	case <-time.After(15 * time.Second):
	}
}

// watchedPaths are the files watched now: the matchers' and the hooks'.
func (l *live) watchedPaths() []string {
	var out []string
	add := func(p string) {
		if !slices.Contains(out, p) {
			out = append(out, p)
		}
	}
	for _, h := range l.hooks.r.Hooks(hooks.FileChanged) {
		for _, name := range strings.Split(h.Matcher, "|") {
			if name = strings.TrimSpace(name); name != "" {
				add(filepath.Join(l.cfg.Paths.Cwd, name))
			}
		}
	}
	w := &l.watch
	w.mu.Lock()
	for _, p := range w.dynamic {
		add(p)
	}
	w.mu.Unlock()
	return out
}

// lookAtWatched compares each watched file with how it stood at the last
// look, and runs the FileChanged hooks for each one that was made, changed
// or removed since. The first look, and the first look at a file newly
// watched, only note how it stands.
func (l *live) lookAtWatched(ctx context.Context, first bool) {
	type change struct{ path, event string }
	var changes []change
	w := &l.watch
	paths := l.watchedPaths()
	w.mu.Lock()
	// Only the files watched now are kept: one dropped and watched again
	// later starts afresh, as one newly watched does.
	stamps := make(map[string]fileStamp, len(paths))
	for _, p := range paths {
		now := stampOf(p)
		was, seen := w.stamps[p]
		stamps[p] = now
		switch {
		case first || !seen:
		case !was.ok && now.ok:
			changes = append(changes, change{p, "add"})
		case was.ok && !now.ok:
			changes = append(changes, change{p, "unlink"})
		case was.ok && now.ok && was != now:
			changes = append(changes, change{p, "change"})
		}
	}
	w.stamps = stamps
	w.mu.Unlock()
	for _, c := range changes {
		if ctx.Err() != nil || l.closing.Load() {
			return
		}
		out, err := l.hooks.run(ctx, hooks.Input{Event: hooks.FileChanged, FilePath: c.path, FileEvent: c.event, EnvFile: l.envFile()})
		if err == nil {
			l.setWatched(out)
		}
	}
}

// setWatched takes the files the hooks said to watch, in place of the ones
// they named before; an empty list clears them.
func (l *live) setWatched(out hooks.Outcome) {
	if !out.WatchSet {
		return
	}
	w := &l.watch
	w.mu.Lock()
	w.dynamic = slices.Clone(out.WatchPaths)
	w.mu.Unlock()
}
