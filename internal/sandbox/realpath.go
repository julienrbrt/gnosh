package sandbox

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// realPath is the path in the system's own spelling, links resolved, as
// Seatbelt matches it; for a path not there yet (a socket made later, a
// file denied before it is written, in folders not made yet), the nearest
// folder above it that is there, resolved, with the rest as written: what
// it will be when it is there. (Only its folder had been resolved, so a
// denied path two folders down from one not there yet, under a link such
// as /tmp, matched neither spelling: the third sandbox review.)
func realPath(s string) string {
	s = filepath.Clean(s)
	rest := ""
	for dir := s; ; {
		if r, err := filepath.EvalSymlinks(dir); err == nil {
			return filepath.Join(r, rest)
		}
		up := filepath.Dir(dir)
		if up == dir {
			return s
		}
		rest = filepath.Join(filepath.Base(dir), rest)
		dir = up
	}
}

// realPaths is realPath of each path of the list, the empty left out.
func realPaths(list []string) []string {
	out := make([]string, 0, len(list))
	for _, s := range list {
		if s != "" {
			out = append(out, realPath(s))
		}
	}
	return out
}

// maxHops is how many links a path may pass through, as Linux allows.
const maxHops = 40

// way is what lies on the way to the path s that a command could change,
// where it may write (in, which takes a resolved path): each link met, by
// its own name, its folder resolved, which a file renamed over, or another
// link, would take the path with it, to a file of the command's own; and
// each folder not there yet, which the command could make a link to a
// folder of its own, to write the path in that. A rule on the path, which
// Seatbelt matches as its links resolve, holds against neither (both shown
// on scratch files, 2026-11-20: a ~/.zshrc kept by a link, as dotfile
// managers keep it, and a .gnosh not there yet).
func way(s string, in func(string) bool) (links, absent []string) {
	rest := strings.Split(filepath.Clean(s), "/")
	cur := "/"
	for hops := 0; len(rest) > 0; {
		name := rest[0]
		rest = rest[1:]
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
			// Not there: every folder from here to the path's own is the
			// command's to make.
			leaf := filepath.Join(append([]string{next}, rest...)...)
			for d := filepath.Dir(leaf); d != cur && under(d, cur); d = filepath.Dir(d) {
				if in(d) {
					absent = append(absent, d)
				}
			}
			return links, absent
		}
		if info.Mode()&os.ModeSymlink == 0 {
			cur = next
			continue
		}
		if in(next) {
			links = append(links, next)
		}
		target, err := os.Readlink(next)
		if hops++; err != nil || hops > maxHops {
			return links, absent
		}
		if filepath.IsAbs(target) {
			cur = "/"
		}
		rest = append(strings.Split(target, "/"), rest...)
	}
	return links, absent
}

// LinksOnTheWay are the links on the way to the paths the policy keeps
// from the command that the command could replace (onTheWay): on Linux
// they are not kept from it, a mount cannot be put on a link, which
// /sandbox says.
func LinksOnTheWay(p Policy) []string {
	links, _ := onTheWay(p)
	return links
}

// onTheWay is, for the paths the policy keeps from the command (read-only,
// denied, hidden), what lies on the way to them that the command could
// change (way): the links, to be kept read-only by their own names, and
// the folders not there yet, where no link may be made (noLinks); both in
// the system's own spelling already, which realPath would take from a
// link (it is the link's target).
func onTheWay(p Policy) (links, absent []string) {
	writable := realPaths(append(append([]string(nil), p.Write...), p.Temp))
	for _, e := range p.Expose {
		if e.Write && e.Path != "" {
			writable = append(writable, realPath(e.Path))
		}
	}
	in := func(s string) bool {
		return slices.ContainsFunc(writable, func(w string) bool { return under(s, w) })
	}
	for _, list := range [][]string{p.ReadOnly, p.DenyRead, p.Hidden} {
		for _, s := range list {
			if filepath.IsAbs(s) {
				l, a := way(s, in)
				links, absent = append(links, l...), append(absent, a...)
			}
		}
	}
	slices.Sort(links)
	slices.Sort(absent)
	return slices.Compact(links), slices.Compact(absent)
}
