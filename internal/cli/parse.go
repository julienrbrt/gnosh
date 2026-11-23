package cli

import (
	"fmt"
	"strings"
)

// occurrence is one flag as it was typed.
type occurrence struct {
	f *flag
	// typed is the flag's spelling as typed, without any value: "-C",
	// "--allowed-tools".
	typed string
	// value is the value of a one-value flag, and has says one was given.
	value string
	has   bool
	// raw is the arguments a list flag took, as typed.
	raw []string
	// choice is the choice value names, once it has been checked.
	choice *choice
}

// parsed is a command line taken apart: which command, which flags with
// which values, and which arguments are left over. Nothing in it has been
// checked against anything but the table's spellings.
type parsed struct {
	t   *Table
	cmd *command
	// occs is the flags in the order they were typed.
	occs []occurrence
	// args is the arguments that are neither flags nor flags' values.
	args []string
	// early is how many of args came before a "--".
	early int
	// help says -h or --help was typed.
	help bool
	// promptOnly says a flag was typed after which no word names a command.
	promptOnly bool
	// name is the last command's name as it was typed (an alias, perhaps).
	name string
}

// walk takes a command line apart, left to right.
//
// An argument is read as one of five things, in this order of asking:
// everything after a lone "--" is an argument and nothing else; "--name"
// and "--name=value" are a flag spelled as a word; "-x" and "-xyz" are one
// or more one-letter flags; the first plain word is a command if the
// command being read has one by that name; anything else is an argument.
// A flag that takes a value takes it from the arguments that follow, as its
// kind says.
func (t *Table) walk(args []string) (*parsed, *Error) {
	p := &parsed{t: t, cmd: t.root}
	ended := false // a lone "--" has been passed
	for i := 0; i < len(args); i++ {
		a := args[i]
		if p.help {
			// Help was asked for. The rest of the line can only say which
			// command's help, and is otherwise not read.
			if !ended && !strings.HasPrefix(a, "-") && len(p.args) == 0 {
				if sub := p.cmd.sub(a); sub != nil {
					p.cmd = sub
					continue
				}
			}
			if a == "--" {
				ended = true
			}
			continue
		}
		if ended {
			p.args = append(p.args, a)
			continue
		}
		var err *Error
		switch {
		case a == "--":
			ended = true
			p.early = len(p.args)
		case strings.HasPrefix(a, "--"):
			i, err = p.word(args, i)
		case a == "-" && p.cmd != nil && p.cmd.dashIsStdin:
			err = p.plain(args, i)
		case a == "-":
			err = &Error{Kind: ErrUsage, Typed: a, Message: fmt.Sprintf(
				"- by itself means nothing to gnosh, so %s. Text piped in is read without being asked for; to send a dash as an argument, put -- before it.",
				nothing(p.cmd))}
		case strings.HasPrefix(a, "-"):
			i, err = p.letters(args, i)
		default:
			err = p.plain(args, i)
		}
		if err != nil {
			return nil, err
		}
	}
	if !ended {
		p.early = len(p.args)
	}
	return p, nil
}

// plain reads an argument that does not begin with a dash: a command's
// name where one can stand, and otherwise an argument of the command.
func (p *parsed) plain(args []string, i int) *Error {
	a := args[i]
	c := p.cmd
	if len(p.args) == 0 && len(c.subs) > 0 && !p.promptOnly {
		if sub := c.sub(a); sub != nil {
			if sub.afterAnything {
				// As with --help and --version, what else is on the line
				// does not matter.
				p.occs = nil
			}
			for k := range p.occs {
				if !sub.takes(p.occs[k].f) {
					return p.misplaced(&p.occs[k], sub, a)
				}
			}
			p.cmd, p.name = sub, a
			return nil
		}
		if c.needSub {
			return p.unknownCommand(a)
		}
	}
	p.args = append(p.args, a)
	return nil
}

// word reads a flag spelled as a word: "--name" or "--name=value". It
// returns the index of the last argument it used.
func (p *parsed) word(args []string, i int) (int, *Error) {
	a := args[i]
	name, attached, hasEq := strings.Cut(a[2:], "=")
	f := p.cmd.long(name)
	if f == nil {
		return i, p.unknownFlag(a, "--"+name)
	}
	return p.take(args, i, f, "--"+name, attached, hasEq, true)
}

// letters reads one-letter flags: "-x", or several sharing a dash, "-xyz".
// Every letter must be a flag; one that takes a value must come last, and
// its value is the next argument. It returns the index of the last argument
// it used.
func (p *parsed) letters(args []string, i int) (int, *Error) {
	a := args[i]
	// A word typed with one dash, as some programs take them. No flag's
	// word is made only of one-letter flags, so this takes nothing away.
	if name, _, _ := strings.Cut(a[1:], "="); len([]rune(name)) > 1 && p.cmd.long(name) != nil {
		return i, &Error{Kind: ErrUnknownFlag, Typed: a, Suggestion: "--" + name, Message: fmt.Sprintf(
			"%s is not a flag of %s, so %s. Flags spelled as words take two dashes: did you mean --%s?",
			shown(a), p.cmd.title(), nothing(p.cmd), name)}
	}
	body := []rune(a[1:])
	for j, r := range body {
		if r == '=' {
			return i, p.letterEquals(a, body, j)
		}
		f := p.cmd.letter(r)
		if f == nil {
			return i, p.unknownLetter(a, r)
		}
		last := j == len(body)-1
		if !last && body[j+1] == '=' {
			return i, p.letterEquals(a, body, j+1)
		}
		var err *Error
		if i, err = p.take(args, i, f, "-"+string(r), "", false, last); err != nil {
			if !last {
				err.Message = shown(a) + ": " + err.Message
			}
			return i, err
		}
	}
	return i, nil
}

// take records one flag and takes its value from the line. attached is the
// text after an equals sign when hasEq says there was one. last says the
// flag is the last thing in its argument, so what follows may be its value.
// It returns the index of the last argument it used.
func (p *parsed) take(args []string, i int, f *flag, typed, attached string, hasEq, last bool) (int, *Error) {
	o := occurrence{f: f, typed: typed}
	switch f.kind {
	case kindBool:
		if hasEq {
			return i, &Error{Kind: ErrBadValue, Typed: args[i], Message: fmt.Sprintf(
				"%s: %s is on when it is typed and takes no value, so %s. Leave the %s off.",
				shown(args[i]), typed, nothing(p.cmd), shown("="+attached))}
		}
	case kindText:
		if hasEq {
			o.value = attached
		} else {
			v, err := p.next(args, i, f, typed, last)
			if err != nil {
				return i, err
			}
			o.value = v
			i++
		}
		o.has = true
	case kindOptional:
		if hasEq {
			o.value, o.has = attached, true
		} else if last && i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
			i++
			o.value, o.has = args[i], true
		}
	case kindAttached:
		if hasEq {
			o.value, o.has = attached, true
		}
	case kindList:
		if hasEq {
			// An attached value is the whole list: it is the way to say
			// where a list ends.
			o.raw = []string{attached}
			break
		}
		v, err := p.next(args, i, f, typed, last)
		if err != nil {
			return i, err
		}
		i++
		o.raw = []string{v}
		// An empty first value says "none", and nothing can be added to
		// that.
		for !blank(v) && i+1 < len(args) && f.list.more(args[i+1]) {
			i++
			o.raw = append(o.raw, args[i])
		}
		// What ended the list is, by the rule, the prompt. If it reads
		// like more rules, ask rather than guess.
		if f.list == listRules && !blank(v) && i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") && looksLikeRules(args[i+1]) {
			after := args[i+1]
			items, _ := splitRules(after)
			return i, &Error{Kind: ErrUsage, Typed: after, Message: fmt.Sprintf(
				"%s after %s reads like more of its list, but an argument with a space in it ends a list and is taken as the prompt, so %s. If it is more of the list, join the items with commas (%s) or give each as its own argument; if it is the prompt, put -- before it.",
				shown(after), typed, nothing(p.cmd), shown(strings.Join(items, ",")))}
		}
	}
	if f.refuseNext != nil && last && !hasEq && i+1 < len(args) {
		after := args[i+1]
		command := len(p.args) == 0 && !p.promptOnly && p.cmd.sub(after) != nil
		if !command && !strings.HasPrefix(after, "-") {
			if why := f.refuseNext(typed, after); why != "" {
				return i, &Error{Kind: ErrUsage, Typed: typed + " " + after, Message: fmt.Sprintf(
					"%s %s: %s", typed, shown(after), why)}
			}
		}
	}
	if f.id == idHelp {
		p.help = true
	}
	if f.endsCommands {
		p.promptOnly = true
	}
	p.occs = append(p.occs, o)
	return i, nil
}

// next returns the value a flag takes from the argument after it, or says
// why there is none. A value that begins with a dash is taken only for a
// flag whose value is free text, and then only if it is not itself one of
// the command's flags; anywhere else it is far more likely a forgotten
// value than a meant one, and it can always be attached with an equals
// sign.
func (p *parsed) next(args []string, i int, f *flag, typed string, last bool) (string, *Error) {
	want := "<" + f.arg + ">"
	if !last {
		return "", &Error{Kind: ErrMissingValue, Typed: typed, Message: fmt.Sprintf(
			"%s takes a value (%s), so it has to be the last letter when one-letter flags share a dash, and %s. Write the other letters first, then %s %s.",
			typed, want, nothing(p.cmd), typed, want)}
	}
	if i+1 >= len(args) {
		return "", &Error{Kind: ErrMissingValue, Typed: typed, Message: fmt.Sprintf(
			"%s needs a value after it (%s) and the command line ends there, so %s.",
			typed, want, nothing(p.cmd))}
	}
	v := args[i+1]
	if !strings.HasPrefix(v, "-") {
		return v, nil
	}
	if f.freeText && v != "--" && !p.cmd.isFlag(v) {
		return v, nil
	}
	what := shown(v)
	if v == "--" {
		what = "--, which ends the flags"
	}
	return "", &Error{Kind: ErrMissingValue, Typed: typed, Message: fmt.Sprintf(
		"%s needs a value after it (%s), and what follows is %s, so %s. If the value really begins with a dash, attach it with an equals sign: %s=%s",
		typed, want, what, nothing(p.cmd), f.main(), shown(v))}
}

// given returns the last time the flag with this id was typed, or nil.
func (p *parsed) given(id string) *occurrence {
	for k := len(p.occs) - 1; k >= 0; k-- {
		if p.occs[k].f.id == id {
			return &p.occs[k]
		}
	}
	return nil
}

// nothing says, for a message, what did not happen because of the error.
func nothing(c *command) string {
	if c.session {
		return "nothing was started"
	}
	return "nothing was done"
}

// unknownFlag is the error for a word spelling that is not a flag of the
// command being read. arg is the whole argument and typed its part before
// any equals sign, both exactly as typed.
func (p *parsed) unknownFlag(arg, typed string) *Error {
	c := p.cmd
	e := &Error{Kind: ErrUnknownFlag, Typed: typed}
	name := strings.TrimPrefix(typed, "--")
	if hasSpace(typed) {
		e.Message = fmt.Sprintf(
			"%s is not a flag of %s, so %s. It has a space in it: a flag and its value are two arguments, and a prompt that begins with a dash goes after --.",
			shown(arg), c.title(), nothing(c))
		return e
	}
	if owners := p.t.owners(func(o *command) bool { return o.long(name) != nil }, c); len(owners) > 0 && !c.passes {
		e.Message = fmt.Sprintf("%s does not go with %s, so %s. It is a flag of %s.",
			shown(typed), c.title(), nothing(c), oneOf(owners, "and"))
		return e
	}
	var names []string
	for _, f := range c.typable {
		names = append(names, f.long...)
	}
	if near := nearest(name, names, slips(len(name))); near != "" {
		e.Suggestion = "--" + near
		e.Message = fmt.Sprintf("%s is not a flag of %s, so %s. Did you mean %s?",
			shown(typed), c.title(), nothing(c), e.Suggestion)
		return e
	}
	e.Message = fmt.Sprintf("%s is not a flag of %s, so %s. %s",
		shown(typed), c.title(), nothing(c), p.flagHint())
	return e
}

// unknownLetter is the error for a letter that is not a one-letter flag of
// the command being read. arg is the whole argument as typed.
func (p *parsed) unknownLetter(arg string, r rune) *Error {
	c := p.cmd
	typed := "-" + string(r)
	e := &Error{Kind: ErrUnknownFlag, Typed: arg}
	lead := shown(arg)
	if arg != typed {
		lead += ": " + shown(typed)
	}
	if hasSpace(arg) {
		e.Message = fmt.Sprintf(
			"%s is not a flag of %s, so %s. It has a space in it: a flag and its value are two arguments, and a prompt that begins with a dash goes after --.",
			shown(arg), c.title(), nothing(c))
		return e
	}
	if owners := p.t.owners(func(o *command) bool { return o.letter(r) != nil }, c); len(owners) > 0 && !c.passes {
		e.Message = fmt.Sprintf("%s does not go with %s, so %s. It is a flag of %s.",
			lead, c.title(), nothing(c), oneOf(owners, "and"))
		return e
	}
	// A letter Claude Code has for a flag gnosh spells out only.
	if long, ok := spelledOut[r]; ok && c.long(long) != nil {
		e.Suggestion = "--" + long
		e.Message = fmt.Sprintf("%s is not a flag of %s, so %s. Did you mean %s? gnosh has it spelled out only: as one letter, a mistyped -cwd would be read as -c -w -d.",
			lead, c.title(), nothing(c), e.Suggestion)
		return e
	}
	// The same letter in the other case.
	for _, f := range c.typable {
		for _, s := range f.short {
			if strings.EqualFold(string(s), string(r)) {
				e.Suggestion = "-" + string(s)
				e.Message = fmt.Sprintf("%s is not a flag of %s, so %s. Did you mean %s (%s)? Letter case counts.",
					lead, c.title(), nothing(c), e.Suggestion, f.main())
				return e
			}
		}
	}
	e.Message = fmt.Sprintf("%s is not a flag of %s, so %s. %s", lead, c.title(), nothing(c), p.flagHint())
	return e
}

// letterEquals is the error for an equals sign among one-letter flags,
// "-m=x". at is where the sign is in body.
func (p *parsed) letterEquals(arg string, body []rune, at int) *Error {
	c := p.cmd
	e := &Error{Kind: ErrBadValue, Typed: arg}
	var f *flag
	if at > 0 {
		f = c.letter(body[at-1])
	}
	if f == nil || f.kind == kindBool {
		e.Message = fmt.Sprintf("%s: a one-letter flag cannot be given a value with an equals sign, so %s. %s",
			shown(arg), nothing(c), p.flagHint())
		return e
	}
	typed := "-" + string(body[at-1])
	value := string(body[at+1:])
	switch f.kind {
	case kindAttached:
		e.Message = fmt.Sprintf("%s: %s takes a value only in its long form, so %s. Write %s=%s",
			shown(arg), typed, nothing(c), f.main(), shown(value))
	default:
		e.Message = fmt.Sprintf("%s: a one-letter flag takes its value as the next argument, so %s. Write %s %s, or %s=%s",
			shown(arg), nothing(c), typed, shown(value), f.main(), shown(value))
	}
	return e
}

// unknownCommand is the error for a word that is not one of the commands
// that must follow the command being read.
func (p *parsed) unknownCommand(word string) *Error {
	c := p.cmd
	e := &Error{Kind: ErrUnknownCommand, Typed: word}
	var names []string
	for _, s := range c.subs {
		names = append(names, s.name)
	}
	msg := fmt.Sprintf("%s is not a command of %s, so %s.", shown(word), c.title(), nothing(c))
	if near := nearest(word, names, slips(len(word))); near != "" {
		e.Suggestion = near
		msg += fmt.Sprintf(" Did you mean %s?", near)
	}
	e.Message = msg + fmt.Sprintf(" Its commands are %s.", oneOf(names, "and"))
	if c.parent != nil && c.parent.parent == nil {
		// (A command first on the line: the words may have been a prompt
		// that begins with its name.)
		e.Message += " " + asPrompt(p.name+" "+word)
	}
	return e
}

// misplaced is the error for a flag that was typed before a command's name
// and does not belong to that command. word is the command's name as typed.
// The word may have been meant as a prompt, so the message says how to send
// it as one.
func (p *parsed) misplaced(o *occurrence, sub *command, word string) *Error {
	owners := p.t.owners(func(c *command) bool { return hasFlag(c.flags, o.f) }, sub)
	what := o.typed
	if what != o.f.main() {
		what += " (" + o.f.main() + ")"
	}
	return &Error{Kind: ErrUnknownFlag, Typed: o.typed, Message: fmt.Sprintf(
		"%s was typed before %s and does not go with it, so %s. It is a flag of %s; flags of %s go after its name. %s",
		what, sub.title(), nothing(sub), oneOf(owners, "and"), sub.title(), asPrompt(word))}
}

// asPrompt ends a message about a word that was read as a command's name
// with how to send it as a prompt instead.
func asPrompt(word string) string {
	return fmt.Sprintf("If %s is a prompt, put -- before it.", shown(word))
}

// flagHint ends an unknown-flag message with where to look, and, where the
// command takes words of its own, how to give one that begins with a dash.
func (p *parsed) flagHint() string {
	c := p.cmd
	help := c.title() + " --help"
	if c.dashHint != "" {
		return fmt.Sprintf("%q lists the flags; %s.", help, c.dashHint)
	}
	return fmt.Sprintf("%q lists the flags.", help)
}

// owners returns, as a person would type them, up to four commands other
// than not for which is holds.
func (t *Table) owners(is func(*command) bool, not *command) []string {
	var out []string
	for _, c := range t.commands {
		if c == not || c.needSub || !is(c) {
			continue
		}
		if len(out) == 4 {
			out[3] = "others"
			break
		}
		out = append(out, c.title())
	}
	return out
}

// spelledOut are the one-letter flags of Claude Code's that gnosh has only
// spelled out, by the flag's word: a letter of their own would let a word
// flag typed with one dash be read as letters (-cwd as -c -w -d).
var spelledOut = map[rune]string{'w': "worktree"}
