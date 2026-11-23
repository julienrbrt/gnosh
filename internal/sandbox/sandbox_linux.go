package sandbox

import (
	"errors"
	"os"
	"path/filepath"
)

// check tries bubblewrap once for the setup: first as the sandbox is
// made, its commands refused user namespaces of their own; where this
// bubblewrap cannot do that (before 0.8), without, which the person is
// told.
func check(s Setup) Status {
	bwrap := lookFor("bwrap")
	if s.Bwrap != "" {
		if !filepath.IsAbs(s.Bwrap) || !isProgram(s.Bwrap) {
			return Status{Tool: s.Bwrap, Why: "sandbox.bwrapPath names " + s.Bwrap + ", which is no program here"}
		}
		bwrap = s.Bwrap
	}
	if bwrap == "" {
		return Status{Why: "bubblewrap is not installed (apt-get install bubblewrap, or dnf install bubblewrap)"}
	}
	dir, err := os.Getwd()
	if err != nil {
		dir = "/"
	}
	trial := []string{"/bin/sh", "-c", ":"}
	p := Policy{HostProc: s.HostProc, blockUserns: !s.HostProc}
	if p.blockUserns && try(bwrapArgs(bwrap, p, dir, trial, statPath)) == "" {
		return withFilter(Status{OK: true, Tool: bwrap, UsernsRefused: true}, bwrap, p, dir, s.Filter)
	}
	p.blockUserns = false
	if said := try(bwrapArgs(bwrap, p, dir, trial, statPath)); said != "" {
		why := "bubblewrap cannot make its sandbox here (" + said + "); on Ubuntu 24.04 and later, the AppArmor rule on unprivileged namespaces is the usual cause"
		if !s.HostProc {
			why += ", and in a container without its own /proc, sandbox.enableWeakerNestedSandbox"
		}
		return Status{Tool: bwrap, Why: why}
	}
	st := Status{OK: true, Tool: bwrap}
	if !s.HostProc {
		// (With the machine's /proc, bubblewrap has no /proc of the
		// sandbox's own to set the limit in: the setting's own cost.)
		st.Weaker = "this bubblewrap cannot keep sandboxed commands from making user namespaces of their own (that takes bubblewrap 0.8 or later)"
	}
	return withFilter(st, bwrap, p, dir, s.Filter)
}

// withFilter tries the filter for Unix sockets once, where the setup names
// the program that puts it on: where it cannot be put on (a kernel or a
// container that refuses it), the sandbox runs without it and says so, as
// Claude Code's does with no filter, rather than refuse every command.
func withFilter(st Status, bwrap string, p Policy, dir, filter string) Status {
	switch {
	case filter == "":
	case !canFilter():
		st.NoFilter = "nothing keeps sandboxed commands from opening Unix sockets on this machine (gnosh's filter is for amd64 and arm64)"
	default:
		p.Filter = filter
		if said := try(bwrapArgs(bwrap, p, dir, []string{"/bin/sh", "-c", ":"}, statPath)); said != "" {
			st.NoFilter = "the filter that keeps sandboxed commands from opening Unix sockets cannot be put on here (" + said + "), so they can open them"
		} else {
			st.Filters = true
		}
	}
	return st
}

// Wrap returns the program and arguments that run argv, a program and its
// arguments, in the folder dir inside the sandbox the policy describes.
func Wrap(p Policy, dir string, argv []string) ([]string, error) {
	st := CheckWith(SetupOf(p))
	if !st.OK {
		return nil, errors.New(st.Why)
	}
	p.blockUserns = st.UsernsRefused && !p.UserNamespaces && !p.HostProc
	if !st.Filters {
		// (It could not be put on here: said at startup and in /sandbox.)
		p.Filter = ""
	}
	// The paths mounted on, and the folder the command starts in, with
	// their links resolved: bubblewrap finds where to mount inside the
	// sandbox it is making, where a link to an absolute path leads
	// nowhere, and it stopped (a session's folder reached through one, a
	// ~/.zshrc a dotfile manager keeps by one: every command failed); and
	// a link in the machine's /tmp is not in the sandbox's. A link on the
	// way that the command could replace is not kept from it: a mount
	// cannot be put on a link (spec/DECISIONS.md 379).
	p.Write, p.ReadOnly, p.DenyRead, p.AllowRead, p.Hidden = realPaths(p.Write), realPaths(p.ReadOnly), realPaths(p.DenyRead), realPaths(p.AllowRead), realPaths(p.Hidden)
	for i := range p.Expose {
		if p.Expose[i].Path != "" {
			p.Expose[i].Path = realPath(p.Expose[i].Path)
		}
	}
	return bwrapArgs(st.Tool, p, realPath(dir), argv, statPath), nil
}

// statPath says whether a path is there, and whether it is a folder,
// following links as bubblewrap's mounts do.
func statPath(p string) (isDir, ok bool) {
	info, err := os.Stat(p)
	if err != nil {
		return false, false
	}
	return info.IsDir(), true
}
