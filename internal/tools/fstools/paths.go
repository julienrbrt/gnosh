package fstools

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/gnosh-ai/gnosh/internal/core"
)

// maxLinkHops is how many symbolic links one path may pass through before
// it is taken for a loop. The kernel draws the same kind of line.
const maxLinkHops = 255

// location is where a path argument leads.
type location struct {
	// abs is the argument made absolute and cleaned. Symbolic links in it
	// are not followed: it is the path as the model wrote it.
	abs string

	// real is abs with every symbolic link followed, as far as the path
	// exists. Where the path stops existing, the rest is kept as written.
	// It is empty when trouble is set.
	real string

	// exists says whether something is there at real.
	exists bool

	// info describes what is at real, when something is. It is never the
	// info of a symbolic link.
	info fs.FileInfo

	// trouble is set when the path could not be followed: its links loop,
	// something on the way is not a directory, or a directory on the way
	// may not be looked into.
	trouble *failure
}

// paths returns what a permission check should look at: the path as
// written and, when links lead elsewhere, the path they lead to.
func (l *location) paths() []string {
	if l.real != "" && l.real != l.abs {
		return []string{l.abs, l.real}
	}
	return []string{l.abs}
}

// viaLink says in a few words that the path was reached through a link,
// for results; it is empty when it was not.
func (l *location) viaLink() string {
	if l.real != "" && l.real != l.abs {
		return " (which leads to " + l.real + ")"
	}
	return ""
}

// locate turns a path argument into a location. A failure means the
// argument cannot be used as a path at all; trouble on disk is reported in
// the location instead, so that a caller can still say which path the call
// was about.
func (o *Options) locate(env *core.ToolEnv, tool, argName, arg string) (location, *failure) {
	if arg == "" {
		return location{}, failf("%s: %s is empty.", tool, argName)
	}
	if strings.ContainsRune(arg, 0) {
		return location{}, failf("%s: %s holds a NUL byte, which no path can. Remove it.", tool, argName)
	}
	if arg[0] == '~' {
		switch {
		case o.HomeDir != "" && arg == "~":
			arg = o.HomeDir
		case o.HomeDir != "" && strings.HasPrefix(arg, "~/"):
			arg = filepath.Join(o.HomeDir, arg[2:])
		default:
			return location{}, failf("%s: %s starts with \"~\", which is not expanded here: %q. Give the absolute path (for a file whose name really starts with \"~\", the absolute path too).", tool, argName, short(arg, 200))
		}
	}
	abs := arg
	if !filepath.IsAbs(abs) {
		if env == nil || env.Cwd == "" || !filepath.IsAbs(env.Cwd) {
			return location{}, failf("%s: %s is a relative path (%q) and the session has no working directory to take it from. Give an absolute path.", tool, argName, short(arg, 200))
		}
		abs = filepath.Join(env.Cwd, abs)
	}
	loc := location{abs: filepath.Clean(abs)}
	real, exists, err := followLinks(loc.abs)
	if err != nil {
		loc.trouble = pathTrouble(loc.abs, err)
		return loc, nil
	}
	loc.real, loc.exists = real, exists
	if exists {
		info, err := os.Lstat(real)
		switch {
		case err == nil:
			loc.info = info
		case errors.Is(err, fs.ErrNotExist):
			loc.exists = false
		default:
			loc.real = ""
			loc.trouble = pathTrouble(loc.abs, err)
		}
	}
	return loc, nil
}

// locateFile is locate for an argument that must name a file, not a
// directory: a path that ends in a slash is refused.
func (o *Options) locateFile(env *core.ToolEnv, tool, arg string) (location, *failure) {
	if len(arg) > 1 && os.IsPathSeparator(arg[len(arg)-1]) {
		return location{}, failf("%s: file_path ends with a slash, which names a directory, not a file: %q.", tool, short(arg, 200))
	}
	return o.locate(env, tool, "file_path", arg)
}

// errLinkLoop is the reason followLinks gives for a path with too many
// links.
var errLinkLoop = errors.New("too many symbolic links")

// followError says where following a path went wrong.
type followError struct {
	// at is the last thing on the path that was reached: the directory
	// that could not be looked into, or the file that something further
	// along the path treats as a directory.
	at  string
	err error
}

// Error says where on the path the trouble was and what it was.
func (e *followError) Error() string { return "at " + e.at + ": " + e.err.Error() }

// Unwrap returns the system's error.
func (e *followError) Unwrap() error { return e.err }

// FollowLinks is followLinks, for the permission engine's Resolve: a
// link's target is walked piece by piece from where the link is, as the
// system walks it, never tidied as text first ("a/../x" with a a link is
// not "x").
func FollowLinks(p string) (real string, exists bool, err error) { return followLinks(p) }

// followLinks resolves every symbolic link in the absolute, clean path p.
// Where the path stops existing, what is left of it is appended as it is
// written, and exists is false: that is the path a new file would be made
// at. A link whose target is missing counts as not existing, and real is
// then the target.
func followLinks(p string) (real string, exists bool, err error) {
	root := filepath.VolumeName(p) + string(filepath.Separator)
	parts := splitPath(p[len(root):])
	cur := root
	hops := 0
	for i := 0; i < len(parts); i++ {
		name := parts[i]
		switch name {
		case "", ".":
			continue
		case "..":
			cur = filepath.Dir(cur)
			continue
		}
		next := filepath.Join(cur, name)
		info, err := os.Lstat(next)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				rest := append([]string{next}, parts[i+1:]...)
				return filepath.Join(rest...), false, nil
			}
			return "", false, &followError{at: cur, err: underlying(err)}
		}
		if info.Mode()&fs.ModeSymlink == 0 {
			cur = next
			continue
		}
		hops++
		if hops > maxLinkHops {
			return "", false, &followError{at: next, err: errLinkLoop}
		}
		target, err := os.Readlink(next)
		if err != nil {
			return "", false, &followError{at: next, err: underlying(err)}
		}
		tail := parts[i+1:]
		if filepath.IsAbs(target) {
			cur = filepath.VolumeName(target) + string(filepath.Separator)
			target = target[len(cur):]
		}
		parts = append(splitPath(target), tail...)
		i = -1
	}
	return cur, true, nil
}

// underlying strips the operation and path that the os package wraps a
// system error in, leaving the reason.
func underlying(err error) error {
	var pe *fs.PathError
	if errors.As(err, &pe) {
		return pe.Err
	}
	var le *os.LinkError
	if errors.As(err, &le) {
		return le.Err
	}
	return err
}

func splitPath(p string) []string {
	return strings.FieldsFunc(p, func(r rune) bool { return r == filepath.Separator || r == '/' })
}

// pathTrouble puts into words why a path could not be followed.
func pathTrouble(abs string, err error) *failure {
	at := ""
	var fe *followError
	if errors.As(err, &fe) {
		at = fe.at
	}
	switch {
	case errors.Is(err, errLinkLoop) || errors.Is(err, syscall.ELOOP):
		return failf("%s cannot be followed: its symbolic links go round in a loop (or there are more than %d of them).", abs, maxLinkHops)
	case errors.Is(err, syscall.ENOTDIR):
		if at != "" {
			return failf("%s cannot exist: %s, on the way to it, is a file, not a directory.", abs, at)
		}
		return failf("%s cannot exist: something on the way to it is a file, not a directory.", abs)
	case errors.Is(err, fs.ErrPermission):
		if at != "" {
			return failf("%s cannot be reached: permission denied in %s.", abs, at)
		}
		return failf("%s cannot be reached: permission denied.", abs)
	case errors.Is(err, syscall.ENAMETOOLONG):
		return failf("%s cannot be used: the path, or one name in it, is too long.", short(abs, 200))
	}
	return failf("%s cannot be reached: %v.", abs, underlying(err))
}

// kindOf names what a file is when it is not a plain file, for refusals.
func kindOf(mode fs.FileMode) string {
	switch {
	case mode.IsDir():
		return "a directory"
	case mode&fs.ModeNamedPipe != 0:
		return "a named pipe (FIFO)"
	case mode&fs.ModeSocket != 0:
		return "a socket"
	case mode&fs.ModeCharDevice != 0:
		return "a character device"
	case mode&fs.ModeDevice != 0:
		return "a block device"
	case mode&fs.ModeSymlink != 0:
		return "a symbolic link"
	case mode.IsRegular():
		return "a file"
	}
	return "not a regular file"
}

// isLink reports whether the path itself, not what it leads to, is a
// symbolic link.
func isLink(path string) bool {
	info, err := os.Lstat(path)
	return err == nil && info.Mode()&fs.ModeSymlink != 0
}

// openRegular opens a file for reading and makes sure that what was opened
// is a regular file. Opening never waits: a FIFO put in the file's place
// between the check and the open cannot hang the call.
func openRegular(path string) (*os.File, fs.FileInfo, *failure) {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, nil, openTrouble(path, err)
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, nil, failf("%s could not be examined: %v.", path, err)
	}
	if !info.Mode().IsRegular() {
		f.Close()
		return nil, nil, failf("%s is %s, not a file with contents to read.", path, kindOf(info.Mode()))
	}
	return f, info, nil
}

// openTrouble puts into words why a file could not be opened.
func openTrouble(path string, err error) *failure {
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return failf("%s does not exist.", path)
	case errors.Is(err, fs.ErrPermission):
		return failf("%s cannot be opened: permission denied.", path)
	}
	var pe *fs.PathError
	if errors.As(err, &pe) {
		return failf("%s cannot be opened: %v.", path, pe.Err)
	}
	return failf("%s cannot be opened: %v.", path, err)
}
