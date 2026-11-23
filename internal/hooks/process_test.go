package hooks

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

// family is the body of a hook that starts a child, says who both are,
// and waits. first runs before anything else; ready is the file written
// once both ids are on disk.
func family(dir, first string) string {
	return first + `
echo 'rendering the video' >&2
sleep 600 &
echo $! > ` + shq(filepath.Join(dir, "child")) + `
echo $$ > ` + shq(filepath.Join(dir, "self")) + `
echo yes > ` + shq(filepath.Join(dir, "ready")) + `
wait`
}

// familyWait is the timeout of a family hook: long enough for its shell to
// start its child and write their ids on a loaded machine, so that the
// hook is stopped while it waits and not before.
const familyWait = 2 * time.Second

// A hook that passes its timeout is stopped with everything it started,
// and counts as a hook that could not evaluate.
func TestTimeoutStopsTheWholeProcessGroup(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	opts := options(t)
	opts.KillGrace = 10 * time.Second // never needed: the hook dies when asked
	r := newRunner(t, opts, Hook{Event: Stop, Name: "render-check", Command: script(t, family(dir, "")), Timeout: familyWait})

	began := time.Now()
	out := run(t, r, input(Stop))
	took := time.Since(began)

	if !out.Blocked || !out.CouldNotEvaluate {
		t.Errorf("Blocked %v, CouldNotEvaluate %v; a hook that ran out of time must hold the turn", out.Blocked, out.CouldNotEvaluate)
	}
	contains(t, "the reason", out.Reason, `The Stop hook "render-check" could not check this: it did not finish within `+spell(familyWait)+` and was stopped.`, "A check that could not run does not pass.", "rendering the video")
	rec := out.Records[0]
	if rec.Result != ResultCouldNotEvaluate || rec.ExitCode != -1 || rec.Duration < familyWait {
		t.Errorf("record: Result %q, ExitCode %d, Duration %v", rec.Result, rec.ExitCode, rec.Duration)
	}
	if took > 5*time.Second {
		t.Errorf("Run took %v to stop a hook that dies when asked", took)
	}
	gone(t, pidIn(t, filepath.Join(dir, "child")))
	gone(t, pidIn(t, filepath.Join(dir, "self")))
}

// A hook that will not stop when asked, and whose child will not either,
// is killed when the grace runs out.
func TestHookThatIgnoresTheRequestToStopIsKilled(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	opts := options(t)
	opts.KillGrace = 50 * time.Millisecond
	r := newRunner(t, opts, Hook{Event: PreToolUse, Command: script(t, family(dir, "trap '' TERM")), Timeout: familyWait})

	out := run(t, r, input(PreToolUse))
	if !out.Blocked || !out.CouldNotEvaluate || out.PermissionDecision != Deny {
		t.Errorf("Blocked %v, CouldNotEvaluate %v, PermissionDecision %q", out.Blocked, out.CouldNotEvaluate, out.PermissionDecision)
	}
	contains(t, "the reason", out.Reason, "did not finish within "+spell(familyWait)+" and was stopped")
	if notes := out.Records[0].Notes; len(notes) != 0 {
		t.Errorf("the record says the hook could not be stopped: %q", notes)
	}
	child, self := pidIn(t, filepath.Join(dir, "child")), pidIn(t, filepath.Join(dir, "self"))
	// Run does not return before the group is gone.
	if running(child) || running(self) {
		t.Errorf("Run returned with the hook's processes still running (child %v, self %v)", running(child), running(self))
	}
	gone(t, child)
	gone(t, self)
}

// A hook that is asked to stop gets the chance to clean up after itself
// before anything is killed.
func TestHookIsAskedBeforeItIsKilled(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	opts := options(t)
	opts.KillGrace = 30 * time.Second // the test fails long before this if the hook is not asked first
	cleaned := filepath.Join(dir, "cleaned")
	r := newRunner(t, opts, Hook{Event: Stop, Command: script(t, family(dir, "trap 'echo yes > "+shq(cleaned)+"; exit 0' TERM")), Timeout: familyWait})

	began := time.Now()
	out := run(t, r, input(Stop))
	if !out.CouldNotEvaluate {
		t.Errorf("the hook timed out, and yet: %+v", out)
	}
	if took := time.Since(began); took > 15*time.Second {
		t.Errorf("Run took %v: it waited out the grace for a hook that had already gone", took)
	}
	if got := waitFor(t, cleaned); got != "yes" {
		t.Errorf("the hook's own clean-up wrote %q", got)
	}
	gone(t, pidIn(t, filepath.Join(dir, "child")))
	gone(t, pidIn(t, filepath.Join(dir, "self")))
}

// Cancelling the turn stops its hooks, with everything they started. That
// is not a hook's failure: no hook is reported as having blocked or
// failed, and the call is still not let through.
func TestCancelStopsTheHooks(t *testing.T) {
	t.Parallel()
	one, two := t.TempDir(), t.TempDir()
	// The clock is read once when a hook starts and once when it is done
	// with, so the fourth reading says that one of the three has finished.
	var readings atomic.Int32
	opts := options(t)
	opts.Now = func() time.Time {
		readings.Add(1)
		return time.Now()
	}
	r := newRunner(t, opts,
		Hook{Event: PreToolUse, Name: "quick", Command: "echo 'said before the cancel' >&2; exit 2"},
		Hook{Event: PreToolUse, Name: "slow", Command: script(t, family(one, ""))},
		Hook{Event: PreToolUse, Name: "slower", Command: script(t, family(two, "trap '' TERM"))},
	)
	stoppedByUser := errors.New("the user pressed Esc")
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)

	type result struct {
		out Outcome
		err error
	}
	done := make(chan result, 1)
	go func() {
		out, err := r.Run(ctx, input(PreToolUse))
		done <- result{out, err}
	}()
	waitFor(t, filepath.Join(one, "ready"))
	waitFor(t, filepath.Join(two, "ready"))
	for deadline := time.Now().Add(10 * time.Second); readings.Load() < 4; time.Sleep(2 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the quick hook never finished")
		}
	}
	select {
	case res := <-done:
		t.Fatalf("Run returned before the hooks had finished or the turn was cancelled: %+v, %v", res.out, res.err)
	default:
	}
	cancel(stoppedByUser)

	var res result
	select {
	case res = <-done:
	case <-time.After(20 * time.Second):
		t.Fatal("Run did not return after the turn was cancelled")
	}
	if !errors.Is(res.err, stoppedByUser) || !errors.Is(res.err, context.Canceled) {
		t.Errorf("Run returned %v; want an error that wraps the context's error and the reason the turn was cancelled", res.err)
	}
	contains(t, "the error", res.err.Error(), "the PreToolUse hooks were stopped before they finished")
	out := res.out
	if out.CouldNotEvaluate || len(out.Warnings) != 0 {
		t.Errorf("a cancelled turn was reported as a hook's failure: %+v", out)
	}
	if !out.Blocked || out.PermissionDecision != Deny {
		t.Errorf("the Outcome of a cancelled Run lets the call through: %+v", out)
	}
	lacks(t, "the reason", out.Reason, "said before the cancel", "could not check")
	var results []string
	for _, rec := range out.Records {
		results = append(results, rec.Name+"="+string(rec.Result))
	}
	if got := strings.Join(results, " "); got != "quick=blocked slow=cancelled slower=cancelled" {
		t.Errorf("records: %s", got)
	}
	for _, dir := range []string{one, two} {
		child, self := pidIn(t, filepath.Join(dir, "child")), pidIn(t, filepath.Join(dir, "self"))
		if running(child) || running(self) {
			t.Errorf("Run returned with a cancelled hook's processes still running")
		}
		gone(t, child)
		gone(t, self)
	}
}

// A turn that is already cancelled starts no hook.
func TestCancelledBeforeAnythingStarts(t *testing.T) {
	t.Parallel()
	started := filepath.Join(t.TempDir(), "started")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, event := range []Event{PreToolUse, UserPromptSubmit, Stop} {
		r := newRunner(t, options(t), Hook{Event: event, Command: ": > " + shq(started)})
		out, err := r.Run(ctx, input(event))
		if !errors.Is(err, context.Canceled) || len(out.Records) != 0 {
			t.Errorf("%s: Run = %+v, %v", event, out, err)
		}
		// A call or a prompt is not let through; the end of a turn the
		// user cancelled is not held up.
		if want := event != Stop; out.Blocked != want {
			t.Errorf("%s: Blocked = %v, want %v", event, out.Blocked, want)
		}
	}
	if _, err := os.Stat(started); err == nil {
		t.Error("a hook was started on a cancelled turn")
	}
}

// A hook may leave a program running, as long as that program does not
// hold the hook's output open. The program is not the hook, and is left
// alone.
func TestHookMayLeaveAProgramRunning(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	child := filepath.Join(dir, "child")
	r := newRunner(t, options(t), Hook{Event: SessionStart, Command: script(t, `sleep 600 > /dev/null 2>&1 &
echo $! > `+shq(child)+`
echo 'the dev server is starting'`)})
	out := run(t, r, input(SessionStart))
	if out.CouldNotEvaluate || len(out.Warnings) != 0 || out.AdditionalContext != "the dev server is starting" {
		t.Errorf("got %+v", out)
	}
	pid := pidIn(t, child)
	if !running(pid) {
		t.Error("the program the hook left running was stopped")
	}
	syscall.Kill(pid, syscall.SIGKILL) // this test started it
	gone(t, pid)
}

// A hook that exits while something it started still holds its output
// open may not have said everything, so its answer is not trusted.
func TestHookThatLeavesItsOutputOpenCouldNotEvaluate(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	child := filepath.Join(dir, "child")
	opts := options(t)
	opts.PipeWait = 150 * time.Millisecond
	r := newRunner(t, opts, Hook{Event: PreToolUse, Command: script(t, `sleep 600 &
echo $! > `+shq(child)+`
echo '{"hookSpecificOutput": {"permissionDecision": "allow"}}'`)})
	out := run(t, r, input(PreToolUse))
	if !out.Blocked || !out.CouldNotEvaluate || out.PermissionDecision != Deny {
		t.Errorf("an approval printed before leaving the output open was trusted: %+v", out)
	}
	contains(t, "the reason", out.Reason, "left a process running that still holds its output open", "must redirect the output")
	if rec := out.Records[0]; rec.ExitCode != 0 || rec.Result != ResultCouldNotEvaluate {
		t.Errorf("record: ExitCode %d, Result %q", rec.ExitCode, rec.Result)
	}
	pid := pidIn(t, child)
	syscall.Kill(pid, syscall.SIGKILL) // this test started it
	gone(t, pid)
}

// A hook whose own process has exited, but which left something holding
// its output, is still stopped whole when its time runs out.
func TestTimeoutAfterTheHooksOwnProcessHasExited(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	child := filepath.Join(dir, "child")
	opts := options(t)
	opts.PipeWait = time.Minute // the hook's timeout comes first
	// (Time enough for a shell to start the child and write its pid on a
	// box busy with every package's tests.)
	r := newRunner(t, opts, Hook{Event: Stop, Timeout: 2 * time.Second, Command: script(t, `sleep 600 &
echo $! > `+shq(child)+`
exit 0`)})
	began := time.Now()
	out := run(t, r, input(Stop))
	if !out.Blocked || !out.CouldNotEvaluate {
		t.Errorf("Blocked %v, CouldNotEvaluate %v", out.Blocked, out.CouldNotEvaluate)
	}
	contains(t, "the reason", out.Reason, "did not finish within 2 seconds and was stopped")
	if took := time.Since(began); took > 20*time.Second {
		t.Errorf("Run took %v", took)
	}
	pid := pidIn(t, child)
	if running(pid) {
		t.Error("Run returned with the process the hook left behind still running")
	}
	gone(t, pid)
}

// Where the system will not say how a hook ended, the hook has not
// passed. A program started with the "child ended" signal ignored is in
// that position, so the check runs in a copy of the test binary that
// ignores it.
func TestHookWhoseEndingIsNotKnownCouldNotEvaluate(t *testing.T) {
	if os.Getenv(ignoreChildrenEnv) == "" {
		t.Parallel()
		cmd := exec.Command(os.Args[0], "-test.run", "^TestHookWhoseEndingIsNotKnownCouldNotEvaluate$", "-test.count=1")
		cmd.Env = append(os.Environ(), ignoreChildrenEnv+"=1")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("in a program that is not told how its children end: %v\n%s", err, out)
		}
		return
	}
	for _, command := range []string{"exit 0", "echo 'no' >&2; exit 2", "exit 1", prints(t, `{"hookSpecificOutput": {"permissionDecision": "allow"}}`)} {
		out := one(t, PreToolUse, command)
		if !out.Blocked || !out.CouldNotEvaluate || out.PermissionDecision != Deny {
			t.Errorf("%s: a hook whose exit status nobody could read was taken at its word: %+v", command, out)
		}
		contains(t, "the reason", out.Reason, "gnosh could not tell what it answered, because the system did not say how it ended")
	}
}

// Cancelling the turn stops what a hook left behind as well, when that
// still holds the hook's output and the hook is therefore not done with.
func TestCancelAfterTheHooksOwnProcessHasExited(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	child, self := filepath.Join(dir, "child"), filepath.Join(dir, "self")
	opts := options(t)
	opts.PipeWait = time.Minute // the cancel comes first
	r := newRunner(t, opts, Hook{Event: Stop, Command: script(t, `sleep 600 &
echo $! > `+shq(child)+`
echo $$ > `+shq(self)+`
exit 0`)})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	failed := make(chan error, 1)
	go func() {
		_, err := r.Run(ctx, input(Stop))
		failed <- err
	}()
	pid := pidIn(t, child)
	gone(t, pidIn(t, self)) // the hook's own process has exited
	cancel()
	select {
	case err := <-failed:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("Run returned %v", err)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("Run did not return after the turn was cancelled")
	}
	if running(pid) {
		t.Error("Run returned with the process the hook left behind still running")
	}
	gone(t, pid)
}

// A hook that prints megabytes is not held up, does not fill memory, and
// has not given an answer that can be read.
func TestHugeOutput(t *testing.T) {
	t.Parallel()
	megabytes := func(n int, letter string) string {
		return "head -c " + strconv.Itoa(n<<20) + " /dev/zero | tr '\\0' " + letter
	}

	t.Run("on stdout", func(t *testing.T) {
		t.Parallel()
		done := filepath.Join(t.TempDir(), "done")
		opts := options(t)
		opts.MaxOutput = 64 << 10
		r := newRunner(t, opts, Hook{Event: UserPromptSubmit, Command: megabytes(8, "x") + "; : > " + shq(done)})
		out := run(t, r, input(UserPromptSubmit))
		if !out.Blocked || !out.CouldNotEvaluate || out.AdditionalContext != "" {
			t.Errorf("Blocked %v, CouldNotEvaluate %v, %d characters of context", out.Blocked, out.CouldNotEvaluate, len(out.AdditionalContext))
		}
		contains(t, "the reason", out.Reason, "it printed more than the 64 KiB gnosh reads from a hook")
		if len(out.Reason) > 1000 {
			t.Errorf("the reason is %d bytes long", len(out.Reason))
		}
		if _, err := os.Stat(done); err != nil {
			t.Error("the hook was held up by its own output and never finished")
		}
	})

	t.Run("on stderr", func(t *testing.T) {
		t.Parallel()
		r := newRunner(t, options(t), Hook{Event: Stop, Command: "echo 'FIRST: 3 tests failed' >&2; " + megabytes(8, "e") + " >&2; echo >&2; echo 'LAST: see TestRender' >&2; exit 2"})
		out := run(t, r, input(Stop))
		if !out.Blocked || out.CouldNotEvaluate {
			t.Errorf("Blocked %v, CouldNotEvaluate %v", out.Blocked, out.CouldNotEvaluate)
		}
		contains(t, "the reason", out.Reason, "FIRST: 3 tests failed", "LAST: see TestRender", "left out by gnosh")
		if n := len(out.Reason); n > 10200 {
			t.Errorf("the reason is %d bytes long; want it clipped to about 10,000", n)
		}
		if n := len(out.Records[0].Stderr); n > stderrStart+10 {
			t.Errorf("the record keeps %d bytes of stderr", n)
		}
	})

	t.Run("a long answer under the limit is read whole", func(t *testing.T) {
		t.Parallel()
		opts := options(t)
		opts.MaxOutput = 64 << 10
		opts.MaxText = 60000
		text := strings.Repeat("context ", 6000) // 48,000 characters
		r := newRunner(t, opts, Hook{Event: SessionStart, Command: prints(t, `{"hookSpecificOutput": {"additionalContext": "`+text+`"}}`)})
		out := run(t, r, input(SessionStart))
		if out.CouldNotEvaluate || out.AdditionalContext != text {
			t.Errorf("CouldNotEvaluate %v, %d characters of context, want %d", out.CouldNotEvaluate, len(out.AdditionalContext), len(text))
		}
	})
}

// A hook is not obliged to read what it is told, however much it is.
func TestHookNeedNotReadItsInput(t *testing.T) {
	t.Parallel()
	in := input(UserPromptSubmit)
	in.Prompt = strings.Repeat("a long paste ", 200000) // 2.6 MB
	r := newRunner(t, options(t),
		Hook{Event: UserPromptSubmit, Command: "exit 0"},
		Hook{Event: UserPromptSubmit, Command: `n=$(wc -c | tr -d ' '); [ "$n" -gt 2600000 ] || { echo "the hook read only $n bytes" >&2; exit 2; }`},
		Hook{Event: UserPromptSubmit, Command: "exec 0<&-; sleep 0.05; echo 'closed its input and went on'"},
	)
	out := run(t, r, in)
	if out.Blocked || out.CouldNotEvaluate || out.AdditionalContext != "closed its input and went on" {
		t.Errorf("Blocked %v, CouldNotEvaluate %v, Reason %q, context %q", out.Blocked, out.CouldNotEvaluate, out.Reason, out.AdditionalContext)
	}
}

// An event too large for a program's environment cannot be handed to a
// hook, and a hook that was not told has not checked.
func TestEventTooLargeForTheEnvironment(t *testing.T) {
	t.Parallel()
	in := input(PreToolUse)
	in.ToolInput = []byte(`{"command": "cat > notes.txt <<'EOF'\n` + strings.Repeat("a line of the file\\n", 200000) + `EOF"}`)
	r := newRunner(t, options(t), Hook{Event: PreToolUse, Command: "exit 0"})
	out := run(t, r, in)
	if !out.Blocked || !out.CouldNotEvaluate {
		t.Fatalf("Blocked %v, CouldNotEvaluate %v", out.Blocked, out.CouldNotEvaluate)
	}
	contains(t, "the reason", out.Reason, "it could not be started", "too long to hand to a program in its environment")
	if len(out.Reason) > 2000 {
		t.Errorf("the reason is %d bytes long", len(out.Reason))
	}
}

// A hook runs in a process group of its own, which is what lets it be
// stopped whole, and has no terminal to read from or draw on.
func TestHookHasItsOwnGroupAndNoTerminal(t *testing.T) {
	t.Parallel()
	command := `pgid=$(ps -o pgid= -p $$ | tr -d ' ')
[ "$pgid" = "$$" ] || { echo "the hook's process group is $pgid, not its own id $$" >&2; exit 2; }
if ( : < /dev/tty ) 2> /dev/null; then echo 'the hook has a terminal' >&2; exit 2; fi
[ -t 0 ] || [ -t 1 ] || [ -t 2 ] && { echo 'the hook is connected to a terminal' >&2; exit 2; }
exit 0`
	out := one(t, PreToolUse, command)
	if out.Blocked || out.CouldNotEvaluate {
		t.Errorf("%s", out.Reason)
	}
}

// Hooks get the time their event allows when they name none, and what
// they name when they do.
func TestAHooksOwnTimeoutIsUsed(t *testing.T) {
	t.Parallel()
	// With the default of 30 seconds this would take that long; with its
	// own 200 ms it is stopped at once.
	r := newRunner(t, options(t), Hook{Event: UserPromptSubmit, Command: "sleep 600", Timeout: 200 * time.Millisecond})
	began := time.Now()
	out := run(t, r, input(UserPromptSubmit))
	if !out.Blocked || !out.CouldNotEvaluate || time.Since(began) > 10*time.Second {
		t.Errorf("Blocked %v, CouldNotEvaluate %v after %v", out.Blocked, out.CouldNotEvaluate, time.Since(began))
	}
	contains(t, "the reason", out.Reason, "did not finish within 200ms")
}
