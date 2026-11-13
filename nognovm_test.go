package main

import (
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// gnoVMPackages are the Gno interpreter and its standard library, which a
// gnosh built with -tags nognovm must not link: they run only in
// gnosh-gnovm-worker (gnovm/cmd/gnosh-gnovm-worker), a program of its own.
// gno's tm2 packages (Apache-2.0), which the x402 client uses, are allowed.
const gnoVMPackages = "github.com/gnolang/gno/gnovm/"

// TestFSLBinaryDoesNotLinkGnoVM is the licence split's acceptance check:
// gnosh built with -tags nognovm imports no package of the Gno interpreter,
// and embeds none of its standard library, on each system where its VMs
// run in the worker. Windows still runs them in-process (DESIGN.md §14).
// The default build, which still contains the GnoVM, is checked too, so
// the test cannot pass by listing nothing.
func TestFSLBinaryDoesNotLinkGnoVM(t *testing.T) {
	t.Parallel()
	gobin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("no go command")
	}
	deps := func(goos string, tags ...string) map[string][]string {
		t.Helper()
		args := append([]string{"list"}, tags...)
		args = append(args, "-deps", "-f", `{{.ImportPath}} {{join .EmbedFiles ","}}`, ".")
		cmd := exec.CommandContext(t.Context(), gobin, args...)
		cmd.Env = append(os.Environ(), "GOOS="+goos, "CGO_ENABLED=0")
		out, err := cmd.Output()
		require.NoError(t, err, "go %s", strings.Join(args, " "))
		pkgs := map[string][]string{}
		for line := range strings.SplitSeq(strings.TrimSpace(string(out)), "\n") {
			path, embeds, _ := strings.Cut(line, " ")
			pkgs[path] = strings.Split(embeds, ",")
		}
		return pkgs
	}
	const gnovm = "github.com/charmbracelet/crush/internal/gnovm"
	for _, goos := range []string{"linux", "darwin"} {
		t.Run(goos, func(t *testing.T) {
			t.Parallel()
			split := deps(goos, "-tags", "nognovm")
			require.Contains(t, split, gnovm, "the client is still linked")
			for path := range split {
				require.False(t, strings.HasPrefix(path, gnoVMPackages), "gnosh -tags nognovm links %s", path)
			}
			for _, f := range split[gnovm] {
				require.False(t, strings.HasPrefix(f, "stdlibs/") || strings.HasPrefix(f, "klashlibs/"),
					"gnosh -tags nognovm embeds %s", f)
			}

			whole := deps(goos)
			linked := false
			for path := range whole {
				linked = linked || strings.HasPrefix(path, gnoVMPackages)
			}
			require.True(t, linked, "the default build contains the GnoVM")
		})
	}
}
