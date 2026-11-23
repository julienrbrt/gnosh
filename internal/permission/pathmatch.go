package permission

import (
	"errors"
	"path"
	"slices"
	"strings"
)

// pathPattern is a Read or Edit rule's specifier made ready to match
// against cleaned absolute paths. The syntax is gitignore's: * and ? stay
// within one path segment, [...] is a character class, ** crosses
// directories, and a backslash takes the meaning from the next character.
type pathPattern struct {
	// segs are the pattern's segments from the filesystem root; "**"
	// stands for any number of directories, none included.
	segs []string
	// orig are segs as written, before fold made them keys: the path a
	// rule names, for the sandbox's lists (SandboxPaths).
	orig []string
	// relative is set for patterns anchored at the current directory by
	// being written as "path" or "./path". These are the rules a "!"
	// pattern can reopen.
	relative bool
	// below is set when a match on a directory also covers everything
	// under it, as in gitignore. Deny and ask rules have it; allow rules
	// must match the path itself.
	below bool
	// fold is set when names are compared by their keys (guardKey): with
	// letter case, composed and taken-apart accents, and a few other
	// spellings that some file systems take for the same name, all
	// counted as one. Deny and ask rules have it; an allow rule never
	// gains from it.
	fold bool
}

// parsePathPattern anchors a specifier and splits it into segments.
//
//	//path   the filesystem root
//	~/path   the home directory
//	/path    the directory of the settings source (the layer's Base)
//	./path   the working directory
//	path     the working directory; a bare file name at any depth below it
func (e *Engine) parsePathPattern(spec string, list List, ly *layer, negate bool) (pathPattern, error) {
	return e.parsePattern(spec, list, ly, negate, false)
}

// parseDirPattern reads a Cd rule's pattern, as Claude Code does: the
// anchors of Read and Edit rules, but matched against the whole of a
// folder's path, not as gitignore matches. * is one segment and ** any
// number of them, so a trailing /** takes in the folder it names too, and
// a pattern without an anchor starts at the working directory. A slash at
// the end changes nothing.
func (e *Engine) parseDirPattern(spec string, list List, ly *layer) (pathPattern, error) {
	for len(spec) > 1 && spec != "//" && strings.HasSuffix(spec, "/") && !strings.HasSuffix(spec, "\\/") {
		spec = spec[:len(spec)-1]
	}
	return e.parsePattern(spec, list, ly, false, true)
}

// parsePattern is parsePathPattern, or with whole parseDirPattern.
func (e *Engine) parsePattern(spec string, list List, ly *layer, negate, whole bool) (pathPattern, error) {
	p := pathPattern{below: list != ListAllow && !whole, fold: list != ListAllow}
	if spec == "" {
		return p, errors.New("the path pattern is empty")
	}
	dirOnly := strings.HasSuffix(spec, "/") && !strings.HasSuffix(spec, "\\/") && !whole
	var base string
	rest := spec
	switch {
	case negate:
		// A "!" pattern is always read from the working directory.
		rest = strings.TrimPrefix(strings.TrimPrefix(rest, "./"), "/")
		base = e.cwd
		p.relative = true
	case strings.HasPrefix(spec, "//"):
		rest = spec[2:]
		base = "/"
	case spec == "~" || strings.HasPrefix(spec, "~/"):
		if e.home == "" {
			return p, errors.New("the pattern starts at the home directory, and no home directory is known")
		}
		rest = strings.TrimPrefix(spec[1:], "/")
		base = e.home
	case strings.HasPrefix(spec, "/"):
		if ly.base == "" {
			return p, errors.New("a pattern that starts with one slash is read from the directory of the settings file it is in, and this source has none; start it with // for the filesystem root or ~/ for the home directory")
		}
		rest = spec[1:]
		base = ly.base
	case strings.HasPrefix(spec, "./"):
		rest = spec[2:]
		base = e.cwd
		p.relative = true
	default:
		base = e.cwd
		p.relative = true
	}

	var segs []string
	for _, s := range strings.Split(rest, "/") {
		switch s {
		case "", ".":
		case "..":
			return p, errors.New("a path pattern cannot hold \"..\"")
		default:
			segs = append(segs, s)
		}
	}
	if len(segs) == 0 && !dirOnly && rest != "" {
		return p, errors.New("the path pattern names nothing")
	}

	// Depth, for patterns written relative to the working directory.
	bare := p.relative && !strings.HasPrefix(spec, "./") && !strings.HasPrefix(spec, "!./") && !whole
	switch {
	case bare && len(segs) == 1 && !dirOnly:
		// A bare file name matches at any depth.
		segs = []string{"**", segs[0]}
	case bare && len(segs) == 1 && dirOnly, bare && len(segs) == 2 && segs[0] != "**":
		// One directory segment: an allow rule means that directory in
		// the working directory; a deny or ask rule means a directory of
		// that name at any depth.
		if list != ListAllow {
			segs = append([]string{"**"}, segs...)
		}
	}
	if dirOnly {
		segs = append(segs, "**")
	}

	// The anchor directory is a real path: nothing in it is a pattern.
	var head []string
	for _, s := range splitPath(base) {
		head = append(head, EscapePath(s))
	}
	p.segs = append(head, segs...)
	p.orig = slices.Clone(p.segs)
	if p.fold {
		for i, s := range p.segs {
			p.segs[i] = guardPattern(s)
		}
		// A rule written by way of macOS's second mount of the data disk
		// is a rule for the path without it (see sameDisk).
		for {
			switch {
			case len(p.segs) >= 3 && p.segs[0] == "system" && p.segs[1] == "volumes" && p.segs[2] == "data":
				p.segs, p.orig = p.segs[3:], p.orig[3:]
			case len(p.segs) >= 1 && p.segs[0] == ".nofollow":
				p.segs, p.orig = p.segs[1:], p.orig[1:]
			case len(p.segs) >= 2 && p.segs[0] == ".resolve" && isDigits(p.segs[1]):
				p.segs, p.orig = p.segs[2:], p.orig[2:]
			default:
				return p, nil
			}
		}
	}
	return p, nil
}

// splitPath cuts a cleaned absolute path into its segments.
func splitPath(abs string) []string {
	var out []string
	for _, s := range strings.Split(abs, "/") {
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

// match reports whether the pattern covers the cleaned absolute path.
func (p pathPattern) match(abs string) bool {
	ok, _ := p.matchHow(abs)
	return ok
}

// matchHow reports whether the pattern covers the path, and whether it
// does so only by covering a directory above it.
func (p pathPattern) matchHow(abs string) (ok, viaDir bool) {
	if p.fold {
		abs = guardPath(abs)
	}
	return p.matchKeyed(abs)
}

// matchKeyed is matchHow for a path that, for a pattern that compares
// names by their keys, has already been turned into its key (guardPath).
func (p pathPattern) matchKeyed(abs string) (ok, viaDir bool) {
	segs := splitPath(abs)
	if matchSegs(p.segs, segs) {
		return true, false
	}
	if p.below {
		for k := len(segs) - 1; k >= 1; k-- {
			if matchSegs(p.segs, segs[:k]) {
				return true, true
			}
		}
	}
	return false, false
}

// matchSegs matches pattern segments against path segments. It never
// backtracks further than the last "**", so its cost is bounded by the
// product of the two lengths whatever the pattern.
func matchSegs(pat, segs []string) bool {
	px, sx := 0, 0
	star, mark := -1, 0
	for sx < len(segs) {
		switch {
		case px < len(pat) && pat[px] == "**":
			star, mark = px, sx
			px++
		case px < len(pat) && segMatch(pat[px], segs[sx]):
			px++
			sx++
		case star >= 0:
			mark++
			px, sx = star+1, mark
		default:
			return false
		}
	}
	for px < len(pat) && pat[px] == "**" {
		px++
	}
	return px == len(pat)
}

// segMatch matches one path segment against one pattern segment, in
// which * is any run of characters, ? any one, [...] a class and a
// backslash takes the meaning from the next character. Like matchSegs it
// only ever backtracks to the last star.
func segMatch(pat, s string) bool {
	px, sx := 0, 0
	star, mark := -1, 0
	for sx < len(s) {
		if px < len(pat) {
			switch c := pat[px]; {
			case c == '*':
				star, mark = px, sx
				px++
				continue
			case c == '?':
				px++
				sx++
				continue
			case c == '[':
				end, ok, hit := classMatch(pat[px:], s[sx:])
				if ok && hit {
					px += end
					sx++
					continue
				}
				// No closing bracket: the [ is an ordinary character.
				if !ok && s[sx] == '[' {
					px++
					sx++
					continue
				}
			case c == '\\' && px+1 < len(pat):
				if pat[px+1] == s[sx] {
					px += 2
					sx++
					continue
				}
			case c == s[sx]:
				px++
				sx++
				continue
			}
		}
		if star < 0 {
			return false
		}
		mark++
		px, sx = star+1, mark
	}
	for px < len(pat) && pat[px] == '*' {
		px++
	}
	return px == len(pat)
}

// classMatch reads the character class that starts pat. It returns where
// the class ends, whether it is well formed, and whether the first byte
// of s is in it.
func classMatch(pat, s string) (end int, ok, hit bool) {
	i := 1
	neg := false
	if i < len(pat) && (pat[i] == '!' || pat[i] == '^') {
		neg = true
		i++
	}
	first := true
	for i < len(pat) {
		c := pat[i]
		if c == ']' && !first {
			return i + 1, true, hit != neg
		}
		first = false
		if c == '\\' && i+1 < len(pat) {
			i++
			c = pat[i]
		}
		lo, hi := c, c
		if i+2 < len(pat) && pat[i+1] == '-' && pat[i+2] != ']' {
			hi = pat[i+2]
			i += 2
		}
		if s != "" && s[0] >= lo && s[0] <= hi {
			hit = true
		}
		i++
	}
	return 0, false, false
}

// cleanAbs returns p as a cleaned absolute path, read from dir when it is
// relative. It reports false when p cannot be made absolute.
func cleanAbs(p, dir string) (string, bool) {
	if p == "" {
		return "", false
	}
	if !strings.HasPrefix(p, "/") {
		if dir == "" {
			return "", false
		}
		p = dir + "/" + p
	}
	return path.Clean(p), true
}

// inside reports whether the cleaned absolute path p is dir or lies under
// it.
func inside(dir, p string) bool {
	if dir == "" {
		return false
	}
	if dir == "/" {
		return strings.HasPrefix(p, "/")
	}
	return p == dir || strings.HasPrefix(p, dir+"/")
}

// hasPathMagic reports whether s holds a character a shell would treat as
// a file name pattern.
func hasPathMagic(s string) bool { return strings.ContainsAny(s, "*?[") }
