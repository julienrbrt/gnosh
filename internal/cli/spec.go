package cli

import (
	"fmt"
	"strings"
)

// Status says whether the feature behind a command, a flag, or one value of a
// flag has been built.
type Status int

const (
	// NotBuilt means gnosh knows the name and reads it correctly, but the
	// part behind it is not in this build: nothing that wires the part in
	// has marked it. Typing it stops startup with a message that says so.
	// It is the zero value, so a row of the table that forgets its status
	// fails closed.
	NotBuilt Status = iota
	// Built means gnosh acts on it.
	Built
)

// String returns "built" or "not built".
func (s Status) String() string {
	if s == Built {
		return "built"
	}
	return "not built"
}

// The kinds of thing an Entry can be.
const (
	EntryCommand = "command"
	EntryFlag    = "flag"
	EntryValue   = "value"
)

// Entry describes one row of the table: a command, a flag, or one value of
// a flag that takes a fixed set of values.
type Entry struct {
	// Name is the key SetStatus takes: a command's words ("doctor",
	// "session list"), a flag's main spelling ("--model"; a flag that
	// belongs to one command carries the command's words in front, "session
	// --json"), or a flag and one of its values ("--output-format=json").
	Name string
	// Kind is EntryCommand, EntryFlag or EntryValue.
	Kind string
	// Spellings is every way to type it. For --verbose the last one, -v,
	// holds only in print mode; elsewhere -v is --version.
	Spellings []string
	// Commands, for a flag, is the commands that take it.
	Commands []string
	// Priority is the specs' P0, P1 or P2, as 0, 1 or 2.
	Priority int
	// Status says whether it is built.
	Status Status
	// Help is its line of help.
	Help string
}

// kind is how a flag takes its value on the command line.
type kind int

const (
	// kindBool takes no value.
	kindBool kind = iota
	// kindText takes one value: `--flag value`, `--flag=value`, `-f value`.
	kindText
	// kindOptional takes the next argument as its value unless that
	// argument begins with a dash; `--flag=value` works too.
	kindOptional
	// kindAttached takes a value only when it is attached with an equals
	// sign; an argument after it is never its value.
	kindAttached
	// kindList takes one argument and then more, as its listKind decides.
	kindList
)

// listKind is what a list flag's items are, which decides how an argument
// is cut into items and which further arguments still belong to the list.
type listKind int

const (
	// listRules is tool names and permission rules. Commas and spaces
	// separate the items, except inside parentheses. A further argument
	// belongs to the list while it has no space outside parentheses.
	listRules listKind = iota + 1
	// listPaths is folders: each argument is one item, taken whole. A
	// further argument belongs to the list while it has no space.
	listPaths
	// listPairs is environment variables, NAME=value: each argument is one
	// item. A further argument belongs to the list while it is one.
	listPairs
	// listHeaders is HTTP headers, "Name: value": each argument is one
	// item. A further argument belongs to the list while it is one.
	listHeaders
)

// choice is one value of a flag that takes a fixed set of values.
type choice struct {
	name string
	// means is the value this one stands for, when it is another spelling
	// of it (manual means default).
	means string
	// refuse, when set, says why the value is known but never accepted.
	refuse string
	pri    int
	status Status
}

// flagValue is what a flag was given, checked and ready to record.
type flagValue struct {
	// text is the value of a one-value flag; for a choice, its main spelling.
	text string
	// has says a value was given. A kindOptional or kindAttached flag typed
	// bare has none.
	has bool
	// n is text as a number, for flags that take one.
	n int
	// items is a list flag's items.
	items []string
}

// flag is one flag of the table.
type flag struct {
	// names is the spellings as they are typed, separated by spaces:
	// "-c -C --continue". The first long one is the flag's main spelling.
	names string
	// id is the key SetStatus takes. It defaults to the main spelling.
	id string
	// printLetter is a one-letter spelling that means this flag only in
	// print mode; elsewhere the letter belongs to another flag.
	printLetter rune
	kind        kind
	// arg is what help calls the value: "model", "rules...".
	arg     string
	choices []choice
	// number says the value is a whole number of min or more.
	number bool
	min    int
	list   listKind
	// freeText says the value is prose that may begin with a dash.
	freeText bool
	// emptyOK says an empty value means something (--tools "").
	emptyOK bool
	// check returns what is wrong with a value, or "".
	check func(v string) string
	// printOnly says the flag has an effect only in print mode.
	printOnly bool
	// endsCommands says that once this flag is seen, no later word names a
	// command: -p makes every word part of the prompt.
	endsCommands bool
	// refuseNext returns why the plain word after the flag must not follow
	// it, or "". It is how `-c DIR` is caught. It is not asked about a word
	// that names a command, or one that begins with a dash.
	refuseNext func(typed, next string) string
	// pri is the specs' priority: 0, 1 or 2.
	pri    int
	status Status
	// group is the heading the flag is listed under in help.
	group string
	help  string
	// set records the flag in the Invocation. It may be nil for a flag
	// that only the cross-flag checks look at.
	set func(inv *Invocation, v flagValue)

	// short and long are names, taken apart.
	short []rune
	long  []string
}

// main returns the flag's main spelling, with its dashes.
func (f *flag) main() string {
	if len(f.long) > 0 {
		return "--" + f.long[0]
	}
	return "-" + string(f.short[0])
}

// spellings returns every way to type the flag, shortest first as in names.
func (f *flag) spellings() []string {
	var out []string
	for _, r := range f.short {
		out = append(out, "-"+string(r))
	}
	for _, l := range f.long {
		out = append(out, "--"+l)
	}
	return out
}

// choice returns the choice a value names, or nil.
func (f *flag) choice(v string) *choice {
	for i := range f.choices {
		if f.choices[i].name == v {
			return &f.choices[i]
		}
	}
	return nil
}

// choiceNames returns the values a person may type, in table order, without
// the refused ones and without second spellings.
func (f *flag) choiceNames() []string {
	var out []string
	for _, c := range f.choices {
		if c.refuse == "" && c.means == "" {
			out = append(out, c.name)
		}
	}
	return out
}

// command is one command of the table. The root command is gnosh itself.
type command struct {
	name    string
	aliases []string
	// cmd is the Invocation.Command it leads to.
	cmd    Command
	parent *command
	subs   []*command
	// flags is every flag the command takes, in help order.
	flags []*flag
	// typable, byWord and byLetter are what reach returns and the two ways
	// of looking a flag up in it. Default fills them in once the table is
	// whole; they do not change after that.
	typable  []*flag
	byWord   map[string]*flag
	byLetter map[rune]*flag
	// minArgs and maxArgs bound the arguments after the command's words;
	// maxArgs below zero means any number.
	minArgs, maxArgs int
	// argsUsage is how help writes the arguments: "<id> <title...>".
	// argsShort, when set, is a shorter form for a list of commands.
	argsUsage string
	argsShort string
	// session says the command opens a session or answers a prompt, so its
	// arguments are the prompt.
	session bool
	// needSub says one of subs must follow.
	needSub bool
	// afterAnything says the command may follow any flags, which are then
	// dropped: help and version, like --help and --version.
	afterAnything bool
	// passes says the command hands its arguments to something with words
	// of its own (mem), so a dash argument it does not know is more likely
	// meant for that than for another gnosh command. dashHint says how to
	// pass such an argument.
	passes   bool
	dashHint string
	// dashIsStdin says - by itself is an argument of the command's, which
	// reads standard input there (mcp add-json <name> -).
	dashIsStdin bool
	pri         int
	status      Status
	// summary is the command's line in a list of commands; about is more,
	// for its own help.
	summary string
	about   string
	// take records the arguments in the Invocation; nil means as Args.
	take func(inv *Invocation, args []string)
	// finish does the command's own checks once flags and arguments are
	// recorded.
	finish func(inv *Invocation, p *parsed, env Env) *Error
}

// built reports whether the command can be used: whether it is built, or,
// for a command that needs a subcommand, whether any of those is.
func (c *command) built() bool {
	if !c.needSub {
		return c.status == Built
	}
	for _, s := range c.subs {
		if s.built() {
			return true
		}
	}
	return false
}

// path returns the words that lead to the command, without "gnosh".
func (c *command) path() []string {
	if c.parent == nil {
		return nil
	}
	return append(c.parent.path(), c.name)
}

// title returns the command as a person would type it: "gnosh session list".
func (c *command) title() string {
	return strings.Join(append([]string{"gnosh"}, c.path()...), " ")
}

// sub returns the subcommand a word names, by name or alias, or nil.
func (c *command) sub(word string) *command {
	for _, s := range c.subs {
		if s.name == word {
			return s
		}
		for _, a := range s.aliases {
			if a == word {
				return s
			}
		}
	}
	return nil
}

// reach returns the flags that may be typed while the command is the one
// being read: its own, and, for a command that still needs a subcommand,
// those of the subcommands below it.
func (c *command) reach() []*flag {
	out := append([]*flag(nil), c.flags...)
	if !c.needSub {
		return out
	}
	for _, s := range c.subs {
		for _, f := range s.reach() {
			if !hasFlag(out, f) {
				out = append(out, f)
			}
		}
	}
	return out
}

// index fills in the command's lookups from reach. Where two flags within
// reach share a spelling the first one keeps it; the table's tests see to
// it that none do.
func (c *command) index() {
	c.typable = c.reach()
	c.byWord = map[string]*flag{}
	c.byLetter = map[rune]*flag{}
	for _, f := range c.typable {
		for _, l := range f.long {
			if _, taken := c.byWord[l]; !taken {
				c.byWord[l] = f
			}
		}
		for _, r := range f.short {
			if _, taken := c.byLetter[r]; !taken {
				c.byLetter[r] = f
			}
		}
	}
}

// takes reports whether f may be typed for the command.
func (c *command) takes(f *flag) bool { return hasFlag(c.typable, f) }

// long returns the flag a word spelling names for the command, or nil. Only
// whole names count: gnosh does not guess from the start of a name.
func (c *command) long(name string) *flag { return c.byWord[name] }

// letter returns the flag a one-letter spelling names for the command, or
// nil.
func (c *command) letter(r rune) *flag { return c.byLetter[r] }

// isFlag reports whether an argument, as it stands, is one of the command's
// flags: a word spelling (with or without =value) or a letter or run of
// letters that are all flags.
func (c *command) isFlag(arg string) bool {
	if strings.HasPrefix(arg, "--") {
		name, _, _ := strings.Cut(arg[2:], "=")
		return c.long(name) != nil
	}
	body := strings.TrimPrefix(arg, "-")
	if body == "" || body == arg {
		return false
	}
	for _, r := range body {
		if c.letter(r) == nil {
			return false
		}
	}
	return true
}

func hasFlag(list []*flag, f *flag) bool {
	for _, g := range list {
		if g == f {
			return true
		}
	}
	return false
}

// Table is gnosh's commands and flags: how each is spelled, what value it
// takes, its help, and whether the feature behind it is built. Default
// returns the table as it ships. A Table is not changed by Parse or Help,
// so one may be shared once its statuses are set.
type Table struct {
	root *command
	// commands is every command, parents before their subcommands.
	commands []*command
	// flags is every flag once, in the order the table declares them.
	flags []*flag
}

// SetStatus marks a command, a flag, or one value of a flag as built or not.
// The name is an Entry's Name: "doctor", "session list", "--fork-session",
// "session --json", "--output-format=json". A name the table does not have
// is an error, so a misspelt one cannot pass for done.
func (t *Table) SetStatus(name string, s Status) error {
	for _, c := range t.commands {
		if c.parent == nil || strings.Join(c.path(), " ") != name {
			continue
		}
		if c.needSub {
			var subs []string
			for _, sub := range c.subs {
				subs = append(subs, sub.title()[len("gnosh "):])
			}
			return fmt.Errorf("cli: %q is a group of commands with no status of its own; set one of %s", name, strings.Join(subs, ", "))
		}
		c.status = s
		return nil
	}
	id, value, hasValue := strings.Cut(name, "=")
	for _, f := range t.flags {
		if f.id != id {
			continue
		}
		if !hasValue {
			f.status = s
			return nil
		}
		c := f.choice(value)
		if c == nil || c.refuse != "" || c.means != "" {
			return fmt.Errorf("cli: %s has no value %q to set a status for; its values are %s", id, value, strings.Join(f.choiceNames(), ", "))
		}
		c.status = s
		return nil
	}
	var names []string
	for _, e := range t.Entries() {
		names = append(names, e.Name)
	}
	if near := nearest(name, names, 3); near != "" {
		return fmt.Errorf("cli: the table has no command or flag named %q (did you mean %q?)", name, near)
	}
	return fmt.Errorf("cli: the table has no command or flag named %q", name)
}

// Entries lists the table: every command, then every flag with its values,
// in the table's own order.
func (t *Table) Entries() []Entry {
	var out []Entry
	for _, c := range t.commands {
		if c.parent == nil || c.needSub {
			continue
		}
		out = append(out, Entry{
			Name:      strings.Join(c.path(), " "),
			Kind:      EntryCommand,
			Spellings: commandSpellings(c),
			Priority:  c.pri,
			Status:    c.status,
			Help:      strings.ReplaceAll(c.summary, nbsp, " "),
		})
	}
	for _, f := range t.flags {
		e := Entry{
			Name:      f.id,
			Kind:      EntryFlag,
			Spellings: f.spellings(),
			Priority:  f.pri,
			Status:    f.status,
			Help:      strings.ReplaceAll(f.help, nbsp, " "),
		}
		if f.printLetter != 0 {
			e.Spellings = append(e.Spellings, "-"+string(f.printLetter))
		}
		for _, c := range t.commands {
			if hasFlag(c.flags, f) {
				e.Commands = append(e.Commands, c.title())
			}
		}
		out = append(out, e)
		for _, c := range f.choices {
			if c.refuse != "" || c.means != "" {
				continue
			}
			spellings := []string{c.name}
			for _, other := range f.choices {
				if other.means == c.name {
					spellings = append(spellings, other.name)
				}
			}
			out = append(out, Entry{
				Name:      f.id + "=" + c.name,
				Kind:      EntryValue,
				Spellings: spellings,
				Priority:  c.pri,
				Status:    c.status,
			})
		}
	}
	return out
}

// commandSpellings returns every way to type a command's words: each name
// and alias of each word on the way to it.
func commandSpellings(c *command) []string {
	if c.parent == nil {
		return []string{""}
	}
	var out []string
	for _, prefix := range commandSpellings(c.parent) {
		for _, word := range append([]string{c.name}, c.aliases...) {
			out = append(out, strings.TrimSpace(prefix+" "+word))
		}
	}
	return out
}
