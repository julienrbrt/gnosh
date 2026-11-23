//go:build linux || darwin

package main

import (
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
)

// A file descriptor gnosh was started with, past the standard three,
// does not reach the commands it runs: marked close-on-exec at startup
// (the third sandbox review).
func TestInheritedDescriptorsStayHere(t *testing.T) {
	// (Opened as one left open by whatever started gnosh would be: not
	// close-on-exec, as Go's own os.OpenFile makes its files.)
	fd, err := syscall.Open(filepath.Join(t.TempDir(), "left-open"), syscall.O_CREAT|syscall.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer syscall.Close(fd)
	reach := func() string {
		t.Helper()
		out, err := exec.Command("/bin/sh", "-c", "test -e /dev/fd/"+strconv.Itoa(fd)+" && echo open || echo closed").Output()
		if err != nil {
			t.Fatal(err)
		}
		return strings.TrimSpace(string(out))
	}
	if got := reach(); got != "open" {
		t.Fatalf("before: a command is given the descriptor %d: %q", fd, got)
	}
	inheritedCloseOnExec()
	if got := reach(); got != "closed" {
		t.Errorf("after: a command still has the descriptor %d: %q", fd, got)
	}
}
