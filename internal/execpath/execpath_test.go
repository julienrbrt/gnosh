package execpath

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func program(t *testing.T, dir, name string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte("#!/bin/sh\necho \"$0\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

// A bare name is looked up on the PATH the program is given, the last one
// in its environment, and in absolute folders only.
func TestFindUsesTheGivenPath(t *testing.T) {
	work, bin, other := t.TempDir(), t.TempDir(), t.TempDir()
	program(t, work, "planted")
	want := program(t, bin, "server")
	program(t, other, "server")
	if p, ok := Find("server", []string{"PATH=" + other, "HOME=/x", "PATH=:.:" + bin}, work); !ok || p != want {
		t.Errorf("Find on the last PATH = %q, %v; want %q", p, ok, want)
	}
	if p, ok := Find("planted", []string{"PATH=:.:planted:" + bin}, work); ok {
		t.Errorf("a relative PATH entry found the folder's own program: %s", p)
	}
	if _, ok := Find("server", []string{"HOME=/x"}, work); ok {
		t.Error("found with no PATH at all")
	}
	if _, ok := Find("", []string{"PATH=" + bin}, work); ok {
		t.Error("an empty name was found")
	}
	// A name with a slash is a path, from the folder when relative.
	if p, ok := Find("sub/run", nil, work); !ok || p != filepath.Join(work, "sub/run") {
		t.Errorf("relative path = %q, %v", p, ok)
	}
	if p, ok := Find("/bin/sh", []string{"PATH="}, work); !ok || p != "/bin/sh" {
		t.Errorf("absolute path = %q, %v", p, ok)
	}
}

// The command found keeps the name as given as its first argument, and
// runs with the environment and folder it was found for.
func TestCommandKeepsTheName(t *testing.T) {
	bin, work := t.TempDir(), t.TempDir()
	program(t, bin, "echoname")
	env := []string{"PATH=" + bin + ":/usr/bin:/bin"}
	cmd, ok := Command("echoname", nil, env, work)
	if !ok {
		t.Fatal("not found")
	}
	if cmd.Path != filepath.Join(bin, "echoname") || cmd.Args[0] != "echoname" || cmd.Dir != work || len(cmd.Env) != 1 {
		t.Fatalf("cmd = %+v", cmd)
	}
	// It runs: the script's $0 is its path, which the kernel passes on.
	if out, err := cmd.Output(); err != nil || strings.TrimSpace(string(out)) != filepath.Join(bin, "echoname") {
		t.Fatalf("output %q, %v", out, err)
	}
	if _, ok := Command("no-such-program-here", nil, env, work); ok {
		t.Error("a missing program was found")
	}
}
