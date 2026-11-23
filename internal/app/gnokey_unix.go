//go:build unix

package app

import (
	"os"
	"os/exec"
	"syscall"
)

// noTerminal starts cmd in a session of its own, without gnosh's terminal.
func noTerminal(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}

// sameFile reports whether now is the very file was was: the same file on
// the same device, unchanged since.
func sameFile(was, now os.FileInfo) bool {
	a, ok1 := was.Sys().(*syscall.Stat_t)
	b, ok2 := now.Sys().(*syscall.Stat_t)
	if !ok1 || !ok2 {
		return false
	}
	return a.Dev == b.Dev && a.Ino == b.Ino && was.Size() == now.Size() && was.ModTime().Equal(now.ModTime()) && was.Mode() == now.Mode()
}
