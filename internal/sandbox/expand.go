package sandbox

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// IsPattern reports whether a path holds a pattern's marks (*, ?, [): a
// path the settings give as a pattern (Claude Code's "~/**/.env") names
// the paths that match it, which bubblewrap's mounts and Seatbelt's rules
// cannot take as it is.
func IsPattern(path string) bool { return strings.ContainsAny(path, "*?[") }

// ExpandBudget is how many folder entries Expand looks at, at most, for
// all the patterns of a session: a pattern from the home folder down
// could otherwise walk the whole disk.
const ExpandBudget = 200_000

// Expand is the paths that match a pattern now, an absolute path whose
// parts may hold *, ? and [...] (each matched as filepath.Match matches
// one name) and ** (any number of folders, none among them); a path that
// is no pattern is itself, there or not. budget is what is left of the
// entries it may look at, and is spent; done is false when it ran out
// before the walk was over. Links to folders are not followed into, so
// that a walk cannot go round in a circle.
func Expand(pattern string, budget *int) (paths []string, done bool) {
	// (A last /** is the folder with what is in it: the folder.)
	for strings.HasSuffix(pattern, "/**") {
		pattern = strings.TrimSuffix(pattern, "/**")
	}
	if !IsPattern(pattern) {
		return []string{pattern}, true
	}
	parts := strings.Split(strings.TrimPrefix(filepath.Clean(pattern), "/"), "/")
	n := 0
	for n < len(parts) && !IsPattern(parts[n]) {
		n++
	}
	w := walker{budget: budget}
	w.match("/"+filepath.Join(parts[:n]...), parts[n:])
	slices.Sort(w.out)
	return slices.Compact(w.out), !w.over
}

// walker is one pattern's walk.
type walker struct {
	budget *int
	over   bool
	out    []string
}

// match adds the paths under dir that match the rest of the pattern.
func (w *walker) match(dir string, rest []string) {
	if w.over {
		return
	}
	if len(rest) == 0 {
		w.out = append(w.out, dir)
		return
	}
	seg := rest[0]
	switch {
	case seg == "**":
		w.match(dir, rest[1:])
		for _, e := range w.read(dir) {
			if e.IsDir() {
				w.match(filepath.Join(dir, e.Name()), rest)
			}
		}
	case !IsPattern(seg):
		if p := filepath.Join(dir, seg); exists(p) {
			w.match(p, rest[1:])
		}
	default:
		for _, e := range w.read(dir) {
			if ok, _ := filepath.Match(seg, e.Name()); ok {
				w.match(filepath.Join(dir, e.Name()), rest[1:])
			}
		}
	}
}

// read is a folder's entries, the budget spent on them; none of a folder
// that cannot be read, or once the budget is gone. (os.ReadDir's entries
// are a link's own: a link to a folder is no folder to walk into.)
func (w *walker) read(dir string) []os.DirEntry {
	if w.over {
		return nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	if *w.budget -= len(entries) + 1; *w.budget < 0 {
		w.over = true
		return nil
	}
	return entries
}

// exists reports whether there is something at the path, a link that
// leads nowhere among them.
func exists(p string) bool {
	_, err := os.Lstat(p)
	return err == nil
}
