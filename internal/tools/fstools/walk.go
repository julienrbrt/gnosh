package fstools

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/gnosh-ai/gnosh/internal/tools/fstools/ignore"
)

// walkJob says how to walk a directory tree.
type walkJob struct {
	// root is the directory to walk: an absolute path with its links
	// followed.
	root string
	// hidden lets the walk visit entries whose names start with a dot.
	hidden bool
	// rules are the ignore rules in force in root, or nil to honour none.
	rules *ignore.Rules
	// workers is how many goroutines walk.
	workers int
	// spread hands every file to the workers as a job of its own, for a
	// visit that takes long. Without it a directory's files are visited by
	// the goroutine that listed it.
	spread bool
	// enter, when not nil, is asked before a directory is entered; rel is
	// its path below root, with slashes.
	enter func(rel string) bool
	// over, when not nil, is asked of an entry that hidden or the ignore
	// rules would pass over: true visits the file, or enters the
	// directory, all the same (a name the search spells out).
	over func(rel string, isDir bool) bool
	// visit is called for every file found: regular files and symbolic
	// links, nothing else. It is called from several goroutines at once.
	visit func(f walkedFile)
}

// walkedFile is one file a walk found.
type walkedFile struct {
	// rel is the file's path below the walk's root, with slashes.
	rel string
	// path is the file's full path.
	path string
	// entry is the file's directory entry.
	entry fs.DirEntry
}

// isLink reports whether the file is a symbolic link.
func (f *walkedFile) isLink() bool { return f.entry.Type()&fs.ModeSymlink != 0 }

// walkProblems is what a walk could not look at.
type walkProblems struct {
	mu sync.Mutex
	// dirs is how many directories could not be listed; first holds the
	// first few, each with its reason.
	dirs  int
	first []string
}

func (p *walkProblems) add(path string, err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.dirs++
	if len(p.first) < 3 {
		p.first = append(p.first, fmt.Sprintf("%s (%v)", path, underlying(err)))
	}
}

// note says what was left out, for the end of a result; it is empty when
// nothing was.
func (p *walkProblems) note() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.dirs == 0 {
		return ""
	}
	verb := "were"
	if p.dirs == 1 {
		verb = "was"
	}
	s := fmt.Sprintf("%s could not be looked into and %s left out: %s",
		countOf(p.dirs, "directory", "directories"), verb, strings.Join(p.first, ", "))
	if p.dirs > len(p.first) {
		s += fmt.Sprintf(", and %d more", p.dirs-len(p.first))
	}
	return s + "."
}

// walkTask is one thing a walk has still to do: list a directory, or
// visit a file.
type walkTask struct {
	// A directory: its path, its path below the root ("" for the root),
	// and the ignore rules of the directory above it.
	dir, rel string
	above    *ignore.Rules
	// A file to visit; file.path is empty for a directory task.
	file walkedFile
}

// walker is a walk in progress.
type walker struct {
	job      *walkJob
	ctx      context.Context
	problems *walkProblems

	mu    sync.Mutex
	cond  *sync.Cond
	tasks []walkTask // a stack: the newest is done first, which keeps it short
	busy  int        // tasks being done right now
	stop  bool       // the context was cancelled
}

// walkTree walks a directory tree with several goroutines and calls
// job.visit for the files in it. It never enters a directory named .git,
// a directory reached through a symbolic link, or one the ignore rules
// leave out, and it passes over entries that are neither files nor
// directories. It returns when the whole tree has been walked or ctx is
// cancelled, in which case the error is ctx's. A root that cannot be
// listed is an error; a directory further down that cannot be listed is
// noted in problems and the walk goes on.
func walkTree(ctx context.Context, job *walkJob, problems *walkProblems) error {
	w := &walker{job: job, ctx: ctx, problems: problems}
	w.cond = sync.NewCond(&w.mu)

	// List the root here, so that failing to is this call's error.
	rootTasks, err := w.list(walkTask{dir: job.root}, true)
	if err != nil {
		return err
	}
	w.tasks = rootTasks

	stopWatching := context.AfterFunc(ctx, func() {
		w.mu.Lock()
		w.stop = true
		w.mu.Unlock()
		w.cond.Broadcast()
	})
	defer stopWatching()

	var wg sync.WaitGroup
	for i := 0; i < max(job.workers, 1); i++ {
		wg.Go(w.work)
	}
	wg.Wait()
	if err := ctx.Err(); err != nil {
		return context.Cause(ctx)
	}
	return nil
}

// work does tasks until none are left and nobody is making more.
func (w *walker) work() {
	for {
		w.mu.Lock()
		for len(w.tasks) == 0 && w.busy > 0 && !w.stop {
			w.cond.Wait()
		}
		if w.stop || len(w.tasks) == 0 {
			w.mu.Unlock()
			// Whoever is still waiting has nothing to wait for either.
			w.cond.Broadcast()
			return
		}
		task := w.tasks[len(w.tasks)-1]
		w.tasks = w.tasks[:len(w.tasks)-1]
		w.busy++
		w.mu.Unlock()

		var more []walkTask
		if task.file.path != "" {
			w.job.visit(task.file)
		} else {
			more, _ = w.list(task, false)
		}

		w.mu.Lock()
		w.tasks = append(w.tasks, more...)
		w.busy--
		w.mu.Unlock()
		w.cond.Broadcast()
	}
}

// list reads one directory. It visits the directory's files, or turns
// them into tasks when the job spreads files, and returns those tasks and
// one task for each directory to enter. Only for the root is a failure to
// read returned; elsewhere it is noted.
func (w *walker) list(task walkTask, root bool) ([]walkTask, error) {
	entries, err := readDir(w.ctx, task.dir)
	if err != nil {
		if root {
			return nil, err
		}
		if w.ctx.Err() == nil {
			w.problems.add(task.dir, err)
		}
		return nil, nil
	}
	var rules *ignore.Rules
	switch {
	case w.job.rules == nil:
	case root:
		rules = w.job.rules
	default:
		rules = task.above.Enter(filepath.Base(task.dir), func(name string) bool {
			for _, e := range entries {
				if e.Name() == name {
					return true
				}
			}
			return false
		})
	}

	var dirs, files []walkTask
	for _, e := range entries {
		name := e.Name()
		if name == ".git" {
			continue
		}
		typ := e.Type()
		isDir := typ.IsDir()
		if !isDir && !typ.IsRegular() && typ&fs.ModeSymlink == 0 {
			continue
		}
		rel := name
		if task.rel != "" {
			rel = task.rel + "/" + name
		}
		if (!w.job.hidden && strings.HasPrefix(name, ".")) || (rules != nil && rules.Ignored(name, isDir)) {
			if w.job.over == nil || !w.job.over(rel, isDir) {
				continue
			}
		}
		path := filepath.Join(task.dir, name)
		if isDir {
			if w.job.enter == nil || w.job.enter(rel) {
				dirs = append(dirs, walkTask{dir: path, rel: rel, above: rules})
			}
			continue
		}
		f := walkedFile{rel: rel, path: path, entry: e}
		if w.job.spread {
			files = append(files, walkTask{file: f})
		} else {
			w.job.visit(f)
		}
	}
	// Files go on top of the stack so that they are done before the
	// directories beside them are opened.
	return append(dirs, files...), nil
}

// readDir lists a directory a batch at a time, so that a huge one can be
// given up on when ctx is cancelled.
func readDir(ctx context.Context, dir string) ([]fs.DirEntry, error) {
	d, err := os.Open(dir)
	if err != nil {
		return nil, err
	}
	defer d.Close()
	var all []fs.DirEntry
	for {
		if err := ctx.Err(); err != nil {
			return nil, context.Cause(ctx)
		}
		batch, err := d.ReadDir(4096)
		all = append(all, batch...)
		if errors.Is(err, io.EOF) {
			return all, nil
		}
		if err != nil {
			return nil, err
		}
		if len(batch) == 0 {
			return all, nil
		}
	}
}
