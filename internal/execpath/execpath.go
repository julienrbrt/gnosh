// Package execpath finds the program a command names as it will be
// started: on the PATH of the environment the program is given, which a
// settings env block or a server's own env may have changed, and not on
// gnosh's own (Go's exec looks only there). Claude Code, on Node, looks a
// program up on the PATH of the environment it starts it with.
package execpath

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Find returns the file that a program's name names, when it is started
// with the environment env in the folder dir. A name holding a slash is a
// path, from dir when it is relative. A bare name is looked for in the
// folders of env's PATH: the last PATH= in env, which is the one a process
// is given, or gnosh's own when env is nil, as the process then has
// gnosh's environment. Only absolute folders are looked in: an empty or
// relative entry would be dir itself, which a repository fills, and Go's
// own lookup passes over them too. ok is false when no such program is
// there.
func Find(name string, env []string, dir string) (path string, ok bool) {
	if strings.Contains(name, "/") {
		if !filepath.IsAbs(name) {
			name = filepath.Join(dir, name)
		}
		return name, true
	}
	if name == "" {
		return "", false
	}
	if env == nil {
		env = os.Environ()
	}
	list := ""
	for _, kv := range env {
		if v, ok := strings.CutPrefix(kv, "PATH="); ok {
			list = v
		}
	}
	for _, d := range filepath.SplitList(list) {
		if !filepath.IsAbs(d) {
			continue
		}
		p := filepath.Join(d, name)
		if info, err := os.Stat(p); err == nil && info.Mode().IsRegular() && info.Mode()&0o111 != 0 {
			return p, true
		}
	}
	return "", false
}

// Command is exec.Command for the program found by Find, with the name
// as given kept as the program's own first argument, as a shell keeps it.
// ok is false, and the command nil, when no such program is there.
func Command(name string, args []string, env []string, dir string) (cmd *exec.Cmd, ok bool) {
	path, ok := Find(name, env, dir)
	if !ok {
		return nil, false
	}
	cmd = exec.Command(path, args...)
	cmd.Args[0] = name
	cmd.Env, cmd.Dir = env, dir
	return cmd, true
}
