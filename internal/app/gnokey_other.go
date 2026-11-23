//go:build !unix

package app

import (
	"os"
	"os/exec"
)

// noTerminal does nothing where there is no session to start.
func noTerminal(cmd *exec.Cmd) {}

// sameFile reports whether now is was, unchanged since.
func sameFile(was, now os.FileInfo) bool {
	return os.SameFile(was, now) && was.Size() == now.Size() && was.ModTime().Equal(now.ModTime())
}
