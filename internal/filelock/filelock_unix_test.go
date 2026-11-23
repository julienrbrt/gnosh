//go:build darwin || dragonfly || freebsd || illumos || linux || netbsd || openbsd

package filelock

import (
	"bufio"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// probe asks, as a process that only wants to know would, whether the lock
// at path is held: it opens the file read-only and asks for a shared lock
// without waiting. It takes nothing from a holder.
func probe(t *testing.T, path string) (held bool) {
	t.Helper()
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_CLOEXEC, 0)
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	defer syscall.Close(fd)
	err = syscall.Flock(fd, syscall.LOCK_SH|syscall.LOCK_NB)
	switch err {
	case nil:
		_ = syscall.Flock(fd, syscall.LOCK_UN)
		return false
	case syscall.EWOULDBLOCK:
		return true
	}
	t.Fatalf("probe: %v", err)
	return false
}

func TestASharedProbeSeesTheLock(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "a.lock")
	release := mustTry(t, path)
	first, second := probe(t, path), probe(t, path)
	if !first || !second {
		t.Fatal("a shared probe did not see the lock, or took it from its holder")
	}
	mustBeHeld(t, path)
	release()
	if probe(t, path) {
		t.Fatal("a shared probe saw a lock that was let go")
	}
}

// While a prober has its shared lock, the exclusive lock cannot be had;
// TryFile says so as contention, and File waits the moment out.
func TestAProbeInProgressIsContention(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "a.lock")
	mustTry(t, path)()
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer syscall.Close(fd)
	if err := syscall.Flock(fd, syscall.LOCK_SH|syscall.LOCK_NB); err != nil {
		t.Fatal(err)
	}
	mustBeHeld(t, path)

	done := make(chan error, 1)
	go func() {
		release, err := File(context.Background(), path)
		if err == nil {
			release()
		}
		done <- err
	}()
	select {
	case err := <-done:
		t.Fatalf("File did not wait for the probe to end: %v", err)
	case <-time.After(30 * time.Millisecond):
	}
	if err := syscall.Flock(fd, syscall.LOCK_UN); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(patience):
		t.Fatal("File was not granted the lock when the probe ended")
	}
}

func TestTheLockFileIsPrivateAndNotInherited(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "a.lock")
	h, err := openLockFile(path)
	if err != nil {
		t.Fatal(err)
	}
	defer closeLockFile(h)
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := st.Mode().Perm(); perm&0o077 != 0 {
		t.Fatalf("the lock file's mode is %o: others than its owner can use it", perm)
	}
	flags, err := unix.FcntlInt(uintptr(h), unix.F_GETFD, 0)
	if err != nil {
		t.Fatal(err)
	}
	if flags&unix.FD_CLOEXEC == 0 {
		t.Fatal("the lock file would be inherited by a program this process starts")
	}
}

// flockTool finds flock(1), a program that takes flock(2) locks, so that the
// lock can be checked against another process and another implementation.
func flockTool(t *testing.T) string {
	t.Helper()
	tool, err := exec.LookPath("flock")
	if err != nil {
		t.Skip("flock(1) is not installed; the lock is checked between processes only where it is")
	}
	return tool
}

func TestAnotherProcessSeesTheLock(t *testing.T) {
	t.Parallel()
	tool := flockTool(t)
	path := filepath.Join(t.TempDir(), "a.lock")
	try := func() error {
		ctx, cancel := context.WithTimeout(context.Background(), patience)
		defer cancel()
		return exec.CommandContext(ctx, tool, "--exclusive", "--nonblock", path, "true").Run()
	}

	release := mustTry(t, path)
	var exit *exec.ExitError
	if err := try(); !errors.As(err, &exit) || exit.ExitCode() != 1 {
		t.Fatalf("another process got past a held lock: %v", err)
	}
	release()
	if err := try(); err != nil {
		t.Fatalf("another process could not take a lock that was let go: %v", err)
	}
}

// A lock held by another process is contention here, is waited for in the
// kernel, and is granted when that process dies, without anyone letting go.
func TestAnotherProcessHoldsTheLock(t *testing.T) {
	t.Parallel()
	tool := flockTool(t)
	path := filepath.Join(t.TempDir(), "a.lock")

	// flock(1) takes the lock, then runs a shell that says so and waits
	// for its input to end. --close keeps the lock out of the shell's
	// hands, so it is flock(1)'s own process that holds it.
	other := exec.Command(tool, "--exclusive", "--close", path, "sh", "-c", "echo held; cat >/dev/null")
	stdin, err := other.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := other.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := other.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = other.Process.Kill()
		_ = stdin.Close()
		_ = other.Wait()
	}()
	said := make(chan string, 1)
	go func() {
		line, _ := bufio.NewReader(stdout).ReadString('\n')
		said <- line
	}()
	select {
	case line := <-said:
		if line != "held\n" {
			t.Fatalf("the other process said %q, not that it holds the lock", line)
		}
	case <-time.After(patience):
		t.Fatal("the other process never took the lock")
	}

	mustBeHeld(t, path)
	done := make(chan error, 1)
	go func() {
		release, err := File(context.Background(), path)
		if err == nil {
			release()
		}
		done <- err
	}()
	select {
	case err := <-done:
		t.Fatalf("File did not wait for the other process: %v", err)
	case <-time.After(30 * time.Millisecond):
	}

	if err := other.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(patience):
		t.Fatal("the lock of a process that died was not granted to the waiter")
	}
}
