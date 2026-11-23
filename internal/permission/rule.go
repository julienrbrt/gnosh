package permission

import (
	"errors"
	"fmt"
	"strings"
)

// Rule is one permission rule: a tool name, or a tool name with a
// specifier in parentheses, as in Bash(git diff *) or Edit(/src/**).
type Rule struct {
	// Tool is the name as written: a tool, an old alias (Task), an MCP
	// name, or a pattern over names (mcp__*).
	Tool string
	// Spec is the text between the parentheses. It is empty for a bare
	// rule; Bash and Bash(*) are the same rule.
	Spec string
}

// ParseRule reads a rule as written in a settings file or a flag. It
// checks the shape only; whether the rule can be applied (a known tool, a
// form its list accepts) is checked by New and Grant, which know where the
// rule came from.
func ParseRule(s string) (Rule, error) {
	t := strings.TrimSpace(s)
	if t == "" {
		return Rule{}, errors.New("the rule is empty")
	}
	open := strings.IndexByte(t, '(')
	if open < 0 {
		if !toolNameOK(t) {
			return Rule{}, fmt.Errorf("%q is not a tool name; a rule is Tool or Tool(specifier)", t)
		}
		return Rule{Tool: t}, nil
	}
	if !strings.HasSuffix(t, ")") {
		return Rule{}, errors.New("the opening parenthesis is never closed; a rule is Tool or Tool(specifier)")
	}
	name, spec := t[:open], t[open+1:len(t)-1]
	if !toolNameOK(name) {
		return Rule{}, fmt.Errorf("%q is not a tool name; a rule is Tool or Tool(specifier)", name)
	}
	if strings.TrimSpace(spec) == "" {
		return Rule{}, fmt.Errorf("the parentheses are empty; write %s for every use of the tool", name)
	}
	if spec == "*" {
		spec = ""
	}
	return Rule{Tool: name, Spec: spec}, nil
}

// toolNameOK reports whether s could be a tool name or a pattern over
// tool names: letters, digits, _ - . and the wildcards * and ?.
func toolNameOK(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if c := s[i]; !(isNameChar(c) || c == '-' || c == '.' || c == '*' || c == '?') {
			return false
		}
	}
	return true
}

// String prints the rule the way it is written.
func (r Rule) String() string {
	if r.Spec == "" {
		return r.Tool
	}
	return r.Tool + "(" + r.Spec + ")"
}

// ruleKind says how a compiled rule matches.
type ruleKind uint8

const (
	// kindTool: every use of one tool.
	kindTool ruleKind = iota
	// kindNameGlob: every use of every tool whose name fits a pattern.
	kindNameGlob
	// kindCommand: a Bash command pattern.
	kindCommand
	// kindPath: a Read or Edit path pattern.
	kindPath
	// kindDomain: a WebFetch host pattern.
	kindDomain
	// kindParam: one top-level input parameter and a value pattern.
	kindParam
	// kindAgent: a sub-agent type.
	kindAgent
	// kindSkill: a skill name, alone or with any arguments.
	kindSkill
	// kindNever: a rule about a tool gnosh does not have.
	kindNever
	// kindCd: the folders the person's /cd may move the session to.
	kindCd
)

// crule is a rule made ready to match.
type crule struct {
	Rule
	list  List
	layer *layer
	// canon is the canonical tool name (aliases resolved).
	canon string
	kind  ruleKind

	cmd cmdPattern // kindCommand
	// cmdFold is cmd with the program's name as its key (progKey): what
	// deny and ask rules are matched by.
	cmdFold cmdPattern
	path    pathPattern // kindPath
	negate  bool        // kindPath: a "!" pattern that reopens earlier matches
	domain  string      // kindDomain, lower case
	param   string      // kindParam
	value   string      // kindParam: value pattern; kindAgent: type; kindSkill: name
	anyArgs bool        // kindSkill: "name *"
}

// compile checks that a parsed rule can be applied in the given list and
// layer, and prepares it. Its errors say what is wrong and what to write.
func (e *Engine) compile(r Rule, list List, ly *layer) (*crule, error) {
	c := &crule{Rule: r, list: list, layer: ly, canon: CanonicalTool(r.Tool)}

	if hasGlob(r.Tool) {
		if r.Spec != "" {
			return nil, errors.New("a pattern over tool names takes no specifier in parentheses")
		}
		c.kind = kindNameGlob
		if list == ListAllow {
			// An allow rule may only sweep up the tools of one MCP server.
			rest, ok := strings.CutPrefix(r.Tool, mcpPrefix)
			server, tail, found := strings.Cut(rest, "__")
			if !ok || !found || server == "" || hasGlob(server) || tail == "" {
				return nil, errors.New("an allow rule cannot use a pattern over tool names, except for the tools of one MCP server (mcp__server__*); name each tool")
			}
		}
		return c, nil
	}

	info, known := e.names.info(c.canon)
	if !known && c.canon == toolCd {
		return e.compileCd(c)
	}
	if !known {
		return nil, fmt.Errorf("gnosh has no tool named %q; the tools are %s, and MCP tools are named mcp__server__tool", r.Tool, strings.Join(e.names.all(), ", "))
	}
	if isMCP(c.canon) {
		if r.Spec != "" {
			return nil, errors.New("a rule for an MCP tool takes no specifier in parentheses; write mcp__server, mcp__server__* or mcp__server__tool")
		}
		// mcp__server stands for every tool of that server.
		if !strings.Contains(strings.TrimPrefix(c.canon, mcpPrefix), "__") {
			c.kind = kindNameGlob
			c.Rule.Tool = r.Tool
			c.canon += "__*"
		}
		return c, nil
	}
	if info.class == classAbsent {
		c.kind = kindNever
		return c, nil
	}
	if r.Spec == "" {
		c.kind = kindTool
		return c, nil
	}

	// Tool(param:value), for the parameters that are not what the tool's
	// own rule form is about.
	if name, val, ok := splitParam(r.Spec, info); ok && !(info.class == classShell && strings.HasSuffix(r.Spec, ":*")) {
		if list == ListAllow {
			return nil, fmt.Errorf("%s(%s:...) matches an input parameter, which only deny and ask rules may do", r.Tool, name)
		}
		c.kind, c.param, c.value = kindParam, name, val
		return c, nil
	}

	if name, _, found := strings.Cut(r.Spec, ":"); found && info.primary != "" && strings.TrimSpace(name) == info.primary {
		return nil, fmt.Errorf("%s(%s:...) names the tool's main input, which its own rule form already matches; write %s(pattern)", r.Tool, info.primary, r.Tool)
	}

	switch {
	case info.class == classShell && c.canon == toolBash:
		p, err := parseCmdPattern(r.Spec)
		if err != nil {
			return nil, err
		}
		if list == ListAllow {
			if _, isAssign := assignName(firstField(r.Spec)); isAssign {
				return nil, errors.New("an allow rule cannot start with a variable assignment: gnosh asks about every command that sets a variable, so the rule would never apply")
			}
		}
		c.kind, c.cmd, c.cmdFold = kindCommand, p, foldPattern(p)
	case c.canon == toolRead || c.canon == toolEdit:
		spec := r.Spec
		if strings.HasPrefix(spec, "!") {
			if list == ListAllow {
				return nil, errors.New("a \"!\" pattern takes matches back out of deny and ask rules; it means nothing in an allow rule")
			}
			c.negate = true
			spec = spec[1:]
		}
		p, err := e.parsePathPattern(spec, list, ly, c.negate)
		if err != nil {
			return nil, err
		}
		c.kind, c.path = kindPath, p
	case info.class == classFileRead || info.class == classFileEdit:
		use := toolRead
		if info.class == classFileEdit {
			use = toolEdit
		}
		return nil, fmt.Errorf("%s takes no path pattern, so this rule would never match; write %s(%s), which covers %s", r.Tool, use, r.Spec, r.Tool)
	case info.class == classFetch:
		host, ok := strings.CutPrefix(r.Spec, "domain:")
		host = strings.ToLower(strings.TrimRight(strings.TrimSpace(host), "."))
		// An address may be written in brackets, as it is in a URL.
		if strings.HasPrefix(host, "[") && strings.HasSuffix(host, "]") {
			host = host[1 : len(host)-1]
		}
		if !ok || host == "" || strings.ContainsAny(host, "/ \t") || strings.Contains(host, ":") && strings.Contains(host, "*") {
			return nil, errors.New("a WebFetch rule names a host: WebFetch(domain:example.com), WebFetch(domain:*.example.com) or WebFetch(domain:*)")
		}
		for i := 0; i < len(host); i++ {
			if host[i] >= 0x80 {
				return nil, errors.New("a WebFetch rule names a host in ASCII; write a name with other letters in the form that starts with xn--, which is the one a request is sent to")
			}
		}
		if !strings.Contains(host, "*") {
			canon, ok := canonHost(host)
			if !ok {
				return nil, errors.New("the host of this WebFetch rule cannot be read as a name or an address")
			}
			host = canon
		}
		c.kind, c.domain = kindDomain, host
	case info.class == classAgent:
		c.kind, c.value = kindAgent, strings.TrimSpace(r.Spec)
	case info.class == classSkill:
		c.kind = kindSkill
		c.value = strings.TrimSpace(r.Spec)
		if name, ok := strings.CutSuffix(c.value, " *"); ok {
			c.value, c.anyArgs = strings.TrimSpace(name), true
		} else if name, ok := strings.CutSuffix(c.value, ":*"); ok {
			c.value, c.anyArgs = name, true
		}
		if c.value == "" || strings.ContainsAny(c.value, " \t*") {
			return nil, errors.New("a Skill rule names one skill: Skill(name), or Skill(name *) for the skill with any arguments")
		}
	default:
		if list == ListAllow {
			return nil, fmt.Errorf("%s takes no specifier in parentheses; write %s for every use of the tool", r.Tool, r.Tool)
		}
		return nil, fmt.Errorf("%s takes no specifier in parentheses; write %s for every use of the tool, or %s(param:value) to match one of its inputs", r.Tool, r.Tool, r.Tool)
	}
	return c, nil
}

// toolCd is the name Cd rules go by. No tool has it: the rules are about
// the person's own /cd, which the model cannot call.
const toolCd = "Cd"

// compileCd prepares a Cd rule, as Claude Code has them: a bare deny rule
// switches /cd off, a deny rule with a pattern keeps /cd out of the
// folders it covers, and once there is any allow rule, /cd goes only to a
// folder one of them covers. There are no ask rules: /cd is the person's
// own command, so there is nobody else to ask.
func (e *Engine) compileCd(c *crule) (*crule, error) {
	if c.list == ListAsk {
		return nil, errors.New("a Cd rule is a deny or an allow rule: /cd is the person's own command, so there is nobody else to ask")
	}
	c.kind = kindCd
	if c.Spec == "" {
		return c, nil
	}
	p, err := e.parseDirPattern(c.Spec, c.list, c.layer)
	if err != nil {
		return nil, err
	}
	c.path = p
	return c, nil
}

// CdRefusal says why the Cd rules in force keep the person's /cd from
// moving the session to a folder, or "" when they do not. names are the
// folder's spellings, each a cleaned absolute path: as it was typed, after
// each symbolic link on the way is followed, and last as it resolves. A
// deny rule is checked against all of them; an allow rule must cover the
// folder as it resolves.
func (e *Engine) CdRefusal(names []string) string {
	if len(names) == 0 {
		return "There is no folder to go to."
	}
	e.mu.RLock()
	defer e.mu.RUnlock()
	for _, c := range e.rules(ListDeny) {
		if c.kind != kindCd {
			continue
		}
		if c.Spec == "" {
			return "The deny rule Cd from " + c.layer.Name + " switches /cd off."
		}
		for _, n := range names {
			if ok, _ := c.path.matchKeyed(guardPath(n)); ok {
				return "The deny rule " + c.Rule.String() + " from " + c.layer.Name + " covers " + n + "."
			}
		}
	}
	real := names[len(names)-1]
	var allows []string
	for _, c := range e.rules(ListAllow) {
		if c.kind != kindCd {
			continue
		}
		if c.Spec == "" || c.path.match(real) {
			return ""
		}
		allows = append(allows, c.Rule.String())
	}
	if len(allows) > 0 {
		return "The Cd allow rules name the folders /cd may go to (" + strings.Join(allows, ", ") + "), and " + real + " is none of them."
	}
	return ""
}

// firstField returns the text up to the first space.
func firstField(s string) string {
	if i := strings.IndexAny(s, " \t"); i >= 0 {
		return s[:i]
	}
	return s
}

// splitParam reports whether spec has the form param:value for a
// parameter the tool takes, other than its primary one.
func splitParam(spec string, info toolInfo) (name, value string, ok bool) {
	name, value, found := strings.Cut(spec, ":")
	if !found {
		return "", "", false
	}
	name, value = strings.TrimSpace(name), strings.TrimSpace(value)
	if info.class == classOther || info.class == classQuiet || info.class == classPerson {
		// These tools have no rule form of their own, so any
		// identifier-like name is a parameter.
		if name == "" || strings.ContainsAny(name, " \t/*") {
			return "", "", false
		}
		return name, value, true
	}
	for _, p := range info.params {
		if p == name {
			return name, value, true
		}
	}
	return "", "", false
}

// cmdPattern is a Bash rule's specifier made ready to match.
type cmdPattern struct {
	// segs are the literal pieces between wildcards. One piece and no
	// wildcard is an exact rule.
	segs []string
	// bare is the command a lone trailing " *" also matches with nothing
	// after it; "" when the rule has no such form.
	bare string
}

// foldPattern returns the pattern with its first word, the program's
// name, turned into its key.
func foldPattern(p cmdPattern) cmdPattern {
	out := cmdPattern{segs: append([]string(nil), p.segs...), bare: foldFirst(p.bare)}
	for i, seg := range out.segs {
		j := strings.IndexAny(seg, " \t")
		if j < 0 {
			out.segs[i] = progKey(seg) // the word goes on after the wildcard
			continue
		}
		out.segs[i] = progKey(seg[:j]) + seg[j:]
		break
	}
	return out
}

// exact reports whether the pattern has no wildcard.
func (p cmdPattern) exact() bool { return len(p.segs) == 1 }

// parseCmdPattern reads a Bash specifier. "*" stands for any text, "\*"
// for a star itself; ":*" at the very end is another way to write " *".
func parseCmdPattern(spec string) (cmdPattern, error) {
	spec = strings.TrimSpace(spec)
	if head, ok := strings.CutSuffix(spec, ":*"); ok {
		spec = head + " *"
	}
	var p cmdPattern
	var cur strings.Builder
	for i := 0; i < len(spec); i++ {
		switch {
		case spec[i] == '\\' && i+1 < len(spec) && spec[i+1] == '*':
			cur.WriteByte('*')
			i++
		case spec[i] == '*':
			p.segs = append(p.segs, cur.String())
			cur.Reset()
			for i+1 < len(spec) && spec[i+1] == '*' {
				i++
			}
		default:
			cur.WriteByte(spec[i])
		}
	}
	p.segs = append(p.segs, cur.String())
	if len(p.segs) == 1 && p.segs[0] == "" {
		return p, errors.New("the Bash pattern is empty")
	}
	// A trailing " *" that is the rule's only wildcard also matches the
	// command with nothing after it.
	if len(p.segs) == 2 && p.segs[1] == "" && strings.HasSuffix(p.segs[0], " ") {
		p.bare = strings.TrimRight(p.segs[0], " ")
	}
	return p, nil
}

// match reports whether the pattern covers the whole of text.
func (p cmdPattern) match(text string) bool {
	if p.exact() {
		return text == p.segs[0]
	}
	if p.bare != "" && text == p.bare {
		return true
	}
	first, last := p.segs[0], p.segs[len(p.segs)-1]
	if len(text) < len(first)+len(last) || !strings.HasPrefix(text, first) || !strings.HasSuffix(text, last) {
		return false
	}
	rest := text[len(first) : len(text)-len(last)]
	for _, mid := range p.segs[1 : len(p.segs)-1] {
		i := strings.Index(rest, mid)
		if i < 0 {
			return false
		}
		rest = rest[i+len(mid):]
	}
	return true
}

// matchValue matches a parameter value against a pattern in which * is
// any text.
func matchValue(pat, s string) bool {
	if !strings.Contains(pat, "*") {
		return pat == s
	}
	p := cmdPattern{segs: strings.Split(pat, "*")}
	return p.match(s)
}

// EscapePath returns path with the characters that mean something in a
// path pattern escaped, so that a rule built from it matches that one path
// and nothing else.
func EscapePath(path string) string {
	var b strings.Builder
	for i := 0; i < len(path); i++ {
		switch path[i] {
		case '*', '?', '[', ']', '\\', '!':
			b.WriteByte('\\')
		}
		b.WriteByte(path[i])
	}
	return b.String()
}
