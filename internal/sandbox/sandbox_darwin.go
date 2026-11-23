package sandbox

import (
	"errors"
)

// check tries Seatbelt once (the setup is bubblewrap's).
func check(Setup) Status {
	tool := lookFor("sandbox-exec")
	if tool == "" {
		return Status{Why: "this macOS has no sandbox-exec"}
	}
	if said := try([]string{tool, "-p", "(version 1)(allow default)", "/usr/bin/true"}); said != "" {
		return Status{Tool: tool, Why: "Seatbelt cannot make its sandbox here (" + said + ")"}
	}
	return Status{OK: true, Tool: tool}
}

// Wrap returns the program and arguments that run argv, a program and its
// arguments, in the folder dir inside the sandbox the policy describes.
// The paths are put in the system's own spelling, links resolved
// (/private/tmp for /tmp), which is what Seatbelt matches; and what lies
// on the way to a path kept from the command is kept too (onTheWay).
func Wrap(p Policy, dir string, argv []string) ([]string, error) {
	st := Check()
	if !st.OK {
		return nil, errors.New(st.Why)
	}
	var links []string
	if !p.FilesystemOff {
		links, p.noLinks = onTheWay(p)
	}
	p.Write, p.ReadOnly, p.DenyRead, p.AllowRead = realPaths(p.Write), append(realPaths(p.ReadOnly), links...), realPaths(p.DenyRead), realPaths(p.AllowRead)
	p.UnixSockets, p.Hidden = realPaths(p.UnixSockets), realPaths(p.Hidden)
	if p.Temp != "" {
		p.Temp = realPath(p.Temp)
	}
	for i := range p.Expose {
		if p.Expose[i].Path != "" {
			p.Expose[i].Path = realPath(p.Expose[i].Path)
		}
	}
	return append([]string{st.Tool, "-p", seatbeltProfile(p)}, argv...), nil
}
