package shelltool

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gnosh-ai/gnosh/internal/core"
	"github.com/gnosh-ai/gnosh/internal/sandbox"
)

// With a sandbox, a command on this machine runs in it: it writes in the
// working directory and its own /tmp, nowhere else, and its cd still
// carries over; a write elsewhere fails with a note that says why and the
// way out; dangerouslyDisableSandbox, where the sandbox lets it be left,
// runs it outside; a command excludedCommands names runs outside too; the
// model is told all of it. Skipped where bubblewrap cannot run.
func TestCommandsRunInTheSandbox(t *testing.T) {
	if st := sandbox.Check(); !st.OK {
		t.Skip(st.Why)
	}
	env := newEnv(t)
	// (Not under /tmp, which inside the sandbox is the sandbox's own.)
	outside, err := os.MkdirTemp("/var/tmp", "gnosh-sandbox-test-")
	if err != nil {
		t.Skip("no /var/tmp to make a folder outside the sandbox in")
	}
	t.Cleanup(func() { os.RemoveAll(outside) })
	temp, err := os.MkdirTemp(t.TempDir(), "sandbox-")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(env.Cwd, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	var wrapped int
	sb := &Sandbox{
		Wrap: func(dir string, roots, argv []string, expose []sandbox.Exposed) ([]string, []string, error) {
			wrapped++
			p := sandbox.Policy{Write: roots, Temp: temp, Expose: expose}
			out, err := sandbox.Wrap(p, dir, argv)
			return out, sandbox.Env(p), err
		},
		Outside:     func(command string, asked bool) bool { return asked || command == "touch "+outside+"/excluded" },
		Unsandboxed: true,
		Writes:      "the project's folder",
	}
	m := newManager(t, Options{Sandbox: sb})

	res := run(t, t.Context(), m, BashName, env, args{argCommand: "echo in > in.txt && cd sub && echo $TMPDIR && echo t > /tmp/x && cat /tmp/x"})
	if res.IsError || !strings.Contains(printed(res), "/tmp\nt") {
		t.Fatalf("a sandboxed command: %q", core.TextOf(res.Content))
	}
	if data, _ := os.ReadFile(filepath.Join(env.Cwd, "in.txt")); string(data) != "in\n" {
		t.Errorf("the write in the working directory: %q", data)
	}
	if res.Cwd != filepath.Join(env.Cwd, "sub") {
		t.Errorf("the cd did not carry over out of the sandbox: %q", res.Cwd)
	}

	res = run(t, t.Context(), m, BashName, env, args{argCommand: "echo out > " + outside + "/out.txt"})
	text := core.TextOf(res.Content)
	if !res.IsError || !strings.Contains(text, "Read-only file system") || !strings.Contains(text, "it ran in the sandbox") || !strings.Contains(text, argNoSandbox+" set to true") {
		t.Errorf("a write elsewhere: %s", text)
	}
	if _, err := os.Stat(filepath.Join(outside, "out.txt")); err == nil {
		t.Error("a sandboxed command wrote outside its folders")
	}

	n := wrapped
	res = run(t, t.Context(), m, BashName, env, args{argCommand: "echo out > " + outside + "/out.txt", argNoSandbox: true})
	if res.IsError || wrapped != n {
		t.Errorf("asked out of the sandbox: wrapped %d times, %s", wrapped-n, core.TextOf(res.Content))
	}
	run(t, t.Context(), m, BashName, env, args{argCommand: "touch " + outside + "/excluded"})
	if _, err := os.Stat(filepath.Join(outside, "excluded")); err != nil || wrapped != n {
		t.Errorf("an excluded command ran in the sandbox: %v", err)
	}

	spec := toolNamed(t, m, BashName).Spec()
	if !strings.Contains(spec.Description, "In the sandbox:") || !strings.Contains(spec.Description, "the project's folder") || !strings.Contains(string(spec.InputSchema), argNoSandbox) {
		t.Errorf("the model is not told of the sandbox:\n%s\n%s", spec.Description, spec.InputSchema)
	}

	// Where it cannot be left, it is not offered, and asking changes
	// nothing.
	sb.Unsandboxed = false
	m = newManager(t, Options{Sandbox: sb})
	spec = toolNamed(t, m, BashName).Spec()
	if strings.Contains(string(spec.InputSchema), argNoSandbox) || !strings.Contains(spec.Description, "cannot be left") {
		t.Errorf("a sandbox that cannot be left:\n%s\n%s", spec.Description, spec.InputSchema)
	}
	res = run(t, t.Context(), m, BashName, env, args{argCommand: "echo out > " + outside + "/strict.txt", argNoSandbox: true})
	if _, err := os.Stat(filepath.Join(outside, "strict.txt")); err == nil || !res.IsError || !strings.Contains(core.TextOf(res.Content), "tell the person") {
		t.Errorf("the strict sandbox was left: %s", core.TextOf(res.Content))
	}
}
