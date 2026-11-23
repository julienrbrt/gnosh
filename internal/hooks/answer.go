package hooks

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
)

// Decision is an answer to "may this tool call run?".
type Decision string

const (
	// NoDecision is no opinion: the call goes to the normal permission
	// check.
	NoDecision Decision = ""
	// Allow lets the call run without a permission question.
	Allow Decision = "allow"
	// Ask puts the call to the user, even when it would otherwise run
	// without asking.
	Ask Decision = "ask"
	// Deny stops the call.
	Deny Decision = "deny"
)

// answer is the JSON object a hook printed, as far as it could be read.
//
// Reading is strict. A field gnosh does not know, a field of the wrong
// type and a decision word that is not exactly one of Claude Code's are
// each recorded in errs, and a hook whose answer has any could not be
// evaluated. Reading also goes on past a mistake, so that a refusal
// written beside an unreadable field is still found: a hook that got one
// field wrong and said "deny" in another meant to deny.
type answer struct {
	// halt is "continue": false, and stopReason the text given with it.
	halt       bool
	stopReason string
	// warning is "systemMessage": text the hook wants the user shown.
	warning string
	// refuse says the hook blocked, denied or refused, in any of the
	// forms it can; ask and approve likewise. reason is the text that
	// goes with whichever it is.
	refuse  bool
	ask     bool
	approve bool
	reason  string
	// interrupt is a PermissionRequest hook's wish that its refusal end
	// the turn.
	interrupt bool
	// changes says that a PermissionRequest hook's answer came with a
	// changed input or changed permissions, which gnosh does not apply.
	changes bool
	// input is "updatedInput": the tool input to use instead.
	input json.RawMessage
	// context is "additionalContext": text for the model.
	context string
	// output is "updatedToolOutput" (or "updatedMCPToolOutput"): what the
	// model is to read in place of what the tool returned, as the hook
	// wrote it.
	output json.RawMessage
	// watch is "watchPaths", the files to watch from now on, and watchSet
	// says it was given (an empty list clears the list).
	watch    []string
	watchSet bool
	// action is an Elicitation or ElicitationResult hook's answer to an
	// MCP server's request, "accept" or "cancel" (a "decline" is a
	// refusal), and content the values that go with accept.
	action  string
	content json.RawMessage
	// notes says, in plain words, what the hook asked for that gnosh does
	// not act on and that is harmless to leave undone.
	notes []string
	// errs says what could not be read.
	errs []string
}

// looksLikeObject reports whether a hook's output is to be read as JSON:
// it starts with "{" and ends with "}", with only white space around.
// Anything else, a JSON list or a quoted string included, is plain text.
func looksLikeObject(out string) bool {
	return strings.HasPrefix(out, "{") && strings.HasSuffix(out, "}")
}

// strayAnswer reports whether a hook's output, which is not one JSON
// object and so counts as plain text, nevertheless holds a JSON object
// that reads as a hook's answer: one that starts a line and has at least
// one of an answer's fields. A stray line before the answer (a shell
// profile's greeting, a progress message) or after it is how that
// happens.
func strayAnswer(out string) bool {
	tries := 0
	for pos := 0; pos < len(out) && tries < 32; {
		next := len(out)
		if end := strings.IndexByte(out[pos:], '\n'); end >= 0 {
			next = pos + end + 1
		}
		line := out[pos:next]
		pos = next
		rest := strings.TrimLeft(line, " \t\r")
		if !strings.HasPrefix(rest, "{") {
			continue
		}
		tries++
		var value json.RawMessage
		from := next - len(rest)
		if json.NewDecoder(strings.NewReader(out[from:])).Decode(&value) != nil {
			continue
		}
		fields, err := members(value)
		if err != nil {
			continue
		}
		for _, f := range fields {
			if slices.Contains(topFields, f.key) {
				return true
			}
		}
	}
	return false
}

// The fields a hook's answer may have at the top, on every event.
var topFields = []string{
	"continue", "stopReason", "systemMessage", "suppressOutput",
	"terminalSequence", "decision", "reason", "hookSpecificOutput",
}

// parseAnswer reads the JSON object a hook on event printed.
func parseAnswer(event Event, info eventInfo, out []byte) answer {
	var a answer
	fields, err := members(out)
	if err != nil {
		var twice duplicateKey
		if errors.As(err, &twice) {
			a.fail("it has %s twice, and only one of the two could count", quote(string(twice)))
		} else {
			a.fail("it is not one JSON object (%s)", jsonError(err))
		}
		return a
	}

	var (
		top        string // the top-level "decision"
		topReason  string
		permission string // hookSpecificOutput.permissionDecision
		permReason string
		behavior   string // hookSpecificOutput.decision.behavior
		message    string
	)
	for _, f := range fields {
		if isNull(f.value) {
			continue
		}
		switch f.key {
		case "continue":
			if on, ok := a.flag(f); ok && !on {
				a.halt = true
			}
		case "stopReason":
			a.stopReason, _ = a.str(f)
		case "systemMessage":
			a.warning, _ = a.str(f)
		case "suppressOutput":
			a.flag(f)
		case "terminalSequence":
			if _, ok := a.str(f); ok {
				a.note("it asked for a terminal sequence to be written, which gnosh does not do")
			}
		case "decision":
			if s, ok := a.str(f); ok {
				if s == "approve" || s == "block" {
					top = s
				} else {
					a.fail("\"decision\" is %s; it must be exactly \"block\" or \"approve\", or be left out", quote(s))
				}
			}
		case "reason":
			topReason, _ = a.str(f)
		case "hookSpecificOutput":
			inner, err := members(f.value)
			if err != nil {
				a.fail("\"hookSpecificOutput\" must be a JSON object; this one %s", unreadable(f.value, err))
				continue
			}
			for _, g := range inner {
				if isNull(g.value) {
					continue
				}
				switch {
				case g.key == "hookEventName":
					if name, ok := a.str(g); ok && name != string(event) {
						a.fail("its \"hookSpecificOutput\" is for the event %s, but the hook was run for %s", quote(name), event)
					}
				case g.key == "permissionDecision":
					// Read on every event, so that a deny written in the
					// wrong place still counts as a refusal.
					if s, ok := a.str(g); ok {
						switch {
						case s == "defer":
							a.fail("\"permissionDecision\" is \"defer\", which gnosh does not support")
						case s != "allow" && s != "deny" && s != "ask":
							a.fail("\"permissionDecision\" is %s; it must be exactly \"allow\", \"deny\" or \"ask\"", quote(s))
						default:
							permission = s
						}
					}
					if event != PreToolUse && event != PreModelSwitch {
						a.fail("it gives \"permissionDecision\", which only a PreToolUse or PreModelSwitch hook can")
					}
				case g.key == "permissionDecisionReason":
					// Read on every event too: it is the reason of a deny
					// that holds wherever it was written.
					permReason, _ = a.str(g)
					if event != PreToolUse && event != PreModelSwitch && !hasKey(inner, "permissionDecision") {
						a.fail("it gives \"permissionDecisionReason\", which only a PreToolUse or PreModelSwitch hook can")
					}
				case !slices.Contains(info.fields, g.key):
					a.fail("\"hookSpecificOutput\" has %s, which is not something a %s hook can say%s", quote(g.key), event, known(info.fields))
				case g.key == "updatedInput":
					if kindOf(g.value) != "an object" {
						a.fail("\"updatedInput\" must be a JSON object holding the whole new input; this one is %s", kindOf(g.value))
						continue
					}
					a.input = compact(g.value)
				case g.key == "additionalContext":
					a.context = a.text(g)
				case g.key == "decision":
					behavior, message = a.permissionRequest(g.value)
				case g.key == "updatedToolOutput", g.key == "updatedMCPToolOutput":
					if a.output != nil && !bytes.Equal(a.output, compact(g.value)) {
						a.fail("it gives both \"updatedToolOutput\" and \"updatedMCPToolOutput\", different, and only one can stand in for the tool's output")
						continue
					}
					a.output = compact(g.value)
				case g.key == "initialUserMessage":
					a.fail("it gives \"initialUserMessage\", to start the conversation with a message of its own, which gnosh does not do yet")
				case g.key == "sessionTitle":
					if _, ok := a.str(g); ok {
						a.note("it asked to name the session, which gnosh does not do from a hook")
					}
				case g.key == "suppressOriginalPrompt":
					a.flag(g)
				case g.key == "reloadSkills":
					if on, _ := a.flag(g); on {
						a.note("it asked for skills to be loaded again, which gnosh does not do from a hook")
					}
				case g.key == "action":
					if s, ok := a.str(g); ok {
						if s == "accept" || s == "decline" || s == "cancel" {
							a.action = s
						} else {
							a.fail("\"action\" is %s; it must be exactly \"accept\", \"decline\" or \"cancel\"", quote(s))
						}
					}
				case g.key == "content":
					if kindOf(g.value) != "an object" {
						a.fail("\"content\" must be a JSON object holding the form's values by name; this one is %s", kindOf(g.value))
						continue
					}
					a.content = compact(g.value)
				case g.key == "watchPaths":
					var paths []string
					if kindOf(g.value) != "a list" || json.Unmarshal(g.value, &paths) != nil {
						a.fail("\"watchPaths\" must be a list of absolute paths; this one is %s", kindOf(g.value))
						continue
					}
					a.watch, a.watchSet = []string{}, true
					for _, p := range paths {
						if !filepath.IsAbs(p) {
							a.fail("\"watchPaths\" holds %s, which is not an absolute path", quote(p))
							continue
						}
						a.watch = append(a.watch, filepath.Clean(p))
					}
				}
			}
		default:
			a.fail("it has %s, which is not a field of a hook's answer%s", quote(f.key), known(topFields))
		}
	}

	// Put the forms a decision can take together. Each event has its own
	// proper form; a refusal in another form is still a refusal, and
	// anything else in another form is a mistake.
	switch event {
	case PreToolUse, PreModelSwitch:
		switch {
		case permission != "": // it wins over the older top-level form
			a.refuse, a.ask, a.approve = permission == "deny", permission == "ask", permission == "allow"
			a.reason = permReason
			if a.reason == "" {
				a.reason = topReason
			}
		case top != "":
			a.refuse, a.approve = top == "block", top == "approve"
			a.reason = topReason
		}
	case Elicitation, ElicitationResult:
		// A decline in any form is a refusal, and wins over an answer in
		// the same output, as a top-level block does in Claude Code.
		switch {
		case top == "block" || a.action == "decline" || permission == "deny":
			a.refuse, a.action, a.content = true, "", nil
			a.reason = topReason
		case a.content != nil && a.action != "accept":
			a.note("it gave \"content\" without \"action\": \"accept\", so its values were not used")
			a.content = nil
		}
		if top == "approve" {
			a.note("it said \"decision\": \"approve\", which answers nothing here; a hook answers a server's form with hookSpecificOutput.action")
		}
	case PermissionRequest:
		switch {
		case behavior != "":
			a.refuse, a.approve = behavior == "deny", behavior == "allow"
			a.reason = message
		case top == "block" || permission == "deny":
			a.refuse = true
			a.reason = topReason
			if a.reason == "" {
				a.reason = permReason
			}
		}
		if top != "" {
			a.fail("it gives a top-level \"decision\"; a PermissionRequest hook answers with hookSpecificOutput.decision.behavior, \"allow\" or \"deny\"")
		}
	default:
		if top == "block" || permission == "deny" {
			a.refuse = true
			a.reason = topReason
			if a.reason == "" {
				a.reason = permReason
			}
		}
	}
	return a
}

// permissionRequest reads the "decision" object of a PermissionRequest
// hook's answer.
func (a *answer) permissionRequest(raw []byte) (behavior, message string) {
	fields, err := members(raw)
	if err != nil {
		a.fail("\"decision\" must be a JSON object such as {\"behavior\": \"allow\"}; this one %s", unreadable(raw, err))
		return "", ""
	}
	for _, f := range fields {
		if isNull(f.value) {
			continue
		}
		switch f.key {
		case "behavior":
			if s, ok := a.str(f); ok {
				if s == "allow" || s == "deny" {
					behavior = s
				} else {
					a.fail("\"behavior\" is %s; it must be exactly \"allow\" or \"deny\"", quote(s))
				}
			}
		case "message":
			message, _ = a.str(f)
		case "interrupt":
			a.interrupt, _ = a.flag(f)
		case "updatedInput":
			if kindOf(f.value) != "an object" {
				a.fail("\"updatedInput\" must be a JSON object holding the whole new input; this one is %s", kindOf(f.value))
			}
			a.changes = true
		case "updatedPermissions":
			if kindOf(f.value) != "a list" {
				a.fail("\"updatedPermissions\" must be a list; this one is %s", kindOf(f.value))
			}
			a.changes = true
		default:
			a.fail("\"decision\" has %s, which is not part of a permission answer%s", quote(f.key), known([]string{"behavior", "message", "interrupt", "updatedInput", "updatedPermissions"}))
		}
	}
	if behavior == "" && len(a.errs) == 0 {
		a.fail("\"decision\" has no \"behavior\"; it must say \"allow\" or \"deny\"")
	}
	return behavior, message
}

// hasKey reports whether an object has the key.
func hasKey(fields []member, key string) bool {
	return slices.ContainsFunc(fields, func(f member) bool { return f.key == key })
}

// fail records something that could not be read.
func (a *answer) fail(format string, args ...any) {
	a.errs = append(a.errs, fmt.Sprintf(format, args...))
}

// note records something that was asked for and is not done.
func (a *answer) note(text string) {
	a.notes = append(a.notes, text)
}

// str reads a field that must be a string.
func (a *answer) str(f member) (string, bool) {
	s, ok := asString(f.value)
	if !ok {
		a.fail("%s must be a string; this one is %s", quote(f.key), kindOf(f.value))
	}
	return s, ok
}

// flag reads a field that must be true or false.
func (a *answer) flag(f member) (value, ok bool) {
	value, ok = asBool(f.value)
	if !ok {
		a.fail("%s must be true or false; this one is %s", quote(f.key), kindOf(f.value))
	}
	return value, ok
}

// text reads a field that may be a string or a list of strings, and
// returns the strings joined by newlines.
func (a *answer) text(f member) string {
	if s, ok := asString(f.value); ok {
		return s
	}
	var parts []string
	if kindOf(f.value) != "a list" || json.Unmarshal(f.value, &parts) != nil {
		a.fail("%s must be a string or a list of strings; this one is %s", quote(f.key), kindOf(f.value))
		return ""
	}
	return strings.Join(parts, "\n")
}

// unreadable finishes a sentence about a value that is not a readable
// object.
func unreadable(raw []byte, err error) string {
	var twice duplicateKey
	switch {
	case errors.As(err, &twice):
		return "has " + quote(string(twice)) + " twice"
	case errors.Is(err, errNotObject):
		return "is " + kindOf(raw)
	}
	return "cannot be read (" + jsonError(err) + ")"
}

// known finishes a sentence with the names that would have been right.
func known(names []string) string {
	if len(names) == 0 {
		return ""
	}
	quoted := make([]string, len(names))
	for i, n := range names {
		quoted[i] = quote(n)
	}
	return " (it may have " + strings.Join(quoted, ", ") + ")"
}

// compact returns raw without insignificant white space, so that two
// hooks that wrote the same JSON differently are seen to agree.
func compact(raw []byte) json.RawMessage {
	var buf bytes.Buffer
	if err := json.Compact(&buf, raw); err != nil {
		return json.RawMessage(bytes.TrimSpace(raw))
	}
	return buf.Bytes()
}
