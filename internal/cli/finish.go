package cli

import (
	"fmt"
	"strings"
)

// takePrompt records the arguments of a session command as its prompt: the
// words joined by single spaces, and otherwise exactly as typed.
func takePrompt(inv *Invocation, args []string) {
	inv.Prompt = strings.Join(args, " ")
}

// finishSession does what `gnosh` needs once its flags are recorded: it
// decides between a session and print mode, and checks what the flags mean
// together.
func finishSession(inv *Invocation, p *parsed, env Env) *Error {
	explicit := p.explicitPrint()
	printing := explicit || !env.StdoutIsTerminal
	inv.Command = CmdInteractive
	if printing {
		inv.Command = CmdPrint
		inv.PrintImplied = !explicit
	}
	// implied goes into messages about print mode when nobody typed -p.
	implied := ""
	if inv.PrintImplied {
		implied = " (gnosh's output is not going to a terminal, so it answers once and exits, as with -p.)"
	}
	fail := func(kind ErrorKind, typed, format string, a ...any) *Error {
		return &Error{Kind: kind, Typed: typed, Message: fmt.Sprintf(format, a...)}
	}

	// Which conversation: one way of choosing at most.
	inv.Conversation = ConversationNew
	var chosen *occurrence
	for k := range p.occs {
		o := &p.occs[k]
		var kind Conversation
		switch o.f.id {
		case idContinue:
			kind = ConversationContinue
		case idResume:
			kind = ConversationResume
		default:
			continue
		}
		if chosen != nil && chosen.f != o.f {
			return fail(ErrConflict, o.typed,
				"%s and %s each say which conversation to work in, and only one can, so nothing was started. Keep one of them.",
				chosen.typed, o.typed)
		}
		chosen = o
		inv.Conversation = kind
	}
	if inv.Conversation == ConversationResume {
		inv.ResumeIsFile = strings.HasPrefix(inv.Resume, "/") && strings.HasSuffix(inv.Resume, ".jsonl")
	}
	if o := p.given(idFork); o != nil && chosen == nil {
		return fail(ErrUsage, o.typed,
			"%s goes on in a copy of an earlier session, and this line starts a new one, so nothing was started. Add -c or -r to say which session to copy.",
			o.typed)
	}
	if o := p.given(idSessionID); o != nil && chosen != nil && !inv.ForkSession {
		return fail(ErrConflict, o.typed,
			"%s gives a new conversation its ID, and %s goes on with an earlier one, so nothing was started. Add --fork-session to go on in a copy that has the new ID, or drop one of the two.",
			o.typed, chosen.typed)
	}
	for _, name := range []string{"--include-partial-messages", "--include-hook-events"} {
		if o := p.given(name); o != nil && printing && inv.OutputFormat != FormatStreamJSON {
			return fail(ErrUsage, o.typed,
				"%s only does something with --output-format stream-json, so nothing was started. Add --output-format stream-json.",
				o.typed)
		}
	}
	if o := p.given(idNoPersist); o != nil {
		// Nothing of the run is kept: it cannot go on with a saved
		// session, or give the one it starts an id or a name to find it by.
		for _, other := range []*occurrence{chosen, p.given(idSessionID), p.given("--name")} {
			if other != nil {
				return fail(ErrConflict, o.typed,
					"%s keeps nothing of the run, and %s is about a session that is kept, so nothing was started. Drop one of the two.",
					o.typed, other.typed)
			}
		}
	}
	if o := p.given(idBack); o != nil {
		// A session sent off at once, a new one or one that -c or -r
		// names: there is nobody here to read a run's output, or to pick
		// a session from a list.
		for _, other := range []*occurrence{p.given(idPrint), p.given(idFork), p.given(idNoPersist)} {
			if other != nil {
				return fail(ErrConflict, o.typed,
					"%s sends a session to the background, and %s does not go with it, so nothing was started. Drop one of the two.",
					o.typed, other.typed)
			}
		}
		if inv.Conversation == ConversationResume && strings.TrimSpace(inv.Resume) == "" {
			return fail(ErrUsage, chosen.typed,
				"%s with %s needs the session's ID or name after it, since nobody is there to pick one from a list, so nothing was started.",
				chosen.typed, o.typed)
		}
		if strings.TrimSpace(inv.Prompt) == "" {
			return fail(ErrUsage, o.typed,
				"%s needs a prompt for the session to work on, after the flags (gnosh --bg \"fix the flaky test\"), so nothing was started.",
				o.typed)
		}
	}
	if printing && inv.Conversation == ConversationResume && inv.Resume == "" {
		return fail(ErrUsage, chosen.typed,
			"%s needs a session's ID or name after it in print mode, where nobody is there to pick from a list, so nothing was started.%s",
			chosen.typed, implied)
	}

	if err := finishYolo(inv, p); err != nil {
		return err
	}

	// Flags that only print mode acts on (a session sent off with --bg
	// is a run in print mode).
	if !printing && p.given(idBack) == nil {
		for k := range p.occs {
			o := &p.occs[k]
			if !o.f.printOnly {
				continue
			}
			inside := ""
			if o.f.id == "--goal" {
				inside = " Inside a session, type /goal."
			}
			return fail(ErrUsage, o.typed,
				"%s only does something in print mode, and this line opens a session, so nothing was started. Add -p to answer once and exit.%s",
				o.typed, inside)
		}
	}
	if printing {
		if inv.OutputFormat == "" {
			inv.OutputFormat = FormatText
		}
		if inv.InputFormat == "" {
			inv.InputFormat = FormatText
		}
		if o := p.given("--input-format"); o != nil && inv.InputFormat == FormatStreamJSON {
			switch {
			case inv.OutputFormat != FormatStreamJSON:
				return fail(ErrUsage, o.typed,
					"--input-format stream-json is a conversation in JSON lines both ways, and needs --output-format stream-json, so nothing was started. Add --output-format stream-json.")
			case inv.Prompt != "":
				return fail(ErrUsage, o.typed,
					"With --input-format stream-json the prompts come on standard input, one JSON line each, and one was given on the command line too, so nothing was started. Send it as the first line instead.")
			}
		}
		if o := p.given("--replay-user-messages"); o != nil && (inv.InputFormat != FormatStreamJSON || inv.OutputFormat != FormatStreamJSON) {
			return fail(ErrUsage, o.typed,
				"%s only does something with --input-format stream-json and --output-format stream-json, so nothing was started.", o.typed)
		}
	}

	inv.SettingsInline = strings.HasPrefix(strings.TrimSpace(inv.Settings), "{")

	// The prompt.
	if len(p.args) > 0 && blank(inv.Prompt) {
		return fail(ErrBadValue, "",
			"The prompt on the command line is empty, so nothing was started. Give it some words, or leave it out.")
	}
	inv.ReadStdin = !env.StdinIsTerminal
	if printing && inv.Prompt == "" && env.StdinIsTerminal && inv.Goal == "" && inv.InputFormat != FormatStreamJSON {
		msg := "Print mode answers one prompt, and none was given, so nothing was started. Put the prompt on the command line, or pipe it in."
		if inv.PrintImplied {
			msg = "No prompt was given, so nothing was started. gnosh's output is not going to a terminal, so it would answer one prompt and exit, as with -p: put the prompt on the command line, or pipe it in."
		}
		// A one-word prompt right after a list flag joins the list. Say so,
		// since that is the likeliest way to end up here with a prompt typed.
		for k := len(p.occs) - 1; k >= 0; k-- {
			if o := &p.occs[k]; o.f.kind == kindList && len(o.raw) > 1 {
				msg += fmt.Sprintf(" (%s took %s as part of its list. If that was the prompt, put it before %s or after --.)",
					o.typed, shown(o.raw[len(o.raw)-1]), o.typed)
				break
			}
		}
		return fail(ErrUsage, "", "%s", msg)
	}
	return nil
}

// finishTrust checks `gnosh trust`: listing and forgetting are two things,
// and listing takes no folder.
func finishTrust(inv *Invocation, p *parsed, _ Env) *Error {
	list, forget := p.given("trust --list"), p.given("trust --forget")
	if list != nil && forget != nil {
		return &Error{Kind: ErrConflict, Typed: forget.typed, Message: fmt.Sprintf(
			"%s and %s ask for two different things, so nothing was done. Give one of them.", list.typed, forget.typed)}
	}
	if list != nil && len(inv.Args) > 0 {
		return &Error{Kind: ErrUsage, Typed: inv.Args[0], Message: fmt.Sprintf(
			"%s shows every trusted folder and takes no folder, and %s was typed with it, so nothing was done.",
			list.typed, shown(inv.Args[0]))}
	}
	return nil
}

// takeRename records `gnosh session rename <id> <title...>`: the ID, then
// the rest of the words as one title.
func takeRename(inv *Invocation, args []string) {
	inv.Args = []string{args[0], strings.Join(args[1:], " ")}
}

// finishHelp turns `gnosh help [command...]` into a request for that
// command's help.
func finishHelp(inv *Invocation, p *parsed, _ Env) *Error {
	c, err := p.t.find(inv.Args)
	if err != nil {
		return err
	}
	inv.HelpFor = c.path()
	inv.Args = nil
	return nil
}

// find returns the command the words lead to.
func (t *Table) find(words []string) (*command, *Error) {
	c := t.root
	for _, w := range words {
		sub := c.sub(w)
		if sub == nil {
			return nil, (&parsed{t: t, cmd: c}).unknownCommand(w)
		}
		c = sub
	}
	return c, nil
}

// finishYolo settles -y, which is a permission mode, so it cannot sit
// beside another: for gnosh, and for gnosh agents, whose view gives it to
// the runs it starts.
func finishYolo(inv *Invocation, p *parsed) *Error {
	y := p.given(idYolo)
	if y == nil {
		return nil
	}
	if m := p.given(idMode); m != nil && inv.PermissionMode != ModeBypassPermissions {
		return &Error{Kind: ErrConflict, Typed: y.typed, Message: fmt.Sprintf(
			"%s skips every permission question and %s %s starts in another mode, so nothing was started. Keep one of them.",
			y.typed, m.typed, inv.PermissionMode)}
	}
	inv.PermissionMode = ModeBypassPermissions
	return nil
}
