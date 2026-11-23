package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gnosh-ai/gnosh/internal/app/skill"
	"github.com/gnosh-ai/gnosh/internal/core"
	"github.com/gnosh-ai/gnosh/internal/permission"
	"github.com/gnosh-ai/gnosh/internal/prompt"
	"github.com/gnosh-ai/gnosh/internal/tools/shelltool"
)

// This file is a skill's commands that run before the model sees it
// (spec/cc/slash-commands-and-skills.md 5.4): !`command` at the start of a
// line or after a blank, and a fenced block opened with ```!. Each runs in
// the session's working directory, with stderr in with stdout and two
// minutes to finish, and what it prints takes its place in the skill's
// text, in one pass: what a command prints is not read for more. A command
// never asks: one the rules do not allow outright (a deny, an ask, or no
// rule that lets it run) stops the skill, and so does one that fails. A
// skill's allowed-tools let its commands run.

// The limits of a skill's command: its time, and how much of what it
// prints stands in the skill's text before the rest goes to a file.
const (
	injectTime    = 2 * time.Minute
	injectMost    = 30000
	injectPreview = 2000
	// injectSpoolMost is the most of what a command prints that is kept:
	// past it the command is stopped.
	injectSpoolMost = 32 << 20
)

// injectCommands runs the commands of a skill's parts (skill.Parts) and
// gives its text with what each printed in its place. What a command
// prints is not read for more. env is that of the agent the skill is
// for: the commands are judged as its calls are, under its grants, and
// run where it works.
func (l *live) injectCommands(env *core.ToolEnv, parts []skill.Part) (string, error) {
	var b strings.Builder
	for _, p := range parts {
		if !p.Command {
			b.WriteString(p.Text)
			continue
		}
		got, err := l.injectOne(env, p.Text, true)
		if err != nil {
			return "", err
		}
		b.WriteString(got)
	}
	return b.String(), nil
}

// checkCommands checks every command of a skill's parts against the
// rules, as injectCommands does before it runs one, and runs none: a typed
// line of several skills checks all of theirs before any runs.
func (l *live) checkCommands(env *core.ToolEnv, parts []skill.Part) error {
	for _, p := range parts {
		if p.Command {
			if _, err := l.injectOne(env, p.Text, false); err != nil {
				return err
			}
		}
	}
	return nil
}

// injectOne checks one command against the rules, as a call of the Bash
// tool by the agent env is for, runs it when run is set, and returns what
// it printed.
func (l *live) injectOne(env *core.ToolEnv, command string, run bool) (string, error) {
	pattern := oneLine(command, 200)
	refused := func(why string) error {
		return fmt.Errorf("Shell command permission check failed for pattern %q: %s", pattern, why)
	}
	var bash core.Tool
	for _, t := range l.tools {
		if t.Spec().Name == shelltool.BashName {
			bash = t
		}
	}
	if bash == nil || l.engine == nil {
		return "", refused("this session has no Bash tool")
	}
	input, err := json.Marshal(map[string]string{"command": command})
	if err != nil {
		return "", refused(err.Error())
	}
	call := core.ToolCall{ID: newCallID(), Name: shelltool.BashName, Input: input}
	if env == nil {
		env = l.env()
	}
	action, err := describe(bash, call, env)
	if err != nil {
		return "", refused(err.Error())
	}
	// A sub-agent's skill runs its commands where the sub-agent works, in
	// its own work tree when it has one, as its own calls do: they ran in
	// the session's folder, judged as the session's own agent's.
	who, cwd, tree := agentOf(env), firstOf(env.Cwd, l.cwd()), env.Tree
	if tree == nil && who == "" && l.agent != nil {
		tree = l.agent.Tree()
	}
	if tree != nil {
		// A session in a git work tree of its own keeps its skills'
		// commands there too, as it keeps its own (review, 16 November).
		if why := l.commandOutsideTree(*tree, cwd, command); why != "" {
			return "", refused(why)
		}
	}
	if d := l.reach(who, cwd, call, action, l.engine.DecideFor(who, cwd, call.Name, call, action)); d.Behavior != permission.Allow {
		return "", refused(firstOf(strings.TrimRight(d.Reason, "."), "no rule lets it run") + ". A skill's commands never ask: its allowed-tools can let them run")
	}
	if !run {
		return "", nil
	}
	out, code, err := l.runInjected(command, cwd)
	if err == nil && code != 0 && (code != 1 || shelltool.HarmlessExit(command) == "") {
		err = fmt.Errorf("exit code %d", code)
	}
	if err != nil {
		return "", fmt.Errorf("Shell command failed for pattern %q: %v\n%s", pattern, err, oneLine(out, 2000))
	}
	// What a command prints is text from outside, though a typed skill's
	// text goes to the model as the person's own prompt: a <gnosh> tag in
	// it must not read as gnosh speaking (the skills review of 2026-11-19).
	return prompt.Untrusted(out), nil
}

// runInjected runs one command and returns what it printed, stderr in
// with stdout, and its exit code. What it prints is kept in a file as it
// comes, up to injectSpoolMost, and only its start in memory; the command
// runs in a process group of its own, which a timeout stops whole. (It
// was all held in memory for up to two minutes, and a timeout stopped the
// shell alone: the skills review of 2026-11-19.)
func (l *live) runInjected(command, dir string) (string, int, error) {
	ctx, cancel := context.WithTimeout(l.ctx, injectTime)
	defer cancel()
	shell := "/bin/sh"
	if st, err := os.Stat("/bin/bash"); err == nil && !st.IsDir() {
		shell = "/bin/bash"
	}
	cmd := exec.CommandContext(ctx, shell, "-c", command)
	cmd.Dir = dir
	// As the Bash tool's commands: what the person's env setting takes
	// away is taken away, and what changes how a shell reads its line
	// (BASH_ENV, SHELLOPTS, PS4, exported functions) is left out. Appended
	// to the whole environment, a taken-away variable stayed (the config
	// review of 2026-11-19).
	cmd.Env = l.p.childEnv(shelltool.CommandEnv(l.p.o.Environ, dir, l.cfg.CommandEnv()))
	cmd.WaitDelay = 2 * time.Second
	groupOf(cmd)
	out := &injectOut{stop: cancel}
	path := filepath.Join(l.spoolDir(), "skill-command-"+newCallID()+".txt")
	if os.MkdirAll(filepath.Dir(path), 0o700) == nil {
		out.f, _ = os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	}
	kept := false
	defer func() {
		if out.f != nil {
			out.f.Close()
			if !kept {
				os.Remove(path)
			}
		}
	}()
	cmd.Stdout, cmd.Stderr = out, out
	err := cmd.Run()
	head := strings.ToValidUTF8(string(out.head[:min(len(out.head), injectMost)]), "")
	code := 0
	var exit *exec.ExitError
	switch {
	case out.over:
		return head, -1, fmt.Errorf("it printed more than %d MiB and was stopped", injectSpoolMost>>20)
	case ctx.Err() != nil:
		return head, -1, fmt.Errorf("it did not finish within %s and was stopped", injectTime)
	case errors.As(err, &exit):
		code, err = exit.ExitCode(), nil
	}
	if err != nil {
		return head, -1, err
	}
	if out.n <= int64(len(out.head)) {
		if text := strings.TrimRight(string(out.head), "\n"); len(text) <= injectMost {
			return text, code, nil
		}
	}
	// All of it is in the file, and its start stands here.
	if out.f != nil && out.f.Close() == nil {
		out.f, kept = nil, true
		return fmt.Sprintf("[The command printed %d bytes; all of it is in %s. It begins:]\n%s", out.n, path, strings.ToValidUTF8(string(out.head[:injectPreview]), "")), code, nil
	}
	return head + "\n[The rest of what the command printed was cut.]", code, nil
}

// injectOut keeps what a skill's command prints: its start in memory, and
// all of it in a file (f, when one could be made), up to injectSpoolMost
// bytes, past which stop ends the command.
type injectOut struct {
	mu   sync.Mutex
	head []byte // the first injectMost+1 bytes
	f    *os.File
	n    int64
	over bool
	stop context.CancelFunc
}

func (o *injectOut) Write(p []byte) (int, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	given := len(p)
	if o.over {
		return given, nil
	}
	if room := injectSpoolMost - o.n; int64(len(p)) > room {
		p, o.over = p[:room], true
		o.stop()
	}
	o.n += int64(len(p))
	if keep := injectMost + 1 - len(o.head); keep > 0 {
		o.head = append(o.head, p[:min(keep, len(p))]...)
	}
	if o.f != nil {
		if _, err := o.f.Write(p); err != nil {
			o.f.Close()
			os.Remove(o.f.Name())
			o.f = nil
		}
	}
	return given, nil
}
