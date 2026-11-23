package permission

import (
	"regexp"
	"runtime"
	"strings"
)

// This file is what git may do in a git work tree of a session's own: read,
// or change the work tree's own branch, index and files in ways gnosh
// knows, and nothing else. A work tree shares its branches, tags and other
// refs, its git settings and hooks, and its list of work trees with the
// checkout it was made from, so git run there can move the checkout's
// branch, or put in a setting or a hook that runs a command there. git
// itself keeps a branch another work tree has out from checkout and branch
// -f, and from little else. Claude Code 2.1.326 refuses the like for its
// sub-agents in work trees.
//
// So the subcommands are an allowlist, each with the flags it may take
// (gitTreeWrites), as the read-only table is (gitRead): a subcommand, a
// flag or a setting gnosh does not know is refused, and so are the
// person's aliases, which may be any of them. And git runs only against
// the work tree's own repository, which the session tells from the disk
// (TreeGit.Repo): a .git file can name another. What the text of a line
// cannot show stays out of reach: a git copied under another name, a
// script that runs git, a remote of the person's whose address is the
// checkout. Two reviews (17 November) found the ways around the first two
// versions; their findings are the test's lists.

// TreeGit is what SharedGitChange asks of the session about its work tree.
type TreeGit struct {
	// Repo says why git run in the folder dir would open a repository
	// other than the work tree's own (another's .git, a .git file that
	// names another git folder, a folder that is not there yet), or ""
	// when it opens the work tree's. Nil leaves that unchecked.
	Repo func(dir string) string
	// Remote reports whether the work tree's repository has a remote of
	// the name. Nil knows none.
	Remote func(name string) bool
	// Stashes reports whether the list of stashes, which the checkout
	// keeps too, holds any: with none, a stash popped or dropped can only
	// be the line's own ("git stash && go test; git stash pop"). Nil says
	// it does.
	Stashes func() bool
}

// SharedGitChange says why git, as the command line runs it, may change
// what a work tree shares with the checkout it was made from, as words for
// the model ("git update-ref is not one gnosh lets run in a work tree of
// your own"); "" when every git run in it only reads, or changes the work
// tree's own branch and files in a way gnosh knows, in the work tree's own
// repository. dir is the folder the line starts in.
func (e *Engine) SharedGitChange(dir, command string, tree TreeGit) string {
	e.mu.RLock()
	defer e.mu.RUnlock()
	at := *e
	if strings.HasPrefix(dir, "/") {
		at.cur = cleanPath(dir)
	}
	v := at.viewBash(command)
	if why := unreadable(v); why != "" {
		return "gnosh cannot tell what git in it does: " + why
	}
	dirs, lost := at.lineDirsKnown(v)
	lost = lost || v.dirsCapped
	envWhy := gitEnvWhy(v, command)
	for _, u := range v.units {
		ws := u.cmd.words
		if len(ws) == 0 || !ws[0].static() {
			continue // a name only known when it runs: GitPlaces refuses those
		}
		if (ws[0].glob || ws[0].brace) && ws[0].raw != "[" && ws[0].raw != "[[" {
			return "the program " + ws[0].raw + " is named by a pattern, which may make it git, and gnosh cannot tell what it would change"
		}
		name := progKey(baseName(ws[0].joined()))
		args := ws[1:]
		switch {
		case name == "git":
		case strings.HasPrefix(name, "git-") && len(name) > len("git-"):
			// git's own programs, run as git runs them: git-update-ref.
			args = append([]word{litOf(name[len("git-"):])}, args...)
		default:
			continue
		}
		if envWhy != "" {
			return envWhy
		}
		if why := at.treeGit(args, dirs, lost, tree); why != "" {
			return why
		}
	}
	return ""
}

// gitTreeEnv are the GIT_ variables a line may set where git runs in a
// work tree of its own.
var gitTreeEnv = map[string]bool{
	"GIT_TERMINAL_PROMPT": true, "GIT_OPTIONAL_LOCKS": true, "GIT_ADVICE": true,
	"GIT_AUTHOR_NAME": true, "GIT_AUTHOR_EMAIL": true, "GIT_AUTHOR_DATE": true,
	"GIT_COMMITTER_NAME": true, "GIT_COMMITTER_EMAIL": true, "GIT_COMMITTER_DATE": true,
	"GIT_MERGE_AUTOEDIT": true,
}

// gitTreeQuiet are the variables naming a program git runs (an editor, a
// pager) that a line may set, to a program that does nothing.
var gitTreeQuiet = map[string]bool{
	"GIT_EDITOR": true, "GIT_SEQUENCE_EDITOR": true, "EDITOR": true, "VISUAL": true,
	"GIT_PAGER": true, "PAGER": true,
}

// quietPrograms are the programs gitTreeQuiet's variables may name.
var quietPrograms = map[string]bool{"true": true, ":": true, "cat": true}

// varSetters are the commands that give a variable named by a word of
// theirs a value: read HOME, printf -v HOME, declare -n r=HOME.
var varSetters = map[string]bool{
	"read": true, "mapfile": true, "readarray": true, "printf": true, "getopts": true,
	"declare": true, "typeset": true, "local": true, "export": true, "readonly": true,
}

// loopSets finds the variable a for or select loop sets: for HOME in x.
var loopSets = regexp.MustCompile(`(?:^|[\s;&|(!{])(?:for|select)\s+([A-Za-z_][A-Za-z0-9_]*)`)

// defaultSet finds a ${NAME:=word} or ${NAME=word}, which sets NAME.
var defaultSet = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*):?=`)

// gitEnvWhy says why the variables the line sets keep gnosh from telling
// what its git does, or "": settings given through the environment
// (GIT_CONFIG_COUNT and its kin; another HOME or XDG_CONFIG_HOME, whose
// settings may name aliases or remotes), a GIT_ variable gnosh does not
// know, a program git runs, another PATH, which finds another git, or a
// variable set by a name only known when the line runs. A variable is set
// in more ways than an assignment: a VAR=value word of any command (env,
// sudo, time), read, printf -v, declare and its kin, a nameref, a loop's
// variable, ${VAR:=x}. text is the line as written.
func gitEnvWhy(v *bashView, text string) string {
	check := func(name, value string, known bool) string {
		switch {
		case name == "HOME" || name == "XDG_CONFIG_HOME" || name == "PATH":
			return "the line sets " + name + ", which changes the git that runs or the settings it reads"
		case gitTreeQuiet[name]:
			if !known || !quietPrograms[strings.TrimSpace(value)] {
				return "the line sets " + name + ", a program git runs: in a work tree of your own it may only be true, : or cat"
			}
		case strings.HasPrefix(name, "GIT_") && !gitTreeEnv[name]:
			return "the line sets " + name + ", which changes what git does in ways gnosh does not follow in a work tree of your own"
		}
		return ""
	}
	for name, lv := range v.vars {
		value, known := "", false
		if lv != nil && len(lv.vals) == 1 {
			value, known = lv.vals[0].text, true
		}
		if why := check(name, value, known); why != "" {
			return why
		}
	}
	for _, m := range loopSets.FindAllStringSubmatch(text, -1) {
		if why := check(m[1], "", false); why != "" {
			return why
		}
	}
	for _, m := range defaultSet.FindAllStringSubmatch(text, -1) {
		if why := check(m[1], "", false); why != "" {
			return why
		}
	}
	for _, u := range v.units {
		for _, a := range u.cmd.assigns {
			_, value, _ := strings.Cut(a.w.joined(), "=")
			if why := check(a.name, value, a.w.static()); why != "" {
				return why
			}
		}
		ws := u.cmd.words
		setter := len(ws) > 0 && ws[0].static() && varSetters[progKey(baseName(ws[0].joined()))]
		for i, w := range ws {
			val := w.raw
			if w.static() {
				val = w.joined()
			}
			// NAME=value, in the words of any command: env, sudo, time,
			// export, declare.
			if eq := strings.IndexByte(val, '='); eq > 0 {
				name := strings.TrimSuffix(val[:eq], "+")
				switch {
				case isVarName(name):
					if why := check(name, val[eq+1:], w.static()); why != "" {
						return why
					}
					if setter && isVarName(val[eq+1:]) {
						// declare -n r=HOME: r stands for HOME.
						if why := check(val[eq+1:], "", false); why != "" {
							return why
						}
					}
				case setter && strings.ContainsAny(name, "$`"):
					return "the line sets a variable whose name is only known when it runs (" + w.raw + "), which may be one of git's"
				}
				continue
			}
			if !setter || i == 0 {
				continue
			}
			// read HOME, printf -v HOME, printf -vHOME, mapfile HOME.
			if name := strings.TrimPrefix(val, "-v"); isVarName(name) {
				if why := check(name, "", false); why != "" {
					return why
				}
			}
			if !w.static() && !strings.HasPrefix(w.raw, "-") {
				return "the line hands " + ws[0].raw + " a variable's name only known when it runs (" + w.raw + "), which may be one of git's"
			}
		}
	}
	return ""
}

// gitTreeGlobal are the flags git may be given before its subcommand in a
// work tree of its own; -C is read where it stands.
var gitTreeGlobal = map[string]bool{
	"--no-pager": true, "-P": true, "--no-optional-locks": true, "--no-advice": true,
	"-p": true, "--paginate": true, "--literal-pathspecs": true, "--glob-pathspecs": true,
	"--noglob-pathspecs": true, "--icase-pathspecs": true,
}

// gitTreeSays are the flags git may be given in place of a subcommand,
// which only print: its version, its help, where it keeps its programs.
var gitTreeSays = map[string]bool{
	"--version": true, "-v": true, "--help": true, "-h": true, "--exec-path": true,
	"--html-path": true, "--man-path": true, "--info-path": true,
}

// gitTreeSettings are the settings -c may give git in a work tree of its
// own: a key, or a section that ends in a dot. Others may run a command,
// name an alias, or point git at another repository or remote.
var gitTreeSettings = []string{
	"user.name", "user.email", "color.", "advice.", "core.quotepath", "commit.gpgsign",
	"tag.gpgsign", "push.default", "pull.rebase", "pull.ff", "merge.ff", "init.defaultbranch",
	"log.", "diff.renames", "diff.noprefix", "status.", "column.", "gc.auto",
}

// treeGit is SharedGitChange for one git run, given its words after the
// program's name and the folders the line may be in (lost: not all known).
func (e *Engine) treeGit(args []word, dirs []string, lost bool, tree TreeGit) string {
	// Where git runs: the line's folders, or where each -C leads from
	// them, one after the other as git takes them.
	places := dirs
	i := 0
words:
	for ; i < len(args); i++ {
		a := args[i]
		if !a.static() || a.glob || a.brace {
			return "a word git reads before its subcommand (" + a.raw + ") is only known when the line runs, so gnosh cannot tell what it changes"
		}
		l := a.joined()
		switch {
		case gitTreeGlobal[l]:
		case gitTreeSays[l]:
			return ""
		case l == "-C":
			if i++; i >= len(args) {
				break words
			}
			c := args[i]
			if !c.static() || c.glob || c.brace {
				return "git -C " + c.raw + " goes to a folder only known when the line runs, so gnosh cannot tell which repository git opens there"
			}
			p := c.value(e.home)
			var next []string
			for _, d := range places {
				if strings.HasPrefix(p, "/") {
					next = append(next, cleanPath(p))
				} else {
					next = append(next, cleanPath(d+"/"+p))
				}
			}
			places = next
		case l == "-c":
			if i++; i >= len(args) {
				break words
			}
			if why := treeSetting(args[i]); why != "" {
				return why
			}
		case l == "--git-dir" || l == "--work-tree" || strings.HasPrefix(l, "--git-dir=") || strings.HasPrefix(l, "--work-tree="):
			return "git " + l + " points git at a repository or a work tree by name, and in a work tree of your own git runs in it as it is"
		case strings.HasPrefix(l, "-"):
			return "git's option " + l + ", before its subcommand, is not one gnosh lets git take in a work tree of your own"
		default:
			break words
		}
	}
	if i >= len(args) {
		return ""
	}
	if tree.Repo != nil {
		if lost {
			return "gnosh cannot tell which folder git runs in, nor so which repository it opens"
		}
		for _, p := range places {
			if why := tree.Repo(p); why != "" {
				return why + "; in a work tree of your own, git runs in its own repository"
			}
		}
	}
	subw := args[i]
	if !subw.static() || subw.glob || subw.brace {
		return "git's subcommand (" + subw.raw + ") is only known when the line runs, so gnosh cannot tell what it changes"
	}
	sub := subw.joined()
	rest := args[i+1:]
	// What only reads is fine: by the read-only table, or by being a
	// command that reads, with no flag that writes a file or starts a
	// program.
	var f cmdFacts
	gitFacts(&f, args[i:])
	if f.readOnly {
		return ""
	}
	if gitReads[sub] {
		if why := readsOnly(sub, rest); why != "" {
			return "git " + sub + ": " + why
		}
		return ""
	}
	if read, why := treeReads(sub, rest); read {
		return ""
	} else if why != "" {
		return "git " + sub + ": " + why
	}
	spec, ok := gitTreeWrites[sub]
	if !ok && sub == "config" {
		return "git config writes the repository's git settings, which the checkout reads too, and in a work tree of your own git leaves them alone: for a commit's author, give git -c user.name=... -c user.email=... commit, or commit --author"
	}
	if !ok {
		return "git " + sub + " is not one gnosh lets run in a work tree of your own, where git may only read or change your own branch, index and files (add, commit, reset, restore, rm, mv, checkout, switch, merge, rebase, cherry-pick, revert, stash push and apply, fetch, pull, push to a remote, branch and tag to make one, clean, apply, bisect, format-patch); an alias may stand for any command, so write the command out"
	}
	p, bad := treeArgs(rest, spec.args)
	if bad != "" {
		return "git " + sub + ": " + bad
	}
	if spec.check != nil {
		return spec.check(sub, p, tree)
	}
	return ""
}

// treeSetting says why a setting given with -c is refused in a work tree
// of its own, or "".
func treeSetting(w word) string {
	text := w.raw
	if w.static() {
		text = w.joined()
	} else {
		text = strings.NewReplacer(`"`, "", `'`, "").Replace(text)
	}
	key, _, _ := strings.Cut(text, "=")
	if !w.static() && strings.ContainsAny(key, "$`\\") {
		return "a setting git is given with -c (" + w.raw + ") has a name only known when the line runs"
	}
	key = strings.ToLower(key)
	for _, ok := range gitTreeSettings {
		if key == ok || strings.HasSuffix(ok, ".") && strings.HasPrefix(key, ok) {
			return ""
		}
	}
	return "git -c " + key + " is not a setting gnosh lets git take in a work tree of your own (a setting may run a command, name an alias or point git elsewhere)"
}

// gitReads are the subcommands that only read, whatever flags they take,
// but for those readsOnly finds.
var gitReads = map[string]bool{
	"status": true, "diff": true, "log": true, "show": true, "grep": true, "blame": true,
	"annotate": true, "rev-parse": true, "rev-list": true, "ls-files": true, "ls-tree": true,
	"describe": true, "shortlog": true, "cat-file": true, "show-ref": true, "for-each-ref": true,
	"merge-base": true, "name-rev": true, "whatchanged": true, "count-objects": true,
	"check-ignore": true, "check-attr": true, "var": true, "version": true, "diff-tree": true,
	"diff-index": true, "diff-files": true, "range-diff": true, "cherry": true, "show-branch": true,
	"verify-commit": true, "verify-tag": true, "help": true,
}

// readDanger are the long flags of the reading subcommands that write a
// file or start a program. git takes a start of a long flag that only one
// flag begins with, so a start of one of these counts too (--open for
// --open-files-in-pager).
var readDanger = []string{"--output", "--output-directory", "--open-files-in-pager", "--ext-diff", "--textconv", "--exec", "--upload-pack", "--receive-pack"}

// commitsOnly finds a command substitution of the git commands that print
// commits and nothing else, which no option can begin: $(git rev-list
// --all), $(git merge-base HEAD main). Unquoted, its words are commits.
var commitsOnly = regexp.MustCompile(`^\$\(git (rev-list|rev-parse|merge-base)( [-A-Za-z0-9_./~^@{}:=]+)*\)$`)

// readsOnly says why the words of a reading subcommand may write a file or
// start a program, or "": a flag of readDanger, or a start of one; git
// grep's -O, also among other letters (-nO); an unquoted word only known
// when the line runs, which may split into options. A quoted one is one
// word, the commit or the file it names.
func readsOnly(sub string, rest []word) string {
	for _, w := range rest {
		if !w.static() {
			if !w.quoted && !commitsOnly.MatchString(w.raw) {
				return "an unquoted word only known when the line runs (" + w.raw + ") may become several words, options among them: quote it"
			}
			continue
		}
		l := w.joined()
		switch {
		case l == "--" || l == "--end-of-options":
			return ""
		case strings.HasPrefix(l, "--"):
			name, _, _ := strings.Cut(l, "=")
			for _, d := range readDanger {
				if len(name) > len("--") && strings.HasPrefix(d, name) {
					return "its option " + name + " writes a file or starts a program"
				}
			}
		case sub == "grep" && strings.HasPrefix(l, "-") && strings.ContainsRune(l[1:], 'O'):
			return "its option -O starts a program"
		}
	}
	return ""
}

// treeReads reports whether a subcommand that may also write only reads,
// given rest, with why it may not when it is one of those that read only
// so: git stash show and list, git reflog showing, git config reading a
// setting or listing them, git remote listing or showing, git
// symbolic-ref reading, git ls-remote.
func treeReads(sub string, rest []word) (read bool, why string) {
	first := ""
	if len(rest) > 0 && rest[0].static() {
		first = rest[0].joined()
	}
	allStatic := true
	for _, w := range rest {
		allStatic = allStatic && w.static()
	}
	switch sub {
	case "stash":
		// (They take the flags of git log and git diff.)
		if first == "show" || first == "list" {
			why = readsOnly("log", rest[1:])
			return why == "", why
		}
	case "reflog":
		if first != "expire" && first != "delete" && first != "drop" {
			why = readsOnly("log", rest)
			return why == "", why
		}
	case "remote":
		switch {
		case !allStatic:
		case first == "" || first == "-v" || first == "--verbose":
			return len(rest) <= 1, ""
		case first == "show" || first == "get-url":
			return true, ""
		}
	case "config":
		return configReads(rest), ""
	case "symbolic-ref":
		if !allStatic {
			return false, ""
		}
		ops := 0
		for _, w := range rest {
			switch l := w.joined(); l {
			case "-q", "--quiet", "--short", "--no-recurse", "--recurse":
			default:
				if strings.HasPrefix(l, "-") {
					return false, ""
				}
				ops++
			}
		}
		return ops <= 1, ""
	case "ls-remote":
		why = readsOnly(sub, rest)
		for _, w := range rest {
			if l := w.lit(); l == "-u" || strings.HasPrefix(l, "--u") || strings.HasPrefix(l, "--e") {
				why = "its option " + l + " names a program to run"
			}
		}
		return why == "", why
	}
	return false, ""
}

// configReads reports whether git config, given rest, only reads a setting
// or lists them: an action that reads (--get, --get-regexp, --list, get,
// list), or a key alone.
func configReads(rest []word) bool {
	var ops []string
	reads := false
	for i := 0; i < len(rest); i++ {
		w := rest[i]
		if !w.static() {
			return false
		}
		l := w.joined()
		switch {
		case l == "-l" || l == "--list" || strings.HasPrefix(l, "--get"):
			reads = true
		case l == "-f" || l == "--file" || l == "--blob" || l == "--type" || l == "--default":
			i++ // its value
		case strings.HasPrefix(l, "--file=") || strings.HasPrefix(l, "--blob=") || strings.HasPrefix(l, "--type=") ||
			strings.HasPrefix(l, "--default=") || strings.HasPrefix(l, "--url="):
		case l == "--global" || l == "--system" || l == "--local" || l == "--worktree" || l == "--show-origin" ||
			l == "--show-scope" || l == "--name-only" || l == "-z" || l == "--null" || l == "--bool" || l == "--int" ||
			l == "--bool-or-int" || l == "--path" || l == "--expiry-date" || l == "--includes" || l == "--no-includes" ||
			l == "--all" || l == "--fixed-value" || l == "--regexp":
		case strings.HasPrefix(l, "-"):
			return false
		default:
			ops = append(ops, l)
		}
	}
	switch {
	case reads:
		return true
	case len(ops) > 0 && (ops[0] == "get" || ops[0] == "list"):
		return true
	}
	return len(ops) == 1
}

// treeWrite is a subcommand git may run in a work tree of its own: the
// flags it may take, written as the read-only table writes them, and a
// check of what it was given.
type treeWrite struct {
	args  roSpec
	check func(sub string, p treeParsed, tree TreeGit) string
}

// strategies are the merge strategies git has itself: -s names a program
// git-merge-<name>, which may be anything.
var strategies = map[string]bool{"ort": true, "recursive": true, "resolve": true, "octopus": true, "ours": true, "subtree": true}

// gitTreeWrites are the subcommands that may change the work tree's own
// branch, index and files, with the flags they may take. Left out: those
// that start a program (rebase -x, bisect run, an editor), write a ref by
// name, reset a branch or take one another work tree has out (branch -f,
// checkout -B), drop stashes, write settings (config, remote, branch -u,
// push -u), or the shared files of a partial or a shallow clone (fetch
// --filter, --depth).
var gitTreeWrites = map[string]treeWrite{
	"add": {args: roSpec{short: "AuNfvn",
		long: "--all --update --intent-to-add --force --verbose --dry-run --ignore-errors --no-all --ignore-removal --renormalize --sparse --chmod= --pathspec-from-file="}},
	"commit": {args: roSpec{noGlob: true, short: "aqvsnoiz", valued: "mFC", optional: "uS",
		long: "--all --quiet --verbose --amend --no-edit --allow-empty --allow-empty-message --signoff --no-signoff --no-verify --verify --only --include --no-gpg-sign --reset-author --no-post-rewrite --dry-run --short --branch --porcelain --long --null --status --no-status --untracked-files --untracked-files= --gpg-sign --gpg-sign= --message= --file= --author= --date= --fixup= --squash= --cleanup= --trailer= --reuse-message= --pathspec-from-file="}},
	"reset": {args: roSpec{noGlob: true, short: "qN",
		long: "--soft --mixed --hard --keep --merge --quiet --intent-to-add --no-refresh --refresh --pathspec-from-file="}},
	"restore": {args: roSpec{short: "SWqm", valued: "s",
		long: "--staged --worktree --quiet --ours --theirs --merge --overlay --no-overlay --ignore-unmerged --progress --no-progress --source= --conflict= --pathspec-from-file="}},
	"rm": {args: roSpec{short: "rfqn",
		long: "--force --cached --quiet --dry-run --ignore-unmatch --sparse --pathspec-from-file="}},
	"mv": {args: roSpec{noGlob: true, short: "fknv", long: "--force --dry-run --verbose --sparse"}},
	"checkout": {args: roSpec{noGlob: true, short: "qfm", valued: "b", optional: "t",
		long: "--quiet --force --detach --ours --theirs --merge --no-track --track --track= --overlay --no-overlay --ignore-skip-worktree-bits --progress --no-progress --guess --no-guess --orphan= --conflict= --pathspec-from-file="},
		check: func(sub string, p treeParsed, _ TreeGit) string {
			if why := newBranch(p, "-b", "--orphan"); why != "" {
				return why
			}
			// A branch, a commit or a file before "--": on a disk that
			// does not tell case apart, Main is main.
			for _, w := range p.ops[:p.plain] {
				if why := caseName(w, "git checkout "+w.lit()); why != "" {
					return why + " (put -- before the names of files)"
				}
			}
			return ""
		}},
	"switch": {args: roSpec{noGlob: true, short: "dqfm", valued: "c", optional: "t",
		long: "--detach --quiet --force --discard-changes --merge --no-track --track --track= --guess --no-guess --progress --no-progress --create= --orphan= --conflict="},
		check: func(sub string, p treeParsed, _ TreeGit) string {
			if why := newBranch(p, "-c", "--create", "--orphan"); why != "" {
				return why
			}
			for _, w := range p.ops {
				if why := caseName(w, "git switch "+w.lit()); why != "" {
					return why
				}
			}
			return ""
		}},
	"merge": {args: roSpec{noGlob: true, short: "qvn", valued: "mFsX", optional: "S",
		long: "--no-ff --ff --ff-only --squash --no-squash --no-edit --abort --continue --quit --quiet --verbose --stat --no-stat --no-commit --commit --allow-unrelated-histories --no-verify --verify --autostash --no-autostash --signoff --no-signoff --log --log= --no-log --rerere-autoupdate --no-rerere-autoupdate --progress --no-progress --no-gpg-sign --gpg-sign --gpg-sign= --file= --strategy= --strategy-option= --cleanup= --into-name="},
		check: knownStrategy},
	"rebase": {args: roSpec{noGlob: true, short: "qvmnfr", valued: "sXC", optional: "S",
		long: "--continue --abort --skip --quit --quiet --verbose --keep-base --root --merge --autostash --no-autostash --autosquash --no-autosquash --fork-point --no-fork-point --committer-date-is-author-date --ignore-date --reset-author-date --signoff --no-verify --verify --no-update-refs --stat --no-stat --keep-empty --no-keep-empty --reapply-cherry-picks --no-reapply-cherry-picks --apply --rerere-autoupdate --no-rerere-autoupdate --no-gpg-sign --gpg-sign --gpg-sign= --ignore-whitespace --force-rebase --no-ff --rebase-merges --rebase-merges= --onto= --strategy= --strategy-option= --empty= --whitespace="},
		check: func(sub string, p treeParsed, tree TreeGit) string {
			_, root := p.flags["--root"]
			if len(p.ops) > 1 || root && len(p.ops) > 0 {
				return "git rebase given a branch to rebase checks that branch out and rewrites it, and it may be one the checkout uses: rebase your own branch, which is out"
			}
			return knownStrategy(sub, p, tree)
		}},
	"cherry-pick": {args: roSpec{noGlob: true, short: "nxs", valued: "mX", optional: "S",
		long: "--no-commit --signoff --ff --allow-empty --allow-empty-message --keep-redundant-commits --continue --abort --skip --quit --rerere-autoupdate --no-rerere-autoupdate --no-gpg-sign --gpg-sign --gpg-sign= --mainline= --strategy= --strategy-option= --empty="},
		check: knownStrategy},
	"revert": {args: roSpec{noGlob: true, short: "ns", valued: "mX", optional: "S",
		long: "--no-edit --no-commit --signoff --continue --abort --skip --quit --rerere-autoupdate --no-rerere-autoupdate --no-gpg-sign --gpg-sign --gpg-sign= --reference --mainline= --strategy= --strategy-option="},
		check: knownStrategy},
	"stash": {args: roSpec{noGlob: true, short: "uakqSp", valued: "m",
		long: "--include-untracked --no-include-untracked --all --keep-index --no-keep-index --quiet --staged --index --message= --pathspec-from-file="},
		check: func(_ string, p treeParsed, tree TreeGit) string {
			if len(p.ops) == 0 {
				return ""
			}
			switch p.ops[0].lit() {
			case "push", "save", "apply":
				return ""
			case "pop", "drop":
				// With no stash of the person's, the one taken is the
				// line's own; one named by its place may be anyone's.
				if len(p.ops) == 1 && tree.Stashes != nil && !tree.Stashes() {
					return ""
				}
			}
			return "git stash " + p.ops[0].raw + " changes the list of stashes, which the checkout keeps too, and it holds stashes now: git stash apply takes one back and leaves the list as it is"
		}},
	"fetch": {args: roSpec{noGlob: true, short: "pqv46k", valued: "jo",
		long: "--all --prune --quiet --verbose --tags --no-tags --dry-run --multiple --no-recurse-submodules --recurse-submodules --recurse-submodules= --progress --no-progress --ipv4 --ipv6 --atomic --no-write-fetch-head --write-fetch-head --auto-maintenance --no-auto-maintenance --auto-gc --no-auto-gc --keep --show-forced-updates --no-show-forced-updates --jobs= --negotiation-tip= --server-option="},
		check: fetchRefspecs},
	"pull": {args: roSpec{noGlob: true, short: "pqv46nr", valued: "jsX",
		long: "--all --prune --quiet --verbose --tags --no-tags --dry-run --no-recurse-submodules --progress --no-progress --ipv4 --ipv6 --rebase --rebase= --no-rebase --ff --no-ff --ff-only --squash --no-squash --commit --no-commit --no-edit --autostash --no-autostash --stat --no-stat --signoff --no-signoff --no-verify --verify --allow-unrelated-histories --log --no-log --jobs= --strategy= --strategy-option= --server-option="},
		check: func(sub string, p treeParsed, tree TreeGit) string {
			if why := knownStrategy(sub, p, tree); why != "" {
				return why
			}
			return fetchRefspecs(sub, p, tree)
		}},
	"push": {args: roSpec{noGlob: true, short: "ufnqvd46", valued: "o",
		long: "--set-upstream --force --dry-run --quiet --verbose --porcelain --delete --tags --follow-tags --no-follow-tags --no-verify --verify --atomic --no-atomic --all --branches --mirror --prune --progress --no-progress --thin --no-thin --force-with-lease --force-with-lease= --no-force-with-lease --force-if-includes --no-force-if-includes --signed --signed= --no-signed --ipv4 --ipv6 --recurse-submodules= --push-option="},
		check: func(_ string, p treeParsed, tree TreeGit) string {
			if len(p.ops) == 0 {
				return ""
			}
			if hasFlag(p, "-u") || hasFlag(p, "--set-upstream") {
				// -u sets the upstream of each branch it pushes, in the
				// settings the checkout reads: only the branch that is out.
				onlyHead := !hasFlag(p, "--all") && !hasFlag(p, "--branches") && !hasFlag(p, "--mirror")
				for _, w := range p.ops[1:] {
					onlyHead = onlyHead && w.static() && (w.joined() == "HEAD" || strings.HasPrefix(w.joined(), "HEAD:"))
				}
				if !onlyHead {
					return "git push -u sets the upstream of every branch it pushes, in the settings the checkout reads too: push -u HEAD alone (git push -u origin HEAD)"
				}
			}
			to := p.ops[0]
			switch {
			case !to.static():
				return "git push to " + to.raw + ", a place only known when the line runs, may push into a repository on this machine"
			case remoteURL(to.joined()):
				return ""
			case remoteName(to.joined()) && tree.Remote != nil && tree.Remote(to.joined()):
				return ""
			}
			return "git push to " + to.raw + " may push into a repository on this machine, the one your work tree shares among them: push to a remote of the repository by its name (git remote -v lists them), or to an address"
		}},
	"branch": {args: roSpec{noGlob: true, short: "arlvqi", optional: "t",
		long: "--all --remotes --list --verbose --quiet --show-current --ignore-case --no-color --color --color= --no-column --column --column= --no-track --track --track= --abbrev --abbrev= --omit-empty --contains= --no-contains= --merged= --no-merged= --points-at= --sort= --format="},
		check: func(_ string, p treeParsed, _ TreeGit) string {
			// (git branch -v with a name makes the branch: -v alone does
			// not make a listing of it.)
			for _, f := range []string{"-a", "-r", "-l", "--all", "--remotes", "--list", "--show-current", "--contains", "--no-contains", "--merged", "--no-merged", "--points-at"} {
				if _, ok := p.flags[f]; ok {
					return ""
				}
			}
			if len(p.ops) == 0 {
				return ""
			}
			return caseName(p.ops[0], "the new branch "+p.ops[0].lit())
		}},
	"tag": {args: roSpec{noGlob: true, short: "aslvi", valued: "mFu", optional: "n",
		long: "--annotate --sign --list --verify --ignore-case --no-column --column --column= --color --color= --create-reflog --no-sign --message= --file= --local-user= --contains= --no-contains= --merged= --no-merged= --points-at= --sort= --format= --cleanup= --trailer="},
		check: func(_ string, p treeParsed, _ TreeGit) string {
			for _, f := range []string{"-l", "--list", "-v", "--verify", "--contains", "--no-contains", "--merged", "--no-merged", "--points-at"} {
				if _, ok := p.flags[f]; ok {
					return ""
				}
			}
			if len(p.ops) == 0 {
				return ""
			}
			return caseName(p.ops[0], "the new tag "+p.ops[0].lit())
		}},
	"clean": {args: roSpec{noGlob: true, short: "nfdxXq", valued: "e", long: "--dry-run --force --quiet --exclude="}},
	"apply": {args: roSpec{noGlob: true, short: "Rv3qN", optional: "pC",
		long: "--check --stat --numstat --summary --index --cached --reverse --3way --reject --verbose --quiet --recount --allow-empty --no-add --binary --unidiff-zero --intent-to-add --whitespace= --exclude= --include= --directory="}},
	// bisect's refs are the work tree's own; bisect run starts a program.
	"bisect": {args: roSpec{noGlob: true, long: "--term-old= --term-new= --term-good= --term-bad= --no-checkout --first-parent"},
		check: func(_ string, p treeParsed, _ TreeGit) string {
			if len(p.ops) == 0 {
				return ""
			}
			switch p.ops[0].lit() {
			case "start", "bad", "good", "new", "old", "skip", "reset", "log", "terms":
				return ""
			}
			return "git bisect " + p.ops[0].raw + " is not one gnosh lets run in a work tree of your own (bisect run starts a program)"
		}},
	"submodule": {args: roSpec{noGlob: true, short: "q", long: "--quiet --cached --recursive"},
		check: func(_ string, p treeParsed, _ TreeGit) string {
			if len(p.ops) > 0 && (p.ops[0].lit() == "status" || p.ops[0].lit() == "summary") {
				return ""
			}
			return "git submodule changes the repository's shared files and settings (foreach runs a command): in a work tree of your own only status and summary run"
		}},
	// format-patch writes the patches into the folder it runs in.
	"format-patch": {args: roSpec{noGlob: true, short: "nNskp", valued: "v", digits: true,
		long: "--stdout --cover-letter --no-cover-letter --numbered --no-numbered --signoff --no-signature --rfc --thread --no-thread --keep-subject --root --zero-commit --no-stat --stat --no-binary --subject-prefix= --start-number= --base= --reroll-count= --suffix="}},
}

// hasFlag reports whether the flag was given.
func hasFlag(p treeParsed, name string) bool {
	_, ok := p.flags[name]
	return ok
}

// knownStrategy refuses a merge strategy git does not have itself: it
// names a program, git-merge-<name>. (cherry-pick's and revert's -s is
// their --signoff.)
func knownStrategy(sub string, p treeParsed, _ TreeGit) string {
	names := []string{"-s", "--strategy"}
	if sub == "cherry-pick" || sub == "revert" {
		names = names[1:]
	}
	for _, f := range names {
		if v, ok := p.flags[f]; ok && !strategies[v] {
			return "git " + sub + "'s strategy " + v + " is not one of git's own (ort, recursive, resolve, octopus, ours, subtree): another names a program"
		}
	}
	return ""
}

// newBranch is the name check of the flags given that make a branch.
func newBranch(p treeParsed, names ...string) string {
	for _, f := range names {
		if v, ok := p.flags[f]; ok {
			if why := caseName(litOf(v), "the new branch "+v); why != "" {
				return why
			}
		}
	}
	return ""
}

// foldsCase says this machine's disks, as a Mac's and a Windows machine's
// are by default, do not tell capital letters from small ones.
var foldsCase = runtime.GOOS == "darwin" || runtime.GOOS == "windows"

// caseName refuses a ref name with capital letters on a disk that does not
// tell them from small ones: there MAIN is main, and a branch or a tag
// named so may be one the checkout uses. what names it for the model.
func caseName(w word, what string) string {
	if foldsCase && strings.ToLower(w.lit()) != w.lit() {
		return what + " has capital letters, and on this machine's disk, which does not tell them from small ones, it may be a branch or a tag the checkout uses: write it in small letters"
	}
	return ""
}

// fetchRefspecs is the check of fetch and pull: a refspec whose
// destination is a branch or a tag ("main:main", "+x:refs/heads/x") moves
// it, where one under refs/remotes/ does not.
func fetchRefspecs(sub string, p treeParsed, _ TreeGit) string {
	if _, ok := p.flags["--multiple"]; ok || len(p.ops) < 2 {
		return "" // a remote alone, or every word a remote
	}
	if _, ok := p.flags["--all"]; ok {
		return ""
	}
	for _, w := range p.ops[1:] {
		if !w.static() {
			return "git " + sub + " has a refspec only known when the line runs (" + w.raw + "), which may write a branch by name"
		}
		spec := w.joined()
		_, dst, found := strings.Cut(strings.TrimPrefix(spec, "+"), ":")
		if found && dst != "" && !strings.HasPrefix(dst, "refs/remotes/") {
			return "git " + sub + " with " + spec + " writes " + dst + ", a branch or another ref, by name (fetch without a destination and use FETCH_HEAD, or into refs/remotes/)"
		}
	}
	return ""
}

// remoteName reports whether s can only be a remote's name: letters,
// digits, dots, dashes and underscores, beginning with a letter or a
// digit. (git takes a word that names no remote for a path.)
func remoteName(s string) bool {
	if s == "" || strings.Contains(s, "..") {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		ok := c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || i > 0 && (c == '.' || c == '-' || c == '_')
		if !ok {
			return false
		}
	}
	return true
}

// remoteURL reports whether s is the address of a repository elsewhere:
// scheme://... (not file://), or scp's user@host:path and host:path.
func remoteURL(s string) bool {
	if i := strings.Index(s, "://"); i > 0 {
		return !strings.EqualFold(s[:i], "file")
	}
	i := strings.IndexByte(s, ':')
	return i > 1 && !strings.ContainsAny(s[:i], "/\\") && !strings.HasPrefix(s, ".") && !strings.HasPrefix(s, "-")
}

// treeParsed is what treeArgs reads of a writing subcommand's words: its
// operands, how many of them came before "--" (plain), and each flag given
// by the name it was given under, with its value ("" for none).
type treeParsed struct {
	ops   []word
	plain int
	flags map[string]string
}

// treeArgs reads a writing subcommand's words by its spec, as git's option
// parser does, and strictly: a flag the spec does not hold is refused, by
// its exact spelling (git takes the start of a long flag too, and a start
// is refused here). A word only known when the line runs may be the value
// of a flag that takes one (-m "$MSG") or come after "--"; anywhere else
// git would take it for an option if it began with a dash, and so it would
// a file an unquoted pattern matched that begins with a dash.
func treeArgs(args []word, spec roSpec) (p treeParsed, bad string) {
	p.flags = map[string]string{}
	long := " " + spec.long + " "
	rest := false
	for i := 0; i < len(args); i++ {
		w := args[i]
		if rest {
			p.ops = append(p.ops, w)
			continue
		}
		if !w.static() {
			return p, "a word only known when the line runs (" + w.raw + ") would be taken for an option if it began with a dash: put -- before it, or write it out"
		}
		s := w.joined()
		if w.brace {
			// (Braces with no comma and no .. in them are only text:
			// stash@{0}, @{u}.)
			if alts, ok := braceExpand(s); ok && len(alts) > 1 {
				return p, "the braces in " + w.raw + " make several words, which may be options: write them out"
			}
		}
		switch {
		case s == "--":
			rest = true
			p.plain = len(p.ops)
			continue
		case w.glob && spec.noGlob && s != "" && strings.ContainsRune("*?[", rune(s[0])):
			// (A pattern that begins with plain text matches only names
			// that begin so: +refs/heads/* cannot be a flag.)
			return p, "the pattern " + w.raw + " may match a file whose name begins with a dash, which git would take for an option: put -- before it"
		case s == "-" || !strings.HasPrefix(s, "-"):
			p.ops = append(p.ops, w)
		case strings.HasPrefix(s, "--"):
			name, val, hasVal := strings.Cut(s, "=")
			switch {
			case hasVal && strings.Contains(long, " "+name+"= "):
				p.flags[name] = val
			case !hasVal && strings.Contains(long, " "+s+" "):
				p.flags[name] = ""
			case !hasVal && strings.Contains(long, " "+s+"= "):
				// The value is the next word, whatever it holds; at the end
				// of the line git takes its default (--merged: HEAD).
				p.flags[name] = ""
				if i+1 < len(args) {
					i++
					p.flags[name] = args[i].lit()
				}
			default:
				return p, "its option " + name + " is not one gnosh lets git take in a work tree of your own (spell options out whole)"
			}
		default:
			for j := 1; j < len(s); j++ {
				c := s[j]
				flag := "-" + string(c)
				switch {
				case strings.IndexByte(spec.optional, c) >= 0:
					p.flags[flag] = s[j+1:]
					j = len(s)
				case strings.IndexByte(spec.valued, c) >= 0:
					p.flags[flag] = s[j+1:]
					if j+1 == len(s) && i+1 < len(args) {
						i++
						p.flags[flag] = args[i].lit()
					}
					j = len(s)
				case strings.IndexByte(spec.short, c) >= 0:
					p.flags[flag] = ""
				case spec.digits && c >= '0' && c <= '9':
				default:
					return p, "its option " + flag + " is not one gnosh lets git take in a work tree of your own"
				}
			}
		}
	}
	if !rest {
		p.plain = len(p.ops)
	}
	return p, ""
}
