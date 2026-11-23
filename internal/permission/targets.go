package permission

import (
	"path"
	"strings"
)

// What a command reads and writes, for the Read and Edit rules and the
// protected paths: the files its words name (fillTargets for the well-known
// file commands, namedFiles and mentioned for any other), what lands where
// it copies (copiedInto), what it reads by a list or writes by an archive
// or a patch (listFlag, hiddenWrites), and the output its flags name
// (outputs).

// namedFiles returns the arguments of a command of the read-only table
// that may name files, when the command could not be judged in full:
// every argument that is not a flag, what stands after the = of a long
// flag (one the table does not know may take a file: "diff
// --from-file=.env", "wc --files0-from=.env"), and for a command that
// takes a pattern first, every one after it.
func namedFiles(name string, args []word) []word {
	name = progKey(name)
	spec, ok := roTable[name]
	switch {
	case name == "git" || name == "find" || name == "stat" || name == "test" || name == "[":
	case !ok || spec.ops == opsWords || spec.ops == opsNone:
		return nil
	}
	if spec.ops == opsPattern {
		return patternFiles(name, args)
	}
	var out []word
	for _, a := range args {
		if a.static() && strings.HasPrefix(a.joined(), "-") && !a.tilde {
			if name, val, ok := strings.Cut(a.joined(), "="); ok && val != "" && strings.HasPrefix(name, "--") {
				part := partOf(a, val)
				part.tilde = a.eqTilde && strings.HasPrefix(val, "~")
				out = append(out, part)
			}
			out = append(out, gluedValues(a)...)
			continue
		}
		out = append(out, a)
		if name == "git" && a.static() {
			if p, ok := revPath(a.joined()); ok {
				out = append(out, partOf(a, p))
			}
		}
	}
	return out
}

// patternValued are the letters that take a value, for the programs
// whose first operand is a pattern or a program text and not a file.
var patternValued = map[string]string{
	"grep": "efmABCdD", "egrep": "efmABCdD", "fgrep": "efmABCdD", "rg": "efgtTABCmjMdEr", "awk": "efvF",
}

// patternFiles returns the files such a program is handed: its operands
// but the first, which is the pattern (to awk, the program), unless a
// flag gave that (grep -e x, grep -ex, awk -f prog); and the file a flag
// names for it (grep -f file). The value of any other flag is passed
// over, wherever it is written: "grep -A 2 x f" reads f and nothing
// named 2.
func patternFiles(name string, args []word) []word {
	valued := patternValued[progKey(name)]
	var out []word
	sawPat, rest := false, false
	for i := 0; i < len(args); i++ {
		a := args[i]
		if rest || !a.static() || !strings.HasPrefix(a.joined(), "-") || a.tilde || a.joined() == "-" {
			out = append(out, a)
			continue
		}
		s := a.joined()
		switch {
		case s == "--":
			rest = true
		case strings.HasPrefix(s, "--"):
			// --regexp=x gives the pattern (--source=x, to awk), --file=f
			// a file of them.
			switch flag, val, has := strings.Cut(s, "="); {
			case flag == "--file" && has:
				sawPat = true
				out = append(out, partOf(a, val))
			case flag == "--file" && i+1 < len(args):
				sawPat = true
				i++
				out = append(out, args[i])
			case flag == "--regexp" || flag == "--source" || flag == "--file":
				sawPat = true
				if !has && i+1 < len(args) {
					i++ // the pattern is the next word
					if mayBecomeWords(args[i]) {
						out = append(out, args[i])
					}
				}
			}
		default:
			// Letters: one that takes a value ends them, with the value
			// written onto it or in the next word. -e gives the pattern
			// and -f a file of them.
		letters:
			for j := 1; j < len(s); j++ {
				c := s[j]
				if strings.IndexByte(valued, c) < 0 {
					continue
				}
				sawPat = sawPat || c == 'e' || c == 'f'
				switch {
				case j+1 < len(s) && c == 'f':
					out = append(out, partOf(a, s[j+1:]))
				case j+1 < len(s):
				case i+1 < len(args):
					i++
					if c == 'f' || mayBecomeWords(args[i]) {
						// (A pattern computed outside quotes may be the
						// pattern and then files.)
						out = append(out, args[i])
					}
				}
				break letters
			}
		}
	}
	// (A first operand that is a file name pattern or braces, or a word
	// computed outside quotes, is several words to the shell, and all but
	// the first of those are files.)
	if !sawPat && len(out) > 0 && !out[0].glob && !out[0].brace && !mayBecomeWords(out[0]) {
		out = out[1:]
	}
	return out
}

// mayBecomeWords reports whether the shell may cut the word into several:
// what a variable or a command substitution outside quotes stands for is
// cut at its blanks.
func mayBecomeWords(w word) bool {
	return !w.static() && !w.quoted
}

// wordsOnly are the builtins and programs whose arguments are words and
// not files, beside those the table of read-only commands says so of.
var wordsOnly = map[string]bool{
	"export": true, "declare": true, "typeset": true, "local": true, "readonly": true, "alias": true, "unalias": true,
	"set": true, "shopt": true, "read": true, "eval": true, "trap": true, "let": true, ":": true, "true": true, "false": true,
	"exit": true, "return": true, "shift": true, "unset": true, "wait": true, "kill": true, "jobs": true, "type": true,
	"hash": true, "getopts": true, "mapfile": true, "readarray": true, "compgen": true, "complete": true, "umask": true,
	"ulimit": true, "bg": true, "fg": true, "disown": true, "break": true, "continue": true, "history": true, "fc": true,
	"pushd": true, "popd": true, "dirs": true, "enable": true, "builtin": true, "command": true, "exec": true, "time": true,
	"sleep": true, "seq": true, "expr": true, "yes": true, "git": true, "find": true, "sed": true, "cd": true, "test": true,
	"[": true, "[[": true, "date": true, "mkdir": true, "touch": true, "rm": true, "rmdir": true, "mv": true, "cp": true,
	"printf": true, "echo": true, "ssh": true,
}

// mentioned returns the words of a command that may name a file, for a
// program this package knows nothing about (tar, openssl, docker, a
// shell handed a script): every argument that is no flag, what stands
// after the = of one that is (--env-file=.env), and what stands after an
// @. They are held against the Read and Edit rules like the operands of
// a program that is known: a rule on .env is a rule on "base64 .env"
// and on "tar cf x.tar .env" alike. (A word that only looks like a file
// is refused too: "mytool --exclude .env".)
func mentioned(name string, args []word) []word {
	name = progKey(name)
	if _, ok := roTable[name]; ok || wordsOnly[name] {
		return nil
	}
	var out []word
	for _, a := range args {
		if !a.static() {
			if !strings.HasPrefix(a.raw, "-") {
				out = append(out, a)
			}
			continue
		}
		l := a.joined()
		_, val, eq := strings.Cut(l, "=")
		switch {
		case eq && val != "":
			// (bash expands a tilde after the = of a word that looks
			// like an assignment: "tool cfg=~/x".)
			part := partOf(a, val)
			part.tilde = a.eqTilde && strings.HasPrefix(val, "~")
			out = append(out, part)
		case strings.HasPrefix(l, "@") && len(l) > 1:
			out = append(out, partOf(a, l[1:]))
		}
		if !strings.HasPrefix(l, "-") || a.tilde {
			out = append(out, a)
		}
		out = append(out, gluedValues(a)...)
	}
	return out
}

// gluedValues are what may stand glued to a cluster of short flags as the
// value of one of them, a file a flag takes among them ("curl -K.env",
// "xargs -a.env", "curl -vK.env"): each text after one of its letters. A
// word with an = in it is left to the = (whose value is taken whole).
func gluedValues(a word) []word {
	l := a.joined()
	if !a.static() || len(l) < 3 || l[0] != '-' || l[1] == '-' || strings.Contains(l, "=") {
		return nil
	}
	out := make([]word, 0, len(l)-2)
	for i := 2; i < len(l); i++ {
		out = append(out, partOf(a, l[i:]))
	}
	return out
}

// fillTargets records the files the well-known file commands read and
// write, whatever their flags, so that Read and Edit deny rules and the
// protected paths can be checked even when the command is not otherwise
// understood.
func fillTargets(f *cmdFacts, args []word) {
	var ops []word
	for _, a := range args {
		if a.static() && strings.HasPrefix(a.joined(), "-") && !a.tilde {
			continue
		}
		ops = append(ops, a)
	}
	// outFlag returns the files a command is told to write by one of the
	// flags given: "-o file", "-ofile", "--output file", "--output=file".
	outFlag := func(flags ...string) []word {
		var out []word
		for i, a := range args {
			if !a.static() {
				continue
			}
			l := a.joined()
			for _, fl := range flags {
				switch {
				case l == fl && i+1 < len(args):
					out = append(out, args[i+1])
				case strings.HasPrefix(fl, "--") && strings.HasPrefix(l, fl+"="):
					out = append(out, partOf(a, l[len(fl)+1:]))
				case !strings.HasPrefix(fl, "--") && strings.HasPrefix(l, fl) && len(l) > len(fl):
					out = append(out, partOf(a, l[len(fl):]))
				}
			}
		}
		return out
	}
	// (By the program's key: these are for the rules that guard.)
	switch progKey(f.name) {
	case "sort":
		f.reads, f.writes = ops, outFlag("-o", "--output")
	case "uniq":
		// The second file is the one uniq writes, and a pattern may be
		// two files.
		f.reads = ops
		if len(ops) > 1 {
			f.writes = ops[1:]
		}
		for _, o := range ops {
			if o.glob || o.brace {
				f.writes = ops // which of them is the second file, nobody knows
				break
			}
		}
	case "curl":
		f.writes = outFlag("-o", "--output")
		// What curl sends: the file after -T, and after an @ in the data
		// it is given ("-d @.env", "-F f=@.env").
		f.reads = outFlag("-T", "--upload-file")
		for _, a := range args {
			if !a.static() {
				continue
			}
			if _, file, ok := strings.Cut(a.joined(), "@"); ok && file != "" && !strings.Contains(a.joined(), "://") {
				f.reads = append(f.reads, partOf(a, strings.TrimSuffix(strings.TrimPrefix(file, "\""), "\"")))
			}
		}
	case "wget":
		f.writes = outFlag("-O", "--output-document")
	case "time":
		f.writes = outFlag("-o", "--output")
	case "git":
		f.writes = outFlag("--output")
		// git archive -o writes the archive, and git format-patch -o the
		// patches into a folder of that name.
		for _, a := range args {
			if a.static() && !strings.HasPrefix(a.joined(), "-") {
				if sub := a.joined(); sub == "archive" || sub == "format-patch" {
					f.writes = moreWords(f.writes, outFlag("-o", "--output-directory"))
				}
				break
			}
		}
	case "cat", "head", "tail", "less", "more", "nl", "bat", "batcat", "wc", "cut", "tac", "strings", "xxd", "hexdump", "od", "base64", "source", ".",
		"vim", "vi", "nvim", "nano", "emacs", "view", "open", "xdg-open", "code", "jq", "yq", "cmp", "comm", "paste", "join", "column", "rev", "fold", "fmt",
		"md5sum", "sha1sum", "sha256sum", "sha512sum", "shasum", "cksum", "md5", "b2sum", "zcat", "bzcat", "xzcat", "zstdcat", "pbcopy":
		f.reads = ops
	case "gzip", "gunzip", "bzip2", "bunzip2", "xz", "unxz", "zstd", "unzstd":
		// What it makes is the file's name with its suffix put on or taken
		// off: "gunzip .env.gz" writes .env.
		f.writes = append(ops, packedNames(ops)...)
	case "mkfifo", "mknod", "chgrp", "chflags":
		f.writes = ops
	case "perl", "ruby":
		// -i edits in place the files named after the script.
		for _, a := range args {
			if l := a.lit(); len(l) > 1 && l[0] == '-' && l[1] != '-' && strings.Contains(l, "i") && !strings.HasPrefix(l, "-e") {
				f.writes = ops
				break
			}
		}
	case "grep", "egrep", "fgrep", "rg", "awk":
		// The first operand is the pattern or the program, unless a flag
		// gave that, or it is a file name pattern or braces.
		f.reads = patternFiles(f.name, args)
	case "tee", "sponge", "touch", "mkdir", "rm", "rmdir", "truncate", "shred", "unlink", "chmod", "chown":
		f.writes = ops
	case "mv":
		// mv takes its sources away, and writes them where it puts them.
		sources, writes, lands := copiedInto("mv", args)
		f.writes, f.lands = append(sources, writes...), lands
	case "cp", "ln", "install", "rsync", "scp":
		f.reads, f.writes, f.lands = copiedInto(progKey(f.name), args)
	case "sed":
		// The script is the first operand unless -e or -f gave one.
		hasE := false
		inPlace := false
		for _, a := range args {
			l := a.lit()
			if l == "-e" || l == "-f" || strings.HasPrefix(l, "--expression") || strings.HasPrefix(l, "--file") {
				hasE = true
			}
			if strings.HasPrefix(l, "-") && !strings.HasPrefix(l, "--") && strings.Contains(l, "i") || strings.HasPrefix(l, "--in-place") {
				inPlace = true
			}
		}
		// (A script computed outside quotes is the script and then files,
		// as grep's pattern is: mayBecomeWords.)
		if !hasE && len(ops) > 0 && !ops[0].glob && !ops[0].brace && !mayBecomeWords(ops[0]) {
			ops = ops[1:]
		}
		if inPlace {
			f.writes = ops
		} else {
			f.reads = ops
		}
	case "dd":
		for _, a := range ops {
			l := a.lit()
			// bash expands a tilde after the = of a word like this one.
			operand := func(v string) word {
				w := partOf(a, v)
				w.tilde = a.eqTilde && strings.HasPrefix(v, "~")
				return w
			}
			if v, ok := strings.CutPrefix(l, "of="); ok {
				f.writes = append(f.writes, operand(v))
			}
			if v, ok := strings.CutPrefix(l, "if="); ok {
				f.reads = append(f.reads, operand(v))
			}
		}
	}
}

// listFlag names the flag by which a command is handed a list of the
// files it reads, in a file or on its standard input: tar -T list, rsync
// --files-from=list, sort --files0-from=list, cpio -o, zip -@. The files
// are named in the list, which is only read when the line runs. "" when
// it has none.
func listFlag(words []word) string {
	if len(words) == 0 || !words[0].static() {
		return ""
	}
	name := progKey(baseName(words[0].joined()))
	// long reports whether l is the long flag full, or a start of it that
	// getopt takes for it (at least min letters).
	long := func(l, full string, min int) bool {
		l, _, _ = strings.Cut(l, "=")
		return len(l) >= min && strings.HasPrefix(full, l)
	}
	for i, a := range words[1:] {
		if !a.static() {
			continue
		}
		l := a.joined()
		short := len(l) > 1 && l[0] == '-' && l[1] != '-'
		switch name {
		case "tar":
			// (tar's old way: its first word, with no dash, is letters.)
			if long(l, "--files-from", len("--files")) || short && strings.Contains(l[1:], "T") || i == 0 && !strings.HasPrefix(l, "-") && strings.Contains(l, "T") {
				return "tar " + l
			}
		case "rsync":
			if long(l, "--files-from", len("--files")) {
				return "rsync " + l
			}
		case "sort", "du", "wc":
			if long(l, "--files0-from", len("--fil")) {
				return name + " " + l
			}
		case "cpio":
			if long(l, "--create", len("--cr")) || long(l, "--pass-through", len("--pa")) || short && strings.ContainsAny(l[1:], "op") {
				return "cpio " + l
			}
		case "zip":
			if short && strings.Contains(l[1:], "@") {
				return "zip " + l
			}
		}
	}
	return ""
}

// copiedInto reads the operands of cp, mv, ln, install, rsync and scp:
// the sources, what is written (the place: the last operand, or the
// folder -t or --target-directory names to cp, mv, ln and install), and
// what lands there where the place is a folder: each source's name in it
// ("cp x/.env ." writes ./.env; a source's pattern stays a pattern there:
// "cp x/.e* ." writes what .e* matches in the folder). install -d makes
// every folder it is given.
func copiedInto(name string, args []word) (sources, writes []word, lands []copyLanding) {
	var ops, dirs []word
	makesDirs := false
	rest := false
	for i := 0; i < len(args); i++ {
		w := args[i]
		l := w.lit()
		value := func(attached string, has bool) {
			switch {
			case has:
				dirs = append(dirs, partOf(w, attached))
			case i+1 < len(args):
				i++
				dirs = append(dirs, args[i])
			}
		}
		switch {
		case rest || !w.static() || !strings.HasPrefix(l, "-") || l == "-" || w.tilde:
			ops = append(ops, w)
		case l == "--":
			rest = true
		case name == "rsync" || name == "scp":
			// (Their letters are other things: rsync's -t keeps times.)
		case strings.HasPrefix(l, "--"):
			flag, val, eq := strings.Cut(l, "=")
			switch {
			case longFlag(flag, "--target-directory", 3):
				value(val, eq)
			case longFlag(flag, "--suffix", 4) && !eq:
				i++ // the suffix
			case name == "install" && longFlag(flag, "--directory", 4):
				makesDirs = true
			}
		default:
			for j := 1; j < len(l); j++ {
				switch l[j] {
				case 't':
					value(l[j+1:], j+1 < len(l))
				case 'S':
					if j+1 == len(l) {
						i++ // the suffix
					}
				case 'd':
					makesDirs = makesDirs || name == "install"
					continue
				default:
					continue
				}
				break
			}
		}
	}
	switch {
	case makesDirs:
		return nil, ops, nil
	case len(dirs) > 0:
		sources = ops
	case len(ops) > 0:
		sources, dirs = ops[:len(ops)-1], ops[len(ops)-1:]
	}
	writes = append(writes, dirs...)
	for _, dir := range dirs {
		if !dir.static() {
			continue // (a place only known when the line runs is a doubt already)
		}
		for _, src := range sources {
			if into, ok := placedIn(dir, src, name == "scp" || name == "rsync"); ok {
				ld := copyLanding{into: into, dir: dir, src: src}
				if host, _, ok := strings.Cut(src.joined(), ":"); (name == "scp" || name == "rsync") && ok && !strings.Contains(host, "/") {
					ld.remote = true // (host:path: its names are the far side's)
				}
				lands = append(lands, ld)
			}
		}
	}
	return sources, writes, lands
}

// copyLanding is where a copy (cp, mv, ln, install, rsync, scp) puts a source
// in a folder: into is the folder and the source's last name as the line
// writes them ("cp x/.env ." writes ./.env), dir and src the folder and
// the source themselves, for working out the names a pattern lands under:
// those of what it matches, which are not in the folder yet (oracle seed
// 176). remote marks a source on another machine (scp's host:path), whose
// pattern matches the files there, not here.
type copyLanding struct {
	into, dir, src word
	remote         bool
}

// placedIn is the name the source src gets in the folder dir: dir, a
// slash, and what follows the last slash written in src, which may be
// only known when the line runs ("cp $f build/" writes build/$f, and "cp
// $D/.env ." ./.env). ok is false where src names what is in a folder (x/,
// x/.), which is not one name. A scp or rsync source's host (host:path)
// is no part of the name.
func placedIn(dir, src word, remote bool) (word, bool) {
	at := strings.TrimRight(dir.joined(), "/") + "/"
	if src.static() {
		text := src.joined()
		if host, p, ok := strings.Cut(text, ":"); remote && ok && !strings.Contains(host, "/") {
			text = p
		}
		base := path.Base(text)
		if text == "" || strings.HasSuffix(text, "/") || base == "." || base == ".." || base == "/" {
			return word{}, false
		}
		into := partOf(dir, at+base)
		into.tilde = dir.tilde
		into.glob = src.glob && hasPathMagic(base) || dir.glob && hasPathMagic(at)
		return into, true
	}
	k, tail := -1, ""
	for i := len(src.parts) - 1; i >= 0; i-- {
		if p := src.parts[i]; p.kind == partLit && strings.Contains(p.lit, "/") {
			k, tail = i, p.lit[strings.LastIndexByte(p.lit, '/')+1:]
			break
		}
	}
	into := src
	into.parts = append([]wordPart{{lit: at + tail}}, src.parts[k+1:]...)
	into.raw = at + src.raw[strings.LastIndexByte(src.raw, '/')+1:]
	into.tilde = dir.tilde
	return into, true
}

// packedSuffixes are the suffixes gzip and its kin put on what they pack.
var packedSuffixes = []string{".gz", ".bz2", ".xz", ".zst", ".tgz", ".tbz2", ".txz", ".z", ".Z"}

// packedNames are the names a packer or an unpacker makes of the files it
// is given: each with a packer's suffix taken off, or ".gz" and its kin
// put on.
func packedNames(ops []word) []word {
	var out []word
	for _, w := range ops {
		if !w.static() {
			continue
		}
		l := w.joined()
		for _, suf := range packedSuffixes {
			if trimmed, ok := strings.CutSuffix(l, suf); ok && trimmed != "" {
				out = append(out, partOf(w, trimmed))
			}
			out = append(out, partOf(w, l+suf))
		}
	}
	return out
}

// hiddenWrites says why a command writes files whose names are not on the
// line: an archive it unpacks (tar -x, unzip, cpio -i) or a patch it
// applies (patch, git apply, git am) names them. "" when it writes none
// such.
func hiddenWrites(words []word) string {
	if len(words) == 0 || !words[0].static() {
		return ""
	}
	name := progKey(baseName(words[0].joined()))
	args := words[1:]
	has := func(test func(l string, i int) bool) bool {
		for i, a := range args {
			if a.static() && test(a.joined(), i) {
				return true
			}
		}
		return false
	}
	short := func(l string, letters string) bool {
		return len(l) > 1 && l[0] == '-' && l[1] != '-' && strings.ContainsAny(l[1:], letters)
	}
	switch name {
	case "tar", "bsdtar":
		if has(func(l string, i int) bool {
			return short(l, "x") || longFlag(l, "--extract", len("--extr")) || l == "--get" || i == 0 && !strings.HasPrefix(l, "-") && strings.Contains(l, "x")
		}) {
			return "tar unpacks the files its archive names"
		}
	case "unzip":
		// (-l, -v, -t and -Z list or test it; -p writes to the output.)
		if !has(func(l string, _ int) bool { return short(l, "lvtpZ") }) {
			return "unzip unpacks the files its archive names"
		}
	case "cpio":
		if has(func(l string, _ int) bool { return short(l, "i") || longFlag(l, "--extract", len("--extr")) }) {
			return "cpio -i unpacks the files its archive names"
		}
	case "patch":
		if !has(func(l string, _ int) bool { return strings.HasPrefix(l, "--dry") }) {
			return "patch writes the files its patch names"
		}
	case "git":
		for i, a := range args {
			if !a.static() || strings.HasPrefix(a.joined(), "-") {
				continue
			}
			switch a.joined() {
			case "apply":
				for _, b := range args[i+1:] {
					if l := b.lit(); l == "--check" || l == "--stat" || l == "--numstat" || l == "--summary" {
						return ""
					}
				}
				return "git apply writes the files its patch names"
			case "am":
				return "git am writes the files its patches name"
			}
			return "" // another subcommand
		}
	}
	return ""
}

// outputs are the files a command is told to write, for a program this
// package knows nothing about: the value of a flag that names its output,
// as most programs spell it (-o file, -ofile, --output=file, --out file,
// -out file), the archive tar makes or adds to (-cf x.tar, cf x.tar) and
// zip's, and sqlite3's database.
func outputs(name string, args []word) []word {
	name = progKey(name)
	if _, ok := roTable[name]; ok || wordsOnly[name] {
		return nil
	}
	var out []word
	var ops []word
	for i := 0; i < len(args); i++ {
		a := args[i]
		l := a.lit()
		if !a.static() || !strings.HasPrefix(l, "-") || l == "-" || a.tilde {
			ops = append(ops, a)
			continue
		}
		flag, val, eq := strings.Cut(l, "=")
		switch {
		case flag == "-o" || flag == "--output" || flag == "--out" || flag == "-out" || flag == "--outfile" || flag == "--output-file" || flag == "-output":
			if eq {
				out = append(out, partOf(a, val))
			} else if i+1 < len(args) {
				i++
				out = append(out, args[i])
			}
		case strings.HasPrefix(l, "-o") && len(l) > 2 && !eq && !strings.HasPrefix(l, "--"):
			out = append(out, partOf(a, l[2:])) // -ofile, as cc takes it
		}
	}
	switch name {
	case "tar", "bsdtar":
		if f := tarArchive(args); f != nil {
			out = append(out, *f)
		}
	case "zip":
		if len(ops) > 0 && ops[0].static() {
			out = append(out, ops[0], partOf(ops[0], ops[0].joined()+".zip"))
		}
	case "sqlite3":
		if len(ops) > 0 {
			out = append(out, ops[0])
		}
	}
	return out
}

// tarArchive is the archive tar makes or adds to (c, r, u, A), named by
// -f, --file or the f of its letters written with no dash; nil when tar
// only reads or unpacks one, or the archive is not named.
func tarArchive(args []word) *word {
	makes := false
	for i, a := range args {
		if !a.static() {
			continue
		}
		l := a.joined()
		switch {
		case longFlag(l, "--create", len("--cr")) || longFlag(l, "--append", len("--ap")) || longFlag(l, "--update", len("--up")) || longFlag(l, "--concatenate", len("--conc")) || longFlag(l, "--catenate", len("--cat")):
			makes = true
		case len(l) > 1 && l[0] == '-' && l[1] != '-' && strings.ContainsAny(l[1:], "cruA"):
			makes = true
		case i == 0 && !strings.HasPrefix(l, "-") && strings.ContainsAny(l, "cruA"):
			makes = true
		}
	}
	if !makes {
		return nil
	}
	for i, a := range args {
		if !a.static() {
			continue
		}
		l := a.joined()
		switch {
		case longFlag(l, "--file", len("--file")) && strings.Contains(l, "="):
			_, v, _ := strings.Cut(l, "=")
			w := partOf(a, v)
			return &w
		case l == "--file" || l == "-f":
			if i+1 < len(args) {
				return &args[i+1]
			}
		case len(l) > 1 && l[0] == '-' && l[1] != '-' && strings.Contains(l[1:], "f"):
			// -czf x.tar: f takes the rest of the cluster, or the next word.
			if j := strings.IndexByte(l, 'f'); j+1 < len(l) {
				w := partOf(a, l[j+1:])
				return &w
			}
			if i+1 < len(args) {
				return &args[i+1]
			}
		case i == 0 && !strings.HasPrefix(l, "-") && strings.Contains(l, "f"):
			// The old way: the letters' values follow in their order; the
			// archive is the f's.
			n := 0
			for _, c := range l[:strings.IndexByte(l, 'f')] {
				if strings.ContainsRune("bCgKLNTVX", c) {
					n++ // a letter before f that also takes a word
				}
			}
			if 1+n < len(args) {
				return &args[1+n]
			}
		}
	}
	return nil
}
