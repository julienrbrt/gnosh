package sandbox

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Status is whether this machine can sandbox commands.
type Status struct {
	// OK says it can; Why says why not, in a sentence, when it cannot.
	OK  bool
	Why string
	// Tool is the program that makes the sandbox: bwrap's path, or
	// sandbox-exec's.
	Tool string
	// UsernsRefused says the sandbox refuses its commands user namespaces
	// of their own (bubblewrap's --disable-userns, from 0.8), and Filters
	// that the filter for Unix sockets can be put on them here. Where the
	// first cannot, Weaker says so, and where the second cannot, NoFilter,
	// for the person.
	UsernsRefused, Filters bool
	Weaker, NoFilter       string
}

// Notes is what the sandbox cannot do here, in a sentence: Weaker, unless
// the settings let user namespaces anyway (userns), and NoFilter.
func (st Status) Notes(userns bool) string {
	var out []string
	if st.Weaker != "" && !userns {
		out = append(out, st.Weaker)
	}
	if st.NoFilter != "" {
		out = append(out, st.NoFilter)
	}
	return strings.Join(out, ", and ")
}

var (
	checkMu sync.Mutex
	checked = map[Setup]Status{}
)

// Check reports whether commands can be sandboxed here as the settings
// set nothing of how: CheckWith for no setup.
func Check() Status { return CheckWith(Setup{}) }

// CheckWith reports whether commands can be sandboxed here, trying the
// sandbox once for the setup in the process's life: a bubblewrap that the
// system will not let make its namespaces (Ubuntu's AppArmor rule for
// unprivileged ones, a container) is found by its first try, not by the
// model's first command.
func CheckWith(s Setup) Status {
	checkMu.Lock()
	defer checkMu.Unlock()
	if st, ok := checked[s]; ok {
		return st
	}
	st := check(s)
	checked[s] = st
	return st
}

// try runs a sandbox's trial command, and says what went wrong, if
// anything, in its own words.
func try(argv []string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Run(); err != nil {
		said := strings.TrimSpace(out.String())
		if i := strings.IndexByte(said, '\n'); i >= 0 {
			said = said[:i]
		}
		if said == "" {
			said = err.Error()
		}
		return said
	}
	return ""
}

// isProgram reports whether the path is a regular file someone may run.
func isProgram(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.Mode().IsRegular() && info.Mode()&0o111 != 0
}

// lookFor finds a program in the system's own folders, never on the PATH:
// a folder there a sandboxed command can write (a virtualenv's, a
// project's node_modules/.bin) could hold a program of that name that
// makes no sandbox at all (the sandbox review).
func lookFor(name string) string {
	for _, dir := range []string{"/usr/bin", "/bin"} {
		if p := filepath.Join(dir, name); isProgram(p) {
			return p
		}
	}
	return ""
}
