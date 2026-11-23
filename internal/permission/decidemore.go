package permission

import (
	"encoding/json"
	"path"
	"regexp"
	"strings"
)

// hasGuards reports whether any deny or ask rule of the given kind is in
// force.
func (e *Engine) hasGuards(kind ruleKind) bool {
	for _, list := range []List{ListDeny, ListAsk} {
		for _, c := range e.rules(list) {
			if c.kind == kind {
				return true
			}
		}
	}
	return false
}

// safety runs the checks that no allow rule and no hook approval passes.
// It reports true when one of them settled the call.
func (e *Engine) safety(req *request) (Decision, bool) {
	ask := func(reason string) (Decision, bool) {
		return Decision{Behavior: Ask, Stage: StageSafety, Reason: reason}, true
	}
	bypass := e.mode == ModeBypass
	if v := req.view; v != nil {
		// A guard that could not be checked has not passed.
		if c := e.couldHit(v); c != nil {
			return Decision{Behavior: Ask, Stage: StageSafety, Rule: c.Rule.String(), Source: c.layer.Name,
				Reason: "The " + string(c.list) + " rule " + c.Rule.String() + " from " + c.layer.Name + " could apply to this command, but part of the command is only known when it runs, so the rule could not be checked."}, true
		}
		if v.opaque != "" && e.hasGuards(kindCommand) {
			return ask("Part of this command cannot be read ahead of time (" + v.opaque + "), so the deny and ask rules for commands could not be checked against it.")
		}
		// The same holds for the Read and Edit rules and a file a command
		// names in a way that cannot be known ahead of time. With
		// permissions skipped this one is let go: nearly any variable
		// could name a guarded file, so asking would stop most of what an
		// unattended session runs, and those rules are a guard against
		// the usual way of naming a file, not a wall.
		if req.pathOpaque != "" && !bypass && e.hasGuards(kindPath) {
			return ask("This command cannot be checked against the Read and Edit rules: " + req.pathOpaque + ".")
		}
		// Not so for what the line itself spells out in more values than
		// gnosh follows: padding a line is not a way past a rule.
		if req.pathCapped != "" && e.hasGuards(kindPath) {
			return ask("This command cannot be checked against the Read and Edit rules: " + req.pathCapped + ".")
		}
		dirs := e.lineDirs(v)
		if v.pwds == nil {
			v.pwds = dirs
		}
		fromSubst := map[string]bool{}
		for _, u := range v.units {
			for _, a := range u.cmd.assigns {
				if a.fromSubst {
					fromSubst[a.name] = true
				}
			}
		}
		for _, u := range v.units {
			if why := e.removalDanger(v, u.cmd, dirs, fromSubst); why != "" {
				return Decision{Behavior: Ask, Stage: StageSafety, Critical: true,
					Reason: dangerWhat(why) + " (" + why + "). No rule approves it; a person decides."}, true
			}
		}
		if v.manyNames != "" {
			// What runs is one of more programs than gnosh follows, each
			// of them written out in the line: an rm among them is not
			// ruled out, and padding a line is not a way past this check.
			return Decision{Behavior: Ask, Stage: StageSafety, Critical: true,
				Reason: "A dangerous removal cannot be ruled out: the line gives the program it runs (" + v.manyNames + ") more values than gnosh follows."}, true
		}
		// A line, or a script inside it that will run (the argument of an
		// eval), that could not be read and that holds an rm: its crude
		// pieces say so, and nothing says what the rm is given.
		crude, why := v.unread, v.opaque
		if !v.sc.ok {
			crude, why = v.frags, v.sc.why
			// The crude pieces are cut at every brace and every < and >,
			// which parts a name written inside a ${...} from the words it
			// is handed ("${X:-rsync} -a --delete src/ ."), and a command
			// from what follows its redirection. So the whole line is cut
			// once more, at what ends a command and nothing else.
			crude = append(append([]string(nil), crude...), looseCommands(v.text)...)
			crude = append(crude, rawCommands(v.text)...)
			// Where the reader stops, the shells go on: a backslash at the
			// end of a line joins it to the next, and a descriptor written
			// with more than one digit ("02>&1") is a word to dash and a
			// descriptor to bash. The line is read as they read it, and
			// what it would run is judged.
			for _, alt := range shellReadings(v.text) {
				av := e.viewBash(alt)
				if !av.sc.ok {
					crude = append(crude, alt)
					continue
				}
				for _, u := range av.units {
					if why := e.removalDanger(av, u.cmd, dirs, nil); why != "" {
						return Decision{Behavior: Ask, Stage: StageSafety, Critical: true,
							Reason: dangerWhat(why) + " (" + why + "). gnosh could not read the whole command (" + v.sc.why + "); read as a shell reads it, this is what it runs. No rule approves it; a person decides."}, true
					}
				}
			}
		}
		for _, f := range crude {
			// (The words are cut the loosest way, so that a name inside a
			// ${...} or behind quotes is still a word: see looseFields.)
			fields := looseFields(f)
			for i := 0; i < len(fields) && i < 3; i++ {
				if n := baseName(fields[i]); n == "rm" || n == "rmdir" {
					return Decision{Behavior: Ask, Stage: StageSafety, Critical: true,
						Reason: "A dangerous removal cannot be ruled out: gnosh could not read the whole command (" + why + "), and the shell may read it differently."}, true
				}
			}
			// The other commands that destroy a directory which has to be
			// kept are judged by what the piece says they are handed, when
			// the piece can be read as a line of its own: "coproc mv .git
			// /tmp/g" holds the piece "mv .git /tmp/g".
			if danger := e.crudeDanger(f, dirs); danger != "" {
				return Decision{Behavior: Ask, Stage: StageSafety, Critical: true,
					Reason: "This command could destroy something that has to be kept (" + danger + "). gnosh could not read the whole command (" + why + "), so this is what a piece of it says. No rule approves it; a person decides."}, true
			}
		}
		// And where what could not be read is the very word such a command
		// is handed ("mv ${HOME:0:${#HOME}} /tmp/h"), the pieces say
		// nothing: a line that names one of those commands and the home
		// or the working directory is not ruled out either.
		if len(crude) > 0 && (strings.Contains(v.text, "HOME") || strings.Contains(v.text, "PWD") || strings.Contains(v.text, "~")) {
			for _, f := range crude {
				for _, field := range looseFields(f) {
					switch progKey(baseName(field)) {
					case "mv", "chmod", "chown", "chgrp", "truncate", "shred", "rsync":
						return Decision{Behavior: Ask, Stage: StageSafety, Critical: true,
							Reason: "A command that could destroy the home or the working directory cannot be ruled out: gnosh could not read the whole command (" + why + "), and it names " + field + " and one of those directories."}, true
					}
				}
			}
		}
		if !v.sc.ok && e.hasGuards(kindCommand) {
			// The crude pieces of the line met no deny rule (that was
			// checked first), but a crude cut does not undo every way of
			// writing a command. A guard that could not be checked has not
			// passed.
			return ask("gnosh could not read the whole command (" + v.sc.why + "), so the deny and ask rules for commands could not be checked against it.")
		}
		if req.remote != "" && !bypass {
			return Decision{Behavior: Ask, Stage: StageSafety, hookAllow: true,
				Reason: "The command runs on the host " + req.remote + ". Allow rules and the read-only list are about this machine, so a person decides."}, true
		}
	}
	if req.byNumber != "" && (!bypass || e.hasGuards(kindPath)) {
		return ask("This names a file by a number (" + req.byNumber + ") and not by where it is, so the rules about files and the protected paths could not be checked against it.")
	}
	if req.info.class == classFetch && req.host == "" && e.hasGuards(kindDomain) {
		return ask("The URL has no host gnosh can read, so the rules about hosts could not be checked against it.")
	}
	if bypass {
		return Decision{}, false
	}
	for _, p := range req.writes {
		if why := e.protectedWhy(p); why != "" {
			return ask("This would change " + p + ": " + why + ". A change there always asks, whatever rule allows the tool.")
		}
	}
	if req.unresolved {
		return ask("A path in this call has links that could not be followed, so gnosh cannot tell where it really leads.")
	}
	if e.gitRuns && req.view != nil {
		for _, u := range req.view.units {
			if len(u.cmd.words) > 0 {
				if name, ok := e.progName(u.cmd.words[0]); ok && progKey(baseName(name)) == "git" {
					return ask("This repository's own git settings name programs that git would run, and the folder is not trusted, so git commands ask first.")
				}
			}
		}
	}
	if e.repoOf != nil && req.view != nil {
		if other := e.otherRepo(); other != "" {
			for _, u := range req.view.units {
				if len(u.cmd.words) > 0 {
					if name, ok := e.progName(u.cmd.words[0]); ok && progKey(baseName(name)) == "git" {
						return ask("The shell's folder, " + e.cur + ", is in another repository than the working directories' (its git directory is " + other + "), whose own git settings could name programs that git would run, so git commands there ask first.")
					}
				}
			}
		}
	}
	return Decision{}, false
}

// otherRepo returns the git directory of the repository the shell's
// folder is in, when that is none of the working directories' own
// repositories, nor a work tree or submodule of one of them (their git
// directories lie inside the main one's); "" when it is one of theirs, or
// the folder is in none.
func (e *Engine) otherRepo() string {
	gd := e.repoOf(e.cur)
	if gd == "" {
		return ""
	}
	for _, d := range e.workDirs() {
		if own := e.repoOf(d); own != "" && inside(own, gd) {
			return ""
		}
	}
	return gd
}

// looseFields cuts a crude piece of a line into words the loosest way
// there is: quotes, backslashes and backticks are taken out, and every
// character that cannot stand in a name, a path, a flag or a pattern is
// taken for a blank. A name that the line writes inside a ${...}, behind
// quotes or after an = then stands as a word of its own: "${HOME:+chmod}"
// is HOME and chmod, r”m is rm, and X=mv is X and mv. This is for
// looking for the name of a command that destroys things in a line nobody
// could read, where more words than the shell makes are more to judge and
// a word that is missed is a directory gone.
func looseFields(piece string) []string {
	var b strings.Builder
	for i := 0; i < len(piece); i++ {
		c := piece[i]
		switch {
		case c == '\'' || c == '"' || c == '\\' || c == '`':
		case c == '$' && i+1 < len(piece) && piece[i+1] == '{':
			// ${name<op>word}: the name, and then the word, without the
			// operator between them, whose - or + would else stick to the
			// word ("${X:-rm}" is X and rm).
			b.WriteByte(' ')
			i += 2
			for i < len(piece) && isNameChar(piece[i]) {
				b.WriteByte(piece[i])
				i++
			}
			b.WriteByte(' ')
			for i < len(piece) && strings.IndexByte(":-+=?#%!@*^,/", piece[i]) >= 0 {
				i++
			}
			i--
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c >= 0x80, strings.IndexByte("_./~*?-@%,[]", c) >= 0:
			b.WriteByte(c)
		default:
			b.WriteByte(' ')
		}
	}
	return strings.Fields(b.String())
}

// looseCommands cuts a line nobody could read into what may be its
// commands, at line breaks, semicolons, ampersands and bars and at nothing
// else, each written out as its looseFields with one blank between them.
// dangerWhat names what the removal check found, by why it found it.
func dangerWhat(why string) string {
	if !strings.HasPrefix(why, "rm") && !strings.Contains(why, " removes what it finds: ") {
		// (mv, chmod -R, git clean, dd, rsync --delete: not removals by
		// name, and as final.)
		return "This command could destroy something that has to be kept"
	}
	return "This removal looks dangerous"
}

// shellReadings gives the line as the shells read what gnosh's reader
// stops at: every backslash at the end of a line joined to the next line,
// as bash and dash do outside single quotes ("$a -\<newline>a" is $a -a);
// every $"..." as what bash makes of it, a message catalog's translation
// of the text, which nobody can know ahead of time and so stands, whole,
// as the output of a command ("$(:)", judged as any word a command makes);
// and then each descriptor of more than one digit read both ways (see
// fdReadings). It gives nothing for a line that has none of them. (The
// changes are made in the text as it is written, quoted or not: what this
// gives is only ever judged for danger, never let go.)
func shellReadings(line string) []string {
	var out []string
	if n := len(line) - len(strings.TrimRight(line, "\\")); n%2 == 1 {
		// A backslash that ends the line, with nothing after it, is a
		// backslash to bash and dash, where the reader stops at it: "$V
		// .git /tmp/g\" with V=mv moved .git to /tmp/g\, and the reading
		// held "mv .git" alone, which destroys nothing, so the line ran
		// with permissions skipped (oracle seed 159).
		line += "\\"
		out = append(out, line)
	}
	if strings.Contains(line, "\\\n") {
		line = strings.ReplaceAll(line, "\\\n", "")
		out = append(out, line)
	}
	if strings.Contains(line, "$\"") {
		// (Oracle seed 128: "mv $\"x\" <(env) d" was cut into crude pieces
		// in which <(env) was the plain word env, and went unasked with
		// permissions skipped.)
		line = translated.ReplaceAllString(line, `"$$(:)"`)
		// (One that is never closed is read from the $ on.)
		line = strings.ReplaceAll(line, "$\"", "\"$(:)")
		out = append(out, line)
	}
	return append(out, fdReadings(line)...)
}

// translated finds a $"..." string.
var translated = regexp.MustCompile(`\$"(?:[^"\\]|\\.)*"`)

// manyDigitFD finds a redirection whose descriptor is written with more
// than one digit, with what stands before it.
var manyDigitFD = regexp.MustCompile("(^|[\\s;&|()`])([0-9]{2,})([<>])")

// fdReadings gives the line as dash reads its descriptors of more than
// one digit, each of them a word of its own ("chmod -R 02>&1 ." is chmod
// -R 02 . with >&1), and as bash does, where the digits name the
// descriptor and the words are the same without them. It gives nothing
// for a line that has no such descriptor. (The digits are found in the
// text as it is written, quoted or not: what this gives is only ever
// judged for danger, never let go.)
func fdReadings(line string) []string {
	if !manyDigitFD.MatchString(line) {
		return nil
	}
	return []string{
		manyDigitFD.ReplaceAllString(line, "$1$2 $3"),
		manyDigitFD.ReplaceAllString(line, "$1$3"),
	}
}

func looseCommands(line string) []string {
	var out []string
	for _, piece := range strings.FieldsFunc(strings.ReplaceAll(line, "\\\n", ""), func(r rune) bool {
		return r == '\n' || r == ';' || r == '&' || r == '|'
	}) {
		if fields := looseFields(piece); len(fields) > 0 {
			out = append(out, strings.Join(fields, " "))
		}
	}
	return out
}

// rawCommands cuts a line nobody could read at the line breaks,
// semicolons, ampersands and bars that stand outside quotes, backquotes,
// (...) and ${...}, and gives each piece as it is written. looseCommands
// takes a piece apart into plain words: after a "coproc kill;" that stops
// the reader, "mv <(date) 0" was "mv date 0", which destroys nothing,
// while bash moved /dev/fd/63, a name for what a command prints (oracle
// seed 161). A piece as it is written keeps its <(...).
func rawCommands(line string) []string {
	var out []string
	start := 0
	scanShell(line, func(i, depth int) bool {
		c := line[i]
		if depth != 0 || strings.IndexByte("\n;&|", c) < 0 {
			return true
		}
		// (>&, <&, >| and &> are redirections.)
		if (c == '&' || c == '|') && i > 0 && (line[i-1] == '>' || line[i-1] == '<') ||
			c == '&' && i+1 < len(line) && line[i+1] == '>' {
			return true
		}
		if p := strings.TrimSpace(line[start:i]); p != "" {
			out = append(out, p)
		}
		start = i + 1
		return true
	})
	if p := strings.TrimSpace(line[start:]); p != "" {
		out = append(out, p)
	}
	return out
}

// scanShell walks a line as far as telling quotes and nesting goes: f is
// called with the index of each byte that stands outside quotes and
// backquotes, and how deep in (...) and ${...} it stands. A ) or } is told
// at the depth it closes to, so one that closes nothing comes at -1 (and
// the walk goes on at 0). A quote never closed ends the walk, and so does
// f returning false.
func scanShell(s string, f func(i, depth int) bool) {
	depth := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch c {
		case '\\':
			i++
			continue
		case '\'', '"', '`':
			j := i + 1
			for j < len(s) && s[j] != c {
				if c != '\'' && s[j] == '\\' {
					j++
				}
				j++
			}
			if j >= len(s) {
				return
			}
			i = j
			continue
		}
		at := depth
		switch {
		case c == '(', c == '$' && i+1 < len(s) && s[i+1] == '{':
			depth++
		case c == ')', c == '}' && depth > 0:
			depth--
			at = depth
		}
		if !f(i, at) {
			return
		}
		depth = max(depth, 0)
	}
}

// nameTails gives, for each word of a piece as it is written that names
// a command crudeDanger judges, the piece from that name on, up to a ) or
// } that closes what was opened before the name. What stands before the
// name may be a word of the shell's own that keeps the piece from being
// read ("do", "coproc", "{"), or the opening of the $(...) it is in.
func nameTails(piece string) []string {
	var out []string
	for i := 0; i < len(piece); i++ {
		if i > 0 && strings.IndexByte(" \t\n({`!", piece[i-1]) < 0 {
			continue
		}
		j := i
		for j < len(piece) && strings.IndexByte(" \t\n;&|()<>`", piece[j]) < 0 {
			j++
		}
		name := strings.NewReplacer("'", "", "\"", "", "\\", "").Replace(piece[i:j])
		if !crudeDestroyer(progKey(baseName(name))) {
			continue
		}
		tail := piece[i:]
		scanShell(tail, func(k, depth int) bool {
			if depth < 0 {
				tail = tail[:k]
				return false
			}
			return true
		})
		out = append(out, tail)
	}
	return out
}

// crudeDestroyer reports whether a program, by its name, is one that
// crudeDanger judges by what it is handed.
func crudeDestroyer(name string) bool {
	switch name {
	case "mv", "chmod", "chown", "chgrp", "git", "dd", "truncate", "shred", "rsync", "find":
		return true
	}
	return false
}

// crudeDanger reads one crude piece of a line that could not be followed
// and says why a command in it would destroy something that has to be
// kept, or "". The piece is read as a line of its own where it can be,
// and its words are taken as they are written from each word on that
// names such a command: where the piece cannot be read ("chmod -R 00$[0
// ."), and where the name is not the piece's own program, which another
// piece of the line may have made stand for nothing ("zz mv .git /tmp/g"
// after an "alias zz=").
func (e *Engine) crudeDanger(piece string, dirs []string) string {
	fields := looseFields(piece)
	var from []int
	for i, f := range fields {
		if crudeDestroyer(progKey(baseName(f))) {
			from = append(from, i)
		}
	}
	if len(from) == 0 {
		return ""
	}
	read := false
	if pv := e.viewBash(piece); pv.sc.ok {
		for _, u := range pv.units {
			if why := e.removalDanger(pv, u.cmd, dirs, nil); why != "" {
				return why
			}
		}
		read = true
	}
	plain := &bashView{sc: &script{ok: true}, vars: map[string]*lineVar{}}
	// From each such name on, the piece as it is written, read as a line
	// of its own: so a word of the shell's own before the name does not
	// keep it from being read ("do mv <(date) 0"), and what the command is
	// handed keeps its <(...), which the words below take apart. Where
	// that cannot be read either, its words as they are written: a <(...)
	// among them stands for whatever a command prints, not for the text
	// it holds ("mv <(wget =x${X:} origin) db/s", where the ${X:} kept
	// the line from being read: oracle seed 163).
	for _, tail := range nameTails(piece) {
		if tail == piece && read {
			continue
		}
		if tv := e.viewBash(tail); tv.sc.ok {
			for _, u := range tv.units {
				if why := e.removalDanger(tv, u.cmd, dirs, nil); why != "" {
					return why
				}
			}
		} else if ws := writtenWords(tail); len(ws) > 0 {
			if why := e.removalDanger(plain, &simpleCmd{words: ws}, dirs, nil); why != "" {
				return why
			}
		}
	}
	// The words from the command's name on, as they are written. In a
	// piece that could be read this is for a name that is not the piece's
	// own program: what stands before it may be nothing once the line
	// runs, made so in a piece of the line that this one does not hold
	// ("alias zz=" on the line before, and then "zz rsync -a --delete src/
	// ."). The piece's own program was judged above, as it was read.
	for _, i := range from {
		if read && i == 0 {
			continue
		}
		// From the name on the words are read as a line of their own where
		// they can be, so that a pattern among them is a pattern ("zz shred
		// -u *"), and as plain text where they cannot.
		if tv := e.viewBash(strings.Join(fields[i:], " ")); tv.sc.ok {
			for _, u := range tv.units {
				if why := e.removalDanger(tv, u.cmd, dirs, nil); why != "" {
					return why
				}
			}
			continue
		}
		var ws []word
		for _, f := range fields[i:] {
			ws = append(ws, litOf(f))
		}
		if why := e.removalDanger(plain, &simpleCmd{words: ws}, dirs, nil); why != "" {
			return why
		}
	}
	return ""
}

// writtenWords cuts a command that could not be read, from its name on,
// into its words as they are written: at blanks outside quotes, (...) and
// ${...}, up to what ends the command. A process substitution, <(...) or
// >(...), is a word that stands for whatever a command prints; the others
// are their text without quotes and backslashes.
func writtenWords(tail string) []word {
	cut := make([]bool, len(tail))
	end := len(tail)
	scanShell(tail, func(i, depth int) bool {
		if depth != 0 {
			return true
		}
		switch tail[i] {
		case ' ', '\t':
			cut[i] = true
		case '\n', ';', '|', '&':
			end = i
			return false
		}
		return true
	})
	var ws []word
	unquote := strings.NewReplacer("'", "", "\"", "", "\\", "")
	for i := 0; i < end; {
		if cut[i] {
			i++
			continue
		}
		j := i
		for j < end && !cut[j] {
			j++
		}
		w := tail[i:j]
		if strings.HasPrefix(w, "<(") || strings.HasPrefix(w, ">(") {
			ws = append(ws, word{raw: w, parts: []wordPart{{kind: partSubst, proc: true}}})
		} else {
			ws = append(ws, litOf(unquote.Replace(w)))
		}
		i = j
	}
	return ws
}

// decideRead judges Read, Grep and Glob: inside the working directories,
// or where an allow rule reaches, they run.
func (e *Engine) decideRead(req *request) Decision {
	if c := e.match(ListAllow, req); c != nil {
		return allowedBy(c)
	}
	var rule *crule
	for _, p := range req.reads {
		in := false
		for _, d := range e.workDirs() {
			in = in || inside(d, p)
		}
		if in {
			continue
		}
		if c := e.allowsPath(p, false); c != nil {
			rule = c
			continue
		}
		dir := path.Dir(p)
		d := Decision{Behavior: Ask, Stage: StageMode, hookAllow: true,
			Reason: p + " is outside the working directories, and reading there asks first."}
		d.Suggestions = []Suggestion{{Rules: []string{"Read(/" + EscapePath(p) + ")"}, Scope: GrantSession}}
		if dir != "/" && e.criticalDir(dir) == "" {
			d.Suggestions = append(d.Suggestions, Suggestion{Rules: []string{"Read(/" + EscapePath(dir) + "/**)"}, Scope: GrantSession})
		}
		return d
	}
	if rule != nil {
		return allowedBy(rule)
	}
	return Decision{Behavior: Allow, Stage: StageMode, Reason: "Reading inside the working directories needs no approval."}
}

// PresentPlan ends plan mode's refusal of an edit. A program that offers
// no ExitPlanMode, nobody being there to approve a plan, says something
// else in its place.
const PresentPlan = "Present the plan with ExitPlanMode."

// decideEdit judges Edit, Write and NotebookEdit.
func (e *Engine) decideEdit(req *request) Decision {
	if e.mode == ModePlan {
		return Decision{Behavior: Deny, Stage: StageMode,
			Reason: "Plan mode is on: nothing is changed until the plan is approved. " + PresentPlan}
	}
	if c := e.match(ListAllow, req); c != nil {
		return allowedBy(c)
	}
	var rule *crule
	covered, within := true, true
	for _, p := range req.writes {
		if c := e.allowsPath(p, true); c != nil {
			rule = c
		} else {
			covered = false
		}
		in := false
		for _, d := range e.workDirs() {
			in = in || inside(d, p)
		}
		within = within && in
	}
	if covered && rule != nil {
		return allowedBy(rule)
	}
	if e.mode == ModeAcceptEdits && within {
		return Decision{Behavior: Allow, Stage: StageMode, Reason: "acceptEdits lets file edits inside the working directories run."}
	}
	d := Decision{Behavior: Ask, Stage: StageMode, hookAllow: true, Reason: "Changing a file asks first in this mode."}
	if !within {
		d.Reason = "This changes a file outside the working directories, which asks first."
	}
	var rules []string
	for _, p := range req.writes {
		rules = append(rules, "Edit(/"+EscapePath(p)+")")
	}
	d.Suggestions = []Suggestion{{Rules: rules, Scope: GrantSession}}
	if within {
		d.Suggestions = append(d.Suggestions, Suggestion{Mode: ModeAcceptEdits, Scope: GrantSession})
	}
	return d
}

// decideFetch judges WebFetch.
func (e *Engine) decideFetch(req *request) Decision {
	if c := e.match(ListAllow, req); c != nil {
		return allowedBy(c)
	}
	d := Decision{Behavior: Ask, Stage: StageMode, hookAllow: true, Reason: "Fetching from the web asks first."}
	if req.host == "" {
		d.Reason = "The URL has no host gnosh can read, so a person decides."
		return d
	}
	d.Reason = "Fetching from " + req.host + " asks first."
	if !strings.Contains(req.host, "*") {
		// (A * in a host is a wildcard in the rule it would make: an
		// answer for https://*.com/x would let every .com be fetched
		// unasked; the approvals review of 2026-11-19.)
		d.Suggestions = []Suggestion{{Rules: []string{"WebFetch(domain:" + req.host + ")"}, Scope: GrantLocal}}
	}
	return d
}

// decideOther judges every tool with no rule family of its own: it asks
// unless an allow rule names the tool.
func (e *Engine) decideOther(req *request) Decision {
	plan := e.mode == ModePlan
	if req.canon == "ExitPlanMode" {
		// The plan is the person's alone to approve: no allow rule, no
		// hook and no channel approves a plan nobody has read, and the
		// session leaves plan mode only on their yes (the plan review of
		// 2026-11-19). Approving it can also accept the edits that carry
		// it out. Outside plan mode there is no plan to approve, and its
		// yes switched the mode whatever the session was in.
		if !plan {
			return Decision{Behavior: Deny, Stage: StageMode,
				Reason: "ExitPlanMode is for plan mode, and the session is in the " + string(e.mode) + " mode: there is no plan to present. Carry the task out, or switch to plan mode with EnterPlanMode first."}
		}
		return Decision{Behavior: Ask, Stage: StageMode, PersonOnly: true,
			Reason:      "The plan is ready; a person approves it or asks for more.",
			Suggestions: []Suggestion{{Mode: ModeAcceptEdits}}}
	}
	// While planning, an approval reaches only what does not change
	// anything: searching the web.
	holds := plan && req.canon != toolWebSearch
	if !holds {
		if c := e.match(ListAllow, req); c != nil {
			return allowedBy(c)
		}
	}
	d := Decision{Behavior: Ask, Stage: StageMode, hookAllow: !holds, PersonOnly: holds, Reason: req.canon + " asks first unless an allow rule names it."}
	if holds {
		d.Reason = "Plan mode: " + req.canon + " may change things, so a person decides."
		return d
	}
	switch {
	case req.canon == toolWebSearch:
		d.Suggestions = []Suggestion{{Rules: []string{toolWebSearch}, Scope: GrantLocal}}
	case req.info.class == classSkill:
		name, args := inputString(req.input, "skill"), inputString(req.input, "args")
		name = strings.TrimPrefix(name, "/")
		if name != "" && !strings.ContainsAny(name, " \t*()\\") {
			rule := "Skill(" + name + ")"
			if strings.TrimSpace(args) != "" {
				rule = "Skill(" + name + " *)"
			}
			d.Suggestions = []Suggestion{{Rules: []string{rule}, Scope: GrantSession}}
		}
	default:
		if !hasGlob(req.canon) && !strings.ContainsAny(req.canon, "() \t") {
			d.Suggestions = []Suggestion{{Rules: []string{req.canon}, Scope: GrantSession}}
		}
	}
	return d
}

// inputString returns the string value of one input field, or "".
func inputString(input map[string]json.RawMessage, key string) string {
	var s string
	if raw, ok := input[key]; ok {
		_ = json.Unmarshal(raw, &s)
	}
	return s
}

// allowedBy is the decision that an allow rule let the call run.
func allowedBy(c *crule) Decision {
	return Decision{Behavior: Allow, Stage: StageAllow, Rule: c.Rule.String(), Source: c.layer.Name,
		Reason: "The allow rule " + c.Rule.String() + " from " + c.layer.Name + " covers this."}
}
