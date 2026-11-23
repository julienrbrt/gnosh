package shelltool

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// bashArgs is one Bash call, checked and with its defaults filled in.
type bashArgs struct {
	command     string        // exactly as the model wrote it
	description string        // what the model says it does; may be empty
	background  bool          // start it and return at once
	limit       time.Duration // how long it may run; 0 for no limit
	asked       time.Duration // the timeout the model asked for, when that was more than it got
	host        *Host         // the machine it is for; nil for this one
	spool       int64         // how much of its output its file may hold, when that is not Options.MaxSpoolBytes; 0 for that
	watch       bool          // a Monitor's: what it prints is passed on as it comes
	sandboxed   bool          // it runs inside the sandbox (Options.Sandbox)
}

// summary is the one line that says what the call would do: the model's
// own description, or failing that the command's first line. For a command
// on another machine it names the machine first.
func (a bashArgs) summary() string {
	s := oneLine(a.description, 200)
	if s == "" {
		s = firstLine(a.command, 200)
	}
	if a.host != nil {
		return "On " + a.host.Name + " (" + a.host.Target + "): " + s
	}
	return s
}

// bashArgs reads and checks the arguments of a Bash call. An error is
// worded for the model: it is what Describe returns and what Run shows.
// What was read before the error is returned beside it, so that a caller
// can still say what the call was aimed at.
func (m *Manager) bashArgs(raw json.RawMessage) (bashArgs, error) {
	fields, err := objectFields(raw)
	if err != nil {
		return bashArgs{}, err
	}
	var a bashArgs
	command, ok, err := stringField(fields, argCommand)
	switch {
	case err != nil:
		return a, err
	case !ok:
		return a, fmt.Errorf("%s is missing: give the shell command to run", argCommand)
	case strings.TrimSpace(command) == "":
		return a, fmt.Errorf("%s is empty: give the shell command to run", argCommand)
	case strings.IndexByte(command, 0) >= 0:
		return a, fmt.Errorf("%s holds a NUL byte, which a shell command cannot", argCommand)
	}
	a.command = command
	if a.description, _, err = stringField(fields, argDescription); err != nil {
		return a, err
	}
	if a.background, err = boolField(fields, argBackground); err != nil {
		return a, err
	}
	asked, err := millisField(fields, argTimeout)
	if err != nil {
		return a, err
	}
	name, _, err := stringField(fields, argHost)
	if err != nil {
		return a, err
	}
	if a.host, err = m.findHost(name); err != nil {
		return a, err
	}
	if err := unknownArg(fields); err != nil {
		return a, err
	}
	leave, err := boolField(fields, argNoSandbox)
	if err != nil {
		return a, err
	}
	a.sandboxed = m.sandboxes(a.command, a.host, leave)

	// A foreground command always has a limit. A background one has one
	// only if the model or the options give it one. A timeout over the
	// ceiling is lowered to it, as Claude Code does, rather than refused.
	fallback, ceiling := m.opt.DefaultTimeout, m.opt.MaxTimeout
	if a.background {
		fallback, ceiling = m.opt.BackgroundTimeout, m.opt.MaxBackgroundTimeout
	}
	a.limit = asked
	if a.limit == 0 {
		a.limit = fallback
	}
	if ceiling > 0 && a.limit > ceiling {
		a.limit, a.asked = ceiling, a.limit
	}
	return a, nil
}

// taskArgs is one TaskOutput or TaskStop call.
type taskArgs struct {
	id     string
	filter *regexp.Regexp // nil when the model gave none
}

// taskArgsOf reads and checks the arguments of a TaskOutput or TaskStop
// call. An error is worded for the model.
func taskArgsOf(raw json.RawMessage) (taskArgs, error) {
	fields, err := objectFields(raw)
	if err != nil {
		return taskArgs{}, err
	}
	var a taskArgs
	id, ok, err := stringField(fields, argTaskID)
	if err == nil && !ok {
		id, ok, err = stringField(fields, argTaskIDOld)
	}
	switch {
	case err != nil:
		return a, err
	case !ok || strings.TrimSpace(id) == "":
		return a, fmt.Errorf("%s is missing: give the id %s returned when it started the command in the background", argTaskID, BashName)
	}
	a.id = strings.TrimSpace(id)
	pattern, ok, err := stringField(fields, argFilter)
	if err != nil {
		return a, err
	}
	if ok && pattern != "" {
		if a.filter, err = regexp.Compile(pattern); err != nil {
			return a, fmt.Errorf("%s is not a regular expression this tool can use (RE2 syntax: no look-around, no back-references): %v", argFilter, err)
		}
	}
	return a, nil
}

// objectFields splits a call's arguments, one JSON object, into its
// fields. No arguments at all count as an empty object.
func objectFields(raw json.RawMessage) (map[string]json.RawMessage, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return nil, nil
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return nil, errors.New("the arguments are not one JSON object")
	}
	return fields, nil
}

// present returns the value of a field, and whether it is there and not
// null.
func present(fields map[string]json.RawMessage, name string) (json.RawMessage, bool) {
	raw := bytes.TrimSpace(fields[name])
	return raw, len(raw) > 0 && !bytes.Equal(raw, []byte("null"))
}

// stringField reads a field that must be a string, and reports whether it
// is there.
func stringField(fields map[string]json.RawMessage, name string) (string, bool, error) {
	raw, ok := present(fields, name)
	if !ok {
		return "", false, nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return "", false, fmt.Errorf("%s must be a string", name)
	}
	return s, true, nil
}

// boolField reads a field that must be true or false. Models now and then
// write "true" in quotes, so that is read too. A missing field is false.
func boolField(fields map[string]json.RawMessage, name string) (bool, error) {
	raw, ok := present(fields, name)
	if !ok {
		return false, nil
	}
	var b bool
	if err := json.Unmarshal(raw, &b); err == nil {
		return b, nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		switch strings.ToLower(strings.TrimSpace(s)) {
		case "true":
			return true, nil
		case "false", "":
			return false, nil
		}
	}
	return false, fmt.Errorf("%s must be true or false", name)
}

// millisField reads a field that must be a number of milliseconds, and
// returns it as a duration. Models now and then write the number in quotes,
// so that is read too. A missing field, and zero, give 0.
func millisField(fields map[string]json.RawMessage, name string) (time.Duration, error) {
	raw, ok := present(fields, name)
	if !ok {
		return 0, nil
	}
	text := string(raw)
	if raw[0] == '"' {
		if err := json.Unmarshal(raw, &text); err != nil {
			return 0, fmt.Errorf("%s must be a number of milliseconds", name)
		}
	}
	ms, err := strconv.ParseFloat(strings.TrimSpace(text), 64)
	switch {
	case err != nil || math.IsNaN(ms) || math.IsInf(ms, 0):
		return 0, fmt.Errorf("%s must be a number of milliseconds", name)
	case ms < 0:
		return 0, fmt.Errorf("%s must not be negative: it is the number of milliseconds the command may run", name)
	}
	// A year is far past any limit; staying under it keeps the arithmetic
	// from overflowing.
	const year = 365 * 24 * time.Hour
	if ms >= float64(year/time.Millisecond) {
		return year, nil
	}
	return time.Duration(math.Ceil(ms)) * time.Millisecond, nil
}

// sandboxes reports whether a command runs inside the sandbox: a command
// on this machine, when there is a sandbox, but one it says runs outside
// (excludedCommands, or the model's asking, where that is let).
func (m *Manager) sandboxes(command string, host *Host, leave bool) bool {
	sb := m.opt.Sandbox
	return sb != nil && host == nil && (sb.Outside == nil || !sb.Outside(command, leave && sb.Unsandboxed))
}
