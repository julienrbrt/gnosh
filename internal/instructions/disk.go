package instructions

import (
	"errors"
	"io"
	"io/fs"
	"path"
	"path/filepath"
	"strings"
	"time"
)

// maxLinks is how many symbolic links one path may lead through before it is
// taken for a loop and reported as unreadable.
const maxLinks = 255

var (
	errLinkLoop   = errors.New("it leads through too many symbolic links, which is probably a loop")
	errNotRegular = errors.New("it is not an ordinary file")
	errTooBig     = errors.New("it is too big")
	errNetMount   = errors.New("it lies in a folder the system mounts from the network as soon as it is looked at, so it is not looked at")
)

// netMounts are folders whose entries the system mounts from the network
// the moment one is looked at: macOS's /net, a folder for every host, and
// /Network; and AFS's cells. A look at /net/beacon.example is a connection
// to that host, so a repository's "@/net/beacon.example/x.md", or a link
// there, would make the machine call out as gnosh starts, before anyone
// has said the folder may be trusted (the skills review of 2026-11-19).
var netMounts = []string{"/net", "/Network", "/afs"}

// disk reads the file system the loader was given. The paths it takes are
// absolute, cleaned and slash-separated ("/a/b"). The file system is rooted
// at "/" and is asked for the same path without the leading slash.
//
// disk follows symbolic links itself, one at a time, and only ever hands the
// file system a path with no links in it. That way a loop of links is caught
// here, and a link with an absolute target works on any file system.
type disk struct {
	fsys fs.FS
	// mine are the places inside a network mount (netMounts) that may be
	// looked at all the same: the project, the person's home and their own
	// files, when they lie there, and the folders on the way to them.
	// Nothing else inside one is.
	mine []string
}

// offLimits reports whether p lies inside a network mount and outside
// every place of mine.
func (d disk) offLimits(p string) bool {
	for _, m := range netMounts {
		if !strings.HasPrefix(p, m+"/") {
			continue
		}
		for _, ok := range d.mine {
			if ok != "" && (within(p, ok) || within(ok, p)) {
				return false
			}
		}
		return true
	}
	return false
}

// fenced is the error for a look at p that is off limits.
func fenced(op, p string) error {
	return &fs.PathError{Op: op, Path: fsName(p), Err: errNetMount}
}

// fsName turns one of the package's paths into the name the file system
// wants.
func fsName(p string) string {
	p = strings.TrimPrefix(p, "/")
	if p == "" {
		return "."
	}
	return p
}

// lstat describes what is at p without following a link there. The
// directory part of p must hold no links.
func (d disk) lstat(p string) (fs.FileInfo, error) {
	if d.offLimits(p) {
		return nil, fenced("lstat", p)
	}
	return fs.Lstat(d.fsys, fsName(p))
}

// stat describes the file at real, a path with no links in it.
func (d disk) stat(real string) (fs.FileInfo, error) {
	if d.offLimits(real) {
		return nil, fenced("stat", real)
	}
	return fs.Stat(d.fsys, fsName(real))
}

// readDir lists the directory at real, a path with no links in it, in name
// order.
func (d disk) readDir(real string) ([]fs.DirEntry, error) {
	if d.offLimits(real) {
		return nil, fenced("readdir", real)
	}
	return fs.ReadDir(d.fsys, fsName(real))
}

// resolve follows every symbolic link in p and returns the path that names
// the same thing with no links in it. It fails if any part of p is not
// there.
func (d disk) resolve(p string) (string, error) {
	todo := strings.Split(p, "/")
	at := "/"
	links := 0
	for len(todo) > 0 {
		part := todo[0]
		todo = todo[1:]
		switch part {
		case "", ".":
			continue
		case "..":
			at = path.Dir(at)
			continue
		}
		next := path.Join(at, part)
		info, err := d.lstat(next)
		if err != nil {
			return "", err
		}
		if info.Mode()&fs.ModeSymlink == 0 {
			at = next
			continue
		}
		links++
		if links > maxLinks {
			return "", errLinkLoop
		}
		target, err := fs.ReadLink(d.fsys, fsName(next))
		if err != nil {
			return "", err
		}
		target = filepath.ToSlash(target)
		if strings.HasPrefix(target, "/") {
			at = "/"
		}
		todo = append(strings.Split(target, "/"), todo...)
	}
	return at, nil
}

// resolveLoose is resolve for a path whose end may not be there yet: it
// follows the links in as much of p as exists and keeps the rest as written.
func (d disk) resolveLoose(p string) string {
	rest := ""
	for {
		real, err := d.resolve(p)
		if err == nil {
			return path.Join(real, rest)
		}
		if p == "/" {
			return path.Join("/", rest)
		}
		rest = path.Join(path.Base(p), rest)
		p = path.Dir(p)
	}
}

// read returns the content of the ordinary file at real, a path with no
// links in it. It refuses a file of more than max bytes without reading it
// all.
func (d disk) read(real string, max int64) ([]byte, fs.FileInfo, error) {
	if d.offLimits(real) {
		return nil, nil, fenced("open", real)
	}
	f, err := d.fsys.Open(fsName(real))
	if err != nil {
		return nil, nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, info, errNotRegular
	}
	if info.Size() > max {
		return nil, info, errTooBig
	}
	data, err := io.ReadAll(io.LimitReader(f, max+1))
	if err != nil {
		return nil, info, err
	}
	if int64(len(data)) > max {
		return nil, info, errTooBig
	}
	return data, info, nil
}

// probe is what a look at one path found.
type probe struct {
	real  string      // the path with its links followed
	info  fs.FileInfo // what the path leads to
	linfo fs.FileInfo // what is at the path itself: a link, if it is one
	err   error
}

// lookups looks paths up on disk and remembers the answers for as long as
// one pass lasts, since a pass asks about the same directories many times.
type lookups struct {
	d     disk
	found map[string]probe
}

func newLookups(d disk) *lookups {
	return &lookups{d: d, found: map[string]probe{}}
}

// probe looks at p, following its links.
func (k *lookups) probe(p string) probe {
	if pr, ok := k.found[p]; ok {
		return pr
	}
	pr := k.look(p)
	k.found[p] = pr
	return pr
}

func (k *lookups) look(p string) probe {
	var pr probe
	if p == "/" {
		pr.real = "/"
		pr.info, pr.err = k.d.stat("/")
		pr.linfo = pr.info
		return pr
	}
	parent := k.probe(path.Dir(p))
	if parent.err != nil {
		pr.err = parent.err
		return pr
	}
	if !parent.info.IsDir() {
		// Nothing can be inside a file. Hosts differ in what they call
		// this; here it is simply not there.
		pr.err = &fs.PathError{Op: "lstat", Path: fsName(p), Err: fs.ErrNotExist}
		return pr
	}
	at := path.Join(parent.real, path.Base(p))
	if pr.linfo, pr.err = k.d.lstat(at); pr.err != nil {
		return pr
	}
	if pr.linfo.Mode()&fs.ModeSymlink == 0 {
		pr.real, pr.info = at, pr.linfo
		return pr
	}
	if pr.real, pr.err = k.d.resolve(at); pr.err != nil {
		return pr
	}
	pr.info, pr.err = k.d.stat(pr.real)
	return pr
}

// realDir returns dir with its links followed.
func (k *lookups) realDir(dir string) (string, error) {
	pr := k.probe(dir)
	return pr.real, pr.err
}

// stamp is what a path was like when the loader looked at it: enough to tell
// later whether a reload would find something else there.
type stamp struct {
	exists bool
	dir    bool
	size   int64
	mod    time.Time
	names  string // the names in a directory
}

// same reports whether two stamps describe the same state.
func (a stamp) same(b stamp) bool {
	return a.exists == b.exists && a.dir == b.dir && a.size == b.size && a.mod.Equal(b.mod) && a.names == b.names
}

// stamp describes what is at p now.
func (k *lookups) stamp(p string) stamp {
	pr := k.probe(p)
	if pr.err != nil {
		return stamp{}
	}
	if pr.info.IsDir() {
		entries, _ := k.d.readDir(pr.real)
		return stamp{exists: true, dir: true, names: joinNames(entries)}
	}
	return stamp{exists: true, size: pr.info.Size(), mod: pr.info.ModTime()}
}

// joinNames writes a directory's entry names as one string.
func joinNames(entries []fs.DirEntry) string {
	names := make([]string, len(entries))
	for i, e := range entries {
		names[i] = e.Name()
	}
	return strings.Join(names, "\x00")
}
