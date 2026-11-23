package permission

import (
	"path"
	"strings"
)

// This file is the table of commands that only read. A command on it runs
// without a question when every flag it is given is one listed here as
// keeping it read-only, and everything it reads lies in the working
// directories. The table is deliberately short and each entry lists what
// is known to be safe: a flag that is not listed means "ask".

// opKind says what a command's operands (its non-flag arguments) are.
type opKind uint8

const (
	// opsPaths: every operand is a file or directory that is read.
	opsPaths opKind = iota
	// opsWords: operands are not files (echo's text, tr's sets).
	opsWords
	// opsPattern: the first operand is a pattern, unless a flag supplied
	// one; the rest are files.
	opsPattern
	// opsNone: no operands at all.
	opsNone
)

// roSpec describes one read-only command.
type roSpec struct {
	// short lists single-letter flags that take no value.
	short string
	// valued lists single-letter flags that take a value, attached or as
	// the next word.
	valued string
	// optional lists single-letter flags whose value, if any, is attached
	// (git's -U3): the next word is never theirs.
	optional string
	// long lists long flags, separated by spaces. "--name" takes no
	// value; "--name=" takes one, attached or as the next word.
	long string
	// ops says what the operands are; max is the most that are allowed
	// (0 means any number).
	ops opKind
	max int
	// digits lets -5 stand for a count, as head and tail take it.
	digits bool
	// noGlob: an unquoted file name pattern among the operands asks,
	// because the command has flags that write or run things and a
	// matched file name could be read as one.
	noGlob bool
	// patFlags are the valued flags that supply the pattern (grep -e).
	patFlags string
	// pathFlags are the valued flags whose value is a file that is read.
	pathFlags string
	// anyArgs: the arguments are only text to the command (echo), so
	// nothing that looks like a flag needs to be known.
	anyArgs bool
	// atFiles: an operand that begins with @ names a file the command
	// reads more arguments from (binutils' strings @opts), which may name
	// files of their own, so such an operand asks.
	atFiles bool
}

// roTable is the read-only set. Commands with rules of their own (cd,
// git, find, sed, date, command, test, [) are handled in readOnlyFacts.
var roTable = map[string]roSpec{
	"ls": {short: "aAbBcCdFfgGhiklmnopqrRsStuUvxX1@eO",
		long: "--all --almost-all --color --color= --human-readable --recursive --reverse --size --classify --directory --inode --numeric-uid-gid --no-group --group-directories-first --full-time --time-style= --sort= --time= --format= --indicator-style= --si --block-size= --hide= --ignore= --literal --quote-name --escape --file-type --width= --author --kibibytes --zero"},
	"cat":  {short: "beEnstTuvA", long: "--number --number-nonblank --squeeze-blank --show-all --show-ends --show-tabs --show-nonprinting"},
	"head": {short: "qvz", valued: "nc", digits: true, long: "--lines= --bytes= --quiet --silent --verbose --zero-terminated"},
	// tail: -b is left out. BSD tail takes a number after it; GNU tail
	// takes none, so the word after it is a file there.
	"tail": {short: "qvzfFr", valued: "ncs", digits: true, long: "--lines= --bytes= --quiet --silent --verbose --zero-terminated --follow --follow= --retry --pid= --sleep-interval="},
	"wc":   {short: "clmwL", long: "--bytes --chars --lines --words --max-line-length"},
	"nl":   {short: "p", valued: "bnwsvihfdl", long: "--body-numbering= --number-format= --number-width= --number-separator= --starting-line-number= --line-increment="},
	"grep": {short: "abcEFGHhIiLlnoPqsTUvwxZzr", valued: "efmABC", patFlags: "ef", pathFlags: "f", ops: opsPattern,
		long: "--include= --exclude= --exclude-dir= --color --color= --colour --colour= --line-number --ignore-case --no-messages --count --files-with-matches --files-without-match --only-matching --quiet --silent --invert-match --word-regexp --line-regexp --extended-regexp --fixed-strings --basic-regexp --perl-regexp --recursive --with-filename --no-filename --null --text --binary-files= --max-count= --after-context= --before-context= --context= --regexp= --line-buffered --byte-offset --initial-tab --null-data"},
	"rg": {short: "niSslcwxvFoHINUuP0", valued: "egtTABCmjM", patFlags: "e", ops: opsPattern,
		long: "--glob= --type= --type-not= --files --hidden --no-ignore --no-ignore-vcs --line-number --no-line-number --no-heading --heading --color= --json --count --count-matches --files-with-matches --files-without-match --ignore-case --smart-case --case-sensitive --fixed-strings --word-regexp --line-regexp --invert-match --only-matching --multiline --multiline-dotall --max-count= --context= --after-context= --before-context= --max-depth= --sort= --sortr= --trim --column --no-messages --pcre2 --stats --vimgrep --null --max-columns= --regexp= --threads= --with-filename --no-filename --text --unrestricted --no-config --passthru"},
	"which": {short: "as", ops: opsWords},
	"type":  {short: "at", ops: opsWords},
	"diff": {short: "ucqsiwbBaNytdEZpT", valued: "UCWILx", max: 2,
		long: "--color --color= --unified --unified= --context --context= --brief --ignore-case --ignore-all-space --ignore-space-change --ignore-blank-lines --ignore-trailing-space --ignore-tab-expansion --side-by-side --width= --label= --exclude= --strip-trailing-cr --minimal --text --new-file --report-identical-files --no-dereference --suppress-common-lines --expand-tabs --show-c-function --ignore-matching-lines="},
	"stat":  {short: "xlnqFs", valued: "c", long: "--format= --printf= --terse"},
	"du":    {short: "achkmsxgbP", valued: "dt", long: "--max-depth= --human-readable --summarize --total --all --apparent-size --block-size= --si --exclude= --one-file-system --bytes --threshold= --time --no-dereference"},
	"pwd":   {short: "LP", ops: opsNone},
	"echo":  {ops: opsWords, anyArgs: true},
	"true":  {ops: opsWords, anyArgs: true},
	"false": {ops: opsWords, anyArgs: true},
	// sleep only waits. (A long one in the foreground is refused by the
	// shell tool, whatever the rules say.)
	"sleep": {ops: opsWords},
	// strings prints the text in files. GNU's takes more arguments from a
	// file named after an @ (atFiles); the BSDs' and macOS's do not.
	"strings": {short: "adfow", valued: "ntesTU", digits: true, atFiles: true,
		long: "--all --data --print-file-name --bytes= --radix= --encoding= --target= --include-all-whitespace --output-separator= --unicode="},
	// gofmt prints Go source formatted, its differences (-d) or the files
	// that differ (-l). -w writes the files and -cpuprofile a profile, and
	// neither is listed.
	"gofmt": {short: "ldes", valued: "r"},
	// ss lists sockets. -K kills them, -D writes what it reads to a file
	// and -F reads a filter from one; none is listed. Its operands are a
	// filter (state established, dport = :80).
	"ss": {short: "hnrapolmeitxuwsSHOZz046bE", valued: "fA", ops: opsWords,
		long: "--numeric --resolve --all --listening --options --extended --memory --processes --info --tcp --udp --unix --raw --summary --no-header --oneline --ipv4 --ipv6 --packet --threads --cgroup --tos --events --context --contexts --family="},
	"sort": {short: "rnufbdghiMVRszcCm", valued: "kt", noGlob: true,
		long: "--reverse --numeric-sort --unique --ignore-case --ignore-leading-blanks --dictionary-order --general-numeric-sort --human-numeric-sort --month-sort --version-sort --stable --zero-terminated --check --check= --merge --key= --field-separator="},
	"uniq": {short: "cduizD", valued: "fsw", max: 1, long: "--count --repeated --unique --ignore-case --zero-terminated --skip-fields= --skip-chars= --check-chars="},
	// od and hexdump dump bytes. (od's -w takes a value only when it is
	// attached, and is left out: "od -w file" must not lose its file.)
	"od": {short: "abcdfilosvx", valued: "AjNSt",
		long: "--address-radix= --skip-bytes= --read-bytes= --strings --strings= --format= --output-duplicates --width= --endian= --traditional"},
	// (hexdump's --format-file is left out: the file it names is read, and
	// only the -f spelling of it is followed as one.)
	"hexdump": {short: "bcCdovx", valued: "ens", pathFlags: "f",
		long: "--one-byte-octal --one-byte-char --canonical --two-bytes-decimal --two-bytes-octal --two-bytes-hex --format= --length= --skip= --no-squeezing --color --color="},
	"cut":      {short: "sz", valued: "dfcb", long: "--delimiter= --fields= --characters= --bytes= --only-delimited --complement --output-delimiter= --zero-terminated"},
	"tr":       {short: "dscCt", ops: opsWords, long: "--delete --squeeze-repeats --complement --truncate-set1"},
	"basename": {short: "az", valued: "s", ops: opsWords, long: "--multiple --suffix= --zero"},
	"dirname":  {short: "z", ops: opsWords, long: "--zero"},
	"realpath": {short: "emqsz", long: "--canonicalize-existing --canonicalize-missing --quiet --strip --no-symlinks --zero"},
	"readlink": {short: "fnemqsvz", long: "--canonicalize --canonicalize-existing --canonicalize-missing --no-newline --quiet --silent --verbose --zero"},
	// file: -p (--preserve-date) is left out because it writes: it sets
	// the file's times back after reading it, to the microsecond. -z is
	// left out because, for the formats file was not built to unpack
	// itself, it starts whatever gzip or lrzip is on the search path.
	"file":   {short: "bihkINnrs0", long: "--brief --mime --mime-type --mime-encoding --no-dereference --keep-going --no-buffer --raw --special-files --print0"},
	"whoami": {ops: opsNone},
	"uname":  {short: "asnrvmpio", ops: opsNone, long: "--all --kernel-name --nodename --kernel-release --kernel-version --machine --processor --hardware-platform --operating-system"},
	"seq":    {short: "w", valued: "sf", ops: opsWords, long: "--equal-width --separator= --format="},
}

// cmdFacts is what is known about one simple command after its wrappers
// are taken off.
type cmdFacts struct {
	// name is the program's name; plain says it was written as plain
	// text, with nothing for the shell to expand or unquote.
	name  string
	plain bool
	// readOnly says the command, as written, only reads. why says why
	// not.
	readOnly bool
	why      string
	// fsEdit says the command is one of the file commands acceptEdits
	// lets run (mkdir, touch, rm, rmdir, mv, cp, sed -i) in a form that
	// is fully understood.
	fsEdit bool
	// reads and writes are the operands the command reads and changes.
	reads, writes []word
	// lands are the places a copy (cp, mv, ln, install, rsync, scp) puts
	// its sources in a folder (copyLanding). They are held to the Read and
	// Edit rules and the protected paths; where the copy lands for the
	// working directories, cpLands says.
	lands []copyLanding
	// rewrites are files the command may put back as they are (see
	// sedFacts): nothing in them changes, but a link among them would be
	// replaced by a copy of what it leads to.
	rewrites []word
	// tree is set for a cp that copies a directory with all that is in it
	// (-R, -a).
	tree bool
	// cd is set for a cd command: the directory it changes to.
	cd *word
	// git is set for any git command.
	git bool
}

// parsed is the result of reading a command's arguments against a spec.
type parsed struct {
	ops      []word
	pathVals []word
	sawPat   bool
	bad      string
	// late are the flags, and the values of flags, that stand after the
	// first operand. GNU's tools take them for flags wherever they
	// stand; the tools of macOS and the BSDs stop reading flags at the
	// first operand, and "cat a.txt -n" reads a file named -n there. So
	// they count as files too.
	late []word
}

// parseArgs reads args against spec. bad is set to the reason when a flag
// is not one the spec lists, or a word is not static text.
func parseArgs(args []word, spec roSpec) parsed {
	var out parsed
	long := " " + spec.long + " "
	rest := false
	// sawOp: an operand has been read, so what follows is all operands to
	// a tool that stops reading flags there.
	sawOp := false
	late := func(w word) {
		if sawOp && !spec.anyArgs {
			out.late = append(out.late, w)
		}
	}
	for i := 0; i < len(args); i++ {
		w := args[i]
		if !w.static() && !(spec.anyArgs && (w.counters() || w.literalParts())) {
			out.bad = "an argument is computed when the command runs: " + w.raw
			return out
		}
		if w.brace {
			out.bad = "an argument holds braces the shell may expand: " + w.raw
			return out
		}
		if w.glob && spec.noGlob {
			out.bad = "a file name pattern (" + w.raw + ") is given to a command that has flags that write or run things"
			return out
		}
		s := w.joined()
		if spec.atFiles && (strings.HasPrefix(s, "@") || w.glob) {
			// (A pattern may match a file whose name begins with @.)
			out.bad = "an argument (" + w.raw + ") may name a file the command reads more arguments from"
			return out
		}
		switch {
		case rest || spec.anyArgs || s == "-" || !strings.HasPrefix(s, "-") || w.tilde:
			out.ops = append(out.ops, w)
			sawOp = true
		case s == "--":
			rest = true
			late(w)
		case strings.HasPrefix(s, "--"):
			late(w)
			// (White space in a flag's name, as in '--color --all', makes
			// it a name the list does not hold; in its value, as in
			// --format='%h %s', it is only text.)
			name, _, hasVal := strings.Cut(s, "=")
			if name == "--regexp" {
				out.sawPat = true
			}
			switch {
			case hasVal && strings.Contains(long, " "+name+"= "):
			case !hasVal && strings.Contains(long, " "+s+" "):
			case !hasVal && strings.Contains(long, " "+s+"= "):
				i++ // the value is the next word
				if i >= len(args) || !args[i].static() || args[i].glob {
					out.bad = "the flag " + s + " has no plain value"
					return out
				}
				late(args[i])
			case s == "--help" || s == "--version":
			default:
				out.bad = "the flag " + name + " is not one known to only read"
				return out
			}
		default:
			late(w)
			for j := 1; j < len(s); j++ {
				c := s[j]
				switch {
				case strings.IndexByte(spec.optional, c) >= 0:
					j = len(s)
				case strings.IndexByte(spec.valued, c) >= 0:
					var val word
					if j+1 < len(s) {
						val = word{raw: s[j+1:], parts: []wordPart{{lit: s[j+1:]}}}
					} else {
						i++
						if i >= len(args) || !args[i].static() || args[i].glob {
							out.bad = "the flag -" + string(c) + " has no plain value"
							return out
						}
						val = args[i]
						late(val)
					}
					if strings.IndexByte(spec.patFlags, c) >= 0 {
						out.sawPat = true
					}
					if strings.IndexByte(spec.pathFlags, c) >= 0 {
						out.pathVals = append(out.pathVals, val)
					}
					j = len(s)
				case strings.IndexByte(spec.short, c) >= 0:
				case spec.digits && c >= '0' && c <= '9':
				default:
					out.bad = "the flag -" + string(c) + " is not one known to only read"
					return out
				}
			}
		}
	}
	return out
}

// litsOf returns the literal text of each word.
func litsOf(ws []word) []string {
	out := make([]string, len(ws))
	for i, w := range ws {
		out[i] = w.lit()
	}
	return out
}

// baseName returns the last element of a program path.
func baseName(s string) string {
	if strings.Contains(s, "/") {
		return path.Base(s)
	}
	return s
}

// plainName reports whether a program name is plain text made of the
// characters program names are made of.
func plainName(w word) bool {
	if w.raw == "[" || w.raw == "[[" {
		return true
	}
	if !w.plain() {
		return false
	}
	s := w.joined()
	if s == "" || s[0] == '-' {
		return false
	}
	if s == "[" {
		return true
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(isNameChar(c) || strings.IndexByte("./+-@%:,", c) >= 0) {
			return false
		}
	}
	return true
}

// facts works out what the command made of words does, as far as this
// package's tables know.
func facts(words []word) (f cmdFacts) {
	if len(words) == 0 {
		f.readOnly = true
		return f
	}
	w0 := words[0]
	f.plain = plainName(w0)
	if !w0.static() {
		f.why = "the program name is computed when the command runs"
		return f
	}
	f.name = w0.joined()
	args := words[1:]
	if !f.plain {
		f.why = "the program name is not written as plain text"
	}
	// Whatever comes of the judgement below, the files the command names
	// stay on record, for the Read and Edit rules: a flag this table does
	// not know, or a name in quotes, must not hide the file beside it.
	defer func() {
		if !f.readOnly && !f.fsEdit {
			f.reads = moreWords(f.reads, namedFiles(f.name, args))
			f.reads = moreWords(f.reads, mentioned(f.name, args))
			f.writes = moreWords(f.writes, outputs(f.name, args))
		}
	}()
	// A program reached by a path is not the one the table describes.
	if strings.Contains(f.name, "/") {
		f.name = baseName(f.name)
		fillTargets(&f, args)
		f.git = f.name == "git"
		if f.why == "" {
			f.why = w0.joined() + " is named by its path, so gnosh cannot tell it is the usual " + f.name
		}
		return f
	}
	fillTargets(&f, args)
	if f.why != "" {
		return f
	}
	switch f.name {
	case "cd":
		cdFacts(&f, args)
	case "git":
		f.git = true
		gitFacts(&f, args)
	case "printf":
		printfFacts(&f, args)
	case "ps":
		psFacts(&f, args)
	case "find":
		findFacts(&f, args)
	case "sed":
		sedFacts(&f, args)
	case "date":
		dateFacts(&f, args)
	case "command":
		// "command -v name" and "command -V name" say what a name is.
		// Every other form runs the command: "command rm x --".
		ok := len(args) >= 2 && args[0].plain() && (args[0].joined() == "-v" || args[0].joined() == "-V")
		for _, a := range args[min(1, len(args)):] {
			ok = ok && a.static() && !a.glob && !a.brace && !strings.HasPrefix(a.joined(), "-")
		}
		if !ok {
			f.why = "only \"command -v name\" is known to only read"
		} else {
			f.readOnly = true
		}
	case "test", "[", "[[":
		testFacts(&f, args)
	case "xxd":
		xxdFacts(&f, args)
	case "mkdir", "touch", "rm", "rmdir", "mv", "cp":
		fsFacts(&f, args)
	default:
		spec, ok := roTable[f.name]
		if !ok {
			f.why = f.name + " is not on the list of commands that only read"
			return f
		}
		p := parseArgs(args, spec)
		if p.bad != "" {
			f.why = p.bad
			return f
		}
		ops := p.ops
		switch spec.ops {
		case opsNone:
			if len(ops) > 0 {
				f.why = f.name + " is given arguments it does not take when it only reads"
				return f
			}
		case opsWords:
			// The operands are text, not files, except that the shell
			// turns an unquoted pattern into the names of files: echo
			// /etc/* is a listing of /etc.
			var named []word
			for _, o := range ops {
				if o.glob {
					named = append(named, o)
				}
			}
			ops = named
		case opsPattern:
			if !p.sawPat && !hasLong(args, "--files") {
				if len(ops) == 0 {
					f.why = f.name + " has no pattern"
					return f
				}
				if ops[0].glob {
					// The shell would put file names where the pattern
					// goes, and one that starts with a dash is a flag.
					f.why = "a file name pattern stands where " + f.name + "'s own pattern goes"
					return f
				}
				// A computed word outside quotes is cut into words at its
				// blanks: the first is the pattern and the others are
				// files the command reads ("grep `git status` x" read the
				// .env git status named; oracle seed 134). Such a word is
				// held as a file too.
				if !mayBecomeWords(ops[0]) {
					ops = ops[1:]
				}
			}
		}
		if spec.max > 0 && len(ops) > spec.max {
			f.why = f.name + " is given more files than it reads"
			return f
		}
		if spec.max > 0 {
			// A pattern may turn into more files than the command reads,
			// and the one after the last it reads may be one it writes:
			// "uniq [ab].txt" is "uniq a.txt b.txt".
			for _, o := range ops {
				if o.glob {
					f.why = "a file name pattern (" + o.raw + ") may turn into more files than " + f.name + " reads"
					return f
				}
			}
		}
		if f.name == "uniq" && len(ops)+len(p.late) > spec.max {
			// uniq's second file is the one it writes, and to the uniq of
			// macOS a flag after the first file is that second file.
			f.why = "uniq is given a word after its file, which some uniq programs take for a file to write"
			return f
		}
		for _, o := range append(ops, p.pathVals...) {
			if o.glob && spec.noGlob {
				f.why = "a file name pattern is given to " + f.name + ", which has flags that write or run things"
				return f
			}
		}
		f.reads = append(ops, p.pathVals...)
		if spec.ops == opsPaths || spec.ops == opsPattern {
			f.reads = append(f.reads, p.late...)
		}
		f.readOnly = true
	}
	return f
}

// partOf makes a word of a piece of another: the file name after an @,
// after a flag's =, after a revision's colon. What the shell would still
// do to the whole word (braces, a file name pattern) it would do to the
// piece.
func partOf(w word, text string) word {
	return word{raw: text, parts: []wordPart{{lit: text}},
		brace: w.brace && strings.ContainsAny(text, "{}"), glob: w.glob && hasPathMagic(text), quoted: w.quoted}
}

// moreWords returns ws with those of more that it does not hold yet.
func moreWords(ws, more []word) []word {
	for _, m := range more {
		have := false
		for _, w := range ws {
			if w.raw == m.raw {
				have = true
				break
			}
		}
		if !have {
			ws = append(ws, m)
		}
	}
	return ws
}

// hasLong reports whether args holds the flag as a word of its own.
func hasLong(args []word, flag string) bool {
	for _, a := range args {
		if a.lit() == flag {
			return true
		}
	}
	return false
}

// cdFacts judges cd: with one plain directory it only moves the shell.
func cdFacts(f *cmdFacts, args []word) {
	if len(args) != 1 || !args[0].static() || args[0].glob || args[0].brace {
		f.why = "cd without one plain directory may leave the working directories"
		return
	}
	if l := args[0].joined(); l == "-" || strings.HasPrefix(l, "-") && !args[0].quoted {
		f.why = "cd to a directory gnosh cannot name ahead of time"
		return
	}
	f.cd = &args[0]
	f.readOnly = true
}

// dateFacts judges date: it may print the time, never set it or read a
// file of dates.
func dateFacts(f *cmdFacts, args []word) {
	p := parseArgs(args, roSpec{short: "uRIj", valued: "d", ops: opsWords,
		long: "--utc --universal --iso-8601 --iso-8601= --rfc-3339= --rfc-email --rfc-2822 --date="})
	if p.bad != "" {
		f.why = p.bad
		return
	}
	for _, o := range p.ops {
		if !strings.HasPrefix(o.joined(), "+") || o.glob {
			f.why = "date with a bare argument sets the clock"
			return
		}
	}
	f.readOnly = true
}

// xxdBool and xxdValued are the flags of xxd that only change how it
// dumps. Each is a word of its own: xxd's flags do not combine, so "-ps"
// is one flag, not -p and -s.
var (
	xxdBool = map[string]bool{"-a": true, "-autoskip": true, "-b": true, "-bits": true, "-C": true, "-capitalize": true,
		"-E": true, "-EBCDIC": true, "-e": true, "-i": true, "-include": true, "-p": true, "-ps": true,
		"-postscript": true, "-plain": true, "-r": true, "-revert": true, "-u": true, "-d": true}
	xxdValued = map[string]bool{"-c": true, "-cols": true, "-g": true, "-groupsize": true, "-l": true, "-len": true,
		"-o": true, "-offset": true, "-s": true, "-seek": true, "-n": true, "-name": true, "-R": true}
)

// xxdFacts judges xxd: it only reads when it writes to standard output,
// which is when it is given at most one file, the one it reads; a second
// file is one it writes. Its flags come first, each a known word, and the
// value of one that takes a value is the plain word after it.
func xxdFacts(f *cmdFacts, args []word) {
	var ops []word
	for i := 0; i < len(args); i++ {
		a := args[i]
		w := a.joined()
		switch {
		case !a.static() || a.glob && len(ops) == 0:
			f.why = "xxd is given a word whose value is not known before it runs"
			return
		case len(ops) > 0:
			ops = append(ops, a)
		case xxdBool[w]:
		case xxdValued[w]:
			if i+1 >= len(args) || !args[i+1].static() || args[i+1].glob {
				f.why = "xxd's " + w + " is not given a plain value"
				return
			}
			i++
		case strings.HasPrefix(w, "-") && w != "-":
			f.why = "xxd is given a flag (" + w + ") that is not known to only read"
			return
		default:
			ops = append(ops, a)
		}
	}
	if len(ops) > 1 {
		f.why = "xxd writes to the second file it is given"
		return
	}
	if len(ops) == 1 && ops[0].glob {
		f.why = "a file name pattern may turn into a second file, which xxd writes"
		return
	}
	if len(ops) == 1 && ops[0].joined() != "-" {
		f.reads = ops
	}
	f.readOnly = true
}

// testOps are the operators of test, [ and [[ that only look at files
// and strings. -v and -R are left out because the shell evaluates their
// operand, and an operand it evaluates can run a command.
var testOps = map[string]bool{
	"-e": true, "-f": true, "-d": true, "-r": true, "-w": true, "-x": true, "-s": true,
	"-L": true, "-h": true, "-b": true, "-c": true, "-p": true, "-S": true, "-g": true,
	"-u": true, "-k": true, "-O": true, "-G": true, "-N": true, "-z": true, "-n": true,
	"-nt": true, "-ot": true, "-ef": true, "-a": true, "-o": true,
}

// testNumOps compare numbers. Shells evaluate their operands as
// arithmetic, and arithmetic can run a command (a[$(cmd)]), so each
// operand must be a plain whole number.
var testNumOps = map[string]bool{"-eq": true, "-ne": true, "-lt": true, "-le": true, "-gt": true, "-ge": true}

// testFacts judges test, [ and [[: they only look, and what they look at
// is read like any other file. Nothing an operand holds may be something
// the shell could evaluate.
func testFacts(f *cmdFacts, args []word) {
	if closer := map[string]string{"[": "]", "[[": "]]"}[f.name]; closer != "" {
		if len(args) == 0 || args[len(args)-1].raw != closer {
			f.why = f.name + " without its closing " + closer
			return
		}
		args = args[:len(args)-1]
	}
	// file marks the operands that a file test looks at: they are read
	// like any other file, whether or not they look like a path. (A link
	// in the folder that leads out of it tells, through -s or -nt, about
	// the file it leads to.)
	file := map[int]bool{}
	for i, a := range args {
		if !a.static() {
			continue
		}
		switch l := a.joined(); {
		case testFileOps[l] && i+1 < len(args):
			file[i+1] = true
		case l == "-nt" || l == "-ot" || l == "-ef":
			file[i-1], file[i+1] = true, true
		}
	}
	for i, a := range args {
		if !a.static() || a.brace || a.glob {
			f.why = "an argument is computed when the command runs, or is a pattern: " + a.raw
			return
		}
		l := a.joined()
		switch {
		case strings.ContainsAny(l, "$`[]()"):
			f.why = "an operand (" + a.raw + ") holds characters the shell could evaluate"
			return
		case testNumOps[l]:
			if i == 0 || i+1 >= len(args) || !isInt(args[i-1].joined()) || !isInt(args[i+1].joined()) {
				f.why = "a number comparison whose operands are not plain numbers"
				return
			}
		case strings.HasPrefix(l, "-") && !testOps[l] && !isInt(l):
			f.why = f.name + "'s " + l + " is not one known to only look"
			return
		case file[i] && !testOps[l], a.tilde || strings.Contains(l, "/") || l == "." || l == "..":
			f.reads = append(f.reads, a)
		}
	}
	f.readOnly = true
}

// testFileOps are the operators of test that look at the file named
// after them.
var testFileOps = map[string]bool{
	"-e": true, "-f": true, "-d": true, "-r": true, "-w": true, "-x": true, "-s": true, "-L": true,
	"-h": true, "-b": true, "-c": true, "-p": true, "-S": true, "-g": true, "-u": true, "-k": true,
	"-O": true, "-G": true, "-N": true,
}

// findFacts judges find: starting points are read, and only tests and
// printing actions are allowed.
func findFacts(f *cmdFacts, args []word) {
	i := 0
	for ; i < len(args); i++ {
		if !args[i].static() || args[i].brace {
			f.why = "an argument is computed when the command runs: " + args[i].raw
			return
		}
		l := args[i].joined()
		if l == "-P" {
			continue
		}
		if strings.HasPrefix(l, "-") || l == "!" || l == "(" {
			break
		}
		if args[i].glob {
			f.why = "a file name pattern is given to find, which has flags that write or run things"
			return
		}
		f.reads = append(f.reads, args[i])
	}
	for _, a := range args {
		if a.glob {
			f.why = "a file name pattern is given to find, which has flags that write or run things: " + a.raw
			return
		}
	}
	noVal := " -empty -depth -prune -print -print0 -not -a -and -o -or -readable -writable -executable -true -false -daystart -mount -xdev -noleaf -ls -quit -nouser -nogroup ! ( ) "
	withVal := " -name -iname -path -ipath -wholename -iwholename -regex -iregex -type -xtype -size -mtime -mmin -atime -amin -ctime -cmin -maxdepth -mindepth -user -group -perm -links -printf -regextype -newermt -uid -gid "
	for ; i < len(args); i++ {
		if !args[i].static() {
			f.why = "an argument is computed when the command runs: " + args[i].raw
			return
		}
		l := args[i].joined()
		switch {
		case strings.Contains(noVal, " "+l+" "):
		case strings.Contains(withVal, " "+l+" "):
			i++
			if i >= len(args) || !args[i].static() {
				f.why = "find's " + l + " has no plain value"
				return
			}
		default:
			f.why = "find's " + l + " is not one known to only read"
			return
		}
	}
	f.readOnly = true
}

// gitGlobal are git's own flags, before the subcommand, that change
// nothing about what runs.
var gitGlobal = map[string]bool{"--no-pager": true, "--no-optional-locks": true}

// gitRead lists, per read-only subcommand, the flags that keep it so.
// Flags are matched whole: git accepts any unambiguous start of a long
// flag, and a shortened --output or --ext-diff must not slip through, so
// anything not spelled exactly as here asks.
//
// A flag whose value git takes only when it is attached (-U3,
// --unified=3, --pretty=x, --abbrev=7) is listed so that the word after
// it stays what git takes it for: a revision or a file. "git diff -U
// ../other/secret a.txt" compares two files, one of them outside the
// repository, and prints both.
var gitRead = map[string]roSpec{
	"status": {short: "sbvz", optional: "u", long: "--short --branch --porcelain --porcelain= --long --untracked-files --untracked-files= --ignored --ignored= --show-stash --ahead-behind --no-ahead-behind --renames --no-renames --verbose --column --no-column"},
	"diff": {short: "pubwRWzMCa", optional: "U", noGlob: true,
		long: "--stat --stat= --numstat --shortstat --name-only --name-status --cached --staged --patch --unified --unified= --no-color --color --color= --word-diff --word-diff= --color-words --ignore-all-space --ignore-space-change --ignore-blank-lines --minimal --patience --histogram --diff-filter= --find-renames --find-renames= --summary --check --no-ext-diff --no-textconv --exit-code --quiet --relative --raw --compact-summary --dirstat --no-renames --merge-base --function-context --no-prefix --abbrev --abbrev= --full-index --text --diff-algorithm="},
	"log": {short: "pusi", valued: "nSG", digits: true, noGlob: true,
		long: "--oneline --max-count= --stat --stat= --graph --all --decorate --decorate= --no-decorate --pretty= --format= --abbrev-commit --name-only --name-status --since= --until= --after= --before= --author= --committer= --grep= --no-merges --merges --first-parent --reverse --follow --date= --color --color= --no-color --shortstat --numstat --patch --no-patch --topo-order --date-order --branches --tags --remotes --left-right --cherry-pick --no-ext-diff --no-textconv --unified --unified= --diff-filter= --full-history --ancestry-path --source --skip= --regexp-ignore-case --invert-grep --abbrev --abbrev= --raw --summary --pretty"},
	"show": {short: "pus", optional: "U", noGlob: true,
		long: "--stat --stat= --name-only --name-status --pretty --pretty= --format= --no-patch --patch --color --color= --no-color --oneline --abbrev-commit --no-ext-diff --no-textconv --unified --unified= --shortstat --numstat --raw --summary --date= --decorate --no-decorate --abbrev --abbrev= --word-diff --word-diff= --ignore-all-space --ignore-space-change"},
	"branch":    {short: "arv", ops: opsNone, long: "--list --show-current --all --remotes --verbose --no-color --color --color= --column --no-column"},
	"rev-parse": {short: "q", ops: opsWords, long: "--show-toplevel --abbrev-ref --short --short= --verify --git-dir --is-inside-work-tree --is-inside-git-dir --show-prefix --show-cdup --is-bare-repository --symbolic-full-name --absolute-git-dir --git-common-dir --quiet --symbolic --all --branches --tags --remotes"},
	"ls-files":  {short: "cdmoisukztv", valued: "x", noGlob: true, long: "--cached --deleted --modified --others --ignored --stage --unmerged --killed --exclude-standard --full-name --error-unmatch --exclude= --abbrev --abbrev= --directory --no-empty-directory --deduplicate"},
	"blame":     {short: "lsewMCctfn", valued: "L", noGlob: true, long: "--date= --porcelain --line-porcelain --show-name --show-number --show-email --root --abbrev --abbrev= --color-lines --color-by-age --incremental"},
	// git grep: -O and --open-files-in-pager start a program, --textconv
	// runs the filters the repository's settings name, -f reads the
	// patterns from a file, and --no-index searches outside the
	// repository. None is listed.
	"grep": {short: "nliIvwcFEGPhHozqLWpa", valued: "eABCm", patFlags: "e", digits: true, noGlob: true,
		long: "--cached --untracked --exclude-standard --no-exclude-standard --recurse-submodules --text --no-textconv --ignore-case --word-regexp --invert-match --full-name --extended-regexp --basic-regexp --perl-regexp --fixed-strings --line-number --column --files-with-matches --name-only --files-without-match --null --only-matching --count --color --color= --no-color --break --heading --show-function --function-context --after-context= --before-context= --context= --threads= --max-depth= --max-count= --and --or --not --all-match --quiet"},
}

// gitReadPairs lists the read-only subcommands git names with two words,
// and the flags that keep each so. git stash list takes git log's flags,
// --output among them, so it takes none here.
var gitReadPairs = map[string]map[string]roSpec{
	"worktree": {"list": {short: "vz", ops: opsNone, long: "--porcelain --verbose --expire="}},
	"stash":    {"list": {ops: opsNone}},
}

// printfFacts judges printf: like echo it only writes its words, but with
// -v it sets a variable of the shell instead ("printf -v PATH x"), and
// that is not reading.
func printfFacts(f *cmdFacts, args []word) {
	if len(args) > 0 && (!args[0].static() || strings.HasPrefix(args[0].joined(), "-v")) {
		f.why = "printf -v sets a variable of the shell"
		return
	}
	f.readOnly = true
}

// The flags of ps that keep it to listing processes, as Linux's procps and
// macOS's ps read them. Each takes a value on both (psValued), or none on
// at least one of them (psShort): taken for one with none, its value is
// read as one more argument, and never passes for less than it is. A value
// that begins with a dash is read as a flag all the same.
const (
	psShort  = "AaCcdefFHhjLlMmNqrSsTvwXxyZ"
	psValued = "GgOoptUu"
	// psBSD are the letters of the dashless form (ps aux).
	psBSD = "aAcCfghHjlLmMnrSsTuvwxXZ"
)

// psLong are procps's long flags that only list; true marks those that
// take a value.
var psLong = map[string]bool{
	"--forest": false, "--no-headers": false, "--headers": false, "--cumulative": false, "--deselect": false, "--info": false,
	"--sort": true, "--pid": true, "--ppid": true, "--user": true, "--User": true, "--group": true, "--Group": true,
	"--format": true, "--cols": true, "--columns": true, "--width": true, "--lines": true, "--rows": true, "--sid": true, "--tty": true,
}

// psFacts judges ps. It only lists processes, but the dashless e of BSD's
// form (ps auxe) and macOS's -E add each one's environment, which holds
// the person's secrets, and those ask, as env does. (On FreeBSD -e is the
// environment as well; gnosh reads ps as Linux and macOS have it.)
func psFacts(f *cmdFacts, args []word) {
	for i := 0; i < len(args); i++ {
		a := args[i]
		if !a.static() || a.glob || a.brace || a.tilde {
			f.why = "an argument to ps is computed when the command runs, or is a pattern: " + a.raw
			return
		}
		s := a.joined()
		switch {
		case strings.HasPrefix(s, "--"):
			name, _, hasVal := strings.Cut(s, "=")
			valued, ok := psLong[name]
			if !ok && s != "--help" && s != "--version" {
				f.why = "the flag " + name + " of ps is not one known to only list"
				return
			}
			if valued && !hasVal && i+1 < len(args) && !strings.HasPrefix(args[i+1].raw, "-") {
				i++
			}
		case strings.HasPrefix(s, "-") && len(s) > 1:
			for j := 1; j < len(s); j++ {
				c := s[j]
				if strings.IndexByte(psValued, c) >= 0 {
					if j == len(s)-1 && i+1 < len(args) && !strings.HasPrefix(args[i+1].raw, "-") {
						i++
					}
					break
				}
				if strings.IndexByte(psShort, c) < 0 {
					f.why = "the flag -" + string(c) + " of ps is not one known to only list"
					return
				}
			}
		case strings.Trim(s, "0123456789,") == "":
			// A process number, or several.
		default:
			if strings.Trim(s, psBSD) != "" {
				f.why = "ps " + s + " is not a form known to only list"
				return
			}
		}
	}
	f.readOnly = true
}

// gitFacts judges a git command.
func gitFacts(f *cmdFacts, args []word) {
	i := 0
	for i < len(args) && args[i].static() && gitGlobal[args[i].joined()] {
		i++
	}
	if i >= len(args) || !args[i].plain() {
		f.why = "git without a plain subcommand"
		return
	}
	sub := args[i].joined()
	spec, ok := gitRead[sub]
	if pairs := gitReadPairs[sub]; pairs != nil && i+1 < len(args) && args[i+1].plain() {
		if s, two := pairs[args[i+1].joined()]; two {
			sub, spec, ok = sub+" "+args[i+1].joined(), s, true
			i++
		}
	}
	if !ok {
		if strings.HasPrefix(sub, "-") {
			f.why = "git's " + sub + " is not one known to only read"
		} else {
			f.why = "git " + sub + " is not on the list of git commands that only read"
		}
		return
	}
	rest := args[i+1:]
	if hasLong(rest, "--help") {
		// git shows help by starting another program, which its settings
		// can name.
		f.why = "git " + sub + " --help starts a viewer"
		return
	}
	p := parseArgs(rest, spec)
	if p.bad != "" {
		f.why = "git " + sub + ": " + p.bad
		return
	}
	if spec.ops == opsNone && len(p.ops) > 0 {
		f.why = "git " + sub + " with a name may change the repository"
		return
	}
	// Operands are revisions and paths. Anything that could name a file
	// outside the repository is read like a file.
	for _, o := range p.ops {
		if o.glob {
			// Whatever the subcommand: a file whose name starts with a
			// dash would reach git as a flag.
			f.why = "a file name pattern is given to git, which has flags that write or run things"
			return
		}
		l := o.joined()
		if spec.ops != opsWords && (o.tilde || strings.HasPrefix(l, "/") || strings.HasPrefix(l, ".") || strings.Contains(l, "/")) {
			f.reads = append(f.reads, o)
		}
		if p, ok := revPath(l); ok && spec.ops != opsWords {
			f.reads = append(f.reads, partOf(o, p))
		}
	}
	f.readOnly = true
}

// revPath returns the file a word of the form revision:path names
// ("HEAD:.env", ":0:src/a.go"): git shows the copy it keeps of that
// file, and a rule about the file is a rule about its copies. A path
// from the repository's root (":/x", which is also how a commit is
// searched for) is left alone.
func revPath(l string) (string, bool) {
	i := strings.LastIndexByte(l, ':')
	if i < 0 || i+1 >= len(l) || l[i+1] == '/' || strings.HasPrefix(l, "-") {
		return "", false
	}
	return l[i+1:], true
}

// sedRead is how one family of sed reads a command's words. There are
// two: GNU sed, which takes a flag wherever it stands and a backup suffix
// only when it is attached to -i, and the sed of macOS and the BSDs,
// which stops reading flags at the first word that is not one and takes
// the word after -i for the suffix whatever it is. "sed -i p x f" is
// "run p on x and f" to the first and "run x on f, keep a backup named
// fp" to the second.
type sedRead struct {
	// fails: this sed stops with an error before it does anything.
	fails   bool
	inPlace bool
	// suffix is what an in-place edit adds to a file's name for its
	// backup ("" for no backup).
	suffix  string
	scripts []word
	files   []word
}

// sedGNU reads the words as GNU sed does. bad says why the words are not
// ones this package follows.
func sedGNU(args []word) (r sedRead, bad string) {
	var operands []word
	rest := false
	for i := 0; i < len(args); i++ {
		a := args[i]
		l := a.joined()
		switch {
		case rest || l == "-" || !strings.HasPrefix(l, "-"):
			operands = append(operands, a)
		case l == "--":
			rest = true
		case l == "--quiet" || l == "--silent" || l == "--regexp-extended":
		case l == "--in-place":
			r.inPlace = true
		case l == "--expression":
			i++
			if i >= len(args) {
				return r, "sed's --expression has no script"
			}
			r.scripts = append(r.scripts, args[i])
		case strings.HasPrefix(l, "--"):
			return r, "sed's " + l + " is not one gnosh follows"
		default:
			for k := 1; k < len(l); k++ {
				switch l[k] {
				case 'n', 'E', 'r':
					continue
				case 'i':
					// Whatever follows the i in the same word is the suffix.
					r.inPlace, r.suffix = true, l[k+1:]
				case 'e':
					if k+1 < len(l) {
						r.scripts = append(r.scripts, litOf(l[k+1:]))
					} else if i++; i < len(args) {
						r.scripts = append(r.scripts, args[i])
					} else {
						return r, "sed's -e has no script"
					}
				default:
					return r, "sed's " + l + " is not one gnosh follows"
				}
				break
			}
		}
	}
	if len(r.scripts) == 0 {
		if len(operands) == 0 {
			return r, "sed has no script"
		}
		r.scripts, operands = operands[:1:1], operands[1:]
	}
	r.files = operands
	return r, ""
}

// sedBSD reads the same words as the sed of macOS and the BSDs does.
func sedBSD(args []word) (r sedRead) {
	i, hasE := 0, false
flags:
	for ; i < len(args); i++ {
		l := args[i].joined()
		switch {
		case l == "--":
			i++
			break flags
		case l == "-" || !strings.HasPrefix(l, "-"):
			break flags
		case strings.HasPrefix(l, "--"):
			r.fails = true // it has no long flags
			return r
		}
		for k := 1; k < len(l); k++ {
			switch l[k] {
			case 'n', 'E', 'r':
				continue
			case 'i', 'e':
				// Both take a value: the rest of the word, or the next one.
				val := litOf(l[k+1:])
				if k+1 >= len(l) {
					if i++; i >= len(args) {
						r.fails = true
						return r
					}
					val = args[i]
				}
				if l[k] == 'i' {
					r.inPlace, r.suffix = true, val.joined()
				} else {
					hasE = true
					r.scripts = append(r.scripts, val)
				}
			default:
				r.fails = true
				return r
			}
			break
		}
	}
	rest := args[i:]
	if !hasE {
		if len(rest) == 0 {
			r.fails = true
			return r
		}
		r.scripts, rest = rest[:1:1], rest[1:]
	}
	r.files = rest
	return r
}

// sedFacts judges sed. Without -i it reads; with -i it edits its files in
// place. Either way every script must be made only of commands that
// cannot run a program or write another file, and that holds for both
// ways of reading the words: what either sed would read or change is
// what the command reads or changes.
func sedFacts(f *cmdFacts, args []word) {
	for _, a := range args {
		if !a.static() || a.brace || a.glob && !a.quoted {
			f.why = "an argument to sed is computed when the command runs, or is a file name pattern: " + a.raw
			return
		}
	}
	gnu, bad := sedGNU(args)
	if bad != "" {
		f.why = bad
		return
	}
	for _, s := range gnu.scripts {
		if !sedScriptSafe(s.joined()) {
			f.why = "the sed script holds a command gnosh does not follow (only s, d, p, q and = are)"
			return
		}
	}
	if gnu.inPlace && gnu.suffix != "" {
		f.why = "sed -i with a suffix (" + gnu.suffix + ") keeps a copy of each file under another name, which gnosh does not follow"
		return
	}
	// A file with no name is no file: sed says so and goes on.
	named := func(ws []word) []word {
		var out []word
		for _, w := range ws {
			if w.joined() != "" {
				out = append(out, w)
			}
		}
		return out
	}
	gnu.files = named(gnu.files)
	var reads, writes, rewrites []word
	// GNU sed given an empty script ("sed -i '' 's/a/b/' f", which is how
	// the other sed is told to keep no backup) changes nothing, and puts
	// every file back as it was, the script among them if there is a
	// file of that name.
	idle := true
	for _, s := range gnu.scripts {
		idle = idle && s.joined() == ""
	}
	switch {
	case gnu.inPlace && idle:
		rewrites = gnu.files
	case gnu.inPlace:
		writes = gnu.files
	default:
		reads = gnu.files
	}
	inPlace := gnu.inPlace
	if bsd := sedBSD(args); !bsd.fails {
		for _, sc := range bsd.scripts {
			if sedScriptSafe(sc.joined()) {
				continue
			}
			switch does, file := bsdSedScript(sc.joined()); does {
			case sedStops:
				// That sed stops. The files it was handed still count:
				// a GNU sed that reads its arguments the same way
				// (POSIXLY_CORRECT) may know the command and go on.
			case sedIdle:
			case sedReadsFile:
				reads = append(reads, litOf(file))
			case sedWritesFile:
				writes = append(writes, litOf(file))
			default:
				f.why = "the sed of macOS and the BSDs would take " + sc.raw + " for its script, and gnosh cannot tell what that script does"
				return
			}
		}
		if !bsd.fails {
			inPlace = inPlace || bsd.inPlace
			for _, file := range named(bsd.files) {
				if bsd.inPlace {
					writes = moreWords(writes, []word{file})
					if bsd.suffix != "" {
						// Its backup: the file's name with the suffix.
						writes = append(writes, litOf(file.joined()+bsd.suffix))
					}
				} else {
					reads = moreWords(reads, []word{file})
				}
			}
		}
	}
	if !inPlace {
		if len(writes) > 0 {
			f.reads, f.writes = reads, writes
			f.why = "the sed of macOS and the BSDs would read one of these words as a script that writes a file"
			return
		}
		f.reads, f.readOnly = reads, true
		return
	}
	if len(writes) == 0 && len(rewrites) == 0 {
		f.why = "sed -i names no file"
		return
	}
	f.reads, f.writes, f.rewrites, f.fsEdit = reads, writes, rewrites, true
}

// What a script does when the sed of macOS and the BSDs is handed a word
// that was meant for a file name.
const (
	// sedUnknown: it may do anything.
	sedUnknown = iota
	// sedStops: sed stops with an error before it opens or changes a thing.
	sedStops
	// sedIdle: it runs and touches no file of its own.
	sedIdle
	// sedReadsFile and sedWritesFile: it reads or writes the file named.
	sedReadsFile
	sedWritesFile
)

// bsdSedScript says what the sed of macOS and the BSDs does with a
// script, which here is a word the line's author meant for a file name
// ("sed -i s/a/b/ notes.txt" hands it notes.txt for a script). Nearly
// every file name is no script at all, and sed stops at its first
// letter. The ones that matter are those that are: "w out" and "r in"
// name a file of their own. It answers sedUnknown whenever it is not
// sure.
func bsdSedScript(s string) (does int, file string) {
	if strings.ContainsAny(s, "\n") {
		return sedUnknown, "" // more than one line: more than one command
	}
	blanks := func(s string) string { return strings.TrimLeft(s, " \t") }
	p := blanks(s)
	if p == "" || p[0] == '#' {
		return sedIdle, ""
	}
	// One address, or two with a comma between them.
	for n := 0; n < 2 && p != ""; n++ {
		switch c := p[0]; {
		case c >= '0' && c <= '9':
			for p != "" && p[0] >= '0' && p[0] <= '9' {
				p = p[1:]
			}
		case c == '$':
			p = p[1:]
		case c == '/':
			end, how := sedDelim(p, 1, '/')
			switch how {
			case delimNone:
				return sedStops, "" // a pattern that never ends
			case delimDiffer:
				return sedUnknown, ""
			}
			p = p[end+1:]
			if strings.HasPrefix(p, "I") {
				// Some of these seds take an I here, to match without
				// regard to case. Only when the script stops either way is
				// the answer known.
				if does, _ := bsdSedScript("1" + p[1:]); does != sedStops {
					return sedUnknown, ""
				}
			}
		case c == '\\':
			return sedUnknown, ""
		default:
			n = 2
			continue
		}
		p = blanks(p)
		if n == 0 && strings.HasPrefix(p, ",") {
			p = blanks(p[1:])
			continue
		}
		break
	}
	if p = blanks(p); strings.HasPrefix(p, "!") {
		p = blanks(p[1:])
	}
	if p == "" {
		return sedStops, "" // an address and no command
	}
	c, rest := p[0], p[1:]
	switch c {
	case '+', '~', ',', '!':
		// A way of writing an address that is not followed here.
		return sedUnknown, ""
	case 'a', 'c', 'i':
		// Text to the end of the line, or an error.
		return sedIdle, ""
	case 'b', 't', ':', 'y':
		// A label or two strings, and then perhaps another command.
		if strings.ContainsAny(rest, ";}") {
			return sedUnknown, ""
		}
		return sedIdle, ""
	case 'd', 'D', 'g', 'G', 'h', 'H', 'l', 'n', 'N', 'p', 'P', 'q', 'x', '=':
		switch {
		case blanks(rest) == "":
			return sedIdle, ""
		case strings.ContainsAny(rest, ";}"):
			return sedUnknown, "" // another command may follow
		case (c == 'q' || c == 'l') && isDigits(blanks(rest)):
			// GNU sed takes a number here (which a GNU sed told to keep
			// to POSIX still does): it runs, where the others stop.
			return sedIdle, ""
		}
		return sedStops, "" // text after a command that takes none
	case 'R', 'W':
		// GNU's own, which a GNU sed told to read its arguments the
		// POSIX way (POSIXLY_CORRECT) still knows: a file read, a file
		// written.
		file := blanks(rest)
		switch {
		case file == "":
			return sedStops, ""
		case c == 'R':
			return sedReadsFile, file
		}
		return sedWritesFile, file
	case 'e', 'F', 'z', 'Q', 'v', 'T', 'L':
		// More of GNU's own: e runs a command.
		return sedUnknown, ""
	case 'r', 'w':
		file := blanks(rest)
		switch {
		case file == "":
			return sedStops, ""
		case c == 'r':
			return sedReadsFile, file
		}
		return sedWritesFile, file
	case 's':
		if rest == "" || rest[0] == '\\' {
			return sedStops, ""
		}
		d := rest[0]
		mid, how := sedDelim(rest, 1, d)
		switch how {
		case delimNone:
			return sedStops, ""
		case delimDiffer:
			return sedUnknown, ""
		}
		end := closing(rest, mid+1, d)
		if end < 0 {
			return sedStops, ""
		}
		flags := rest[end+1:]
		if strings.ContainsAny(flags, "w;}") {
			return sedUnknown, ""
		}
		for i := 0; i < len(flags); i++ {
			if strings.IndexByte("gpiImMe0123456789 \t", flags[i]) < 0 {
				return sedStops, "" // a flag no sed has
			}
		}
		if strings.Trim(flags, "gpiI0123456789 \t") == "" {
			return sedIdle, ""
		}
		return sedUnknown, ""
	case '{', '}', ';':
		return sedUnknown, ""
	}
	// A letter that is no command of this sed: it stops there.
	return sedStops, ""
}

// How the two families of sed agree on where a pattern ends.
const (
	// delimSame: both end it at the index returned.
	delimSame = iota
	// delimNone: neither finds an end.
	delimNone
	// delimDiffer: they end it in different places.
	delimDiffer
)

// sedDelim finds the delimiter d that ends the pattern starting at s[i].
// GNU sed ends a pattern at the first d that has no backslash before it.
// The sed of macOS and the BSDs also keeps a d that stands inside a
// bracket expression: to it "s/[/]/x/" replaces a slash, and to GNU sed
// the pattern ends at the second slash. A script the two would cut in
// different places is not one this package follows.
func sedDelim(s string, i int, d byte) (end int, how int) {
	gnu := closing(s, i, d)
	bsd := -1
	for j := i; j < len(s); j++ {
		switch {
		case s[j] == '\\':
			j++
		case s[j] == '[' && d != '[':
			// To the ] that closes the brackets: a ] right after the [ (or
			// after [^) is a member, and so is all of [:name:], [.x.] and
			// [=x=].
			k := j + 1
			if k < len(s) && s[k] == '^' {
				k++
			}
			if k < len(s) && s[k] == ']' {
				k++
			}
			for k < len(s) && s[k] != ']' {
				if s[k] == '[' && k+1 < len(s) && strings.IndexByte(".:=", s[k+1]) >= 0 {
					if e := strings.Index(s[k+2:], string(s[k+1])+"]"); e >= 0 {
						k += e + 3
					}
				}
				k++
			}
			if k >= len(s) {
				// Brackets that never close: this sed stops with an error.
				j = len(s)
				continue
			}
			j = k
		case s[j] == d:
			bsd = j
		}
		if bsd >= 0 {
			break
		}
	}
	switch {
	case gnu < 0 && bsd < 0:
		return -1, delimNone
	case gnu != bsd:
		return -1, delimDiffer
	}
	return gnu, delimSame
}

// sedScriptSafe reports whether a sed script is a list of the commands
// s, d, p, q and =, each with an optional address, and none with a flag
// that runs a program (e) or writes a file (w).
func sedScriptSafe(s string) bool {
	for _, cmd := range strings.FieldsFunc(s, func(r rune) bool { return r == ';' || r == '\n' }) {
		c := strings.TrimSpace(cmd)
		// An address: numbers, $, commas, steps, or /regex/ pairs.
		for c != "" {
			switch {
			case c[0] >= '0' && c[0] <= '9', c[0] == '$', c[0] == ',', c[0] == '~', c[0] == '+', c[0] == ' ', c[0] == '!':
				c = c[1:]
				continue
			case c[0] == '/':
				end, how := sedDelim(c, 1, '/')
				if how != delimSame {
					return false
				}
				c = c[end+1:]
				continue
			}
			break
		}
		switch {
		case c == "d" || c == "p" || c == "q" || c == "=":
		case strings.HasPrefix(c, "s") && len(c) > 3:
			d := c[1]
			if isNameChar(d) || d == ' ' || d == '\\' {
				return false
			}
			mid, how := sedDelim(c, 2, d)
			if how != delimSame {
				return false
			}
			end := closing(c, mid+1, d)
			if end < 0 {
				return false
			}
			for _, fl := range c[end+1:] {
				if !(fl == 'g' || fl == 'i' || fl == 'I' || fl == 'p' || fl >= '0' && fl <= '9') {
					return false
				}
			}
		default:
			return false
		}
	}
	return true
}

// closing returns the index of the next unescaped d in s at or after i.
func closing(s string, i int, d byte) int {
	for ; i < len(s); i++ {
		if s[i] == '\\' {
			i++
			continue
		}
		if s[i] == d {
			return i
		}
	}
	return -1
}

// fsFacts judges the file commands acceptEdits lets run: mkdir, touch,
// rm, rmdir, mv and cp, with only their everyday flags.
func fsFacts(f *cmdFacts, args []word) {
	// cp -r is left out on purpose: on macOS and the BSDs it follows the
	// links inside the directory it copies, which brings whatever they
	// lead to into the project. -R and -a copy a link as a link everywhere.
	flags := map[string]string{
		"mkdir": "pv", "touch": "", "rm": "rRfvd", "rmdir": "v", "mv": "fnv", "cp": "fnvRpa",
	}[f.name]
	// late are the flags written after the first file. To the tools of
	// macOS and the BSDs they are files too: "cp a b -R" copies a and b
	// into a directory named -R there.
	var ops, late []word
	rest := false
	for _, a := range args {
		if !a.static() || a.brace {
			f.why = "an argument is computed when the command runs: " + a.raw
			return
		}
		l := a.joined()
		if rest || !strings.HasPrefix(l, "-") || a.tilde || l == "-" {
			ops = append(ops, a)
			continue
		}
		if len(ops) > 0 {
			late = append(late, a)
		}
		if l == "--" {
			rest = true
			continue
		}
		for _, c := range l[1:] {
			if !strings.ContainsRune(flags, c) || strings.HasPrefix(l, "--") {
				f.why = f.name + "'s " + l + " is not one of its everyday flags"
				return
			}
			if f.name == "cp" && (c == 'R' || c == 'a') {
				f.tree = true
			}
		}
	}
	if len(ops) == 0 || (f.name == "mv" || f.name == "cp") && len(ops) < 2 {
		f.why = f.name + " names too few files"
		return
	}
	if f.name == "touch" {
		for _, o := range ops {
			if o.glob {
				f.why = "touch with a file name pattern"
				return
			}
		}
	}
	f.writes = ops
	if f.name == "cp" {
		f.reads, f.writes = ops[:len(ops)-1:len(ops)-1], ops[len(ops)-1:]
	}
	f.writes = append(f.writes[:len(f.writes):len(f.writes)], late...)
	if f.name == "cp" || f.name == "mv" {
		_, _, f.lands = copiedInto(f.name, args)
	}
	f.fsEdit = true
}
