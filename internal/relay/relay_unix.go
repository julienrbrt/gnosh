//go:build linux || darwin

package relay

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"

	"github.com/gnosh-ai/gnosh/internal/clipboard"
)

// relay runs ssh with gnosh's input and error as they are and a pipe for
// its output, which is written on to the terminal as it comes, the far
// side's copies taken from it on the way.
func relay(path string, o Options) (int, syscall.Signal) {
	r, w, err := os.Pipe()
	if err != nil {
		fmt.Fprintf(o.Err, "gnosh ssh: the clipboard relay could not start (%v), so ssh runs without it.\n", err)
		return plain(path, o)
	}
	defer r.Close()
	cmd := command(path, o)
	cmd.Stdout = w
	// What the far machine copies goes on this one's clipboard as text
	// that is safe to paste: it went as it came, escapes and all (the
	// relay review of 2026-11-19).
	cp := newCopier(func(ctx context.Context, text string) error { return o.Copy(ctx, clipboard.Plain(text)) })
	shown := make(chan struct{})
	go func() {
		defer close(shown)
		pass(r, o.Out, cp.offer)
	}()
	code, sig := supervise(cmd, o.Err, func() { w.Close() })
	select {
	case <-shown:
	case <-time.After(drainWait):
	}
	switch all, failed, err := cp.finish(copyWait); {
	case !all:
		fmt.Fprintf(o.Err, "gnosh ssh: the clipboard took more than %s; the far side's last copy may not have reached it.\n", copyWait)
	case failed > 0:
		fmt.Fprintf(o.Err, "gnosh ssh: %d of the far side's copies did not reach the clipboard (%v).\n", failed, err)
	}
	return code, sig
}

// plain runs ssh with gnosh's own input and output, nothing in between.
// Where it may, gnosh hands its process over to ssh, which is then just
// what it is by itself.
func plain(path string, o Options) (int, syscall.Signal) {
	if o.Exec {
		err := syscall.Exec(path, append([]string{o.SSH}, o.Args...), os.Environ())
		fmt.Fprintf(o.Err, "gnosh ssh: %s: %v\n", path, err)
		return 126, 0
	}
	return supervise(command(path, o), o.Err, func() {})
}

// supervise starts cmd and waits for it to end. The signals that end a
// program, sent to gnosh, are passed on to it: those a terminal sends
// reach it by themselves, and one more of the same changes nothing. One
// that was ignored when gnosh started (nohup's SIGHUP) stays ignored, by
// ssh too. started is called once cmd has started, or failed to.
func supervise(cmd *exec.Cmd, errOut io.Writer, started func()) (int, syscall.Signal) {
	sigs := make(chan os.Signal, 8)
	for _, s := range []os.Signal{syscall.SIGINT, syscall.SIGQUIT, syscall.SIGTERM, syscall.SIGHUP} {
		if !signal.Ignored(s) {
			signal.Notify(sigs, s)
		}
	}
	defer signal.Stop(sigs)
	err := cmd.Start()
	started()
	if err != nil {
		fmt.Fprintf(errOut, "gnosh ssh: %v\n", err)
		return 126, 0
	}
	pid := cmd.Process.Pid
	ended := make(chan syscall.WaitStatus, 1)
	go func() { ended <- waitFor(pid) }()
	for {
		select {
		case s := <-sigs:
			_ = syscall.Kill(pid, s.(syscall.Signal))
		case ws := <-ended:
			_ = cmd.Process.Release()
			if ws.Signaled() {
				return 128 + int(ws.Signal()), ws.Signal()
			}
			return ws.ExitStatus(), 0
		}
	}
}

// stopSettle is how long a stop of ssh's is let settle before gnosh acts
// on it: when the shell stopped the whole job and has gone on with it,
// ssh is going on again by then.
const stopSettle = 50 * time.Millisecond

// waitFor waits for the process pid to end and says how it ended. When the
// process stops by itself (ssh's ~^Z), gnosh stops the same way, so that
// the shell sees its job stopped, and the shell's fg goes on with both.
func waitFor(pid int) syscall.WaitStatus {
	for {
		var ws syscall.WaitStatus
		_, err := syscall.Wait4(pid, &ws, syscall.WUNTRACED, nil)
		switch {
		case err == syscall.EINTR:
			continue
		case err != nil:
			// (It cannot be waited for, so how it ended is not known.)
			return syscall.WaitStatus(255 << 8)
		case !ws.Stopped():
			return ws
		}
		time.Sleep(stopSettle)
		if !stopped(pid) {
			continue
		}
		_ = syscall.Kill(syscall.Getpid(), ws.StopSignal())
		time.Sleep(stopSettle)
		if stopped(pid) {
			_ = syscall.Kill(pid, syscall.SIGCONT)
		}
	}
}

// dieOf ends gnosh by sig, as ssh was ended, where sig is one that ends a
// program and that gnosh does not keep for itself; otherwise it returns,
// and gnosh ends with 128 and its number.
func dieOf(sig syscall.Signal) {
	switch sig {
	case syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP, syscall.SIGKILL:
	default:
		return
	}
	signal.Reset(sig)
	_ = syscall.Kill(syscall.Getpid(), sig)
	// (It reaches gnosh on another thread, as a signal may.)
	time.Sleep(time.Second)
}
