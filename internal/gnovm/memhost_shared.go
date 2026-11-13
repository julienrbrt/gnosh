// Copyright (C) 2026 Jae Kwon
//
// This file is part of gnosh's GnoVM machine subsystem. It is licensed under
// the GNO Network General Public License, version 6, as published by
// NewTendermint, LLC or its successor, or any later version of that License
// that the Licensor accepts as the proxy under its section 14, by a statement
// published in the canonical copy of internal/gnovm/LICENSE.md; together with
// the Additional Terms (Strong Attribution) in that file.

package gnovm

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"

	"github.com/charmbracelet/crush/internal/lock"
)

// memStatus is status.json: mem.JSON() as gnosh computed it for no session
// after a change, with what it was computed for. Hooks read it, and so does
// the <mem> block at a session's start, without opening mem.
type memStatus struct {
	Generation string `json:"generation"`
	Seq        int64  `json:"seq"`
	// Day is the user's local date it was computed on, and At when (Unix
	// seconds): when mem last changed.
	Day string          `json:"day"`
	At  int64           `json:"at"`
	Mem json.RawMessage `json:"mem"`
}

const memStatusFile = "status.json"

// lockMemDir takes the lock of mem in dir, waiting while another holds it
// (a gnosh process's call, or its main process reading the index or
// writing vectors) until ctx is done. The wait is the kernel's: the lock
// is granted the moment its holder lets go, with no polling and no limit
// but ctx; every holder's turn is one call, which mem bounds.
func lockMemDir(ctx context.Context, dir string) (func(), error) {
	if err := mkdirSync(dir); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, "lock")
	if release, err := lock.TryFile(path); err == nil {
		return release, nil
	} else if !errors.Is(err, lock.ErrContended) {
		return nil, err
	}
	// A worker's heartbeats say it waits, so the client does not count the
	// wait against the request's deadline.
	if w := lockWaiting(ctx); w != nil {
		w.Store(true)
		defer w.Store(false)
	}
	release, err := lock.File(ctx, path)
	if err != nil {
		if ctx.Err() != nil {
			return nil, &busyError{msg: "stopped waiting for mem, which another gnosh process was using"}
		}
		return nil, err
	}
	return release, nil
}

// lockWaitingKey carries a request's flag for "waiting for mem's lock".
type lockWaitingKey struct{}

func lockWaiting(ctx context.Context) *atomic.Bool {
	flag, _ := ctx.Value(lockWaitingKey{}).(*atomic.Bool)
	return flag
}

// memIndexLayout is the layout of mem's index: a change to its schema or to
// how Ops become rows is a new number, and an index of another is rebuilt.
// 2 keeps an item's status; 3 gives thoughts rows of their own.
const memIndexLayout = 3

// memDir is where a project's mem lives under dir, keyed by memRoot.
func memDir(dir, project string) string {
	return keyedMemDir(dir, memRoot(dir, project))
}

// keyedMemDir is where the mem keyed by root lives under dir.
func keyedMemDir(dir, root string) string {
	return filepath.Join(dir, projectKey(root), "mem")
}

// memRoot is what a project's mem under data is keyed by. Outside git, it is
// the project's directory. In a repository, it is the first to have a mem
// of: the git common dir, which the repository's worktrees and
// subdirectories share; the folder the common dir is the .git of. With
// neither, the common dir. So a folder keeps its mem when it becomes a
// repository (a scaffold's git init), and a worktree made after shares its
// main checkout's. Symlinks are resolved first, and nothing is cached, so
// every process finds the same mem.
func memRoot(data, project string) string {
	dir, err := filepath.EvalSymlinks(project)
	if err != nil {
		dir = filepath.Clean(project)
	}
	for d := dir; ; d = filepath.Dir(d) {
		git := filepath.Join(d, ".git")
		if fi, err := os.Stat(git); err == nil {
			if fi.IsDir() {
				return keptRoot(data, git)
			}
			if common := gitCommonDir(git); common != "" {
				return keptRoot(data, common)
			}
		}
		if filepath.Dir(d) == d {
			return dir
		}
	}
}

// keptRoot is memRoot in a repository whose git common dir is common.
func keptRoot(data, common string) string {
	roots := []string{common}
	if filepath.Base(common) == ".git" {
		roots = append(roots, filepath.Dir(common))
	}
	for _, root := range roots {
		if _, err := os.Stat(keyedMemDir(data, root)); err == nil {
			return root
		}
	}
	return common
}

// gitCommonDir reads a worktree's .git file ("gitdir: <path>") and that git
// dir's commondir file, and returns the common dir they name; "" if they do
// not.
func gitCommonDir(gitFile string) string {
	b, err := os.ReadFile(gitFile)
	if err != nil {
		return ""
	}
	gitdir, ok := strings.CutPrefix(strings.TrimSpace(string(b)), "gitdir:")
	if !ok {
		return ""
	}
	gitdir = strings.TrimSpace(gitdir)
	if !filepath.IsAbs(gitdir) {
		gitdir = filepath.Join(filepath.Dir(gitFile), gitdir)
	}
	common := gitdir
	if b, err := os.ReadFile(filepath.Join(gitdir, "commondir")); err == nil {
		common = strings.TrimSpace(string(b))
		if !filepath.IsAbs(common) {
			common = filepath.Join(gitdir, common)
		}
	}
	if resolved, err := filepath.EvalSymlinks(common); err == nil {
		return resolved
	}
	return filepath.Clean(common)
}
