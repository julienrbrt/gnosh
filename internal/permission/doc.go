// Package permission decides, for every tool call a model makes, whether
// the call runs, is refused, or a person is asked first.
//
// It is a pure decision engine. It reads no files, shows no prompts and
// keeps no global state: the mode, the rules with their sources, the
// working directories, the home directory and whether the folder is
// trusted are all passed in, and the agent loop acts on the answer.
//
//	e, err := permission.New(permission.Options{
//		Cwd: "/home/jae/proj", Home: "/home/jae", Trusted: true,
//		Layers: []permission.Layer{
//			{Scope: permission.ScopeUser, Name: "~/.config/gnosh/settings.json", Base: "/home/jae/.config/gnosh",
//				Allow: []string{"Bash(go test *)"}, Deny: []string{"Read(.env)"}},
//			{Scope: permission.ScopeCLI, Name: "--disallowedTools", Deny: []string{"WebSearch"}},
//		},
//	})
//	d := e.Decide("Bash", call, action) // d.Behavior is Allow, Ask or Deny
//
// When the engine is unsure it asks. It never guesses toward allowing.
//
// # The order of a decision
//
// The first step that has something to say settles the call.
//
//  1. The tool is switched off (a deny rule names the whole tool, or
//     --tools left it out): refused.
//  2. Deny rules, from every source: refused. A deny from any source beats
//     an allow from any other, and no allow rule carves a hole in one.
//     Then the blocks outside the working directories, Claude Code's
//     blockReadsOutsideWorkingDirectories and
//     blockWritesOutsideWorkingDirectories (Options.BlockReads and
//     BlockWrites, block.go): a file tool's read or change outside them
//     is refused, and a shell command that reads outside them, or whose
//     files cannot be told, is asked about, in every mode.
//  3. Ask rules: a person is asked, in every mode (dontAsk refuses).
//  4. The checks that no allow rule and no hook approval passes: a change
//     to a protected path, a dangerous removal, a guard that could not be
//     checked. See below.
//  5. Allow rules: the call runs.
//  6. The mode: the call runs, or a person is asked.
//
// PreToolUse hooks run before all of this, in the agent loop. The loop
// runs them, computes the tool's Action from the input they left, calls
// Decide, and folds the hooks' verdict in with Decision.WithHook: a
// hook's block always wins; a hook's "ask" asks even when the call would
// have run; a hook's approval skips only the question that step 6 would
// have asked. Decision.Stage says which step decided.
//
// # Modes
//
//   - default: reading inside the working directories and the read-only
//     shell commands run; everything else asks.
//   - acceptEdits: also runs file edits, and the commands mkdir, touch,
//     rm, rmdir, mv, cp and sed -i in their everyday forms, when every
//     path stays inside the working directories (after links are
//     followed). Protected paths, dangerous removals, anything outside,
//     fetches and every other command still ask.
//   - plan: the model looks and does not change. File edits are refused;
//     commands that are not read-only ask, whatever allow rule names
//     them; a hook's approval does not reach them either.
//   - dontAsk: runs what default runs and what allow rules cover, and
//     refuses everything that would have asked.
//   - bypassPermissions: runs everything except what a deny rule refuses,
//     what an ask rule or a hook wants asked, a dangerous removal, a
//     command a deny or ask rule could not be checked against, and what
//     the blocks outside the working directories refuse or ask about.
//
// "manual" is another name for default. "auto" is not a gnosh mode and
// reads as default, with a note in Explain. A project's settings can
// never start a session in bypassPermissions.
//
// # Rules
//
// A rule is a tool name, or a tool name and a specifier in parentheses.
//
//	Bash                         every use of the tool (Bash(*) is the same)
//	Bash(npm run build)          that exact command
//	Bash(git diff *)             the command and anything after it, or nothing
//	Bash(ls:*)                   the same, in the older spelling
//	Read(.env)                   a file of that name anywhere under the working directory
//	Edit(/src/**)                everything under src, beside the settings file the rule is in
//	Read(//etc/**)   Read(~/x)   from the filesystem root; from the home directory
//	Read(!public/**)             deny and ask only: take these back out of the rules before it
//	WebFetch(domain:example.com) that host; domain:*.example.com is its subdomains
//	mcp__github   mcp__github__get_*   mcp__github__get_issue
//	Agent(Explore)               that sub-agent type (Task is an old name for Agent)
//	Skill(deploy)  Skill(deploy *)     the skill alone; the skill with any arguments
//	Bash(run_in_background:true) deny and ask only: one input parameter and a value pattern
//	*   mcp__*                   deny and ask only: every tool whose name fits
//
// Edit(path) covers every tool that changes files (Edit, Write,
// NotebookEdit); Read(path) covers Read, Grep and Glob. An Edit allow also
// grants reading. A Read deny also stops Edit and Write on that path.
// Both kinds of deny rule also reach the files a shell command names:
// cat .env, a redirection into a denied file, tee, sed -i, rm, mv, cp,
// sort -o, the file curl sends or saves, the copy git keeps
// (git show HEAD:.env).
//
// A rule that cannot be applied is an error that names the rule and where
// it was written, and New returns no engine: an unknown tool name, a form
// its list does not take, Write(path) where Edit(path) was meant. Nothing
// is accepted and ignored. The one exception is by design: names Claude
// Code has and gnosh does not (TodoWrite, MultiEdit, ...) load and never
// match, so that settings written for Claude Code still load.
//
// Deny and ask path rules cover everything under a directory they match,
// and they hold for every spelling that reaches the same file on some
// file system: another letter case, an accent written as a character of
// its own, a character nobody can see, a dot or a blank at the end of a
// name (fold.go). Two files may share such a spelling, and then the rule
// covers both. Allow rules are exact about all of it.
//
// On macOS a file has more absolute paths than one, and no call that
// follows links says so: /System/Volumes/Data/x (the disk that holds
// everybody's files, mounted a second time), /.nofollow/x and
// /.resolve/1/x are all x. Deny and ask rules, the protected paths and
// the dangerous removals take those ways in off a path before they look
// at it. A path under /.vol (or, on Linux, under /proc/<pid>/root, cwd
// or fd) names a file by a number: nothing can be held against that, so
// a call that names one asks, and with permissions skipped it asks when
// there is a rule about files to keep.
//
// A WebFetch rule is matched against the host a request would go to:
// lower case, without a trailing dot, an address in one form however the
// URL writes it (127.1, 0x7f.0.0.1, 2130706433 and ::ffff:127.0.0.1 are
// 127.0.0.1). A host that cannot be compared (letters outside ASCII, a
// number that is no address) matches no rule, and a call to one asks. A
// rule for a name is for that name; *.name is its subdomains. Decide
// judges the one URL it is given: a redirect is a new request, and the
// loop must put each one to Decide before it follows it. Whether two
// names lead to the same machine is not something a rule can know.
//
// # Shell commands
//
// The engine has its own reader of shell command lines (shell.go). It
// reads them as bash does, and keeps to what bash and a plain sh (dash)
// agree on wherever it lets something run. It splits a line at && || ;
// | |& & and line breaks, and follows quoting, escapes, $'...',
// comments, here-documents, redirections, leading assignments, $(...),
// backticks, <(...), subshells, groups, if, while, for, case, [[ ]],
// (( )) and function definitions. Whatever else it meets it reports as
// not understood, and a line it did not understand is never allowed by a
// rule or by the read-only list:
//
//   - select, coproc, $[...], quotes inside ${...}, $"...", zsh-only
//     syntax, an operator with nothing after it;
//   - a word of the shell's own after time (time coproc x, time if ...):
//     to bash what follows time is a pipeline of any kind;
//   - a backslash at the end of a line that joins two pieces of text
//     into one token ("$\<newline>(x)" is "$(x)" to a shell); the reader
//     follows one only where a blank stands beside it;
//   - a here-document with an unquoted delimiter in which a backslash at
//     the end of a line could move where the body ends (the line before
//     the delimiter, or lines that spell the delimiter once they are
//     joined): that is then a matter of which shell reads it;
//   - a comment that holds operators or quotes inside a substitution;
//   - a redirection whose descriptor has more than one digit (10>&1,
//     02>&1), which bash and sh read differently: the removal check
//     judges what each of them would run;
//   - outside quotes, a character that looks like a blank or cannot be
//     seen (a no-break space, a zero-width space, a mark that turns the
//     direction of the text), and bytes that are not text.
//
// Four forms are read twice, because sh has none of them: [[ a || b ]]
// is also the commands "[[ a" and "b ]]", with any < and > in it as
// redirections (a number right before one is its descriptor, and the ]]
// right after one is its file); (( x )) is also the command x in two
// subshells, with any * ? or [ in it a file name pattern (a variable at
// its head that the line did not set is passed over as too common to
// ask about, and the word after it is then the program: to that shell
// "(( $n rm y ))" runs rm y where n holds nothing); "a &>f b" is also a
// in the background and then the command b; and "mv . {fd}>log" is also
// mv with one more argument, {fd}: the {name} that bash takes before a
// redirection for the variable of a descriptor is a word like any to a
// plain sh. Deny and ask rules, and the checks for what destroys a
// directory, see both readings. An allow rule or the read-only list
// lets such a line run only when the second reading runs nothing more
// ([[ -f a && -d b ]], ls &>/dev/null).
//
// Deny and ask rules apply when any command in the line matches: one of
// its own, one nested in a subshell, a substitution or a loop body, the
// command after a wrapper (env, sudo, timeout, xargs, find -exec, ...),
// the script handed to sh -c (wherever the shell's options put it:
// bash +c x, sh -c - x, bash -c -eo pipefail x), eval, trap, a
// here-document or a pipe into a shell (also the shell behind a wrapper,
// or inside a group), to source /dev/stdin, to an alias, to mapfile -C or
// compgen -C (with the words the builtin puts after the command it
// calls: the index and the line, the word being completed), the words
// that follow an alias where the line uses it ("alias g=git" and then
// "g push origin" is git push origin to dash, and to bash where it was
// told to expand aliases; an alias that stands for nothing leaves the
// next word as the program), every word of an array where the
// program's name goes (a=(git push origin); "${a[@]}"),
// the string env -S splits into arguments of its own, the
// program hash -p or BASH_CMDS names (with the words that follow the
// name where the line uses it: "hash -p /bin/rm ls; ls -rf x" is rm -rf
// x), a command in a variable that some
// program runs (PAGER, GIT_EXTERNAL_DIFF), set before it or by export or
// declare, the file BASH_ENV names (as by source), what history -s puts
// in the shell's history (fc runs it again), the command a git setting
// given with git -c or written by git config runs (core.pager,
// core.sshCommand, diff.external and their kin) and a git alias (git -c
// alias.z='!rm x' z runs rm; git -c alias.p=push p pushes), and a
// $(...) inside quoted text, which the shell may yet evaluate:
// x='a[$(cmd)]'; echo $((x)),
// and the word after >&, which bash expands twice (ls >&'$(cmd)').
// Each is matched as written, with quotes removed, without its directory
// (/bin/rm is rm), whatever the case of the program's name (RM is rm,
// and BASH bash, on a file system that takes no notice of case), past
// any assignments, as the words bash's braces make of it ({rm,-f} x,
// bash {-c,'rm x'}), and with the variables the line itself set filled
// in (x=rm; $x y; see "What the line gives its variables" below).
// $IFS is one of them: a shell starts with a blank, a tab and a line
// break in it whatever its environment holds, so rm${IFS}-rf${IFS}x is
// rm -rf x; and a value is cut into words at whatever else the line
// puts in IFS (IFS=_; x=rm_-rf_y; $x). What follows a wrapper's own
// word that is only known when the line runs is read as the command it
// may be (sudo $FLAGS rm x), and what a command prints where a wrapper
// takes the value of a flag may be that value and the command after it
// (nice -n $(echo 5 rm x), and with text beside what is printed, env -u
// $(echo X rm x)m): such a line asks where a rule could apply. env takes
// any word with an = in it for a variable of its own, before "--" and
// after it, where a shell would look for a program of that name: "env --
// 0=1 rm x" runs rm x, and the command after such words is read. bash
// takes -p and -- for its keyword time's own only where they follow the
// keyword at once: after a redirection they name the program it runs
// ("time>&- -p x" runs a program named -p), while to a plain sh time is a
// program, which reads them as its flags. Both are read, and no allow
// rule for the command after them covers such a line. The
// arguments of a program whose name is not known ahead of time are read
// as the command or script they would be if it were a wrapper or a
// shell, what is written onto a flag or after a name= among them
// (-c'rm x', zz='rm x'); a program named by a number is such a one
// (/proc/self/exe is the shell itself, /dev/fd/3 a file that was opened,
// /.vol/... on macOS any file at all). A name of which only part is
// written (alias${x}, /bin/${X}sh) is read as each program this package
// has a reading for that it could be: the builtins that run text or
// change the shell, the shells and the wrappers. For a line the reader did not
// understand, the text is also cut crudely at everything that could start
// a command, and those pieces are matched too: that finds too much rather
// than too little.
//
//	deny Bash(rm *) stops:  ls; rm -rf x    echo $(rm -rf x)    'r'm -rf x
//	                        FOO=1 rm x      sudo rm x           sh -c 'rm x'
//	and does not stop:      echo rm -rf x   grep rm notes.txt
//
// A guard that could not be checked has not passed. When a deny or ask
// rule for commands is in force, the engine asks, in every mode, about a
// line the reader did not understand; about a command part of which is
// only known once the shell runs it ($CMD -rf x, git ${X:-push}) and
// which a rule could match once it is, the words xargs will add among
// them (echo push | xargs git); and about a line that changes how the
// rest of it is read or builds a command out of data (set -o histexpand,
// shopt -s expand_aliases, enable -f, eval "$X", a pipe into xargs
// sh -c, a script that printf builds from a format or echo from
// escapes); and about a value the line gives a variable that holds an
// unfinished substitution, of which bash makes a command of its own
// when it evaluates the value (x='$(git pushh'; echo ${x@P} runs git
// push). A rule that could not match changes nothing (echo $HOME
// under a deny on rm). The same holds for the Read and Edit rules and a
// file nobody can name ahead of time, a program that is only known when
// the line runs among them, except that with permissions skipped those
// are let go: nearly any variable could name a guarded file. What can be
// worked out is, in every mode: braces are the words bash makes of them
// (cat {.env,x}), a variable the line set is what it was set to, and a
// name with any other variable in it is also read with the variable
// not set, which is what a variable nobody set is: empty, or the text it
// is given to stand for then (cat .e${x}nv, cat ${X:-.env}; a flag is
// read that way too, and git's subcommand: rm -r$x is rm -r, git
// clean$x is git clean); where one
// of the line's own variables stands beside it, that one is written out
// ("d=.env; cat $X$d" reads .env). A file written onto a flag in such a
// word (awk -f${X:-.env}, sort -o$X.env, tool --config=${X:-.env}) is
// the file the flag names with nothing set. A name that starts with a
// dash, among what a pattern matches or what the line gave a variable,
// is a flag to the command it is handed to, and the words beside it are
// then other things than they seem: with a file named -R in the
// directory, "for f in *; do cp a.txt b.txt $f; done" copies a.txt over
// b.txt. A default or an alternative that is written with blanks, outside
// double quotes, is several words where its ${...} stands for it, with
// what is written beside it joined to the first and the last of them: "mv
// ${X:-.git /tmp/g} /tmp" is mv .git /tmp/g /tmp where X is not set,
// "rm -rf ${HOME:+. x}" is rm -rf . x, and "${X:-rm -rf} ." is rm -rf "."
// too. The command as it is then is read beside the one that is written,
// and held against every rule and check. What a variable holds is cut
// into words in the same way wherever the variable stands outside double
// quotes, whatever is quoted beside it in the word. With x holding ". y",
// each of these is rm -rf . y:
//
//	rm -rf $x''
//	rm -rf ""$x
//
// A word counts as quoted only when every variable and every command
// substitution in it stands inside the quotes.
//
// A program that a flag of its own sends to another folder works there
// (env -C, sudo -D, tar -C, make -C, go -C, and git's own -C, each of
// which goes on from the one before): each such folder is one the line
// may be in, as a cd makes it, so "env -C sub cat ../.env" reads .env. A
// cd found inside a command (command cd, builtin cd, eval 'cd x') counts
// where that command stands in the line. A file glued to a short flag is
// a name too (curl -K.env), and so is the value after the = of a long
// flag that the table of read-only commands does not know (diff
// --from-file=.env). A command handed a list of the files it reads (tar
// -T, rsync --files-from, cpio -o, zip -@, --files0-from) names files
// nobody sees ahead of time. A folder changed with all that is in it (rm
// -r, chmod -R, mv, a copy into it with cp -r or rsync -r) changes a file
// that a rule names inside it, by its path written out, when the file is
// there (a copy: when it brings a file of that name along); a rule that
// names files inside it by a pattern makes the command one that cannot be
// checked. A rule that matches at any depth (a bare name: Edit(.env)) is
// looked for in such a folder and two folders below it, and no deeper. A
// program this package knows nothing about
// writes the file its output flag names (-o, -ofile, --output, -out), tar
// the archive it makes, zip its archive and sqlite3 its database.
//
// What the line gives its variables is followed along the roads a line
// plainly takes: an assignment, or several (each value counts); an
// argument of export, declare, typeset, readonly or local; one variable
// handed to the next (a=.env; b=$a); the list of a for loop, braces and
// patterns in it expanded; the words of an array and ${a[0]}, ${a[@]};
// a default that assigns (${F:=.env}); and all of these inside a script
// the line runs (eval F=.env, bash -c 'F=.env; cat $F'). A command that
// uses such a variable is read once for each value (as one command that
// holds them all past four ways of choosing), so "for f in a .env; do
// cat $f; done" is refused by a rule on .env, and "for c in ls rm; do $c
// x; done" by one on rm. cd and pushd are followed to the directory
// they name, $PWD and ~+ are wherever the line may be by then, and
// $OLDPWD, ~- and ~2 wherever it has been. A ~ before a variable is the
// home directory where a slash stands between them (~/$d), and a plain
// ~ where none does (~$d). Each ${...}
// form is read for what it stands for, and not for the variable alone:
// ${v:-word} and ${v:+word} are also the word (${HOME:+rm} is rm, for
// HOME is always set), and ${v%pattern} and ${v#pattern} are any piece
// the pattern may leave of a value that is known. Of the forms bash
// adds, those whose outcome the text alone gives are worked out for a
// known value: ${v/text/other} and ${v//text/other} with plain text to
// look for, ${v:from:length} with numbers, the changes of case, and
// ${!v}, the variable that v's value names (${HOME/x/y} is the home
// directory, whose name holds no x). A variable that is made a name
// for another (declare -n r=d, typeset -n, local -n) shares every value
// with it, and neither is then known for sure: an assignment to r is
// one to d, and $r is what d holds. Values the
// line only gets by running something are not followed: $(...), read,
// printf -v, mapfile, a file that is sourced. A file named through one
// of those is the file nobody can name ahead of time of the paragraph
// above. The words a function of the line's own, a "set --" or a script
// after sh -c is handed are followed: $1, $2 and "$@" stand for any of
// them inside it, whichever its number ("f() { rm -rf "$1"; }; f ~"
// removes the home directory). What the line does
// spell out, in more values than gnosh follows (256 for one word) or
// with more changes of directory (32), is not let go: such a line asks
// in every mode where a rule about files is in force, and a recursive
// removal of such a target is a dangerous one. gnosh says a file is
// known only where the line gave every variable in its name all the
// values it can have: the line was read to its end, the variable's name
// stands nowhere on it but where it is assigned or expanded (read F and
// unset F hold the bare name), IFS is left alone, and one of the values
// is sure to be in place when the command runs. That is so for a
// command in the body of the for loop that gives the values, and for one
// that follows an assignment standing on its own at the top of the line
// (or an export), with no pipe, background job, redirection, alias or
// function of that name in its way; "X=1 cmd", a branch, a subshell
// and "F=a &>f" (an assignment in the background, to a plain sh) leave
// the variable as it was, which may be nothing, and the name is then
// also read that way.
//
// What a deny rule does not reach is a program's own script: bash x.sh,
// source x.sh, make, python x.py, a git alias, a value a line reads
// from a file and then uses as a number (read n <f; echo $((n))). A
// Read or Edit rule is held against every command that is handed the
// file by name, whatever the program: the operands of the file commands
// this package knows, the targets of redirections, and for a program it
// knows nothing about every argument that is no flag, what follows the
// = of one that is, and what follows an @ (base64 .env, tar cf x.tar
// .env, bash .env, docker run --env-file .env, node --env-file=.env; a
// word that only looks like the file is refused too). It does not
// reach a command that reads a file without naming it: grep -r x . and
// git diff show what is in the files they come across, and python -c
// "open('.env')" names it in a language gnosh does not read.
// A rule is held against the line, and against text the line hands to a
// shell; it is a guard against the usual way of writing a command, not a
// wall around the program.
//
// An allow rule is deliberately narrower. A line runs on allow rules only
// when the reader understood all of it and every script inside it that
// will run (the argument of an eval, a subscript that read or test works
// out as a number), and it holds no substitution,
// subshell, loop or other compound form, background job, assignment,
// ${...} form beyond the plain ones, or redirection to a file, and every
// command in it is, on its own, either named by an allow rule or
// read-only. A command is matched as the model wrote it (with runs of
// blanks counted as one), and its program name must be plain text. Only
// timeout, time, nice, nohup, stdbuf, command, builtin, noglob and a bare
// xargs are seen through; behind xargs, which adds arguments of its own,
// only a rule that ends in a wildcard counts. A rule with a * never
// approves watch, setsid, ionice, flock, or a find that runs, deletes or
// writes (or holds a word that is only known when the line runs, which
// may be its -exec); only the exact command does. A command that changes the shell
// itself (alias, export, set, shopt, hash, read, pushd, ...) can be
// allowed when it is the whole line, and asks in a line with other
// commands, which were judged as if nothing had changed. A bare Bash rule
// is the whole tool and allows every line.
//
//	allow Bash(npm run *) runs:   npm run build     timeout 60 npm run test 2>&1
//	                              npm run build | head -20
//	and asks about:               npm run build && npm install
//	                              npm run $(cat x)   NODE_ENV=test npm run build
//	                              npm run build > log.txt   "npm" run build
//
// A Bash call may name the machine its command is for (the host
// argument). Left blank or named "local", that is this machine, and the
// call is judged like any other. A command for another machine is held
// against the deny and ask rules like one for this machine and then
// asked about, with no rule to offer: allow rules and the read-only
// list are about this machine. With permissions skipped it runs; that
// the host was declared is then the whole gate.
//
// # Read-only commands
//
// readonly.go holds the table of commands that run without asking in
// every mode: ls, cat, head, tail, wc, grep, rg, find, which, diff, stat,
// du, pwd, echo, cd, sort, uniq, cut, tr, date, test and a few more, and
// git status, diff, log, show, branch, rev-parse, ls-files and blame. Each
// entry lists the flags known to keep the command read-only; any other
// flag asks, and so does a flag shortened to a prefix. A read-only command
// is also held to the working directories: every file it names, and the
// directory it would run in after any cd in the line, must lie inside
// them as written and as resolved. "As resolved" is the kernel's way: a
// ".." after a link is taken from where the link leads, so link/../x is
// judged at both places. A file a test looks at counts as read, and git
// is never handed an unquoted pattern (a file named like a flag would
// reach it as one). A repository's own settings can make even git status
// run a program (core.fsmonitor, a filter, a diff driver), so git after a
// cd in the line asks, and with Options.RepoOf every git command asks
// while the shell's folder, moved there by an earlier command, is in a
// repository that is not a working directory's own, nor a work tree or
// submodule of one; Options.GitRunsPrograms makes git ask in an untrusted
// folder whose own settings name such programs. diff, which reads the
// files of a directory it is given and follows the links among them, has
// every entry of that directory checked. A variable, a brace pattern or (with no
// Options.Glob) a file name pattern cannot be checked ahead of time and
// asks, and so does a file that an earlier cp, mv or ln of the same line
// puts in place. A pattern that starts with a dot (.*) is taken to match
// . and .. as well, as it does in sh and in bash before 5.2: "ls .*"
// lists the directory above. A * or a ? leaves out the names that start
// with a dot, as the shell does, so "cat *" is not held to a rule about
// .env; where the line may have told the shell otherwise (shopt -s
// dotglob, GLOBIGNORE, a shell it starts, a file it sources, a name gnosh
// cannot read), those names count. A pattern that matches nothing stays as it
// is written, so its text is held to the project as a name too: "cat /["
// reads a file named [. A pattern given to a command that takes one file
// or two asks, because the file after the last it reads may be one it
// writes: "uniq [ab].txt" is "uniq a.txt b.txt". "command" only reads as
// "command -v name".
//
// The tools of macOS and the BSDs are not GNU's, and a line is held to
// what either would do with it. GNU's take a flag wherever it stands;
// most of the others stop at the first file, so that "cat a.txt -n"
// reads a file named -n and "uniq a.txt -c" writes one named -c: a flag
// that follows a file is held to the project like a file (and asks,
// after uniq). The two seds differ more. To GNU sed "sed -i p x f" runs
// p on x and f; to the other it runs the script x on f and keeps a
// backup named fp. An empty word after -i, which is how that one is told
// to keep no backup, makes GNU sed run nothing and put the script (if
// there is a file of that name) and the files back as they were. A
// slash inside brackets ends a pattern for one and not for the
// other. So a sed line runs unasked only when every script either sed
// would take is made of s, d, p, q and = (or, for a word meant as a file
// name, stops that sed before it does a thing), and every file either
// would read, write or keep a backup in lies in the project. A GNU sed
// that is told to keep to POSIX (POSIXLY_CORRECT in the environment)
// takes its script from where the BSD one does and still knows GNU's
// own commands, so a word that would be its script must not hold one of
// those either: "sed '1e id' -e p f" runs id there.
//
// # What the engine is told, and must be told right
//
// The engine reads no files, so three things it cannot see for itself
// decide how much of the above holds:
//
//   - Options.Resolve must follow every link the kernel would, keep the
//     part of a path that does not exist yet, and follow a link whose
//     target is not there (a write through it makes the target). With no
//     Resolve nothing about links is checked for shell commands at all.
//   - Options.Glob must return every path a pattern matches: * and ?
//     within one path segment, [abc] and [a-c] for one of those
//     characters and [^abc] for any other, links to directories
//     followed. That is all it is asked: the engine hands a shell's
//     [!abc] over as [^abc], which Go's path matching and the shells
//     read alike, and does not hand over what matchers read differently
//     (a [[:class:]], a ] that opens a set, a backslash); such a pattern
//     asks. Matching too much is safe; matching too little is not. In
//     particular it must return the names that start with a dot for a
//     plain *, as filepath.Glob does: a shell leaves those out, but not
//     one that was told otherwise (dotglob, GLOBIGNORE), and the engine
//     takes them out itself only for a line that cannot have told it
//     so: one read to its end that runs nothing to build itself, starts
//     no shell, sources no file and names no such setting in any word.
//     The engine adds . and .. itself where a pattern could match them,
//     and the pattern's own text for when it matches nothing, and asks
//     about a line that changes what patterns match (shopt, GLOBIGNORE).
//   - The shell is taken to be bash or a plain sh, started with nothing
//     in its environment that changes how it reads a line or finds a
//     program. BASH_ENV, ENV, exported functions, SHELLOPTS, BASHOPTS,
//     POSIXLY_CORRECT, CDPATH and GLOBIGNORE in the environment of the
//     person who started gnosh are that person's own doing; the engine
//     cannot see them. Nor does it read the profile files a login shell
//     runs (sh -l): like a file sourced, they are programs of their own. zsh has not been held against this package.
//
// # Protected paths and dangerous removals
//
// A change to a protected path asks even when a rule allows the tool or
// the mode is acceptEdits: .git, .vscode, .idea, .husky, .cargo,
// .devcontainer, .yarn, .mvn, .claude, .gnosh, .ssh, .gnupg, shell
// startup files, the settings files of package managers and other tools,
// gnosh.json, and whatever Options.Protected lists. Only bypassPermissions
// skips the question; dontAsk refuses.
//
// Some directories have to be kept: the root and the directories right
// under it, the home directory, the working directory and what is above
// it, the repository's .git (in a working directory or above one; a
// .git further down is some other repository's), and gnosh's own folders
// (Options.Protected: its config, its state and data, what a feature
// keeps) with whatever holds one of them. An rm or rmdir whose target is
// one of those, or that
// could become one of those if a variable were empty, not set, or set
// (rm -rf "$DIR"/*, rm -rf ~/$DIR, rm -rf $DIR/.., rm -rf ${DIR:-/},
// rm -rf ${DIR:+/}, rm -rf ${X:-$HOME}, and by its whole path rm -rf
// $PREFIX/home/u; not "$DIR/build", a lone name under the root that is
// none of the usual ones), or that is some piece cut from
// one (rm -rf ${HOME%/*}), or the directory the line was in before a cd
// (~-, $OLDPWD), or one of the line's own variables beside one nobody
// set ("d=~; rm -rf $PREFIX$d"), or a pattern that may take one of them in or sweeps up
// what is in one (rm -rf /e*, rm -rf /home/u*, rm -rf ../p*, rm -rf
// /*/*, rm -rf */*; not rm -f */*.o, which names its own files),
// or whose program's name has something unknown inside it that may be
// a blank (rm${s}-rf${s}/),
// asks in every mode, bypassPermissions included (Decision.Critical is
// set, and the loop then asks with a time limit). "${DIR:?}" is the way
// to write such a command so that it does not. A target that is only
// what a command prints (rm -r "$(pwd)", rmdir "$(...)", the names xargs
// hands to rm -r) is the same doubt. What xargs adds to a command is as
// many words as it read: for mv the ones that are moved and the place
// they go to (echo .git /tmp/g | xargs mv), for chmod -R the mode and
// what is changed; and the same holds for the words that follow an
// alias where it is used, and for those a builtin puts after the
// command it calls (mapfile -C 'rm -rf'). A find that removes all
// it finds (find . -delete, find . -exec rm -rf {} +) is the same
// removal by another name, and so is one that hands all it finds to
// shred, truncate, mv, chmod, chown or chgrp; one with a test in front
// (-name, -type) is
// not, because what it finds is not known. A variable the line itself
// set is read as what it was set to (d=.; rm -rf $d). Where the engine
// is given a way to see what is in a directory (Options.Glob), targets
// that between them are everything in one that has to be kept are a
// sweep however they are written: "rm -rf [!.]*", "rm -rf [a-m]*
// [n-z]*", a list of every name.
//
// The other commands that destroy such a directory as surely as rm are
// asked about in the same way, with the same reading of their targets:
// mv of one (mv ~ /tmp/x, mv .git old, mv * elsewhere in the project;
// and mv -T onto one), chmod -R, chown -R and chgrp -R over one, git
// clean with -x or -X in one, sent to one with -C or handed one (not a
// dry run, and not a clean that leaves ignored files alone; with or
// without -f, which a setting can make git not want), dd of=, truncate and
// shred onto one or onto a whole disk under /dev, rsync --delete into
// one and rsync --remove-source-files out of one. An option is read in
// the shorter spellings GNU's tools take as well (chmod --rec, mv
// --t=dir). The words of an array are known as a set, so where they
// are such a command's operands any of them may be the one that is
// moved, or the place rsync empties (a=(/tmp/g .git /tmp/g); mv
// "${a[@]}" moves .git). A pattern or a list in braces that is all such
// a command is handed stands for its every operand: "mv .*" moves ".",
// ".." and each name that starts with a dot into the last of them (in
// dash, and in bash before 5.2), and "mv {.git,/tmp/g}" is mv .git
// /tmp/g. Their ordinary uses are not asked about: mv into a
// directory, chmod without -R, git clean -fd, rsync without --delete or
// into some other place.
//
// A line the reader could not follow is judged by its crude pieces: one
// that names rm is a removal nobody can rule out, and one that names
// another of these commands is judged by what the piece says the command
// is handed ("coproc mv .git /tmp/g"). A program named by a word that
// the line gives more than sixteen values asks in the same way: an rm
// among them is not ruled out.
//
// One thing is let go on purpose, so that a session nobody watches can
// clean up after itself: a target that is only a variable gnosh knows
// nothing about (rm -rf "$tmp", mv "$f" "$f.bak") is not asked about,
// whatever the command. What the line itself gives the variable is
// followed (see above), and a variable with a slash or a pattern after
// it is the doubt of the first paragraph. Two more things the check does
// not see, because it looks at what the line says and catches the usual
// accidents: a flag or a subcommand that only running the line produces
// (echo -rf . | xargs rm, git $(echo clean -fdx), chmod $(echo -R) 000
// .), and words that a command's output brings along beyond the one it
// stands for, where it stands without quotes in a place the command
// takes something other than a target (the owner in "chown -R $(whoami)
// build", the place in "mv a.txt $(mktemp -d)"). TestWhatTheRemovalCheckLetsGo
// is the record of all of these.
//
// In acceptEdits the file commands that run are mkdir, touch, rm, rmdir,
// mv, cp and sed -i with their everyday flags. cp -r is not one of them:
// on macOS it follows the links in the directory it copies, which brings
// whatever they lead to into the project; cp -R and cp -a do not. Handed
// a directory, cp writes each source under its own name inside it, and
// through a link of that name where one is already there: with da/s a
// link that leads out of the project, "cp db/s da" changes the file out
// there. So every name a copy may get is held to the working directories
// like a file named outright. A cp -R or -a goes on in the same way
// through every file of the directory it copies, so what the place
// already holds under the copy's name is looked through, and a link among
// it that leads out asks; a place that holds no such link takes the copy
// without a question. (mv puts its source in the link's place and writes
// through nothing, and a link that cp -R or -a copies stays a link.)
//
// # How this was attacked
//
// The reader was not trusted to agree with a shell; it was held against
// one. oracle_linux_test.go builds a jail: a throw-away project, home
// and outside directory with marked files and links that lead out (some
// of them named like flags: -n, -l, -R, 1), and a directory of stub
// programs, one binary linked under every command name the engine's
// tables know, each of which writes down its name, its arguments after
// the shell's expansions, its directory, what its first three
// descriptors are and which programs started it. Every generated line
// is run by bash and by dash through the shell tool's own wrapper (eval
// of the line as one argument), as a user with no rights, in process,
// mount and network namespaces of its own where everything but the jail
// is read-only, with a time limit. bash's trace gives the builtins; a
// snapshot of the tree gives what was written. The passes: stubs that
// succeed; stubs that fail (so both sides of && and || are seen); one
// where the read-only tools, the wrappers, nested shells and (for lines
// acceptEdits allowed) the file commands are the real programs; and
// that one again with the real tools told to keep to POSIX, which makes
// GNU's stop reading flags at the first file as the tools of macOS do.
//
// What is held against each run, under fixed rule sets and under sets
// made from the line's own commands:
//
//   - No silent widening. If the engine allowed the line on rules or the
//     read-only list, every command that ran is one the engine read, with
//     the same name and arguments, or one it allows when shown it alone;
//     no file changed that a rule or acceptEdits did not cover; nothing
//     marked as outside the project was shown by a line that ran only
//     because it reads.
//   - No dodged deny. If a command ran that a deny or ask rule names, or
//     a file was read or changed that one names, or a removal ran that
//     always asks, the engine did not say yes, in any mode. One thing
//     is not counted: with permissions skipped, a file named through a
//     word of which the engine itself says that it cannot know it (see
//     "What the line gives its variables"). A word made of variables
//     the line set is counted, and that is how the roads a value takes
//     (a for loop, an array, a default, ${v:+word}) were checked.
//   - Agreement of reading. Where the engine says it followed the whole
//     line, the commands it read are the ones that ran.
//
// The lines come from a grammar of shell constructs, from ordinary lines
// with a second command smuggled in some five hundred ways, from the
// read-only table's own flags with files in and out of the project and
// the flags before and after them, from random soup, and from a list
// written by hand (attackLines). Every disagreement is shrunk to its
// shortest form. The ones found are in attack_test.go and danger_test.go,
// which need no shell. A run of the size given with -oracle.lines is
// started by hand; the runs that were made, and what each found, are
// recorded in oracle_linux_test.go. The report at the end of a run shows
// one line for each kind of finding, and several holes can stand behind
// one kind: GNOSH_ORACLE_FINDINGS names a file that is given every
// finding as it is found, and TestOracleProbe judges single lines by hand.
//
// What the oracle cannot see: programs it does not run for real (a stub
// curl fetches nothing, a stub make runs no Makefile), so what a program
// does with its arguments, its environment and the files it reads is
// judged by reading the manuals; the tools and the sed of macOS and the
// BSDs, which were read from their manuals and tried by hand on a Mac
// (the POSIX pass stands in for the order of their flags and for nothing
// else); bash 3.2, which macOS ships, tried by hand on some sixty lines
// and not at volume; zsh not at all; and a shell whose environment
// changes how it reads (see above).
//
// # Suggestions
//
// A Decision that asks carries the choices "yes, and don't ask again"
// could make, narrowest first: for a command, the exact command and then
// a prefix rule one step wider (Bash(npm test *)), one rule for each part
// of the line that needed approval; for an edit, that one file, then
// acceptEdits; for a fetch, the host. There are none when no rule could
// cover the call (a line with a substitution, a protected path, an ask
// rule), and then only a one-time yes can be offered.
//
// # An untrusted folder
//
// While Options.Trusted is false, the layers the project supplied can
// only take away: their deny and ask rules apply, and their allow rules,
// additional directories and default mode wait. Explain lists what is
// held.
package permission
