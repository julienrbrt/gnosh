//go:build !unix

package lsp

import "os/exec"

// ownGroup does nothing where there are no process groups.
func ownGroup(*exec.Cmd) {}

// stop kills the server at the last step.
func stop(cmd *exec.Cmd, step int, _ bool) {
	if step >= 2 {
		_ = cmd.Process.Kill()
	}
}
