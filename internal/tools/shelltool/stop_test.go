package shelltool

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/gnosh-ai/gnosh/internal/core"
)

// tree is a command that starts a child and a grandchild, says who they
// are, and then waits for a very long time at all three levels.
const tree = `echo "parent=$$"
sh -c 'echo "child=$$"; sh -c "echo grandchild=\$\$; echo ready; exec sleep 300" & wait' &
wait`

func TestTimeoutStopsTheWholeTree(t *testing.T) {
	t.Parallel()
	m := newManager(t, Options{})
	env := newEnv(t)
	start := time.Now()
	res := run(t, t.Context(), m, BashName, env, args{argCommand: tree, argTimeout: 700})
	took := time.Since(start)

	if !res.IsError {
		t.Error("a command that timed out is not marked as an error")
	}
	headline, body, _ := parts(res)
	if want := "timed out after 0.7 s and was stopped; the output so far is below"; !strings.HasPrefix(headline, want) {
		t.Errorf("headline = %q, want it to start %q", headline, want)
	}
	if !strings.Contains(headline, "milliseconds") {
		t.Errorf("headline %q does not remind that so short a timeout is in milliseconds", headline)
	}
	pids := pidsIn(body)
	if len(pids) != 3 || !strings.Contains(body, "ready") {
		t.Fatalf("the output so far is not in the result: %q", body)
	}
	allGone(t, pids)
	if got := res.Meta[metaReason]; got != ReasonTimeout {
		t.Errorf("reason = %v, want %q", got, ReasonTimeout)
	}
	if got := res.Meta[metaTimedOutAfter]; got != int64(700) {
		t.Errorf("timedOutAfterMs = %v, want 700", got)
	}
	if res.Cwd != "" {
		t.Errorf("Cwd = %q after a timeout, want none", res.Cwd)
	}
	// SIGTERM is enough for sleep, so the grace is not used up.
	if took > 700*time.Millisecond+m.opt.TermGrace+3*time.Second {
		t.Errorf("stopping took %v, far longer than the timeout and the grace", took)
	}
}

func TestCancelStopsTheWholeTree(t *testing.T) {
	t.Parallel()
	m := newManager(t, Options{})
	env := newEnv(t)
	live := newCollector()
	env.Progress = live.take
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	done := begin(t, ctx, m, BashName, env, args{argCommand: tree, argTimeout: 60_000})
	live.waitFor(t, "ready\n")
	cancel()
	res := <-done

	headline, body, _ := parts(res)
	if !res.IsError || !strings.HasPrefix(headline, "interrupted after ") {
		t.Errorf("headline = %q (IsError %v), want news of the interruption", headline, res.IsError)
	}
	pids := pidsIn(body)
	if len(pids) != 3 {
		t.Fatalf("the output so far is not in the result: %q", body)
	}
	allGone(t, pids)
	if res.Meta[metaReason] != ReasonCancelled || res.Meta[metaInterrupted] != true {
		t.Errorf("Meta = %v, want reason %q and interrupted", res.Meta, ReasonCancelled)
	}
	live.mu.Lock()
	defer live.mu.Unlock()
	if len(live.status) == 0 || !strings.Contains(live.status[0], "interrupted") {
		t.Errorf("the person watching was not told the command was being stopped: %q", live.status)
	}
}

func TestCancelledBeforeStart(t *testing.T) {
	t.Parallel()
	m := newManager(t, Options{})
	env := newEnv(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	res := run(t, ctx, m, BashName, env, args{argCommand: "touch started"})
	if !res.IsError || !strings.Contains(core.TextOf(res.Content), "not started") {
		t.Errorf("result = %q, want word that the command was not started", core.TextOf(res.Content))
	}
	if _, err := os.Stat(filepath.Join(env.Cwd, "started")); err == nil {
		t.Error("the command ran although its call was already cancelled")
	}
}

func TestCommandThatIgnoresSIGTERM(t *testing.T) {
	t.Parallel()
	m := newManager(t, Options{TermGrace: 300 * time.Millisecond})
	env := newEnv(t)
	// The shell ignores SIGTERM, and so do the children it starts: an
	// ignored signal stays ignored across exec.
	command := `trap '' TERM
echo "parent=$$"
sh -c 'echo "child=$$"; echo ready; exec sleep 300' &
wait
echo "parent woke up"
sleep 300`
	start := time.Now()
	res := run(t, t.Context(), m, BashName, env, args{argCommand: command, argTimeout: 500})
	took := time.Since(start)

	headline, body, _ := parts(res)
	if !strings.HasPrefix(headline, "timed out after 0.5 s") {
		t.Errorf("headline = %q", headline)
	}
	pids := pidsIn(body)
	if len(pids) != 2 {
		t.Fatalf("output = %q, want two pids", body)
	}
	allGone(t, pids)
	if got := res.Meta[metaSignal]; got != "SIGKILL" {
		t.Errorf("signal = %v, want SIGKILL: only that could have ended it", got)
	}
	if took < 500*time.Millisecond+300*time.Millisecond {
		t.Errorf("it took %v, less than the timeout and the grace: was SIGTERM skipped?", took)
	}
}

// TestStopComesAtAnyMoment stops commands in their very first moments,
// while the shell is still starting up and forking. A signal sent then can
// miss a child that is forked an instant later, or be forgotten by a child
// that is just becoming the program it was forked to run; either way the
// shell then waits for a child that will not end. Such a child must be
// found and stopped, not left for the SIGKILL that comes when the grace is
// over.
func TestStopComesAtAnyMoment(t *testing.T) {
	t.Parallel()
	m := newManager(t, Options{TermGrace: 5 * time.Second})
	env := newEnv(t)
	for i := range 120 {
		start := time.Now()
		// A different delay each time, to land on different moments.
		res := run(t, t.Context(), m, BashName, env, args{argCommand: "sleep 300", argTimeout: 1 + i%4})
		took := time.Since(start)
		if sig := res.Meta[metaSignal]; sig == "SIGKILL" || took > 3*time.Second {
			t.Fatalf("stop %d took %v and the shell ended on %v: something sat out the grace", i, took, sig)
		}
		if headline, _, _ := parts(res); !strings.HasPrefix(headline, "timed out") {
			t.Fatalf("stop %d: headline %q", i, headline)
		}
	}
}

func TestProcessesThatLeaveTheGroup(t *testing.T) {
	t.Parallel()
	m := newManager(t, Options{})
	env := newEnv(t)
	live := newCollector()
	env.Progress = live.take
	// With job control on, the shell puts each background job in a process
	// group of its own, as timeout(1) does with itself. A signal to the
	// shell's group alone would miss them.
	command := `echo "parent=$$"
set -m
sleep 300 &
echo "job=$!"
set +m
echo ready
sleep 300`
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := begin(t, ctx, m, BashName, env, args{argCommand: command, argTimeout: 60_000})
	live.waitFor(t, "ready\n")
	pids := pidsIn(live.text())
	if len(pids) != 2 {
		t.Fatalf("live output = %q, want two pids", live.text())
	}

	// While it runs: the shell leads a session of its own, and the job has
	// left the shell's group but not its session.
	if procs, err := sessionProcs(pids["parent"]); err == nil {
		groups := make(map[int]int)
		for _, p := range procs {
			groups[p.pid] = p.pgid
		}
		if groups[pids["parent"]] != pids["parent"] {
			t.Errorf("the shell is not the leader of its own session and group: %v", procs)
		}
		if g, ok := groups[pids["job"]]; !ok || g == pids["parent"] {
			t.Errorf("the job should be in the shell's session but in a group of its own: %v", procs)
		}
	} else if !errors.Is(err, errNoProcList) {
		t.Errorf("listing the session: %v", err)
	}

	cancel()
	res := <-done
	if !res.IsError {
		t.Error("an interrupted command is not marked as an error")
	}
	allGone(t, pids)
}

func TestTimeoutProgramIsStoppedToo(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("timeout"); err != nil {
		t.Skip("this system has no timeout program")
	}
	m := newManager(t, Options{})
	env := newEnv(t)
	// timeout(1) moves itself, and so the command it runs, into a group of
	// its own. If only the shell's group were signalled, both would run on
	// for five minutes after the tool had said the command was stopped.
	command := `echo "parent=$$"
timeout 300 sh -c 'echo "inner=$$"; echo ready; exec sleep 300'`
	res := run(t, t.Context(), m, BashName, env, args{argCommand: command, argTimeout: 700})
	_, body, _ := parts(res)
	pids := pidsIn(body)
	if len(pids) != 2 {
		t.Fatalf("output = %q, want two pids", body)
	}
	allGone(t, pids)
	// The timeout program itself printed no pid; the session tells.
	eventually(t, "the session to be empty", func() bool {
		procs, err := sessionProcs(pids["parent"])
		return err != nil || len(procs) == 0
	})
}

func TestProcessLeftRunning(t *testing.T) {
	t.Parallel()
	m := newManager(t, Options{})
	env := newEnv(t)
	start := time.Now()
	res := run(t, t.Context(), m, BashName, env, args{argCommand: `sleep 300 & echo "left=$!"; echo "parent=$$"`, argTimeout: 60_000})
	took := time.Since(start)

	if took > 10*time.Second {
		t.Errorf("the tool waited %v for a process the command left behind", took)
	}
	if res.IsError || exitCode(t, res) != 0 {
		t.Errorf("the command itself succeeded, but the result says IsError %v, exit code %d", res.IsError, exitCode(t, res))
	}
	pids := pidsIn(printed(res))
	if len(pids) != 2 {
		t.Fatalf("output = %q, want two pids", printed(res))
	}
	if !alive(pids["left"]) {
		t.Fatal("the process the command left behind was killed; it should have been left running")
	}
	// The shell was the leader of its group, so the group's id is its pid.
	group := pids["parent"]
	if !hasNote(res, "left a process running") || !hasNote(res, "process group "+itoa(group)) || !hasNote(res, "kill -- -"+itoa(group)) {
		_, _, notes := parts(res)
		t.Errorf("notes = %q; want news of the process left running, its group %d, and how to stop it", notes, group)
	}
	if got, ok := res.Meta[metaLeftRunning].([]int); !ok || len(got) != 1 || got[0] != group {
		t.Errorf("leftRunning = %v, want [%d]", res.Meta[metaLeftRunning], group)
	}
	file, _ := res.Meta[metaOutputFile].(string)
	if _, err := os.Stat(file); err != nil {
		t.Errorf("the output file the note names is not there: %v", err)
	}

	// It still holds the command's output, so closing the manager stops it.
	if err := m.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
	allGone(t, map[string]int{"left": pids["left"]})
}

func TestLeftProcessKeepsWriting(t *testing.T) {
	t.Parallel()
	m := newManager(t, Options{})
	env := newEnv(t)
	// The process left behind goes on printing. It must neither block on a
	// pipe nobody reads nor be killed by one that was closed: what it
	// prints goes to the output file.
	command := `(while [ ! -e stop ]; do sleep 0.02; done; echo late; echo finished > finished) & echo early`
	res := run(t, t.Context(), m, BashName, env, args{argCommand: command, argTimeout: 60_000})
	if got := printed(res); got != "early" {
		t.Errorf("output = %q, want only what was printed before the command ended", got)
	}
	file, _ := res.Meta[metaOutputFile].(string)
	if file == "" {
		t.Fatalf("the result names no output file: %v", res.Meta)
	}
	if err := os.WriteFile(filepath.Join(env.Cwd, "stop"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the process left behind to finish", func() bool {
		_, err := os.Stat(filepath.Join(env.Cwd, "finished"))
		return err == nil
	})
	eventually(t, "its late output to reach the output file", func() bool {
		data, _ := os.ReadFile(file)
		return string(data) == "early\nlate\n"
	})
	// Once it is gone the manager has nothing left to remember.
	eventually(t, "the manager to forget the command", func() bool {
		m.mu.Lock()
		defer m.mu.Unlock()
		return len(m.lingering) == 0 && len(m.running) == 0
	})
}

func TestTooManyProcessesLeftRunning(t *testing.T) {
	t.Parallel()
	m := newManager(t, Options{})
	m.maxLingering = 2
	env := newEnv(t)
	// Every process left running costs the manager a goroutine and two
	// open files for as long as it lives, so past a number of them the
	// next one is stopped instead, and the model is told why.
	var kept []int
	for range 2 {
		res := run(t, t.Context(), m, BashName, env, args{argCommand: `sleep 300 & echo "left=$!"`, argTimeout: 60_000})
		if !hasNote(res, "It will be stopped when the session ends") {
			t.Fatalf("result = %s", core.TextOf(res.Content))
		}
		kept = append(kept, pidsIn(printed(res))["left"])
	}
	res := run(t, t.Context(), m, BashName, env, args{argCommand: `sleep 300 & echo "left=$!"`, argTimeout: 60_000})
	if res.IsError || !hasNote(res, "Too many processes left like that are already being listened to, so this one was stopped") || !hasNote(res, argBackground) {
		t.Errorf("result = %s", core.TextOf(res.Content))
	}
	if _, ok := res.Meta[metaLeftRunning]; ok {
		t.Errorf("Meta lists a process as left running that was stopped: %v", res.Meta)
	}
	allGone(t, pidsIn(printed(res)))
	for _, pid := range kept {
		if !alive(pid) {
			t.Errorf("an earlier process left running (pid %d) was stopped too", pid)
		}
	}
}

func TestDaemonThatLetsGoIsReported(t *testing.T) {
	t.Parallel()
	m := newManager(t, Options{})
	env := newEnv(t)
	// A process that does not hold the command's output does not hold the
	// call up: the command ends cleanly, and is no failure. It goes on
	// running, and the result says so and how to stop it (Close stops it:
	// TestLetGoIsStoppedAtClose).
	res := run(t, t.Context(), m, BashName, env, args{argCommand: `sleep 300 >/dev/null 2>&1 & echo "daemon=$!"`, argTimeout: 60_000})
	_, body, notes := parts(res)
	pids := pidsIn(body)
	if res.IsError || len(pids) != 1 || len(notes) != 1 || !strings.Contains(notes[0], "let go of its output") || !strings.Contains(notes[0], "kill -- -") {
		t.Fatalf("result = %q with notes %q; want a clean end that says what runs on", body, notes)
	}
	if !alive(pids["daemon"]) {
		t.Error("a process that let go of the output was killed")
	}
	syscall.Kill(pids["daemon"], syscall.SIGKILL)
	allGone(t, pids)
}

func TestProcessThatLeftTheSession(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("setsid"); err != nil {
		t.Skip("this system has no setsid program")
	}
	m := newManager(t, Options{})
	env := newEnv(t)
	// A process in a session of its own is out of reach, and means to be.
	// It still holds the command's output, though, and the call must not
	// wait for it once the command itself has been stopped.
	command := `setsid sleep 300 &
echo "escaped=$!"
echo "parent=$$"
sleep 300`
	start := time.Now()
	res := run(t, t.Context(), m, BashName, env, args{argCommand: command, argTimeout: 500})
	took := time.Since(start)
	headline, body, _ := parts(res)
	pids := pidsIn(body)
	if !strings.HasPrefix(headline, "timed out") || len(pids) != 2 {
		t.Fatalf("headline %q, output %q", headline, body)
	}
	defer syscall.Kill(pids["escaped"], syscall.SIGKILL)
	if took > 10*time.Second {
		t.Errorf("the call took %v: it waited for a process it could not stop", took)
	}
	allGone(t, map[string]int{"parent": pids["parent"]})
	if !alive(pids["escaped"]) {
		t.Error("the process in a session of its own was killed; nothing here should have reached it")
	}
	if !hasNote(res, "had moved to a session of its own") {
		_, _, notes := parts(res)
		t.Errorf("notes = %q, want word that a process got away", notes)
	}
	// The manager no longer holds the pipe, or anything else of the command.
	eventually(t, "the manager to forget the command", func() bool {
		m.mu.Lock()
		defer m.mu.Unlock()
		return len(m.running) == 0 && len(m.lingering) == 0
	})
	syscall.Kill(pids["escaped"], syscall.SIGKILL)
	allGone(t, map[string]int{"escaped": pids["escaped"]})
}

func TestStoppedProcessIsWokenToDie(t *testing.T) {
	t.Parallel()
	m := newManager(t, Options{TermGrace: 5 * time.Second})
	env := newEnv(t)
	// A process that has been stopped (as by Ctrl-Z) cannot act on a
	// SIGTERM until it is continued. Without the SIGCONT it would sit out
	// the whole grace and then be killed.
	start := time.Now()
	res := run(t, t.Context(), m, BashName, env, args{argCommand: `echo "parent=$$"; kill -STOP $$; echo "never mind"`, argTimeout: 300})
	took := time.Since(start)
	pids := pidsIn(printed(res))
	if len(pids) != 1 {
		t.Fatalf("output = %q", printed(res))
	}
	allGone(t, pids)
	if got := res.Meta[metaSignal]; got != "SIGTERM" {
		t.Errorf("the shell ended on %v, want SIGTERM: it should not have needed SIGKILL", got)
	}
	if took > 4*time.Second {
		t.Errorf("stopping a stopped process took %v, as if it had not been woken", took)
	}
}

func TestCommandThatHandlesSIGTERM(t *testing.T) {
	t.Parallel()
	m := newManager(t, Options{TermGrace: 5 * time.Second})
	env := newEnv(t)
	live := newCollector()
	env.Progress = live.take
	// A command that cleans up when asked to stop gets the time to, and
	// what it says on the way out is part of the result.
	command := `trap 'echo "cleaning up"; exit 0' TERM
echo "parent=$$"
sleep 300 &
echo "child=$!"
echo ready
wait`
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := begin(t, ctx, m, BashName, env, args{argCommand: command, argTimeout: 60_000})
	live.waitFor(t, "ready\n")
	start := time.Now()
	cancel()
	res := <-done
	headline, body, notes := parts(res)
	if !res.IsError || !strings.HasPrefix(headline, "interrupted after ") || !strings.HasSuffix(body, "\nready\ncleaning up") {
		t.Errorf("headline %q, output %q", headline, body)
	}
	// It exited with code 0 of its own accord, but it was stopped: there is
	// no exit code to complain about, and no signal either.
	if code := exitCode(t, res); code != 0 || len(notes) != 0 || res.Meta[metaSignal] != nil {
		t.Errorf("exit code %d, notes %q, Meta %v", code, notes, res.Meta)
	}
	if took := time.Since(start); took > 4*time.Second {
		t.Errorf("a command that exits when asked took %v to stop", took)
	}
	allGone(t, pidsIn(body))
}

func TestOutputAfterTheShellExits(t *testing.T) {
	t.Parallel()
	// The grace is long here so that the test does not depend on timing:
	// a process that finishes soon after the shell is simply waited for.
	m := newManager(t, Options{PipeGrace: 10 * time.Second})
	env := newEnv(t)
	start := time.Now()
	res := bash(t, m, env, `(sleep 0.1; echo late) & echo early`)
	if _, body, notes := parts(res); body != "early\nlate" || len(notes) != 0 || res.IsError {
		t.Errorf("output %q, notes %q", body, notes)
	}
	if took := time.Since(start); took > 8*time.Second {
		t.Errorf("the call took %v: it sat out the grace although the output had ended", took)
	}
}

func TestSlowReaderIsNotTakenForAHeldPipe(t *testing.T) {
	t.Parallel()
	// On a busy machine the manager may be slow to read out what a command
	// printed: slower than the grace it gives a process that holds the
	// output open. That must not be mistaken for such a process, or the
	// output would be cut short and a process reported that is not there.
	m := newManager(t, Options{PipeGrace: 10 * time.Millisecond})
	m.pumpDelay = 300 * time.Millisecond
	env := newEnv(t)
	res := bash(t, m, env, "echo all of it; exit 3")
	_, body, notes := parts(res)
	if body != "all of it" || len(notes) != 1 || notes[0] != "exit code 3" {
		t.Errorf("output %q, notes %q", body, notes)
	}
	// Stopped commands likewise: the output so far is all there.
	res = run(t, t.Context(), m, BashName, env, args{argCommand: "echo so far; sleep 300", argTimeout: 100})
	if headline, body, notes := parts(res); !strings.HasPrefix(headline, "timed out") || body != "so far" || len(notes) != 0 {
		t.Errorf("headline %q, output %q, notes %q", headline, body, notes)
	}
	// And a process that does hold the output is still found.
	res = run(t, t.Context(), m, BashName, env, args{argCommand: `sleep 300 & echo "left=$!"`, argTimeout: 60_000})
	if !hasNote(res, "left a process running") || len(pidsIn(printed(res))) != 1 {
		t.Errorf("result = %s", core.TextOf(res.Content))
	}
}

func TestCommandThatClosesItsOutput(t *testing.T) {
	t.Parallel()
	m := newManager(t, Options{})
	env := newEnv(t)
	// The output ends before the command does. The call still waits for the
	// command, and still learns its exit code and where it went.
	if err := os.Mkdir(filepath.Join(env.Cwd, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	res := bash(t, m, env, `echo before; exec >&- 2>&-; while [ ! -e sub/go ]; do sleep 0.02; touch sub/go; done; cd sub; exit 3`)
	if got := printed(res); got != "before" {
		t.Errorf("output = %q", got)
	}
	if code := exitCode(t, res); code != 3 || res.Cwd != filepath.Join(env.Cwd, "sub") {
		t.Errorf("exit code %d, Cwd %q", code, res.Cwd)
	}
}

func TestOutputLimitStopsTheCommand(t *testing.T) {
	t.Parallel()
	m := newManager(t, Options{MaxOutputBytes: 1 << 20, MaxSpoolBytes: 1 << 16})
	env := newEnv(t)
	res := run(t, t.Context(), m, BashName, env, args{argCommand: `echo "parent=$$"; yes "line of output"`, argTimeout: 60_000})
	headline, body, _ := parts(res)
	if !res.IsError || !strings.HasPrefix(headline, "stopped after printing more than 1 MiB") {
		t.Errorf("headline = %q (IsError %v)", headline, res.IsError)
	}
	if res.Meta[metaReason] != ReasonOutputLimit {
		t.Errorf("reason = %v, want %q", res.Meta[metaReason], ReasonOutputLimit)
	}
	if total, _ := res.Meta[metaOutputBytes].(int64); total <= 1<<20 {
		t.Errorf("outputBytes = %d, want more than the limit", total)
	}
	allGone(t, pidsIn(body))
}

func TestProcessThatCannotBeKilled(t *testing.T) {
	t.Parallel()
	m := newManager(t, Options{TermGrace: 100 * time.Millisecond, KillWait: 200 * time.Millisecond})
	env := newEnv(t)
	// Signals that go nowhere stand in for a process the kernel will not
	// let die. The tool must come back and say so, not wait for ever.
	var mu sync.Mutex
	deaf := true
	m.signal = func(pid int, sig syscall.Signal) error {
		mu.Lock()
		defer mu.Unlock()
		if deaf && sig != 0 {
			return nil
		}
		return syscall.Kill(pid, sig)
	}
	start := time.Now()
	res := run(t, t.Context(), m, BashName, env, args{argCommand: `echo "parent=$$"; exec sleep 300`, argTimeout: 300})
	took := time.Since(start)

	pids := pidsIn(printed(res))
	if len(pids) != 1 {
		t.Fatalf("output = %q", printed(res))
	}
	if took > 10*time.Second {
		t.Errorf("the tool took %v to give up on a process that would not die", took)
	}
	if !res.IsError || !hasNote(res, "could not stop: pid "+itoa(pids["parent"])) {
		_, _, notes := parts(res)
		t.Errorf("notes = %q, want word that pid %d could not be stopped", notes, pids["parent"])
	}
	if code := exitCode(t, res); code != -1 {
		t.Errorf("exit code = %d, want -1 for a shell that never ended", code)
	}

	// Now let signals through again, and see it off.
	mu.Lock()
	deaf = false
	mu.Unlock()
	syscall.Kill(-pids["parent"], syscall.SIGKILL)
	allGone(t, pids)
}

func TestStoppingWithoutAProcessList(t *testing.T) {
	t.Parallel()
	m := newManager(t, Options{})
	// As on a system where a session's processes cannot be listed: only
	// the process group is left to go by.
	m.procs = func(int) ([]procInfo, error) { return nil, errNoProcList }
	env := newEnv(t)

	res := run(t, t.Context(), m, BashName, env, args{argCommand: tree, argTimeout: 700})
	headline, body, _ := parts(res)
	if !strings.HasPrefix(headline, "timed out") {
		t.Errorf("headline = %q", headline)
	}
	allGone(t, pidsIn(body))

	res = run(t, t.Context(), m, BashName, env, args{argCommand: `sleep 300 & echo "left=$!"; echo "parent=$$"`, argTimeout: 60_000})
	pids := pidsIn(printed(res))
	if len(pids) != 2 || !hasNote(res, "process group "+itoa(pids["parent"])) {
		_, _, notes := parts(res)
		t.Fatalf("notes = %q; want the group of the process left running, %v", notes, pids)
	}
	// Without a process list the manager cannot be sure the group is still
	// the command's when it closes, so it only closes the pipe. The
	// process is this test's to stop.
	syscall.Kill(-pids["parent"], syscall.SIGKILL)
	allGone(t, map[string]int{"left": pids["left"]})
}

// TestPlainShell runs the paths that depend on how the shell behaves with
// sh, which on many systems is a much smaller shell than bash: it does not
// run its exit trap when it is terminated, for one.
func TestPlainShell(t *testing.T) {
	t.Parallel()
	news := newEnds()
	m := newManager(t, Options{Shell: "/bin/sh", OnEnd: news.take})
	env := newEnv(t)
	if got := toolNamed(t, m, BashName).Spec().Description; !strings.Contains(got, "A fresh sh runs it") {
		t.Errorf("the description does not name the shell in use:\n%.200s", got)
	}

	// A timeout stops the whole tree.
	res := run(t, t.Context(), m, BashName, env, args{argCommand: tree, argTimeout: 700})
	headline, body, _ := parts(res)
	if !strings.HasPrefix(headline, "timed out after 0.7 s") || len(pidsIn(body)) != 3 {
		t.Errorf("headline %q, output %q", headline, body)
	}
	allGone(t, pidsIn(body))

	// A process left running is reported and the call comes back.
	res = run(t, t.Context(), m, BashName, env, args{argCommand: `sleep 300 & echo "left=$!"; echo "parent=$$"; exit 4`, argTimeout: 60_000})
	pids := pidsIn(printed(res))
	if exitCode(t, res) != 4 || !hasNote(res, "process group "+itoa(pids["parent"])) {
		_, _, notes := parts(res)
		t.Errorf("exit code %d, notes %q", exitCode(t, res), notes)
	}

	// A background command, read and stopped.
	id, _ := startBackground(t, m, env, `echo "bg=$$"; echo ready; sleep 300`)
	bg := pidsIn(readUntil(t, m, env, id, "ready\n"))
	res = run(t, t.Context(), m, StopName, env, args{argTaskID: id})
	if text := core.TextOf(res.Content); !strings.HasPrefix(text, "Stopped "+id) {
		t.Errorf("stop result = %q", text)
	}
	allGone(t, bg)

	// One that ends by itself is announced.
	id, _ = startBackground(t, m, env, "echo over; exit 6")
	if ended := news.next(t); ended.ID != id || ended.ExitCode != 6 || ended.Tail != "over\n" {
		t.Errorf("news = %+v, tail %q", ended.Task, ended.Tail)
	}

	if err := m.Close(); err != nil {
		t.Error(err)
	}
	allGone(t, map[string]int{"left": pids["left"]})
}

func TestSessionProcs(t *testing.T) {
	t.Parallel()
	cmd := exec.Command("sh", "-c", "sleep 300 & sleep 300 & wait")
	cmd.Env = append(os.Environ(), mark)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	sid := cmd.Process.Pid
	defer func() {
		syscall.Kill(-sid, syscall.SIGKILL)
		cmd.Wait()
	}()
	var procs []procInfo
	var err error
	names := make(map[string]int)
	// A child is a copy of the shell for a moment before it becomes sleep,
	// and is listed as such if looked at just then.
	eventually(t, "the shell and its two sleeps to show up in the session", func() bool {
		procs, err = sessionProcs(sid)
		clear(names)
		for _, p := range procs {
			names[p.name]++
		}
		return err != nil || (len(procs) == 3 && names["sleep"] == 2)
	})
	if errors.Is(err, errNoProcList) {
		t.Skip("this system cannot list a session's processes")
	}
	if err != nil {
		t.Fatal(err)
	}
	images := make(map[string]bool)
	for _, p := range procs {
		images[p.image] = true
		if p.pgid != sid {
			t.Errorf("%+v is not in the shell's process group %d", p, sid)
		}
		if p.start == 0 {
			t.Errorf("%+v has no start time", p)
		}
	}
	if len(names) != 2 {
		t.Errorf("processes = %+v, want a shell and two sleeps", procs)
	}
	// The shell and a sleep are different programs, and are told apart.
	// (The two sleeps are told apart too wherever the kernel puts each
	// program's stack at an address of its own, which is nearly everywhere.)
	if len(images) < 2 {
		t.Errorf("processes = %+v, want the programs told apart", procs)
	}
	if !sameProcess(procs, procs[:1]) || sameProcess(procs, []procInfo{{pid: procs[0].pid, start: procs[0].start + 1}}) {
		t.Error("sameProcess does not go by pid and start time")
	}

	// A session with nothing in it, and one that never was.
	if procs, err := sessionProcs(1 << 30); err != nil || len(procs) != 0 {
		t.Errorf("an unused session id gave %v, %v", procs, err)
	}
}

func itoa(n int) string { return strconv.Itoa(n) }
