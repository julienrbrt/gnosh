//go:build !linux

package sandbox

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"syscall"
)

// runChild runs the program at path and returns its status as a shell
// gives it. (The relay runs only in Linux's sandbox.)
func runChild(path string, argv []string) int {
	cmd := &exec.Cmd{Path: path, Args: argv, Stdin: os.Stdin, Stdout: os.Stdout, Stderr: os.Stderr}
	err := cmd.Run()
	var ee *exec.ExitError
	switch {
	case err == nil:
		return 0
	case errors.As(err, &ee):
		if st, ok := ee.Sys().(syscall.WaitStatus); ok && st.Signaled() {
			return 128 + int(st.Signal())
		}
		return ee.ExitCode()
	}
	fmt.Fprintf(os.Stderr, "gnosh: %v\n", err)
	return 127
}
