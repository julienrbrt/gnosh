package permission

import (
	"path"
	"strings"
)

// The dangerous-removal check: rm and rmdir commands whose target is the
// filesystem root, a top-level directory, the home directory, the working
// directory or something above it, the repository's .git, one of gnosh's
// own folders, or a target that could turn into one of those when a
// variable is empty; and the other commands that destroy such a
// directory as surely as rm does: mv that takes it away, chmod -R and
// chown -R over it, git clean -fdx in it, dd, truncate and shred onto
// it, find -delete in it, rsync --delete into it. No allow rule and no
// hook approves one. It is a seat belt, not a wall: it catches the usual
// accidents.

// topLevelNames are directory names commonly found directly under the
// filesystem root.
var topLevelNames = map[string]bool{
	"bin": true, "boot": true, "dev": true, "etc": true, "home": true, "lib": true,
	"lib32": true, "lib64": true, "media": true, "mnt": true, "opt": true, "proc": true,
	"root": true, "run": true, "sbin": true, "srv": true, "sys": true, "tmp": true,
	"usr": true, "var": true, "Applications": true, "Library": true, "System": true,
	"Users": true, "Volumes": true, "private": true, "cores": true, "snap": true,
}

// criticalDir says why removing the cleaned absolute path p would be
// dangerous, or "".
func (e *Engine) criticalDir(p string) string {
	p = oneDisk(p)
	switch {
	case byNumber(p):
		return "it is named by a number (" + p + "), and what that is cannot be known ahead of time"
	case p == "/":
		return "it is the filesystem root"
	case path.Dir(p) == "/":
		return p + " is a top-level directory"
	case e.home != "" && p == e.home:
		return "it is the home directory"
	case p == e.cwd || p == e.cur:
		return "it is the working directory"
	case inside(p, e.cwd) || inside(p, e.cur):
		return p + " holds the working directory"
	}
	// The repository's own state: .git in a working directory, or in a
	// directory above one (the project may be part of a larger checkout).
	// A .git further down is some other repository's, vendored or cloned
	// into the project, and is a directory like any.
	if strings.EqualFold(path.Base(p), ".git") {
		for _, d := range append([]string{e.cur}, e.workDirs()...) {
			if inside(path.Dir(p), d) {
				return p + " holds the repository's history"
			}
		}
	}
	// gnosh's own folders (its config, its state and data, what a feature
	// keeps), and whatever holds one of them.
	low := guardPath(p)
	for _, x := range e.protected {
		if inside(low, x) {
			if low == x {
				return p + " is one of gnosh's own folders or files"
			}
			return p + " holds one of gnosh's own folders"
		}
	}
	return ""
}

// criticalGlobDir says why removing everything directly under p would be
// dangerous, or "".
func (e *Engine) criticalGlobDir(p string) string {
	p = oneDisk(p)
	if why := e.criticalDir(p); why != "" {
		return "the pattern covers everything in " + p + ": " + why
	}
	for _, d := range e.workDirs() {
		if inside(p, d) {
			return "the pattern covers everything in " + p + ", which is or holds a working directory"
		}
	}
	return ""
}

// removalDanger says why a command is a dangerous removal, or "". dirs
// are the directories the command may be running in; fromSubst names the
// variables that the same line assigned from a command's output.
func (e *Engine) removalDanger(v *bashView, c *simpleCmd, dirs []string, fromSubst map[string]bool) string {
	// "rm${s}-rf${s}/" is rm -rf / where s holds a blank: a program's
	// name with something unknown inside it is also read with a blank
	// in each such place.
	if alt := spaceReading(c); alt != nil {
		if why := e.removalDanger(v, alt, dirs, fromSubst); why != "" {
			return why
		}
	}
	if len(c.words) < 2 {
		return ""
	}
	// A flag with a variable written onto it is also read as it is when
	// that variable is not set: "rm -r$x ." is rm -r ., and "chmod -R$v
	// 000 ." chmod -R.
	if alt, changed := e.unsetFlags(c.words); changed {
		ac := *c
		ac.words = alt
		if why := e.removalDanger(v, &ac, dirs, fromSubst); why != "" {
			return why
		}
	}
	// A program's name with a variable in it is also read as it is when
	// that variable is not set: "rmdir${X} . -n" is rmdir . -n, and
	// "rm${X[1]} -rf ." rm -rf . (oracle seed 126).
	if w0 := c.words[0]; !w0.static() {
		if lit := w0.joined(); lit != "" && strings.TrimSpace(lit) == lit {
			ac := *c
			ac.words = append([]word{litOf(lit)}, c.words[1:]...)
			if why := e.removalDanger(v, &ac, dirs, fromSubst); why != "" {
				return why
			}
		}
	}
	full, ok := e.progName(c.words[0])
	name := progKey(baseName(full))
	if !ok || progIsPattern(c.words[0]) {
		// A program that is not known ahead of time, given what rm is
		// given ("$RM -rf /", "$(which rm) -rf ~"), is taken for rm.
		name = "rm"
		looksLikeRm := false
		for _, w := range c.words[1:] {
			// The letters are the ones some rm takes (GNU's, and -P, -W and
			// -x of the BSDs).
			if l := w.lit(); w.static() && (recursiveLong(l) || len(l) > 1 && l[0] == '-' && l[1] != '-' && strings.Trim(l[1:], "rRfvdiIPWx") == "" && strings.ContainsAny(l, "rR")) {
				looksLikeRm = true
			}
		}
		if !looksLikeRm {
			return ""
		}
	}
	switch name {
	case "find":
		return e.findDanger(v, c, dirs, fromSubst)
	case "mv":
		return e.mvDanger(v, c, dirs, fromSubst)
	case "chmod", "chown", "chgrp":
		return e.modeDanger(v, name, c, dirs, fromSubst)
	case "git":
		return e.gitCleanDanger(v, c, dirs, fromSubst)
	case "dd", "truncate", "shred":
		return e.overwriteDanger(v, name, c, dirs, fromSubst)
	case "rsync":
		return e.rsyncDanger(v, c, dirs, fromSubst)
	case "rm", "rmdir":
	default:
		return ""
	}
	recursive := false
	var targets []word
	rest := false
	for _, w := range c.words[1:] {
		l := w.lit()
		switch {
		case rest || !w.static() || !strings.HasPrefix(l, "-") || w.tilde:
			targets = append(targets, w)
		case l == "--":
			rest = true
		case recursiveLong(l) || !strings.HasPrefix(l, "--") && strings.ContainsAny(l, "rR"):
			recursive = true
		}
	}
	// rmdir takes a directory whole, like rm -r: one whose name is only
	// known when the line runs may be one to keep.
	recursive = recursive || name == "rmdir"
	return e.targetsDanger(v, v.place(c), name, targets, dirs, fromSubst, recursive)
}

// targetsDanger judges the targets of a command that removes what it is
// handed, or changes it past mending: each as it is written, as the
// words its braces make, and as what the line's own variables make of
// it. name is the command, for the reason, and at where it stands among
// the line's own commands (or noPlace).
func (e *Engine) targetsDanger(v *bashView, at int, name string, targets []word, dirs []string, fromSubst map[string]bool, recursive bool) string {
	// Braces around a variable are several targets too: "rm -rf {$d,x}".
	for i := 0; i < len(targets); i++ {
		if alts, ok := braceAlts(targets[i]); ok && len(targets)+len(alts) < 256 {
			targets = append(targets, alts...)
		}
	}
	for _, w := range targets {
		// A word whose variables the line itself set, each of them for
		// sure by the time the command runs, is what they make it and
		// nothing else: "d=build; rm -rf $d/" removes build/, and no
		// variable in it can turn out empty. Any other word is judged as
		// it is written, with what is not known in it left out.
		// (A word that also holds something nobody knows is judged with
		// the line's own variables written out, below: "d=x; rm -rf
		// "$DIR"/$d" is $DIR/x, and no lone slash.)
		vals, _, known := e.filledAll(v, w)
		var parts []word
		capped := false
		if !known {
			parts, capped = e.partFills(v, w, at)
		}
		if w.static() || !e.closedWord(v, w, at) && len(parts) == 0 && !capped {
			if why := e.targetDanger(w, dirs, fromSubst, recursive); why != "" {
				return name + " " + w.raw + ": " + why
			}
		}
		// Braces are several targets: "rm -rf {.,x}".
		if w.brace && w.static() && !w.quoted {
			if alts, ok := braceExpand(w.joined()); ok {
				for _, alt := range alts {
					part := litOf(alt)
					part.glob, part.tilde = w.glob && hasPathMagic(alt), strings.HasPrefix(alt, "~")
					if why := e.targetDanger(part, dirs, fromSubst, recursive); alt != "" && why != "" {
						return name + " " + w.raw + ": " + why
					}
				}
			}
		}
		// A variable the line itself set is also tried as what it was set
		// to: "d=.; rm -rf $d".
		// (Not quoted, its value is cut into words, and a pattern in it
		// is one: "d='*'; rm -rf $d".)
		// (A for loop's list gives it several: each is tried. More of
		// them than gnosh follows are one more doubt.)
		if recursive && e.tooManyFills(v, w) {
			return name + " " + w.raw + ": the line gives the target more values than gnosh follows"
		}
		// A word with one of the line's variables beside something nobody
		// knows: "d=~; rm -rf $PREFIX$d" is the home directory where
		// PREFIX is empty.
		if capped && recursive {
			return name + " " + w.raw + ": the line gives the target more values than gnosh follows"
		}
		for _, pw := range parts {
			if why := e.targetDanger(pw, dirs, fromSubst, recursive); why != "" {
				return name + " " + w.raw + ": " + why
			}
		}
		for _, val := range vals {
			if val.text == "" {
				continue
			}
			fields := []string{val.text}
			if !w.quoted {
				fields = e.fields(v, val.text)
			}
			for _, f := range fields {
				t := litOf(f)
				t.glob = hasPathMagic(f) && (w.glob || !w.quoted || val.glob)
				t.tilde = false
				if val.loose {
					// What is left of a value when a variable it was made
					// of is empty ("w=$X/; rm -rf $w"): judged as the
					// word it came from would be where it is written out
					// ("rm -rf $X/"), by a variable nobody knows put
					// after it. One that stood in the value's last name
					// ("t=/tmp$X-a") is put there by name, so that the
					// name is not taken for one written out.
					hole := wordPart{kind: partVar}
					if val.hole {
						hole.name = holeVar
					}
					t.parts = append(t.parts, hole)
				}
				if why := e.targetDanger(t, dirs, fromSubst, recursive); why != "" {
					return name + " " + w.raw + ": " + why
				}
			}
		}
	}
	if why := e.takesAll(targets, dirs); why != "" {
		return name + ": " + why
	}
	return ""
}

// takesAll reports whether the targets, taken together, are everything
// in a directory that has to be kept, however that is written: "[!.]*"
// and "*?" match what "*" does, "[a-m]* [n-z]*" between them, and so
// does a list of every name. It needs a way to see what is there
// (Options.Glob); without one a pattern is judged by how it is written.
// A directory with a single entry is not counted: removing the one thing
// in it is removing that thing.
func (e *Engine) takesAll(targets []word, dirs []string) string {
	if e.glob == nil {
		return ""
	}
	for _, d := range dirs {
		// What the targets name, by the directory each name lies in.
		named := map[string]map[string]bool{}
		note := func(p string) {
			dir := parentDir(p)
			if named[dir] == nil {
				named[dir] = map[string]bool{}
			}
			named[dir][p] = true
		}
		for _, w := range targets {
			ws := []word{w}
			if w.brace && w.static() && !w.quoted {
				if alts, ok := braceExpand(w.joined()); ok {
					ws = ws[:0]
					for _, alt := range alts {
						part := litOf(alt)
						part.glob, part.tilde = w.glob && hasPathMagic(alt), strings.HasPrefix(alt, "~")
						ws = append(ws, part)
					}
				}
			}
			for _, t := range ws {
				abs, pattern, ok := e.absAll(t, []string{d})
				if !ok {
					continue
				}
				for _, a := range abs {
					if !pattern {
						note(a)
						continue
					}
					if ms, err := e.expandGlob(a); err == nil {
						for _, m := range ms {
							note(m)
						}
					}
				}
			}
		}
		for dir, names := range named {
			why := e.criticalGlobDir(dir)
			if why == "" || len(names) < 2 {
				continue
			}
			entries, err := e.expandGlob(strings.TrimSuffix(dir, "/") + "/*")
			if err != nil {
				continue
			}
			n, all := 0, true
			for _, en := range entries {
				if strings.HasPrefix(path.Base(en), ".") {
					continue // a * leaves these out
				}
				n++
				all = all && names[en]
			}
			if n >= 2 && all {
				return "between them the targets are everything in " + dir + ": " + strings.TrimPrefix(why, "the pattern covers everything in "+dir+": ")
			}
		}
	}
	return ""
}

// flagsAndOperands parts a command's arguments into the flags that are
// written out and its operands. takes says of a flag whether the word
// after it is its value, which is then neither. After "--" every word is
// an operand, and so is one that is only known when the line runs. What
// xargs adds stands for as many words as xargs read: where it is a
// flag's value, the words after the first are operands.
func flagsAndOperands(args []word, takes func(flag string) bool) (flags []string, ops []word) {
	rest := false
	for i := 0; i < len(args); i++ {
		w := args[i]
		l := w.lit()
		switch {
		case rest || !w.static() || !strings.HasPrefix(l, "-") || l == "-" || w.tilde:
			ops = append(ops, w)
		case l == "--":
			rest = true
		default:
			flags = append(flags, l)
			if takes != nil && takes(l) {
				i++
				if i < len(args) && xargsWord(args[i]) {
					ops = append(ops, args[i])
				}
			}
		}
	}
	return flags, ops
}

// xargsWord reports whether a word is the one that stands for what xargs
// adds to a command (xargsAdds): any number of words, none of them known.
func xargsWord(w word) bool {
	return w.raw == "{}" && len(w.parts) == 1 && w.parts[0].kind == partSubst
}

// manyWords reports whether a word may turn into several once the line
// runs, none of them known ahead of time: what xargs adds, and what a
// command prints where no quotes keep it together ("mv $(cat list)").
func manyWords(w word) bool {
	return xargsWord(w) || !w.quoted && w.onlySubst()
}

// severalWords reports whether one word may stand for several once the
// line runs: what manyWords knows of, and a pattern or a list in braces,
// which the shell writes out as every name it matches or lists. Where
// such a word is all a command is handed, it holds the command's every
// operand: "mv .*" moves ".", ".." and what else starts with a dot into
// the last of them, and "mv {.git,/tmp/g}" is mv .git /tmp/g.
func severalWords(w word) bool {
	return manyWords(w) || w.glob || w.brace
}

// mvDanger: mv takes a directory away as surely as rm does. What it is
// handed to move is judged like rm -r's targets ("mv ~ /tmp/x", "mv .git
// old", "mv * ../elsewhere" in the project); the place it moves things
// to is not, unless -T makes mv put its one source in that place's
// stead. Moving a thing into a directory that must be kept is what mv is
// for.
func (e *Engine) mvDanger(v *bashView, c *simpleCmd, dirs []string, fromSubst map[string]bool) string {
	short := func(f string, letter byte) bool {
		return !strings.HasPrefix(f, "--") && strings.IndexByte(f, letter) >= 0
	}
	flags, ops := flagsAndOperands(c.words[1:], func(f string) bool {
		// -t and -S take the next word, also at the end of a cluster, and
		// so do their long forms where no = hands them their value.
		if strings.HasPrefix(f, "--") {
			return !strings.Contains(f, "=") && (longFlag(f, "--target-directory", 3) || longFlag(f, "--suffix", 4))
		}
		return strings.HasSuffix(f, "t") || strings.HasSuffix(f, "S")
	})
	into, onto := false, false
	for _, f := range flags {
		into = into || longFlag(f, "--target-directory", 3) || short(f, 't')
		onto = onto || longFlag(f, "--no-target-directory", 6) || short(f, 'T')
	}
	sources := ops
	var dest []word
	if !into && len(ops) > 0 {
		// The last word is the place, and the ones before it are what is
		// moved. What xargs adds is as many words as it read, the ones
		// that are moved among them ("echo .git /tmp/g | xargs mv"), and
		// so is what a command prints when it is all mv is handed ("mv
		// $(cat list)"): such a word is judged with the sources. After
		// other words a command's output is taken for the place it is
		// there to be: "mv a.txt $(mktemp -d)" is not asked about.
		last := ops[len(ops)-1]
		switch {
		case xargsWord(last), len(ops) == 1 && severalWords(last), last.anyOrder, last.glob, last.brace:
			// (The words of an array are known as a set: where one of
			// them stands last, any of them may be one that is moved. So
			// with a pattern or a list in braces: all the names it stands
			// for but the last are moved, and "mv f /*" moves /bin and
			// the rest into /var.)
		case len(ops) < 2:
			return ""
		default:
			sources = ops[:len(ops)-1]
		}
		dest = []word{last}
	}
	// A thing moved onto itself is not moved: mv refuses "mv x x". (A
	// pattern or a list in braces is not one thing: where it is all mv is
	// handed, the names it stands for are moved into the last of them.)
	if len(dest) == 1 && dest[0].static() && !dest[0].anyOrder && !dest[0].glob && !dest[0].brace {
		kept := sources[:0:0]
		for _, w := range sources {
			if !w.static() || w.value(e.home) != dest[0].value(e.home) {
				kept = append(kept, w)
			}
		}
		sources = kept
	}
	if why := e.targetsDanger(v, v.place(c), "mv", sources, dirs, fromSubst, true); why != "" {
		return why + " (it would be moved away)"
	}
	if onto {
		if why := e.targetsDanger(v, v.place(c), "mv -T", dest, dirs, fromSubst, true); why != "" {
			return why + " (it would be replaced)"
		}
	}
	return ""
}

// modeDanger: chmod -R, chown -R and chgrp -R over a directory that must
// be kept change every file in it at once. Without -R they change one
// thing, which can be changed back.
func (e *Engine) modeDanger(v *bashView, name string, c *simpleCmd, dirs []string, fromSubst map[string]bool) string {
	flags, ops := flagsAndOperands(c.words[1:], func(f string) bool {
		return !strings.Contains(f, "=") && (longFlag(f, "--reference", 5) || longFlag(f, "--from", 3))
	})
	recursive, given := false, false
	for _, f := range flags {
		switch {
		case longFlag(f, "--recursive", 5):
			recursive = true
		case longFlag(f, "--reference", 5):
			given = true // the mode or the owner comes from a file
		case strings.HasPrefix(f, "--"):
		case name == "chmod" && strings.Trim(f[1:], "rwxXstugoa+-=,") == "":
			given = true // "-w", "-x": a mode that takes rights away
		default:
			recursive = recursive || strings.ContainsRune(f, 'R')
		}
	}
	if !recursive {
		return ""
	}
	// The first operand is the mode or the owner, unless a flag gave it.
	// What xargs adds is that and the targets after it ("echo 000 . |
	// xargs chmod -R"), and so is what a command prints when it is all
	// there is ("chmod -R $(cat args)"). With targets written after it, a
	// command's output in that place is taken for the mode or the owner
	// it is there to be: "chown -R $(whoami) build" is not asked about.
	if !given {
		if len(ops) == 0 {
			return ""
		}
		if !(xargsWord(ops[0]) || len(ops) == 1 && severalWords(ops[0]) || ops[0].anyOrder) {
			ops = ops[1:]
		}
	}
	return e.targetsDanger(v, v.place(c), name+" -R", ops, dirs, fromSubst, true)
}

// gitCleanDanger: "git clean -fdx" removes every file git does not
// track, the ignored ones too: local settings, keys, whatever was never
// committed. In a directory that must be kept, or handed one, that is a
// removal like rm's. A dry run (-n) removes nothing, and a clean that
// leaves ignored files alone (no -x, no -X) is not asked about here.
func (e *Engine) gitCleanDanger(v *bashView, c *simpleCmd, dirs []string, fromSubst map[string]bool) string {
	args := c.words[1:]
	// The words before the subcommand: -C says where git runs.
	var at []word
	sub := -1
	// subName is the subcommand as it is written, or as it is when no
	// variable in it is set: "git clean$x -fdx" is git clean.
	subName := ""
	// printed is the first word before the subcommand that holds what a
	// command prints. Like any printed text it is read as nothing ("git
	// $(true) clean -fdx" is git clean), and where no subcommand is
	// written after it, as the subcommand: "git c`x`lean -fdx" is git
	// clean when x prints nothing. (A flag that only a command prints is
	// not one this check claims to see: see TestWhatTheRemovalCheckLetsGo.)
	printed := -1
	for i := 0; i < len(args) && sub < 0; i++ {
		l := args[i].joined()
		if !args[i].static() {
			if !allLitOrVar(args[i]) {
				if printed < 0 {
					printed = i
				}
				continue
			}
			if l = e.unsetValue(args[i]); l == "" {
				continue // a word that is not there when its variable is not set
			}
		}
		switch {
		case l == "-C" && i+1 < len(args):
			i++
			at = append(at, args[i])
		case l == "-c" || l == "--git-dir" || l == "--work-tree" || l == "--namespace" || l == "--super-prefix" || l == "--config-env":
			i++
		case strings.HasPrefix(l, "-"):
		default:
			sub, subName = i, l
		}
	}
	if sub < 0 && printed >= 0 {
		// The printed word may be the subcommand itself: clean, judged
		// by the flags the line writes out after it.
		sub, subName = printed, "clean"
	}
	rest := args[min(sub+1, len(args)):]
	if sub >= 0 && subName != "clean" && args[sub].anyOrder {
		// The words of an array (or of "$@") are known as a set: where
		// git's subcommand is one of them, clean may be it, and every
		// other word one it is handed.
		for i, a := range args {
			if a.anyOrder && a.static() && a.joined() == "clean" {
				subName = "clean"
				rest = append(append([]word(nil), args[:i]...), args[i+1:]...)
			}
		}
	}
	if sub < 0 || subName != "clean" {
		return ""
	}
	flags, paths := flagsAndOperands(rest, func(f string) bool {
		return f == "--exclude" || !strings.HasPrefix(f, "--") && strings.HasSuffix(f, "e")
	})
	// (-f is not looked for: git wants it only where clean.requireForce
	// says so, and "git -c clean.requireForce=false clean -dx" says no.)
	dry, ignored := false, false
	for _, f := range flags {
		switch {
		case longFlag(f, "--dry-run", 3):
			dry = true
		case strings.HasPrefix(f, "--"):
		default:
			dry = dry || strings.ContainsRune(f, 'n')
			ignored = ignored || strings.ContainsAny(f, "xX")
		}
	}
	if dry || !ignored {
		return ""
	}
	where := dirs
	if len(at) > 0 {
		// The directory git is sent to is judged like a target of rm: a
		// variable the line itself set is what it was set to, and what is
		// written beside one that may be empty is what is left ("w=./$d;
		// git -C $w clean -fdx").
		if why := e.targetsDanger(v, v.place(c), "git -C", at, dirs, fromSubst, true); why != "" {
			return why + " (git clean there removes every file that git does not track, the ignored ones too)"
		}
		where = nil
		for _, w := range at {
			abs, _, ok := e.absAll(w, dirs)
			if !ok {
				return "" // a directory that is only known when the line runs
			}
			where = append(where, abs...)
		}
	}
	if len(paths) > 0 {
		if why := e.targetsDanger(v, v.place(c), "git clean", paths, where, fromSubst, true); why != "" {
			return why + " (every file there that git does not track would be removed, the ignored ones too)"
		}
		// (Words known as a set may not be paths of this clean at all:
		// then it is a clean of the directory git is in.)
		limited := false
		for _, w := range paths {
			limited = limited || !w.anyOrder
		}
		if limited {
			return ""
		}
	}
	for _, d := range where {
		if why := e.criticalDir(d); why != "" {
			return "git clean in " + d + " removes every file there that git does not track, the ignored ones too: " + why
		}
	}
	return ""
}

// overwriteDanger: dd, truncate and shred destroy what a file holds
// where it lies. Their targets are judged like rm's (a pattern that takes
// every file of a directory that must be kept, a name that an empty
// variable would turn into one), and a disk as a whole is one more.
func (e *Engine) overwriteDanger(v *bashView, name string, c *simpleCmd, dirs []string, fromSubst map[string]bool) string {
	var targets []word
	if name == "dd" {
		for _, a := range c.words[1:] {
			if w, ok := afterPrefix(a, "of="); ok {
				targets = append(targets, w)
			}
		}
	} else {
		// The flags that take a value are each program's own: truncate's
		// -r names a reference file, and shred has no -r, so the word
		// after "shred -rf" is its target (oracle seed 116).
		valued := map[string]bool{"-s": true, "--size": true}
		switch name {
		case "truncate":
			valued["-r"], valued["--reference"] = true, true
		case "shred":
			valued["-n"], valued["--iterations"], valued["--random-source"] = true, true, true
		}
		_, targets = flagsAndOperands(c.words[1:], func(f string) bool { return valued[f] })
	}
	if why := e.targetsDanger(v, v.place(c), name, targets, dirs, fromSubst, true); why != "" {
		return why + " (what it holds would be destroyed)"
	}
	for _, w := range targets {
		abs, _, ok := e.absAll(w, dirs)
		for _, p := range abs {
			if ok && diskDevice(p) {
				return name + " " + w.raw + ": it is a disk as a whole"
			}
		}
	}
	return ""
}

// allLitOrVar reports whether a word is made of written text and
// variables only: nothing a command prints, and no sum.
func allLitOrVar(w word) bool {
	for _, p := range w.parts {
		if p.kind != partLit && p.kind != partVar {
			return false
		}
	}
	return true
}

// afterPrefix returns what follows a prefix written at the head of a
// word ("of=" in dd's of=file), as a word of its own. bash expands a
// tilde after the = of such a word.
func afterPrefix(a word, prefix string) (word, bool) {
	if len(a.parts) == 0 || a.parts[0].kind != partLit || !strings.HasPrefix(a.parts[0].lit, prefix) {
		return word{}, false
	}
	rest := a.parts[0].lit[len(prefix):]
	out := a
	out.raw = strings.TrimPrefix(a.raw, prefix)
	out.parts = append([]wordPart{{lit: rest}}, a.parts[1:]...)
	out.tilde = a.eqTilde && strings.HasPrefix(rest, "~")
	out.eqHead, out.eqTilde = false, false
	return out, true
}

// diskDevice reports whether a path names a disk, or a part of one, as
// the kernel lists them under /dev.
func diskDevice(p string) bool {
	name, ok := strings.CutPrefix(oneDisk(p), "/dev/")
	if !ok {
		return false
	}
	for _, pre := range []string{"sd", "hd", "vd", "xvd", "nvme", "mmcblk", "disk", "rdisk", "dm-", "md", "loop", "mapper/", "sr"} {
		if strings.HasPrefix(name, pre) {
			return true
		}
	}
	return false
}

// rsyncDanger: rsync --delete removes from the place it copies to
// whatever the source does not hold, so that an empty or a wrong source
// empties that place; and --remove-source-files takes the sources away
// like mv. The place is judged where it is on this machine.
func (e *Engine) rsyncDanger(v *bashView, c *simpleCmd, dirs []string, fromSubst map[string]bool) string {
	valued := map[string]bool{"-e": true, "--rsh": true, "-f": true, "--filter": true, "--exclude": true, "--include": true,
		"--files-from": true, "--exclude-from": true, "--include-from": true, "-T": true, "--temp-dir": true, "--backup-dir": true,
		"--suffix": true, "--compare-dest": true, "--copy-dest": true, "--link-dest": true, "--log-file": true, "--partial-dir": true,
		"--chmod": true, "--chown": true, "--bwlimit": true, "--port": true, "--password-file": true, "--out-format": true,
		"--timeout": true, "-B": true, "--block-size": true, "-M": true, "--max-size": true, "--min-size": true, "--info": true, "--debug": true}
	flags, ops := flagsAndOperands(c.words[1:], func(f string) bool {
		return valued[f] || !strings.HasPrefix(f, "--") && len(f) > 2 && strings.ContainsAny(f[len(f)-1:], "efTBM")
	})
	deletes, away := false, false
	for _, f := range flags {
		deletes = deletes || f == "--del" || strings.HasPrefix(f, "--delete")
		away = away || f == "--remove-source-files" || f == "--remove-sent-files"
	}
	// The last word is the place. One that may be several words (what
	// xargs adds, a command's output without quotes) may be all of them:
	// "echo src/ . | xargs rsync -a --delete".
	if len(ops) == 0 || len(ops) < 2 && !severalWords(ops[0]) {
		return ""
	}
	// (A name with a colon before any slash is a place on another
	// machine. Only what is written of the word counts: a colon inside a
	// $(...) is the command's own.)
	here := func(w word) bool {
		l := w.joined()
		i := strings.IndexByte(l, ':')
		return i < 0 || strings.Contains(l[:i], "/")
	}
	dest := ops[len(ops)-1]
	places := []word{dest}
	if dest.anyOrder {
		places = ops // (the words of an array are known as a set: any may be the last)
	}
	for _, place := range places {
		if !deletes || !here(place) {
			continue
		}
		if why := e.targetsDanger(v, v.place(c), "rsync --delete", []word{place}, dirs, fromSubst, true); why != "" {
			return why + " (whatever is there and not in the source would be deleted)"
		}
	}
	if away {
		var sources []word
		from := ops[:len(ops)-1]
		if xargsWord(dest) || len(ops) == 1 {
			from = ops
		}
		for _, w := range from {
			if here(w) {
				sources = append(sources, w)
			}
		}
		if why := e.targetsDanger(v, v.place(c), "rsync --remove-source-files", sources, dirs, fromSubst, true); why != "" {
			return why + " (it would be moved away)"
		}
	}
	return ""
}

// spaceReading returns the command as it is when every part of its
// first word that is not written out holds a blank, where that makes
// more than one word of it: what the shell does with "rm${IFS}-rf" and
// with any variable that holds a blank. It returns nil for a first word
// that is all written out, or all unknown.
func spaceReading(c *simpleCmd) *simpleCmd {
	if len(c.words) == 0 || c.words[0].static() {
		return nil
	}
	var b strings.Builder
	lits, holes := 0, 0
	for _, p := range c.words[0].parts {
		if p.kind == partLit {
			b.WriteString(p.lit)
			lits += len(p.lit)
		} else {
			b.WriteByte(' ')
			holes++
		}
	}
	fields := strings.Fields(b.String())
	if lits == 0 || holes == 0 || len(fields) < 2 {
		return nil
	}
	var ws []word
	for i, f := range fields {
		part := litOf(f)
		part.glob = i > 0 && hasPathMagic(f)
		ws = append(ws, part)
	}
	return &simpleCmd{words: append(ws, c.words[1:]...), redirs: c.redirs, nest: c.nest}
}

// recursiveLong reports whether l is rm's --recursive, or a shorter way
// of writing it that GNU rm takes (--r, --rec, ...).
func recursiveLong(l string) bool {
	return len(l) >= 3 && strings.HasPrefix("--recursive", l)
}

// longFlag reports whether a flag, with or without an =value after it, is
// the long option full, or a shorter way of writing it that GNU's tools
// take: any beginning of it that is at least min characters long, which
// is where it stops being the beginning of another option as well ("--rec"
// is --recursive to chmod, "--t=dir" is --target-directory=dir to mv).
func longFlag(flag, full string, min int) bool {
	name, _, _ := strings.Cut(flag, "=")
	return len(name) >= min && strings.HasPrefix(name, "--") && strings.HasPrefix(full, name)
}

// findNarrows are the tests of find that pick some files and leave
// others: with one of them in front, what an action touches is not
// known ahead of time.
var findNarrows = map[string]bool{
	"-name": true, "-iname": true, "-path": true, "-ipath": true, "-wholename": true, "-iwholename": true,
	"-regex": true, "-iregex": true, "-type": true, "-xtype": true, "-size": true, "-newer": true,
	"-anewer": true, "-cnewer": true, "-mtime": true, "-mmin": true, "-atime": true, "-amin": true,
	"-ctime": true, "-cmin": true, "-empty": true, "-user": true, "-group": true, "-perm": true,
	"-links": true, "-inum": true, "-samefile": true, "-lname": true, "-ilname": true, "-uid": true,
	"-gid": true, "-nouser": true, "-nogroup": true, "-readable": true, "-writable": true,
	"-executable": true, "-fstype": true, "-used": true, "-newermt": true, "-false": true,
}

// findDanger judges a find that removes what it finds: "find . -delete"
// and "find . -exec rm -rf {} +" are "rm -rf ." by another name. It only
// speaks when nothing narrows what find visits, so that the usual
// "find . -name '*.o' -delete" is left alone.
func (e *Engine) findDanger(v *bashView, c *simpleCmd, dirs []string, fromSubst map[string]bool) string {
	args := c.words[1:]
	var starts []word
	i := 0
	for ; i < len(args); i++ {
		l := args[i].lit()
		if l == "-P" || l == "-L" || l == "-H" {
			continue
		}
		if strings.HasPrefix(l, "-") || l == "!" || l == "(" {
			break
		}
		starts = append(starts, args[i])
	}
	if len(starts) == 0 {
		starts = []word{litOf(".")}
	}
	// does says what find does with all it finds, when that destroys it.
	does := ""
	for ; i < len(args); i++ {
		l := args[i].lit()
		switch {
		case findNarrows[l] || strings.HasPrefix(l, "-newer"):
			return ""
		case l == "-delete":
			does = "removes what it finds"
		case l == "-exec" || l == "-execdir" || l == "-ok" || l == "-okdir":
			for k := i + 1; k < len(args) && k < i+4; k++ {
				switch n := baseName(args[k].lit()); n {
				case "rm", "rmdir":
					does = "removes what it finds"
				case "shred", "truncate", "mv", "chmod", "chown", "chgrp":
					// (find goes through everything below its starting
					// point by itself: a chmod of all it finds is chmod -R.)
					if does == "" {
						does = "hands all it finds to " + n
					}
				}
			}
		}
	}
	if does == "" {
		return ""
	}
	for _, w := range starts {
		// find removes what is in the starting point, and the starting
		// point too when it can.
		for _, t := range []word{w, {raw: w.raw + "/*", parts: append(append([]wordPart(nil), w.parts...), wordPart{lit: "/*"}), glob: true, quoted: w.quoted, tilde: w.tilde}} {
			if why := e.targetsDanger(v, v.place(c), "find", []word{t}, dirs, fromSubst, true); why != "" {
				return "find " + w.raw + " " + does + ": " + strings.TrimPrefix(why, "find "+t.raw+": ")
			}
		}
	}
	return ""
}

// leadsTo returns where the cleaned absolute path p leads when links
// are followed, if that is somewhere else and can be told.
func (e *Engine) leadsTo(p string) string {
	if e.resolve == nil {
		return ""
	}
	r, err := e.resolve(p)
	if err != nil || !strings.HasPrefix(r, "/") {
		return ""
	}
	if r = cleanPath(r); r == p {
		return ""
	}
	return r
}

// holeVar names the variable put after a loose value whose last name had
// a part not known in it: no shell variable can have the name.
const holeVar = "(not known)"

// targetDanger judges one target of a removal.
func (e *Engine) targetDanger(w word, dirs []string, fromSubst map[string]bool, recursive bool) string {
	// What the target is when the variables whose values are known are
	// filled in, and every other expansion comes out empty.
	// A ${name:-word} or a ${name:+word} is also judged as the word it
	// may stand for ("rm -rf ${X:+/}", "rm -rf ${X:-$HOME}"), and a
	// ${HOME%/*} as each piece that may be left of the directory.
	for i, p := range w.parts {
		if p.kind != partVar {
			continue
		}
		var readings []word
		switch {
		case p.body != nil:
			readings = append(readings, withBody(w, i, e.home))
		case !p.plainly() && (p.name == "HOME" && e.home != "" || p.name == "PWD"):
			base := e.home
			if p.name == "PWD" {
				base = e.cur
			}
			texts, _ := p.of(base, e.home)
			for _, t := range texts {
				r := w
				r.parts = append(append(append([]wordPart(nil), w.parts[:i]...), wordPart{lit: t}), w.parts[i+1:]...)
				readings = append(readings, r)
			}
		}
		for _, r := range readings {
			if why := e.targetDanger(r, dirs, fromSubst, recursive); why != "" {
				return why
			}
		}
	}
	var val strings.Builder
	unknown, guardedOnly, vars, substs := false, true, 0, 0
	// lastUnknown is where in the value the last part not known ahead of
	// time stands (-1: none), to tell "$DIR/build" from "/tmp$X-a".
	lastUnknown := -1
	// stated: every variable that is not known says what it stands for
	// when it is not set (${DIR:-/}): the word is then a path like any.
	stated := true
	for _, p := range w.parts {
		switch p.kind {
		case partLit:
			val.WriteString(p.lit)
		case partVar:
			vars++
			switch {
			case p.name == "HOME" && e.home != "" && p.plainly():
				val.WriteString(e.home)
			case p.name == "PWD" && p.plainly():
				val.WriteString(e.cur)
			default:
				unknown = true
				if p.name != "" {
					// (A variable with no name stands for what was not
					// known of a value made of other variables, put after
					// what was: where that stood is not known.)
					lastUnknown = val.Len()
				}
				if !p.guarded {
					guardedOnly = false
				}
				if fromSubst[p.name] && len(w.parts) <= 2 {
					return "the variable " + p.name + " was just set from a command's output, which may be a directory to keep"
				}
				// "${DIR:-/}" is / when DIR is not set.
				val.WriteString(p.defIn(e.home))
				stated = stated && p.hasDef && p.def != ""
			}
		default:
			substs++
			unknown = true
			lastUnknown = val.Len()
			guardedOnly = false
			stated = false
		}
	}
	s := val.String()
	switch {
	case w.eqHead:
		// zsh puts the path of a program in place of "=name"; bash and sh,
		// which run the line, keep the word as it is written, and "=x/.."
		// is the working directory when there is a directory "=x" (oracle
		// seed 135). The word is judged as that text.
		lit := w
		lit.eqHead = false
		return e.targetDanger(lit, dirs, fromSubst, recursive)
	case w.tilde && w.static():
		s = w.value(e.home)
	case w.tilde && allLit(w) && s != "~+" && !strings.HasPrefix(s, "~+/") && func() bool { _, ok := dirTilde(w); return ok }():
		// ~- is the directory the shell was in before, and ~2 one that
		// pushd put by: after a cd, one of those the line has been in.
		rest, _ := dirTilde(w)
		for _, d := range dirs {
			t := litOf(d + rest)
			t.glob = w.glob && hasPathMagic(rest)
			if why := e.targetDanger(t, dirs, fromSubst, recursive); why != "" {
				return why
			}
		}
		if rest == "" {
			return "it is a directory the line was in before"
		}
		return ""
	case w.tilde && (s == "~+" || strings.HasPrefix(s, "~+/")):
		// ~+ is the directory the shell is in.
		s = e.cur + s[2:]
	case w.tilde && e.home != "" && w.parts[0].kind == partLit && strings.HasPrefix(w.parts[0].lit, "~/"):
		// "~/$X" is the home directory and whatever X holds, which may
		// be nothing.
		s = e.home + s[1:]
	case w.tilde:
		// ~name is somebody's home directory, and so is "~name/$X" where
		// X holds nothing.
		if rest := strings.TrimRight(strings.TrimSuffix(strings.TrimRight(s, "/"), "/*"), "/"); !strings.Contains(rest, "/") && len(rest) > 1 {
			return "it is the home directory of " + rest[1:]
		}
		return ""
	}
	if unknown {
		if recursive && xargsWord(w) {
			return "the target is among words that are only known when the line runs"
		}
		if recursive && w.onlySubst() {
			return "the target is whatever a command prints"
		}
		if recursive && w.parts[0].kind == partSubst && w.parts[0].proc {
			// "<(cmd)x" is /dev/fd/63x: a path named by a number, which is
			// only known when the line runs. Taken for a word that may be
			// nothing, it was judged as "x" (oracle seed 164).
			return "the target is a path a process substitution names, which is only known when the line runs"
		}
		if stated && !w.tilde {
			left := litOf(s)
			left.glob = w.glob
			if why := e.targetDanger(left, dirs, fromSubst, recursive); why != "" {
				return "if the variable is not set this removes " + s + ": " + why
			}
		}
		if guardedOnly && substs == 0 {
			// Every unknown variable is written ${NAME:?}: the shell stops
			// rather than expanding it to nothing.
			return ""
		}
		trimmed := strings.TrimSuffix(s, "/")
		switch {
		case s == "":
			return ""
		case strings.HasSuffix(trimmed, "/*") || (strings.HasSuffix(s, "/") && strings.HasPrefix(s, "/")):
			return "if the variable is empty this removes " + s
		case strings.HasPrefix(s, "/") && !strings.Contains(strings.Trim(s, "/"), "/") && topLevelNames[strings.Trim(s, "/")]:
			return "if the variable is empty this removes " + s
		case strings.HasPrefix(s, "/") && strings.Contains(s, "/.."):
			// What is left may climb: "rm -rf ~/$X/.." is the directory
			// above the home directory.
			left := litOf(s)
			left.glob = w.glob
			if why := e.targetDanger(left, dirs, fromSubst, recursive); why != "" {
				return "if the variable is empty this removes " + s + ": " + why
			}
		case strings.HasPrefix(s, "/") && w.glob && hasPathMagic(s):
			// What is left may be a pattern that takes in a directory that
			// has to be kept: "d=~; rm -rf $X$d*" is the home directory,
			// and every name that begins like it, where X is empty. (One
			// name of its own right under the root is how a build
			// directory is written, "$OUT/build*", unless it may be one
			// that holds what has to be kept: "$X/b*" takes in /bin.)
			if seg := strings.Trim(path.Clean(s), "/"); !strings.Contains(seg, "/") && !e.topPatternKeeps(seg) {
				return ""
			}
			left := litOf(s)
			left.glob = true
			if why := e.targetDanger(left, dirs, fromSubst, recursive); why != "" {
				return "if the variable is empty this removes " + s + ": " + why
			}
		case strings.HasPrefix(s, "/"):
			// What is left may be a directory that has to be kept, by its
			// whole path: "rm -rf $PREFIX/home/u". (A lone name right under
			// the root that is not one of the usual ones is "$DIR/build",
			// which is how a build directory is written: the variable
			// stands before the name, which is written out. One inside the
			// name is not that: "mv /tmp$X-a /tmp" moves /tmp-a, a
			// directory right under the root, where X is empty; oracle
			// seed 105.)
			inName := lastUnknown >= strings.LastIndex(strings.TrimRight(s, "/"), "/")+1
			// (The root itself is no lone name under it: "$d/." is / where
			// d is empty; oracle seed 116.)
			if c := cleanPath(s); c == "" || c != "/" && path.Dir(c) == "/" && c != e.home && c != e.cwd && c != e.cur && !inName {
				return ""
			}
			if why := e.targetDanger(litOf(s), dirs, fromSubst, recursive); why != "" {
				return "if the variable is empty this removes " + s + ": " + why
			}
		case !strings.HasPrefix(s, "/") && !w.tilde:
			// What is left of the word when the variable is empty may be
			// the working directory itself, or everything in it:
			// "rm -rf $X." and "rm -rf $PREFIX*".
			left := litOf(s)
			left.glob = w.glob
			if why := e.targetDanger(left, dirs, fromSubst, recursive); why != "" {
				return "if the variable is empty this removes " + s + ": " + why
			}
		}
		return ""
	}
	if s == "" {
		return ""
	}
	globbed := w.glob && hasPathMagic(s)
	for _, d := range dirs {
		abs, ok := cleanAbs(s, d)
		if !ok {
			continue
		}
		if !globbed {
			if why := e.criticalDir(abs); why != "" {
				return why
			}
			// Where "link/.." really leads, when that is somewhere else.
			if phys, ok := e.physical(s, d); ok && phys != abs {
				if why := e.criticalDir(phys); why != "" {
					return why
				}
			}
			// And where the target itself leads, when it is a link: with
			// a slash after it, or to the rm of macOS, removing the link
			// is removing what it leads to.
			if to := e.leadsTo(abs); to != "" {
				if why := e.criticalDir(to); why != "" {
					return "it is a link to " + to + ": " + why
				}
			}
			continue
		}
		// A pattern: find the directory whose entries it sweeps up, or
		// the directory to keep that it may take in.
		segs := splitPath(abs)
		first, last := -1, len(segs)-1
		for i, seg := range segs {
			if hasPathMagic(seg) {
				first = i
				break
			}
		}
		if first < 0 {
			// A ".." took the pattern back out: every match of "*/.." is
			// the directory the pattern stands in (oracle seed 97).
			if why := e.criticalDir(abs); why != "" {
				return "every match of the pattern " + s + " is " + abs + ": " + why
			}
			continue
		}
		// A part that takes every entry there is: *.tmp names its own
		// files, * names them all.
		sweeps := func(seg string) bool { return hasPathMagic(seg) && strings.Trim(seg, "*.?") == "" }
		dir := "/" + strings.Join(segs[:first], "/")
		all := true
		for _, seg := range segs[first:] {
			all = all && sweeps(seg)
		}
		if all {
			// Everything in the directory, or ("*/*") everything in
			// everything in it.
			if why := e.criticalGlobDir(dir); why != "" {
				return why
			}
			if to := e.leadsTo(dir); to != "" {
				if why := e.criticalGlobDir(to); why != "" {
					return dir + " is a link to " + to + ": " + why
				}
			}
		}
		if last == 0 {
			// "/e*", "/[!x]*": whatever it matches lies right under the
			// root.
			return "the pattern names what lies directly under the filesystem root"
		}
		// The directories to keep, each held against the pattern: it may
		// take one of them in ("/home/u*", "../p*", "/*/u"), or
		// everything in one ("/h*/*").
		for _, keep := range e.keepDirs() {
			ks := splitPath(keep)
			if len(ks) == len(segs) && segsMatch(segs, ks) {
				if why := e.criticalDir(keep); why != "" {
					return "the pattern takes in " + keep + ": " + why
				}
			}
			if sweeps(segs[last]) && len(ks) == last && segsMatch(segs[:last], ks) {
				if why := e.criticalGlobDir(keep); why != "" {
					return why
				}
			}
		}
	}
	return ""
}

// keepDirs returns the directories a pattern is held against by name:
// the home directory, the working directories and everything above
// them, and the usual directories under the root.
func (e *Engine) keepDirs() []string {
	var out []string
	add := func(p string) {
		for p != "" && p != "/" {
			out = unionStrings(out, []string{p})
			p = path.Dir(p)
		}
	}
	add(e.home)
	add(e.cwd)
	add(e.cur)
	for _, d := range e.workDirs() {
		add(d)
	}
	for _, d := range append([]string{e.cur}, e.workDirs()...) {
		if d != "" {
			out = unionStrings(out, []string{path.Join(d, ".git")})
		}
	}
	for _, x := range e.protected {
		add(x)
	}
	for name := range topLevelNames {
		out = unionStrings(out, []string{"/" + name})
	}
	return out
}

// topPatternKeeps reports whether a pattern for one name right under the
// root takes in a directory that is, or holds, one that has to be kept
// (keepDirs): "b*" takes /bin, "pr*" a working directory /proj.
func (e *Engine) topPatternKeeps(seg string) bool {
	for _, d := range e.keepDirs() {
		first, _, _ := strings.Cut(strings.TrimPrefix(d, "/"), "/")
		if ok, _ := path.Match(seg, first); ok && first != "" {
			return true
		}
	}
	return false
}

// segsMatch reports whether a path's parts could be what the pattern's
// parts match, one for one. A part it cannot judge matches.
func segsMatch(pats, names []string) bool {
	for i, pat := range pats {
		if !hasPathMagic(pat) {
			if !strings.EqualFold(pat, names[i]) {
				return false
			}
			continue
		}
		// (The shells write [!a] for what Go's matcher writes [^a].)
		if ok, err := path.Match(strings.ReplaceAll(pat, "[!", "[^"), names[i]); err == nil && !ok {
			return false
		}
	}
	return true
}
