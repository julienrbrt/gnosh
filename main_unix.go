//go:build linux || darwin

package main

import (
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"syscall"
)

// inheritedCloseOnExec marks every file descriptor gnosh was started
// with, past the standard three, to be closed on exec: one that whatever
// started gnosh left open (a connected Unix socket, an agent's, say) would
// otherwise reach every command gnosh runs, a sandboxed one too, which
// could use it without making a socket (the third sandbox review). What Go
// opens is marked so already.
func inheritedCloseOnExec() {
	dir := "/proc/self/fd"
	if runtime.GOOS == "darwin" {
		dir = "/dev/fd"
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if fd, err := strconv.Atoi(e.Name()); err == nil && fd > 2 {
			syscall.CloseOnExec(fd)
		}
	}
}

// detach starts gnosh with args as a process of its own, in a session of
// its own, so that it goes on when this terminal and this process are
// gone, in the environment env, with nothing to read and what it prints in
// the file at log. It
// returns the new process's id, and does not wait for it.
func detach(self string, args, env []string, log string) (int, error) {
	out, err := os.OpenFile(log, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return 0, err
	}
	defer out.Close()
	cmd := exec.Command(self, args...)
	cmd.Env = env
	cmd.Stdout, cmd.Stderr = out, out
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return 0, err
	}
	pid := cmd.Process.Pid
	// Reaped when it ends while this process still runs (a /fork run
	// beside the session), so that it leaves no zombie; it goes on, not
	// waited for, after this process ends.
	go func() { _ = cmd.Wait() }()
	return pid, nil
}
