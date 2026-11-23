package permission

import (
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// maxCommandLen is the longest command line the engine will read. A
// longer one asks.
const maxCommandLen = 10000

// maxUnits bounds how many commands the engine will dig out of one line
// (wrappers inside wrappers, scripts inside scripts) before it stops and
// treats the line as one it cannot follow.
const maxUnits = 64

// unit is one command to hold against the rules: a simple command of the
// line itself, or one found by looking inside another (after a wrapper,
// in a script handed to a shell).
type unit struct {
	cmd *simpleCmd
	// derived is set for commands found inside another command.
	derived bool
	// origin is the place among the line's own commands of the one the
	// command is, or was found inside: where it runs, in the order of the
	// line (the cd that "command cd sub" runs comes before a cd written
	// after it).
	origin int
	// texts caches denyTexts for the command.
	texts []string
}

// bashView is what the engine learned about one command line.
type bashView struct {
	text string
	sc   *script
	// units are every command to check against deny and ask rules.
	units []*unit
	// origin is the place of the line's own command being read, which the
	// units found inside it are given (unit.origin).
	origin int
	// opaque is not empty when something will run that could not be read:
	// a program name computed at run time, a script built by eval. It
	// says what.
	opaque string
	// frags are crude pieces of text to check against deny and ask
	// rules, for lines (or scripts inside them) the reader could not
	// follow.
	frags []string
	// tooMany is set when the line held more commands than maxUnits.
	tooMany bool
	// vars are the variables the line gives a value (noteVars), and
	// pending the values among them that are made of other variables.
	// grew is set when a value turned up after the commands that use it
	// may have been read: inside a script the line runs.
	vars    map[string]*lineVar
	pending []pendingVal
	grew    bool
	// pwds are the directories the line may be in, once they are worked
	// out: what $PWD may be.
	pwds []string
	// tight and bare are what closedVar goes by, worked out once the
	// whole line is read (tightDone).
	tight     bool
	bare      map[string]bool
	tightDone bool
	// closing are the variables closedVar is in the middle of asking
	// about, so that one made of itself ("a=$b; b=$a") ends.
	closing map[string]bool
	// index is where each of the line's own commands stands in sc.cmds
	// (see place).
	index map[*simpleCmd]int
	// dirsCapped is set when the line goes to more directories than
	// maxLineDirs, and moves when it holds a cd or a pushd at all.
	dirsCapped bool
	moves      bool
	// bareShell is set when a shell is started with no script in sight:
	// its script is whatever reaches its standard input.
	bareShell bool
	// unread are the crude pieces of scripts inside the line that will
	// run and could not be read (the argument of an eval, of sh -c).
	unread []string
	// refs are the variables the line makes names for other variables
	// (declare -n): see mergeRefs.
	refs map[string]bool
	// manyNames is set when the line gives the word that names a program
	// more values than maxProgNames, and says which word.
	manyNames string
	// aliases are what the line's alias commands make each name stand
	// for (see useAlias).
	aliases map[string][]string
	// funcNames are the functions the line defines (see giveArgs).
	funcNames map[string]bool
}

// shellNames are programs that run a script given as text.
var shellNames = map[string]bool{
	"sh": true, "bash": true, "zsh": true, "dash": true, "ksh": true, "ash": true, "fish": true, "su": true,
	"rbash": true, "mksh": true, "yash": true, "posh": true, "csh": true, "tcsh": true,
	"capsh": true, // hands what follows its -- to bash
}

// evaluators are builtins that evaluate their operands (see derive).
var evaluators = map[string]bool{
	"[[": true, "[": true, "test": true, "let": true, "declare": true, "typeset": true, "local": true,
	"readonly": true, "export": true, "read": true, "printf": true, "unset": true, "integer": true, "float": true,
	"wait": true, "getopts": true,
}

// wrapSpec says how to find the command inside a wrapper's arguments.
type wrapSpec struct {
	// valued lists the flags that take a separate value.
	valued string
	// pos is how many operands come before the command.
	pos int
	// assigns: NAME=value words may come before the command.
	assigns bool
	// nameAssigns: those are the shell's own assignments, a valid name
	// before the = (bash's time), where env and sudo take any word with an
	// = in it: "time a.b=1 x" runs a program named a.b=1.
	nameAssigns bool
	// script lists the flags whose value is itself a script.
	script string
	// args lists the flags whose value is split into more arguments of
	// the wrapper's own: env -S '-i rm x' is env -i rm x.
	args string
	// joined: the wrapper hands its arguments to a shell as one line.
	joined bool
	// posMaybe: one more operand may or may not come before the command
	// (taskset's mask, script's file): both are tried.
	posMaybe bool
	// dashStarts: a word that starts with a dash may be the command
	// itself. bash's own time takes -p and nothing else: "time -x y"
	// runs a program named -x.
	dashStarts bool
}

// wrappers are programs that run the command made of their arguments.
// This list is used to look inside commands for what deny and ask rules
// name; it is deliberately wider than the list of wrappers an allow rule
// sees through (strictStrip).
var wrappers = map[string]wrapSpec{
	"env":         {valued: " -u --unset -C --chdir ", assigns: true, args: " -S --split-string "},
	"sudo":        {valued: " -u -g -h -p -C -D -R -T -U -r -t --user --group --host --prompt --chdir ", assigns: true},
	"doas":        {valued: " -u -C "},
	"timeout":     {valued: " -s --signal -k --kill-after ", pos: 1},
	"nice":        {valued: " -n --adjustment "},
	"ionice":      {valued: " -c -n -p --class --classdata "},
	"nohup":       {},
	"time":        {valued: " -f -o --format --output ", assigns: true, nameAssigns: true, dashStarts: true},
	"stdbuf":      {valued: " -i -o -e --input --output --error "},
	"setsid":      {},
	"watch":       {valued: " -n --interval -d ", joined: true},
	"flock":       {valued: " -w --wait --timeout -E --conflict-exit-code ", pos: 1, script: " -c --command "},
	"xargs":       {valued: " -I -J -R -S -n -P -L -d -s -E -a --max-args --max-procs --max-lines --delimiter --arg-file "},
	"command":     {},
	"builtin":     {},
	"exec":        {valued: " -a "},
	"noglob":      {},
	"nocorrect":   {},
	"busybox":     {},
	"toybox":      {},
	"chronic":     {},
	"caffeinate":  {valued: " -t -w "},
	"unbuffer":    {},
	"chroot":      {pos: 1},
	"repeat":      {pos: 1},
	"strace":      {valued: " -o -e -p -s -u "},
	"ltrace":      {valued: " -o -e -p -s -u "},
	"nsenter":     {valued: " -t --target "},
	"unshare":     {},
	"proxychains": {}, "proxychains4": {}, "torsocks": {}, "tsocks": {},
	"fakeroot": {}, "eatmydata": {}, "rlwrap": {}, "torify": {}, "valgrind": {}, "ccache": {}, "catchsegv": {},
	"dbus-run-session": {}, "setpriv": {}, "systemd-run": {}, "numactl": {}, "firejail": {}, "bwrap": {},
	"xvfb-run": {valued: " -s --server-args -n --server-num -e --error-file -f --auth-file -p --xauth-protocol -w --wait "},
	"sshpass":  {valued: " -p -f -d -P "},
	"runuser":  {valued: " -u --user -g --group -G --supp-group -s --shell ", script: " -c --command "},
	"taskset":  {valued: " -c --cpu-list -p --pid ", posMaybe: true},
	"chrt":     {valued: " -p --pid -T --sched-runtime -P --sched-period -D --sched-deadline ", posMaybe: true},
	"script":   {script: " -c --command ", posMaybe: true},
	"parallel": {valued: " -j --jobs -S --sshlogin "},
	"setarch":  {posMaybe: true}, "linux32": {}, "linux64": {}, "i386": {}, "x86_64": {}, "uname26": {},
	"runcon": {valued: " -u --user -r --role -t --type -l --range "},
	"screen": {valued: " -S -c -L -p -s -t -T -e -h "},
	// ssh runs the words after the host as one line, on a machine that
	// may be this one.
	"ssh": {valued: " -b -c -D -E -e -F -I -i -J -L -l -m -O -o -p -Q -R -S -W -w ", pos: 1, joined: true},
	// What macOS and the BSDs have of the kind.
	"apply": {}, "arch": {}, "lockf": {valued: " -t ", pos: 1},
	"xcrun":        {valued: " --sdk -sdk --toolchain -toolchain "},
	"sandbox-exec": {valued: " -f -n -p -D "},
}

// viewBash reads a command line and finds every command in it.
func (e *Engine) viewBash(text string) *bashView {
	v := e.readSettled(text)
	// dash has no $'...': it keeps the $, and reads what follows as a
	// string in single quotes. Wherever that text is read again (an
	// alias, eval, sh -c, a trap), the $ is an expansion: "sh -c
	// $'1rm -rf ~'" runs $1rm, which is rm when $1 is empty, and "alias
	// zz=$'1rm'" makes zz stand for it (oracle seed 131). So the line is
	// read once more as dash reads it, every $' written \$', and what
	// that reading runs is held against the rules and the removal check
	// as well, as derived commands, which let nothing through.
	if strings.Contains(text, "$'") {
		dv := e.readSettled(strings.ReplaceAll(text, "$'", "\\$'"))
		for _, u := range dv.units {
			d := *u
			d.derived = true
			v.units = append(v.units, &d)
		}
		v.frags = append(v.frags, dv.frags...)
		switch {
		case !dv.sc.ok:
			v.setOpaque("dash reads a $'...' as a $ and a string in quotes, and so read the line cannot be followed (" + dv.sc.why + ")")
		case dv.opaque != "":
			v.setOpaque(dv.opaque)
		}
	}
	return v
}

// readSettled is readBash until what the line's scripts give its
// variables is known.
func (e *Engine) readSettled(text string) *bashView {
	v := e.readBash(text, nil)
	// What a line gives its variables inside a script it runs ("eval
	// f=.env; cat $f") is known only once that script is read. The line
	// is then read again with those values in hand.
	for i := 0; i < 2 && v.grew; i++ {
		v = e.readBash(text, v)
	}
	return v
}

// readBash is viewBash for one reading of the line; before is the
// reading that came before it, for the variables that one found.
func (e *Engine) readBash(text string, before *bashView) *bashView {
	v := &bashView{text: text, vars: map[string]*lineVar{}}
	if len(text) > maxCommandLen {
		v.sc = &script{why: "the command is longer than 10,000 characters"}
		v.frags = crudeFragments(text)
		return v
	}
	v.sc = parseScript(text)
	if !v.sc.ok {
		v.frags = crudeFragments(text)
	}
	if before != nil {
		v.vars, v.pending = before.vars, before.pending
	}
	e.noteScript(v, v.sc, true)
	for i, c := range v.sc.cmds {
		e.noteVars(v, c, i)
	}
	e.settleVars(v, nil)
	v.grew = false
	// The line's own commands always count; what is dug out of them is
	// bounded.
	for i, c := range v.sc.cmds {
		v.units = append(v.units, &unit{cmd: c, origin: i})
	}
	var prev *simpleCmd
	for i, c := range v.sc.cmds {
		v.origin = i
		e.derive(v, c, prev, 0)
		prev = c
	}
	v.origin = len(v.sc.cmds) // what is found after the line's own commands

	// A shell with no (( )) runs its text as commands.
	for _, a := range v.sc.arith {
		e.addScript(v, a, nestSubshell, 0, scriptArith)
	}
	for _, w := range v.sc.loose {
		e.scanValue(v, w, nestCompound, 0)
	}
	if v.bareShell {
		// "{ sh; } <<< 'rm x'" and "echo 'rm x' | (sh)": a shell with no
		// script of its own reads the here-document of the group it
		// stands in, or the pipe into it. Every here-document in the
		// line, and what every echo prints, is read as a script that may
		// run; any other pipe is a script nobody can read.
		if v.sc.piped {
			for _, f := range v.sc.cmds {
				if len(f.words) > 0 && f.words[0].static() && allStatic(f.words[1:]) &&
					(f.words[0].joined() == "echo" || f.words[0].joined() == "printf") {
					e.addScript(v, strings.Join(litsOf(f.words[1:]), " "), f.nest|nestScript, 0, scriptMaybe)
				}
			}
			v.setOpaque("a shell reads its script from a pipe")
		}
		for _, c := range v.sc.cmds {
			for _, r := range c.redirs {
				switch {
				case (r.op == "<<" || r.op == "<<-") && r.bodyStatic:
					e.addScript(v, r.body, c.nest|nestScript, 0, scriptMaybe)
				case r.op == "<<<" && r.target.static():
					e.addScript(v, r.target.joined(), c.nest|nestScript, 0, scriptMaybe)
				case r.op == "<<" || r.op == "<<-" || r.op == "<<<" || r.op == "<" && r.target.hasSubst():
					v.setOpaque("a shell reads a script that is computed when the command runs")
				}
			}
		}
	}
	// (What the scripts inside the line give its variables. By now every
	// command of the line is known, and with them the names that stand
	// in it as text of their own: "read F", "unset F".)
	e.settleVars(v, bareNames(v))
	return v
}

// lineVal is one value a line gives a variable.
type lineVal struct {
	text string
	// glob says the shell matched the text against file names where the
	// value was made: the list of a for loop, the words of an array.
	glob bool
	// loose says the text is what is left of a value when a variable it
	// is made of holds nothing: one the line does not set, or does not
	// set for sure before the value is made. "w=$X/" may make w a lone
	// slash, and "w=.e${X}nv" may make it .env. Such a text is held
	// against the rules about files like any other, and by the removal
	// check as the word it came from would be where it is written out
	// ("rm -rf $X/"); it is never written into a command (filledArgs).
	loose bool
	// hole says a part not known stood in the loose text's last name
	// ("t=/tmp$X-a"): what is left there is not the name written out.
	hole bool
}

// lineVar is what a line says of a variable it sets. The values are
// used only to find what deny and ask rules name ("x=rm; $x -rf y",
// "for f in .env; do cat $f; done"): a line that sets a variable is
// never allowed by a rule, so nothing is let through on their account.
type lineVar struct {
	vals []lineVal
	// open says the variable may also hold something that is not known
	// ahead of time: a value the line computes, or the one it came with.
	open bool
	// capped says the line gives the variable more values than gnosh
	// follows: a word made of it is one of more texts than were tried.
	capped bool
}

// pendingVal is a value made of other variables, which is known once
// they are.
type pendingVal struct {
	name   string
	w      word
	assign bool
	list   bool
	// at is where the command that makes the value stands among the
	// line's own (for a loop's list, where the loop's body begins), or
	// noPlace for one found inside another command: what the variables
	// in the word hold is what they hold there.
	at int
}

// maxFilled bounds how many texts one word is read as when the line
// gives its variables several values.
const maxFilled = 256

// maxProgNames bounds how many programs the word that names one is read
// as when the line gives its variables several values. A word that would
// be more is asked about.
const maxProgNames = 16

// give records a value the line gives a variable.
func (v *bashView) give(name string, val lineVal) {
	lv := v.vars[name]
	if lv == nil {
		lv = &lineVar{}
		v.vars[name] = lv
	}
	for _, have := range lv.vals {
		if have == val {
			return
		}
	}
	lv.vals = append(lv.vals, val)
	v.grew = true
}

// openVar records that the line gives a variable a value that is not
// known ahead of time.
func (v *bashView) openVar(name string) {
	lv := v.vars[name]
	if lv == nil {
		lv = &lineVar{}
		v.vars[name] = lv
	}
	if !lv.open {
		lv.open, v.grew = true, true
	}
}

// declares are the builtins whose arguments assign.
var declares = map[string]bool{"export": true, "declare": true, "typeset": true, "readonly": true, "local": true}

// noteScript records the variables a script's for loops go through. own
// says the script is the line itself, and not one found inside it.
func (e *Engine) noteScript(v *bashView, sc *script, own bool) {
	for _, name := range sc.funcNames {
		if v.funcNames == nil {
			v.funcNames = map[string]bool{}
		}
		v.funcNames[name] = true
	}
	for _, l := range sc.loops {
		if l.args {
			v.openVar(l.name) // the words the script was started with
			continue
		}
		at := noPlace
		if own {
			at = l.from
		}
		for _, w := range l.words {
			e.noteWord(v, l.name, w, false, true, at)
		}
	}
	for _, w := range sc.loose {
		e.noteSets(v, w)
	}
}

// noteVars records what a command gives the line's variables: by an
// assignment, as an argument of export and its kin, or by a
// ${name:=word} in one of its words. at is where the command stands
// among the line's own, or noPlace.
func (e *Engine) noteVars(v *bashView, c *simpleCmd, at int) {
	for _, a := range c.assigns {
		e.noteSets(v, a.w)
		if len(c.words) > 0 {
			// "X=1 cmd": the value is for cmd alone, and the variable is
			// as it was in the words of the command itself.
			v.openVar(a.name)
		}
		switch {
		case a.array:
			// name=(word ...) and name+=(word ...): each word is a value.
			for _, el := range a.elems {
				e.noteSets(v, el)
				if strings.HasPrefix(el.raw, "[") {
					v.openVar(a.name) // [i]=word: not followed
					continue
				}
				e.noteWord(v, a.name, el, false, true, at)
			}
		case strings.HasPrefix(a.w.raw, a.name+"+="):
			v.openVar(a.name) // text added to what is there: not followed
		default:
			e.noteWord(v, a.name, a.w, true, false, at)
		}
	}
	for _, w := range c.words {
		e.noteSets(v, w)
	}
	for _, r := range c.redirs {
		e.noteSets(v, r.target)
	}
	// "time F=x" is an assignment to bash, for which time is no program.
	if len(c.words) > 1 && c.words[0].static() && c.words[0].joined() == "time" {
		for _, w := range c.words[1:] {
			if l := w.lit(); l == "-p" || l == "--" || l == "!" || l == "time" {
				continue
			}
			name, ok := assignName(w.raw)
			if !ok {
				break
			}
			if strings.HasPrefix(w.raw, name+"=") {
				e.noteWord(v, name, w, true, false, at)
			}
			v.openVar(name)
		}
	}
	// export x=1 and its kin assign too, and so may a program whose name
	// is only known when the line runs.
	if len(c.words) > 1 && (!c.words[0].static() || declares[c.words[0].joined()]) {
		// With -n the variable is a name for another one (see mergeRefs).
		ref := false
		for _, w := range c.words[1:] {
			if l := w.lit(); w.static() && len(l) > 1 && (l[0] == '-' || l[0] == '+') && l[1] != '-' && strings.Contains(l[1:], "n") {
				ref = true
			}
		}
		for _, w := range c.words[1:] {
			name, ok := assignName(w.raw)
			switch {
			case ok && strings.HasPrefix(w.raw, name+"+="):
				v.openVar(name)
			case ok:
				e.noteWord(v, name, w, true, true, at)
			case w.static() && isVarName(w.joined()):
				name, ok = w.joined(), true // "declare -n r": whatever r holds is the name
			}
			if ok && ref {
				if v.refs == nil {
					v.refs = map[string]bool{}
				}
				v.refs[name] = true
			}
		}
	}
}

// isVarName reports whether s can be the name of a shell variable.
func isVarName(s string) bool {
	if s == "" || !isNameStart(s[0]) {
		return false
	}
	for i := 1; i < len(s); i++ {
		if !isNameChar(s[i]) {
			return false
		}
	}
	return true
}

// mergeRefs follows the variables that are names for others. After
// "declare -n r=d" an assignment to r is one to d, and $r is what d
// holds: "d=build; declare -n r=d; r=/; rm -rf $d" removes the root, and
// "d=x; declare -n d=w; w=$d/" makes w a lone slash, because $d is by
// then what w held, which was nothing. So every value such a variable is
// given that could be a name is taken for one it may stand for: the two
// then share every value either was given, and neither is known for
// sure. A name for HOME or PWD stands for what gnosh was told those are
// (on a line that changes directory, PWD is more than gnosh follows
// here).
func (e *Engine) mergeRefs(v *bashView) {
	for r := range v.refs {
		v.openVar(r)
		for _, val := range append([]lineVal(nil), v.vars[r].vals...) {
			t := val.text
			if !isVarName(t) || t == r {
				continue
			}
			switch {
			case t == "HOME" && e.home != "" && v.vars[t] == nil:
				v.give(r, lineVal{text: e.home})
			case (t == "PWD" || t == "OLDPWD") && e.cur != "":
				v.give(r, lineVal{text: e.cur})
				if lv := v.vars[r]; !lv.capped && (strings.Contains(v.text, "cd") || strings.Contains(v.text, "pushd") || strings.Contains(v.text, "popd")) {
					lv.capped, v.grew = true, true
				}
			}
			v.openVar(t)
			for _, x := range append([]lineVal(nil), v.vars[r].vals...) {
				if x.text != t {
					v.give(t, x)
				}
			}
			for _, x := range append([]lineVal(nil), v.vars[t].vals...) {
				v.give(r, x)
			}
		}
	}
}

// noteSets records the ${name:=word} forms in a word: a variable with
// no value is given the word, and keeps it for the rest of the line.
func (e *Engine) noteSets(v *bashView, w word) {
	for _, p := range w.parts {
		if p.kind == partVar && p.sets && p.name != "" {
			if p.hasDef {
				v.give(p.name, lineVal{text: p.defIn(e.home)})
			}
			v.openVar(p.name)
		}
	}
}

// assigned returns the value in the text of an assignment word:
// what follows name= or name[i]=.
func assigned(name, text string) (string, bool) {
	if strings.HasPrefix(text, name+"=") {
		return text[len(name)+1:], true
	}
	if strings.HasPrefix(text, name+"[") {
		if k := strings.Index(text, "]="); k >= 0 {
			return text[k+2:], true
		}
	}
	return "", false
}

// noteWord records the value a word gives a variable. assign says the
// word is an assignment (name=value) and not the value alone; list says
// the shell makes more words of the braces in it and, where it is the
// value alone, matches its patterns: an argument of export, a word of an
// array or of a for loop's list. at is where the command that holds the
// word stands among the line's own, or noPlace.
func (e *Engine) noteWord(v *bashView, name string, w word, assign, list bool, at int) {
	if !w.static() {
		// Made of other variables, perhaps ones the line sets
		// ("a=.env; b=$a"): see settleVars.
		for _, have := range v.pending {
			if have.name == name && have.w.raw == w.raw && have.assign == assign && have.list == list && have.at == at {
				return
			}
		}
		v.pending = append(v.pending, pendingVal{name: name, w: w, assign: assign, list: list, at: at})
		return
	}
	texts := []string{w.value(e.home)}
	if list && w.brace {
		if w.quoted {
			v.openVar(name) // braces with quoted text among them: not told for sure
		}
		if alts, ok := braceExpand(w.joined()); ok {
			texts = texts[:0]
			for _, alt := range alts {
				part := litOf(alt)
				part.tilde = strings.HasPrefix(alt, "~") && !w.quoted
				texts = append(texts, part.value(e.home))
			}
		}
	}
	for _, t := range texts {
		val := t
		if assign {
			var ok bool
			if val, ok = assigned(name, t); !ok {
				v.openVar(name)
				continue
			}
		}
		v.give(name, lineVal{text: val, glob: list && !assign && w.glob && hasPathMagic(val)})
	}
}

// settleVars works out the values that are made of other variables the
// line sets, a few links deep, and marks as not known for sure every
// value made of a variable that is not: one the line does not set, or
// does not set for sure before the value is made ("w=$d/; d=x" makes w a
// lone slash, whatever d is given afterwards). What is left of such a
// value when that variable holds nothing is one more value the variable
// may have, a loose one (see lineVal).
//
// bare are the names that stand on the line as text of their own (see
// bareNames), once the whole line is read, and nil before that: a
// variable some builtin may have been handed by name ("read d", "unset
// d") is not known for sure either.
func (e *Engine) settleVars(v *bashView, bare map[string]bool) {
	fill := func() {
		e.mergeRefs(v)
		for _, pv := range v.pending {
			for _, w := range e.pendingWords(pv) {
				vals, _, ok := e.filledAll(v, w)
				if !ok {
					continue
				}
				for _, fv := range vals {
					e.giveFilled(v, pv, fv)
				}
			}
		}
		e.mergeRefs(v)
	}
	for round := 0; round < 3; round++ {
		fill()
	}
	// A value that is not sure makes the ones built on it unsure in
	// their turn, so this goes round until nothing changes (or eight
	// times: past that the chain is longer than anybody writes).
	for round := 0; round < 8; round++ {
		before := v.weight()
		for _, pv := range v.pending {
			if e.firmWord(v, pv.w, pv.at, bare) {
				continue
			}
			v.openVar(pv.name)
			for _, w := range e.pendingWords(pv) {
				texts, holes, capped := e.looseTexts(v, w, pv.at, bare)
				if capped && !v.vars[pv.name].capped {
					v.vars[pv.name].capped, v.grew = true, true
				}
				for i, t := range texts {
					e.giveFilled(v, pv, lineVal{text: t, loose: true, hole: holes[i]})
				}
			}
		}
		fill()
		if v.weight() == before {
			break
		}
	}
}

// weight counts what is recorded of the line's variables, so that a
// round of settleVars can tell whether it added anything.
func (v *bashView) weight() int {
	n := 0
	for _, lv := range v.vars {
		n += len(lv.vals)
		if lv.open {
			n++
		}
		if lv.capped {
			n++
		}
	}
	return n
}

// pendingWords returns the readings of a word that makes a value: the
// word itself and, for an assignment with a ~ the shell expands
// ("w=~/$d"), the word with the home directory in its place.
func (e *Engine) pendingWords(pv pendingVal) []word {
	out := []word{pv.w}
	if pv.assign && pv.w.eqTilde && e.home != "" {
		if w, ok := homeFilled(pv.w, e.home); ok {
			out = append(out, w)
		}
	}
	return out
}

// homeFilled returns an assignment word with each ~ the shell expands in
// its value replaced by the home directory: one that opens the value or
// follows a colon in it, and has a slash, a colon or the end of the word
// after it. A ~ right before a variable is not expanded ("w=~$d" is a
// ~ and what d holds), and neither is one a variable's value brings
// along: the shell looks for tildes before it fills variables in.
func homeFilled(w word, home string) (word, bool) {
	out := w
	out.parts = append([]wordPart(nil), w.parts...)
	out.eqTilde = false
	changed, afterEq, start := false, false, false
	for i, p := range out.parts {
		if p.kind != partLit {
			start = false
			continue
		}
		var b strings.Builder
		for k := 0; k < len(p.lit); k++ {
			c := p.lit[k]
			switch {
			case c == '~' && start:
				start = false
				last := k+1 == len(p.lit)
				if !last && (p.lit[k+1] == '/' || p.lit[k+1] == ':') || last && i == len(out.parts)-1 {
					b.WriteString(home)
					changed = true
					continue
				}
				b.WriteByte(c)
			case c == '=' && !afterEq:
				afterEq, start = true, true
				b.WriteByte(c)
			case c == ':' && afterEq:
				start = true
				b.WriteByte(c)
			default:
				start = false
				b.WriteByte(c)
			}
		}
		out.parts[i].lit = b.String()
	}
	return out, changed
}

// giveFilled records a text that a word which makes a value came to.
func (e *Engine) giveFilled(v *bashView, pv pendingVal, fv lineVal) {
	val := fv.text
	if pv.assign {
		var ok bool
		if val, ok = assigned(pv.name, fv.text); !ok {
			return
		}
	}
	texts := []string{val}
	switch {
	case pv.list && !pv.assign && !pv.w.quoted:
		// A word of a list that nothing quotes is cut into as many
		// words as its value has.
		texts = e.fields(v, val)
	case pv.list && pv.assign && !pv.w.quoted:
		// An argument of export: a plain sh cuts it too, and the
		// variable gets what stands before the first cut.
		if f := e.fields(v, val); len(f) > 1 {
			texts = append(texts, f[0])
		}
	}
	for _, t := range texts {
		if fv.loose && t == "" {
			continue // nothing is left, and nothing is known
		}
		v.give(pv.name, lineVal{text: t, glob: fv.glob || pv.list && !pv.assign && !pv.w.quoted && hasPathMagic(t), loose: fv.loose, hole: fv.hole})
	}
}

// firmVar reports whether a variable is one the line gave all its
// values, with one of them in place where the command at index at
// stands, and that no builtin was handed by name (bare, when the whole
// line is read; closedVar asks what else can be asked then).
func firmVar(v *bashView, name string, at int, bare map[string]bool) bool {
	lv := v.vars[name]
	return lv != nil && !lv.open && len(lv.vals) > 0 && !bare[name] && !shellOwned[name] && sureSet(v, name, at)
}

// firmWord reports whether a word that makes a value is known for sure
// where the command at index at makes it: every variable in it is firm
// there.
func (e *Engine) firmWord(v *bashView, w word, at int, bare map[string]bool) bool {
	if _, _, ok := e.filledAll(v, w); !ok {
		return false
	}
	for _, p := range w.parts {
		switch {
		case p.kind == partLit:
		case p.kind == partVar && (p.op == "!" || p.op == "~"):
			return false // ${!name}, ${name@U}: what the form makes of it is not followed that far
		case p.kind == partVar && p.name != "":
			if !firmVar(v, p.name, at, bare) {
				return false
			}
		default:
			return false
		}
	}
	return true
}

// maxLoose bounds how many loose texts one word is read as. A word that
// would be more is one the line gives more values than gnosh follows.
const maxLoose = 64

// looseTexts returns what is left of a word that makes a value when
// every variable in it that is not known for sure holds nothing (or what
// its ${name:-word} says it then stands for): the variables that are firm
// where the value is made keep their values, and so do HOME and PWD as
// gnosh was told they are. It returns nothing for a word with no such
// variable, and for one that holds what a command prints. A variable that
// is not known for sure may still hold what the line gave it ("d=~; read
// d; w=$X$d" may make w the home directory), so each of those values is
// tried beside the nothing. capped says there were more texts than
// maxLoose, and none is returned.
func (e *Engine) looseTexts(v *bashView, w word, at int, bare map[string]bool) (out []string, holes []bool, capped bool) {
	out, holes = []string{""}, []bool{false}
	soft := false
	for _, p := range w.parts {
		var alts []string
		partSoft := false
		switch {
		case p.kind == partLit:
			alts = []string{p.lit}
		case p.kind != partVar || p.op == "!":
			return nil, nil, false
		case p.name != "" && p.op != "~" && firmVar(v, p.name, at, bare):
			for _, val := range v.vars[p.name].vals {
				texts, known := p.of(val.text, e.home)
				if !known {
					return nil, nil, false
				}
				alts = unionStrings(alts, texts)
			}
		case v.vars[p.name] == nil && (p.name == "HOME" && e.home != "" || p.name == "PWD" && e.cur != ""):
			base := e.home
			if p.name == "PWD" {
				base = e.cur
			}
			texts, known := p.of(base, e.home)
			if !known {
				return nil, nil, false
			}
			alts = texts
		default:
			soft, partSoft = true, true
			alts = []string{p.defIn(e.home)}
			if lv := v.vars[p.name]; lv != nil {
				for _, val := range lv.vals {
					if texts, known := p.of(val.text, e.home); known {
						alts = unionStrings(alts, texts)
					}
				}
			}
		}
		if len(out)*len(alts) > maxLoose {
			return nil, nil, true
		}
		next := make([]string, 0, len(out)*len(alts))
		nextHoles := make([]bool, 0, len(out)*len(alts))
		for i, o := range out {
			for _, a := range alts {
				// A part not known stands in the last name until a known
				// one begins a name after it.
				hole := holes[i]
				switch {
				case partSoft:
					hole = true
				case strings.Contains(a, "/"):
					hole = false
				}
				next, nextHoles = append(next, o+a), append(nextHoles, hole)
			}
		}
		out, holes = next, nextHoles
	}
	if !soft {
		return nil, nil, false
	}
	if w.tilde && e.home != "" {
		// (A word of a list: "for f in ~/$X".)
		for i, t := range out {
			if t == "~" || strings.HasPrefix(t, "~/") {
				out[i] = e.home + t[1:]
			}
		}
	}
	return out, holes, false
}

// closedWord reports whether the texts filledAll gives for the word are
// all it can be where the command at index at of the line's own
// commands uses it: the line gave each variable in it every value it
// has, in ways this package follows, nothing else on the line can have
// given it another (closedVar), and one of those values is sure to be in
// place by then (sureSet). Only then is a file the word names known for
// sure. With at set to anyTime the last of these is not asked: the
// answer is then about the values, and not about when they are there.
// It is for use once the whole line is read.
func (e *Engine) closedWord(v *bashView, w word, at int) bool {
	if _, _, ok := e.filledAll(v, w); !ok {
		return false
	}
	for _, p := range w.parts {
		switch {
		case p.kind == partLit:
		case p.kind == partVar && (p.op == "!" || p.op == "~"):
			return false
		case p.kind == partVar && p.name != "" && v.vars[p.name] != nil:
			if !e.closedVar(v, p.name) || at != anyTime && !sureSet(v, p.name, at) {
				return false
			}
		case p.kind == partVar && (p.name == "HOME" && e.home != "" || p.name == "PWD" && len(v.pwds) == 1 || p.name == "IFS"):
			// As gnosh was told they are, unless the line touches them.
			if e.closedVar(v, p.name); !v.tight || v.bare[p.name] {
				return false
			}
		default:
			return false
		}
	}
	return true
}

// The places closedWord can be asked about that are not a command of
// the line's own: anyTime for the values alone, noPlace for a command
// found inside another, of which nobody knows what runs before it.
const (
	anyTime = -1
	noPlace = -2
)

// sureSet reports whether a variable is sure to hold a value the line
// gave it when the command at index at runs: an assignment that stands
// on its own, outside every group, loop, pipe and background job, comes
// before the command, and is not one that an && or an || before it may
// have skipped (unless the command hangs on the same chain of &&); or
// the command is in the body of a for loop that goes through the
// variable. (A value from "X=1 cmd", from a branch that may not be taken
// or from a subshell may not be there, and the variable is then what it
// was when the line began.)
func sureSet(v *bashView, name string, at int) bool {
	if at < 0 || v.sc.piped || v.sc.background {
		return false
	}
	// A plain sh reads "F=a &>f" another way than bash: it puts the
	// assignment in the background. Nothing is sure on such a line.
	if strings.Contains(v.text, "&>") {
		return false
	}
	for _, l := range v.sc.loops {
		if l.name == name && !l.args && l.from <= at && at < l.to {
			return true
		}
	}
	for i, c := range v.sc.cmds {
		if i >= at {
			break
		}
		if c.nest != 0 || c.bg || len(c.redirs) > 0 {
			continue
		}
		switch c.sep {
		case sepStart, sepSemi:
			// It runs, whatever came before it.
		case sepAnd:
			// It runs only when what stands before it went well. The
			// command at at knows that it did where it hangs on the same
			// chain of &&: "cd x && d=y && cat $d".
			if !andChain(v.sc.cmds, i+1, at) {
				continue
			}
		default:
			continue
		}
		for _, a := range c.assigns {
			if a.name == name && len(c.words) == 0 && !strings.HasPrefix(a.w.raw, name+"+=") {
				return true
			}
		}
		// (export and readonly are every shell's; declare is bash's. A
		// function of the same name would stand in their way.)
		if len(c.words) > 1 && !v.sc.funcs && c.words[0].static() && (c.words[0].joined() == "export" || c.words[0].joined() == "readonly") {
			for _, w := range c.words[1:] {
				if n, ok := assignName(w.raw); ok && n == name && strings.HasPrefix(w.raw, name+"=") {
					return true
				}
			}
		}
	}
	return false
}

// andChain reports whether the line's own commands from index from to
// index to, both counted, are each joined to the one before by &&, at the
// top of the line: the last of them then runs only when all the others,
// and the one before them, went well.
func andChain(cmds []*simpleCmd, from, to int) bool {
	if from > to || to >= len(cmds) {
		return false
	}
	for _, c := range cmds[from : to+1] {
		if c.sep != sepAnd || c.nest != 0 {
			return false
		}
	}
	return true
}

// closedVar reports whether the values the line gave a variable are all
// it can hold: the line was read to its end, every program in it is
// known, none of them is a file read into this shell, no builtin that
// sets variables is handed a name that is only known when the line
// runs, the field separator is left alone, and the variable's name
// stands nowhere but where it is assigned and where it is expanded
// ("read F", "printf -v F", "let F=1", "unset F" and "declare -n r=F"
// all hold the bare name).
func (e *Engine) closedVar(v *bashView, name string) bool {
	if !v.tightDone {
		v.tight, v.bare, v.tightDone = e.tightLine(v), bareNames(v), true
	}
	lv := v.vars[name]
	if !v.tight || lv == nil || len(lv.vals) == 0 || lv.open || v.bare[name] || v.bare["IFS"] || v.vars["IFS"] != nil || shellOwned[name] {
		return false
	}
	// A value made of other variables is all the variable can hold only
	// where those were known for sure when it was made ("d=x; read d;
	// w=$d": w is whatever was read).
	if v.closing[name] {
		return false // it is made of itself
	}
	if v.closing == nil {
		v.closing = map[string]bool{}
	}
	v.closing[name] = true
	defer delete(v.closing, name)
	for _, pv := range v.pending {
		if pv.name == name && !e.closedWord(v, pv.w, pv.at) {
			return false
		}
	}
	return true
}

// shellOwned are variables the shell gives a value of its own, whatever
// a line assigns to them.
var shellOwned = map[string]bool{
	"RANDOM": true, "SRANDOM": true, "SECONDS": true, "LINENO": true, "BASHPID": true, "PPID": true, "UID": true, "EUID": true,
	"GROUPS": true, "FUNCNAME": true, "PIPESTATUS": true, "BASH_COMMAND": true, "BASH_SUBSHELL": true, "EPOCHSECONDS": true,
	"EPOCHREALTIME": true, "OPTIND": true, "OPTARG": true, "REPLY": true, "_": true, "PWD": true, "OLDPWD": true, "HISTCMD": true,
	"SHLVL": true, "BASH_ARGV0": true, "BASH_REMATCH": true, "DIRSTACK": true, "BASHOPTS": true, "SHELLOPTS": true, "COLUMNS": true,
	"LINES": true, "IFS": true,
}

// tightLine is the part of closedVar that is about the whole line.
func (e *Engine) tightLine(v *bashView) bool {
	if !v.sc.ok || v.opaque != "" || v.tooMany || len(v.unread) > 0 || v.sc.blindSet {
		return false
	}
	for _, u := range v.units {
		ws := u.cmd.words
		if len(ws) == 0 {
			continue
		}
		name, known := e.progName(ws[0])
		if !known || progIsPattern(ws[0]) {
			return false
		}
		k := progKey(baseName(name))
		if k == "source" || k == "." || k == "enable" || k == "alias" {
			// A file read into this shell sets what it likes, and an
			// alias makes another command of the words that follow it.
			return false
		}
		if evaluators[k] || declares[k] || k == "mapfile" || k == "readarray" {
			for _, w := range ws[1:] {
				if _, _, ok := e.filledAll(v, w); !w.static() && !ok {
					return false
				}
			}
		}
	}
	return true
}

// bareNames returns every name that stands on the line as text of its
// own: not as the variable an assignment sets, and not as one that is
// expanded ($name, ${name}).
func bareNames(v *bashView) map[string]bool {
	out := map[string]bool{}
	note := func(text string) {
		for i := 0; i < len(text); {
			if !isNameStart(text[i]) {
				i++
				continue
			}
			j := i
			for j < len(text) && isNameChar(text[j]) {
				j++
			}
			out[text[i:j]] = true
			i = j
		}
	}
	word := func(w word, assigns bool) {
		text := w.raw
		if w.static() {
			text = w.joined()
		}
		if name, ok := assignName(w.raw); ok && assigns {
			if _, val, cut := strings.Cut(text, "="); cut && strings.HasPrefix(text, name) {
				text = val
			}
		}
		if !w.static() {
			text = lessExpansions(text)
		}
		// A flag's own letters are not a name ("[ -f x ]"); what is
		// written onto it may be ("printf -vF x", "--var=F").
		switch {
		case strings.HasPrefix(text, "--"):
			_, text, _ = strings.Cut(text, "=")
		case strings.HasPrefix(text, "-") && len(text) > 2:
			text = text[2:]
		case strings.HasPrefix(text, "-"):
			text = ""
		}
		note(text)
	}
	for _, w := range v.sc.loose {
		word(w, false)
	}
	for _, a := range v.sc.arith {
		note(lessExpansions(a))
	}
	for _, u := range v.units {
		c := u.cmd
		for _, a := range c.assigns {
			word(a.w, true)
			for _, el := range a.elems {
				word(el, false)
			}
		}
		for i, w := range c.words {
			if i > 0 {
				word(w, !c.words[0].static() || declares[c.words[0].joined()])
			}
		}
		for _, r := range c.redirs {
			word(r.target, false)
		}
	}
	return out
}

// lessExpansions returns shell text with each $name and ${name taken
// out: what is left of a name there is text of its own.
func lessExpansions(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] != '$' {
			b.WriteByte(s[i])
			continue
		}
		j := i + 1
		if j < len(s) && s[j] == '{' {
			j++
			if j < len(s) && (s[j] == '#' || s[j] == '!') {
				j++
			}
		}
		k := j
		for k < len(s) && isNameChar(s[k]) {
			k++
		}
		if k == j {
			b.WriteByte(s[i])
			continue
		}
		b.WriteByte(' ')
		i = k - 1
	}
	return b.String()
}

// filledAll returns every text the word may be once the variables the
// line itself set are filled in, when that makes the whole word known.
// strict says there is one such text and no other: each variable was
// given one value, known ahead of time.
func (e *Engine) filledAll(v *bashView, w word) (out []lineVal, strict, ok bool) {
	return e.filledWith(v, w, nil)
}

// braced reports whether bash's braces may make several words of the
// word: it holds an unquoted brace, and a { among what is written of it
// with a } somewhere after it. A brace with nothing to close it opens
// nothing: in "${a[*]}x}" the } is a character like any, and so is the {
// in "$@"{, which leaves .git standing alone among the words of "f() {
// mv "$@"{; }; f .git /tmp/g" (oracle seed 117).
func braced(w word) bool {
	if !w.brace {
		return false
	}
	s := w.joined()
	open := strings.Index(s, "{")
	return open >= 0 && strings.Contains(s[open+1:], "}")
}

// filledWith is filledAll with a way to say what a pattern matched.
// A variable that went through the matches of a pattern ("for f in
// *.txt") is read by filledAll as the pattern, which serves where the
// variable is all there is to a word: the word is then that pattern.
// With more text around it, it is not: "new_$f" is new_a.txt, a name
// the pattern new_*.txt may match nothing of. Given matches, every such
// value is read as each name it says the pattern matched (and as the
// pattern's own text, which is what a pattern that matches nothing
// leaves): the texts are then names, and none of them a pattern.
func (e *Engine) filledWith(v *bashView, w word, matches func(pattern string) ([]string, bool)) (out []lineVal, strict, ok bool) {
	out, strict, ok, _ = e.filledWhy(v, w, matches)
	return out, strict, ok
}

// filledWhy is filledWith, and says of a word it gave nothing for whether
// that was because the line gives its variables more values than
// maxFilled ways of choosing among them (many), and not because one of
// them is not known. A target like that is a doubt, not something nobody
// knows: the values are on the line.
func (e *Engine) filledWhy(v *bashView, w word, matches func(pattern string) ([]string, bool)) (out []lineVal, strict, ok, many bool) {
	if w.static() || braced(w) || w.eqHead {
		return nil, false, false, false
	}
	// A ~ at the head of the word is the home directory where a slash
	// follows it ("~/$d/x"), and stays a ~ where a variable does
	// ("~$d"). Somebody else's home (~name) is not known.
	home := false
	if w.tilde {
		first := ""
		if w.parts[0].kind == partLit {
			first = w.parts[0].lit
		}
		switch {
		case first == "~" && len(w.parts) > 1:
		case strings.HasPrefix(first, "~/") && e.home != "":
			home = true
		default:
			return nil, false, false, false
		}
	}
	defer func() {
		if home {
			for i := range out {
				out[i].text = e.home + out[i].text[1:]
			}
		}
	}()
	out, strict = []lineVal{{}}, true
	for _, p := range w.parts {
		var alts []lineVal
		switch {
		case p.kind == partLit:
			alts = []lineVal{{text: p.lit}}
		case p.kind == partVar && p.op == "!":
			// ${!name} is the variable that name's value names: "v=HOME;
			// rm -rf ${!v}" is the home directory.
			lv := v.vars[p.name]
			if lv == nil || len(lv.vals) == 0 {
				return nil, false, false, false
			}
			strict = false
			for _, val := range lv.vals {
				to := v.vars[val.text]
				switch {
				case val.text == "HOME" && to == nil && e.home != "":
					alts = append(alts, lineVal{text: e.home})
				case val.text == "PWD" && to == nil:
					for _, d := range append([]string{e.cur}, v.pwds...) {
						alts = append(alts, lineVal{text: d})
					}
				case to != nil && len(to.vals) > 0:
				vals:
					for _, x := range to.vals {
						for _, have := range alts {
							if have == x {
								continue vals
							}
						}
						alts = append(alts, x)
					}
				}
				// (A value that names no variable gnosh knows of is one
				// more thing the form may stand for, and nothing to try.)
			}
			if len(alts) == 0 {
				return nil, false, false, false
			}
		case p.kind == partVar && p.name != "":
			var vals []lineVal
			lv := v.vars[p.name]
			switch {
			case lv != nil && len(lv.vals) > 0:
				vals = lv.vals
				strict = strict && len(lv.vals) == 1 && !lv.open && !lv.vals[0].glob
			case lv != nil:
				return nil, false, false, false
			case p.name == "HOME" && e.home != "":
				vals = []lineVal{{text: e.home}}
			case p.name == "PWD" && len(v.pwds) > 0:
				// (After a cd it is wherever the line may be by then.)
				for _, d := range v.pwds {
					vals = append(vals, lineVal{text: d})
				}
			case p.name == "PWD":
				vals = []lineVal{{text: e.cur}}
			case p.name == "IFS":
				// A shell starts with a blank, a tab and a line break in
				// it, whatever its environment holds: "rm${IFS}-rf${IFS}x"
				// is rm -rf x.
				vals = []lineVal{{text: " \t\n"}}
			case p.name == "OLDPWD" && v.moves && len(v.pwds) > 0:
				// After a cd it is a directory the line was in before.
				for _, d := range v.pwds {
					vals = append(vals, lineVal{text: d})
				}
				strict = false
			default:
				return nil, false, false, false
			}
			// What the form makes of each value: ${v:+word} is the word,
			// ${v%.txt} what is left of the value.
			for _, val := range vals {
				names, glob := []string{val.text}, val.glob
				if val.glob && matches != nil {
					ms, known := matches(val.text)
					if !known || len(ms) > maxFilled {
						return nil, false, false, known
					}
					names, glob = append(names, ms...), false
				}
				for _, name := range names {
					texts, known := p.of(name, e.home)
					if !known {
						return nil, false, false, false
					}
				texts:
					for _, t := range texts {
						for _, have := range alts {
							if have.text == t && have.glob == glob && have.loose == val.loose && have.hole == val.hole {
								continue texts
							}
						}
						alts = append(alts, lineVal{text: t, glob: glob, loose: val.loose, hole: val.hole})
					}
				}
			}
			strict = strict && len(alts) == 1
		default:
			return nil, false, false, false
		}
		if len(out)*len(alts) > maxFilled {
			return nil, false, false, true
		}
		next := make([]lineVal, 0, len(out)*len(alts))
		for _, o := range out {
			for _, a := range alts {
				// A hole stays in the last name until a name begins after it.
				hole := a.hole || o.hole && !strings.Contains(a.text, "/")
				next = append(next, lineVal{text: o.text + a.text, glob: o.glob || a.glob, loose: o.loose || a.loose, hole: hole})
			}
		}
		out = next
	}
	return out, strict, true, false
}

// partFills returns a word that holds both variables the line itself set
// and something that is only known when the line runs, with each of the
// line's own variables written out as each value it was given: "$PREFIX$d"
// after "d=~" is $PREFIX and the home directory. filledAll gives nothing
// for such a word, because not all of it is known; what is known of it
// still says what it may name. A variable that is not sure to hold one of
// those values where the command at index at stands (closedVar, sureSet)
// is also left as it is written, for whatever else it may hold. It
// returns nothing for a word with none of the line's variables in it,
// and capped when there are more such words than gnosh follows.
func (e *Engine) partFills(v *bashView, w word, at int) (out []word, capped bool) {
	if w.static() || braced(w) || w.eqHead {
		return nil, false
	}
	own := false
	outs := [][]wordPart{nil}
	for _, p := range w.parts {
		alts := [][]wordPart{{p}}
		if lv := v.vars[p.name]; p.kind == partVar && p.name != "" && p.op != "!" && lv != nil && len(lv.vals) > 0 {
			own = true
			alts = nil
			for _, val := range lv.vals {
				texts, known := p.of(val.text, e.home)
				if !known {
					continue
				}
				for _, t := range texts {
					alts = append(alts, []wordPart{{lit: t}})
				}
			}
			if len(alts) == 0 || p.op == "~" || !e.closedVar(v, p.name) || at == anyTime || !sureSet(v, p.name, at) {
				alts = append(alts, []wordPart{p})
			}
		}
		if len(outs)*len(alts) > maxLoose {
			return nil, true
		}
		next := make([][]wordPart, 0, len(outs)*len(alts))
		for _, o := range outs {
			for _, a := range alts {
				next = append(next, append(append([]wordPart(nil), o...), a...))
			}
		}
		outs = next
	}
	if !own {
		return nil, false
	}
	for _, parts := range outs {
		pw := w
		pw.parts = parts
		// (A pattern in a value that nothing quotes is one: "w=.*; rm -rf
		// $X$w" removes what .* matches.)
		for _, p := range parts {
			pw.glob = pw.glob || !w.quoted && p.kind == partLit && hasPathMagic(p.lit)
		}
		if !pw.static() {
			out = append(out, pw)
		}
	}
	return out, false
}

// mixesPattern reports whether a word holds a variable that went through
// the matches of a pattern together with something that makes another
// text of them: more text before or after it, or a form that cuts a
// piece off its value (${f%.txt}).
func mixesPattern(v *bashView, w word) bool {
	pattern, parts, cut := false, 0, false
	for _, p := range w.parts {
		if p.kind == partLit && p.lit == "" {
			continue
		}
		parts++
		if p.kind != partVar || p.name == "" {
			continue
		}
		lv := v.vars[p.name]
		if lv == nil {
			continue
		}
		for _, val := range lv.vals {
			if val.glob {
				pattern = true
				cut = cut || !p.plainly()
			}
		}
	}
	return pattern && (parts > 1 || cut)
}

// fields cuts a value that nothing quotes into words, as the shell
// does: at blanks, tabs and line breaks, and at whatever else the line
// puts in IFS ("IFS=_; x=rm_-rf_y; $x" is rm -rf y). Where the line set
// IFS the shell cuts at fewer places than this; more words than it makes
// are more to hold against the rules, never fewer.
func (e *Engine) fields(v *bashView, s string) []string {
	seps := " \t\n"
	if lv := v.vars["IFS"]; lv != nil {
		for _, val := range lv.vals {
			seps += val.text
		}
	}
	return strings.FieldsFunc(s, func(r rune) bool { return strings.ContainsRune(seps, r) })
}

// tooManyFills reports whether filledAll gave up on a word because the
// line gives its variables more values than maxFilled ways of choosing
// among them, and not because one of them is not known.
func (e *Engine) tooManyFills(v *bashView, w word) bool {
	if w.static() || braced(w) || w.eqHead {
		return false
	}
	// (What filledAll itself ran into: the count below does not follow a
	// ${!name} to the variables that name's values name, where the ways
	// multiply. "d=HOME; w=$d/; declare -n w=d; rm -rf ${!d}" gave d and
	// HOME some thirty values each.)
	if _, _, ok, many := e.filledWhy(v, w, nil); !ok && many {
		return true
	}
	ways := 1
	for _, p := range w.parts {
		if p.kind != partVar || p.name == "" {
			continue
		}
		lv := v.vars[p.name]
		if lv != nil && lv.capped {
			return true
		}
		if lv == nil || len(lv.vals) == 0 {
			continue
		}
		n := 0
		for _, val := range lv.vals {
			texts, _ := p.of(val.text, e.home)
			n += len(texts)
		}
		if n == 0 {
			n = 1
		}
		if ways *= n; ways > maxFilled {
			return true
		}
	}
	return false
}

// filled returns the word's text with every variable the line itself set
// filled in, when that makes the whole word known and there is one text
// it can be.
func (e *Engine) filled(v *bashView, w word) (string, bool) {
	if w.glob {
		return "", false
	}
	vals, strict, ok := e.filledAll(v, w)
	if !ok || !strict {
		return "", false
	}
	return vals[0].text, true
}

// varBraces returns a command's words as bash and as dash read a word
// that holds braces and a variable: bash's with the braces taken apart
// (braceAlts; nil when they cannot be told), and dash's with the braces
// left as characters, which the variable is then filled in beside. ok is
// false when no word is such a word.
func varBraces(ws []word) (bashWords, dashWords []word, ok bool) {
	bashKnown := true
	for _, w := range ws {
		if !braced(w) || w.static() {
			bashWords = append(bashWords, w)
			dashWords = append(dashWords, w)
			continue
		}
		ok = true
		lit := w
		lit.brace = false
		dashWords = append(dashWords, lit)
		if alts, fine := braceAlts(w); fine {
			bashWords = append(bashWords, alts...)
		} else {
			bashKnown = false
		}
	}
	if !ok {
		return nil, nil, false
	}
	if !bashKnown {
		bashWords = nil
	}
	return bashWords, dashWords, true
}

// braceWords returns the command's words after bash's brace expansion,
// when some word holds braces that expand. sure is false when a word
// with braces also has quoted text, whose commas and braces count for
// nothing to the shell but cannot be told apart here.
func braceWords(ws []word) (out []word, sure, ok bool) {
	sure = true
	for _, w := range ws {
		if !w.brace || !w.static() {
			out = append(out, w)
			continue
		}
		alts, fine := braceExpand(w.joined())
		if !fine || len(alts) == 1 && alts[0] == w.joined() {
			out = append(out, w)
			continue
		}
		ok = true
		sure = sure && !w.quoted
		for _, alt := range alts {
			if alt == "" && !w.quoted {
				continue // an empty word that nothing quotes is no word
			}
			part := litOf(alt)
			part.glob, part.tilde, part.quoted = w.glob && hasPathMagic(alt), strings.HasPrefix(alt, "~") && !w.quoted, w.quoted
			out = append(out, part)
		}
	}
	if len(out) == 0 {
		return nil, false, false
	}
	return out, sure, ok
}

// filledArgs returns the command with each argument that the line's own
// variables make known replaced by its value: one word when the
// argument was quoted, and otherwise as many as the value has. Where
// the line gives a variable several values (a for loop's list), there
// is one command for each way of choosing among them, up to
// maxFilledWays; past that, one command that holds them all, which names every file and
// every flag the others would. That last one is also what "${a[@]}"
// makes of an array, each of its words a word of the command ("a=(-rf
// x); rm "${a[@]}"" is rm -rf x), so a command with such an argument is
// read that way too. It returns nothing when no argument changes.
//
// A value that is only what is left when a variable is empty (see
// lineVal) is not written into a command here: the removal check and the
// rules about files try it by itself.
//
// every says some argument was every word of an array: the values of an
// array are kept as a set, so how many words there are and in which
// order they stand is then not known for sure ("a=(/tmp/g .git /tmp/g);
// mv "${a[@]}"" moves .git, which the set has last).
func (e *Engine) filledArgs(v *bashView, ws []word) (alts [][]word, every bool) {
	per := make([][][]word, len(ws)) // for each word, the words it may stand for
	per[0] = [][]word{{ws[0]}}
	changed, ways := false, 1
	for i, w := range ws[1:] {
		vals, _, ok := e.filledAll(v, w)
		if ok && len(w.parts) > 1 {
			// Text written beside every word of an array joins the first
			// and the last of them, and the ones between stand alone:
			// "${a[@]}.bak" of (x .git /tmp/g) is x, .git and /tmp/g.bak.
			// So each word is also read without that text.
			for _, p := range w.parts {
				if p.kind == partVar && p.all {
					bare := word{raw: w.raw, parts: []wordPart{p}, quoted: w.quoted}
					if more, _, ok := e.filledAll(v, bare); ok {
						vals = append(append([]lineVal(nil), vals...), more...)
					}
				}
			}
		}
		if ok {
			firm := vals[:0:0]
			for _, val := range vals {
				if !val.loose {
					firm = append(firm, val)
				}
			}
			vals = firm
		}
		// ($1 and its kin are any of the words a function or a script was
		// handed, like the words of an array: see giveArgs.)
		whole := false
		for _, p := range w.parts {
			whole = whole || p.kind == partVar && (p.all || len(p.name) == 1 && p.name[0] >= '0' && p.name[0] <= '9')
		}
		// (A word with a pattern in it is left as it is written, unless
		// it is every word of an array: those are words of their own
		// whatever is written beside them.)
		if !ok || w.glob && !whole || len(vals) == 0 {
			per[i+1] = [][]word{{w}}
			continue
		}
		changed = true
		every = every || whole
		if seq := e.arraySeq(v, w); len(seq) > 0 {
			// Every word of an array with text written beside it, as the
			// shell makes it where the array was given its words once.
			per[i+1] = append(per[i+1], seq)
		}
		for _, val := range vals {
			var alt []word
			if w.quoted {
				part := litOf(val.text)
				part.glob, part.anyOrder = val.glob && hasPathMagic(val.text), whole
				alt = append(alt, part)
			} else {
				// Not quoted: the shell cuts the value into words and
				// matches the patterns in them.
				for _, f := range e.fields(v, val.text) {
					part := litOf(f)
					part.glob, part.anyOrder = hasPathMagic(f), whole
					alt = append(alt, part)
				}
			}
			per[i+1] = append(per[i+1], alt)
		}
	}
	if !changed {
		return nil, false
	}
	for _, alts := range per {
		if ways *= len(alts); ways > maxFilledWays {
			ways = maxFilledWays + 1
		}
	}
	var out [][]word
	if ways > maxFilledWays || every && ways > 1 {
		var all []word
		for _, alts := range per {
			for _, alt := range alts {
				all = append(all, alt...)
			}
		}
		if len(all) > 2048 {
			return nil, false
		}
		// A "--" in it is one way's word, and may be no other way's: read
		// as the end of the options it would hide the flags after it from
		// the guards. So the command is read without it as well.
		alls := [][]word{all}
		if bare := withoutEndOfOptions(all); len(bare) < len(all) {
			alls = append(alls, bare)
		}
		if ways > maxFilledWays {
			return alls, every
		}
		out = append(out, alls...)
	}
	each := [][]word{nil}
	for _, alts := range per {
		var next [][]word
		for _, o := range each {
			for _, alt := range alts {
				next = append(next, append(append([]word(nil), o...), alt...))
			}
		}
		each = next
	}
	// Ways of choosing that make the same command are one command: the
	// three ways "echo $1 $2 $3" is echo x, where one of the three is x
	// and the others are empty.
	seen := map[string]bool{}
	for _, ws := range each {
		var key strings.Builder
		for _, w := range ws {
			key.WriteString(w.joined())
			if w.glob {
				key.WriteString("\x01")
			}
			key.WriteString("\x00")
		}
		if !seen[key.String()] {
			seen[key.String()] = true
			out = append(out, ws)
		}
	}
	return out, every
}

// withoutEndOfOptions is ws less each word that is "--" and nothing else.
func withoutEndOfOptions(ws []word) []word {
	out := make([]word, 0, len(ws))
	for _, w := range ws {
		if !w.glob && w.joined() == "--" {
			continue
		}
		out = append(out, w)
	}
	return out
}

// maxFilledWays bounds how many commands filledArgs makes of one, one for
// each way of choosing among its variables' values; past it, it makes the
// one that holds them all. A deny rule names a command exactly, so the
// ways are followed as far as is cheap: "f() { echo $1 $2 $3; }; f x" is
// eight ways, three of them echo x, which a rule against echo x must meet
// (oracle seed 101).
const maxFilledWays = 64

// spreadWords returns a command's words as they stand where each default
// (${name:-text}) or alternative (${name:+text}) that is written with
// blanks, outside double quotes, is what its ${...} stands for. The shell
// cuts such a text into words at its blanks, with what is written before
// it joined to the first of them and what is written after it to the
// last: "mv ${X:-.git /tmp/g} /tmp" is mv .git /tmp/g /tmp where X is not
// set, "rm -rf ${HOME:+. x}" is rm -rf . x, and "${X:-rm -rf} ." is rm -rf
// "." too. Read as one word, such a text names a file nobody has. ok is
// false when no word of the command is cut that way.
func (e *Engine) spreadWords(v *bashView, ws []word) (out []word, ok bool) {
	for _, w := range ws {
		fields, cut := e.spread(v, w)
		if !cut {
			out = append(out, w)
			continue
		}
		ok = true
		out = append(out, fields...)
	}
	if !ok {
		return nil, false
	}
	return out, true
}

// spread cuts one word as spreadWords says. cut is false for a word that
// holds no such text, and for one with a part whose text cannot be
// written out again (what a command prints, arithmetic, the other forms
// of ${...}).
func (e *Engine) spread(v *bashView, w word) (out []word, cut bool) {
	// text is what a ${...} with a default or an alternative stands for.
	text := func(p wordPart) string {
		if p.hasAlt {
			return p.alt
		}
		return p.def
	}
	blank := func(s string) bool { return len(e.fields(v, s)) > 1 || s != strings.Join(e.fields(v, s), "") }
	for _, p := range w.parts {
		switch {
		case p.kind == partLit:
		case p.kind == partVar && (p.hasDef || p.hasAlt):
			cut = cut || p.bare && blank(text(p))
		case p.kind == partVar && p.op == "" && !p.all && p.name != "":
		default:
			return nil, false
		}
	}
	if !cut {
		return nil, false
	}
	var cur word
	open := false
	flush := func() {
		if open {
			cur.quoted = w.quoted
			out = append(out, cur)
		}
		cur, open = word{}, false
	}
	add := func(s string, pattern bool) {
		if s == "" {
			return
		}
		cur.parts = append(cur.parts, wordPart{kind: partLit, lit: s})
		cur.raw += s
		cur.glob = cur.glob || pattern && hasPathMagic(s)
		open = true
	}
	for i, p := range w.parts {
		switch {
		case p.kind == partLit:
			if i == 0 && w.tilde {
				cur.tilde = true
			}
			add(p.lit, w.glob)
		case p.hasDef || p.hasAlt:
			s := text(p)
			tilde := s == "~" || strings.HasPrefix(s, "~/")
			if !p.bare || !blank(s) {
				cur.tilde = cur.tilde || !open && tilde
				add(s, p.bare)
				continue
			}
			// Blanks at its ends cut the text off from what is written
			// beside it.
			fields := e.fields(v, s)
			if len(fields) == 0 || !strings.HasPrefix(s, fields[0]) {
				flush()
			}
			for k, f := range fields {
				if k > 0 {
					flush()
				}
				cur.tilde = cur.tilde || !open && k == 0 && tilde
				add(f, true)
			}
			if len(fields) > 0 && !strings.HasSuffix(s, fields[len(fields)-1]) {
				flush()
			}
		default:
			// A plain $name: what the line gave it is filled in later.
			cur.parts = append(cur.parts, p)
			cur.raw += "${" + p.name + "}"
			open = true
		}
	}
	flush()
	return out, true
}

// arraySeq returns the words the shell makes of a word that is every
// word of an array with plain text written before or after it: the text
// before joins the first of them and the text after the last ("${a[@]}*"
// of (clean -fdx) is clean and -fdx*). The words are taken in the order
// the line gave them, which is the array's own where it was given its
// words in one go; filledArgs reads them in other ways too.
func (e *Engine) arraySeq(v *bashView, w word) []word {
	at := -1
	for i, p := range w.parts {
		switch {
		case p.kind == partVar && p.all && at < 0:
			at = i
		case p.kind != partLit:
			return nil
		}
	}
	if at < 0 || len(w.parts) < 2 {
		return nil
	}
	vals, _, ok := e.filledAll(v, word{raw: w.raw, parts: []wordPart{w.parts[at]}, quoted: w.quoted})
	if !ok || len(vals) == 0 || len(vals) > maxFilled {
		return nil
	}
	before, after := "", ""
	for i, p := range w.parts {
		if i < at {
			before += p.lit
		} else if i > at {
			after += p.lit
		}
	}
	var out []word
	for i, val := range vals {
		if val.loose {
			return nil
		}
		text := val.text
		if i == 0 {
			text = before + text
		}
		if i == len(vals)-1 {
			text += after
		}
		part := litOf(text)
		part.glob, part.anyOrder = !w.quoted && hasPathMagic(text) || w.glob && hasPathMagic(text), true
		out = append(out, part)
	}
	return out
}

// place returns where a command stands among the line's own, or noPlace
// for one found inside another: only for the line's own is it known
// which assignments come before it.
func (v *bashView) place(c *simpleCmd) int {
	if v.sc == nil {
		return noPlace
	}
	if v.index == nil {
		v.index = map[*simpleCmd]int{}
		for i, own := range v.sc.cmds {
			v.index[own] = i
		}
	}
	if i, ok := v.index[c]; ok {
		return i
	}
	return noPlace
}

// setOpaque records the first thing that could not be read.
func (v *bashView) setOpaque(why string) {
	if v.opaque == "" {
		v.opaque = why
	}
}

// How addScript takes a piece of text.
const (
	// scriptText: the text is a script; every command in it runs.
	scriptText = iota
	// scriptMaybe: the text may be run by a shell that reads it another
	// way (the inside of (( )) under dash). Its commands are held against
	// the rules; that it cannot be read means nothing.
	scriptMaybe
	// scriptOperand: the text is an operand a builtin evaluates. Only the
	// commands in its substitutions run; if it cannot be read, what runs
	// is not known.
	scriptOperand
	// scriptData: the text is data that the shell may yet evaluate (a
	// quoted word that looks like a substitution). Only the commands in
	// its substitutions count, and that it cannot be read means nothing.
	scriptData
	// scriptValue: the text is a value the line gives a variable, which
	// the shell may evaluate where the variable is used as a number, as
	// a prompt or as a name (${x@P}, $((x)), ${!x}). Like scriptData,
	// except that a value which cannot be read is one of which nobody
	// knows what an evaluation runs: bash makes "tee pwne" of the
	// unfinished "$(tee pwned".
	scriptValue
	// scriptArith: the inside of (( )), as a shell without that form
	// would run it. Like scriptMaybe, and its words are taken as they
	// are written: the * of a product is not a file name pattern worth
	// asking about.
	scriptArith
)

// addScript reads text that a command hands to a shell, or that the
// shell may evaluate, and adds the commands it runs.
func (e *Engine) addScript(v *bashView, text string, nest nesting, depth int, how int) {
	sub := parseScript(text)
	if !sub.ok {
		crude := crudeFragments(text)
		switch how {
		case scriptText, scriptOperand, scriptValue:
			v.setOpaque("a script inside the command could not be read: " + sub.why)
			v.unread = append(v.unread, crude...)
		case scriptArith:
			// A plain sh runs the text of (( )) as commands, and what it
			// runs cannot be told when that text cannot be read as
			// commands: "(( <<$X git * ))" ran git under dash, with its
			// here-document ended by the end of the line (oracle seed
			// 162). Then the guards cannot be checked against it.
			v.setOpaque("the text of (( )), which a plain sh runs as commands, could not be read as commands: " + sub.why)
			v.unread = append(v.unread, crude...)
		}
		v.frags = append(v.frags, crude...)
	}
	e.noteScript(v, sub, false)
	if sub.subst && (how == scriptText || how == scriptOperand) {
		// A substitution in an operand a builtin evaluates is a
		// substitution: "test -v 'a[$(cmd)]'" runs cmd, so no rule for
		// test lets the line run unasked.
		v.sc.subst = true
	}
	var prev *simpleCmd
	for _, c := range sub.cmds {
		if (how == scriptOperand || how == scriptData || how == scriptValue) && c.nest&nestSubst == 0 {
			continue
		}
		if how == scriptArith {
			// The first word is taken as it is written (total[i] is an
			// array's element), with a note that a plain sh would take it
			// for a pattern; the * of a product stays what it is to that
			// sh, something that turns into file names.
			if len(c.words) > 0 && (c.words[0].glob || c.words[0].brace) {
				c.nameGlob = c.words[0].glob
				c.words[0].glob, c.words[0].brace = false, false
			}
			// "(( $n > 0 ))" names no program to bash, and to a shell
			// that would run it as a command it names whatever n holds.
			// That counts where the line itself set n, and not otherwise:
			// arithmetic on a variable is too common to ask about.
			// (A name that is part text, or what a command prints, is kept:
			// "(( r${x}m y ))" and "(( $(echo rm) y ))" run rm under sh.)
			// Where n holds nothing, that shell takes the next word for
			// the program: "(( $n rm y ))" runs rm y.
			if len(c.words) > 0 && !c.words[0].static() {
				name, ok := e.filled(v, c.words[0])
				switch {
				case ok && name != "":
					c.words[0] = litOf(name)
				case onlyVars(c.words[0]):
					for len(c.words) > 0 && !c.words[0].static() && onlyVars(c.words[0]) {
						c.words = c.words[1:]
					}
					if len(c.words) == 0 {
						continue
					}
					if c.words[0].glob || c.words[0].brace {
						c.nameGlob = c.words[0].glob
						c.words[0].glob, c.words[0].brace = false, false
					}
					// (An == there is a word like any other to that sh.)
					c.words[0].eqHead = false
				}
			}
		}
		c.nest |= nest | nestScript
		e.addDerived(v, c, prev, depth)
		prev = c
	}
	for _, a := range sub.arith {
		e.addScript(v, a, nest|nestSubshell, depth+1, scriptArith)
	}
}

// shiftWord finds a shift in a line: what moves the numbered words.
var shiftWord = regexp.MustCompile(`(^|[^A-Za-z0-9_])shift([^A-Za-z0-9_]|$)`)

// positionals are the names a script's or a function's own words go by.
var positionals = []string{"0", "1", "2", "3", "4", "5", "6", "7", "8", "9", "@", "*"}

// giveArgs records the words a function of the line's own, a "set --" or
// a script after sh -c is handed: what $1, $2, "$@" and their kin stand
// for inside it. Which word is which number is not followed (a shift
// moves them), so each of those names may be any of the words: "f() {
// rm -rf "$1"; }; f ~" removes the home directory, and so does "sh -c
// 'rm -rf "$0"' ~". A word with a variable nobody set in it gives what is
// left of it when that variable is empty, with the line's own variables
// written out: "f() { mv "$@"; }; f .$git /tmp/g" moves the working
// directory. A word with a command's output in it gives them nothing, and
// they are never known for sure.
//
// zero says the first of the words is the script's $0, which "$@" and $1
// leave out.
func (e *Engine) giveArgs(v *bashView, ws []word, zero bool) {
	for i, w := range ws {
		var vals []lineVal
		if w.static() {
			vals = []lineVal{{text: w.value(e.home), glob: w.glob && !w.quoted}}
		} else if got, _, ok := e.filledAll(v, w); ok {
			vals = got
		} else if allLitOrVar(w) {
			readings := []word{w}
			if parts, capped := e.partFills(v, w, anyTime); !capped {
				readings = append(readings, parts...)
			}
			for _, r := range readings {
				if l := e.unsetValue(r); l != "" {
					vals = append(vals, lineVal{text: l, glob: r.glob && !r.quoted})
				}
			}
		}
		for _, val := range vals {
			for _, name := range positionals {
				if zero && (i == 0) != (name == "0") {
					continue
				}
				v.give(name, val)
			}
		}
	}
	// A number the call hands no word to is unset: unquoted, it stands
	// for nothing. "f() { echo $1 $2 $3; }; f x" runs echo x (oracle seed
	// 101). Where the call's words are written out, the numbers past them
	// are unset, and only those: "f() { rm -rf "$1/build"; }; f proj" is
	// proj/build. From a word that may make no word at all ("f $X a")
	// every number on may be unset, and a shift anywhere in the line
	// moves them all.
	sure := 0
	for _, w := range ws {
		if !w.static() {
			break
		}
		sure++
	}
	if zero && sure > 0 {
		sure-- // (the first word is $0)
	}
	if shiftWord.MatchString(v.text) {
		sure = 0
	}
	for _, name := range positionals {
		n, err := strconv.Atoi(name)
		switch {
		case name == "0":
		case err != nil && sure == 0, err == nil && n > sure:
			v.give(name, lineVal{})
		}
	}
	for _, name := range positionals {
		v.openVar(name)
	}
}

// useAlias reads a command whose first word is an alias the line made as
// what a shell that expands aliases runs: the last command of what the
// alias stands for, with the command's other words after it. dash
// expands aliases whenever it is handed a line, and bash where it was
// told to (shopt -s expand_aliases, POSIX mode). An alias that stands for
// nothing leaves the next word as the program: "alias z=" and then "z rm
// -rf x" is rm -rf x. A name is taken for its alias once in a chain, as
// the shells do, so that "alias ls='ls -l'" ends.
func (e *Engine) useAlias(v *bashView, c *simpleCmd, prev *simpleCmd, depth int) {
	if !c.words[0].static() {
		return
	}
	name := c.words[0].joined()
	vals := v.aliases[name]
	for _, seen := range c.aliased {
		if seen == name {
			return
		}
	}
	for _, val := range vals {
		// The shell reads the alias's text and then the words after the
		// alias, as more of the line: most often more words of the text's
		// last command, but after a ; a command of their own.
		ws, redirs := c.words[1:], c.redirs
		if strings.TrimSpace(val) != "" {
			lc, at, why := landing(val)
			switch {
			case why != "":
				v.setOpaque("an alias stands for text gnosh cannot read with the words after it: " + why)
				continue
			case lc == nil:
				continue // the words after the alias fall after a #
			}
			ws = append(append(append([]word(nil), lc.words[:at]...), c.words[1:]...), lc.words[at+1:]...)
			redirs = append(append([]redir(nil), lc.redirs...), c.redirs...)
		}
		if len(ws) == 0 {
			continue
		}
		e.addDerived(v, &simpleCmd{words: ws, redirs: redirs, sep: c.sep, nest: c.nest | nestScript,
			aliased: append(append([]string(nil), c.aliased...), name)}, prev, depth)
		if strings.TrimSpace(val) == "" {
			// After an alias of blanks only, dash reads the next word as
			// a command name that may be an alias, this same one
			// included: that word was typed, not made by the alias. With
			// "alias zz=", "zz zz rm x" runs rm x there. (bash checks no
			// further.) Both are read.
			e.addDerived(v, &simpleCmd{words: ws, redirs: redirs, sep: c.sep, nest: c.nest | nestScript,
				aliased: append([]string(nil), c.aliased...)}, prev, depth)
		}
	}
}

// landing reads text with a marker word after it, as the shell reads text
// with more words after it (an alias's, the command of mapfile -C), and
// says where those words land: in which command, at which of its words.
// It returns no command when they land nowhere (after a #), and a reason
// when the text cannot be read with them, or they land where they are not
// words of a command: a file to write, a variable's value, or joined on to
// the text's last word.
func landing(text string) (c *simpleCmd, at int, why string) {
	sub := parseScript(text + " " + calledMarker)
	if !sub.ok {
		return nil, 0, "it does not read as a whole"
	}
	for _, c := range sub.cmds {
		for _, r := range c.redirs {
			if strings.Contains(r.target.raw, calledMarker) || strings.Contains(r.body, calledMarker) {
				return nil, 0, "the first of the words names a file"
			}
		}
		for _, a := range c.assigns {
			if strings.Contains(a.w.raw, calledMarker) {
				return nil, 0, "the words are a variable's value"
			}
		}
		for i, w := range c.words {
			if !strings.Contains(w.raw, calledMarker) {
				continue
			}
			if !w.static() || w.lit() != calledMarker || c.nest != 0 {
				return nil, 0, "the words join on to it"
			}
			return c, i, ""
		}
	}
	return nil, 0, ""
}

// calledMarker stands for the words a builtin puts after the text it runs,
// so that reading the two together shows where those words land.
const calledMarker = "__gnosh_called_words__"

// addCalled adds what a text runs when the shell reads it with more words
// after it than it holds: what an alias stands for (the words that follow
// the alias where it is used), the command of mapfile -C (the index and
// the line that was read) and of compgen -C (compgen, the word being
// completed and the one before it). then are those words, as far as they
// are known; a word nobody knows is the one that stands for what xargs
// adds.
//
// The shell puts a blank between the text and the words and reads the
// whole as a script. Most often the words are more words of the text's
// last command ("alias r='rm -rf'" is an rm -rf of whatever follows r
// later on), but after a ; or a line break they are a command of their
// own (compgen -C 'true;' runs compgen with the word being completed,
// which may be -C and a command of its own), after a # they are nothing,
// and after a > the first is a file. Where they land is found by reading
// the text with a marker word after it.
func (e *Engine) addCalled(v *bashView, text string, then []word, nest nesting, depth int) {
	lc, at, why := landing(text)
	switch {
	case why != "":
		v.setOpaque("a builtin runs text that gnosh cannot read with the words it adds after it: " + why)
	case lc != nil:
		ws := append(append(append([]word(nil), lc.words[:at]...), then...), lc.words[at+1:]...)
		e.addDerived(v, &simpleCmd{words: ws, redirs: lc.redirs, nest: nest | nestScript}, nil, depth)
	}
	// Otherwise the words fall after a #, where nothing reads them.
}

// unknownWord stands for a word only known when the line runs: what xargs
// adds, the words after an alias where it is used.
func unknownWord() word { return word{raw: "{}", parts: []wordPart{{kind: partSubst}}} }

// compgenWord is the word compgen completes, which it hands the command of
// -C as its second word: its operand, or nothing.
func compgenWord(args []word) word {
	const takes = "ACFGPSWXo" // the flags that take a value
	for i := 0; i < len(args); i++ {
		l := args[i].joined()
		switch {
		case l == "--":
			if i+1 < len(args) {
				return args[i+1]
			}
			return litOf("")
		case len(l) > 1 && l[0] == '-':
			// A cluster of flags: the first that takes a value takes the
			// rest of the word, or else the next word.
			for k := 1; k < len(l); k++ {
				if strings.IndexByte(takes, l[k]) >= 0 {
					if k == len(l)-1 {
						i++
					}
					break
				}
			}
		default:
			return args[i]
		}
	}
	return litOf("")
}

// onlyVars reports whether a word is made of variables and nothing else.
func onlyVars(w word) bool {
	for _, p := range w.parts {
		if p.kind != partVar && !(p.kind == partLit && p.lit == "") {
			return false
		}
	}
	return true
}

// commandVars are environment variables whose value is a command line
// that some program runs: a pager, an editor, a diff or ssh command.
var commandVars = map[string]bool{
	"PAGER": true, "GIT_PAGER": true, "MANPAGER": true, "EDITOR": true, "VISUAL": true, "GIT_EDITOR": true,
	"GIT_SEQUENCE_EDITOR": true, "FCEDIT": true, "BROWSER": true, "GIT_EXTERNAL_DIFF": true,
	"GIT_SSH_COMMAND": true, "GIT_SSH": true, "GIT_ASKPASS": true, "SSH_ASKPASS": true, "SUDO_ASKPASS": true,
	"GIT_PROXY_COMMAND": true, "PROMPT_COMMAND": true, "LESSOPEN": true, "LESSCLOSE": true, "LESSEDIT": true,
	"SHELL": true, "RSYNC_RSH": true, "CC": true, "CXX": true,
}

// commandVar reads the value of an assignment to one of commandVars as
// the command it is: "GIT_EXTERNAL_DIFF='rm -rf x' git diff" runs rm.
// This serves deny and ask rules only; a line that sets a variable is
// never allowed by a rule.
func (e *Engine) commandVar(v *bashView, w word, nest nesting, depth int) {
	name, ok := assignName(w.raw)
	if !ok {
		return
	}
	switch name {
	case "BASH_CMDS", "BASH_ALIASES":
		// bash's own tables of where a command is and what an alias
		// stands for: BASH_CMDS[ls]=/bin/rm makes ls run rm. The value is
		// held against the rules, and the line is one whose later commands
		// are not what they are written as.
		v.setOpaque("the line writes to " + name + ", the shell's own table of what a name runs")
		if key, val, cut := strings.Cut(e.unsetValue(w), "="); cut && val != "" && strings.HasSuffix(key, "]") {
			// The name between the brackets stands for the value from here
			// on, with whatever words follow it (useAlias).
			if i := strings.IndexByte(key, '['); i > 0 {
				if v.aliases == nil {
					v.aliases = map[string][]string{}
				}
				alias := strings.Trim(key[i+1:len(key)-1], "'\"")
				v.aliases[alias] = append(v.aliases[alias], val)
			}
		}
		if _, val, cut := strings.Cut(w.joined(), "="); cut && w.static() && val != "" {
			if name == "BASH_ALIASES" {
				e.addScript(v, val, nest, depth, scriptMaybe)
				e.addCalled(v, val, []word{unknownWord()}, nest, depth)
			} else {
				e.addDerived(v, &simpleCmd{words: []word{litOf(val)}, nest: nest}, nil, depth)
			}
		}
		return
	case "BASH_ENV":
		// The file bash runs before anything else, as by source:
		// "BASH_ENV=e.sh bash -c true" runs e.sh, and BASH_ENV=.env reads
		// .env. It is read as that.
		if file, ok := afterPrefix(w, name+"="); ok && file.raw != "" {
			e.addDerived(v, &simpleCmd{words: []word{litOf("."), file}, nest: nest}, nil, depth)
		}
		return
	}
	if !commandVars[name] || !w.static() {
		return
	}
	_, val, _ := strings.Cut(w.joined(), "=")
	if val = strings.TrimLeft(val, "| "); val != "" {
		e.addScript(v, val, nest, depth, scriptMaybe)
	}
}

// scanWord looks at a word whose text is known for a command written
// inside it. The shell evaluates more than it seems to: a variable used
// as a number, an array subscript, ${x@P} and ${!x} all run the $(...) in
// a value that was set in single quotes. So quoted text that looks like a
// substitution is read as one, wherever it stands.
func (e *Engine) scanWord(v *bashView, w word, nest nesting, depth int) {
	e.scanText(v, w, nest, depth, scriptData)
}

// scanValue is scanWord for a word that gives a variable its value: an
// assignment, a word of an array or of a for loop's list.
func (e *Engine) scanValue(v *bashView, w word, nest nesting, depth int) {
	e.scanText(v, w, nest, depth, scriptValue)
}

func (e *Engine) scanText(v *bashView, w word, nest nesting, depth int, how int) {
	if depth > maxShellDepth {
		return
	}
	// The pieces of the word that are plain text, whatever stands between
	// them: ${x}'a[$(cmd)]' is evaluated like 'a[$(cmd)]'.
	if l := w.joined(); strings.Contains(l, "$(") || strings.Contains(l, "`") {
		e.addScript(v, l, nest|nestScript, depth, how)
	}
}

// addWordText reads text that the shell will expand as a word, not run
// as a script: the commands in its substitutions run, and nothing else
// in it means anything. Text that cannot be read that way is held
// against the rules crudely, and the line is one that was not followed.
func (e *Engine) addWordText(v *bashView, text string, nest nesting, depth int) {
	p := &parser{src: text, sc: &script{ok: true}}
	for p.pos < len(text) && !p.failed {
		c := text[p.pos]
		if c < 0x20 && c != '\t' || c == 0x7f {
			p.fail("a control character")
			break
		}
		procSubst := (c == '<' || c == '>') && p.pos+1 < len(text) && text[p.pos+1] == '('
		if isWordEnd(c) && !procSubst {
			p.pos++ // an operator's character is only text here
			continue
		}
		if _, ok := p.readWord(); !ok {
			p.fail("text gnosh cannot read as a word")
		}
	}
	if p.failed || len(p.docs) > 0 {
		e.addScript(v, text, nest, depth, scriptOperand)
		return
	}
	if p.sc.subst {
		v.sc.subst = true
	}
	var prev *simpleCmd
	for _, c := range p.sc.cmds {
		c.nest |= nest | nestScript
		e.addDerived(v, c, prev, depth)
		prev = c
	}
	for _, a := range p.sc.arith {
		e.addScript(v, a, nest|nestSubshell, depth+1, scriptArith)
	}
}

// addDerived records a command found inside another and looks inside it
// in turn.
func (e *Engine) addDerived(v *bashView, c *simpleCmd, prev *simpleCmd, depth int) {
	// "time ! cmd" and "! cmd" run cmd, and so does "X=1 cmd" wherever a
	// shell reads those words as a command of their own. ("time X=1" is
	// an assignment to bash: the value may be the variable's.) A "!" is
	// the shell's own word only before the assignments: after one it is a
	// command's name, which "X=1 !" runs (the permission oracle, seed
	// 174).
	assigned := len(c.assigns) > 0
	for len(c.words) > 0 && (!assigned && c.words[0].raw == "!" && len(c.words) > 1 || isAssignWord(c.words[0].raw)) {
		if name, ok := assignName(c.words[0].raw); ok {
			assigned = true
			if strings.HasPrefix(c.words[0].raw, name+"=") {
				e.noteWord(v, name, c.words[0], true, false, noPlace)
			}
			v.openVar(name)
		}
		if len(c.words) == 1 {
			break
		}
		c.words = c.words[1:]
	}
	if !assigned && len(c.words) == 1 && c.words[0].raw == "!" {
		c.words = nil
	}
	if len(v.units) >= maxUnits+len(v.sc.cmds) {
		if !v.tooMany {
			v.tooMany = true
			v.setOpaque("it holds more nested commands than gnosh will follow")
			crude := crudeFragments(v.text)
			v.frags = append(v.frags, crude...)
			v.unread = append(v.unread, crude...)
		}
		return
	}
	v.units = append(v.units, &unit{cmd: c, derived: true, origin: v.origin})
	e.noteVars(v, c, noPlace)
	e.derive(v, c, prev, depth+1)
}

// progName returns the program a command names, with $HOME and a leading
// ~ filled in, and whether the name is known ahead of time.
func (e *Engine) progName(w word) (string, bool) {
	if w.static() {
		name := w.value(e.home)
		return name, !magicProg(name)
	}
	var b strings.Builder
	for _, p := range w.parts {
		switch {
		case p.kind == partLit:
			b.WriteString(p.lit)
		case p.kind == partVar && p.plainly() && p.name == "HOME" && e.home != "":
			// (Not ${HOME:+rm}, which is rm.)
			b.WriteString(e.home)
		case p.kind == partVar && p.plainly() && p.name == "PWD":
			b.WriteString(e.cur)
		default:
			return "", false
		}
	}
	return b.String(), !w.eqHead && !w.tilde
}

// fedScript reads the script a shell (or "source /dev/stdin") is fed on
// its standard input: a here-document, a here-string, or a pipe from
// echo, printf or a cat that passes a here-document along. It reports
// whether there was one.
func (e *Engine) fedScript(v *bashView, c, prev *simpleCmd, nest nesting, depth int, pipe bool) bool {
	found := false
	for _, r := range c.redirs {
		switch r.op {
		case "<<", "<<-":
			found = true
			if r.bodyStatic {
				e.addScript(v, r.body, nest, depth, scriptText)
			} else {
				v.setOpaque("a shell is fed a script that is computed when the command runs")
				e.addScript(v, r.body, nest, depth, scriptMaybe)
			}
		case "<<<":
			found = true
			if r.target.static() {
				e.addScript(v, r.target.joined(), nest, depth, scriptText)
			} else {
				v.setOpaque("a shell is fed a script that is computed when the command runs")
				e.addScript(v, maybeText([]word{r.target}), nest, depth, scriptMaybe)
			}
		case "<":
			if r.target.hasSubst() {
				found = true
				v.setOpaque("a shell is fed the script a command prints")
			}
		}
	}
	if found || !pipe || c.sep != sepPipe {
		return found
	}
	feeder := ""
	if prev != nil && len(prev.words) > 0 && prev.words[0].static() {
		feeder = prev.words[0].joined()
	}
	switch {
	case (feeder == "echo" || feeder == "printf") && allStatic(prev.words[1:]):
		text, sure := printed(feeder, prev.words[1:])
		if sure {
			e.addScript(v, text, nest, depth, scriptText)
		} else {
			// What is printed is built from a format or from escapes. The
			// words are still held against the rules as they stand, and
			// the script is one nobody can read ahead of time.
			e.addScript(v, text, nest, depth, scriptMaybe)
			v.setOpaque("a shell is fed text that " + feeder + " builds from a format or from escapes")
		}
	case feeder == "cat" && len(prev.words) == 1 && len(prev.redirs) > 0:
		for _, r := range prev.redirs {
			switch {
			case (r.op == "<<" || r.op == "<<-") && r.bodyStatic:
				e.addScript(v, r.body, nest, depth, scriptText)
			case r.op == "<<<" && r.target.static():
				e.addScript(v, r.target.joined(), nest, depth, scriptText)
			default:
				v.setOpaque("a shell is fed its script through a pipe")
			}
		}
	default:
		v.setOpaque("a shell is fed its script through a pipe")
	}
	return true
}

// scriptAfter returns the index of the word a shell takes for the script
// of its -c: the first one after the options that stand from args[i] on.
// A lone - or -- ends the options, a lone + is an option with nothing in
// it, and with values each o and O among an option's letters takes the
// next word for its value (set -o name, shopt -O name).
func scriptAfter(args []word, i int, values bool) int {
	k := i
	for k < len(args) && args[k].static() {
		o := args[k].joined()
		if k > i {
			if o == "--" || o == "-" {
				return k + 1
			}
			if o == "" || o[0] != '-' && o[0] != '+' {
				return k
			}
		}
		k++
		switch {
		case o == "--rcfile" || o == "--init-file":
			k++
		case values && !strings.HasPrefix(o, "--"):
			k += strings.Count(o, "o") + strings.Count(o, "O")
		}
	}
	return k
}

// printed returns what "echo words" or "printf words" writes, and
// whether that is sure to be the words as they stand. It is for echo
// given no flag but -n and no backslash (sh's echo turns \0162 into a
// letter, and so does bash's with -e or xpg_echo), and for printf given
// a format with no % and no backslash, which it prints once whatever
// follows it.
func printed(feeder string, args []word) (text string, sure bool) {
	lits := litsOf(args)
	if feeder == "printf" {
		if len(lits) == 0 {
			return "", true
		}
		if strings.HasPrefix(lits[0], "-") || strings.ContainsAny(lits[0], "%\\") {
			return strings.Join(lits, " "), false
		}
		return lits[0], true
	}
	for len(lits) > 0 && lits[0] == "-n" {
		lits = lits[1:]
	}
	text = strings.Join(lits, " ")
	return text, !(len(lits) > 0 && strings.HasPrefix(lits[0], "-")) && !strings.Contains(text, "\\")
}

// maybeText is the text of words that are not all known ahead of time,
// as a script to hold against the rules: what is written stays as it
// is, and each piece the shell would fill in becomes a variable nobody
// has set. "eval 'ls > '$F" cannot be read for what it will run, but
// what stands in it (an ls, a redirection) is still held to the guards.
func maybeText(ws []word) string {
	var b strings.Builder
	for i, w := range ws {
		if i > 0 {
			b.WriteByte(' ')
		}
		for _, p := range w.parts {
			if p.kind == partLit {
				b.WriteString(p.lit)
			} else {
				b.WriteString("${GNOSH_UNKNOWN}")
			}
		}
	}
	return b.String()
}

// flagValue hands the value of the flag (a single-letter one, written
// -C value or -Cvalue) to fn, for every time the flag is given.
func flagValue(args []word, flag byte, fn func(w word, ok bool)) {
	for i := 0; i < len(args); i++ {
		// A word that is not all known is read by what is written of it:
		// "$x-C'rm x'" hands the flag its value whatever x holds, and that
		// value is then not known for sure (ok is false).
		l := args[i].joined()
		if len(l) < 2 || l[0] != '-' || l[1] == '-' || strings.IndexByte(l, flag) < 0 {
			continue
		}
		if rest := l[strings.IndexByte(l, flag)+1:]; rest != "" {
			fn(litOf(rest), args[i].static())
		} else if i+1 < len(args) {
			fn(args[i+1], args[i+1].static() && args[i].static())
			i++
		}
	}
}

// litOf makes a word of plain text.
func litOf(s string) word { return word{raw: s, parts: []wordPart{{lit: s}}} }

// derive looks inside c for the commands it would run: the command after
// a wrapper, the script handed to a shell or to eval.
func (e *Engine) derive(v *bashView, c *simpleCmd, prev *simpleCmd, depth int) {
	if v.tooMany {
		return
	}
	if depth > maxShellDepth {
		v.setOpaque("commands are nested too deeply to read")
		return
	}
	nest := c.nest | nestScript
	for _, a := range c.assigns {
		e.scanValue(v, a.w, nest, depth)
		for _, el := range a.elems {
			e.scanValue(v, el, nest, depth)
		}
		e.commandVar(v, a.w, nest, depth)
	}
	for _, r := range c.redirs {
		// bash expands the word after >& a second time when it is not a
		// descriptor: "ls >&'$(cmd)'" runs cmd, quotes or no quotes. What
		// the first expansion leaves of such a word is read as text the
		// shell evaluates; any other target that holds the text of a
		// substitution is read as one that may be.
		if r.op == ">&" && !isFdTarget(r.target) && !r.target.static() {
			// What the first expansion of a computed word leaves is not
			// known, and it may be the text of a substitution that the
			// second runs: Y='$(rm x)'; ls >&"$Y" (oracle seed 124).
			v.setOpaque("bash expands the word after >& a second time, and what a word computed when the line runs leaves of it may run a command")
			e.scanWord(v, r.target, nest, depth)
		} else if l := r.target.joined(); r.op == ">&" && !isFdTarget(r.target) && strings.ContainsAny(l, "$`<>") {
			e.addWordText(v, l, nest, depth)
		} else {
			e.scanWord(v, r.target, nest, depth)
		}
	}
	// The other reading of a [[ ]] test: see testCommand.
	if len(c.pieces) > 0 || len(c.altRedirs) > 0 {
		pieces, alt := c.pieces, c.altRedirs
		c.pieces, c.altRedirs = nil, nil
		for _, piece := range pieces {
			e.addDerived(v, &simpleCmd{words: piece, nest: nest}, nil, depth)
		}
		if len(alt) > 0 && len(v.units) < maxUnits+len(v.sc.cmds) {
			v.units = append(v.units, &unit{cmd: &simpleCmd{redirs: alt, nest: nest}, derived: true, origin: v.origin})
		}
		c.pieces, c.altRedirs = pieces, alt
	}
	if len(c.words) == 0 {
		return
	}
	// A default or an alternative written with blanks is several words
	// where its ${...} stands for it: the command as it is then is read as
	// well.
	if spread, ok := e.spreadWords(v, c.words); ok {
		e.addDerived(v, &simpleCmd{words: spread, redirs: c.redirs, sep: c.sep, nest: nest}, prev, depth)
	}
	e.useAlias(v, c, prev, depth)
	if c.words[0].static() && v.funcNames[c.words[0].joined()] {
		e.giveArgs(v, c.words[1:], false) // a function of the line's own, and the words it is handed
	}
	// An argument that holds a variable the line itself set is also read
	// as what it was set to, in as many words as the shell would make of
	// it: "o=-exec; find . $o rm {} \\;" and "f=-c; sh $f 'rm x'".
	alts, _ := e.filledArgs(v, c.words)
	for _, alt := range alts {
		e.addDerived(v, &simpleCmd{words: alt, redirs: c.redirs, sep: c.sep, nest: c.nest}, prev, depth)
	}
	// Braces are words bash makes before it looks at anything else, the
	// program's name and its flags among them: "bash {-c,'rm x'}" is
	// bash -c 'rm x'. The command is read again as those words. (With a
	// quote among the braces the words cannot be told for sure.)
	if alt, sure, ok := braceWords(c.words); ok {
		if !sure {
			v.setOpaque("braces with quoted text in them make words gnosh cannot tell for sure")
		}
		e.addDerived(v, &simpleCmd{words: alt, redirs: c.redirs, sep: c.sep, nest: c.nest}, prev, depth)
	}
	// Braces in a word with a variable in it: bash takes the braces apart
	// before it fills the variable, and dash takes nothing apart, the
	// braces being characters beside the variable. "f() { mv "$@"{,x};
	// }; f .git /tmp/g" moves .git either way: bash runs mv .git /tmp/g
	// .gitx /tmp/gx, and dash mv .git '/tmp/g{,x}'. Each reading is judged.
	if bashWords, dashWords, ok := varBraces(c.words); ok {
		if bashWords != nil {
			e.addDerived(v, &simpleCmd{words: bashWords, redirs: c.redirs, sep: c.sep, nest: c.nest}, prev, depth)
		}
		e.addDerived(v, &simpleCmd{words: dashWords, redirs: c.redirs, sep: c.sep, nest: c.nest}, prev, depth)
	}
	full, ok := e.progName(c.words[0])
	if !ok || progIsPattern(c.words[0]) {
		// What runs is not known ahead of time. Whether a deny or ask
		// rule could still apply is worked out from the command's shape
		// (couldHit). A name the line itself set ("x=rm; $x y") is tried
		// as that name as well.
		names, _, _ := e.filledAll(v, c.words[0])
		if c.words[0].glob {
			names = nil
		}
		if len(names) > maxProgNames || e.tooManyFills(v, c.words[0]) {
			// (Padding the list is no way past: see safety.)
			names, v.manyNames = nil, c.words[0].raw
		}
		if w0 := c.words[0]; len(w0.parts) == 1 && w0.parts[0].all && len(names) > 0 {
			// "${a[@]}" where the program's name goes is every word of the
			// array: "a=(rm -rf /); ${a[@]}" is rm -rf /. The array may
			// have been given its words in more goes than one, so the
			// command is read from each of its words on.
			var all []word
			for _, nv := range names {
				if nv.loose {
					continue
				}
				if w0.quoted {
					part := litOf(nv.text)
					part.anyOrder = true
					all = append(all, part)
					continue
				}
				for i, f := range e.fields(v, nv.text) {
					part := litOf(f)
					part.glob, part.anyOrder = (len(all) > 0 || i > 0) && hasPathMagic(f), true
					all = append(all, part)
				}
			}
			for i := range all {
				ws := append(append([]word(nil), all[i:]...), c.words[1:]...)
				e.addDerived(v, &simpleCmd{words: ws, redirs: c.redirs, nest: nest}, prev, depth)
			}
		}
		for _, nv := range names {
			name := nv.text
			if name == "" {
				continue
			}
			ws := append([]word{litOf(name)}, c.words[1:]...)
			if fields := e.fields(v, name); len(fields) > 1 {
				// An unquoted variable is split into words, and the
				// patterns in those are matched.
				ws = nil
				for i, f := range fields {
					part := litOf(f)
					part.glob = i > 0 && hasPathMagic(f)
					ws = append(ws, part)
				}
				ws = append(ws, c.words[1:]...)
			}
			e.addDerived(v, &simpleCmd{words: ws, redirs: c.redirs, nest: nest}, prev, depth)
		}
		// A name of which enough is written to tell what it could be is
		// read as each of the programs this file knows something about
		// that it could be: "alias${x} zz='rm x'" is alias when x is not
		// set, and so read.
		for _, name := range e.couldBe(c.words[0]) {
			ws := append([]word{litOf(name)}, c.words[1:]...)
			e.addDerived(v, &simpleCmd{words: ws, redirs: c.redirs, sep: c.sep, nest: c.nest}, prev, depth)
		}
		// A program that is not known may be a shell or a wrapper
		// ("$SH -c 'rm x'", "$RUN rm x"): its arguments are held against
		// the rules as the script and as the command they would then be.
		// It may be a shell that reads its script from a pipe or a
		// here-document: "echo 'rm x' | ba${x}sh".
		if e.couldBeShell(c.words[0]) {
			e.fedScript(v, c, prev, nest, depth, true)
		} else {
			// Whatever it is, the text it is fed is held against the
			// rules: it may be one that runs what it reads.
			for _, r := range c.redirs {
				switch {
				case r.op == "<<" || r.op == "<<-":
					e.addScript(v, r.body, nest, depth, scriptMaybe)
				case r.op == "<<<":
					e.addScript(v, maybeText([]word{r.target}), nest, depth, scriptMaybe)
				}
			}
		}
		// It may also be xargs, which adds the words it reads to the
		// command, where its mark stands or at the end: with something on
		// its standard input, those are words nobody knows.
		fed := c.sep == sepPipe || len(c.redirs) > 0
		marks := []string{"{}"}
		for i := 1; i < len(c.words) && i <= 8; i++ {
			e.addDerived(v, &simpleCmd{words: c.words[i:], nest: nest}, nil, depth)
			if a := c.words[i]; a.static() {
				// The word may be a script, and so may what a flag or a
				// name holds: -c'rm x', --command='rm x', zz='rm x'.
				l := a.joined()
				_, val, eq := strings.Cut(l, "=")
				switch {
				case !strings.HasPrefix(l, "-"):
					e.addScript(v, l, nest, depth, scriptMaybe)
				case !strings.HasPrefix(l, "--") && len(l) > 2:
					e.addScript(v, l[2:], nest, depth, scriptMaybe)
				}
				if eq && val != "" {
					e.addScript(v, val, nest, depth, scriptMaybe)
				}
			}
			if fed {
				e.addDerived(v, &simpleCmd{words: xargsAdds(c.words[i:], marks), nest: nest}, nil, depth)
			}
			if l := c.words[i].lit(); strings.HasPrefix(l, "-I") || strings.HasPrefix(l, "-i") {
				if len(l) > 2 {
					marks = append(marks, l[2:])
				} else if i+1 < len(c.words) {
					marks = append(marks, c.words[i+1].lit())
				}
			}
		}
		return
	}
	// The tables below go by the program's key: BASH is bash, and XARGS
	// xargs, where the file system takes no notice of case.
	name := progKey(baseName(full))
	args := c.words[1:]
	// A program that a flag of its own sends to another folder (env -C,
	// sudo -D, tar -C, make -C, go -C) works there: each folder it may be
	// sent to is read as a cd the line may take, so that the words it is
	// handed are judged from there too ("env --chdir=/ rm -rf usr" removes
	// /usr). A folder only known when the line runs is a place nobody
	// knows, as after cd "$X".
	// So does git with its own -C: "git -C sub diff --no-index ../.env
	// /dev/null" prints the .env beside sub.
	dirs := chdirValues(name, args)
	if name == "git" {
		dirs = gitChdirs(args)
		for _, text := range gitRuns(args) {
			e.addScript(v, text, nest, depth, scriptMaybe)
		}
	}
	for _, dir := range dirs {
		e.addDerived(v, &simpleCmd{words: []word{litOf("cd"), dir}, nest: nest, flagCd: true}, prev, depth)
	}

	isScript := true
	switch {
	case shellNames[name]:
		hasScript, operand := false, false
		for i := 0; i < len(args); i++ {
			a := args[i]
			if !a.static() || a.glob && !operand {
				// (A pattern before the script may be a flag, were there a
				// file of that name.)
				v.setOpaque("a shell is started with arguments computed when the command runs")
				break
			}
			l := a.joined()
			// A c among the letters of a word that starts with - or with +
			// ("bash +c 'x'" runs x as well).
			cluster := len(l) > 1 && (l[0] == '-' || l[0] == '+') && !strings.HasPrefix(l, "--")
			if cluster && strings.Contains(l[1:], "c") || l == "--command" {
				hasScript = true
				// The script is the first word after the options that
				// follow: "bash -c -- 'x'", "sh -c -e 'x'", "bash -c - 'x'"
				// and "bash -c -eo pipefail 'x'" all run x. Whether an o
				// takes the next word for its value depends on the shell,
				// so both places are read.
				k1, k2 := scriptAfter(args, i, true), scriptAfter(args, i, false)
				for n, k := range []int{k1, k2} {
					if k >= len(args) || n == 1 && k == k1 {
						continue
					}
					// The words after the script are its $0, $1 and so on.
					e.giveArgs(v, args[k+1:], true)
					switch {
					case !allKnown(args[k : k+1]):
						v.setOpaque("a shell is handed a script computed when the command runs")
						e.addScript(v, maybeText(args[k:k+1]), nest, depth, scriptMaybe)
					case n == 0:
						e.addScript(v, args[k].joined(), nest, depth, scriptText)
					default:
						e.addScript(v, args[k].joined(), nest, depth, scriptMaybe)
					}
				}
				break
			}
			if !strings.HasPrefix(l, "-") {
				// A script file, or the value of a flag such as -o. A -c
				// after it is still looked for: finding a script that is
				// not one only makes the check stricter.
				operand = true
			}
		}
		if name == "su" {
			// su takes the script attached to the flag as well: -c'x'.
			flagValue(args, 'c', func(w word, ok bool) {
				if ok {
					e.addScript(v, w.joined(), nest, depth, scriptMaybe)
				}
			})
		}
		if e.fedScript(v, c, prev, nest, depth, !hasScript && !operand && name != "su") {
			hasScript = true
		}
		if !hasScript {
			v.bareShell = true
		}
	case name == "source" || name == ".":
		// A file that is sourced is a program of its own, which no rule
		// reads. Text the line itself supplies is read. The file is the
		// first word that stays: a word that may come to nothing (an
		// unquoted $* or ${X}, a "$@" with no words) leaves the next one
		// to be the file, so each such next one is judged as it too
		// ("source $* ${X} <(cmd)" runs what cmd prints; oracle seed 122).
		for _, w := range args {
			e.sourcedArg(v, c, prev, nest, depth, w)
			if !mayVanish(w) {
				break
			}
		}
	case name == "eval":
		if len(args) > 0 && args[0].lit() == "--" {
			args = args[1:] // bash's eval ends its options there
		}
		if allKnown(args) {
			e.addScript(v, strings.Join(litsOf(args), " "), nest, depth, scriptText)
		} else {
			v.setOpaque("eval runs text that is computed when the command runs")
			e.addScript(v, maybeText(args), nest, depth, scriptMaybe)
		}
	case name == "history":
		// What history -s puts in the shell's history runs when fc runs it
		// again: "set -o history; history -s 'rm -rf ~'; fc -s" runs rm
		// -rf ~ in bash -c. It is read as text that may run. (What history
		// -r reads from a file is the file's, as a sourced file is.)
		for i, a := range args {
			if a.lit() != "-s" {
				continue
			}
			if rest := args[i+1:]; allKnown(rest) {
				e.addScript(v, strings.Join(litsOf(rest), " "), nest, depth, scriptMaybe)
			} else {
				v.setOpaque("history -s is given text computed when the command runs, which fc may run")
				e.addScript(v, maybeText(rest), nest, depth, scriptMaybe)
			}
			break
		}
	case name == "alias":
		// What an alias stands for runs wherever its name is written
		// later in the line.
		for _, a := range args {
			text, how := a.joined(), scriptText
			if !a.static() {
				// (Read as it is when no variable in it is set: "alias
				// z=${x}rm" is an alias for rm then.)
				v.setOpaque("an alias is given text computed when the command runs")
				text, how = e.unsetValue(a), scriptMaybe
			}
			if alias, val, ok := strings.Cut(text, "="); ok {
				// The words that follow the alias where it is used are the
				// last words of what it stands for: "alias g=git" and then
				// "g push origin" is git push origin. Where the line itself
				// uses the alias, that is read word for word (useAlias);
				// wherever else it is used, the words are nobody's to know.
				if v.aliases == nil {
					v.aliases = map[string][]string{}
				}
				v.aliases[alias] = append(v.aliases[alias], val)
				if val != "" {
					e.addScript(v, val, nest, depth, how)
					e.addCalled(v, val, []word{unknownWord()}, nest, depth)
				}
			}
		}
	case name == "mapfile" || name == "readarray" || name == "compgen" || name == "complete":
		// -C names a command the builtin runs, with words of its own
		// after it: mapfile adds the index and the line it read; compgen
		// adds compgen, the word being completed and the one before it,
		// which is nothing outside a completion; and complete, whose
		// command runs when a person completes a word, adds three words
		// nobody knows ahead of time.
		then := []word{unknownWord(), unknownWord()}
		switch name {
		case "compgen":
			then = []word{litOf("compgen"), compgenWord(args), litOf("")}
		case "complete":
			then = append(then, unknownWord())
		}
		flagValue(args, 'C', func(w word, ok bool) {
			if ok {
				e.addScript(v, w.joined(), nest, depth, scriptText)
				e.addCalled(v, w.joined(), then, nest, depth)
			} else {
				v.setOpaque(name + " -C runs text that is computed when the command runs")
				e.addScript(v, maybeText([]word{w}), nest, depth, scriptMaybe)
				e.addCalled(v, maybeText([]word{w}), then, nest, depth)
			}
		})
		for _, a := range args {
			if !a.static() || a.glob {
				// Whatever the word turns into may be the flag and the
				// command it names.
				v.setOpaque(name + " is given a word computed when the command runs, which may name a command for it to call")
				break
			}
		}
		isScript = false
	case name == "hash":
		// hash -p file name: the file runs whenever name is written.
		flagValue(args, 'p', func(w word, ok bool) {
			if ok {
				e.addDerived(v, &simpleCmd{words: []word{w}, nest: nest}, nil, depth)
			} else {
				v.setOpaque("hash -p names a program computed when the command runs")
			}
		})
		// The names after the options run the file from here on, with
		// whatever words follow them: "hash -p /bin/rm ls; ls -rf x" is
		// rm -rf x. The options are read as bash's getopts reads them,
		// and each -p's file is taken for the names, as bash takes the
		// last: "hash -p h -p /bin/mv zz" made zz mv, while only the first
		// was read (oracle seed 168). A word computed when the command
		// runs may be such an option. They are read like aliases
		// (useAlias).
		var files []string
		i := 0
		for ; i < len(args); i++ {
			a := args[i]
			l := a.lit()
			if !a.static() {
				v.setOpaque("hash is given a word computed when the command runs, which may name the program a later name runs")
				break
			}
			if len(l) < 2 || l[0] != '-' {
				break
			}
			if l == "--" {
				i++
				break
			}
			if k := strings.IndexByte(l, 'p'); k > 0 {
				if k+1 < len(l) {
					files = append(files, l[k+1:])
				} else if i+1 < len(args) {
					i++
					files = append(files, e.unsetValue(args[i]))
				}
			}
		}
		for _, n := range args[min(i, len(args)):] {
			if !n.static() || strings.HasPrefix(n.joined(), "-") {
				continue
			}
			for _, file := range files {
				if file == "" {
					continue
				}
				if v.aliases == nil {
					v.aliases = map[string][]string{}
				}
				v.aliases[n.joined()] = append(v.aliases[n.joined()], file)
			}
		}
		for _, a := range args {
			if !a.static() || a.glob {
				v.setOpaque("hash is given a word computed when the command runs, which may tell the shell where a program is")
				break
			}
		}
		isScript = false
	case name == "enable":
		flagValue(args, 'f', func(word, bool) {
			v.setOpaque("enable -f loads a program into the shell")
		})
		for _, a := range args {
			if !a.static() || a.glob {
				v.setOpaque("enable is given a word computed when the command runs")
				break
			}
		}
		isScript = false
	case name == "set" || name == "shopt":
		v.shellOptions(name, args)
		if name == "set" {
			// What follows the options is what $1, $2 and "$@" stand for
			// from here on.
			for i, a := range args {
				if l := a.lit(); a.static() && l != "--" && l != "-" && (strings.HasPrefix(l, "-") || strings.HasPrefix(l, "+")) {
					continue
				}
				if a.static() && (a.lit() == "--" || a.lit() == "-") {
					i++
				}
				e.giveArgs(v, args[i:], false)
				break
			}
		}
		isScript = false
	case evaluators[name]:
		// These builtins evaluate their operands as arithmetic or as
		// variable names with subscripts, and a subscript can hold a
		// command: [[ 'a[$(cmd)]' -eq 1 ]]. Quoted text that looks like a
		// substitution is read as one.
		for _, a := range args {
			if l := a.joined(); strings.Contains(l, "$(") || strings.Contains(l, "`") {
				e.addScript(v, l, nest, depth, scriptOperand)
			}
			if declares[name] {
				// export PAGER='rm x' sets what git runs as its pager, as
				// PAGER='rm x' git does.
				e.commandVar(v, a, nest, depth)
			}
		}
	case name == "trap":
		// The script is the first argument that is not an option.
		for _, a := range args {
			if !a.static() || a.glob {
				v.setOpaque("trap runs text that is computed when the command runs")
				e.addScript(v, maybeText([]word{a}), nest, depth, scriptMaybe)
				break
			}
			if !strings.HasPrefix(a.joined(), "-") {
				if s := a.joined(); s != "" {
					e.addScript(v, s, nest, depth, scriptText)
				}
				break
			}
		}
	case name == "find":
		isScript = false
		// runs adds the command find would run from args[from:to].
		runs := func(from, to int) {
			if to <= from {
				return
			}
			// The names of the files found stand where {} does: words
			// nobody knows ahead of time.
			run := append([]word(nil), args[from:to]...)
			for k := 1; k < len(run); k++ {
				if strings.Contains(run[k].lit(), "{}") {
					run[k] = word{raw: run[k].raw, parts: []wordPart{{kind: partVar}}}
				}
			}
			e.addDerived(v, &simpleCmd{words: run, nest: nest}, nil, depth)
			if buildsCommand(args[from:to], "{}") {
				v.setOpaque("find runs the names of the files it finds as commands")
			}
		}
		// end returns where the command that starts at args[from] ends: at
		// a ; or a +.
		end := func(from int) int {
			j := from
			for j < len(args) && args[j].lit() != ";" && args[j].lit() != "+" {
				j++
			}
			return j
		}
		for i := 0; i < len(args); i++ {
			// A word that is not all known is read by what is written of
			// it ("$x-exec"). One that is not known at all may be -exec:
			// the words after it, up to a ; or a +, may then be a command.
			// And one written without quotes may turn into several words,
			// an -exec and its command among them.
			if args[i].static() && args[i].glob && strings.HasPrefix(args[i].joined(), "-") {
				// A pattern that starts with a dash may be -exec, were
				// there a file of that name.
				v.setOpaque("find is given a file name pattern that may turn into one of its own words")
			}
			if !args[i].static() {
				if !args[i].quoted {
					v.setOpaque("find is given words computed when the command runs, which may tell it to run a command")
				}
				if args[i].joined() == "" {
					if j := end(i + 1); j < len(args) {
						runs(i+1, j)
					}
					continue
				}
			}
			switch args[i].joined() {
			case "-exec", "-execdir", "-ok", "-okdir":
				j := end(i + 1)
				runs(i+1, j)
				i = j
			}
		}
	default:
		isScript = false
		spec, ok := wrappers[name]
		if !ok && (strings.HasPrefix(name, "ld-") || strings.HasPrefix(name, "ld.so")) && strings.Contains(name, ".so") {
			// The dynamic loader runs the program it is given:
			// /lib64/ld-linux-x86-64.so.2 /bin/rm x.
			spec, ok = wrapSpec{valued: " --library-path --preload --argv0 --audit --glibc-hwcaps-prepend --glibc-hwcaps-mask "}, true
		}
		if !ok || name == "command" && len(args) > 0 && (args[0].lit() == "-v" || args[0].lit() == "-V") {
			break
		}
		if name == "time" && cutsTime(c) {
			// bash runs what follows the redirection, a "-p" included; a
			// plain sh's time reads the words as its own. Both are read.
			e.addDerived(v, &simpleCmd{words: c.words[c.timeCut:], redirs: c.redirs, sep: c.sep, nest: nest}, prev, depth)
		} else if at := bashTimeStart(c.words); name == "time" && at > 0 && at < len(c.words) && strings.HasPrefix(c.words[at].joined(), "-") {
			// bash's time takes one -p and one -- for its own, so bash runs
			// a dash word after them as the program ("time -p -p cat x" runs
			// -p), where a plain sh's time takes it for an option. Both are
			// read.
			e.addDerived(v, &simpleCmd{words: c.words[at:], redirs: c.redirs, sep: c.sep, nest: nest}, prev, depth)
		}
		for _, start := range e.wrappedAt(v, spec, args, nest, depth) {
			inner := args[start:]
			if name == "xargs" {
				// xargs adds the words it reads to the command: after its
				// last word, or wherever the mark of -I stands. Nobody
				// knows those words ahead of time, and a rule may name
				// what they make: "echo push | xargs git".
				inner = xargsAdds(inner, xargsMarks(args[:start]))
			}
			if name == "parallel" {
				// parallel does the same with the words after :::, or with
				// what it reads.
				for k, w := range inner {
					if strings.HasPrefix(w.lit(), ":::") {
						inner = inner[:k]
						break
					}
				}
				if len(inner) == 0 {
					continue
				}
				inner = xargsAdds(inner, []string{"{}"})
			}
			// The wrapped command has the wrapper's standard input: a
			// here-document or a pipe into "sudo sh" is sh's script.
			e.addDerived(v, &simpleCmd{words: inner, redirs: c.redirs, sep: c.sep, nest: nest}, prev, depth)
			if spec.joined && allStatic(args[start:]) {
				e.addScript(v, strings.Join(litsOf(args[start:]), " "), nest, depth, scriptText)
			}
			if name == "xargs" {
				// xargs adds what it reads to the command. Given to a
				// program that runs its arguments, or put where the
				// program's name is, that is a command nobody wrote down.
				inner, _ := e.progName(args[start])
				prog := progKey(baseName(inner))
				_, runs := wrappers[prog]
				for _, mark := range xargsMarks(args[:start]) {
					runs = runs || buildsCommand(args[start:], mark)
				}
				if shellNames[prog] || prog == "eval" || runs {
					v.setOpaque("xargs hands what it reads to a program that runs it")
				}
			}
		}
	}
	if !isScript {
		for _, a := range args {
			e.scanWord(v, a, nest, depth)
		}
	}
}

// readNames are the programs this package reads the arguments of in a
// way of their own: the builtins derive knows, those that take the line
// to another directory or give a variable its value, the shells and the
// wrappers. They are in the order of the alphabet, so that the same
// line is always read the same way.
var readNames = func() []string {
	seen := map[string]bool{}
	var out []string
	add := func(name string) {
		if !seen[name] {
			seen[name] = true
			out = append(out, name)
		}
	}
	for _, name := range []string{"eval", "source", ".", "alias", "trap", "mapfile", "readarray", "compgen", "complete",
		"hash", "enable", "set", "shopt", "find", "cd", "pushd", "popd"} {
		add(name)
	}
	for _, names := range []map[string]bool{shellNames, evaluators, declares} {
		for name := range names {
			add(name)
		}
	}
	for name := range wrappers {
		add(name)
	}
	sort.Strings(out)
	return out
}()

// couldBe returns the programs this package has a reading for whose
// name the word could turn out to be, when part of the name is written:
// "alias${x}" is alias when x is not set. A name that is all variable
// could be any of them, and is left to the checks that say so.
func (e *Engine) couldBe(w word) []string {
	if progIsPattern(w) || w.eqHead || w.tilde || w.static() {
		return nil
	}
	var head []tok
	lits := 0
	for _, p := range w.parts {
		if p.kind == partLit {
			head = appendLit(head, p.lit)
			lits += len(p.lit)
		} else {
			head = appendHole(head)
		}
	}
	if lits == 0 {
		return nil
	}
	head = foldHead(head)
	// The program by its name alone is what follows the last / that is
	// written, as it is for the rules (skeletons).
	base := head
	for i := len(head) - 1; i >= 0; i-- {
		if !head[i].hole && head[i].b == '/' {
			base = head[i+1:]
			break
		}
	}
	var out []string
	for _, name := range readNames {
		if couldMatch(base, appendLit(nil, name)) {
			out = append(out, name)
		}
	}
	return out
}

// couldBeShell reports whether a program name that is not known ahead of
// time could turn out to be a shell's: its known pieces fit one of the
// names, with or without a directory in front.
func (e *Engine) couldBeShell(w word) bool {
	var head []tok
	if progIsPattern(w) || w.eqHead || w.tilde || w.static() {
		// (A name that is all there and still not known is one that
		// names a program by a number: see magicProg.)
		head = appendHole(head)
	} else {
		for _, p := range w.parts {
			if p.kind == partLit {
				head = appendLit(head, p.lit)
			} else {
				head = appendHole(head)
			}
		}
	}
	head = foldHead(head)
	for name := range shellNames {
		if couldMatch(head, appendLit(nil, name)) || couldMatch(head, appendLit(appendHole(nil), "/"+name)) {
			return true
		}
	}
	// So may a name that names a program by the process that runs it or
	// by a number (magicProg): /proc/self$x/exe and /proc/$$/exe are the
	// shell that reads the line (oracle seed 141), /dev/fd/$n a file
	// opened earlier.
	for _, magic := range [][]tok{
		appendLit(appendHole(appendLit(appendHole(nil), "/proc/")), "/exe"),
		appendHole(appendLit(appendHole(nil), "/dev/fd/")),
	} {
		if couldMatch(head, magic) {
			return true
		}
	}
	// source and . read a script too, when their file is /dev/stdin.
	return couldMatch(head, appendLit(nil, "source")) || couldMatch(head, appendLit(nil, "."))
}

// xargsMarks returns the marks that stand, in the command xargs runs,
// for what it reads: {} and whatever -I, -J (the BSDs'), -i or
// --replace names.
func xargsMarks(flags []word) []string {
	marks := []string{"{}"}
	for _, f := range []byte{'I', 'J', 'i'} {
		flagValue(flags, f, func(w word, ok bool) {
			if ok && w.joined() != "" {
				marks = append(marks, w.joined())
			}
		})
	}
	for _, w := range flags {
		if v, ok := strings.CutPrefix(w.lit(), "--replace="); ok && v != "" {
			marks = append(marks, v)
		}
	}
	return marks
}

// xargsAdds returns the command xargs would run from ws: every word that
// holds one of the marks, and one more word at the end, is something
// only known when the line runs.
func xargsAdds(ws []word, marks []string) []word {
	unknown := unknownWord()
	out := append([]word(nil), ws...)
	for k := 1; k < len(out); k++ {
		for _, m := range marks {
			if m != "" && strings.Contains(out[k].lit(), m) {
				out[k] = unknown
			}
		}
	}
	return append(out, unknown)
}

// buildsCommand reports whether the words run something made from the
// text that stands for repl: repl as the program's name, or inside the
// script of a shell.
func buildsCommand(ws []word, repl string) bool {
	if len(ws) == 0 || repl == "" {
		return false
	}
	if strings.Contains(ws[0].lit(), repl) {
		return true
	}
	if prog := progKey(baseName(ws[0].lit())); !shellNames[prog] && prog != "eval" {
		return false
	}
	for _, w := range ws[1:] {
		if strings.Contains(w.lit(), repl) {
			return true
		}
	}
	return false
}

// wrappedAt returns every place among a wrapper's arguments where the
// command it runs may start. A flag the table does not know may or may
// not take the next word as its value, so both are followed.
func (e *Engine) wrappedAt(v *bashView, spec wrapSpec, args []word, nest nesting, depth int) []int {
	type state struct{ i, pos int }
	seen := map[state]bool{}
	isStart := map[int]bool{}
	work := []state{{0, spec.pos}}
	if spec.posMaybe {
		work = append(work, state{0, spec.pos + 1})
	}
	for len(work) > 0 {
		cur := work[len(work)-1]
		work = work[:len(work)-1]
		i, pos := cur.i, cur.pos
	scan:
		for ; i < len(args); i++ {
			if seen[state{i, pos}] {
				break
			}
			if len(seen) >= 512 {
				v.setOpaque("a wrapper is given more flags than gnosh will follow")
				work = nil
				break
			}
			seen[state{i, pos}] = true
			a := args[i]
			if !a.static() || a.glob {
				// (A pattern among a wrapper's own words may be a flag,
				// were there a file of that name.) The word may be the
				// command itself, a flag, or a flag and its value: what
				// stands at each of those places is read as the command
				// ("sudo $RM -rf /", "env rm${s}-rf${s}/").
				v.setOpaque("a wrapper is given arguments computed when the command runs")
				for k := i + pos; k <= i+pos+2 && k < len(args); k++ {
					isStart[k] = true
				}
				break
			}
			l := a.joined()
			switch {
			case l == "--":
				if i+1+pos < len(args) {
					isStart[i+1+pos] = true
				}
				if !spec.assigns {
					break scan
				}
				// env goes on taking NAME=VALUE words after "--", and takes
				// any word with an = in it for one, whatever stands before
				// the =: "env -- 0=1 rm x" runs rm x, where a shell would
				// look for a program named 0=1. The command that starts
				// after such words is read as well.
				for k := i + 1; k < len(args); k++ {
					w := args[k]
					if !w.static() || w.glob {
						v.setOpaque("a wrapper is given arguments computed when the command runs")
						for n := k + pos; n <= k+pos+2 && n < len(args); n++ {
							isStart[n] = true
						}
						break
					}
					if strings.IndexByte(w.joined(), '=') <= 0 {
						if k+pos < len(args) {
							isStart[k+pos] = true
						}
						break
					}
					e.commandVar(v, w, nest, depth)
				}
				break scan
			case l == "-":
				// env's "-" is its -i. To time and the rest it is no flag:
				// "time - x" runs a program named "-".
				isStart[i] = true
			case strings.HasPrefix(l, "-"):
				if spec.dashStarts && l != "-p" {
					isStart[i] = true
				}
				flag, val, attached := strings.Cut(l, "=")
				// kind says what a flag's value is: a script for a shell
				// (s), more of the wrapper's own arguments (a), or a value
				// that runs nothing (v).
				kind := func(f string) byte {
					switch {
					case strings.Contains(spec.script, " "+f+" "):
						return 's'
					case strings.Contains(spec.args, " "+f+" "):
						return 'a'
					case strings.Contains(spec.valued, " "+f+" "):
						return 'v'
					}
					return 0
				}
				// hand gives a flag's value to what reads it; after are the
				// words that follow the value. spliced is set when the value
				// was more arguments for the wrapper: the words after it
				// were then read along with them, and the scan ends here.
				spliced := false
				hand := func(k byte, w word, after []word) {
					switch {
					case k == 'v':
						// What a command prints, with no quotes to keep it
						// together, may be the value and the command after
						// it: "nice -n $(echo 5 rm x)" runs rm x, and so does
						// "env -u $(echo X rm x)m", where text stands beside
						// what is printed.
						if manyWords(w) || w.bareSubst {
							v.setOpaque("a wrapper is given arguments computed when the command runs")
						}
					case !w.static():
						v.setOpaque("a wrapper is handed a script computed when the command runs")
						e.addScript(v, maybeText([]word{w}), nest, depth, scriptMaybe)
					case k == 's':
						e.addScript(v, w.joined(), nest, depth, scriptText)
					default:
						e.wrapperArgs(v, spec, w.joined(), after, nest, depth)
						spliced = true
					}
				}
				// next hands over the word after the flag. A word that may
				// come to nothing (an unquoted $X that is empty) is not
				// there for the wrapper: the word after it is then the
				// value, and the command starts one further on ("nice -n
				// $X 5 git push" runs git push; oracle seed 174), which is
				// read too.
				next := func(k byte) {
					if i+1 < len(args) {
						hand(k, args[i+1], args[i+2:])
						if mayVanish(args[i+1]) && i+2 < len(args) {
							switch k {
							case 's':
								hand(k, args[i+2], args[i+3:])
							case 'a':
								v.setOpaque("a wrapper is given arguments computed when the command runs")
							}
							work = append(work, state{i + 3, pos})
						}
					}
					i++
				}
				long := strings.HasPrefix(l, "--")
				switch k := kind(flag); {
				case long && k != 0 && attached:
					hand(k, litOf(val), args[i+1:]) // --split-string='rm x'
				case long && k != 0:
					next(k)
				case long && attached:
				case long:
					work = append(work, state{i + 2, pos})
				default:
					// Letters in one word: the first that takes a value takes
					// the rest of the word (-S'rm x', -iS'rm x') or, when it
					// is the last, the next word.
					found := false
					for c := 1; c < len(l) && !found; c++ {
						if k := kind("-" + l[c:c+1]); k != 0 {
							found = true
							if c+1 < len(l) {
								hand(k, litOf(l[c+1:]), args[i+1:])
							} else {
								next(k)
							}
						}
					}
					if !found {
						work = append(work, state{i + 2, pos})
					}
				}
				if spliced {
					break scan
				}
			case spec.assigns && strings.IndexByte(l, '=') > 0 && (!spec.nameAssigns || isAssignWord(a.raw)):
				// env takes any word with an = in it for an assignment,
				// whatever stands before the = ("a.b=1"); bash's time only
				// a valid name's.
				e.commandVar(v, a, nest, depth)
			case pos > 0:
				pos--
			default:
				isStart[i] = true
				break scan
			}
		}
	}
	var out []int
	for i := range args {
		if isStart[i] {
			out = append(out, i)
		}
	}
	return out
}

// wrapperArgs reads a string that a wrapper splits into more arguments of
// its own (env -S): flags and assignments first, and then the command,
// which goes on with the words that follow the string (after). It is
// also held against the rules the way a shell would read it.
func (e *Engine) wrapperArgs(v *bashView, spec wrapSpec, text string, after []word, nest nesting, depth int) {
	if v.tooMany {
		return
	}
	fields, ok := splitArgs(text)
	if !ok || depth > maxShellDepth {
		v.setOpaque("env -S is given a string gnosh cannot split the way env does")
	}
	fields = append(fields, after...)
	if depth <= maxShellDepth {
		for _, start := range e.wrappedAt(v, spec, fields, nest, depth+1) {
			e.addDerived(v, &simpleCmd{words: fields[start:], nest: nest}, nil, depth)
		}
	}
	e.addScript(v, text, nest, depth, scriptMaybe)
}

// splitArgs cuts a string into words the way env -S does, as GNU env 9.4
// was seen to on Linux: at blanks, and a # at the start of a word ends the
// string. In single quotes only \\ and \' are escapes. Outside them, and
// in double quotes, \f \n \r \t and \v are those characters; \_ is a
// blank that separates words (a space in double quotes); \# \$ \" \' and
// \\ are the character itself; and \c ends the string (env refuses it in
// double quotes). ${NAME} stands for something not known here. Any other
// sequence, a backslash at the end and a quote that is not closed make env
// refuse the string, and splitArgs reports false for those, as for any
// string it cannot cut.
func splitArgs(s string) (out []word, ok bool) {
	var cur strings.Builder
	have, dynamic := false, false
	flush := func() {
		if have {
			w := litOf(cur.String())
			if dynamic {
				w.parts = append(w.parts, wordPart{kind: partVar})
			}
			out = append(out, w)
		}
		cur.Reset()
		have, dynamic = false, false
	}
	// escaped is what a backslash and e stand for outside single quotes,
	// or 0 for a sequence env refuses.
	escaped := func(e byte) byte {
		switch e {
		case 'f':
			return '\f'
		case 'n':
			return '\n'
		case 'r':
			return '\r'
		case 't':
			return '\t'
		case 'v':
			return '\v'
		case '#', '$', '"', '\'', '\\':
			return e
		}
		return 0
	}
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\f' || c == '\v':
			flush()
		case c == '#' && !have:
			return out, true
		case c == '\'':
			have = true
			for i++; ; i++ {
				if i >= len(s) {
					return out, false
				}
				if s[i] == '\'' {
					break
				}
				if s[i] == '\\' && i+1 < len(s) && (s[i+1] == '\\' || s[i+1] == '\'') {
					i++
				}
				cur.WriteByte(s[i])
			}
		case c == '"':
			have = true
			for i++; ; i++ {
				if i >= len(s) {
					return out, false
				}
				if s[i] == '"' {
					break
				}
				switch s[i] {
				case '\\':
					if i+1 >= len(s) {
						return out, false
					}
					i++
					switch e := s[i]; {
					case e == '_':
						cur.WriteByte(' ')
					case escaped(e) != 0:
						cur.WriteByte(escaped(e))
					default:
						// \c and the sequences env does not know.
						return out, false
					}
				case '$':
					dynamic = true
				default:
					cur.WriteByte(s[i])
				}
			}
		case c == '\\':
			if i+1 >= len(s) {
				return out, false
			}
			i++
			switch e := s[i]; {
			case e == 'c':
				// \c ends the string there.
				flush()
				return out, true
			case e == '_':
				flush()
			case escaped(e) != 0:
				cur.WriteByte(escaped(e))
				have = true
			default:
				return out, false
			}
		case c == '$':
			have, dynamic = true, true
		default:
			cur.WriteByte(c)
			have = true
		}
	}
	flush()
	return out, true
}

// safeSetFlags and safeSetNames are the options of set that change
// nothing about how the rest of a line is read. The others do: with
// history and histexpand on, "!!:s/echo/rm/" runs a command the line
// never spells out, and -k turns arguments into assignments.
const safeSetFlags = "euxfCEaTbmhvBPtn"

var safeSetNames = map[string]bool{
	"errexit": true, "nounset": true, "xtrace": true, "noglob": true, "noclobber": true, "pipefail": true,
	"errtrace": true, "functrace": true, "allexport": true, "notify": true, "monitor": true, "hashall": true,
	"verbose": true, "braceexpand": true, "physical": true, "onecmd": true, "noexec": true, "ignoreeof": true,
}

// safeShopts are the shopt options that change neither how a line is
// read nor which files a pattern matches.
var safeShopts = map[string]bool{
	"lastpipe": true, "inherit_errexit": true, "checkwinsize": true, "huponexit": true, "cmdhist": true,
	"lithist": true, "histappend": true, "xpg_echo": true, "gnu_errfmt": true, "shift_verbose": true,
	"execfail": true, "checkhash": true, "checkjobs": true, "nocasematch": true,
}

// shellOptions judges a set or shopt command: one that switches on (or
// off) an option outside the lists above makes the rest of the line
// something this package cannot claim to have read.
func (v *bashView) shellOptions(name string, args []word) {
	bad := func(what string) {
		v.setOpaque(name + " changes how the shell reads the rest of the line (" + what + ")")
	}
	for i := 0; i < len(args); i++ {
		if !args[i].static() {
			bad(args[i].raw)
			return
		}
		l := args[i].joined()
		if name == "shopt" {
			switch {
			case l == "-o":
				// The names that follow are set's.
				for _, a := range args[i+1:] {
					if a.static() && !strings.HasPrefix(a.joined(), "-") && !safeSetNames[a.joined()] {
						bad(a.raw)
					}
				}
				return
			case strings.HasPrefix(l, "-"):
			case !safeShopts[l]:
				bad(l)
			}
			continue
		}
		if l == "--" || l == "-" || l == "" || l[0] != '-' && l[0] != '+' {
			return // what follows are the positional parameters
		}
		for k := 1; k < len(l); k++ {
			switch {
			case l[k] == 'o':
				i++
				if i < len(args) && !(args[i].static() && safeSetNames[args[i].joined()]) {
					bad(args[i].raw)
				}
			case strings.IndexByte(safeSetFlags, l[k]) < 0:
				bad(l)
			}
		}
	}
}

// progIsPattern reports whether the word that names the program is one
// the shell would expand into something else: braces or a file name
// pattern. The test commands [ and [[ are names, not patterns.
func progIsPattern(w word) bool {
	return (w.brace || w.glob) && w.raw != "[" && w.raw != "[["
}

// isAssignWord reports whether s looks like NAME=value.
func isAssignWord(s string) bool {
	_, ok := assignName(s)
	return ok
}

// allStatic reports whether every word is known ahead of time.
func allStatic(ws []word) bool {
	for _, w := range ws {
		if !w.static() {
			return false
		}
	}
	return true
}

// allKnown reports whether every word is known ahead of time and is one
// word: nothing for the shell to expand into file names or alternatives.
func allKnown(ws []word) bool {
	for _, w := range ws {
		if !w.static() || w.brace || w.glob {
			return false
		}
	}
	return true
}

// rawJoined is a command's words as written, one space apart. This is the
// text an allow rule is matched against.
func rawJoined(ws []word) string {
	parts := make([]string, len(ws))
	for i, w := range ws {
		parts[i] = w.raw
	}
	return strings.Join(parts, " ")
}

// denyTexts returns every way of writing the command that a deny or ask
// rule is matched against: as written, with quotes removed, with
// expansions dropped, with the program's directory dropped, and with its
// assignments in front.
func (e *Engine) denyTexts(c *simpleCmd) []string {
	if len(c.words) == 0 {
		return nil
	}
	var out []string
	add := func(s string) {
		s = strings.TrimSpace(s)
		if s == "" {
			return
		}
		for _, o := range out {
			if o == s {
				return
			}
		}
		out = append(out, s)
	}
	raw := rawJoined(c.words)
	lits := litsOf(c.words)
	// The words with only what is written of them: a word that is nothing
	// but variables and command output, outside quotes, is no word at all
	// when they come out empty ("echo $X x" is echo x).
	bare := []string{c.words[0].joined()}
	for _, w := range c.words[1:] {
		if j := w.joined(); j != "" || w.quoted {
			bare = append(bare, j)
		}
	}
	add(raw)
	add(strings.Join(lits, " "))
	add(strings.Join(bare, " "))
	if name, ok := e.progName(c.words[0]); ok {
		for _, n := range []string{name, baseName(name)} {
			add(strings.Join(append([]string{n}, lits[1:]...), " "))
			add(strings.Join(append([]string{n}, bare[1:]...), " "))
		}
	}
	if len(c.assigns) > 0 {
		var as []string
		for _, a := range c.assigns {
			as = append(as, a.w.raw)
		}
		add(strings.Join(as, " ") + " " + raw)
	}
	return out
}

// filledText is the command with the variables the line itself set
// filled in, when that makes an argument known that was not: one more
// text for deny and ask rules to match ("x=push; git $x").
func (e *Engine) filledText(v *bashView, c *simpleCmd) string {
	if len(c.words) < 2 {
		return ""
	}
	name, ok := e.progName(c.words[0])
	if !ok {
		return ""
	}
	parts := []string{baseName(name)}
	any := false
	for _, w := range c.words[1:] {
		if w.static() {
			parts = append(parts, w.lit())
			continue
		}
		val, ok := e.filled(v, w)
		if !ok {
			return ""
		}
		any = true
		parts = append(parts, val)
	}
	if !any {
		return ""
	}
	return strings.Join(parts, " ")
}

// strictStrip takes one wrapper off the front of a command, when the
// wrapper is on the short list an allow rule sees through and is written
// in a form this package fully understands. It returns the words that are
// left.
func strictStrip(ws []word) ([]word, bool) {
	if len(ws) < 2 || !plainName(ws[0]) {
		return nil, false
	}
	rest := ws[1:]
	for _, w := range rest {
		if !w.static() {
			return nil, false
		}
	}
	isFlag := func(w word) bool { return strings.HasPrefix(w.joined(), "-") }
	switch ws[0].joined() {
	case "nohup", "builtin", "noglob", "xargs":
		if isFlag(rest[0]) {
			return nil, false
		}
		return rest, true
	case "command":
		if isFlag(rest[0]) {
			return nil, false
		}
		return rest, true
	case "time":
		if rest[0].joined() == "-p" {
			rest = rest[1:]
		}
		if len(rest) == 0 || isFlag(rest[0]) {
			return nil, false
		}
		return rest, true
	case "nice":
		switch l := rest[0].joined(); {
		case l == "-n" && len(rest) > 2 && isInt(rest[1].joined()):
			rest = rest[2:]
		case strings.HasPrefix(l, "-n") && isInt(l[2:]), strings.HasPrefix(l, "--adjustment=") && isInt(l[13:]), len(l) > 1 && l[0] == '-' && isInt(l[1:]):
			rest = rest[1:]
		}
		if len(rest) == 0 || isFlag(rest[0]) {
			return nil, false
		}
		return rest, true
	case "stdbuf":
		for len(rest) > 0 && isFlag(rest[0]) {
			l := rest[0].joined()
			ok := len(l) >= 3 && strings.IndexByte("ioe", l[1]) >= 0 && l[0] == '-' && l[1] != '-' ||
				strings.HasPrefix(l, "--input=") || strings.HasPrefix(l, "--output=") || strings.HasPrefix(l, "--error=")
			if !ok {
				return nil, false
			}
			rest = rest[1:]
		}
		if len(rest) == 0 {
			return nil, false
		}
		return rest, true
	case "timeout":
		for len(rest) > 0 && isFlag(rest[0]) {
			l := rest[0].joined()
			switch {
			case l == "--foreground" || l == "--preserve-status" || l == "-v" || l == "--verbose",
				strings.HasPrefix(l, "--signal=") || strings.HasPrefix(l, "--kill-after="):
				rest = rest[1:]
			case (l == "-s" || l == "--signal" || l == "-k" || l == "--kill-after") && len(rest) > 1:
				rest = rest[2:]
			default:
				return nil, false
			}
		}
		if len(rest) < 2 || !isDuration(rest[0].joined()) || isFlag(rest[1]) {
			return nil, false
		}
		return rest[1:], true
	}
	return nil, false
}

// isInt reports whether s is a whole number, with or without a sign.
func isInt(s string) bool {
	s = strings.TrimPrefix(strings.TrimPrefix(s, "-"), "+")
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// isDuration reports whether s is a number with an optional unit, as
// timeout takes it.
func isDuration(s string) bool {
	if s != "" && strings.IndexByte("smhd", s[len(s)-1]) >= 0 {
		s = s[:len(s)-1]
	}
	whole, frac, _ := strings.Cut(s, ".")
	return isInt(whole) && !strings.HasPrefix(whole, "-") && !strings.HasPrefix(whole, "+") && (frac == "" || isInt(frac) && frac[0] >= '0' && frac[0] <= '9')
}

// prefixOnlyExact reports whether a command is one a wildcard rule never
// approves: a wrapper that runs whatever it is given, or a find that
// runs, deletes or writes. Only a rule that spells out the whole command
// approves those.
func prefixOnlyExact(ws []word) bool {
	if len(ws) == 0 {
		return false
	}
	switch progKey(baseName(ws[0].lit())) {
	case "watch", "setsid", "ionice", "flock":
		return true
	case "find":
		for _, w := range ws[1:] {
			// (A word that is only known when the line runs may be any of
			// these.)
			l := w.joined()
			if !w.static() || strings.HasPrefix(l, "-exec") || strings.HasPrefix(l, "-ok") || strings.HasPrefix(l, "-fprint") ||
				l == "-delete" || l == "-rm" || l == "-fls" || l == "-files0-from" {
				return true
			}
		}
	}
	return false
}

// sourcedArg judges w as the file source or . runs.
func (e *Engine) sourcedArg(v *bashView, c *simpleCmd, prev *simpleCmd, nest nesting, depth int, w word) {
	switch {
	case w.hasSubst():
		v.setOpaque("a script that a command prints is run in this shell")
	case !w.static() || w.glob || w.brace:
		// A name that is not known ahead of time (a variable, a pattern,
		// braces) may be /dev/stdin: what is fed to the command is then
		// its script.
		e.fedScript(v, c, prev, nest, depth, true)
	default:
		f := w.joined()
		if strings.HasPrefix(f, "/") {
			f = oneDisk(cleanPath(f))
		}
		if f == "/dev/stdin" || strings.HasPrefix(f, "/dev/fd/") || strings.HasPrefix(f, "/proc/self/fd/") || strings.HasPrefix(f, "/proc/thread-self/fd/") {
			if !e.fedScript(v, c, prev, nest, depth, true) {
				v.bareShell = true
			}
		}
	}
}

// mayVanish reports whether a word may come to no word at all when the
// line runs: nothing in it but what is expanded, outside quotes (an empty
// value is then no word), or every word of a list ("$@", "${a[@]}"), which
// may hold none.
func mayVanish(w word) bool {
	if w.static() || w.joined() != "" {
		return false
	}
	if !w.quoted {
		return true
	}
	for _, p := range w.parts {
		if !(p.kind == partVar && p.all) {
			return false
		}
	}
	return true
}
