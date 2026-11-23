package sandbox

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// bwrapArgs is the bubblewrap command line that runs argv in dir under the
// policy. stat says whether a path is there, and whether it is a folder.
//
// The machine is mounted read-only, with a /dev and a /proc of the
// sandbox's own; the sandbox's temporary folder is /tmp; the folders it
// may write in are mounted over that writable, then the read-only paths
// inside them, then the denied ones hidden (an empty folder, or the null
// device for a file), then the paths inside those it may read after all.
// A later mount covers an earlier one, which is what makes that order the
// rule. It has its own processes (so it can signal no other) and its own
// network, where nothing is reachable, unless the policy gives it the
// machine's; it may make no user namespace of its own where bubblewrap
// can refuse it one. It dies with gnosh.
//
// With the filesystem left open (FilesystemOff) the machine is mounted as
// it is, writable, and nothing of the files is confined.
func bwrapArgs(bwrap string, p Policy, dir string, argv []string, stat func(string) (isDir, ok bool)) []string {
	there := func(s string) bool { _, ok := stat(s); return ok }
	a := []string{bwrap, "--die-with-parent"}
	if p.blockUserns {
		a = append(a, "--unshare-user", "--disable-userns")
	}
	a = append(a, "--unshare-pid", "--unshare-ipc")
	if filters(p) {
		// gnosh, the relay or the filter, is the sandbox's first process,
		// in place of bubblewrap's own init: that one is dumpable and has
		// no filter, so where any process of the same user may be traced
		// (kernel.yama.ptrace_scope 0, as on Debian and Fedora) a command
		// could have it make a Unix socket for it (the third sandbox
		// review).
		a = append(a, "--as-pid-1")
	}
	if !p.Network {
		a = append(a, "--unshare-net")
	}
	if p.FilesystemOff {
		a = append(a, "--bind", "/", "/", "--dev", "/dev")
	} else {
		a = append(a, "--ro-bind", "/", "/", "--dev", "/dev")
	}
	if !p.HostProc {
		// (Else the machine's /proc stays, mounted with the rest.)
		a = append(a, "--proc", "/proc")
	}
	// The machine's sockets out of sight: a read-only mount does not stop
	// a connection to one, and systemd's (a job outside, as root with its
	// private socket), D-Bus's and docker's lie under /run (the sandbox
	// review). (/var/run is a link to it where it is not a folder.)
	for _, r := range []string{"/run", "/var/run"} {
		if isDir, ok := stat(r); ok && isDir && !isLink(r) && !p.AllUnixSockets {
			a = append(a, "--tmpfs", r)
		}
	}
	if p.FilesystemOff {
		if p.Proxy != "" && p.Relay != "" && there(p.Proxy) {
			a = append(a, "--ro-bind", p.Proxy, p.Proxy) // (pinned: below)
		}
		a = append(a, "--chdir", filepath.Clean(dir), "--")
		if p.Proxy != "" && p.Relay != "" {
			// (The proxy's socket where it is: the machine's /tmp is the
			// sandbox's.)
			a = append(a, p.Relay, RelayWord, p.Proxy)
		}
		return append(a, filterFirst(p, argv)...)
	}
	if p.Temp != "" && there(p.Temp) {
		a = append(a, "--bind", p.Temp, "/tmp")
	}
	for _, w := range kept(p.Write, there) {
		a = append(a, "--bind", w, w)
	}
	for _, r := range kept(p.ReadOnly, there) {
		a = append(a, "--ro-bind", r, r)
	}
	// gnosh's own files after the read-only paths, which may hold them
	// (the session's folder under gnosh's data folder): the shell must
	// write where it ends up.
	for _, e := range p.Expose {
		if e.Path == "" || !there(e.Path) {
			continue
		}
		if e.Write {
			a = append(a, "--bind", e.Path, e.Path)
		} else {
			a = append(a, "--ro-bind", e.Path, e.Path)
		}
	}
	denied := kept(p.DenyRead, there)
	// A denied folder is an empty one, made read-only last: the paths
	// opened again inside it need their places made in it first.
	var emptied []string
	hide := func(d string) {
		if isDir, _ := stat(d); isDir {
			a = append(a, "--tmpfs", d)
			emptied = append(emptied, d)
		} else {
			a = append(a, "--ro-bind", "/dev/null", d)
		}
	}
	for _, d := range denied {
		hide(d)
	}
	var opened []string
	for _, r := range kept(p.AllowRead, there) {
		// (Only a path inside a denied one needs opening again.)
		if slices.ContainsFunc(denied, func(d string) bool { return r != d && under(r, d) }) {
			a = append(a, "--ro-bind", r, r)
			opened = append(opened, r)
		}
	}
	// The folders it writes in, and gnosh's own files, inside a denied
	// path come back as they were: a denyRead of the home folder had taken
	// the project from it, and the files its shell needs (the sandbox
	// review).
	for _, w := range kept(p.Write, there) {
		if slices.ContainsFunc(denied, func(d string) bool { return under(w, d) }) {
			a = append(a, "--bind", w, w)
			opened = append(opened, w)
		}
	}
	for _, e := range p.Expose {
		if e.Path != "" && there(e.Path) && slices.ContainsFunc(denied, func(d string) bool { return under(e.Path, d) }) {
			if e.Write {
				a = append(a, "--bind", e.Path, e.Path)
			} else {
				a = append(a, "--ro-bind", e.Path, e.Path)
			}
		}
	}
	if len(opened) > 0 {
		// (And the read-only paths inside those, again.)
		for _, r := range kept(p.ReadOnly, there) {
			if slices.ContainsFunc(opened, func(o string) bool { return r != o && under(r, o) }) {
				a = append(a, "--ro-bind", r, r)
			}
		}
	}
	// A denied path inside one opened again stays denied: the narrower
	// wins, as in Claude Code.
	for _, d := range present(p.DenyRead, there) {
		if slices.ContainsFunc(opened, func(r string) bool { return d != r && under(d, r) }) {
			hide(d)
		}
	}
	// The credentials the settings hide, last, so that nothing opened
	// above brings one back (a bind of a hidden path, or of a folder
	// that holds one, is covered by this mount; a mount cannot be renamed
	// away, and moves with a folder renamed that holds it).
	for _, h := range present(p.Hidden, there) {
		hide(h)
	}
	// The proxy's socket, pinned where the relay finds it: in the /tmp the
	// command writes, it could put a link to another socket in its place
	// (docker's, an agent's), and the relay, which may make Unix sockets,
	// would connect to that (the second sandbox review). A mount point is
	// neither deleted nor renamed, and a socket on a read-only mount
	// still takes connections.
	if inside, ok := insideTemp(p); ok && p.Relay != "" && there(p.Proxy) {
		a = append(a, "--ro-bind", p.Proxy, inside)
	}
	// gnosh's own program, which runs inside as the relay and the filter:
	// in sight wherever it lies (under the real /tmp, in a denied folder),
	// and not for the command to change, as it runs outside later too.
	var self []string
	if _, ok := insideTemp(p); ok && p.Relay != "" {
		self = append(self, p.Relay)
	}
	if filters(p) && p.Filter != p.Relay {
		self = append(self, p.Filter)
	}
	for _, s := range self {
		if there(s) {
			a = append(a, "--ro-bind", s, s)
		}
	}
	for _, d := range emptied {
		a = append(a, "--remount-ro", d)
	}
	a = append(a, "--chdir", filepath.Clean(dir), "--")
	if inside, ok := insideTemp(p); ok && p.Relay != "" {
		// The way out to the proxy: gnosh, run inside, relays the
		// sandbox's own loopback to the proxy's socket, which is in the
		// sandbox's /tmp.
		a = append(a, p.Relay, RelayWord, inside)
	}
	return append(a, filterFirst(p, argv)...)
}

// filters reports whether the command is to run behind the filter that
// refuses it new Unix sockets.
func filters(p Policy) bool { return p.Filter != "" && !p.AllUnixSockets && canFilter() }

// filterFirst is argv as the sandbox runs it: through gnosh's filter,
// which then becomes it, where the policy has one; after the relay, which
// must make Unix sockets.
func filterFirst(p Policy, argv []string) []string {
	if !filters(p) {
		return argv
	}
	return append([]string{p.Filter, FilterWord}, argv...)
}

// insideTemp is the proxy's socket as the sandbox sees it: in its /tmp,
// which is Temp.
func insideTemp(p Policy) (string, bool) {
	if p.Proxy == "" || p.Temp == "" {
		return "", false
	}
	rel, err := filepath.Rel(p.Temp, p.Proxy)
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") {
		return "", false
	}
	return filepath.Join("/tmp", rel), true
}

// isLink reports whether the path is a symbolic link.
func isLink(p string) bool {
	info, err := os.Lstat(p)
	return err == nil && info.Mode()&os.ModeSymlink != 0
}
