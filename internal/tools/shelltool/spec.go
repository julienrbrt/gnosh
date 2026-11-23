package shelltool

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/gnosh-ai/gnosh/internal/core"
)

// This file holds what the model is told about the tools: gnosh's own
// words, built from the manager's real limits so that they stay true when
// the limits change.

// bashSpec describes the Bash tool to the model.
func (m *Manager) bashSpec() core.ToolSpec {
	o := &m.opt
	var b strings.Builder
	fmt.Fprintf(&b, `Runs a shell command and returns what it printed.

How the command runs:
- A fresh %s runs it, without a terminal, in the session's working directory. Write the command exactly as you would type it: several lines, pipes, quotes, variables and here-documents all work as usual.
- Nobody can type into it. Its standard input is empty, so a program that asks a question gets no answer and gives up or fails. Pass the flags that answer for you (-y, --yes, --no-edit), and do not start an editor or a pager.
- What it writes to stdout and to stderr comes back as one stream, in the order written. An exit code other than 0 is given at the end.
- At most %d characters of output are shown (%d when the command failed). Longer output is shown as its start and its end, with a line between them that says how many lines were left out and which file holds all of it; read or search that file if you need the middle.

What carries over to your next command:
- The working directory, and nothing else. If the command ends in another directory inside the project, later commands start there. A directory outside the project is not kept, and the result says so.
- Variables, functions, aliases and shell options set in one command are gone in the next. Put steps that depend on each other in one command.

How long it may run:
- %s is in milliseconds. Without it a command gets %d (%s); you can ask for up to %d (%s), and a larger number is lowered to that.
`, filepath.Base(m.shell), o.MaxOutputChars, o.MaxFailedOutputChars, argTimeout,
		o.DefaultTimeout.Milliseconds(), seconds(o.DefaultTimeout), o.MaxTimeout.Milliseconds(), seconds(o.MaxTimeout))
	if o.TimeoutToBackground && !o.NoBackground {
		fmt.Fprintf(&b, "- A command still running when its time is up is not stopped: it goes on in the background, and the result gives you its task id and what it has printed so far.\n")
	} else {
		fmt.Fprintf(&b, "- When the time is up, the command and everything it started are stopped, and you get what it printed until then.\n")
	}
	if o.NoBackground {
		fmt.Fprintf(&b, "- Background commands are off in this session: %s is refused, and every command must end within its time.", argBackground)
	} else {
		fmt.Fprintf(&b, "- For a server, a watcher, or anything that may take longer, set %s to true. The call then returns at once with a task id and the file that collects the output.", argBackground)
		switch {
		case m.opt.NoOutputTool:
			fmt.Fprintf(&b, " Read that file to see the output, and stop the command with %s.", StopName)
		default:
			fmt.Fprintf(&b, " %s returns what it has printed since you last asked, and %s stops it.", OutputName, StopName)
		}
		if o.OnEnd != nil {
			b.WriteString(" You are told when it ends, so there is no need to wait for it or to keep checking.")
		}
	}
	fmt.Fprintf(&b, "\n- Do not keep a process alive by ending the command with &. It stays tied to this call's output and is reported as left running; %s is the way.", argBackground)

	if sb := o.Sandbox; sb != nil {
		if sb.Writes == "" {
			fmt.Fprintf(&b, "\n\nIn the sandbox:\n- Commands on this machine run in a sandbox: they read and write files as usual, and %s.", sb.network())
		} else {
			fmt.Fprintf(&b, "\n\nIn the sandbox:\n- Commands on this machine run in a sandbox: they write only in %s, %s, and read everything else as usual.", sb.Writes, sb.network())
		}
		if sb.Unsandboxed {
			fmt.Fprintf(&b, "\n- A command that failed because of the sandbox (\"Read-only file system\" or \"Operation not permitted\" on a write elsewhere, a name that does not resolve, a connection that cannot be made) may be run again outside it with %s set to true; that call is judged as any command is, the person asked unless a rule or the mode lets it run. Do not set it for a command that has not failed that way.", argNoSandbox)
		} else {
			b.WriteString("\n- The sandbox cannot be left in this session: when a command must write elsewhere or reach the network, tell the person what and why.")
		}
	}

	// What is said of other machines is the same whether or not any is
	// declared, so that the tools a model is sent are the same bytes in
	// every session. Which machines there are goes with the first prompt.
	fmt.Fprintf(&b, `

On another machine:
- With %[1]s set to the name of a declared machine, ssh runs the command there, in that account's login shell and home directory. The machines declared, if any, are listed with your first prompt. Without %[1]s the command runs here.
- Nothing carries over there, not even the directory: put cd and the work in one command. There is no %[2]s there either; a command is stopped when its time is up, and a program there that prints nothing may outlive the stop.
- Every result begins with a line that says where it ran: <host>name (account@address)</host><cwd>directory</cwd>. A path belongs to the machine it was seen on.
- With machines declared, never type ssh or scp in a command: it is refused.`, argHost, argBackground)

	timeout := fmt.Sprintf("How long the command may run, in milliseconds. Default %d, at most %d.", o.DefaultTimeout.Milliseconds(), o.MaxTimeout.Milliseconds())
	params := []param{
		{argCommand, "string", "The command to run, exactly as you would type it at a shell prompt."},
		{argDescription, "string", "What the command does, in a few plain words, for the person watching."},
		{argTimeout, "number", timeout},
		{argBackground, "boolean", "Set to true to start the command in the background and return at once."},
		{argHost, "string", "The name of a declared machine to run the command on. Leave it out to run on this machine."},
	}
	if o.Sandbox != nil && o.Sandbox.Unsandboxed {
		params = append(params, param{argNoSandbox, "boolean", "Set to true to run the command outside the sandbox, after it failed because of the sandbox. The call is then judged as any command is."})
	}
	return core.ToolSpec{
		Name:        BashName,
		Description: b.String(),
		InputSchema: schema([]string{argCommand}, params...),
	}
}

// outputSpec describes the TaskOutput tool to the model.
func (m *Manager) outputSpec() core.ToolSpec {
	text := fmt.Sprintf(`Returns what a background command has printed since you last asked, and whether it is still running.

- %[1]s is the id %[2]s gave when it started the command with %[3]s.
- Each call returns only output you have not been given yet, so ask again later for more. The whole output stays in the command's output file.
- %[4]s, if you give one, is a regular expression: only the new lines that match it are returned, and the other new lines are passed over for good. The syntax is RE2's, which has no look-around and no back-references.
- At most %[5]d characters are returned; longer output is shown as its start and its end.
- When the command has ended, the result says how: its exit code, or the signal that killed it.`,
		argTaskID, BashName, argBackground, argFilter, m.opt.MaxOutputChars)
	return core.ToolSpec{
		Name:        OutputName,
		Description: text,
		InputSchema: schema([]string{argTaskID},
			param{argTaskID, "string", "The id of the background command."},
			param{argFilter, "string", "A regular expression; only matching lines are returned."},
		),
	}
}

// stopSpec describes the TaskStop tool to the model.
func (m *Manager) stopSpec() core.ToolSpec {
	started := fmt.Sprintf("that %s started with %s", BashName, argBackground)
	if m.opt.OnMonitor != nil && !m.opt.NoBackground {
		started += fmt.Sprintf(", or the command a %s call watches", MonitorName)
	}
	text := fmt.Sprintf(`Stops a background command %[1]s.

The command and everything it started are asked to exit and, if they have not gone within %[2]s, are killed. The result says how the command ended. Stopping a command that has already ended changes nothing and says how it ended.`,
		started, seconds(m.opt.TermGrace))
	return core.ToolSpec{
		Name:        StopName,
		Description: text,
		InputSchema: schema([]string{argTaskID},
			param{argTaskID, "string", "The id of the background command to stop."},
		),
	}
}

// param is one argument in a tool's schema.
type param struct {
	name, kind, about string
}

// schema writes the JSON Schema of a tool's arguments: an object with the
// given properties, in the order given, of which required must be there.
func schema(required []string, params ...param) json.RawMessage {
	var b bytes.Buffer
	b.WriteString(`{"type":"object","properties":{`)
	for i, p := range params {
		if i > 0 {
			b.WriteByte(',')
		}
		b.Write(jsonText(p.name))
		b.WriteString(`:{"type":`)
		b.Write(jsonText(p.kind))
		b.WriteString(`,"description":`)
		b.Write(jsonText(p.about))
		b.WriteByte('}')
	}
	b.WriteString(`},"required":`)
	b.Write(jsonText(required))
	b.WriteString(`,"additionalProperties":false}`)
	return b.Bytes()
}

// jsonText encodes v as JSON, leaving <, > and & as they are.
func jsonText(v any) []byte {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		// It is only ever given strings and lists of strings, which always
		// encode.
		return []byte(`""`)
	}
	return bytes.TrimSuffix(b.Bytes(), []byte("\n"))
}
