package permission

import "strings"

// A command that changes a folder with all that is in it (rm -r, chmod
// -R, chown -R, mv, a copy into it with cp -r or rsync -r) changes files
// the line never names. A Read or Edit rule that names a file inside such
// a folder ("rm -rf x" under Edit(./x/secret.txt)) is held to the command
// when the file is there: the rule's own path is taken for a file the
// command changes. A copy changes it when the copy brings a file of that
// name along. A rule that names files inside the folder by a pattern
// (Edit(./x/keys/*.pem)) makes the command one that cannot be checked
// against the rules, when the pattern's own folder is there. A rule that
// matches at any depth (a bare name: Edit(.env)) is looked for in the
// folder and as deep as shallowDepth folders below it (x/.env,
// x/app/.env, x/app/cfg/.env): a search of all that is in it would mean
// listing every file there.

// treeCopy is where the contents of a folder a command copies or moves
// may land: from is the folder, and each of into one place they may land
// in (the folder's name in the place, or the place itself).
type treeCopy struct {
	from word
	into []word
}

// treeOps reads the folders a command changes with all that is in them
// (trees), and where it copies or moves a folder's contents (copies).
func treeOps(words []word) (trees []word, copies []treeCopy) {
	if len(words) == 0 || !words[0].static() {
		return nil, nil
	}
	name := progKey(baseName(words[0].joined()))
	args := words[1:]
	var flags []string
	var ops []word
	rest := false
	for _, a := range args {
		l := a.lit()
		switch {
		case rest || !a.static() || !strings.HasPrefix(l, "-") || l == "-" || a.tilde:
			ops = append(ops, a)
		case l == "--":
			rest = true
		default:
			flags = append(flags, l)
		}
	}
	has := func(letters string, long ...string) bool {
		for _, f := range flags {
			if !strings.HasPrefix(f, "--") {
				if strings.ContainsAny(f[1:], letters) {
					return true
				}
				continue
			}
			for _, l := range long {
				if longFlag(f, l, len(l)-2) {
					return true
				}
			}
		}
		return false
	}
	lands := func(sources []word, places []word) {
		for _, src := range sources {
			tc := treeCopy{from: src}
			for _, place := range places {
				tc.into = append(tc.into, place)
				if into, ok := placedIn(place, src, name == "rsync"); ok && place.static() {
					tc.into = append(tc.into, into)
				}
			}
			copies = append(copies, tc)
		}
	}
	switch name {
	case "rm":
		if has("rR", "--recursive") {
			trees = ops
		}
	case "chmod", "chown", "chgrp":
		switch {
		case !has("R", "--recursive"):
		case has("", "--reference"):
			trees = ops
		case len(ops) > 1:
			trees = ops[1:] // (the first is the mode or the owner)
		}
	case "mv":
		sources, places, _ := copiedInto(name, args)
		trees = sources
		lands(sources, places)
	case "cp":
		if has("rRa", "--recursive", "--archive") {
			sources, places, _ := copiedInto(name, args)
			lands(sources, places)
		}
	case "rsync":
		if has("ra", "--recursive", "--archive") {
			sources, places, _ := copiedInto(name, args)
			lands(sources, places)
		}
	}
	return trees, copies
}

// shallowDepth is how many folders below a folder a command changes with
// all that is in it a name that matches at any depth is looked for.
const shallowDepth = 2

// anyDepth splits a pattern that matches its tail at any depth below its
// start (Edit(.env) is <working directory>/**/.env): the start, and the
// tail, which is written out. ok is false for any other pattern.
func (p pathPattern) anyDepth() (start, tail string, ok bool) {
	n := 0
	for n < len(p.segs) && p.segs[n] != "**" && !hasPathMagic(p.segs[n]) {
		n++
	}
	if n+1 >= len(p.segs) || p.segs[n] != "**" {
		return "", "", false
	}
	for _, s := range p.segs[n+1:] {
		if s == "**" || hasPathMagic(s) {
			return "", "", false
		}
	}
	return "/" + strings.Join(p.segs[:n], "/"), strings.Join(p.segs[n+1:], "/"), true
}

// literal is the pattern's path as far as it is written out: its
// segments before the first that is a pattern. whole says that is all
// of it.
func (p pathPattern) literal() (string, bool) {
	n := 0
	for n < len(p.segs) && p.segs[n] != "**" && !hasPathMagic(p.segs[n]) {
		n++
	}
	return "/" + strings.Join(p.segs[:n], "/"), n == len(p.segs)
}

// treeGuards holds the Read and Edit rules that name files inside the
// folders a command changes with all that is in them to it (see the top
// of this file): a file the command changes is added to writes, and a
// rule's pattern that cannot be told is said in req.pathOpaque.
func (e *Engine) treeGuards(trees []word, copies []treeCopy, dirs []string, req *request, writes *[]string) {
	var guards []*crule
	for _, list := range []List{ListDeny, ListAsk} {
		for _, c := range e.rules(list) {
			if c.kind == kindPath && !c.negate {
				guards = append(guards, c)
			}
		}
	}
	if len(guards) == 0 {
		return
	}
	// (With no way to look, a file may be there.)
	there := func(p string) bool {
		ms, err := e.expandGlob(p)
		return err != nil || len(ms) > 0
	}
	// hit holds the rules to the folder root, where the command changes
	// all that is in it; from is the folder a copy brings it from.
	hit := func(root, from string) {
		for _, c := range guards {
			if start, tail, ok := c.path.anyDepth(); ok && inside(start, root) {
				// Looked for in what the command changes: the folder, or
				// for a copy the folder it brings, as it lands there.
				in := root
				if from != "" {
					in = from
				}
				for d := 0; d <= shallowDepth; d++ {
					ms, _ := e.expandGlob(in + strings.Repeat("/*", d) + "/" + tail)
					for _, m := range ms {
						*writes = append(*writes, root+m[len(in):])
					}
				}
				continue
			}
			lit, whole := c.path.literal()
			if lit == root || !inside(root, lit) || hasPathMagic(lit) {
				continue // (one that names the folder itself is held to it already)
			}
			brought := lit
			if from != "" {
				brought = from + lit[len(root):]
			}
			if !there(brought) {
				continue
			}
			if whole {
				*writes = append(*writes, lit)
			} else if req.pathOpaque == "" {
				req.pathOpaque = "it changes all that is in " + root + ", and the rule " + c.Rule.String() + " names files there by a pattern"
			}
		}
	}
	roots := func(w word) []string {
		abs, pattern, ok := e.absAll(w, dirs)
		if !ok || pattern {
			return nil // (a name only known when the line runs, or a pattern, is a doubt already)
		}
		return abs
	}
	for _, t := range trees {
		for _, root := range roots(t) {
			hit(cleanPath(root), "")
		}
	}
	for _, tc := range copies {
		for _, from := range roots(tc.from) {
			for _, into := range tc.into {
				for _, root := range roots(into) {
					hit(cleanPath(root), cleanPath(from))
				}
			}
		}
	}
}
