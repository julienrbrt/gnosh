package sandbox

import (
	"path/filepath"
	"slices"
	"strings"
)

// seatbeltProfile is the Seatbelt profile (macOS's sandbox language) for
// the policy: everything is allowed but writing outside the folders the
// policy names and a few devices, writing the read-only paths, reading
// the denied ones (but those opened again inside them), the network
// (Unix sockets among it), and the services that would start a program
// outside the sandbox or fetch from the network for it. A later rule wins
// over an earlier one, which is what makes the order the rule. The paths
// must be the system's own spelling of them, links resolved (/private/tmp,
// not /tmp): Seatbelt matches what it is asked about, which is that.
func seatbeltProfile(p Policy) string {
	var b strings.Builder
	b.WriteString("(version 1)\n(allow default)\n")
	// What would start a program outside the sandbox: an Apple Event to
	// another application, a launchd job, opening a file or an app through
	// Launch Services (the sandbox review; Claude Code blocks these too).
	// And the certificate trust service, which fetches what a certificate
	// names for whoever asks: a way out past the proxy, closed as Claude
	// Code closes it (Go's programs then cannot check a certificate).
	if !p.AppleEvents {
		b.WriteString("(deny appleevent-send)\n")
	}
	b.WriteString("(deny job-creation)\n")
	// Signals to its own processes only, as Linux's sandbox, with a pid
	// namespace of its own, sees no other: not to gnosh, nor the person's
	// editor, servers or agents (tried on a Mac: an outside process was
	// reachable before, refused with this, and the command's own children
	// and its shell are still signalled).
	b.WriteString("(deny signal)\n(allow signal (target same-sandbox))\n")
	b.WriteString("(deny mach-lookup (global-name \"com.apple.coreservices.launchservicesd\") (global-name \"com.apple.coreservices.quarantine-resolver\")")
	if !p.TrustService {
		b.WriteString(" (global-name \"com.apple.trustd.agent\")")
	}
	if !p.FilesystemOff && (len(p.DenyRead) > 0 || len(p.Hidden) > 0) {
		// Spotlight, which reads the files the command may not and
		// answers questions about what is in them (mdfind's queries, one
		// match at a time, could spell a denied file out: a lead of the
		// third sandbox review; checked: denied, mdfind finds nothing).
		// allowMachLookup can name it again.
		b.WriteString(" (global-name-prefix \"com.apple.metadata.mds\")")
	}
	b.WriteString(")\n")
	var names []string
	for _, n := range p.MachLookup {
		switch {
		case n == "*":
			names = []string{"*"}
		case len(names) == 1 && names[0] == "*":
		case strings.HasSuffix(n, "*"):
			names = append(names, "(global-name-prefix "+sbString(strings.TrimSuffix(n, "*"))+")")
		case n != "":
			names = append(names, "(global-name "+sbString(n)+")")
		}
	}
	switch {
	case len(names) == 1 && names[0] == "*":
		b.WriteString("(allow mach-lookup)\n")
	case len(names) > 0:
		b.WriteString("(allow mach-lookup " + strings.Join(names, " ") + ")\n")
	}
	if !p.Network {
		// (Unix sockets among it.)
		b.WriteString("(deny network*)\n")
		if p.Proxy != "" {
			// The one way out: the proxy, on this machine.
			b.WriteString("(allow network-outbound (remote ip " + sbString(strings.Replace(p.Proxy, "127.0.0.1:", "localhost:", 1)) + "))\n")
		}
		var socks []string
		for _, s := range p.UnixSockets {
			if s != "" {
				socks = append(socks, "  (remote unix-socket (path-literal "+sbString(s)+"))")
			}
		}
		switch {
		case p.AllUnixSockets:
			b.WriteString("(allow network-outbound (remote unix-socket))\n")
		case len(socks) > 0:
			b.WriteString("(allow network-outbound\n" + strings.Join(socks, "\n") + ")\n")
		}
		if p.LocalBinding {
			// A port of its own, on any of the machine's addresses, and
			// any port of the machine's own.
			b.WriteString("(allow network-bind (local ip \"*:*\"))\n(allow network-inbound (local ip \"*:*\"))\n(allow network-outbound (remote ip \"localhost:*\"))\n")
		}
	}
	// The terminals are no command's (a command has none of its own, and
	// another tab's is not its to read or to draw on: the sandbox review).
	b.WriteString("(deny file-read* file-write* (literal \"/dev/tty\") (regex #\"^/dev/ttys[0-9]+$\"))\n")
	if p.FilesystemOff {
		return b.String()
	}
	// No volume put over a folder (hdiutil attach -mountpoint, diskutil
	// mount -mountPoint, diskutil image attach --mountPoint): the mount is
	// made by a service outside the sandbox, which asks the sandbox only
	// this, so a disk image of the command's own over a kept folder (a
	// .claude, a .git/hooks) gave gnosh, or git, its files there (tried on
	// scratch files on a Mac, 2026-11-20). A volume mounted where macOS
	// puts one, under /Volumes, is not stopped, and covers nothing.
	b.WriteString("(deny file-mount)\n")
	// No application's preferences written: the preferences service
	// (cfprefsd, defaults write) writes them outside the sandbox, past
	// every path rule, and Terminal's, for one, can name a command to run
	// in each new window (tried with a made-up domain, 2026-11-20).
	// Reading them is left.
	b.WriteString("(deny user-preference-write)\n")
	b.WriteString("(deny file-write*)\n(allow file-write*\n")
	for _, w := range append(append([]string(nil), p.Write...), p.Temp) {
		if w != "" {
			b.WriteString("  (subpath " + sbString(w) + ")\n")
		}
	}
	// The devices a shell writes to whatever it runs.
	b.WriteString("  (literal \"/dev/null\") (literal \"/dev/zero\") (literal \"/dev/dtracehelper\")\n")
	b.WriteString("  (regex #\"^/dev/fd/[0-9]+$\"))\n")
	// (Again after the writes allowed: a folder written in may hold them.)
	b.WriteString("(deny file-read* file-write* (literal \"/dev/tty\") (regex #\"^/dev/ttys[0-9]+$\"))\n")
	rule := func(what string, paths []string) {
		var some []string
		for _, s := range paths {
			if s != "" {
				some = append(some, "  (subpath "+sbString(s)+")")
			}
		}
		if len(some) > 0 {
			b.WriteString("(" + what + "\n" + strings.Join(some, "\n") + ")\n")
		}
	}
	// Nothing moved out of a path it may not read (a rename or a delete,
	// Seatbelt's file-write-unlink; writing in it is another matter):
	// moved, a file is out from under the rule's path (the sandbox
	// review). But in what is opened again inside one, as the reads are
	// below.
	opened := append(append([]string(nil), p.AllowRead...), p.Write...)
	for _, e := range p.Expose {
		opened = append(opened, e.Path)
	}
	rule("deny file-write-unlink", p.DenyRead)
	// Moving and deleting come back only where writing does: what is
	// opened again to be read only (allowRead, a file of gnosh's it reads)
	// stays where it is, as its read-only mount keeps it on Linux (the
	// second sandbox review: a file allowRead opened in a denied folder
	// was deleted, and moved out).
	writes := append([]string(nil), p.Write...)
	for _, e := range p.Expose {
		if e.Write {
			writes = append(writes, e.Path)
		}
	}
	var inside, again []string
	for _, o := range writes {
		for _, d := range p.DenyRead {
			if o != "" && d != "" && o != d && under(o, d) {
				inside = append(inside, o)
				break
			}
		}
	}
	// A denied path inside one opened again stays denied: the narrower
	// wins, as in Claude Code.
	for _, d := range p.DenyRead {
		for _, r := range opened {
			if d != "" && r != "" && d != r && under(d, r) {
				again = append(again, d)
				break
			}
		}
	}
	rule("allow file-write-unlink", inside)
	rule("deny file-write-unlink", again)
	rule("deny file-write*", p.ReadOnly)
	// gnosh's own files after the read-only paths, which may hold them.
	var writable []string
	for _, e := range p.Expose {
		if e.Write && e.Path != "" {
			writable = append(writable, "  (literal "+sbString(e.Path)+")")
		}
	}
	if len(writable) > 0 {
		b.WriteString("(allow file-write*\n" + strings.Join(writable, "\n") + ")\n")
	}
	rule("deny file-read*", p.DenyRead)
	// Opened again: allowRead, and the folders it writes in and gnosh's
	// own files, which a denied path must not take from it.
	if len(p.DenyRead) > 0 {
		rule("allow file-read*", opened)
	}
	rule("deny file-read*", again)
	// The credentials the settings hide, last, so that nothing opened
	// above brings one back; nor may one be changed, or moved away.
	rule("deny file-read* file-write*", p.Hidden)
	// No folder that holds a path kept from the command may be renamed or
	// taken away, up to the root: renamed, a folder takes what is in it
	// out from under the rules' paths, into a folder it may read and
	// write (the sandbox review: the project folder itself, moved into
	// the sandbox's temporary folder, gave up a denied file in it).
	var held []string
	for _, list := range [][]string{p.DenyRead, p.ReadOnly, p.Hidden} {
		for _, path := range list {
			for dir := filepath.Dir(path); dir != "/" && dir != "." && dir != path; dir = filepath.Dir(dir) {
				if !slices.Contains(held, dir) {
					held = append(held, dir)
				}
			}
		}
	}
	if len(held) > 0 {
		slices.Sort(held)
		lits := make([]string, len(held))
		for i, h := range held {
			lits[i] = "  (literal " + sbString(h) + ")"
		}
		b.WriteString("(deny file-write-unlink\n" + strings.Join(lits, "\n") + ")\n")
	}
	// No link made, or renamed in, where a folder not there yet lies on
	// the way to a path kept from the command (onTheWay): a link to a
	// folder of its own, it would write the path in that. A folder of its
	// own may be made there.
	if len(p.noLinks) > 0 {
		lits := make([]string, len(p.noLinks))
		for i, d := range p.noLinks {
			lits[i] = "  (require-all (literal " + sbString(d) + ") (vnode-type SYMLINK))"
		}
		b.WriteString("(deny file-write-create\n" + strings.Join(lits, "\n") + ")\n")
	}
	if len(p.DenyRead) > 0 || len(p.Hidden) > 0 {
		// No folder cloned whole: clonefile(2) copies a folder's tree in
		// the kernel, and Seatbelt asks only about the folder, so a copy of
		// one the command may read brought the denied and hidden files in
		// it out, readable (the second sandbox review, tested). A file is
		// still cloned one by one (cp -c, ditto --clone), where each is
		// asked about.
		b.WriteString("(deny file-clone (vnode-type DIRECTORY))\n")
	}
	return b.String()
}

// sbString is a string in Seatbelt's language: in double quotes, with a
// backslash before a quote or a backslash.
func sbString(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}
