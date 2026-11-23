package config

import (
	"fmt"
	"path"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"
)

// op is one thing a config file says, ready to be put in force. Reading a
// file gives a list of ops; putting them in force, in order, gives the
// Config. Keeping the two steps apart is what lets an untrusted folder's
// file be read by the same reader and then held back op by op.
type op struct {
	// kind sorts what the op would do, in the trust package's words.
	kind string
	// text is the setting in a few words, for the trust question.
	text string
	// origin is the line that says it.
	origin Origin
	// feature is the part of gnosh the setting is for, or "" when it needs
	// none. An op for a part the binary does not have stops startup.
	feature Feature
	// what names the setting in the "not built" message: "a hook".
	what string
	// apply puts the setting in force.
	apply func(b *builder)
}

// source is the file or flag being read, and what a reader needs to know
// about where it sits.
type source struct {
	// file is the path, or a flag's name for settings given on the command
	// line.
	file  string
	layer Layer
	// scope is "user", "project", "local" or "command line".
	scope string
	// theirs says the file is the repository's, not the person's: a
	// project's, or a local file gnosh did not write (one shipped in the
	// repository), which waits for the folder's trust as a project's does.
	theirs bool
	// baseDir is the directory the file is in.
	baseDir string
	// ruleDir is what a permission rule's "/path" is under.
	ruleDir string
	home    string
	cwd     string
	// tools is every tool name a permission rule may use; nil when the
	// calling program gave none.
	tools []string
	// getenv looks a variable up in the environment Load was given.
	getenv func(name string) (string, bool)

	probs   *problems
	secrets *secrets
	ops     []op
	// noLines is set for a source whose lines are not the file's own (the
	// options of the state file, read one by one): its origins name the
	// file alone.
	noLines bool
}

// at is the Origin of a line of the source.
func (s *source) at(line int) Origin {
	if s.noLines {
		line = 0
	}
	return Origin{Layer: s.layer, File: s.file, Line: line}
}

// errorf records a mistake on a line of the source.
func (s *source) errorf(line int, format string, args ...any) {
	s.probs.errorf(s.file, line, format, args...)
}

// add records an op.
func (s *source) add(o op) { s.ops = append(s.ops, o) }

// unique returns an object's members, reporting any key given twice. Which
// of two copies would count is anyone's guess, so neither does.
func (s *source) unique(n *jnode, under string) []jfield {
	seen := map[string]int{}
	var out []jfield
	for _, f := range n.fields {
		if first, twice := seen[f.key]; twice {
			s.errorf(f.line, "the key %s is given twice (it is also on line %d). Give it once.", showName(under+f.key), first)
			continue
		}
		seen[f.key] = f.line
		out = append(out, f)
	}
	return out
}

// idPattern is what an id may look like: letters, digits, dots, dashes and
// underscores, starting with a letter or a digit.
var idPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

// levelPattern is what a thinking level may look like.
var levelPattern = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,31}$`)

// isHeaderName reports whether s can be the name of an HTTP header.
func isHeaderName(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c == '-' || c == '_' || c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z') {
			return false
		}
	}
	return true
}

// isName reports whether s is a variable name: letters, digits and
// underscores, not starting with a digit.
func isName(s string) bool {
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

func isNameStart(c byte) bool {
	return c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}

func isNameChar(c byte) bool {
	return isNameStart(c) || c >= '0' && c <= '9'
}

// assignmentName reports whether a word of a command line is NAME=value,
// the shell's way to set a variable for one command, and returns the name.
func assignmentName(word string) (string, bool) {
	i := strings.IndexByte(word, '=')
	if i <= 0 {
		return "", false
	}
	name := strings.TrimSuffix(word[:i], "+")
	return name, isName(name)
}

// clip cuts s to at most n bytes for a message, on a character boundary,
// and drops control characters.
func clip(s string, n int) string {
	var b strings.Builder
	for _, r := range s {
		if b.Len()+utf8.RuneLen(r) > n {
			b.WriteString("…")
			break
		}
		if r < 0x20 || r == 0x7f {
			r = ' '
		}
		b.WriteRune(r)
	}
	return b.String()
}

// hookEventFor checks the event a hook is written under.
func (s *source) hookEventFor(line int, name string) *hookEvent {
	e := findHookEvent(name)
	if e == nil {
		hint := ""
		if near := nearHookEvent(name); near != "" {
			hint = fmt.Sprintf(" Did you mean %s? Event names are exact: no other letter case, no underscores.", near)
		}
		s.errorf(line, "%s is not a hook event gnosh fires, so a hook on it would never run.%s The events are: %s.", showName(name), hint, list(HookEventNames()))
	}
	return e
}

// checkMatcher says what is wrong with a hook's matcher for its event, or
// "".
func checkMatcher(e *hookEvent, matcher string) string {
	if matcher == "" {
		return ""
	}
	if e.matcher == "" {
		return fmt.Sprintf("%s has nothing for a matcher to match, so a hook with one would never run. Take the matcher away", e.name)
	}
	if matcher == "*" || isExactMatcher(matcher) {
		return ""
	}
	if _, err := regexp.Compile(matcher); err != nil {
		return fmt.Sprintf("the matcher %s is read as a regular expression (it holds more than names, commas and |) and cannot be compiled: %v", show(matcher), err)
	}
	return ""
}

// isExactMatcher reports whether a matcher is a list of exact names: only
// letters, digits, underscores, dashes, spaces, commas and |.
func isExactMatcher(m string) bool {
	for i := 0; i < len(m); i++ {
		c := m[i]
		if !(c == '_' || c == '-' || c == ' ' || c == ',' || c == '|' || c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z') {
			return false
		}
	}
	return true
}

// rule checks one permission rule and records it.
func (s *source) rule(which, text string, o Origin) {
	if why := checkRule(which, text, s.tools); why != "" {
		s.probs.at(Error, o, "the %s rule %s cannot be used: %s.", which, show(text), why)
		return
	}
	kind := map[string]string{"allow": KindAllowRule, "ask": KindAskRule, "deny": KindDenyRule}[which]
	file, scope, dir := s.file, s.scope, s.ruleDir
	if o.File == "" {
		file = ""
	}
	s.add(op{kind: kind, text: which + " " + text, origin: o, feature: FeaturePermissions, what: "a permission rule",
		apply: func(b *builder) {
			if b.in.Tools == nil && !strings.HasPrefix(text, "mcp__") {
				b.probs.at(Error, o, "the %s rule %s cannot be checked: this gnosh was started without its list of tool names, so a tool's name cannot be told from a misspelling.", which, show(text))
			}
			b.addRule(which, Rule{Text: text, Origin: o}, scope, file, dir)
		}})
}

// checkRule says why a permission rule cannot be used, or "". It checks
// the rule's shape and that the tool it names is one gnosh has. What the
// pattern in the parentheses means is left to the permission package.
func checkRule(which, text string, tools []string) string {
	if text == "" {
		return "it is empty"
	}
	name := text
	if i := strings.IndexByte(text, '('); i >= 0 {
		if !strings.HasSuffix(text, ")") {
			return "the parenthesis it opens is not closed at its end"
		}
		name = text[:i]
	} else if strings.ContainsAny(text, ")") {
		return "it closes a parenthesis it never opened"
	}
	if name == "" {
		return "no tool name comes before the parenthesis"
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		if !(c == '_' || c == '-' || c == '*' || c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z') {
			return fmt.Sprintf("%s is not a tool name: a rule is a tool name, or a tool name with a pattern in parentheses", showName(name))
		}
	}
	glob := strings.Contains(name, "*")
	if strings.HasPrefix(name, "mcp__") {
		if which != "allow" {
			return "it names a tool of an MCP server, and such a rule would do nothing here: those tools are only known once the server connects. To keep a server's tools away, do not add the server"
		}
		if glob {
			server, _, ok := strings.Cut(strings.TrimPrefix(name, "mcp__"), "__")
			if !ok || server == "" || strings.Contains(server, "*") {
				return "an allow rule may use * only after a whole server name, as in mcp__server__get_*"
			}
		}
		if name != text {
			return "a rule for an MCP tool takes no pattern in parentheses"
		}
		return ""
	}
	if glob && which == "allow" {
		return "an allow rule cannot use * in the tool's name: it would let every matching tool run without asking. Name each tool"
	}
	if tools == nil {
		// The calling program gave no list of tools, so the name cannot be
		// told from a misspelling here. That is said when the rule is put
		// in force, and not for a rule that is only held.
		return ""
	}
	if glob {
		for _, t := range tools {
			if ok, _ := path.Match(name, t); ok {
				return ""
			}
		}
		return fmt.Sprintf("no tool's name matches %s. The tools are: %s", show(name), list(tools))
	}
	if name == "Cd" {
		// Cd rules are about /cd, the person's own command, which is no
		// tool (the permission engine reads their patterns).
		return ""
	}
	if !slices.Contains(tools, name) {
		hint := ""
		for _, t := range tools {
			if strings.EqualFold(t, name) {
				hint = fmt.Sprintf(" Did you mean %s? Tool names are exact.", t)
			}
		}
		return fmt.Sprintf("%s is not a tool gnosh has, so the rule would do nothing.%s The tools are: %s", showName(name), hint, list(tools))
	}
	return ""
}

// SplitRules splits a list of permission rules written in one argument, as
// --allowedTools "Bash(git *) Edit" or "Bash,Read". It splits at commas and
// spaces, but not inside parentheses.
func SplitRules(s string) []string {
	var out []string
	depth, start := 0, -1
	for i, r := range s {
		switch {
		case r == '(':
			depth++
		case r == ')' && depth > 0:
			depth--
		}
		if depth == 0 && (r == ',' || r == ' ' || r == '\t' || r == '\n') {
			if start >= 0 {
				out = append(out, s[start:i])
				start = -1
			}
			continue
		}
		if start < 0 {
			start = i
		}
	}
	if start >= 0 {
		out = append(out, s[start:])
	}
	return out
}

// addItem adds a value to a list unless the list already has it.
func addItem(items *[]ListItem, v string, o Origin) {
	for _, it := range *items {
		if it.V == v {
			return
		}
	}
	*items = append(*items, ListItem{V: v, Origin: o})
}
