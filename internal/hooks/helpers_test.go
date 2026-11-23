package hooks

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// marker is a variable every hook started by these tests carries in its
// environment, with a value no other process has. TestMain looks for it at
// the end to prove that no test left a process behind.
var marker = fmt.Sprintf("GNOSH_HOOKS_TEST=%d-%d", os.Getpid(), time.Now().UnixNano())

// testPath is where test hooks find sh, cat, sleep and the rest, on Linux
// and on macOS.
const testPath = "/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin"

// subreaperEnv, when set, makes the test binary inherit the processes its
// hooks orphan and never collect them (see becomeSubreaper).
const subreaperEnv = "GNOSH_HOOKS_TEST_SUBREAPER"

// ignoreChildrenEnv, when set, makes the test binary ignore the signal
// that says a child has ended. The system then collects its children
// itself and will not tell it how any of them ended.
const ignoreChildrenEnv = "GNOSH_HOOKS_TEST_IGNORE_SIGCHLD"

func TestMain(m *testing.M) {
	if os.Getenv(ignoreChildrenEnv) != "" {
		signal.Ignore(syscall.SIGCHLD)
	}
	if os.Getenv(subreaperEnv) != "" {
		if err := becomeSubreaper(); err != nil {
			fmt.Fprintf(os.Stderr, "FAIL: %v\n", err)
			os.Exit(1)
		}
	}
	code := m.Run()
	if left := leftovers(); len(left) > 0 {
		fmt.Fprintf(os.Stderr, "FAIL: the tests left processes running: %v\n", left)
		for _, pid := range left {
			syscall.Kill(pid, syscall.SIGKILL) // ours: they carry the marker
		}
		code = 1
	}
	if stacks := lingering(); stacks != "" {
		fmt.Fprintf(os.Stderr, "FAIL: goroutines of the hooks package outlived the tests:\n\n%s\n", stacks)
		code = 1
	}
	os.Exit(code)
}

// leftovers returns the ids of the live processes that carry the marker.
// It can only look where the system lists every process's environment
// (Linux); elsewhere the tests' own checks by process id stand alone.
func leftovers() []int {
	var found []int
	deadline := time.Now().Add(3 * time.Second)
	for {
		found = found[:0]
		entries, err := os.ReadDir("/proc")
		if err != nil {
			return nil
		}
		for _, e := range entries {
			pid, err := strconv.Atoi(e.Name())
			if err != nil || pid == os.Getpid() {
				continue
			}
			env, err := os.ReadFile(filepath.Join("/proc", e.Name(), "environ"))
			if err == nil && bytes.Contains(env, []byte(marker)) && running(pid) {
				found = append(found, pid)
			}
		}
		if len(found) == 0 || time.Now().After(deadline) {
			return found
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// lingering returns the stacks of the goroutines that Runner.exec started
// and that are still there, after giving them a moment to finish.
func lingering() string {
	deadline := time.Now().Add(3 * time.Second)
	for {
		buf := make([]byte, 1<<20)
		buf = buf[:runtime.Stack(buf, true)]
		var left []string
		for _, g := range strings.Split(string(buf), "\n\n") {
			if strings.Contains(g, "hooks.(*Runner).exec") {
				left = append(left, g)
			}
		}
		if len(left) == 0 {
			return ""
		}
		if time.Now().After(deadline) {
			return strings.Join(left, "\n\n")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// options returns Options for a test: every event fires, the environment
// holds only what a hook needs to find its programs, and the waits are
// short.
func options(t *testing.T) Options {
	t.Helper()
	dir := t.TempDir()
	return Options{
		Fires:      Events(),
		ProjectDir: dir,
		StartDir:   dir,
		Shell:      "/bin/sh",
		Environ:    func() []string { return []string{"PATH=" + testPath, "HOME=" + dir, marker} },
		KillGrace:  50 * time.Millisecond,
		PipeWait:   5 * time.Second,
	}
}

// newRunner makes a Runner for the hooks, with the test's options.
func newRunner(t *testing.T, opts Options, hooks ...Hook) *Runner {
	t.Helper()
	r, err := NewRunner(Config{Hooks: hooks}, opts)
	if err != nil {
		t.Fatalf("NewRunner: %v", err)
	}
	return r
}

// run runs the event's hooks and fails the test if Run returns an error.
func run(t *testing.T, r *Runner, in Input) Outcome {
	t.Helper()
	out, err := r.Run(context.Background(), in)
	if err != nil {
		t.Fatalf("Run(%s): %v", in.Event, err)
	}
	return out
}

// one makes a Runner with a single hook on the event, running command,
// and runs the event.
func one(t *testing.T, event Event, command string) Outcome {
	t.Helper()
	r := newRunner(t, options(t), Hook{Event: event, Command: command})
	return run(t, r, input(event))
}

// input returns an Input for the event with every field its hooks are
// matched against filled in.
func input(event Event) Input {
	in := Input{Event: event, SessionID: "session-1", TranscriptPath: "/transcripts/session-1.jsonl"}
	switch event {
	case PreToolUse, PostToolUse, PostToolUseFailure, PermissionRequest:
		in.ToolName = "Bash"
		in.ToolUseID = "toolu_1"
		in.ToolInput = json.RawMessage(`{"command":"ls"}`)
	case SessionStart:
		in.Source = "startup"
	case SessionEnd:
		in.Reason = "other"
	case PreCompact, PostCompact:
		in.Trigger = "manual"
	case StopFailure:
		in.Error = "server_error"
	case InstructionsLoaded:
		in.LoadReason = "session_start"
	case Notification:
		in.NotificationType = "idle_prompt"
	case PreModelSwitch, PostModelSwitch:
		in.FromModel, in.ToModel, in.RequestedModel, in.Source = "local/a", "local/b", "local/b", "command"
	case DirectoryAdded:
		in.Directory, in.Source = "/project/docs", "slash_command"
	case CwdChanged:
		in.OldCwd, in.NewCwd = "/project", "/project/sub"
	case UserPromptExpansion:
		in.ExpansionType, in.CommandName, in.CommandArgs, in.CommandSource, in.Prompt = "slash_command", "deploy", "staging", "project", "Deploy to staging."
	case ConfigChange:
		in.Source, in.FilePath = "project_settings", "/project/gnosh.json"
	case FileChanged:
		in.FilePath, in.FileEvent = "/project/.envrc", "change"
	case SubagentStart, SubagentStop:
		in.AgentID = "agent-1"
		in.AgentType = "Explore"
	case Elicitation:
		in.MCPServerName, in.Message, in.Mode = "issue-tracker", "Which project?", "form"
		in.RequestedSchema = json.RawMessage(`{"type":"object","properties":{"project_key":{"type":"string","title":"Project key"}},"required":["project_key"]}`)
	case ElicitationResult:
		in.MCPServerName, in.Action, in.Mode = "issue-tracker", "accept", "form"
		in.Content = json.RawMessage(`{"project_key":"WEB"}`)
	}
	return in
}

// shq quotes s for a POSIX shell.
func shq(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// script writes a shell script into a temp dir and returns the command
// line that runs it. The script is run by sh rather than executed, so
// that a test never executes a file it has only just written.
func script(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "hook.sh")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return "sh " + shq(path)
}

// prints returns a command that prints text on stdout, exactly.
func prints(t *testing.T, text string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "answer")
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	return "cat " + shq(path)
}

// running reports whether a live process has the id. A process that has
// ended and only waits to be collected does not count.
func running(pid int) bool {
	if err := syscall.Kill(pid, 0); err != nil {
		return errors.Is(err, syscall.EPERM)
	}
	if stat, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid)); err == nil {
		if i := bytes.LastIndexByte(stat, ')'); i >= 0 && i+2 < len(stat) && (stat[i+2] == 'Z' || stat[i+2] == 'X') {
			return false
		}
	}
	return true
}

// gone fails the test unless the process ends within a few seconds.
func gone(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for running(pid) {
		if time.Now().After(deadline) {
			t.Fatalf("process %d is still running", pid)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// waitFor returns what is in the file once it has been written, and fails
// the test if that does not happen within a few seconds.
func waitFor(t *testing.T, path string) string {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		if data, err := os.ReadFile(path); err == nil && len(bytes.TrimSpace(data)) > 0 {
			return strings.TrimSpace(string(data))
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s was never written", path)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// pidIn reads a process id a hook wrote to a file.
func pidIn(t *testing.T, path string) int {
	t.Helper()
	pid, err := strconv.Atoi(waitFor(t, path))
	if err != nil {
		t.Fatalf("%s does not hold a process id: %v", path, err)
	}
	return pid
}

// contains fails the test unless text holds every one of the parts.
func contains(t *testing.T, what, text string, parts ...string) {
	t.Helper()
	for _, p := range parts {
		if !strings.Contains(text, p) {
			t.Errorf("%s does not mention %q:\n%s", what, p, text)
		}
	}
}

// lacks fails the test if text holds any of the parts.
func lacks(t *testing.T, what, text string, parts ...string) {
	t.Helper()
	for _, p := range parts {
		if strings.Contains(text, p) {
			t.Errorf("%s mentions %q:\n%s", what, p, text)
		}
	}
}

// problemsText joins problems, one to a line.
func problemsText(problems []Problem) string {
	lines := make([]string, len(problems))
	for i, p := range problems {
		lines[i] = p.String()
	}
	return strings.Join(lines, "\n")
}
